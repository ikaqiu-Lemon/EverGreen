package cli

// mark_reviewed_transaction_test.go —— M6 · T-…-072 批次 C2b：`eg mark-reviewed` 事务化的
// 聚焦反证。被测对象是 mark_reviewed.go 里的临界区 markReviewedCritical，它把「锁外扫库 +
// 直落实盘 + 事后 Git」的旧写口，改成与 plan 写链 / capture / undelete **同一把锁、同一套
// 顺序**的 A 类事务（S1 取锁 → S2 恢复 → S3 锁内重读 → S4 预演 → S5 intent → S6 提交 →
// S7 Git → S8 写后索引同步 → S9 释放锁）。
//
// 本文件钉这条时序**在 mark-reviewed 上**的可观察后果：恢复屏障必须真的改变「上一次过目
// 时刻」这个业务事实并被锁内重读吃到（W26 一路保留到最终 report）、锁拿不到就有限阻断且
// 零权威写、一次标记恰一个事务且 intent 只含目标、reviewed_at 单键不变量在事务化之后逐字
// 不变、Git 失败保留目标态且无第二次权威写、S6 回滚是退 3 的主动放弃。
//
// 「committed < git < index_sync < releasing + healthy 索引收敛」那一组要直接读索引库，
// 按命令层的文件级位置锁（cmd/eg/arch_test.go 的 TestStage4IndexPackageBoundary ⑤）住在
// index_after_write_mark_reviewed_test.go。
//
// 【为什么没有「同 stamp ⇒ 零 write-set」那一例】
// store 的预演层按「**是否发生过权威写**」而不是「字节是否变化」登记 accepted write-set
// （internal/store/atomic.go 的 stage：written=true 即进 order，不比对 staged 与前像）。
// 因此同一时刻重复标记仍会产出一个非空 write-set，「零 write-set」在现有语义下构造不出来。
// 命令侧的 `len(ws) == 0` 分支照合同保留（不开 txn / 不 Git / 不 S8），但本批次**不**为了
// 造这条用例去改 store 的 write-set 语义 —— 那是另一个改动面，且会波及全部写命令。
//
// 观测手段沿用 recover_hook_test.go 的 txnOrderHook 与那一组磁盘取证辅助，不另造第二套口径。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/txn"
)

// —— 辅助 ——

// runMarkReviewedWith 用调用方给定的 Root 跑一次 `eg mark-reviewed`（用于注入 NewRepo 等接缝），
// 并把 --json 信封解析出来。
//
// 刻意不改既有的 runMarkReviewedCLI：那个 helper 自己 new Root、只回文本，四组历史断言都靠它。
func runMarkReviewedWith(t *testing.T, r *Root, dir, at string, args ...string) (int, Envelope, string) {
	t.Helper()
	setGitIdentity(t)
	r.Now = func() time.Time { return stampAt(t, at) }
	r.In = closedStdin{}
	full := append([]string{"mark-reviewed"}, args...)
	full = append(full, "--vault", dir, "--json")
	code, out, errOut := runCLI(t, r, full...)
	var env Envelope
	if strings.TrimSpace(out) != "" {
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("--json 输出不可解析：%v\n%s", err, out)
		}
	}
	return code, env, out + errOut
}

// mrReviewedLine 取目标文件里 `reviewed_at:` 那一整行（不存在返回空串）。
func mrReviewedLine(t *testing.T, dir, rel string) string {
	t.Helper()
	return fmKeyLineOf(t, string(mustRead(t, absIn(dir, rel))), model.FMKeyReviewedAt)
}

// —— ① S2 崩溃恢复屏障必须早于 S3 的目标重读 ——

// TestMarkReviewedRecoverPrecedesTargetReread：
// 用一笔**真实的未闭合事务**造现场：intent 已发布、权威文件已被 rename 成「reviewed_at 已
// 写上」的目标态、commit / abort 皆缺席（合同 §6 的 P4 崩溃点）。
//
// 这个现场把「上一次过目是什么时候」这个**业务事实**整个翻了过来：
//   - 若本次 mark-reviewed 在恢复之前就读目标（旧实现在锁外读），B3 守卫基准就取自那笔未闭合
//     事务的目标态字节；恢复一旦发生，该 hash 立刻变脏，守卫写当场判 SkipFileChanged —— 命令
//     要么零写入、要么把崩溃态的 reviewed_at 当成既有值留在盘上；
//   - 只有先恢复、后重读，才会拿到回滚后的前像（从未过目）与正确的 hash，把本次时刻写进去。
//
// 所以顺序的判据是**磁盘与产物上的业务事实**，不是 hook 名字：恢复回调瞬间目标已回到无
// reviewed_at 的前像、终态 reviewed_at 恰是本次 Root.Now（不是崩溃值）、Git 多出一个 commit、
// W26 留在最终 report。摘要文案不作判据 —— --json 信封不渲染 Summary。
func TestMarkReviewedRecoverPrecedesTargetReread(t *testing.T) {
	const crashAt = "2026-09-02T09:00:00+08:00"
	const markAt = "2026-09-04T09:00:00+08:00"

	dir, cardRel, _ := reviewedVault(t)
	abs := absIn(dir, cardRel)
	pre := mustRead(t, abs) // 前像：从未过目（没有 reviewed_at 行）

	// 借真实命令生成「崩溃事务的目标态」字节，避免用例自己拼 frontmatter。
	if code, out := runMarkReviewedCLI(t, dir, crashAt, "--target", applyCardID); code != ExitOK {
		t.Fatalf("前置 mark-reviewed 退出码 = %d：%s", code, out)
	}
	target := mustRead(t, abs)
	if !strings.Contains(string(target), crashAt) {
		t.Fatalf("夹具失效：目标态里没有崩溃时刻 %s：\n%s", crashAt, target)
	}

	stale, err := txn.AllocateTxnID(dir)
	if err != nil {
		t.Fatalf("分配 txn_id 失败：%v", err)
	}
	now := func() time.Time { return stampAt(t, crashAt) }
	if _, err := txn.WriteIntent(dir, stale, txn.IntentInput{
		Argv: []string{"eg", "mark-reviewed"}, ExpectCommit: true, Now: now,
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

	code, env, output := runMarkReviewedWith(t, newTestRoot(t, dir), dir, markAt,
		"--target", applyCardID)
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0（恢复成功后照常标记）：%s", code, output)
	}

	// ① 现场取证：取锁瞬间盘上还是崩溃态，恢复回调瞬间已回到「无 reviewed_at」的前像。
	if !strings.Contains(atAcquire, crashAt) {
		t.Fatal("取锁瞬间盘上就不是崩溃态：夹具没生效，本用例什么都没证明")
	}
	if atRecovered != string(pre) {
		t.Fatal("恢复节点回调时目标仍未回到前像：S2 没有真正回滚")
	}
	if strings.Contains(atRecovered, model.FMKeyReviewedAt) {
		t.Fatalf("恢复之后目标仍带 %s：重读将拿到崩溃事务的事实", model.FMKeyReviewedAt)
	}
	// ② 顺序的业务证据：守卫写用的是**恢复之后**的前像与 hash —— 终态是本次时刻，
	//    既不是崩溃值，也没有因脏 hash 被判 SkipFileChanged 而零写入。
	got := mrReviewedLine(t, dir, cardRel)
	if !strings.Contains(got, markAt) {
		t.Fatalf("%s 行 = %q，期望值为本次 Root.Now = %s（重读若早于恢复，守卫基准变脏必然写不进去）",
			model.FMKeyReviewedAt, got, markAt)
	}
	if strings.Contains(got, crashAt) {
		t.Fatalf("%s 行 = %q 仍是崩溃事务的时刻 %s：S3 读在 S2 之前",
			model.FMKeyReviewedAt, got, crashAt)
	}
	if n := strings.Count(string(mustRead(t, abs)), "\n"+model.FMKeyReviewedAt+":"); n != 1 {
		t.Fatalf("%s 出现 %d 行，期望恰 1 行（单键覆盖）", model.FMKeyReviewedAt, n)
	}
	// ③ 恢复不吃掉本次写入：Git 恰多一个 commit。
	if n := gitLogCount(t, dir); n != logBefore+1 {
		t.Fatalf("commit 数 %d → %d，期望恰 +1（恢复之后本次标记照常提交）", logBefore, n)
	}
	// ④ W26 必须保留在**最终 report** 里（真实回滚才有这条）。
	if !contains(undReportWarnCodes(t, env), txn.CodeTxnRecovered) {
		t.Fatalf("真实回滚必须在最终 report.warnings[] 留一条 %s，实得 %v",
			txn.CodeTxnRecovered, undReportWarnCodes(t, env))
	}
	// ⑤ 被恢复的那笔已写下 abort，且本次不复用它的号码。
	if !markerExists(dir, stale, txn.AbortMarker) {
		t.Fatal("被回滚的未闭合事务必须写下 abort 标记")
	}
	id := onlyNewTxn(t, dir, base, "恢复之后的一次真实 mark-reviewed")
	if id == stale {
		t.Fatalf("本次不得复用被回滚的事务号 %q", stale)
	}
	if got := undReportTxnID(t, env); got != id {
		t.Fatalf("report.txn_id = %q，新增事务目录 = %q：两者必须恰好对上", got, id)
	}
}

// —— ② 锁被占住：有限时间内退 E16，权威零写 ——

func TestMarkReviewedLockBusyReturnsE16WithoutHanging(t *testing.T) {
	dir, _, _ := reviewedVault(t)
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
		code, env, output := runMarkReviewedWith(t, newTestRoot(t, dir), dir,
			"2026-09-04T09:00:00+08:00", "--target", applyCardID)
		done <- runOut{code: code, env: env, out: output}
	}()

	var got runOut
	select {
	case got = <-done:
	case <-time.After(20 * time.Second):
		t.Fatalf("锁被占用时 mark-reviewed 在 20s 内没有返回：等待上限只有 %s=80ms，"+
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
	assertLockWaitBuckets(t, got.env, "mark-reviewed 撞上锁")
	assertAuthorityUnchanged(t, dir, before, "mark-reviewed 撞上锁")
	assertNoNewTxn(t, dir, base, "mark-reviewed 撞上锁")
	if n := gitLogCount(t, dir); n != logBefore {
		t.Fatalf("没拿到锁不得产生 commit：%d → %d", logBefore, n)
	}
}

// —— ③ 成功路径：一个事务、intent 只含目标、txn_id 对账、reviewed_at 单键不变量 ——

// TestMarkReviewedTxnCoversOnlyTargetAndKeepsSingleKey：
// 事务化**不得**扩大写面。write-set 与 intent 都必须恰含目标一个文件；文件内部同样只多出
// reviewed_at 一行，status / updated_at 逐字不变、删除维度一格不长、别的产物一个字节不动。
func TestMarkReviewedTxnCoversOnlyTargetAndKeepsSingleKey(t *testing.T) {
	const markAt = "2026-09-04T09:00:00+08:00"
	dir, cardRel, noteRel := reviewedVault(t)

	beforeRaw := string(mustRead(t, absIn(dir, cardRel)))
	statusBefore := fmKeyLineOf(t, beforeRaw, "status")
	updatedBefore := fmKeyLineOf(t, beforeRaw, "updated_at")
	if updatedBefore == "" || statusBefore == "" {
		t.Fatal("前置不成立：目标缺 status / updated_at 行，本用例失去判据")
	}
	noteBefore := string(mustRead(t, absIn(dir, noteRel)))
	base := txnIDsOn(t, dir)

	code, env, output := runMarkReviewedWith(t, newTestRoot(t, dir), dir, markAt,
		"--target", applyCardID)
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0：%s", code, output)
	}

	// ① 一个事务、一份 intent、恰一个目标文件。
	id := onlyNewTxn(t, dir, base, "一次成功的 mark-reviewed")
	if got := intentPathsOf(t, dir, id); strings.Join(got, ",") != cardRel {
		t.Fatalf("intent.files[] = %v，期望恰含 %s（标记已过目只动目标一个文件）", got, cardRel)
	}
	// ② report.txn_id 与事务日志同真（A-59），且事务真的提交了。
	if got := undReportTxnID(t, env); got != id {
		t.Fatalf("report.txn_id = %q，新增事务目录 = %q：两者必须恰好对上", got, id)
	}
	if !markerExists(dir, id, txn.CommitMarker) {
		t.Fatal("标记成功，commit 标记必须在盘")
	}
	if markerExists(dir, id, txn.AbortMarker) {
		t.Fatal("已提交的事务不该有 abort 标记")
	}
	// ③ 单键不变量（事务化之后逐字仍成立）。
	after := string(mustRead(t, absIn(dir, cardRel)))
	if n := strings.Count(after, "\n"+model.FMKeyReviewedAt+":"); n != 1 {
		t.Fatalf("%s 出现 %d 行，期望恰 1 行：\n%s", model.FMKeyReviewedAt, n, after)
	}
	if !strings.Contains(after, markAt) {
		t.Fatalf("%s 的值不是本次时刻 %s：\n%s", model.FMKeyReviewedAt, markAt, after)
	}
	if got := fmKeyLineOf(t, after, "status"); got != statusBefore {
		t.Fatalf("status 行 = %q，期望逐字仍是 %q", got, statusBefore)
	}
	if got := fmKeyLineOf(t, after, "updated_at"); got != updatedBefore {
		t.Fatalf("updated_at 行 = %q，期望逐字仍是 %q（标记已过目绝不更新它）", got, updatedBefore)
	}
	for _, key := range []string{"deleted_at:", "deleted_reason:"} {
		if strings.Contains(after, key) {
			t.Fatalf("标记已过目长出了 %s：删除维度与过目维度正交", key)
		}
	}
	if got := string(mustRead(t, absIn(dir, noteRel))); got != noteBefore {
		t.Fatalf("%s 的字节发生变化：事务不得把别的产物卷进 write-set", noteRel)
	}
}

// —— ④ S7 Git 失败：退 4、不回滚、不做第二次权威写，且仍要走完 S8 ——

func TestMarkReviewedGitFailureKeepsTargetStateWithoutSecondWrite(t *testing.T) {
	const markAt = "2026-09-04T09:00:00+08:00"
	dir, cardRel, _ := reviewedVault(t)
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
	code, env, output := runMarkReviewedWith(t, r, dir, markAt, "--target", applyCardID)
	if code != ExitCommitFailed {
		t.Fatalf("退出码 = %d，期望 4：%s", code, output)
	}
	if atGit == nil {
		t.Fatal("没有触达 Git 节点：TxnStepGit 在失败支没有触发，本用例什么都没证明")
	}

	// ① Markdown 已生效并保持目标态：reviewed_at 仍是本次时刻（不回滚）。
	if got := mrReviewedLine(t, dir, cardRel); !strings.Contains(got, markAt) {
		t.Fatalf("Git 失败不得回滚 Markdown，%s 行 = %q", model.FMKeyReviewedAt, got)
	}
	// ② Git 之后没有第二次权威写。
	assertAuthorityUnchanged(t, dir, atGit, "mark-reviewed 的 Git 失败后")
	// ②' 失败支同样要走完 S8，且必须仍在**同一把锁内**（index_sync 夹在 S7 与 S9 之间）。
	assertStepOrder(t, rec, TxnStepCommitted, TxnStepGit, TxnStepIndexSync, TxnStepReleasing)
	// ③ 事务侧：恰一个事务、commit 标记在盘、没有 abort。
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
	if !hasSubstring(diagMessages(errorDiagsOf(t, env)), "Git") {
		t.Fatalf("Git 失败明细必须交付：%+v", errorDiagsOf(t, env))
	}
}

// —— ⑤ S6 提交期普通 I/O 失败 ⇒ 合同 §5.2 主动放弃（退 3，不是退 1）——

func TestMarkReviewedCommitRollbackIsPartialWriteNotBlocked(t *testing.T) {
	const markAt = "2026-09-04T09:00:00+08:00"
	dir, cardRel, _ := reviewedVault(t)
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

	code, env, output := runMarkReviewedWith(t, newTestRoot(t, dir), dir, markAt,
		"--target", applyCardID)
	if code != ExitPartialWrite {
		t.Fatalf("退出码 = %d，期望 3（提交失败已整体回滚，属主动放弃而非基础设施崩坏）：%s",
			code, output)
	}
	if blocked == "" {
		t.Fatal("没有触达 intent 节点：本用例什么都没证明")
	}
	_ = os.RemoveAll(blocked)

	// ① 权威内容回到事务开始前：reviewed_at 根本没长出来。
	assertAuthorityUnchanged(t, dir, before, "mark-reviewed 提交失败并回滚后")
	if got := mrReviewedLine(t, dir, cardRel); got != "" {
		t.Fatalf("回滚之后不得留下 %s 行，实得 %q", model.FMKeyReviewedAt, got)
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
