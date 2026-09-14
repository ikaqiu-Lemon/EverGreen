package cli

// index_after_write_capture_test.go —— M6 · T-…-072 批次 C1 修正：`eg capture` 的 **S8
// 写后索引同步**反证（合同 §16.1 / §16.3；M5 索引架构合同 §5.3 / §6.1）。
//
// 为什么这些用例住在 index* 文件里，而不是 capture_transaction_test.go：
// 命令层的文件级位置锁（cmd/eg/arch_test.go 的 TestStage4IndexPackageBoundary ⑤）规定
// `internal/cli/` 下**只有 index 前缀的文件**可以 import 索引包 —— 这是「索引不作为任何
// 命令的前置」的机器形态。写后同步的判据必须直接读索引（Digest / Inspect / status），
// 所以按位置锁把它们放在这里；用例名仍以 TestCapture 开头，`-run TestCapture` 一并覆盖。
//
// 本组钉的事实只有一条主线：capture 的非空 write-set 在 S7 之后、Release 之前，
// 于**同一把 run.lock** 内调用唯一的 syncIndexAfterWrite，Git 成败一视同仁；
// 索引问题只产 W22 / W24，绝不改变退出码、绝不回滚、绝不二次写权威。

import (
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/index"
)

// —— ⑩ S8：写后索引同步必须在**同一把锁内**、S7 之后、Release 之前发生 ——
//
// C1 首版在 S7 之后直接 return，`eg capture` 因此完全跳过了 S8：已有 healthy 索引时，
// capture 的 Git commit 把 HEAD 往前推，索引水位线却停在旧值——命令一返回，索引立刻陈旧，
// 而且这个陈旧窗口是在**锁外**产生的，任何进程都能读到一个自称健康、实际落后的库。
// 本组把「S8 确实发生过、且发生在正确的位置」变成可反证的事实（合同 §16.1 / §16.3）。

// assertStepOrder 断言若干编排节点**全部触发**且严格按给定次序发生。
func assertStepOrder(t *testing.T, rec *txnSteps, steps ...string) {
	t.Helper()
	for _, s := range steps {
		if rec.at(s) < 0 {
			t.Fatalf("节点 %q 没有触发，实际序列 %v", s, rec.names)
		}
	}
	for i := 1; i < len(steps); i++ {
		if rec.at(steps[i-1]) >= rec.at(steps[i]) {
			t.Fatalf("节点顺序错：%q 必须早于 %q，实际序列 %v",
				steps[i-1], steps[i], rec.names)
		}
	}
}

// capIndexedVault 建一个「有真实卡内容 + 已构建 healthy 索引」的 vault。
//
// 用 idxVault 而不是 captureVault：索引里必须真的有卡行，否则「水位线收敛」这条判据
// 会退化成在一个空库上自说自话。
func capIndexedVault(t *testing.T) string {
	t.Helper()
	dir := idxVault(t)
	if code, _, errOut := runIndexCLI(t, dir, "build"); code != ExitOK {
		t.Fatalf("前置 build 退出码 = %d：%s", code, errOut)
	}
	if h := capIndexStatus(t, dir, "health"); h != string(index.HealthHealthy) {
		t.Fatalf("前置：索引应 healthy，实得 %q", h)
	}
	return dir
}

// capIndexStatus 跑一次 `eg index status` 并取 `data.index.<key>`。
func capIndexStatus(t *testing.T, dir, key string) string {
	t.Helper()
	code, out, errOut := runIndexCLI(t, dir, "status")
	if code != ExitOK {
		t.Fatalf("index status 退出码 = %d（体检恒退 0）：%s", code, errOut)
	}
	return idxString(t, out, key)
}

func capHeadOf(t *testing.T, dir string) string {
	t.Helper()
	return strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))
}

// capAssertIndexConverged 断言索引仍 healthy、且水位线已收敛到当前 HEAD。
func capAssertIndexConverged(t *testing.T, dir, what string) {
	t.Helper()
	if h := capIndexStatus(t, dir, "health"); h != string(index.HealthHealthy) {
		t.Fatalf("%s：索引 health = %q，期望 %q（写后同步绝不许把库弄坏）",
			what, h, index.HealthHealthy)
	}
	if f := capIndexStatus(t, dir, "freshness"); f != string(index.FreshnessFresh) {
		t.Fatalf("%s：freshness = %q，期望 %q（S8 之后索引必须已跟上权威）",
			what, f, index.FreshnessFresh)
	}
	if got, want := capIndexStatus(t, dir, "head"), capHeadOf(t, dir); got != want {
		t.Fatalf("%s：索引水位线 head = %q，当前 HEAD = %q（水位线必须逐字收敛）",
			what, got, want)
	}
}

// capAssertNoIndexTrouble 断言本次没有产生索引侧的 W22 / W24。
func capAssertNoIndexTrouble(t *testing.T, env Envelope) {
	t.Helper()
	for _, code := range []string{index.CodeIndexStale, index.CodeIndexCorrupt} {
		if envHasWarnCode(env, code) {
			t.Fatalf("写后同步本该成功，却报了 %s：%v", code, env.Warnings)
		}
	}
}

// TestCaptureSyncsIndexInsideLockAfterGit：Git 成功时，S8 在同一把锁内、S7 之后、
// Release 之前执行，索引水位线随 HEAD 一起前进。
func TestCaptureSyncsIndexInsideLockAfterGit(t *testing.T) {
	dir := capIndexedVault(t)
	headBefore := capHeadOf(t, dir)

	rec := watchTxn(t, nil)
	env := captureOnce(t, dir,
		"--url", "https://example.com/idx-sync", "--title", "写后同步", "--reason", "r")

	// 前置有效性：本次确实产生了 commit，HEAD 真的动了 —— 否则「水位线收敛」无从谈起。
	headAfter := capHeadOf(t, dir)
	if headAfter == headBefore {
		t.Fatal("本次收录没有产生 commit：水位线反证会退化成空跑")
	}
	txnID := captureTxnIDOf(env)
	if txnID == "" {
		t.Fatalf("有权威写入必须分配 txn_id：%v", env.Data)
	}

	// ① 顺序：commit marker → Git → 索引同步 → 释放锁。S8 夹在 S7 与 S9 之间，
	//    意味着它**仍持着同一把 run.lock**（释放尚未开始）。
	assertStepOrder(t, rec, TxnStepCommitted, TxnStepGit, TxnStepIndexSync, TxnStepReleasing)
	if got := rec.idAt(TxnStepIndexSync); got != txnID {
		t.Fatalf("index_sync 节点带的 txn_id = %q，期望本次事务 %q（不得另开事务）", got, txnID)
	}
	// ② 索引仍健康、水位线已收敛到新 HEAD（这正是首版缺 S8 时会红的那一格）。
	capAssertIndexConverged(t, dir, "Git 成功后的 capture")
	capAssertNoIndexTrouble(t, env)
	// ③ 产物契约零漂移：写后同步只许追加既有诊断，不得多出 data 键。
	if _, ok := env.Data["index"]; ok {
		t.Fatalf("capture 的 data 里出现了 index 键：写后同步只许追加既有诊断结构：%v", env.Data)
	}
	if _, ok := env.Data["warnings"]; !ok {
		t.Fatal("data.warnings[] 必须恒在（诊断面不得因为接了 S8 而改形态）")
	}
	// ④ 索引恒不进 Git：写后同步不得弄脏工作区。
	if n := rcPorcelainCount(t, dir); n != 0 {
		t.Fatalf("写后工作区脏了 %d 行：.index/ 必须被整目录忽略", n)
	}
}

// TestCaptureSyncsIndexEvenWhenGitFails：Git 失败**不能**成为跳过 S8 的理由。
//
// commit marker 已经落盘 ⇒ Markdown 已原子生效、权威内容已经变了；「这批改动没进版本
// 历史」与「派生索引要不要跟上」是两件事。首版从 Git 失败支提前 return，等于把索引留在
// 一个明知已经落后的状态上，还把这件事瞒了下来。
func TestCaptureSyncsIndexEvenWhenGitFails(t *testing.T) {
	dir := capIndexedVault(t)
	logBefore := logCount(t, dir)

	rec := watchTxn(t, nil)
	r := newTestRoot(t, dir)
	r.NewRepo = failingCommitRepo()
	code, env, errOut := runCaptureWith(t, r, dir, captureBody,
		"--url", "https://example.com/idx-git-fail", "--title", "Git 失败也要同步", "--reason", "r")
	if code != ExitCommitFailed {
		t.Fatalf("退出码 = %d，期望 4（索引同步不改变退出码）：%s", code, errOut)
	}
	if n := logCount(t, dir); n != logBefore {
		t.Fatalf("Git 失败不得产生 commit：%d → %d", logBefore, n)
	}

	// ① S8 仍在锁内、Git 之后发生（失败支一样要走完）。
	assertStepOrder(t, rec, TxnStepCommitted, TxnStepGit, TxnStepIndexSync, TxnStepReleasing)
	if got, want := rec.idAt(TxnStepIndexSync), captureTxnIDOf(env); got != want || want == "" {
		t.Fatalf("index_sync 的 txn_id = %q，期望本次事务 %q", got, want)
	}
	// ② 索引照样收敛（HEAD 没动，但同步真的跑过：库仍 healthy 且与现态一致）。
	capAssertIndexConverged(t, dir, "Git 失败后的 capture")
	capAssertNoIndexTrouble(t, env)
	// ③ Git 失败的明细一条不丢，退出码仍由它决定。
	if !hasSubstring(diagMessages(errorDiagsOf(t, env)), "Git") {
		t.Fatalf("Git 失败明细必须交付：%+v", errorDiagsOf(t, env))
	}
	commit, _ := env.Data["commit"].(map[string]interface{})
	if commit == nil || commit["created"] != false {
		t.Fatalf("Git 失败时 commit.created 必须为 false：%v", env.Data["commit"])
	}
}

// TestCaptureZeroWriteSetDoesNotFakeIndexSync：零 accepted write-set ⇒ 不开事务、不跑 Git，
// **也不许伪造 S8**（没写过任何东西，就没有「写后」可言；假装同步过会让节点序列说谎）。
func TestCaptureZeroWriteSetDoesNotFakeIndexSync(t *testing.T) {
	dir := capIndexedVault(t)
	args := []string{"--url", "https://example.com/idx-dup", "--title", "重复收录", "--reason", "同一个理由"}
	captureOnce(t, dir, args...) // 第一次：真实事务 + 真实 S8

	base := txnIDsOn(t, dir)
	logBefore := logCount(t, dir)
	digestBefore, err := index.Digest(index.DirPath(dir))
	if err != nil {
		t.Fatalf("首次收录后索引应可读：%v", err)
	}

	rec := watchTxn(t, nil)
	env := captureOnce(t, dir, args...) // 第二次：完全重复 ⇒ 零 accepted write-set

	if rec.at(TxnStepIndexSync) >= 0 {
		t.Fatalf("零 accepted write-set 不得触发 S8，实际序列 %v", rec.names)
	}
	if rec.at(TxnStepGit) >= 0 {
		t.Fatalf("零 accepted write-set 不得跑 Git，实际序列 %v", rec.names)
	}
	assertNoNewTxn(t, dir, base, "零 accepted write-set")
	if n := logCount(t, dir); n != logBefore {
		t.Fatalf("零写入不得制造空 commit：%d → %d", logBefore, n)
	}
	if _, ok := env.Data["txn_id"]; ok {
		t.Fatalf("未分配号码时 data 必须省略 txn_id，实得 %v", env.Data["txn_id"])
	}
	digestAfter, err := index.Digest(index.DirPath(dir))
	if err != nil {
		t.Fatalf("零写入后索引应仍可读：%v", err)
	}
	if digestAfter != digestBefore {
		t.Fatal("零写入却动了索引：没有权威写入就不该有任何派生写入")
	}
	capAssertIndexConverged(t, dir, "零 accepted write-set 的 capture")
}

// TestCaptureCorruptIndexKeepsExitCodeAndReportsW24：索引损坏时，capture 照常成功（退 0）、
// 如实报 W24、且**不自动修**；诊断落在 capture 既有的 warnings 面上，一条都不丢。
func TestCaptureCorruptIndexKeepsExitCodeAndReportsW24(t *testing.T) {
	dir := capIndexedVault(t)
	idxCorruptDB(t, dir)
	logBefore := logCount(t, dir)

	rec := watchTxn(t, nil)
	env := captureOnce(t, dir,
		"--url", "https://example.com/idx-corrupt", "--title", "坏索引不拖垮收录", "--reason", "r")

	// ① 退出码不变、Git 照常：索引问题不改变退出码（合同 §6.1）。
	if n := logCount(t, dir); n != logBefore+1 {
		t.Fatalf("commit 数 %d → %d，期望恰 +1（索引不是 capture 的前置）", logBefore, n)
	}
	// ② S8 仍然尝试过（尝试的结果是「跳过并报 W24」，不是「没走这一步」）。
	assertStepOrder(t, rec, TxnStepGit, TxnStepIndexSync, TxnStepReleasing)
	// ③ W24 同时出现在信封 warnings（带码）与 data.warnings（capture 的诊断面）。
	if !envHasWarnCode(env, index.CodeIndexCorrupt) {
		t.Fatalf("坏索引必须如实报 %s，实得 %v", index.CodeIndexCorrupt, warnCodesOfEnv(env))
	}
	if warns := dataWarnings(t, env); !hasSubstring(warns, "eg index rebuild") {
		t.Fatalf("W24 必须落到 data.warnings[] 并给出修复建议，实得 %v", warns)
	}
	// ④ 不自动修：坏库保持坏，等用户显式 rebuild（自动修会掩盖「为什么坏」）。
	if _, err := index.Digest(index.DirPath(dir)); err == nil {
		t.Fatal("capture 自动修好了坏索引：修复只能走 eg index rebuild")
	}
}

// TestCaptureCorruptIndexWithGitFailureKeepsBothFacts：坏索引 + Git 失败同时发生。
//
// 退出码仍由 Git 失败决定（退 4，索引一格不影响），但两件事实都必须交付：
// W24 在 warnings 面上，Git 失败在错误明细里。
func TestCaptureCorruptIndexWithGitFailureKeepsBothFacts(t *testing.T) {
	dir := capIndexedVault(t)
	idxCorruptDB(t, dir)

	rec := watchTxn(t, nil)
	r := newTestRoot(t, dir)
	r.NewRepo = failingCommitRepo()
	code, env, errOut := runCaptureWith(t, r, dir, captureBody,
		"--url", "https://example.com/idx-both", "--title", "坏索引撞上 Git 失败", "--reason", "r")
	if code != ExitCommitFailed {
		t.Fatalf("退出码 = %d，期望 4（索引问题不参与退出码裁决）：%s", code, errOut)
	}
	assertStepOrder(t, rec, TxnStepCommitted, TxnStepGit, TxnStepIndexSync, TxnStepReleasing)
	if !envHasWarnCode(env, index.CodeIndexCorrupt) {
		t.Fatalf("坏索引的 %s 不得因为 Git 失败而消失，实得 %v",
			index.CodeIndexCorrupt, warnCodesOfEnv(env))
	}
	if !hasSubstring(diagMessages(errorDiagsOf(t, env)), "Git") {
		t.Fatalf("Git 失败明细必须交付：%+v", errorDiagsOf(t, env))
	}
	if _, err := index.Digest(index.DirPath(dir)); err == nil {
		t.Fatal("坏索引被自动修复了：修复只能走 eg index rebuild")
	}
}

// diagMessages 取诊断清单的消息文本（用于「某条事实有没有被交付」这类判据）。
func diagMessages(diags []Diagnostic) []string {
	out := make([]string, 0, len(diags))
	for _, d := range diags {
		out = append(out, d.Message)
	}
	return out
}
