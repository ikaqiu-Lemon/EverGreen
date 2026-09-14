package cli

import (
	"errors"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/txn"
)

// exit5_test.go —— R7 定稿形态（合同 §17.2 / §17.4）的三支反证：常量值恰 5、
// 两类类型化错误经 ExitCodeFor 各映射为 5 且 errors.As 链穿透 071 / 072 的 sentinel。
// 判据 12（V-R7-1 ~ V-R7-2）。

// TestExitPrecheckOrLockIsFive：常量值恰 5，且退出码常量表内取值为 5 的导出常量**恰一个**。
func TestExitPrecheckOrLockIsFive(t *testing.T) {
	if ExitPrecheckOrLock != 5 {
		t.Fatalf("ExitPrecheckOrLock = %d，合同 §17.1 定死为 5", ExitPrecheckOrLock)
	}
	// 取值为 5 的导出退出码常量恰一个：5 是「两类成因共用一码」，不得为两类各定一个值为 5 的常量。
	fives := map[string]int{
		"ExitOK":             ExitOK,
		"ExitUsage":          ExitUsage,
		"ExitValidation":     ExitValidation,
		"ExitPartialWrite":   ExitPartialWrite,
		"ExitCommitFailed":   ExitCommitFailed,
		"ExitPrecheckOrLock": ExitPrecheckOrLock,
		"ExitNeedConfirm":    ExitNeedConfirm,
	}
	n5 := 0
	for _, v := range fives {
		if v == 5 {
			n5++
		}
	}
	if n5 != 1 {
		t.Fatalf("退出码常量表内取值为 5 的常量数 = %d，必须恰 1（一码两成因）", n5)
	}
	if !ExitCode5Enabled() {
		t.Fatal("ExitCode5Enabled() 应恒为 true（M6 已启用退出码 5）")
	}
}

// TestExitCodeForPrecheckFailed：PrecheckFailedError 经 ExitCodeFor 得 5，
// 且 errors.As 链穿透到 072 的 sentinel（这里用 B-R3 冲突错误），errors.Is 亦可穿透。
func TestExitCodeForPrecheckFailed(t *testing.T) {
	inner := &txn.RecoverConflictError{
		TxnID:     "txn-0001",
		Conflicts: []txn.ConflictFile{{Path: "domains/ai/k-x.md", Reason: "post-crash 外部编辑"}},
	}
	err := &PrecheckFailedError{blockedError("写前强校验失败，本次请求事务零权威写入", inner)}

	if got := ExitCodeFor(err); got != ExitPrecheckOrLock {
		t.Fatalf("ExitCodeFor(PrecheckFailedError) = %d，应为 %d", got, ExitPrecheckOrLock)
	}
	// errors.As 命中最外层类型化错误。
	var pf *PrecheckFailedError
	if !errors.As(err, &pf) {
		t.Fatal("errors.As 未命中最外层 *PrecheckFailedError")
	}
	// errors.As 继续穿透到内层 *TxnBlockedError（capture 等既有消费点仍拿得到）。
	var tb *TxnBlockedError
	if !errors.As(err, &tb) {
		t.Fatal("errors.As 未穿透到内层 *TxnBlockedError")
	}
	// errors.As 一路穿透到 072 的 sentinel（恢复冲突错误）。
	var rce *txn.RecoverConflictError
	if !errors.As(err, &rce) {
		t.Fatal("errors.As 未穿透到 072 的 *txn.RecoverConflictError sentinel")
	}
	// 诊断搬运：E15 必须如实出现在载荷里（不丢码）。
	if !hasDiagCode(err, txn.CodePrecheckFailed) {
		t.Fatalf("PrecheckFailedError 的诊断缺 %s（写前强校验失败必须携该码）", txn.CodePrecheckFailed)
	}
}

// TestExitCodeForLockUnavailable：LockUnavailableError 经 ExitCodeFor 得 5，
// A 类写命令与 B 类 index 维护命令各一例，errors.Is 穿透 txn.ErrLockUnavailable。
func TestExitCodeForLockUnavailable(t *testing.T) {
	cases := []struct {
		name string
		msg  string
	}{
		{"A 类写命令锁忙", "获取 run.lock 失败，本次零写入"},
		{"B 类 index 维护命令锁忙", "eg index build 获取 run.lock 失败，.index/ 零字节写入"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inner := &txn.LockTimeoutError{}
			err := &LockUnavailableError{blockedError(c.msg, inner)}

			if got := ExitCodeFor(err); got != ExitPrecheckOrLock {
				t.Fatalf("ExitCodeFor(LockUnavailableError) = %d，应为 %d", got, ExitPrecheckOrLock)
			}
			var lu *LockUnavailableError
			if !errors.As(err, &lu) {
				t.Fatal("errors.As 未命中最外层 *LockUnavailableError")
			}
			if !errors.Is(err, txn.ErrLockUnavailable) {
				t.Fatal("errors.Is 未穿透到 txn.ErrLockUnavailable sentinel")
			}
			if !hasDiagCode(err, txn.CodeLockTimeout) {
				t.Fatalf("LockUnavailableError 的诊断缺 %s（锁不可用必须携该码）", txn.CodeLockTimeout)
			}
		})
	}
}

// hasDiagCode 报告错误的诊断载荷里是否含指定码（沿 diagnoser 接口读取）。
func hasDiagCode(err error, code string) bool {
	var d diagnoser
	if !errors.As(err, &d) {
		return false
	}
	for _, one := range d.Diagnostics() {
		if one.Code == code {
			return true
		}
	}
	return false
}
