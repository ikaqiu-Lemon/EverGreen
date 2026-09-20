package mdfile

// 审阅式 Note 的**批注词表与渲染标记**——内置七类固定标签、扩展 key 规则、Agent
// blockquote 标记的**单一真源**（Schema v2 契约 §4.2.2 / §5.1 / D-10）。
//
// # 为什么词表与标记落在 mdfile
//
// 批注词表与 `> **[Agent X]** ` 标记同时被三处消费：plan 校验 annotation 是否合法、
// store 的 v2 review writer 渲染标记、mdfile 的 review 读侧核对可见标记与机器锚点是否一致。
// 三处若各写一份「七类映射」或「标记字面量」，加一类批注就得改三处，第一个后果就是
// 「plan 认了但 writer 没渲染」或「writer 渲染了但 parser 认不出」的静默分歧。mdfile 是
// store 与 plan 的共同下游包（store/plan 都 import 它，它不反向 import 任何一方），
// 把词表、扩展规则与标记构造收在这里，三处才共用同一份字面量与同一套判定。
//
// 本文件只负责「批注 key → 渲染标签」这一层语义；机器锚点的编码 / 解码在 review_anchor.go，
// 审阅正文的读回在 review.go —— 三者同属 mdfile 的「审阅式 Note 线格式」但各管一件事。

import (
	"fmt"
	"regexp"
	"strings"
)

// builtinAnnotations 是七类内置批注的**声明序**固定映射（契约 §4.2.2 表格逐行逐字）。
//
// 顺序即契约表格顺序：guide / supplement / emphasis / summary / distinction /
// verification / reflection。用切片而非 map 承载，因为「声明序固定」本身是契约的一部分
// （诊断枚举合法 key 时按此序列出），map 的遍历序不可复算。
var builtinAnnotations = []struct{ Key, Label string }{
	{"guide", "导读"},
	{"supplement", "补充"},
	{"emphasis", "强调"},
	{"summary", "总结"},
	{"distinction", "辨析"},
	{"verification", "待验证"},
	{"reflection", "反思"},
}

// BuiltinAnnotationLabel 返回内置批注 key 的固定中文标签；非内置返回 ("", false)。
func BuiltinAnnotationLabel(key string) (string, bool) {
	for _, a := range builtinAnnotations {
		if a.Key == key {
			return a.Label, true
		}
	}
	return "", false
}

// BuiltinAnnotationKeys 按声明序返回七类内置 key（供诊断文案枚举合法取值，单一真源）。
func BuiltinAnnotationKeys() []string {
	keys := make([]string, len(builtinAnnotations))
	for i, a := range builtinAnnotations {
		keys[i] = a.Key
	}
	return keys
}

// extAnnotationKeyRe 是扩展批注 key 的**唯一**合法形态（契约 §4.2.2 逐字）：
// 首字符小写字母，其后 0..31 个小写字母 / 数字 / 下划线 / 连字符，总长 1..32。
var extAnnotationKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// ValidExtensionAnnotationKey 报告 key 是否为合法扩展批注 key（不含内置判定：调用方先查内置）。
func ValidExtensionAnnotationKey(key string) bool {
	return extAnnotationKeyRe.MatchString(key)
}

// agentMarkerOpen / agentMarkerClose 是 Agent blockquote 首行标记的**唯一**字面量
// （契约 §5.1：`> **[Agent <标签>]** `）。writer 渲染与 parser 识别共用这一份，
// 不各写一遍——否则「多一个空格」这类差异会让 render→parse→render 字节不稳。
const (
	agentMarkerOpen  = "> **[Agent "
	agentMarkerClose = "]** "
	// AgentQuotePrefix 是 Agent blockquote **续行**的引用前缀（首行用 AgentMarker）。
	AgentQuotePrefix = "> "
)

// AgentMarker 用给定的**显示标签**拼出 Agent blockquote 的首行标记。
func AgentMarker(label string) string {
	return agentMarkerOpen + label + agentMarkerClose
}

// displayAgentLabel 把标签渲染成**确定性单行**可见形态：合同不限自定义 label 的字符集，
// label 可含换行 / 回车（trim 后非空即合法），但 Agent 标记是**单行** blockquote 首行标记，
// 真换行会把标记劈成两行、被 parser 误当截断。因此可见层对换行 / 回车 / 反斜杠做无歧义转义
// （`\`→`\\`、`\n`→`\n`、`\r`→`\r`），机器锚点仍保留**原始** label 不动；writer 渲染与
// parser 核对共用本函数，双方对同一 label 得到同一可见字节，核对通过后再从锚点回读原值。
var agentLabelDisplayReplacer = strings.NewReplacer(
	`\`, `\\`,
	"\n", `\n`,
	"\r", `\r`,
)

func displayAgentLabel(label string) string {
	return agentLabelDisplayReplacer.Replace(label)
}

// ResolveAgentLabel 把 (annotation, label) 解析成 Agent 块的**显示标签**（契约 §4.2.2 / §5.1）。
//
//   - 内置 key：返回其固定中文标签（label 由 plan 侧保证为空，此处不读它，避免「用 label
//     悄悄改写内置含义」的第二条路径）；
//   - 合法扩展 key 且 label（trim 后）非空：返回**原始** label（不 trim、不改写，契约「按原 label 渲染」）；
//   - 其余：error（fail closed）——writer 绝不给一个来路不明的 annotation 编一个标签。
func ResolveAgentLabel(annotation, label string) (string, error) {
	if l, ok := BuiltinAnnotationLabel(annotation); ok {
		return l, nil
	}
	if ValidExtensionAnnotationKey(annotation) && strings.TrimSpace(label) != "" {
		return label, nil
	}
	return "", fmt.Errorf("annotation %q 既非内置七类、也非「合法扩展 key + 非空 label」，无法解析显示标签", annotation)
}
