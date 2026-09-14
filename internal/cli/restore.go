package cli

// `eg restore` 的写路径（授权合同 §2 矩阵 #3；状态合同 §8.1 的 `restore` op；T-…-039）。
//
// 与 `eg deprecate` 逐字共用 runStatusCommand → buildStateOpPlan → runPlan 一条链路，
// 差别只有 op 名（落盘写 `status: active`）。同一条链路意味着 W7 / W11 / 矩阵 #3 /
// B1–B4 对两条命令是同一套代码事实，不存在第二份实现。
//
// **红线（本命令的关键否定语义）**：restore **不以「存在有效 support」为前提**。
// 系统不做这道拦截，也不输出任何「依据不够」类标记——依据是否充分属人的判断，
// 该提示归后续 S3；在此处替用户判断等于把工具变成裁判（EG-KNW-06 三道物理约束之外
// 不新增第四道）。因此本文件既无 support 计数、也无任何据此改变退出码的分支。

import "github.com/ikaqiu-Lemon/EverGreen/internal/plan"

// runRestore 实现 eg restore --target <id> --reason <text>。
func (r *Root) runRestore(inv *Invocation) (*Result, error) {
	// 无 support 前置判定：目标卡有没有有效依据一律不查，直接合成 op 交给 plan 层。
	return r.runStatusCommand(inv, plan.OpRestore, "restore（status → active）")
}
