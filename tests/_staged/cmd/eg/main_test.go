package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/cli"
	"github.com/ikaqiu-Lemon/EverGreen/internal/version"
)

// run 是可测试入口：T-…-003 起真实分发在 internal/cli，本文件只验证入口接线。
func run(args []string, stdout, stderr *bytes.Buffer) int {
	return cli.New().Run(args, stdout, stderr)
}

func TestHelpExitsZero(t *testing.T) {
	for _, arg := range []string{"--help", "-h", "help"} {
		var out, errOut bytes.Buffer
		if code := run([]string{arg}, &out, &errOut); code != 0 {
			t.Fatalf("eg %s 退出码 = %d，期望 0", arg, code)
		}
		if !strings.Contains(out.String(), "用法：") {
			t.Fatalf("eg %s 未在 stdout 打印用法：%q", arg, out.String())
		}
		if errOut.Len() != 0 {
			t.Fatalf("eg %s 不应写 stderr：%q", arg, errOut.String())
		}
	}
}

func TestVersionExitsZero(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"--version"}, &out, &errOut); code != 0 {
		t.Fatalf("eg --version 退出码 = %d，期望 0", code)
	}
	if got := strings.TrimSpace(out.String()); got != version.String() {
		t.Fatalf("eg --version 输出 = %q，期望 %q", got, version.String())
	}
}

func TestUsageErrorExitsOne(t *testing.T) {
	for _, args := range [][]string{{}, {"capture"}, {"--nope"}, {"reconcile"}} {
		var out, errOut bytes.Buffer
		if code := run(args, &out, &errOut); code != 1 {
			t.Fatalf("eg %v 退出码 = %d，期望 1", args, code)
		}
		if out.Len() != 0 {
			t.Fatalf("用法错误时 stdout 必须为空，实际 %q", out.String())
		}
		if !strings.Contains(errOut.String(), "错误：") {
			t.Fatalf("用法错误时 stderr 需含错误说明，实际 %q", errOut.String())
		}
	}
}
