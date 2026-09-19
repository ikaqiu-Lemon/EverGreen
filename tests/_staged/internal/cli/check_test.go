// `eg check` 命令级用例（M4 · T-evergreen.s1_main_flow-158614-059 阶段 2）。
//
// 本文件只测**命令本体**：检查面恰 R3 / R4、五个被排除的 check 永不产出、恒 0 次提交、
// 三档退出码、以及与 `eg reconcile --dry-run` 的**真子集**关系。检查包（R1–R7）自身的判定
// 用例在 `internal/reconcile/**`，此处一条不重复。
//
// 三条纪律（写在最前面，便于复核）：
//   - **零 mock**：`eg check` 只读、零写入、恒 0 次提交，没有任何「不可稳定复现的失败分支」
//     需要注入，因此全文件**没有一处 mock**（对照 reconcile_test.go 那处 Git Runner 注入）。
//   - **构造的测试输入不是 mock**：`chkAllThirteenFindings` 用产品代码的 `reconcile.NewFinding`
//     真实构造十三值各一条合法 finding，只作为**过滤函数的输入**，不替换任何被测行为。
//     标注见该函数注释（构造测试内容 · owner 2026-09-07 授权）。
//   - **语料都是磁盘真事实**：重复 ID 是逐字节复制出来的真文件；关系异常是把 `relations[]`
//     真实写进 frontmatter（模拟用户绕过 CLI 的外部编辑，这正是 M4 要纳管的那件事）。
package cli

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/reconcile"
)

// —— 独立复述的两份 check 名单（**不引用产品代码派生**）——
//
// 判据要求「check 只跑 R3 / R4」与「五个 check 永不产出」都能被逐字复算。
// 因此这里把两份名单**独立写死**：产品代码那边是从 `reconcile.Specs()` 的 R 列派生的，
// 两侧任一处漂移，下面的等号断言就会红（互为对照，不是互相引用）。
var (
	// chkWantScope 是 `eg check` 应当产出的 8 个 check（R4 三个 + R3 五个，行序 = 合同 §3 表格序；
	// 末位 opinion_unsupported_validated 为 A-62 新增的 R3·W29，追加在真源表尾部）。
	chkWantScope = []string{
		"duplicate_id", "dangling_ref", "orphan",
		"relation_target_missing", "relation_prefix_invalid",
		"relation_opposing_asymmetric", "relation_duplicate",
		"opinion_unsupported_validated",
	}
	// chkWantExcluded 是 `eg check` **永不产出**的 5 个 check（R1 / R2 / R5 / R6 / R7 各一个）。
	chkWantExcluded = []string{
		"git_uncommitted", "reviewed_at_missing",
		"domain_moved", "recap_stale", "support_insufficient",
	}
)

// runCheckCLI 在 dir 上跑一次 `eg check …`（恒带 --json 信封）。
// stdin 恒为已关闭状态：本命令没有任何确认点（退出码 6 不在它的码集里），
// 任何读 stdin 的行为都会当场红。
func runCheckCLI(t *testing.T, dir string, extra ...string) (int, string, string) {
	t.Helper()
	setGitIdentity(t)
	r := newTestRoot(t, dir)
	r.In = closedStdin{}
	r.Now = func() time.Time { return stampAt(t, reconcileFixedNow) }
	args := append([]string{"check", "--vault", dir, "--json"}, extra...)
	return runCLI(t, r, args...)
}

// chkData 取 `data.check` 的原始字节（键序也是合同的一部分，故走 rcRawAt 而非结构体解码）。
func chkData(t *testing.T, out string) []byte {
	t.Helper()
	return rcRawAt(t, []byte(out), "data", "check")
}

// chkFindings 解出 `data.check.findings`（恒非 null；四键投影复用 reconcile_test.go 的 rcFinding）。
func chkFindings(t *testing.T, out string) []rcFinding {
	t.Helper()
	raw := rcRawAt(t, chkData(t, out), "findings")
	if strings.TrimSpace(string(raw)) == "null" {
		t.Fatalf("findings 恒非 null（零条时也必须是 []）：%s", raw)
	}
	var fs []rcFinding
	if err := json.Unmarshal(raw, &fs); err != nil {
		t.Fatalf("findings 不是数组：%v\n%s", err, raw)
	}
	return fs
}

// chkStringSlice 解出 `data.check` 下的一个字符串数组（scope / excluded）。
func chkStringSlice(t *testing.T, out, key string) []string {
	t.Helper()
	var got []string
	if err := json.Unmarshal(rcRawAt(t, chkData(t, out), key), &got); err != nil {
		t.Fatalf("data.check.%s 不是字符串数组：%v", key, err)
	}
	return got
}

// chkNames 取一批 finding 的 check 名（去重升序），供集合比较。
func chkNames(fs []rcFinding) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range fs {
		if !seen[f.Check] {
			seen[f.Check] = true
			out = append(out, f.Check)
		}
	}
	sort.Strings(out)
	return out
}

// chkRelVault 建一个**真实**含关系异常的库（模拟用户绕过 CLI 直接编辑 frontmatter）：
//
//	① 一条 target 查无此对象的关系 → relation_target_missing（E13，error）
//	② 同一条 (from, supports, target) 写两遍   → relation_duplicate（W16，warning）
//
// 这不是 mock：两条异常都是磁盘上的真字节，R3 会如实判定。
// 同时这次外部编辑让工作区变脏 —— 于是 `eg reconcile` 会另外产出 git_uncommitted 与
// reviewed_at_missing 两条 warning，正好成为「真子集」与「五个 check 永不产出」的**非空**对照。
func chkRelVault(t *testing.T) (dir, cardRel string) {
	t.Helper()
	dir, cardRel, _ = reviewedVault(t)
	if code, _, errOut := runApplyPlan(t, dir, cardPlan(applyCard2ID, "")); code != ExitOK {
		t.Fatalf("前置：第二张卡未建成：%s", errOut)
	}
	path := absIn(dir, cardRel)
	body := string(mustRead(t, path))
	const anchor = "sources:"
	idx := strings.Index(body, anchor)
	if idx < 0 {
		t.Fatalf("前置不成立：卡 frontmatter 里找不到 %q 锚点：\n%s", anchor, body)
	}
	rels := "relations:\n" +
		"  - type: 'supports'\n    target: '" + applyCard2ID + "'\n    reason: '重复条目其一'\n" +
		"  - type: 'supports'\n    target: '" + applyCard2ID + "'\n    reason: '重复条目其二'\n" +
		"  - type: 'supports'\n    target: 'k-20991231-missing'\n    reason: '指向不存在的卡'\n"
	if err := os.WriteFile(path, []byte(body[:idx]+rels+body[idx:]), 0o644); err != nil {
		t.Fatalf("写关系异常失败：%v", err)
	}
	return dir, cardRel
}

// chkAllThirteenFindings 用产品代码的 `reconcile.NewFinding` 为**十三个 check 各造一条**合法 finding。
//
// **构造测试内容（owner 2026-09-07 授权）**：这不是 mock —— 它不替换任何被测行为，
// 只是给白名单过滤函数喂一份「十三值全都出现」的输入，从而把「恰留 8 条、五个一条不留」
// 这一格证成封闭等式（否则五个被排除项里只有 R1 / R2 那两个能在命令级语料里稳定出现，
// R5 / R6 / R7 三个要靠各自的 e2e 语料，命令级用例会留下缺口）。
func chkAllThirteenFindings(t *testing.T) []reconcile.Finding {
	t.Helper()
	var out []reconcile.Finding
	for _, spec := range reconcile.Specs() {
		targets := []string{"k-20260901-attention"}
		if spec.Check == "domain_moved" { // targets 顺序固定的封闭例外：恰三元（合同 §8）
			targets = []string{"k-20260901-attention", "ai-infra", "ai"}
		}
		f, err := reconcile.NewFinding(spec.Check, targets, "构造用例输入："+spec.Check)
		if err != nil {
			t.Fatalf("构造 %q 的 finding 失败：%v", spec.Check, err)
		}
		out = append(out, f)
	}
	if len(out) != reconcile.CheckCount {
		t.Fatalf("构造了 %d 条 finding，期望 %d（十三值各一条）", len(out), reconcile.CheckCount)
	}
	return out
}

// —— ① 检查面恰 R3 / R4 八值（派生集合、封闭等式、真实语料三处同时对撞）——

func TestCheckRunsOnlyR3AndR4(t *testing.T) {
	// ① 派生集合与独立名单逐字相等（顺序也锁：真源表行序 = 合同 §3 表格序）。
	if got := CheckScopeChecks(); strings.Join(got, ",") != strings.Join(chkWantScope, ",") {
		t.Fatalf("CheckScopeChecks() = %v，期望逐字有序 %v", got, chkWantScope)
	}
	if got := CheckExcludedChecks(); strings.Join(got, ",") != strings.Join(chkWantExcluded, ",") {
		t.Fatalf("CheckExcludedChecks() = %v，期望逐字有序 %v", got, chkWantExcluded)
	}
	// ② 封闭等式 8 + 5 = 13：任何一侧漂移都会红。
	if CheckScopeCount != len(chkWantScope) || CheckExcludedCount != len(chkWantExcluded) {
		t.Fatalf("封闭计数漂移：CheckScopeCount=%d（期望 %d）、CheckExcludedCount=%d（期望 %d）",
			CheckScopeCount, len(chkWantScope), CheckExcludedCount, len(chkWantExcluded))
	}
	if CheckScopeCount+CheckExcludedCount != reconcile.CheckCount {
		t.Fatalf("%d + %d ≠ %d（check 枚举的封闭基数）",
			CheckScopeCount, CheckExcludedCount, reconcile.CheckCount)
	}
	// ③ 每一个入选 check 的 R 归属必须真的是 R3 或 R4（不是「凑够 8 个」）。
	for _, name := range chkWantScope {
		spec, ok := reconcile.SpecOf(name)
		if !ok {
			t.Fatalf("check %q 不在真源表里", name)
		}
		if spec.R != reconcile.R3 && spec.R != reconcile.R4 {
			t.Fatalf("check %q 的 R 归属 = %q，不属 R3 / R4", name, spec.R)
		}
	}
	// ④ 白名单过滤：十三值全进 → 恰留 8 条，且顺序与真源行序一致。
	kept := checkScopeFindings(chkAllThirteenFindings(t))
	if len(kept) != CheckScopeCount {
		t.Fatalf("过滤后留下 %d 条，期望恰 %d 条", len(kept), CheckScopeCount)
	}
	var keptNames []string
	for _, f := range kept {
		keptNames = append(keptNames, f.Check)
	}
	if strings.Join(keptNames, ",") != strings.Join(CheckScopeChecks(), ",") {
		t.Fatalf("过滤后的 check 序列 = %v，期望 %v", keptNames, CheckScopeChecks())
	}

	// ⑤ 真实语料：三份库跑下来，findings 里出现的 check 一律落在检查面内。
	cases := []struct {
		name  string
		setup func(t *testing.T) string
	}{
		{"clean_vault", func(t *testing.T) string { dir, _, _ := reviewedVault(t); return dir }},
		{"duplicate_id", func(t *testing.T) string { dir, _, _ := rcDupVault(t); return dir }},
		{"relation_anomaly", func(t *testing.T) string { dir, _ := chkRelVault(t); return dir }},
	}
	scope := map[string]bool{}
	for _, n := range chkWantScope {
		scope[n] = true
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := tc.setup(t)
			_, out, errOut := runCheckCLI(t, dir)
			fs := chkFindings(t, out)
			for _, f := range fs {
				if !scope[f.Check] {
					t.Fatalf("产出了检查面外的 check %q（%s / %s）", f.Check, out, errOut)
				}
			}
			// data.check 的两份名单同样逐字对齐（输出面也不许漂移）。
			if got := chkStringSlice(t, out, "scope"); strings.Join(got, ",") !=
				strings.Join(chkWantScope, ",") {
				t.Fatalf("data.check.scope = %v，期望 %v", got, chkWantScope)
			}
			if got := chkStringSlice(t, out, "excluded"); strings.Join(got, ",") !=
				strings.Join(chkWantExcluded, ",") {
				t.Fatalf("data.check.excluded = %v，期望 %v", got, chkWantExcluded)
			}
		})
	}
}

// —— ② 五个 check 永不产出（真实语料 + 封闭等式双侧）——

func TestCheckNeverEmitsFiveChecks(t *testing.T) {
	// ① 真实语料：同一份库上 `eg reconcile --dry-run` **确实**产出了被排除项，
	//    `eg check` 一条都不产 —— 非空对照，避免「反正都没有」的空判。
	dir, _ := chkRelVault(t)
	_, chkOut, chkErr := runCheckCLI(t, dir)
	_, rcOut, rcErr := runReconcileCLI(t, newTestRoot(t, dir), dir, "--dry-run")

	excluded := map[string]bool{}
	for _, n := range chkWantExcluded {
		excluded[n] = true
	}
	for _, f := range chkFindings(t, chkOut) {
		if excluded[f.Check] {
			t.Fatalf("eg check 产出了被排除的 check %q（%s / %s）", f.Check, chkOut, chkErr)
		}
	}
	var seenByReconcile []string
	for _, f := range rcAssertReconcileShape(t, rcRawAt(t, []byte(rcOut), "data", "reconcile")) {
		if excluded[f.Check] {
			seenByReconcile = append(seenByReconcile, f.Check)
		}
	}
	if len(seenByReconcile) == 0 {
		t.Fatalf("前置不成立：本语料下 eg reconcile 也没产出任何被排除项，"+
			"「eg check 不产」是空判（%s / %s）", rcOut, rcErr)
	}

	// ② 封闭等式：十三值全都出现时，五个被排除项**逐值**被滤掉（R5 / R6 / R7 三个
	//    在命令级语料里不易稳定构造，这一格把它们一并锁死）。
	kept := map[string]bool{}
	for _, f := range checkScopeFindings(chkAllThirteenFindings(t)) {
		kept[f.Check] = true
	}
	for _, name := range chkWantExcluded {
		if kept[name] {
			t.Fatalf("白名单过滤没有滤掉被排除的 check %q", name)
		}
	}
	if len(kept) != CheckScopeCount {
		t.Fatalf("过滤后留下 %d 个不同的 check，期望恰 %d 个", len(kept), CheckScopeCount)
	}
}

// —— ③ 恒 0 次提交 + 零写入（四份语料逐份对撞）——

func TestCheckZeroCommitAlways(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T) string
		want  int
	}{
		{"clean_vault", func(t *testing.T) string { dir, _, _ := reviewedVault(t); return dir }, ExitOK},
		{"duplicate_id", func(t *testing.T) string { dir, _, _ := rcDupVault(t); return dir }, ExitValidation},
		{"relation_anomaly", func(t *testing.T) string { dir, _ := chkRelVault(t); return dir }, ExitValidation},
		{"run_twice_idempotent", func(t *testing.T) string {
			dir, _ := chkRelVault(t)
			runCheckCLI(t, dir) // 先跑一次：只读命令跑两次不该有任何累积效应
			return dir
		}, ExitValidation},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := tc.setup(t)
			beforeCommits := gitLogCount(t, dir)
			beforePorcelain := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain"))
			beforeTree := strings.TrimSpace(gitOut(t, dir, "log", "-1", "--format=%T"))

			code, out, errOut := runCheckCLI(t, dir)
			if code != tc.want {
				t.Fatalf("退出码 = %d，期望 %d（%s / %s）", code, tc.want, out, errOut)
			}
			if got := gitLogCount(t, dir); got != beforeCommits {
				t.Fatalf("commit 数 %d → %d，期望**恒不变**（eg check 恒 0 次提交）", beforeCommits, got)
			}
			if got := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); got != beforePorcelain {
				t.Fatalf("工作区状态被改动了：\n前 %q\n后 %q（eg check 零写入）", beforePorcelain, got)
			}
			if got := strings.TrimSpace(gitOut(t, dir, "log", "-1", "--format=%T")); got != beforeTree {
				t.Fatalf("HEAD 树对象 %s → %s，期望不变", beforeTree, got)
			}
			// 报告体里的 commit 恒为 null（只读命令没有提交这一格）。
			if raw := rcRawAt(t, []byte(out), "data", "report", "git", "commit"); strings.TrimSpace(
				string(raw)) != "null" {
				t.Fatalf("report.git.commit = %s，期望 null（恒 0 次提交）", raw)
			}
			// 报告 `reconcile` 三键保持占位（A-41：eg check 不是全库对账）。
			if got := strings.TrimSpace(string(rcRawAt(t, []byte(out), "data", "report",
				"reconcile"))); got != report_ReconcilePlaceholder() {
				t.Fatalf("report.reconcile = %s，期望占位 %s", got, report_ReconcilePlaceholder())
			}
		})
	}
}

// report_ReconcilePlaceholder 复述报告 `reconcile` 三键的占位形态（真源在 internal/report）。
func report_ReconcilePlaceholder() string { return `{"ran":false,"commit":null,"findings":[]}` }

// —— ④ 退出码 0：无 error 级 finding（含只有 warning）——

func TestCheckExitZeroWhenNoError(t *testing.T) {
	dir, _, _ := reviewedVault(t)
	code, out, errOut := runCheckCLI(t, dir)
	if code != ExitOK {
		t.Fatalf("干净库退出码 = %d，期望 0（%s / %s）", code, out, errOut)
	}
	fs := chkFindings(t, out)
	if len(fs) == 0 {
		t.Fatal("前置不成立：本语料需要至少一条 warning 级 finding（否则退 0 是空判）")
	}
	for _, f := range fs {
		if f.Severity == "error" {
			t.Fatalf("语料里出现 error 级 finding %q，本用例前置（只有 warning）不成立", f.Check)
		}
	}
	// 信封恰五键 + 键序，且 data 首键是 check（DataOrder 锁死）。
	var env map[string]interface{}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("--json 输出不可解析：%v（%q）", err, out)
	}
	assertEnvelopeKeys(t, env)
	if got := rcOrderedKeys(t, []byte(out)); strings.Join(got, ",") != strings.Join(EnvelopeKeys(), ",") {
		t.Fatalf("信封键序 = %v，期望逐字 %v", got, EnvelopeKeys())
	}
	if got := rcOrderedKeys(t, rcRawAt(t, []byte(out), "data")); len(got) == 0 || got[0] != "check" {
		t.Fatalf("data 首键 = %v，期望 check 在最前（res.DataOrder）", got)
	}
	// 「未判 ≠ 不存在」这句诚实性交代必须出现在输出里（退 0 最容易被误读成「库是干净的」）。
	if !strings.Contains(out, CheckExcludedNotice) {
		t.Fatalf("输出里缺「本次未判的 5 个 check」这句交代：%s", out)
	}
}

// —— ⑤ 退出码 2：有 error 级 finding，且**零写入零提交**（与 eg reconcile 的差就在这里）——

func TestCheckExitTwoOnErrorFindingZeroWrite(t *testing.T) {
	cases := []struct {
		name      string
		setup     func(t *testing.T) string
		wantCheck string
	}{
		{"duplicate_id_E11", func(t *testing.T) string { dir, _, _ := rcDupVault(t); return dir }, "duplicate_id"},
		{"relation_target_missing_E13", func(t *testing.T) string { dir, _ := chkRelVault(t); return dir },
			"relation_target_missing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := tc.setup(t)
			before := gitLogCount(t, dir)
			beforePorcelain := rcPorcelainCount(t, dir)

			code, out, errOut := runCheckCLI(t, dir)
			if code != ExitValidation {
				t.Fatalf("有 error 级 finding 时退出码 = %d，期望 2（%s / %s）", code, out, errOut)
			}
			// **与 eg reconcile 的关键差**：reconcile 会先完成 R1 纳管 commit（+1）再退 2；
			// check 恒 0 次提交，脏工作区照旧脏（它不纳管、不写盘）。
			if got := gitLogCount(t, dir); got != before {
				t.Fatalf("commit 数 %d → %d，期望不变（eg check 不纳管、不提交）", before, got)
			}
			if got := rcPorcelainCount(t, dir); got != beforePorcelain {
				t.Fatalf("porcelain 行数 %d → %d，期望不变（零写入）", beforePorcelain, got)
			}
			var hit bool
			for _, f := range chkFindings(t, out) {
				if f.Check == tc.wantCheck {
					if f.Severity != "error" {
						t.Fatalf("check %q 的 severity = %q，期望 error（真源恒在检查包）",
							f.Check, f.Severity)
					}
					hit = true
				}
			}
			if !hit {
				t.Fatalf("语料应产出 %q（error 级），实得 %v", tc.wantCheck, chkNames(chkFindings(t, out)))
			}
			// 退 2 时 data.errors[] 必须能定位到问题（诊断码取自检查包的单射表）。
			if !strings.Contains(out, `"level":"error"`) {
				t.Fatalf("退 2 但 data.errors[] 里没有 error 级诊断：%s", out)
			}
		})
	}
}

// —— ⑥ 与 `eg reconcile --dry-run` 的**真子集**关系（同一份库上逐字比对）——

func TestCheckIsStrictSubsetOfReconcileDryRun(t *testing.T) {
	dir, _ := chkRelVault(t)

	// 先跑 check（只读），再跑 reconcile --dry-run（同样零写入）——两次都不改变磁盘，
	// 因此两份 findings 面对的是**同一个**库状态，可以逐字比对。
	beforeCommits := gitLogCount(t, dir)
	codeChk, chkOut, chkErr := runCheckCLI(t, dir)
	codeRc, rcOut, rcErr := runReconcileCLI(t, newTestRoot(t, dir), dir, "--dry-run")
	if got := gitLogCount(t, dir); got != beforeCommits {
		t.Fatalf("两条只读命令跑完 commit 数 %d → %d，期望不变", beforeCommits, got)
	}
	if codeChk != ExitValidation || codeRc != ExitValidation {
		t.Fatalf("两侧都应因 error 级 finding 退 2：check=%d reconcile=%d（%s / %s / %s / %s）",
			codeChk, codeRc, chkOut, chkErr, rcOut, rcErr)
	}

	chkFs := chkFindings(t, chkOut)
	rcFs := rcAssertReconcileShape(t, rcRawAt(t, []byte(rcOut), "data", "reconcile"))
	if len(chkFs) == 0 {
		t.Fatal("前置不成立：check 侧零 finding，子集关系是空判")
	}

	// ① 逐条（四键全等）都能在 reconcile 侧找到同一条：check 不自造事实、不改写事实。
	key := func(f rcFinding) string {
		return strings.Join([]string{f.Check, f.Severity, strings.Join(f.Targets, "\x00"), f.Detail}, "\x1f")
	}
	rcSet := map[string]bool{}
	for _, f := range rcFs {
		rcSet[key(f)] = true
	}
	for _, f := range chkFs {
		if !rcSet[key(f)] {
			t.Fatalf("check 侧的 finding %+v 在 reconcile --dry-run 侧找不到逐字相同的一条\n%s", f, rcOut)
		}
	}
	// ② **真**子集：reconcile 侧必须更多（本语料里多出 git_uncommitted / reviewed_at_missing）。
	if len(rcFs) <= len(chkFs) {
		t.Fatalf("reconcile 侧 %d 条 ≤ check 侧 %d 条：真子集关系不成立", len(rcFs), len(chkFs))
	}
	chkNameSet, rcNameSet := chkNames(chkFs), chkNames(rcFs)
	if strings.Join(chkNameSet, ",") == strings.Join(rcNameSet, ",") {
		t.Fatalf("两侧 check 集合完全相同（%v）：应是**真**子集", chkNameSet)
	}
	inRc := map[string]bool{}
	for _, n := range rcNameSet {
		inRc[n] = true
	}
	for _, n := range chkNameSet {
		if !inRc[n] {
			t.Fatalf("check 侧的 %q 不在 reconcile 侧集合 %v 内：子集关系不成立", n, rcNameSet)
		}
	}
}

// —— ⑦ 参数面：不作用面参数与位置参数一律退 1，且零副作用 ——

func TestCheckRejectsExcludedFlagsAndPositionalArgs(t *testing.T) {
	cases := []struct {
		name  string
		extra []string
	}{
		// 可见性合同 §3.4：`--include-deprecated` 属 `eg check` 的**不作用面**，传入即退 1。
		{"include_deprecated", []string{"--include-deprecated"}},
		// 范围收窄参数属 S4 性能面：本命令一个都不声明。
		{"target_flag", []string{"--target", "k-20260901-attention"}},
		{"domain_flag", []string{"--domain", "ai-infra"}},
		// 修复开关不存在（检查与修复彻底分离）。
		{"fix_flag", []string{"--fix"}},
		// 全库口径不吃对象参数。
		{"positional", []string{"k-20260901-attention"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, _, _ := reviewedVault(t)
			before := gitLogCount(t, dir)
			beforePorcelain := rcPorcelainCount(t, dir)
			code, out, errOut := runCheckCLI(t, dir, tc.extra...)
			if code != ExitUsage {
				t.Fatalf("参数 %v 的退出码 = %d，期望 1（%s / %s）", tc.extra, code, out, errOut)
			}
			if got := gitLogCount(t, dir); got != before {
				t.Fatalf("参数非法路径产生了提交：%d → %d（必须零写入）", before, got)
			}
			if got := rcPorcelainCount(t, dir); got != beforePorcelain {
				t.Fatalf("参数非法路径动了工作区：%d → %d（必须零写入）", beforePorcelain, got)
			}
		})
	}
}

// —— ⑧ 退出码集合恰 {0, 1, 2}：3 / 4 / 5 / 6 结构上取不到 ——

func TestCheckExitCodeNeverThreeToSix(t *testing.T) {
	// 纯函数只有两个出口，且与 reconcile 的四档判定**不共用**一条通路。
	if got := checkExitCode(0); got != ExitOK {
		t.Fatalf("checkExitCode(0) = %d，期望 %d", got, ExitOK)
	}
	for _, n := range []int{1, 2, 7, 100} {
		if got := checkExitCode(n); got != ExitValidation {
			t.Fatalf("checkExitCode(%d) = %d，期望 %d", n, got, ExitValidation)
		}
	}
	for _, n := range []int{-1, 0} { // 负数与 0 都不算 error（防御性口径：不会误报 2）
		if got := checkExitCode(n); got != ExitOK {
			t.Fatalf("checkExitCode(%d) = %d，期望 %d", n, got, ExitOK)
		}
	}
	banned := map[int]string{
		ExitPartialWrite: "3（有写入被 B3 跳过）—— eg check 没有写入面",
		ExitCommitFailed: "4（Git 提交失败）—— eg check 没有提交面",
		ExitNeedConfirm:  "6（仅缺确认）—— eg check 没有确认前置",
	}
	for _, n := range []int{-1, 0, 1, 2, 3, 42} {
		if why, bad := banned[checkExitCode(n)]; bad {
			t.Fatalf("checkExitCode(%d) 落到了不启用的退出码 %s", n, why)
		}
	}
}

// —— ⑨ 只读到底：不改写 `eg report --last` 的事实 ——

func TestCheckDoesNotTouchLastReport(t *testing.T) {
	dir, _ := chkRelVault(t)
	readLast := func() string {
		t.Helper()
		setGitIdentity(t)
		r := newTestRoot(t, dir)
		r.In = closedStdin{}
		code, out, errOut := runCLI(t, r, "report", "--last", "--vault", dir, "--json")
		if code != ExitOK {
			t.Fatalf("eg report --last 退出码 = %d（%s / %s）", code, out, errOut)
		}
		return out
	}
	before := readLast()
	if code, out, errOut := runCheckCLI(t, dir); code != ExitValidation {
		t.Fatalf("前置：本语料应退 2，实得 %d（%s / %s）", code, out, errOut)
	}
	if after := readLast(); after != before {
		t.Fatalf("eg check 改写了最近一次报告：\n前 %s\n后 %s", before, after)
	}
}
