package cli

// index_after_write_proposal_test.go —— M6 · T-…-072 批次 C4a：`eg proposal new` /
// `eg proposal reject` 的 **S8 写后索引同步**成功路径反证（合同 §16.1 / §16.3）。
//
// 为什么住在 index* 文件里：命令层的文件级位置锁（cmd/eg/arch_test.go 的
// TestStage4IndexPackageBoundary）规定 `internal/cli/` 下**只有 index 前缀的文件**可以触达
// 索引判据（本文件借同包的 cap* 助手直接读 `eg index status`）。这是「索引不作为任何命令
// 前置」的机器形态；用例名以 TestProposalNew / TestProposalReject 开头，不影响 -run 归类。
//
// 本组只钉成功路径的一条主线：proposal 写命令产生 commit 把 HEAD 推进后，必须在 S7 之后、
// Release 之前，于**同一把 run.lock** 内跑唯一的 syncIndexAfterWrite，把索引水位线带到新
// HEAD；releasing 只发生一次（显式 release + defer 幂等），且 index_sync 记的就是本次事务号。

import (
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/proposal"
)

// countTxnStep 数某编排节点在本次命令里触发了几次。
func countTxnStep(rec *txnSteps, name string) int {
	n := 0
	for _, s := range rec.names {
		if s == name {
			n++
		}
	}
	return n
}

// TestProposalNewSyncsIndexInsideLock：新建提案的 commit 把 HEAD 推进后，S8 在同一把锁内、
// S7 之后、Release 之前执行，索引水位线随 HEAD 一起前进；releasing 恰一次。
func TestProposalNewSyncsIndexInsideLock(t *testing.T) {
	dir := proposalVault(t)

	// 在最后一次基线 commit 后构建 healthy 索引，并确认它已收敛到当前 HEAD。
	if code, _, errOut := runIndexCLI(t, dir, "build"); code != ExitOK {
		t.Fatalf("前置 index build 退出码 = %d：%s", code, errOut)
	}
	capAssertIndexConverged(t, dir, "proposal new 前置")
	headBefore := capHeadOf(t, dir)

	rec := watchTxn(t, nil)
	code, env, errOut := runProposalCLI(t, dir,
		"new", "--type", string(proposal.TypeLogicalDelete), "--target", applyCardID,
		"--reason", "该卡已过时")
	if code != ExitOK {
		t.Fatalf("proposal new 退出码 = %d，期望 0：%s", code, errOut)
	}

	// 前置有效性：本次确实产生了 commit，HEAD 真的动了 —— 否则「水位线收敛」无从谈起。
	if headAfter := capHeadOf(t, dir); headAfter == headBefore {
		t.Fatal("本次 new 没有产生 commit：写后同步反证会退化成空跑")
	}
	rep := applyReport(t, env)
	if rep.TxnID == "" {
		t.Fatalf("有权威写入必须分配 txn_id：%+v", rep)
	}

	// ① 顺序：commit marker → Git → 索引同步 → 释放锁。S8 夹在 S7 与 S9 之间 ⇒ 仍持同一把锁。
	assertStepOrder(t, rec, TxnStepCommitted, TxnStepGit, TxnStepIndexSync, TxnStepReleasing)
	// ② index_sync 记的就是本次事务号（不得另开事务）。
	if got := rec.idAt(TxnStepIndexSync); got != rep.TxnID {
		t.Fatalf("index_sync 节点带的 txn_id = %q，期望本次事务 %q", got, rep.TxnID)
	}
	// ③ releasing 恰一次：显式 release 之后的 defer 必须幂等，不许多打一个 S9。
	if n := countTxnStep(rec, TxnStepReleasing); n != 1 {
		t.Fatalf("releasing 必须恰一次（显式 release + defer 幂等），实得 %d 次：%v", n, rec.names)
	}
	// ④ 索引仍健康、水位线已收敛到新 HEAD，且没有 W22 / W24。
	capAssertIndexConverged(t, dir, "proposal new 之后")
	capAssertNoIndexTrouble(t, env)
	// ⑤ 索引恒不进 Git：写后同步不得弄脏工作区。
	if n := rcPorcelainCount(t, dir); n != 0 {
		t.Fatalf("写后工作区脏了 %d 行：.index/ 必须被整目录忽略", n)
	}
}

// TestProposalRejectSyncsIndexInsideLock：先真实 new 得 pending 提案、再 build 索引、再采基线；
// 拒绝的 commit 把 HEAD 推进后，S8 同样在同一把锁内、S7 之后、Release 之前跑，水位线随之收敛。
func TestProposalRejectSyncsIndexInsideLock(t *testing.T) {
	dir := proposalVault(t)

	// 先建一份 pending 提案作为被拒目标。
	codeNew, envNew, newOut := runProposalCLI(t, dir,
		"new", "--type", string(proposal.TypeLogicalDelete), "--target", applyCardID,
		"--reason", "该卡已过时")
	if codeNew != ExitOK {
		t.Fatalf("前置 proposal new 退出码 = %d，期望 0：%s", codeNew, newOut)
	}
	repNew := applyReport(t, envNew)
	if len(repNew.Proposals) != 1 {
		t.Fatalf("前置 new 的 report.proposals[] 必须恰 1 条，实得 %v", repNew.Proposals)
	}
	pid := repNew.Proposals[0].ID

	// 在 new 落定之后（最后一次基线 commit 之后）再构建索引并确认收敛。
	if code, _, errOut := runIndexCLI(t, dir, "build"); code != ExitOK {
		t.Fatalf("前置 index build 退出码 = %d：%s", code, errOut)
	}
	capAssertIndexConverged(t, dir, "proposal reject 前置")
	headBefore := capHeadOf(t, dir)

	rec := watchTxn(t, nil)
	code, env, errOut := runProposalCLI(t, dir, "reject", pid, "--reason", "复核后决定放弃")
	if code != ExitOK {
		t.Fatalf("proposal reject 退出码 = %d，期望 0：%s", code, errOut)
	}

	if headAfter := capHeadOf(t, dir); headAfter == headBefore {
		t.Fatal("本次 reject 没有产生 commit：写后同步反证会退化成空跑")
	}
	rep := applyReport(t, env)
	if rep.TxnID == "" {
		t.Fatalf("有权威写入必须分配 txn_id：%+v", rep)
	}

	// ① 顺序：commit marker → Git → 索引同步 → 释放锁，S8 仍持同一把锁。
	assertStepOrder(t, rec, TxnStepCommitted, TxnStepGit, TxnStepIndexSync, TxnStepReleasing)
	// ② index_sync 记的就是本次事务号。
	if got := rec.idAt(TxnStepIndexSync); got != rep.TxnID {
		t.Fatalf("index_sync 节点带的 txn_id = %q，期望本次事务 %q", got, rep.TxnID)
	}
	// ③ releasing 恰一次。
	if n := countTxnStep(rec, TxnStepReleasing); n != 1 {
		t.Fatalf("releasing 必须恰一次（显式 release + defer 幂等），实得 %d 次：%v", n, rec.names)
	}
	// ④ 索引仍健康、水位线已收敛到新 HEAD，且没有 W22 / W24。
	capAssertIndexConverged(t, dir, "proposal reject 之后")
	capAssertNoIndexTrouble(t, env)
	// ⑤ 索引恒不进 Git。
	if n := rcPorcelainCount(t, dir); n != 0 {
		t.Fatalf("写后工作区脏了 %d 行：.index/ 必须被整目录忽略", n)
	}
}

// TestProposalApproveSyncsIndexInsideLock：先真实 new 得 pending 提案、再 build 索引、再采基线；
// 批准的 commit 把 HEAD 推进后，S8 同样在同一把锁内、S7 之后、Release 之前跑，水位线随之收敛。
func TestProposalApproveSyncsIndexInsideLock(t *testing.T) {
	dir := proposalVault(t)

	// 先建一份 pending 提案作为被批准目标（影响面未变 ⇒ 走 T1 批准分支）。
	codeNew, envNew, newOut := runProposalCLI(t, dir,
		"new", "--type", string(proposal.TypeLogicalDelete), "--target", applyCardID,
		"--reason", "该卡已过时")
	if codeNew != ExitOK {
		t.Fatalf("前置 proposal new 退出码 = %d，期望 0：%s", codeNew, newOut)
	}
	repNew := applyReport(t, envNew)
	if len(repNew.Proposals) != 1 {
		t.Fatalf("前置 new 的 report.proposals[] 必须恰 1 条，实得 %v", repNew.Proposals)
	}
	pid := repNew.Proposals[0].ID

	// 在 new 落定之后（最后一次基线 commit 之后）再构建索引并确认收敛。
	if code, _, errOut := runIndexCLI(t, dir, "build"); code != ExitOK {
		t.Fatalf("前置 index build 退出码 = %d：%s", code, errOut)
	}
	capAssertIndexConverged(t, dir, "proposal approve 前置")
	headBefore := capHeadOf(t, dir)

	rec := watchTxn(t, nil)
	code, env, errOut := runProposalCLI(t, dir, "approve", pid, "--confirm", "--user-request")
	if code != ExitOK {
		t.Fatalf("proposal approve 退出码 = %d，期望 0：%s", code, errOut)
	}

	if headAfter := capHeadOf(t, dir); headAfter == headBefore {
		t.Fatal("本次 approve 没有产生 commit：写后同步反证会退化成空跑")
	}
	rep := applyReport(t, env)
	if rep.TxnID == "" {
		t.Fatalf("有权威写入必须分配 txn_id：%+v", rep)
	}

	// ① 顺序：commit marker → Git → 索引同步 → 释放锁，S8 仍持同一把锁。
	assertStepOrder(t, rec, TxnStepCommitted, TxnStepGit, TxnStepIndexSync, TxnStepReleasing)
	// ② index_sync 记的就是本次事务号。
	if got := rec.idAt(TxnStepIndexSync); got != rep.TxnID {
		t.Fatalf("index_sync 节点带的 txn_id = %q，期望本次事务 %q", got, rep.TxnID)
	}
	// ③ releasing 恰一次。
	if n := countTxnStep(rec, TxnStepReleasing); n != 1 {
		t.Fatalf("releasing 必须恰一次（显式 release + defer 幂等），实得 %d 次：%v", n, rec.names)
	}
	// ④ 索引仍健康、水位线已收敛到新 HEAD，且没有 W22 / W24。
	capAssertIndexConverged(t, dir, "proposal approve 之后")
	capAssertNoIndexTrouble(t, env)
	// ⑤ 索引恒不进 Git。
	if n := rcPorcelainCount(t, dir); n != 0 {
		t.Fatalf("写后工作区脏了 %d 行：.index/ 必须被整目录忽略", n)
	}
}
