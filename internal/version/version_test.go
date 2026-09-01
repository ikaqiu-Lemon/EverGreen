package version

// internal/version 的机器判据。
//
// M1 期只锁「String() 含各字段」「默认值非空」；T-…-027 追加两条：
//   - TestVersionNotScaffold：默认版本号**不得**再是 `0.0.0-dev`（风险 R-3 的关闭判据），
//     且必须与 Makefile 的 `VERSION ?=` 缺省值逐字相等（三处同源的其中两处）；
//   - TestVersionStringHasThreeParts：`String()` 同时含版本号、commit、构建时间三段。
//
// 第三处（决策文档声明的版本号）由 test/e2e/m2_docs_commands.sh 与
// internal/cli/docs_test.go 逐字比对。

import (
	"os"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestStringContainsVersionAndToolchain(t *testing.T) {
	got := String()
	for _, want := range []string{"eg ", Version, Commit, Date, runtime.Version()} {
		if !strings.Contains(got, want) {
			t.Fatalf("String() = %q，缺少 %q", got, want)
		}
	}
}

func TestDefaultsNotEmpty(t *testing.T) {
	if Version == "" || Commit == "" || Date == "" {
		t.Fatalf("版本字段不得为空：Version=%q Commit=%q Date=%q", Version, Commit, Date)
	}
}

// TestVersionNotScaffold：默认版本号脱离 0.0.0-dev，且与 Makefile 缺省值逐字相等。
func TestVersionNotScaffold(t *testing.T) {
	if Version == ScaffoldVersion {
		t.Fatalf("默认版本号仍是脚手架值 %q：M2 起必须给出真实版本号（风险 R-3）", ScaffoldVersion)
	}
	if DefaultVersion == ScaffoldVersion {
		t.Fatalf("DefaultVersion 仍是 %q", ScaffoldVersion)
	}
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+`).MatchString(DefaultVersion) {
		t.Fatalf("默认版本号 %q 不符合 x.y.z[-suffix] 形态", DefaultVersion)
	}
	// Makefile 的 VERSION 缺省值必须与本包字面量逐字相等（三处同源之二）。
	raw, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatalf("读不到 Makefile：%v", err)
	}
	m := regexp.MustCompile(`(?m)^VERSION\s*\?=\s*(\S+)\s*$`).FindStringSubmatch(string(raw))
	if m == nil {
		t.Fatal("Makefile 未定义 VERSION ?= <值>")
	}
	if m[1] != DefaultVersion {
		t.Fatalf("Makefile 的 VERSION=%q 与 internal/version.DefaultVersion=%q 不同源", m[1], DefaultVersion)
	}
	if m[1] == ScaffoldVersion {
		t.Fatalf("Makefile 的 VERSION 仍是 %q", ScaffoldVersion)
	}
}

// TestVersionStringHasThreeParts：--version 输出含版本号 / commit / 构建时间三段。
func TestVersionStringHasThreeParts(t *testing.T) {
	got := String()
	for _, part := range []string{Version, "commit " + Commit, "built " + Date} {
		if !strings.Contains(got, part) {
			t.Fatalf("String() = %q 缺少分段 %q（版本号 / commit / 构建时间三段必须齐全）", got, part)
		}
	}
	if strings.Contains(got, ScaffoldVersion) {
		t.Fatalf("String() 仍含脚手架版本号：%q", got)
	}
}
