package query

// Q 系列只读诊断（M2 查询合同 `2026-09-19-m2-query-contract.md` §5）。
//
// M1 的扫描对不可解析文件一律静默 `return nil`，后果是「缺失结果看起来像完整结果」
// （M-002 风险 R-1）。Q 系列就是该风险的关闭手段：**扫不动的东西必须说出来**。
//
// 三条硬口径（合同 §5.1）：
//   - Q 系列只用于**只读查询命令**，一律 warning，**不影响退出码**（只读命令仍退 0）；
//   - **不进** ChangePlan 的 E1–E6 / W1–W8 / I1 分级表，也不参与写路径任何判定；
//   - 每条诊断必须带**文件路径**与**人类可读原因**，人类可读输出与 --json 同源同事实。

import (
	"fmt"
	"sort"
)

// Q 码（合同 §5.1，恰三条，封闭集合）。
const (
	// CodeQ1 文件不可解析：Parse 失败 / frontmatter 解码失败 / 缺 id / 同 ID 重复。
	CodeQ1 = "Q1"
	// CodeQ2 悬空引用：relations[] 的 target 在全库中不存在。
	CodeQ2 = "Q2"
	// CodeQ3 结果不完整：本次扫描存在 ≥1 条 Q1 或 Q2 时的汇总项。
	CodeQ3 = "Q3"
)

// DiagLevel 是 Q 系列的分级：合同定死**一律 warning**，不存在第二个取值。
const DiagLevel = "warning"

// Diagnostic 是一条只读诊断（结构与 CLI 合同 §5 的诊断载荷同构：
// code / level / path / message；op_index 由 CLI 层统一填 -1，查询层不产生 op 概念）。
type Diagnostic struct {
	Code    string `json:"code"`
	Level   string `json:"level"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

// String 是人类可读形态。CLI 的纯文本输出与 --json 的 warnings[] 由同一批 Diagnostic
// 渲染，保证「同源同事实」——不得只在 --json 里出现。
func (d Diagnostic) String() string {
	return fmt.Sprintf("%s [%s] %s：%s", d.Level, d.Code, d.Path, d.Message)
}

// newQ1 记一条「文件不可解析」。path 必须是 vault 内相对路径。
func newQ1(path, format string, args ...interface{}) Diagnostic {
	return Diagnostic{Code: CodeQ1, Level: DiagLevel, Path: path,
		Message: fmt.Sprintf(format, args...)}
}

// newQ2 记一条「悬空引用」。message 必须含被引用的目标 ID（合同 §5.1）。
func newQ2(path, format string, args ...interface{}) Diagnostic {
	return Diagnostic{Code: CodeQ2, Level: DiagLevel, Path: path,
		Message: fmt.Sprintf(format, args...)}
}

// finalizeDiagnostics 给诊断集合排序并按需追加**恰一条** Q3 汇总项。
//
// 次序确定（合同 §5.1）：先按 code（Q1 → Q2），同码按 path 字典序，最后追加 Q3。
// 无 Q1 / Q2 时**不出现** Q3（反向可判）。
func finalizeDiagnostics(diags []Diagnostic) []Diagnostic {
	out := append([]Diagnostic{}, diags...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Code != out[j].Code {
			return out[i].Code < out[j].Code
		}
		return out[i].Path < out[j].Path
	})
	q1, q2 := 0, 0
	for _, d := range out {
		switch d.Code {
		case CodeQ1:
			q1++
		case CodeQ2:
			q2++
		}
	}
	if q1+q2 == 0 {
		return out
	}
	return append(out, Diagnostic{
		Code: CodeQ3, Level: DiagLevel, Path: "(汇总)",
		Message: fmt.Sprintf("本次结果不完整：跳过 %d 个不可解析文件、%d 条悬空引用；"+
			"缺失结果不等于完整结果，请按上列路径修复后重查", q1, q2),
	})
}
