package cli

// internal/cli/precheck_wire_test.go —— 写前强校验（S3 precheck，`--strict`）CLI 接线的
// **真实写路径反证**（M6 · T-evergreen.s1_main_flow-158614-074 批次 B2a；合同 §9 / §17.1）。
//
// 全部用例走真实 store 写口、真实 git 仓、真实 run.lock 与事务日志，不打桩。观测手段沿用
// recover_hook_test.go 的 txnOrderHook 与那一组磁盘取证辅助（authoritySnapshot /
// assertAuthorityUnchanged / txnIDsOn / assertNoNewTxn），不另造第二套口径。
//
// 钉住的核心事实（缺一即违反合同）：
//   ① `--strict` 命中升级面 ⇒ 退 5（E15）、报告 errors[] 同时含 E15 与被升级的 W 码；
//   ② 同一份计划**不带** `--strict` ⇒ 照 M1~M5 语义写入成功、退 0（默认路径零漂移）；
//   ③ precheck 失败时本次请求**零权威写入**：知识内容逐字节不变、不开事务、HEAD 不动；
//   ④ precheck 恒在临界区内、S3 重读之后、开事务之前触发：观测到 Acquired + Reread，
//      但**绝不**出现 Allocated / Intent（写前强校验在发布屏障之前把写拦下）；
//   ⑤ 全部 plan 写命令的参数面都认得 `--strict`（写路径接线无遗漏）；
//   ⑥ 只读体检 `eg check --strict` 恒等变换 —— 不退 5、如实留一条恒等交代 info。

import (
	"flag"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/txn"
)

// w4CardPlan 是一份**除顶层废弃字段外完全合法**的建卡计划：`candidate` 命中废弃字段黑名单
// （card.candidate）产 W4。它在非 strict 下只是 warning、照常写入；在 `--strict` 下 W4 升 error、
// 写前强校验把写拦下。用它当语料，就能让「同一份计划、开关一变、结局两分」成为可执行断言。
func w4CardPlan() string {
	return cardPlan(applyCardID, `,"candidate":true`)
}

// landNote 落一篇材料笔记（建卡的 sources 需要它可解析），返回后不留脏工作区。
func landNote(t *testing.T, dir string) {
	t.Helper()
	if code, _, errOut := runApplyPlan(t, dir, notePlan()); code != ExitOK {
		t.Fatalf("前置落笔记退出码 = %d：%s", code, errOut)
	}
}

// —— ① + ③：strict 命中升级面 ⇒ 退 5（E15 + 被升级 W 码）且本次零权威写入 ——

func TestStrictPrecheckBlocksWriteWithExit5AndZeroWrite(t *testing.T) {
	dir := applyVault(t)
	landNote(t, dir)

	// 基线全部在被测这一次**之前**抓：夹具与落笔记都是合法历史，「零写入」按增量判定。
	before := gitLogCount(t, dir)
	headBefore := gitOut(t, dir, "rev-parse", "HEAD")
	authBefore := authoritySnapshot(t, dir)
	txnsBefore := txnIDsOn(t, dir)

	code, env, errOut := runApplyPlan(t, dir, w4CardPlan(), "--strict")
	if code != ExitPrecheckOrLock {
		t.Fatalf("退出码 = %d，期望 5（写前强校验失败）：%s", code, errOut)
	}

	// 报告 errors[] 必须同时交代「是写前强校验拦的」（E15）与「是哪条 W 拦的」（W4）。
	diags := errorDiagsOf(t, env)
	if !diagsHaveCode(diags, txn.CodePrecheckFailed) {
		t.Fatalf("errors[] 必须含写前强校验码 %s：%+v", txn.CodePrecheckFailed, diags)
	}
	if !diagsHaveCode(diags, "W4") {
		t.Fatalf("errors[] 必须点名被升级的 W4：%+v", diags)
	}

	// 本次请求零权威写入：知识内容逐字节不变、不开事务、HEAD 不动、commit 数不变。
	assertAuthorityUnchanged(t, dir, authBefore, "写前强校验失败")
	assertNoNewTxn(t, dir, txnsBefore, "写前强校验失败")
	if got := gitOut(t, dir, "rev-parse", "HEAD"); got != headBefore {
		t.Fatalf("写前强校验失败不得移动 HEAD：%q → %q", headBefore, got)
	}
	if got := gitLogCount(t, dir); got != before {
		t.Fatalf("commit 数 = %d，期望不变 %d", got, before)
	}
}

// —— ② 默认路径零漂移：同一份 W4 计划**不带** --strict ⇒ 照常写入、退 0 ——

func TestNonStrictSamePlanStillWritesExit0(t *testing.T) {
	dir := applyVault(t)
	landNote(t, dir)
	before := gitLogCount(t, dir)

	code, env, errOut := runApplyPlan(t, dir, w4CardPlan())
	if code != ExitOK {
		t.Fatalf("非 strict 下 W4 只是 warning，应照常写入退 0，实得 %d：%s", code, errOut)
	}
	if got := gitLogCount(t, dir); got != before+1 {
		t.Fatalf("非 strict 写入应恰产 1 次 commit：%d → %d", before, got)
	}
	// W4 仍如实在 warnings[] 上（升级只发生在 strict；默认路径 severity 一字不变）。
	rep := applyReport(t, env)
	found := false
	for _, w := range rep.Warnings {
		if w.Code == "W4" {
			found = true
		}
	}
	if !found {
		t.Fatalf("非 strict 下 W4 必须仍以 warning 形态如实出现：%+v", rep.Warnings)
	}
}

// —— ④：precheck 恒在临界区内、S3 重读之后、开事务之前触发 ——

func TestStrictPrecheckRunsInsideCriticalSectionBeforeTxn(t *testing.T) {
	dir := applyVault(t)
	landNote(t, dir)

	steps := watchTxn(t, nil)
	code, _, errOut := runApplyPlan(t, dir, w4CardPlan(), "--strict")
	if code != ExitPrecheckOrLock {
		t.Fatalf("退出码 = %d，期望 5：%s", code, errOut)
	}

	// 必须已进临界区并做完 S3 重读：Acquired + Reread 都在。
	if steps.at(TxnStepAcquired) < 0 {
		t.Fatalf("precheck 必须在持锁后触发，却没观测到 %s：%v", TxnStepAcquired, steps.names)
	}
	if steps.at(TxnStepReread) < 0 {
		t.Fatalf("precheck 必须在 S3 锁内重读重校验之后触发，却没观测到 %s：%v",
			TxnStepReread, steps.names)
	}
	// 但绝不能开事务：Allocated / Intent 一格都不许出现（写前强校验在发布屏障之前拦下）。
	if steps.at(TxnStepAllocated) >= 0 || steps.at(TxnStepIntent) >= 0 {
		t.Fatalf("写前强校验失败不得分配 txn_id / 发布 intent，却观测到：%v", steps.names)
	}
}

// —— ⑤：全部 plan 写命令的参数面都认得 --strict（接线无遗漏）——

func TestStrictFlagWiredOnEveryPlanWriteCommand(t *testing.T) {
	// runPlan 覆盖的七条写命令：apply / edit / deprecate / restore / replaced-by / rel（add|remove）。
	// 命令树上 rel add / rel remove 同属 `rel` 一级命令，故命令名去重后是这六个。
	writeCmds := map[string]bool{
		"apply": true, "edit": true, "deprecate": true,
		"restore": true, "replaced-by": true, "rel": true,
	}
	seen := map[string]bool{}
	for _, c := range New().Commands() {
		if !writeCmds[c.Name] {
			continue
		}
		seen[c.Name] = true
		fs := flag.NewFlagSet("eg "+c.Name, flag.ContinueOnError)
		if c.Flags != nil {
			c.Flags(fs)
		}
		if fs.Lookup(StrictFlag) == nil {
			t.Errorf("plan 写命令 eg %s 未注册 --%s（写前强校验接线遗漏）", c.Name, StrictFlag)
		}
		if !strings.Contains(c.Usage, "--"+StrictFlag) {
			t.Errorf("eg %s 的 --help 未列出 --%s", c.Name, StrictFlag)
		}
	}
	for name := range writeCmds {
		if !seen[name] {
			t.Errorf("命令树上缺 plan 写命令 %q：写路径接线断言无法覆盖它", name)
		}
	}
}

// —— ⑥：只读体检 eg check --strict 恒等变换 —— 不退 5、留一条恒等交代 info ——

func TestCheckStrictIsIdentityNeverExits5(t *testing.T) {
	dir := applyVault(t)
	landNote(t, dir)

	code, out, errOut := runCheckCLI(t, dir, "--strict")
	if code == ExitPrecheckOrLock {
		t.Fatalf("eg check 只判 R3/R4，其 finding 与升级面不相交，--strict 绝不该让它退 5：%s", errOut)
	}
	// 恒等变换的诚实交代：显式 --strict 时留一条 info（逐字前缀取自 CheckStrictNotice）。
	if !strings.Contains(out, CheckStrictNotice[:len("eg check --strict")]) {
		t.Fatalf("eg check --strict 应留一条恒等交代 info，输出里没找到：%s", out)
	}
}
