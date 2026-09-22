package index

// 全量构建（合同 §4.4 确定性口径 + §5.1 水位线口径）。
//
// 输入是**中性快照** Snapshot：调用方（CLI 层）用 M2 的只读扫描底座读完权威 Markdown、
// 算好每文件 content_hash（与 M1 store 的 B3 同源同算法）之后喂进来。
// 本包因此既不解析 Markdown、也不碰权威文件读写口（§13 禁令第一条）。
//
// SQLite 输出是 `.index/eg.db`：全部表 + 水位线**同一个事务**提交。Storage v3 的
// per-Note candidate 投影另写 `.index/blocks/`；它不进入数据库 schema，并独立做
// 权威对账与降级。
//
// 确定性（`TestBuildTwiceByteIdentical` / `TestBuildFullDeterministic`）：
//   - 一切插入按确定序：cards 按 id 升序、relations 按 (src_id, verb, dst_id) 升序、
//     files / skipped 按 path 升序，rowid 从 1 显式递增；
//   - `built_at_unix` / `files.indexed_at_unix` 这类**天然非确定**的列不参与等价比较
//     （等价比较口径 = Digest：除这两列外的全部行按确定序导出后逐字相等）。

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	// 纯 Go SQLite 驱动（合同 A-41：modernc.org/sqlite v1.45.0 钉死；cgo 驱动永久禁入，
	// 否则「CGO_ENABLED=0 静态单二进制」这条最高约束当场破功）。
	_ "modernc.org/sqlite"
)

// driverName 是 modernc.org/sqlite 注册的驱动名。
const driverName = "sqlite"

// ErrIndexExists 表示 `.index/eg.db` 已存在：Build 不覆盖既有库
// （覆盖语义归 Rebuild —— 先删再全量建，语义显式）。
var ErrIndexExists = errors.New("索引库已存在")

// Card 是快照里的一张知识卡或观点（**中性 DTO**：字段都是标量，不引本仓任何结构体）。
type Card struct {
	ID          string
	Path        string // vault 内相对路径（/ 分隔）
	Domain      string
	Title       string
	Status      string
	Deprecated  bool
	Deleted     bool
	ReplacedBy  string // replaced_by.target 的逐字原值；未设置即空串
	Body        string // frontmatter 之后的正文全文（喂 FTS）
	ContentHash string // 与 M1 store 的 B3 同源同算法（由调用方算）
	MTimeUnix   int64
	// Kind 是产物分型（Schema v2 §7）：必为 CardKinds() 之一，**没有默认值**。
	//
	// 调用方必须显式给值：空串一律被写入口拒绝，绝不按 knowledge 兜底 —— 「忘记设分型」
	// 与「这确实是一张知识卡」是两件事，用默认值把前者伪装成后者，漂移就只能等到
	// 读路径把观点当知识卡返回时才暴露。
	Kind string
	// Validation 是观点的论证进度（Schema v2 §6.1）：
	// Kind == CardKindOpinion 时必为 CardValidations() 之一；knowledge 恒为空串。
	//
	// 与 Status 正交：rejected 的观点仍可以是 active，索引层不做任何互相推导。
	Validation string
}

// Relation 是一条正向关系边（`relations` 只存正向边，合同 §4.1）。
type Relation struct {
	SrcID   string
	Verb    string
	DstID   string
	SrcPath string
	// Line 是关系在源文件里的行号：M5 全量构建不采集（恒 0），
	// 它只是定位提示，检索与反查都不依赖它。需要时由读路径 task 补采。
	Line int
}

// File 是被索引文件的水位线最小单位（合同 §5.1：`mtime`/`size` 仅作快路径过滤，
// **绝不**单独作为「未变更」的最终结论 —— 最终结论恒以 content_hash 为准）。
type File struct {
	Path        string
	ContentHash string
	Size        int64
	MTimeUnix   int64
}

// SkippedFile 是一条「本次未写入 / 未收录」的如实交代；Kind 取值必须 ∈ SkippedKinds()。
type SkippedFile struct {
	Path string
	Kind string
}

// Snapshot 是一次全量构建的完整输入。
type Snapshot struct {
	// Head 是构建时的 Git HEAD（全长 40 位）；非 git 仓写空串（合同 §4.2）。
	Head      string
	Cards     []Card
	Relations []Relation
	Files     []File
	Skipped   []SkippedFile
	// Blocks is the complete per-Note candidate projection. It is persisted
	// under .index/blocks and never enters the SQLite schema.
	Blocks []BlockDocument
}

// Options 是构建的可注入项：Now 让 `built_at_unix` 在测试里完全确定。
type Options struct {
	Now func() time.Time
}

func (o Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

// Meta 是 `index_meta` 的强类型视图（恰 6 键，合同 §4.2）。
type Meta struct {
	SchemaVersion int
	Head          string
	FilesHash     string
	TokenizerMode string
	BuiltAtUnix   int64
	CardCount     int
}

// Result 是一次构建的回执。
type Result struct {
	DBPath        string
	Meta          Meta
	CardCount     int
	RelationCount int
	FileCount     int
	SkippedCount  int
	BlockCount    int
	BlockAction   string
	// DroppedDuplicateCards / DroppedDuplicateRelations 是**如实交代**：
	// 库里本来就可能存在重复 ID 与重复关系边（`eg check` 的 duplicate_id /
	// relation_duplicate 两个 finding 就是它们），索引层按主键只收一份，
	// 但绝不静默 —— 丢了几条必须能被调用方读到并转述给用户。
	DroppedDuplicateCards     int
	DroppedDuplicateRelations int
}

// Build 在 dir（= vault/.index）下全量构建索引。
//
// dir 不存在则创建；`eg.db` 已存在则返回 ErrIndexExists（不覆盖，且**零删除**）。
// 构建失败时**不留半成品**：删掉、且**只**删掉本次调用亲手造出来的那几个路径
// （见 cleanupBuildAttempt），让下一次 build 从同一个起点重来。
func Build(dir string, snap Snapshot, opt Options) (*Result, error) {
	// Validate every sidecar before creating either derived representation.
	// An invalid neutral snapshot is a caller error and must stay zero-write.
	if _, err := canonicalBlockMap(snap.Blocks); err != nil {
		return nil, err
	}
	dbPath := filepath.Join(dir, DBFileName)
	if _, err := os.Stat(dbPath); err == nil {
		// 早退：本次调用一个字节都没写，因此也一个条目都不许删。
		return nil, fmt.Errorf("%w：%s", ErrIndexExists, dbPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	// 这两件事必须在**任何写入之前**问清楚：目录是不是本次建的、哪些候选产物是预存的。
	// 写下去之后就再也问不出来了。
	indexDirExistedBefore := pathExists(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		// 目录都没建成（典型：`.index` 位置上是个悬空符号链接），本次零产物 → 零删除。
		return nil, err
	}
	// createdPaths = 本次调用**实际新建**的路径。候选是主库 + WAL 两件副产物；
	// 登记时就已在盘的（外部污染、上一次运行遗留的孤儿 `-wal`）是**预存条目**，
	// 不入账 ⇒ 失败时不删。未来若在 `.index/` 里落任何临时 / scratch 文件，
	// **必须**在创建前一并登记进来 —— 没进账的路径在失败清理时不会被碰。
	createdPaths := notCreatedYet(
		dbPath, dbPath+walSuffix, dbPath+shmSuffix,
	)
	res, err := buildInto(dbPath, snap, opt)
	if err != nil {
		// 半成品对下游是纯负担：它既不能查，又会让 status 报 corrupt。
		_ = cleanupBuildAttempt(dir, indexDirExistedBefore, createdPaths)
		return nil, err
	}
	blocks, err := SyncBlocks(dir, snap.Blocks)
	if err != nil {
		_ = cleanupBuildAttempt(dir, indexDirExistedBefore, createdPaths)
		return nil, fmt.Errorf("写 block sidecar 失败：%w", err)
	}
	res.BlockCount = blocks.Count
	res.BlockAction = blocks.Action
	return res, nil
}

// pathExists 报告 path 这个**条目**在不在盘（`lstat` 口径，不跟随符号链接）。
//
// 必须是 `lstat`：`.index` 位置上的一条悬空符号链接在 `stat` 眼里「不存在」，
// 于是失败清理会以为「这是我建的目录」，反手把用户的链接删掉。
func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// notCreatedYet 从候选路径里筛出**此刻还不在盘**的那些（次序即候选次序）。
//
// 语义是「本次调用将要亲手创建的东西」，因此必须在创建**之前**调用。
// 状态不可知（`lstat` 因非 NotExist 原因失败）时按「预存」处理：保守方向恒为**不删**。
func notCreatedYet(candidates ...string) []string {
	out := make([]string, 0, len(candidates))
	for _, p := range candidates {
		if _, err := os.Lstat(p); errors.Is(err, os.ErrNotExist) {
			out = append(out, p)
		}
	}
	return out
}

// cleanupBuildAttempt 在构建失败后回收**本次调用**的产物（Build 专用）。
//
// # 与 purgeNonReserved 是两条方向相反的删除策略，永不互调
//
//	cleanupBuildAttempt（本函数） 只删 createdPaths —— 调用前已存在的污染、以及
//	                             M6 运行时保留条目（`run.lock` / `txn/`）一律保留；
//	purgeNonReserved（rebuild.go） 只留保留条目 —— 其余一切按项删除。
//
// 两者的区别不是「删多少」，而是「凭什么删」：本函数凭**因果**（这是我造的），
// purge 凭**名册**（这不在保留名单上）。刻意做成两个独立函数、而不是同一个函数加一个
// bool 开关：删除面是本 task 里最危险的一格，用参数切语义会让「这次到底会删掉什么」
// 在调用点上不可读、在评审时不可判。
//
// 清理分两阶段：
//
//	① 逐条删 createdPaths（登记逆序，**非递归** os.Remove）；
//	② 仅当 indexDirExistedBefore = false（目录是本次建的）且此刻 ReadDir 恰好为空时，
//	   用非递归 os.Remove 把这个空目录也收走。
//
// 阶段②的两个条件缺一不可：目录原本就在 ⇒ 它不归本次调用处置；目录非空 ⇒ 里面剩的是
// 预存污染或运行时保留条目，删了就越界。`os.RemoveAll(dir)` 在 M6 语境下永久禁用 ——
// 它会连同互斥锁的 inode 与崩溃恢复所需的事务日志一起抹掉。
//
// createdPaths 只收**文件**路径：清理面刻意保持非递归，一次性目录请走 NewScratch
// （它自建自删，删除目标结构上不可被外部指定）。
func cleanupBuildAttempt(dir string, indexDirExistedBefore bool, createdPaths []string) error {
	var first error
	keep := func(err error) {
		if err != nil && !errors.Is(err, os.ErrNotExist) && first == nil {
			first = err
		}
	}
	// —— 阶段①：只删本次亲手造出来的产物 ——
	for i := len(createdPaths) - 1; i >= 0; i-- {
		p := createdPaths[i]
		if p == dir {
			continue // 结构性护栏：目录本身只可能走阶段②。
		}
		keep(os.Remove(p))
	}
	// —— 阶段②：目录本身，两个条件缺一不可 ——
	if indexDirExistedBefore {
		return first
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		keep(err)
		return first
	}
	if len(entries) != 0 {
		// 本次虽然建了目录，但里面还有别人的东西（预存条目 / 并发写入）：留着。
		return first
	}
	keep(os.Remove(dir))
	return first
}

// buildInto 是真正的建库流程（建表 → 探测档位 → 单事务写入全部表与水位线）。
func buildInto(dbPath string, snap Snapshot, opt Options) (*Result, error) {
	db, err := openDB(dbPath, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	for _, p := range pragmas() {
		if _, err := db.Exec(p); err != nil {
			return nil, fmt.Errorf("%s 失败：%w", p, err)
		}
	}
	for _, ddl := range baseDDL() {
		if _, err := db.Exec(ddl); err != nil {
			return nil, fmt.Errorf("建表失败：%w", err)
		}
	}
	mode, err := detectTokenizer(db)
	if err != nil {
		return nil, err
	}

	cards, dupCards := dedupCards(sortedCards(snap.Cards))
	rels, dupRels := dedupRelations(sortedRelations(snap.Relations))
	files := sortedFiles(snap.Files)
	skipped := sortedSkipped(snap.Skipped)
	for _, s := range skipped {
		if !validSkippedKind(s.Kind) {
			return nil, fmt.Errorf("skipped.kind = %q 不在封闭取值域 %v 内", s.Kind, SkippedKinds())
		}
	}

	built := opt.now().UTC().Unix()
	meta := Meta{
		SchemaVersion: IndexSchemaVersion,
		Head:          snap.Head,
		FilesHash:     FilesHash(files),
		TokenizerMode: mode,
		BuiltAtUnix:   built,
		CardCount:     len(cards),
	}

	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	if err := writeAll(tx, meta, cards, rels, stampFiles(files, built), skipped); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	// 建库收尾把 WAL 全量回灌主库并截断：`.index/` 里允许出现 `eg.db-wal` / `eg.db-shm`
	// （合同 §2.3 A-43），但 build 结束后主库自身必须已经是完整可读的，
	// 这样后续只读打开（status / Digest / Inspect）不依赖 WAL 里的残留帧。
	// 回灌失败不算建库失败：数据已经提交，最坏只是 WAL 里还留着帧。
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		_ = err
	}
	return &Result{
		DBPath: dbPath, Meta: meta,
		CardCount: len(cards), RelationCount: len(rels),
		FileCount: len(files), SkippedCount: len(skipped),
		DroppedDuplicateCards: dupCards, DroppedDuplicateRelations: dupRels,
	}, nil
}

// fileRow 是 `files` 表的一行：文件水位线 + **这一行自己的**索引时间。
//
// 为什么 `indexed_at_unix` 要按行携带而不是整表统一取「本次时间」：增量更新
// （incremental.go）必须让**未变更文件**的这一列原值不动 —— 否则「只碰受影响文件」
// 这条 Acceptance 会被一列时间戳当场推翻。全量构建则全部行取同一时刻（stampFiles）。
type fileRow struct {
	File
	IndexedAtUnix int64
}

// stampFiles 把一批文件水位线统一盖上同一个索引时间（全量构建口径）。
func stampFiles(files []File, at int64) []fileRow {
	out := make([]fileRow, 0, len(files))
	for _, f := range files {
		out = append(out, fileRow{File: f, IndexedAtUnix: at})
	}
	return out
}

// writeAll 在**同一个事务**里写完六张表（含水位线）：要么全在，要么一行都没有。
//
// 这是**唯一**的写入口：全量构建（buildInto）与增量更新（applyDelta）共用它，
// 因此「增量结果与全量重建逐字等价」不依赖两套写入代码保持同步 —— 它们本来就是一套。
//
// 分型校验（Schema v2）也因此只需要一处：任何路径想把 kind 缺失 / (kind, validation)
// 自相矛盾的行写进库，都必须先过这里。
func writeAll(tx *sql.Tx, meta Meta, cards []Card, rels []Relation,
	files []fileRow, skipped []SkippedFile) error {
	if err := validateCardKinds(cards); err != nil {
		return err
	}
	for _, kv := range metaRows(meta) {
		if _, err := tx.Exec(`INSERT INTO `+TableIndexMeta+`(key, value) VALUES(?, ?)`,
			kv[0], kv[1]); err != nil {
			return fmt.Errorf("写 %s 失败：%w", TableIndexMeta, err)
		}
	}
	for i, c := range cards {
		rowid := int64(i + 1)
		if _, err := tx.Exec(`INSERT INTO `+TableCards+
			`(rowid, id, path, domain, title, status, deprecated, deleted, replaced_by,
			  content_hash, mtime_unix, kind, validation)
			 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			rowid, c.ID, c.Path, c.Domain, c.Title, c.Status,
			boolInt(c.Deprecated), boolInt(c.Deleted), c.ReplacedBy, c.ContentHash, c.MTimeUnix,
			c.Kind, c.Validation,
		); err != nil {
			return fmt.Errorf("写 %s 失败（id=%s）：%w", TableCards, c.ID, err)
		}
		if _, err := tx.Exec(`INSERT INTO `+TableCardsFTS+
			`(rowid, id, title, body, bigram_text, kind, validation)
			 VALUES(?, ?, ?, ?, ?, ?, ?)`,
			rowid, c.ID, c.Title, c.Body, BigramText(c.Title+"\n"+c.Body),
			c.Kind, c.Validation,
		); err != nil {
			return fmt.Errorf("写 %s 失败（id=%s）：%w", TableCardsFTS, c.ID, err)
		}
	}
	for i, rel := range rels {
		if _, err := tx.Exec(`INSERT INTO `+TableRelations+
			`(rowid, src_id, verb, dst_id, src_path, line) VALUES(?, ?, ?, ?, ?, ?)`,
			int64(i+1), rel.SrcID, rel.Verb, rel.DstID, rel.SrcPath, rel.Line,
		); err != nil {
			return fmt.Errorf("写 %s 失败（%s %s %s）：%w",
				TableRelations, rel.SrcID, rel.Verb, rel.DstID, err)
		}
	}
	for _, f := range files {
		if _, err := tx.Exec(`INSERT INTO `+TableFiles+
			`(path, content_hash, size, mtime_unix, indexed_at_unix) VALUES(?, ?, ?, ?, ?)`,
			f.Path, f.ContentHash, f.Size, f.MTimeUnix, f.IndexedAtUnix,
		); err != nil {
			return fmt.Errorf("写 %s 失败（%s）：%w", TableFiles, f.Path, err)
		}
	}
	for _, s := range skipped {
		if _, err := tx.Exec(`INSERT INTO `+TableSkipped+`(path, kind) VALUES(?, ?)`,
			s.Path, s.Kind); err != nil {
			return fmt.Errorf("写 %s 失败（%s）：%w", TableSkipped, s.Path, err)
		}
	}
	return nil
}

// metaRows 把 Meta 摊成键值对，次序恒为 MetaKeys()（恰 6 行，第 7 行不存在）。
func metaRows(m Meta) [][2]string {
	return [][2]string{
		{MetaSchemaVersion, strconv.Itoa(m.SchemaVersion)},
		{MetaHead, m.Head},
		{MetaFilesHash, m.FilesHash},
		{MetaTokenizerMode, m.TokenizerMode},
		{MetaBuiltAtUnix, strconv.FormatInt(m.BuiltAtUnix, 10)},
		{MetaCardCount, strconv.Itoa(m.CardCount)},
	}
}

// detectTokenizer 是分词档位的**单点判定**（合同 §2.6）：先试 trigram（D0），
// 失败退 unicode61（D1），FTS5 整体不可用再退普通表（D2）。
//
// 判定只在**建库时**发生一次，结果写进 `index_meta.tokenizer_mode`；
// 读路径只读该键，**不得**重新探测（合同 §2.6 第 1 条）。
func detectTokenizer(db *sql.DB) (string, error) {
	var last error
	for _, mode := range TokenizerModes() {
		if _, err := db.Exec(ftsDDL(mode)); err == nil {
			return mode, nil
		} else {
			last = err
			// 建表失败可能留下半张表，删干净再试下一档（虚表建失败通常什么都不留）。
			_, _ = db.Exec(`DROP TABLE IF EXISTS ` + TableCardsFTS)
		}
	}
	return "", fmt.Errorf("三档分词形态全部建表失败（最后一次：%w）", last)
}

// FilesHash 是水位线主键（合同 §5.1）：全部 content_hash 按 path 升序聚合的 SHA-256。
//
// 逐字口径：对每个文件写入 `path + "\x00" + content_hash + "\n"`，再取整体 SHA-256 十六进制。
// 空集合的取值是空串的哈希（**不是**空串本身）——「零文件」也是一个明确的水位线状态。
func FilesHash(files []File) string {
	sorted := sortedFiles(files)
	h := sha256.New()
	for _, f := range sorted {
		h.Write([]byte(f.Path))
		h.Write([]byte{0})
		h.Write([]byte(f.ContentHash))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// BigramText 把字符串里的 CJK 连续串预切成**空格分隔的 bigram**（D0b 补路的写入侧口径，
// 合同 §2.6）：`trigram` 单独无法覆盖 1 ~ 2 字中文查询，故写入侧多留一列。
//
// 非 CJK 片段按空白切成词后原样保留（ASCII 由主路 tokenizer 负责，这里不拆字母），
// 单字 CJK 串原样作为一个 token。
//
// 结果**两端各留一个空格**（非空时）：主路 D0 下整表 tokenizer 是 `trigram`，查询串同样被
// 切成 3 字符，因此 2 字 bigram 只能以「bigram + 相邻空格」的 3 字符形态被命中。两端补空格
// 让首尾 bigram 与中间 bigram 具备**同一种**可命中形态，读路径（T-…-067）无须对首尾特判。
// **只写进 `.index/`，绝不回写 Markdown**（P-1）。
func BigramText(s string) string {
	var out []string
	cjk := make([]rune, 0, 16)
	word := make([]rune, 0, 16)
	flushCJK := func() {
		if len(cjk) == 0 {
			return
		}
		if len(cjk) == 1 {
			out = append(out, string(cjk))
		}
		for i := 0; i+1 < len(cjk); i++ {
			out = append(out, string(cjk[i:i+2]))
		}
		cjk = cjk[:0]
	}
	flushWord := func() {
		if len(word) == 0 {
			return
		}
		out = append(out, string(word))
		word = word[:0]
	}
	for _, r := range s {
		switch {
		case isCJK(r):
			flushWord()
			cjk = append(cjk, r)
		case unicode.IsSpace(r):
			flushCJK()
			flushWord()
		default:
			flushCJK()
			word = append(word, r)
		}
	}
	flushCJK()
	flushWord()
	if len(out) == 0 {
		return ""
	}
	return " " + strings.Join(out, " ") + " "
}

// isCJK 判定一个 rune 是否属需要预切的表意文字区间（判定面刻意保守：
// 只覆盖统一表意文字与扩展 A、兼容表意文字，以及日文假名 —— 它们都不带天然分词空格）。
func isCJK(r rune) bool {
	switch {
	case r >= 0x3040 && r <= 0x30FF: // 平假名 / 片假名
		return true
	case r >= 0x3400 && r <= 0x4DBF: // 扩展 A
		return true
	case r >= 0x4E00 && r <= 0x9FFF: // 统一表意文字
		return true
	case r >= 0xF900 && r <= 0xFAFF: // 兼容表意文字
		return true
	}
	return false
}

// Digest 是「索引逻辑内容」的确定性摘要：等价比较的**唯一**口径（合同 §4.4）。
//
// 导出范围 = 六张表的全部行按确定序，**除**两列天然非确定的时间戳
// （`index_meta.built_at_unix` 与 `files.indexed_at_unix`）之外逐字进摘要。
// 因此「同一 vault 连续两次 build」「rebuild 与 fresh build」都必须得到同一个 Digest。
func Digest(dir string) (string, error) {
	dump, err := exportRows(dir)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(dump))
	return hex.EncodeToString(sum[:]), nil
}

// exportRows 以只读方式把库导出成确定性文本（Digest 的输入；测试里也直接用于人读 diff）。
func exportRows(dir string) (string, error) {
	db, err := openDB(filepath.Join(dir, DBFileName), true)
	if err != nil {
		return "", err
	}
	defer func() { _ = db.Close() }()

	var b strings.Builder
	queries := []struct {
		table string
		sql   string
	}{
		{TableIndexMeta, `SELECT key, value FROM ` + TableIndexMeta +
			` WHERE key <> '` + MetaBuiltAtUnix + `' ORDER BY key`},
		{TableCards, `SELECT rowid, id, path, domain, title, status, deprecated, deleted,
			replaced_by, content_hash, mtime_unix, kind, validation FROM ` + TableCards +
			` ORDER BY rowid`},
		{TableCardsFTS, `SELECT rowid, id, title, body, bigram_text, kind, validation FROM ` +
			TableCardsFTS + ` ORDER BY rowid`},
		{TableRelations, `SELECT rowid, src_id, verb, dst_id, src_path, line FROM ` +
			TableRelations + ` ORDER BY rowid`},
		{TableFiles, `SELECT path, content_hash, size, mtime_unix FROM ` + TableFiles + ` ORDER BY path`},
		{TableSkipped, `SELECT path, kind FROM ` + TableSkipped + ` ORDER BY path`},
	}
	for _, q := range queries {
		b.WriteString("## table " + q.table + "\n")
		if err := dumpQuery(db, q.sql, &b); err != nil {
			return "", fmt.Errorf("导出 %s 失败：%w", q.table, err)
		}
	}
	return b.String(), nil
}

// dumpQuery 把一条查询的结果按「\x1f 分隔字段 + \n 分隔行」写进 b（NULL 记作 <null>）。
func dumpQuery(db *sql.DB, query string, b *strings.Builder) error {
	rows, err := db.Query(query)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		return err
	}
	for rows.Next() {
		cells := make([]interface{}, len(cols))
		for i := range cells {
			cells[i] = new(sql.RawBytes)
		}
		if err := rows.Scan(cells...); err != nil {
			return err
		}
		parts := make([]string, 0, len(cols))
		for _, c := range cells {
			raw := c.(*sql.RawBytes)
			if *raw == nil {
				parts = append(parts, "<null>")
				continue
			}
			parts = append(parts, string(*raw))
		}
		b.WriteString(strings.Join(parts, "\x1f"))
		b.WriteString("\n")
	}
	return rows.Err()
}

// ReadMeta 只读取回 `index_meta` 的六键（多一键 / 少一键都是错误：键集合封闭）。
func ReadMeta(dir string) (Meta, error) {
	db, err := openDB(filepath.Join(dir, DBFileName), true)
	if err != nil {
		return Meta{}, err
	}
	defer func() { _ = db.Close() }()
	return readMetaFrom(db)
}

func readMetaFrom(db *sql.DB) (Meta, error) {
	rows, err := db.Query(`SELECT key, value FROM ` + TableIndexMeta)
	if err != nil {
		return Meta{}, err
	}
	defer func() { _ = rows.Close() }()
	got := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return Meta{}, err
		}
		got[k] = v
	}
	if err := rows.Err(); err != nil {
		return Meta{}, err
	}
	return metaFromKV(got)
}

// metaFromKV 把 `index_meta` 的键值对折成强类型 Meta，并做**键集合封闭**校验
// （少一键 / 多一键都是错误）。只读连接与事务内读回共用它，避免两套校验漂移。
func metaFromKV(got map[string]string) (Meta, error) {
	for _, k := range MetaKeys() {
		if _, ok := got[k]; !ok {
			return Meta{}, fmt.Errorf("%s 缺键 %s", TableIndexMeta, k)
		}
	}
	if len(got) != len(MetaKeys()) {
		return Meta{}, fmt.Errorf("%s 有 %d 键，期望恰 %d 键（键集合封闭）",
			TableIndexMeta, len(got), len(MetaKeys()))
	}
	m := Meta{
		Head: got[MetaHead], FilesHash: got[MetaFilesHash],
		TokenizerMode: got[MetaTokenizerMode],
	}
	var err1, err2, err3 error
	m.SchemaVersion, err1 = strconv.Atoi(got[MetaSchemaVersion])
	m.BuiltAtUnix, err2 = strconv.ParseInt(got[MetaBuiltAtUnix], 10, 64)
	m.CardCount, err3 = strconv.Atoi(got[MetaCardCount])
	for _, e := range []error{err1, err2, err3} {
		if e != nil {
			return Meta{}, fmt.Errorf("%s 的数值键不可解析：%w", TableIndexMeta, e)
		}
	}
	return m, nil
}

// openDB 打开索引库。readOnly=true 走 `mode=ro`：`eg index status` 只读到底，
// 不得在查看状态时改写派生库的任何字节。
func openDB(dbPath string, readOnly bool) (*sql.DB, error) {
	db, err := sql.Open(driverName, dsn(dbPath, readOnly))
	if err != nil {
		return nil, err
	}
	// 单连接：PRAGMA 与事务口径不因连接池而漂移（写入是单进程单事务）。
	db.SetMaxOpenConns(1)
	return db, nil
}

// dsn 拼出驱动可吃的连接串（路径经 URL 转义，含空格 / 中文的 vault 路径同样可用）。
func dsn(dbPath string, readOnly bool) string {
	u := url.URL{Scheme: "file", Path: dbPath}
	if readOnly {
		u.RawQuery = "mode=ro"
	}
	return u.String()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func validSkippedKind(kind string) bool {
	for _, k := range SkippedKinds() {
		if k == kind {
			return true
		}
	}
	return false
}

// validateCardKinds 在写盘前逐卡校验分型与论证进度（Schema v2 §7 / §6.1）。
//
// 两条判定都取 schema.go 的封闭取值域（单一真源），错误文案必须**指名道姓**：
// 哪张卡、哪个字段、实得什么值、封闭域是什么 —— 库上的 CHECK 只会回一句
// `constraint failed`，对调用方（写命令 / 扫描面）毫无定位价值，两层各司其职。
//
// 这里**不做**任何兜底：既不把空 kind 补成 knowledge，也不把空 validation 补成 pending。
// 兜底会把「调用方漏传」变成「库里一行看似合法的错行」，而错行只能在读路径被用户发现。
func validateCardKinds(cards []Card) error {
	for _, c := range cards {
		if !validCardKind(c.Kind) {
			return fmt.Errorf("卡片 %s（%s）的 kind = %q 不在封闭取值域 %v 内"+
				"（调用方必须显式给值，索引层不做默认分型）", c.ID, c.Path, c.Kind, CardKinds())
		}
		if !validCardValidation(c.Kind, c.Validation) {
			if c.Kind == CardKindKnowledge {
				return fmt.Errorf("卡片 %s（%s）是 %s，validation 必须为空串，实得 %q"+
					"（论证进度只属观点）", c.ID, c.Path, CardKindKnowledge, c.Validation)
			}
			return fmt.Errorf("观点 %s（%s）的 validation = %q 不在封闭取值域 %v 内",
				c.ID, c.Path, c.Validation, CardValidations())
		}
	}
	return nil
}

// —— 确定序：四份输入各有唯一排序键，排序恒在副本上做（不改调用方的切片）——

func sortedCards(in []Card) []Card {
	out := append([]Card(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		// 同 ID 多文件是库里的既有病态（`eg check` 的 duplicate_id）：按 path 定序，
		// 使「留下哪一份」这件事**确定**而不是看扫描次序。
		return out[i].Path < out[j].Path
	})
	return out
}

// dedupCards 按 id 只保留第一份（确定序下即 path 最小的那份），返回被丢弃的条数。
func dedupCards(in []Card) ([]Card, int) {
	out := make([]Card, 0, len(in))
	seen := make(map[string]bool, len(in))
	dropped := 0
	for _, c := range in {
		if seen[c.ID] {
			dropped++
			continue
		}
		seen[c.ID] = true
		out = append(out, c)
	}
	return out, dropped
}

// dedupRelations 按 (src_id, verb, dst_id) 只保留第一条（UNIQUE 约束的等价前置去重），
// 返回被丢弃的条数。重复边本身是 `eg check` 的 relation_duplicate finding，索引不替库治病。
func dedupRelations(in []Relation) ([]Relation, int) {
	out := make([]Relation, 0, len(in))
	seen := make(map[string]bool, len(in))
	dropped := 0
	for _, rel := range in {
		key := rel.SrcID + "\x00" + rel.Verb + "\x00" + rel.DstID
		if seen[key] {
			dropped++
			continue
		}
		seen[key] = true
		out = append(out, rel)
	}
	return out, dropped
}

func sortedRelations(in []Relation) []Relation {
	out := append([]Relation(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.SrcID != b.SrcID {
			return a.SrcID < b.SrcID
		}
		if a.Verb != b.Verb {
			return a.Verb < b.Verb
		}
		return a.DstID < b.DstID
	})
	return out
}

func sortedFiles(in []File) []File {
	out := append([]File(nil), in...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func sortedSkipped(in []SkippedFile) []SkippedFile {
	out := append([]SkippedFile(nil), in...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
