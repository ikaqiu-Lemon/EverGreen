// test/txnctl/main_test.go：反证 txnctl helper「非法参数 → 非零退出，且经 panic 而非
// （直接 / 间接）os.Exit」的硬边界。此前 8 处 flag.ExitOnError 会在解析失败时以退出码 2 结束
// 进程（间接 os.Exit），而全库门禁 TestOnlyMainCallsOsExit 只扫直接字面量抓不到。改用
// flag.ContinueOnError + mustParse 后，这里做三重反证。
//
// 反证串一律**分片拼接**（"os."+"Exit(" / "flag."+"ExitOnError"），以免本测试文件自身被
// TestOnlyMainCallsOsExit 等按字面量扫描的全库门禁误命中。
package main

import (
	"bytes"
	"flag"
	"io"
	"os"
	osexec "os/exec"
	"strings"
	"testing"
)

// TestMustParsePanicsOnBadFlag：非法参数经 mustParse 必须 panic（既非静默通过，也非 os.Exit）。
func TestMustParsePanicsOnBadFlag(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("非法参数应经 mustParse（flag.ContinueOnError）panic，实测未 panic")
		}
	}()
	fs := flag.NewFlagSet("unit", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	mustParse(fs, []string{"--definitely-not-a-flag"})
	t.Fatal("mustParse 在非法参数下应已 panic，不应到达此处")
}

// TestBadFlagExitsNonZeroViaPanicNotOsExit：以**子进程**观测真实退出行为 —— 非法参数导致
// 非零退出，且经 panic（stderr 含 "panic:"）而非 flag.ExitOnError 那种无 panic 的干净退出。
func TestBadFlagExitsNonZeroViaPanicNotOsExit(t *testing.T) {
	if os.Getenv("TXNCTL_REEXEC_BADFLAG") == "1" {
		// 子进程分支：走与生产代码同款的 ContinueOnError + mustParse，非法 flag 触发 panic。
		fs := flag.NewFlagSet("reexec", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		mustParse(fs, []string{"--definitely-not-a-flag"})
		return // 不应到达（mustParse 已 panic）
	}
	cmd := osexec.Command(os.Args[0], "-test.run", "TestBadFlagExitsNonZeroViaPanicNotOsExit")
	cmd.Env = append(os.Environ(), "TXNCTL_REEXEC_BADFLAG=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatal("非法参数应导致子进程非零退出，实测退出码为 0")
	}
	if !strings.Contains(stderr.String(), "panic:") {
		t.Fatalf("子进程应经 panic 结束（stderr 需含 \"panic:\"，以区别 flag.ExitOnError 的干净退出），实测 stderr=%q", stderr.String())
	}
}

// TestTxnctlSourceHasNoIndirectOsExit：源码级反证 —— main.go 不得用会间接退出的 flag.ExitOnError
// 构造 FlagSet，不得直接调用 os.Exit，且必须改用 flag.ContinueOnError + mustParse。
func TestTxnctlSourceHasNoIndirectOsExit(t *testing.T) {
	raw, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("读 main.go 失败：%v", err)
	}
	src := string(raw)
	if strings.Contains(src, ", flag."+"ExitOnError)") {
		t.Fatal("txnctl 仍以 flag.ExitOnError 构造 FlagSet：解析失败会间接退出进程，违反不触达 os.Exit 的硬边界")
	}
	if strings.Contains(src, "os."+"Exit(") {
		t.Fatal("txnctl 不得直接调用 os.Exit（含间接）")
	}
	if !strings.Contains(src, "flag."+"ContinueOnError") || !strings.Contains(src, "mustParse(") {
		t.Fatal("txnctl 应改用 flag.ContinueOnError + mustParse（显式检查 Parse 错误后 panic）")
	}
}
