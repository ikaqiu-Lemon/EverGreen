package plan

// `edit_section` 的字段表、校验与展开：`eg edit` 的**唯一** op 载体
// （授权合同 §9 临时裁决 **A-13**「判 eg edit 属 M3 范围」+ §2 写权限矩阵 **#12** 的 P-U ✅ 列；
// T-evergreen.s1_main_flow-158614-045）。
//
// # 为什么它不进 M3OpNames（op 计数没有变小也没有被偷偷改大）
//
// M3OpNames 逐字对应**提案与状态合同 §8.1 表格的 8 行**，那张表里没有 `edit_section`；
// 把它塞进去会让「§8.1 有几行」这个可复算事实与代码不再一致。它的依据是**授权合同**的
// A-13 与矩阵 #12，因此单列成 EditOpNames()，并只在派发全集 AllOpNames() 里汇总。
// 计数事实随之如实变成 7（S1 冻结）+ 8（§8.1）+ 1（A-13）= 16，由 m3_test.go 断言。
//
// # 为什么它是「替换」却不违反 B1
//
// B1 的原文定义本身把用户显式路径排除在外：「替换与删除**只在用户显式发起的命令里存在
// （S2 起）**」（授权合同 §5 B1 行）。因此**渲染器只对 P-U 提供替换能力**——本文件的
// 门闸先把非 P-U 的 `edit_section` 一律拒掉（editSectionGate），Agent 自动路径拿不到
// 这条形态，这是代码层约束而不是运行时开关。
//
// # 阶段边界（本 op 一律不做）
//
//   - 不写「用户补充」（U-03：两条路径同 🔴，`--section 用户补充` 必 E6 退 2）；
//   - 不替换「理解自检」整段：那里的历史记录块只追加、永不改写（EG-CHK-06），
//     要换当前有效问题块请用 `replace_block` + `base_block_hash`；
//   - 不改 `status` / `deleted_at` / `reviewed_at`（三个正交维度，改一个绝不碰另两个）；
//   - 不做块级三方合并、不做部分应用：冲突仍是**整文件跳过**（B3，R-13 已知风险）。

import (
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// OpEditSection 是 `eg edit` 合成的 op 名（A-13 的载体，不属提案合同 §8.1 的 8 行）。
const OpEditSection = "edit_section" // M3 / S2

// ActEditSection 是分区正文替换这一写入形态（落盘走 store.ApplyReplaceSection）。
const ActEditSection ActionKind = "edit_section_write" // M3 / S2

// SectionEdit 是一次分区正文替换的写入意图（校验后确定要落盘的值）。
type SectionEdit struct {
	// Section 是被替换的分区名（逐字，取自 EditableSections 白名单）。
	Section string
	// Candidate is empty for the legacy artifact-H2 mode. A non-empty key
	// selects one unmaterialized Note candidate H4 payload.
	Candidate string
	// Payload 是新的分区正文字节，逐字落盘，必须以 \n 结束。
	Payload []byte
}

// EditOpNames 是 A-13 载体的 op 名（**恰 1 个**）。
func EditOpNames() []string { return []string{OpEditSection} }

// IsEditOp 报告该 op 名是否属 A-13 载体。
func IsEditOp(name string) bool { return inList(EditOpNames(), name) }

// EditOpFields 是 `edit_section` 的字段名集合（`op` 自身不计）。
func EditOpFields() []string {
	return []string{"target", "candidate", "section", "content", "initiator"}
}

// editOpKnownKeys 是 `edit_section` 的顶层键集合；集合外的键落进 Extra + I1（前向兼容）。
func editOpKnownKeys(name string) []string {
	if name != OpEditSection {
		return nil
	}
	return append([]string{"op"}, EditOpFields()...)
}

// EditableSections 是 `eg edit` 允许替换的分区白名单（**恰 3 个**，按 F5 固定分区顺序）。
//
// 取值依据逐格来自矩阵：#12「知识内容」P-U ✅（A-13 点名 `eg edit` 是载体）、
// #13「解释与依据」、#14「条件与边界」两格 P-U 亦为 ✅。**不含**：
//   - #15「用户补充」——两条路径同 🔴，永不由 CLI 写入（U-03）；
//   - 「理解自检」——矩阵 #16 / #17 只给「追加块」与「替换当前有效问题块」两种形态，
//     整段替换会连历史记录块一起改写，违反 EG-CHK-06。
//
// 不自行放宽：任何一格符号翻转都应先改 matrix.go，本白名单再随之调整。
func EditableSections() []string {
	return []string{store.SecKnowledge, store.SecRationale, store.SecBoundary}
}

// parseEditFields 解析 `edit_section` 独有的字段（`content`）。
//
// 与 S1 / M3 字段同一条只读路径：只做类型断言，不做任何规范化——用户正文一律以 []byte
// 交给写入侧（ContentGiven 区分「缺 content」与「给了空串」，两者的诊断成因不同）。
func parseEditFields(op *Op, m map[string]interface{}) {
	op.Candidate, _ = asString(m["candidate"])
	if v, ok := m["content"]; ok {
		op.ContentGiven = true
		s, _ := asString(v)
		op.Content = []byte(s)
	}
}

// editSection 校验并展开 edit_section。
//
// 判据次序固定（先说清「不许写什么」，再说「凭据够不够」）：
//  1. `target` 必须是可解析的知识卡（E2 / E4）；
//  2. `section` 必须在 EditableSections 白名单内——「用户补充」走矩阵 #15 出 **E6**
//     （与是否 P-U 无关，U-03），其余越界分区同判 E6；
//  3. 授权：W7 分级（`edit_section` 不在 A-15 窄口径五个状态类 op 内 → warning 形态）
//     ＋ **editSectionGate**（P-U 才有替换能力；缺命令行 `--user-request` 的伪造形态
//     落 P-A → E6 → 退 2、零写入）；
//  4. `content` 必须给出、非空、以换行结束（E5：本工具不生成、不补写用户内容）；
//  5. B3：`baseCheck` 写入 ExpectedHash，base 未覆盖该文件 → W6 + 跳过（退 3）。
func (v *validator) editSection(op *Op) {
	if op.Candidate != "" {
		v.editCandidateSection(op)
		return
	}
	rel, ok := v.cardTarget(op, "target", op.Target)
	if !ok {
		return
	}
	// W7 与矩阵在此**分工**（与 mark_reviewed / remove_relation 同一套）：W7 回答
	// 「授权表达完整吗」——edit_section 不在 A-15 窄口径五个状态类 op 内，故只记 warning
	// 形态、不拦截；能不能写这一格由紧随其后的门闸（矩阵）说话。
	v.requireInitiator(op)
	if !v.editSectionGate(op, op.Section) {
		return
	}
	if !op.ContentGiven || len(op.Content) == 0 {
		v.add(errorAt(E5, op.Index, opPath(op.Index, "content"),
			"edit_section 缺 content：新的分区正文必须逐字给出（本工具不生成、不补写用户内容）"))
		return
	}
	if op.Content[len(op.Content)-1] != '\n' {
		v.add(errorAt(E5, op.Index, opPath(op.Index, "content"),
			"content 必须以换行结束：写入是字节级区间替换，不替调用方补字节"))
		return
	}

	edit := SectionEdit{Section: op.Section, Payload: op.Content}
	act := Action{Kind: ActEditSection, OpIndex: op.Index, Op: op, ID: op.Target, Path: rel,
		Domain: store.DomainOf(rel), Edit: &edit}
	v.baseCheck(op, op.Target, rel, &act)
	v.res.Actions = append(v.res.Actions, act)
}

func (v *validator) editCandidateSection(op *Op) {
	if _, err := model.ParseNoteID(op.Target); err != nil {
		v.add(errorAt(E2, op.Index, opPath(op.Index, "target"),
			"candidate 模式的 target 必须是合法 Note ID：%v", err))
		return
	}
	rel, ok := v.resolve(op.Target)
	if !ok {
		v.add(errorAt(E2, op.Index, opPath(op.Index, "target"),
			"candidate 模式的 Note %s 不存在于全库", op.Target))
		return
	}
	v.requireInitiator(op)
	if !v.candidateEditGate(op) {
		return
	}
	if !op.ContentGiven || len(op.Content) == 0 {
		v.add(errorAt(E5, op.Index, opPath(op.Index, "content"),
			"edit_section candidate 模式缺 content"))
		return
	}
	if op.Content[len(op.Content)-1] != '\n' {
		v.add(errorAt(E5, op.Index, opPath(op.Index, "content"),
			"content 必须以换行结束：写入是字节级区间替换"))
		return
	}
	raw, readable := v.readExisting(rel)
	if !readable {
		v.add(errorAt(E4, op.Index, opPath(op.Index, "target"),
			"candidate 模式无法读取目标 Note：%s", rel))
		return
	}
	candidates, err := mdfile.ParseCandidates(raw)
	if err != nil {
		v.add(errorAt(E4, op.Index, opPath(op.Index, "candidate"),
			"目标 Note 的 candidate 协议无法严格读回：%v", err))
		return
	}
	var found *mdfile.Candidate
	for i := range candidates {
		if candidates[i].Key == op.Candidate {
			found = &candidates[i]
			break
		}
	}
	if found == nil {
		v.add(errorAt(E2, op.Index, opPath(op.Index, "candidate"),
			"目标 Note 不含 candidate %q", op.Candidate))
		return
	}
	if found.Anchor.Output != "" {
		v.add(errorAt(E2, op.Index, opPath(op.Index, "candidate"),
			"candidate %s 已物化为 %s，不得再通过命令式路径编辑",
			op.Candidate, found.Anchor.Output))
		return
	}
	sectionFound := false
	for _, section := range found.Sections {
		if section.Name == op.Section {
			sectionFound = true
			break
		}
	}
	if !sectionFound {
		v.add(errorAt(E2, op.Index, opPath(op.Index, "section"),
			"candidate %s 不含 H4 分区 %q", op.Candidate, op.Section))
		return
	}
	edit := SectionEdit{Candidate: op.Candidate, Section: op.Section, Payload: op.Content}
	act := Action{Kind: ActEditSection, OpIndex: op.Index, Op: op, ID: op.Target, Path: rel,
		Domain: store.DomainOf(rel), Edit: &edit}
	v.baseCheck(op, op.Target, rel, &act)
	v.res.Actions = append(v.res.Actions, act)
}

func (v *validator) candidateEditGate(op *Op) bool {
	if v.pathOf(op) == PathUser {
		return true
	}
	why := fmt.Sprintf("%s candidate 模式只允许用户显式路径（%s）", op.Name, PathUser)
	if Forged(op, v.auth()) {
		why = fmt.Sprintf("%s 的授权佐证不成立：%s", op.Name, ForgeryNotice)
	}
	v.add(v.gateDiag(op, E6, "candidate",
		why+"；整条 op 不执行，目标 Note 字节不变"))
	return false
}

// editableSection 报告分区是否在 `eg edit` 的白名单内。
func editableSection(name string) bool { return inList(EditableSections(), name) }

// editSectionSelfCheckNotice 是「理解自检」被拒的逐字理由（报告文案与用例共用一份字面量）。
var editSectionSelfCheckNotice = fmt.Sprintf(
	"「%s」不由 edit_section 整段替换：历史记录块只追加、永不改写（EG-CHK-06），"+
		"要换当前有效问题块请用 %s + base_block_hash", SelfCheckSection, OpReplaceBlock)
