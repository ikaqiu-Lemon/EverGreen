package cli

// delete_transaction_test.go —— M6 · T-…-072 批次 C3：`eg delete` 真实删除分支的
// **事务反证**（原子性合同 §5.2 / §16.1、A-59）。
//
// 这一组与 delete_test.go 分工明确：那边看的是业务语义（status 不变、support_check、
// 退出码 6 等），这边只看事务本体 —— 一次删除是不是**一笔**事务、原子域里到底有哪些文件、
// commit marker 在不在盘、失败时权威内容有没有回到前像。
//
// 为什么删除格外需要这两针：它是本仓唯一一条「**多个**知识产物 + 一份提案 execution
// 必须同生共死」的写命令。原子域少一个文件，库里就会留下「卡删了、提案还说没执行」
// 或者「删了一半」的状态；而 M3 的旧形态恰恰允许后者，因此这条边界必须钉在机器里。

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/proposal"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
	"github.com/ikaqiu-Lemon/EverGreen/internal/txn"
)

// deleteTwoTargetVault 造「笔记 + 两张卡」的库，并返回一份 targets 恰含
// {笔记, 第二张卡} 的 **approved** 提案。
//
// 提案在第二张卡落盘**之后**才建：影响面因此与执行前重算逐项一致（不触发 9a 改判），
// 走的就是真实删除分支。两个目标是这一组的前提 —— 原子域只有跨越多个文件时，
// 「全有或全无」才有可能被证否。
func deleteTwoTargetVault(t *testing.T) (dir, noteRel, card2Rel string, rec proposal.Record) {
	t.Helper()
	dir, noteRel, _ = deleteVault(t)
	code, _, errOut := runApplyPlan(t, dir, cardPlan(applyCard2ID, ""))
	if code != ExitOK {
		t.Fatalf("前置：落第二张卡退出码 = %d：%s", code, errOut)
	}
	card2Rel = store.CardRel("ai-infra", applyCard2ID)
	rec = approvedProposalFor(t, dir, []string{applyNoteID, applyCard2ID})
	return dir, noteRel, card2Rel, rec
}

// deleteExecStatus 读回提案落盘后的 execution.status。
func deleteExecStatus(t *testing.T, dir, rel string) proposal.ExecStatus {
	t.Helper()
	after, err := proposal.LoadRel(store.New(dir), rel)
	if err != nil {
		t.Fatalf("提案读回失败：%v", err)
	}
	return after.Proposal().Execution.Status
}

// —— A) 多 target 成功：一笔事务、原子域恰「全部 targets + 提案」——

// TestDeleteTxnCoversAllTargetsAndProposal 一次钉死成功路径的五件事：
//
//	① 一次 delete = **一个**事务（不得每个文件各开一笔）；
//	② intent.files[] 恰含全部 target 文件 + 那份提案文件 —— 一个不少（漏了就不在原子域里）、
//	   一个不多（多了说明顺手改了别的东西）；
//	③ report.txn_id 与盘上事务目录逐字对上（A-59：号码即审计凭据）；
//	④ commit 标记在盘、abort 不在盘；
//	⑤ 两个目标的 deleted 标记与提案 execution=succeeded **同时**生效 —— 它们同处一笔事务，
//	   不存在「卡删了、提案还说 not_started」这种中间态。
func TestDeleteTxnCoversAllTargetsAndProposal(t *testing.T) {
	dir, noteRel, card2Rel, rec := deleteTwoTargetVault(t)
	base := txnIDsOn(t, dir)
	logBefore := gitLogCount(t, dir)

	code, env, output := runDeleteCLI(t, newTestRoot(t, dir), dir,
		"--target", applyNoteID, "--reason", "批量清理", "--proposal", string(rec.ID),
		"--confirm", "--user-request")
	if code != ExitOK {
		t.Fatalf("eg delete 退出码 = %d，期望 0：%s", code, output)
	}

	// ① + ② 一个事务，原子域恰三份文件。
	id := onlyNewTxn(t, dir, base, "一次成功的多目标 delete")
	want := []string{card2Rel, noteRel, rec.Rel}
	sort.Strings(want)
	if got := intentPathsOf(t, dir, id); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("intent.files[] = %v，期望恰含全部 target 与提案 %v", got, want)
	}
	// ③ 号码与日志同真。
	if got := undReportTxnID(t, env); got != id {
		t.Fatalf("report.txn_id = %q，新增事务目录 = %q：两者必须恰好对上", got, id)
	}
	// ④ commit 在、abort 不在。
	if !markerExists(dir, id, txn.CommitMarker) {
		t.Fatal("删除已生效，commit 标记必须在盘")
	}
	if markerExists(dir, id, txn.AbortMarker) {
		t.Fatal("已提交的事务不该有 abort 标记")
	}
	// ⑤ 两个目标都删了，且提案同一笔事务里记成 succeeded。
	for _, rel := range []string{noteRel, card2Rel} {
		if got := string(mustRead(t, absIn(dir, rel))); !strings.Contains(got, "deleted_at:") {
			t.Fatalf("%s 必须已写 deleted_at（原子域内的目标一个都不能落下）：\n%s", rel, got)
		}
	}
	if got := deleteExecStatus(t, dir, rec.Rel); got != proposal.ExecSucceeded {
		t.Fatalf("execution.status = %q，期望 %q（与删除标记同处一笔事务）",
			got, proposal.ExecSucceeded)
	}
	// 附带：成功路径恰 +1 条 commit（三份文件同进一次历史）。
	if n := gitLogCount(t, dir); n != logBefore+1 {
		t.Fatalf("commit 数 %d → %d，期望恰 +1", logBefore, n)
	}
}

// —— B) S6 提交期失败 ⇒ 合同 §5.2 主动放弃：全部回前像，退 3 ——

// TestDeleteCommitRollbackRestoresAllTargets 用「把 commit 标记的 tmp 路径占成目录」
// 制造提交期的普通 I/O 失败，钉死 txn 层的主动放弃在**多目标**下同样彻底：
//
//	① 全部 target 与提案回到事务开始前的字节（一个 deleted_at 都不许留下，
//	   提案 execution 也不许被改判）；
//	② abort 标记在盘、commit 标记不在盘；
//	③ 退 3（主动放弃，不是基础设施崩坏的退 1）；
//	④ txn_id 仍进报告（A-59：审计边界是「已分配」，不是「已提交」）；
//	⑤ Git 与 S8 一步都没跑 —— Markdown 根本没生效，谈不上「写后」。
func TestDeleteCommitRollbackRestoresAllTargets(t *testing.T) {
	dir, noteRel, card2Rel, rec := deleteTwoTargetVault(t)
	before := authoritySnapshot(t, dir)
	base := txnIDsOn(t, dir)
	logBefore := gitLogCount(t, dir)

	var blocked string
	steps := watchTxn(t, func(step, txnID string) {
		if step != TxnStepIntent {
			return
		}
		// commit 标记要经 `<marker>.tmp` 落盘；把那个路径占成非空目录，rename 必然失败。
		blocked = filepath.Join(txn.TxnDirPath(dir, txnID), txn.CommitMarker+".tmp")
		if err := os.MkdirAll(filepath.Join(blocked, "occupied"), 0o755); err != nil {
			t.Fatalf("制造标记发布失败失败：%v", err)
		}
	})

	code, env, output := runDeleteCLI(t, newTestRoot(t, dir), dir,
		"--target", applyNoteID, "--reason", "批量清理", "--proposal", string(rec.ID),
		"--confirm", "--user-request")
	if code != ExitPartialWrite {
		t.Fatalf("退出码 = %d，期望 3（提交失败已整体回滚，属主动放弃）：%s", code, output)
	}
	if blocked == "" {
		t.Fatal("没有触达 intent 节点：本用例什么都没证明")
	}
	_ = os.RemoveAll(blocked)

	// ① 权威内容逐字回到前像：两个目标都没长出 deleted_at，提案 execution 未被改判。
	assertAuthorityUnchanged(t, dir, before, "delete 提交失败并回滚后")
	for _, rel := range []string{noteRel, card2Rel} {
		if got := string(mustRead(t, absIn(dir, rel))); strings.Contains(got, "deleted_at:") {
			t.Fatalf("回滚之后 %s 不得留下 deleted_at：\n%s", rel, got)
		}
	}
	if got := deleteExecStatus(t, dir, rec.Rel); got != rec.Proposal().Execution.Status {
		t.Fatalf("execution.status = %q，期望保持 %q（回滚后不得改判提案）",
			got, rec.Proposal().Execution.Status)
	}
	// ② abort 在盘、commit 不在盘。
	id := onlyNewTxn(t, dir, base, "提交失败并回滚")
	if !markerExists(dir, id, txn.AbortMarker) {
		t.Fatal("回滚完成后必须写下 abort 标记")
	}
	if markerExists(dir, id, txn.CommitMarker) {
		t.Fatal("没提交成功却有 commit 标记")
	}
	// ④ 号码保留且恰是那个 abort 目录。
	if got := undReportTxnID(t, env); got != id {
		t.Fatalf("report.txn_id = %q，abort 日志目录 = %q：主动回滚的事务号必须交付且相等", got, id)
	}
	// ⑤ Git 与 S8 均未触发，也没有新 commit。
	if steps.at(TxnStepGit) >= 0 || steps.at(TxnStepIndexSync) >= 0 {
		t.Fatalf("提交回滚后不得跑 Git / S8，实际序列 %v", steps.names)
	}
	if n := gitLogCount(t, dir); n != logBefore {
		t.Fatalf("提交失败不得产生 commit：%d → %d", logBefore, n)
	}
}

// —— C) S2 崩溃恢复屏障必须早于 S3 的**提案**重读 ——

// deleteRejectBytes 把一份 approved 提案的字节改写成 rejected 的**目标态**。
//
// 走纯字节替换而不是 proposal.MarkRejected：T2 只认 `pending → rejected`，
// approved → rejected 是 §2.4 的非法边，正规写口根本造不出这份字节 —— 而崩溃现场
// 要的恰恰是「一份还没提交、因此也从未被校验过的目标态」。两处 scalar 一起改
// （frontmatter 的 status 与 decision.result），是为了让这份字节在 status ⟷
// decision.result 一致性上也说得通：本用例要证否的是「读得早了」，不是「读到一份烂文件」。
func deleteRejectBytes(t *testing.T, approved []byte) []byte {
	t.Helper()
	quoted := func(key string, st proposal.Status) []byte {
		return []byte(key + ": '" + string(st) + "'")
	}
	out := bytes.ReplaceAll(approved,
		quoted(proposal.KeyStatus, proposal.StatusApproved),
		quoted(proposal.KeyStatus, proposal.StatusRejected))
	out = bytes.ReplaceAll(out,
		quoted(proposal.KeyResult, proposal.StatusApproved),
		quoted(proposal.KeyResult, proposal.StatusRejected))
	if bytes.Equal(out, approved) {
		t.Fatalf("夹具失效：提案里没有 status/result = %s 可改，崩溃现场造不出来\n%s",
			proposal.StatusApproved, approved)
	}
	if !bytes.Contains(out, quoted(proposal.KeyStatus, proposal.StatusRejected)) {
		t.Fatalf("夹具失效：目标态里没有 status: %s\n%s", proposal.StatusRejected, out)
	}
	return out
}

// TestDeleteRecoverPrecedesProposalReread：
// 用一笔**真实的未闭合事务**（合同 §6 的 P4 崩溃点：intent 已发布、权威文件已 rename 成
// 目标态、commit / abort 皆缺席）把**提案本身**翻过来 —— 盘上当下写着 `status: rejected`，
// 而 intent 里的前像仍是 `approved`。
//
// 这个现场直接决定 delete 的生死：`--proposal` 必须指向 status=approved 的提案
// （W7 后半 ≡ V10），否则退 2、零写入。于是顺序就有了**业务可观察**的证据：
//
//   - 若 S3 的提案 Load/Validate 发生在 S2 恢复之前（或者干脆复用了锁外读到的快照），
//     读到的是那份未闭合事务的 rejected 目标态 ⇒ 校验失败、退 2、目标一个字节都不会删；
//   - 只有「先恢复、后锁内重读」，才会读到回滚后的 approved 前像 ⇒ 删除照常执行。
//
// 因此本用例不看钩子名称，而是让结果本身作证：命令成功、目标真的被删、提案 execution
// 真的记成 succeeded。钩子只补一针现场取证（取锁瞬间盘上确实还是 rejected 的崩溃态）。
// 另外三条是恢复屏障的审计面：W26 一路留到**最终 report**、被恢复的那笔写下 abort、
// 本次另起一个不复用的新号码。
func TestDeleteRecoverPrecedesProposalReread(t *testing.T) {
	dir, noteRel, _ := deleteVault(t)
	rec := approvedProposalFor(t, dir, []string{applyNoteID})
	prelAbs := absIn(dir, rec.Rel)

	pre := mustRead(t, prelAbs) // 前像：status: approved
	target := deleteRejectBytes(t, pre)

	// 造崩溃现场：分配号码 → 发布 intent（ExpectCommit）→ 把目标态写到盘上，
	// 之后**不写** commit / abort —— 这正是 Scan 认定的 OpenTxn（P4）。
	stale, err := txn.AllocateTxnID(dir)
	if err != nil {
		t.Fatalf("分配 txn_id 失败：%v", err)
	}
	now := func() time.Time { return captureAt(t) }
	if _, err := txn.WriteIntent(dir, stale, txn.IntentInput{
		Argv: []string{"eg", "proposal", "reject"}, ExpectCommit: true, Now: now,
		Files: []txn.FileSpec{{
			Path: rec.Rel, PreBytes: pre, TargetBytes: target, TargetOp: "update",
		}},
	}); err != nil {
		t.Fatalf("发布 intent 失败：%v", err)
	}
	if err := os.WriteFile(prelAbs, target, 0o644); err != nil {
		t.Fatalf("写崩溃态失败：%v", err)
	}

	base := txnIDsOn(t, dir)
	logBefore := gitLogCount(t, dir)

	var atAcquire, atRecovered string
	watchTxn(t, func(step, _ string) {
		switch step {
		case TxnStepAcquired:
			atAcquire = string(mustRead(t, prelAbs))
		case TxnStepRecovered:
			atRecovered = string(mustRead(t, prelAbs))
		}
	})

	code, env, output := runDeleteCLI(t, newTestRoot(t, dir), dir,
		"--target", applyNoteID, "--reason", "恢复先于提案重读", "--proposal", string(rec.ID),
		"--confirm", "--user-request")

	// ① 顺序的直接证据：命令成功了 —— 说明锁内读到的是回滚后的 approved 前像。
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0（S2 应先把提案回滚成 approved，S3 才去读它）：%s",
			code, output)
	}
	// ② 现场取证：取锁瞬间盘上确实还是崩溃态，恢复完成瞬间已逐字回到前像。
	if !strings.Contains(atAcquire, proposal.KeyStatus+": '"+string(proposal.StatusRejected)+"'") {
		t.Fatalf("取锁瞬间盘上就不是 rejected 崩溃态：夹具没生效，本用例什么都没证明\n%s", atAcquire)
	}
	if atRecovered != string(pre) {
		t.Fatal("恢复节点回调时提案仍未回到前像：S2 没有真正回滚")
	}
	// ③ 真的删了：目标写上删除标记，提案在同一笔事务里记成 succeeded。
	if got := string(mustRead(t, absIn(dir, noteRel))); !strings.Contains(got, "deleted_at:") {
		t.Fatalf("恢复之后必须真的执行一次删除，目标却没有 deleted_at：\n%s", got)
	}
	if got := deleteExecStatus(t, dir, rec.Rel); got != proposal.ExecSucceeded {
		t.Fatalf("execution.status = %q，期望 %q", got, proposal.ExecSucceeded)
	}
	if n := gitLogCount(t, dir); n != logBefore+1 {
		t.Fatalf("commit 数 %d → %d，期望恰 +1（恢复后照常走完 S7）", logBefore, n)
	}
	// ④ W26 必须保留在**最终 report** 里（真实回滚才有这条）。
	if !contains(undReportWarnCodes(t, env), txn.CodeTxnRecovered) {
		t.Fatalf("真实回滚必须在最终 report.warnings[] 留一条 %s，实得 %v",
			txn.CodeTxnRecovered, undReportWarnCodes(t, env))
	}
	// ⑤ 被恢复的那笔写下 abort，本次另起一个新号码且不复用它。
	if !markerExists(dir, stale, txn.AbortMarker) {
		t.Fatal("被回滚的未闭合事务必须写下 abort 标记")
	}
	if markerExists(dir, stale, txn.CommitMarker) {
		t.Fatal("被回滚的未闭合事务不该有 commit 标记")
	}
	id := onlyNewTxn(t, dir, base, "恢复之后的一次真实 delete")
	if id == stale {
		t.Fatalf("本次不得复用被回滚的事务号 %q", stale)
	}
	if got := undReportTxnID(t, env); got != id {
		t.Fatalf("report.txn_id = %q，新增事务目录 = %q：两者必须恰好对上", got, id)
	}
}

// —— D) S1 撞锁：有限时间内带 E16 返回，权威 / 事务 / Git 全线零变化 ——

// TestDeleteLockBusyReturnsE16WithoutHanging：
// 另一个进程（这里用同进程的另一个 fd 真实 flock）正持着 `.index/run.lock`，
// 本次 delete 必须在**有限时间**内带 E16 阻断，而不是挂死，也不是绕开锁去写。
//
// 「有限时间」这一针针对的是一个非常具体的坑：全仓 CLI 用例都把业务时钟 `Root.Now`
// 钉成常量（stamp / intent.started_at 需要可复算）。若 Acquire 的等待判据误用了这个
// 业务时钟，`now().Sub(start)` 恒为 0 ⇒ `waited >= timeout` 永不成立 ⇒ 锁一旦被占住，
// 退避重试就会无限循环。所以这里同时钉住两件事：**等待走真实墙钟**（80ms 上限，
// 10s 内必须返回），以及**等待期留痕如实交付**（W28 与最终的 E16 一起进 errors[]）。
//
// 另一半是「撞锁 = 什么都没做」：delete 的 S0 只看参数形态，任何库内事实的读写都在锁后，
// 因此权威内容逐字节不变、事务目录集合零新增、既有事务日志一个字节没动、Git 也不动
// （commit 数与 HEAD 双判据）。
func TestDeleteLockBusyReturnsE16WithoutHanging(t *testing.T) {
	dir, noteRel, card2Rel, rec := deleteTwoTargetVault(t)
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
		// runDeleteCLI 内部把 Root.Now 钉成常量（captureAt）：业务时钟固定，
		// 锁等待却必须照样走真实墙钟。
		code, env, output := runDeleteCLI(t, newTestRoot(t, dir), dir,
			"--target", applyNoteID, "--reason", "锁被占住", "--proposal", string(rec.ID),
			"--confirm", "--user-request")
		done <- runOut{code: code, env: env, out: output}
	}()

	var got runOut
	select {
	case got = <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("锁被占用时 delete 在 10s 内没有返回：等待上限只有 %s=80ms，"+
			"说明 Acquire 拿到的是被钉死的业务时钟（elapsed 恒为 0 ⇒ 无限退避）",
			txn.LockTimeoutEnv)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("等待上限 80ms，实际耗时 %s：锁等待没有走真实墙钟", elapsed)
	}

	// ① 非零返回：撞锁是阻断，绝不能被当成「删成功了」。
	if got.code == ExitOK {
		t.Fatalf("锁被占用必须阻断，实得退出码 0：%s", got.out)
	}
	// ② 诊断：最终的 E16 与等待期的 W28 都必须随错误一起交付。
	diags := errorDiagsOf(t, got.env)
	if !diagsHaveCode(diags, txn.CodeLockTimeout) {
		t.Fatalf("锁等待超时必须携 %s，实得 %+v", txn.CodeLockTimeout, diags)
	}
	// I-…-016：W28 是 level=warning，取材面按合同 §3 搬到信封 warnings[]，另加分桶双侧封闭。
	assertLockWaitBuckets(t, got.env, "delete 撞上锁")
	// ③ 权威零写：两个目标都没长出删除标记，提案 execution 也没被改判。
	assertAuthorityUnchanged(t, dir, before, "delete 撞上锁")
	for _, rel := range []string{noteRel, card2Rel} {
		if raw := string(mustRead(t, absIn(dir, rel))); strings.Contains(raw, "deleted_at:") {
			t.Fatalf("没拿到锁却写了 %s：\n%s", rel, raw)
		}
	}
	if st := deleteExecStatus(t, dir, rec.Rel); st != rec.Proposal().Execution.Status {
		t.Fatalf("execution.status = %q，期望保持 %q（撞锁不得改判提案）",
			st, rec.Proposal().Execution.Status)
	}
	// ④ 事务侧：零新增事务目录，既有事务日志逐字节不变（连 seq 都不许悄悄前进）。
	assertNoNewTxn(t, dir, base, "delete 撞上锁")
	assertTxnTreeUnchanged(t, dir, indexBefore, "delete 撞上锁")
	// ⑤ Git 侧：commit 数与 HEAD 双判据都不动。
	if n := gitLogCount(t, dir); n != logBefore {
		t.Fatalf("没拿到锁不得产生 commit：%d → %d", logBefore, n)
	}
	if h := gitOut(t, dir, "rev-parse", "HEAD"); h != headBefore {
		t.Fatalf("没拿到锁不得移动 HEAD：%q → %q", headBefore, h)
	}
}

// —— E) S7 Git 失败：退 4、全部目标保持目标态、绝不做第二次权威写 ——

// TestDeleteGitFailureKeepsAllTargetStateWithoutSecondWrite：
// commit marker 已经落盘 ⇒ 两个目标的删除标记与提案 `execution=succeeded` **已经原子生效**。
// 此时 Git 失败只是「这批改动没进版本历史」，不是 Markdown 事务失败。
//
// 这条分支在 delete 上格外危险，因为 M3 的旧形态会在提交失败时补一刀
// `RecordExecution(failed)` —— 那是**事务外的第二次权威写**：它会让盘上的提案字节
// 与 intent 里声明的目标字节对不上，也就是让崩溃恢复失去判据。所以这里逐条钉死：
//
//	① 退 4；
//	② 两个 target 都保持「已删」的目标态，提案保持 succeeded（不回滚、不改判）；
//	③ 恰一个事务，commit 标记在盘、abort 不在盘（Git 失败不是事务失败，也不开第二个事务）；
//	④ Git 侧零 commit；
//	⑤ **Git 节点当时的权威快照 == 命令返回后的权威快照**：Git 之后一个权威字节都没再动；
//	⑥ committed < git < index_sync < releasing —— 失败支同样要走完 S8，且仍在同一把锁内。
//	   从 Git 失败支提前 return 会让 index_sync 缺席，等于瞒着索引「权威内容已经变了」，
//	   还把陈旧窗口留到锁外。
func TestDeleteGitFailureKeepsAllTargetStateWithoutSecondWrite(t *testing.T) {
	dir, noteRel, card2Rel, rec := deleteTwoTargetVault(t)
	base := txnIDsOn(t, dir)
	logBefore := gitLogCount(t, dir)

	var atGit map[string]string
	steps := watchTxn(t, func(step, _ string) {
		if step == TxnStepGit {
			// Git 刚尝试完（失败）的瞬间取证 —— 这一针同时要求 TxnStepGit 在**失败支**也触发。
			atGit = authoritySnapshot(t, dir)
		}
	})

	r := newTestRoot(t, dir)
	r.NewRepo = failingCommitRepo()
	code, env, output := runDeleteCLI(t, r, dir,
		"--target", applyNoteID, "--reason", "批量清理", "--proposal", string(rec.ID),
		"--confirm", "--user-request")

	// ① 退 4。
	if code != ExitCommitFailed {
		t.Fatalf("退出码 = %d，期望 4（Markdown 已原子生效、Git 失败）：%s", code, output)
	}
	if atGit == nil {
		t.Fatal("没有触达 Git 节点：TxnStepGit 在失败支没有触发，本用例什么都没证明")
	}

	// ② 全部目标保持目标态：两个 target 都已删，提案记成 succeeded（一个都不许回滚 / 改判）。
	for _, rel := range []string{noteRel, card2Rel} {
		if raw := string(mustRead(t, absIn(dir, rel))); !strings.Contains(raw, "deleted_at:") {
			t.Fatalf("Git 失败不得回滚 Markdown，%s 却没有 deleted_at：\n%s", rel, raw)
		}
	}
	if st := deleteExecStatus(t, dir, rec.Rel); st != proposal.ExecSucceeded {
		t.Fatalf("execution.status = %q，期望 %q（Git 失败不得改判提案，"+
			"更不得补一次 execution=failed 的事务外写）", st, proposal.ExecSucceeded)
	}
	// ⑤ Git 之后零二次权威写：返回时的字节与 Git 节点当时逐字相同。
	assertAuthorityUnchanged(t, dir, atGit, "delete 的 Git 失败后")

	// ③ 事务侧：恰一个事务、commit 在盘、abort 不在盘、号码与报告对上。
	id := onlyNewTxn(t, dir, base, "Git 失败")
	if got := undReportTxnID(t, env); got != id {
		t.Fatalf("report.txn_id = %q 与新增事务目录 %q 对不上（Git 失败绝不开第二个事务）",
			got, id)
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
	// ⑥ 失败支的节点次序：S6 → S7 → S8 → S9，四格全触发且严格递增（S8 仍在锁内）。
	assertStepOrder(t, steps, TxnStepCommitted, TxnStepGit, TxnStepIndexSync, TxnStepReleasing)
}

// —— F) 第 9a 步改判：原子域恰「原提案 + 新提案」，知识产物一个字节都不碰 ——

// TestDeleteSupersedeTxnCoversBothProposals：
// 提案批准之后库又变了（同一篇笔记多支撑了一张卡 ⇒ cards_losing_support 多一项），
// delete 的执行前重算因此与提案里记的影响面不一致 ⇒ 走第 9a 步：**不执行删除**，
// 原提案标 superseded（T4）+ 生成承载新影响面的新提案。
//
// 这一支的原子域与真实删除分支完全不同，必须单独钉：
//
//	① **知识产物逐字不变** —— 改判只动提案控制面；笔记与两张卡一个字节都不许被碰
//	   （尤其不许留下 deleted_at：批准的前提已经不成立了）；
//	② 一次改判 = **一笔**事务，且 intent.files[] 恰是「原提案 + 新提案」两份 ——
//	   少一份就会留下孤儿新提案或两份都自称有效的提案，多一份说明顺手改了别的东西；
//	③ 原提案 status=superseded 且 decision.superseded_by 指向新提案（提案链不断裂）；
//	④ 新提案真的在盘上；
//	⑤ commit 标记在盘、abort 不在盘；
//	⑥ report.txn_id 与事务目录逐字对上（A-59）；
//	⑦ Git 恰 +1（两份提案同进一次历史）。
func TestDeleteSupersedeTxnCoversBothProposals(t *testing.T) {
	dir, noteRel, cardRel := deleteVault(t)
	rec := approvedProposalFor(t, dir, []string{applyNoteID})

	// 批准**之后**再落一张同样由这篇笔记支撑的卡：影响面里的 cards_losing_support 随之变化，
	// 执行前重算与提案记录的影响面不再一致 ⇒ 触发第 9a 步。
	if code, _, errOut := runApplyPlan(t, dir, cardPlan(applyCard2ID, "")); code != ExitOK {
		t.Fatalf("前置：落第二张卡退出码 = %d：%s", code, errOut)
	}
	card2Rel := store.CardRel("ai-infra", applyCard2ID)

	// 知识产物的前像（改判分支之后必须逐字相同）。
	knowledge := map[string]string{}
	for _, rel := range []string{noteRel, cardRel, card2Rel} {
		knowledge[rel] = string(mustRead(t, absIn(dir, rel)))
	}
	base := txnIDsOn(t, dir)
	logBefore := gitLogCount(t, dir)

	code, env, output := runDeleteCLI(t, newTestRoot(t, dir), dir,
		"--target", applyNoteID, "--reason", "影响面已变化", "--proposal", string(rec.ID),
		"--confirm", "--user-request")
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0（第 9a 步改判本身是一次成功的提案控制面写入）：%s",
			code, output)
	}

	// ① 知识产物逐字不变：改判不执行删除，一个知识文件都不许被碰。
	for rel, want := range knowledge {
		got := string(mustRead(t, absIn(dir, rel)))
		if got != want {
			t.Fatalf("改判分支不得改动任何知识产物，%s 却变了：\n前：%q\n后：%q", rel, want, got)
		}
		if strings.Contains(got, "deleted_at:") {
			t.Fatalf("改判分支不得执行删除，%s 却写上了 deleted_at", rel)
		}
	}

	// ② 一笔事务，原子域恰两份提案。新提案的路径从 intent 里反解（它由 Supersede 现取当日序号）。
	id := onlyNewTxn(t, dir, base, "一次第 9a 步改判")
	paths := intentPathsOf(t, dir, id)
	if len(paths) != 2 {
		t.Fatalf("intent.files[] = %v，期望恰两份提案（原提案 + 新提案）", paths)
	}
	if !contains(paths, rec.Rel) {
		t.Fatalf("原提案 %s 不在 intent.files[] 里：%v（改判漏出了原子域）", rec.Rel, paths)
	}
	var newRel string
	for _, p := range paths {
		if p != rec.Rel {
			newRel = p
		}
	}
	if !strings.HasPrefix(newRel, proposal.DirProposals+"/") {
		t.Fatalf("原子域里的第二份文件 %q 不是提案：%v（改判只该动提案控制面）", newRel, paths)
	}
	// ④ 新提案真的在盘上。
	newRec, err := proposal.LoadRel(store.New(dir), newRel)
	if err != nil {
		t.Fatalf("新提案 %s 读回失败：%v", newRel, err)
	}

	// ③ 原提案已 superseded，且提案链指向新提案。
	after, err := proposal.LoadRel(store.New(dir), rec.Rel)
	if err != nil {
		t.Fatalf("原提案读回失败：%v", err)
	}
	if got := after.Proposal().Status; got != proposal.StatusSuperseded {
		t.Fatalf("原提案 status = %q，期望 %q", got, proposal.StatusSuperseded)
	}
	if got := after.Proposal().Decision.SupersededBy; got != string(newRec.ID) {
		t.Fatalf("原提案 decision.%s = %q，期望指向新提案 %q（提案链不得断裂）",
			proposal.KeySupersededBy, got, newRec.ID)
	}

	// ⑤ commit 在、abort 不在。
	if !markerExists(dir, id, txn.CommitMarker) {
		t.Fatal("两份提案已生效，commit 标记必须在盘")
	}
	if markerExists(dir, id, txn.AbortMarker) {
		t.Fatal("已提交的事务不该有 abort 标记")
	}
	// ⑥ 号码与日志同真。
	if got := undReportTxnID(t, env); got != id {
		t.Fatalf("report.txn_id = %q，新增事务目录 = %q：两者必须恰好对上", got, id)
	}
	// ⑦ 恰 +1 条 commit（两份提案同进一次历史）。
	if n := gitLogCount(t, dir); n != logBefore+1 {
		t.Fatalf("commit 数 %d → %d，期望恰 +1", logBefore, n)
	}
}
