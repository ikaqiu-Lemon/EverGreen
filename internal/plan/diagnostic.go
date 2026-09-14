package plan

// 诊断分级与载荷结构（合同 `docs/specs/2026-09-08-changeplan-contract.md` §4 / §5）。
//
// error 恰 E1–E6，warning 恰 W1–W8，info 恰 I1；另有两条**未编号 warning**
// （`ops[]` 空列表、`verb` 未知值退化），其 Code 填空串，出处写在 Message 里。
// 编号集合是**封闭**的：严禁自创编号，也严禁把 W 升级成 error（严格化属 S5 / M6）。

import "fmt"

// error 级编号（触发即零写入，由 CLI 翻译成退出码 2）。
const (
	// E1 id 缺失或与全库既有产物 id 重复。
	E1 = "E1"
	// E2 关系 target（或 op 的目标 ID）无法解析：ID 不存在 / 格式非法。
	E2 = "E2"
	// E3 `s-` 写进 relations，或 `k-` 写进 sources。
	E3 = "E3"
	// E4 目标文件 frontmatter YAML 无法解析。
	E4 = "E4"
	// E5 plan_version 不匹配、未知 op、或 op 必填字段不成立（含 V3/V7 建卡缺 sources[]）。
	E5 = "E5"
	// E6 写入目标落在自动路径的「知识内容」，或任何时候的「用户补充」。
	E6 = "E6"
)

// warning / info 级编号（一律照写 + 进报告，不影响退出码）。
const (
	// W1 写入目标落在 plan.domain 之外（S5 起才严格化）。
	W1 = "W1"
	// W2 关系四要素不全，或 reason 为空 / 等于关系名本身。
	W2 = "W2"
	// W3 论证关系某端不是 status: active（**S2 起判定**、S5 起 error）。
	W3 = "W3"
	// W4 顶层命中废弃字段黑名单（按字段路径判定）。
	W4 = "W4"
	// W5 convergence[] 缺条目 / 三维度不齐 / relation 异常。
	W5 = "W5"
	// W6 base 未覆盖某个被改文件（+ 跳过该文件）。
	W6 = "W6"
	// W7 状态类 op 的相关告警（**S1 无状态类 op，保留占位**）。
	W7 = "W7"
	// W8 opposing 方向未规范化，或同对重复。
	W8 = "W8"
	// I1 未知附加字段 / 未知分区 / coverage_gaps 透传等原样保留类信息。
	I1 = "I1"
)

// Unnumbered 是未编号 warning 的 Code 取值（§4.5.1 表未收录这些条目）。
// **不得**用 W5 顶替：W5 的原义是 convergence[] 缺条目或三维度不齐。
const Unnumbered = ""

// 未编号 warning 的出处文案（下游按前缀断言）。
const (
	// OpsEmptyNotice 是 `ops[]` 为空列表的未编号 warning 文案（§4.5 `ops[]` 行）。
	OpsEmptyNotice = "未编号 warning（§4.5 `ops[]` 空列表）：ops[] 为空，输出零写入报告"
	// VerbUnknownNotice 是 verb 缺失 / 未知值退化的未编号 warning 文案。
	VerbUnknownNotice = "未编号 warning（§4.5 `verb` 未知值退化）：verb 未知，已退化为 process"
)

// 诊断级别。
const (
	LevelError   = "error"
	LevelWarning = "warning"
	LevelInfo    = "info"
)

// NonOp 是非 op 级诊断的 op 下标占位值（合同 §5）。
const NonOp = -1

// Diagnostic 是一条诊断，字段恰六个，与 CLI 合同 §5 同一结构。
// 每条 error / warning / info 都必须带 op 下标 + 字段路径。
type Diagnostic struct {
	Code    string `json:"code"`
	Level   string `json:"level"`
	Path    string `json:"path"`
	OpIndex int    `json:"op_index"`
	Message string `json:"message"`
	Target  string `json:"target,omitempty"`
}

// String 是可读单行（供人类可读渲染与用例断言）。
func (d Diagnostic) String() string {
	return fmt.Sprintf("[%s] %s ops[%d] %s：%s", d.Code, d.Level, d.OpIndex, d.Path, d.Message)
}

func errorAt(code string, opIndex int, path, msg string, args ...interface{}) Diagnostic {
	return Diagnostic{Code: code, Level: LevelError, Path: path, OpIndex: opIndex,
		Message: fmt.Sprintf(msg, args...)}
}

func warnAt(code string, opIndex int, path, msg string, args ...interface{}) Diagnostic {
	return Diagnostic{Code: code, Level: LevelWarning, Path: path, OpIndex: opIndex,
		Message: fmt.Sprintf(msg, args...)}
}

func infoAt(opIndex int, path, msg string, args ...interface{}) Diagnostic {
	return Diagnostic{Code: I1, Level: LevelInfo, Path: path, OpIndex: opIndex,
		Message: fmt.Sprintf(msg, args...)}
}

// opPath 拼字段路径 `ops[N].<field>`（诊断必须可定位到字段）。
func opPath(index int, field string) string {
	if field == "" {
		return fmt.Sprintf("ops[%d]", index)
	}
	return fmt.Sprintf("ops[%d].%s", index, field)
}
