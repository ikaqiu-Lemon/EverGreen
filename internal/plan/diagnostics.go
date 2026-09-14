package plan

// M3 新增诊断码的注册与载荷（提案合同 §8.2；CLI 合同 §5 的**六字段**载荷不变）。
//
// 编号纪律（§8.2.4 编号占用总览）：
//   - error 从 **E7** 起，恰 `E7`–`E10`；warning 从 **W9** 起，恰 `W9`–`W12`；
//   - **不新增 info 编号**（`I1` 之外没有第二个）；
//   - **不新增第七个诊断字段**（载荷仍是 code / level / path / op_index / message / target）；
//   - **不新增 `skipped[].kind`**（仍恰两值，见 internal/store/receipt.go）；
//   - 全库 `code` 字面量必须闭合在 `E1..E10 ∪ W1..W12 ∪ {I1}`，
//     由 TestDiagnosticCodes_Closed + e2e grep 双侧钉死。
//
// 既有编号在 M3 的**分级变化**（§8.2.3，不新增编号）：
//   - `W7`：S1 是 warning 占位，**M3 起对五个状态类 op 是 error**（A-15 窄口径），
//     另三个 op 仍是 warning；V9 / V10 不另起编号（授权合同 §9 A-14）。
//   - `W3`：仍是 warning，但 **M3 起真正开始判定**（论证关系某端不是 active 或已被逻辑删除）。

import (
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/proposal"
)

// M3 新增的 error 编号（触发即零写入，CLI 退 2）。
const (
	// E7 `status: superseded` 但 decision.superseded_by 为空或无法解析到存在的提案 ID。
	E7 = "E7"
	// E8 提案 status / execution.status 越界，或 decision.result 与 status 不一致。
	E8 = "E8"
	// E9 非法 status 迁移（12 条之一），或 status × execution 命中可达矩阵 🔴 / ⚠️ 格。
	E9 = "E9"
	// E10 set_replaced_by 的 target 已被逻辑删除（deleted_at 非空）。
	E10 = "E10"
)

// M3 新增的 warning 编号（一律不影响退出码）。
const (
	// W9 提案十项必备缺项（照常创建提案 + 进 warnings[]）。
	W9 = "W9"
	// W10 remove_relation 未命中任何既有关系（幂等 no-op：零写入、零 commit）。
	W10 = "W10"
	// W11 deprecate 目标已 deprecated / restore 目标已 active / undelete 目标未被删除。
	W11 = "W11"
	// W12 set_replaced_by 的 target 是 deprecated 且未删除（照常写入 + 进报告提示）。
	W12 = "W12"
)

// ErrorCodes 是 M3 收口后的 error 编号全集（恰 E1–E10，声明顺序即编号顺序）。
func ErrorCodes() []string {
	return []string{E1, E2, E3, E4, E5, E6, E7, E8, E9, E10}
}

// WarningCodes 是 M3 收口后的 warning 编号全集（恰 W1–W12）。
func WarningCodes() []string {
	return []string{W1, W2, W3, W4, W5, W6, W7, W8, W9, W10, W11, W12}
}

// InfoCodes 是 info 编号全集（**恰 I1**：M3 不新增 info 编号）。
func InfoCodes() []string { return []string{I1} }

// AllCodes 是编号闭合集合 `E1..E10 ∪ W1..W12 ∪ {I1}`（共 23 个）。
// 未编号 warning 的 Code 是空串（Unnumbered），不在本集合内、也不参与闭合断言。
func AllCodes() []string {
	out := make([]string, 0, len(ErrorCodes())+len(WarningCodes())+len(InfoCodes()))
	out = append(out, ErrorCodes()...)
	out = append(out, WarningCodes()...)
	return append(out, InfoCodes()...)
}

// IsKnownCode 报告某个 code 字面量是否在闭合集合内（空串是未编号 warning，不算越界）。
func IsKnownCode(code string) bool {
	return code == Unnumbered || inList(AllCodes(), code)
}

// DiagnosticFields 是诊断载荷的字段名（**恰六个**，与 CLI 合同 §5 同序）。
// M3 不新增第七个字段，由 TestDiagnosticPayload_SixFields 反射断言。
func DiagnosticFields() []string {
	return []string{"code", "level", "path", "op_index", "message", "target"}
}

// CodeForProposalViolation 把 internal/proposal 的结构化违规映射到 M3 的 error 编号。
//
// 接驳口径（proposal/state.go 的 DiagClass 注释逐字要求「037 的映射表照此接驳」）：
//   - DiagEnumOrConsistency（status / execution.status 越界、decision.result 不一致）→ **E8**；
//   - DiagIllegalTransition（12 条非法迁移之一，含终态出边）→ **E9**；
//   - DiagSupersededChain（`superseded` 但 superseded_by 缺失 / 无法解析到存在的提案，
//     M3 · T-…-035 追加的第三族）→ **E7**（复用既有编号，不新增第五个 error）；
//   - 其余形态违规（frontmatter 键集合、正文分区、ID 形态…）不是本表的编号对象，返回空串，
//     由调用点自行选择更贴切的既有编号（如 E4 / E5）或按 W9 缺项处理。
//
// internal/proposal **不出现任何编号字面量**：编号只在本包发。
func CodeForProposalViolation(err error) string {
	kind := proposal.KindOf(err)
	if proposal.DiagClassOfSupersededChain(kind) == proposal.DiagSupersededChain {
		return E7
	}
	switch proposal.DiagClassOf(kind) {
	case proposal.DiagEnumOrConsistency:
		return E8
	case proposal.DiagIllegalTransition:
		return E9
	default:
		return ""
	}
}

// UnreachableStateCombo 报告落盘的 `status` × `execution` 组合是否命中提案合同 §4.2
// 可达矩阵的 🔴 / ⚠️ 格（命中 → **E9**）。
//
// 矩阵 12 格里 ✅ **恰 6 格**：`approved` 行三格全可达，其余三态**只有** `not_started`
// 可达。因此判据可逐字归约成一句：**非 approved 的提案，execution.status 必须是
// not_started**（`pending`/`rejected` × 执行结果 = 🔴「未批准 / 已拒绝不可能执行过」，
// `superseded` × `succeeded` = 🔴，`superseded` × `failed` = ⚠️ 本合同新定禁止）。
//
// **矩阵本体自 T-…-036 起只有一份实现**：internal/proposal 的 ReachabilityMatrix / Reachable
// （4 × 3 全集表驱动生成）。本函数只做两件事——把枚举越界让给 E8 先说话，
// 再把矩阵的判定取反成「是否命中不可达格」，从而映射到 E9。**本包不另写一套矩阵。**
func UnreachableStateCombo(status proposal.Status, exec proposal.ExecStatus) bool {
	if !status.Valid() || !exec.Valid() {
		// 枚举越界（含键缺失的空值）属 E8 的判定对象：矩阵不抢它的话，也不重复表态。
		return false
	}
	return !proposal.Reachable(status, exec)
}

// proposalStateError 拼一条提案状态类 error（E7 / E8 / E9 共用的诊断形态）。
func proposalStateError(code string, opIndex int, field, id, msg string,
	args ...interface{}) Diagnostic {
	d := errorAt(code, opIndex, field, "%s", fmt.Sprintf(msg, args...))
	d.Target = id
	return d
}
