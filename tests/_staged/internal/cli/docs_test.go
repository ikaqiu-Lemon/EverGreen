package cli

// T-…-027 的文档机器判据：README.md / INSTALL.md 的误述反证与「版本号三处同源」。
//
// 与 test/e2e/m2_docs_commands.sh 的分工：
//   - 本文件只做**静态断言**（措辞反证、命令抽取计数、三处同源逐字比对），跑得快、随 go test 一起绿；
//   - e2e 脚本负责**真跑**（逐条执行文档里的命令、make release、SHA256 校验）。
// 两边共用同一组正则口径，任一处被绕过都会被另一处抓住。

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/txn"
	"github.com/ikaqiu-Lemon/EverGreen/internal/version"
)

const (
	docsREADME  = "../../README.md"
	docsINSTALL = "../../INSTALL.md"
	docsSKILL   = "../../skill/SKILL.md"
	// 版本与发布口径的唯一决策文档（在同级 teamwork/ 计划仓内）。
	// 指针始终指向**当前里程碑**的版本决策文档——判据本体不变（该文档必须逐字声明
	// version.DefaultVersion 并对推送 / tag 标「未知」），只是随里程碑推进换到当期出处：
	//   M2：2026-10-03-m2-release-and-version.md（只读保留为历史出处）
	//   M3：2026-11-08-m3-release-and-version.md（只读保留为历史出处）
	//   M4：2026-12-11-m4-release-and-version.md（只读保留为历史出处；K-062-01 收敛）
	//   M5：2027-01-17-m5-release-and-version.md（只读保留为历史出处；T-…-069）
	//   M6：2027-02-21-m6-release-and-version.md（只读保留为历史出处；T-…-075）
	//   knowledge_opinion_split（当期）：2026-09-15-knowledge-opinion-schema-v2-design.md
	//     —— 本 Epic 版本号与发布口径的唯一决策出处（§0.0），逐字声明当期版本号并对推送 / tag 标「未知」。
	// 只把当期指针往前挪属**阶段化更新**，不放宽任何断言：M2/M3/M4/M5/M6 各份历史文档仍在盘、事实一字未改。
	docsReleaseSpec = "../../../teamwork/projects/evergreen/knowledge_opinion_split/docs/specs/2026-09-15-knowledge-opinion-schema-v2-design.md"
)

func readDocs(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读不到文档 %s：%v", path, err)
	}
	return string(raw)
}

// TestDocsNoScaffoldWording：M1 遗留的「脚手架阶段」措辞必须清零（风险 R-2）。
func TestDocsNoScaffoldWording(t *testing.T) {
	for _, p := range []string{docsREADME, docsINSTALL} {
		for i, line := range strings.Split(readDocs(t, p), "\n") {
			if strings.Contains(line, "脚手架阶段") {
				t.Fatalf("%s:%d 仍含「脚手架阶段」：%s", p, i+1, line)
			}
		}
	}
}

// TestDocsNoInvalidVersionCommand：文档不得示范无效命令 `eg version`（真实入口是 `eg --version`）。
func TestDocsNoInvalidVersionCommand(t *testing.T) {
	bad := regexp.MustCompile(`(^|[^-])\beg version\b|bin/eg version`)
	for _, p := range []string{docsREADME, docsINSTALL} {
		for i, line := range strings.Split(readDocs(t, p), "\n") {
			if bad.MatchString(line) {
				t.Fatalf("%s:%d 示范了无效命令 `eg version`：%s", p, i+1, line)
			}
		}
	}
	if !strings.Contains(readDocs(t, docsREADME), "--version") {
		t.Fatal("README.md 未出现 --version")
	}
}

// TestDocsVersionThreeSourcesConsistent：Makefile / internal/version / 决策文档三处版本号逐字相等。
func TestDocsVersionThreeSourcesConsistent(t *testing.T) {
	raw := readDocs(t, "../../Makefile")
	m := regexp.MustCompile(`(?m)^VERSION\s*\?=\s*(\S+)\s*$`).FindStringSubmatch(raw)
	if m == nil {
		t.Fatal("Makefile 未定义 VERSION ?= <值>")
	}
	if m[1] != version.DefaultVersion {
		t.Fatalf("Makefile VERSION=%q ≠ version.DefaultVersion=%q", m[1], version.DefaultVersion)
	}
	if version.DefaultVersion == version.ScaffoldVersion {
		t.Fatalf("默认版本号仍是脚手架值 %q", version.ScaffoldVersion)
	}
	// 第三处：决策文档。teamwork/ 是同级独立仓，缺失时只跳过这一段，不影响前两处判据。
	spec, err := os.ReadFile(filepath.Clean(docsReleaseSpec))
	if err != nil {
		t.Skipf("决策文档不可读（同级 teamwork/ 仓缺失），跳过第三处比对：%v", err)
	}
	if !strings.Contains(string(spec), version.DefaultVersion) {
		t.Fatalf("决策文档未逐字声明版本号 %q", version.DefaultVersion)
	}
	if !strings.Contains(string(spec), "未知") {
		t.Fatal("决策文档必须对「是否推送远端 / tag」标注「未知」")
	}
	// README / INSTALL 也必须写出同一个版本号。
	for _, p := range []string{docsREADME, docsINSTALL} {
		if !strings.Contains(readDocs(t, p), version.DefaultVersion) {
			t.Fatalf("%s 未写出版本号 %q", p, version.DefaultVersion)
		}
	}
}

// TestDocsDarwinLimitationHonest：darwin 产物必须如实登记为「仅交叉编译、未经真机运行验证」。
func TestDocsDarwinLimitationHonest(t *testing.T) {
	install := readDocs(t, docsINSTALL)
	hit := false
	for _, line := range strings.Split(install, "\n") {
		if strings.Contains(line, "darwin") &&
			(strings.Contains(line, "未经真机运行验证") || strings.Contains(line, "未做")) {
			hit = true
			break
		}
	}
	if !hit {
		t.Fatal("INSTALL.md 未在同一段落登记 darwin 的「未经真机运行验证 / 未做」")
	}
	overclaim := regexp.MustCompile(`已在 macOS (验证|测试)|已验证 macOS`)
	for _, p := range []string{docsINSTALL, docsREADME} {
		if loc := overclaim.FindString(readDocs(t, p)); loc != "" {
			t.Fatalf("%s 出现不实声称：%q", p, loc)
		}
	}
}

// TestDocsAreSelfContained ensures public documentation does not depend on the
// private planning repository or carry unresolved repository placeholders.
func TestDocsAreSelfContained(t *testing.T) {
	for _, p := range []string{docsREADME, docsINSTALL} {
		doc := readDocs(t, p)
		for _, forbidden := range []string{"../teamwork/", "<owner>", "<repository>"} {
			if strings.Contains(doc, forbidden) {
				t.Fatalf("%s contains non-public reference %q", p, forbidden)
			}
		}
	}
}

// docsBashCommands 从 ```bash 代码块里机械抽取可执行命令行（与 e2e 脚本同一口径）。
func docsBashCommands(t *testing.T, path string) []string {
	t.Helper()
	var out []string
	inBlock := false
	prefixes := []string{"make ", "go ", "./bin/eg ", "eg ", "gofmt", "sha256sum"}
	for _, line := range strings.Split(readDocs(t, path), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inBlock = trimmed == "```bash"
			continue
		}
		if !inBlock {
			continue
		}
		for _, p := range prefixes {
			if strings.HasPrefix(trimmed, p) {
				out = append(out, trimmed)
				break
			}
		}
	}
	return out
}

// TestDocsCommandsExtractable：两份文档合计可抽取的命令数 ≥ 8（防止靠删空代码块变绿）。
func TestDocsCommandsExtractable(t *testing.T) {
	got := append(docsBashCommands(t, docsREADME), docsBashCommands(t, docsINSTALL)...)
	if len(got) < 8 {
		t.Fatalf("文档可抽取命令仅 %d 条（< 8）：%v", len(got), got)
	}
	if len(docsBashCommands(t, docsREADME)) == 0 {
		t.Fatal("README.md 的 bash 代码块里没有可执行命令")
	}
	if len(docsBashCommands(t, docsINSTALL)) == 0 {
		t.Fatal("INSTALL.md 的 bash 代码块里没有可执行命令")
	}
}

// TestDocs_ExitCodeTableMatchesImpl：INSTALL.md 的退出码表与 internal/cli 的常量集合逐值比对（T-…-046）。
//
// 判据（四条，全部按**值**而不是措辞判）：
//
//	① 表里出现的退出码集合 == 实现启用集合 ∪ {5}（`5` 只允许以「未启用」的形态出现）；
//	② `5` 那一行必须逐字带「未启用」——它属 S5，本阶段全程不启用；
//	③ `6` 那一行必须写出**白名单恰两条命令**（与 NeedConfirmCommands() 同源）与「权威 Markdown 完全不变」；
//	④ 判定顺序「1 → 2 → 6」必须在文档里写明（与 ExitCodeForConfirm 的固定次序同源）。
func TestDocs_ExitCodeTableMatchesImpl(t *testing.T) {
	install := readDocs(t, docsINSTALL)
	// 只解析「退出码表」那一节（INSTALL.md 里还有一张以数字开头的已知限制表）。
	const tableHeading = "### 退出码表"
	start := strings.Index(install, tableHeading)
	if start < 0 {
		t.Fatalf("INSTALL.md 缺 %q 一节", tableHeading)
	}
	section := install[start:]
	if next := strings.Index(section[len(tableHeading):], "\n## "); next >= 0 {
		section = section[:len(tableHeading)+next]
	}
	rowRE := regexp.MustCompile(`^\| *([0-9]+) *\|(.*)$`)
	rows := map[string]string{}
	for _, line := range strings.Split(section, "\n") {
		if m := rowRE.FindStringSubmatch(line); m != nil {
			rows[m[1]] = m[2]
		}
	}
	// 实现启用的退出码（自 M6 起 5 = ExitPrecheckOrLock 已启用，纳入必检集合）。
	enabled := []int{ExitOK, ExitUsage, ExitValidation, ExitPartialWrite, ExitCommitFailed, ExitPrecheckOrLock, ExitNeedConfirm}
	for _, code := range enabled {
		if _, ok := rows[strconv.Itoa(code)]; !ok {
			t.Fatalf("INSTALL.md 退出码表缺已启用的 %d：文档与 exitcode.go 不一致", code)
		}
	}
	for got := range rows {
		n, err := strconv.Atoi(got)
		if err != nil {
			t.Fatalf("退出码表出现非数字行首 %q", got)
		}
		var known bool
		for _, code := range enabled {
			if code == n {
				known = true
			}
		}
		if !known {
			t.Fatalf("INSTALL.md 退出码表出现实现里不存在的 %d", n)
		}
	}
	// 退出码 5 行必须逐字写明 M6 的两类成因码与「零权威写入」边界，不得再标「未启用」。
	// 成因码经 txn 常量取值，避免本测试文件出现越界诊断码字面量（诊断码分域发放门禁）。
	if row, ok := rows["5"]; ok {
		for _, must := range []string{txn.CodePrecheckFailed, txn.CodeLockTimeout} {
			if !strings.Contains(row, must) {
				t.Fatalf("退出码 5 行缺成因码 %s（ExitCode5Enabled() = %v）：%s", must, ExitCode5Enabled(), row)
			}
		}
		if strings.Contains(row, "未启用") {
			t.Fatalf("退出码 5 自 M6 起已启用，5 行不得再标「未启用」：%s", row)
		}
	}
	if !ExitCode5Enabled() {
		t.Fatal("ExitCode5Enabled() 应为 true：文档口径「5 自 M6 启用」要求启用位为真")
	}
	row6 := rows["6"]
	for _, c := range NeedConfirmCommands() {
		if !strings.Contains(row6, "eg "+c) {
			t.Fatalf("退出码 6 行未写出白名单命令 `eg %s`：%s", c, row6)
		}
	}
	if !strings.Contains(row6, "权威 Markdown 完全不变") {
		t.Fatalf("退出码 6 行缺语义边界「权威 Markdown 完全不变」：%s", row6)
	}
	if n := strings.Count(row6, "eg "); n != len(NeedConfirmCommands()) {
		t.Fatalf("退出码 6 行列出 %d 条命令，白名单恰 %d 条：%s", n, len(NeedConfirmCommands()), row6)
	}
	// ④ 判定顺序与 ExitCodeForConfirm 同源。
	if !strings.Contains(section, "参数错 `1` → 校验失败 `2` → 仅缺确认 `6`") {
		t.Fatal("INSTALL.md 未写明判定顺序「参数错 1 → 校验失败 2 → 仅缺确认 6」")
	}
	if got := ExitCodeForConfirm(ConfirmDecision{ArgsValid: true, ValidationPassed: true, ConfirmMissing: true}); got != ExitNeedConfirm {
		t.Fatalf("ExitCodeForConfirm 的顺序与文档不一致：got=%d", got)
	}
}

// TestDocsM3CommandsCovered：README 的命令清单与注册表逐条对齐（T-…-046）。
func TestDocsM3CommandsCovered(t *testing.T) {
	readme := readDocs(t, docsREADME)
	r := New()
	for _, c := range r.Commands() {
		if !strings.Contains(readme, "`eg "+c.Name) {
			t.Fatalf("README.md 未登记已注册命令 %q", c.Display)
		}
	}
	for _, must := range []string{
		"eg proposal new", "eg proposal list", "eg proposal show", "eg proposal approve",
		"eg proposal reject", "eg rel remove", "--include-deleted",
	} {
		if !strings.Contains(readme, must) {
			t.Fatalf("README.md 缺 M3 命令 %q", must)
		}
	}
	// 占位字样零残留（三份文档同判）。
	stale := "M3/S2 " + "未实现"
	for _, p := range []string{docsREADME, docsINSTALL, docsSKILL} {
		if strings.Contains(readDocs(t, p), stale) {
			t.Fatalf("%s 仍含占位字样 %q", p, stale)
		}
	}
	// 三条禁止措辞（授权合同 §5）在三份文档里零命中。
	for _, banned := range []string{"跳过 hash 比对", "自动回滚到执行前", "Agent 自动路径亦可改"} {
		for _, p := range []string{docsREADME, docsINSTALL, docsSKILL} {
			if strings.Contains(readDocs(t, p), banned) {
				t.Fatalf("%s 出现被禁措辞 %q", p, banned)
			}
		}
	}
}
