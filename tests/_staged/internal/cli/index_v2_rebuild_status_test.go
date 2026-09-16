package cli

// T-005-D 收口合同：派生索引 Schema v2 的**最后三格**，全部走真实 vault + 真实 CLI + 真实 SQL。
//
//	① 真 v1 → v2 的**自动整库重建**：盘上先摆一份**真实 v1 形态**的旧 `.index/`
//	   （schema_version=1、`cards` / `cards_fts` 确实没有 v2 的 kind / validation 列），
//	   在一份 clean 的权威 vault 上跑真实 `eg index build`，必须识别
//	   `schema_version_mismatch` 并走既有整库替换策略，结果是一份合法 v2 库；
//	   权威 Markdown 与 Git 状态逐字不变，且**没有任何迁移**（v1 的行不许被搬进新库）。
//	② `eg index status` 的分类计数：`knowledge_count` / `opinion_count` 只从派生表
//	   按 `cards.kind` **现算**，与 SQL、与磁盘 `k-*` / `o-*` 全集精确相等，且
//	   两者之和恒等于 `card_count`；`index_meta` 不得因此长出第 7 个键；
//	   索引缺失 / 结构损坏时这两格必须**缺席**，不许拿 0 冒充可读事实。
//	③ build 与 rebuild 的等价收口：同一份 v2 权威语料上，`cards.kind` 全集、每条观点的
//	   `validation`、三态观点的 FTS 唯一令牌命中集、以及 `Digest` 在「build」与「删掉
//	   `.index/` 后真实 `eg index rebuild`」之间逐字等价；表仍恰 6 张、meta 仍恰 6 键。
//
// 为什么 v1 旧库必须**真的建成 v1 形态**、而不是把 v2 库里的 meta 数字改成 1：
// 后者的 `cards` 依然有 kind / validation 两列，于是「新二进制遇到缺列的旧库」这条真实
// 路径根本没被走到 —— 恰恰是最容易在真实用户机器上炸的那一条。因此本文件用**逐字写死的
// v1 DDL**（十列 cards + 四列 cards_fts）现场建库，并在跑 build 之前用 `PRAGMA table_info`
// 自证「这确实是一份缺列的旧库」。

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/index"
)

// —— ① 真 v1 形态旧库（逐字写死的历史 DDL；**不引用** schema.go 的 v2 真源）——
//
// 这份 DDL 是 IndexSchemaVersion=1 时期的库形态快照：`cards` 恰十列（无 kind / validation）、
// `cards_fts` 恰四列（id / title / body / bigram_text）、无 `cards_kind_idx`。
// 刻意不复用产品的建表函数：一旦复用，v1 就会随 schema.go 一起「进化」成 v2，
// 那么本用例证明的将不再是「新二进制能吃掉真旧库」，而是「新二进制能吃掉自己」。
func idxV1DDL() []string {
	return []string{
		`CREATE TABLE index_meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
)`,
		`CREATE TABLE cards (
  id           TEXT PRIMARY KEY,
  path         TEXT NOT NULL,
  domain       TEXT NOT NULL,
  title        TEXT NOT NULL,
  status       TEXT NOT NULL,
  deprecated   INTEGER NOT NULL,
  deleted      INTEGER NOT NULL,
  replaced_by  TEXT NOT NULL,
  content_hash TEXT NOT NULL,
  mtime_unix   INTEGER NOT NULL
)`,
		`CREATE INDEX cards_replaced_by_idx ON cards(replaced_by)`,
		`CREATE INDEX cards_path_idx ON cards(path)`,
		`CREATE VIRTUAL TABLE cards_fts USING fts5(
  id UNINDEXED, title, body, bigram_text,
  tokenize='trigram'
)`,
		`CREATE TABLE relations (
  src_id   TEXT NOT NULL,
  verb     TEXT NOT NULL,
  dst_id   TEXT NOT NULL,
  src_path TEXT NOT NULL,
  line     INTEGER NOT NULL DEFAULT 0,
  UNIQUE(src_id, verb, dst_id)
)`,
		`CREATE INDEX relations_dst_idx ON relations(dst_id)`,
		`CREATE TABLE files (
  path            TEXT PRIMARY KEY,
  content_hash    TEXT NOT NULL,
  size            INTEGER NOT NULL,
  mtime_unix      INTEGER NOT NULL,
  indexed_at_unix INTEGER NOT NULL
)`,
		`CREATE TABLE skipped (
  path TEXT PRIMARY KEY,
  kind TEXT NOT NULL CHECK (kind IN ('file_changed', 'user_block_unsafe'))
)`,
	}
}

// idxV1SentinelID 是只存在于 v1 旧库里的**哨兵行** id：权威 vault 里没有这个卡文件。
//
// 它的用途是把「整库重建」与「迁移」区分成一个可判定的事实：重建 = 旧库整体作废，
// 哨兵行必然消失；迁移 = 旧行被搬进新库，哨兵行会活下来。
const idxV1SentinelID = "k-19990101-legacy"

// idxWriteV1Database 在 vault 的 `.index/` 里现场造一份**真实 v1 形态**的旧库。
//
// 除 DDL 之外还落了真实数据（一行 cards + 对应 cards_fts + files + relations + 六键 meta，
// schema_version=1 且 card_count 与 cards 实际行数自洽）：一份「空壳」旧库会让
// `card_count` 自洽性、`integrity_check` 这些前置判定失去意义，也无法证明哨兵行确实被丢弃。
func idxWriteV1Database(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(index.DirPath(dir), 0o755); err != nil {
		t.Fatalf("建 %s 失败：%v", index.DirName, err)
	}
	db, err := sql.Open("sqlite", "file:"+index.DBPath(dir))
	if err != nil {
		t.Fatalf("打开 v1 旧库失败：%v", err)
	}
	db.SetMaxOpenConns(1)
	defer func() { _ = db.Close() }()

	stmts := append([]string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA synchronous=NORMAL`,
	}, idxV1DDL()...)
	stmts = append(stmts,
		`INSERT INTO cards (id, path, domain, title, status, deprecated, deleted,
		  replaced_by, content_hash, mtime_unix)
		 VALUES ('`+idxV1SentinelID+`', 'domains/`+opnDomain+`/knowledge/`+idxV1SentinelID+`.md',
		         '`+opnDomain+`', 'v1 时代的旧卡', 'active', 0, 0, '', 'sha256:v1legacy', 900000000)`,
		`INSERT INTO cards_fts (id, title, body, bigram_text)
		 VALUES ('`+idxV1SentinelID+`', 'v1 时代的旧卡', 'v1 正文', ' v1 ')`,
		`INSERT INTO files (path, content_hash, size, mtime_unix, indexed_at_unix)
		 VALUES ('domains/`+opnDomain+`/knowledge/`+idxV1SentinelID+`.md',
		         'sha256:v1legacy', 128, 900000000, 900000000)`,
		`INSERT INTO relations (src_id, verb, dst_id, src_path)
		 VALUES ('`+idxV1SentinelID+`', 'refines', '`+applyCardID+`',
		         'domains/`+opnDomain+`/knowledge/`+idxV1SentinelID+`.md')`,
		`INSERT INTO index_meta (key, value) VALUES
		  ('`+index.MetaSchemaVersion+`', '1'),
		  ('`+index.MetaHead+`', ''),
		  ('`+index.MetaFilesHash+`', 'sha256:v1fileshash'),
		  ('`+index.MetaTokenizerMode+`', '`+index.TokenizerTrigram+`'),
		  ('`+index.MetaBuiltAtUnix+`', '900000000'),
		  ('`+index.MetaCardCount+`', '1')`,
		`PRAGMA wal_checkpoint(TRUNCATE)`,
	)
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("建 v1 旧库失败（%s）：%v", strings.SplitN(s, "\n", 2)[0], err)
		}
	}
}

// —— 只读 SQL 脚手架（本文件专用；与 opn* 系列共库不同函数，避免改动既有断言面）——

// idxTableColumns 返回某张表的列名（`PRAGMA table_info` 的声明序）。
func idxTableColumns(t *testing.T, dir, table string) []string {
	t.Helper()
	db := opnOpenDB(t, dir)
	defer func() { _ = db.Close() }()
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatalf("读 %s 列信息失败：%v", table, err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var (
			cid         int
			name, typ   string
			notNull, pk int
			dflt        sql.NullString
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scan %s 列信息失败：%v", table, err)
		}
		out = append(out, name)
	}
	return out
}

// idxHasColumn 判某张表有没有某一列。
func idxHasColumn(t *testing.T, dir, table, col string) bool {
	t.Helper()
	for _, c := range idxTableColumns(t, dir, table) {
		if c == col {
			return true
		}
	}
	return false
}

// idxMetaKeysOnDisk 返回 `index_meta` 磁盘上的键集合（升序）。
func idxMetaKeysOnDisk(t *testing.T, dir string) []string {
	t.Helper()
	db := opnOpenDB(t, dir)
	defer func() { _ = db.Close() }()
	rows, err := db.Query(`SELECT key FROM ` + index.TableIndexMeta + ` ORDER BY key`)
	if err != nil {
		t.Fatalf("读 index_meta 键集合失败：%v", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatalf("scan index_meta.key 失败：%v", err)
		}
		out = append(out, k)
	}
	return out
}

// idxMetaValueOnDisk 取 `index_meta` 某个键的原值。
func idxMetaValueOnDisk(t *testing.T, dir, key string) string {
	t.Helper()
	db := opnOpenDB(t, dir)
	defer func() { _ = db.Close() }()
	var v string
	if err := db.QueryRow(`SELECT value FROM `+index.TableIndexMeta+` WHERE key = ?`, key).
		Scan(&v); err != nil {
		t.Fatalf("读 index_meta[%s] 失败：%v", key, err)
	}
	return v
}

// idxCountSQL 数一条 count 查询（本文件的分类计数一律以 SQL 现算为真值来源）。
func idxCountSQL(t *testing.T, dir, query string, args ...interface{}) int {
	t.Helper()
	db := opnOpenDB(t, dir)
	defer func() { _ = db.Close() }()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("计数失败（%s）：%v", query, err)
	}
	return n
}

// idxCardIDsExist 判某个 id 在 cards / cards_fts 里还有没有行（重建 vs 迁移的判据）。
func idxCardIDsExist(t *testing.T, dir, id string) (bool, bool) {
	t.Helper()
	cards := idxCountSQL(t, dir, `SELECT count(*) FROM `+index.TableCards+` WHERE id = ?`, id)
	fts := idxCountSQL(t, dir, `SELECT count(*) FROM `+index.TableCardsFTS+` WHERE id = ?`, id)
	return cards > 0, fts > 0
}

// —— JSON / 人读输出脚手架 ——

// idxDataIndex 把 `data.index` 解析成 map：本文件要断言**某些键必须缺席**
// （索引缺失 / 损坏时不许拿 0 冒充可读事实），而 idxString 取不到键会直接 Fatal。
func idxDataIndex(t *testing.T, out string) map[string]interface{} {
	t.Helper()
	raw := rcRawAt(t, []byte(out), "data", "index")
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("data.index 不是 JSON 对象：%v\n%s", err, raw)
	}
	return m
}

// idxIntKey 取 `data.index.<key>` 的整数值（缺键 / 非数字都当场红）。
func idxIntKey(t *testing.T, out, key string) int {
	t.Helper()
	m := idxDataIndex(t, out)
	v, ok := m[key]
	if !ok {
		t.Fatalf("data.index 缺键 %q（分类计数必须是机器可读的一格）", key)
	}
	f, ok := v.(float64)
	if !ok {
		t.Fatalf("data.index.%s = %#v，期望数字", key, v)
	}
	if f != float64(int(f)) {
		t.Fatalf("data.index.%s = %v，期望整数", key, f)
	}
	return int(f)
}

// idxAssertKeysAbsent 断言 `data.index` 里**没有**这些键。
func idxAssertKeysAbsent(t *testing.T, out string, keys ...string) {
	t.Helper()
	m := idxDataIndex(t, out)
	for _, k := range keys {
		if v, ok := m[k]; ok {
			t.Fatalf("data.index 出现了不该有的 %q = %#v："+
				"索引不可读时必须缺席这一格，不许拿 0 冒充可读事实", k, v)
		}
	}
}

// idxRunHuman 跑一次**人读**（无 --json）`eg index <sub>`，返回 stdout。
//
// 存在理由：机器输出与 human summary 必须同源同事实。JSON 信封里没有 summary 这一格
// （它只在人读渲染里出现），因此「human summary 是否同步显示分类计数」只能这样验。
func idxRunHuman(t *testing.T, dir, sub string, extra ...string) (int, string, string) {
	t.Helper()
	setGitIdentity(t)
	r := newTestRoot(t, dir)
	r.In = closedStdin{}
	r.Now = func() time.Time { return stampAt(t, idxFixedNow) }
	args := append([]string{"index", sub, "--vault", dir}, extra...)
	return runCLI(t, r, args...)
}

// idxAssertIndexDirClean 断言 `.index/` 里只有允许的索引产物与 M6 运行时保留条目 ——
// 「没有迁移脚本 / 迁移中间产物」的机器形态（迁移一旦存在，几乎必然落下 `.bak` / `.old`
// / 半迁移库之类的第三种条目）。
func idxAssertIndexDirClean(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(index.DirPath(dir))
	if err != nil {
		t.Fatalf("读 %s 失败：%v", index.DirName, err)
	}
	allowed := idxIndexArtifactNames()
	reserved := idxRuntimeReservedNames()
	for _, e := range entries {
		if allowed[e.Name()] || reserved[e.Name()] {
			continue
		}
		t.Fatalf("%s/ 下出现第三种条目 %q：整库重建不留迁移中间产物"+
			"（允许集合恰 %v + 运行时保留条目 %v）",
			index.DirName, e.Name(), index.AllowedFiles(), index.RuntimeReservedEntries())
	}
}

// —— ① 真 v1 → v2 自动整库重建 ——

// TestIndexBuildRebuildsRealV1Database 钉住「不兼容 = 整库重建，永不迁移」这条合同在
// **真实旧库**上的兑现：v1 形态旧 `.index/` + clean 权威 vault（k-* 与 o-* 齐全）
// → 真实 `eg index build` → 一份合法 v2 库，且权威零改动、无迁移。
func TestIndexBuildRebuildsRealV1Database(t *testing.T) {
	dir := idxVaultWithOpinions(t)
	idxWriteV1Database(t, dir)

	// —— 前置自证：这确实是一份**真实 v1 形态**的旧库，不是「v2 库改了个数字」——
	if got := idxMetaValueOnDisk(t, dir, index.MetaSchemaVersion); got != "1" {
		t.Fatalf("前置：旧库 schema_version = %q，期望 %q", got, "1")
	}
	for _, col := range []string{"kind", "validation"} {
		if idxHasColumn(t, dir, index.TableCards, col) {
			t.Fatalf("前置：v1 旧库的 %s 表不得有 v2 的 %s 列（否则没走到「缺列旧库」这条路径）",
				index.TableCards, col)
		}
		if idxHasColumn(t, dir, index.TableCardsFTS, col) {
			t.Fatalf("前置：v1 旧库的 %s 表不得有 v2 的 %s 列", index.TableCardsFTS, col)
		}
	}
	if cols := idxTableColumns(t, dir, index.TableCards); len(cols) != 10 {
		t.Fatalf("前置：v1 的 cards 恰十列，实得 %d 列 %v", len(cols), cols)
	}
	if cols := idxTableColumns(t, dir, index.TableCardsFTS); len(cols) != 4 {
		t.Fatalf("前置：v1 的 cards_fts 恰四列，实得 %d 列 %v", len(cols), cols)
	}
	// 识别口径：机器可读的子因必须是既有的 schema_version_mismatch（不是 open_failed
	// 之类的兜底，也不是新造的码）。
	before := index.Inspect(index.DirPath(dir))
	if before.Health != index.HealthCorrupt || before.Code != index.CodeIndexCorrupt ||
		before.Reason != index.ReasonSchemaVersionMismatch {
		t.Fatalf("v1 旧库应被识别为 %s / %s / %s，实得 %s / %s / %s（%s）",
			index.HealthCorrupt, index.CodeIndexCorrupt, index.ReasonSchemaVersionMismatch,
			before.Health, before.Code, before.Reason, before.Message)
	}

	authBefore := idxAuthoritySnapshot(t, dir)
	gitStatusBefore := gitOut(t, dir, "status", "--porcelain")
	gitHeadBefore := gitOut(t, dir, "rev-parse", "HEAD")
	commitsBefore := gitOut(t, dir, "rev-list", "--count", "HEAD")

	// —— 真实 `eg index build`：走既有整库替换策略（EnsureBuilt 的第三支）——
	code, out, errOut := runIndexCLI(t, dir, "build")
	if code != ExitOK {
		t.Fatalf("build 退出码 = %d：%s", code, errOut)
	}
	// action 取**既有精确字面**：不可用索引经 build 修复的动作恒为 ActionRepaired，
	// 它就是「整库替换」在动作枚举里的那一格（用户显式 rebuild 才是 ActionRebuilt）。
	if got := idxString(t, out, "action"); got != string(index.ActionRepaired) {
		t.Fatalf("action = %q，期望既有字面 %q（不兼容旧库经 build 整库重建）",
			got, index.ActionRepaired)
	}
	// 留痕：原本坏在哪必须如实说出来，且用的是既有 W24。
	if codes := idxWarnCodes(t, out); len(codes) == 0 {
		t.Fatal("build 修掉一份不兼容旧库必须留痕（W24），实得零 warning")
	} else {
		found := false
		for _, c := range codes {
			if c == index.CodeIndexCorrupt {
				found = true
			}
		}
		if !found {
			t.Fatalf("warnings = %v，期望含既有 %s", codes, index.CodeIndexCorrupt)
		}
	}

	// —— 结果是一份合法 v2 库 ——
	if got := idxMetaValueOnDisk(t, dir, index.MetaSchemaVersion); got != strconv.Itoa(index.IndexSchemaVersion) {
		t.Fatalf("重建后 schema_version = %q，期望 %d", got, index.IndexSchemaVersion)
	}
	for _, col := range []string{"kind", "validation"} {
		if !idxHasColumn(t, dir, index.TableCards, col) {
			t.Fatalf("重建后 %s 必须有 v2 的 %s 列", index.TableCards, col)
		}
		if !idxHasColumn(t, dir, index.TableCardsFTS, col) {
			t.Fatalf("重建后 %s 必须有 v2 的 %s 列", index.TableCardsFTS, col)
		}
	}
	if n := opnTableCount(t, dir); n != len(index.TableNames()) {
		t.Fatalf("重建后表数 = %d，期望恰 %d 张", n, len(index.TableNames()))
	}
	if got, want := idxMetaKeysOnDisk(t, dir), opnSortedCopy(index.MetaKeys()); !opnEqualStrSets(got, want) {
		t.Fatalf("重建后 index_meta 键集合 = %v，期望恰 %v（不新增第 7 键）", got, want)
	}

	// 行集 == 磁盘全集（v2 语料：k-* 与 o-* 各归其位）。
	wantKnow := diskMarkdownPaths(t, dir, opnDomain, "knowledge")
	if got := opnPathsByKind(t, dir, index.CardKindKnowledge); !opnEqualStrSets(got, wantKnow) {
		t.Fatalf("重建后 knowledge 行集 = %v，期望磁盘 %v", got, wantKnow)
	}
	wantOpn := diskMarkdownPaths(t, dir, opnDomain, "opinions")
	if got := opnPathsByKind(t, dir, index.CardKindOpinion); !opnEqualStrSets(got, wantOpn) {
		t.Fatalf("重建后 opinion 行集 = %v，期望磁盘 %v", got, wantOpn)
	}

	// —— 重建 ≠ 迁移：v1 哨兵行必须**不在**新库里（cards 与 cards_fts 都不许留）——
	inCards, inFTS := idxCardIDsExist(t, dir, idxV1SentinelID)
	if inCards || inFTS {
		t.Fatalf("v1 哨兵行 %s 活到了新库里（cards=%v / cards_fts=%v）："+
			"合同是整库重建、永不迁移，旧行必须随旧库一并作废", idxV1SentinelID, inCards, inFTS)
	}
	if n := idxCountSQL(t, dir, `SELECT count(*) FROM `+index.TableFiles+` WHERE path = ?`,
		"domains/"+opnDomain+"/knowledge/"+idxV1SentinelID+".md"); n != 0 {
		t.Fatalf("v1 时代的 files 行还在（%d 行）：整库重建后水位线必须完全来自本次全量扫描", n)
	}
	if n := idxCountSQL(t, dir, `SELECT count(*) FROM `+index.TableRelations+` WHERE src_id = ?`,
		idxV1SentinelID); n != 0 {
		t.Fatalf("v1 时代的 relations 行还在（%d 行）", n)
	}
	idxAssertIndexDirClean(t, dir)

	// —— 权威 Markdown / Git 状态逐字不变 ——
	idxAssertAuthorityUnchanged(t, dir, authBefore)
	if got := gitOut(t, dir, "status", "--porcelain"); got != gitStatusBefore {
		t.Fatalf("工作区变了：前 %q → 后 %q（索引只许写 %s/）",
			gitStatusBefore, got, index.DirName)
	}
	if got := gitOut(t, dir, "rev-parse", "HEAD"); got != gitHeadBefore {
		t.Fatalf("HEAD 变了：前 %q → 后 %q（索引恒零 commit）", gitHeadBefore, got)
	}
	if got := gitOut(t, dir, "rev-list", "--count", "HEAD"); got != commitsBefore {
		t.Fatalf("commit 数变了：前 %q → 后 %q", commitsBefore, got)
	}

	// —— 重建完的库在 status 上必须是 healthy/fresh（自动重建的终点不是「还得再修一次」）——
	code, out, errOut = runIndexCLI(t, dir, "status")
	if code != ExitOK {
		t.Fatalf("status 退出码 = %d：%s", code, errOut)
	}
	if h, f := idxHealth(t, out), idxString(t, out, "freshness"); h != string(index.HealthHealthy) ||
		f != string(index.FreshnessFresh) {
		t.Fatalf("重建后应 healthy/fresh，实得 %s / %s", h, f)
	}
}

// —— ② status 分类计数 ——

// TestIndexStatusReportsKindCountsFromDerivedTables 钉住分类计数的四件事：
//
//	① 机器输出有稳定键 knowledge_count / opinion_count；
//	② 两个数与 SQL 现算、与磁盘 k-* / o-* 全集**精确相等**，且之和 == card_count；
//	③ human summary 同步显示同样两个数（两侧同源同事实）；
//	④ `index_meta` 仍恰 6 键（分类计数是**现算**，不落第 7 个 key），且 status 仍只读、恒退 0。
func TestIndexStatusReportsKindCountsFromDerivedTables(t *testing.T) {
	dir := idxVaultWithOpinions(t)
	if code, _, errOut := runIndexCLI(t, dir, "build"); code != ExitOK {
		t.Fatalf("前置 build 退出码非 0：%s", errOut)
	}

	digestBefore, err := index.Digest(index.DirPath(dir))
	if err != nil {
		t.Fatalf("Digest 读取失败：%v", err)
	}

	code, out, errOut := runIndexCLI(t, dir, "status")
	if code != ExitOK {
		t.Fatalf("status 退出码 = %d：%s", code, errOut)
	}

	// ② 三方对账：JSON ↔ SQL 现算 ↔ 磁盘文件全集。
	gotKnow := idxIntKey(t, out, "knowledge_count")
	gotOpn := idxIntKey(t, out, "opinion_count")
	sqlKnow := idxCountSQL(t, dir, `SELECT count(*) FROM `+index.TableCards+` WHERE kind = ?`,
		index.CardKindKnowledge)
	sqlOpn := idxCountSQL(t, dir, `SELECT count(*) FROM `+index.TableCards+` WHERE kind = ?`,
		index.CardKindOpinion)
	diskKnow := len(diskMarkdownPaths(t, dir, opnDomain, "knowledge"))
	diskOpn := len(diskMarkdownPaths(t, dir, opnDomain, "opinions"))
	if diskKnow == 0 || diskOpn == 0 {
		t.Fatalf("前置：磁盘语料必须同时含 k-* 与 o-*，实得 %d / %d", diskKnow, diskOpn)
	}
	if gotKnow != sqlKnow || gotKnow != diskKnow {
		t.Fatalf("knowledge_count：status=%d、SQL=%d、磁盘=%d，三者必须精确相等",
			gotKnow, sqlKnow, diskKnow)
	}
	if gotOpn != sqlOpn || gotOpn != diskOpn {
		t.Fatalf("opinion_count：status=%d、SQL=%d、磁盘=%d，三者必须精确相等",
			gotOpn, sqlOpn, diskOpn)
	}
	cardCount := idxIntKey(t, out, "card_count")
	if gotKnow+gotOpn != cardCount {
		t.Fatalf("knowledge_count(%d) + opinion_count(%d) = %d ≠ card_count(%d)："+
			"分类计数必须是 card_count 的一次完整划分", gotKnow, gotOpn, gotKnow+gotOpn, cardCount)
	}
	if total := idxCountSQL(t, dir, `SELECT count(*) FROM `+index.TableCards); total != cardCount {
		t.Fatalf("card_count = %d，但 cards 实际 %d 行", cardCount, total)
	}

	// ③ human summary 同步显示（同一次事实的两种呈现，不许只喂机器）。
	hCode, hOut, hErr := idxRunHuman(t, dir, "status")
	if hCode != ExitOK {
		t.Fatalf("人读 status 退出码 = %d：%s", hCode, hErr)
	}
	for _, want := range []string{
		"knowledge_count=" + strconv.Itoa(gotKnow),
		"opinion_count=" + strconv.Itoa(gotOpn),
	} {
		if !strings.Contains(hOut, want) {
			t.Fatalf("human summary 未同步显示 %q：\n%s", want, hOut)
		}
	}

	// ④ 不新增第 7 个 meta 键；status 只读（Digest 不变）。
	if got, want := idxMetaKeysOnDisk(t, dir), opnSortedCopy(index.MetaKeys()); !opnEqualStrSets(got, want) {
		t.Fatalf("index_meta 键集合 = %v，期望恰 %v —— 分类计数只许从派生表现算", got, want)
	}
	digestAfter, err := index.Digest(index.DirPath(dir))
	if err != nil {
		t.Fatalf("status 之后 Digest 读取失败：%v", err)
	}
	if digestBefore != digestAfter {
		t.Fatal("status 改动了索引：它必须只读")
	}
}

// TestIndexStatusKindCountsAbsentWhenIndexUnreadable 钉住反面：索引**缺失**或**结构损坏**时，
// 分类计数这两格必须**缺席** —— 0 是一个可读事实的断言，而此刻根本读不到派生表；
// 用 0 顶上会让调用方以为「库里确实一条卡都没有」。status 仍恒退 0（体检是诊断不是失败）。
func TestIndexStatusKindCountsAbsentWhenIndexUnreadable(t *testing.T) {
	t.Run("索引缺失（从未 build）", func(t *testing.T) {
		dir := idxVaultWithOpinions(t)
		code, out, errOut := runIndexCLI(t, dir, "status")
		if code != ExitOK {
			t.Fatalf("status 必须恒退 0，实得 %d：%s", code, errOut)
		}
		if h := idxHealth(t, out); h != string(index.HealthMissing) {
			t.Fatalf("health = %q，期望 %q", h, index.HealthMissing)
		}
		idxAssertKeysAbsent(t, out, "knowledge_count", "opinion_count", "card_count")
	})

	t.Run("库文件被截断（结构损坏）", func(t *testing.T) {
		dir := idxVaultWithOpinions(t)
		if code, _, errOut := runIndexCLI(t, dir, "build"); code != ExitOK {
			t.Fatalf("前置 build 退出码非 0：%s", errOut)
		}
		// 真字节损坏：把库文件截成一段非法头（不是打桩让 Inspect 返回 corrupt）。
		if err := os.WriteFile(index.DBPath(dir), []byte("not a sqlite file"), 0o644); err != nil {
			t.Fatalf("注入截断损坏失败：%v", err)
		}
		for _, side := range []string{index.DBFileName + "-wal", index.DBFileName + "-shm"} {
			_ = os.Remove(filepath.Join(index.DirPath(dir), side))
		}
		code, out, errOut := runIndexCLI(t, dir, "status")
		if code != ExitOK {
			t.Fatalf("status 必须恒退 0，实得 %d：%s", code, errOut)
		}
		if h := idxHealth(t, out); h != string(index.HealthCorrupt) {
			t.Fatalf("health = %q，期望 %q", h, index.HealthCorrupt)
		}
		idxAssertKeysAbsent(t, out, "knowledge_count", "opinion_count")
	})
}

// —— ③ build / rebuild 等价收口 ——

// idxV2Facts 是一份 v2 库的**判别事实全景**：分型全集、每条观点的 validation、
// 三态观点的 FTS 唯一令牌命中集、Digest、表数与 meta 键集合。
//
// 「等价」在本文件里就是这份结构体逐字相等 —— 不靠 Digest 一个数字自证：Digest 相等
// 已经很强，但它是一个哈希，红的时候说不出差在哪；把语义面一并抓下来，出错时能直接定位。
type idxV2Facts struct {
	knowledgeIDs []string
	opinionIDs   []string
	validation   map[string]string
	ftsHits      map[string][]string
	digest       string
	tableCount   int
	metaKeys     []string
}

func idxCollectV2Facts(t *testing.T, dir string) idxV2Facts {
	t.Helper()
	facts, _ := opnCardRowsByID(t, dir)
	digest, err := index.Digest(index.DirPath(dir))
	if err != nil {
		t.Fatalf("Digest 读取失败：%v", err)
	}
	val := map[string]string{}
	for id, f := range facts {
		val[id] = f.validation
	}
	hits := map[string][]string{}
	for _, s := range opnSeeds() {
		hits[s.token] = opnFTSMatchIDs(t, dir, s.token)
	}
	return idxV2Facts{
		knowledgeIDs: opnIDsOfKind(facts, index.CardKindKnowledge),
		opinionIDs:   opnIDsOfKind(facts, index.CardKindOpinion),
		validation:   val,
		ftsHits:      hits,
		digest:       digest,
		tableCount:   opnTableCount(t, dir),
		metaKeys:     idxMetaKeysOnDisk(t, dir),
	}
}

// TestIndexBuildRebuildEquivalentForV2Corpus 钉住 build 与「删掉 `.index/` 后真实
// `eg index rebuild`」在同一份 v2 权威语料上的逐字等价。
func TestIndexBuildRebuildEquivalentForV2Corpus(t *testing.T) {
	dir := idxVaultWithOpinions(t)
	if code, _, errOut := runIndexCLI(t, dir, "build"); code != ExitOK {
		t.Fatalf("build 退出码非 0：%s", errOut)
	}
	built := idxCollectV2Facts(t, dir)

	// 自证语料强度：分型两侧都非空、三条观点的 validation 三态齐全、每个令牌恰命中一行。
	if len(built.knowledgeIDs) == 0 || len(built.opinionIDs) != len(opnSeeds()) {
		t.Fatalf("前置语料太弱：knowledge %v / opinion %v", built.knowledgeIDs, built.opinionIDs)
	}
	seenValidation := map[string]bool{}
	for _, id := range built.opinionIDs {
		seenValidation[built.validation[id]] = true
	}
	if len(seenValidation) != len(index.CardValidations()) {
		t.Fatalf("前置语料未覆盖全部三态观点，实得 %v", seenValidation)
	}
	for token, ids := range built.ftsHits {
		if len(ids) != 1 {
			t.Fatalf("令牌 %q 应恰命中一行观点，实得 %v", token, ids)
		}
	}

	// —— 删掉 `.index/` 之后跑**真实** rebuild（不是在原库上重建：那样证不到「从零重来」）——
	if err := os.RemoveAll(index.DirPath(dir)); err != nil {
		t.Fatalf("删除 %s 失败：%v", index.DirName, err)
	}
	code, out, errOut := runIndexCLI(t, dir, "rebuild")
	if code != ExitOK {
		t.Fatalf("rebuild 退出码 = %d：%s", code, errOut)
	}
	if got := idxString(t, out, "action"); got != string(index.ActionRebuilt) {
		t.Fatalf("action = %q，期望 %q", got, index.ActionRebuilt)
	}
	rebuilt := idxCollectV2Facts(t, dir)

	// —— 逐项等价 ——
	if !opnEqualStrSets(built.knowledgeIDs, rebuilt.knowledgeIDs) {
		t.Fatalf("cards.kind=knowledge 全集不等：build %v vs rebuild %v",
			built.knowledgeIDs, rebuilt.knowledgeIDs)
	}
	if !opnEqualStrSets(built.opinionIDs, rebuilt.opinionIDs) {
		t.Fatalf("cards.kind=opinion 全集不等：build %v vs rebuild %v",
			built.opinionIDs, rebuilt.opinionIDs)
	}
	if len(built.validation) != len(rebuilt.validation) {
		t.Fatalf("validation 映射规模不等：build %d vs rebuild %d",
			len(built.validation), len(rebuilt.validation))
	}
	for id, want := range built.validation {
		if got, ok := rebuilt.validation[id]; !ok || got != want {
			t.Fatalf("validation[%s]：build %q vs rebuild %q（present=%v）", id, want, got, ok)
		}
	}
	if len(built.ftsHits) != len(rebuilt.ftsHits) {
		t.Fatalf("FTS 令牌数不等：build %d vs rebuild %d", len(built.ftsHits), len(rebuilt.ftsHits))
	}
	for token, want := range built.ftsHits {
		got, ok := rebuilt.ftsHits[token]
		if !ok {
			t.Fatalf("rebuild 后令牌 %q 不在命中集里", token)
		}
		if strings.Join(opnSortedCopy(got), ",") != strings.Join(opnSortedCopy(want), ",") {
			t.Fatalf("令牌 %q 命中集不等：build %v vs rebuild %v", token, want, got)
		}
	}
	if built.digest != rebuilt.digest {
		t.Fatalf("Digest 不等：build %s vs rebuild %s（合同 §4.4 要求逐字等价）",
			built.digest, rebuilt.digest)
	}
	if rebuilt.tableCount != len(index.TableNames()) || built.tableCount != rebuilt.tableCount {
		t.Fatalf("表数：build %d、rebuild %d，期望恒 %d 张",
			built.tableCount, rebuilt.tableCount, len(index.TableNames()))
	}
	if !opnEqualStrSets(rebuilt.metaKeys, opnSortedCopy(index.MetaKeys())) ||
		!opnEqualStrSets(built.metaKeys, rebuilt.metaKeys) {
		t.Fatalf("index_meta 键集合：build %v、rebuild %v，期望恰 %v",
			built.metaKeys, rebuilt.metaKeys, index.MetaKeys())
	}

	// status 的分类计数在两侧同样相等（③ 与 ② 的接缝：等价不止于库字节，也包括对外交代）。
	sort.Strings(built.opinionIDs)
	code, out, errOut = runIndexCLI(t, dir, "status")
	if code != ExitOK {
		t.Fatalf("status 退出码 = %d：%s", code, errOut)
	}
	if got := idxIntKey(t, out, "knowledge_count"); got != len(built.knowledgeIDs) {
		t.Fatalf("rebuild 后 knowledge_count = %d，期望 %d", got, len(built.knowledgeIDs))
	}
	if got := idxIntKey(t, out, "opinion_count"); got != len(built.opinionIDs) {
		t.Fatalf("rebuild 后 opinion_count = %d，期望 %d", got, len(built.opinionIDs))
	}
}
