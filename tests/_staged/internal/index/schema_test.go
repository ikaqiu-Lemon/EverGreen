package index_test

// T-…-065 的机器判据（其一）：Schema 与版本常量的**封闭性**。
//
// 判据来源：M5 索引架构合同 `2026-12-19-m5-index-architecture-contract.md`
// §4.1（表集合恰 6 张）/ §4.2（index_meta 恰 6 键）/ §4.3（IndexSchemaVersion 语义）/
// §2.6（三档分词，主路 trigram）。
//
// 这些断言刻意写成「逐字反证」而不是「包含即通过」：封闭集合的价值全在**不能多**。
// 多一张表 / 多一个键都意味着下游（066 的 sync、067 的读路径、M6 的强校验）要多消费一个
// 未被合同承诺的事实，那类漂移只有在这里当场失败才拦得住。

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/ikaqiu-Lemon/EverGreen/internal/index"
)

// TestSchemaVersionConstant 钉住版本常量本体与它的**语义唯一性**。
//
// M5 首版取 1（合同 §4.3）。这条断言不是形式主义：schema_version 的唯一处置是
// 「不匹配 ⇒ 整库重建」，一旦有人顺手 +1 而不改 rebuild 语义，旧库会被判 corrupt
// 而用户拿不到任何迁移路径 —— 所以改动必须显式落在合同上，先在这里失败。
func TestSchemaVersionConstant(t *testing.T) {
	if index.IndexSchemaVersion != 1 {
		t.Fatalf("IndexSchemaVersion = %d，M5 首版应为 1（改版必须同步改合同 §4.3）",
			index.IndexSchemaVersion)
	}
	// 版本必须真的落进库里（而不是只活在 Go 常量里）：否则「不匹配即重建」无从判定。
	dir := buildFixture(t, sampleSnapshot())
	meta, err := index.ReadMeta(dir)
	if err != nil {
		t.Fatalf("ReadMeta 失败：%v", err)
	}
	if meta.SchemaVersion != index.IndexSchemaVersion {
		t.Fatalf("index_meta.schema_version = %d，期望 %d", meta.SchemaVersion, index.IndexSchemaVersion)
	}
}

// TestSchemaTablesClosed 反证表集合恰 6 张：过滤 FTS5 影子表与 sqlite_ 内部表后，
// `sqlite_master` 里的表 / 视图名集合必须与 TableNames() **逐字相等**（不多不少）。
func TestSchemaTablesClosed(t *testing.T) {
	want := index.TableNames()
	if len(want) != 6 {
		t.Fatalf("TableNames() 有 %d 张，合同 §4.1 规定恰 6 张", len(want))
	}
	dir := buildFixture(t, sampleSnapshot())

	db := openFixture(t, dir, true)
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type IN ('table','view') ORDER BY name`)
	if err != nil {
		t.Fatalf("查 sqlite_master 失败：%v", err)
	}
	defer func() { _ = rows.Close() }()

	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("扫描表名失败：%v", err)
		}
		if strings.HasPrefix(name, index.FTSShadowPrefix) || strings.HasPrefix(name, "sqlite_") {
			continue // FTS5 影子表是虚表的实现细节，不属对外表集合
		}
		got = append(got, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("遍历表名失败：%v", err)
	}
	if !equalSet(got, want) {
		t.Fatalf("表集合不封闭：\n实际 = %v\n期望 = %v", got, want)
	}
}

// TestMetaKeysClosed 反证 index_meta 键集合恰 6 键：多第 7 键即失败。
//
// 「不能多」在这里尤其关键：index_meta 是水位线的唯一落点，任何额外键都会被下游误当作
// 可依赖事实（合同 §4.2 明确禁止新增键；需要新事实就升 schema_version 并重建）。
func TestMetaKeysClosed(t *testing.T) {
	want := index.MetaKeys()
	if len(want) != 6 {
		t.Fatalf("MetaKeys() 有 %d 键，合同 §4.2 规定恰 6 键", len(want))
	}
	dir := buildFixture(t, sampleSnapshot())

	db := openFixture(t, dir, true)
	rows, err := db.Query(`SELECT key FROM ` + index.TableIndexMeta + ` ORDER BY key`)
	if err != nil {
		t.Fatalf("查 index_meta 失败：%v", err)
	}
	defer func() { _ = rows.Close() }()

	var got []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatalf("扫描键名失败：%v", err)
		}
		got = append(got, k)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("遍历键名失败：%v", err)
	}
	if !equalSet(got, want) {
		t.Fatalf("index_meta 键集合不封闭：\n实际 = %v\n期望 = %v", got, want)
	}
}

// TestFTS5VirtualTableCreated 反证主路 D0 真的生效：`cards_fts` 是 FTS5 **虚表**，
// tokenize 恰为 `trigram`，四列固定（id UNINDEXED / title / body / bigram_text），
// 且落库的 `tokenizer_mode` 与建表形态一致。
//
// 这条断言同时是「纯 Go 驱动带 FTS5」的运行期证据（合同 A-41 / A-42 的前提）：
// 如果驱动构建标签丢了 FTS5，这里会退到 like_scan 而当场失败，而不是等到读路径才发现。
func TestFTS5VirtualTableCreated(t *testing.T) {
	dir := buildFixture(t, sampleSnapshot())

	meta, err := index.ReadMeta(dir)
	if err != nil {
		t.Fatalf("ReadMeta 失败：%v", err)
	}
	if meta.TokenizerMode != index.TokenizerTrigram {
		t.Fatalf("tokenizer_mode = %q，本环境应能建 trigram 主路（降级档位只应在 FTS5 缺失时出现）",
			meta.TokenizerMode)
	}

	db := openFixture(t, dir, true)
	var ddl string
	if err := db.QueryRow(
		`SELECT sql FROM sqlite_master WHERE name = ?`, index.TableCardsFTS).Scan(&ddl); err != nil {
		t.Fatalf("读 %s 的 DDL 失败：%v", index.TableCardsFTS, err)
	}
	lower := strings.ToLower(ddl)
	for _, must := range []string{"virtual table", "fts5", "tokenize='trigram'",
		"id unindexed", "title", "body", "bigram_text"} {
		if !strings.Contains(lower, must) {
			t.Fatalf("%s 的 DDL 缺少 %q：\n%s", index.TableCardsFTS, must, ddl)
		}
	}

	// 虚表可用性的最小正向证据：MATCH 查询能命中（读路径实现属 067，这里只证形态可查）。
	var hits int
	if err := db.QueryRow(`SELECT count(*) FROM `+index.TableCardsFTS+
		` WHERE `+index.TableCardsFTS+` MATCH ?`, "attention").Scan(&hits); err != nil {
		t.Fatalf("FTS5 MATCH 查询失败：%v", err)
	}
	if hits == 0 {
		t.Fatalf("FTS5 MATCH 'attention' 命中 0 行，虚表未真正索引正文")
	}
}

// TestTokenizerModesClosed 钉住三档分词取值域（D0 / D1 / D2 各一，D0b 是 D0 的同表补路，
// D3「放弃索引」不落库）。
func TestTokenizerModesClosed(t *testing.T) {
	want := []string{index.TokenizerTrigram, index.TokenizerUnicode61Bigram, index.TokenizerLikeScan}
	if got := index.TokenizerModes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("TokenizerModes() = %v，期望 %v", got, want)
	}
}

// TestSkippedKindsClosed 钉住 skipped.kind 恰 2 值，且与 M2 既有字面量逐字相同；
// 同时反证 CHECK 约束真的在库上（第 3 值必须被 SQLite 拒绝，而不是只靠 Go 侧校验）。
func TestSkippedKindsClosed(t *testing.T) {
	want := []string{"file_changed", "user_block_unsafe"}
	if got := index.SkippedKinds(); !reflect.DeepEqual(got, want) {
		t.Fatalf("SkippedKinds() = %v，期望与 M2 逐字相同的 %v", got, want)
	}
	dir := buildFixture(t, sampleSnapshot())

	db := openFixture(t, dir, false)
	if _, err := db.Exec(`INSERT INTO ` + index.TableSkipped +
		`(path, kind) VALUES('x.md', 'brand_new_kind')`); err == nil {
		t.Fatalf("库上应有 CHECK 约束拒绝第 3 种 kind，实际插入成功")
	}
	// 两个合法值必须都能写进去（CHECK 不能收得比取值域更紧）。
	for i, k := range want {
		if _, err := db.Exec(`INSERT INTO `+index.TableSkipped+`(path, kind) VALUES(?, ?)`,
			"ok-"+k+".md", k); err != nil {
			t.Fatalf("第 %d 个合法 kind %q 被拒：%v", i, k, err)
		}
	}
}

// TestAllowedFilesClosed 钉住 `.index/` 下允许存在的文件名恰 3 个（合同 §3.2）。
func TestAllowedFilesClosed(t *testing.T) {
	want := []string{"eg.db", "eg.db-wal", "eg.db-shm"}
	if got := index.AllowedFiles(); !reflect.DeepEqual(got, want) {
		t.Fatalf("AllowedFiles() = %v，期望 %v", got, want)
	}
	if index.DirName != ".index" || index.DBFileName != "eg.db" {
		t.Fatalf("布局常量漂移：DirName = %q，DBFileName = %q", index.DirName, index.DBFileName)
	}
}

// TestPragmasApplied 反证运行期 PRAGMA 口径（合同 §3.3）：journal_mode = wal，
// 且**不使用** user_version（版本走显式表，user_version 必须保持 0）。
func TestPragmasApplied(t *testing.T) {
	dir := buildFixture(t, sampleSnapshot())
	db := openFixture(t, dir, false)

	var mode string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("读 journal_mode 失败：%v", err)
	}
	if strings.ToLower(mode) != "wal" {
		t.Fatalf("journal_mode = %q，期望 wal", mode)
	}
	var userVersion int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&userVersion); err != nil {
		t.Fatalf("读 user_version 失败：%v", err)
	}
	if userVersion != 0 {
		t.Fatalf("user_version = %d，合同 §4.2 规定不使用该机制（应保持 0）", userVersion)
	}
}

// openFixture 用同一个纯 Go 驱动直连索引库（测试脚手架：产品代码走 index 包自己的口）。
func openFixture(t *testing.T, dir string, readOnly bool) *sql.DB {
	t.Helper()
	dsn := "file:" + filepath.Join(dir, index.DBFileName)
	if readOnly {
		dsn += "?mode=ro"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("打开索引库失败：%v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// equalSet 比较两个字符串切片是否为同一集合（次序无关，但**元素个数必须相等**）。
func equalSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]int{}
	for _, g := range got {
		seen[g]++
	}
	for _, w := range want {
		seen[w]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}
