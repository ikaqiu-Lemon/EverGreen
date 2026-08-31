package main

// 包结构与依赖方向的机器判据（T-evergreen.s1_main_flow-158614-001 Acceptance）。
//
// 断言三条：
//  ① internal/*/doc.go 首行匹配 ^// \[S[1-5]\]（§13 九包 + 脚手架 version 包，逐包命中）；
//  ② 每个 doc.go 的「允许依赖：」列表与施工索引 §13 表格逐包**相等**；
//  ③ go list -deps ./internal/<pkg> 的实际本仓 internal/ 依赖集合 ⊆ 该包的允许依赖，
//     且 model 的实际依赖为空集（零依赖硬约束）。
//
// ③ 之所以是「⊆」而不是「==」：允许依赖是**上限**，第一批 task（001–008、011）落地后
// plan / report / rules 等包的业务实现尚未进场（T-…-009/010/012–018），其实际依赖必然是
// 允许集合的真子集。等号只在 M1 收口（T-…-018）时才可能成立；本测试同时打印「已声明但
// 尚未实际使用」的差集，作为收口前的进度证据，不以差集非空判失败。

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// §13 的 S1 九包（顺序同施工索引表格）。
var ch13Packages = []string{
	"model", "mdfile", "store", "git", "plan", "report", "cli", "query", "rules",
}

// 脚手架包：§15.2 明确标注「非 §13 的 S1 九包之一」。
var scaffoldPackages = []string{"version"}

// S1–S3 不得存在的包。
var forbiddenPackages = []string{"index", "proposal", "reconcile"}

const designDoc = "../../../teamwork/projects/evergreen/s1_main_flow/docs/specs/" +
	"2026-08-31-evergreen-s1-tech-design.md"

var firstLineRE = regexp.MustCompile(`^// \[S[1-5]\]`)

// parseDeps 把一段「允许依赖」描述归一化成包名集合。
// 同一个函数同时用于 doc.go 的「允许依赖：…」与 §13 表格箭头右侧，保证两边口径一致。
//   - "无（零依赖…）" / "零依赖"          → 空集
//   - "以上全部（…）"                     → 除 self 外的全部 §13 包
//   - "model（S1 直连 mdfile）"           → {model, mdfile}
func parseDeps(self, text string) map[string]bool {
	got := map[string]bool{}
	if strings.Contains(text, "以上全部") {
		for _, p := range ch13Packages {
			if p != self {
				got[p] = true
			}
		}
		return got
	}
	for _, p := range ch13Packages {
		if p == self {
			continue
		}
		if strings.Contains(text, p) {
			got[p] = true
		}
	}
	return got
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// declaredDeps 读 internal/<pkg>/doc.go：返回首行与「允许依赖」集合。
func declaredDeps(t *testing.T, pkg string) (string, map[string]bool) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("../../internal", pkg, docFile(pkg)))
	if err != nil {
		t.Fatalf("读取 internal/%s 的包注释文件失败：%v", pkg, err)
	}
	lines := strings.Split(string(raw), "\n")
	var declared string
	for _, l := range lines {
		if strings.Contains(l, "允许依赖：") {
			declared = l[strings.Index(l, "允许依赖：")+len("允许依赖："):]
			break
		}
	}
	if declared == "" {
		t.Fatalf("internal/%s 的包注释缺少「允许依赖：」行", pkg)
	}
	return strings.TrimRight(lines[0], "\r"), parseDeps(pkg, declared)
}

// docFile 返回承载包注释的文件名：§13 九包统一为 doc.go，脚手架 version 包为 version.go。
func docFile(pkg string) string {
	if pkg == "version" {
		return "version.go"
	}
	return "doc.go"
}

// ch13Table 解析施工索引 §13 代码块，返回「包名 → 允许依赖集合」。
// 依赖箭头一律取**最后一个 `]` 之后**的部分：plan 行的说明文字里也有 `→`（"op → store 展开"），
// 只有 `[阶段]` 标注之后的那个箭头才是依赖方向。
func ch13Table(t *testing.T) map[string]map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(designDoc)
	if err != nil {
		t.Skipf("施工索引不可读（%v）：§13 表格比对跳过；本用例要求 teamwork/ 与 evergreen/ 同级", err)
	}
	table := map[string]map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		for _, pkg := range ch13Packages {
			if !strings.HasPrefix(trimmed, pkg+"/") {
				continue
			}
			if !strings.Contains(trimmed, "[S") {
				continue
			}
			cell := trimmed[strings.LastIndex(trimmed, "]")+1:]
			table[pkg] = parseDeps(pkg, cell)
		}
	}
	if len(table) != len(ch13Packages) {
		seen := map[string]bool{}
		for k := range table {
			seen[k] = true
		}
		t.Fatalf("§13 表格解析到 %d 个包（%v），期望 %d 个",
			len(table), sortedKeys(seen), len(ch13Packages))
	}
	return table
}

// directDeps 返回该包 **直接 import** 的本仓 internal/ 包（§13 表格就是直接依赖表：
// store → model mdfile git 指的是 store 自己 import 这三个）。
func directDeps(t *testing.T, pkg string) (map[string]bool, bool) {
	t.Helper()
	return listDeps(t, pkg, `{{join .Imports "\n"}}`)
}

// actualDeps 返回 go list -deps 结果里属于本仓 internal/ 的依赖包（**传递闭包**，排除 self 与脚手架 version）。
func actualDeps(t *testing.T, pkg string) (map[string]bool, bool) {
	t.Helper()
	return listDeps(t, pkg, "")
}

func listDeps(t *testing.T, pkg, format string) (map[string]bool, bool) {
	t.Helper()
	args := []string{"list"}
	if format == "" {
		args = append(args, "-deps")
	} else {
		args = append(args, "-f", format)
	}
	args = append(args, "./internal/"+pkg)
	cmd := exec.Command("go", args...)
	cmd.Dir = "../.."
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	got := map[string]bool{}
	for _, l := range strings.Split(string(out), "\n") {
		l = strings.TrimSpace(l)
		if !strings.HasPrefix(l, "evergreen/internal/") {
			continue
		}
		name := strings.TrimPrefix(l, "evergreen/internal/")
		if name == pkg || name == "version" {
			continue
		}
		got[name] = true
	}
	return got, true
}

// TestInternalPackageSetIsExactlyCh13 反证包集合没有被悄悄扩张 / 缩小。
func TestInternalPackageSetIsExactlyCh13(t *testing.T) {
	entries, err := os.ReadDir("../../internal")
	if err != nil {
		t.Fatalf("读 internal/ 失败：%v", err)
	}
	got := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() {
			got[e.Name()] = true
		}
	}
	want := map[string]bool{}
	for _, p := range append(append([]string{}, ch13Packages...), scaffoldPackages...) {
		want[p] = true
	}
	if strings.Join(sortedKeys(got), ",") != strings.Join(sortedKeys(want), ",") {
		t.Fatalf("internal/ 包集合 = %v，期望 %v", sortedKeys(got), sortedKeys(want))
	}
	for _, p := range forbiddenPackages {
		if got[p] {
			t.Fatalf("internal/%s 属 S2/S3/S4，S1 不得创建", p)
		}
	}
}

// TestDocGoFirstLineDeclaresStage 断言 ① 首行阶段标注。
func TestDocGoFirstLineDeclaresStage(t *testing.T) {
	for _, pkg := range append(append([]string{}, ch13Packages...), scaffoldPackages...) {
		first, _ := declaredDeps(t, pkg)
		if !firstLineRE.MatchString(first) {
			t.Errorf("internal/%s 首行 = %q，不匹配 %s", pkg, first, firstLineRE)
		}
	}
}

// TestDeclaredDepsEqualCh13Table 断言 ② doc.go 声明 == §13 表格。
func TestDeclaredDepsEqualCh13Table(t *testing.T) {
	table := ch13Table(t)
	for _, pkg := range ch13Packages {
		_, declared := declaredDeps(t, pkg)
		want := table[pkg]
		if strings.Join(sortedKeys(declared), ",") != strings.Join(sortedKeys(want), ",") {
			t.Errorf("internal/%s：doc.go 声明 %v，§13 表格 %v，两者必须相等",
				pkg, sortedKeys(declared), sortedKeys(want))
		}
	}
}

// declaredClosure 返回允许依赖表的**传递闭包**：§13 是直接依赖表，
// 「plan → store」自然连带 store 自己的允许依赖（model mdfile git）。
// 闭包只沿允许边扩张，因此仍能抓出任何不在允许图上的间接依赖（例如 plan → report）。
func declaredClosure(t *testing.T, pkg string, table map[string]map[string]bool) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	queue := sortedKeys(table[pkg])
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == pkg || out[cur] {
			continue
		}
		out[cur] = true
		queue = append(queue, sortedKeys(table[cur])...)
	}
	return out
}

// TestActualDepsWithinDeclared 断言 ③ 实际依赖 ⊆ 允许依赖，且 model 零依赖。
//
// 分两层，两层都必须成立（比单层更严，不是放宽）：
//   - **直接 import ⊆ 本包允许依赖**：§13 表格是直接依赖表，据此抓「plan 直连 mdfile」
//     这类跨层直取（plan 只能经 store / query 拿文件结构口径）。
//   - **传递依赖 ⊆ 允许依赖的传递闭包**：抓任何不在允许图上的间接依赖。
//     不能拿本包允许集合直接比传递依赖：plan → store → mdfile 是 §13 明文允许的合法链路。
func TestActualDepsWithinDeclared(t *testing.T) {
	table := ch13Table(t)
	for _, pkg := range ch13Packages {
		_, declared := declaredDeps(t, pkg)
		direct, ok := directDeps(t, pkg)
		if !ok {
			t.Skipf("go list 不可用，跳过实际依赖比对")
		}
		for dep := range direct {
			if !declared[dep] {
				t.Errorf("internal/%s 直接 import internal/%s，但允许依赖只有 %v（§13 依赖方向）",
					pkg, dep, sortedKeys(declared))
			}
		}
		actual, ok := actualDeps(t, pkg)
		if !ok {
			t.Skipf("go list 不可用，跳过实际依赖比对")
		}
		closure := declaredClosure(t, pkg, table)
		for dep := range actual {
			if !closure[dep] {
				t.Errorf("internal/%s 传递依赖 internal/%s，不在允许依赖闭包 %v 内（§13 依赖方向）",
					pkg, dep, sortedKeys(closure))
			}
		}
		if pkg == "model" && len(actual) != 0 {
			t.Errorf("internal/model 必须零依赖，实测依赖 %v", sortedKeys(actual))
		}
		var pending []string
		for _, dep := range sortedKeys(declared) {
			if !actual[dep] {
				pending = append(pending, dep)
			}
		}
		if len(pending) > 0 {
			t.Logf("internal/%s 已声明但尚未实际使用的依赖（下游 task 落地后收敛）：%v", pkg, pending)
		}
	}
}
