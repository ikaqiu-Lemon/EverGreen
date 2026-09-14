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

// Q 码：S1/M2 恰三条（封闭集合）；**S3 起恰四条**——owner 裁决 A-39 放宽 M2 §5.1
// 「Q 码表（恰三条）」为四条，追加 CodeQ4，**Q1–Q3 的语义与次序一字不变**；
// **S4 起恰五条** —— M5 索引架构合同 §6.2（A-45 选项 ①）追加 CodeQ5，
// **Q1–Q4 的语义、取值与次序同样一字不变**，只多一个成员。
// A-39 的机器反证要求 CodeQ1..CodeQ4 **都定义在本文件**（internal/query/diagnostic.go），
// 故第四条与前三条同处一个 const 块，不散落到 card.go / relation.go；
// 第五条依同一条纪律也定义在这里，backend.go / degrade.go 只调用不持有。
const (
	// CodeQ1 文件不可解析：Parse 失败 / frontmatter 解码失败 / 缺 id / 同 ID 重复。
	CodeQ1 = "Q1"
	// CodeQ2 悬空引用：relations[] 的 target 在全库中不存在。
	CodeQ2 = "Q2"
	// CodeQ3 结果不完整：本次扫描存在 ≥1 条 Q1 或 Q2 时的汇总项。
	CodeQ3 = "Q3"
	// CodeQ4 默认视图隐藏了 N 个 deprecated 关系端点（T-…-061；owner 裁决 A-38/A-39）。
	//
	// A-38：被隐藏信息经 warnings[] 承载，`data` 键集合不扩张（不新增任何隐藏计数类第 N+1 键）。
	// A-39：M2 §5.1「Q 码表（恰三条）」放宽为四条，Q1–Q3 语义与次序不变，Q4 追加。
	// 口径：查询域、level=warning、path 逐字「(汇总)」、op_index 由 CLI 层统一填 -1、N≥1 才产出、
	// 每次至多一条、--include-deprecated 下恒不产出、**绝不触发 Q3**、
	// 诊断次序 Q1→Q2→Q4→Q3（Q3 恒末位）。**不使用 W21**（越域码，属 ChangePlan 分级表，查询域不得借用）。
	CodeQ4 = "Q4"
	// CodeQ5 本次读**走的是降级路径**：索引缺失 / 损坏 / 陈旧，取数已回落全量 Markdown
	// 扫描（M5 索引架构合同 §6.2 / §6.3；T-…-067）。
	//
	// 口径：查询域、level=warning、path 逐字「(汇总)」、op_index 由 CLI 层统一填 -1、
	// 每次读**至多一条**、与 `W22`|`W23`|`W24` 中的**恰一条**同现（有 Q5 必有原因码，
	// 反之亦然）、**健康索引下恒不产出**、**绝不触发 Q3**（它不是「结果不完整」：
	// 降级结果与索引在位时逐字相等）、也**不改变退出码**（只读命令仍退 0）。
	//
	// 「索引健康但本次查询不可由索引表达」**不是**降级，因此不产 Q5（见 backend.go 的
	// ReasonQueryNotExpressible）：那种情况下索引既没坏也没旧，没有任何要修的东西。
	CodeQ5 = "Q5"
)

// diagSummaryPath 是汇总类诊断（Q3 / Q4 / Q5 与降级原因码）的 path 逐字取值：
// 它们不属于某一个文件，路径位因此恒为「(汇总)」而不是空串——诊断必须有 path。
const diagSummaryPath = "(汇总)"

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

// withDeprecatedHiddenDiagnostic 按 A-38/A-39 口径在诊断集合里追加**至多一条** Q4
// （T-…-061）。归位说明：Q4 的码定义与组装逻辑都收在本文件（diagnostic.go），与 Q1–Q3 同处，
// 满足 A-39「CodeQ1..CodeQ4 都定义在 internal/query/diagnostic.go」的机器反证；
// card.go / relation.go 只调用、不再各自持有 Q4 的码或组装分支。
//
// 语义：includeDeprecated 为真或 hiddenDeprecated < 1 时不产出（Q4 只服务默认视图且 N≥1）。
// 插入位置：Q3 恒末位，故 Q4 插到末尾 Q3 之前；无 Q3 时追加到末尾（Q4 **不触发** Q3）。
func withDeprecatedHiddenDiagnostic(diags []Diagnostic, hiddenDeprecated int, includeDeprecated bool) []Diagnostic {
	if includeDeprecated || hiddenDeprecated < 1 {
		return diags // 显式放开或无隐藏：不产出 Q4
	}
	q4 := newQ4(hiddenDeprecated)
	if n := len(diags); n > 0 && diags[n-1].Code == CodeQ3 {
		out := append([]Diagnostic{}, diags[:n-1]...)
		out = append(out, q4, diags[n-1])
		return out
	}
	return append(append([]Diagnostic{}, diags...), q4)
}

// withIndexDegradedDiagnostics 把降级留痕（**恰一条** W22|W23|W24 + **恰一条** Q5）
// 追加进诊断集合（T-…-067）。extra 为空时原样返回（健康索引：一个字都不多）。
//
// 插入位置与 Q4 同一条纪律：**Q3 恒末位**，故降级两条插到末尾 Q3 之前；无 Q3 时追加到
// 末尾。整体次序因此确定为 Q1 → Q2 → Q4 → W2x → Q5 → Q3，同一状态两次执行逐字相同。
//
// 为什么不塞进 finalizeDiagnostics：那里按 code 字典序排，`W22` 会被排到 `Q…` 之后、
// 而 Q3 必须恒末位 —— 降级留痕是**读路径层**的事实（与「扫到了什么」无关），
// 因此与 Q4 一样在 finalize 之后按位置插入，不去动 M2 冻结的排序口径。
func withIndexDegradedDiagnostics(diags []Diagnostic, extra []Diagnostic) []Diagnostic {
	if len(extra) == 0 {
		return diags
	}
	if n := len(diags); n > 0 && diags[n-1].Code == CodeQ3 {
		out := append([]Diagnostic{}, diags[:n-1]...)
		out = append(out, extra...)
		return append(out, diags[n-1])
	}
	return append(append([]Diagnostic{}, diags...), extra...)
}

// newQ5 记一条「本次读已降级」的汇总诊断（path 逐字「(汇总)」）。
//
// message 只说三件事：哪条读路径、为什么降级（机器可读子因）、以及**结果仍然可信**
// （权威是 Markdown）。具体「索引怎么了 / 怎么修」由同现的 W22|W23|W24 交代，
// 两条各说一件事，不重复也不互相矛盾。
func newQ5(readPath, reason string) Diagnostic {
	return Diagnostic{Code: CodeQ5, Level: DiagLevel, Path: diagSummaryPath,
		Message: fmt.Sprintf("本次 %s 已降级为全量 Markdown 扫描（原因：%s）；"+
			"结果以权威 Markdown 为准、与索引在位时一致，但**不是**索引路径给出的结果 —— "+
			"详见同时给出的索引诊断", readPath, reason)}
}

// newQ4 记一条「默认视图隐藏了 N 个 deprecated 关系端点」的汇总诊断（path 逐字「(汇总)」）。
func newQ4(hiddenDeprecated int) Diagnostic {
	return Diagnostic{Code: CodeQ4, Level: DiagLevel, Path: "(汇总)",
		Message: fmt.Sprintf("默认视图隐藏了 %d 个失效（deprecated）关系端点；"+
			"如需查看请加 --include-deprecated（已删除对端不受此 flag 影响）", hiddenDeprecated)}
}
