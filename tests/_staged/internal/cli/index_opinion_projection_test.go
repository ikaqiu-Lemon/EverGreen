package cli

// T-005-B 端到端合同：全量 index 快照与读路径权威投影**同批同口径收录 Opinion**，
// 且含观点的 v2 索引在 `eg index status` 与默认 `eg search` 两条路径上都保持 healthy /
// 走 index backend、不 W24/Q5 降级；默认 search 不泄漏观点。
//
// 与 T-005-A（内层 schema 存储合同）的分工：A 只钉 `internal/index` 包内两列的存储、
// 约束与无损往返（库**形态**），本文件钉的是**产品面**——`o-*` 是否真的经由
// `internal/cli/index.go:indexSnapshotWith` 与 `internal/query` 的权威投影进了
// `cards`/`cards_fts`/`files`/`relations`，以及「写入侧」与「读路径」两侧口径是否逐格一致。
//
// 为什么用真实 vault、真实 `eg index build`、真实 SQL 对账，而不是 mock 扫描结果：
// 本批的关键判据恰恰是「产品扫描面到底有没有把观点带进来」这件事本身——用伪造的
// 扫描结果替身去喂索引，正好把要证的那一环 stub 掉了。因此 vault 里同时放真实的 k-*
// 与 o-*（validation 三态齐全），关系也走真实 frontmatter 解析。

import (
	"database/sql"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/index"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// —— 观点语料常量（ID 刻意以 o- 起头：sortedCards 按 ID 全序，故 o-* 恒排在 k-* 之后，
// 这也是「新增观点不打乱既有 k-* rowid」这条确定性性质的前提）——
const (
	opnDomain = "ai-infra"

	opnPendingID   = "o-20260901-alpha"
	opnValidatedID = "o-20260901-beta"
	opnRejectedID  = "o-20260901-gamma"

	// 每条观点正文里放一个**知识卡语料里绝不出现**的唯一 ASCII 令牌：
	// 用它同时钉两件相反的事——业务默认 search 不得召回观点（不泄漏），
	// 而底层 FTS 存储必须能按它召回该观点行（正文确实进了 FTS）。
	opnPendingToken   = "zylphionqk"
	opnValidatedToken = "qwopraxvtd"
	opnRejectedToken  = "mnbvcxlkju"
)

// opnFileContent 造一份**盘上已有**的观点：validation 与正文令牌、关系目标由参数给定。
//
// 分区严格用 KnownSections(opinion) 的固定五分区（观点 / 论据与推理 / 条件与反例 /
// 待验证 / 用户补充），frontmatter 带 `title`（落进 Extra，供 entryTitle 取标题——
// 写入侧与读路径投影都走同一个 entryTitle，口径不分叉）。sources 引用 idxVault 已建的
// s-/n-，relations 指向一张真实存在的知识卡（避免悬空引用干扰对账）。
func opnFileContent(id, title, validation, token, relVerb, relTarget string) string {
	return "---\n" +
		"id: " + id + "\n" +
		"title: " + title + "\n" +
		"status: active\n" +
		"validation: " + validation + "\n" +
		"created_at: '2026-09-01'\n" +
		"updated_at: '2026-09-01T10:00:00+08:00'\n" +
		"sources:\n" +
		"  - source: " + applySourceID + "\n" +
		"    note: " + applyNoteID + "\n" +
		"    rel: support\n" +
		"    reason: 依据材料第一节的分析\n" +
		"relations:\n" +
		"  - type: " + relVerb + "\n" +
		"    target: " + relTarget + "\n" +
		"    reason: 与该卡结论的论证关系\n" +
		"tags: []\n" +
		"---\n\n" +
		"# " + title + "\n\n" +
		"## " + store.SecOpinionClaim + "\n\n" +
		"主张一句 " + token + "。\n\n" +
		"## " + store.SecArgument + "\n\n" +
		"论据与推理占位。\n\n" +
		"## " + store.SecCounter + "\n\n" +
		"条件与反例占位。\n\n" +
		"## " + store.SecToVerify + "\n\n" +
		"- 待补一个真实反例\n\n" +
		"## " + store.SecUserAppend + "\n\n" +
		"我的看法先记在这里。\n"
}

// opnSeed 描述一条要落盘的观点。
type opnSeed struct {
	id, title, validation, token, relVerb, relTarget string
}

func opnSeeds() []opnSeed {
	return []opnSeed{
		{opnPendingID, "缩放注意力的经济性存疑", string(index.ValidationPending),
			opnPendingToken, "supports", applyCardID},
		{opnValidatedID, "长序列下注意力不经济", string(index.ValidationValidated),
			opnValidatedToken, "limits", applyCard2ID},
		{opnRejectedID, "注意力对短序列无益的判断不成立", string(index.ValidationRejected),
			opnRejectedToken, "opposing", applyCardID},
	}
}

// idxVaultWithOpinions 在 idxVault（两张知识卡 + 一篇笔记）之上落三条真实观点
// （validation 三态齐全），git 提交后工作区干净。返回 vault 目录。
func idxVaultWithOpinions(t *testing.T) string {
	t.Helper()
	dir := idxVault(t)
	for _, s := range opnSeeds() {
		rel := store.OpinionRel(opnDomain, s.id)
		writeFileMk(t, filepath.Join(dir, filepath.FromSlash(rel)),
			opnFileContent(s.id, s.title, s.validation, s.token, s.relVerb, s.relTarget))
	}
	// 用户真实路径下这些文件会被提交进 Git；这里同样提交，让工作区干净、水位线 head 稳定。
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "-c", "user.name=eg-test", "-c", "user.email=eg-test@example.com",
		"commit", "-q", "-m", "seed opinions")
	if got := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); got != "" {
		t.Fatalf("前置条件：落观点后工作区必须干净，得到 %q", got)
	}
	return dir
}

// —— SQL 对账脚手架（只读直连 .index/eg.db；驱动名 "sqlite" 由 internal/index 空导入注册）——

func opnOpenDB(t *testing.T, dir string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+index.DBPath(dir)+"?mode=ro")
	if err != nil {
		t.Fatalf("打开索引库失败：%v", err)
	}
	return db
}

// opnPathsByKind 取 cards 表某个 kind 的 path 集合（升序）。
func opnPathsByKind(t *testing.T, dir, kind string) []string {
	t.Helper()
	db := opnOpenDB(t, dir)
	defer func() { _ = db.Close() }()
	rows, err := db.Query(`SELECT path FROM `+index.TableCards+` WHERE kind = ? ORDER BY path`, kind)
	if err != nil {
		t.Fatalf("查询 cards.kind=%s 失败：%v", kind, err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			t.Fatalf("scan 失败：%v", err)
		}
		out = append(out, p)
	}
	return out
}

// opnValidationByKindPath 取某 kind 全部行的 path→validation 映射。
func opnValidationByID(t *testing.T, dir string) map[string]string {
	t.Helper()
	db := opnOpenDB(t, dir)
	defer func() { _ = db.Close() }()
	rows, err := db.Query(`SELECT id, validation FROM ` + index.TableCards + ` ORDER BY id`)
	if err != nil {
		t.Fatalf("查询 validation 失败：%v", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var id, v string
		if err := rows.Scan(&id, &v); err != nil {
			t.Fatalf("scan 失败：%v", err)
		}
		out[id] = v
	}
	return out
}

// opnFTSMatchIDs 直查 cards_fts 底层存储：按令牌 MATCH，返回命中行的 id 集合（升序）。
func opnFTSMatchIDs(t *testing.T, dir, token string) []string {
	t.Helper()
	db := opnOpenDB(t, dir)
	defer func() { _ = db.Close() }()
	rows, err := db.Query(`SELECT id FROM `+index.TableCardsFTS+` WHERE `+
		index.TableCardsFTS+` MATCH ? ORDER BY id`, token)
	if err != nil {
		t.Fatalf("FTS MATCH %q 失败：%v", token, err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan 失败：%v", err)
		}
		out = append(out, id)
	}
	return out
}

// opnRelSrcIDs 取 relations 表全部 src_id（升序，含重复剔除）。
func opnRelSrcIDs(t *testing.T, dir string) []string {
	t.Helper()
	db := opnOpenDB(t, dir)
	defer func() { _ = db.Close() }()
	rows, err := db.Query(`SELECT DISTINCT src_id FROM ` + index.TableRelations + ` ORDER BY src_id`)
	if err != nil {
		t.Fatalf("查询 relations.src_id 失败：%v", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan 失败：%v", err)
		}
		out = append(out, s)
	}
	return out
}

// opnFilePaths 取 files 表全部 path（升序）。
func opnFilePaths(t *testing.T, dir string) []string {
	t.Helper()
	db := opnOpenDB(t, dir)
	defer func() { _ = db.Close() }()
	rows, err := db.Query(`SELECT path FROM ` + index.TableFiles + ` ORDER BY path`)
	if err != nil {
		t.Fatalf("查询 files.path 失败：%v", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			t.Fatalf("scan 失败：%v", err)
		}
		out = append(out, p)
	}
	return out
}

// opnTableCount 数库里实际存在的表（对照封闭集合恰 6 张）。
func opnTableCount(t *testing.T, dir string) int {
	t.Helper()
	db := opnOpenDB(t, dir)
	defer func() { _ = db.Close() }()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatalf("查询表集合失败：%v", err)
	}
	defer func() { _ = rows.Close() }()
	names := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan 失败：%v", err)
		}
		// FTS5 影子表（cards_fts_data 等）不计入 6 张业务表。
		if strings.HasPrefix(n, index.FTSShadowPrefix) {
			continue
		}
		names[n] = true
	}
	return len(names)
}

// opnCardRowids 取 cards 表 (rowid, id)，按 rowid 升序。
func opnCardRowids(t *testing.T, dir string) [][2]string {
	t.Helper()
	db := opnOpenDB(t, dir)
	defer func() { _ = db.Close() }()
	rows, err := db.Query(`SELECT rowid, id FROM ` + index.TableCards + ` ORDER BY rowid`)
	if err != nil {
		t.Fatalf("查询 cards rowid 失败：%v", err)
	}
	defer func() { _ = rows.Close() }()
	var out [][2]string
	for rows.Next() {
		var rid, id string
		if err := rows.Scan(&rid, &id); err != nil {
			t.Fatalf("scan 失败：%v", err)
		}
		out = append(out, [2]string{rid, id})
	}
	return out
}

// diskMarkdownPaths 走查某个领域子目录下的全部 .md（vault 内相对路径，升序）。
func diskMarkdownPaths(t *testing.T, dir, domain, sub string) []string {
	t.Helper()
	base := filepath.Join(dir, "domains", domain, sub)
	matches, err := filepath.Glob(filepath.Join(base, "*.md"))
	if err != nil {
		t.Fatalf("glob %s 失败：%v", base, err)
	}
	var out []string
	for _, m := range matches {
		rel, _ := filepath.Rel(dir, m)
		out = append(out, filepath.ToSlash(rel))
	}
	sort.Strings(out)
	return out
}

// searchWarnCodes 从 search 的 map 信封里取 warnings[].code 集合。
func searchWarnCodes(t *testing.T, env map[string]interface{}) []string {
	t.Helper()
	warns, _ := env["warnings"].([]interface{})
	var out []string
	for _, w := range warns {
		m, _ := w.(map[string]interface{})
		if c, ok := m["code"].(string); ok && c != "" {
			out = append(out, c)
		}
	}
	return out
}

// mustAtoi 把 rowid 字符串解析成 int64（脚手架）。
func mustAtoi(t *testing.T, s string) int64 {
	t.Helper()
	var n int64
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			t.Fatalf("rowid 非纯数字：%q", s)
		}
		n = n*10 + int64(ch-'0')
	}
	return n
}

func opnSortedCopy(in []string) []string {
	out := append([]string{}, in...)
	sort.Strings(out)
	return out
}

func opnEqualStrSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	sa, sb := opnSortedCopy(a), opnSortedCopy(b)
	for i := range sa {
		if sa[i] != sb[i] {
			return false
		}
	}
	return true
}

// TestIndexBuildProjectsOpinionRowsMatchDisk —— 全量 build 后 SQL 对账：
//
//	① cards 的 knowledge 行集恰等于磁盘 knowledge/k-*，opinion 行集恰等于 opinions/o-*；
//	② 每条观点的 validation 逐字等于 frontmatter，knowledge 行 validation 恒空；
//	③ files 表纳管全部 5 个文件（2 知识卡 + 3 观点），无遗漏；
//	④ relations 表纳管观点关系（三条 src_id 均为 o-*）；
//	⑤ 表仍恰 6 张（没有偷偷分表）。
func TestIndexBuildProjectsOpinionRowsMatchDisk(t *testing.T) {
	dir := idxVaultWithOpinions(t)
	if code, _, errOut := runIndexCLI(t, dir, "build"); code != ExitOK {
		t.Fatalf("build 退出码非 0：%s", errOut)
	}

	// ① knowledge 行集 == 磁盘 k-*
	wantKnow := diskMarkdownPaths(t, dir, opnDomain, "knowledge")
	gotKnow := opnPathsByKind(t, dir, string(index.CardKindKnowledge))
	if !opnEqualStrSets(wantKnow, gotKnow) {
		t.Fatalf("cards.knowledge 行集与磁盘 k-* 不符：期望 %v，实得 %v", wantKnow, gotKnow)
	}
	// ① opinion 行集 == 磁盘 o-*
	wantOpn := diskMarkdownPaths(t, dir, opnDomain, "opinions")
	if len(wantOpn) != 3 {
		t.Fatalf("前置：磁盘应有 3 条观点，实得 %v", wantOpn)
	}
	gotOpn := opnPathsByKind(t, dir, string(index.CardKindOpinion))
	if !opnEqualStrSets(wantOpn, gotOpn) {
		t.Fatalf("cards.opinion 行集与磁盘 o-* 不符：期望 %v，实得 %v", wantOpn, gotOpn)
	}

	// ② validation 逐条等于 frontmatter；knowledge 恒空
	val := opnValidationByID(t, dir)
	wantVal := map[string]string{
		opnPendingID:   string(index.ValidationPending),
		opnValidatedID: string(index.ValidationValidated),
		opnRejectedID:  string(index.ValidationRejected),
		applyCardID:    "",
		applyCard2ID:   "",
	}
	for id, want := range wantVal {
		if got, ok := val[id]; !ok || got != want {
			t.Fatalf("cards.validation[%s] = %q（present=%v），期望 %q", id, got, ok, want)
		}
	}

	// ③ files 纳管全部 5 个文件
	wantFiles := append(opnSortedCopy(wantKnow), wantOpn...)
	if !opnEqualStrSets(wantFiles, opnFilePaths(t, dir)) {
		t.Fatalf("files 表未同口径纳管观点：期望 %v，实得 %v", wantFiles, opnFilePaths(t, dir))
	}

	// ④ relations 纳管观点关系（三条 src 均为观点）
	wantRelSrc := []string{opnPendingID, opnValidatedID, opnRejectedID}
	if !opnEqualStrSets(wantRelSrc, opnRelSrcIDs(t, dir)) {
		t.Fatalf("relations.src_id 未纳管观点关系：期望 %v，实得 %v", wantRelSrc, opnRelSrcIDs(t, dir))
	}

	// ⑤ 表仍恰 6 张
	if n := opnTableCount(t, dir); n != len(index.TableNames()) {
		t.Fatalf("表数 = %d，期望恰 %d（不得分表）", n, len(index.TableNames()))
	}
}

// TestIndexRebuildOpinionEqualsBuild —— 删 .index/ 后 rebuild 与 build 在 kind/validation/
// FTS 命中集三者对账一致（索引是可重建派生）。
func TestIndexRebuildOpinionEqualsBuild(t *testing.T) {
	dir := idxVaultWithOpinions(t)
	if code, _, errOut := runIndexCLI(t, dir, "build"); code != ExitOK {
		t.Fatalf("build 退出码非 0：%s", errOut)
	}
	buildOpn := opnPathsByKind(t, dir, string(index.CardKindOpinion))
	if len(buildOpn) != 3 {
		t.Fatalf("前置：build 后 opinion 行应有 3 条，实得 %v", buildOpn)
	}
	buildVal := opnValidationByID(t, dir)
	buildFTS := opnFTSMatchIDs(t, dir, opnPendingToken)
	if len(buildFTS) == 0 {
		t.Fatalf("前置：build 后 FTS 应能按观点令牌召回，实得空")
	}

	if code, _, errOut := runIndexCLI(t, dir, "rebuild"); code != ExitOK {
		t.Fatalf("rebuild 退出码非 0：%s", errOut)
	}
	if !opnEqualStrSets(buildOpn, opnPathsByKind(t, dir, string(index.CardKindOpinion))) {
		t.Fatal("rebuild 后 opinion 行集与 build 不一致")
	}
	if got := opnValidationByID(t, dir); len(got) != len(buildVal) {
		t.Fatal("rebuild 后 validation 映射与 build 不一致")
	}
	if !opnEqualStrSets(buildFTS, opnFTSMatchIDs(t, dir, opnPendingToken)) {
		t.Fatal("rebuild 后 FTS 命中集与 build 不一致")
	}
}

// TestOpinionIndexHealthyAndSearchNoLeak —— 含观点的库上：
//
//	① eg index status 判 healthy / fresh / use_index=true（不 W24、不陈旧）；
//	② 默认 eg search 命中知识卡、且 warnings 里没有 Q5/W22/W23/W24（走 index backend、未降级）；
//	③ 默认 eg search 对**观点专属令牌**零命中（不泄漏 o-*）；
//	④ 但底层 cards_fts 能按该令牌召回观点行（观点正文确实进了 FTS 存储）。
func TestOpinionIndexHealthyAndSearchNoLeak(t *testing.T) {
	dir := idxVaultWithOpinions(t)
	if code, _, errOut := runIndexCLI(t, dir, "build"); code != ExitOK {
		t.Fatalf("build 退出码非 0：%s", errOut)
	}

	// ① 体检 healthy / fresh / use_index
	code, out, errOut := runIndexCLI(t, dir, "status")
	if code != ExitOK {
		t.Fatalf("status 退出码 = %d：%s", code, errOut)
	}
	if h := idxHealth(t, out); h != string(index.HealthHealthy) {
		t.Fatalf("含观点索引 health = %q，期望 healthy（不得 W24 降级）", h)
	}
	if f := idxString(t, out, "freshness"); f != string(index.FreshnessFresh) {
		t.Fatalf("含观点索引 freshness = %q，期望 fresh", f)
	}
	if u := idxString(t, out, "use_index"); u != "true" {
		t.Fatalf("含观点索引 use_index = %q，期望 true（读侧应走 index backend）", u)
	}

	// ② 默认 search 命中知识卡，且无降级诊断
	scode, senv, serr := runSearchJSON(t, dir, "注意力")
	if scode != ExitOK {
		t.Fatalf("search 退出码 = %d：%s", scode, serr)
	}
	ids := searchHitIDs(t, senv)
	if !contains(ids, applyCardID) || !contains(ids, applyCard2ID) {
		t.Fatalf("默认 search 应命中两张知识卡，实得 %v", ids)
	}
	for _, code := range searchWarnCodes(t, senv) {
		switch code {
		case index.CodeIndexStale, index.CodeIndexMissing, index.CodeIndexCorrupt, "Q5":
			t.Fatalf("默认 search 出现降级诊断 %s：含观点索引不应退回扫描", code)
		}
	}

	// ③ 观点专属令牌零命中（不泄漏）
	for _, tok := range []string{opnPendingToken, opnValidatedToken, opnRejectedToken} {
		lcode, lenv, lerr := runSearchJSON(t, dir, tok)
		if lcode != ExitOK {
			t.Fatalf("search %q 退出码 = %d：%s", tok, lcode, lerr)
		}
		if hits := searchHitIDs(t, lenv); len(hits) != 0 {
			t.Fatalf("默认 search 泄漏观点：令牌 %q 命中 %v，期望零命中", tok, hits)
		}
	}

	// ④ 底层 FTS 能按观点令牌召回观点行（正文确实进了 FTS 存储）
	if got := opnFTSMatchIDs(t, dir, opnPendingToken); !opnEqualStrSets(got, []string{opnPendingID}) {
		t.Fatalf("cards_fts 未按令牌 %q 召回观点 %s，实得 %v", opnPendingToken, opnPendingID, got)
	}
	if got := opnFTSMatchIDs(t, dir, opnRejectedToken); !opnEqualStrSets(got, []string{opnRejectedID}) {
		t.Fatalf("cards_fts 未按令牌 %q 召回观点 %s，实得 %v", opnRejectedToken, opnRejectedID, got)
	}
}

// TestKnowledgeRowidStableAcrossOpinionAddition —— 确定性：同一批知识卡在「仅 knowledge」
// 与「新增 o-* 后重建」两库中 rowid 保持不变，且 o-* 的 rowid 全部排在 k-* 之后。
func TestKnowledgeRowidStableAcrossOpinionAddition(t *testing.T) {
	// 库甲：仅知识卡。
	knowOnly := idxVault(t)
	if code, _, errOut := runIndexCLI(t, knowOnly, "build"); code != ExitOK {
		t.Fatalf("build（仅知识卡）退出码非 0：%s", errOut)
	}
	baseRowid := map[string]string{}
	for _, r := range opnCardRowids(t, knowOnly) {
		baseRowid[r[1]] = r[0]
	}
	if len(baseRowid) != 2 {
		t.Fatalf("前置：仅知识卡库应有 2 张卡，实得 %d", len(baseRowid))
	}

	// 库乙：知识卡 + 观点。
	withOpn := idxVaultWithOpinions(t)
	if code, _, errOut := runIndexCLI(t, withOpn, "build"); code != ExitOK {
		t.Fatalf("build（含观点）退出码非 0：%s", errOut)
	}
	rows := opnCardRowids(t, withOpn)
	if len(rows) != 5 {
		t.Fatalf("前置：含观点库应有 5 行（2 知识卡 + 3 观点），实得 %d 行", len(rows))
	}
	maxKnow := int64(-1)
	minOpn := int64(1 << 62)
	opnCount := 0
	for _, r := range rows {
		id, rid := r[1], mustAtoi(t, r[0])
		if base, ok := baseRowid[id]; ok {
			if base != r[0] {
				t.Fatalf("知识卡 %s 的 rowid 因新增观点而变动：仅知识卡库 %s → 含观点库 %s",
					id, base, r[0])
			}
			if rid > maxKnow {
				maxKnow = rid
			}
		} else {
			opnCount++
			if rid < minOpn {
				minOpn = rid
			}
		}
	}
	if opnCount != 3 {
		t.Fatalf("含观点库应有 3 条观点行，实得 %d", opnCount)
	}
	if minOpn <= maxKnow {
		t.Fatalf("观点 rowid(min=%d) 未全部排在知识卡 rowid(max=%d) 之后", minOpn, maxKnow)
	}
}
