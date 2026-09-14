package cli

// capture_transaction_test.go —— M6 · T-…-072 批次 C1：`eg capture` 事务化的聚焦反证。
//
// 被测对象是 capture.go 里的临界区 captureCritical。它把「直落实盘 + 事后 Git」的收录
// 写口，改成与 plan 写链**同一把锁、同一套顺序**的 A 类事务：
//
//	S0 锁外解析 → S1 取锁 → S2 崩溃恢复 → S3 锁内判重/读笔记 → S4 原子预演 →
//	S5 intent 屏障 → S6 原子提交 → S7 Git → S9 释放锁
//
// 本文件钉的是这条时序**在 capture 上**的可观察后果，逐条对着合同的硬边界：
// 一次收录 = 一个事务（原文与收件区同一份 intent）、W26 必须早于判重、锁拿不到就有限
// 阻断、零 accepted write-set 不开事务也不跑 Git、txn_id 与事务日志同真、Git 失败不回滚、
// 提交期回滚是退 3 的主动放弃。
//
// 观测手段沿用 recover_hook_test.go 的 txnOrderHook 与那一组磁盘取证辅助（authoritySnapshot /
// onlyNewTxn / intentPathsOf / markerExists…），不另造第二套口径。
// 本文件不测 internal/txn 自身的崩溃安全（那是 txn 包的用例）。

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
	"github.com/ikaqiu-Lemon/EverGreen/internal/txn"
)

// —— 辅助 ——

// runCaptureWith 用调用方给定的 Root 跑一次收录（用于注入 NewRepo 等接缝）。
func runCaptureWith(t *testing.T, r *Root, dir, body string, args ...string) (int, Envelope, string) {
	t.Helper()
	setGitIdentity(t)
	r.In = strings.NewReader(body)
	r.Now = func() time.Time { return captureAt(t) }
	code, out, errOut := runCLI(t, r, append([]string{
		"capture", "--vault", dir, "--json", "--body-stdin"}, args...)...)
	var env Envelope
	if strings.TrimSpace(out) != "" {
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("--json 输出不可解析：%v\n%s", err, out)
		}
	}
	return code, env, errOut
}

// dataWarnings 取 `data.warnings[]`（capture 没有 report 容器，警告面就在这里）。
func dataWarnings(t *testing.T, env Envelope) []string {
	t.Helper()
	raw, ok := env.Data["warnings"]
	if !ok {
		return nil
	}
	list, ok := raw.([]interface{})
	if !ok {
		t.Fatalf("data.warnings 不是数组：%T", raw)
	}
	var out []string
	for _, one := range list {
		s, _ := one.(string)
		out = append(out, s)
	}
	return out
}

func hasSubstring(list []string, want string) bool {
	for _, s := range list {
		if strings.Contains(s, want) {
			return true
		}
	}
	return false
}

// warnCodesOfEnv 收集信封 warnings[] 的诊断码（空码不计）。
func warnCodesOfEnv(env Envelope) []string {
	var out []string
	for _, w := range env.Warnings {
		if w.Code != "" {
			out = append(out, w.Code)
		}
	}
	return out
}

func envHasWarnCode(env Envelope, code string) bool {
	return contains(warnCodesOfEnv(env), code)
}

// captureTxnIDOf 取 data.txn_id（缺席返回空串）。
func captureTxnIDOf(env Envelope) string {
	id, _ := env.Data["txn_id"].(string)
	return id
}

func inboxPathOf(dir string) string { return filepath.Join(dir, store.UnprocessedFile) }

// —— ① 一次收录 = 一个事务：原文与收件区条目在**同一份** intent 里 ——

// TestCaptureSourceAndInboxRideOneIntent：首次收录写两个权威文件（原文 + 收件区），
// 它们必须落在同一个事务、同一份 intent.files[] 里 —— 否则「要么都在、要么都不在」
// 这条最基本的原子性根本无从谈起（旧实现是两次独立落盘，中间崩溃就会留下孤儿原文）。
func TestCaptureSourceAndInboxRideOneIntent(t *testing.T) {
	dir := captureVault(t)
	base := txnIDsOn(t, dir)

	env := captureOnce(t, dir, "--url", "https://example.com/a", "--title", "同一事务", "--reason", "r")
	rel := env.Data["path"].(string)

	id := onlyNewTxn(t, dir, base, "首次收录")
	if got := captureTxnIDOf(env); got != id {
		t.Fatalf("data.txn_id = %q，新增事务目录 = %q：两者必须恰好对上", got, id)
	}
	want := []string{rel, store.UnprocessedFile}
	sort.Strings(want)
	if got := intentPathsOf(t, dir, id); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("intent.files[] = %v，期望恰为 %v（原文与收件区必须同一份 intent）", got, want)
	}
	if !markerExists(dir, id, txn.CommitMarker) {
		t.Fatal("收录成功，commit 标记必须在盘")
	}
	if markerExists(dir, id, txn.AbortMarker) {
		t.Fatal("已提交的事务不该有 abort 标记")
	}
	// 两个文件都真的在盘上（intent 声明的目标态已生效）。
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
		t.Fatalf("原文必须在盘：%v", err)
	}
	if n := inboxEntries(t, dir); n != 1 {
		t.Fatalf("收件区条目数 = %d，期望 1", n)
	}
}

// —— ② 判重命中：理由追加与收件区重登记同样只开**一个**事务 ——

// TestCaptureDedupReasonAndInboxRideOneIntent：判重命中且条目此前已被移出队列时，
// 本次要写两处（原文的 reasons 追加 + 收件区重新登记）。这两处同样必须同生共死。
func TestCaptureDedupReasonAndInboxRideOneIntent(t *testing.T) {
	dir := captureVault(t)
	emptyInbox := mustRead(t, inboxPathOf(dir))

	first := captureOnce(t, dir, "--url", "https://example.com/a", "--title", "判重同事务", "--reason", "第一个理由")
	rel := first.Data["path"].(string)

	// 把条目移出队列（模拟已加工过一轮），并提交，使工作区干净。
	if err := os.WriteFile(inboxPathOf(dir), emptyInbox, 0o644); err != nil {
		t.Fatalf("重置收件区失败：%v", err)
	}
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-m", "chore: 清空收件区")
	base := txnIDsOn(t, dir)
	logBefore := logCount(t, dir)

	env := captureOnce(t, dir, "--url", "https://example.com/a", "--title", "判重同事务", "--reason", "第二个理由")
	if env.Data["deduped"] != true {
		t.Fatalf("同 URL 必须判重命中：%v", env.Data)
	}
	if got := sourceFiles(t, dir); len(got) != 1 {
		t.Fatalf("判重命中不得产生第二份原文：%v", got)
	}

	id := onlyNewTxn(t, dir, base, "判重命中且需重登记条目")
	if got := captureTxnIDOf(env); got != id {
		t.Fatalf("data.txn_id = %q，新增事务目录 = %q", got, id)
	}
	want := []string{rel, store.UnprocessedFile}
	sort.Strings(want)
	if got := intentPathsOf(t, dir, id); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("intent.files[] = %v，期望恰为 %v（理由追加与条目登记必须同一份 intent）", got, want)
	}
	if !markerExists(dir, id, txn.CommitMarker) {
		t.Fatal("提交成功，commit 标记必须在盘")
	}
	// 两处目标态都已生效，且这一次确实产生了 commit。
	if !strings.Contains(string(mustRead(t, filepath.Join(dir, filepath.FromSlash(rel)))), "第二个理由") {
		t.Fatal("理由未追加进原文")
	}
	if n := inboxEntries(t, dir); n != 1 {
		t.Fatalf("收件区应重新登记一条，实得 %d", n)
	}
	if got := logCount(t, dir); got != logBefore+1 {
		t.Fatalf("有权威写入必须产生一条 commit：%d → %d", logBefore, got)
	}
}

// —— ③ S2 崩溃恢复屏障必须早于锁内判重 ——

// TestCaptureRecoverPrecedesDuplicateScan：
// 崩溃残留把原文改成了「另一篇」（url / title 都变了）。本次收录若在恢复之前就扫描判重，
// 看到的是那份**未闭合事务的目标态**，于是判定「库里没有这篇」→ 凭空写出第二份原文；
// 只有先恢复、后判重，才会看到回滚后的前像并正确命中判重。
//
// 因此本用例不靠读代码，而是让**判重结果本身**成为顺序的证据：
// deduped=true 且 sources/ 仍只有一份 ⇒ 判重读到的必定是恢复之后的字节。
// 钩子里再补两针现场取证：取锁瞬间盘上还是崩溃态，恢复完成瞬间已回到前像。
func TestCaptureRecoverPrecedesDuplicateScan(t *testing.T) {
	dir := captureVault(t)
	first := captureOnce(t, dir, "--url", "https://example.com/a", "--title", "恢复先于判重", "--reason", "r")
	rel := first.Data["path"].(string)
	abs := filepath.Join(dir, filepath.FromSlash(rel))
	pre := mustRead(t, abs)

	// 崩溃前的目标态：url 与 title 双双改掉 —— 两个判重键（URL 精确、标题精确）同时失效。
	target := bytes.ReplaceAll(pre, []byte("url: 'https://example.com/a'"), []byte("url: 'https://example.com/zzz'"))
	target = bytes.ReplaceAll(target, []byte("title: '恢复先于判重'"), []byte("title: '完全不同的标题'"))
	if bytes.Equal(target, pre) {
		t.Fatal("夹具失效：原文里没有预期的 url / title 行，改写没有生效")
	}

	// 造一个「intent 已发布、权威 rename 已完成、commit / abort 皆缺席」的崩溃现场（§6 P4）。
	stale, err := txn.AllocateTxnID(dir)
	if err != nil {
		t.Fatalf("分配 txn_id 失败：%v", err)
	}
	now := func() time.Time { return captureAt(t) }
	if _, err := txn.WriteIntent(dir, stale, txn.IntentInput{
		Argv: []string{"eg", "capture"}, ExpectCommit: true, Now: now,
		Files: []txn.FileSpec{{
			Path: rel, PreBytes: pre, TargetBytes: target, TargetOp: "update",
		}},
	}); err != nil {
		t.Fatalf("发布 intent 失败：%v", err)
	}
	if err := os.WriteFile(abs, target, 0o644); err != nil {
		t.Fatalf("写崩溃态失败：%v", err)
	}

	var atAcquire, atRecovered string
	watchTxn(t, func(step, _ string) {
		switch step {
		case TxnStepAcquired:
			atAcquire = string(mustRead(t, abs))
		case TxnStepRecovered:
			atRecovered = string(mustRead(t, abs))
		}
	})

	code, env, errOut := runCaptureCLI(t, dir, captureBody,
		"--url", "https://example.com/a", "--title", "恢复先于判重", "--reason", "r")
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0（恢复成功后照常收录）：%s", code, errOut)
	}

	// ① 顺序的直接证据：判重命中，且库里仍只有一份原文。
	if env.Data["deduped"] != true {
		t.Fatalf("判重必须命中（说明读到的是恢复后的前像）：%v", env.Data)
	}
	if got := sourceFiles(t, dir); len(got) != 1 {
		t.Fatalf("恢复先于判重 ⇒ 不得写出第二份原文，实得 %v", got)
	}
	// ② 现场取证：取锁瞬间还是崩溃态，恢复完成瞬间已是前像。
	if atAcquire != string(target) {
		t.Fatal("取锁瞬间盘上就不是崩溃态：夹具没生效，本用例什么都没证明")
	}
	if atRecovered != string(pre) {
		t.Fatal("恢复节点回调时原文仍未回到前像：S2 没有真正回滚")
	}
	// ③ W26 如实进 warnings（capture 没有 report 容器，落点就是 Result.Warnings / data.warnings）。
	if !envHasWarnCode(env, txn.CodeTxnRecovered) {
		t.Fatalf("真实回滚必须留一条 %s：%v", txn.CodeTxnRecovered, env.Warnings)
	}
	// ④ 被恢复的那笔已写下 abort，且不是本次的事务号。
	if !markerExists(dir, stale, txn.AbortMarker) {
		t.Fatal("被回滚的未闭合事务必须写下 abort 标记")
	}
	if got := captureTxnIDOf(env); got == stale {
		t.Fatalf("本次不得复用被回滚的事务号 %q", stale)
	}
}

// —— ④ 锁被占住：有限时间内退 E16，权威零写 ——

// TestCaptureLockBusyReturnsE16WithoutHanging：
// 业务时钟被钉成常量（全仓 CLI 用例的统一口径）+ 锁被另一个 fd 真实占住 ⇒
// 必须在有限时间内带 E16 返回，绝不挂死；并且零权威写、零事务、零 Git。
func TestCaptureLockBusyReturnsE16WithoutHanging(t *testing.T) {
	dir := captureVault(t)
	before := authoritySnapshot(t, dir)
	logBefore := logCount(t, dir)
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
		err  string
	}
	done := make(chan runOut, 1)
	started := time.Now()
	go func() {
		r := newTestRoot(t, dir)
		code, env, errOut := runCaptureWith(t, r, dir, captureBody,
			"--url", "https://example.com/a", "--title", "锁被占住", "--reason", "r")
		done <- runOut{code: code, env: env, err: errOut}
	}()

	var got runOut
	select {
	case got = <-done:
	case <-time.After(20 * time.Second):
		t.Fatalf("锁被占用时 capture 在 20s 内没有返回：等待上限只有 %s=80ms，"+
			"说明 Acquire 拿到的是被钉死的业务时钟（elapsed 恒为 0 ⇒ 无限退避）", txn.LockTimeoutEnv)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("等待上限 80ms，实际耗时 %s：锁等待没有走真实墙钟", elapsed)
	}
	if got.code == ExitOK {
		t.Fatalf("锁被占用必须阻断，实得退出码 0：%+v", got.env)
	}
	diags := errorDiagsOf(t, got.env)
	if !diagsHaveCode(diags, txn.CodeLockTimeout) {
		t.Fatalf("锁等待超时必须携 %s，实得 %+v", txn.CodeLockTimeout, diags)
	}
	// I-…-016：W28 是 level=warning，取材面按合同 §3 搬到信封 warnings[]，另加分桶双侧封闭。
	assertLockWaitBuckets(t, got.env, "capture 撞上锁")
	assertAuthorityUnchanged(t, dir, before, "capture 撞上锁")
	assertNoNewTxn(t, dir, base, "capture 撞上锁")
	if n := logCount(t, dir); n != logBefore {
		t.Fatalf("没拿到锁不得产生 commit：%d → %d", logBefore, n)
	}
}

// —— ⑤ 零 accepted write-set：不开事务、不跑 Git、不出 txn_id ——

// TestCaptureZeroWriteSetOpensNoTxnAndNoGit：完全重复的一次收录（同 URL、同理由、
// 条目已在队列）一个字节都不写 ⇒ 不分配 txn_id、不建事务目录、不制造空 commit。
func TestCaptureZeroWriteSetOpensNoTxnAndNoGit(t *testing.T) {
	dir := captureVault(t)
	captureOnce(t, dir, "--url", "https://example.com/a", "--title", "零写入", "--reason", "同一个理由")

	before := authoritySnapshot(t, dir)
	base := txnIDsOn(t, dir)
	logBefore := logCount(t, dir)

	env := captureOnce(t, dir, "--url", "https://example.com/a", "--title", "零写入", "--reason", "同一个理由")

	assertAuthorityUnchanged(t, dir, before, "完全重复的收录")
	assertNoNewTxn(t, dir, base, "零 accepted write-set")
	if n := logCount(t, dir); n != logBefore {
		t.Fatalf("零写入不得制造空 commit：%d → %d", logBefore, n)
	}
	if _, ok := env.Data["txn_id"]; ok {
		t.Fatalf("未分配号码时 data 必须省略 txn_id，实得 %v", env.Data["txn_id"])
	}
	commit, _ := env.Data["commit"].(map[string]interface{})
	if commit == nil || commit["created"] != false {
		t.Fatalf("零写入时 commit.created 必须为 false：%v", env.Data["commit"])
	}
}

// —— ⑥ A-59：data.txn_id 与事务日志同真 ——

// TestCaptureTxnIDMatchesJournal：报告里的那个号码必须是合法形态、必须恰是盘上那个
// 事务目录、那个目录必须真的提交了，且 intent 覆盖本次写过的每一个路径。
func TestCaptureTxnIDMatchesJournal(t *testing.T) {
	dir := captureVault(t)
	base := txnIDsOn(t, dir)

	env := captureOnce(t, dir, "--url", "https://example.com/a", "--title", "号码对账", "--reason", "r")
	id := captureTxnIDOf(env)

	if !regexp.MustCompile(`^t[0-9a-f]{16}$`).MatchString(id) {
		t.Fatalf("txn_id = %q，不符合 `t` + 16 位小写 hex", id)
	}
	if !txn.ValidTxnID(id) {
		t.Fatalf("txn_id = %q 未被 txn.ValidTxnID 接受", id)
	}
	if got := onlyNewTxn(t, dir, base, "一次成功收录"); got != id {
		t.Fatalf("新增事务目录 = %q，data.txn_id = %q：两者必须恰好对上", got, id)
	}
	if !markerExists(dir, id, txn.CommitMarker) {
		t.Fatal("data 填了 txn_id，事务目录里却没有 commit 标记")
	}
	paths := intentPathsOf(t, dir, id)
	for _, rel := range []string{env.Data["path"].(string), store.UnprocessedFile} {
		if !contains(paths, rel) {
			t.Fatalf("本次写了 %s，intent.files[] 里却没有它：%v", rel, paths)
		}
	}
	if argv, _ := readIntentOf(t, dir, id)["argv"].([]any); len(argv) < 2 {
		t.Fatalf("intent.argv 至少要有 `eg capture`，实得 %v", argv)
	}
}

// —— ⑦ S7 Git 失败：退 4、不回滚、不做第二次权威写 ——

func TestCaptureGitFailureKeepsTargetStateWithoutSecondWrite(t *testing.T) {
	dir := captureVault(t)
	base := txnIDsOn(t, dir)
	logBefore := logCount(t, dir)

	var atGit map[string]string
	watchTxn(t, func(step, _ string) {
		if step == TxnStepGit {
			// Git 刚尝试完（失败）的瞬间取证 —— 这一针要求 TxnStepGit 在**成败两支**都触发。
			atGit = authoritySnapshot(t, dir)
		}
	})

	r := newTestRoot(t, dir)
	r.NewRepo = failingCommitRepo()
	code, env, errOut := runCaptureWith(t, r, dir, captureBody,
		"--url", "https://example.com/a", "--title", "Git 失败", "--reason", "r")
	if code != ExitCommitFailed {
		t.Fatalf("退出码 = %d，期望 4：%s", code, errOut)
	}
	if atGit == nil {
		t.Fatal("没有触达 Git 节点：TxnStepGit 在失败支没有触发，本用例什么都没证明")
	}

	// ① Markdown 已生效并保持目标态（原文与收件区都在盘上）。
	rel, _ := env.Data["path"].(string)
	if rel == "" {
		t.Fatalf("Git 失败也必须交付 data：%v", env.Data)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
		t.Fatalf("Git 失败不得回滚 Markdown，%s 却不在盘上：%v", rel, err)
	}
	if n := inboxEntries(t, dir); n != 1 {
		t.Fatalf("收件区条目应已生效，实得 %d", n)
	}
	// ② Git 之后没有第二次权威写。
	assertAuthorityUnchanged(t, dir, atGit, "capture 的 Git 失败后")
	// ③ 事务侧：恰一个事务、commit 标记在盘、没有 abort、没有第二个事务。
	id := onlyNewTxn(t, dir, base, "Git 失败")
	if got := captureTxnIDOf(env); got != id {
		t.Fatalf("data.txn_id = %q 与新增事务目录 %q 对不上（Git 失败绝不开第二个事务）", got, id)
	}
	if !markerExists(dir, id, txn.CommitMarker) {
		t.Fatal("Markdown 事务已提交，commit 标记必须在盘")
	}
	if markerExists(dir, id, txn.AbortMarker) {
		t.Fatal("Git 失败不是 Markdown 事务失败，不该有 abort 标记")
	}
	// ④ Git 侧：没有新 commit，data.commit.created=false。
	if n := logCount(t, dir); n != logBefore {
		t.Fatalf("Git 失败不得产生 commit：%d → %d", logBefore, n)
	}
	commit, _ := env.Data["commit"].(map[string]interface{})
	if commit == nil || commit["created"] != false {
		t.Fatalf("Git 失败时 commit.created 必须为 false：%v", env.Data["commit"])
	}
}

// —— ⑧ S6 提交期普通 I/O 失败 ⇒ 合同 §5.2 主动放弃（退 3，不是退 1）——

// TestCaptureCommitRollbackIsPartialWriteNotBlocked：
// 在 intent 发布之后、commit 标记发布之前把标记的临时名占死 —— 备料与全部权威 rename 都
// 照常成功、磁盘一度真的是目标态，只有最后一格失败。txn 层据此把全部目标还原成前像并写
// abort。CLI 必须把它翻译成「一条都没写成」的退 3（与 runPlan 同一裁决），而不是基础设施
// 崩坏的退 1；txn_id 必须保留、逐路径交代未写、Git 一步不许跑。
func TestCaptureCommitRollbackIsPartialWriteNotBlocked(t *testing.T) {
	dir := captureVault(t)
	before := authoritySnapshot(t, dir)
	base := txnIDsOn(t, dir)
	logBefore := logCount(t, dir)

	var blocked string
	watchTxn(t, func(step, txnID string) {
		if step != TxnStepIntent {
			return
		}
		blocked = filepath.Join(txn.TxnDirPath(dir, txnID), txn.CommitMarker+".tmp")
		if err := os.MkdirAll(filepath.Join(blocked, "occupied"), 0o755); err != nil {
			t.Fatalf("制造标记发布失败失败：%v", err)
		}
	})

	code, env, errOut := runCaptureCLI(t, dir, captureBody,
		"--url", "https://example.com/a", "--title", "提交回滚", "--reason", "r")
	if code != ExitPartialWrite {
		t.Fatalf("退出码 = %d，期望 3（提交失败已整体回滚，属主动放弃而非基础设施崩坏）：%s",
			code, errOut)
	}
	if blocked == "" {
		t.Fatal("没有触达 intent 节点：本用例什么都没证明")
	}
	_ = os.RemoveAll(blocked)

	// ① 权威内容回到事务开始前：原文没生出来、收件区没多条目。
	assertAuthorityUnchanged(t, dir, before, "capture 提交失败并回滚后")
	// ② 事务侧：abort 在盘、commit 不在盘。
	id := onlyNewTxn(t, dir, base, "提交失败并回滚")
	if !markerExists(dir, id, txn.AbortMarker) {
		t.Fatal("回滚完成后必须写下 abort 标记")
	}
	if markerExists(dir, id, txn.CommitMarker) {
		t.Fatal("没提交成功却有 commit 标记")
	}
	// ③ Git 一步没跑。
	if n := logCount(t, dir); n != logBefore {
		t.Fatalf("提交失败不得产生 commit：%d → %d", logBefore, n)
	}
	// ④ 号码保留（A-59：审计边界是「已分配」），且恰是 abort 目录名。
	if got := captureTxnIDOf(env); got != id {
		t.Fatalf("data.txn_id = %q，abort 日志目录 = %q：主动回滚的事务号必须交付且两者相等", got, id)
	}
	// ⑤ 逐路径交代「目标未写入」（原文 + 收件区两条，一条都不能少）。
	warns := dataWarnings(t, env)
	var unwritten int
	for _, w := range warns {
		if strings.Contains(w, "目标未写入") {
			unwritten++
		}
	}
	if unwritten != 2 {
		t.Fatalf("必须逐路径交代「目标未写入」（原文 + 收件区共 2 条），实得 %d 条：%v", unwritten, warns)
	}
	commit, _ := env.Data["commit"].(map[string]interface{})
	if commit == nil || commit["created"] != false {
		t.Fatalf("回滚后 commit.created 必须为 false：%v", env.Data["commit"])
	}
}

// —— ⑨ 退 4 与收件区跳过并存：优先级归优先级，跳过的事实一条都不许丢 ——

// TestCaptureGitFailureStillReportsInboxSkip：收件区不可读（B3 跳过，本该退 3）
// 撞上 Git 失败（退 4）。退出码按 4 走，但「条目没登记」这件事已经发生，
// 必须同时出现在 data.warnings[] 与错误明细里 —— 否则用户永远不知道队列里少了一条。
func TestCaptureGitFailureStillReportsInboxSkip(t *testing.T) {
	dir := captureVault(t)
	// 把收件区换成目录 ⇒ 读它必然失败 ⇒ AttachEntry 报 B3 跳过（原文照常写）。
	if err := os.Remove(inboxPathOf(dir)); err != nil {
		t.Fatalf("移除收件区失败：%v", err)
	}
	if err := os.MkdirAll(inboxPathOf(dir), 0o755); err != nil {
		t.Fatalf("制造不可读收件区失败：%v", err)
	}

	r := newTestRoot(t, dir)
	r.NewRepo = failingCommitRepo()
	code, env, errOut := runCaptureWith(t, r, dir, captureBody,
		"--url", "https://example.com/a", "--title", "跳过撞上 Git 失败", "--reason", "r")
	if code != ExitCommitFailed {
		t.Fatalf("退出码 = %d，期望 4（Git 失败优先于收件区跳过的退 3）：%s", code, errOut)
	}
	// ① 跳过的事实进了 data.warnings[]。
	if warns := dataWarnings(t, env); !hasSubstring(warns, "条目未登记") {
		t.Fatalf("收件区跳过必须留在 data.warnings[]，实得 %v", warns)
	}
	// ② 跳过的明细也进了错误清单。
	//
	// **C2 · I-…-015 取材面搬家**：`skipped[].cause` 的枚举值（content_hash_mismatch）
	// 不再占 `code` 位 —— `code` 位按合同 §5 一律是编号（B2/B3 跳过发 E24），cause / kind
	// 逐字仍可从 `report.skipped[]` 与本条 `message` 取到。保护一格没松，反而加严：
	// 既要求编号在册，又要求 cause 仍能取到，还反向封闭「cause 不许回到 code 位」。
	diags := errorDiagsOf(t, env)
	cause := store.CauseFor(store.SkipFileChanged)
	if !diagsHaveCode(diags, E24) {
		t.Fatalf("收件区跳过必须以编号 %s 上报（I-…-015：error 级 code 位落编号域），实得 %+v", E24, diags)
	}
	if diagsHaveCode(diags, cause) {
		t.Fatalf("cause=%s 不得占 code 位（I-…-015），实得 %+v", cause, diags)
	}
	if !hasSubstring(diagMessages(diags), cause) {
		t.Fatalf("错误明细必须带上收件区跳过的 cause=%s（文案里可读），实得 %+v", cause, diags)
	}
	// ③ 原文照常写入并保持目标态（跳过的是条目，不是整批）。
	rel, _ := env.Data["path"].(string)
	if rel == "" {
		t.Fatalf("Git 失败也必须交付 data：%v", env.Data)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
		t.Fatalf("原文必须已写入：%v", err)
	}
	// ④ 事务只覆盖真正写过的那一个路径：被跳过的收件区不进 write-set。
	id := captureTxnIDOf(env)
	if id == "" {
		t.Fatalf("有权威写入必须分配 txn_id：%v", env.Data)
	}
	if got := intentPathsOf(t, dir, id); strings.Join(got, ",") != rel {
		t.Fatalf("intent.files[] = %v，期望只含 %s（跳过的条目不入 write-set）", got, rel)
	}
}
