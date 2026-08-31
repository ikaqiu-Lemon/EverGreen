// Command eg 是 Evergreen 的确定性本地 CLI 的唯一入口（单二进制）。
//
// 当前是脚手架阶段：只实现 `eg --help` 与 `eg version`。
// S1 的九个命令（init / config / capture / context / apply / search / card show /
// rel / report）由 T-evergreen.s1_main_flow-158614-002（合同）与 -003（实现）落地，
// 本文件在此之前不含任何业务逻辑，也不含任何模型调用与网络请求。
//
// 退出码（施工索引 §7）：0 成功；1 用法 / 参数非法（零写入）。
// 2 / 3 / 4 由后续任务在实现命令时引入，本阶段不出现。
package main

import (
	"fmt"
	"io"
	"os"

	"evergreen/internal/version"
)

const usage = `eg — Evergreen 确定性本地 CLI（单二进制，不调模型、不抓网页）

用法：
  eg <command> [flags]

当前可用命令（脚手架阶段）：
  version      打印版本信息
  help         打印本帮助（等价于 --help / -h）

S1 规划中的命令（尚未实现，见 T-…-002 / T-…-003）：
  init, config get|set, capture, context, apply --plan,
  search, card show, rel, report --last

退出码：0 成功；1 用法 / 参数非法（零写入）。
`

// run 是可测试的入口：返回进程退出码，不直接调用 os.Exit。
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		fmt.Fprintln(stderr, "错误：缺少子命令")
		return 1
	}
	switch args[0] {
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return 0
	case "version", "--version":
		fmt.Fprintln(stdout, version.String())
		return 0
	default:
		fmt.Fprint(stderr, usage)
		fmt.Fprintf(stderr, "错误：未知命令 %q\n", args[0])
		return 1
	}
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
