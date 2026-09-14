// [S1] internal/version：版本信息，零依赖（不属 §13 的 S1 九包）。
//
// 允许依赖：无（仅 stdlib）。
// 说明：Version / Commit / Date 由 Makefile 的 -ldflags 注入，默认值用于本地开发
// （`go build` / `go run` / `go test` 不经 Makefile 时生效）。
//
// The default version is the source-build fallback. Keep it byte-for-byte in
// sync with `VERSION ?=` in the Makefile; release builds inject the selected
// version, commit, and build time through -ldflags.
package version

import "runtime"

// DefaultVersion is the local-development fallback and must match Makefile.
const DefaultVersion = "0.6.0-m6"

// ScaffoldVersion is retained as a regression sentinel.
const ScaffoldVersion = "0.0.0-dev"

// Version, Commit, and Date are overridden by release build flags.
var (
	Version = DefaultVersion
	Commit  = "unknown"
	Date    = "unknown"
)

// String returns the single-line version description used by eg --version.
func String() string {
	return "eg " + Version + " (commit " + Commit + ", built " + Date + ", " + runtime.Version() + ")"
}
