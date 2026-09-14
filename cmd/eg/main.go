// Command eg 是 Evergreen 的确定性本地 CLI 的唯一入口（单二进制）。
//
// 命令注册树、参数、退出码映射与双渲染全部在 internal/cli；本文件只做两件事：
// 把 os.Args 交给 cli.Root.Run，并把返回的退出码交给 os.Exit。
//
// **本文件是全仓唯一允许调用 os.Exit 的地方**（T-…-003 Acceptance 的 grep 反证）：
// 业务层只返回带类型的错误，退出码由 internal/cli/exit.go 集中翻译一次。
//
// eg 不调用任何模型、不做任何网络请求（合同硬边界 1）。
package main

import (
	"os"

	"github.com/ikaqiu-Lemon/EverGreen/internal/cli"
)

func main() {
	os.Exit(cli.New().Run(os.Args[1:], os.Stdout, os.Stderr))
}
