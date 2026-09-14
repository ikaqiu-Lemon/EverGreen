package cli

// 退出码 `6`（仅缺用户确认）的**判定顺序**与**启用边界**（合同 §4 的 S2/M3 增量）。
//
// 合同落点：`eg proposal approve` 与 `eg delete` 的「校验已过、只差 --confirm」分支。
// 本文件只定常量、白名单与一个纯判定函数，**不做任何输出、不调 os.Exit**：
// 退出码到进程的翻译仍然只发生在 exit.go 的 ExitCodeFor + cmd/eg/main.go 一处（§4 硬约束）。
//
// 为什么把顺序做成表驱动的纯函数：`1` / `2` / `6` 三者的优先级一旦散落到各命令的
// if 分支里，就会出现「参数写错却先提示缺确认」这类语义漂移 —— 那等于把「校验已通过」
// 这个前提悄悄放弃掉。顺序在这里锁死一次，各命令只负责如实报三个事实。
//
// `6` 的语义边界（不可放宽）：**权威 Markdown 完全不变**。它出现时磁盘零写入、无 commit，
// 因此它不是「部分成功」，status 仍派生为 failed（StatusFor 的 default 分支）。

import "sort"

// ExitNeedConfirm 是退出码 6：**校验已通过、仅缺用户确认**，权威 Markdown 完全不变。
//
// 与 2 的区别是前提：2 表示「校验没过」，6 表示「校验全过了，只差用户点头」。
// 因此 6 的优先级必然低于 1 与 2 —— 校验都没过时谈不上「只差确认」。
const ExitNeedConfirm = 6

// ExitPrecheckOrLock 是退出码 5：**写前强校验失败（E15）或锁不可用（E16）**，两类情形共用一个码。
//
// M6 已启用，成因恰两类（合同 §9 / §17.1，A-58 = ①）：
//   - **写前强校验失败**（`PrecheckFailedError`，携 `E15`）：`S3` 锁内重读重算后的强校验失败，
//     含 `--strict` 升级面、B3 前像不匹配的 error 面、以及 `S2` 恢复屏障无法安全完成的五形态、
//     `seq` 溢出、`.index/` reserved entry 类型违规（后者只在 A / B 类写路径）；
//   - **锁不可用**（`LockUnavailableError`，携 `E16`）：`run.lock` 等待超时（A 类写命令与 B 类
//     index 维护命令）。
//
// 两类情形均**零权威写入**（口径见合同 §9.1）。落地形态是「常量 + 两类类型化错误 +
// `ExitCodeFor` 恰两条 `errors.As` 分支」——**不是**在本文件触发 5 号进程直退（R1 ~ R6 的该
// 表述已由 R7 撤销）：`internal/**` 一律不触发进程退出，退出仍只发生在 `cmd/eg/main.go`。
//
// 常量单独放在一个 const 块（而非 `const ExitPrecheckOrLock = 5` 单行）：机器验证
// V-R7-1 的锚点 grep 逐字要求行首缩进后即 `ExitPrecheckOrLock = 5`，不得夹 `const` 关键字。
const (
	ExitPrecheckOrLock = 5
)

// —— 启用边界：一个封闭的命令白名单 ——

// 命令名（与命令注册处的完整命令名逐字一致，用于白名单断言）。
const (
	// CmdProposalApprove 是 `eg proposal approve`：批准前必须拿到用户确认。
	CmdProposalApprove = "proposal approve"
	// CmdDelete 是 `eg delete`：逻辑删除前必须拿到用户确认。
	CmdDelete = "delete"
)

// needConfirmCommands 是允许返回退出码 6 的命令**封闭集合**（恰两条）。
//
// 只有「会改动权威 Markdown 且必须由用户拍板」的命令才配有确认门；
// 其余命令即使缺参数也只能是 1 / 2，不得借用 6 表达别的语义。
var needConfirmCommands = []string{CmdProposalApprove, CmdDelete}

// NeedConfirmCommands 返回启用退出码 6 的命令白名单（升序副本，供测试直接断言）。
func NeedConfirmCommands() []string {
	out := append([]string{}, needConfirmCommands...)
	sort.Strings(out)
	return out
}

// NeedConfirmEnabled 报告某条命令是否允许返回退出码 6。
func NeedConfirmEnabled(command string) bool {
	for _, c := range needConfirmCommands {
		if c == command {
			return true
		}
	}
	return false
}

// ExitCode5Enabled 报告退出码 5 是否启用 —— M6 起恒 true（`ExitPrecheckOrLock` 值恰 5）。
// 以函数而非注释表达，是为了让「5 已启用」这条边界可被测试直接消费。
func ExitCode5Enabled() bool { return true }

// —— 判定顺序：参数错 1 → 校验失败 2 → 仅缺确认 6 ——

// ConfirmDecision 是一次退出码判定的三个输入事实（由命令层如实填写，不含任何顺序逻辑）。
//
// 三者都是**已知事实**而非猜测：ArgsValid 由参数解析给出，ValidationPassed 由校验器给出，
// ConfirmMissing 由 --confirm 标志给出。判定函数只排序，不推断缺失事实。
type ConfirmDecision struct {
	// ArgsValid 参数是否合法（false → 1）。
	ArgsValid bool
	// ValidationPassed 校验是否全部通过（false → 2）。
	ValidationPassed bool
	// ConfirmMissing 是否缺用户确认（true 且前两项都成立 → 6）。
	ConfirmMissing bool
}

// ExitCodeForConfirm 是 1 / 2 / 6 的**唯一**顺序裁决点。
//
// 固定次序（不可重排）：参数错 1 → 校验失败 2 → 仅缺确认 6 → 否则 0。
// 三个事实都成立时返回 ExitOK，表示「可以继续执行」，而不是「已经执行完」。
func ExitCodeForConfirm(d ConfirmDecision) int {
	switch {
	case !d.ArgsValid:
		return ExitUsage
	case !d.ValidationPassed:
		return ExitValidation
	case d.ConfirmMissing:
		return ExitNeedConfirm
	}
	return ExitOK
}

// NeedConfirmError → 退出码 6（校验已通过、仅缺用户确认；权威 Markdown 完全不变）。
//
// Command 必须在 NeedConfirmCommands() 白名单内 —— 命令层自证边界；
// 不在白名单内时 ExitCodeFor 拒绝给 6（退化为 1），封闭集合因此守得住。
type NeedConfirmError struct {
	Command string
	Msg     string
	Diags   []Diagnostic
}

func (e *NeedConfirmError) Error() string { return e.Msg }

// Diagnostics 实现 diagnoser。
func (e *NeedConfirmError) Diagnostics() []Diagnostic { return e.Diags }

// ExitCode 给出该错误对应的退出码：白名单内 6，白名单外退化为 1（不是新语义，是实现 bug 信号）。
func (e *NeedConfirmError) ExitCode() int {
	if NeedConfirmEnabled(e.Command) {
		return ExitNeedConfirm
	}
	return ExitUsage
}

// —— 退出码 5 的两类类型化错误（R7 定稿形态，合同 §17.1）——
//
// 两者都**包裹** *TxnBlockedError：写前阻断 / 基础设施失败的「零权威写入、无 commit」
// 事实与诊断搬运全由 TxnBlockedError 承担，这里只在其上**新增一层类型信息**，好让
// ExitCodeFor 用恰两条 errors.As 分支把它们翻译成 5。刻意不各自复制 Msg / Err / Diags：
// 复制会让「诊断搬运」出现第二套实现，而两类错误与普通 TxnBlockedError 的诊断是同一批。
//
// **Unwrap 到内层 *TxnBlockedError**（而不是直接到 txn 的 sentinel）：这样
//   - errors.As(err, &PrecheckFailedError / &LockUnavailableError) —— 命中最外层；
//   - errors.As(err, &TxnBlockedError) —— 命中内层（capture 等既有消费点仍拿得到）；
//   - errors.Is(err, txn.ErrLockUnavailable / txn.ErrLockPathUnsafe …) —— 经内层
//     TxnBlockedError.Unwrap 继续穿透到 071 / 072 的 sentinel。
// 三条链路一次满足，无需在本层重抄任何码字面量（诊断码闭合门禁：E15 / E16 的字面量只许
// 落在 internal/txn，本包一律走常量与运行期搬运）。

// PrecheckFailedError → 退出码 5（**写前强校验失败**，携 E15）。
//
// 承载「写前安全复核」这一语义大类：S3 强校验失败（含 --strict 升级面、B3 前像不匹配）、
// S2 恢复屏障五形态、seq 溢出，以及 .index/ reserved entry 类型违规**在 A / B 类写路径上**
// 的那一支。共同事实：本次请求事务零权威写入、无 commit、事务保持未闭合、不发 W26。
type PrecheckFailedError struct{ *TxnBlockedError }

// Unwrap 暴露内层 *TxnBlockedError（它再 Unwrap 到 071 / 072 的 sentinel）。
func (e *PrecheckFailedError) Unwrap() error { return e.TxnBlockedError }

// LockUnavailableError → 退出码 5（**锁不可用**，携 E16）。
//
// 承载 run.lock 等待超时：A 类写命令与 B 类 index 维护命令锁忙。此时连事务都没开始，
// 整条命令磁盘零变化（合同 §9.1）。
type LockUnavailableError struct{ *TxnBlockedError }

// Unwrap 暴露内层 *TxnBlockedError（它再 Unwrap 到 071 / 072 的 sentinel）。
func (e *LockUnavailableError) Unwrap() error { return e.TxnBlockedError }
