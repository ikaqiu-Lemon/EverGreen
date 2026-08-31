// [S1] internal/version：脚手架用版本信息，零依赖（不属 §13 的 S1 九包）。
//
// 允许依赖：无（仅 stdlib）。
// 说明：Version / Commit / Date 由 Makefile 的 -ldflags 注入，默认值用于本地开发。
package version

import "runtime"

// 由 -ldflags "-X evergreen/internal/version.<字段>=<值>" 注入。
var (
	Version = "0.0.0-dev"
	Commit  = "unknown"
	Date    = "unknown"
)

// String 返回单行版本描述，供 `eg version` 输出。
func String() string {
	return "eg " + Version + " (commit " + Commit + ", built " + Date + ", " + runtime.Version() + ")"
}
