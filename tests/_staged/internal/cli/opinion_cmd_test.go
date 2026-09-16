package cli

// `eg opinion` 命令面的机器判据（读路径 CLI 拆分设计 §5.4；T-…-006 批次 B1a 起，B1b-cli 接通 search）。
//
// 命令面不变量（跨批次恒成立）：一次性注册 opinion 顶层命令，子命令集合与顺序**恰**为
// search|show|validate|reject，顶层名册 22 → 23。非法形态（缺/未知子命令、位置参数个数不符、
// <o-id> 形态非法）一律 UsageError（退 1、零写入）。
//
// B1b-cli 起：`search` 子命令**已接通**只读检索（退 0、零副作用；行为判据见 opinion_search_test.go），
// 其余三条 show/validate/reject **仍是未挂载骨架**（合法形态走 NotWiredError，退 1、零写入零 commit）。
//
// 覆盖：① 子命令封闭集与顺序；② --help 恰列 23 条且 opinion 恰一行；③ 命令数 22+1=23
// 加法等式；④ 缺/未知子命令退 1；⑤ search/show/validate/reject 位置参数个数校验；
// ⑥ <o-id> 形态校验（必须经 model.OpinionID.Valid）；⑦ search 已接通退 0、其余三条确定性
// NotWired、退 1、零写入零 commit。

import (
	"strings"
	"testing"
)

// runOpinionCLI 在 dir 这个 vault 上跑一次 `eg opinion …`（人类可读面，便于对 stderr 断言）。
func runOpinionCLI(t *testing.T, dir string, args ...string) (int, string, string) {
	t.Helper()
	setGitIdentity(t)
	r := newTestRoot(t, dir)
	full := append([]string{"opinion"}, args...)
	full = append(full, "--vault", dir)
	return runCLI(t, r, full...)
}

// opinionVaultSnapshot 记 vault 的工作区状态与 commit 数，供「零副作用」前后对比。
func opinionVaultSnapshot(t *testing.T, dir string) (string, int) {
	t.Helper()
	return strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")), gitLogCount(t, dir)
}

// —— ① 子命令集合恰四个且顺序 = search|show|validate|reject ——

func TestOpinionSubcommandsExactlyFour(t *testing.T) {
	want := []string{"search", "show", "validate", "reject"}

	// OpinionSubcommands() 是单一数据源：集合、顺序都由它给出。
	if got := OpinionSubcommands(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("OpinionSubcommands() = %v，期望逐字有序 %v", got, want)
	}

	cmd := New().Lookup("opinion")
	if cmd == nil {
		t.Fatal("eg opinion 必须已注册")
	}
	if strings.Join(cmd.Subs, ",") != strings.Join(want, ",") {
		t.Fatalf("cmd.Subs = %v，期望逐字有序 %v（顺序即 --help 顺序）", cmd.Subs, want)
	}
	if !cmd.SubRequired {
		t.Fatal("eg opinion 必须 SubRequired：无子命令即用法错")
	}
	if cmd.Placeholder {
		t.Fatal("eg opinion 不得是占位命令（占位命令走 placeholderError，本批要的是 NotWiredError）")
	}
	// 本批挂上壳处理器 runOpinion（使「注册面 == 挂载面」成立），但该壳对任一子命令只
	// 返回 NotWiredError：命令**已挂载**（Handler != nil），业务实现仍未落地。
	if cmd.Handler == nil {
		t.Fatal("eg opinion 必须挂上壳处理器 runOpinion（注册面须等于挂载面）")
	}
	// B1b-cli：`eg opinion search` 接通后，父命令**必须**注册 search-only 检索 / 分页 flag
	// （domain/tag/since/until/include-deleted/limit/offset，恰不含 --kind）——具体集合与
	// 「合同可查」由 cli_test.go 的 flag-vs-contract 判据逐格反证，这里只钉「不再是空 flag 面」。
	if cmd.Flags == nil {
		t.Fatal("B1b-cli 阶段 eg opinion 必须注册 search-only flag（search 复用 eg search 口径）")
	}
	if cmd.Validate == nil {
		t.Fatal("eg opinion 必须挂 Validate（钉位置参数与 <o-id> 形态）")
	}
}

// —— ② --help 恰列 23 条，且 opinion 恰一行 ——

func TestOpinionAppearsInHelpExactlyOnce(t *testing.T) {
	r := New()
	help := r.Usage()
	block := helpCommandBlock(t, help)
	if len(block) != 23 {
		t.Fatalf("--help 命令区 = %d 行，期望 23（22 + opinion）", len(block))
	}
	n := 0
	for _, line := range block {
		if strings.Contains(line, "opinion search|show|validate|reject") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("--help 命令区出现 %d 行 opinion，期望恰 1 行", n)
	}
}

// —— ③ 命令数 22 + 1 = 23 加法等式（终值 22 那一格由 TestCommandCountTwentyTwo 保留）——

func TestCommandCountTwentyThree(t *testing.T) {
	// M5 终值（0.5.0）注册表：逐字照抄，**不引用 wantCommands 派生**，两份清单必须独立。
	m5Terminal := []string{
		"init", "config", "capture", "context", "apply",
		"search", "card", "rel", "report",
		"deprecate", "restore", "replaced-by",
		"proposal", "delete", "undelete",
		"mark-reviewed", "unreviewed", "edit",
		"reconcile", "check", "index", "bench",
	}
	added := []string{"opinion"} // T-…-006 批次 B1a：读路径 opinion 命令。

	if len(m5Terminal) != 22 {
		t.Fatalf("M5 终值清单写错了：%d 条，M5 收口时恰 22 条", len(m5Terminal))
	}
	if want := len(m5Terminal) + len(added); want != 23 {
		t.Fatalf("加法等式不成立：%d + %d = %d，期望 23（设计 §5.4 命令名册）",
			len(m5Terminal), len(added), want)
	}
	if len(m5Terminal)+len(added) != wantCommandCount {
		t.Fatalf("M5 终值 %d + B1a 新增 %d 与 wantCommandCount = %d 不一致",
			len(m5Terminal), len(added), wantCommandCount)
	}

	got := New().Commands()
	if len(got) != 23 {
		t.Fatalf("注册命令数 = %d，期望 23", len(got))
	}
	for i, w := range m5Terminal {
		if got[i].Name != w {
			t.Fatalf("第 %d 条命令 = %q，期望 %q（只准在尾部追加，不准重排既有条目）", i+1, got[i].Name, w)
		}
	}
	if got[len(got)-1].Name != "opinion" {
		t.Fatalf("注册表尾条 = %q，期望 %q（新命令一律追加在尾部）", got[len(got)-1].Name, "opinion")
	}
	// opinion 已注册且挂上壳处理器（注册面须等于挂载面）；壳对任一子命令只返回 NotWiredError。
	cmd := New().Lookup("opinion")
	if cmd == nil {
		t.Fatal("命令 opinion 未注册")
	}
	if cmd.Handler == nil {
		t.Fatal("命令 opinion 必须挂上壳处理器 runOpinion（注册面须等于挂载面）")
	}
}

// —— ④ 缺 / 未知子命令：退 1、零副作用 ——

func TestOpinionMissingOrUnknownSubcommand(t *testing.T) {
	dir := captureVault(t)
	statusBefore, logBefore := opinionVaultSnapshot(t, dir)

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"缺子命令", []string{}},
		{"未知子命令", []string{"bogus"}},
		{"未知子命令-approve 不在集合", []string{"approve"}},
	} {
		code, _, errOut := runOpinionCLI(t, dir, tc.args...)
		if code != ExitUsage {
			t.Fatalf("[%s] 退出码 = %d，期望 1（用法错）：%s", tc.name, code, errOut)
		}
		statusAfter, logAfter := opinionVaultSnapshot(t, dir)
		if statusAfter != statusBefore || logAfter != logBefore {
			t.Fatalf("[%s] 改变了工作区或 commit 数（用法错必须零写入）", tc.name)
		}
	}
}

// —— ⑤ 位置参数个数校验 ——

func TestOpinionPositionalArgArity(t *testing.T) {
	dir := captureVault(t)
	statusBefore, logBefore := opinionVaultSnapshot(t, dir)

	const validID = "o-20260101-demo"
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"search 缺 <q>", []string{"search"}},
		{"search 多余位置参数", []string{"search", "a", "b"}},
		{"show 缺 <o-id>", []string{"show"}},
		{"show 多余位置参数", []string{"show", validID, "extra"}},
		{"validate 缺 <o-id>", []string{"validate"}},
		{"validate 多余位置参数", []string{"validate", validID, "extra"}},
		{"reject 缺 <o-id>", []string{"reject"}},
		{"reject 多余位置参数", []string{"reject", validID, "extra"}},
	} {
		code, _, errOut := runOpinionCLI(t, dir, tc.args...)
		if code != ExitUsage {
			t.Fatalf("[%s] 退出码 = %d，期望 1（参数个数非法）：%s", tc.name, code, errOut)
		}
		statusAfter, logAfter := opinionVaultSnapshot(t, dir)
		if statusAfter != statusBefore || logAfter != logBefore {
			t.Fatalf("[%s] 改变了工作区或 commit 数（参数非法必须零写入）", tc.name)
		}
	}
}

// —— ⑥ <o-id> 形态校验：必须经 model.OpinionID.Valid（前缀 o-，防 k-/n-/s- 混用）——

func TestOpinionIDShapeRejected(t *testing.T) {
	dir := captureVault(t)
	statusBefore, logBefore := opinionVaultSnapshot(t, dir)

	badIDs := []string{
		"k-20260101-demo", // 知识卡前缀：不得当成观点 ID
		"n-20260101-demo", // 笔记前缀
		"s-20260101-demo", // 原文前缀
		"o-bad",           // 缺 <yyyymmdd>-<slug>
		"o-2026-demo",     // 日期段非 8 位
		"o-20261301-",     // slug 空
		"opinion",         // 完全不成形
	}
	for _, sub := range []string{"show", "validate", "reject"} {
		for _, id := range badIDs {
			code, _, errOut := runOpinionCLI(t, dir, sub, id)
			if code != ExitUsage {
				t.Fatalf("eg opinion %s %q 退出码 = %d，期望 1（ID 形态非法）：%s",
					sub, id, code, errOut)
			}
		}
	}
	statusAfter, logAfter := opinionVaultSnapshot(t, dir)
	if statusAfter != statusBefore || logAfter != logBefore {
		t.Fatal("ID 形态非法路径改变了工作区或 commit 数（必须零写入）")
	}
}

// —— ⑦ search 已接通、show/validate/reject 仍确定性 NotWired（退 1、零写入零 commit）——

func TestOpinionSearchWiredOthersNotWired(t *testing.T) {
	dir := captureVault(t)
	statusBefore, logBefore := opinionVaultSnapshot(t, dir)

	// search 已接通：合法查询词 → 退 0（空库零命中仍合法），零文件变化、零 commit。
	scode, _, serr := runOpinionCLI(t, dir, "search", "语言模型")
	if scode != ExitOK {
		t.Fatalf("eg opinion search 已接通，应退 0（空库零命中仍合法），实得 %d：%s", scode, serr)
	}
	if statusAfter, logAfter := opinionVaultSnapshot(t, dir); statusAfter != statusBefore || logAfter != logBefore {
		t.Fatal("eg opinion search 是只读检索：不得改动工作区或产生 commit")
	}

	// 其余三条仍是未挂载骨架：合法形态一律 NotWired（退 1、零写入零 commit）。
	const validID = "o-20260101-demo"
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"show <o-id>", []string{"show", validID}},
		{"validate <o-id>", []string{"validate", validID}},
		{"reject <o-id>", []string{"reject", validID}},
	} {
		code, _, errOut := runOpinionCLI(t, dir, tc.args...)
		if code != ExitUsage {
			t.Fatalf("[%s] 退出码 = %d，期望 1（骨架未挂载实现）：%s", tc.name, code, errOut)
		}
		// 必须是 NotWiredError（「业务实现尚未挂载」），而不是别的用法错——
		// 用它证明「合法形态确实穿过了 Validate 与 guard，止步于未挂载的 Handler」。
		if !strings.Contains(errOut, "尚未挂载") {
			t.Fatalf("[%s] stderr 未含 NotWired 措辞（应止步于未挂载 Handler）：%s", tc.name, errOut)
		}
		statusAfter, logAfter := opinionVaultSnapshot(t, dir)
		if statusAfter != statusBefore {
			t.Fatalf("[%s] 改变了工作区（骨架必须零文件变化）：%q → %q",
				tc.name, statusBefore, statusAfter)
		}
		if logAfter != logBefore {
			t.Fatalf("[%s] 产生了 commit（骨架必须零 commit）", tc.name)
		}
	}
}
