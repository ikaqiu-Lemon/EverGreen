package proposal

// `status` × `execution` 的 **4 × 3 可达矩阵**（提案合同 §4.2；T-evergreen.s1_main_flow-158614-036）。
//
// # 正交的精确含义
//
// 两维**各自独立取值、互不推导**：`approved` 推不出 `succeeded`（批准只是用户决定），
// `succeeded` 也不回写 `status`（执行结果不改用户决定）。正交**不等于**任意组合都能落盘 ——
// 12 格里 ✅ **恰 6 格**，其余 6 格是「事实上不可能发生」的组合，落盘即实现缺陷。
//
// # 12 格（表驱动：4 × 3 全集由 allStatuses() × ExecStatuses() 生成，禁止手写枚举漏项）
//
//	                not_started   succeeded        failed
//	pending         ✅            🔴               🔴
//	approved        ✅            ✅               ✅
//	rejected        ✅            🔴               🔴
//	superseded      ✅            🔴               ⚠️（本合同新定禁止）
//
// 🔴 是「原文即可推出」的禁止：未批准 / 已拒绝的提案不可能产生执行结果；
// ⚠️ 是本合同**新定**的一格（M-2）：`superseded` 的语义是「**不强行执行**」，
// 与「执行已产生副作用」互斥，因此同样禁止 —— 两类禁止在**判定与编号上完全一致**
// （都不可达、都走同一个既有 error 编号），差别只在报告文案里说明来源。
//
// # 编号与本包的边界
//
// 本包**不出现任何编号字面量**：命中不可达格返回 ViolationIllegalTransition
// （与 T-…-034 的非法迁移同一族 DiagIllegalTransition），由 internal/plan 的映射表
// 统一发既有的那个 error 编号（不新增编号）。internal/plan 的 UnreachableStateCombo
// 直接委托本文件的 Reachable —— 全仓**只有这一份矩阵实现**。
//
// # 使用位置
//
//   - 落盘校验（internal/plan）：读到的提案命中不可达格 → error、退 2、零写入；
//   - 回写前的最后一道组合校验（execution.go 的 RecordExecution）：候选组合不可达即**拒写**，
//     因此本工具自己永远写不出一个不可达组合（040 / 041 的落盘前同样过这一道）。

import "fmt"

// Verdict 是矩阵一格的判定结论（恰三值）。
type Verdict string

// 三个判定结论。
const (
	// VerdictReachable 是 ✅：组合可以落盘。
	VerdictReachable Verdict = "reachable"
	// VerdictForbidden 是 🔴：原文即可推出的禁止组合。
	VerdictForbidden Verdict = "forbidden"
	// VerdictNewlyForbidden 是 ⚠️：本合同新定禁止的那一格（superseded × failed，M-2）。
	VerdictNewlyForbidden Verdict = "newly_forbidden"
)

// Verdicts 返回判定结论的封闭集合。
func Verdicts() []Verdict {
	return []Verdict{VerdictReachable, VerdictForbidden, VerdictNewlyForbidden}
}

// Cell 是矩阵的一格：两维取值 + 判定 + 判定理由（理由只进报告文本，不参与判定）。
type Cell struct {
	Status  Status
	Exec    ExecStatus
	Verdict Verdict
	Why     string
}

// Reachable 报告该格是否可落盘。
func (c Cell) Reachable() bool { return c.Verdict == VerdictReachable }

// ReachabilityMatrix 返回**全部 12 格**：4 × 3 全集由两个封闭集合的笛卡尔积生成。
//
// 行序 = allStatuses() 的顺序，列序 = ExecStatuses() 的顺序；集合一旦增删，
// 这里的行数自动跟着变（用例断言 len == 4 × 3 与 ✅ 恰 6 格，漏项立刻红）。
func ReachabilityMatrix() []Cell {
	out := make([]Cell, 0, len(allStatuses())*len(ExecStatuses()))
	for _, st := range allStatuses() {
		for _, ex := range ExecStatuses() {
			out = append(out, CellFor(st, ex))
		}
	}
	return out
}

// CellFor 返回一格的判定（矩阵的**唯一**判据实现）。
//
// 归约成一句话：**只有 approved 行的三种执行结果全可达，其余三态只有 not_started 可达**。
// 两维取值必须先在封闭集合内 —— 越界取值不是矩阵的判定对象（枚举越界另有判据先说话），
// 这里保守判为不可达并在 Why 里说明。
func CellFor(st Status, ex ExecStatus) Cell {
	c := Cell{Status: st, Exec: ex}
	switch {
	case !st.Valid() || !ex.Valid():
		c.Verdict = VerdictForbidden
		c.Why = fmt.Sprintf("两维取值必须先在封闭集合内（%s / %s）：枚举越界不是矩阵的判定对象",
			joinStatuses(), joinExecStatuses())
	case ex == ExecNotStarted:
		c.Verdict = VerdictReachable
		c.Why = fmt.Sprintf("%s 是默认值：任何 status 下「尚未执行」都成立", ExecNotStarted)
	case st == StatusApproved:
		c.Verdict = VerdictReachable
		c.Why = fmt.Sprintf("只有 %s 的提案会被执行，因此三种执行结果全可达（%s ≠ 已执行，两维互不推导）",
			StatusApproved, StatusApproved)
	case st == StatusSuperseded && ex == ExecFailed:
		c.Verdict = VerdictNewlyForbidden
		c.Why = fmt.Sprintf("%s 的语义是「%s」，与「执行已产生副作用」互斥（本合同新定禁止）",
			StatusSuperseded, NotExecutedNotice)
	case st == StatusSuperseded:
		c.Verdict = VerdictForbidden
		c.Why = fmt.Sprintf("执行成功后不会再把 %s 改成 %s", StatusApproved, StatusSuperseded)
	default:
		c.Verdict = VerdictForbidden
		c.Why = fmt.Sprintf("status=%s 的提案未被批准执行，不可能有执行结果", st)
	}
	return c
}

// Reachable 报告 status × execution 组合是否落在 ✅ 格内。
func Reachable(st Status, ex ExecStatus) bool { return CellFor(st, ex).Reachable() }

// CheckReachable 判定组合是否可落盘：命中 🔴 / ⚠️ 格返回 ViolationIllegalTransition
// （DiagClassOf → DiagIllegalTransition，由 internal/plan 映射成既有 error 编号）。
func CheckReachable(st Status, ex ExecStatus) error {
	c := CellFor(st, ex)
	if c.Reachable() {
		return nil
	}
	return violate(ViolationIllegalTransition, KeyExecBlock+"."+KeyExecStatus,
		"status=%s × %s.%s=%s 命中可达矩阵不可达格（%s）：%s",
		st, KeyExecBlock, KeyExecStatus, ex, c.Verdict, c.Why)
}

// ReachableCount 返回矩阵里 ✅ 的格数（用例据此断言「恰 6 格」）。
func ReachableCount() int {
	n := 0
	for _, c := range ReachabilityMatrix() {
		if c.Reachable() {
			n++
		}
	}
	return n
}
