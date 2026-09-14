package cli

// index_after_write_mark_reviewed_test.go —— M6 · T-…-072 批次 C2b：`eg mark-reviewed`
// 成功写路径的事务对账与 **S8 写后索引同步**反证（合同 §16.1 / §16.3；M5 索引架构合同
// §5.3 / §6.1）。
//
// 为什么这一组不住在 mark_reviewed_transaction_test.go：
// 命令层的文件级位置锁（cmd/eg/arch_test.go 的 TestStage4IndexPackageBoundary ⑤）规定
// `internal/cli/` 下**只有 index 前缀的文件**可以 import 索引包 —— 这是「索引不作为任何命令
// 的前置」的机器形态。「索引真的收敛了」这条判据必须直接读索引（status / Digest），因此按
// 位置锁放这里；用例名仍以 TestMarkReviewed 开头，`-run TestMarkReviewed` 一并覆盖。
//
// mark-reviewed 尤其需要这一格：索引里并没有 reviewed_at 这一列，很容易被误认为「标记已过目
// 不影响索引，S8 可以省」。但文件指纹与 HEAD 都变了，水位线不跟上就会把陈旧窗口留到锁外，
// 后续 search / card 读路径的降级判断随之失真。

import (
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/txn"
)

// mrIndexedVault 造一个「目标卡从未过目 + 已构建 healthy 索引」的 vault。
//
// 索引在夹具的最后一次提交之后才 build：水位线一开始就与 HEAD 对齐，
// 「mark-reviewed 之后索引跟着 HEAD 一起前进」才是一条有判据的断言，而不是自说自话。
func mrIndexedVault(t *testing.T) (dir string, cardRel string) {
	t.Helper()
	dir, cardRel, _ = reviewedVault(t)
	if code, _, errOut := runIndexCLI(t, dir, "build"); code != ExitOK {
		t.Fatalf("前置 build 退出码 = %d：%s", code, errOut)
	}
	capAssertIndexConverged(t, dir, "前置：mark-reviewed 之前")
	return dir, cardRel
}

// TestMarkReviewedCommitsOneIntentAndSyncsIndexInsideLock：成功写路径的四件事一次钉死。
//
//	① 一次 mark-reviewed = 一个事务，intent.files[] **恰含目标一个文件**
//	   （只写 reviewed_at 一个键，绝不级联）；
//	② report.txn_id 与盘上那个事务目录逐字对上，且该目录真的有 commit 标记、没有 abort；
//	③ 节点顺序 committed → git → index_sync → releasing：S8 夹在 S7 与 S9 之间，
//	   意味着它**仍持着同一把 run.lock**（释放尚未开始）；
//	④ 索引仍 healthy 且水位线已收敛到新 HEAD —— 这正是「S8 被跳过」时会红的那一格。
func TestMarkReviewedCommitsOneIntentAndSyncsIndexInsideLock(t *testing.T) {
	const markAt = "2026-09-04T09:00:00+08:00"
	dir, cardRel := mrIndexedVault(t)
	headBefore := capHeadOf(t, dir)
	base := txnIDsOn(t, dir)

	rec := watchTxn(t, nil)
	code, env, output := runMarkReviewedWith(t, newTestRoot(t, dir), dir, markAt,
		"--target", applyCardID)
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0：%s", code, output)
	}

	// 前置有效性：本次确实产生了 commit，HEAD 真的动了 —— 否则「水位线收敛」无从谈起。
	if capHeadOf(t, dir) == headBefore {
		t.Fatal("本次 mark-reviewed 没有产生 commit：水位线反证会退化成空跑")
	}
	if got := mrReviewedLine(t, dir, cardRel); !strings.Contains(got, markAt) {
		t.Fatalf("mark-reviewed 退 0 却没写上本次时刻，%s 行 = %q", model.FMKeyReviewedAt, got)
	}

	// ① 一个事务、一份 intent、恰一个目标文件。
	id := onlyNewTxn(t, dir, base, "一次成功的 mark-reviewed")
	if got := intentPathsOf(t, dir, id); strings.Join(got, ",") != cardRel {
		t.Fatalf("intent.files[] = %v，期望恰含 %s（标记已过目只动目标一个文件）", got, cardRel)
	}
	// ② report.txn_id 与事务日志同真（A-59）。
	if got := undReportTxnID(t, env); got != id {
		t.Fatalf("report.txn_id = %q，新增事务目录 = %q：两者必须恰好对上", got, id)
	}
	if !markerExists(dir, id, txn.CommitMarker) {
		t.Fatal("mark-reviewed 成功，commit 标记必须在盘")
	}
	if markerExists(dir, id, txn.AbortMarker) {
		t.Fatal("已提交的事务不该有 abort 标记")
	}
	// ③ S8 在同一把锁内、S7 之后、Release 之前。
	assertStepOrder(t, rec, TxnStepCommitted, TxnStepGit, TxnStepIndexSync, TxnStepReleasing)
	if got := rec.idAt(TxnStepIndexSync); got != id {
		t.Fatalf("index_sync 节点带的 txn_id = %q，期望本次事务 %q（不得另开事务）", got, id)
	}
	// ④ 索引仍健康、水位线已收敛到新 HEAD，且没有 W22 / W24。
	capAssertIndexConverged(t, dir, "mark-reviewed 成功后")
	capAssertNoIndexTrouble(t, env)
	// ⑤ 索引恒不进 Git：写后同步不得弄脏工作区。
	if n := rcPorcelainCount(t, dir); n != 0 {
		t.Fatalf("写后工作区脏了 %d 行：.index/ 必须被整目录忽略", n)
	}
}
