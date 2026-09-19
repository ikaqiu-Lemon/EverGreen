package cli

// `eg opinion validate|reject` 的**用法面 / 退出码面 / 授权面**机器判据（读路径 CLI 拆分设计 §5.4；
// 原 T-…-006 批次 D 定型的形态合同）。
//
// 形态合同（位置参数 / <o-id> 形态 / flag 分域 / 非空 reason / 缺 --user-request → E19）在此逐格锁死；
// 生命周期状态机与写入事务已由 T-007 接线（见 opinion_lifecycle_transaction_test.go）。本文件所有用例
// 都以空库不存在的 <o-id> 驱动：要么在形态 / 授权面提前退出（退 1 / 退 2），要么越过授权后止步于锁内
// S3 resolve（目标不存在 → 退 2、E18），因此**每一格都零写入零 commit**，可专注钉住决策顺序与授权边界。
//
// 锁死的判据（每条都在真实临时 vault 上驱动真实命令；事实只回读 git 自己与 eg 自己的 --json 信封）：
//
//	① 父命令注册 --reason，但**只允许** validate/reject 使用：search / show 显式带 --reason → 退 1
//	   （flag 分域，逐字点名 --reason；绝不静默接受）。
//	② validate/reject **必带非空 --reason**：缺 / 空串 / 纯空白 → 用法错退 1（逐字点名 --reason），
//	   且**先于**授权判定（即便已带 --user-request 也仍退 1）。
//	③ 判定顺序（逐字锁）：位置参数 → <o-id> 形态 → flag 分域 → 缺/空 reason（以上均退 1）→
//	   缺 --user-request（退 2）→ 授权齐备进入**已接线**的生命周期事务（本文件以空库不存在的
//	   <o-id> 驱动，止步于锁内 S3 resolve → 退 2、E18、零写入）。
//	④ 授权面：参数合法但缺 --user-request → 退 2，JSON data.errors[] **恰 1 条** E19 error，
//	   逐字段 code=E19 / level=error / path=--user-request / target=<o-id> / op_index=-1，零写入零 commit。
//	⑤ 授权齐备（非空 reason + --user-request）→ 越过 Validate 与授权判定，进入**已接线**的生命周期
//	   事务（锁内 S3 resolve）；本文件以空库不存在的 <o-id> 驱动，故止步于目标不存在 → 退 2、E18、
//	   零写入零 commit（据此证明命令已穿透骨架边界，而非再停在 NotWired）。
//	⑥ 全程零副作用：信封之外，权威 Markdown / Git 工作区 / commit 数逐字不变（本文件用例均在写入前退出）。

import (
	"encoding/json"
	"strings"
	"testing"
)

// runOpinionCLIJSON 跑一次 `eg opinion … --json` 并解出信封（供 E19 逐字段与信封断言）。
// 与 runOpinionCLI（人类可读）同源，只是把 stdout 解成 Envelope；stderr 一并回传便于失败取证。
func runOpinionCLIJSON(t *testing.T, dir string, args ...string) (int, Envelope, string) {
	t.Helper()
	setGitIdentity(t)
	r := newTestRoot(t, dir)
	full := append([]string{"opinion"}, args...)
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

// opinionErrorDiags 把信封 data.errors[] 解成 []Diagnostic（error 级逐字段反证用）。
func opinionErrorDiags(t *testing.T, env Envelope) []Diagnostic {
	t.Helper()
	raw, ok := env.Data["errors"]
	if !ok {
		return nil
	}
	blob, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("data.errors 不可 marshal：%v", err)
	}
	var diags []Diagnostic
	if err := json.Unmarshal(blob, &diags); err != nil {
		t.Fatalf("data.errors 不可解成 []Diagnostic：%v（%s）", err, blob)
	}
	return diags
}

// —— ① --reason 只允许 validate/reject：search / show 显式带 --reason → 退 1、零副作用 ——

func TestOpinionSearchShowRejectReasonFlag(t *testing.T) {
	dir := captureVault(t)
	statusBefore, logBefore := opinionVaultSnapshot(t, dir)

	const validID = "o-20260101-demo"
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"search 带 --reason", []string{"search", "语言模型", "--reason", "不该出现在检索路径"}},
		{"show 带 --reason", []string{"show", validID, "--reason", "不该出现在查看路径"}},
	} {
		code, _, errOut := runOpinionCLI(t, dir, tc.args...)
		if code != ExitUsage {
			t.Fatalf("[%s] 退出码 = %d，期望 1（--reason 只作用于 validate/reject）：%s",
				tc.name, code, errOut)
		}
		if strings.Contains(errOut, "尚未挂载") {
			t.Fatalf("[%s] 应止步于 flag 分域用法错，而非放行到 NotWired：%s", tc.name, errOut)
		}
		if !strings.Contains(errOut, "不接受 --reason") {
			t.Fatalf("[%s] 用法错须逐字点名 --reason（拒绝可判定）：%s", tc.name, errOut)
		}
	}

	if statusAfter, logAfter := opinionVaultSnapshot(t, dir); statusAfter != statusBefore || logAfter != logBefore {
		t.Fatal("拒绝 --reason 的路径改变了工作区或 commit 数（必须零写入）")
	}
}

// —— ② validate/reject 必带非空 --reason：缺 / 空 → 退 1，且**先于**授权判定 ——

func TestOpinionValidateRejectRequireNonEmptyReason(t *testing.T) {
	dir := captureVault(t)
	statusBefore, logBefore := opinionVaultSnapshot(t, dir)

	const validID = "o-20260101-demo"
	for _, tc := range []struct {
		name string
		args []string
	}{
		// 带 --user-request 但缺 reason：仍退 1（缺/空 reason 先于授权判定，绝不冒名成退 2）。
		{"validate 缺 --reason（带 --user-request）", []string{"validate", validID, "--user-request"}},
		{"validate --reason 空串", []string{"validate", validID, "--reason", "", "--user-request"}},
		{"validate --reason 纯空白", []string{"validate", validID, "--reason", "   ", "--user-request"}},
		{"reject 缺 --reason", []string{"reject", validID}},
		{"reject --reason 空串（带 --user-request）", []string{"reject", validID, "--reason", "", "--user-request"}},
	} {
		code, _, errOut := runOpinionCLI(t, dir, tc.args...)
		if code != ExitUsage {
			t.Fatalf("[%s] 退出码 = %d，期望 1（缺/空 reason 属用法错）：%s", tc.name, code, errOut)
		}
		if strings.Contains(errOut, "尚未挂载") {
			t.Fatalf("[%s] 缺/空 reason 应先判用法错，绝不放行到 NotWired：%s", tc.name, errOut)
		}
		if !strings.Contains(errOut, "需要 --reason") {
			t.Fatalf("[%s] 用法错须逐字点名 --reason（缺/空理由）：%s", tc.name, errOut)
		}
	}

	if statusAfter, logAfter := opinionVaultSnapshot(t, dir); statusAfter != statusBefore || logAfter != logBefore {
		t.Fatal("缺/空 reason 路径改变了工作区或 commit 数（必须零写入）")
	}
}

// —— ③ 判定顺序逐字锁：flag 分域 → 缺/空 reason → 缺 --user-request → 授权齐备进入已接线事务 ——

func TestOpinionValidateRejectDecisionOrder(t *testing.T) {
	dir := captureVault(t)
	const validID = "o-20260101-demo"

	// ①a flag 分域**先于**缺 reason：既带读 flag（--domain）又缺 reason → 用法错须点名该读 flag。
	code, _, errOut := runOpinionCLI(t, dir, "validate", validID, "--domain", "tech")
	if code != ExitUsage {
		t.Fatalf("validate 带读 flag 退出码 = %d，期望 1（flag 分域）：%s", code, errOut)
	}
	if !strings.Contains(errOut, "不接受 --domain") {
		t.Fatalf("既带读 flag 又缺 reason 时，flag 分域应先判（点名 --domain）：%s", errOut)
	}

	// ①b <o-id> 形态**先于** reason / 授权：坏 ID + 缺 reason + 缺 --user-request → 仍退 1（ID 形态）。
	code, _, errOut = runOpinionCLI(t, dir, "validate", "o-bad")
	if code != ExitUsage {
		t.Fatalf("validate 坏 ID 退出码 = %d，期望 1（ID 形态先判）：%s", code, errOut)
	}
	if strings.Contains(errOut, "尚未挂载") {
		t.Fatalf("坏 ID 应止步于 ID 形态用法错，而非放行到 NotWired：%s", errOut)
	}

	// ② 缺 reason **先于**缺 --user-request：给了 --user-request 但缺 reason → 退 1（不进授权判定退 2）。
	code, _, errOut = runOpinionCLI(t, dir, "reject", validID, "--user-request")
	if code != ExitUsage {
		t.Fatalf("reject 缺 reason（带 --user-request）退出码 = %d，期望 1（reason 先于授权判定）：%s",
			code, errOut)
	}

	// ③ reason 齐备但缺 --user-request → 越过 Validate 进授权判定 → 授权失败退 2。
	code, _, errOut = runOpinionCLI(t, dir, "reject", validID, "--reason", "论证不成立")
	if code != ExitValidation {
		t.Fatalf("reject reason 齐备缺 --user-request 退出码 = %d，期望 2（E19 授权失败）：%s",
			code, errOut)
	}

	// ④ reason + --user-request 齐备 → 越过授权判定进入**已接线**的生命周期事务；本用例 <o-id> 在
	//    空库里解析不到，故止步于锁内 S3 resolve → 退 2（E18、零写入），而**不再**是骨架期的 NotWired。
	code, _, errOut = runOpinionCLI(t, dir, "reject", validID, "--reason", "论证不成立", "--user-request")
	if code != ExitValidation {
		t.Fatalf("reject 授权齐备退出码 = %d，期望 2（已接线：越过授权后锁内解析不到目标观点）：%s", code, errOut)
	}
	if strings.Contains(errOut, "尚未挂载") {
		t.Fatalf("reject 授权齐备不得再是 NotWired 骨架（生命周期已接线）：%s", errOut)
	}
	if !strings.Contains(errOut, "解析不到") {
		t.Fatalf("reject 授权齐备应越过授权判定、进入锁内 resolve 并因目标不存在退 2（E18）：%s", errOut)
	}
}

// —— ④ 授权面：缺 --user-request → 退 2，JSON data.errors[] 恰 1 条 E19，逐字段 + 零副作用 ——

func TestOpinionValidateRejectMissingUserRequestE19(t *testing.T) {
	dir := captureVault(t)
	statusBefore, logBefore := opinionVaultSnapshot(t, dir)

	const validID = "o-20260101-demo"
	for _, sub := range []string{"validate", "reject"} {
		code, env, combined := runOpinionCLIJSON(t, dir, sub, validID, "--reason", "证据已充分复核")
		if code != ExitValidation {
			t.Fatalf("eg opinion %s 缺 --user-request 退出码 = %d，期望 2（授权失败）：%s",
				sub, code, combined)
		}
		// 信封由退出码唯一派生：exit_code=2 / status=failed / ok=false。
		if env.ExitCode != ExitValidation || env.Status != StatusFailed || env.OK {
			t.Fatalf("eg opinion %s 信封与退出码不一致：exit_code=%d status=%q ok=%t",
				sub, env.ExitCode, env.Status, env.OK)
		}
		// data.errors[] **恰 1 条** E19 error，逐字段反证（机读侧据此定位到 --user-request 这一格）。
		diags := opinionErrorDiags(t, env)
		if len(diags) != 1 {
			t.Fatalf("eg opinion %s data.errors[] 应恰 1 条，实得 %d：%+v", sub, len(diags), diags)
		}
		d := diags[0]
		if d.Code != E19 {
			t.Fatalf("eg opinion %s E19 逐字段：code=%q，期望 %q", sub, d.Code, E19)
		}
		if d.Level != LevelError {
			t.Fatalf("eg opinion %s E19 逐字段：level=%q，期望 %q", sub, d.Level, LevelError)
		}
		if d.Path != "--"+UserRequestFlag {
			t.Fatalf("eg opinion %s E19 逐字段：path=%q，期望 --%s", sub, d.Path, UserRequestFlag)
		}
		if d.Target != validID {
			t.Fatalf("eg opinion %s E19 逐字段：target=%q，期望 %q", sub, d.Target, validID)
		}
		if d.OpIndex != NonOpDiagnostic {
			t.Fatalf("eg opinion %s E19 逐字段：op_index=%d，期望 %d（非 op 级）",
				sub, d.OpIndex, NonOpDiagnostic)
		}
		if strings.Contains(combined, "尚未挂载") {
			t.Fatalf("eg opinion %s 缺授权应止步于 E19 退 2，绝不放行到 NotWired：%s", sub, combined)
		}
		if statusAfter, logAfter := opinionVaultSnapshot(t, dir); statusAfter != statusBefore || logAfter != logBefore {
			t.Fatalf("eg opinion %s 授权失败路径改变了工作区或 commit 数（必须零写入零 commit）", sub)
		}
	}
}

// —— ⑤ 授权齐备 → 生命周期**已接线**：非空 reason + --user-request 越过 Validate / guard / 授权判定，
// 进入锁内 S3 resolve。本用例目标 <o-id> 不在空库中，故退 2（E18、零写入零 commit），据此证明命令
// 已**穿透骨架边界**接上真实事务，而非再停在 NotWired。 ——

func TestOpinionValidateRejectAuthorizedReachesWiredLifecycle(t *testing.T) {
	dir := captureVault(t)
	statusBefore, logBefore := opinionVaultSnapshot(t, dir)

	const validID = "o-20260101-demo"
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"validate 授权齐备", []string{"validate", validID, "--reason", "证据已充分复核", "--user-request"}},
		{"reject 授权齐备", []string{"reject", validID, "--reason", "论证不成立", "--user-request"}},
	} {
		code, _, errOut := runOpinionCLI(t, dir, tc.args...)
		if code != ExitValidation {
			t.Fatalf("[%s] 退出码 = %d，期望 2（已接线：越过授权后锁内解析不到目标观点）：%s",
				tc.name, code, errOut)
		}
		// 必须**不再**是 NotWiredError：证明合法且授权齐备的形态确实穿过了 Validate、guard 与授权
		// 判定，进入了已挂载的生命周期事务（锁内 S3 resolve），只是止步于目标不存在（E18），
		// 而非止步于未挂载的状态机。
		if strings.Contains(errOut, "尚未挂载") {
			t.Fatalf("[%s] 授权齐备不得再是 NotWired 骨架（生命周期已接线）：%s", tc.name, errOut)
		}
		if !strings.Contains(errOut, "解析不到") {
			t.Fatalf("[%s] 授权齐备应进入锁内 resolve 并因目标不存在退 2（E18）：%s", tc.name, errOut)
		}
		statusAfter, logAfter := opinionVaultSnapshot(t, dir)
		if statusAfter != statusBefore {
			t.Fatalf("[%s] 改变了工作区（解析不到目标必须零文件变化）：%q → %q", tc.name, statusBefore, statusAfter)
		}
		if logAfter != logBefore {
			t.Fatalf("[%s] 产生了 commit（解析不到目标必须零 commit）", tc.name)
		}
	}
}

// —— A2 · --reopen 分域合同：只 validate 接受；reject / search / show 显式带即退 1、逐字点名、零副作用 ——
//
// 设计出处：观点 schema v2 设计 §6.1 状态机 + §6.2「回到 pending（复议）复用 eg opinion validate --reopen，
// 避免再加命令」。本组钉住 --reopen 的**参数面与分域合同**：validate 接受它（授权齐备后进入已接线的
// 生命周期事务），reject / search / show 一律显式拒绝（逐字点名 --reopen）。
//
// 关键判定顺序（逐字锁）：--reopen 分域**先于** reason / 授权判定。因此
//   - reject --reopen（哪怕同时缺 reason）→ 退 1 且点名 --reopen，绝不冒名成缺 reason 用法错；
//   - validate --reopen 路径仍是「reason 先于授权」：缺 reason 退 1（点名 --reason）、reason 齐备缺
//     --user-request 退 2（E19）、reason + --user-request 齐备越过授权进入已接线事务（本文件空库
//     解析不到目标 → 退 2、E18）。

func TestOpinionReopenRejectedOutsideValidate(t *testing.T) {
	dir := captureVault(t)
	statusBefore, logBefore := opinionVaultSnapshot(t, dir)

	const validID = "o-20260101-demo"
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"search 带 --reopen", []string{"search", "语言模型", "--reopen"}},
		{"show 带 --reopen", []string{"show", validID, "--reopen"}},
		// reject 带 --reopen 且缺 reason：--reopen 分域**先于**缺 reason，须点名 --reopen（不冒名成缺 reason）。
		{"reject 带 --reopen（缺 reason）", []string{"reject", validID, "--reopen"}},
		// reject 带 --reopen 且 reason 齐备：仍因 --reopen 不属 reject 分域而退 1、点名 --reopen。
		{"reject 带 --reopen（reason 齐备）", []string{"reject", validID, "--reason", "论证不成立", "--reopen"}},
	} {
		code, _, errOut := runOpinionCLI(t, dir, tc.args...)
		if code != ExitUsage {
			t.Fatalf("[%s] 退出码 = %d，期望 1（--reopen 只作用于 validate）：%s", tc.name, code, errOut)
		}
		if strings.Contains(errOut, "尚未挂载") {
			t.Fatalf("[%s] 应止步于 flag 分域用法错，而非放行到 NotWired：%s", tc.name, errOut)
		}
		if !strings.Contains(errOut, "不接受 --reopen") {
			t.Fatalf("[%s] 用法错须逐字点名 --reopen（拒绝可判定）：%s", tc.name, errOut)
		}
		if strings.Contains(errOut, "需要 --reason") {
			t.Fatalf("[%s] --reopen 分域应先于缺 reason 判定，不得冒名成缺 reason：%s", tc.name, errOut)
		}
	}

	if statusAfter, logAfter := opinionVaultSnapshot(t, dir); statusAfter != statusBefore || logAfter != logBefore {
		t.Fatal("拒绝 --reopen 的路径改变了工作区或 commit 数（必须零写入）")
	}
}

// —— A2 · validate 接受 --reopen：判定顺序 reason 先于授权；授权齐备后进入已接线事务、零副作用 ——

func TestOpinionValidateReopenDecisionOrder(t *testing.T) {
	dir := captureVault(t)
	statusBefore, logBefore := opinionVaultSnapshot(t, dir)
	const validID = "o-20260101-demo"

	// ① validate --reopen 缺 reason（哪怕带 --user-request）：reason **先于**授权 → 退 1、点名 --reason
	//    （绝不冒名成授权失败退 2，也绝不把 validate 的 --reopen 判成分域拒绝）。
	code, _, errOut := runOpinionCLI(t, dir, "validate", validID, "--reopen", "--user-request")
	if code != ExitUsage {
		t.Fatalf("validate --reopen 缺 reason 退出码 = %d，期望 1（reason 先于授权）：%s", code, errOut)
	}
	if !strings.Contains(errOut, "需要 --reason") {
		t.Fatalf("validate --reopen 缺 reason 须点名 --reason：%s", errOut)
	}
	if strings.Contains(errOut, "不接受 --reopen") {
		t.Fatalf("validate 必须**接受** --reopen，不得点名拒绝它：%s", errOut)
	}

	// ② validate --reopen + reason 齐备但缺 --user-request → 授权失败退 2，data.errors[] 恰 1 条 E19。
	code2, env, combined := runOpinionCLIJSON(t, dir, "validate", validID, "--reopen", "--reason", "出现新反例")
	if code2 != ExitValidation {
		t.Fatalf("validate --reopen reason 齐备缺 --user-request 退出码 = %d，期望 2（E19）：%s", code2, combined)
	}
	diags := opinionErrorDiags(t, env)
	if len(diags) != 1 || diags[0].Code != E19 {
		t.Fatalf("validate --reopen 缺授权 data.errors[] 应恰 1 条 E19，实得 %+v", diags)
	}

	// ③ validate --reopen 授权齐备（非空 reason + --user-request）→ 越过授权判定进入**已接线**的
	//    生命周期事务；本用例目标 <o-id> 不在空库中，故止步于锁内 S3 resolve → 退 2（E18、零写入），
	//    据此证明 --reopen 授权齐备形态已穿透骨架接上真实状态机，而非再停在 NotWired。
	code3, _, errOut3 := runOpinionCLI(t, dir, "validate", validID, "--reopen", "--reason", "出现新反例", "--user-request")
	if code3 != ExitValidation {
		t.Fatalf("validate --reopen 授权齐备退出码 = %d，期望 2（已接线：解析不到目标观点）：%s", code3, errOut3)
	}
	if strings.Contains(errOut3, "尚未挂载") {
		t.Fatalf("validate --reopen 授权齐备不得再是 NotWired 骨架（生命周期已接线）：%s", errOut3)
	}
	if !strings.Contains(errOut3, "解析不到") {
		t.Fatalf("validate --reopen 授权齐备应进入锁内 resolve 并因目标不存在退 2（E18）：%s", errOut3)
	}

	if statusAfter, logAfter := opinionVaultSnapshot(t, dir); statusAfter != statusBefore || logAfter != logBefore {
		t.Fatal("validate --reopen 全路径改变了工作区或 commit 数（本批零写入零 commit）")
	}
}
