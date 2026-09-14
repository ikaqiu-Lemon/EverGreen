package cli

import (
	"errors"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/txn"
)

// exit5_classify_test.go —— classifyExit5 的核心反证（合同 §17.1 / §17.4）。
//
// classifyExit5 是「把 *TxnBlockedError 按诊断码/哨兵提升为退出码 5 的两类类型化错误」
// 的唯一归类点。它的正确性有两面，缺一不可：
//   - 正证：携 E16 的阻断被归为 LockUnavailableError、携 E15 的被归为 PrecheckFailedError，
//     经唯一翻译点 ExitCodeFor 落 5；
//   - 反证：非 *TxnBlockedError 一律原样透出（绝不无中生有一个 5），既有 1/2/4 分类不被抹平；
//     且**不携** E15/E16 的 *TxnBlockedError 不得被提升到 5（只有 fail-closed 语义才配 5）。

// TestClassifyExit5LockBusyPromoted：携 E16（锁不可用哨兵）的裸 *TxnBlockedError
// 被归为 *LockUnavailableError，落 5。
func TestClassifyExit5LockBusyPromoted(t *testing.T) {
	raw := blockedError("获取 run.lock 失败，本次零写入", &txn.LockTimeoutError{})
	// 前提反证：归类前它只是裸 *TxnBlockedError，尚未是任何一类 5 号错误。
	var pre *LockUnavailableError
	if errors.As(error(raw), &pre) {
		t.Fatal("前提不成立：裸 TxnBlockedError 归类前不应已是 *LockUnavailableError")
	}
	got := classifyExit5(raw)
	var lu *LockUnavailableError
	if !errors.As(got, &lu) {
		t.Fatalf("携 E16 的阻断应被归为 *LockUnavailableError，实得 %T", got)
	}
	if code := ExitCodeFor(got); code != ExitPrecheckOrLock {
		t.Fatalf("归类后经 ExitCodeFor 应落 %d，实得 %d", ExitPrecheckOrLock, code)
	}
}

// TestClassifyExit5PrecheckPromoted：携 E15（写前强校验失败）的裸 *TxnBlockedError
// 被归为 *PrecheckFailedError，落 5，且不误判为锁不可用。
func TestClassifyExit5PrecheckPromoted(t *testing.T) {
	inner := &txn.RecoverConflictError{
		TxnID:     "txn-0007",
		Conflicts: []txn.ConflictFile{{Path: "domains/ai/k-x.md", Reason: "post-crash 外部编辑"}},
	}
	raw := blockedError("恢复屏障无法安全完成，本次请求事务零权威写入", inner)
	got := classifyExit5(raw)
	var pf *PrecheckFailedError
	if !errors.As(got, &pf) {
		t.Fatalf("携 E15 的阻断应被归为 *PrecheckFailedError，实得 %T", got)
	}
	// 不得被误判为锁不可用（E15 与 E16 是两类成因，绝不能混线）。
	var lu *LockUnavailableError
	if errors.As(got, &lu) {
		t.Fatal("携 E15 的阻断被误判为 *LockUnavailableError（E15/E16 混线）")
	}
	if code := ExitCodeFor(got); code != ExitPrecheckOrLock {
		t.Fatalf("归类后经 ExitCodeFor 应落 %d，实得 %d", ExitPrecheckOrLock, code)
	}
}

// TestClassifyExit5NilStaysNil：nil 恒返回 nil（不制造错误）。
func TestClassifyExit5NilStaysNil(t *testing.T) {
	if got := classifyExit5(nil); got != nil {
		t.Fatalf("classifyExit5(nil) = %v，应为 nil", got)
	}
}

// TestClassifyExit5Idempotent：已归类的错误再归类一次，类型与退出码都不变（幂等）。
func TestClassifyExit5Idempotent(t *testing.T) {
	once := classifyExit5(blockedError("获取 run.lock 失败", &txn.LockTimeoutError{}))
	twice := classifyExit5(once)
	var lu *LockUnavailableError
	if !errors.As(twice, &lu) {
		t.Fatalf("二次归类后应仍是 *LockUnavailableError，实得 %T", twice)
	}
	if ExitCodeFor(once) != ExitCodeFor(twice) {
		t.Fatalf("幂等性破坏：一次 %d、二次 %d", ExitCodeFor(once), ExitCodeFor(twice))
	}
}

// TestClassifyExit5PassthroughNonTxn（反证）：非 *TxnBlockedError 原样透出，
// 既有 1/2/4 分类绝不被抹平成 5。
func TestClassifyExit5PassthroughNonTxn(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"UsageError 仍是 1", &UsageError{Msg: "参数非法"}, ExitUsage},
		{"ValidationError 仍是 2", &ValidationError{Msg: "校验失败"}, ExitValidation},
		{"CommitFailedError 仍是 4", &CommitFailedError{Msg: "Git 提交失败", Err: errors.New("boom")}, ExitCommitFailed},
		{"裸 error 仍走未分类兜底 1", errors.New("不明原因"), ExitUsage},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classifyExit5(c.err)
			if got != c.err {
				t.Fatalf("非 TxnBlockedError 应原样透出，却被改写为 %T", got)
			}
			if code := ExitCodeFor(got); code != c.want {
				t.Fatalf("退出码被误改：期望 %d，实得 %d（绝不能兜底成 5）", c.want, code)
			}
		})
	}
}

// TestClassifyExit5CodelessTxnBlockedNotPromoted（反证 · 最关键的一条）：
// 一个**不携** E15/E16 的 *TxnBlockedError（既非锁不可用也非写前强校验失败）
// 绝不被提升到 5 —— 只有 fail-closed 的两类语义才配退出码 5，其余仍走未分类兜底 1。
func TestClassifyExit5CodelessTxnBlockedNotPromoted(t *testing.T) {
	// blockedError 对不携诊断的底层错误只会挂一条空码兜底诊断，既无 E15 也无 E16。
	raw := blockedError("某个既非锁忙也非写前校验的写前阻断", errors.New("plain"))
	if hasDiagCode(raw, txn.CodeLockTimeout) || hasDiagCode(raw, txn.CodePrecheckFailed) {
		t.Fatal("用例前提不成立：这个裸阻断不应携 E15/E16")
	}
	got := classifyExit5(raw)
	var pf *PrecheckFailedError
	var lu *LockUnavailableError
	if errors.As(got, &pf) || errors.As(got, &lu) {
		t.Fatalf("不携 E15/E16 的阻断被错误提升到 5（%T）", got)
	}
	if code := ExitCodeFor(got); code != ExitUsage {
		t.Fatalf("不携 E15/E16 的阻断应走未分类兜底 %d，实得 %d", ExitUsage, code)
	}
}
