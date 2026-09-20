package plan

// note_review_parse_test.go —— T-evergreen.knowledge_opinion_split-158614-012 · T12-1
// 「解析承载」批次的**先红**判据（Schema v2 契约 §4.2 / §4.2.1 / §4.2.3）。
//
// 本批**只**验证解析层是否把 §4.2 的 v2 新字段原样承载进 Op / NoteBlock，
// 不触碰任何 validator / executor / writer 语义（那些属后续批次）。因此所有判据都落在
// `plan.Parse` 的返回值上：解析后的 `Op` 字段、`NoteBlock` 字段与解析期诊断（`p.Diags`）。
//
// 为什么直接打 Parse 而不走 run()/Execute：T12-1 不改变任何诊断/落盘输出，
// 新字段此刻对 validate/execute 完全透明；唯一能证明「字段被承载而非被 classifyExtra
// 记 I1 后静默丢弃」的观测点，就是解析产物本身。
//
// 覆盖（对应 T12-1 边界 D 条 + 本轮审查补强）：
//   ① 完整字段**按序**解析：blocks[].source_ref/annotation/label（含**非空自定义 annotation + 非空 label**）、
//      omissions[].{source_ref,reason}、extraction_coverage[].{module,source_refs,summary,disposition,outputs,reason}；
//   ② *Given 真：显式给出（含空数组）时 OmissionsGiven / ExtractionCoverageGiven == true；
//   ③ *Given 假 + v1 回归：明确的 plan_version:1 + sections{} 解析不变，两个 Given == false；
//   ④ 未知嵌套字段 → I1（按 Diagnostic.Path 含键名计数，不依赖 Message 文案）；
//   ⑤ 数组/成员形态错误 → 字段级 E5：数组非数组 / 成员非对象 / **显式 null** /
//      source_refs·outputs 非数组 / source_refs·outputs 含非 string 成员，路径精确到字段或 [i]；
//   ⑥ 严格数组不做隐式转换：`7` 不得被悄悄变成 "7"，非数组不得被悄悄变成空数组。

import (
	"strings"
	"testing"
)

// parsePlan 是本文件的解析入口：只做 Parse，不做 validate/execute。
//
// Parse 只在「整份 plan 无法解析成映射」时返回 error；字段级问题一律进 p.Diags，
// 所以这里对 err 直接 fatal——测试构造的都是合法 JSON 对象，出 error 说明测试写坏了。
func parsePlan(t *testing.T, body string) *ChangePlan {
	t.Helper()
	p, err := Parse([]byte(body))
	if err != nil {
		t.Fatalf("plan 必须能解析成映射（字段级问题应进 diags 而非 error）：%v", err)
	}
	return p
}

// firstOp 取第 0 条 op，缺失即 fatal。
func firstOp(t *testing.T, p *ChangePlan) *Op {
	t.Helper()
	if len(p.Ops) == 0 {
		t.Fatal("plan 至少应解析出一条 op")
	}
	return p.Ops[0]
}

// fullWriteNote 是一份**字段齐全**的 v2 write_note：三个 blocks（source/agent/source 交错，
// 各带 §4.2 新字段；agent 块用**非空自定义 annotation + 非空 label** 以证明 parser 真读了 label）、
// 一条 omissions、两条 extraction_coverage。刻意让 module 逆字典序（m-002 在前、m-001 在后）
// 与 source_refs/outputs 多元素，以便同时钉住「顺序保留」。
const fullWriteNote = `{"ops":[{"op":"write_note","source":"s-20260915-example","note_id":"n-20261017-full",
 "blocks":[
   {"role":"source","source_ref":"L14-L28","heading":"1. 背景","body":"背景正文。"},
   {"role":"agent","annotation":"custom_note","label":"我的自定义标签","body":"辨析批注。"},
   {"role":"source","source_ref":"L29-L51","heading":"2. 方法","body":"方法正文。"}
 ],
 "omissions":[
   {"source_ref":"L52-L55","reason":"页脚导航噪声"}
 ],
 "extraction_coverage":[
   {"module":"m-002","source_refs":["L29-L51","L14-L28"],"summary":"方法三步","disposition":"outputs","outputs":["k-a","o-b"]},
   {"module":"m-001","source_refs":["L14-L28"],"summary":"背景","disposition":"note_only","reason":"无独立复用价值"}
 ]}]}`

// TestParseWriteNoteCarriesAllV2FieldsInOrder —— 判据①：全字段按序承载。
func TestParseWriteNoteCarriesAllV2FieldsInOrder(t *testing.T) {
	p := parsePlan(t, fullWriteNote)
	// 全字段都是 known key：既然逐字承载，就**不得**同时误报任何 I1（或其它诊断）。
	// 断言 p.Diags 恰空，直接钉死「承载了但又把 source_ref/annotation/label/omissions/
	// extraction_coverage 当未知字段记一条 I1」这种自相矛盾的实现。
	if len(p.Diags) != 0 {
		t.Fatalf("字段齐全的 v2 plan 不应产生任何诊断（新字段均为 known key），实得 %v", codes(p.Diags))
	}
	op := firstOp(t, p)

	// --- blocks[].source_ref / annotation / label（逐块、按序）---
	if len(op.Blocks) != 3 {
		t.Fatalf("应解析出 3 个 block（顺序保留、不丢块），实得 %d", len(op.Blocks))
	}
	if op.Blocks[0].Role != NoteBlockSource || op.Blocks[0].SourceRef != "L14-L28" ||
		op.Blocks[0].Heading != "1. 背景" || string(op.Blocks[0].Body) != "背景正文。" {
		t.Fatalf("block[0] 承载错误：%+v", op.Blocks[0])
	}
	// agent 块：非空自定义 annotation 与非空 label 都必须被读进结构体（证明 parser 真读 label）。
	if op.Blocks[1].Role != NoteBlockAgent || op.Blocks[1].Annotation != "custom_note" {
		t.Fatalf("block[1] 的 annotation 未承载：%+v", op.Blocks[1])
	}
	if op.Blocks[1].Label != "我的自定义标签" {
		t.Fatalf("block[1] 的 label 未承载（当前 %q）", op.Blocks[1].Label)
	}
	if op.Blocks[2].SourceRef != "L29-L51" || op.Blocks[2].Heading != "2. 方法" {
		t.Fatalf("block[2] 的 source_ref/heading 未按序承载：%+v", op.Blocks[2])
	}

	// --- omissions[].{source_ref, reason} ---
	if !op.OmissionsGiven {
		t.Fatal("给出 omissions 后 OmissionsGiven 必须为 true")
	}
	if len(op.Omissions) != 1 || op.Omissions[0].SourceRef != "L52-L55" ||
		op.Omissions[0].Reason != "页脚导航噪声" {
		t.Fatalf("omissions 承载错误：%+v", op.Omissions)
	}

	// --- extraction_coverage[]（顺序 + 嵌套数组顺序）---
	if !op.ExtractionCoverageGiven {
		t.Fatal("给出 extraction_coverage 后 ExtractionCoverageGiven 必须为 true")
	}
	if len(op.ExtractionCoverage) != 2 {
		t.Fatalf("应解析出 2 条覆盖项，实得 %d", len(op.ExtractionCoverage))
	}
	// 顺序保留：数组序 m-002 在前、m-001 在后（不排序、不重排）。
	if op.ExtractionCoverage[0].Module != "m-002" || op.ExtractionCoverage[1].Module != "m-001" {
		t.Fatalf("extraction_coverage 顺序被重排：%s,%s",
			op.ExtractionCoverage[0].Module, op.ExtractionCoverage[1].Module)
	}
	c0 := op.ExtractionCoverage[0]
	if c0.Summary != "方法三步" || c0.Disposition != "outputs" {
		t.Fatalf("覆盖项标量字段承载错误：%+v", c0)
	}
	// source_refs / outputs 的元素顺序逐字保留。
	if strings.Join(c0.SourceRefs, "|") != "L29-L51|L14-L28" {
		t.Fatalf("source_refs 元素顺序被改：%v", c0.SourceRefs)
	}
	if strings.Join(c0.Outputs, "|") != "k-a|o-b" {
		t.Fatalf("outputs 元素顺序被改：%v", c0.Outputs)
	}
	c1 := op.ExtractionCoverage[1]
	if c1.Disposition != "note_only" || c1.Reason != "无独立复用价值" {
		t.Fatalf("note_only 覆盖项承载错误：%+v", c1)
	}
}

// TestParseWriteNoteGivenFlagsDistinguishAbsentFromEmpty —— 判据②：
// 显式空数组 → Given=true 且长度 0（区分「缺字段」）。
func TestParseWriteNoteGivenFlagsDistinguishAbsentFromEmpty(t *testing.T) {
	empty := `{"ops":[{"op":"write_note","source":"s-x","note_id":"n-empty",
 "blocks":[{"role":"source","source_ref":"L1-L2","body":"正文。"}],
 "omissions":[],"extraction_coverage":[]}]}`
	op := firstOp(t, parsePlan(t, empty))
	if !op.OmissionsGiven || len(op.Omissions) != 0 {
		t.Fatalf("显式空 omissions：Given 应为 true 且长度 0，实得 given=%v len=%d",
			op.OmissionsGiven, len(op.Omissions))
	}
	if !op.ExtractionCoverageGiven || len(op.ExtractionCoverage) != 0 {
		t.Fatalf("显式空 extraction_coverage：Given 应为 true 且长度 0，实得 given=%v len=%d",
			op.ExtractionCoverageGiven, len(op.ExtractionCoverage))
	}
}

// TestParseV1SectionsRegression —— 判据③：明确的 v1（plan_version:1 + sections{}）解析不变。
//
// 刻意给出 plan_version:1 与**真实的 v1 固定分区名**（`材料提炼` / `Agent 分析`，二者按
// v1LegacyNoteOrder 都映射到 v2 的「整理正文」），而**不是**用「缺 plan_version 的 blocks
// fixture」冒充 v1：后者其实是 v2 形态，证明不了 v1 老写法在新增解析路径后仍然照旧。
func TestParseV1SectionsRegression(t *testing.T) {
	v1 := `{"plan_version":1,"ops":[{"op":"write_note","source":"s-y","note_id":"n-v1",
 "sections":{"材料提炼":"材料提炼正文。","Agent 分析":"Agent 分析正文。"}}]}`
	p := parsePlan(t, v1)
	if p.Version != 1 {
		t.Fatalf("plan_version 应解析为 1，实得 %d", p.Version)
	}
	op := firstOp(t, p)
	// v1 固定分区照旧解析。
	if op.Sections == nil || string(op.Sections["材料提炼"]) != "材料提炼正文。" ||
		string(op.Sections["Agent 分析"]) != "Agent 分析正文。" {
		t.Fatalf("v1 sections 解析被改变：%+v", op.Sections)
	}
	// v1 plan 既没有 blocks，也没有新的两组清单：三个 Given 全 false。
	if op.BlocksGiven || op.OmissionsGiven || op.ExtractionCoverageGiven {
		t.Fatalf("v1 plan 的 Given 全应为 false，实得 blocks=%v om=%v ec=%v",
			op.BlocksGiven, op.OmissionsGiven, op.ExtractionCoverageGiven)
	}
	// 回归：v1 plan 不应因新增解析路径而多出任何解析期诊断。
	if len(p.Diags) != 0 {
		t.Fatalf("v1 plan 不应新增解析期诊断，实得 %v", codes(p.Diags))
	}
}

// TestParseWriteNoteUnknownNestedFieldIsI1 —— 判据④：blocks / omissions /
// extraction_coverage 内的未知字段一律 I1（info、原样忽略），已知字段照常承载。
func TestParseWriteNoteUnknownNestedFieldIsI1(t *testing.T) {
	body := `{"ops":[{"op":"write_note","source":"s-z","note_id":"n-unknown",
 "blocks":[{"role":"source","source_ref":"L1-L3","body":"正文。","future_key":"x"}],
 "omissions":[{"source_ref":"L4-L5","reason":"噪声","future_key":"y"}],
 "extraction_coverage":[{"module":"m-1","source_refs":["L1-L3"],"summary":"s","disposition":"note_only","reason":"r","future_key":"z"}]}]}`
	p := parsePlan(t, body)
	op := firstOp(t, p)

	// 已知字段仍被承载（未知字段不得连累已知字段）。
	if op.Blocks[0].SourceRef != "L1-L3" || op.Omissions[0].Reason != "噪声" ||
		op.ExtractionCoverage[0].Module != "m-1" {
		t.Fatalf("未知字段影响了已知字段的承载：block=%+v om=%+v ec=%+v",
			op.Blocks[0], op.Omissions[0], op.ExtractionCoverage[0])
	}
	// 三处未知字段各产出一条 I1（info 级），无 error。键名落在诊断 Path 上
	// （形如 ops[0].omissions[0].future_key），**不**要求 Message 含键名。
	infos := 0
	for _, d := range p.Diags {
		if d.Level == LevelInfo && d.Code == I1 && strings.Contains(d.Path, "future_key") {
			infos++
		}
		if d.Level == LevelError {
			t.Fatalf("未知附加字段不得升级为 error：%v", d)
		}
	}
	if infos != 3 {
		t.Fatalf("三处未知嵌套字段应各产出一条 I1，实得 %d（diags=%v）", infos, codes(p.Diags))
	}
}

// TestParseWriteNoteMalformedArraysAreE5 —— 判据⑤：数组本身或其成员形态错误 → 字段级 E5。
//
// 逐子例分开，且路径精确到字段或 [i]；所有子例都不得 panic（能跑完即证明）。
func TestParseWriteNoteMalformedArraysAreE5(t *testing.T) {
	cases := []struct {
		name     string
		ops      string
		pathPart string
	}{
		{"omissions 非数组",
			`{"op":"write_note","source":"s","note_id":"n","omissions":"oops"}`,
			"ops[0].omissions"},
		{"omissions 显式 null",
			`{"op":"write_note","source":"s","note_id":"n","omissions":null}`,
			"ops[0].omissions"},
		{"omissions 成员非对象",
			`{"op":"write_note","source":"s","note_id":"n","omissions":["oops"]}`,
			"ops[0].omissions[0]"},
		{"extraction_coverage 非数组",
			`{"op":"write_note","source":"s","note_id":"n","extraction_coverage":42}`,
			"ops[0].extraction_coverage"},
		{"extraction_coverage 显式 null",
			`{"op":"write_note","source":"s","note_id":"n","extraction_coverage":null}`,
			"ops[0].extraction_coverage"},
		{"extraction_coverage 成员非对象",
			`{"op":"write_note","source":"s","note_id":"n","extraction_coverage":[7]}`,
			"ops[0].extraction_coverage[0]"},
		{"source_refs 非数组",
			`{"op":"write_note","source":"s","note_id":"n","extraction_coverage":[{"module":"m","source_refs":"L1-L2"}]}`,
			"ops[0].extraction_coverage[0].source_refs"},
		{"source_refs 含非 string 成员",
			`{"op":"write_note","source":"s","note_id":"n","extraction_coverage":[{"module":"m","source_refs":["L1-L2",7]}]}`,
			"ops[0].extraction_coverage[0].source_refs[1]"},
		{"outputs 非数组",
			`{"op":"write_note","source":"s","note_id":"n","extraction_coverage":[{"module":"m","outputs":"k-a"}]}`,
			"ops[0].extraction_coverage[0].outputs"},
		{"outputs 含非 string 成员",
			`{"op":"write_note","source":"s","note_id":"n","extraction_coverage":[{"module":"m","outputs":["k-a",5]}]}`,
			"ops[0].extraction_coverage[0].outputs[1]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := parsePlan(t, `{"ops":[`+c.ops+`]}`)
			var got *Diagnostic
			for i := range p.Diags {
				if p.Diags[i].Code == E5 && p.Diags[i].Path == c.pathPart {
					d := p.Diags[i]
					got = &d
					break
				}
			}
			if got == nil {
				t.Fatalf("形态错误必须在精确路径 %q 产出 E5，实得 diags=%v",
					c.pathPart, diagPaths(p.Diags))
			}
		})
	}
}

// TestParseCoverageArraysNoSilentConversion —— 判据⑥：严格数组不做隐式转换。
//
// source_refs=["L1-L2", 7]：非 string 成员 7 必须触发 E5，且**绝不**被悄悄转成 "7"
// 塞进结果——最终 SourceRefs 只保留合法的 "L1-L2"，不含 "7"。
func TestParseCoverageArraysNoSilentConversion(t *testing.T) {
	body := `{"ops":[{"op":"write_note","source":"s","note_id":"n",
 "extraction_coverage":[{"module":"m","source_refs":["L1-L2",7]}]}]}`
	p := parsePlan(t, body)
	if _, ok := find(p.Diags, E5); !ok {
		t.Fatalf("含非 string 成员应产出 E5，实得 %v", codes(p.Diags))
	}
	op := firstOp(t, p)
	if len(op.ExtractionCoverage) != 1 {
		t.Fatalf("覆盖项本身合法，应保留 1 条，实得 %d", len(op.ExtractionCoverage))
	}
	refs := op.ExtractionCoverage[0].SourceRefs
	for _, r := range refs {
		if r == "7" {
			t.Fatalf("非 string 成员 7 被静默转成了 \"7\"：%v", refs)
		}
	}
	if strings.Join(refs, "|") != "L1-L2" {
		t.Fatalf("应只保留合法成员 [L1-L2]（非法成员记 E5 后跳过、不转换），实得 %v", refs)
	}
}

// diagPaths 是失败信息用的路径清单（比整条 diag 更易读）。
func diagPaths(diags []Diagnostic) []string {
	out := make([]string, 0, len(diags))
	for _, d := range diags {
		out = append(out, d.Code+"@"+d.Path)
	}
	return out
}
