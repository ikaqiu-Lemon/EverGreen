package cli

// delete_release_order_test.go —— M6 · T-…-072 批次 C3：`eg delete` 的 S8 → S9 顺序
// 与「锁只还一次」（原子性合同 §16.1 / §16.3）。
//
// 这一格钉的是两件很容易被写坏、又不会被别的用例照出来的事：
//
//   - **S8 仍在锁内**：写后索引同步必须夹在 Git 与释放之间。若把它挪到释放之后，
//     索引的陈旧窗口就被留到了锁外，别的写者会在这段真空里看到「HEAD 已经动了、
//     索引还停在上一版」的库。
//   - **S9 恰一次**：C3 让两条 delete 分支在锁内取完报告所需的磁盘事实后**显式**还锁
//     （报告渲染因此回到锁外），而 deleteCritical 里那句 `defer sess.release()` 仍在。
//     没有 txnSession.release 的幂等，这两处就会各还一次：第二次 Release 必然失败并
//     多打一个 S9 节点、多产一条「释放 run.lock 失败」的诊断 —— 一条纯属自伤的假警报。

import (
	"strings"
	"testing"
)

// countStep 数某个节点被触发的次数（顺序断言用 assertStepOrder，这里只关心次数）。
func countStep(rec *txnSteps, name string) int {
	n := 0
	for _, s := range rec.names {
		if s == name {
			n++
		}
	}
	return n
}

// TestDeleteSyncsIndexBeforeSingleRelease：成功删除一次跑完，index_sync 早于 releasing，
// 且 releasing 恰触发一次（显式还锁 + defer 兜底 = 一次真正的 Release）。
func TestDeleteSyncsIndexBeforeSingleRelease(t *testing.T) {
	dir, _, _ := deleteVault(t)
	rec := approvedProposalFor(t, dir, []string{applyNoteID})

	steps := watchTxn(t, nil)
	code, _, output := runDeleteCLI(t, newTestRoot(t, dir), dir,
		"--target", applyNoteID, "--reason", "笔记已过时", "--proposal", string(rec.ID),
		"--confirm", "--user-request")
	if code != ExitOK {
		t.Fatalf("eg delete 退出码 = %d，期望 0：%s", code, output)
	}

	// S6 → S7 → S8 → S9：索引同步仍持着同一把 run.lock，释放尚未开始。
	assertStepOrder(t, steps, TxnStepCommitted, TxnStepGit, TxnStepIndexSync, TxnStepReleasing)

	// 锁只还一次：显式 release 之后，deleteCritical 的 defer 必须是个空动作。
	if n := countStep(steps, TxnStepReleasing); n != 1 {
		t.Fatalf("%s 触发 %d 次，期望恰 1 次（release 必须幂等）：实际序列 %v",
			TxnStepReleasing, n, steps.names)
	}
	// 幂等的直接后果：不得因为「第二次 Release 失败」而凭空多出一条诊断。
	if strings.Contains(output, "释放 run.lock 失败") {
		t.Fatalf("成功路径不该出现释放失败诊断（二次 Release 的痕迹）：\n%s", output)
	}
}
