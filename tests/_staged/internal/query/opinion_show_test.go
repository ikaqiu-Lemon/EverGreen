package query

// [B2a 消费面] query-only 的**单条观点视图**（Opinion show 投影）。
//
// 本支只钉「读路径把一条观点投影成什么」，不接任何 CLI（B2a 边界：不碰 opinion_cmd.go）。
// 五件事逐条钉死，且全部**真实走 vault → scan → index**（复用 backend_test / index_backed_
// opinion_test 的语料与建库脚手架，绝不用伪造 DTO 掩盖链路）：
//
//	① 权威 Markdown 五分区（OpinionSections 固定序）+ validation + markers + sources；
//	② supports / limits / opposing 三组，每组各有「正向（以该观点为起点）」与
//	   「反向（以该观点为终点）」两段；derives **不进**这三组；
//	③ 复用 card show 的可见性 / 确定性排序 / **一个全局** limit/offset 分页口径
//	   （对端 deprecated 默认隐藏并计 Q4，已删除对端任何 flag 下都隐藏；跨段截断只产一条 W25）；
//	④ healthy 索引与 missing-index 降级（W23 + Q5）结果**逐字等价**，healthy 无降级诊断；
//	⑤ 观点 ID 形态 / 不存在 / 已删除语义，以及既有 card / search JSON 键集合**零扩张**。
//
// # 端点写模型边界（B2a 的最小定向检查结论，写进用例而不是顺手改模型）
//
// model.Relation.Target 的落盘类型是 model.CardID：观点是关系的**持有方**，正向边
// `o-* → k-*` 是当前写模型能表达的真实事实（scan.OpinionEntry.Relations）；而
// 「卡侧不写回」（scan.go 对 OpinionEntry.Relations 的注释）意味着当前写模型**无法**
// 令任何产物以某观点为 target（`add_relation` 的 from/target 都过 ParseCardID，只认 k-*）。
// 因此本批把反向段实现为「全库扫描 target==o-id」的通用机制，但在写模型忠实的语料上它
// **恒为空**——这一边界在 TestShowOpinionReverseGroupsEmptyUnderWriteModel 里逐字钉死，
// 端点扩展（让产物以观点为终点）留 B2c，本批不改 model/store/plan/reconcile/index 写模型。

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// —— 语料令牌：知识卡语料里绝不出现的唯一串，用来逐字反证「五分区取的是权威正文」——
const (
	osClaimTok   = "zopinionclaimq"
	osArgTok     = "zopinionargq"
	osCounterTok = "zopinioncounterq"
	osVerifyTok  = "zopinionverifyq"
	osAppendTok  = "zopinionappendq"
)

const osRichID = "o-20261207-scaling"

// osRichOpinion 造一条**关系覆盖面完整**的观点：
//   - derives → rnn（必须被三组排除）；
//   - supports → ops / attention / rnn（rnn 已 deprecated：默认隐藏 + 计 Q4）；
//   - limits → sched（跨域 active：可见）；
//   - opposing → attention / missing（missing 悬空：仍作为端点保留，不静默丢）。
//
// 五分区正文各埋一个唯一令牌；sources[] 非空（材料层引用）。所有 target 都指向 bkVault
// 里**真实存在**（或刻意悬空）的卡，避免与本支无关的诊断噪声。
func osRichOpinion() string {
	return "---\n" +
		"id: " + osRichID + "\n" +
		"status: active\n" +
		"created_at: '2026-12-07'\n" +
		"updated_at: '2026-12-08T10:00:00+08:00'\n" +
		"title: 缩放注意力的经济性\n" +
		"validation: pending\n" +
		"sources:\n" +
		"  - source: s-20261207-paper\n" +
		"    note: n-20261207-note\n" +
		"    rel: support\n" +
		"    reason: 原始论文佐证\n" +
		"relations:\n" +
		"  - type: derives\n    target: k-20261201-rnn\n    reason: 从循环网络演进而来\n" +
		"  - type: supports\n    target: k-20261201-ops\n    reason: 算子层支撑该主张\n" +
		"  - type: supports\n    target: k-20261201-attention\n    reason: 注意力卡直接支撑\n" +
		"  - type: supports\n    target: k-20261201-rnn\n    reason: 循环网络卡亦支撑（该卡已失效）\n" +
		"  - type: limits\n    target: k-20261201-sched\n    reason: 调度视角构成限制\n" +
		"  - type: opposing\n    target: k-20261201-attention\n    reason: 也存在反对论证\n" +
		"  - type: opposing\n    target: k-20261201-missing\n    reason: 指向一张不存在的卡\n" +
		"---\n\n" +
		"# 缩放注意力的经济性\n\n" +
		"## 观点\n\n主张：缩放注意力在长序列下不经济 " + osClaimTok + "。\n\n" +
		"## 论据与推理\n\n推理链条 " + osArgTok + "。\n\n" +
		"## 条件与反例\n\n反例：短序列 " + osCounterTok + "。\n\n" +
		"## 待验证\n\n待验证项 " + osVerifyTok + "。\n\n" +
		"## 用户补充\n\n用户补充 " + osAppendTok + "。\n"
}

const osPlainID = "o-20261207-plain"

// osPlainOpinion 造一条**无关系、无 sources、只有必填分区**的最小观点：
// 用来钉「三组六段都是 `[]` 而非 nil、sources 为 `[]`、缺分区键仍在值为空串」。
func osPlainOpinion() string {
	return "---\n" +
		"id: " + osPlainID + "\n" +
		"status: active\n" +
		"created_at: '2026-12-07'\n" +
		"updated_at: '2026-12-07T10:00:00+08:00'\n" +
		"title: 朴素观点\n" +
		"validation: validated\n" +
		"sources: []\n" +
		"---\n\n" +
		"# 朴素观点\n\n" +
		"## 观点\n\n只有一个必填分区。\n"
}

const osDeadID = "o-20261207-dead"

// osDeadOpinion 造一条**已删除**（deleted_at 有值）观点：钉删除语义（仍可显式查看，带 [已删除]）。
func osDeadOpinion() string {
	return "---\n" +
		"id: " + osDeadID + "\n" +
		"status: active\n" +
		"created_at: '2026-12-07'\n" +
		"updated_at: '2026-12-08T10:00:00+08:00'\n" +
		"deleted_at: '2026-12-09T10:00:00+08:00'\n" +
		"deleted_reason: 结论被推翻\n" +
		"title: 已删除的观点\n" +
		"validation: rejected\n" +
		"sources: []\n" +
		"---\n\n" +
		"# 已删除的观点\n\n" +
		"## 观点\n\n主张一句。\n"
}

// osVault 在 bkVault（含 deprecated 卡 rnn、悬空引用、坏卡）之上落三条观点，返回 vault 根。
func osVault(t *testing.T) string {
	t.Helper()
	root := bkVault(t)
	bkWrite(t, root, store.OpinionRel("ai-infra", osRichID), osRichOpinion())
	bkWrite(t, root, store.OpinionRel("ai-infra", osPlainID), osPlainOpinion())
	bkWrite(t, root, store.OpinionRel("ai-infra", osDeadID), osDeadOpinion())
	return root
}

// osShow 是注入 bkDeps 后的 Opinion show（生产等价调用形态；分页取零值＝不限量）。
func osShow(root string, id string, opts ...VisibilityPolicy) (*OpinionShowResult, error) {
	return ShowOpinionPaged(root, model.OpinionID(id), bkDeps, PageSpec{}, opts...)
}

// targetsOf 抽出一段关系边的 target 序列（正向段断言用）。
func targetsOf(edges []RelationEdge) []string {
	out := []string{}
	for _, e := range edges {
		out = append(out, e.Target)
	}
	return out
}

// codesOf 抽出诊断码序列。
func codesOf(diags []Diagnostic) []string {
	out := []string{}
	for _, d := range diags {
		out = append(out, d.Code)
	}
	return out
}

func hasCode(diags []Diagnostic, code string) bool {
	for _, d := range diags {
		if d.Code == code {
			return true
		}
	}
	return false
}

func countCode(diags []Diagnostic, code string) int {
	n := 0
	for _, d := range diags {
		if d.Code == code {
			n++
		}
	}
	return n
}

// TestShowOpinionSectionsValidationMarkersSources —— ①：五分区（固定序）+ validation +
// markers + sources 都取自权威 Markdown。
func TestShowOpinionSectionsValidationMarkersSources(t *testing.T) {
	root := osVault(t)
	res, err := osShow(root, osRichID)
	if err != nil {
		t.Fatalf("Opinion show 失败：%v", err)
	}
	o := res.Opinion
	if o.ID != osRichID {
		t.Fatalf("ID = %q，期望 %q", o.ID, osRichID)
	}
	if o.Validation != "pending" {
		t.Fatalf("validation = %q，期望 pending", o.Validation)
	}
	// 分区键序恒为 OpinionSections()（五分区声明序），且缺一不可。
	if got, want := strings.Join(o.Sections.Keys(), "|"),
		strings.Join(mdfile.OpinionSections(), "|"); got != want {
		t.Fatalf("分区键序 = %q，期望 %q", got, want)
	}
	// 五分区正文各含其唯一令牌（逐字取权威字节，不是从别处拼）。
	toks := map[string]string{
		mdfile.SecOpinionClaim: osClaimTok, mdfile.SecArgument: osArgTok,
		mdfile.SecCounter: osCounterTok, mdfile.SecToVerify: osVerifyTok,
		mdfile.SecUserAppend: osAppendTok,
	}
	for sec, tok := range toks {
		if !strings.Contains(o.Sections.Get(sec), tok) {
			t.Fatalf("分区 %q 正文 = %q，应含令牌 %q", sec, o.Sections.Get(sec), tok)
		}
	}
	// active 且未删除：无标记。
	if len(o.Markers) != 0 {
		t.Fatalf("markers = %v，active 未删除观点应为空", o.Markers)
	}
	// sources[] 逐字来自 frontmatter。
	if len(o.Sources) != 1 || string(o.Sources[0].Source) != "s-20261207-paper" ||
		string(o.Sources[0].Rel) != "support" {
		t.Fatalf("sources = %+v，期望恰一条 s-20261207-paper/support", o.Sources)
	}
}

// TestShowOpinionThreeGroupsForwardSortedAndDerivesExcluded —— ②③：三组正向段的可见性、
// 确定性排序、derives 排除；deprecated 对端默认隐藏并计 Q4。
func TestShowOpinionThreeGroupsForwardSortedAndDerivesExcluded(t *testing.T) {
	root := osVault(t)
	res, err := osShow(root, osRichID)
	if err != nil {
		t.Fatalf("Opinion show 失败：%v", err)
	}
	o := res.Opinion
	// supports 正向：rnn（deprecated）默认隐藏，余下按 target 升序。
	if got := targetsOf(o.Supports.Forward); strings.Join(got, ",") !=
		"k-20261201-attention,k-20261201-ops" {
		t.Fatalf("supports.forward = %v，期望 [attention, ops]（rnn 因 deprecated 隐藏）", got)
	}
	if got := targetsOf(o.Limits.Forward); strings.Join(got, ",") != "k-20261201-sched" {
		t.Fatalf("limits.forward = %v，期望 [sched]", got)
	}
	// opposing 正向：missing 悬空仍保留（悬空是诊断的事，不是端点可见性的事）。
	if got := targetsOf(o.Opposing.Forward); strings.Join(got, ",") !=
		"k-20261201-attention,k-20261201-missing" {
		t.Fatalf("opposing.forward = %v，期望 [attention, missing]", got)
	}
	// derives → rnn 绝不出现在任何一组。
	for _, grp := range [][]RelationEdge{
		o.Supports.Forward, o.Limits.Forward, o.Opposing.Forward} {
		for _, e := range grp {
			if e.Type == string(model.RelationDerives) {
				t.Fatalf("三组里出现了 derives 边：%+v", e)
			}
		}
	}
	// 隐藏了 1 个 deprecated 端点（supports→rnn）⇒ Q4 恰一条，默认 depPeers 为空。
	if res.HiddenDeprecated != 1 {
		t.Fatalf("HiddenDeprecated = %d，期望 1（supports→rnn）", res.HiddenDeprecated)
	}
	if countCode(res.Diagnostics, CodeQ4) != 1 {
		t.Fatalf("Q4 应恰一条，实际诊断 = %v", codesOf(res.Diagnostics))
	}
	if len(res.DeprecatedPeers) != 0 {
		t.Fatalf("默认视图 DeprecatedPeers 应为空，实际 %v", res.DeprecatedPeers)
	}

	// --include-deprecated：rnn 现身 supports 正向（升序末位），depPeers 记 rnn，无 Q4。
	res2, err := osShow(root, osRichID, VisibilityPolicy{IncludeDeprecated: true})
	if err != nil {
		t.Fatalf("Opinion show（含 deprecated）失败：%v", err)
	}
	if got := targetsOf(res2.Opinion.Supports.Forward); strings.Join(got, ",") !=
		"k-20261201-attention,k-20261201-ops,k-20261201-rnn" {
		t.Fatalf("含 deprecated 时 supports.forward = %v，期望三条含 rnn", got)
	}
	if strings.Join(res2.DeprecatedPeers, ",") != "k-20261201-rnn" {
		t.Fatalf("DeprecatedPeers = %v，期望 [rnn]", res2.DeprecatedPeers)
	}
	if hasCode(res2.Diagnostics, CodeQ4) {
		t.Fatalf("显式放开 deprecated 后不应有 Q4，实际 = %v", codesOf(res2.Diagnostics))
	}
}

// TestShowOpinionReverseGroupsEmptyUnderWriteModel —— ②/边界：反向段在**写模型忠实**的
// 语料上恒为空（「卡侧不写回」＋ target 只认 k-*）；端点扩展留 B2c。三组的正向/反向段
// 都必须是非 nil 的 JSON 数组（`[]`，绝不 `null`）。
func TestShowOpinionReverseGroupsEmptyUnderWriteModel(t *testing.T) {
	root := osVault(t)
	res, err := osShow(root, osRichID)
	if err != nil {
		t.Fatalf("Opinion show 失败：%v", err)
	}
	for name, grp := range map[string]RelationGroup{
		"supports": res.Opinion.Supports, "limits": res.Opinion.Limits,
		"opposing": res.Opinion.Opposing} {
		if grp.Reverse == nil {
			t.Fatalf("%s.reverse 为 nil，应为非 nil 空数组", name)
		}
		if len(grp.Reverse) != 0 {
			t.Fatalf("%s.reverse = %v，写模型忠实语料下应为空（无产物能以观点为 target）",
				name, targetsOf(grp.Reverse))
		}
		if grp.Forward == nil {
			t.Fatalf("%s.forward 为 nil，应为非 nil 空数组", name)
		}
	}
	// 朴素观点：六段全空但都是 `[]`，sources 为 `[]`，缺分区键仍在（值空串）。
	pres, err := osShow(root, osPlainID)
	if err != nil {
		t.Fatalf("Opinion show（朴素）失败：%v", err)
	}
	blob, err := json.Marshal(pres.Opinion)
	if err != nil {
		t.Fatalf("marshal：%v", err)
	}
	for _, frag := range []string{
		`"supports":{"forward":[],"reverse":[]}`,
		`"limits":{"forward":[],"reverse":[]}`,
		`"opposing":{"forward":[],"reverse":[]}`,
		`"sources":[]`,
	} {
		if !strings.Contains(string(blob), frag) {
			t.Fatalf("朴素观点 JSON 缺片段 %s：\n%s", frag, blob)
		}
	}
	if pres.Opinion.Sections.Get(mdfile.SecArgument) != "" {
		t.Fatalf("朴素观点缺失分区应为空串，实际 %q", pres.Opinion.Sections.Get(mdfile.SecArgument))
	}
}

// TestShowOpinionGlobalPaginationOneW25 —— ③：一个全局 limit/offset 跨六段生效，
// total 恒为分页前总数，截断只产**恰一条** W25；offset 超界返回空且不报错。
func TestShowOpinionGlobalPaginationOneW25(t *testing.T) {
	root := osVault(t)
	// 默认可见正向合计 = supports(2) + limits(1) + opposing(2) = 5。
	res, err := ShowOpinionPaged(root, model.OpinionID(osRichID), bkDeps, PageSpec{Limit: 2})
	if err != nil {
		t.Fatalf("Opinion show（limit=2）失败：%v", err)
	}
	if res.Page.Total != 5 {
		t.Fatalf("Page.Total = %d，期望 5（分页前可见关系总数）", res.Page.Total)
	}
	if res.Page.Returned != 2 {
		t.Fatalf("Page.Returned = %d，期望 2", res.Page.Returned)
	}
	// 段序 group-major、正向在前：前 2 条落在 supports.forward。
	if got := targetsOf(res.Opinion.Supports.Forward); strings.Join(got, ",") !=
		"k-20261201-attention,k-20261201-ops" {
		t.Fatalf("limit=2 首页 supports.forward = %v，期望 [attention, ops]", got)
	}
	total := len(res.Opinion.Supports.Forward) + len(res.Opinion.Limits.Forward) +
		len(res.Opinion.Opposing.Forward)
	if total != 2 {
		t.Fatalf("六段合计返回 %d 条，期望恰 2（一个全局上限，不是每段各 2）", total)
	}
	if !res.Page.Truncated || countCode(res.Diagnostics, CodeResultTruncated) != 1 {
		t.Fatalf("应截断且恰一条 W25，实际 truncated=%v diags=%v",
			res.Page.Truncated, codesOf(res.Diagnostics))
	}
	// offset 超界：空结果、退 0（不报错、不 W25）。
	res2, err := ShowOpinionPaged(root, model.OpinionID(osRichID), bkDeps, PageSpec{Limit: 3, Offset: 99})
	if err != nil {
		t.Fatalf("Opinion show（offset 超界）不应报错：%v", err)
	}
	if res2.Page.Returned != 0 || hasCode(res2.Diagnostics, CodeResultTruncated) {
		t.Fatalf("offset 超界应空且无 W25，实际 returned=%d diags=%v",
			res2.Page.Returned, codesOf(res2.Diagnostics))
	}
	if res2.Page.Total != 5 {
		t.Fatalf("offset 超界 total 仍应为 5，实际 %d", res2.Page.Total)
	}
}

// TestShowOpinionHealthyMissingEquivalence —— ④：healthy 索引与 missing-index 降级的
// 投影**逐字等价**；missing 恰 W23 + Q5，healthy 无任何降级诊断。
func TestShowOpinionHealthyMissingEquivalence(t *testing.T) {
	root := osVault(t)

	// 先 missing（尚未建索引）：证不出→不，本例是**索引缺失**，走扫描 + W23 + Q5。
	missing, err := osShow(root, osRichID)
	if err != nil {
		t.Fatalf("Opinion show（missing index）失败：%v", err)
	}
	if missing.backend.UseIndex() {
		t.Fatalf("缺索引时不应走索引后端，实际 kind=%s", missing.backend.Kind)
	}
	if !hasCode(missing.Diagnostics, "W23") || !hasCode(missing.Diagnostics, CodeQ5) {
		t.Fatalf("缺索引降级应含 W23 + Q5，实际 %v", codesOf(missing.Diagnostics))
	}

	// 再 healthy：建一次含观点的全量索引。
	bkoBuildIndexWithOpinions(t, root)
	healthy, err := osShow(root, osRichID)
	if err != nil {
		t.Fatalf("Opinion show（healthy index）失败：%v", err)
	}
	if !healthy.backend.UseIndex() {
		t.Fatalf("健康索引应走索引后端，实际 kind=%s reason=%s", healthy.backend.Kind, healthy.backend.Reason)
	}
	if hasCode(healthy.Diagnostics, CodeQ5) || hasCode(healthy.Diagnostics, "W23") ||
		hasCode(healthy.Diagnostics, "W22") || hasCode(healthy.Diagnostics, "W24") {
		t.Fatalf("健康索引不应有任何降级诊断，实际 %v", codesOf(healthy.Diagnostics))
	}

	// 投影逐字等价（marshal 后逐字节比对：五分区 / validation / markers / sources / 三组皆同）。
	mb, _ := json.Marshal(missing.Opinion)
	hb, _ := json.Marshal(healthy.Opinion)
	if string(mb) != string(hb) {
		t.Fatalf("healthy 与 missing 投影不等价：\nmissing=%s\nhealthy=%s", mb, hb)
	}
	// 计数守恒也应等价（两条后端不得分叉）。
	if missing.ScannedFiles != healthy.ScannedFiles || missing.SkippedFiles != healthy.SkippedFiles {
		t.Fatalf("两后端计数分叉：missing scanned=%d skipped=%d，healthy scanned=%d skipped=%d",
			missing.ScannedFiles, missing.SkippedFiles, healthy.ScannedFiles, healthy.SkippedFiles)
	}
}

// TestShowOpinionIDSemantics —— ⑤：观点 ID 形态 / 不存在 / 已删除语义。
func TestShowOpinionIDSemantics(t *testing.T) {
	root := osVault(t)

	// 形态非法：知识卡 ID（k-*）或垃圾串都必须 ErrInvalidOpinionID，且零结果。
	for _, bad := range []string{"k-20261201-attention", "o-bad", "garbage", ""} {
		if res, err := osShow(root, bad); err == nil || res != nil {
			t.Fatalf("非法观点 ID %q 应报错且零结果，实际 res=%v err=%v", bad, res, err)
		}
	}
	// 形态合法但库中不存在：ErrOpinionNotFound。
	if _, err := osShow(root, "o-20261299-none"); err == nil {
		t.Fatalf("不存在的观点应报 ErrOpinionNotFound，实际 nil")
	}
	// 已删除观点：仍可显式查看，Deleted=true 且 markers 含 [已删除]。
	dead, err := osShow(root, osDeadID)
	if err != nil {
		t.Fatalf("已删除观点应可显式查看：%v", err)
	}
	if !dead.Opinion.Deleted {
		t.Fatalf("已删除观点 Deleted 应为 true")
	}
	if strings.Join(dead.Opinion.Markers, "") != MarkerDeleted {
		t.Fatalf("已删除观点 markers = %v，应恰含 %q", dead.Opinion.Markers, MarkerDeleted)
	}
}

// TestOpinionShowDoesNotExpandExistingContracts —— ⑤：既有 card / search JSON 键集合逐字不变；
// 新投影的键序自洽且与既有合同解耦。
func TestOpinionShowDoesNotExpandExistingContracts(t *testing.T) {
	// card show data 键仍是冻结的 16 键（一字不改、不扩张）。
	wantCard := "id,title,domain,status,deprecated,created_at,updated_at,path,tags," +
		"markers,sections,sources,relations_out,relations_in,deleted,unreviewed"
	if got := strings.Join(CardDataKeys(), ","); got != wantCard {
		t.Fatalf("CardDataKeys 被改动：\n got=%s\nwant=%s", got, wantCard)
	}
	// search hits[] / data 键仍是冻结集合。
	if got := strings.Join(SearchHitKeys(), ","); got !=
		"id,title,domain,tags,status,deprecated,updated_at,created_at,path,matched_fields,score,deleted" {
		t.Fatalf("SearchHitKeys 被改动：%s", got)
	}
	if got := strings.Join(SearchDataKeys(), ","); got != "hits,total,scanned_files,skipped_files" {
		t.Fatalf("SearchDataKeys 被改动：%s", got)
	}
	// Opinion 投影键序：结构体字段序即键序，marshal 出来必须逐字等于 OpinionDataKeys()。
	root := osVault(t)
	res, err := osShow(root, osPlainID)
	if err != nil {
		t.Fatalf("Opinion show 失败：%v", err)
	}
	blob, err := json.Marshal(res.Opinion)
	if err != nil {
		t.Fatalf("marshal：%v", err)
	}
	var order []string
	dec := json.NewDecoder(strings.NewReader(string(blob)))
	if _, err := dec.Token(); err != nil { // 读掉开头 '{'
		t.Fatalf("decode token：%v", err)
	}
	depth := 1
	for dec.More() && depth == 1 {
		tk, err := dec.Token()
		if err != nil {
			t.Fatalf("decode key：%v", err)
		}
		key, ok := tk.(string)
		if !ok {
			break
		}
		order = append(order, key)
		// 跳过该键的值（可能是对象/数组/标量）。
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			t.Fatalf("decode value：%v", err)
		}
	}
	if strings.Join(order, ",") != strings.Join(OpinionDataKeys(), ",") {
		t.Fatalf("Opinion 投影键序 = %v，期望 %v", order, OpinionDataKeys())
	}
}

// TestApplyPageGroupsEqualsGlobalApplyPage —— ③ 分页器的**构造性等价**（page.go 的
// ApplyPageGroups 文档承诺）：拼接各返回段 == ApplyPage(concat(输入各段)) 的第一个返回值，
// 且 Page 事实逐格相等。覆盖不限量 / 首页 / 带 offset 跨段 / 大 limit / offset 超界等边界，
// 并钉每个返回段恒为非 nil（`[]` 而非 `null`）。
func TestApplyPageGroupsEqualsGlobalApplyPage(t *testing.T) {
	mk := func(prefix string, n int) []RelationEdge {
		s := []RelationEdge{}
		for i := 0; i < n; i++ {
			s = append(s, RelationEdge{From: prefix, Type: "supports",
				Target: fmt.Sprintf("%s-%02d", prefix, i)})
		}
		return s
	}
	segs := [][]RelationEdge{mk("a", 2), {}, mk("b", 1), mk("c", 3), {}, mk("d", 2)}
	merged := []RelationEdge{}
	for _, s := range segs {
		merged = append(merged, s...)
	}
	for _, p := range []PageSpec{
		{}, {Limit: 3}, {Limit: 3, Offset: 2}, {Limit: 100}, {Offset: 99, Limit: 5}, {Limit: 1}} {
		got, pg := ApplyPageGroups(segs, p)
		flat := []RelationEdge{}
		for _, s := range got {
			if s == nil {
				t.Fatalf("p=%+v：返回段为 nil，应为非 nil `[]`", p)
			}
			flat = append(flat, s...)
		}
		want, wpg := ApplyPage(merged, p)
		if len(flat) != len(want) {
			t.Fatalf("p=%+v：拼接返回 %d 条 ≠ 全局 ApplyPage %d 条", p, len(flat), len(want))
		}
		for i := range want {
			if flat[i] != want[i] {
				t.Fatalf("p=%+v：第 %d 条分叉 got=%+v want=%+v", p, i, flat[i], want[i])
			}
		}
		if pg != wpg {
			t.Fatalf("p=%+v：Page 事实分叉 got=%+v want=%+v", p, pg, wpg)
		}
	}
}
