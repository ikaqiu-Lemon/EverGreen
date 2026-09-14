package cli

// index_after_write_delete_test.go —— M6 · T-…-072 批次 C3：`eg delete` **多目标**成功写路径的
// **S8 写后索引同步**反证（合同 §16.1 / §16.3；M5 索引架构合同 §5.3 / §6.1）。
//
// 为什么这一支住在 index* 文件里，而不是 delete_transaction_test.go：
// 命令层的文件级位置锁（cmd/eg/arch_test.go 的 TestStage4IndexPackageBoundary ⑤）规定
// `internal/cli/` 下**只有 index 前缀的文件**可以碰索引包 —— 这是「索引不作为任何命令的
// 前置」的机器形态。本用例的判据取自 `eg index status`（health / freshness / head），
// 沿用 capture / undelete 那一组既有辅助，不另造第二套口径；用例名仍以 TestDelete 开头，
// `-run TestDelete` 一并覆盖。
//
// delete 在这一组里的特殊性只有一条、但很要命：它是唯一一条**一次改动多个知识产物**的
// 写命令。S8 若被跳过（或被挪到锁外），delete 的 Git commit 会把 HEAD 往前推、索引水位线
// 却停在旧值 —— 命令一返回，索引立刻陈旧，而且这个陈旧窗口产生在锁外，任何进程都能读到
// 一个自称健康、实际落后的库。

import (
	"testing"
)

// TestDeleteSyncsIndexInsideLock：多目标删除成功后的三件事一次钉死。
//
//	① 节点顺序 committed → git → index_sync → releasing：S8 夹在 S7 与 S9 之间，
//	   意味着它**仍持着同一把 run.lock**（释放尚未开始），且带的是本次那个 txn_id；
//	② 索引仍 healthy、freshness=fresh，水位线 head 逐字等于当前 Git HEAD
//	   —— 这正是「S8 被跳过」时会红的那一格；
//	③ 没有 W22 / W24，且 `.index/` 不弄脏工作区（索引恒不进 Git）。
func TestDeleteSyncsIndexInsideLock(t *testing.T) {
	dir, _, _, rec := deleteTwoTargetVault(t)

	// 索引在全部前置写入**之后**才 build：水位线一开始就与 HEAD 对齐，
	// 「delete 之后索引跟着 HEAD 一起前进」才是一条有判据的断言，而不是自说自话。
	if code, _, errOut := runIndexCLI(t, dir, "build"); code != ExitOK {
		t.Fatalf("前置 build 退出码 = %d：%s", code, errOut)
	}
	capAssertIndexConverged(t, dir, "前置：delete 之前")
	headBefore := capHeadOf(t, dir)

	steps := watchTxn(t, nil)
	code, env, output := runDeleteCLI(t, newTestRoot(t, dir), dir,
		"--target", applyNoteID, "--reason", "批量清理", "--proposal", string(rec.ID),
		"--confirm", "--user-request")
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0：%s", code, output)
	}
	// 前置有效性：本次确实产生了 commit，HEAD 真的动了 —— 否则「水位线收敛」无从谈起。
	if capHeadOf(t, dir) == headBefore {
		t.Fatal("本次 delete 没有产生 commit：水位线反证会退化成空跑")
	}

	// ① S8 在同一把锁内、S7 之后、Release 之前，且挂在本次事务上。
	assertStepOrder(t, steps, TxnStepCommitted, TxnStepGit, TxnStepIndexSync, TxnStepReleasing)
	id := undReportTxnID(t, env)
	if id == "" {
		t.Fatal("成功写入的报告必须带 txn_id")
	}
	if got := steps.idAt(TxnStepIndexSync); got != id {
		t.Fatalf("index_sync 节点带的 txn_id = %q，期望本次事务 %q（S8 不得另开事务）", got, id)
	}

	// ② 索引仍健康、freshness=fresh、水位线已收敛到新 HEAD。
	capAssertIndexConverged(t, dir, "多目标 delete 成功后")

	// ③ 没有索引侧的 W22 / W24，且写后同步不弄脏工作区。
	capAssertNoIndexTrouble(t, env)
	if n := rcPorcelainCount(t, dir); n != 0 {
		t.Fatalf("写后工作区脏了 %d 行：.index/ 必须被整目录忽略", n)
	}
}
