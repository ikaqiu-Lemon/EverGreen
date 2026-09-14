package cli

// undelete_transaction_test.go —— M6 · T-…-072 批次 C2a：`eg undelete` 事务化的聚焦反证。
//
// 被测对象是 undelete.go 里的临界区 undeleteCritical。它把「锁外扫库判 W11 + 直落实盘 +
// 事后 Git」的旧写口，改成与 plan 写链 / capture **同一把锁、同一套顺序**的 A 类事务：
//
//	S0 锁外解析 → S1 取锁 → S2 崩溃恢复 → S3 锁内 ScanIDs/Resolve/Read/frontmatter/W11 →
//	S4 原子预演 → S5 intent 屏障 → S6 原子提交 → S7 Git → S8 写后索引同步 → S9 释放锁
//
// 本文件钉这条时序**在 undelete 上**的可观察后果：恢复屏障必须真的改变「这张卡到底删没删」
// 这个事实并被锁内重读吃到（W26 一路保留到最终 report）、锁拿不到就有限阻断且零权威写、
// W11 幂等分支必须过锁与恢复但绝不开事务 / 不跑 Git / 不伪造 S8。
//
// 「一个 intent 只含目标文件 + txn_id 对账 + committed<git<index_sync<release + 索引收敛」
// 那一组判据要直接读索引库，按命令层的文件级位置锁（cmd/eg/arch_test.go 的
// TestStage4IndexPackageBoundary ⑤）住在 index_after_write_undelete_test.go。
//
// 观测手段沿用 recover_hook_test.go 的 txnOrderHook 与那一组磁盘取证辅助
// （authoritySnapshot / onlyNewTxn / intentPathsOf / markerExists…），不另造第二套口径。
// 本文件不测 internal/txn 自身的崩溃安全（那是 txn 包的用例）。

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/txn"
)

// —— 辅助 ——

// undReportOf 取信封里的 `data.report`（undelete 全程走 proposalResult，报告恒在）。
func undReportOf(t *testing.T, env Envelope) map[string]interface{} {
	t.Helper()
	rep, ok := env.Data["report"].(map[string]interface{})
	if !ok {
		t.Fatalf("data.report 缺席或形态不对：%v", env.Data)
	}
	return rep
}

// undReportTxnID 取 `data.report.txn_id`（未分配号码时该键整个省略 ⇒ 返回空串）。
func undReportTxnID(t *testing.T, env Envelope) string {
	t.Helper()
	id, _ := undReportOf(t, env)["txn_id"].(string)
	return id
}

// undReportWarnCodes 收集 `data.report.warnings[].code`（空码不计）。
//
// 刻意读**最终报告体**而不是信封的 warnings[]：合同要求 W26 这类「进临界区时库里发生过
// 什么」的事实随产物交付，而产物就是 report —— 只看 hook 名称或只看信封都证明不了这一点。
func undReportWarnCodes(t *testing.T, env Envelope) []string {
	t.Helper()
	raw, ok := undReportOf(t, env)["warnings"].([]interface{})
	if !ok {
		return nil
	}
	var out []string
	for _, one := range raw {
		w, _ := one.(map[string]interface{})
		if code, _ := w["code"].(string); code != "" {
			out = append(out, code)
		}
	}
	return out
}

// undDeletedKeys 判断一份 Markdown 里还有没有删除标记（两个键任一在即算「已删」）。
func undDeletedKeys(raw string) bool {
	return strings.Contains(raw, "deleted_at:") || strings.Contains(raw, "deleted_reason:")
}

// undStripDeleted 生成「删除标记已被清空」的目标字节 —— 即一次 undelete **本该**写出的结果。
//
// 用它当崩溃事务的 TargetBytes：这样崩溃现场看上去就像「上一次 undelete 写完权威文件、
// 还没来得及写 commit 标记就断电了」，恢复屏障必须把它整体回滚回「仍是已删除」的前像。
func undStripDeleted(t *testing.T, pre []byte) []byte {
	t.Helper()
	var kept [][]byte
	for _, line := range bytes.Split(pre, []byte("\n")) {
		s := string(line)
		if strings.HasPrefix(s, "deleted_at:") || strings.HasPrefix(s, "deleted_reason:") {
			continue
		}
		kept = append(kept, line)
	}
	out := bytes.Join(kept, []byte("\n"))
	if bytes.Equal(out, pre) {
		t.Fatal("夹具失效：目标卡上没有 deleted_at / deleted_reason 行，崩溃现场造不出来")
	}
	return out
}

// —— ① S2 崩溃恢复屏障必须早于 S3 的目标重读 ——

// TestUndeleteRecoverPrecedesTargetReread：
// 用一笔**真实的未闭合事务**造现场：intent 已发布、权威文件已被 rename 成「删除标记已清空」
// 的目标态、commit / abort 皆缺席（合同 §6 的 P4 崩溃点）。
//
// 这个现场把「这张卡到底删没删」这个**业务事实**整个翻了过来：
//   - 若本次 undelete 在恢复之前就读目标（旧实现在锁外读），看到的是那份未闭合事务的目标态
//     ——「没有 deleted_at」——于是判定 W11 幂等 no-op：退 0、零写入、零 commit，
//     用户明明该被恢复的卡被静悄悄地跳过了；
//   - 只有先恢复、后重读，才会看到回滚后的前像（仍带 deleted_at），从而真正执行一次 undelete。
//
// 因此本用例不看 hook 名称，而是让**业务结果本身**成为顺序的证据：真的写了、真的提交了。
// 钩子只用来补两针现场取证（取锁瞬间是崩溃态、恢复完成瞬间已回前像），而 W26 则必须
// 一路保留到最终 report。
func TestUndeleteRecoverPrecedesTargetReread(t *testing.T) {
	dir, cardRel, _ := undeletableVault(t)
	abs := absIn(dir, cardRel)

	pre := mustRead(t, abs) // 前像：仍带 deleted_at / deleted_reason
	if !undDeletedKeys(string(pre)) {
		t.Fatal("前置不成立：目标卡应处于已逻辑删除状态")
	}
	target := undStripDeleted(t, pre) // 崩溃事务的目标态：删除标记已清空

	stale, err := txn.AllocateTxnID(dir)
	if err != nil {
		t.Fatalf("分配 txn_id 失败：%v", err)
	}
	now := func() time.Time { return captureAt(t) }
	if _, err := txn.WriteIntent(dir, stale, txn.IntentInput{
		Argv: []string{"eg", "undelete"}, ExpectCommit: true, Now: now,
		Files: []txn.FileSpec{{
			Path: cardRel, PreBytes: pre, TargetBytes: target, TargetOp: "update",
		}},
	}); err != nil {
		t.Fatalf("发布 intent 失败：%v", err)
	}
	if err := os.WriteFile(abs, target, 0o644); err != nil {
		t.Fatalf("写崩溃态失败：%v", err)
	}

	base := txnIDsOn(t, dir)
	logBefore := gitLogCount(t, dir)

	var atAcquire, atRecovered string
	watchTxn(t, func(step, _ string) {
		switch step {
		case TxnStepAcquired:
			atAcquire = string(mustRead(t, abs))
		case TxnStepRecovered:
			atRecovered = string(mustRead(t, abs))
		}
	})

	code, env, output := runUndeleteCLI(t, newTestRoot(t, dir), dir,
		"--target", applyCardID, "--reason", "恢复先于重读")
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0（恢复成功后照常执行 undelete）：%s", code, output)
	}

	// ① 顺序的直接证据：本次是**真的写了**，而不是被 W11 静默跳过。
	if strings.Contains(output, UndeleteIdempotentMsg) {
		t.Fatalf("命中了 W11 幂等分支：说明重读发生在恢复之前，读到的是崩溃事务的目标态\n%s", output)
	}
	if got := gitLogCount(t, dir); got != logBefore+1 {
		t.Fatalf("commit 数 %d → %d，期望恰 +1（恢复后必须真的执行一次 undelete）", logBefore, got)
	}
	if after := string(mustRead(t, abs)); undDeletedKeys(after) {
		t.Fatalf("undelete 已执行，删除标记却还在：\n%s", after)
	}
	// ② 现场取证：取锁瞬间盘上还是崩溃态，恢复完成瞬间已回到前像。
	if undDeletedKeys(atAcquire) {
		t.Fatal("取锁瞬间盘上就已经是前像：夹具没生效，本用例什么都没证明")
	}
	if atRecovered != string(pre) {
		t.Fatal("恢复节点回调时目标仍未回到前像：S2 没有真正回滚")
	}
	// ③ W26 必须保留在**最终 report** 里（真实回滚才有这条）。
	if !contains(undReportWarnCodes(t, env), txn.CodeTxnRecovered) {
		t.Fatalf("真实回滚必须在最终 report.warnings[] 留一条 %s，实得 %v",
			txn.CodeTxnRecovered, undReportWarnCodes(t, env))
	}
	// ④ 被恢复的那笔已写下 abort，且本次不复用它的号码。
	if !markerExists(dir, stale, txn.AbortMarker) {
		t.Fatal("被回滚的未闭合事务必须写下 abort 标记")
	}
	id := onlyNewTxn(t, dir, base, "恢复之后的一次真实 undelete")
	if id == stale {
		t.Fatalf("本次不得复用被回滚的事务号 %q", stale)
	}
	if got := undReportTxnID(t, env); got != id {
		t.Fatalf("report.txn_id = %q，新增事务目录 = %q：两者必须恰好对上", got, id)
	}
}

// —— ② 锁被占住：有限时间内退 E16，权威零写 ——

// TestUndeleteLockBusyReturnsE16WithoutHanging：
// 业务时钟被钉成常量（全仓 CLI 用例的统一口径）+ 锁被另一个 fd 真实占住 ⇒
// 必须在有限时间内带 E16 返回，绝不挂死；并且零权威写、零事务、零 commit。
func TestUndeleteLockBusyReturnsE16WithoutHanging(t *testing.T) {
	dir, _, _ := undeletableVault(t)
	before := authoritySnapshot(t, dir)
	logBefore := gitLogCount(t, dir)
	base := txnIDsOn(t, dir)

	t.Setenv(txn.LockTimeoutEnv, "80")
	held, err := txn.Acquire(dir, txn.LockOptions{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("外部持锁失败，用例前提不成立：%v", err)
	}
	defer func() { _ = held.Release() }()

	type runOut struct {
		code int
		env  Envelope
		out  string
	}
	done := make(chan runOut, 1)
	started := time.Now()
	go func() {
		code, env, output := runUndeleteCLI(t, newTestRoot(t, dir), dir,
			"--target", applyCardID, "--reason", "锁被占住")
		done <- runOut{code: code, env: env, out: output}
	}()

	var got runOut
	select {
	case got = <-done:
	case <-time.After(20 * time.Second):
		t.Fatalf("锁被占用时 undelete 在 20s 内没有返回：等待上限只有 %s=80ms，"+
			"说明 Acquire 拿到的是被钉死的业务时钟（elapsed 恒为 0 ⇒ 无限退避）", txn.LockTimeoutEnv)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("等待上限 80ms，实际耗时 %s：锁等待没有走真实墙钟", elapsed)
	}
	if got.code == ExitOK {
		t.Fatalf("锁被占用必须阻断，实得退出码 0：%s", got.out)
	}
	diags := errorDiagsOf(t, got.env)
	if !diagsHaveCode(diags, txn.CodeLockTimeout) {
		t.Fatalf("锁等待超时必须携 %s，实得 %+v", txn.CodeLockTimeout, diags)
	}
	// I-…-016：W28 是 level=warning，取材面按合同 §3 搬到信封 warnings[]，另加分桶双侧封闭。
	assertLockWaitBuckets(t, got.env, "undelete 撞上锁")
	assertAuthorityUnchanged(t, dir, before, "undelete 撞上锁")
	assertNoNewTxn(t, dir, base, "undelete 撞上锁")
	if n := gitLogCount(t, dir); n != logBefore {
		t.Fatalf("没拿到锁不得产生 commit：%d → %d", logBefore, n)
	}
}

// —— ③ W11 幂等 no-op：过锁、过恢复屏障，但不开事务、不跑 Git、不伪造 S8 ——

// TestUndeleteNoOpPassesRecoveryButOpensNoTxn：
// 目标从未被逻辑删除 ⇒ W11 命中。这条分支的两面都必须钉住：
//   - **必须**先取锁、过恢复屏障（判据本身取自恢复之后的字节，否则就是拿脏事实做幂等判定）；
//   - **不得**分配 txn_id、不得建事务目录、不得跑 Git、不得触发 S8
//     （一个字节都没写，就没有「写后」可言；假装同步过会让节点序列说谎）。
func TestUndeleteNoOpPassesRecoveryButOpensNoTxn(t *testing.T) {
	dir, _, _ := deleteVault(t) // 目标卡从未被删除过 ⇒ W11 必然命中
	before := authoritySnapshot(t, dir)
	base := txnIDsOn(t, dir)
	logBefore := gitLogCount(t, dir)

	rec := watchTxn(t, nil)
	code, env, output := runUndeleteCLI(t, newTestRoot(t, dir), dir,
		"--target", applyCardID, "--reason", "重复恢复")
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0（幂等 no-op 不是失败）：%s", code, output)
	}
	if !strings.Contains(output, UndeleteIdempotentMsg) {
		t.Fatalf("报告缺 W11 幂等说明 %q：\n%s", UndeleteIdempotentMsg, output)
	}

	// ① 必须过锁与恢复屏障：W11 的判据来自恢复之后的 frontmatter。
	for _, step := range []string{TxnStepAcquired, TxnStepRecovered, TxnStepReread} {
		if rec.at(step) < 0 {
			t.Fatalf("W11 分支也必须走完 %q，实际序列 %v", step, rec.names)
		}
	}
	if rec.at(TxnStepReleasing) < 0 {
		t.Fatalf("W11 分支必须照常释放锁，实际序列 %v", rec.names)
	}
	if rec.at(TxnStepRecovered) >= rec.at(TxnStepReread) {
		t.Fatalf("恢复屏障必须早于目标重读，实际序列 %v", rec.names)
	}
	// ② 不开事务、不跑 Git、不伪造 S8。
	for _, step := range []string{TxnStepAllocated, TxnStepIntent, TxnStepCommitted,
		TxnStepGit, TxnStepIndexSync} {
		if rec.at(step) >= 0 {
			t.Fatalf("W11 零写入不得触发 %q，实际序列 %v", step, rec.names)
		}
	}
	assertNoNewTxn(t, dir, base, "W11 幂等 no-op")
	assertAuthorityUnchanged(t, dir, before, "W11 幂等 no-op")
	if n := gitLogCount(t, dir); n != logBefore {
		t.Fatalf("零写入不得制造空 commit：%d → %d", logBefore, n)
	}
	// ③ 没分配号码 ⇒ report 必须整个省略 txn_id（填了就是编造事实，A-59）。
	if _, ok := undReportOf(t, env)["txn_id"]; ok {
		t.Fatalf("未开事务的 report 不得出现 txn_id，实得 %v", undReportOf(t, env)["txn_id"])
	}
}

// —— ④ S7 Git 失败：退 4、不回滚、不做第二次权威写 ——

// TestUndeleteGitFailureKeepsTargetStateWithoutSecondWrite：
// commit marker 已经落盘 ⇒ 两个删除键的整行删除**已经原子生效**。此时 Git 失败只是
// 「这批改动没进版本历史」，不是 Markdown 事务失败：绝不许回滚、绝不许再写第二次权威。
func TestUndeleteGitFailureKeepsTargetStateWithoutSecondWrite(t *testing.T) {
	dir, cardRel, _ := undeletableVault(t)
	base := txnIDsOn(t, dir)
	logBefore := gitLogCount(t, dir)

	var atGit map[string]string
	rec := watchTxn(t, func(step, _ string) {
		if step == TxnStepGit {
			// Git 刚尝试完（失败）的瞬间取证 —— 这一针要求 TxnStepGit 在**成败两支**都触发。
			atGit = authoritySnapshot(t, dir)
		}
	})

	r := newTestRoot(t, dir)
	r.NewRepo = failingCommitRepo()
	code, env, output := runUndeleteCLI(t, r, dir,
		"--target", applyCardID, "--reason", "Git 失败")
	if code != ExitCommitFailed {
		t.Fatalf("退出码 = %d，期望 4：%s", code, output)
	}
	if atGit == nil {
		t.Fatal("没有触达 Git 节点：TxnStepGit 在失败支没有触发，本用例什么都没证明")
	}

	// ① Markdown 已生效并保持目标态：两个键仍是被清空的（不回滚）。
	if after := string(mustRead(t, absIn(dir, cardRel))); undDeletedKeys(after) {
		t.Fatalf("Git 失败不得回滚 Markdown，删除标记却回来了：\n%s", after)
	}
	// ② Git 之后没有第二次权威写。
	assertAuthorityUnchanged(t, dir, atGit, "undelete 的 Git 失败后")
	// ②' 失败支同样要走完 S8，且必须仍在**同一把锁内**：
	//     committed → git → index_sync → releasing 四节点全触发且严格递增，
	//     意味着写后索引同步夹在 S7 与 S9 之间（释放尚未开始）。
	//     从 Git 失败支提前 return 会让 index_sync 缺席 —— 那等于把「commit marker 已落盘、
	//     权威内容已经变了」这件事瞒着索引，还把陈旧窗口留到锁外。
	assertStepOrder(t, rec, TxnStepCommitted, TxnStepGit, TxnStepIndexSync, TxnStepReleasing)
	// ③ 事务侧：恰一个事务、commit 标记在盘、没有 abort、没有第二个事务。
	id := onlyNewTxn(t, dir, base, "Git 失败")
	if got := undReportTxnID(t, env); got != id {
		t.Fatalf("report.txn_id = %q 与新增事务目录 %q 对不上（Git 失败绝不开第二个事务）", got, id)
	}
	if !markerExists(dir, id, txn.CommitMarker) {
		t.Fatal("Markdown 事务已提交，commit 标记必须在盘")
	}
	if markerExists(dir, id, txn.AbortMarker) {
		t.Fatal("Git 失败不是 Markdown 事务失败，不该有 abort 标记")
	}
	// ④ Git 侧：没有新 commit，报告的 git.commit 为空。
	if n := gitLogCount(t, dir); n != logBefore {
		t.Fatalf("Git 失败不得产生 commit：%d → %d", logBefore, n)
	}
	if g, _ := undReportOf(t, env)["git"].(map[string]interface{}); g == nil || g["commit"] != nil {
		t.Fatalf("Git 失败时 report.git.commit 必须为空：%v", undReportOf(t, env)["git"])
	}
	// ⑤ 失败明细如实交付（退出码 4 的由来必须能被读出来）。
	if !hasSubstring(diagMessages(errorDiagsOf(t, env)), "Git") {
		t.Fatalf("Git 失败明细必须交付：%+v", errorDiagsOf(t, env))
	}
}

// —— ⑤ S6 提交期普通 I/O 失败 ⇒ 合同 §5.2 主动放弃（退 3，不是退 1）——

// TestUndeleteCommitRollbackIsPartialWriteNotBlocked：
// 在 intent 发布之后、commit 标记发布之前把标记的临时名占死 —— 备料与权威 rename 都照常
// 成功、磁盘一度真的是目标态，只有最后一格失败。txn 层据此把目标还原成前像并写 abort。
// CLI 必须把它翻译成「一条都没写成」的退 3（与 runPlan / capture 同一裁决），而不是基础
// 设施崩坏的退 1；txn_id 必须保留、逐路径交代未写、Git 一步不许跑。
func TestUndeleteCommitRollbackIsPartialWriteNotBlocked(t *testing.T) {
	dir, cardRel, _ := undeletableVault(t)
	before := authoritySnapshot(t, dir)
	base := txnIDsOn(t, dir)
	logBefore := gitLogCount(t, dir)

	var blocked string
	rec := watchTxn(t, func(step, txnID string) {
		if step != TxnStepIntent {
			return
		}
		blocked = filepath.Join(txn.TxnDirPath(dir, txnID), txn.CommitMarker+".tmp")
		if err := os.MkdirAll(filepath.Join(blocked, "occupied"), 0o755); err != nil {
			t.Fatalf("制造标记发布失败失败：%v", err)
		}
	})

	code, env, output := runUndeleteCLI(t, newTestRoot(t, dir), dir,
		"--target", applyCardID, "--reason", "提交回滚")
	if code != ExitPartialWrite {
		t.Fatalf("退出码 = %d，期望 3（提交失败已整体回滚，属主动放弃而非基础设施崩坏）：%s",
			code, output)
	}
	if blocked == "" {
		t.Fatal("没有触达 intent 节点：本用例什么都没证明")
	}
	_ = os.RemoveAll(blocked)

	// ① 权威内容回到事务开始前：删除标记原封不动地还在。
	assertAuthorityUnchanged(t, dir, before, "undelete 提交失败并回滚后")
	if after := string(mustRead(t, absIn(dir, cardRel))); !undDeletedKeys(after) {
		t.Fatalf("回滚之后删除标记必须仍在（前像）：\n%s", after)
	}
	// ② 事务侧：abort 在盘、commit 不在盘。
	id := onlyNewTxn(t, dir, base, "提交失败并回滚")
	if !markerExists(dir, id, txn.AbortMarker) {
		t.Fatal("回滚完成后必须写下 abort 标记")
	}
	if markerExists(dir, id, txn.CommitMarker) {
		t.Fatal("没提交成功却有 commit 标记")
	}
	// ③ Git 与 S8 一步没跑（Markdown 根本没生效，谈不上「写后」）。
	if rec.at(TxnStepGit) >= 0 || rec.at(TxnStepIndexSync) >= 0 {
		t.Fatalf("提交回滚后不得跑 Git / S8，实际序列 %v", rec.names)
	}
	if n := gitLogCount(t, dir); n != logBefore {
		t.Fatalf("提交失败不得产生 commit：%d → %d", logBefore, n)
	}
	// ④ 号码保留（A-59：审计边界是「已分配」），且恰是 abort 目录名。
	if got := undReportTxnID(t, env); got != id {
		t.Fatalf("report.txn_id = %q，abort 日志目录 = %q：主动回滚的事务号必须交付且两者相等", got, id)
	}
	// ⑤ 逐路径交代「目标未写入」（本命令恰一个目标，一条都不能少、也不该多）。
	var unwritten int
	for _, w := range undReportWarnMessages(t, env) {
		if strings.Contains(w, "目标未写入") {
			unwritten++
		}
	}
	if unwritten != 1 {
		t.Fatalf("必须逐路径交代「目标未写入」（恰 1 条），实得 %d 条：%v",
			unwritten, undReportWarnMessages(t, env))
	}
}

// undReportWarnMessages 收集 `data.report.warnings[].message`。
func undReportWarnMessages(t *testing.T, env Envelope) []string {
	t.Helper()
	raw, ok := undReportOf(t, env)["warnings"].([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, one := range raw {
		w, _ := one.(map[string]interface{})
		msg, _ := w["message"].(string)
		out = append(out, msg)
	}
	return out
}
