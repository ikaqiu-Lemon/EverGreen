package cli

// proposal_approve_transaction_test.go —— M6 · T-…-072 批次 C4b：`eg proposal approve` 的
// **事务正证**（原子性合同 §16.1、A-59）。
//
// 与 proposal_approve_test.go 分工：那边看命令侧门禁语义（U-12、退出码 6、确认点恰 1 处、
// approved 真实落盘）；这边只看事务本体 —— 一次批准是不是**一笔**事务、原子域是不是恰那一份
// 被批准的提案、commit marker 在不在盘、号码有没有如实交付、S8 写后索引同步是否仍夹在
// 同一把 run.lock 内（committed < git < index_sync < releasing，且 releasing 恰一次）。

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

// TestProposalApproveTxnCoversOnlyTarget 钉死批准成功路径：先用真实 proposal new 建一份
// pending 提案，再运行真实 approve --confirm --user-request（影响面未变 ⇒ 走 T1 批准分支），
// 逐条验证：
//
//	① 退 0；
//	② report.txn_id 非空且对应相对基线**唯一**新增的事务目录（A-59）；
//	③ intent.files[] 恰 1 条，且路径等于原提案 rel（原子域恰那一份提案）；
//	④ commit 标记在盘、abort 不在盘；
//	⑤ 最终 status = decision.result = approved，且 execution 仍 not_started（批准 ≠ 已执行）；
//	⑥ Git commit 恰 +1、工作区干净；
//	⑦ 步骤顺序 committed < git < index_sync < releasing，且 releasing 恰一次
//	   （S8 仍在锁内、早于 release；显式 release 之后的 defer 幂等，不多打一个 S9）。
func TestProposalApproveTxnCoversOnlyTarget(t *testing.T) {
	dir := proposalVault(t)

	// 先建一份 pending 提案作为被批准目标（真实 proposal new）。
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

	// 基线在 new 之后、approve 之前采集：事务增量与 Git 增量都相对这一刻算。
	base := txnIDsOn(t, dir)
	logBefore := gitLogCount(t, dir)

	rec := watchTxn(t, nil)
	code, env, errOut := runProposalCLI(t, dir, "approve", pid, "--confirm", "--user-request")

	// ① 退 0。
	if code != ExitOK {
		t.Fatalf("eg proposal approve 退出码 = %d，期望 0：%s", code, errOut)
	}
	rep := applyReport(t, env)

	// ② report.txn_id 非空且对应唯一新增事务目录。
	if rep.TxnID == "" {
		t.Fatalf("report.txn_id 必须非空（A-59：成功分配的事务号一律交付）：%+v", rep)
	}
	id := onlyNewTxn(t, dir, base, "一次成功的 proposal approve")
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
		t.Fatal("提案已批准，commit 标记必须在盘")
	}
	if markerExists(dir, id, txn.AbortMarker) {
		t.Fatal("已提交的事务不该有 abort 标记")
	}

	// ⑤ 被批准提案落到 approved（status 与 decision.result 两处），execution 仍 not_started。
	st := store.New(dir)
	after, lerr := proposal.LoadRel(st, prel)
	if lerr != nil {
		t.Fatalf("回读被批准提案失败：%v", lerr)
	}
	p := after.Proposal()
	if string(p.Status) != string(proposal.StatusApproved) {
		t.Fatalf("提案最终 status = %q，期望 %q", p.Status, proposal.StatusApproved)
	}
	if string(p.Decision.Result) != string(proposal.StatusApproved) {
		t.Fatalf("提案最终 decision.result = %q，期望 %q", p.Decision.Result, proposal.StatusApproved)
	}
	if string(p.Execution.Status) != string(proposal.ExecNotStarted) {
		t.Fatalf("提案最终 execution.status = %q，期望 %q（批准 ≠ 已执行）",
			p.Execution.Status, proposal.ExecNotStarted)
	}

	// ⑥ Git 恰 +1、工作区干净。
	if n := gitLogCount(t, dir); n != logBefore+1 {
		t.Fatalf("commit 数 %d → %d，期望恰 +1", logBefore, n)
	}
	if got := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); got != "" {
		t.Fatalf("批准成功后工作区应干净，实得 %q", got)
	}

	// ⑦ 步骤顺序：commit marker → Git → 索引同步 → 释放锁（S8 仍在锁内、早于 release）。
	assertStepOrder(t, rec, TxnStepCommitted, TxnStepGit, TxnStepIndexSync, TxnStepReleasing)
	if n := countTxnStep(rec, TxnStepReleasing); n != 1 {
		t.Fatalf("releasing 必须恰一次（显式 release + defer 幂等），实得 %d 次：%v", n, rec.names)
	}
}

// TestProposalApproveSupersedeTxnCoversBothProposals 钉死批准的**改判分支**事务本体：
// 批准前重算发现前提已变（目标卡被写上 deleted_at ⇒ exits_default_view 不再含它，impact 不一致），
// 于是走触发②：原提案改判 superseded + 生成新 pending 提案，且**不执行**删除。
//
// 造前提照 TestApprove_ImpactChangedSupersedesWithoutExecuting：直接给目标卡写 deleted_at，
// 且**先把这处夹具变化单独 commit**，好让 approve 之后「Git 恰 +1、工作区干净」这两针只反映
// 本次改判自己的一次 commit。逐条验证：
//
//	① 退 0；
//	② report.txn_id 非空且对应相对基线**唯一**新增事务目录（A-59）；
//	③ intent.files[] 恰 2 条 = 原提案 + 新提案，且**不含**知识目标卡（改判只动控制面）；
//	④ 原提案落 superseded 且 decision.superseded_by 指向那份新生成的 pending 提案（提案链不断裂）；
//	⑤ commit 标记在盘、abort 不在盘；
//	⑥ Git commit 恰 +1、工作区干净；
//	⑦ 步骤顺序 committed < git < index_sync < releasing（S8 仍在锁内、早于 release）。
func TestProposalApproveSupersedeTxnCoversBothProposals(t *testing.T) {
	dir := proposalVault(t)

	// 先建一份 pending 提案（真实 proposal new）。
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

	// 造「前提已变」：给目标卡写上 deleted_at，重算出的影响面与提案记录的 impact 不再一致。
	cardRel := store.CardRel("ai-infra", applyCardID)
	cardAbs := filepath.Join(dir, filepath.FromSlash(cardRel))
	raw := string(mustRead(t, cardAbs))
	const statusLine = "status: 'active'"
	changed := strings.Replace(raw, statusLine,
		statusLine+"\ndeleted_at: '2026-09-02T10:00:00+08:00'\ndeleted_reason: '语料已过时'", 1)
	if changed == raw {
		t.Fatalf("夹具前提不成立：卡片里没有 %s 可供改写", statusLine)
	}
	if err := os.WriteFile(cardAbs, []byte(changed), 0o644); err != nil {
		t.Fatalf("改写夹具：%v", err)
	}
	// **先把夹具变化单独提交**：这样 approve 后的「Git +1 / 工作区干净」只反映本次改判自己。
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-m", "test: 目标卡改判夹具（写入 deleted_at）")

	// 基线在夹具 commit 之后、approve 之前采集。
	base := txnIDsOn(t, dir)
	logBefore := gitLogCount(t, dir)

	rec := watchTxn(t, nil)
	code, env, errOut := runProposalCLI(t, dir, "approve", pid, "--confirm", "--user-request")

	// ① 退 0。
	if code != ExitOK {
		t.Fatalf("eg proposal approve（改判分支）退出码 = %d，期望 0：%s", code, errOut)
	}
	rep := applyReport(t, env)

	// ② report.txn_id 非空且对应唯一新增事务目录。
	if rep.TxnID == "" {
		t.Fatalf("report.txn_id 必须非空（A-59：成功分配的事务号一律交付）：%+v", rep)
	}
	id := onlyNewTxn(t, dir, base, "一次成功的 proposal approve 改判")
	if id != rep.TxnID {
		t.Fatalf("report.txn_id = %q，新增事务目录 = %q：两者必须恰好对上", rep.TxnID, id)
	}

	// 报告里必含新旧两条提案（数量守恒）；新提案 rel = 那条不等于原提案的。
	if len(rep.Proposals) != 2 {
		t.Fatalf("report.proposals[] 必须含新旧两条，实得 %v", rep.Proposals)
	}
	var newRel string
	for _, e := range rep.Proposals {
		if e.Path != prel {
			newRel = e.Path
		}
	}
	if newRel == "" {
		t.Fatalf("报告里找不到新提案条目（原提案 %q，proposals=%v）", prel, rep.Proposals)
	}

	// ③ intent.files[] 恰 2 条 = 原提案 + 新提案，且不含知识目标卡。
	paths := intentPathsOf(t, dir, id)
	got := map[string]bool{}
	for _, p := range paths {
		got[p] = true
	}
	if len(paths) != 2 || !got[prel] || !got[newRel] {
		t.Fatalf("intent.files[] = %v，期望恰含原提案 %q 与新提案 %q 两份", paths, prel, newRel)
	}
	if got[cardRel] {
		t.Fatalf("intent.files[] 不得含知识目标 %q：改判只动提案控制面，绝不碰知识数据", cardRel)
	}

	// ④ 原提案 superseded 且 decision.superseded_by 指向新生成的 pending 提案。
	st := store.New(dir)
	origAfter, lerr := proposal.LoadRel(st, prel)
	if lerr != nil {
		t.Fatalf("回读原提案失败：%v", lerr)
	}
	op := origAfter.Proposal()
	if string(op.Status) != string(proposal.StatusSuperseded) {
		t.Fatalf("原提案最终 status = %q，期望 %q", op.Status, proposal.StatusSuperseded)
	}
	if op.Decision.SupersededBy == "" {
		t.Fatal("decision.superseded_by 必须指向新提案（提案链不得断裂）")
	}
	newAfter, lerr := proposal.LoadRel(st, newRel)
	if lerr != nil {
		t.Fatalf("回读新提案失败：%v", lerr)
	}
	np := newAfter.Proposal()
	if string(np.Status) != string(proposal.StatusPending) {
		t.Fatalf("新提案 status = %q，期望 %q（改判生成的新提案待用户重新决定）",
			np.Status, proposal.StatusPending)
	}
	if string(op.Decision.SupersededBy) != string(newAfter.ID) {
		t.Fatalf("decision.superseded_by = %q，期望指向新 pending 提案 %q",
			op.Decision.SupersededBy, newAfter.ID)
	}

	// ⑤ commit 在盘、abort 不在盘。
	if !markerExists(dir, id, txn.CommitMarker) {
		t.Fatal("改判已提交，commit 标记必须在盘")
	}
	if markerExists(dir, id, txn.AbortMarker) {
		t.Fatal("已提交的事务不该有 abort 标记")
	}

	// ⑥ Git 恰 +1、工作区干净。
	if n := gitLogCount(t, dir); n != logBefore+1 {
		t.Fatalf("commit 数 %d → %d，期望恰 +1", logBefore, n)
	}
	if got := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); got != "" {
		t.Fatalf("改判成功后工作区应干净，实得 %q", got)
	}

	// ⑦ 步骤顺序：commit marker → Git → 索引同步 → 释放锁（S8 仍在锁内、早于 release）。
	assertStepOrder(t, rec, TxnStepCommitted, TxnStepGit, TxnStepIndexSync, TxnStepReleasing)
	if n := countTxnStep(rec, TxnStepReleasing); n != 1 {
		t.Fatalf("releasing 必须恰一次（显式 release + defer 幂等），实得 %d 次：%v", n, rec.names)
	}
}

// TestProposalApproveRecoverPrecedesProposalReread 钉死 S2（崩溃恢复屏障）**先于** S3（锁内
// 重读提案并判状态迁移）：让一笔未闭合的 approve 事务把目标提案的盘面污染成 approved 终态，
// 再运行真实 approve。若 S3 读早，盘上的 approved 会让 MarkApproved 的 CheckTransition 命中
// 「终态无出边」而失败（approved → approved 非法）；只有 S2 先把这笔未闭合写回滚回 pending，
// pending → approved 才是合法边、批准才能成功。
//
// 与 reject 版同套夹具与判据：借临时 atomic overlay 让 proposal.MarkApproved 产出合法的
// approved 目标态字节（避免用例自己拼 frontmatter），EndAtomic 后 overlay 整体丢弃、实盘仍是
// pending；随后发布一笔 ExpectCommit 的 update intent 并把 approved 写到盘上，但**不写**
// commit / abort —— 这正是 Scan 认定的 OpenTxn。钩子补两针现场取证（取锁瞬间盘上是 approved
// 崩溃态、恢复完成瞬间已回到 pending 前像），另有三条恢复屏障审计面：W26 一路留到最终 report、
// 被恢复的那笔写下 abort 无 commit、本次另起一个不复用的新号码。
func TestProposalApproveRecoverPrecedesProposalReread(t *testing.T) {
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

	// 借临时 Store 的 atomic overlay 生成「崩溃事务的 approved 目标态」字节；EndAtomic 后
	// overlay 整体丢弃，实盘仍是 pending。
	var approved []byte
	stTmp := store.New(dir)
	if err := stTmp.BeginAtomic(); err != nil {
		t.Fatalf("开临时预演失败：%v", err)
	}
	if _, err := proposal.MarkApproved(proposal.ApproveSpec{
		Store: stTmp, Original: proposal.ID(pid), OriginalRel: prel, Reason: "崩溃前的批准理由",
	}); err != nil {
		t.Fatalf("预演 MarkApproved 失败：%v", err)
	}
	for _, f := range stTmp.AtomicWriteSet() {
		if f.Path == prel {
			approved = append([]byte(nil), f.TargetBytes...)
		}
	}
	stTmp.EndAtomic()
	if len(approved) == 0 {
		t.Fatal("没能从预演取到 approved 目标态字节")
	}
	if bytes.Equal(approved, pending) {
		t.Fatal("approved 目标态与 pending 前像相同：夹具无法区分恢复前后")
	}

	// 造崩溃现场：分配号码 → 发布 update intent（ExpectCommit，PreBytes=pending、TargetBytes=approved）
	// → 把 approved 目标态写到盘上，之后**不写** commit / abort —— 这正是 Scan 认定的 OpenTxn。
	stale, err := txn.AllocateTxnID(dir)
	if err != nil {
		t.Fatalf("分配 txn_id 失败：%v", err)
	}
	now := func() time.Time { return captureAt(t) }
	if _, err := txn.WriteIntent(dir, stale, txn.IntentInput{
		Argv: []string{"eg", "proposal", "approve"}, ExpectCommit: true, Now: now,
		Files: []txn.FileSpec{{
			Path: prel, Create: false, PreBytes: pending, TargetBytes: approved, TargetOp: "update",
		}},
	}); err != nil {
		t.Fatalf("发布 update intent 失败：%v", err)
	}
	if err := os.WriteFile(abs, approved, 0o644); err != nil {
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

	code, env, errOut := runProposalCLI(t, dir, "approve", pid, "--confirm", "--user-request")

	// ① 顺序的直接证据：命令成功。若 S3 读早，盘上的 approved 会让 CheckTransition 命中终态
	//    出边（approved 无合法出边）而失败；只有恢复先把它回滚回 pending，pending → approved 才合法。
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0（S2 应先把未闭合 approve 回滚回 pending，S3 才去判状态迁移）：%s",
			code, errOut)
	}
	rep := applyReport(t, env)

	// ② 现场取证：取锁瞬间盘上是 approved 崩溃态，恢复完成瞬间已回到 pending 前像。
	if !bytes.Equal(atAcquire, approved) {
		t.Fatal("取锁瞬间盘上不是 approved 崩溃态：夹具没生效，本用例什么都没证明")
	}
	if !bytes.Equal(atRecovered, pending) {
		t.Fatal("恢复节点回调时目标仍未回到 pending 前像：S2 没有真正回滚这笔未闭合 approve")
	}

	// ③ 最终仍落到 approved（恢复没吃掉本次批准）。
	stAfter := store.New(dir)
	after, lerr := proposal.LoadRel(stAfter, prel)
	if lerr != nil {
		t.Fatalf("回读被批准提案失败：%v", lerr)
	}
	p := after.Proposal()
	if string(p.Status) != string(proposal.StatusApproved) ||
		string(p.Decision.Result) != string(proposal.StatusApproved) {
		t.Fatalf("提案最终 status=%q decision.result=%q，期望均为 %q",
			p.Status, p.Decision.Result, proposal.StatusApproved)
	}

	// ④ W26 必须保留在**最终 report** 里（真实回滚才有这条）。
	if !contains(undReportWarnCodes(t, env), txn.CodeTxnRecovered) {
		t.Fatalf("真实回滚必须在最终 report.warnings[] 留一条 %s，实得 %v",
			txn.CodeTxnRecovered, undReportWarnCodes(t, env))
	}

	// ⑤ 被恢复的那笔写下 abort、无 commit；本次另起一个新号码且不复用它，report 与之对上。
	if !markerExists(dir, stale, txn.AbortMarker) {
		t.Fatal("被回滚的未闭合事务必须写下 abort 标记")
	}
	if markerExists(dir, stale, txn.CommitMarker) {
		t.Fatal("被回滚的未闭合事务不该有 commit 标记")
	}
	id := onlyNewTxn(t, dir, base, "恢复之后的一次真实 proposal approve")
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

// TestProposalApproveLockBusyReturnsE16WithoutHanging：
// 另一个持有者正占着 `.index/run.lock`，本次 approve 必须在**有限时间**内带 E16 阻断，
// 而不是挂死，也不是绕开锁去批准提案。照 proposal new / reject 的 lock busy 用例同套夹具与判据。
//
// 「有限时间」这一针针对同一个坑：全仓 CLI 用例都把业务时钟 Root.Now 钉成常量；若 Acquire
// 的等待判据误用它，elapsed 恒为 0 ⇒ 退避重试无限循环。所以钉住：等待走真实墙钟（80ms 上限、
// 10s 内必须返回），且等待期 W28 与收尾 E16 都随错误一起交付。
//
// 另一半是「撞锁 = 什么都没做」：approve 的 S0 只解析参数形态与 U-12 授权守卫，任何库内事实的
// 读写都在锁后，因此目标提案字节逐字不变、全部权威快照不变、事务目录集合零新增、既有事务日志
// 一字节没动、Git 也不动（commit 数与 HEAD 双判据）。
func TestProposalApproveLockBusyReturnsE16WithoutHanging(t *testing.T) {
	dir := proposalVault(t)

	// 先建一份 pending 提案作为被批准目标，并存下它的字节基线。
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
		code, env, output := runProposalCLI(t, dir, "approve", pid, "--confirm", "--user-request")
		done <- runOut{code: code, env: env, out: output}
	}()

	var got runOut
	select {
	case got = <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("锁被占用时 proposal approve 在 10s 内没有返回：等待上限只有 %s=80ms，"+
			"说明 Acquire 拿到的是被钉死的业务时钟（elapsed 恒为 0 ⇒ 无限退避）",
			txn.LockTimeoutEnv)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("等待上限 80ms，实际耗时 %s：锁等待没有走真实墙钟", elapsed)
	}

	// ① M6/T-074 映射：锁不可用（E16）经 classifyExit5 归为 LockUnavailableError，
	//    在唯一翻译点 ExitCodeFor 落 ExitPrecheckOrLock(5)，绝不是「批准成功」，也不再兜底到 1。
	if got.code != ExitPrecheckOrLock {
		t.Fatalf("撞锁应按 M6 映射退 %d（锁不可用 E16 → 退出码 5），实得 %d：%s",
			ExitPrecheckOrLock, got.code, got.out)
	}
	// ② 诊断：最终的 E16（= errors.Is(err, txn.ErrLockUnavailable)）与等待期的 W28 都必须交付，
	//    且消息里带「锁不可用」这一稳定文案（不依赖「超时」字样）。
	diags := errorDiagsOf(t, got.env)
	if !diagsHaveCode(diags, txn.CodeLockTimeout) {
		t.Fatalf("锁等待超时必须携 %s（ErrLockUnavailable 的诊断映射），实得 %+v",
			txn.CodeLockTimeout, diags)
	}
	// I-…-016：W28 是 level=warning，取材面按合同 §3 搬到信封 warnings[]，另加分桶双侧封闭。
	assertLockWaitBuckets(t, got.env, "proposal approve 撞上锁")
	if !hasSubstring(diagMessages(diags), "锁不可用") {
		t.Fatalf("诊断消息必须交代锁不可用（含「锁不可用」），实得 %v", diagMessages(diags))
	}
	// ③ 目标提案字节逐字不变，全部权威快照也不变。
	if now := mustRead(t, absIn(dir, prel)); !bytes.Equal(now, proposalBytes) {
		t.Fatalf("没拿到锁不得批准提案 %s 的字节", prel)
	}
	assertAuthorityUnchanged(t, dir, before, "proposal approve 撞上锁")
	// ④ 事务侧：零新增事务目录，既有事务日志逐字节不变。
	assertNoNewTxn(t, dir, base, "proposal approve 撞上锁")
	assertTxnTreeUnchanged(t, dir, indexBefore, "proposal approve 撞上锁")
	// ⑤ Git 侧：commit 数与 HEAD 双判据都不动。
	if n := gitLogCount(t, dir); n != logBefore {
		t.Fatalf("没拿到锁不得产生 commit：%d → %d", logBefore, n)
	}
	if h := gitOut(t, dir, "rev-parse", "HEAD"); h != headBefore {
		t.Fatalf("没拿到锁不得移动 HEAD：%q → %q", headBefore, h)
	}
}

// TestProposalApproveSupersedeCommitRollbackRestoresBoth：
// 把 reject 的「S6 提交期 I/O 失败 ⇒ 整体回滚」反证搬到 approve 的**改判分支**上。改判分支的
// write-set 有两份（原提案改 superseded + 新生成一份 pending 提案），因此这是验证「多文件事务
// 提交失败必须**整体**回滚、不留半张脸」的关键一针。
//
// 前置照 TestProposalApproveSupersedeTxnCoversBothProposals：给目标卡写 deleted_at 造成 impact
// changed，并**先把夹具变化单独 commit**，再采完整权威 / txn / Git 基线。随后在 approve 的
// TxnStepIntent 节点把本笔事务目录里的 commit 标记名占成非空目录，S6 发布 commit marker 的原子
// rename 必然失败。
//
// 合同 §5.2：提交期失败属**主动放弃**（已 stage 的 overlay 整体回滚），退 3（ExitPartialWrite）。
// 逐条钉死：① 退 3；② 完整权威快照逐字不变 —— 原提案仍 pending、没有任何新提案残留；
// ③ 唯一新事务写下 abort、无 commit，report.txn_id 与之对上（A-59 号码照常交付）；
// ④ Git 与 S8 一步没跑、Git commit 数纹丝不动；⑤ 逐路径交代「目标未写入」，两份 write-set
// 各一条、恰 2 条。
func TestProposalApproveSupersedeCommitRollbackRestoresBoth(t *testing.T) {
	dir := proposalVault(t)

	// 先建一份 pending 提案（真实 proposal new）。
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

	// 造「前提已变」：给目标卡写上 deleted_at，重算出的影响面与提案记录的 impact 不再一致。
	cardRel := store.CardRel("ai-infra", applyCardID)
	cardAbs := filepath.Join(dir, filepath.FromSlash(cardRel))
	raw := string(mustRead(t, cardAbs))
	const statusLine = "status: 'active'"
	changed := strings.Replace(raw, statusLine,
		statusLine+"\ndeleted_at: '2026-09-02T10:00:00+08:00'\ndeleted_reason: '语料已过时'", 1)
	if changed == raw {
		t.Fatalf("夹具前提不成立：卡片里没有 %s 可供改写", statusLine)
	}
	if err := os.WriteFile(cardAbs, []byte(changed), 0o644); err != nil {
		t.Fatalf("改写夹具：%v", err)
	}
	// **先把夹具变化单独提交**：这样后面的「Git 不变」只反映本次改判自己没落任何 commit。
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-m", "test: 目标卡改判夹具（写入 deleted_at）")

	// 采完整权威 / txn / Git 基线（都在夹具 commit 之后、approve 之前）。
	pending := mustRead(t, absIn(dir, prel)) // 原提案仍是 pending 前像
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

	code, env, output := runProposalCLI(t, dir, "approve", pid, "--confirm", "--user-request")
	if code != ExitPartialWrite {
		t.Fatalf("退出码 = %d，期望 3（改判提交失败已整体回滚，属主动放弃而非基础设施崩坏）：%s",
			code, output)
	}
	if blocked == "" {
		t.Fatal("没有触达 intent 节点：本用例什么都没证明")
	}
	_ = os.RemoveAll(blocked)

	// ① 完整权威快照逐字不变 —— 原提案仍 pending、没有任何新提案残留。
	assertAuthorityUnchanged(t, dir, before, "proposal approve 改判提交失败并回滚后")
	if now := mustRead(t, absIn(dir, prel)); !bytes.Equal(now, pending) {
		t.Fatalf("提交回滚后原提案 %s 必须回到 pending 前像（superseded 字节从未落盘）", prel)
	}

	// ② 事务侧：唯一新事务写下 abort、无 commit。
	id := onlyNewTxn(t, dir, base, "改判提交失败并回滚")
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

	// ④ Git 与 S8 一步没跑（Markdown 根本没生效，谈不上「写后」）；Git commit 数纹丝不动。
	if rec.at(TxnStepGit) >= 0 || rec.at(TxnStepIndexSync) >= 0 {
		t.Fatalf("提交回滚后不得跑 Git / S8，实际序列 %v", rec.names)
	}
	if n := gitLogCount(t, dir); n != logBefore {
		t.Fatalf("提交失败不得产生 commit：%d → %d", logBefore, n)
	}

	// ⑤ 逐路径交代「目标未写入」：改判 write-set 恰两份（原提案 + 新提案），一条都不能少、也不该多。
	var unwritten int
	for _, w := range undReportWarnMessages(t, env) {
		if strings.Contains(w, "目标未写入") {
			unwritten++
		}
	}
	if unwritten != 2 {
		t.Fatalf("改判 write-set 有两份，必须逐路径交代「目标未写入」（恰 2 条），实得 %d 条：%v",
			unwritten, undReportWarnMessages(t, env))
	}
}

// TestProposalApproveGitFailureKeepsTargetWithoutSecondWrite：
// 把 proposal new / delete / reject 的「S7 Git 失败」反证搬到 approve 上。注入 commit 必失败的
// 仓库，跑真实 approve：commit marker 一旦落盘，approved 目标态就已**原子生效**；此时 Git 失败
// 只是「这批改动没进版本历史」，绝不是 Markdown 事务失败。
//
// 逐条钉死：① 退 4（ExitCommitFailed，不是退 1）；② 在 TxnStepGit 瞬间取权威快照，命令返回后
// 与之**逐字相同**且提案已 approved —— Git 之后没有第二次权威写；③ 失败支同样走完 S8 且仍在
// 同一把锁内：committed < git < index_sync < releasing；④ 恰一个新事务、commit 在盘、无 abort、
// report.txn_id 对上；⑤ Git 侧零 commit、report.git.commit 为空，错误诊断如实交代 Git 失败。
func TestProposalApproveGitFailureKeepsTargetWithoutSecondWrite(t *testing.T) {
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
	code, out, errOut := runCLI(t, r, "proposal", "approve", pid,
		"--confirm", "--user-request", "--vault", dir, "--json")
	var env Envelope
	if strings.TrimSpace(out) != "" {
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("--json 输出不可解析：%v\n%s", err, out)
		}
	}

	// ① 退 4：approved 已原子生效，仅 Git 失败。
	if code != ExitCommitFailed {
		t.Fatalf("退出码 = %d，期望 4（approved 已原子生效、Git 失败）：%s", code, errOut)
	}
	if atGit == nil {
		t.Fatal("没有触达 Git 节点：TxnStepGit 在失败支没有触发，本用例什么都没证明")
	}

	// ② Git 之后零二次权威写：返回时的字节与 Git 节点当时逐字相同，且提案已 approved。
	assertAuthorityUnchanged(t, dir, atGit, "proposal approve 的 Git 失败后")
	st := store.New(dir)
	after, lerr := proposal.LoadRel(st, prel)
	if lerr != nil {
		t.Fatalf("回读被批准提案失败：%v", lerr)
	}
	if p := after.Proposal(); string(p.Status) != string(proposal.StatusApproved) ||
		string(p.Decision.Result) != string(proposal.StatusApproved) {
		t.Fatalf("Git 失败不得回滚 Markdown，提案应保持 approved，实得 status=%q result=%q",
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
