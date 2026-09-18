package cli

// `eg opinion validate|reject` **骨架合同**的机器判据（读路径 CLI 拆分设计 §5.4；T-…-006 批次 D）。
//
// 本批**只补合同、不接状态机**：validate/reject 的用法面、退出码面、授权面全部落定，但业务实现
// （观点验证生命周期状态机、任何写入）归 T-007，本骨架一格不碰 store / plan / txn。
//
// 锁死的判据（每条都在真实临时 vault 上驱动真实命令；事实只回读 git 自己与 eg 自己的 --json 信封）：
//
//	① 父命令注册 --reason，但**只允许** validate/reject 使用：search / show 显式带 --reason → 退 1
//	   （flag 分域，逐字点名 --reason；绝不静默接受）。
//	② validate/reject **必带非空 --reason**：缺 / 空串 / 纯空白 → 用法错退 1（逐字点名 --reason），
//	   且**先于**授权判定（即便已带 --user-request 也仍退 1）。
//	③ 判定顺序（逐字锁）：位置参数 → <o-id> 形态 → flag 分域 → 缺/空 reason（以上均退 1）→
//	   缺 --user-request（退 2）→ 授权齐备仍 NotWired（退 1）。
//	④ 授权面：参数合法但缺 --user-request → 退 2，JSON data.errors[] **恰 1 条** E19 error，
//	   逐字段 code=E19 / level=error / path=--user-request / target=<o-id> / op_index=-1，零写入零 commit。
//	⑤ 授权齐备（非空 reason + --user-request）→ 越过 Validate 与授权判定，止步于未挂载状态机 →
//	   NotWired（退 1、零写入零 commit）。
//	⑥ 全程零副作用：信封之外，权威 Markdown / Git 工作区 / commit 数逐字不变（骨架不接任何写口）。

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

// —— ③ 判定顺序逐字锁：flag 分域 → 缺/空 reason → 缺 --user-request → 授权齐备仍 NotWired ——

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

	// ④ reason + --user-request 齐备 → 越过授权，止步于未挂载状态机 → NotWired 退 1。
	code, _, errOut = runOpinionCLI(t, dir, "reject", validID, "--reason", "论证不成立", "--user-request")
	if code != ExitUsage {
		t.Fatalf("reject 授权齐备退出码 = %d，期望 1（NotWired 骨架）：%s", code, errOut)
	}
	if !strings.Contains(errOut, "尚未挂载") {
		t.Fatalf("reject 授权齐备应止步于 NotWired（业务实现未挂载）：%s", errOut)
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

// —— ⑤ 授权齐备仍 NotWired：非空 reason + --user-request → 退 1、零写入零 commit（不接 store/plan/txn）——

func TestOpinionValidateRejectAuthorizedStillNotWired(t *testing.T) {
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
		if code != ExitUsage {
			t.Fatalf("[%s] 退出码 = %d，期望 1（NotWired 骨架）：%s", tc.name, code, errOut)
		}
		// 必须是 NotWiredError（「业务实现尚未挂载」）——证明合法且授权齐备的形态确实穿过了
		// Validate、guard 与授权判定，止步于**未挂载的状态机**，而非别的用法/授权失败。
		if !strings.Contains(errOut, "尚未挂载") {
			t.Fatalf("[%s] stderr 未含 NotWired 措辞（应止步于未挂载状态机）：%s", tc.name, errOut)
		}
		statusAfter, logAfter := opinionVaultSnapshot(t, dir)
		if statusAfter != statusBefore {
			t.Fatalf("[%s] 改变了工作区（骨架必须零文件变化）：%q → %q", tc.name, statusBefore, statusAfter)
		}
		if logAfter != logBefore {
			t.Fatalf("[%s] 产生了 commit（骨架必须零 commit）", tc.name)
		}
	}
}

// —— A2 · --reopen 分域合同：只 validate 接受；reject / search / show 显式带即退 1、逐字点名、零副作用 ——
//
// 设计出处：观点 schema v2 设计 §6.1 状态机 + §6.2「回到 pending（复议）复用 eg opinion validate --reopen，
// 避免再加命令」。本批**只补 --reopen 的参数面与分域合同、不接状态机**：validate 接受它但授权齐备仍
// NotWired（零写入零 commit），reject / search / show 一律显式拒绝（逐字点名 --reopen）。
//
// 关键判定顺序（逐字锁）：--reopen 分域**先于** reason / 授权判定。因此
//   - reject --reopen（哪怕同时缺 reason）→ 退 1 且点名 --reopen，绝不冒名成缺 reason 用法错；
//   - validate --reopen 路径仍是「reason 先于授权」：缺 reason 退 1（点名 --reason）、reason 齐备缺
//     --user-request 退 2（E19）、reason + --user-request 齐备仍 NotWired 退 1。

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

// —— A2 · validate 接受 --reopen：判定顺序 reason 先于授权；授权齐备仍 NotWired、零副作用 ——

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

	// ③ validate --reopen 授权齐备（非空 reason + --user-request）→ 越过授权，止步于未挂载状态机 →
	//    NotWired 退 1（本批只做参数面，绝不接状态机 / store / plan / txn）。
	code3, _, errOut3 := runOpinionCLI(t, dir, "validate", validID, "--reopen", "--reason", "出现新反例", "--user-request")
	if code3 != ExitUsage {
		t.Fatalf("validate --reopen 授权齐备退出码 = %d，期望 1（NotWired 骨架）：%s", code3, errOut3)
	}
	if !strings.Contains(errOut3, "尚未挂载") {
		t.Fatalf("validate --reopen 授权齐备应止步于未挂载状态机（NotWired）：%s", errOut3)
	}

	if statusAfter, logAfter := opinionVaultSnapshot(t, dir); statusAfter != statusBefore || logAfter != logBefore {
		t.Fatal("validate --reopen 全路径改变了工作区或 commit 数（本批零写入零 commit）")
	}
}
