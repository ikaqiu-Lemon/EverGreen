// [S1] internal/version：版本信息，零依赖（不属 §13 的 S1 九包）。
//
// 允许依赖：无（仅 stdlib）。
// 说明：Version / Commit / Date 由 Makefile 的 -ldflags 注入，默认值用于本地开发
// （`go build` / `go run` / `go test` 不经 Makefile 时生效）。
//
// 版本号口径（T-…-027）：唯一决策文档
// ../teamwork/projects/evergreen/s1_main_flow/docs/specs/2026-10-03-m2-release-and-version.md。
// 本文件的 Version 默认字面量、`Makefile` 的 `VERSION ?=` 缺省值、决策文档声明的版本号
// **三处同源同值**（version_test.go 与 test/e2e/m2_docs_commands.sh 双向锁死）；
// M2 起默认值不再是 `0.0.0-dev`（M1 遗留的脚手架口径，风险 R-3）。
package version

import "runtime"

// DefaultVersion 是本地开发缺省版本号，与 Makefile 的 VERSION 缺省值逐字相同。
// 单独抽成常量，便于测试与文档做「三处同源」的逐字比对。
const DefaultVersion = "0.2.0-m2"

// ScaffoldVersion 是 M1 期的脚手架缺省值，仅作为**反证基线**保留：
// 默认版本号不得再回退到它（见 TestVersionNotScaffold）。
const ScaffoldVersion = "0.0.0-dev"

// 由 -ldflags "-X evergreen/internal/version.<字段>=<值>" 注入。
var (
	Version = DefaultVersion
	Commit  = "unknown"
	Date    = "unknown"
)

// String 返回单行版本描述，供 `eg --version` 输出（含版本号、commit、构建时间三段 + 工具链）。
func String() string {
	return "eg " + Version + " (commit " + Commit + ", built " + Date + ", " + runtime.Version() + ")"
}
