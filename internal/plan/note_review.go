package plan

// v2 审阅式提炼的**解析层**：`write_note.omissions[]`（§4.2.1）与
// `write_note.extraction_coverage[]`（§4.2.3）的反序列化。
//
// 与 blocks.go 同一分层原则：本文件只回答「字段长什么样」，不回答「这样写允不允许」。
// 因此这里只做三件事——把数组读成有序切片、把每项的已知键读进结构体、对未知嵌套字段记 I1；
// 语义判定（source_ref 能否解析、module 是否唯一、覆盖是否相交、disposition 取值、
// 与 output_cards 是否一致等）一律留给后续批次的 validate。
//
// 唯一在解析期就必须表态的错误是**形态错误**（数组不是数组、成员不是对象）：这类问题会让
// 「静默丢元素」与「解析出一条能跑但语义已残缺的 op」二选一，两者都比一条字段级 E5 更糟，
// 所以在这里就地判 E5（与 parseNoteBlocks 对 blocks 的处理完全对称）。

import (
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// Omission / ExtractionCoverage 是 store 侧数据结构的**类型别名**（理由同 NoteBlock）：
// 落盘渲染的唯一实现将在 store，plan 只把用户给的项原样传过去，不另立同形结构体。
type Omission = store.Omission

// ExtractionCoverage 见 store.ExtractionCoverage。
type ExtractionCoverage = store.ExtractionCoverage

// omissionKnownKeys 是单条 omission 的字段表（恰两键，契约 §4.2.1）。
func omissionKnownKeys() []string { return []string{"source_ref", "reason"} }

// extractionCoverageKnownKeys 是单条覆盖项的字段表（恰六键，契约 §4.2.3；声明序即字段表顺序）。
func extractionCoverageKnownKeys() []string {
	return []string{"module", "source_refs", "summary", "disposition", "outputs", "reason"}
}

// parseOmissions 解析 `omissions[]`。
//
// 顺序逐字保留（数组序即报告序，不排序/不去重）。值非列表（**含键显式存在但值为 null**）
// → 字段级 E5；单项非对象 → 该项处 E5 并跳过（不静默塞入空 Omission）；未知键 → I1。
func parseOmissions(opIndex int, v interface{}) ([]Omission, []Diagnostic) {
	var diags []Diagnostic
	items, ok := asList(v)
	if !ok {
		return nil, append(diags, errorAt(E5, opIndex, opPath(opIndex, "omissions"),
			"omissions 必须是有序遗漏项列表（数组顺序即报告顺序；键存在则值不得为 null 或非数组）"))
	}
	known := set(omissionKnownKeys())
	out := make([]Omission, 0, len(items))
	for i, item := range items {
		m, ok := asMap(item)
		if !ok {
			diags = append(diags, errorAt(E5, opIndex, omissionPath(opIndex, i, ""),
				"omissions 的每一项必须是对象（恰 %v）", omissionKnownKeys()))
			continue
		}
		o := Omission{}
		o.SourceRef, _ = asString(m["source_ref"])
		o.Reason, _ = asString(m["reason"])
		for k := range m {
			if known[k] {
				continue
			}
			diags = append(diags, infoAt(opIndex, omissionPath(opIndex, i, k),
				"未知附加字段已原样忽略（前向兼容：omissions 的字段表恰 %v）", omissionKnownKeys()))
		}
		out = append(out, o)
	}
	return out, diags
}

// parseExtractionCoverage 解析 `extraction_coverage[]`。
//
// 顺序逐字保留（数组序即渲染序）。值非列表（**含键显式存在但值为 null**）→ 字段级 E5；
// 单项非对象 → 该项处 E5 并跳过；未知键 → I1。source_refs / outputs 走**严格数组**解析
// （见 parseStringArrayStrict）：必须是数组、每项必须是 string，绝不把 7 转成 "7"、
// 也绝不把标量静默变成空数组；「非空 / 引用有效 / 与 output_cards 一致」等语义判定属后续批次。
func parseExtractionCoverage(opIndex int, v interface{}) ([]ExtractionCoverage, []Diagnostic) {
	var diags []Diagnostic
	items, ok := asList(v)
	if !ok {
		return nil, append(diags, errorAt(E5, opIndex, opPath(opIndex, "extraction_coverage"),
			"extraction_coverage 必须是有序覆盖项列表（数组顺序即渲染顺序；键存在则值不得为 null 或非数组）"))
	}
	known := set(extractionCoverageKnownKeys())
	out := make([]ExtractionCoverage, 0, len(items))
	for i, item := range items {
		m, ok := asMap(item)
		if !ok {
			diags = append(diags, errorAt(E5, opIndex, coveragePath(opIndex, i, ""),
				"extraction_coverage 的每一项必须是对象（恰 %v）", extractionCoverageKnownKeys()))
			continue
		}
		c := ExtractionCoverage{}
		c.Module, _ = asString(m["module"])
		c.Summary, _ = asString(m["summary"])
		c.Disposition, _ = asString(m["disposition"])
		c.Reason, _ = asString(m["reason"])
		if sv, ok := m["source_refs"]; ok {
			refs, rd := parseStringArrayStrict(opIndex, coveragePath(opIndex, i, "source_refs"), sv)
			c.SourceRefs = refs
			diags = append(diags, rd...)
		}
		if ov, ok := m["outputs"]; ok {
			outs, od := parseStringArrayStrict(opIndex, coveragePath(opIndex, i, "outputs"), ov)
			c.Outputs = outs
			diags = append(diags, od...)
		}
		for k := range m {
			if known[k] {
				continue
			}
			diags = append(diags, infoAt(opIndex, coveragePath(opIndex, i, k),
				"未知附加字段已原样忽略（前向兼容：extraction_coverage 的字段表恰 %v）",
				extractionCoverageKnownKeys()))
		}
		out = append(out, c)
	}
	return out, diags
}

// omissionPath / coveragePath 是两组清单的字段级诊断路径（对齐 blockPath 的形态）。
func omissionPath(opIndex, i int, key string) string {
	base := fmt.Sprintf("ops[%d].omissions[%d]", opIndex, i)
	if key == "" {
		return base
	}
	return base + "." + key
}

func coveragePath(opIndex, i int, key string) string {
	base := fmt.Sprintf("ops[%d].extraction_coverage[%d]", opIndex, i)
	if key == "" {
		return base
	}
	return base + "." + key
}

// parseStringArrayStrict 是「必须是字符串数组」的**严格**解析器（契约 §4.2.3 的 source_refs / outputs）。
//
// 与包内既有的宽松助手 asStringSlice 刻意分开：asStringSlice 会把标量 7 经 asString 变成 "7"、
// 把「非数组」经 listOf 变成空切片——这两种宽松转换会让「用户写错了类型」在解析期被悄悄抹平，
// 等语义批次再看时已无从分辨。严格解析在此就地表态：
//   - 值非数组（含键显式存在但值为 null）→ 定位到**字段**的 E5；
//   - 某成员非 string（int / bool / 对象 / null …）→ 定位到 **[i]** 的 E5，跳过该成员但**不静默**
//     （已产出诊断即非静默），绝不把它转成字符串塞进结果。
//
// 只做类型形态判定，不判空、不判引用有效性（那些属后续批次）；任何输入都不 panic。
func parseStringArrayStrict(opIndex int, path string, v interface{}) ([]string, []Diagnostic) {
	items, ok := asList(v)
	if !ok {
		return nil, []Diagnostic{errorAt(E5, opIndex, path,
			"%s 必须是字符串数组（值非数组，含显式 null）", path)}
	}
	var diags []Diagnostic
	out := make([]string, 0, len(items))
	for i, item := range items {
		s, isStr := item.(string)
		if !isStr {
			diags = append(diags, errorAt(E5, opIndex, fmt.Sprintf("%s[%d]", path, i),
				"%s[%d] 必须是字符串（不做隐式类型转换）", path, i))
			continue
		}
		out = append(out, s)
	}
	return out, diags
}
