// M3 验收的 Go 断言集（T-evergreen.s1_main_flow-158614-047）。
//
// 只覆盖「shell 不易表达」的四类闭合性断言（判据 8 / 11 / 15 / 17 的机器可判部分），
// 不重复 24 个子脚本已覆盖的行为：
//  1. TestExitCodeSetClosed          退出码全集 == {0,1,2,3,4,5,6}，`5` 自 M6 启用、白名单恰两条命令；
//  2. TestDiagnosticCodesCovered     诊断码 ⊆ E1..E10 ∪ W1..W12 ∪ {I1}，且 E7–E10 / W9–W12
//     各被至少一个用例覆盖，`skipped[].kind` 不新增；
//  3. TestM3OpsAllExecutable         8 个新增 op 逐个可派发且各有 e2e 落点；
//  4. TestWritePermissionMatrixCounts 43 行矩阵：43 / 严格解锁 16 / 条件解锁 1 / 两路径同 🔴 5。
//
// 另加两条「验收自身可复算」的守卫：总控脚本覆盖 17 条判据且按固定顺序串起 24 个子脚本；
// M1 / M2 历史用例名仍在源码里（不回归的机器可判部分）；M3 期真实会话留痕在盘且无回放替代物。
//
// 本文件不改任何产品实现，也不新造口径：判据原文见 `milestones/M-003-m3.md`。
package e2e

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/cli"
	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/plan"
)

// repoRootT047 返回 evergreen 仓根（本文件位于 <root>/test/e2e/）。
func repoRootT047(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

func readT047(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s：%v", path, err)
	}
	return string(raw)
}

// walkGoT047 遍历目录下的 .go 文件，onlyProd 为真时跳过 _test.go。
func walkGoT047(t *testing.T, dir string, onlyProd bool, fn func(path, body string)) {
	t.Helper()
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(p, ".go") {
			return nil
		}
		if onlyProd && strings.HasSuffix(p, "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		fn(p, string(raw))
		return nil
	})
	if err != nil {
		t.Fatalf("遍历 %s：%v", dir, err)
	}
}

// TestExitCodeSetClosed：退出码全集恰 {0,1,2,3,4,5,6}，`5` 自 M6（T-074）启用（判据 11 / 17）。
func TestExitCodeSetClosed(t *testing.T) {
	root := repoRootT047(t)

	// ① 常量侧：源码里声明的 Exit* 常量值集合。
	decl := regexp.MustCompile(`(?m)^(?:const )?\s*Exit[A-Za-z]* = (\d+)\s*$`)
	got := map[int]bool{}
	for _, f := range []string{
		filepath.Join(root, "internal", "cli", "exit.go"),
		filepath.Join(root, "internal", "cli", "exitcode.go"),
	} {
		for _, m := range decl.FindAllStringSubmatch(readT047(t, f), -1) {
			n, err := strconv.Atoi(m[1])
			if err != nil {
				t.Fatalf("解析退出码常量 %q：%v", m[1], err)
			}
			got[n] = true
		}
	}
	want := []int{0, 1, 2, 3, 4, 5, 6}
	if len(got) != len(want) {
		keys := make([]int, 0, len(got))
		for k := range got {
			keys = append(keys, k)
		}
		sort.Ints(keys)
		t.Fatalf("退出码常量集合 %v，应恰 %v", keys, want)
	}
	for _, w := range want {
		if !got[w] {
			t.Fatalf("退出码集合缺 %d（应恰 %v）", w, want)
		}
	}
	// ② 值侧：常量取值与语义逐个对齐。
	for name, pair := range map[string][2]int{
		"ExitOK":           {cli.ExitOK, 0},
		"ExitUsage":        {cli.ExitUsage, 1},
		"ExitValidation":   {cli.ExitValidation, 2},
		"ExitPartialWrite": {cli.ExitPartialWrite, 3},
		"ExitCommitFailed": {cli.ExitCommitFailed, 4},
		"ExitNeedConfirm":  {cli.ExitNeedConfirm, 6},
	} {
		if pair[0] != pair[1] {
			t.Fatalf("%s == %d，应为 %d", name, pair[0], pair[1])
		}
	}
	// ③ `5` 自 M6（T-074）起启用为**常量** ExitPrecheckOrLock=5，但**仍无进程级 5 号直退**：
	// 退出码到进程的翻译只发生在 cmd/eg/main.go 的一处 generic 退出翻译，任何文件都不得出现
	// 字面量的 5 号进程直退（探针串拼接构造，避免自身被 internal/cli 的「只有 main 可以调
	// 进程退出」守卫误伤）。M3 这条历史不变量被 T-074 按合同 §17 重钉：启用位翻真、字面量禁令不放宽。
	probe := "os." + "Exit(5)"
	if !cli.ExitCode5Enabled() {
		t.Fatal("ExitCode5Enabled() 自 M6 起恒 true（退出码 5 已由 T-074 按合同 §17.1 启用）")
	}
	for _, dir := range []string{filepath.Join(root, "internal"), filepath.Join(root, "cmd")} {
		walkGoT047(t, dir, true, func(p, body string) {
			if strings.Contains(body, probe) {
				t.Fatalf("%s 出现 %s：5 仍由 main 统一 generic 翻译，不得有字面量 5 号直退", p, probe)
			}
		})
	}
	// ④ 6 的启用白名单恰两条命令，且判定顺序为 参数错 1 → 校验失败 2 → 缺确认 6。
	if wl := cli.NeedConfirmCommands(); len(wl) != 2 ||
		wl[0] != cli.CmdDelete || wl[1] != cli.CmdProposalApprove {
		t.Fatalf("退出码 6 白名单 %v，应恰 {delete, proposal approve}", wl)
	}
	if got := cli.ExitCodeForConfirm(cli.ConfirmDecision{
		ArgsValid: false, ValidationPassed: false, ConfirmMissing: true}); got != cli.ExitUsage {
		t.Fatalf("参数错时应先给 1，实得 %d", got)
	}
	if got := cli.ExitCodeForConfirm(cli.ConfirmDecision{
		ArgsValid: true, ValidationPassed: false, ConfirmMissing: true}); got != cli.ExitValidation {
		t.Fatalf("校验失败时应给 2，实得 %d", got)
	}
	if got := cli.ExitCodeForConfirm(cli.ConfirmDecision{
		ArgsValid: true, ValidationPassed: true, ConfirmMissing: true}); got != cli.ExitNeedConfirm {
		t.Fatalf("仅缺确认时应给 6，实得 %d", got)
	}
}

// TestDiagnosticCodesCovered：诊断码集合闭合，E7–E10 / W9–W12 各被至少一个用例覆盖（判据 15）。
func TestDiagnosticCodesCovered(t *testing.T) {
	root := repoRootT047(t)

	// ① 闭合集合恰 24 值。
	//
	// **Schema v2 · T-…-003 重钉（事实变了，判据形态不变）**：知识 / 观点分离契约 D-9 把
	// `W21`（structure_coverage，`write_note.blocks[]` 的结构覆盖诊断）正式发放给 ChangePlan 域，
	// 于是闭合集合由 23 变 **24 = E1..E10 ∪ W1..W12 ∪ {W21} ∪ {I1}**。`E1`–`E10` / `W1`–`W12` /
	// `I1` 的语义、编号与顺序一字未动，新码只在 warning 末位追加 —— 判据仍是**双侧等号**
	// （多一码 / 少一码都判红），且下面 ② 另加一域把 `W21` 的落点收窄到 `internal/plan`。
	const m3ClosedCodes, planV2NewCodes = 23, 1
	all := plan.AllCodes()
	if len(all) != m3ClosedCodes+planV2NewCodes {
		t.Fatalf("plan.AllCodes() 共 %d 个，应恰「M3 期 %d + Schema v2 新增 %d = %d」"+
			"（E1..E10 ∪ W1..W12 ∪ {W21} ∪ {I1}）",
			len(all), m3ClosedCodes, planV2NewCodes, m3ClosedCodes+planV2NewCodes)
	}
	closed := map[string]bool{}
	for _, c := range all {
		closed[c] = true
	}
	for i := 1; i <= 10; i++ {
		if !closed["E"+strconv.Itoa(i)] {
			t.Fatalf("闭合集合缺 E%d", i)
		}
	}
	for i := 1; i <= 12; i++ {
		if !closed["W"+strconv.Itoa(i)] {
			t.Fatalf("闭合集合缺 W%d", i)
		}
	}
	if !closed["I1"] {
		t.Fatal("闭合集合缺 I1")
	}
	// Schema v2 新增的那一码逐字是 `W21`（契约 D-9），不是别的空号：漏发 / 改号都判红。
	if !closed[plan.W21] || plan.W21 != "W"+strconv.Itoa(21) {
		t.Fatalf("闭合集合缺 W21（Schema v2 · 契约 D-9 的 structure_coverage），实得 %v", all)
	}

	// ② 源码里出现的每个码都在闭合集合内。
	//
	// **重钉理由（事实变了，不是放宽）**：原判据是「internal/ 全库出现的码必须落在 M3 闭合
	// 23 值内，E11 / W13 / I2 之类一律不许出现」。M4 · T-…-049 起，对账合同 §3 把
	// E11–E14 / W13–W20 **正式发放**给 `internal/reconcile` 的十二值 check 枚举 —— 那批码在
	// 该包内是合同要求存在的事实。判据形态改为**按包分域**，判据本体（越界码不许出现）逐条
	// 保留且反证面变严：`internal/reconcile` 之外仍**恰**闭合在 M3 的 23 值（一格不放宽）；
	// 包内只许 M4 已发放的 12 个码，E15+ / W21+ / I2+ 仍零命中；且 M4 码必须真实出现在该包内
	// （否则分域失去事实基础，立即判红）。
	m4Codes := map[string]bool{}
	for i := 11; i <= 14; i++ {
		m4Codes["E"+strconv.Itoa(i)] = true
	}
	for i := 13; i <= 20; i++ {
		m4Codes["W"+strconv.Itoa(i)] = true
	}
	// **knowledge_opinion_split · A-62 追加（同一手法，只增不改）**：对账合同 §3 把 `W29`
	// （opinion_unsupported_validated，R3 第五项）正式发放给 internal/reconcile 的 check 枚举，
	// 唯一字面量落点是 internal/reconcile/check.go 的 CodeW29 常量（其余包只引用该常量、无字面量）。
	// 因此把 W29 并入 reconcile 分域允许集；W29 出现在 reconcile 之外的任何包仍判越界（双侧等号一格不放宽）。
	m4Codes["W29"] = true
	// **M5 · T-…-065 追加一域（同一手法，只增不改）**：M5 索引架构合同 A-45 把
	// `W22`–`W25` / `Q5` 发放给 S4 索引面，其中 T-…-065 只实际启用 **W23 / W24**
	// （索引缺失 / 索引不可用两态）。分域表因此从一行变两行，两行都是双侧等号。
	//
	// **T-…-066 阶段 B 精确重钉**：本 task 按合同 §5.2 / §6.3 正式启用 **W22**
	// （index_stale，索引落后于权威），落点仍恰在 S4 索引包内。因此本域允许集合
	// 从 `W23 / W24` 变为 `W22 / W23 / W24`，其余一格不放宽：
	// `internal/index` 之外这三个码恒零命中（命令层引用一律走 index.CodeIndex* 常量），
	// 包内 `W20` / `W21` / `W25` / `E15+` / `I2+` 仍零命中（`W25` 与 `Q5` 属读路径接入，
	// 在下游 T-…-067），且包内必须真实出现本域码。
	// **M5 · T-…-069 追加第三域（同一手法，只增不改）**：`W25`（result_truncated）由
	// T-…-068 按合同 §7.4 / A-47 落地，唯一落点 `internal/query/page.go`。上面 T-…-066 那段
	// 已写明「`W25` 与 `Q5` 属读路径接入，在下游」——本 task 只把这笔**既成事实**登记进表
	// （T-…-068 落地时漏登，M5 收口在此补齐）。本域允许集合恰 `{W25}`：`internal/query`
	// 内出现 `W22` / `W23` / `W24` / `W20` / `W21` / `E11+` / `I2+` 一律零命中（读路径引用
	// 索引域三码必须走 index.CodeIndex* 常量），`internal/query` 之外 `W25` 零命中，
	// 且包内必须真实出现 `W25`。
	m5Codes := map[string]bool{"W22": true, "W23": true, "W24": true}
	queryCodes := map[string]bool{"W25": true}
	// **M6 · S5 事务域（同一手法，只增不改）**：M6 原子性与强校验合同 §12 把新增诊断码恰
	// 5 条（E15 / E16 / W26 / W27 / W28）发放给 M6。截至 T-…-072（批次 B1）的 S5 事务包已实际
	// 启用 **E15 / E16 / W26 / W28** 四码，落点恰在 `internal/txn`（其中 W26 由 recover.go 的真实
	// 崩溃恢复回滚路径产出——合同 §12 与 T-…-072 明确：真实恢复产 W26，W28 仅锁等待重试，二者不得
	// 互换；四个字面量只在该包常量块出现，其余落点走常量引用）。本域允许集合精确闭合为
	// `{E15, E16, W26, W28}`：
	//   - `internal/txn` 之外这四码恒零命中（其它包引用必须走该包的 Code* 常量，不许抄字面量）；
	//   - 包内 `W27`（T-…-073 保留，尚未启用）与 E17+ / W29+ / I2+ 仍零命中；
	//   - 且包内必须真实出现本域码（否则分域失去事实基础，立即判红）。
	txnCodes := map[string]bool{"E15": true, "E16": true, "W26": true, "W28": true}
	// **M6 · T-…-073 追加块级安全合并域（同一手法，只增不改）**：合同 §12 把 `W27`
	// （block_merge_conflict）的唯一字面量落点发放给 `internal/mdfile`（判定内核 block_merge.go，
	// 常量 CodeBlockMergeConflict 唯一持有；store 侧接线只走该常量、不抄字面量）。上面事务域那段
	// 「W27（T-…-073 保留，尚未启用）」由本 task 转为已发放。本域允许集合恰 `{W27}`：
	//   - `internal/mdfile` 之外 `W27` 恒零命中（store 侧走常量引用，源码无字面量）；
	//   - 包内其余号段（事务域 E15/E16/W26/W28、索引域 W22–W25、M3 已闭合码、E17+/W29+/I2+）仍零命中；
	//   - 且包内必须真实出现 `W27`（否则分域失去事实基础，立即判红）。
	blockMergeCodes := map[string]bool{"W27": true}
	// **C2 · I-…-015 追加第六域（同一手法，只增不改；粒度是「一个文件」而非一个包 = 比前五域更严）**：
	//
	// **重钉理由（事实变了，不是放宽）**：CLI 合同 `2026-09-01-eg-cli-contract.md` §5 的现态脚注
	// （C2 · I-…-015）把命令层 **`E17`–`E25` 恰九个**编号正式发放出去，并把落点**封闭在一个文件**
	// `internal/cli/codes.go`（其余 cli 文件零码字面量、只引用常量名）。发放动因是 §5 原句
	// 「未编号 warning 填 `""`」只对 warning 开放，而实现侧曾把 37 处 **error 级**条目的 `code`
	// 留空、另有 3 处把 `skipped[].cause` 的枚举值塞进 `code` 位 —— 那是 P1/major 缺陷，
	// 修法只能是「给命令层 error 发新号」，**不能**挪用 M3 已冻结的 23 值（A-29 只增不改：
	// `E1`–`E6` = plan 校验、`E7`–`E10` = M3 提案态，语义已冻结，占用即改写既有编号语义）。
	//
	// **本次为什么是「登记漏账」而不是新决定**：同一笔现态重钉在 e2e 脚本侧
	// （`tests/e2e/ops-diagnostics/ops_diagnostics.sh` ④ grep 组 1）已随 I-…-015 落地 ——
	// base 域 `--exclude=codes.go`，并对该文件新增封闭双侧等号 `{E17…E25}`。而本 Go 门禁只在
	// integration / full / race profile 上跑，C2 期间的回归口径是 core，因此漏了同一笔登记，
	// 由 full（run-20260913-160609）暴露。此处把两侧口径对齐，判据本体一格不放宽。
	//
	// **收窄手法（逐条比前五域更严）**：
	//   - 落地面是**单个文件** `internal/cli/codes.go`，不是 `internal/cli/` 整个目录：
	//     E17–E25 出现在 cli 任何其它文件（或任何其它包）仍判越界；
	//   - 该文件内**封闭双侧等号**：只许 `{E17…E25}`，出现任何其它 E / W / I 数字码
	//     （**含 M3 已闭合的 23 值**）当场红 —— 与 S5 事务域同一手法，故本域也**先于**
	//     baseline/closed 放行处理；
	//   - 九码**逐码**各至少出现一次（少一码即红），因此该文件码集合精确等于 `{E17…E25}`；
	//   - `E26+` / `W29+` / `I2+` / `Q*` 仍留给下游 task，出现即红。
	cliCodes := map[string]bool{}
	for i := 17; i <= 25; i++ {
		cliCodes["E"+strconv.Itoa(i)] = true
	}
	cliCodesFile := filepath.Join(root, "internal", "cli", "codes.go")
	m4Owner := filepath.Join(root, "internal", "reconcile") + string(filepath.Separator)
	m5Owner := filepath.Join(root, "internal", "index") + string(filepath.Separator)
	queryOwner := filepath.Join(root, "internal", "query") + string(filepath.Separator)
	txnOwner := filepath.Join(root, "internal", "txn") + string(filepath.Separator)
	mdfileOwner := filepath.Join(root, "internal", "mdfile") + string(filepath.Separator)
	// **Schema v2 · T-…-003 追加 ChangePlan 域（同一手法，只增不改）**：上面几段都写着
	// 「`W21` 仍零命中，留给下游」——本 task 即那位下游。契约 D-9 把 `W21` 发放给 ChangePlan 域，
	// 唯一字面量落点是 `internal/plan/diagnostics.go` 的 `W21` 常量（其余落点一律引用该常量）。
	// 由于 ① 已把 `W21` 纳入闭合集合，若不加这一域，`W21` 就会被 `closed` 无条件放行到全库，
	// 那是**放宽**；因此这一域**先于** closed 处理，仍是双侧等号：
	//   - `internal/plan` 之外 `W21` 恒零命中（命令层 / 索引层引用必须走 plan.W21 常量）；
	//   - `internal/plan` 内必须真实出现 `W21`，否则分域失去事实基础，立即判红。
	planOwner := filepath.Join(root, "internal", "plan") + string(filepath.Separator)
	planSeen := 0
	lit := regexp.MustCompile(`"([EWI][0-9]+)"`)
	m4Seen, m5Seen, querySeen := 0, 0, 0
	// mdfileSeen 记录 internal/mdfile 内 W27 的命中数（块级合并域的事实基础）。
	mdfileSeen := 0
	// txnSeen 逐码记录 E15 / E16 / W26 / W28 各自的命中数：合同要求 M6 事务域**每个码都必须出现**，
	// 单一计数器只能证明「四者任一存在」，无法满足精确集合断言。
	txnSeen := map[string]int{}
	// cliSeen 逐码记录 E17–E25 各自在 internal/cli/codes.go 内的命中数（命令层码域的事实基础）。
	cliSeen := map[string]int{}
	walkGoT047(t, filepath.Join(root, "internal"), true, func(p, body string) {
		inM4Owner := strings.HasPrefix(p, m4Owner)
		inM5Owner := strings.HasPrefix(p, m5Owner)
		inQueryOwner := strings.HasPrefix(p, queryOwner)
		inTxnOwner := strings.HasPrefix(p, txnOwner)
		inMdfileOwner := strings.HasPrefix(p, mdfileOwner)
		inPlanOwner := strings.HasPrefix(p, planOwner)
		inCLICodesFile := p == cliCodesFile
		for _, m := range lit.FindAllStringSubmatch(body, -1) {
			// ChangePlan 域（Schema v2 · 契约 D-9）**先于** baseline/closed 放行：`W21` 只许出现在
			// internal/plan，出现在任何其它包都判红（引用必须走 plan.W21 常量）。
			if m[1] == plan.W21 {
				if !inPlanOwner {
					t.Fatalf("%s 出现 %s：ChangePlan 域的 W21 唯一字面量落点是 internal/plan，"+
						"其它包只许引用 plan.W21 常量", p, m[1])
				}
				planSeen++
				continue
			}
			// S5 事务域**先于** baseline/closed 放行处理：精确闭合为 {E15, E16, W26, W28}，任何其他
			// E / W / I 数字码（含 M3 已闭合的 23 值，以及尚未启用的 W27）都判红 —— 否则「txn 内混入
			// E1 / W1 / W27 也能过」会与下方「internal/txn 精确等于 {E15, E16, W26, W28}」自相矛盾。
			if inTxnOwner {
				if txnCodes[m[1]] {
					txnSeen[m[1]]++
					continue
				}
				t.Fatalf("%s 出现越界诊断码 %s：S5 事务域精确闭合为 {E15 / E16 / W26 / W28}，"+
					"任何其他 E / W / I 数字码（含 M3 已闭合码与尚未启用的 W27）都不许出现在 internal/txn", p, m[1])
			}
			// 命令层码域（C2 · I-…-015）同样**先于** baseline/closed 放行：精确闭合为 {E17…E25}，
			// 任何其他 E / W / I 数字码（含 M3 已闭合的 23 值）出现在 internal/cli/codes.go 都判红 ——
			// 该文件是**纯码表**，混入 base 码就意味着命令层在抄别的包的号，与「各包发各自的码」相悖。
			if inCLICodesFile {
				if cliCodes[m[1]] {
					cliSeen[m[1]]++
					continue
				}
				t.Fatalf("%s 出现越界诊断码 %s：命令层码域精确闭合为 {E17…E25}（CLI 合同 §5 现态脚注 / "+
					"I-…-015），任何其他 E / W / I 数字码（含 M3 已闭合的 23 值）都不许出现在该文件", p, m[1])
			}
			if closed[m[1]] {
				continue
			}
			if inM4Owner && m4Codes[m[1]] {
				m4Seen++
				continue
			}
			if inM5Owner && m5Codes[m[1]] {
				m5Seen++
				continue
			}
			if inQueryOwner && queryCodes[m[1]] {
				querySeen++
				continue
			}
			if inMdfileOwner && blockMergeCodes[m[1]] {
				mdfileSeen++
				continue
			}
			t.Fatalf("%s 出现越界诊断码 %s（M3 闭合 23 值之外；E11–E14 / W13–W20 / W29 只许"+
				"出现在 internal/reconcile，W22 / W23 / W24 只许出现在 internal/index，"+
				"W25 只许出现在 internal/query，E15 / E16 / W26 / W28 只许出现在 internal/txn，"+
				"W27 只许出现在 internal/mdfile，E17–E25 只许出现在 internal/cli/codes.go 这一个文件，"+
				"E26+ / W30+ / I2+ 留给下游 task）", p, m[1])
		}
	})
	if m4Seen == 0 {
		t.Fatal("internal/reconcile 内未见任何 M4 诊断码：分域判据失去事实基础，" +
			"应回落为「全库恰闭合在 M3 的 23 值」的原形态")
	}
	if m5Seen == 0 {
		t.Fatal("internal/index 内未见任何 M5 诊断码：分域判据失去事实基础，" +
			"应回落为「全库恰闭合在 M3 的 23 值」的原形态")
	}
	if querySeen == 0 {
		t.Fatal("internal/query 内未见 W25：分域判据失去事实基础，" +
			"应回落为「全库恰闭合在 M3 的 23 值」的原形态")
	}
	if mdfileSeen == 0 {
		t.Fatal("internal/mdfile 内未见 W27：块级合并分域判据失去事实基础，" +
			"应回落为「全库恰闭合在 M3 的 23 值」的原形态")
	}
	if planSeen == 0 {
		t.Fatal("internal/plan 内未见 W21：ChangePlan 分域判据失去事实基础，" +
			"应回落为「全库恰闭合在 M3 的 23 值」的原形态")
	}
	// M6 事务域：E15 / E16 / W26 / W28 **逐码**各至少出现一次（不满足于「四者任一存在」）。
	// 与上方的越界禁令合起来，即断言 internal/txn 内诊断码字面量精确等于集合 {E15, E16, W26, W28}。
	for _, code := range []string{"E15", "E16", "W26", "W28"} {
		if txnSeen[code] == 0 {
			t.Fatalf("internal/txn 内未见诊断码 %s：M6 事务域要求 E15 / E16 / W26 / W28 "+
				"逐码各至少出现一次（单一计数器只证明四者任一存在，不满足精确集合），"+
				"应回落为「全库恰闭合在 M3 的 23 值」的原形态", code)
		}
	}
	// 命令层码域（C2 · I-…-015）：E17–E25 **逐码**各至少出现一次。与上方「该文件精确闭合为
	// {E17…E25}」的越界禁令合起来，即断言 internal/cli/codes.go 的码字面量集合**精确等于**
	// {E17, E18, E19, E20, E21, E22, E23, E24, E25} —— 少一码（编号有空洞 / 某类 error 又回到
	// 空码）当场红，多一码（命令层私自扩号段）也当场红；与 e2e 脚本侧
	// ops_diagnostics.sh ④ 的 `diff -u want_codes_cmd` 双侧等号同构，两侧互为反证。
	for i := 17; i <= 25; i++ {
		code := "E" + strconv.Itoa(i)
		if cliSeen[code] == 0 {
			t.Fatalf("internal/cli/codes.go 内未见诊断码 %s：命令层码域要求 E17–E25 逐码各至少"+
				"出现一次（CLI 合同 §5 现态脚注「恰九个」+ 编号连续无空洞），"+
				"分域失去事实基础，应回落为「全库恰闭合在 M3 的 23 值」的原形态", code)
		}
	}

	// ③ E7–E10 / W9–W12 各被至少一个用例覆盖（测试源 + e2e 脚本都算用例侧）。
	covered := map[string]bool{}
	mark := func(body string) {
		for _, m := range lit.FindAllStringSubmatch(body, -1) {
			covered[m[1]] = true
		}
		for _, code := range []string{"E7", "E8", "E9", "E10", "W9", "W10", "W11", "W12"} {
			if strings.Contains(body, code) {
				covered[code] = true
			}
		}
	}
	for _, dir := range []string{filepath.Join(root, "internal"), filepath.Join(root, "test")} {
		err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			if strings.HasSuffix(p, "_test.go") || strings.HasSuffix(p, ".sh") {
				raw, rerr := os.ReadFile(p)
				if rerr != nil {
					return rerr
				}
				mark(string(raw))
			}
			return nil
		})
		if err != nil {
			t.Fatalf("遍历 %s：%v", dir, err)
		}
	}
	for _, code := range []string{"E7", "E8", "E9", "E10", "W9", "W10", "W11", "W12"} {
		if !covered[code] {
			t.Fatalf("M3 新增诊断码 %s 没有任何用例覆盖", code)
		}
	}

	// ④ skipped[].kind 不新增：非空取值恰两个，且不得出现块级新造值。
	kinds := map[string]bool{}
	kindLit := regexp.MustCompile(`kind"?:\s*"([a-z_]+)"`)
	walkGoT047(t, filepath.Join(root, "internal"), true, func(p, body string) {
		for _, m := range kindLit.FindAllStringSubmatch(body, -1) {
			kinds[m[1]] = true
		}
		for _, banned := range []string{"block_conflict", "block_hash_changed"} {
			if strings.Contains(body, banned) {
				t.Fatalf("%s 出现新造 skipped kind %q（§8.3 只许复用既有两值）", p, banned)
			}
		}
	})
	for k := range kinds {
		if k != "file_changed" && k != "content_hash_mismatch" {
			t.Fatalf("skipped[].kind 出现集合外取值 %q", k)
		}
	}
}

// TestM3OpsAllExecutable：8 个新增 op 逐个可派发、字段表非空、且各有 e2e 落点（判据 15）。
func TestM3OpsAllExecutable(t *testing.T) {
	root := repoRootT047(t)
	ops := plan.M3OpNames()
	if len(ops) != 8 {
		t.Fatalf("M3 新增 op %d 个，应恰 8", len(ops))
	}
	// `AllOpNames` 按**加法等式**钉死（2026-09-07 随 M4 · T-…-055 按实测重钉）：
	//   M3 期 16（S1 七 + M3 八 + edit_section 一，**历史事实，一格不改写**）
	// + M4 新增 1（R6 的 `set_stale`，A-33）= 17。
	// 只改判据形态、不放宽本体：M3 期那 8 个 op 的逐个可派发 / 字段表非空 / e2e 落点三条
	// 断言逐字未动；下面另新增两格加严（M4 新增面恰 1 个且名字逐字 `set_stale`、
	// 且它**不**混进 M3 面），任何往 op 全集偷加一个都会立刻红。
	//
	// **Schema v2 · T-…-003 再次重钉（同一手法，事实变了，判据形态不变）**：契约 §4.4 把主链路
	// 写口由 S1 的七个扩为 **九个** —— `create_card` / `append_card` 改名为
	// `create_knowledge` / `append_knowledge`（**改名，不是新增**；旧名以兼容别名保留、
	// 在解析末尾被改写，不计入名册），并**新增** `create_opinion` / `append_opinion`
	// 两个 Opinion 写口。于是加法等式由「16 + 1 = 17」写成逐项形态
	// 「主链路 9 + M3 8 + 编辑 1 + M4 1 = 19」，差额恰等于新增的两个 Opinion op，一个不多。
	// M3 面恒 8、状态类恒 5、编辑恒 1、M4 恒 1 四个计数一律不动；下面另新增三格加严
	// （主链路恰九个且逐字含两个 Opinion op、两个兼容别名不得进名册、Opinion op 不得混进
	// M3 / 状态类 / M4 三个面），任何往 op 全集偷加一个仍会立刻红。
	const mainOps, m3Ops, editOps, m4NewOps = 9, 8, 1, 1
	wantAllOps := mainOps + m3Ops + editOps + m4NewOps
	if all := plan.AllOpNames(); len(all) != wantAllOps {
		t.Fatalf("plan.AllOpNames() %d 个，应恰「主链路 %d + M3 %d + 编辑 %d + M4 %d = %d」：%v",
			len(all), mainOps, m3Ops, editOps, m4NewOps, wantAllOps, all)
	}
	if main := plan.OpNames(); len(main) != mainOps {
		t.Fatalf("主链路 op %d 个，应恰 %d（契约 §4.4）：%v", len(main), mainOps, main)
	}
	for _, op := range []string{plan.OpCreateOpinion, plan.OpAppendOpinion} {
		if !opInList(plan.OpNames(), op) {
			t.Fatalf("主链路名册缺 Opinion 写口 %q（契约 §4.4）：%v", op, plan.OpNames())
		}
		if plan.IsM3Op(op) || plan.IsStateOp(op) || plan.IsM4Op(op) {
			t.Fatalf("%q 属 Schema v2 主链路，不得混进 M3OpNames %v / StateOpNames %v / M4OpNames %v",
				op, plan.M3OpNames(), plan.StateOpNames(), plan.M4OpNames())
		}
	}
	// 兼容别名恰两个，且**不**在名册里：它们不是独立 op，只是同一个 op 的旧名字。
	if aliases := plan.OpAliases(); len(aliases) != 2 {
		t.Fatalf("兼容别名应恰两个（create_card / append_card），实得 %v", aliases)
	}
	for alias, canonical := range plan.OpAliases() {
		if opInList(plan.OpNames(), alias) || opInList(plan.AllOpNames(), alias) {
			t.Fatalf("兼容别名 %q 不得进入 op 名册（主链路 %v / 全集 %v）",
				alias, plan.OpNames(), plan.AllOpNames())
		}
		if !opInList(plan.OpNames(), canonical) {
			t.Fatalf("别名 %q 的规范名 %q 必须在主链路名册里：%v", alias, canonical, plan.OpNames())
		}
	}
	if m4 := plan.M4OpNames(); len(m4) != m4NewOps || m4[0] != plan.OpSetStale ||
		plan.OpSetStale != "set_stale" {
		t.Fatalf("M4 新增 op 应恰 %d 个且逐字为 set_stale，实得 %v", m4NewOps, m4)
	}
	if plan.IsM3Op(plan.OpSetStale) {
		t.Fatalf("%s 属 M4，不得混进 M3OpNames %v（M3 期恒 8 是历史事实）",
			plan.OpSetStale, plan.M3OpNames())
	}
	dispatch := map[string]bool{}
	for _, n := range plan.AllOpNames() {
		dispatch[n] = true
	}
	// e2e 侧证据：把**全部** e2e 资产拼起来，逐个 op 名找落点。迁移后语料由两处组成：
	//   ① 运行期 test/e2e —— Go 端到端用例（tests/_staged 回填）；
	//   ② tests/e2e/<capability> —— 按能力重组后的 shell 场景脚本。
	// 语料只增不减：历史上这两类文件都住在 test/e2e 一个目录，故这里遍历两处等价于原判据。
	var corpus strings.Builder
	for _, dir := range []string{
		filepath.Join(root, "test", "e2e"),
		filepath.Join(root, "tests", "e2e"),
	} {
		err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || strings.Contains(p, string(os.PathSeparator)+"testdata"+string(os.PathSeparator)) {
				return nil
			}
			raw, rerr := os.ReadFile(p)
			if rerr != nil {
				return rerr
			}
			corpus.Write(raw)
			return nil
		})
		if err != nil {
			t.Fatalf("遍历 %s：%v", dir, err)
		}
	}
	body := corpus.String()
	for _, op := range ops {
		if !dispatch[op] {
			t.Fatalf("op %q 不在 AllOpNames() 派发全集内", op)
		}
		if fields := plan.M3OpFields(op); len(fields) == 0 {
			t.Fatalf("op %q 的字段表为空", op)
		}
		if !plan.IsM3Op(op) {
			t.Fatalf("IsM3Op(%q) 应为真", op)
		}
		if !strings.Contains(body, op) {
			t.Fatalf("op %q 在 test/e2e 与 tests/e2e 下都没有执行落点", op)
		}
	}
	// 状态类 op 窄口径恰五个（W7 升 error 的适用面）。
	if st := plan.StateOpNames(); len(st) != 5 {
		t.Fatalf("状态类 op %d 个，应恰 5（A-15 窄口径）", len(st))
	}
}

// opInList 报告 name 是否逐字出现在 list 里（op 名册断言的小工具，不做任何归一化：
// 名册判据要的就是「逐字相等」，任何 trim / 大小写折叠都会把漂移掩盖过去）。
func opInList(list []string, name string) bool {
	for _, v := range list {
		if v == name {
			return true
		}
	}
	return false
}

// TestWritePermissionMatrixCounts：矩阵行数与 §2.8 的三个计数（判据 8 / 9 / 10）。
//
// **Schema v2 · T-…-003 重钉（事实变了，判据形态不变）**：知识 / 观点分离契约 §3.3 在矩阵尾部
// **追加** 7 行（#44–#50 = Note v2 的 `整理正文` / `提取结果` 两分区 + Opinion 五分区），
// 于是 43 行 / 86 格 / 7 个对象类分别变 **50 / 100 / 8**，条件解锁由 1 变 **2**（新增 #46
// 与 #12 同口径），两路皆拒由 4 变 **5**（新增 #50，Opinion 的 `用户补充`，B2 安全底线）。
// 追加而非改名 #23/#24/#27 的理由见 matrix.go 行注释：存量笔记里 v1 分区仍在，改名会让
// LookupRow 对存量分区查无此格、把合法追加拦死。
// 判据本体一格不放宽：路径恒 2、严格解锁恒 16（新增七行无「P-A 🔴 且 P-U ✅」形态）、
// #33 的四格断言与「#33 不再落进三个集合」逐字保留；下面另新增两格加严
// （#46 恰是「P-A 条件解锁 / P-U ✅」、#50 恰是两路皆拒）。
func TestWritePermissionMatrixCounts(t *testing.T) {
	// 行数与格数按**加法等式**钉死：合同 §2.8 的 43 是历史事实、一格不改写；契约 §3.3 新增 7。
	const m3Rows, planV2Rows = 43, 7
	wantRows := m3Rows + planV2Rows
	if got := len(plan.Matrix()); got != wantRows {
		t.Fatalf("写权限矩阵 %d 行，应恰「合同 §2.8 的 %d + 契约 §3.3 的 %d = %d」",
			got, m3Rows, planV2Rows, wantRows)
	}
	if got := plan.MatrixCells(); got != wantRows*2 {
		t.Fatalf("矩阵格数 %d，应恰 %d（%d × 2 条路径）", got, wantRows*2, wantRows)
	}
	if got := len(plan.Paths()); got != 2 {
		t.Fatalf("写入路径 %d 条，应恰 2（P-A / P-U）", got)
	}
	if got := len(plan.StrictUnlockRows()); got != 16 {
		t.Fatalf("严格「P-A 🔴 → P-U ✅」%d 行，应恰 16（契约 §3.3 新增七行均无此形态）", got)
	}
	// 条件解锁：M3 期恰 1 行（#12 知识卡「知识内容」）+ 契约 §3.3 新增 1 行（#46 观点「观点」，
	// §3.3 明确「同 知识内容 口径」）= 2，且两行的行号逐字钉死、顺序即矩阵行序。
	const m3Conditional, planV2Conditional = 1, 1
	cond := plan.ConditionalUnlockRows()
	if len(cond) != m3Conditional+planV2Conditional {
		t.Fatalf("条件解锁 %d 行，应恰「M3 期 %d（#12）+ 契约 §3.3 新增 %d（#46）= %d」",
			len(cond), m3Conditional, planV2Conditional, m3Conditional+planV2Conditional)
	}
	for i, wantNum := range []int{12, 46} {
		if cond[i].Num != wantNum {
			t.Fatalf("条件解锁第 %d 行是 #%d，应为 #%d", i+1, cond[i].Num, wantNum)
		}
	}
	// 新增的那一行**恰**是 Opinion 的「观点」分区，且**恰**与 #12 同形态：
	// P-A 既有 🔴 又有 ✅（对已有观点 🔴、create_opinion 新建 ✅），P-U 纯 ✅。
	row46, ok := plan.LookupRow(plan.ObjectOpinion, plan.SectionField(mdfile.SecOpinionClaim))
	if !ok || row46.Num != 46 {
		t.Fatalf("矩阵里找不到 #46（%s · 分区「观点」），实得 %+v", plan.ObjectOpinion, row46)
	}
	if !row46.Auto.Has(plan.VerdictDeny) || !row46.Auto.Has(plan.VerdictAllow) ||
		!row46.User.Has(plan.VerdictAllow) || row46.User.Has(plan.VerdictDeny) {
		t.Fatalf("#46 应为 P-A 条件解锁（🔴 + ✅）/ P-U ✅，与 #12 同口径，实得 %s", row46)
	}
	// 两路径同 🔴 按**加减法等式**钉死（2026-09-07 随 M4 · T-…-055 按实测重钉）：
	//   M3 期 5（历史事实，**一格不改写**）− 依 A-34 放开 1（矩阵 #33 的 P-A，R6 由对账
	// 自动写 stale/stale_reason，不需要 --user-request）= 4；
	// 再 + 契约 §3.3 新增 1（#50 观点「用户补充」，B2：CLI 写入路径对四类实体一律永不写）= 5。
	// 只改判据形态、不放宽本体：放开的那一格**恰**是 #33 且**恰**是 P-A（P-U 仍 🔴）、
	// #33 既不进严格解锁也不进条件解锁、且离开同 🔴 集合的**恰**这一行，三条逐字未动。
	const m3BothDenied, a34Unlocked, planV2BothDenied = 5, 1, 1
	wantBoth := m3BothDenied - a34Unlocked + planV2BothDenied
	both := plan.BothDeniedRows()
	if len(both) != wantBoth {
		t.Fatalf("两路径同 🔴 %d 行，应恰「M3 期 %d − 依 A-34 放开 %d + 契约 §3.3 新增 %d = %d」",
			len(both), m3BothDenied, a34Unlocked, planV2BothDenied, wantBoth)
	}
	row50, ok := plan.LookupRow(plan.ObjectOpinion, plan.SectionField(mdfile.SecUserAppend))
	if !ok || row50.Num != 50 {
		t.Fatalf("矩阵里找不到 #50（%s · 分区「用户补充」），实得 %+v", plan.ObjectOpinion, row50)
	}
	if row50.Auto.Has(plan.VerdictAllow) || !row50.Auto.Has(plan.VerdictDeny) ||
		row50.User.Has(plan.VerdictAllow) || !row50.User.Has(plan.VerdictDeny) {
		t.Fatalf("#50 应两路径同 🔴（B2：CLI 永不写「用户补充」），实得 %s", row50)
	}
	row33, ok := plan.LookupRow(plan.ObjectReview, plan.FieldReviewStale)
	if !ok || row33.Num != 33 {
		t.Fatalf("矩阵里找不到 #33（%s · %s），实得 %+v",
			plan.ObjectReview, plan.FieldReviewStale, row33)
	}
	// 放开的那一格恰 P-A 且恰一个 ✅；P-U 必须仍是纯 🔴。
	if !row33.Auto.Has(plan.VerdictAllow) || row33.Auto.Has(plan.VerdictDeny) ||
		!row33.User.Has(plan.VerdictDeny) || row33.User.Has(plan.VerdictAllow) {
		t.Fatalf("#33 应为 P-A ✅ / P-U 🔴（A-34），实得 %s", row33)
	}
	for _, set := range []struct {
		name string
		rows []plan.MatrixRow
	}{
		{"两路径同 🔴", both},
		{"严格解锁", plan.StrictUnlockRows()},
		{"条件解锁", cond},
	} {
		for _, r := range set.rows {
			if r.Num == 33 {
				t.Fatalf("#33 放开后不应再落进「%s」集合，实得 %s", set.name, r)
			}
		}
	}
	// 对象类：合同 §2.8 的 7 类（历史事实）+ 契约 §3.3 的 ObjectOpinion 一类 = 8。
	// 观点是与知识**同级**的第四类实体，故必须是独立对象类，不能挂在 ObjectCard 下
	// （复用会让日后放开观点某一格时连带放开知识卡的同名格）。
	const m3Objects, planV2Objects = 7, 1
	if got := len(plan.MatrixObjects()); got != m3Objects+planV2Objects {
		t.Fatalf("矩阵对象类 %d 个，应恰「合同 §2.8 的 %d + 契约 §3.3 的 %d = %d」",
			got, m3Objects, planV2Objects, m3Objects+planV2Objects)
	}
	var opinionObjects int
	for _, o := range plan.MatrixObjects() {
		if o == plan.ObjectOpinion {
			opinionObjects++
		}
	}
	if opinionObjects != 1 {
		t.Fatalf("对象类里 %s 应恰出现一次，实得 %d 次：%v",
			plan.ObjectOpinion, opinionObjects, plan.MatrixObjects())
	}
}

// m3AcceptanceSubScripts 是总控脚本必须按固定顺序串起的 24 个子脚本
// （M1 恰 1 + M2 恰 9 + M3 恰 14；总控脚本自身不自调，故 M3 期 25 − 1 = 24）。
//
// 2026-09-06 随 M4 · T-…-050 按实测重钉（只改口径说明，名单一条未删、顺序一格未放宽）：
// 磁盘 e2e 总数由 25 变 **26**（新增 M4 的 `m4_r1_takeover.sh`）；2026-09-06 随 M4 · T-…-052
// 再由 26 变 **27**（新增 M4 的 `m4_r4_structure.sh`，R4 结构检查三项只读）；2026-09-06 随
// M4 · T-…-051 再由 27 变 **28**（新增 M4 的 `m4_r2_reviewed_backfill.sh`，R2 `reviewed_at`
// 补齐）；2026-09-06 随 M4 · T-…-053 再由 28 变 **29**（新增 M4 的 `m4_r3_relation.sh`，
// R3 关系校验四子检查，只报告不自动修）；2026-09-06 随 M4 · T-…-054 再由 29 变 **30**
// （新增 M4 的 `m4_r5_domain_moved.sh`，R5 手工跨领域移动检测 W18，只报告 + targets 恰三元
// 顺序固定）。M3 总控脚本**不串**
// M4 脚本 —— 它验收的是 M3 的 17 条判据，M4 的判据归 M4 的收敛点（T-…-063）。因此本名单
// 恒 24 条，且下面的 TestM3AcceptanceScriptCoversAllCriteria 额外反证「总控脚本里不出现
// 任何 m4_ 脚本」，防 M4 脚本被悄悄塞进 M3 验收凑绿。
//
// 2026-09-07 随 M4 · T-…-058 阶段 3 同步旁注（**本名单一条未删、顺序一格未放宽**）：磁盘
// e2e 总数由 33 变 **34**（M4 期 8 → 9，新增 `m4_cmd_reconcile.sh` —— `eg reconcile` 命令
// 本体端到端）。中间的 30 → 31（T-…-056 `m4_r7_support.sh`）、31 → 32（T-…-055
// `m4_r6_recap_stale.sh`）、32 → 33（T-…-057 `m4_report_reconcile.sh`）三次变更同属 M4 期
// 新增，一并在此登记。本名单仍恒 24 条（M1 1 + M2 9 + M3 14，总控脚本自身不自调）——
// M4 期新增多少个脚本都**不得**进这份 M3 名单。
var m3AcceptanceSubScripts = []string{
	"m1_real_article.sh",
	"m2_acceptance.sh", "m2_card_show.sh", "m2_context_polish.sh", "m2_convergence.sh",
	"m2_docs_commands.sh", "m2_ppe_replay.sh", "m2_rel_add.sh", "m2_rel_query.sh", "m2_search.sh",
	"m3_proposal_layout.sh", "m3_proposal_state.sh", "m3_ops_diagnostics.sh", "m3_superseded.sh",
	"m3_execution_failed.sh", "m3_authorization.sh", "m3_lifecycle_state.sh", "m3_proposal_cli.sh",
	"m3_logical_delete.sh", "m3_reviewed.sh", "m3_markers.sh", "m3_rel_remove.sh",
	"m3_edit.sh", "m3_docs_commands.sh",
}

// TestM3AcceptanceScriptCoversAllCriteria：17 条判据标记齐备 + 24 个子脚本在场且顺序固定。
func TestM3AcceptanceScriptCoversAllCriteria(t *testing.T) {
	root := repoRootT047(t)
	// 迁移后：M3 聚合器归档到 tests/archive/history/stage-acceptance/（判据标记与三值口径逐字保全），
	// 24 个子脚本按能力搬到 tests/e2e/<capability>/；两侧路径均由迁移表解析。
	body := readT047(t, migratedAbs(t, root, "test/e2e/m3_acceptance.sh"))

	for i := 1; i <= 17; i++ {
		marker := "[判据 " + strconv.Itoa(i) + "]"
		if got := strings.Count(body, marker); got != 1 {
			t.Fatalf("m3_acceptance.sh 中 %q 出现 %d 次，应恰 1 次", marker, got)
		}
	}
	for _, want := range []string{"PASS", "FAIL", "NOT-VERIFIED", "M3 结论：", "checks=", "failed="} {
		if !strings.Contains(body, want) {
			t.Fatalf("m3_acceptance.sh 缺少 %q", want)
		}
	}
	pos := -1
	for _, s := range m3AcceptanceSubScripts {
		if _, err := os.Stat(migratedAbs(t, root, "test/e2e/"+s)); err != nil {
			t.Fatalf("子脚本缺失 %s（迁移后路径 %s）：%v", s, migratedRel(t, root, "test/e2e/"+s), err)
		}
		at := strings.Index(body, s)
		if at < 0 {
			t.Fatalf("m3_acceptance.sh 未调起子脚本 %s", s)
		}
		if at <= pos {
			t.Fatalf("子脚本 %s 的调起顺序与固定顺序不一致", s)
		}
		pos = at
	}
	// M4 分域反证（2026-09-06 随 T-…-050 新增，纯加严）：M4 期的 e2e 脚本确实在盘
	// （否则本反证失去事实基础），但 M3 总控脚本一个都不得**调起**——M3 的 17 条判据不许拿
	// M4 脚本凑绿，M4 的验收归 M4 自己的收敛点。判定只看**非注释行**：总控脚本的重钉旁注
	// 里必须写清「新增的是哪个 M4 脚本」，那是留痕而不是调用。
	// 迁移后：M4 期脚本已按能力改名（tests/e2e/<capability>/），其**历史文件名**由迁移表给出，
	// 并逐个校验迁移后仍在盘 —— 事实基础不减；「M3 聚合器不得调起 M4 脚本」这一侧用历史名判定，
	// 因为归档件里引用的就是历史名。
	m4 := historicalStageScripts(t, root, "m4")
	if len(m4) == 0 {
		t.Fatal("迁移表里没有任何 m4 阶段脚本：M4 分域反证失去事实基础")
	}
	var code []string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		code = append(code, line)
	}
	codeBody := strings.Join(code, "\n")
	for _, p := range m4 {
		if name := filepath.Base(p); strings.Contains(codeBody, name) {
			t.Fatalf("m3_acceptance.sh 调起了 M4 脚本 %s（M3 验收不得拿 M4 凑绿）", name)
		}
	}
	// 越界 / 占位 / 措辞 / 留痕四组门禁必须都在脚本里成文。
	// 门禁项里的进程退出、文件删除与 M2 占位探针同样分片拼接（同 internal/cli 的守卫口径）：
	// 整串写死会让本文件与被测脚本自身被 teamwork 侧「占位零残留」门禁判成残留。
	for _, want := range []string{"reconcile", "flock", "run\\.lock", "FTS5", "os." + "Exit(5)",
		"os." + "Remove", "checkout --", "reset --hard", "PLACE_LIT", "M3/S2 未" + `"` + `"` + "实现",
		"跳过 hash 比对", "自动回滚到执行前", "Agent 自动路径亦可改", "m3-raw-session"} {
		if !strings.Contains(body, want) {
			t.Fatalf("m3_acceptance.sh 缺门禁项 %q", want)
		}
	}
}

// TestM3RawSessionTraceInPlace：M3 期真实会话留痕在盘、plan 内 M3 op ≥ 3、无回放替代物。
func TestM3RawSessionTraceInPlace(t *testing.T) {
	root := repoRootT047(t)
	dir := filepath.Join(root, "test", "e2e", "testdata", "ppe", "m3-raw-session")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("M3 期会话留痕目录缺失：%v", err)
	}
	if len(entries) < 1 {
		t.Fatal("M3 期会话留痕目录为空")
	}
	planBody := readT047(t, filepath.Join(dir, "m3-ops-plan.json"))
	distinct := map[string]bool{}
	for _, op := range plan.M3OpNames() {
		if strings.Contains(planBody, "\""+op+"\"") {
			distinct[op] = true
		}
	}
	if len(distinct) < 3 {
		t.Fatalf("会话 plan 内不同 M3 op %d 个，应 ≥ 3", len(distinct))
	}
	// M2 会话 ID 不得被冒充成 M3 的证据：会话记录里必须出现一个不同的 ID。
	trace := readT047(t, filepath.Join(dir, "session.md"))
	uuid := regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	own := false
	for _, id := range uuid.FindAllString(trace, -1) {
		if id != "56cc0392-ffa1-41ee-9b89-21d7ae5c0199" {
			own = true
		}
	}
	if !own {
		t.Fatal("会话记录里没有独立于 M2 的 M3 期会话 ID")
	}
	// 无回放替代物。
	globbed, err := filepath.Glob(filepath.Join(root, "test", "e2e", "m3_*replay*.sh"))
	if err != nil {
		t.Fatalf("glob：%v", err)
	}
	if len(globbed) != 0 {
		t.Fatalf("出现 m3 回放脚本 %v：门禁 8 不接受回放替代真实会话", globbed)
	}
}
