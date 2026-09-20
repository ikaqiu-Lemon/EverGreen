package plan

// note_coverage_test.go —— T-evergreen.knowledge_opinion_split-158614-012 · T12-4
// 「extraction_coverage 语义校验」的**先红**判据（Schema v2 契约 §4.2.3）。
//
// 本批**只**加严 `plan_version: 2` 且 blocks[] 给出（BlocksGiven）的 write_note：
//   - 该路径必须显式给出**非空** extraction_coverage[]；缺字段或空数组 → 整条 op 字段级 E2、零 action；
//   - 每条覆盖项：module TrimSpace 非空且在本 Note 内按**原始字节**唯一；
//     source_refs 每项非空、且精确回指本次 role:source 块声明的 source_ref（不按 multiset、不排序、
//     不去重、不重排）；summary TrimSpace 非空；disposition 封闭三值 outputs|note_only|missing；
//       · outputs：outputs 至少 1 个、每个必须是形态完整的 k-*/o-* 端点，reason 必须为空；
//       · note_only：outputs 必须为空，reason TrimSpace 非空；
//       · missing：outputs 必须为空，reason TrimSpace 非空，且**无条件 E2 阻止 apply**（缺漏必须为 0）；
//   - 每个 source 块的 source_ref 至少进入一个覆盖模块；同一 ref / 同一 output 可进入多个模块（合法）；
//   - 与 output_cards 成员**双向一致**（不按次数）：每个 coverage.outputs 必须出现在 output_cards.card；
//     每张 output_cards.card 必须至少被一个 disposition=outputs 的模块引用；output_cards 中空值 /
//     非法 / 非 k/o ID 一律 E2；
//   - v1 sections / v1 blocks / v2 sections 兼容路径**不受本校验波及**。
//
// 本文件对**语义裁决**先红；覆盖矩阵的确定性渲染与 parser round-trip 分别由
// internal/mdfile 与 internal/store 的 T12-4 判据锁定。这里只额外做一条**渲染接线**冒烟：
// 合法覆盖执行后，「提取结果」分区必须出现覆盖矩阵与机器锚点。

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
)

// —— 覆盖项 / op 构造 ——

// ncJSONArr 把字符串切片编成 JSON 数组字面量（逐字，不排序 / 不去重）。
func ncJSONArr(ss []string) string {
	parts := make([]string, len(ss))
	for i, s := range ss {
		parts[i] = fmt.Sprintf("%q", s)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// ncMod 造一条 extraction_coverage[] 项（六键齐全，声明序即契约字段序）。
func ncMod(module string, refs []string, summary, disposition string, outputs []string, reason string) string {
	return fmt.Sprintf(`{"module":%q,"source_refs":%s,"summary":%q,"disposition":%q,"outputs":%s,"reason":%q}`,
		module, ncJSONArr(refs), summary, disposition, ncJSONArr(outputs), reason)
}

// ncOp 造一条 v2 write_note op：blocks + omissions + output_cards + extraction_coverage。
// covGiven=false 表示**根本不给 extraction_coverage 字段**（考「v2 blocks 必须显式给覆盖」）。
func ncOp(noteID string, blocks, omissions, cards, coverage []string, covGiven bool) string {
	op := `{"op":"write_note","source":"` + covSrcID + `","note_id":"` + noteID +
		`","blocks":[` + strings.Join(blocks, ",") + `],"omissions":[` + strings.Join(omissions, ",") + `]` +
		`,"output_cards":[` + strings.Join(cards, ",") + `]`
	if covGiven {
		op += `,"extraction_coverage":[` + strings.Join(coverage, ",") + `]`
	}
	return op + `}`
}

// 合法基线的构件：两个 source 块（L1-L2 / L3-L4）恰覆盖 covBody4 的四个非空行；
// 两张产出卡（k / o 各一）；两条覆盖模块分别 outputs 到这两张卡、各引一段来源。
func ncBlocks() []string {
	return []string{covSB("L1-L2", "抄前两行。"), covSB("L3-L4", "抄后两行。")}
}
func ncCards() []string {
	return []string{outputCard("k-20260901-cov", "新建"), outputCard("o-20260901-cov", "新建")}
}
func ncCoverage() []string {
	return []string{
		ncMod("材料方法", []string{"L1-L2"}, "前两行讲方法", "outputs", []string{"k-20260901-cov"}, ""),
		ncMod("核心观点", []string{"L3-L4"}, "后两行给观点", "outputs", []string{"o-20260901-cov"}, ""),
	}
}

// ncValidate 用既有 Source（covBody4 布局）跑一次 v2 校验。
func ncValidate(t *testing.T, blocks, omissions, cards, coverage []string, covGiven bool) *Result {
	t.Helper()
	return covValidate(t, covBody4, ncOp("n-20261017-cov", blocks, omissions, cards, coverage, covGiven))
}

// ncReject 是所有 coverage 语义错误的**统一断言**：既钉死存在一条 E2@path，
// 又证明当前 write_note **零 action**——任一语义错误都必须让整条 op 不展开（零写入），
// 而不是只挑三例证明。凡断言某条覆盖语义被拒的用例，都走这个 helper，不再各写一遍零写入判断。
func ncReject(t *testing.T, res *Result, path string) Diagnostic {
	t.Helper()
	d := requireErrorAt(t, res, E2, path)
	if len(res.Actions) != 0 {
		t.Fatalf("coverage 语义错误必须零写入（path=%s）：%+v", path, res.Actions)
	}
	return d
}

// —— 合法基线 ——

func TestCoverageLegalBaselinePasses(t *testing.T) {
	res := ncValidate(t, ncBlocks(), nil, ncCards(), ncCoverage(), true)
	requireNoError(t, res)
	if len(res.Actions) == 0 {
		t.Fatal("合法 v2 覆盖矩阵应展开出写入 action")
	}
}

// —— 存在性：缺字段 / 空数组（整条 op 字段级 E2、零 action）——

func TestCoverageMissingFieldRejected(t *testing.T) {
	res := ncValidate(t, ncBlocks(), nil, ncCards(), nil, false)
	ncReject(t, res, "ops[0].extraction_coverage")
}

func TestCoverageEmptyArrayRejected(t *testing.T) {
	res := ncValidate(t, ncBlocks(), nil, ncCards(), []string{}, true)
	ncReject(t, res, "ops[0].extraction_coverage")
}

// —— module ——

func TestCoverageModuleEmptyRejected(t *testing.T) {
	cov := []string{
		ncMod("  ", []string{"L1-L2"}, "前两行", "outputs", []string{"k-20260901-cov"}, ""),
		ncMod("核心观点", []string{"L3-L4"}, "后两行", "outputs", []string{"o-20260901-cov"}, ""),
	}
	res := ncValidate(t, ncBlocks(), nil, ncCards(), cov, true)
	ncReject(t, res, "ops[0].extraction_coverage[0].module")
}

func TestCoverageModuleDuplicateRejected(t *testing.T) {
	// 两个模块 module 原始字节相同 → 第二个处 E2（唯一性按原始字节，不 trim / 不归一化）。
	cov := []string{
		ncMod("同名模块", []string{"L1-L2"}, "前两行", "outputs", []string{"k-20260901-cov"}, ""),
		ncMod("同名模块", []string{"L3-L4"}, "后两行", "outputs", []string{"o-20260901-cov"}, ""),
	}
	res := ncValidate(t, ncBlocks(), nil, ncCards(), cov, true)
	ncReject(t, res, "ops[0].extraction_coverage[1].module")
}

// —— source_refs ——

func TestCoverageSourceRefsEmptyRejected(t *testing.T) {
	cov := []string{
		ncMod("材料方法", []string{}, "前两行", "outputs", []string{"k-20260901-cov"}, ""),
		ncMod("核心观点", []string{"L3-L4"}, "后两行", "outputs", []string{"o-20260901-cov"}, ""),
	}
	res := ncValidate(t, ncBlocks(), nil, ncCards(), cov, true)
	ncReject(t, res, "ops[0].extraction_coverage[0].source_refs")
}

func TestCoverageSourceRefEmptyItemRejected(t *testing.T) {
	cov := []string{
		ncMod("材料方法", []string{" "}, "前两行", "outputs", []string{"k-20260901-cov"}, ""),
		ncMod("核心观点", []string{"L3-L4"}, "后两行", "outputs", []string{"o-20260901-cov"}, ""),
	}
	res := ncValidate(t, ncBlocks(), nil, ncCards(), cov, true)
	ncReject(t, res, "ops[0].extraction_coverage[0].source_refs[0]")
}

func TestCoverageSourceRefDanglingRejected(t *testing.T) {
	// L9-L9 未在本次 role:source 块声明 → 悬空引用 E2（精确回指本次声明，不凭空接受）。
	cov := []string{
		ncMod("材料方法", []string{"L9-L9"}, "前两行", "outputs", []string{"k-20260901-cov"}, ""),
		ncMod("核心观点", []string{"L3-L4"}, "后两行", "outputs", []string{"o-20260901-cov"}, ""),
	}
	res := ncValidate(t, ncBlocks(), nil, ncCards(), cov, true)
	ncReject(t, res, "ops[0].extraction_coverage[0].source_refs[0]")
}

func TestCoverageUncoveredSourceRefRejected(t *testing.T) {
	// 只覆盖 L1-L2，漏掉 L3-L4 → 指向第二个 source 块的 E2（每段来源至少进入一个模块）。
	cov := []string{
		ncMod("材料方法", []string{"L1-L2"}, "前两行", "outputs", []string{"k-20260901-cov"}, ""),
	}
	// 去掉引用 o-cov 的模块后，o-20260901-cov 会变成「未被引用的产出卡」；为把断言钉死在
	// 「未覆盖来源」这条规则上，本用例的 output_cards 只留 k-cov。
	res := ncValidate(t, ncBlocks(), nil, []string{outputCard("k-20260901-cov", "新建")}, cov, true)
	ncReject(t, res, "ops[0].blocks[1].source_ref")
}

// —— summary ——

func TestCoverageSummaryEmptyRejected(t *testing.T) {
	cov := []string{
		ncMod("材料方法", []string{"L1-L2"}, "  ", "outputs", []string{"k-20260901-cov"}, ""),
		ncMod("核心观点", []string{"L3-L4"}, "后两行", "outputs", []string{"o-20260901-cov"}, ""),
	}
	res := ncValidate(t, ncBlocks(), nil, ncCards(), cov, true)
	ncReject(t, res, "ops[0].extraction_coverage[0].summary")
}

// —— disposition ——

func TestCoverageUnknownDispositionRejected(t *testing.T) {
	cov := []string{
		ncMod("材料方法", []string{"L1-L2"}, "前两行", "archived", []string{}, ""),
		ncMod("核心观点", []string{"L3-L4"}, "后两行", "outputs", []string{"o-20260901-cov"}, ""),
	}
	res := ncValidate(t, ncBlocks(), nil, ncCards(), cov, true)
	ncReject(t, res, "ops[0].extraction_coverage[0].disposition")
}

func TestCoverageOutputsRequiresOutputs(t *testing.T) {
	cov := []string{
		ncMod("材料方法", []string{"L1-L2"}, "前两行", "outputs", []string{}, ""),
		ncMod("核心观点", []string{"L3-L4"}, "后两行", "outputs", []string{"o-20260901-cov"}, ""),
	}
	res := ncValidate(t, ncBlocks(), nil, ncCards(), cov, true)
	ncReject(t, res, "ops[0].extraction_coverage[0].outputs")
}

func TestCoverageOutputsForbidsReason(t *testing.T) {
	cov := []string{
		ncMod("材料方法", []string{"L1-L2"}, "前两行", "outputs", []string{"k-20260901-cov"}, "不该给理由"),
		ncMod("核心观点", []string{"L3-L4"}, "后两行", "outputs", []string{"o-20260901-cov"}, ""),
	}
	res := ncValidate(t, ncBlocks(), nil, ncCards(), cov, true)
	ncReject(t, res, "ops[0].extraction_coverage[0].reason")
}

func TestCoverageOutputsInvalidEndpointRejected(t *testing.T) {
	// s-* 前缀不是合法的关系端点（只能是 k-*/o-*）。
	cov := []string{
		ncMod("材料方法", []string{"L1-L2"}, "前两行", "outputs", []string{"s-20260901-cov"}, ""),
		ncMod("核心观点", []string{"L3-L4"}, "后两行", "outputs", []string{"o-20260901-cov"}, ""),
	}
	res := ncValidate(t, ncBlocks(), nil, ncCards(), cov, true)
	ncReject(t, res, "ops[0].extraction_coverage[0].outputs[0]")
}

func TestCoverageOutputsMalformedKOEndpointRejected(t *testing.T) {
	// 前缀正确但**形态残缺**的 k-：ParseRelationEndpoint 同样拒绝（不是只看前缀）。
	// "k-20260901" 缺 <slug> 段（没有第二个 '-'）→ 非法 ID。
	cov := []string{
		ncMod("材料方法", []string{"L1-L2"}, "前两行", "outputs", []string{"k-20260901"}, ""),
		ncMod("核心观点", []string{"L3-L4"}, "后两行", "outputs", []string{"o-20260901-cov"}, ""),
	}
	res := ncValidate(t, ncBlocks(), nil, ncCards(), cov, true)
	ncReject(t, res, "ops[0].extraction_coverage[0].outputs[0]")
}

func TestCoverageNoteOnlyForbidsOutputs(t *testing.T) {
	cov := []string{
		ncMod("材料方法", []string{"L1-L2"}, "前两行", "outputs", []string{"k-20260901-cov"}, ""),
		ncMod("核心观点", []string{"L3-L4"}, "后两行", "note_only", []string{"o-20260901-cov"}, "仅记录"),
	}
	res := ncValidate(t, ncBlocks(), nil, ncCards(), cov, true)
	ncReject(t, res, "ops[0].extraction_coverage[1].outputs")
}

func TestCoverageNoteOnlyRequiresReason(t *testing.T) {
	cov := []string{
		ncMod("材料方法", []string{"L1-L2"}, "前两行", "outputs", []string{"k-20260901-cov"}, ""),
		ncMod("核心观点", []string{"L3-L4"}, "后两行", "note_only", []string{}, "  "),
	}
	// 此时 o-20260901-cov 无模块引用，会另有一条针对 output_cards 的 E2；断言只钉 reason 这条。
	res := ncValidate(t, ncBlocks(), nil, ncCards(), cov, true)
	ncReject(t, res, "ops[0].extraction_coverage[1].reason")
}

func TestCoverageMissingDispositionBlocksApply(t *testing.T) {
	// missing 表缺漏：即便字段齐全（outputs 空、reason 非空）也必须无条件 E2，且整条 op 零 action。
	cov := []string{
		ncMod("材料方法", []string{"L1-L2"}, "前两行", "outputs", []string{"k-20260901-cov"}, ""),
		ncMod("核心观点", []string{"L3-L4"}, "后两行", "missing", []string{}, "还没提炼出产出"),
	}
	res := ncValidate(t, ncBlocks(), nil, ncCards(), cov, true)
	ncReject(t, res, "ops[0].extraction_coverage[1].disposition")
}

func TestCoverageMissingForbidsOutputs(t *testing.T) {
	// missing 与 note_only 一样：outputs 必须为空。此处 missing 模块非法地给了 outputs → outputs 路径 E2。
	cov := []string{
		ncMod("材料方法", []string{"L1-L2"}, "前两行", "outputs", []string{"k-20260901-cov"}, ""),
		ncMod("核心观点", []string{"L3-L4"}, "后两行", "missing", []string{"k-20260901-cov"}, "还没提炼出产出"),
	}
	res := ncValidate(t, ncBlocks(), nil, []string{outputCard("k-20260901-cov", "新建")}, cov, true)
	ncReject(t, res, "ops[0].extraction_coverage[1].outputs")
}

func TestCoverageMissingRequiresReason(t *testing.T) {
	// missing 必须给非空 reason（TrimSpace 口径）：纯空白 → reason 路径 E2。
	cov := []string{
		ncMod("材料方法", []string{"L1-L2"}, "前两行", "outputs", []string{"k-20260901-cov"}, ""),
		ncMod("核心观点", []string{"L3-L4"}, "后两行", "missing", []string{}, "  "),
	}
	res := ncValidate(t, ncBlocks(), nil, []string{outputCard("k-20260901-cov", "新建")}, cov, true)
	ncReject(t, res, "ops[0].extraction_coverage[1].reason")
}

// —— 与 output_cards 双向一致 ——

func TestCoverageOutputNotInOutputCardsRejected(t *testing.T) {
	// 模块引用了 output_cards 里没有的 o-…-extra → E2（成员一致：coverage → cards 方向）。
	cov := []string{
		ncMod("材料方法", []string{"L1-L2"}, "前两行", "outputs", []string{"k-20260901-cov"}, ""),
		ncMod("核心观点", []string{"L3-L4"}, "后两行", "outputs", []string{"o-20261017-extra"}, ""),
	}
	res := ncValidate(t, ncBlocks(), nil, ncCards(), cov, true)
	ncReject(t, res, "ops[0].extraction_coverage[1].outputs[0]")
}

func TestCoverageOutputCardNotReferencedRejected(t *testing.T) {
	// output_cards 多出一张 k-…-extra，没有任何 disposition=outputs 模块引用它 → E2（cards → coverage 方向）。
	cards := []string{
		outputCard("k-20260901-cov", "新建"),
		outputCard("o-20260901-cov", "新建"),
		outputCard("k-20261017-extra", "复用"),
	}
	res := ncValidate(t, ncBlocks(), nil, cards, ncCoverage(), true)
	ncReject(t, res, "ops[0].output_cards[2].card")
}

func TestCoverageOutputCardEmptyRejected(t *testing.T) {
	cards := []string{
		outputCard("k-20260901-cov", "新建"),
		outputCard("o-20260901-cov", "新建"),
		outputCard("", "复用"),
	}
	res := ncValidate(t, ncBlocks(), nil, cards, ncCoverage(), true)
	ncReject(t, res, "ops[0].output_cards[2].card")
}

func TestCoverageOutputCardInvalidEndpointRejected(t *testing.T) {
	// n-* 不是合法的产出卡端点。
	cards := []string{
		outputCard("k-20260901-cov", "新建"),
		outputCard("o-20260901-cov", "新建"),
		outputCard("n-20261017-note", "复用"),
	}
	res := ncValidate(t, ncBlocks(), nil, cards, ncCoverage(), true)
	ncReject(t, res, "ops[0].output_cards[2].card")
}

func TestCoverageOutputCardMalformedKOEndpointRejected(t *testing.T) {
	// 前缀正确但形态残缺的 o-：ParseRelationEndpoint 拒绝。"o-2026-cost" 的日期段不是 8 位 → 非法 ID。
	cards := []string{
		outputCard("k-20260901-cov", "新建"),
		outputCard("o-20260901-cov", "新建"),
		outputCard("o-2026-cost", "复用"),
	}
	res := ncValidate(t, ncBlocks(), nil, cards, ncCoverage(), true)
	ncReject(t, res, "ops[0].output_cards[2].card")
}

// —— 不凭空拒绝合法的重复 / 多引用 ——

// TestCoverageModuleRawByteDistinctAllowed —— module 唯一性按**原始字节**判定的负控：
// "x" 与 " x "（前后带空格）TrimSpace 后都非空、且原始字节不同 → 两条模块合法，不得当作重复拒绝。
func TestCoverageModuleRawByteDistinctAllowed(t *testing.T) {
	cov := []string{
		ncMod("x", []string{"L1-L2"}, "前两行", "outputs", []string{"k-20260901-cov"}, ""),
		ncMod(" x ", []string{"L3-L4"}, "后两行", "outputs", []string{"o-20260901-cov"}, ""),
	}
	res := ncValidate(t, ncBlocks(), nil, ncCards(), cov, true)
	requireNoError(t, res)
}

func TestCoverageDupSourceRefWithinModuleAllowed(t *testing.T) {
	// 同一模块内重复 source_ref：合同未声明其非法 → 不得凭空拒绝。L3-L4 仍由第二个模块覆盖。
	cov := []string{
		ncMod("材料方法", []string{"L1-L2", "L1-L2"}, "前两行", "outputs", []string{"k-20260901-cov"}, ""),
		ncMod("核心观点", []string{"L3-L4"}, "后两行", "outputs", []string{"o-20260901-cov"}, ""),
	}
	res := ncValidate(t, ncBlocks(), nil, ncCards(), cov, true)
	requireNoError(t, res)
}

func TestCoverageDupOutputWithinModuleAllowed(t *testing.T) {
	cov := []string{
		ncMod("材料方法", []string{"L1-L2"}, "前两行", "outputs", []string{"k-20260901-cov", "k-20260901-cov"}, ""),
		ncMod("核心观点", []string{"L3-L4"}, "后两行", "outputs", []string{"o-20260901-cov"}, ""),
	}
	res := ncValidate(t, ncBlocks(), nil, ncCards(), cov, true)
	requireNoError(t, res)
}

func TestCoverageSameOutputMultipleModulesAllowed(t *testing.T) {
	// 同一 output 被多个模块引用合法；此处 output_cards 只留 k-cov，两个模块都 outputs 到它。
	cov := []string{
		ncMod("材料方法", []string{"L1-L2"}, "前两行", "outputs", []string{"k-20260901-cov"}, ""),
		ncMod("补充视角", []string{"L3-L4"}, "后两行也归到同一张卡", "outputs", []string{"k-20260901-cov"}, ""),
	}
	res := ncValidate(t, ncBlocks(), nil, []string{outputCard("k-20260901-cov", "新建")}, cov, true)
	requireNoError(t, res)
}

func TestCoverageSameSourceRefMultipleModulesAllowed(t *testing.T) {
	// 同一 source_ref 进多个模块合法，并混一个 note_only 模块（outputs 空、reason 非空）。
	cov := []string{
		ncMod("材料方法", []string{"L1-L2"}, "前两行讲方法", "outputs", []string{"k-20260901-cov"}, ""),
		ncMod("延伸讨论", []string{"L1-L2", "L3-L4"}, "两段一起只作记录", "note_only", []string{}, "暂不产出卡"),
	}
	res := ncValidate(t, ncBlocks(), nil, []string{outputCard("k-20260901-cov", "新建")}, cov, true)
	requireNoError(t, res)
}

// —— 兼容路径不受波及 ——

func TestCoverageV1SectionsUnaffected(t *testing.T) {
	// v1 plan 的 sections{} write_note：不要求 extraction_coverage。
	op := `{"op":"write_note","source":"` + covSrcID + `","note_id":"n-20261017-v1s",` +
		`"sections":{"材料提炼":"整理正文。\n"}}`
	res := covV1(t, covBody4, op)
	requireNoError(t, res)
}

func TestCoverageV1BlocksUnaffected(t *testing.T) {
	// v1 plan 用 blocks[]：走旧落盘口径，不触发 v2 覆盖 / 来源校验。
	op := `{"op":"write_note","source":"` + covSrcID + `","note_id":"n-20261017-v1b",` +
		`"blocks":[` + covSB("L1-L1", "旧口径整理。") + `]}`
	res := covV1(t, covBody4, op)
	requireNoError(t, res)
}

func TestCoverageV2SectionsUnaffected(t *testing.T) {
	// v2 plan 仍用 sections{}（非 BlocksGiven）：走兼容映射，不要求 extraction_coverage。
	files := covSourceFile(covBody4)
	op := `{"op":"write_note","source":"` + covSrcID + `","note_id":"n-20261017-v2s",` +
		`"sections":{"材料提炼":"整理正文。\n"}}`
	res := run(t, vault(t, files), v2Plan(t, files, op))
	requireNoError(t, res)
}

// —— 渲染接线冒烟：合法覆盖执行后「提取结果」出现覆盖矩阵与机器锚点 ——

func TestCoverageMatrixWiredIntoExtraction(t *testing.T) {
	files := covSourceFile(covBody4)
	op := ncOp("n-20261017-wire", ncBlocks(), nil, ncCards(), ncCoverage(), true)
	res := run(t, vault(t, files), v2Plan(t, files, op))
	requireNoError(t, res)
	dir, _ := execOn(t, files, res)
	raw := readVaultFile(t, dir, "domains/ai-infra/notes/n-20261017-wire.md")
	body := extractionOf(t, raw)

	for _, want := range []string{
		"### 覆盖矩阵",
		"| 模块 | 来源范围 | 语义模块 | 处置 |",
		"| --- | --- | --- | --- |",
		"`材料方法`",
		"`核心观点`",
		"`k-20260901-cov`",
		"`o-20260901-cov`",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("「提取结果」缺覆盖矩阵内容 %q：\n%s", want, body)
		}
	}
	// 机器锚点：覆盖矩阵使用独立版本化协议 eg:nc:1（不复用 T12-3 的 eg:nr:1）。
	// 逐字节的可见矩阵 ↔ 机器锚点等价由 internal/store 的 T12-4 判据钉死（那里字节完全可控）；
	// 本处只冒烟接线：至少出现一条本协议锚点，且能被窄 parser 读回两条覆盖项、模块顺序不变。
	if !strings.Contains(body, "<!-- eg:nc:1 ") {
		t.Fatalf("覆盖矩阵缺 eg:nc:1 机器锚点：\n%s", body)
	}
	mi := strings.Index(body, "### 覆盖矩阵")
	if mi < 0 {
		t.Fatalf("定位覆盖矩阵失败：\n%s", body)
	}
	// 从矩阵首行到最后一条锚点行，切出**紧凑**的矩阵块（去掉分区尾随的空白 / 下一分区），
	// 交给窄 parser 做逐字节交叉核对。
	matrix := body[mi:]
	if last := strings.LastIndex(matrix, mdfile.CoverageAnchorClose+"\n"); last >= 0 {
		matrix = matrix[:last+len(mdfile.CoverageAnchorClose)+1]
	}
	covs, err := mdfile.ParseCoverageMatrix([]byte(matrix))
	if err != nil {
		t.Fatalf("覆盖矩阵窄 parser 读回失败：%v\n%s", err, matrix)
	}
	if len(covs) != 2 || covs[0].Module != "材料方法" || covs[1].Module != "核心观点" {
		t.Fatalf("覆盖矩阵读回的模块 / 顺序不对：%+v", covs)
	}
}
