package cli

// proposal_new_transaction_test.go —— M6 · T-…-072 批次 C4a：`eg proposal new` 的
// **事务反证**（原子性合同 §5.2 / §16.1、A-59）。
//
// 与 proposal_cmd_test.go 分工：那边看业务语义（Agent 可调用、影响面四项、W9 缺项），
// 这边只看事务本体 —— 一次新建是不是**一笔**事务、原子域里是不是恰那一份提案、
// commit marker 在不在盘、号码有没有如实交付、崩溃恢复屏障是否早于锁内 ID 重算。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/proposal"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
	"github.com/ikaqiu-Lemon/EverGreen/internal/txn"
)

// TestProposalNewTxnCoversOneProposal 一次钉死成功路径的七件事：
//
//	① 退 0；
//	② report.txn_id 非空（A-59：号码即审计凭据）；
//	③ 相对基线恰新增**一个**事务目录；
//	④ intent.files[] 恰 1 条，且路径等于 report.proposals[0].path（原子域恰那份新提案）；
//	⑤ commit 标记在盘；
//	⑥ Git commit 恰 +1（新提案进一次历史）；
//	⑦ 工作区干净（事务闭合、无脏文件残留）。
func TestProposalNewTxnCoversOneProposal(t *testing.T) {
	dir := proposalVault(t)
	base := txnIDsOn(t, dir)
	logBefore := gitLogCount(t, dir)

	code, env, errOut := runProposalCLI(t, dir,
		"new", "--type", string(proposal.TypeLogicalDelete), "--target", applyCardID,
		"--reason", "该卡已过时")

	// ① 退 0。
	if code != ExitOK {
		t.Fatalf("eg proposal new 退出码 = %d，期望 0：%s", code, errOut)
	}

	rep := applyReport(t, env)
	// ② 号码非空。
	if rep.TxnID == "" {
		t.Fatalf("report.txn_id 必须非空（A-59：成功分配的事务号一律交付）：%+v", rep)
	}
	if len(rep.Proposals) != 1 {
		t.Fatalf("report.proposals[] 必须恰 1 条，实得 %d 条：%v", len(rep.Proposals), rep.Proposals)
	}
	newRel := rep.Proposals[0].Path

	// ③ 恰新增一个事务目录，且与报告号码对上。
	id := onlyNewTxn(t, dir, base, "一次成功的 proposal new")
	if id != rep.TxnID {
		t.Fatalf("report.txn_id = %q，新增事务目录 = %q：两者必须恰好对上", rep.TxnID, id)
	}

	// ④ intent.files[] 恰 1 条，且等于新提案路径。
	paths := intentPathsOf(t, dir, id)
	if len(paths) != 1 || paths[0] != newRel {
		t.Fatalf("intent.files[] = %v，期望恰含新提案 %q 一份", paths, newRel)
	}

	// ⑤ commit 在盘、abort 不在盘。
	if !markerExists(dir, id, txn.CommitMarker) {
		t.Fatal("提案已创建，commit 标记必须在盘")
	}
	if markerExists(dir, id, txn.AbortMarker) {
		t.Fatal("已提交的事务不该有 abort 标记")
	}

	// ⑥ Git 恰 +1。
	if n := gitLogCount(t, dir); n != logBefore+1 {
		t.Fatalf("commit 数 %d → %d，期望恰 +1", logBefore, n)
	}

	// ⑦ 工作区干净。
	if got := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); got != "" {
		t.Fatalf("新建成功后工作区应干净，实得 %q", got)
	}
}

// TestProposalNewRecoverPrecedesIDReread：
// 用一笔**真实的未闭合 create 事务**（合同 §6 的 P4 崩溃点：intent 已发布、目标文件已
// 以新建形态 rename 到盘上，commit / abort 皆缺席）占住当天的 `p-...-001`。
//
// 这个现场直接决定 new 分配到几号：`ListIDs` / `NextSeq` 必须在**锁内、且恢复之后**重算。
//
//   - 若 S3 的 ID 重算发生在 S2 恢复之前（或复用了锁外快照），会看到那份未闭合事务留下的
//     `-001` 文件 ⇒ 误判当日已用 001 ⇒ 错误地分配 `-002`；
//   - 只有「先恢复、后锁内重扫」，恢复才会把那份 create 回滚删除，重扫看到空库 ⇒ 照常分配 `-001`。
//
// 因此本用例让结果本身作证：命令成功、report.proposals[0].path 仍是同一个 `-001`。
// 钩子补两针现场取证（取锁瞬间该文件在、恢复完成瞬间已被删）。另有三条恢复屏障审计面：
// W26 一路留到最终 report、被恢复的那笔写下 abort、本次另起一个不复用的新号码。
func TestProposalNewRecoverPrecedesIDReread(t *testing.T) {
	dir := proposalVault(t)

	// 固定业务时钟的日期（runProposalCLI 内部把 Root.Now 钉成 captureAt）：据此推出当天 001。
	date := model.NewDate(captureAt(t))
	id001, err := proposal.NewID(date, 1)
	if err != nil {
		t.Fatalf("拼当天 001 提案 ID 失败：%v", err)
	}
	rel001 := proposal.Rel(id001)
	abs001 := absIn(dir, rel001)

	// 目标字节：一份**恰合规**的 001 提案（影响面取自当下重算，与真实 new 会写的同源）。
	impact, err := proposal.RecomputeImpact(store.New(dir), []string{applyCardID})
	if err != nil {
		t.Fatalf("重算影响面失败：%v", err)
	}
	target, err := proposal.RenderTemplate(proposal.Template{
		ID: id001, Title: "逻辑删除 " + applyCardID, CreatedAt: date.String(),
		Targets: []string{applyCardID}, Impact: impact,
	})
	if err != nil {
		t.Fatalf("渲染 001 目标字节失败：%v", err)
	}

	// 造崩溃现场：分配号码 → 发布 create intent（ExpectCommit）→ 把目标态写到盘上，
	// 之后**不写** commit / abort —— 这正是 Scan 认定的 OpenTxn（P4）。
	stale, err := txn.AllocateTxnID(dir)
	if err != nil {
		t.Fatalf("分配 txn_id 失败：%v", err)
	}
	now := func() time.Time { return captureAt(t) }
	if _, err := txn.WriteIntent(dir, stale, txn.IntentInput{
		Argv: []string{"eg", "proposal", "new"}, ExpectCommit: true, Now: now,
		Files: []txn.FileSpec{{
			Path: rel001, Create: true, TargetBytes: target, TargetOp: "create",
		}},
	}); err != nil {
		t.Fatalf("发布 create intent 失败：%v", err)
	}
	if err := os.MkdirAll(filepath.Dir(abs001), 0o755); err != nil {
		t.Fatalf("建 proposals/ 目录失败：%v", err)
	}
	if err := os.WriteFile(abs001, target, 0o644); err != nil {
		t.Fatalf("写崩溃态失败：%v", err)
	}

	base := txnIDsOn(t, dir)
	logBefore := gitLogCount(t, dir)

	var existAtAcquire, existAtRecovered bool
	watchTxn(t, func(step, _ string) {
		switch step {
		case TxnStepAcquired:
			_, statErr := os.Stat(abs001)
			existAtAcquire = statErr == nil
		case TxnStepRecovered:
			_, statErr := os.Stat(abs001)
			existAtRecovered = statErr == nil
		}
	})

	code, env, errOut := runProposalCLI(t, dir,
		"new", "--type", string(proposal.TypeLogicalDelete), "--target", applyCardID,
		"--reason", "恢复先于 ID 重算")

	// ① 顺序的直接证据：命令成功，且仍分配到 001 —— 说明锁内重扫发生在恢复删掉旧 001 之后。
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0（S2 应先回滚删除旧 001，S3 才去重算序号）：%s", code, errOut)
	}
	rep := applyReport(t, env)
	if len(rep.Proposals) != 1 {
		t.Fatalf("report.proposals[] 必须恰 1 条，实得 %v", rep.Proposals)
	}
	if got := rep.Proposals[0].Path; got != rel001 {
		t.Fatalf("report.proposals[0].path = %q，期望仍是 %q（ListIDs/NextSeq 若读早了会错分 002）",
			got, rel001)
	}

	// ② 现场取证：取锁瞬间旧 001 还在，恢复完成瞬间已被回滚删除。
	if !existAtAcquire {
		t.Fatal("取锁瞬间盘上就没有旧 001：夹具没生效，本用例什么都没证明")
	}
	if existAtRecovered {
		t.Fatal("恢复节点回调时旧 001 仍在：S2 没有真正回滚这笔未闭合 create")
	}

	// ③ W26 必须保留在**最终 report** 里（真实回滚才有这条）。
	if !contains(undReportWarnCodes(t, env), txn.CodeTxnRecovered) {
		t.Fatalf("真实回滚必须在最终 report.warnings[] 留一条 %s，实得 %v",
			txn.CodeTxnRecovered, undReportWarnCodes(t, env))
	}

	// ④ 被恢复的那笔写下 abort、无 commit；本次另起一个新号码且不复用它。
	if !markerExists(dir, stale, txn.AbortMarker) {
		t.Fatal("被回滚的未闭合事务必须写下 abort 标记")
	}
	if markerExists(dir, stale, txn.CommitMarker) {
		t.Fatal("被回滚的未闭合事务不该有 commit 标记")
	}
	id := onlyNewTxn(t, dir, base, "恢复之后的一次真实 proposal new")
	if id == stale {
		t.Fatalf("本次不得复用被回滚的事务号 %q", stale)
	}
	if got := rep.TxnID; got != id {
		t.Fatalf("report.txn_id = %q，新增事务目录 = %q：两者必须恰好对上", got, id)
	}

	// ⑤ 恢复后照常走完 S7：Git 恰 +1。
	if n := gitLogCount(t, dir); n != logBefore+1 {
		t.Fatalf("commit 数 %d → %d，期望恰 +1", logBefore, n)
	}
}

// TestProposalNewLockBusyReturnsE16WithoutHanging：
// 另一个持有者（这里用同进程的另一个 fd 真实 flock）正占着 `.index/run.lock`，
// 本次 proposal new 必须在**有限时间**内带 E16 阻断，而不是挂死，也不是绕开锁去写。
//
// 「有限时间」这一针针对一个具体的坑：全仓 CLI 用例都把业务时钟 `Root.Now` 钉成常量
// （stamp / intent.started_at 需要可复算）。若 Acquire 的等待判据误用了这个业务时钟，
// `now().Sub(start)` 恒为 0 ⇒ `waited >= timeout` 永不成立 ⇒ 锁一旦被占住，退避重试
// 就会无限循环。所以这里同时钉住两件事：**等待走真实墙钟**（80ms 上限，10s 内必须返回），
// 以及**等待期留痕如实交付**（W28 与最终的 E16 一起进 errors[]）。
//
// E16（txn.CodeLockTimeout）恰是 txn.ErrLockUnavailable 这枚 sentinel 的诊断映射
// （LockTimeoutError.Unwrap → ErrLockUnavailable、Code → E16），因此 errors[] 里出现
// E16 就等价于「底层 errors.Is(err, txn.ErrLockUnavailable) 成立」。M6/T-074 起退出码 5
// 已启用：TxnBlockedError 携 E16 经 classifyExit5 归为 LockUnavailableError，在唯一翻译点
// ExitCodeFor 落 ExitPrecheckOrLock(5)——这里钉的就是这条目标态，不再是未分类兜底 1。
//
// 另一半是「撞锁 = 什么都没做」：new 的 S0 只解析参数形态，任何库内事实的读写都在锁后，
// 因此提案权威字节逐字不变、事务目录集合零新增、既有事务日志一个字节没动、Git 也不动
// （commit 数与 HEAD 双判据）。
func TestProposalNewLockBusyReturnsE16WithoutHanging(t *testing.T) {
	dir := proposalVault(t)
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
		// runProposalCLI 内部把 Root.Now 钉成常量（captureAt）：业务时钟固定，
		// 锁等待却必须照样走真实墙钟。
		code, env, output := runProposalCLI(t, dir,
			"new", "--type", string(proposal.TypeLogicalDelete), "--target", applyCardID,
			"--reason", "锁被占住")
		done <- runOut{code: code, env: env, out: output}
	}()

	var got runOut
	select {
	case got = <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("锁被占用时 proposal new 在 10s 内没有返回：等待上限只有 %s=80ms，"+
			"说明 Acquire 拿到的是被钉死的业务时钟（elapsed 恒为 0 ⇒ 无限退避）",
			txn.LockTimeoutEnv)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("等待上限 80ms，实际耗时 %s：锁等待没有走真实墙钟", elapsed)
	}

	// ① M6/T-074 映射：锁不可用（E16）经 classifyExit5 归为 LockUnavailableError，
	//    在唯一翻译点 ExitCodeFor 落 ExitPrecheckOrLock(5)，绝不是「创建成功」，也不再兜底到 1。
	if got.code != ExitPrecheckOrLock {
		t.Fatalf("撞锁应按 M6 映射退 %d（锁不可用 E16 → 退出码 5），实得 %d：%s",
			ExitPrecheckOrLock, got.code, got.out)
	}
	// ② 诊断：最终的 E16（= errors.Is(err, txn.ErrLockUnavailable)）与等待期的 W28 都必须交付。
	diags := errorDiagsOf(t, got.env)
	if !diagsHaveCode(diags, txn.CodeLockTimeout) {
		t.Fatalf("锁等待超时必须携 %s（ErrLockUnavailable 的诊断映射），实得 %+v",
			txn.CodeLockTimeout, diags)
	}
	// I-…-016：W28 是 level=warning，取材面按合同 §3 搬到信封 warnings[]，另加分桶双侧封闭。
	assertLockWaitBuckets(t, got.env, "proposal new 撞上锁")
	// ③ 权威零写：提案目录逐字节不变。
	assertAuthorityUnchanged(t, dir, before, "proposal new 撞上锁")
	// ④ 事务侧：零新增事务目录，既有事务日志逐字节不变（连 seq 都不许悄悄前进）。
	assertNoNewTxn(t, dir, base, "proposal new 撞上锁")
	assertTxnTreeUnchanged(t, dir, indexBefore, "proposal new 撞上锁")
	// ⑤ Git 侧：commit 数与 HEAD 双判据都不动。
	if n := gitLogCount(t, dir); n != logBefore {
		t.Fatalf("没拿到锁不得产生 commit：%d → %d", logBefore, n)
	}
	if h := gitOut(t, dir, "rev-parse", "HEAD"); h != headBefore {
		t.Fatalf("没拿到锁不得移动 HEAD：%q → %q", headBefore, h)
	}
}

// TestProposalNewCommitRollbackIsPartialWriteNotBlocked：
// 复刻 mark-reviewed 的「S6 提交期 I/O 失败」反证到 new 上。做法一致：在 S5 发布 intent 的
// 节点，把本笔事务目录里的 `commit` 标记名占成一个**非空目录**（`commit.tmp/occupied`），
// 于是 S6 发布 commit marker 的原子 rename 必然失败。
//
// 合同 §5.2：提交期失败属**主动放弃**（已 stage 的 overlay 整体回滚），退 3（ExitPartialWrite），
// 而不是把它当成基础设施崩坏退 1。对 new 而言「回滚」意味着那份**新提案压根不存在**——
// overlay 里 stage 的新建文件从未落到权威盘上。因此这里既查完整权威快照逐字不变，
// 又单独钉一针「新提案路径 os.Stat 不存在」，把「创建被整体撤销」说死。
//
// 事务侧：这一笔写下 abort、无 commit，且 report.txn_id 恰等于该 abort 目录名
// （A-59：号码一旦分配即为审计凭据，主动放弃也要如实交付）。Markdown 从未生效 ⇒ S7/S8
// 一步都不许跑，Git commit 数纹丝不动。最后逐路径交代：本命令恰一个目标，「目标未写入」
// 必须恰一条，不多不少。
func TestProposalNewCommitRollbackIsPartialWriteNotBlocked(t *testing.T) {
	dir := proposalVault(t)
	before := authoritySnapshot(t, dir)
	base := txnIDsOn(t, dir)
	logBefore := gitLogCount(t, dir)

	// 本次预期分配到当天 001（proposalVault 当日尚无提案），据此断言「新提案不存在」。
	date := model.NewDate(captureAt(t))
	id001, err := proposal.NewID(date, 1)
	if err != nil {
		t.Fatalf("拼当天 001 提案 ID 失败：%v", err)
	}
	absNew := absIn(dir, proposal.Rel(id001))

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

	code, env, output := runProposalCLI(t, dir,
		"new", "--type", string(proposal.TypeLogicalDelete), "--target", applyCardID,
		"--reason", "提交期失败整体回滚")
	if code != ExitPartialWrite {
		t.Fatalf("退出码 = %d，期望 3（提交失败已整体回滚，属主动放弃而非基础设施崩坏）：%s",
			code, output)
	}
	if blocked == "" {
		t.Fatal("没有触达 intent 节点：本用例什么都没证明")
	}
	_ = os.RemoveAll(blocked)

	// ① 权威内容回到事务开始前，且那份新提案根本没落盘。
	assertAuthorityUnchanged(t, dir, before, "proposal new 提交失败并回滚后")
	if _, statErr := os.Stat(absNew); !os.IsNotExist(statErr) {
		t.Fatalf("提交回滚后新提案不得存在，但 %s 仍在（stat err=%v）", proposal.Rel(id001), statErr)
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

// TestProposalNewGitFailureKeepsTargetWithoutSecondWrite：
// 复刻 mark-reviewed / delete 的「S7 Git 失败」反证到 new 上。注入一个 commit 必失败的
// 仓库（failingCommitRepo），跑真实 proposal new：commit marker 一旦落盘，那份新提案就已
// **原子生效**；此时 Git 失败只是「这批改动没进版本历史」，绝不是 Markdown 事务失败。
//
// 于是逐条钉死：① 退 4（ExitCommitFailed，不是退 1）；② 在 TxnStepGit 瞬间取权威快照，
// 命令返回后与之**逐字相同**——Git 之后没有第二次权威写（new 不得因 Git 失败去补写/删除
// 那份已生效的提案）；③ 失败支同样走完 S8 且仍在同一把锁内：committed < git <
// index_sync < releasing 四格全触发且严格递增；④ 恰一个新事务、commit 标记在盘、无 abort、
// report.txn_id 与之对上（Git 失败绝不开第二个事务，也不写 abort）；⑤ Git 侧零 commit、
// report.git.commit 为空，且错误诊断如实交代 Git 失败。
func TestProposalNewGitFailureKeepsTargetWithoutSecondWrite(t *testing.T) {
	dir := proposalVault(t)
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
	code, out, errOut := runCLI(t, r, "proposal", "new",
		"--type", string(proposal.TypeLogicalDelete), "--target", applyCardID,
		"--reason", "Git 注入失败", "--vault", dir, "--json")
	var env Envelope
	if strings.TrimSpace(out) != "" {
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("--json 输出不可解析：%v\n%s", err, out)
		}
	}

	// ① 退 4：Markdown 已原子生效，仅 Git 失败。
	if code != ExitCommitFailed {
		t.Fatalf("退出码 = %d，期望 4（提案已原子生效、Git 失败）：%s", code, errOut)
	}
	if atGit == nil {
		t.Fatal("没有触达 Git 节点：TxnStepGit 在失败支没有触发，本用例什么都没证明")
	}

	// ② Git 之后零二次权威写：返回时的字节与 Git 节点当时逐字相同。
	assertAuthorityUnchanged(t, dir, atGit, "proposal new 的 Git 失败后")
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
