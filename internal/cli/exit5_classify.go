package cli

import (
	"errors"

	"github.com/ikaqiu-Lemon/EverGreen/internal/txn"
)

// exit5_classify.go —— 把 TxnBlockedError 按诊断码/哨兵分类为退出码 5 的两类类型化错误。
//
// 约束：
//   - 退出码数字仍只在 ExitCodeFor 翻译一次；这里仅做「错误类型」归类。
//   - 禁止在 internal/cli 写 E15/E16 字面量：仅引用 txn 常量并读取搬运后的诊断载荷。
//   - 只对 *TxnBlockedError 生效；其余错误原样返回。

// classifyExit5 如果 err（或其 unwrap 链）里含 *TxnBlockedError，则按诊断码把它提升为
// *PrecheckFailedError 或 *LockUnavailableError（两者都 Unwrap 到内层 *TxnBlockedError）。
func classifyExit5(err error) error {
	if err == nil {
		return nil
	}
	// 已分类则幂等。
	var (
		pf *PrecheckFailedError
		lu *LockUnavailableError
	)
	if errors.As(err, &pf) || errors.As(err, &lu) {
		return err
	}
	var tb *TxnBlockedError
	if !errors.As(err, &tb) {
		return err
	}
	// 锁不可用（E16）：优先用 sentinel（最硬），其次看诊断载荷。
	if errors.Is(err, txn.ErrLockUnavailable) || hasCliDiag(tb, txn.CodeLockTimeout) {
		return &LockUnavailableError{tb}
	}
	// 写前强校验失败（E15）。
	if hasCliDiag(tb, txn.CodePrecheckFailed) {
		return &PrecheckFailedError{tb}
	}
	// 兜底：TxnBlockedError 的 CLI 层 Diags 可能为空（如 enterTxnCritical 的
	// blockedError 只搬 .Err），此时改看 **底层 txn 错误自带的诊断码**。txn 包给
	// 恢复屏障五形态、seq 溢出、损坏 intent、路径越界、扫描全集异常、reserved 类型违规等
	// 都实装了 Code() —— 它们都属 E15「写前安全复核」语义大类，一律归 PrecheckFailedError；
	// 锁超时归 E16。这样「8 个原因共用 E15 / 锁忙共用 E16」在本单点闭合，不必逐处搬诊断。
	var c codedTxnError
	if errors.As(err, &c) {
		switch c.Code() {
		case txn.CodeLockTimeout:
			return &LockUnavailableError{tb}
		case txn.CodePrecheckFailed:
			return &PrecheckFailedError{tb}
		}
	}
	return err
}

// codedTxnError 是 txn 层「携诊断码的错误」在 cli 侧的最小镜像（txn 的 coded 未导出）。
// 只用来读 Code()，不产码、不写 E15/E16 字面量（码值仍取自 txn 常量）。
type codedTxnError interface{ Code() string }

func hasCliDiag(tb *TxnBlockedError, code string) bool {
	if tb == nil {
		return false
	}
	for _, d := range tb.Diagnostics() {
		if d.Code == code {
			return true
		}
	}
	return false
}
