package proposal

// 提案 `status` 的**四态状态机**（提案合同 §2 全节；T-…-034）。
//
// 本文件是 035 / 036 / 040 / 041 的**唯一状态判定来源**：合法边、非法边、终态、
// 以及「`status` ⟷ `decision.result` 一致性」四件事只在这里定义一次。
//
// 三条形态硬约束：
//   - **四态封闭**（§2.1）：pending / approved / rejected / superseded，不存在第五种；
//     解析到集合外取值 → 结构化错误（037 映射为 M3 的第二个新增 error，见 DiagClass）。
//   - **合法边恰 5 条**（§2.3）：真实迁移 T1–T4 共 4 条 + 初始边 T0 一条。
//   - **非法边恰 12 条**（§2.4）：4 × 4 = 16 对有序配对**在代码里减去** T1–T4，
//     自环 4 + 异向 8 —— 绝不手写枚举，避免漏项（本 task 的机器判据之一）。
//
// # A-22 临时裁决（提案合同 §2.5「本合同的临时裁决」，逐字引用）
//
//	「`approved` 判为**非终态**，其**唯一合法出边是 `→ superseded`**。
//	 理由：状态图明确画出了 `approved --> superseded`，若判 `approved` 为终态则该边不可达，
//	 与图矛盾。执行成功后提案**停在 `approved` + `execution.succeeded`**，不再迁移——
//	 这与「处理过的提案留在 `proposals/` 原地，不搬目录」（§10.1 第 3 条）一致。」
//
// **A-22 是本仓临时裁决而非飞书原文**（原文 §10 状态图没有 `approved --> [*]` 边）。
// owner 若回改状态图，改动落点恰是本文件的 legalEdges 表 + terminal 判定 + state_test.go
// 的三张表，别处不会散落第二份状态知识。
//
// # 本 task 的边界
//
//   - **不发诊断码**：本包不出现任何编号字面量；违规一律返回 *Violation，
//     由 T-…-037 按 DiagClass 统一映射到 M3 新增的 error 编号。
//   - **不写盘**：整个文件是纯函数层，没有任何文件写口（A-23 下写口属 T-…-040），
//     因此「非法迁移零写入」在**结构上**成立，而不是靠运行时回滚。
//   - `superseded_by` **缺失**的判定（037 里的第一个新增 error）属 T-…-035；
//     本文件只保证 `→ superseded` 这条边合法且把 superseded_by 列进必写字段。
//   - `execution` 维度与可达矩阵属 T-…-036，本文件只判 `status` 一维。

import (
	"fmt"
	"strings"
)

// StatusNone 是状态图里的初始伪态 `[*]`（**不是**第五个 status 取值）。
//
// 它只作为 T0（`eg proposal new`）这条**初始边**的起点出现，永不落盘：
// Statuses() 仍恰返回四个真实取值，Status("").Valid() 为 false。
const StatusNone Status = ""

// —— ① 合法迁移表（§2.3，恰 5 行；表驱动，不散落 if）——

// Edge 是一条合法迁移边。
//
// ID 是合同 §2.3 的行号（T0–T4，逐字），Trigger 是触发命令 / 条件的逐字抄录，
// Required 是该边**必写字段**的点分键路径（键名一律取自 schema.go 的常量，不抄字面量）。
type Edge struct {
	ID       string
	From     Status
	To       Status
	Trigger  string
	Required []string
}

// LegalEdges 返回合法迁移表：**恰 5 条** = 真实迁移 T1–T4 + 初始边 T0（§2.3 末行）。
//
// 每次调用返回新切片：调用方改不动本包的权威表。
func LegalEdges() []Edge {
	dot := func(block, sub string) string { return block + "." + sub }
	return []Edge{
		{
			ID: "T0", From: StatusNone, To: StatusPending,
			Trigger: "eg proposal new --type logical_delete --target <id>",
			Required: []string{KeyID, KeyType, KeyStatus, KeyCreatedAt,
				KeyTargets, KeyImpact},
		},
		{
			ID: "T1", From: StatusPending, To: StatusApproved,
			Trigger:  "eg proposal approve <id> --confirm（且重算影响面与 impact 一致）",
			Required: []string{dot(KeyDecision, KeyResult)},
		},
		{
			ID: "T2", From: StatusPending, To: StatusRejected,
			Trigger: "eg proposal reject <id> --reason <r>",
			Required: []string{dot(KeyDecision, KeyResult),
				dot(KeyDecision, KeyReason)},
		},
		{
			ID: "T3", From: StatusPending, To: StatusSuperseded,
			Trigger: "触发① 部分接受 / 触发② 前提变化",
			Required: []string{dot(KeyDecision, KeyResult),
				dot(KeyDecision, KeySupersededBy)},
		},
		{
			ID: "T4", From: StatusApproved, To: StatusSuperseded,
			Trigger: "触发② 执行前提已变化 → 不强行执行",
			Required: []string{dot(KeyDecision, KeyResult),
				dot(KeyDecision, KeySupersededBy)},
		},
	}
}

// LegalEdge 查一条合法边（起态 → 止态）；不合法时第二个返回值为 false。
func LegalEdge(from, to Status) (Edge, bool) {
	for _, e := range LegalEdges() {
		if e.From == from && e.To == to {
			return e, true
		}
	}
	return Edge{}, false
}

// IsLegalTransition 报告 from → to 是否是 §2.3 的合法边。
func IsLegalTransition(from, to Status) bool {
	_, ok := LegalEdge(from, to)
	return ok
}

// OutEdges 返回某个态的全部合法出边（终态返回空切片）。
func OutEdges(s Status) []Edge {
	var out []Edge
	for _, e := range LegalEdges() {
		if e.From == s {
			out = append(out, e)
		}
	}
	return out
}

// —— ② 非法迁移表（§2.4，恰 12 条；**由 4 × 4 全集减法算出**）——

// Transition 是一次「起态 → 止态」的有序配对（两端都是真实四态，不含初始伪态）。
type Transition struct {
	From Status
	To   Status
}

// allStatuses 是**四态全集**的迭代源。
//
// 非法边表的唯一构造入口就是它：谁想加第五态，Statuses() 一改，16 对配对与 12 条
// 非法边同时变化，state_test.go 的 4 × 4 等号断言立刻红——这是「不存在第五种」的机器防线。
func allStatuses() []Status { return Statuses() }

// illegalEdges 由「4 × 4 有序配对全集 − LegalEdges 里的真实迁移」算出。
//
// 恰 12 条 = 自环 4（pending/approved/rejected/superseded 各一条）+ 异向 8。
// **刻意不手写枚举**：手写表漏一行没人看得出来，减法表漏一行意味着 legalEdges 多了一行，
// 会同时撞坏「合法边恰 5 条」与「非法边恰 12 条」两条断言（§2.4 的反证要求）。
func illegalEdges() []Transition {
	out := make([]Transition, 0, len(allStatuses())*len(allStatuses()))
	for _, from := range allStatuses() {
		for _, to := range allStatuses() {
			if IsLegalTransition(from, to) {
				continue
			}
			out = append(out, Transition{From: from, To: to})
		}
	}
	return out
}

// IllegalTransitions 返回 §2.4 的非法迁移表（恰 12 条，顺序 = 四态全集的行优先序）。
func IllegalTransitions() []Transition { return illegalEdges() }

// —— ③ 终态（§2.5）——

// TerminalStatuses 返回终态集合：`rejected` / `superseded`（状态图各有 `--> [*]` 边）。
//
// `pending` 有三条出边、`approved` 有唯一出边 `→ superseded`（**A-22 临时裁决**），
// 两者都**不是**终态。
func TerminalStatuses() []Status { return []Status{StatusRejected, StatusSuperseded} }

// IsTerminal 报告某个态是否是终态（终态的合法出边数恒为 0）。
func IsTerminal(s Status) bool {
	for _, t := range TerminalStatuses() {
		if s == t {
			return true
		}
	}
	return false
}

// —— ④ 结构化错误：本包只说「哪条规则不成立」，编号由 037 统一发 ——

// 状态机新增的三个 ViolationKind（与 schema.go 的形态类 kind 同一封闭集合）。
const (
	// ViolationIllegalTransition：非法 status 迁移（§2.4 的 12 条之一，含终态出边）。
	ViolationIllegalTransition ViolationKind = "illegal_transition"
	// ViolationDecisionMismatch：status 与 decision.result 不一致（含 pending 时非空）。
	ViolationDecisionMismatch ViolationKind = "decision_result_mismatch"
	// ViolationRequiredField：合法边的必写字段缺失（§2.3「必写字段」列）。
	ViolationRequiredField ViolationKind = "required_field_missing"
)

// DiagClass 是一条违规**应当**被映射到的 M3 诊断族。
//
// 本包不写编号字面量（诊断码由 T-…-037 统一发）；这里只给出**语义分类**，
// 037 的映射表照此接驳：
//   - DiagEnumOrConsistency ←→ M3 新增 error 表里「枚举越界 / decision.result 与
//     status 不一致」那一行（提案合同 §8.2.1 第 2 行）；
//   - DiagIllegalTransition ←→ 同表「非法 status 迁移 / 命中可达矩阵红格」那一行
//     （§8.2.1 第 3 行；可达矩阵一维属 T-…-036）。
//
// 两者的 CLI 行为都是 ExitCodeValidation + 零写入（§8.2.1「退 2，零写入」列）。
type DiagClass string

// 两个诊断族（封闭集合；本包不产生第三族）。
const (
	DiagNone              DiagClass = ""
	DiagEnumOrConsistency DiagClass = "enum_or_consistency"
	DiagIllegalTransition DiagClass = "illegal_transition"
)

// missSeparator 是「缺失字段」列表的分隔符（渲染用，不参与判定）。
const missSeparator = ", "

// ExitCodeValidation 是本包全部违规映射到的 CLI 退出码：**2**（校验失败、零写入）。
//
// 与 internal/cli 的同名常量同值同语义；本包不导入 cli（依赖方向不允许），
// 故各自持有同一份数字，由 state_test.go 的等号断言与 e2e 脚本共同锁死。
const ExitCodeValidation = 2

// DiagClassOf 把 ViolationKind 归到 M3 的诊断族（不认识的 kind 返回 DiagNone）。
func DiagClassOf(kind ViolationKind) DiagClass {
	switch kind {
	case ViolationStatusEnum, ViolationExecEnum, ViolationDecisionMismatch:
		return DiagEnumOrConsistency
	case ViolationIllegalTransition:
		return DiagIllegalTransition
	default:
		return DiagNone
	}
}

// —— ⑤ 三个判定入口 ——

// CheckStatus 判定 status 取值是否在四态内（§2.1「不存在第五种」）。
//
// 越界（draft / applied / candidate …）→ ViolationStatusEnum，
// DiagClassOf 归入 DiagEnumOrConsistency，CLI 侧退 ExitCodeValidation 且零写入。
// 初始伪态 StatusNone 也算越界：它永不落盘。
func CheckStatus(s Status) error {
	if s.Valid() {
		return nil
	}
	return violate(ViolationStatusEnum, KeyStatus,
		"status = %q 不在四态内（封闭集合：%s），不存在第五种", s, joinStatuses())
}

// CheckTransition 判定一次迁移是否合法。
//
// 两端都必须先在封闭集合内（起点允许 StatusNone —— 那是 T0 的初始边），
// 然后按 LegalEdges 表判边：命中 → nil；未命中 → ViolationIllegalTransition
// （§2.4 的 12 条之一，终态出边天然落在其中），037 映射为「非法迁移」那条 error。
func CheckTransition(from, to Status) error {
	if from != StatusNone {
		if err := CheckStatus(from); err != nil {
			return err
		}
	}
	if err := CheckStatus(to); err != nil {
		return err
	}
	if IsLegalTransition(from, to) {
		return nil
	}
	if IsTerminal(from) {
		return violate(ViolationIllegalTransition, KeyStatus,
			"%s → %s 非法：%s 是终态，没有任何合法出边", from, to, from)
	}
	if from == to {
		return violate(ViolationIllegalTransition, KeyStatus,
			"%s → %s 非法：自环不是迁移（合法边恰 %d 条，见提案合同 §2.3）",
			from, to, len(LegalEdges()))
	}
	return violate(ViolationIllegalTransition, KeyStatus,
		"%s → %s 非法：%s 的合法出边只有 %s", from, to, from, joinOut(from))
}

// CheckDecisionConsistency 判定「status ⟷ decision.result」一致性（M-5 / A-20）。
//
// 顶层 status 是**权威**（A-20），decision.result 只是用户决定的记录副本：
//   - status 必须先在四态内（否则先报枚举越界）；
//   - status == pending 时 decision.result 必须为**空**（还没有决定）；
//   - 其余三态下 decision.result 必须与 status **逐字相等**。
//
// 不满足 → ViolationDecisionMismatch，DiagClassOf 归入 DiagEnumOrConsistency。
func CheckDecisionConsistency(p Proposal) error {
	if err := CheckStatus(p.Status); err != nil {
		return err
	}
	field := KeyDecision + "." + KeyResult
	got := p.Decision.Result
	if p.Status == StatusPending {
		if got != StatusNone {
			return violate(ViolationDecisionMismatch, field,
				"status = %s 时 %s 必须为空，实为 %q（尚无用户决定）",
				StatusPending, field, got)
		}
		return nil
	}
	if got != p.Status {
		return violate(ViolationDecisionMismatch, field,
			"%s = %q 必须与权威的 status = %q 逐字相等（顶层 status 是权威）",
			field, got, p.Status)
	}
	return nil
}

// CheckRequiredFields 判定某条合法边的**必写字段**是否都已落盘（§2.3 必写字段列）。
//
// present 是调用方从 frontmatter 实际读到的**非空**键路径集合（点分，如 decision.result）。
// 只判「在不在」，不判取值语义：取值语义分别由 CheckDecisionConsistency（result）
// 与 T-…-035（superseded_by 能否解析到存在的提案）承接。
func CheckRequiredFields(e Edge, present map[string]bool) error {
	var miss []string
	for _, k := range e.Required {
		if !present[k] {
			miss = append(miss, k)
		}
	}
	if len(miss) == 0 {
		return nil
	}
	return violate(ViolationRequiredField, strings.Join(miss, missSeparator),
		"%s（%s → %s）的必写字段缺失：%s",
		e.ID, edgeFrom(e), e.To, strings.Join(miss, missSeparator))
}

// joinOut 把某个态的合法出边渲染成「pending → approved | rejected | superseded」的右半。
func joinOut(s Status) string {
	outs := OutEdges(s)
	if len(outs) == 0 {
		return "（无）"
	}
	names := make([]string, 0, len(outs))
	for _, e := range outs {
		names = append(names, fmt.Sprintf("%s（%s）", e.To, e.ID))
	}
	return strings.Join(names, " | ")
}

// edgeFrom 渲染边的起点：初始伪态显示为状态图里的 `[*]`。
func edgeFrom(e Edge) string {
	if e.From == StatusNone {
		return "[*]"
	}
	return string(e.From)
}
