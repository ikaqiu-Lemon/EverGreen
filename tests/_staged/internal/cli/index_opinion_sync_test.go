package cli

// T-005-C 端到端合同：`eg index sync` 对 `domains/<域>/opinions/o-*.md` 的
// **增量收敛**（新增 / 修改 / 删除三态），全程走真实 CLI + 真实 Markdown 扫描链路。
//
// 与 A / B 两批的分工（本文件一格都不重复它们）：
//   - A（`internal/index`）钉的是**库形态**：两列的 DDL、约束、以及通用列的无损往返；
//   - B（`internal/cli` + `internal/query`）钉的是**全量**快照与读路径投影是否同口径收录观点；
//   - C 钉的是**增量**：`eg index sync` 在观点的增 / 改 / 删三态上必须只**读取 / 解析受影响的
//     权威文件**来收敛，而**不是**退化成从全量权威扫描重建；SQL 派生行空间则按既有实现
//     （`clearDerived` + `writeAll`，同一事务内按确定序）**整体重写**——即 `action == "synced"`
//     的含义是「没有走全量权威扫描重建」，**不是**「SQLite 只写了受影响的那几行」。
//     此外每一态的结果都要与「同一份权威 Markdown 的全量重建」逐字等价。
//
// 为什么必须端到端而不能只在 `internal/index` 上喂 DTO：
// 增量的判定链是「真实文件 → `query.VaultScan` → `indexSnapshotWith` 的观点投影 →
// `DiffFiles` 三向差异 → `deltaFromSnapshot` 按受影响路径切片 → `applyDelta` 合并重写」。
// 用假 DTO 从中间插入，恰好把「观点到底有没有进这条链」这一环 stub 掉——而那正是本批要证的。
// 因此本文件只用真实 `o-*` Markdown、真实 `eg index build/sync/rebuild/status`、真实 SQL 对账。
//
// 关于「sections」这一格的口径（**必须先说清，避免读者按不存在的表去找证据**）：
// 本仓索引的表集合是**封闭的 6 张**（`index.TableNames()`：index_meta / cards / cards_fts /
// relations / files / skipped），**没有 sections 表**——分区不是独立派生行，而是经
// `Body()` 逐字进 `cards_fts.body`（FTS 检索面）。所以本文件把「section 派生与权威一致」
// 落成**逐分区对账**：把 `cards_fts.body` 与磁盘 Markdown 各自按 `## ` 切成有序分区，
// 逐个比较**分区名与分区正文**，并要求分区名序列恰等于 `store.OpinionSections()` 的固定五分区。
// 这比"看看总数"强，也不需要为了造证据去增第 7 张表（那会同时违反封闭集合合同与本批范围）。

import (
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/index"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// —— 本文件专用语料常量（`sopn` = sync-opinion 前缀，与 B 批的 `opn*` 不冲突）——
const (
	// 新增的那条观点：ID 仍以 o- 起头（sortedCards 的 ID 全序 ⇒ 恒排在 k-* 之后）。
	sopnNewID    = "o-20261101-delta"
	sopnNewTitle = "增量收敛下的观点应当可被检索"

	// 三个**互不相同**、且既有语料里绝不出现的 ASCII 令牌：
	//   · sopnAddToken 用于「新增后能命中」；
	//   · sopnModToken 用于「修改后新令牌能命中」；
	//   · 修改阶段同时要求 sopnAddToken **消失**（旧正文没有被留在 FTS 里）。
	sopnAddToken = "xkvbnmqwtz"
	sopnModToken = "hjkltrewqzp"

	// 旁路全量重建用的**另一个**固定时刻：与 idxFixedNow 刻意不同秒，
	// 这样 built_at_unix / indexed_at_unix 两侧必然不同 —— 用来证明 Digest 等价
	// 不是靠时间戳恰好相同凑出来的（见 sopnAssertDigestEqualsFullRebuild）。
	sopnBypassNow = "2026-12-19T11:30:00+08:00"
)

// sopnRel 是新增观点在 vault 内的相对路径（唯一真源，删除阶段也用它）。
func sopnRel() string { return store.OpinionRel(opnDomain, sopnNewID) }

// sopnWriteOpinion 把新增 / 修改后的观点**真实落盘并提交**（用户的真实路径就是这样）。
//
// 提交的意义有两层：① 工作区保持干净，便于「索引恒零写权威、恒零 commit」这条反证；
// ② `head` 水位线随之前进，于是本文件每一次 sync 都是「head 变了 + 文件集/内容变了」
// 这个真实组合，而不是只改文件不动 git 的实验室态。
func sopnWriteOpinion(t *testing.T, dir, validation, token, msg string) {
	t.Helper()
	writeFileMk(t, filepath.Join(dir, filepath.FromSlash(sopnRel())),
		opnFileContent(sopnNewID, sopnNewTitle, validation, token, "supports", applyCardID))
	sopnCommitAll(t, dir, msg)
}

// sopnDeleteOpinion 真实删除该观点文件并提交。
func sopnDeleteOpinion(t *testing.T, dir string) {
	t.Helper()
	if err := os.Remove(filepath.Join(dir, filepath.FromSlash(sopnRel()))); err != nil {
		t.Fatalf("删除观点文件失败：%v", err)
	}
	sopnCommitAll(t, dir, "drop opinion")
}

func sopnCommitAll(t *testing.T, dir, msg string) {
	t.Helper()
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "-c", "user.name=eg-test", "-c", "user.email=eg-test@example.com",
		"commit", "-q", "-m", msg)
	if got := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); got != "" {
		t.Fatalf("前置：落盘后工作区必须干净，得到 %q", got)
	}
}

// —— SQL 快照脚手架：按**任意** DB 路径只读打开（活库与旁路库共用一套读法）——

func sopnOpenDBAt(t *testing.T, dbPath string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		t.Fatalf("打开索引库 %s 失败：%v", dbPath, err)
	}
	return db
}

// sopnQueryRows 把一条只读 SQL 的结果集折成「每行各列用 \x1f 连接」的字符串切片。
//
// 用一个通用取数器而不是每张表写一个 struct：本文件的判据都是「逐列逐行**逐字**相等」，
// 字符串化之后既能深等比较，也能在失败时直接把两侧打出来供人读 diff。
func sopnQueryRows(t *testing.T, dbPath, query string, args ...interface{}) []string {
	t.Helper()
	db := sopnOpenDBAt(t, dbPath)
	defer func() { _ = db.Close() }()
	rows, err := db.Query(query, args...)
	if err != nil {
		t.Fatalf("查询失败（%s）：%v", query, err)
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("取列名失败：%v", err)
	}
	var out []string
	for rows.Next() {
		cells := make([]interface{}, len(cols))
		holders := make([]sql.NullString, len(cols))
		for i := range holders {
			cells[i] = &holders[i]
		}
		if err := rows.Scan(cells...); err != nil {
			t.Fatalf("scan 失败：%v", err)
		}
		parts := make([]string, 0, len(cols))
		for i, c := range cols {
			parts = append(parts, c+"="+holders[i].String)
		}
		out = append(out, strings.Join(parts, "\x1f"))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("遍历结果集失败：%v", err)
	}
	return out
}

// sopnLive 返回活库（vault/.index/eg.db）的路径。
func sopnLive(dir string) string { return index.DBPath(dir) }

// —— knowledge 侧「不受损」的精确快照：五张业务表里与 k-* 有关的**全部语义列** ——
//
// 判据不是「知识卡还有 2 张」这种计数，而是把 cards / cards_fts / relations / files
// 四张表里 knowledge 相关的行**逐列逐行**抓成快照，三个阶段结束后逐字比对。
// （skipped 表另有全表比对，见 sopnKnowledgeSnapshot 的第 5 项。）
func sopnKnowledgeSnapshot(t *testing.T, dbPath string) map[string][]string {
	t.Helper()
	k := string(index.CardKindKnowledge)
	return map[string][]string{
		"cards": sopnQueryRows(t, dbPath, `SELECT id, path, domain, title, status, deprecated,
			deleted, replaced_by, content_hash, mtime_unix, kind, validation FROM `+
			index.TableCards+` WHERE kind = ? ORDER BY id`, k),
		"cards_fts": sopnQueryRows(t, dbPath, `SELECT id, title, body, bigram_text, kind, validation
			FROM `+index.TableCardsFTS+` WHERE kind = ? ORDER BY id`, k),
		"relations": sopnQueryRows(t, dbPath, `SELECT src_id, verb, dst_id, src_path, line FROM `+
			index.TableRelations+` WHERE src_id IN (SELECT id FROM `+index.TableCards+
			` WHERE kind = ?) ORDER BY src_id, verb, dst_id, line`, k),
		"files": sopnQueryRows(t, dbPath, `SELECT path, content_hash, size, mtime_unix FROM `+
			index.TableFiles+` WHERE path IN (SELECT path FROM `+index.TableCards+
			` WHERE kind = ?) ORDER BY path`, k),
		"skipped": sopnQueryRows(t, dbPath, `SELECT path, kind FROM `+index.TableSkipped+` ORDER BY path`),
	}
}

func sopnAssertKnowledgeIntact(t *testing.T, phase, dbPath string, want map[string][]string) {
	t.Helper()
	got := sopnKnowledgeSnapshot(t, dbPath)
	if len(got) != len(want) {
		t.Fatalf("[%s] knowledge 快照分组数变了：前 %d、后 %d", phase, len(want), len(got))
	}
	for table, wantRows := range want {
		gotRows, ok := got[table]
		if !ok {
			t.Fatalf("[%s] knowledge 快照缺分组 %s", phase, table)
		}
		if len(gotRows) != len(wantRows) {
			t.Fatalf("[%s] %s 的 knowledge 行数变了：前 %d、后 %d\n前=%v\n后=%v",
				phase, table, len(wantRows), len(gotRows), wantRows, gotRows)
		}
		for i := range wantRows {
			if gotRows[i] != wantRows[i] {
				t.Fatalf("[%s] %s 第 %d 行的 knowledge 派生列被改动了：\n前=%s\n后=%s",
					phase, table, i, wantRows[i], gotRows[i])
			}
		}
	}
	if len(want["cards"]) != 2 {
		t.Fatalf("[%s] 前置：knowledge 基线应恰 2 张卡，实得 %d（否则本反证会退化成空转）",
			phase, len(want["cards"]))
	}
}

// —— 「sections 与权威一致」的逐分区对账脚手架 ——

// sopnSection 是一条分区：名字 + 正文（正文按行保留原样，仅去掉首尾空行）。
type sopnSection struct {
	name string
	body string
}

// sopnSplitSections 把一段 Markdown 正文按 `## ` 标题切成**有序**分区。
//
// 两侧（磁盘文件、cards_fts.body）都走这**同一个**切法，因此比较的是分区语义本身，
// 不会因为「正文起点差一个换行」这种偏移把等价关系判错；而分区名与分区正文逐条比较，
// 又保证了「少一个分区 / 多一个分区 / 某个分区正文漂了」都会当场红。
func sopnSplitSections(text string) []sopnSection {
	var out []sopnSection
	var cur *sopnSection
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "## ") {
			out = append(out, sopnSection{name: strings.TrimSpace(strings.TrimPrefix(line, "## "))})
			cur = &out[len(out)-1]
			continue
		}
		if cur == nil {
			continue // 分区之前的内容（`# 标题`）不属于任何分区
		}
		cur.body += line + "\n"
	}
	for i := range out {
		out[i].body = strings.Trim(out[i].body, "\n")
	}
	return out
}

// sopnDiskBody 读磁盘 Markdown，返回 frontmatter 之后的正文。
//
// 刻意**不**复用产品解析器：本判据要证的是「派生行与盘上内容逐字一致」，
// 若期望值也由产品解析器给出，两侧就会一起漂而测不出来。切法是测试自备的最朴素规则：
// 首行 `---`，其后第一处 `\n---\n` 即 frontmatter 结束。
func sopnDiskBody(t *testing.T, dir, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("读磁盘 Markdown %s 失败：%v", rel, err)
	}
	text := string(raw)
	const open = "---\n"
	if !strings.HasPrefix(text, open) {
		t.Fatalf("%s 不以 frontmatter 起始，语料不合约", rel)
	}
	idx := strings.Index(text[len(open):], "\n"+open)
	if idx < 0 {
		t.Fatalf("%s 找不到 frontmatter 结束标记", rel)
	}
	return text[len(open)+idx+len("\n")+len(open):]
}

// sopnAssertSectionsMatchDisk 逐分区对账：cards_fts.body ↔ 磁盘 Markdown ↔ 固定五分区。
func sopnAssertSectionsMatchDisk(t *testing.T, phase, dir, id string) {
	t.Helper()
	rows := sopnQueryRows(t, sopnLive(dir),
		`SELECT body FROM `+index.TableCardsFTS+` WHERE id = ?`, id)
	if len(rows) != 1 {
		t.Fatalf("[%s] cards_fts 里 id=%s 的行数 = %d，期望恰 1", phase, id, len(rows))
	}
	ftsBody := strings.TrimPrefix(rows[0], "body=")
	diskSecs := sopnSplitSections(sopnDiskBody(t, dir, sopnRel()))
	ftsSecs := sopnSplitSections(ftsBody)

	wantNames := store.OpinionSections()
	if len(diskSecs) != len(wantNames) {
		t.Fatalf("[%s] 磁盘上 %s 的分区数 = %d，期望固定五分区 %v",
			phase, id, len(diskSecs), wantNames)
	}
	if len(ftsSecs) != len(diskSecs) {
		t.Fatalf("[%s] cards_fts.body 的分区数 = %d，磁盘 = %d（派生分区与权威不一致）",
			phase, len(ftsSecs), len(diskSecs))
	}
	for i := range diskSecs {
		if diskSecs[i].name != wantNames[i] {
			t.Fatalf("[%s] 磁盘第 %d 个分区名 = %q，期望 %q（语料不合约）",
				phase, i, diskSecs[i].name, wantNames[i])
		}
		if ftsSecs[i].name != diskSecs[i].name {
			t.Fatalf("[%s] 第 %d 个派生分区名 = %q，磁盘为 %q",
				phase, i, ftsSecs[i].name, diskSecs[i].name)
		}
		if ftsSecs[i].body != diskSecs[i].body {
			t.Fatalf("[%s] 分区 %q 的派生正文与磁盘不符：\n派生=%q\n磁盘=%q",
				phase, diskSecs[i].name, ftsSecs[i].body, diskSecs[i].body)
		}
	}
}

// —— 「增量现态 == 同一份权威 Markdown 的全量重建」旁路 ——

// sopnAssertDigestEqualsFullRebuild 用**同一份权威 Markdown** 在一个空目录里做一次
// 全量构建（与 `eg index build` 完全同一条实现：`indexSnapshot` + `index.Build`），
// 再与活库比 `index.Digest`。
//
// 为什么不直接跑 `eg index rebuild`：那会把活库覆盖掉，三个阶段的增量链就断了——
// 后面两个阶段就不再是「在上一次增量结果之上再增量」，而是「在一次全量重建之上增量」，
// 那正是本批最该证伪的东西。（收尾时另有一次真实 `eg index rebuild` 的等价核对。）
//
// **假等价 / 假不等价的排除**：旁路库刻意用**另一个时刻**（sopnBypassNow）构建，
// 因此 `index_meta.built_at_unix` 与 `files.indexed_at_unix` 两侧必然不同；
// 本函数先断言「它们确实不同」，再断言 Digest 相同 —— 于是这条等价既不可能是
// 「时间戳恰好一样」蒙来的，也证明了比较口径确实把非确定字段剔在外面。
func sopnAssertDigestEqualsFullRebuild(t *testing.T, phase, dir string) string {
	t.Helper()
	live, err := index.Digest(index.DirPath(dir))
	if err != nil {
		t.Fatalf("[%s] 活库应可读：%v", phase, err)
	}

	setGitIdentity(t)
	r := newTestRoot(t, dir)
	bypassNow := func() time.Time { return stampAt(t, sopnBypassNow) }
	r.Now = bypassNow
	snap, _, err := r.indexSnapshot(dir)
	if err != nil {
		t.Fatalf("[%s] 旁路快照组装失败：%v", phase, err)
	}
	scratch := filepath.Join(t.TempDir(), "bypass-"+phase)
	if _, err := index.Build(scratch, snap, index.Options{Now: bypassNow}); err != nil {
		t.Fatalf("[%s] 旁路全量构建失败：%v", phase, err)
	}
	scratchDB := filepath.Join(scratch, index.DBFileName)

	// ① 非确定字段两侧必须**确实不同**（否则下面的等价判据会退化成「时间戳也一样」的巧合）。
	liveBuilt := sopnQueryRows(t, sopnLive(dir),
		`SELECT value FROM `+index.TableIndexMeta+` WHERE key = ?`, index.MetaBuiltAtUnix)
	bypassBuilt := sopnQueryRows(t, scratchDB,
		`SELECT value FROM `+index.TableIndexMeta+` WHERE key = ?`, index.MetaBuiltAtUnix)
	if len(liveBuilt) != 1 || len(bypassBuilt) != 1 {
		t.Fatalf("[%s] built_at_unix 应各恰 1 行，实得 %v / %v", phase, liveBuilt, bypassBuilt)
	}
	if liveBuilt[0] == bypassBuilt[0] {
		t.Fatalf("[%s] 活库与旁路库的 built_at_unix 相同（%s）：本判据要求两侧时钟不同，"+
			"否则证不出 Digest 对非确定字段不敏感", phase, liveBuilt[0])
	}
	liveIndexedAt := sopnQueryRows(t, sopnLive(dir),
		`SELECT path, indexed_at_unix FROM `+index.TableFiles+` ORDER BY path`)
	bypassIndexedAt := sopnQueryRows(t, scratchDB,
		`SELECT path, indexed_at_unix FROM `+index.TableFiles+` ORDER BY path`)
	if len(liveIndexedAt) != len(bypassIndexedAt) {
		t.Fatalf("[%s] files 行数两侧不同：活库 %d、旁路 %d", phase, len(liveIndexedAt), len(bypassIndexedAt))
	}
	sameIndexedAt := true
	for i := range liveIndexedAt {
		if liveIndexedAt[i] != bypassIndexedAt[i] {
			sameIndexedAt = false
			break
		}
	}
	if sameIndexedAt {
		t.Fatalf("[%s] 活库与旁路库的 files.indexed_at_unix 逐行相同：本判据要求两侧不同，"+
			"否则证不出 Digest 对非确定字段不敏感（实得 %v）", phase, liveIndexedAt)
	}

	// ② 在此前提下，Digest 必须逐字相等。
	full, err := index.Digest(scratch)
	if err != nil {
		t.Fatalf("[%s] 旁路库应可读：%v", phase, err)
	}
	if full != live {
		t.Fatalf("[%s] 增量现态与同一份权威 Markdown 的全量重建不等价：增量 %s ≠ 全量 %s",
			phase, live, full)
	}
	return live
}

// —— sync 一次并把「非 rebuild」这件事钉死 ——

// sopnSyncIncremental 跑一次真实 `eg index sync`，并断言它是**增量收敛**：
//
//	action == synced（**不是** sync_built / sync_rebuilt / skipped / built / rebuilt 里的任何一个）；
//	degraded == false；三向 diff 计数逐个等于期望；且本次不写权威、不发 commit。
func sopnSyncIncremental(t *testing.T, phase, dir string, wantAdded, wantModified, wantRemoved, wantUnchanged int) {
	t.Helper()
	authBefore := idxAuthoritySnapshot(t, dir)
	commitsBefore := strings.TrimSpace(gitOut(t, dir, "rev-list", "--count", "HEAD"))
	statusBefore := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain"))

	code, out, errOut := runIndexCLI(t, dir, "sync")
	if code != ExitOK {
		t.Fatalf("[%s] sync 退出码 = %d，期望 0：%s", phase, code, errOut)
	}

	got := idxString(t, out, "action")
	if got != string(index.ActionSynced) {
		t.Fatalf("[%s] action = %q，期望 %q（观点的增 / 改 / 删必须是增量收敛）",
			phase, got, index.ActionSynced)
	}
	// 逐个点名排除**所有**全量口径的 action：让「不是 rebuild」这件事不依赖上一条断言的措辞。
	for _, forbidden := range []index.SyncAction{
		index.ActionSyncBuilt, index.ActionSyncRebuilt, index.ActionSkipped, index.ActionSyncNoop,
	} {
		if got == string(forbidden) {
			t.Fatalf("[%s] action = %q：观点的增量收敛不得退化成 %q", phase, got, forbidden)
		}
	}
	if d := idxString(t, out, "degraded"); d != "false" {
		t.Fatalf("[%s] degraded = %q，期望 false（健康索引 + 观点变更不该触发任何退化）", phase, d)
	}
	if codes := idxWarnCodes(t, out); len(codes) != 0 {
		t.Fatalf("[%s] sync 产出 warning %v，期望零条（退化才留痕；这里不该退化）", phase, codes)
	}
	for key, want := range map[string]int{
		"changed_added": wantAdded, "changed_modified": wantModified,
		"changed_removed": wantRemoved, "changed_unchanged": wantUnchanged,
	} {
		if g := idxString(t, out, key); g != itoa(int64(want)) {
			t.Fatalf("[%s] %s = %q，期望 %d（只许碰受影响的那一个文件）", phase, key, g, want)
		}
	}

	// 反证：sync 只写 `.index/`——权威内容（逐字快照）、commit 数、工作区脏度三项都不许动。
	idxAssertAuthorityUnchanged(t, dir, authBefore)
	if c := strings.TrimSpace(gitOut(t, dir, "rev-list", "--count", "HEAD")); c != commitsBefore {
		t.Fatalf("[%s] commit 数变了：前 %s → 后 %s（索引恒零 commit）", phase, commitsBefore, c)
	}
	if s := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); s != statusBefore {
		t.Fatalf("[%s] 工作区脏度变了：前 %q → 后 %q（.index/ 不进 Git）", phase, statusBefore, s)
	}

	// 收敛完立刻体检：healthy / fresh / use_index=true、零 warning。
	// 这一条同时是「行级不撒谎」的机器反证：增量若把观点行写歪，rowlevel 会判 W24。
	scode, sout, serr := runIndexCLI(t, dir, "status")
	if scode != ExitOK {
		t.Fatalf("[%s] 收敛后 status 退出码 = %d：%s", phase, scode, serr)
	}
	if h := idxHealth(t, sout); h != string(index.HealthHealthy) {
		t.Fatalf("[%s] 收敛后 health = %q，期望 healthy", phase, h)
	}
	if f := idxString(t, sout, "freshness"); f != string(index.FreshnessFresh) {
		t.Fatalf("[%s] 收敛后 freshness = %q，期望 fresh", phase, f)
	}
	if u := idxString(t, sout, "use_index"); u != "true" {
		t.Fatalf("[%s] 收敛后 use_index = %q，期望 true（读路径不该降级）", phase, u)
	}
	if codes := idxWarnCodes(t, sout); len(codes) != 0 {
		t.Fatalf("[%s] 收敛后 status 仍有 warning %v，期望零条", phase, codes)
	}
}

// sopnDerivedSnapshot 抓**五张派生表的全部行**（全部语义列，确定性排序）。
//
// 用途是删除阶段的「零孤儿」终判：删掉那条观点之后，全库派生行必须**逐字回到基线**——
// 这比「查几个 WHERE 看有没有残行」更强：它同时排除了「残留在别的表里」「顺序错位」
// 「顺手改坏了别人」三类问题。index_meta 不入快照，因为 head 会随 git 提交前进
// （那是权威事实的变化，不是派生残留）。
func sopnDerivedSnapshot(t *testing.T, dbPath string) map[string][]string {
	t.Helper()
	return map[string][]string{
		"cards": sopnQueryRows(t, dbPath, `SELECT id, path, domain, title, status, deprecated,
			deleted, replaced_by, content_hash, mtime_unix, kind, validation FROM `+
			index.TableCards+` ORDER BY id`),
		"cards_fts": sopnQueryRows(t, dbPath, `SELECT id, title, body, bigram_text, kind, validation
			FROM `+index.TableCardsFTS+` ORDER BY id`),
		"relations": sopnQueryRows(t, dbPath, `SELECT src_id, verb, dst_id, src_path, line FROM `+
			index.TableRelations+` ORDER BY src_id, verb, dst_id, line`),
		"files": sopnQueryRows(t, dbPath, `SELECT path, content_hash, size, mtime_unix FROM `+
			index.TableFiles+` ORDER BY path`),
		"skipped": sopnQueryRows(t, dbPath, `SELECT path, kind FROM `+index.TableSkipped+` ORDER BY path`),
	}
}

// sopnRowsOfID 取该 ID / 路径在五张表里的**全部**痕迹（用于「存在」与「零孤儿」两向断言）。
func sopnRowsOfID(t *testing.T, dir, id, rel string) map[string][]string {
	t.Helper()
	db := sopnLive(dir)
	return map[string][]string{
		"cards": sopnQueryRows(t, db, `SELECT id, path, kind, validation, title, domain FROM `+
			index.TableCards+` WHERE id = ? OR path = ? ORDER BY id`, id, rel),
		"cards_fts": sopnQueryRows(t, db, `SELECT id, kind, validation FROM `+
			index.TableCardsFTS+` WHERE id = ? ORDER BY id`, id),
		"relations": sopnQueryRows(t, db, `SELECT src_id, verb, dst_id, src_path FROM `+
			index.TableRelations+` WHERE src_id = ? OR dst_id = ? OR src_path = ? ORDER BY verb, dst_id`,
			id, id, rel),
		"files": sopnQueryRows(t, db, `SELECT path, content_hash, size FROM `+
			index.TableFiles+` WHERE path = ?`, rel),
		"skipped": sopnQueryRows(t, db, `SELECT path, kind FROM `+
			index.TableSkipped+` WHERE path = ?`, rel),
	}
}

// sopnFileHash 取 files 表某条路径的 content_hash（阶段间比较「内容确实变了」）。
func sopnFileHash(t *testing.T, dir, rel string) string {
	t.Helper()
	rows := sopnQueryRows(t, sopnLive(dir),
		`SELECT content_hash FROM `+index.TableFiles+` WHERE path = ?`, rel)
	if len(rows) != 1 {
		t.Fatalf("files 里 %s 的行数 = %d，期望恰 1", rel, len(rows))
	}
	return strings.TrimPrefix(rows[0], "content_hash=")
}

// TestIndexSyncConvergesOpinionAddModifyDelete —— T-005-C 的**唯一**主合同：
// `eg index sync` 对 `o-*` 的新增 / 修改 / 删除三态都做增量收敛，且每一态都与
// 「同一份权威 Markdown 的全量重建」逐字等价、既有 knowledge 一格不受损。
//
// 五个阶段（同一个 vault、同一条增量链，后一阶段建立在前一阶段的增量结果之上）：
//
//	阶段 0（基线）  ：2 张 k-* + 3 条 o-*，全量 build 出 schema v2 索引，抓基线证据；
//	阶段 1（新增）  ：落第 4 条 o-*（pending，正文含唯一令牌）→ sync；
//	阶段 2（修改）  ：换正文令牌 + validation pending → validated → sync；
//	阶段 3（删除）  ：删掉该 o-* → sync；全库派生行必须逐字回到基线（零孤儿）；
//	阶段 4（收尾）  ：真实 `eg index rebuild` 与末态增量结果 Digest 逐字相等。
func TestIndexSyncConvergesOpinionAddModifyDelete(t *testing.T) {
	// —— 阶段 0：基线 —— 同时含 knowledge 与 opinion 的真实 vault + 全量 build ——
	dir := idxVaultWithOpinions(t)
	if code, _, errOut := runIndexCLI(t, dir, "build"); code != ExitOK {
		t.Fatalf("[阶段0] 前置 build 退出码非 0：%s", errOut)
	}
	if v := sopnQueryRows(t, sopnLive(dir), `SELECT value FROM `+index.TableIndexMeta+
		` WHERE key = ?`, index.MetaSchemaVersion); len(v) != 1 ||
		v[0] != "value="+itoa(int64(index.IndexSchemaVersion)) {
		t.Fatalf("[阶段0] 前置：索引应为 schema v%d，实得 %v", index.IndexSchemaVersion, v)
	}
	baseKnow := sopnKnowledgeSnapshot(t, sopnLive(dir))
	baseDerived := sopnDerivedSnapshot(t, sopnLive(dir))
	if got := len(baseDerived["cards"]); got != 5 {
		t.Fatalf("[阶段0] 前置：基线应有 5 行卡（2 knowledge + 3 opinion），实得 %d", got)
	}
	if got := len(baseDerived["files"]); got != 5 {
		t.Fatalf("[阶段0] 前置：基线 files 应 5 行，实得 %d", got)
	}
	// 基线自身也过一次旁路等价，先证明「旁路机制本身可信」，后面三阶段才敢用它当判据。
	sopnAssertDigestEqualsFullRebuild(t, "阶段0", dir)
	sopnAssertKnowledgeIntact(t, "阶段0", sopnLive(dir), baseKnow)

	// —— 阶段 1：新增一条真实 o-*（pending）——
	sopnWriteOpinion(t, dir, string(index.ValidationPending), sopnAddToken, "add opinion")
	sopnSyncIncremental(t, "阶段1-新增", dir, 1, 0, 0, 5)

	rows := sopnRowsOfID(t, dir, sopnNewID, sopnRel())
	wantCard := "id=" + sopnNewID + "\x1fpath=" + sopnRel() + "\x1fkind=" +
		string(index.CardKindOpinion) + "\x1fvalidation=" + string(index.ValidationPending) +
		"\x1ftitle=" + sopnNewTitle + "\x1fdomain=" + opnDomain
	if len(rows["cards"]) != 1 || rows["cards"][0] != wantCard {
		t.Fatalf("[阶段1-新增] cards 未如实纳管新观点：期望恰一行 %q，实得 %v", wantCard, rows["cards"])
	}
	wantFTS := "id=" + sopnNewID + "\x1fkind=" + string(index.CardKindOpinion) +
		"\x1fvalidation=" + string(index.ValidationPending)
	if len(rows["cards_fts"]) != 1 || rows["cards_fts"][0] != wantFTS {
		t.Fatalf("[阶段1-新增] cards_fts 未如实纳管新观点：期望恰一行 %q，实得 %v",
			wantFTS, rows["cards_fts"])
	}
	wantRel := "src_id=" + sopnNewID + "\x1fverb=supports\x1fdst_id=" + applyCardID +
		"\x1fsrc_path=" + sopnRel()
	if len(rows["relations"]) != 1 || rows["relations"][0] != wantRel {
		t.Fatalf("[阶段1-新增] relations 未如实纳管观点关系：期望恰一行 %q，实得 %v",
			wantRel, rows["relations"])
	}
	if len(rows["files"]) != 1 {
		t.Fatalf("[阶段1-新增] files 未纳管新观点文件，实得 %v", rows["files"])
	}
	if len(rows["skipped"]) != 0 {
		t.Fatalf("[阶段1-新增] 新观点被记成 skipped：%v（它是合法产物，必须进派生行）", rows["skipped"])
	}
	if got := opnFTSMatchIDs(t, dir, sopnAddToken); !opnEqualStrSets(got, []string{sopnNewID}) {
		t.Fatalf("[阶段1-新增] FTS 未按令牌 %q 恰召回 %s，实得 %v", sopnAddToken, sopnNewID, got)
	}
	sopnAssertSectionsMatchDisk(t, "阶段1-新增", dir, sopnNewID)
	if got := len(sopnDerivedSnapshot(t, sopnLive(dir))["cards"]); got != 6 {
		t.Fatalf("[阶段1-新增] 卡行数 = %d，期望 6（基线 5 + 新增 1）", got)
	}
	sopnAssertKnowledgeIntact(t, "阶段1-新增", sopnLive(dir), baseKnow)
	addedHash := sopnFileHash(t, dir, sopnRel())
	sopnAssertDigestEqualsFullRebuild(t, "阶段1-新增", dir)

	// —— 阶段 2：同一条 o-* 换正文令牌 + validation pending → validated ——
	//
	// 这是一次真实的合法状态跃迁（pending → validated，见 index.CardValidations()），
	// 并且**同时**改了正文，于是「判别列更新」与「FTS 命中集迁移」两件事在一次 sync 里一起被检验。
	sopnWriteOpinion(t, dir, string(index.ValidationValidated), sopnModToken,
		"shift opinion to validated")
	sopnSyncIncremental(t, "阶段2-修改", dir, 0, 1, 0, 5)

	rows = sopnRowsOfID(t, dir, sopnNewID, sopnRel())
	wantCard = "id=" + sopnNewID + "\x1fpath=" + sopnRel() + "\x1fkind=" +
		string(index.CardKindOpinion) + "\x1fvalidation=" + string(index.ValidationValidated) +
		"\x1ftitle=" + sopnNewTitle + "\x1fdomain=" + opnDomain
	if len(rows["cards"]) != 1 || rows["cards"][0] != wantCard {
		t.Fatalf("[阶段2-修改] cards.validation 未跟随权威跃迁：期望恰一行 %q，实得 %v",
			wantCard, rows["cards"])
	}
	wantFTS = "id=" + sopnNewID + "\x1fkind=" + string(index.CardKindOpinion) +
		"\x1fvalidation=" + string(index.ValidationValidated)
	if len(rows["cards_fts"]) != 1 || rows["cards_fts"][0] != wantFTS {
		t.Fatalf("[阶段2-修改] cards_fts 的判别列未跟随权威跃迁：期望恰一行 %q，实得 %v",
			wantFTS, rows["cards_fts"])
	}
	if got := opnFTSMatchIDs(t, dir, sopnAddToken); len(got) != 0 {
		t.Fatalf("[阶段2-修改] 旧令牌 %q 仍能召回 %v：旧正文被留在 FTS 里了", sopnAddToken, got)
	}
	if got := opnFTSMatchIDs(t, dir, sopnModToken); !opnEqualStrSets(got, []string{sopnNewID}) {
		t.Fatalf("[阶段2-修改] 新令牌 %q 未恰召回 %s，实得 %v", sopnModToken, sopnNewID, got)
	}
	if got := sopnFileHash(t, dir, sopnRel()); got == addedHash {
		t.Fatalf("[阶段2-修改] files.content_hash 未变（仍为 %s）：水位线没跟上真实改写", got)
	}
	if len(rows["relations"]) != 1 || rows["relations"][0] != wantRel {
		t.Fatalf("[阶段2-修改] relations 与权威不一致：期望恰一行 %q，实得 %v", wantRel, rows["relations"])
	}
	if len(rows["files"]) != 1 {
		t.Fatalf("[阶段2-修改] files 行数异常：%v", rows["files"])
	}
	sopnAssertSectionsMatchDisk(t, "阶段2-修改", dir, sopnNewID)
	if got := len(sopnDerivedSnapshot(t, sopnLive(dir))["cards"]); got != 6 {
		t.Fatalf("[阶段2-修改] 卡行数 = %d，期望仍为 6（修改不该增删行）", got)
	}
	sopnAssertKnowledgeIntact(t, "阶段2-修改", sopnLive(dir), baseKnow)
	sopnAssertDigestEqualsFullRebuild(t, "阶段2-修改", dir)

	// —— 阶段 3：删除该 o-* ——
	sopnDeleteOpinion(t, dir)
	sopnSyncIncremental(t, "阶段3-删除", dir, 0, 0, 1, 5)

	rows = sopnRowsOfID(t, dir, sopnNewID, sopnRel())
	for table, got := range rows {
		if len(got) != 0 {
			t.Fatalf("[阶段3-删除] %s 里仍留有该观点的派生行（孤儿）：%v", table, got)
		}
	}
	for _, tok := range []string{sopnAddToken, sopnModToken} {
		if got := opnFTSMatchIDs(t, dir, tok); len(got) != 0 {
			t.Fatalf("[阶段3-删除] FTS 仍能按令牌 %q 召回 %v：正文残留在检索面", tok, got)
		}
	}
	// 终判：全库派生行**逐字回到基线**（既没有残留，也没有顺手改坏别人）。
	afterDelete := sopnDerivedSnapshot(t, sopnLive(dir))
	if len(afterDelete) != len(baseDerived) {
		t.Fatalf("[阶段3-删除] 派生快照分组数变了：前 %d、后 %d", len(baseDerived), len(afterDelete))
	}
	for table, wantRows := range baseDerived {
		gotRows := afterDelete[table]
		if len(gotRows) != len(wantRows) {
			t.Fatalf("[阶段3-删除] %s 行数未回到基线：基线 %d、实得 %d\n基线=%v\n实得=%v",
				table, len(wantRows), len(gotRows), wantRows, gotRows)
		}
		for i := range wantRows {
			if gotRows[i] != wantRows[i] {
				t.Fatalf("[阶段3-删除] %s 第 %d 行未回到基线：\n基线=%s\n实得=%s",
					table, i, wantRows[i], gotRows[i])
			}
		}
	}
	sopnAssertKnowledgeIntact(t, "阶段3-删除", sopnLive(dir), baseKnow)
	lastDigest := sopnAssertDigestEqualsFullRebuild(t, "阶段3-删除", dir)

	// —— 阶段 4：收尾——真实 `eg index rebuild` 与末态增量结果逐字等价 ——
	//
	// 阶段 1~3 的旁路等价用的是「同一实现的全量构建 + 另一个时钟」（不破坏增量链）；
	// 这里再用**真实 CLI 的整库重建**收一次口：此刻增量链已经走完，覆盖活库无妨。
	if code, _, errOut := runIndexCLI(t, dir, "rebuild"); code != ExitOK {
		t.Fatalf("[阶段4] rebuild 退出码非 0：%s", errOut)
	}
	rebuilt, err := index.Digest(index.DirPath(dir))
	if err != nil {
		t.Fatalf("[阶段4] 重建后索引应可读：%v", err)
	}
	if rebuilt != lastDigest {
		t.Fatalf("[阶段4] 真实 eg index rebuild 与末态增量结果不等价：增量 %s ≠ 重建 %s",
			lastDigest, rebuilt)
	}
	// 再 sync 一次必须是 noop：增量链走完后索引与权威已经完全一致（零写入）。
	code, out, errOut := runIndexCLI(t, dir, "sync")
	if code != ExitOK {
		t.Fatalf("[阶段4] 收尾 sync 退出码 = %d：%s", code, errOut)
	}
	if a := idxString(t, out, "action"); a != string(index.ActionSyncNoop) {
		t.Fatalf("[阶段4] 收尾 sync action = %q，期望 %q（幂等：零写入）", a, index.ActionSyncNoop)
	}
	if again, derr := index.Digest(index.DirPath(dir)); derr != nil {
		t.Fatalf("[阶段4] noop 后索引应仍可读：%v", derr)
	} else if again != rebuilt {
		t.Fatal("[阶段4] noop 分支改动了索引内容：它必须零写入")
	}

	// 语料确实用到了三态之外的既有观点：确认基线那三条 o-* 一条都没被本文件的增删改波及。
	baseOpnIDs := []string{opnPendingID, opnValidatedID, opnRejectedID}
	got := sopnQueryRows(t, sopnLive(dir), `SELECT id FROM `+index.TableCards+
		` WHERE kind = ? ORDER BY id`, string(index.CardKindOpinion))
	var gotIDs []string
	for _, row := range got {
		gotIDs = append(gotIDs, strings.TrimPrefix(row, "id="))
	}
	sort.Strings(baseOpnIDs)
	if !opnEqualStrSets(baseOpnIDs, gotIDs) {
		t.Fatalf("末态观点行集 = %v，期望恰为基线三条 %v", gotIDs, baseOpnIDs)
	}
}
