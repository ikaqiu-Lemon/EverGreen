package cli

// `eg bench` 的机器判据（M5 · T-evergreen.s1_main_flow-158614-068 阶段 B）。
//
// Task Acceptance 判据 14 点名三条：`TestBenchFiveMetricKeys` /
// `TestBenchJSONEnvelopeFiveKeys` / `TestBenchZeroWriteToMarkdown`。
// 本文件在此之上再钉四条**只加严**的边界：口径冻结、生产路径必用冻结口径、
// 门槛公式逐格复算、命令数 M5 终值 22 的加法等式。
//
// # 为什么这些用例要真的 fork 一个 eg
//
// 合同 §7.3 的计时边界是「端到端**进程**墙钟」。若用例在进程内直接调 handler，
// 量到的就不是合同定义的那个数，`TestBenchZeroWriteToMarkdown` 也证不出
// 「子进程真的没写权威」。因此这里用 `go build` 造一个真 eg 二进制（每个包只造一次），
// 通过**包内**注入点 `benchExec` 交给被测命令 —— 生产路径恒为 `os.Executable()`。

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/query/queryset"
)

// —— 测试脚手架 ——

var (
	benchBinOnce sync.Once
	benchBinPath string
	benchBinErr  error
)

// benchEgBinary 造一个真 eg 二进制（每个测试包只造一次），返回其路径。
//
// `CGO_ENABLED=0`：与交付口径一致（纯 Go SQLite，静态构建），也免得在没有 gcc 的
// 环境里失败。造不出来就 Skip 而不是 Fail —— 「本机没有 go 工具链」不是本 task 的缺陷。
func benchEgBinary(t *testing.T) string {
	t.Helper()
	benchBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "eg-bench-bin-")
		if err != nil {
			benchBinErr = err
			return
		}
		bin := filepath.Join(dir, "eg")
		cmd := exec.Command("go", "build", "-o", bin, "./cmd/eg")
		cmd.Dir = filepath.Join("..", "..") // internal/cli → 仓库根
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, berr := cmd.CombinedOutput(); berr != nil {
			benchBinErr = fmt.Errorf("go build ./cmd/eg 失败：%v\n%s", berr, out)
			return
		}
		benchBinPath = bin
	})
	if benchBinErr != nil {
		t.Skipf("本机造不出 eg 二进制，跳过进程级采样用例：%v", benchBinErr)
	}
	return benchBinPath
}

// benchVault 造一个**最小可采样**的 vault：三张卡 + 一条关系，并建好健康索引。
//
// 用真 eg 建卡太绕（要走 capture/apply），这里直接落 Markdown —— 与既有 e2e 同手法。
func benchVault(t *testing.T) (root, egBin string) {
	t.Helper()
	egBin = benchEgBinary(t)
	root = t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(egBin, append([]string{"--vault", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("eg %s 失败：%v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "--domain", "bench")
	run("config", "set", "default_domain", "bench")

	dir := filepath.Join(root, "domains", "bench", "knowledge")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	card := func(id, title, rels string) {
		t.Helper()
		body := "---\nid: " + id + "\nstatus: active\ncreated_at: '2026-12-01'\n" +
			"updated_at: '2026-12-01T10:00:00+08:00'\ntitle: " + title + "\nsources: []\n" +
			rels + "---\n\n## 知识内容\n\n索引 / retrieval：" + title + "。\n"
		if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	card("k-20261201-bench-a", "采样卡 A",
		"relations:\n  - type: supports\n    target: k-20261201-bench-b\n    reason: a-b\n")
	card("k-20261201-bench-b", "采样卡 B", "")
	card("k-20261201-bench-c", "采样卡 C", "")
	run("index", "build")
	return root, egBin
}

// benchRoot 组一个注入好「真 eg 路径」与「小轮数口径」的 Root。
//
// 轮数调小**只发生在包内测试**：`benchSpecOverride` 不导出，命令行上也没有对应 flag
// （`TestBenchProductionUsesFrozenSpec` 正面反证生产路径恒用冻结口径）。
func benchRoot(t *testing.T, egBin string, spec BenchSpec) *Root {
	t.Helper()
	r := New()
	r.benchExec = func() (string, error) { return egBin, nil }
	r.benchSpecOverride = &spec
	return r
}

// benchTinySpec 是单测用的最小口径：1 轮预热 + 2 轮计入 + 构建 1 次。
// 它只用来跑通整条流程（含真实 fork），不用于产生任何门槛数值。
func benchTinySpec() BenchSpec { return BenchSpec{Warmup: 1, Rounds: 2, P95Rank: 2, BuildRuns: 1} }

// —— ① Task 点名：data 恰五键（键名与次序都不许漂移）——

func TestBenchFiveMetricKeys(t *testing.T) {
	want := []string{
		"search_p95_ms", "card_show_p95_ms", "rel_p95_ms",
		"index_build_ms", "index_incremental_ms",
	}
	got := BenchMetricKeys()
	if len(got) != 5 {
		t.Fatalf("指标键 %d 个，合同 §7.2 要求恰 5 个：%v", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 个指标键 = %q，期望逐字 %q（次序即合同 §7.2 表格行序）", i+1, got[i], want[i])
		}
	}
	// 常量与清单同源：任一常量被改名都会在这里当场红。
	for i, c := range []string{
		BenchKeySearchP95, BenchKeyCardShowP95, BenchKeyRelP95, BenchKeyIndexBuild, BenchKeyIndexIncr,
	} {
		if c != want[i] {
			t.Fatalf("常量第 %d 个 = %q，期望 %q", i+1, c, want[i])
		}
	}
}

// —— ② Task 点名：--json 信封五键 + data 恰五键（真实跑一次 eg bench）——

func TestBenchJSONEnvelopeFiveKeys(t *testing.T) {
	root, egBin := benchVault(t)
	r := benchRoot(t, egBin, benchTinySpec())

	code, out := benchExecJSON(t, r, root)
	if code != ExitOK {
		t.Fatalf("eg bench 退出码 = %d，期望 0；输出：%s", code, out)
	}

	// 信封键**恰五个**（合同 §8.3 不扩张）。
	var env map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("--json 输出不是合法 JSON：%v\n%s", err, out)
	}
	gotKeys := make([]string, 0, len(env))
	for k := range env {
		gotKeys = append(gotKeys, k)
	}
	sort.Strings(gotKeys)
	wantKeys := append([]string{}, EnvelopeKeys()...)
	sort.Strings(wantKeys)
	if strings.Join(gotKeys, ",") != strings.Join(wantKeys, ",") {
		t.Fatalf("信封键 = %v，期望恰 %v", gotKeys, wantKeys)
	}

	// data 键**恰五个**且顺序逐字为合同次序（次序用原文位置反证，不靠 map 遍历）。
	var data map[string]json.Number
	if err := json.Unmarshal(env["data"], &data); err != nil {
		t.Fatalf("data 不是对象：%v", err)
	}
	if len(data) != 5 {
		t.Fatalf("data 键 %d 个，期望恰 5 个：%v", len(data), data)
	}
	prev := -1
	for _, k := range BenchMetricKeys() {
		v, ok := data[k]
		if !ok {
			t.Fatalf("data 缺键 %q", k)
		}
		if n, err := v.Int64(); err != nil || n < 0 {
			t.Fatalf("指标 %q = %v，期望非负整数毫秒", k, v)
		}
		at := strings.Index(out, `"`+k+`"`)
		if at <= prev {
			t.Fatalf("data 键序漂移：%q 出现在位置 %d，应晚于前一个键的位置 %d", k, at, prev)
		}
		prev = at
	}
	// 环境信息与门槛建议只在 summary / 诊断区，**不得**混进 data（§7.2 / §8.3）。
	for _, forbidden := range []string{"go_version", "cpu", "threshold", "env", "report"} {
		if _, ok := data[forbidden]; ok {
			t.Fatalf("data 里出现了越界键 %q：data 恰五键，环境与门槛只进 summary", forbidden)
		}
	}
}

// —— ③ Task 点名：全程零写权威 Markdown（含 .index/ 与 Git）——

func TestBenchZeroWriteToMarkdown(t *testing.T) {
	root, egBin := benchVault(t)
	before := benchSnapshot(t, root)

	r := benchRoot(t, egBin, benchTinySpec())
	if code, out := benchExecJSON(t, r, root); code != ExitOK {
		t.Fatalf("eg bench 退出码 = %d，期望 0；输出：%s", code, out)
	}

	after := benchSnapshot(t, root)
	if before != after {
		t.Fatalf("eg bench 改动了 vault（它必须是只读采样）：\n前：%s\n后：%s", before, after)
	}
}

// benchSnapshot 把「vault 里所有文件的相对路径 + 内容长度 + 内容摘要」串成一个字符串。
//
// 覆盖面刻意包含 `.index/`：合同 §8.1 给 bench 的「写 .index/」一栏是**否** ——
// 构建耗时必须去临时副本上测，测完副本删掉，当前库一个字节都不许动。
func benchSnapshot(t *testing.T, root string) string {
	t.Helper()
	var lines []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		lines = append(lines, fmt.Sprintf("%s\t%d\t%x", filepath.ToSlash(rel), len(b), simpleSum(b)))
		return nil
	})
	if err != nil {
		t.Fatalf("快照 vault 失败：%v", err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// simpleSum 是一个够用的内容指纹（FNV-1a 64）：用例只需要「变了 / 没变」。
func simpleSum(b []byte) uint64 {
	var h uint64 = 0xcbf29ce484222325
	for _, c := range b {
		h ^= uint64(c)
		h *= 0x100000001b3
	}
	return h
}

// benchExecJSON 跑一次 `eg bench --json` 并返回退出码与 stdout 原文。
func benchExecJSON(t *testing.T, r *Root, root string) (int, string) {
	t.Helper()
	var out, errBuf strings.Builder
	code := r.Run([]string{"--vault", root, "bench", "--json"}, &out, &errBuf)
	if s := errBuf.String(); strings.TrimSpace(s) != "" {
		t.Logf("stderr：%s", s)
	}
	return code, strings.TrimSpace(out.String())
}

// —— ④ 口径冻结：BenchFrozenSpec 逐格等于合同 §7.3 ——

func TestBenchSpecIsFrozen(t *testing.T) {
	spec := BenchFrozenSpec()
	for _, c := range []struct {
		name string
		got  int
		want int
		why  string
	}{
		{"Warmup", spec.Warmup, 3, "合同 §7.3 预热轮数行：先跑 3 轮丢弃"},
		{"Rounds", spec.Rounds, 50, "合同 §7.3 预热轮数行：再采 50 轮计入统计"},
		{"P95Rank", spec.P95Rank, 48, "合同 §7.3 P95 定义行：ceil(0.95 × 50) = 第 48 小值，不插值"},
		{"BuildRuns", spec.BuildRuns, 3, "合同 §7.3：构建两指标不预热、各测 3 次取中位数"},
	} {
		if c.got != c.want {
			t.Fatalf("BenchFrozenSpec().%s = %d，期望 %d（%s）", c.name, c.got, c.want, c.why)
		}
	}
	// 查询集大小同样是冻结值（合同 §7.3 查询集行：固定 20 词 / 20 id）。
	if n := len(queryset.Keywords()); n != 20 {
		t.Fatalf("固定关键词 %d 个，期望 20", n)
	}
	if queryset.KeywordCount != 20 {
		t.Fatalf("queryset.KeywordCount = %d，期望 20（清单与常量必须同源）", queryset.KeywordCount)
	}
	if queryset.IDSampleCount != 20 {
		t.Fatalf("固定 id 采样点 = %d 个，期望 20", queryset.IDSampleCount)
	}
	// 查询集必须**去重**：重复词会让某个词被采样两次，选择度分布悄悄偏移。
	seen := map[string]bool{}
	for _, k := range queryset.Keywords() {
		if seen[k] {
			t.Fatalf("固定关键词重复：%q", k)
		}
		seen[k] = true
	}
}

// —— ⑤ 生产路径恒用冻结口径（注入点不得泄漏到命令面）——

func TestBenchProductionUsesFrozenSpec(t *testing.T) {
	r := New()
	if got := r.benchSpec(); got != BenchFrozenSpec() {
		t.Fatalf("未注入时 benchSpec() = %+v，期望恒为 BenchFrozenSpec() = %+v", got, BenchFrozenSpec())
	}
	// 命令面上不得存在任何能改动口径的 flag：bench 的私有 flag 集合恰为空。
	cmd := New().Lookup("bench")
	if cmd == nil {
		t.Fatal("命令 bench 未注册")
	}
	if cmd.Handler == nil {
		t.Fatal("命令 bench 已注册但实现未挂载（wireBench 漏了）")
	}
	if cmd.Placeholder {
		t.Fatal("bench 不得是占位命令：T-…-068 阶段 B 就是它的实现")
	}
	if !cmd.ReadOnly {
		t.Fatal("bench 必须标记 ReadOnly：它是只读采样命令")
	}
	if n := len(wantFlags["bench"]); n != 0 {
		t.Fatalf("bench 的命令私有 flag = %d 个，期望 0（能被命令行调小的门槛不是门槛）", n)
	}
	// 位置参数一律判用法错（退 1、零副作用）。
	var out, errBuf strings.Builder
	if code := New().Run([]string{"bench", "extra"}, &out, &errBuf); code != ExitUsage {
		t.Fatalf("eg bench extra 退出码 = %d，期望 %d（用法错、零副作用）", code, ExitUsage)
	}
}

// —— ⑥ 门槛公式逐格复算（ceil(实测 × 1.5 / 10) × 10）——

func TestBenchThresholdFormula(t *testing.T) {
	for _, c := range []struct{ measured, want int }{
		{1, 10},    // 0.15ms → 向上取到一个 10ms 台阶
		{10, 20},   // 15 → 20
		{100, 150}, // 150 → 150（正好落在台阶上，不多进一格）
		{101, 160}, // 151.5 → 160
		{2408, 3620},
		{3198, 4800},
		{6035, 9060},
		{9958, 14940},
	} {
		if got := BenchThreshold(c.measured); got != c.want {
			t.Fatalf("BenchThreshold(%d) = %d，期望 %d（= ceil(%d × 1.5 / 10) × 10）",
				c.measured, got, c.want, c.measured)
		}
	}
	// 实测为 0（快到量不出来）时门槛取最小台阶 10ms，绝不取 0 —— 0 门槛会让任何回归都判红。
	if got := BenchThreshold(0); got != 10 {
		t.Fatalf("BenchThreshold(0) = %d，期望 10", got)
	}
}

// —— ⑦ 采样前置：索引不健康时不采样（退 1、零副作用）——

func TestBenchRequiresHealthyIndex(t *testing.T) {
	root, egBin := benchVault(t)
	if err := os.RemoveAll(filepath.Join(root, ".index")); err != nil {
		t.Fatal(err)
	}
	before := benchSnapshot(t, root)

	r := benchRoot(t, egBin, benchTinySpec())
	code, out := benchExecJSON(t, r, root)
	if code != ExitUsage {
		t.Fatalf("索引缺失时退出码 = %d，期望 %d（不在降级路径上采样）；输出：%s", code, ExitUsage, out)
	}
	if after := benchSnapshot(t, root); after != before {
		t.Fatal("前置不满足时必须零副作用，但 vault 变了")
	}
}

// —— ⑧ M5 终值：命令数 21 + 1 = 22 的加法等式 ——

func TestCommandCountTwentyTwo(t *testing.T) {
	// T-…-065 收口时的注册表基线（M5 过程值 21 条），逐字照抄、不引用 wantCommands 派生。
	m5Baseline := []string{
		"init", "config", "capture", "context", "apply",
		"search", "card", "rel", "report",
		"deprecate", "restore", "replaced-by",
		"proposal", "delete", "undelete",
		"mark-reviewed", "unreviewed", "edit",
		"reconcile", "check", "index",
	}
	added := []string{"bench"}
	// T-…-006-B1a 更晚新增（`opinion`）：本用例只负责 068 那一条「M5 终值 22」等式，
	// 摘掉后再复算 22。**22 这个 M5 终值结论一个字不删**，只是多了一条要摘掉的后来者。
	laterAdded := []string{"opinion"}

	if len(m5Baseline) != 21 {
		t.Fatalf("M5 过程基线写错了：%d 条，T-…-065 收口时恰 21 条", len(m5Baseline))
	}
	want := len(m5Baseline) + len(added)
	if want != 22 {
		t.Fatalf("加法等式不成立：%d + %d = %d，期望 22（合同 §8.1：20 + 1 + 1）",
			len(m5Baseline), len(added), want)
	}
	if want+len(laterAdded) != wantCommandCount {
		t.Fatalf("M5 终值 %d + 更晚新增 %d 与 wantCommandCount = %d 不一致",
			want, len(laterAdded), wantCommandCount)
	}

	got := New().Commands()
	if len(got)-len(laterAdded) != 22 {
		t.Fatalf("摘掉更晚新增 %v 后命令数 = %d，期望 22（M5 终值）", laterAdded, len(got)-len(laterAdded))
	}
	for i, w := range m5Baseline {
		if got[i].Name != w {
			t.Fatalf("第 %d 条命令 = %q，期望 %q（只准在尾部追加，不准重排既有条目）", i+1, got[i].Name, w)
		}
	}
	if n := len(got) - len(laterAdded); got[n-1].Name != "bench" {
		t.Fatalf("摘掉 %v 后的注册表尾条 = %q，期望 %q（新命令一律追加在尾部）",
			laterAdded, got[n-1].Name, "bench")
	}
	// `eg index` 的四个子命令按既有惯例**不单独计数**（与 `eg config get|set` 同型）：
	// 若哪天它们被计成 4 条，这里的 22 会立刻变成 25，等式当场红。
	if n := len(IndexSubcommands()); n != 4 {
		t.Fatalf("eg index 子命令 = %d 个，期望恰 4（它们不单独计入命令数）", n)
	}
}
