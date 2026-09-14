package cli

// reconcile_transaction_test.go —— M6 · T-…-072 批次 C5：`eg reconcile` **非 dry-run** 路径
// 的事务正证（对账事务底座 · 合同 §16.1 / A-59）。
//
// 与 reconcile_test.go 分工：那边看命令侧语义（信封五键、data.reconcile 三键、退出码次序、
// dry-run 零写入、非法参数零副作用）；这边只看**一次真实修复对账**的事务本体 —— R2 补过目信号
// 单键 + R6 写失准标记两键这两处权威写，是不是合并成**一笔**事务、原子域是不是恰那两份被改写的
// 文件、commit marker 在不在盘、号码有没有如实交付、Git 是不是恰一次纳管提交、S8 写后索引同步
// 是否仍夹在同一把 run.lock 内（committed < git < index_sync < releasing，且 releasing 恰一次）。
//
// 造数不 mock：r6BumpCard 事后更新引用卡（真实提交）让综述失准 → R6 命中 recap_stale；
// r2EditFile 直接在编辑器里改卡正文（真实未提交改动）→ R2 命中 reviewed_at_missing。两处修复
// 都是真实计划层 → 落盘层写入，事实一律回读真实文件字节、intent.json、commit / abort 标记与
// git log，不看实现自报。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/reconcile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/txn"
)

// runReconcileEnvelope 跑一次 `eg reconcile …` 并把 --json 信封解析出来。
// 刻意不改既有的 runReconcileCLI（它只回文本，reconcile_test.go 的历史断言都靠它）；
// 这里额外把 stdout 的信封反序列化成 Envelope，并把原始信封字节一并回给调用方，
// 供「data.reconcile 恰三键 + 键序」这类需要保留键序的断言使用。
func runReconcileEnvelope(t *testing.T, r *Root, dir string, extra ...string) (int, Envelope, string, string) {
	t.Helper()
	code, out, errOut := runReconcileCLI(t, r, dir, extra...)
	var env Envelope
	if strings.TrimSpace(out) != "" {
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("--json 输出不可解析：%v\n%s", err, out)
		}
	}
	return code, env, out, errOut
}

// TestReconcileTxnCoversR2AndR6Repairs 钉死「R2 + R6 两处修复合并为一笔事务」的成功路径：
//
//	① 退出码按真实 findings 只可能是 0（仅 warning 级）或 2（若真有 error 级 finding），
//	   绝不可能是 3（无 B3 跳过）或 4（Git 未失败）；
//	② report.txn_id 非空且对应相对基线**唯一**新增的事务目录（A-59：有写入必开且恰一笔）；
//	③ intent.files[] 恰 2 条 = 被补过目的卡 + 被标失准的综述（原子域恰这两份权威文件）；
//	④ commit 标记在盘、abort 不在盘；
//	⑤ 卡**只补** reviewed_at（不长 stale 两键）、综述**只写** stale 两键（不长 reviewed_at）；
//	⑥ Git commit 恰 +1、工作区干净（R2/R6 的写 + 用户既有脏改动合并进恰一次纳管提交）；
//	⑦ data.reconcile 仍恰三键（键序即合同 §11）；
//	⑧ 步骤顺序 committed < git < index_sync < releasing，且 releasing 恰一次
//	   （S8 仍在锁内、早于 release；显式 release 之后的 defer 幂等，不多打一个 S9）。
func TestReconcileTxnCoversR2AndR6Repairs(t *testing.T) {
	dir, recapRel, cardRel, _ := r6Vault(t)

	// R6：事后更新引用卡（真实提交，工作区随后干净）→ 卡 updated_at 晚于综述 ⇒ 综述失准。
	r6BumpCard(t, dir, cardRel, r6CardBumpAt)
	// R2：直接在编辑器里改卡正文（未提交改动）⇒ 该卡缺过目信号，R2 命中 reviewed_at_missing。
	r2EditFile(t, dir, cardRel)

	// 基线在两处造数之后、reconcile 之前采集：事务增量与 Git 增量都相对这一刻算。
	base := txnIDsOn(t, dir)
	logBefore := gitLogCount(t, dir)

	rec := watchTxn(t, nil)
	// --user-request：R2 的 reviewed_at 补齐受写权限矩阵 #7/#22 管，只在用户显式发起时才写
	//（A-34 没有解锁 R2）；R6 的两键 A-34 已裁决不需要它。带上它两处修复才都会真正落盘。
	code, env, rawOut, errOut := runReconcileEnvelope(t, newTestRoot(t, dir), dir, "--user-request")

	// ① 退出码：真实 findings 决定 0 或 2；绝不允许 3（无 B3 跳过）/ 4（Git 未失败）。
	if code == ExitPartialWrite || code == ExitCommitFailed {
		t.Fatalf("退出码 = %d：本用例无 B3 跳过、Git 也未失败，绝不允许 3/4：%s", code, errOut)
	}
	if code != ExitOK && code != ExitValidation {
		t.Fatalf("退出码 = %d，期望 0 或 2（按真实 findings）：%s", code, errOut)
	}
	rep := applyReport(t, env)

	// ② report.txn_id 非空且对应唯一新增事务目录（有权威写入 ⇒ 必开且恰一笔）。
	if rep.TxnID == "" {
		t.Fatalf("report.txn_id 必须非空（A-59：成功分配的事务号一律交付）：%+v", rep)
	}
	id := onlyNewTxn(t, dir, base, "一次含 R2 + R6 修复写入的对账")
	if id != rep.TxnID {
		t.Fatalf("report.txn_id = %q，新增事务目录 = %q：两者必须恰好对上", rep.TxnID, id)
	}

	// ③ intent.files[] 恰 2 条 = 被补过目的卡 + 被标失准的综述。
	wantFiles := []string{cardRel, recapRel}
	sort.Strings(wantFiles)
	if paths := intentPathsOf(t, dir, id); !reflect.DeepEqual(paths, wantFiles) {
		t.Fatalf("intent.files[] = %v，期望恰含卡 %q 与综述 %q 两份（原子域恰这两处权威写）",
			paths, cardRel, recapRel)
	}

	// ④ commit 在盘、abort 不在盘。
	if !markerExists(dir, id, txn.CommitMarker) {
		t.Fatal("两处修复已提交，commit 标记必须在盘")
	}
	if markerExists(dir, id, txn.AbortMarker) {
		t.Fatal("已提交的事务不该有 abort 标记")
	}

	// ⑤ 逐维正交：卡只补 reviewed_at（不长 stale 两键）、综述只写 stale 两键（不长 reviewed_at）。
	cardAfter := string(mustRead(t, absIn(dir, cardRel)))
	if fmKeyLineOf(t, cardAfter, model.FMKeyReviewedAt) == "" {
		t.Fatalf("卡应补上 %s（R2 过目信号单键）：\n%s", model.FMKeyReviewedAt, cardAfter)
	}
	for _, key := range reconcile.RecapStaleKeys() {
		if fmKeyLineOf(t, cardAfter, key) != "" {
			t.Fatalf("卡长出了失准标记键 %s：R2 与 R6 维度正交，卡上一格都不该有", key)
		}
	}
	recapAfter := string(mustRead(t, absIn(dir, recapRel)))
	for _, key := range reconcile.RecapStaleKeys() {
		if fmKeyLineOf(t, recapAfter, key) == "" {
			t.Fatalf("综述缺失失准标记键 %s（R6 应写 stale 两键）：\n%s", key, recapAfter)
		}
	}
	if fmKeyLineOf(t, recapAfter, model.FMKeyReviewedAt) != "" {
		t.Fatalf("综述长出了 %s：过目信号只补在被编辑的卡上，综述一格都不该有", model.FMKeyReviewedAt)
	}

	// ⑥ Git 恰 +1、工作区干净（两处修复的写 + 用户既有脏改动合并进恰一次纳管提交）。
	if n := gitLogCount(t, dir); n != logBefore+1 {
		t.Fatalf("commit 数 %d → %d，期望恰 +1（R1 纳管 + R2/R6 修复合并为一次提交）", logBefore, n)
	}
	if got := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); got != "" {
		t.Fatalf("对账成功后工作区应干净，实得 %q", got)
	}

	// ⑦ data.reconcile 仍恰三键（键序即合同 §11，findings 恒非 null）。
	rcAssertReconcileShape(t, rcRawAt(t, []byte(rawOut), "data", "reconcile"))

	// ⑧ 步骤顺序：commit marker → Git → 索引同步 → 释放锁（S8 仍在锁内、早于 release）。
	assertStepOrder(t, rec, TxnStepCommitted, TxnStepGit, TxnStepIndexSync, TxnStepReleasing)
	if n := countTxnStep(rec, TxnStepReleasing); n != 1 {
		t.Fatalf("releasing 必须恰一次（显式 release + defer 幂等），实得 %d 次：%v", n, rec.names)
	}
}

// TestReconcileLockBusyReturnsE16WithoutHanging：
// 另一个持有者正占着 `.index/run.lock`，本次**非 dry-run** 对账（有真实可修复项）必须在
// **有限时间**内带 E16 阻断，而不是挂死，也不是绕开锁去写。照 proposal / delete / capture /
// mark-reviewed 的 lock busy 用例同套夹具与判据。
//
// 「有限时间」这一针针对同一个坑：全仓 CLI 用例都把业务时钟 Root.Now 钉成常量；若 Acquire
// 的等待判据误用它，elapsed 恒为 0 ⇒ 退避重试无限循环。所以钉住：等待走真实墙钟（80ms 上限、
// 10s 内必须返回），且收尾的 E16（= ErrLockUnavailable 的诊断映射，消息含「锁不可用」）随错误
// 一起交付。
//
// 另一半是「撞锁 = 什么都没做」：reconcile 的 S1 取锁在任何一次业务读写之前，撞锁时连锁内重采样
// 都没发生，因此库里那两处真实可修复项（R2 的卡 + R6 的综述）逐字节不变、事务目录集合零新增、
// 既有事务日志一字节没动、Git 也不动（commit 数与 HEAD 双判据）。
func TestReconcileLockBusyReturnsE16WithoutHanging(t *testing.T) {
	// 有实际可修复项的库：R6（事后更新引用卡令综述失准）+ R2（外部编辑卡缺过目信号）。
	// 若不撞锁，这次 --user-request 对账本会真的写这两处并提交；撞锁后必须一处都不写。
	dir, _, cardRel, _ := r6Vault(t)
	r6BumpCard(t, dir, cardRel, r6CardBumpAt)
	r2EditFile(t, dir, cardRel)

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
		// runReconcileCLI 内部把 Root.Now 钉成常量（reconcileFixedNow）：业务时钟固定，
		// 锁等待却必须照样走真实墙钟。非 dry-run + --user-request，两桥修复都会尝试写。
		code, env, _, output := runReconcileEnvelope(t, newTestRoot(t, dir), dir, "--user-request")
		done <- runOut{code: code, env: env, out: output}
	}()

	var got runOut
	select {
	case got = <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("锁被占用时 reconcile 在 10s 内没有返回：等待上限只有 %s=80ms，"+
			"说明 Acquire 拿到的是被钉死的业务时钟（elapsed 恒为 0 ⇒ 无限退避）",
			txn.LockTimeoutEnv)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("等待上限 80ms，实际耗时 %s：锁等待没有走真实墙钟", elapsed)
	}

	// ① 有限返回且按 M6/T-074 映射退 ExitPrecheckOrLock(5)：锁不可用 E16 经 classifyExit5
	//    归为 LockUnavailableError，在唯一翻译点 ExitCodeFor 落 5，绝不能被当成「对账成功了」。
	if got.code != ExitPrecheckOrLock {
		t.Fatalf("锁被占用必须按 M6 映射退 %d（E16 → 退出码 5），实得退出码 %d：%s",
			ExitPrecheckOrLock, got.code, got.out)
	}
	// ② 诊断：收尾的 E16（= errors.Is(err, txn.ErrLockUnavailable) 的诊断映射，即 lock_timeout）
	//    与消息里的「锁不可用」都必须随错误一起交付（不依赖「超时」字样）。
	diags := errorDiagsOf(t, got.env)
	if !diagsHaveCode(diags, txn.CodeLockTimeout) {
		t.Fatalf("锁等待超时必须携 %s（E16 / lock_timeout），实得 %+v", txn.CodeLockTimeout, diags)
	}
	if !hasSubstring(diagMessages(diags), "锁不可用") {
		t.Fatalf("诊断消息必须交代锁不可用（含「锁不可用」），实得 %v", diagMessages(diags))
	}
	// ③ 权威零写：那两处可修复项逐字节不变（撞锁在任何业务读写之前）。
	assertAuthorityUnchanged(t, dir, before, "reconcile 撞上锁")
	// ④ 事务侧：零新增事务目录（集合不变），既有事务日志逐字节不变。
	assertNoNewTxn(t, dir, base, "reconcile 撞上锁")
	assertTxnTreeUnchanged(t, dir, indexBefore, "reconcile 撞上锁")
	// ⑤ Git 侧：commit 数与 HEAD 双判据都不动。
	if n := gitLogCount(t, dir); n != logBefore {
		t.Fatalf("没拿到锁不得产生 commit：%d → %d", logBefore, n)
	}
	if h := gitOut(t, dir, "rev-parse", "HEAD"); h != headBefore {
		t.Fatalf("没拿到锁不得移动 HEAD：%q → %q", headBefore, h)
	}
}

// TestReconcileRecoverPrecedesFullResample：S2 崩溃恢复必须早于 S3 的锁内**全量重采样**。
//
// 这一针钉的是「对账在恢复之后才重新采样，而不是复用锁外/崩溃态快照」这个时序在业务事实上的
// 后果，判据全在磁盘与产物上，不看 hook 名字：
//
// 造现场——先用 reviewedVault + r2EditFile 得到「已被外部编辑、但从未过目（无 reviewed_at）」
// 的前像 pre；再借**真实** mark-reviewed 在较早的 crashAt 生成一个含 reviewed_at 的合法目标态
// target（= pre + reviewed_at@crashAt，并真实提交进 HEAD）。随后发布一笔 ExpectCommit 的
// **未闭合 update 事务**（PreBytes=pre、TargetBytes=target），把 target 落到盘上但既不写
// commit 也不写 abort —— 这正是合同 §6 的 P4 崩溃点。
//
// 关键对撞：崩溃态盘上已带 reviewed_at@crashAt。
//   - 若本次对账在恢复**之前**就采样（读到崩溃态），它会看见卡**已有** reviewed_at ⇒ R2 的
//     reviewed_at_missing 根本不命中 ⇒ 不修复 ⇒ 终态 reviewed_at 停在 crashAt；
//   - 只有先恢复（盘逐字回到无 reviewed_at 的前像 pre）、再全量重采样，R2 才会命中并把
//     **本次** Root.Now = reconcileFixedNow 写进去。
//
// 所以「终态 reviewed_at == reconcileFixedNow 且不是 crashAt」就是 S2 早于 S3 的硬证据：
// 取锁瞬间盘上还是崩溃态（带 crashAt）、恢复回调瞬间已逐字回到 pre（无 reviewed_at）、终态是
// 本次时刻、W26 一路留到最终 report、被恢复的旧事务写下 abort 且无 commit、本次不复用旧号且
// report.txn_id 与新增事务目录对上、Git 恰 +1。
func TestReconcileRecoverPrecedesFullResample(t *testing.T) {
	const crashAt = "2026-09-02T09:00:00+08:00"

	dir, cardRel, _ := reviewedVault(t)
	abs := absIn(dir, cardRel)

	// 前像：外部编辑过、但从未过目（没有 reviewed_at 行）。
	r2EditFile(t, dir, cardRel)
	pre := mustRead(t, abs)
	if strings.Contains(string(pre), model.FMKeyReviewedAt) {
		t.Fatalf("前置不成立：前像不该带 %s（本用例的起点是「已编辑但从未过目」）", model.FMKeyReviewedAt)
	}
	if !strings.Contains(string(pre), "用户在编辑器里手写的一行") {
		t.Fatal("前置不成立：前像里没有外部编辑痕迹，R2 的「未提交改动」条件立不住")
	}

	// 借真实 mark-reviewed 在较早的 crashAt 生成合法目标态（= 前像 + reviewed_at@crashAt），
	// 避免用例自己拼 frontmatter；这一步会真实提交，HEAD 因此带上崩溃时刻的过目信号。
	if code, out := runMarkReviewedCLI(t, dir, crashAt, "--target", applyCardID); code != ExitOK {
		t.Fatalf("前置 mark-reviewed 退出码 = %d：%s", code, out)
	}
	target := mustRead(t, abs)
	if !strings.Contains(string(target), crashAt) {
		t.Fatalf("夹具失效：目标态里没有崩溃时刻 %s：\n%s", crashAt, target)
	}

	// 发布一笔未闭合的 update 事务：intent 已写、目标态在盘、commit / abort 皆缺席。
	stale, err := txn.AllocateTxnID(dir)
	if err != nil {
		t.Fatalf("分配 txn_id 失败：%v", err)
	}
	now := func() time.Time { return stampAt(t, crashAt) }
	if _, err := txn.WriteIntent(dir, stale, txn.IntentInput{
		Argv: []string{"eg", "reconcile"}, ExpectCommit: true, Now: now,
		Files: []txn.FileSpec{{
			Path: cardRel, PreBytes: pre, TargetBytes: target, TargetOp: "update",
		}},
	}); err != nil {
		t.Fatalf("发布 intent 失败：%v", err)
	}
	if err := os.WriteFile(abs, target, 0o644); err != nil {
		t.Fatalf("写崩溃态失败：%v", err)
	}

	// 基线在崩溃事务构造之后、本次对账之前采集：事务增量与 Git 增量都相对这一刻算。
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

	// Root.Now = reconcileFixedNow：R2 补齐的过目时刻因此可逐字断言。--user-request 让 R2
	// 的 reviewed_at 补齐得到写权限矩阵 #7/#22 的放行，恢复之后的全量重采样才会真正落盘。
	r := newTestRoot(t, dir)
	r.Now = func() time.Time { return stampAt(t, reconcileFixedNow) }
	code, env, _, errOut := runReconcileEnvelope(t, r, dir, "--user-request")

	// 退出码：真实 findings 决定 0 或 2；绝不允许 3（无 B3 跳过）/ 4（Git 未失败）。
	if code == ExitPartialWrite || code == ExitCommitFailed {
		t.Fatalf("退出码 = %d：本用例无 B3 跳过、Git 也未失败，绝不允许 3/4：%s", code, errOut)
	}
	if code != ExitOK && code != ExitValidation {
		t.Fatalf("退出码 = %d，期望 0 或 2（恢复成功后照常修复）：%s", code, errOut)
	}

	// ① 取锁瞬间盘上还是崩溃目标态（带 crashAt 的 reviewed_at）——目标态确实在盘。
	if !strings.Contains(atAcquire, crashAt) {
		t.Fatal("取锁瞬间盘上就不是崩溃态：夹具没生效，本用例什么都没证明")
	}
	if !strings.Contains(atAcquire, model.FMKeyReviewedAt) {
		t.Fatalf("取锁瞬间盘上缺 %s：崩溃目标态没落盘", model.FMKeyReviewedAt)
	}
	// ② 恢复回调瞬间已**逐字**回到前像 pre，且不含 reviewed_at（S3 若在此之后重采样，
	//    才会把这张卡判成 reviewed_at_missing）。
	if atRecovered != string(pre) {
		t.Fatal("恢复节点回调时目标未逐字回到前像：S2 没有真正回滚")
	}
	if strings.Contains(atRecovered, model.FMKeyReviewedAt) {
		t.Fatalf("恢复之后目标仍带 %s：重采样将拿到崩溃事务的过目信号，R2 不会命中",
			model.FMKeyReviewedAt)
	}

	// ③ 时序的业务证据：终态 reviewed_at 恰是**本次** reconcileFixedNow，不是 crashAt。
	//    这只可能来自「先恢复到无 reviewed_at 的前像 → S3 全量重采样命中 R2 → 补本次时刻」；
	//    若重采样早于恢复（读到崩溃态已带 reviewed_at），R2 根本不命中，终态会停在 crashAt。
	after := string(mustRead(t, abs))
	line := fmKeyLineOf(t, after, model.FMKeyReviewedAt)
	if !strings.Contains(line, reconcileFixedNow) {
		t.Fatalf("%s 行 = %q，期望值为本次 Root.Now = %s（读早于恢复则 R2 不命中，终态会停在崩溃值）",
			model.FMKeyReviewedAt, line, reconcileFixedNow)
	}
	if strings.Contains(line, crashAt) {
		t.Fatalf("%s 行 = %q 仍是崩溃事务的时刻 %s：S3 重采样读在 S2 恢复之前",
			model.FMKeyReviewedAt, line, crashAt)
	}
	if n := strings.Count(after, "\n"+model.FMKeyReviewedAt+":"); n != 1 {
		t.Fatalf("%s 出现 %d 行，期望恰 1 行（单键覆盖）", model.FMKeyReviewedAt, n)
	}

	// ④ W26 必须保留在**最终 report** 里（真实回滚才有这条）。
	if !contains(undReportWarnCodes(t, env), txn.CodeTxnRecovered) {
		t.Fatalf("真实回滚必须在最终 report.warnings[] 留一条 %s，实得 %v",
			txn.CodeTxnRecovered, undReportWarnCodes(t, env))
	}

	// ⑤ 被恢复的那笔已写下 abort、且无 commit，本次不复用它的号码。
	if !markerExists(dir, stale, txn.AbortMarker) {
		t.Fatal("被回滚的未闭合事务必须写下 abort 标记")
	}
	if markerExists(dir, stale, txn.CommitMarker) {
		t.Fatal("被回滚的未闭合事务不该有 commit 标记")
	}
	id := onlyNewTxn(t, dir, base, "恢复之后的一次真实对账")
	if id == stale {
		t.Fatalf("本次不得复用被回滚的事务号 %q", stale)
	}
	if rep := applyReport(t, env); rep.TxnID != id {
		t.Fatalf("report.txn_id = %q，新增事务目录 = %q：两者必须恰好对上", rep.TxnID, id)
	}

	// ⑥ 恢复不吃掉本次写入：Git 恰多一个 commit（R1 纳管 + R2 补齐合并进一次提交）。
	if n := gitLogCount(t, dir); n != logBefore+1 {
		t.Fatalf("commit 数 %d → %d，期望恰 +1（恢复之后本次修复照常提交）", logBefore, n)
	}
}

// TestReconcileCommitRollbackRestoresR2AndR6：合同 §5.2 —— 对账 S6 提交期普通 I/O 失败属
// **主动放弃**（已 stage 的 R2 + R6 overlay 整体回滚），退 3（ExitPartialWrite），不是退 1。
//
// 前置与 TestReconcileTxnCoversR2AndR6Repairs 完全同构：r6BumpCard 令综述失准（R6 命中
// recap_stale 两键）、r2EditFile 令卡缺过目信号（R2 命中 reviewed_at_missing）。成功路径本会
// 把这两处写进**同一笔**事务并提交；这里在 TxnStepIntent 把 commit 标记名占成非空目录，令 S6
// 发布 commit marker 的 rename 必失败，txn 层随即把两个目标逐个还原成前像、写下 abort。
//
// 逐条钉死：① 退 3；② 完整权威快照逐字回到前像 —— 卡不长 reviewed_at、综述不长 stale 两键、
// 用户原始外部编辑仍逐字保留（前像 = 用户编辑态）；③ 唯一新事务写下 abort、无 commit，
// report.txn_id 与之对上（A-59 号码照常交付）；④ Git 与 S8 一步没跑、Git commit 数与 HEAD
// 纹丝不动；⑤ 两条 write-set 路径各交代一条「目标未写入」（恰 2 条，不多不少）；
// ⑥ 失败信封的 data.reconcile 仍恰三键（键序即合同 §11）。
func TestReconcileCommitRollbackRestoresR2AndR6(t *testing.T) {
	dir, recapRel, cardRel, _ := r6Vault(t)

	// R6：事后更新引用卡（真实提交，工作区随后干净）→ 综述失准。
	r6BumpCard(t, dir, cardRel, r6CardBumpAt)
	// R2：直接在编辑器里改卡正文（未提交改动）→ 该卡缺过目信号。
	r2EditFile(t, dir, cardRel)

	// 采完整权威 / txn / Git 基线（都在两处造数之后、reconcile 之前）。
	before := authoritySnapshot(t, dir)
	base := txnIDsOn(t, dir)
	logBefore := gitLogCount(t, dir)
	headBefore := gitOut(t, dir, "rev-parse", "HEAD")

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

	code, env, rawOut, errOut := runReconcileEnvelope(t, newTestRoot(t, dir), dir, "--user-request")
	if code != ExitPartialWrite {
		t.Fatalf("退出码 = %d，期望 3（提交失败已整体回滚，属主动放弃而非基础设施崩坏）：%s",
			code, errOut)
	}
	if blocked == "" {
		t.Fatal("没有触达 intent 节点：本用例什么都没证明")
	}
	_ = os.RemoveAll(blocked)

	// ① 完整权威快照逐字回到前像。
	assertAuthorityUnchanged(t, dir, before, "reconcile 提交失败并回滚后")
	// ①' 逐维显式复核：卡不长 reviewed_at、用户原始外部编辑仍在；综述不长 stale 两键。
	cardAfter := string(mustRead(t, absIn(dir, cardRel)))
	if fmKeyLineOf(t, cardAfter, model.FMKeyReviewedAt) != "" {
		t.Fatalf("回滚后卡不得留下 %s（R2 的写已随事务整体回滚）：\n%s", model.FMKeyReviewedAt, cardAfter)
	}
	if !strings.Contains(cardAfter, "用户在编辑器里手写的一行") {
		t.Fatal("回滚后必须保留用户的原始外部编辑：前像 = 用户编辑态，不是更早的干净态")
	}
	recapAfter := string(mustRead(t, absIn(dir, recapRel)))
	for _, key := range reconcile.RecapStaleKeys() {
		if fmKeyLineOf(t, recapAfter, key) != "" {
			t.Fatalf("回滚后综述不得新增失准标记键 %s（R6 的写已随事务整体回滚）：\n%s", key, recapAfter)
		}
	}

	// ② 事务侧：唯一新事务写下 abort、无 commit。
	id := onlyNewTxn(t, dir, base, "对账提交失败并回滚")
	if !markerExists(dir, id, txn.AbortMarker) {
		t.Fatal("回滚完成后必须写下 abort 标记")
	}
	if markerExists(dir, id, txn.CommitMarker) {
		t.Fatal("没提交成功却有 commit 标记")
	}
	// ③ 号码保留（A-59），且恰是 abort 目录名。
	rep := applyReport(t, env)
	if rep.TxnID != id {
		t.Fatalf("report.txn_id = %q，abort 日志目录 = %q：主动回滚的事务号必须交付且两者相等",
			rep.TxnID, id)
	}

	// ④ Git 与 S8 一步没跑（Markdown 根本没生效，谈不上「写后」）；Git commit 数与 HEAD 都不动。
	if rec.at(TxnStepGit) >= 0 || rec.at(TxnStepIndexSync) >= 0 {
		t.Fatalf("提交回滚后不得跑 Git / S8，实际序列 %v", rec.names)
	}
	if n := gitLogCount(t, dir); n != logBefore {
		t.Fatalf("提交失败不得产生 commit：%d → %d", logBefore, n)
	}
	if h := gitOut(t, dir, "rev-parse", "HEAD"); h != headBefore {
		t.Fatalf("提交失败不得移动 HEAD：%q → %q", headBefore, h)
	}

	// ⑤ 逐路径交代「目标未写入」：write-set 恰两份（被补过目的卡 + 被标失准的综述），
	//    每条路径各一条，且总数恰 2（不多不少）。
	for _, p := range []string{cardRel, recapRel} {
		n := 0
		for _, w := range rep.Warnings {
			if w.Path == p && strings.Contains(w.Message, "目标未写入") {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("write-set 路径 %q 必须恰一条「目标未写入」warning，实得 %d 条：%+v",
				p, n, rep.Warnings)
		}
	}
	var unwritten int
	for _, w := range rep.Warnings {
		if strings.Contains(w.Message, "目标未写入") {
			unwritten++
		}
	}
	if unwritten != 2 {
		t.Fatalf("write-set 恰两份，「目标未写入」必须恰 2 条，实得 %d 条：%+v", unwritten, rep.Warnings)
	}

	// ⑥ 失败信封的 data.reconcile 仍恰三键（键序即合同 §11，findings 恒非 null）。
	rcAssertReconcileShape(t, rcRawAt(t, []byte(rawOut), "data", "reconcile"))
}

// TestReconcileGitFailureKeepsR2AndR6TargetsWithoutSecondWrite：对账 S7 Git（R1 纳管提交）
// 失败 ⇒ 退 4（ExitCommitFailed），且**不回滚、不做第二次权威写**，仍要走完 S8。
//
// 前置与 TestReconcileTxnCoversR2AndR6Repairs 完全同构：r6BumpCard 令综述失准（R6 命中
// recap_stale 两键）、r2EditFile 令卡缺过目信号（R2 命中 reviewed_at_missing）。这两处修复在
// S6 已随 commit marker 落盘生效；随后注入 failingCommitRepo —— 只有 `git commit` 这一步失败，
// add / status / log 全走真实 git，模拟「纳管提交在 Git 层失败」这一外部事实。
//
// 逐条钉死：① 退 4；② TxnStepGit 瞬间的权威快照与**最终**逐字相同（Git 之后没有第二次权威
// 写），卡的 reviewed_at 目标态与综述的 stale 两键目标态都保留（Git 失败不回滚 Markdown）；
// ③ 失败支同样走完 S8 且仍在同一把锁内：committed < git < index_sync < releasing；
// ④ 恰一个新事务、commit marker 在盘、无 abort，report.txn_id 与之对上（绝不开第二个事务）；
// ⑤ Git commit 数纹丝不动；⑥ data.reconcile 恰三键且 commit = null（无 sha），错误诊断含 Git。
func TestReconcileGitFailureKeepsR2AndR6TargetsWithoutSecondWrite(t *testing.T) {
	dir, recapRel, cardRel, _ := r6Vault(t)

	// R6：事后更新引用卡（真实提交，工作区随后干净）→ 综述失准。
	r6BumpCard(t, dir, cardRel, r6CardBumpAt)
	// R2：直接在编辑器里改卡正文（未提交改动）→ 该卡缺过目信号。
	r2EditFile(t, dir, cardRel)

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
	// Root.Now = reconcileFixedNow：R2 补齐的过目时刻因此可逐字断言（newTestRoot 默认取真实
	// 墙钟，会盖过 runReconcileCLI 的「nil 才兜底」分支，故这里显式钉死）。
	r.Now = func() time.Time { return stampAt(t, reconcileFixedNow) }
	// mock（owner 2026-09-07 授权）：注入一个「只有 git commit 这一步失败」的 Runner
	// （failingCommitRepo → git.NewWithRunner，add / status / log 等**全部走真实 git**）。
	r.NewRepo = failingCommitRepo()
	code, env, rawOut, errOut := runReconcileEnvelope(t, r, dir, "--user-request")
	if code != ExitCommitFailed {
		t.Fatalf("退出码 = %d，期望 4：%s", code, errOut)
	}
	if atGit == nil {
		t.Fatal("没有触达 Git 节点：TxnStepGit 在失败支没有触发，本用例什么都没证明")
	}

	// ① Git 尝试后到最终逐字相同（Git 之后没有第二次权威写）。
	assertAuthorityUnchanged(t, dir, atGit, "reconcile 的 Git 失败后")
	// ①' R2/R6 目标态确实保留：卡带本次 reviewed_at、综述带 stale 两键（Git 失败不回滚 Markdown）。
	cardAfter := string(mustRead(t, absIn(dir, cardRel)))
	if line := fmKeyLineOf(t, cardAfter, model.FMKeyReviewedAt); !strings.Contains(line, reconcileFixedNow) {
		t.Fatalf("Git 失败不得回滚 R2 的写，%s 行 = %q，期望含本次时刻 %s",
			model.FMKeyReviewedAt, line, reconcileFixedNow)
	}
	recapAfter := string(mustRead(t, absIn(dir, recapRel)))
	for _, key := range reconcile.RecapStaleKeys() {
		if fmKeyLineOf(t, recapAfter, key) == "" {
			t.Fatalf("Git 失败不得回滚 R6 的写，综述缺失准标记键 %s：\n%s", key, recapAfter)
		}
	}

	// ② 失败支同样走完 S8，且必须仍在**同一把锁内**（index_sync 夹在 S7 与 S9 之间）。
	assertStepOrder(t, rec, TxnStepCommitted, TxnStepGit, TxnStepIndexSync, TxnStepReleasing)

	// ③ 事务侧：恰一个新事务、commit 标记在盘、无 abort，report.txn_id 与之对上。
	id := onlyNewTxn(t, dir, base, "Git 失败")
	rep := applyReport(t, env)
	if rep.TxnID != id {
		t.Fatalf("report.txn_id = %q 与新增事务目录 %q 对不上（Git 失败绝不开第二个事务）", rep.TxnID, id)
	}
	if !markerExists(dir, id, txn.CommitMarker) {
		t.Fatal("Markdown 事务已提交，commit 标记必须在盘")
	}
	if markerExists(dir, id, txn.AbortMarker) {
		t.Fatal("Git 失败不是 Markdown 事务失败，不该有 abort 标记")
	}

	// ④ Git 侧：没有新 commit。
	if n := gitLogCount(t, dir); n != logBefore {
		t.Fatalf("Git 失败不得产生 commit：%d → %d", logBefore, n)
	}

	// ⑤ data.reconcile 恰三键且 commit = null（Git 未成功，无 sha）。
	rcAssertReconcileShape(t, rcRawAt(t, []byte(rawOut), "data", "reconcile"))
	if got := strings.TrimSpace(string(rcRawAt(t, []byte(rawOut), "data", "reconcile", "commit"))); got != "null" {
		t.Fatalf("Git 失败时 data.reconcile.commit 必须为 null，实得 %q", got)
	}

	// ⑥ 错误诊断必须交代 Git 失败。
	if !hasSubstring(diagMessages(errorDiagsOf(t, env)), "Git") {
		t.Fatalf("Git 失败明细必须交付：%+v", errorDiagsOf(t, env))
	}
}
