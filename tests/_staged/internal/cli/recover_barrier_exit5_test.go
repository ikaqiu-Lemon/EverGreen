package cli

// recover_barrier_exit5_test.go —— M6 · T-…-075 批次 C1a：**恢复屏障五类 fail closed 的
// CLI 端到端反证**（合同 §4.1 / §9 / §17.1）。
//
// internal/txn 的 recover_test.go 已在 txn 层逐条钉住五类崩溃恢复屏障各自 fail closed 并携
// E15；exit5_test.go 已在单元层钉住「E15 → 退出码 5」的类型化映射。本文件补的是**两者之间
// 那一段真实写路径**：用 `eg apply` 真实进入临界区，让 S2 崩溃恢复屏障在真实 run.lock /
// 事务日志上被触发，从进程退出码这一端反证：
//
//	① 五类屏障（post-crash 外部编辑冲突 / 损坏 intent / 路径越界 / 前像不可信 / 多 OpenTxn）
//	   任一命中 ⇒ 退出码 5、errors[] 携 E15；
//	② **无 W26**：屏障在任何回滚写之前整体阻断，没有任何事务被真正回滚，因此不产恢复回执；
//	③ 现场事务**保持未闭合**：既不写 abort、也不写 commit —— 崩溃证据原样保留待人工核对；
//	④ 本次请求**零权威写入、不开新事务、HEAD 不动**；
//	⑤ 合法组合「先 W26 后 S3 退 5」：一笔可干净回滚的未闭合事务先产出 W26，随后同一次命令的
//	   S3 写前强校验（--strict）命中升级面再退 5 —— W26 与退出码 5 并存是合规的，二者来自
//	   编排的不同阶段（S2 vs S3），不得相互吞没。
//
// 全部用例走真实 store 写口、真实 git 仓、真实 run.lock 与事务日志，不打桩。夹具沿用
// applyVault / applyNoteAndCard 与 recover_hook_test.go 的那一组磁盘取证辅助
// （authoritySnapshot / assertAuthorityUnchanged / txnIDsOn / assertNoNewTxn /
// markerExists / reportHasCode / errorDiagsOf / diagsHaveCode），不另造第二套口径。

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/txn"
)

// —— 崩溃现场构造辅助（全部走 txn 导出 API，不碰 txn 包内测试 helper）——

// seedOpenTxn 造一笔**未闭合事务**（intent 已发布、commit / abort 皆缺席 = 崩溃现场）。
//
// 走真实 AllocateTxnID + WriteIntent：`.index/txn/<id>/` 在盘、pre/0 装着 pre 的前像副本、
// intent.json 记着「pre → target」的写意图。返回事务号。默认**不动权威文件**（保持 pre），
// 因此若不做额外改动，本事务在 S2 恢复时属「全部文件仍在前像」的可干净回滚态（产 W26）。
func seedOpenTxn(t *testing.T, dir, cardRel string, pre []byte) string {
	t.Helper()
	id, err := txn.AllocateTxnID(dir)
	if err != nil {
		t.Fatalf("分配 txn_id 失败：%v", err)
	}
	now := func() time.Time { return captureAt(t) }
	target := append(append([]byte(nil), pre...), []byte("\n<!-- 崩溃前的目标态 -->\n")...)
	if _, err := txn.WriteIntent(dir, id, txn.IntentInput{
		Argv: []string{"eg", "apply"}, ExpectCommit: true, Now: now,
		Files: []txn.FileSpec{{
			Path: cardRel, PreBytes: pre, TargetBytes: target, TargetOp: "update",
		}},
	}); err != nil {
		t.Fatalf("发布 intent 失败：%v", err)
	}
	return id
}

// assertBarrierExit5 是五类屏障共用的断言组：退 5、E15、无 W26、现场未闭合、零权威写、不开新事务。
//
// stale 为「屏障命中前就摆在盘上的那笔崩溃事务号」；barrier 描述现场类型，仅用于失败信息可读。
func assertBarrierExit5(t *testing.T, dir, stale, barrier string,
	authBefore map[string]string, txnsBefore []string, headBefore string, logBefore int,
	code int, env Envelope, errOut string) {
	t.Helper()

	// ① 退出码 5：写前安全复核 fail closed（E15 / E16 共用一码），端到端翻译只此一次。
	if code != ExitPrecheckOrLock {
		t.Fatalf("%s：退出码 = %d，期望 5（崩溃恢复屏障 fail closed）：%s", barrier, code, errOut)
	}
	// ① errors[] 如实携 E15：恢复屏障的诊断经 blockedError（errors.As）一路搬到 CLI 错误载荷。
	diags := errorDiagsOf(t, env)
	if !diagsHaveCode(diags, txn.CodePrecheckFailed) {
		t.Fatalf("%s：errors[] 必须携写前强校验码 %s，实得 %+v",
			barrier, txn.CodePrecheckFailed, diags)
	}

	// ② 无 W26：屏障在任何回滚写之前整体阻断，没有事务被真正回滚 ⇒ 不产恢复回执。
	//    错误路径不落 last-report，report 键通常缺席；即便存在也不得含 W26。两侧都查。
	if diagsHaveCode(diags, txn.CodeTxnRecovered) {
		t.Fatalf("%s：屏障阻断不该产 W26，errors[] 里却出现了 %s：%+v",
			barrier, txn.CodeTxnRecovered, diags)
	}
	if reportHasCode(applyReport(t, env), txn.CodeTxnRecovered) {
		t.Fatalf("%s：屏障阻断不该产 W26，report.warnings 里却出现了 %s",
			barrier, txn.CodeTxnRecovered)
	}

	// ③ 现场事务保持未闭合：既不写 abort、也不写 commit（崩溃证据原样留待人工核对）。
	if stale != "" {
		if markerExists(dir, stale, txn.AbortMarker) {
			t.Fatalf("%s：屏障阻断绝不能给现场事务 %s 补写 abort（那等于替它裁决了）", barrier, stale)
		}
		if markerExists(dir, stale, txn.CommitMarker) {
			t.Fatalf("%s：现场事务 %s 不该凭空多出 commit 标记", barrier, stale)
		}
	}

	// ④ 本次请求零权威写入、不开新事务、HEAD 不动、commit 数不变。
	assertAuthorityUnchanged(t, dir, authBefore, barrier)
	assertNoNewTxn(t, dir, txnsBefore, barrier)
	if got := gitOut(t, dir, "rev-parse", "HEAD"); got != headBefore {
		t.Fatalf("%s：不得移动 HEAD：%q → %q", barrier, headBefore, got)
	}
	if got := gitLogCount(t, dir); got != logBefore {
		t.Fatalf("%s：不得产生 commit：%d → %d", barrier, logBefore, got)
	}
}

// capture 五份基线的小工具（顺序与 assertBarrierExit5 的形参一致，避免调用点写错）。
func barrierBaselines(t *testing.T, dir string) (auth map[string]string, txns []string, head string, log int) {
	t.Helper()
	return authoritySnapshot(t, dir), txnIDsOn(t, dir),
		gitOut(t, dir, "rev-parse", "HEAD"), gitLogCount(t, dir)
}

// —— ① post-crash 外部编辑冲突（B-R3）⇒ 退 5 ——

// TestRecoveryBarrierConflictExits5：未闭合事务的权威文件在崩溃后被外部编辑成**第三种字节**
// （既非前像、也非目标态）⇒ Pass A 判 B-R3 冲突、fail closed（E15）⇒ 退 5、无 W26、现场未闭合。
func TestRecoveryBarrierConflictExits5(t *testing.T) {
	dir := applyVault(t)
	_, cardRel := applyNoteAndCard(t, dir)
	cardAbs := filepath.Join(dir, filepath.FromSlash(cardRel))
	pre := mustRead(t, cardAbs)

	stale := seedOpenTxn(t, dir, cardRel, pre)
	// 外部把权威文件编辑成第三种字节：current 既不等于 pre、也不等于 intent 里的 target。
	if err := os.WriteFile(cardAbs, []byte("EXTERNAL-EDIT-AFTER-CRASH\n"), 0o644); err != nil {
		t.Fatalf("制造 post-crash 外部编辑失败：%v", err)
	}

	authBefore, txnsBefore, headBefore, logBefore := barrierBaselines(t, dir)
	code, env, errOut := runApplyPlan(t, dir, cardPlan("k-20260901-after-conflict", ""))
	assertBarrierExit5(t, dir, stale, "post-crash 外部编辑冲突",
		authBefore, txnsBefore, headBefore, logBefore, code, env, errOut)
}

// TestRecoverRefusesPostCrashUserEdit：与上一支同源但换个视角——强调「用户在崩溃后手动改过
// 那个文件」这一现实场景：恢复层拒绝在无法确认前像的情况下擅自覆盖用户的手改，fail closed 退 5，
// 用户的手改字节**逐字保留**（不被回滚成前像、也不被推进成目标态）。
func TestRecoverRefusesPostCrashUserEdit(t *testing.T) {
	dir := applyVault(t)
	_, cardRel := applyNoteAndCard(t, dir)
	cardAbs := filepath.Join(dir, filepath.FromSlash(cardRel))
	pre := mustRead(t, cardAbs)

	stale := seedOpenTxn(t, dir, cardRel, pre)
	userEdit := []byte("用户在崩溃后手动救火改的内容\n")
	if err := os.WriteFile(cardAbs, userEdit, 0o644); err != nil {
		t.Fatalf("制造用户手改失败：%v", err)
	}

	authBefore, txnsBefore, headBefore, logBefore := barrierBaselines(t, dir)
	code, env, errOut := runApplyPlan(t, dir, cardPlan("k-20260901-after-useredit", ""))
	assertBarrierExit5(t, dir, stale, "post-crash 用户手改",
		authBefore, txnsBefore, headBefore, logBefore, code, env, errOut)

	// 额外一条：用户手改的字节逐字保留（恢复层没擅自还原成前像、也没推进成目标态）。
	if got := mustRead(t, cardAbs); string(got) != string(userEdit) {
		t.Fatalf("崩溃后用户手改的字节必须原样保留，实得 %q", got)
	}
}

// —— ② 损坏 intent（不可解析）⇒ 退 5 ——

// TestCorruptIntentExits5：事务目录里的 intent.json 不可解析 ⇒ 全集裁决判为 CorruptTxn、
// 在任何回滚写之前整体阻断（E15）⇒ 退 5、无 W26、现场目录原样保留。
func TestCorruptIntentExits5(t *testing.T) {
	dir := applyVault(t)
	applyNoteAndCard(t, dir)

	stale, err := txn.AllocateTxnID(dir)
	if err != nil {
		t.Fatalf("分配 txn_id 失败：%v", err)
	}
	// 写半个 JSON（不可解析）——CorruptTxn 的最小构造。
	if err := os.WriteFile(filepath.Join(txn.TxnDirPath(dir, stale), txn.IntentFileName),
		[]byte(`{"txn_id":`), 0o644); err != nil {
		t.Fatalf("写损坏 intent 失败：%v", err)
	}

	authBefore, txnsBefore, headBefore, logBefore := barrierBaselines(t, dir)
	code, env, errOut := runApplyPlan(t, dir, cardPlan("k-20260901-after-corrupt", ""))
	assertBarrierExit5(t, dir, stale, "损坏 intent",
		authBefore, txnsBefore, headBefore, logBefore, code, env, errOut)
}

// —— ③ intent 路径越界 ⇒ 退 5 ——

// TestEscapingIntentPathExits5：intent.files[].path 逃出 vault（此处用 `..` 上跳）⇒ Pass A
// 路径校验 fail closed（E15）⇒ 退 5、无 W26。用 create:true 免前像依赖，让违规精确落在路径判据上。
func TestEscapingIntentPathExits5(t *testing.T) {
	dir := applyVault(t)
	applyNoteAndCard(t, dir)

	stale, err := txn.AllocateTxnID(dir)
	if err != nil {
		t.Fatalf("分配 txn_id 失败：%v", err)
	}
	raw := `{"txn_id":"` + stale + `","started_at":"2027-02-01T00:00:00Z","argv":["eg","apply"],` +
		`"files":[{"path":"../../outside.md","pre_hash":"","pre_size":0,"target_hash":"` +
		txn.HashBytes([]byte("x")) + `","target_size":1,"create":true,"target_op":"create"}],` +
		`"skipped":[],"git":{"expect_commit":false},"journal_version":1}`
	if err := os.WriteFile(filepath.Join(txn.TxnDirPath(dir, stale), txn.IntentFileName),
		[]byte(raw), 0o644); err != nil {
		t.Fatalf("写越界 intent 失败：%v", err)
	}

	authBefore, txnsBefore, headBefore, logBefore := barrierBaselines(t, dir)
	code, env, errOut := runApplyPlan(t, dir, cardPlan("k-20260901-after-escape", ""))
	assertBarrierExit5(t, dir, stale, "intent 路径越界",
		authBefore, txnsBefore, headBefore, logBefore, code, env, errOut)
}

// —— ④ 前像不可信 ⇒ 退 5 ——

// TestCorruptPreimageExits5：权威文件已处于目标态（本应 B-R2 回滚），但事务里的前像副本 pre/0
// 被截断成与 pre_size 不符 ⇒ Pass A 前像可用性校验 fail closed（E15）⇒ 退 5、绝不进 Pass B、
// 权威文件保持目标态不被回滚、无 W26。
func TestCorruptPreimageExits5(t *testing.T) {
	dir := applyVault(t)
	_, cardRel := applyNoteAndCard(t, dir)
	cardAbs := filepath.Join(dir, filepath.FromSlash(cardRel))
	pre := mustRead(t, cardAbs)

	stale := seedOpenTxn(t, dir, cardRel, pre)
	// 把权威文件推进到目标态（= current==target ⇒ 若前像可信本会 B-R2 回滚）。
	target := append(append([]byte(nil), pre...), []byte("\n<!-- 崩溃前的目标态 -->\n")...)
	if err := os.WriteFile(cardAbs, target, 0o644); err != nil {
		t.Fatalf("推进权威文件到目标态失败：%v", err)
	}
	// 截断前像副本：size 与 intent 里记的 pre_size 不符 ⇒ 前像不可信。
	preCopy := filepath.Join(txn.TxnDirPath(dir, stale), txn.PreDirName, "0")
	if err := os.WriteFile(preCopy, []byte("X"), 0o644); err != nil {
		t.Fatalf("截断前像副本失败：%v", err)
	}

	authBefore, txnsBefore, headBefore, logBefore := barrierBaselines(t, dir)
	code, env, errOut := runApplyPlan(t, dir, cardPlan("k-20260901-after-preimage", ""))
	assertBarrierExit5(t, dir, stale, "前像不可信",
		authBefore, txnsBefore, headBefore, logBefore, code, env, errOut)

	// 额外一条：前像不可信时权威文件保持目标态、绝不被写回（未进入 Pass B）。
	if got := mustRead(t, cardAbs); string(got) != string(target) {
		t.Fatalf("前像不可信时不应改写权威文件，实得 %q", got)
	}
}

// —— ⑤ 多 OpenTxn ⇒ 退 5 ——

// TestMultipleOpenTxnExits5：盘上同时存在两笔未闭合事务 ⇒ 全集裁决整体阻断（E15）⇒ 退 5、
// 两笔现场都保持未闭合（谁都不许被单独回滚）、无 W26。
func TestMultipleOpenTxnExits5(t *testing.T) {
	dir := applyVault(t)
	_, cardRel := applyNoteAndCard(t, dir)
	pre := mustRead(t, filepath.Join(dir, filepath.FromSlash(cardRel)))

	id1 := seedOpenTxn(t, dir, cardRel, pre)
	id2 := seedOpenTxn(t, dir, cardRel, pre)

	authBefore, txnsBefore, headBefore, logBefore := barrierBaselines(t, dir)
	code, env, errOut := runApplyPlan(t, dir, cardPlan("k-20260901-after-multiopen", ""))
	// 用 id1 走通用断言组（含「现场未闭合」），再单独补 id2 的未闭合断言。
	assertBarrierExit5(t, dir, id1, "多 OpenTxn",
		authBefore, txnsBefore, headBefore, logBefore, code, env, errOut)
	if markerExists(dir, id2, txn.AbortMarker) || markerExists(dir, id2, txn.CommitMarker) {
		t.Fatalf("多 OpenTxn 整体阻断：第二笔事务 %s 也必须保持未闭合", id2)
	}
}

// —— ⑥ 合法组合：先 W26（S2 干净回滚）后 S3 退 5（写前强校验）——

// TestRecoverW26ThenStrictPrecheckExit5：一笔**可干净回滚**的未闭合事务先在 S2 被回滚并产出
// 一条 W26，随后同一次命令带 `--strict` 的 S3 写前强校验命中升级面再退 5。
//
// 这钉的是「W26 与退出码 5 并存合规」这条边界：二者来自编排的不同阶段（S2 恢复 vs S3 强校验），
// 报告体里 W26 一条不少，errors[] 里 E15 + 被升级的 W 码一样不缺，谁都不该把谁吞掉。
// 与前五支的差别在于：这一支的 S2 **成功**（故有 W26、报告照产、退出码 5 走 PrecheckFailed 分支），
// 而前五支的 S2 **失败**（故无 W26、报告缺席、退出码 5 走 blockedError 分支）。
func TestRecoverW26ThenStrictPrecheckExit5(t *testing.T) {
	dir := applyVault(t)
	landNote(t, dir)
	_, cardRel := applyNoteAndCard(t, dir)
	cardAbs := filepath.Join(dir, filepath.FromSlash(cardRel))
	pre := mustRead(t, cardAbs)

	// 造一笔可干净回滚的未闭合事务：权威文件保持前像（B-R1，全部在前像 ⇒ 回滚为 0 还原 + W26）。
	stale := seedOpenTxn(t, dir, cardRel, pre)

	authBefore := authoritySnapshot(t, dir)
	headBefore := gitOut(t, dir, "rev-parse", "HEAD")
	logBefore := gitLogCount(t, dir)

	// w4CardPlan + --strict：W4 在 strict 下升 error，S3 写前强校验退 5。
	code, env, errOut := runApplyPlan(t, dir, w4CardPlan(), "--strict")
	if code != ExitPrecheckOrLock {
		t.Fatalf("退出码 = %d，期望 5（S2 回滚后 S3 写前强校验拦下）：%s", code, errOut)
	}

	// ① S2 的 W26 如实进报告（本次确实回滚了一笔崩溃残留）。
	rep := applyReport(t, env)
	if !reportHasCode(rep, txn.CodeTxnRecovered) {
		t.Fatalf("先回滚过未闭合事务 %s，报告必须留一条 %s：%v",
			stale, txn.CodeTxnRecovered, warnCodesOf(rep))
	}
	// ② S3 的退 5 诊断齐备：errors[] 同时含 E15 与被升级的 W4。
	diags := errorDiagsOf(t, env)
	if !diagsHaveCode(diags, txn.CodePrecheckFailed) {
		t.Fatalf("errors[] 必须含写前强校验码 %s：%+v", txn.CodePrecheckFailed, diags)
	}
	if !diagsHaveCode(diags, "W4") {
		t.Fatalf("errors[] 必须点名被升级的 W4：%+v", diags)
	}
	// ③ 被回滚的那笔已闭合（写了 abort），而本次强校验请求零权威写、不开新事务、HEAD 不动。
	if !markerExists(dir, stale, txn.AbortMarker) {
		t.Fatalf("可干净回滚的未闭合事务 %s 应在 S2 被写下 abort", stale)
	}
	assertAuthorityUnchanged(t, dir, authBefore, "S2 回滚后 S3 退 5")
	if got := gitOut(t, dir, "rev-parse", "HEAD"); got != headBefore {
		t.Fatalf("写前强校验失败不得移动 HEAD：%q → %q", headBefore, got)
	}
	if got := gitLogCount(t, dir); got != logBefore {
		t.Fatalf("写前强校验失败不得产生 commit：%d → %d", logBefore, got)
	}
}
