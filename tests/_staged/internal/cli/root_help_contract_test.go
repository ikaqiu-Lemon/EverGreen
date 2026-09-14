package cli

// 顶层 `--help` 声明面与实现面的**集合相等**断言（I-…-012 / I-…-013）。
//
// 为什么是集合相等而不是子集：子集断言挡不住这两条缺陷本身 ——
//   - I-…-012 的现场是 help 少列 5 / 6（help ⊂ 实现，子集断言仍绿）；
//   - 反向漏洞同样要挡：help 多列一个并不存在的码（实现 ⊂ help），
//     集成方会去处理一个永不出现的分支。
// 两个方向都必须转红，所以用双向差集断言，并把差集内容打进失败信息。

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// helpExitCodes 解析 `--help` 退出码区块里声明的码值集合。
//
// 只认「行首两空格 + 数字 + 两空格」这一种渲染形态（Usage 的唯一写法），
// 避免把正文里出现的其它数字误当成退出码而让断言虚假通过。
func helpExitCodes(t *testing.T, usage string) []int {
	t.Helper()
	idx := strings.Index(usage, "退出码：\n")
	if idx < 0 {
		t.Fatalf("help 中找不到「退出码：」区块；实现面已定义 %v，声明面缺失", ExitCodeSet())
	}
	block := usage[idx:]
	re := regexp.MustCompile(`(?m)^  (\d+)  `)
	ms := re.FindAllStringSubmatch(block, -1)
	if len(ms) == 0 {
		t.Fatalf("退出码区块存在但未解析到任何条目，区块内容：\n%s", block)
	}
	seen := map[int]bool{}
	var out []int
	for _, m := range ms {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("退出码条目 %q 不是整数：%v", m[1], err)
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Ints(out)
	return out
}

// diffInts 返回 a\b 与 b\a，供双向差集断言给出可定位的失败信息。
func diffInts(a, b []int) (onlyA, onlyB []int) {
	in := func(xs []int, v int) bool {
		for _, x := range xs {
			if x == v {
				return true
			}
		}
		return false
	}
	for _, v := range a {
		if !in(b, v) {
			onlyA = append(onlyA, v)
		}
	}
	for _, v := range b {
		if !in(a, v) {
			onlyB = append(onlyB, v)
		}
	}
	return
}

// TestHelpExitCodeSetEqualsImplementation 断言：help 声明的退出码集合 == 实现定义的全集。
//
// 实现面的权威是 exit.go / exitcode.go 的常量；这里逐一列出常量而不是复用
// ExitCodeSet()，否则「表漏一个码」时两边同时漏、断言自我循环而永远为真。
func TestHelpExitCodeSetEqualsImplementation(t *testing.T) {
	// 实现面全集：直接引用常量（合同 §9.1 恰 7 值）。
	impl := []int{
		ExitOK,
		ExitUsage,
		ExitValidation,
		ExitPartialWrite,
		ExitCommitFailed,
		ExitPrecheckOrLock,
		ExitNeedConfirm,
	}
	sort.Ints(impl)

	r := New()
	got := helpExitCodes(t, r.Usage())

	onlyHelp, onlyImpl := diffInts(got, impl)
	if len(onlyHelp) > 0 || len(onlyImpl) > 0 {
		t.Fatalf("help 退出码集合与实现不相等\n  help  = %v\n  impl  = %v\n  仅 help 有（声明了不存在的码）= %v\n  仅实现有（漏声明，集成方会落进 default）= %v",
			got, impl, onlyHelp, onlyImpl)
	}

	// 声明面数据源也必须与常量集合一致（挡住「表和常量各自演进」）。
	if fmt.Sprint(ExitCodeSet()) != fmt.Sprint(impl) {
		t.Fatalf("ExitCodeDocs 覆盖的码集合 %v != 实现常量集合 %v", ExitCodeSet(), impl)
	}

	// 每条声明都必须有非空语义描述：只给码值不给语义等于没有声明面。
	for _, d := range ExitCodeDocs() {
		if strings.TrimSpace(d.Desc) == "" {
			t.Fatalf("退出码 %d 的语义描述为空", d.Code)
		}
	}
}

// TestHelpExitCodeBlockRejectsUnknownCode 反证：往声明面注入一个实现里不存在的码，断言必须转红。
//
// 不注入全局状态，只验证差集逻辑对「help 多列」这一方向真的报错 ——
// 否则上面的断言可能只是单向有效。
func TestHelpExitCodeBlockRejectsUnknownCode(t *testing.T) {
	impl := []int{0, 1, 2, 3, 4, 5, 6}
	withBogus := []int{0, 1, 2, 3, 4, 5, 6, 9}
	onlyHelp, onlyImpl := diffInts(withBogus, impl)
	if len(onlyHelp) != 1 || onlyHelp[0] != 9 {
		t.Fatalf("差集未能识别「help 多列 9」：onlyHelp=%v", onlyHelp)
	}
	if len(onlyImpl) != 0 {
		t.Fatalf("不应有「仅实现有」的差集：%v", onlyImpl)
	}

	missing := []int{0, 1, 2, 3, 4}
	onlyHelp2, onlyImpl2 := diffInts(missing, impl)
	if len(onlyImpl2) != 2 {
		t.Fatalf("差集未能识别「help 漏列 5/6」（I-…-012 的原始现场）：onlyImpl=%v", onlyImpl2)
	}
	if len(onlyHelp2) != 0 {
		t.Fatalf("不应有「仅 help 有」的差集：%v", onlyHelp2)
	}
}

// helpCommandNames 解析 help 命令区列出的命令条目（取每行第一个 token）。
func helpCommandNames(t *testing.T, usage string) []string {
	t.Helper()
	re := regexp.MustCompile(`(?m)^命令（共 (\d+) 条）：$`)
	m := re.FindStringSubmatch(usage)
	if m == nil {
		t.Fatalf("help 命令区标题不符合「命令（共 N 条）：」形态，可能残留阶段代号（I-…-013）。usage 首 200 字：\n%.200s", usage)
	}
	declared, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("标题条数不是整数：%v", err)
	}

	start := strings.Index(usage, m[0]) + len(m[0]) + 1
	rest := usage[start:]
	end := strings.Index(rest, "\n全局 flag：")
	if end < 0 {
		t.Fatalf("找不到命令区结束位置（\\n全局 flag：）")
	}
	var names []string
	for _, line := range strings.Split(rest[:end], "\n") {
		if !strings.HasPrefix(line, "  ") || strings.TrimSpace(line) == "" {
			continue
		}
		names = append(names, strings.Fields(line)[0])
	}
	if declared != len(names) {
		t.Fatalf("标题自称 %d 条，命令区实际列出 %d 条（这正是 I-…-013 的缺陷形态：标题与实际条数脱钩）",
			declared, len(names))
	}
	sort.Strings(names)
	return names
}

// TestHelpCommandSetEqualsRegistry 断言：help 列出的命令集合 == 注册表命令集合，且标题条数一致。
//
// 覆盖 I-…-013 的两个面：标题不得绑定阶段代号（"S1 九命令"），
// 且标题条数、列出条数、注册表条数三者必须同时相等。
func TestHelpCommandSetEqualsRegistry(t *testing.T) {
	r := New()
	usage := r.Usage()

	if strings.Contains(usage, "S1 九命令") {
		t.Fatalf("help 仍含内部阶段代号「S1 九命令」（I-…-013 未修）")
	}
	// 阶段代号泄漏的一般化断言：命令区标题不得出现 S1~S9 之类的阶段前缀。
	if regexp.MustCompile(`命令（\s*S\d`).MatchString(usage) {
		t.Fatalf("help 命令区标题绑定了阶段代号，属内部口径泄漏")
	}

	got := helpCommandNames(t, usage)

	var want []string
	for _, c := range r.cmds {
		want = append(want, strings.Fields(c.Display)[0])
	}
	sort.Strings(want)

	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("help 命令集合与注册表不相等\n  help     = %v\n  registry = %v", got, want)
	}
	if len(want) != len(r.cmds) {
		t.Fatalf("注册表条数 %d 与解析出的 want %d 不一致", len(r.cmds), len(want))
	}
}

// —— 子命令自述面：`退出码：` 行必须与该命令的实际码面一致（I-…-012 第 2 条）——
//
// 缺陷现场：15 条会进 `.index/run.lock` 临界区的命令，其 `--help` 的「退出码：」行统一止于 4，
// 而正文里已经在讲「锁忙退 5 + E16」。声明面漏一个码，集成方就会把 5 落进 default 分支
// （当成未知失败），这正是 I-…-012 要修的东西。
//
// 断言做成 **iff（双向）**：
//   - 白名单内命令（PrecheckOrLockHelpCommands）必须列 5；漏列即红（原始缺陷方向）；
//   - 白名单外命令（只读命令）不得列 5；多列即红（声明一个永不出现的分支同样是漂移）。
// 6 同理按 NeedConfirmCommands 的顶层命令投影做 iff。

// helpExitCodeLine 取一条子命令 usage 里的「退出码：」区块（该区块恒在 usage 末尾）。
func helpExitCodeLine(t *testing.T, name, usage string) string {
	t.Helper()
	const marker = "退出码："
	idx := strings.Index(usage, marker)
	if idx < 0 {
		t.Fatalf("命令 %s 的 --help 没有「退出码：」区块：自述面缺失，调用方无从判断码面", name)
	}
	return usage[idx+len(marker):]
}

// subcommandExitCodes 解析「退出码：」区块声明的码集合。
//
// 只认区块的规范枚举形态：以 `|` 分隔、每段**以码值开头**（`0 | 1 参数非法 | 5 …`）。
// 段内出现的 E15 / W22 / B3 / R1 这类带字母前缀的编号不会被误读成退出码，
// 反过来，不按这个形态写的码也不算声明（自述面必须是机器可读的，不能靠人脑从散文里捞）。
func subcommandExitCodes(t *testing.T, name, usage string) []int {
	t.Helper()
	block := helpExitCodeLine(t, name, usage)
	seen := map[int]bool{}
	var out []int
	for _, seg := range regexp.MustCompile(`[|\n]`).Split(block, -1) {
		s := strings.TrimSpace(seg)
		if s == "" {
			continue
		}
		r := []rune(s)
		if r[0] < '0' || r[0] > '9' {
			continue
		}
		// 只取单个数字：两位数（E15 之类已被上一条排除）不是退出码。
		if len(r) > 1 && r[1] >= '0' && r[1] <= '9' {
			t.Fatalf("命令 %s 的退出码区块出现多位数字段 %q：退出码恒为一位数", name, s)
		}
		n, err := strconv.Atoi(string(r[0]))
		if err != nil {
			t.Fatalf("命令 %s：退出码段 %q 解析失败：%v", name, s, err)
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		t.Fatalf("命令 %s 的退出码区块解析为空，区块内容：\n%s", name, block)
	}
	sort.Ints(out)
	return out
}

// TestSubcommandHelpExitCodesMatchClass 逐条命令断言：
//  1. 声明的码全部落在实现全集内（不得声明 7 / 9 这类不存在的码）；
//  2. 5 ∈ 声明 ⟺ 该命令在退出码 5 的自述面白名单内；
//  3. 6 ∈ 声明 ⟺ 该命令是两条确认门命令之一。
func TestSubcommandHelpExitCodesMatchClass(t *testing.T) {
	r := New()

	all := map[int]bool{}
	for _, c := range ExitCodeSet() {
		all[c] = true
	}

	// 需要列 6 的顶层命令 = NeedConfirmCommands() 的顶层投影（"proposal approve" → "proposal"）。
	needConfirmTop := map[string]bool{}
	for _, c := range NeedConfirmCommands() {
		needConfirmTop[strings.Fields(c)[0]] = true
	}
	if len(needConfirmTop) == 0 {
		t.Fatalf("退出码 6 的命令白名单为空，断言会空洞成立")
	}

	var withFive, withoutFive []string
	for _, c := range r.cmds {
		name := strings.Fields(c.Display)[0]
		codes := subcommandExitCodes(t, name, c.Usage)

		for _, n := range codes {
			if !all[n] {
				t.Fatalf("命令 %s 声明了实现里不存在的退出码 %d（声明面 %v，实现全集 %v）",
					name, n, codes, ExitCodeSet())
			}
		}

		has := func(v int) bool {
			for _, n := range codes {
				if n == v {
					return true
				}
			}
			return false
		}

		want5 := PrecheckOrLockHelpEnabled(name)
		if has(5) != want5 {
			if want5 {
				t.Fatalf("命令 %s 会进 run.lock 临界区（可退 5 = E15/E16），但 --help 未声明 5：%v\n"+
					"这正是 I-…-012 的缺陷形态：正文讲 5、退出码行止于 4", name, codes)
			}
			t.Fatalf("只读命令 %s 的 --help 声明了 5，但它不取锁、不开事务：%v", name, codes)
		}
		if has(5) {
			withFive = append(withFive, name)
		} else {
			withoutFive = append(withoutFive, name)
		}

		want6 := needConfirmTop[name]
		if has(6) != want6 {
			if want6 {
				t.Fatalf("确认门命令 %s 的 --help 未声明 6：%v", name, codes)
			}
			t.Fatalf("非确认门命令 %s 的 --help 声明了 6：%v", name, codes)
		}
	}

	// 白名单必须被完整覆盖：白名单里写了某条命令、但注册表里没有它，等于白名单在放空炮。
	sort.Strings(withFive)
	want := PrecheckOrLockHelpCommands()
	if fmt.Sprint(withFive) != fmt.Sprint(want) {
		t.Fatalf("声明了 5 的命令集合与白名单不相等\n  help     = %v\n  whitelist = %v", withFive, want)
	}
	// 两侧都必须非空：否则上面的 iff 只是一边空集，挡不住任何东西。
	if len(withFive) == 0 || len(withoutFive) == 0 {
		t.Fatalf("iff 断言退化：列 5 的 %d 条、不列 5 的 %d 条", len(withFive), len(withoutFive))
	}
}

// TestSubcommandExitCodeParserRejectsProseNumbers 反证解析器本身不是"抓到数字就算"。
//
// 若解析器把 E15 / B3 / 「再退 2」这类散文数字也当成声明，上面的 iff 会因为
// 误把 5 从 E15 这类带字母前缀的编号里读出来而假绿 —— 那时缺陷（退出码行止于 4）会被判成已修。
//
// 注：本文件按 m3_test.go 的分域发放纪律，绝不写出被双引号包住的诊断码字面量
// （那会被全库扫描判成 internal/cli 内的域外码），需要提及时一律裸写或用「」包住。
func TestSubcommandExitCodeParserRejectsProseNumbers(t *testing.T) {
	// 只有散文编号、没有规范枚举 → 必须解析为空（用子测试捕获 Fatalf）。
	block := "退出码：0 成功 | 1 参数非法（E15 与 B3 见合同 §9.1，锁忙 E16 时另有说明）"
	got := subcommandExitCodes(t, "probe", block)
	if fmt.Sprint(got) != fmt.Sprint([]int{0, 1}) {
		t.Fatalf("解析器把散文编号读成了退出码：%v（应恰为 [0 1]）", got)
	}

	// 规范枚举里的 5 必须能被读到（证明上一条不是因为"什么都读不到"而通过）。
	got2 := subcommandExitCodes(t, "probe", "退出码：0 | 4 Git 提交失败 |\n        5 写前强校验失败（E15）")
	if fmt.Sprint(got2) != fmt.Sprint([]int{0, 4, 5}) {
		t.Fatalf("规范枚举中的 5 未被解析到：%v", got2)
	}
}
