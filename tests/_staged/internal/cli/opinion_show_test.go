package cli

// `eg opinion show` 的 **CLI 侧**机器判据（读路径 CLI 拆分设计 §5.4；T-…-006 批次 B2b）。
//
// B2a 已在 query 层把「按合法 o-id 读一条观点」的投影钉死（五分区 / validation / sources /
// supports·limits·opposing 三组各正反两段 / 可见性 / 分页 / 悬空 / 降级等价），见
// internal/query/opinion_show_test.go。B2b 只做**接通**：CLI show 分派到 query.ShowOpinionPaged +
// readIndexDeps，把权威结果折成文本 / JSON 双渲染，并把 flag 精确分域。本支因此**全部走真实
// CLI → vault → index**（绝不伪造 DTO），逐条钉死接通后的对外事实：
//
//	① JSON data 键序逐字 == query.OpinionDataKeys()；validation / 五分区 / sources /
//	   三组正反向都来自权威；derives 不进三组；既有 card/search JSON 键集合零扩张；
//	② 文本模式与 --json 同源同事实：显式显示 validation、五分区、sources、三组正反向
//	   （空段写「无」）、悬空目标标「（目标不存在）」；
//	③ 对端 deprecated 默认隐藏并计 Q4，--include-deprecated 才展示且无 Q4；
//	④ 一个全局 limit/offset 跨六段，截断产恰一条 W25；
//	⑤ 已删除观点仍可显式查看并标 [已删除]；wrong kind（k-*）/ 不存在（o-*）→ 退 1；
//	⑥ card show 收到合法 o-* 在 Validate 阶段退 1 并指引改用 opinion show（不读 vault）；
//	⑦ flag 精确分域：show 拒检索过滤 flag、search 拒 include-deprecated、validate/reject 拒全部读 flag；
//	⑧ healthy 索引与 missing-index 降级（W23+Q5）投影等价、降级码精确；
//	⑨ 全部 show 路径三维零副作用（工作区 / commit 不变）。

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
)

// —— 语料令牌：知识卡语料里绝不出现的唯一串，用来逐字反证「五分区取的是权威正文」——
const (
	ocsClaimTok   = "zclishowclaimq"
	ocsArgTok     = "zclishowargq"
	ocsCounterTok = "zclishowcounterq"
	ocsVerifyTok  = "zclishowverifyq"
	ocsAppendTok  = "zclishowappendq"
)

const (
	ocsRichID  = "o-20261207-scaling"
	ocsPlainID = "o-20261207-plain"
	ocsDeadID  = "o-20261207-dead"
)

// opinionShowVault 造一份 CLI 侧观点视图语料：ai-infra 领域 + 四张知识卡（其中 rnn 已 deprecated）
// + 三条观点（关系覆盖面完整的 rich、无关系的 plain、已删除的 dead）。o-rich 的 opposing 正向
// 指向不存在的 k-*missing（悬空），供 MissingTargets / Q2 判据。返回 git 已初始化的 vault 根。
func opinionShowVault(t *testing.T) string {
	t.Helper()
	dir := captureVault(t) // eg init：ai-infra 领域、git 已初始化且工作区干净
	card := func(id, status, title string) {
		writeFileMk(t, filepath.Join(dir, "domains", "ai-infra", "knowledge", id+".md"),
			"---\nid: "+id+"\nstatus: "+status+"\ncreated_at: '2026-12-01'\n"+
				"updated_at: '2026-12-01T10:00:00+08:00'\nreviewed_at: '2026-12-01T10:00:00+08:00'\n"+
				"title: "+title+"\nsources: []\n---\n\n## 知识内容\n\n"+title+" 正文。\n")
	}
	card("k-20261201-attention", "active", "注意力卡")
	card("k-20261201-ops", "active", "算子卡")
	card("k-20261201-rnn", "deprecated", "循环网络卡")
	card("k-20261201-sched", "active", "调度卡")

	writeFileMk(t, filepath.Join(dir, "domains", "ai-infra", "opinions", ocsRichID+".md"),
		"---\n"+
			"id: "+ocsRichID+"\nstatus: active\ncreated_at: '2026-12-07'\n"+
			"updated_at: '2026-12-08T10:00:00+08:00'\ntitle: 缩放注意力的经济性\n"+
			"validation: pending\n"+
			"sources:\n"+
			"  - source: s-20261207-paper\n    note: n-20261207-note\n    rel: support\n"+
			"    reason: 原始论文佐证\n"+
			"relations:\n"+
			"  - type: derives\n    target: k-20261201-rnn\n    reason: 从循环网络演进而来\n"+
			"  - type: supports\n    target: k-20261201-ops\n    reason: 算子层支撑该主张\n"+
			"  - type: supports\n    target: k-20261201-attention\n    reason: 注意力卡直接支撑\n"+
			"  - type: supports\n    target: k-20261201-rnn\n    reason: 循环网络卡亦支撑（该卡已失效）\n"+
			"  - type: limits\n    target: k-20261201-sched\n    reason: 调度视角构成限制\n"+
			"  - type: opposing\n    target: k-20261201-attention\n    reason: 也存在反对论证\n"+
			"  - type: opposing\n    target: k-20261201-missing\n    reason: 指向一张不存在的卡\n"+
			"---\n\n# 缩放注意力的经济性\n\n"+
			"## 观点\n\n主张 "+ocsClaimTok+"。\n\n"+
			"## 论据与推理\n\n推理链条 "+ocsArgTok+"。\n\n"+
			"## 条件与反例\n\n反例 "+ocsCounterTok+"。\n\n"+
			"## 待验证\n\n待验证项 "+ocsVerifyTok+"。\n\n"+
			"## 用户补充\n\n用户补充 "+ocsAppendTok+"。\n")

	writeFileMk(t, filepath.Join(dir, "domains", "ai-infra", "opinions", ocsPlainID+".md"),
		"---\nid: "+ocsPlainID+"\nstatus: active\ncreated_at: '2026-12-07'\n"+
			"updated_at: '2026-12-07T10:00:00+08:00'\ntitle: 朴素观点\nvalidation: validated\n"+
			"sources: []\n---\n\n# 朴素观点\n\n## 观点\n\n只有一个必填分区。\n")

	writeFileMk(t, filepath.Join(dir, "domains", "ai-infra", "opinions", ocsDeadID+".md"),
		"---\nid: "+ocsDeadID+"\nstatus: active\ncreated_at: '2026-12-07'\n"+
			"updated_at: '2026-12-08T10:00:00+08:00'\ndeleted_at: '2026-12-09T10:00:00+08:00'\n"+
			"deleted_reason: 结论被推翻\ntitle: 已删除的观点\nvalidation: rejected\n"+
			"sources: []\n---\n\n# 已删除的观点\n\n## 观点\n\n主张一句。\n")
	return dir
}

// runOpinionShowJSON 跑一次 `eg opinion show --json` 并解出信封（供 data / warnings 断言）。
func runOpinionShowJSON(t *testing.T, dir string, args ...string) (int, map[string]interface{}, string) {
	t.Helper()
	setGitIdentity(t)
	r := newTestRoot(t, dir)
	full := append([]string{"--vault", dir, "--json", "opinion", "show"}, args...)
	code, out, errOut := runCLI(t, r, full...)
	var env map[string]interface{}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("--json 输出不是合法 JSON：%v（%q / %q）", err, out, errOut)
	}
	assertEnvelopeKeys(t, env)
	return code, env, errOut
}

// groupForwardTargets 抽出 data.<group>.forward[].target 序列（正向段断言用）。
func groupForwardTargets(t *testing.T, env map[string]interface{}, group string) []string {
	t.Helper()
	data, _ := env["data"].(map[string]interface{})
	g, _ := data[group].(map[string]interface{})
	fwd, _ := g["forward"].([]interface{})
	out := []string{}
	for _, e := range fwd {
		m, _ := e.(map[string]interface{})
		out = append(out, fmt.Sprint(m["target"]))
	}
	return out
}

// —— ① JSON 形状：data 键序 == OpinionDataKeys()；validation / 五分区 / sources / 三组来自权威 ——

func TestOpinionShowJSONShapeAndAuthoritativeFacts(t *testing.T) {
	dir := opinionShowVault(t)

	// data 键序逐字 == query.OpinionDataKeys()（新投影，与 card/search 解耦、不扩张）。
	gotKeys := dataKeysFromCLI(t, dir, "opinion", "show", ocsRichID)
	wantKeys := "id,title,domain,status,deprecated,validation,created_at,updated_at,path," +
		"tags,markers,sections,sources,supports,limits,opposing,deleted"
	if strings.Join(gotKeys, ",") != wantKeys {
		t.Fatalf("opinion show data 键序 = %v，期望 %s", gotKeys, wantKeys)
	}

	code, env, errOut := runOpinionShowJSON(t, dir, ocsRichID)
	if code != ExitOK {
		t.Fatalf("opinion show 退出码 = %d，期望 0：%s", code, errOut)
	}
	data, _ := env["data"].(map[string]interface{})
	if data["validation"] != "pending" {
		t.Fatalf("validation = %v，期望 pending", data["validation"])
	}
	// 五分区键齐全（缺一不可）且各含其权威令牌。
	sections, _ := data["sections"].(map[string]interface{})
	for _, tok := range []string{ocsClaimTok, ocsArgTok, ocsCounterTok, ocsVerifyTok, ocsAppendTok} {
		blob, _ := json.Marshal(sections)
		if !strings.Contains(string(blob), tok) {
			t.Fatalf("五分区正文缺权威令牌 %q：%s", tok, blob)
		}
	}
	// sources[] 逐字来自 frontmatter。
	sources, _ := data["sources"].([]interface{})
	if len(sources) != 1 {
		t.Fatalf("sources 应恰 1 条，实际 %d", len(sources))
	}
	// 三组正向（默认视图，rnn 因 deprecated 隐藏）：supports=[attention,ops]、limits=[sched]、
	// opposing=[attention,missing]（missing 悬空仍保留）；derives→rnn 绝不出现在任何一组。
	if got := groupForwardTargets(t, env, "supports"); strings.Join(got, ",") !=
		"k-20261201-attention,k-20261201-ops" {
		t.Fatalf("supports.forward = %v，期望 [attention, ops]（rnn 隐藏）", got)
	}
	if got := groupForwardTargets(t, env, "limits"); strings.Join(got, ",") != "k-20261201-sched" {
		t.Fatalf("limits.forward = %v，期望 [sched]", got)
	}
	if got := groupForwardTargets(t, env, "opposing"); strings.Join(got, ",") !=
		"k-20261201-attention,k-20261201-missing" {
		t.Fatalf("opposing.forward = %v，期望 [attention, missing]", got)
	}
	for _, g := range []string{"supports", "limits", "opposing"} {
		for _, tgt := range groupForwardTargets(t, env, g) {
			if tgt == "k-20261201-rnn" {
				// derives→rnn 与 supports→rnn 都不应以 derives 身份出现；这里 rnn 默认应被隐藏。
				t.Fatalf("%s.forward 出现 rnn（默认应因 deprecated 隐藏）：%v", g, groupForwardTargets(t, env, g))
			}
		}
	}
	// 既有 card/search JSON 键集合零扩张（CLI 侧再核一次，与 query 侧 TestOpinionShowDoesNotExpand 呼应）。
	cardKeys := dataKeysFromCLI(t, dir, "card", "show", "k-20261201-attention")
	wantCard := "id,title,domain,status,deprecated,created_at,updated_at,path,tags," +
		"markers,sections,sources,relations_out,relations_in,deleted,unreviewed"
	if strings.Join(cardKeys, ",") != wantCard {
		t.Fatalf("card show data 键被 opinion 接通污染：%v", cardKeys)
	}
}

// —— ② 文本模式：显式显示 validation / 五分区 / sources / 三组正反向（空段写「无」）+ 悬空标注 ——

func TestOpinionShowTextRendersAllFacets(t *testing.T) {
	dir := opinionShowVault(t)
	code, out, errOut := runOpinionCLI(t, dir, "show", ocsRichID)
	if code != ExitOK {
		t.Fatalf("opinion show 退出码 = %d，期望 0：%s", code, errOut)
	}
	for _, want := range []string{
		"validation=pending",
		ocsClaimTok, ocsArgTok, ocsCounterTok, ocsVerifyTok, ocsAppendTok, // 五分区正文
		"材料出处：s-20261207-paper",
		"支持·正向：", "限制·正向：", "反对·正向：",
		"支持·反向：无", "限制·反向：无", "反对·反向：无", // 反向段空写「无」
		"（目标不存在）", // opposing→missing 悬空标注
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("文本模式缺事实 %q：\n%s", want, out)
		}
	}
	// 朴素观点：三组正反六段都写「无」，sources 写「无」。
	pcode, pout, _ := runOpinionCLI(t, dir, "show", ocsPlainID)
	if pcode != ExitOK {
		t.Fatalf("朴素观点 show 退出码 = %d", pcode)
	}
	for _, want := range []string{"支持·正向：无", "支持·反向：无", "材料出处 sources[]：无", "validation=validated"} {
		if !strings.Contains(pout, want) {
			t.Fatalf("朴素观点文本缺 %q：\n%s", want, pout)
		}
	}
}

// —— ③ 对端 deprecated 默认隐藏并计 Q4，--include-deprecated 才展示且无 Q4 ——

func TestOpinionShowDeprecatedDefaultHiddenThenInclude(t *testing.T) {
	dir := opinionShowVault(t)

	// 默认：rnn 隐藏，warnings 恰含 Q4。
	_, env, _ := runOpinionShowJSON(t, dir, ocsRichID)
	if countStr(warnCodes(t, env), query.CodeQ4) != 1 {
		t.Fatalf("默认视图应恰一条 Q4（隐藏 supports→rnn），实际 %v", warnCodes(t, env))
	}
	// --include-deprecated：rnn 现身 supports 正向（升序末位），无 Q4。
	_, envInc, _ := runOpinionShowJSON(t, dir, ocsRichID, "--include-deprecated")
	if got := groupForwardTargets(t, envInc, "supports"); strings.Join(got, ",") !=
		"k-20261201-attention,k-20261201-ops,k-20261201-rnn" {
		t.Fatalf("--include-deprecated 下 supports.forward = %v，期望三条含 rnn", got)
	}
	if countStr(warnCodes(t, envInc), query.CodeQ4) != 0 {
		t.Fatalf("--include-deprecated 放开后不得再有 Q4，实际 %v", warnCodes(t, envInc))
	}
	// 文本模式：放开后应出现对端 [失效] 标记（rnn 已显示）。
	_, out, _ := runOpinionCLI(t, dir, "show", ocsRichID, "--include-deprecated")
	if !strings.Contains(out, "k-20261201-rnn") {
		t.Fatalf("--include-deprecated 文本应显示 rnn 边：\n%s", out)
	}
}

// —— ② 悬空目标 + Q2（观点来源）——

func TestOpinionShowMissingTargetProducesQ2(t *testing.T) {
	dir := opinionShowVault(t)
	_, env, _ := runOpinionShowJSON(t, dir, ocsRichID)
	codes := warnCodes(t, env)
	if countStr(codes, query.CodeQ2) < 1 {
		t.Fatalf("opposing→missing 悬空应产 Q2，实际诊断 %v", codes)
	}
	// 文本模式据 MissingTargets 标注「（目标不存在）」（MissingTargets 仅供文本、不进 data）。
	_, out, _ := runOpinionCLI(t, dir, "show", ocsRichID)
	if !strings.Contains(out, "k-20261201-missing") || !strings.Contains(out, "（目标不存在）") {
		t.Fatalf("文本应标注悬空目标 k-20261201-missing（目标不存在）：\n%s", out)
	}
}

// —— ④ 一个全局 limit/offset 跨六段，截断产恰一条 W25，并有分页摘要行 ——

func TestOpinionShowGlobalPaginationOneW25(t *testing.T) {
	dir := opinionShowVault(t)
	// 默认可见正向合计 = supports(2) + limits(1) + opposing(2) = 5；limit=2 截断。
	_, env, _ := runOpinionShowJSON(t, dir, ocsRichID, "--limit", "2")
	if countStr(warnCodes(t, env), query.CodeResultTruncated) != 1 {
		t.Fatalf("limit=2 应截断并产恰一条 W25，实际诊断 %v", warnCodes(t, env))
	}
	// 首页前 2 条落在 supports.forward（group-major、正向在前）。
	if got := groupForwardTargets(t, env, "supports"); strings.Join(got, ",") !=
		"k-20261201-attention,k-20261201-ops" {
		t.Fatalf("limit=2 首页 supports.forward = %v，期望 [attention, ops]", got)
	}
	// 文本模式带分页摘要行（本页 N 条 / 共 5 条）。
	_, out, _ := runOpinionCLI(t, dir, "show", ocsRichID, "--limit", "2")
	if !strings.Contains(out, "共 5 条") {
		t.Fatalf("文本分页摘要应说明共 5 条：\n%s", out)
	}
	// offset 超界：空结果、退 0、无 W25。
	code, env2, errOut := runOpinionShowJSON(t, dir, ocsRichID, "--limit", "3", "--offset", "99")
	if code != ExitOK {
		t.Fatalf("offset 超界应退 0，实际 %d：%s", code, errOut)
	}
	if countStr(warnCodes(t, env2), query.CodeResultTruncated) != 0 {
		t.Fatalf("offset 超界不应产 W25，实际 %v", warnCodes(t, env2))
	}
}

// —— ⑤ 已删除观点仍可显式查看并标 [已删除]；wrong kind / 不存在 → 退 1 ——

func TestOpinionShowDeletedAndErrorSemantics(t *testing.T) {
	dir := opinionShowVault(t)

	// 已删除观点：退 0、Deleted=true、markers 含 [已删除]、文本带标记。
	code, env, errOut := runOpinionShowJSON(t, dir, ocsDeadID)
	if code != ExitOK {
		t.Fatalf("已删除观点应可显式查看（退 0），实际 %d：%s", code, errOut)
	}
	data, _ := env["data"].(map[string]interface{})
	if data["deleted"] != true {
		t.Fatalf("已删除观点 deleted 应为 true，实际 %v", data["deleted"])
	}
	markers, _ := data["markers"].([]interface{})
	joined := fmt.Sprint(markers...)
	if !strings.Contains(joined, "已删除") {
		t.Fatalf("已删除观点 markers 应含 [已删除]，实际 %v", markers)
	}

	// wrong kind：k-* 形态非法（不是 o-*）→ 退 1（Validate 阶段拦下）。
	if c, _, _ := runOpinionCLI(t, dir, "show", "k-20261201-attention"); c != ExitUsage {
		t.Fatalf("opinion show k-* 应退 1（ID 形态非法），实际 %d", c)
	}
	// 形态合法但不存在 → 退 1，报「观点不存在」（非 NotWired）。
	c, _, e := runOpinionCLI(t, dir, "show", "o-20261299-none")
	if c != ExitUsage {
		t.Fatalf("不存在的观点应退 1，实际 %d", c)
	}
	if !strings.Contains(e, "不存在") || strings.Contains(e, "尚未挂载") {
		t.Fatalf("不存在应报「观点不存在」（非 NotWired），实际：%s", e)
	}
}

// —— ⑥ card show 收到合法 o-* 在 Validate 阶段退 1 并指引 opinion show（不读 vault）——

func TestCardShowRedirectsValidOpinionID(t *testing.T) {
	dir := opinionShowVault(t)
	statusBefore, logBefore := opinionVaultSnapshot(t, dir)

	// 合法 o-*（且库中真实存在）：card show 不去读它，Validate 阶段就退 1 并指引改用 opinion show。
	r := newTestRoot(t, dir)
	code, out, errOut := runCLI(t, r, "--vault", dir, "card", "show", ocsRichID)
	if code != ExitUsage {
		t.Fatalf("card show o-* 应退 1（Validate 指引），实际 %d：%s / %s", code, out, errOut)
	}
	if !strings.Contains(errOut, "eg opinion show") {
		t.Fatalf("card show o-* 的错误应指引改用 eg opinion show：%s", errOut)
	}
	if statusAfter, logAfter := opinionVaultSnapshot(t, dir); statusAfter != statusBefore || logAfter != logBefore {
		t.Fatal("card show o-* 走 Validate 拦截：不得读 vault / 改动工作区 / 产生 commit")
	}

	// 负控：其它 k-id 行为不变——不存在的 k-* 仍落到 query 层报「不存在」，绝不指引 opinion show。
	kcode, _, kerr := runCLI(t, newTestRoot(t, dir), "--vault", dir, "card", "show", "k-20261299-none")
	if kcode != ExitUsage {
		t.Fatalf("card show 不存在 k-* 应退 1，实际 %d", kcode)
	}
	if strings.Contains(kerr, "eg opinion show") {
		t.Fatalf("card show 的 k-* 路径不应出现 opinion show 指引：%s", kerr)
	}
}

// —— ⑦ flag 精确分域：show 拒检索过滤 flag、search 拒 include-deprecated、validate/reject 拒全部读 flag ——

func TestOpinionFlagDomainPartitioning(t *testing.T) {
	dir := opinionShowVault(t)
	statusBefore, logBefore := opinionVaultSnapshot(t, dir)

	// show 拒检索过滤 flag（domain / tag / since / until / include-deleted），逐字点名该 flag。
	for _, f := range [][]string{
		{"--domain", "ai-infra"}, {"--tag", "x"}, {"--since", "2026-01-01"},
		{"--until", "2026-12-31"}, {"--include-deleted"},
	} {
		args := append([]string{"show", ocsRichID}, f...)
		code, _, errOut := runOpinionCLI(t, dir, args...)
		if code != ExitUsage {
			t.Fatalf("opinion show %v 应退 1（show 不吃检索过滤 flag），实际 %d：%s", f, code, errOut)
		}
		flagName := strings.TrimPrefix(f[0], "--")
		if !strings.Contains(errOut, flagName) {
			t.Fatalf("opinion show 拒 %s 的错误应逐字点名该 flag：%s", flagName, errOut)
		}
	}
	// show 接受 include-deprecated / limit / offset（退 0）。
	if c, _, e := runOpinionCLI(t, dir, "show", ocsRichID, "--include-deprecated", "--limit", "3", "--offset", "0"); c != ExitOK {
		t.Fatalf("opinion show 应接受 include-deprecated/limit/offset（退 0），实际 %d：%s", c, e)
	}
	// search 拒 include-deprecated（show 专属可见性开关）。
	if c, _, e := runOpinionCLI(t, dir, "search", "注意力", "--include-deprecated"); c != ExitUsage ||
		!strings.Contains(e, "include-deprecated") {
		t.Fatalf("opinion search 应拒 include-deprecated 并点名，实际 code=%d err=%s", c, e)
	}
	// validate / reject 拒全部读 flag（检索过滤 / 可见性 / 分页）。
	for _, sub := range []string{"validate", "reject"} {
		for _, f := range [][]string{{"--domain", "ai-infra"}, {"--include-deprecated"}, {"--limit", "3"}} {
			args := append([]string{sub, ocsRichID}, f...)
			code, _, errOut := runOpinionCLI(t, dir, args...)
			if code != ExitUsage {
				t.Fatalf("opinion %s %v 应退 1（拒全部读 flag），实际 %d：%s", sub, f, code, errOut)
			}
		}
	}
	if statusAfter, logAfter := opinionVaultSnapshot(t, dir); statusAfter != statusBefore || logAfter != logBefore {
		t.Fatal("flag 分域拒绝路径必须零写入零 commit")
	}
}

// —— ⑧ healthy 索引与 missing-index 降级投影等价、降级码精确 ——

func TestOpinionShowHealthyMissingEquivalence(t *testing.T) {
	dir := opinionShowVault(t)

	// missing：尚未建索引 → 走全量扫描 + W23 + Q5。
	_, missEnv, _ := runOpinionShowJSON(t, dir, ocsRichID)
	mc := warnCodes(t, missEnv)
	if countStr(mc, codeIndexMissing) != 1 || countStr(mc, query.CodeQ5) != 1 {
		t.Fatalf("缺索引降级应恰 W23×1 + Q5×1，实际 %v", mc)
	}
	if countStr(mc, codeIndexStale) != 0 || countStr(mc, codeIndexCorrupt) != 0 {
		t.Fatalf("缺索引不应出现 W22/W24，实际 %v", mc)
	}
	missData, _ := json.Marshal(missEnv["data"])

	// 提交语料并建一次含观点的全量索引（healthy 路径）。
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "-c", "user.name=eg-test", "-c", "user.email=eg-test@example.com",
		"commit", "-q", "-m", "opinion show corpus")
	if c, _, e := runCLI(t, newTestRoot(t, dir), "--vault", dir, "index", "build"); c != ExitOK {
		t.Fatalf("eg index build 应退 0，实际 %d：%s", c, e)
	}

	// healthy：走索引后端，四个降级码全零。
	_, healEnv, _ := runOpinionShowJSON(t, dir, ocsRichID)
	hc := warnCodes(t, healEnv)
	for _, c := range []string{codeIndexStale, codeIndexMissing, codeIndexCorrupt, query.CodeQ5} {
		if countStr(hc, c) != 0 {
			t.Fatalf("healthy 索引不应有降级码 %s，实际 %v", c, hc)
		}
	}
	healData, _ := json.Marshal(healEnv["data"])
	// 投影逐字等价（data 载荷完全相同：两条后端不得分叉）。
	if string(missData) != string(healData) {
		t.Fatalf("healthy 与 missing 的 data 投影不等价：\nmissing=%s\nhealthy=%s", missData, healData)
	}
}
