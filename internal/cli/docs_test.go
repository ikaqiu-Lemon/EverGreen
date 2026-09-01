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
	"strings"
	"testing"

	"evergreen/internal/version"
)

const (
	docsREADME  = "../../README.md"
	docsINSTALL = "../../INSTALL.md"
	// 版本与发布口径的唯一决策文档（在同级 teamwork/ 计划仓内）。
	docsReleaseSpec = "../../../teamwork/projects/evergreen/s1_main_flow/docs/specs/2026-10-03-m2-release-and-version.md"
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

// TestDocsNoRemoteAssumption：无远端信息，文档不得出现 git push 之类的臆造步骤。
func TestDocsNoRemoteAssumption(t *testing.T) {
	for _, p := range []string{docsREADME, docsINSTALL} {
		if strings.Contains(readDocs(t, p), "git push") {
			t.Fatalf("%s 出现 git push：本仓无远端，不得臆造推送步骤", p)
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
