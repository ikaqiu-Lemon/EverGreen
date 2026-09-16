package cli

// `eg opinion search` 的行为判据（读路径 CLI 拆分设计 §5.4；T-…-006 批次 B1b-cli）。
//
// 本批只接通 search 一条子命令，其边界与判据：
//   - 只搜观点（o-*）：runOpinionSearch 把 Kind 固定成 opinion，知识卡（k-*）一条都不进 hits[]；
//   - validation 三态（pending / validated / rejected）**全部召回**，validation 绝不作隐式过滤；
//   - 专用 DTO：沿用 `eg search` hit 的既有字段，**追加** validation 与 relation_summary；
//     relation_summary 的 JSON 键固定 supports / limits / opposing（次序即声明序）；
//   - 人类可读每行显式带 [validation]，且三类关系计数按 supports → limits → opposing 固定次序；
//   - 复用 `eg search` 的领域校验、Index deps、分页、错误与 diagnostics 口径（--kind 不注册）；
//   - 全路径**零副作用**：只读检索，零文件变化、零 commit；
//   - 普通 `eg search` 的 JSON 键集合**一字不改**（观点专属字段绝不泄漏到 eg search）。
//
// 测试名统一含 `Opinion`（与 verify.test 的 -run 对齐）。

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
)

// 索引健康度诊断码（与 internal/index.Code* 同源）：本文件按 S4 命令层文件级位置锁
// （cmd/eg 的 TestStage4IndexPackageBoundary ⑤：只有 index* / bench* 前缀的 cli 文件可
// import 索引包）**不得** import internal/index，故以合同码字面量登记，语义等价。
const (
	codeIndexMissing = "W23" // = index.CodeIndexMissing（索引缺失）
	codeIndexStale   = "W22" // = index.CodeIndexStale（索引陈旧）
	codeIndexCorrupt = "W24" // = index.CodeIndexCorrupt（索引损坏）
)

// —— 语料常量：所有观点标题都含同一个 ASCII 令牌 optok，便于用一个查询词召回全部三态。——
const (
	opsToken   = "quotaxyz"
	opsKnowID  = "k-20260905-cost"
	opsAlphaID = "o-20260910-alpha" // validated；supports=3 limits=1 opposing=2（另有 1 条 derives 不计）
	opsBetaID  = "o-20260909-beta"  // pending；无关系 → 0/0/0
	opsGammaID = "o-20260908-gamma" // rejected（ops 领域）；opposing=1
	opsDeltaID = "o-20260907-delta" // pending 且已删除（deleted_at 非空）→ 默认视图不返回
)

// oRel 是造一条观点关系的最小三元（type/target/reason 里 reason 固定占位）。
type oRel struct{ typ, target string }

// seedOpinionRel 写一份**盘上已有**的观点：validation / tags / relations / deleted_at 由参数给定
// （测试脚手架，不是产品写路径）。frontmatter 键与 model.Opinion 的 yaml tag 逐一对齐。
func seedOpinionRel(t *testing.T, root, domain, id, title, validation, created, updated string,
	tags []string, rels []oRel, deletedAt string, body string) {
	t.Helper()
	fm := "---\nid: " + id + "\nstatus: active\ncreated_at: '" + created +
		"'\nupdated_at: '" + updated + "'\ntitle: " + title +
		"\nvalidation: " + validation + "\nsources: []\n"
	if len(tags) > 0 {
		fm += "tags:\n"
		for _, tag := range tags {
			fm += "  - " + tag + "\n"
		}
	}
	if len(rels) > 0 {
		fm += "relations:\n"
		for _, r := range rels {
			fm += "  - type: " + r.typ + "\n    target: " + r.target + "\n    reason: 关系占位理由\n"
		}
	}
	if deletedAt != "" {
		fm += "deleted_at: '" + deletedAt + "'\n"
	}
	fm += "---\n\n## 观点\n\n" + body + "\n"
	writeFileMk(t, filepath.Join(root, "domains", domain, "opinions", id+".md"), fm)
}

// opinionCLIVault 造一个 git 化、工作区干净的双领域 vault：一张知识卡 + 四条观点
// （validation 三态齐全 + 一条已删除），全部标题含 opsToken。
func opinionCLIVault(t *testing.T) string {
	t.Helper()
	setGitIdentity(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ConfigFileName),
		"version: 1\ndomains:\n  - ai-infra\n  - ops\ndefault_domain: ai-infra\n")

	// 知识卡也含令牌：用来反证「opinion search 只搜观点」——它必须**不**出现在结果里。
	seedCard(t, dir, "ai-infra", opsKnowID, "配额成本 "+opsToken, "active",
		"2026-09-05", "2026-09-05T10:00:00+08:00", nil, "正文占位 "+opsToken+"。")

	// validated：supports=3 / limits=1 / opposing=2，另加 1 条 derives（不计入三类计数）。
	seedOpinionRel(t, dir, "ai-infra", opsAlphaID, "配额经济性已验证 "+opsToken, "validated",
		"2026-09-10", "2026-09-10T10:00:00+08:00", []string{"经济", "核心"},
		[]oRel{
			{"supports", "k-1"}, {"supports", "k-2"}, {"supports", "k-3"},
			{"limits", "k-4"},
			{"opposing", "k-5"}, {"opposing", "k-6"},
			{"derives", "k-7"},
		}, "", "主张一句 "+opsToken+"。")

	// pending：无任何关系 → 三类计数全 0；无 tags（tag 过滤反证用）。
	seedOpinionRel(t, dir, "ai-infra", opsBetaID, "配额分配待定 "+opsToken, "pending",
		"2026-09-09", "2026-09-09T10:00:00+08:00", nil, nil, "", "主张一句 "+opsToken+"。")

	// rejected（ops 领域）：opposing=1（领域过滤反证用）。
	seedOpinionRel(t, dir, "ops", opsGammaID, "配额上限判断不成立 "+opsToken, "rejected",
		"2026-09-08", "2026-09-08T10:00:00+08:00", []string{"经济"},
		[]oRel{{"opposing", "k-9"}}, "", "主张一句 "+opsToken+"。")

	// pending 且已删除：默认视图不返回，--include-deleted 才带回并标 [已删除]。
	seedOpinionRel(t, dir, "ai-infra", opsDeltaID, "配额废弃观点 "+opsToken, "pending",
		"2026-09-07", "2026-09-07T10:00:00+08:00", nil, nil,
		"2026-09-11T10:00:00+08:00", "主张一句 "+opsToken+"。")

	gitOut(t, dir, "init", "-q")
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "-c", "user.name=eg-test", "-c", "user.email=eg-test@example.com",
		"commit", "-q", "-m", "seed opinion corpus")
	if got := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); got != "" {
		t.Fatalf("前置条件：造语料后工作区必须干净，得到 %q", got)
	}
	return dir
}

// runOpinionSearchRaw 跑一次 `eg opinion search --json …`，返回退出码与**原始** stdout（供键序逐字反证）。
func runOpinionSearchRaw(t *testing.T, dir string, args ...string) (int, string, string) {
	t.Helper()
	setGitIdentity(t)
	r := newTestRoot(t, dir)
	full := append([]string{"--vault", dir, "--json", "opinion", "search"}, args...)
	return runCLI(t, r, full...)
}

// runOpinionSearchJSON 跑一次 `eg opinion search --json …` 并解出信封。
func runOpinionSearchJSON(t *testing.T, dir string, args ...string) (int, map[string]interface{}, string) {
	t.Helper()
	code, out, errOut := runOpinionSearchRaw(t, dir, args...)
	var env map[string]interface{}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("--json 输出不是合法 JSON：%v（%q / %q）", err, out, errOut)
	}
	assertEnvelopeKeys(t, env)
	return code, env, errOut
}

// opinionHitTriple 是一条观点命中的判别投影：validation + 三类关系计数。
type opinionHitTriple struct {
	validation                 string
	supports, limits, opposing int
}

// opinionMetaByID 从 opinion search 信封里取 id → (validation, relation_summary 三计数)。
func opinionMetaByID(t *testing.T, env map[string]interface{}) map[string]opinionHitTriple {
	t.Helper()
	data, _ := env["data"].(map[string]interface{})
	hits, ok := data["hits"].([]interface{})
	if !ok {
		t.Fatalf("data.hits 不是数组：%v", data["hits"])
	}
	out := map[string]opinionHitTriple{}
	for _, h := range hits {
		m, _ := h.(map[string]interface{})
		id, _ := m["id"].(string)
		val, ok := m["validation"].(string)
		if !ok {
			t.Fatalf("opinion 命中缺 validation 键（专用 DTO 必须追加）：%v", m)
		}
		rs, ok := m["relation_summary"].(map[string]interface{})
		if !ok {
			t.Fatalf("opinion 命中缺 relation_summary 对象（专用 DTO 必须追加）：%v", m)
		}
		out[id] = opinionHitTriple{
			validation: val,
			supports:   jsonInt(t, rs, "supports"),
			limits:     jsonInt(t, rs, "limits"),
			opposing:   jsonInt(t, rs, "opposing"),
		}
	}
	return out
}

func jsonInt(t *testing.T, m map[string]interface{}, key string) int {
	t.Helper()
	f, ok := m[key].(float64)
	if !ok {
		t.Fatalf("relation_summary 缺整数键 %q：%v", key, m)
	}
	return int(f)
}

// —— ① 只搜观点：知识卡一条不进结果，三态观点全召回 ——

func TestOpinionSearchOnlyReturnsOpinions(t *testing.T) {
	dir := opinionCLIVault(t)
	code, env, errOut := runOpinionSearchJSON(t, dir, opsToken)
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0：%s", code, errOut)
	}
	ids := searchHitIDs(t, env)
	if contains(ids, opsKnowID) {
		t.Fatalf("opinion search 泄漏知识卡 %s：只应召回观点，实得 %v", opsKnowID, ids)
	}
	for _, id := range ids {
		if !strings.HasPrefix(id, "o-") {
			t.Fatalf("opinion search 返回了非观点 id %q（只搜观点）：%v", id, ids)
		}
	}
	// 三条**未删除**观点全召回（已删除的 delta 默认不出现）。
	for _, want := range []string{opsAlphaID, opsBetaID, opsGammaID} {
		if !contains(ids, want) {
			t.Fatalf("opinion search 缺观点 %s（validation 三态必须全召回）：%v", want, ids)
		}
	}
	if contains(ids, opsDeltaID) {
		t.Fatalf("默认视图不得返回已删除观点 %s：%v", opsDeltaID, ids)
	}
}

// —— ② validation 三态逐条如实透出（绝不隐式过滤）——

func TestOpinionSearchValidationThreeStates(t *testing.T) {
	dir := opinionCLIVault(t)
	_, env, _ := runOpinionSearchJSON(t, dir, opsToken)
	meta := opinionMetaByID(t, env)
	want := map[string]string{
		opsAlphaID: "validated",
		opsBetaID:  "pending",
		opsGammaID: "rejected",
	}
	for id, wv := range want {
		got, ok := meta[id]
		if !ok {
			t.Fatalf("缺观点 %s", id)
		}
		if got.validation != wv {
			t.Fatalf("观点 %s 的 validation = %q，期望 %q（三态如实透出）", id, got.validation, wv)
		}
	}
}

// —— ③ relation_summary 三类计数：只数 supports/limits/opposing，derives 不计 ——

func TestOpinionSearchRelationCounts(t *testing.T) {
	dir := opinionCLIVault(t)
	_, env, _ := runOpinionSearchJSON(t, dir, opsToken)
	meta := opinionMetaByID(t, env)
	cases := map[string]opinionHitTriple{
		opsAlphaID: {"validated", 3, 1, 2}, // 含 1 条 derives，但只数三类 → 3/1/2
		opsBetaID:  {"pending", 0, 0, 0},
		opsGammaID: {"rejected", 0, 0, 1},
	}
	for id, w := range cases {
		got := meta[id]
		if got.supports != w.supports || got.limits != w.limits || got.opposing != w.opposing {
			t.Fatalf("观点 %s 关系计数 = (s=%d,l=%d,o=%d)，期望 (s=%d,l=%d,o=%d)（derives 不计）",
				id, got.supports, got.limits, got.opposing, w.supports, w.limits, w.opposing)
		}
	}
}

// —— ④ JSON 键序：hit 追加键在既有键之后、relation_summary 内部 supports→limits→opposing ——

func TestOpinionSearchJSONKeyOrder(t *testing.T) {
	dir := opinionCLIVault(t)
	code, out, errOut := runOpinionSearchRaw(t, dir, opsToken)
	if code != ExitOK {
		t.Fatalf("退出码 = %d：%s", code, errOut)
	}
	// data 键序 = eg search 合同 §1.2（一字不改）。
	dataRaw := out[strings.Index(out, `"data":`)+len(`"data":`):]
	at := 0
	for _, k := range query.SearchDataKeys() {
		i := strings.Index(dataRaw[at:], `"`+k+`":`)
		if i < 0 {
			t.Fatalf("data 缺键 %s 或键序与 eg search 合同不一致：%s", k, out)
		}
		at += i
	}
	// hits[] 元素键序：先 eg search 的既有键（末键 deleted），再 validation，再 relation_summary。
	wantHit := append([]string{}, query.SearchHitKeys()...)
	wantHit = append(wantHit, "validation", "relation_summary")
	at = strings.Index(out, `"hits":[`)
	if at < 0 {
		t.Fatalf("输出缺 hits 数组：%s", out)
	}
	for _, k := range wantHit {
		i := strings.Index(out[at:], `"`+k+`":`)
		if i < 0 {
			t.Fatalf("hits[] 元素缺键 %s 或键序不符（既有键→validation→relation_summary）：%s", k, out)
		}
		at += i
	}
	// relation_summary 对象内部：supports → limits → opposing（固定次序）。
	rsAt := strings.Index(out, `"relation_summary":`)
	inner := out[rsAt:]
	prev := 0
	for _, k := range []string{"supports", "limits", "opposing"} {
		i := strings.Index(inner[prev:], `"`+k+`":`)
		if i < 0 {
			t.Fatalf("relation_summary 缺键 %s 或次序非 supports→limits→opposing：%s", k, out)
		}
		prev += i
	}
}

// —— ⑤ 人类可读每行显式带 [validation] 且计数按 supports→limits→opposing 次序 ——

func TestOpinionSearchTextLineShowsValidationAndCounts(t *testing.T) {
	dir := opinionCLIVault(t)
	setGitIdentity(t)
	r := newTestRoot(t, dir)
	code, out, errOut := runCLI(t, r, "opinion", "search", opsToken, "--vault", dir)
	if code != ExitOK {
		t.Fatalf("退出码 = %d：%s", code, errOut)
	}
	// 定位 alpha 那一行（validated，s=3 l=1 o=2）。
	var line string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, opsAlphaID) {
			line = l
			break
		}
	}
	if line == "" {
		t.Fatalf("人类可读输出缺 %s 的命中行：%s", opsAlphaID, out)
	}
	if !strings.Contains(line, "[validated]") {
		t.Fatalf("命中行未显式标出 [validation]：%q", line)
	}
	// 计数三段按 supports→limits→opposing 固定次序出现。
	si := strings.Index(line, "supports=3")
	li := strings.Index(line, "limits=1")
	oi := strings.Index(line, "opposing=2")
	if si < 0 || li < 0 || oi < 0 {
		t.Fatalf("命中行缺三类计数（supports=3 / limits=1 / opposing=2）：%q", line)
	}
	if !(si < li && li < oi) {
		t.Fatalf("三类计数次序非 supports→limits→opposing：%q", line)
	}
}

// —— ⑥ 领域过滤：--domain 只召回该领域观点，非法领域退 1 ——

func TestOpinionSearchDomainFilter(t *testing.T) {
	dir := opinionCLIVault(t)

	_, env, _ := runOpinionSearchJSON(t, dir, opsToken, "--domain", "ops")
	ids := searchHitIDs(t, env)
	if len(ids) != 1 || ids[0] != opsGammaID {
		t.Fatalf("--domain ops 应恰返回 %s，实得 %v", opsGammaID, ids)
	}

	_, env2, _ := runOpinionSearchJSON(t, dir, opsToken, "--domain", "ai-infra")
	ids2 := searchHitIDs(t, env2)
	if contains(ids2, opsGammaID) {
		t.Fatalf("--domain ai-infra 不应含 ops 的 %s：%v", opsGammaID, ids2)
	}
	if !contains(ids2, opsAlphaID) || !contains(ids2, opsBetaID) {
		t.Fatalf("--domain ai-infra 应含 alpha/beta：%v", ids2)
	}

	code, _, _ := runOpinionSearchRaw(t, dir, opsToken, "--domain", "nonexistent")
	if code != ExitUsage {
		t.Fatalf("非法领域退出码 = %d，期望 1（eg 绝不自选领域）", code)
	}
}

// —— ⑦ 标签过滤：可重复 --tag 为 AND ——

func TestOpinionSearchTagFilterAND(t *testing.T) {
	dir := opinionCLIVault(t)

	_, env, _ := runOpinionSearchJSON(t, dir, opsToken, "--tag", "经济")
	ids := searchHitIDs(t, env)
	if !contains(ids, opsAlphaID) || !contains(ids, opsGammaID) {
		t.Fatalf("--tag 经济 应含 alpha/gamma：%v", ids)
	}
	if contains(ids, opsBetaID) {
		t.Fatalf("--tag 经济 不应含无标签的 beta：%v", ids)
	}

	// AND：两个 --tag 同时命中，只有 alpha 同时含 经济 + 核心。
	_, env2, _ := runOpinionSearchJSON(t, dir, opsToken, "--tag", "经济", "--tag", "核心")
	ids2 := searchHitIDs(t, env2)
	if len(ids2) != 1 || ids2[0] != opsAlphaID {
		t.Fatalf("--tag 经济 --tag 核心（AND）应恰返回 alpha，实得 %v", ids2)
	}
}

// —— ⑧ since/until 日期过滤（闭区间，作用于 updated_at 日期部分）——

func TestOpinionSearchSinceUntil(t *testing.T) {
	dir := opinionCLIVault(t)

	_, env, _ := runOpinionSearchJSON(t, dir, opsToken, "--since", "2026-09-09")
	ids := searchHitIDs(t, env)
	if !contains(ids, opsAlphaID) || !contains(ids, opsBetaID) || contains(ids, opsGammaID) {
		t.Fatalf("--since 2026-09-09 应含 alpha/beta、不含 gamma(0908)：%v", ids)
	}

	_, env2, _ := runOpinionSearchJSON(t, dir, opsToken, "--until", "2026-09-09")
	ids2 := searchHitIDs(t, env2)
	if !contains(ids2, opsBetaID) || !contains(ids2, opsGammaID) || contains(ids2, opsAlphaID) {
		t.Fatalf("--until 2026-09-09 应含 beta/gamma、不含 alpha(0910)：%v", ids2)
	}
}

// —— ⑨ include-deleted：默认不返回已删除观点，显式开关才带回 ——

func TestOpinionSearchIncludeDeleted(t *testing.T) {
	dir := opinionCLIVault(t)

	_, env, _ := runOpinionSearchJSON(t, dir, opsToken)
	if contains(searchHitIDs(t, env), opsDeltaID) {
		t.Fatalf("默认视图不得返回已删除观点 %s", opsDeltaID)
	}

	_, env2, _ := runOpinionSearchJSON(t, dir, opsToken, "--include-deleted")
	if !contains(searchHitIDs(t, env2), opsDeltaID) {
		t.Fatalf("--include-deleted 应带回已删除观点 %s", opsDeltaID)
	}
}

// —— ⑩ 分页：limit/offset 在排序之后施加，total 恒为分页前总数 ——

func TestOpinionSearchPaging(t *testing.T) {
	dir := opinionCLIVault(t)
	// 排序：匹配分相同（令牌均在标题）→ updated_at 倒序 = alpha(0910) → beta(0909) → gamma(0908)。
	_, env0, _ := runOpinionSearchJSON(t, dir, opsToken, "--limit", "1", "--offset", "0")
	if ids := searchHitIDs(t, env0); len(ids) != 1 || ids[0] != opsAlphaID {
		t.Fatalf("limit1 offset0 应恰 alpha，实得 %v", ids)
	}
	_, env1, _ := runOpinionSearchJSON(t, dir, opsToken, "--limit", "1", "--offset", "1")
	if ids := searchHitIDs(t, env1); len(ids) != 1 || ids[0] != opsBetaID {
		t.Fatalf("limit1 offset1 应恰 beta，实得 %v", ids)
	}
	// total 恒为分页前总数（3 条未删除观点）。
	data, _ := env1["data"].(map[string]interface{})
	if tot, _ := data["total"].(float64); int(tot) != 3 {
		t.Fatalf("total = %v，期望 3（分页前总数）", data["total"])
	}
}

// —— ⑪ 零命中：退 0、hits 空、total 0 ——

func TestOpinionSearchZeroHit(t *testing.T) {
	dir := opinionCLIVault(t)
	code, env, errOut := runOpinionSearchJSON(t, dir, "绝不命中的词zzz")
	if code != ExitOK {
		t.Fatalf("零命中退出码 = %d，期望 0：%s", code, errOut)
	}
	if ids := searchHitIDs(t, env); len(ids) != 0 {
		t.Fatalf("零命中应空 hits，实得 %v", ids)
	}
	data, _ := env["data"].(map[string]interface{})
	if tot, _ := data["total"].(float64); int(tot) != 0 {
		t.Fatalf("零命中 total = %v，期望 0", data["total"])
	}
}

// —— ⑫ 非法参数：坏日期 / 负 limit → 退 1 ——

func TestOpinionSearchInvalidArgs(t *testing.T) {
	dir := opinionCLIVault(t)
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"坏 since", []string{opsToken, "--since", "2026-13-01"}},
		{"坏 until", []string{opsToken, "--until", "2026-02-30"}},
		{"负 limit", []string{opsToken, "--limit", "-1"}},
		{"非整 offset", []string{opsToken, "--offset", "x"}},
	} {
		code, _, _ := runOpinionSearchRaw(t, dir, tc.args...)
		if code != ExitUsage {
			t.Fatalf("[%s] 退出码 = %d，期望 1（参数非法）", tc.name, code)
		}
	}
}

// —— ⑬ --kind 未注册：opinion search 显式带 --kind 一律退 1（不静默接受）——

func TestOpinionSearchRejectsKindFlag(t *testing.T) {
	dir := opinionCLIVault(t)
	for _, val := range []string{"opinion", "knowledge", "all"} {
		code, _, _ := runOpinionSearchRaw(t, dir, opsToken, "--kind", val)
		if code != ExitUsage {
			t.Fatalf("opinion search --kind %s 退出码 = %d，期望 1（不注册 --kind）", val, code)
		}
	}
}

// —— ⑭ 非 search 子命令显式带 search-only flag：退 1、零副作用（不静默接受）——

func TestOpinionNonSearchSubsRejectSearchOnlyFlags(t *testing.T) {
	dir := opinionCLIVault(t)
	statusBefore, logBefore := opinionVaultSnapshot(t, dir)

	const validID = "o-20260101-demo"
	searchOnly := [][]string{
		{"--domain", "ai-infra"},
		{"--tag", "经济"},
		{"--since", "2026-09-01"},
		{"--until", "2026-09-30"},
		{"--include-deleted"},
		{"--limit", "5"},
		{"--offset", "1"},
	}
	for _, sub := range []string{"show", "validate", "reject"} {
		for _, fl := range searchOnly {
			args := append([]string{sub, validID}, fl...)
			code, _, errOut := runOpinionCLI(t, dir, args...)
			if code != ExitUsage {
				t.Fatalf("eg opinion %s %v 退出码 = %d，期望 1（search-only flag 不得静默接受）：%s",
					sub, fl, code, errOut)
			}
		}
	}
	statusAfter, logAfter := opinionVaultSnapshot(t, dir)
	if statusAfter != statusBefore || logAfter != logBefore {
		t.Fatal("拒绝 search-only flag 的路径改变了工作区或 commit 数（必须零写入）")
	}
}

// —— ⑮ healthy 索引 vs missing 回退：结果等价，missing 恰 W23 + Q5 降级、healthy 不降级 ——

func TestOpinionSearchHealthyVsMissingEquivalence(t *testing.T) {
	// missing：git 化 vault 无索引 → 全量扫描回退，留痕 W23 + Q5。
	missingDir := idxVaultWithOpinions(t)
	mcode, menv, merr := runOpinionSearchJSON(t, missingDir, "注意力")
	if mcode != ExitOK {
		t.Fatalf("missing 态退出码 = %d：%s", mcode, merr)
	}
	mMeta := opinionMetaByID(t, menv)
	mCodes := searchWarnCodes(t, menv)
	if !contains(mCodes, codeIndexMissing) {
		t.Fatalf("missing 态应留痕 %s（索引缺失），实得 %v", codeIndexMissing, mCodes)
	}
	if !contains(mCodes, query.CodeQ5) {
		t.Fatalf("missing 态应留痕 %s（读路径降级），实得 %v", query.CodeQ5, mCodes)
	}

	// healthy：同语料 build 后走 index backend，不降级。
	healthyDir := idxVaultWithOpinions(t)
	if code, _, errOut := runIndexCLI(t, healthyDir, "build"); code != ExitOK {
		t.Fatalf("build 退出码 = %d：%s", code, errOut)
	}
	hcode, henv, herr := runOpinionSearchJSON(t, healthyDir, "注意力")
	if hcode != ExitOK {
		t.Fatalf("healthy 态退出码 = %d：%s", hcode, herr)
	}
	hMeta := opinionMetaByID(t, henv)
	for _, bad := range []string{codeIndexMissing, codeIndexStale, codeIndexCorrupt, query.CodeQ5} {
		if contains(searchWarnCodes(t, henv), bad) {
			t.Fatalf("healthy 态不应出现降级诊断 %s：%v", bad, searchWarnCodes(t, henv))
		}
	}

	// 等价：两态命中的 id 集合、每条 validation 与三类计数逐条相同（索引只提速不改答案）。
	if len(mMeta) != len(hMeta) || len(mMeta) != 3 {
		t.Fatalf("两态命中数不一致或非 3：missing=%d healthy=%d", len(mMeta), len(hMeta))
	}
	for id, mv := range mMeta {
		hv, ok := hMeta[id]
		if !ok {
			t.Fatalf("healthy 缺 missing 的命中 %s（两态必须等价）", id)
		}
		if mv != hv {
			t.Fatalf("观点 %s 两态投影不等价：missing=%+v healthy=%+v", id, mv, hv)
		}
	}
	// 逐条钉死 idxVaultWithOpinions 三条观点的期望投影（关系各恰 1 条）。
	want := map[string]opinionHitTriple{
		"o-20260901-alpha": {"pending", 1, 0, 0},   // supports → applyCardID
		"o-20260901-beta":  {"validated", 0, 1, 0}, // limits → applyCard2ID
		"o-20260901-gamma": {"rejected", 0, 0, 1},  // opposing → applyCardID
	}
	for id, w := range want {
		if got := hMeta[id]; got != w {
			t.Fatalf("观点 %s 投影 = %+v，期望 %+v", id, got, w)
		}
	}
}

// —— ⑯ 普通 eg search 的 JSON 键集合一字不改（观点专属字段绝不泄漏到 eg search）——

func TestOpinionSearchDoesNotLeakIntoPlainSearch(t *testing.T) {
	dir := opinionCLIVault(t)
	// 普通 search 命中知识卡（opinion search 的观点专属字段不得出现）。
	code, env, errOut := runSearchJSON(t, dir, opsToken)
	if code != ExitOK {
		t.Fatalf("退出码 = %d：%s", code, errOut)
	}
	data, _ := env["data"].(map[string]interface{})
	hits, _ := data["hits"].([]interface{})
	if len(hits) == 0 {
		t.Fatalf("普通 search 应至少命中知识卡 %s", opsKnowID)
	}
	for _, h := range hits {
		m, _ := h.(map[string]interface{})
		// 键集合恰等于 eg search 合同键（不多 validation / relation_summary）。
		for _, forbidden := range []string{"validation", "relation_summary"} {
			if _, ok := m[forbidden]; ok {
				t.Fatalf("普通 eg search 泄漏观点专属键 %q：%v", forbidden, m)
			}
		}
		if len(m) != len(query.SearchHitKeys()) {
			t.Fatalf("普通 search hit 键数 = %d，期望 %d（合同键集合不变）：%v",
				len(m), len(query.SearchHitKeys()), m)
		}
	}
}

// —— ⑰ 全路径零副作用：多种检索形态跑完，工作区与 commit 数一字不变 ——

func TestOpinionSearchZeroSideEffects(t *testing.T) {
	dir := opinionCLIVault(t)
	statusBefore, logBefore := opinionVaultSnapshot(t, dir)

	runs := [][]string{
		{opsToken},
		{opsToken, "--domain", "ops"},
		{opsToken, "--tag", "经济"},
		{opsToken, "--since", "2026-09-09"},
		{opsToken, "--include-deleted"},
		{opsToken, "--limit", "1", "--offset", "1"},
		{"绝不命中zzz"},
	}
	for _, args := range runs {
		if code, _, _ := runOpinionSearchRaw(t, dir, args...); code != ExitOK {
			t.Fatalf("检索形态 %v 退出码非 0", args)
		}
	}
	statusAfter, logAfter := opinionVaultSnapshot(t, dir)
	if statusAfter != statusBefore {
		t.Fatalf("检索改变了工作区（只读必须零文件变化）：%q → %q", statusBefore, statusAfter)
	}
	if logAfter != logBefore {
		t.Fatal("检索产生了 commit（只读必须零 commit）")
	}
}
