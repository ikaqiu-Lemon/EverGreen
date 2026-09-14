package cli

// proposal_reject_transaction_test.go —— M6 · T-…-072 批次 C4a：`eg proposal reject` 的
// **事务正证**（原子性合同 §16.1、A-59）。
//
// 与 proposal_cmd_test.go 分工：那边看 T2 业务语义（E8/E9、rejected 终态），
// 这边只看事务本体 —— 一次拒绝是不是**一笔**事务、原子域是不是恰那一份被拒提案、
// commit marker 在不在盘、号码有没有如实交付、被拒提案是否落到 rejected 而别的权威文件不动。

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/proposal"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
	"github.com/ikaqiu-Lemon/EverGreen/internal/txn"
)

// TestProposalRejectTxnCoversOnlyTarget 钉死拒绝成功路径：先用真实 proposal new 建一份
// pending 提案作被拒目标，再运行真实 reject，逐条验证：
//
//	① 退 0；
//	② report.txn_id 非空且对应相对基线**唯一**新增的事务目录（A-59）；
//	③ intent.files[] 恰 1 条，且路径等于原提案 rel（原子域恰那一份提案）；
//	④ commit 标记在盘、abort 不在盘；
//	⑤ 被拒提案最终 status = decision.result = rejected，且**其他权威文件一个字节都不变**；
//	⑥ Git commit 恰 +1、工作区干净。
func TestProposalRejectTxnCoversOnlyTarget(t *testing.T) {
	dir := proposalVault(t)

	// 先建一份 pending 提案作为被拒目标（真实 proposal new）。
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
	prel := repNew.Proposals[0].Path

	// 基线在 new 之后、reject 之前采集：事务增量与 Git 增量都相对这一刻算。
	before := authoritySnapshot(t, dir)
	base := txnIDsOn(t, dir)
	logBefore := gitLogCount(t, dir)

	code, env, errOut := runProposalCLI(t, dir, "reject", pid, "--reason", "复核后决定放弃")

	// ① 退 0。
	if code != ExitOK {
		t.Fatalf("eg proposal reject 退出码 = %d，期望 0：%s", code, errOut)
	}
	rep := applyReport(t, env)

	// ② report.txn_id 非空且对应唯一新增事务目录。
	if rep.TxnID == "" {
		t.Fatalf("report.txn_id 必须非空（A-59：成功分配的事务号一律交付）：%+v", rep)
	}
	id := onlyNewTxn(t, dir, base, "一次成功的 proposal reject")
	if id != rep.TxnID {
		t.Fatalf("report.txn_id = %q，新增事务目录 = %q：两者必须恰好对上", rep.TxnID, id)
	}

	// ③ intent.files[] 恰 1 条，且等于原提案 rel。
	paths := intentPathsOf(t, dir, id)
	if len(paths) != 1 || paths[0] != prel {
		t.Fatalf("intent.files[] = %v，期望恰含原提案 %q 一份", paths, prel)
	}

	// ④ commit 在盘、abort 不在盘。
	if !markerExists(dir, id, txn.CommitMarker) {
		t.Fatal("提案已拒绝，commit 标记必须在盘")
	}
	if markerExists(dir, id, txn.AbortMarker) {
		t.Fatal("已提交的事务不该有 abort 标记")
	}

	// ⑤ 被拒提案落到 rejected（status 与 decision.result 两处），其他权威文件逐字节不变。
	st := store.New(dir)
	after, lerr := proposal.LoadRel(st, prel)
	if lerr != nil {
		t.Fatalf("回读被拒提案失败：%v", lerr)
	}
	p := after.Proposal()
	if string(p.Status) != string(proposal.StatusRejected) {
		t.Fatalf("提案最终 status = %q，期望 %q", p.Status, proposal.StatusRejected)
	}
	if string(p.Decision.Result) != string(proposal.StatusRejected) {
		t.Fatalf("提案最终 decision.result = %q，期望 %q", p.Decision.Result, proposal.StatusRejected)
	}
	afterSnap := authoritySnapshot(t, dir)
	if afterSnap[prel] == before[prel] {
		t.Fatalf("被拒提案 %s 的字节必须发生变化（pending → rejected）", prel)
	}
	for rel, h := range afterSnap {
		if rel == prel {
			continue
		}
		old, ok := before[rel]
		if !ok {
			t.Fatalf("拒绝只该改写目标提案，却新增了 %s", rel)
		}
		if old != h {
			t.Fatalf("拒绝只该改写目标提案，却改写了 %s", rel)
		}
	}
	for rel := range before {
		if _, ok := afterSnap[rel]; !ok {
			t.Fatalf("拒绝只该改写目标提案，却删除了 %s", rel)
		}
	}

	// ⑥ Git 恰 +1、工作区干净。
	if n := gitLogCount(t, dir); n != logBefore+1 {
		t.Fatalf("commit 数 %d → %d，期望恰 +1", logBefore, n)
	}
	if got := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); got != "" {
		t.Fatalf("拒绝成功后工作区应干净，实得 %q", got)
	}
}

// TestProposalRejectRecoverPrecedesProposalReread：
// 用一笔**真实的未闭合 reject 事务**（合同 §6 的 P4 崩溃点：intent 已发布、目标提案已按
// rejected 目标态 rename 到盘上，commit / abort 皆缺席）占住一份本该 pending 的提案。
//
// 这个现场直接决定 reject 能不能成功：MarkRejected 会先 CheckTransition(status → rejected)，
// 而 rejected → rejected 是 §2.4 的终态自环非法边（E9）。因此：
//
//   - 若 S3 的加载 / 状态判定发生在 S2 恢复之前（或复用了锁外快照），读到的就是那份未闭合
//     事务留下的 rejected 字节 ⇒ CheckTransition 命中终态出边 ⇒ 整条命令因 E9 退 2；
//   - 只有「先恢复、后锁内重读」，恢复才会把那份 update 回滚回 pending 前像，重读看到 pending
//     ⇒ pending → rejected 是合法边 ⇒ 照常拒绝成功。
//
// 因此本用例让结果本身作证：命令成功、提案最终仍落到 rejected。钩子补两针现场取证
// （取锁瞬间盘上是 rejected 崩溃态、恢复完成瞬间已回到 pending 前像）。另有三条恢复屏障
// 审计面：W26 一路留到最终 report、被恢复的那笔写下 abort、本次另起一个不复用的新号码。
func TestProposalRejectRecoverPrecedesProposalReread(t *testing.T) {
	dir := proposalVault(t)

	// 先建一份 pending 提案，并存下它的 pending 前像字节。
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
	prel := repNew.Proposals[0].Path
	abs := absIn(dir, prel)
	pending := mustRead(t, abs) // 前像：pending，decision.result 为空

	// 借临时 Store 的 atomic overlay 生成「崩溃事务的 rejected 目标态」字节，避免用例自己拼
	// frontmatter；EndAtomic 后 overlay 整体丢弃，实盘仍是 pending。
	var rejected []byte
	stTmp := store.New(dir)
	if err := stTmp.BeginAtomic(); err != nil {
		t.Fatalf("开临时预演失败：%v", err)
	}
	if _, err := proposal.MarkRejected(proposal.RejectSpec{
		Store: stTmp, Original: proposal.ID(pid), OriginalRel: prel, Reason: "崩溃前的拒绝理由",
	}); err != nil {
		t.Fatalf("预演 MarkRejected 失败：%v", err)
	}
	for _, f := range stTmp.AtomicWriteSet() {
		if f.Path == prel {
			rejected = append([]byte(nil), f.TargetBytes...)
		}
	}
	stTmp.EndAtomic()
	if len(rejected) == 0 {
		t.Fatal("没能从预演取到 rejected 目标态字节")
	}
	if bytes.Equal(rejected, pending) {
		t.Fatal("rejected 目标态与 pending 前像相同：夹具无法区分恢复前后")
	}

	// 造崩溃现场：分配号码 → 发布 update intent（ExpectCommit，PreBytes=pending、TargetBytes=rejected）
	// → 把 rejected 目标态写到盘上，之后**不写** commit / abort —— 这正是 Scan 认定的 OpenTxn（P4）。
	stale, err := txn.AllocateTxnID(dir)
	if err != nil {
		t.Fatalf("分配 txn_id 失败：%v", err)
	}
	now := func() time.Time { return captureAt(t) }
	if _, err := txn.WriteIntent(dir, stale, txn.IntentInput{
		Argv: []string{"eg", "proposal", "reject"}, ExpectCommit: true, Now: now,
		Files: []txn.FileSpec{{
			Path: prel, Create: false, PreBytes: pending, TargetBytes: rejected, TargetOp: "update",
		}},
	}); err != nil {
		t.Fatalf("发布 update intent 失败：%v", err)
	}
	if err := os.WriteFile(abs, rejected, 0o644); err != nil {
		t.Fatalf("写崩溃态失败：%v", err)
	}

	base := txnIDsOn(t, dir)
	logBefore := gitLogCount(t, dir)

	var atAcquire, atRecovered []byte
	watchTxn(t, func(step, _ string) {
		switch step {
		case TxnStepAcquired:
			atAcquire = mustRead(t, abs)
		case TxnStepRecovered:
			atRecovered = mustRead(t, abs)
		}
	})

	code, env, errOut := runProposalCLI(t, dir, "reject", pid, "--reason", "复核后决定放弃")

	// ① 顺序的直接证据：命令成功。若 S3 读早，盘上的 rejected 会让 CheckTransition 命中终态
	//    出边（E9）而退 2；只有恢复先把它回滚回 pending，pending → rejected 才是合法边。
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0（S2 应先把未闭合 reject 回滚回 pending，S3 才去判状态迁移）：%s",
			code, errOut)
	}
	rep := applyReport(t, env)

	// ② 现场取证：取锁瞬间盘上是 rejected 崩溃态，恢复完成瞬间已回到 pending 前像。
	if !bytes.Equal(atAcquire, rejected) {
		t.Fatal("取锁瞬间盘上不是 rejected 崩溃态：夹具没生效，本用例什么都没证明")
	}
	if !bytes.Equal(atRecovered, pending) {
		t.Fatal("恢复节点回调时目标仍未回到 pending 前像：S2 没有真正回滚这笔未闭合 reject")
	}

	// ③ 最终仍落到 rejected（恢复没吃掉本次拒绝）。
	stAfter := store.New(dir)
	after, lerr := proposal.LoadRel(stAfter, prel)
	if lerr != nil {
		t.Fatalf("回读被拒提案失败：%v", lerr)
	}
	p := after.Proposal()
	if string(p.Status) != string(proposal.StatusRejected) ||
		string(p.Decision.Result) != string(proposal.StatusRejected) {
		t.Fatalf("提案最终 status=%q decision.result=%q，期望均为 %q",
			p.Status, p.Decision.Result, proposal.StatusRejected)
	}

	// ④ W26 必须保留在**最终 report** 里（真实回滚才有这条）。
	if !contains(undReportWarnCodes(t, env), txn.CodeTxnRecovered) {
		t.Fatalf("真实回滚必须在最终 report.warnings[] 留一条 %s，实得 %v",
			txn.CodeTxnRecovered, undReportWarnCodes(t, env))
	}

	// ⑤ 被恢复的那笔写下 abort、无 commit；本次另起一个新号码且不复用它。
	if !markerExists(dir, stale, txn.AbortMarker) {
		t.Fatal("被回滚的未闭合事务必须写下 abort 标记")
	}
	if markerExists(dir, stale, txn.CommitMarker) {
		t.Fatal("被回滚的未闭合事务不该有 commit 标记")
	}
	id := onlyNewTxn(t, dir, base, "恢复之后的一次真实 proposal reject")
	if id == stale {
		t.Fatalf("本次不得复用被回滚的事务号 %q", stale)
	}
	if got := rep.TxnID; got != id {
		t.Fatalf("report.txn_id = %q，新增事务目录 = %q：两者必须恰好对上", got, id)
	}

	// ⑥ 恢复后照常走完 S7：Git 恰 +1。
	if n := gitLogCount(t, dir); n != logBefore+1 {
		t.Fatalf("commit 数 %d → %d，期望恰 +1", logBefore, n)
	}
}

// TestProposalRejectLockBusyReturnsE16WithoutHanging：
// 另一个持有者正占着 `.index/run.lock`，本次 reject 必须在**有限时间**内带 E16 阻断，
// 而不是挂死，也不是绕开锁去改判提案。照 proposal new 的 lock busy 用例同套夹具与判据。
//
// 「有限时间」这一针针对同一个坑：全仓 CLI 用例都把业务时钟 Root.Now 钉成常量；若 Acquire
// 的等待判据误用它，elapsed 恒为 0 ⇒ 退避重试无限循环。所以钉住：等待走真实墙钟（80ms 上限、
// 10s 内必须返回），且等待期 W28 与收尾 E16 都随错误一起交付。
//
// 另一半是「撞锁 = 什么都没做」：reject 的 S0 只解析参数形态，任何库内事实的读写都在锁后，
// 因此目标提案字节逐字不变、全部权威快照不变、事务目录集合零新增、既有事务日志一字节没动、
// Git 也不动（commit 数与 HEAD 双判据）。
func TestProposalRejectLockBusyReturnsE16WithoutHanging(t *testing.T) {
	dir := proposalVault(t)

	// 先建一份 pending 提案作为被拒目标，并存下它的字节基线。
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
	prel := repNew.Proposals[0].Path
	proposalBytes := mustRead(t, absIn(dir, prel))

	before := authoritySnapshot(t, dir)
	indexBefore := indexTreeSnapshot(t, dir)
	base := txnIDsOn(t, dir)
	logBefore := gitLogCount(t, dir)
	headBefore := gitOut(t, dir, "rev-parse", "HEAD")

	// 等待上限压到 80ms：真实墙钟下这条命令必须很快带 E16 回来。
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
		code, env, output := runProposalCLI(t, dir, "reject", pid, "--reason", "锁被占住")
		done <- runOut{code: code, env: env, out: output}
	}()

	var got runOut
	select {
	case got = <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("锁被占用时 proposal reject 在 10s 内没有返回：等待上限只有 %s=80ms，"+
			"说明 Acquire 拿到的是被钉死的业务时钟（elapsed 恒为 0 ⇒ 无限退避）",
			txn.LockTimeoutEnv)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("等待上限 80ms，实际耗时 %s：锁等待没有走真实墙钟", elapsed)
	}

	// ① M6/T-074 映射：锁不可用（E16）经 classifyExit5 归为 LockUnavailableError，
	//    在唯一翻译点 ExitCodeFor 落 ExitPrecheckOrLock(5)，绝不是「改判成功」，也不再兜底到 1。
	if got.code != ExitPrecheckOrLock {
		t.Fatalf("撞锁应按 M6 映射退 %d（锁不可用 E16 → 退出码 5），实得 %d：%s",
			ExitPrecheckOrLock, got.code, got.out)
	}
	// ② 诊断：最终的 E16（= errors.Is(err, txn.ErrLockUnavailable)）与等待期的 W28 都必须交付，
	//    且消息里带 lock timeout 证据（「等待超时」）。
	diags := errorDiagsOf(t, got.env)
	if !diagsHaveCode(diags, txn.CodeLockTimeout) {
		t.Fatalf("锁等待超时必须携 %s（ErrLockUnavailable 的诊断映射），实得 %+v",
			txn.CodeLockTimeout, diags)
	}
	// I-…-016：W28 是 level=warning，取材面按合同 §3 搬到信封 warnings[]，另加分桶双侧封闭。
	assertLockWaitBuckets(t, got.env, "proposal reject 撞上锁")
	if !hasSubstring(diagMessages(diags), "锁不可用") {
		t.Fatalf("诊断消息必须交代 lock timeout（含「锁不可用」），实得 %v", diagMessages(diags))
	}
	// ③ 目标提案字节逐字不变，全部权威快照也不变。
	if now := mustRead(t, absIn(dir, prel)); !bytes.Equal(now, proposalBytes) {
		t.Fatalf("没拿到锁不得改判提案 %s 的字节", prel)
	}
	assertAuthorityUnchanged(t, dir, before, "proposal reject 撞上锁")
	// ④ 事务侧：零新增事务目录，既有事务日志逐字节不变。
	assertNoNewTxn(t, dir, base, "proposal reject 撞上锁")
	assertTxnTreeUnchanged(t, dir, indexBefore, "proposal reject 撞上锁")
	// ⑤ Git 侧：commit 数与 HEAD 双判据都不动。
	if n := gitLogCount(t, dir); n != logBefore {
		t.Fatalf("没拿到锁不得产生 commit：%d → %d", logBefore, n)
	}
	if h := gitOut(t, dir, "rev-parse", "HEAD"); h != headBefore {
		t.Fatalf("没拿到锁不得移动 HEAD：%q → %q", headBefore, h)
	}
}

// TestProposalRejectCommitRollbackIsPartialWriteNotBlocked：
// 复刻 mark-reviewed / proposal new 的「S6 提交期 I/O 失败」反证到 reject 上。在 S5 发布
// intent 的节点，把本笔事务目录里的 commit 标记名占成一个**非空目录**（commit.tmp/occupied），
// 于是 S6 发布 commit marker 的原子 rename 必然失败。
//
// 合同 §5.2：提交期失败属**主动放弃**（已 stage 的 overlay 整体回滚），退 3（ExitPartialWrite），
// 而不是当成基础设施崩坏退 1。对 reject 而言「回滚」意味着那份提案**回到 pending 前像**——
// overlay 里 stage 的 rejected 字节从未落到权威盘上。因此既查完整权威快照逐字不变，
// 又单独钉一针「提案字节 == pending 前像」，把「改判被整体撤销」说死。
//
// 事务侧：这一笔写下 abort、无 commit，report.txn_id 恰等于该 abort 目录名。Markdown 从未
// 生效 ⇒ S7/S8 一步不许跑、Git commit 数纹丝不动。最后逐路径交代：本命令恰一个目标，
// 「目标未写入」必须恰一条。
func TestProposalRejectCommitRollbackIsPartialWriteNotBlocked(t *testing.T) {
	dir := proposalVault(t)

	// 先建一份 pending 提案作为被拒目标，存下 pending 前像与基线。
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
	prel := repNew.Proposals[0].Path
	pending := mustRead(t, absIn(dir, prel))

	before := authoritySnapshot(t, dir)
	base := txnIDsOn(t, dir)
	logBefore := gitLogCount(t, dir)

	// 在 intent 节点把 commit 标记名占成非空目录：S6 发布 commit marker 的 rename 必失败。
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

	code, env, output := runProposalCLI(t, dir, "reject", pid, "--reason", "提交期失败整体回滚")
	if code != ExitPartialWrite {
		t.Fatalf("退出码 = %d，期望 3（提交失败已整体回滚，属主动放弃而非基础设施崩坏）：%s",
			code, output)
	}
	if blocked == "" {
		t.Fatal("没有触达 intent 节点：本用例什么都没证明")
	}
	_ = os.RemoveAll(blocked)

	// ① 权威内容回到事务开始前，且提案字节逐字等于 pending 前像（改判被整体撤销）。
	assertAuthorityUnchanged(t, dir, before, "proposal reject 提交失败并回滚后")
	if now := mustRead(t, absIn(dir, prel)); !bytes.Equal(now, pending) {
		t.Fatalf("提交回滚后提案 %s 必须回到 pending 前像（rejected 字节从未落盘）", prel)
	}

	// ② 事务侧：abort 在盘、commit 不在盘。
	id := onlyNewTxn(t, dir, base, "提交失败并回滚")
	if !markerExists(dir, id, txn.AbortMarker) {
		t.Fatal("回滚完成后必须写下 abort 标记")
	}
	if markerExists(dir, id, txn.CommitMarker) {
		t.Fatal("没提交成功却有 commit 标记")
	}
	// ③ 号码保留（A-59），且恰是 abort 目录名。
	if got := undReportTxnID(t, env); got != id {
		t.Fatalf("report.txn_id = %q，abort 日志目录 = %q：主动回滚的事务号必须交付且两者相等", got, id)
	}
	// ④ Git 与 S8 一步没跑（Markdown 根本没生效，谈不上「写后」）。
	if rec.at(TxnStepGit) >= 0 || rec.at(TxnStepIndexSync) >= 0 {
		t.Fatalf("提交回滚后不得跑 Git / S8，实际序列 %v", rec.names)
	}
	if n := gitLogCount(t, dir); n != logBefore {
		t.Fatalf("提交失败不得产生 commit：%d → %d", logBefore, n)
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

// TestProposalRejectGitFailureKeepsTargetWithoutSecondWrite：
// 复刻 proposal new / delete 的「S7 Git 失败」反证到 reject 上。注入 commit 必失败的仓库，
// 跑真实 reject：commit marker 一旦落盘，rejected 目标态就已**原子生效**；此时 Git 失败只是
// 「这批改动没进版本历史」，绝不是 Markdown 事务失败。
//
// 逐条钉死：① 退 4（ExitCommitFailed，不是退 1）；② 在 TxnStepGit 瞬间取权威快照，命令返回后
// 与之**逐字相同**且提案仍是 rejected —— Git 之后没有第二次权威写；③ 失败支同样走完 S8 且仍在
// 同一把锁内：committed < git < index_sync < releasing；④ 恰一个新事务、commit 在盘、无 abort、
// report.txn_id 对上；⑤ Git 侧零 commit、report.git.commit 为空，错误诊断如实交代 Git 失败。
func TestProposalRejectGitFailureKeepsTargetWithoutSecondWrite(t *testing.T) {
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
	prel := repNew.Proposals[0].Path

	base := txnIDsOn(t, dir)
	logBefore := gitLogCount(t, dir)

	var atGit map[string]string
	rec := watchTxn(t, func(step, _ string) {
		if step == TxnStepGit {
			// Git 刚尝试完（失败）的瞬间取证 —— 这一针要求 TxnStepGit 在**失败支**也触发。
			atGit = authoritySnapshot(t, dir)
		}
	})

	// runProposalCLI 自建 root，无法注入失败仓库；这里照它的装配手工搭一遍，只把 NewRepo 换成必失败版。
	setGitIdentity(t)
	r := newTestRoot(t, dir)
	r.Now = func() time.Time { return captureAt(t) }
	r.NewRepo = failingCommitRepo()
	code, out, errOut := runCLI(t, r, "proposal", "reject", pid,
		"--reason", "Git 注入失败", "--vault", dir, "--json")
	var env Envelope
	if strings.TrimSpace(out) != "" {
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("--json 输出不可解析：%v\n%s", err, out)
		}
	}

	// ① 退 4：rejected 已原子生效，仅 Git 失败。
	if code != ExitCommitFailed {
		t.Fatalf("退出码 = %d，期望 4（rejected 已原子生效、Git 失败）：%s", code, errOut)
	}
	if atGit == nil {
		t.Fatal("没有触达 Git 节点：TxnStepGit 在失败支没有触发，本用例什么都没证明")
	}

	// ② Git 之后零二次权威写：返回时的字节与 Git 节点当时逐字相同，且提案仍是 rejected。
	assertAuthorityUnchanged(t, dir, atGit, "proposal reject 的 Git 失败后")
	st := store.New(dir)
	after, lerr := proposal.LoadRel(st, prel)
	if lerr != nil {
		t.Fatalf("回读被拒提案失败：%v", lerr)
	}
	if p := after.Proposal(); string(p.Status) != string(proposal.StatusRejected) ||
		string(p.Decision.Result) != string(proposal.StatusRejected) {
		t.Fatalf("Git 失败不得回滚 Markdown，提案应保持 rejected，实得 status=%q result=%q",
			p.Status, p.Decision.Result)
	}
	// ③ 失败支同样走完 S8，且必须仍在同一把锁内：committed < git < index_sync < releasing。
	assertStepOrder(t, rec, TxnStepCommitted, TxnStepGit, TxnStepIndexSync, TxnStepReleasing)

	// ④ 事务侧：恰一个事务、commit 标记在盘、无 abort、号码与报告对上。
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
	// ⑤ Git 侧：没有新 commit，报告的 git.commit 为空，错误诊断含 Git。
	if n := gitLogCount(t, dir); n != logBefore {
		t.Fatalf("Git 失败不得产生 commit：%d → %d", logBefore, n)
	}
	if g, _ := undReportOf(t, env)["git"].(map[string]interface{}); g == nil || g["commit"] != nil {
		t.Fatalf("Git 失败时 report.git.commit 必须为空：%v", undReportOf(t, env)["git"])
	}
	if !hasSubstring(diagMessages(errorDiagsOf(t, env)), "Git") {
		t.Fatalf("Git 失败明细必须交付：%+v", errorDiagsOf(t, env))
	}
}
