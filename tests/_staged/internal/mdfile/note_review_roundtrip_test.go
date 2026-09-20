package mdfile

// note_review_roundtrip_test.go —— T-evergreen.knowledge_opinion_split-158614-012 · T12-3
// 审阅式 Note 线格式的 **writer/parser round-trip 与 fail-closed** 验收
// （Schema v2 契约 §4.2 第 4 条 / §4.2.2 / §5.1）。
//
// RenderReviewNote 是 writer、ParseReviewNote 是 parser，二者必须互逆：解析回来的结构
// 逐字段等于原输入（body 以 bytes.Trim 掉首尾换行为准），再次渲染字节完全稳定。读侧对一切
// 畸形 / 未知协议、非法元数据、可见标签与锚点不一致、截断 blockquote 一律 fail closed。
//
// 白盒（package mdfile）：直接用未导出的 encodeReviewAnchor / reviewAnchor 构造**合法结构但
// 语义非法**的锚点（如 source 锚点携 annotation、agent 锚点携 source_ref），这类输入无法用
// 导出 API 造出，却正是 parser 必须拒绝的形态。

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

// —— 构造器 ——

func rtSrc(heading, sourceRef, body string) ReviewBlock {
	return ReviewBlock{Role: ReviewRoleSource, Heading: heading, SourceRef: sourceRef, Body: []byte(body)}
}

func rtAgent(heading, annotation, label, body string) ReviewBlock {
	return ReviewBlock{Role: ReviewRoleAgent, Heading: heading, Annotation: annotation, Label: label, Body: []byte(body)}
}

// assertRoundTrip 渲染 → 解析 → 逐字段比对 → 再渲染比字节。
func assertRoundTrip(t *testing.T, blocks []ReviewBlock, oms []ReviewOmission) {
	t.Helper()
	rendered, err := RenderReviewNote(blocks, oms)
	if err != nil {
		t.Fatalf("RenderReviewNote 不应失败：%v", err)
	}
	got, err := ParseReviewNote(rendered)
	if err != nil {
		t.Fatalf("ParseReviewNote 不应失败：%v\n渲染字节：\n%s", err, rendered)
	}
	if len(got.Blocks) != len(blocks) {
		t.Fatalf("块数不一致：want %d got %d\n%s", len(blocks), len(got.Blocks), rendered)
	}
	for i := range blocks {
		wantBody := bytes.Trim(blocks[i].Body, "\n")
		g := got.Blocks[i]
		if g.Role != blocks[i].Role || g.Heading != blocks[i].Heading ||
			g.SourceRef != blocks[i].SourceRef || g.Annotation != blocks[i].Annotation ||
			g.Label != blocks[i].Label || !bytes.Equal(g.Body, wantBody) {
			t.Fatalf("block[%d] 解析不等价：\nwant role=%q head=%q ref=%q ann=%q label=%q body=%q\ngot  role=%q head=%q ref=%q ann=%q label=%q body=%q",
				i, blocks[i].Role, blocks[i].Heading, blocks[i].SourceRef, blocks[i].Annotation, blocks[i].Label, wantBody,
				g.Role, g.Heading, g.SourceRef, g.Annotation, g.Label, g.Body)
		}
	}
	if len(got.Omissions) != len(oms) {
		t.Fatalf("omission 数不一致：want %d got %d", len(oms), len(got.Omissions))
	}
	for i := range oms {
		if got.Omissions[i] != oms[i] {
			t.Fatalf("omission[%d] 解析不等价：want %+v got %+v", i, oms[i], got.Omissions[i])
		}
	}
	rendered2, err := RenderReviewNote(got.Blocks, got.Omissions)
	if err != nil {
		t.Fatalf("二次渲染不应失败：%v", err)
	}
	if !bytes.Equal(rendered, rendered2) {
		t.Fatalf("render→parse→render 字节不稳定：\n首次：\n%s\n二次：\n%s", rendered, rendered2)
	}
}

// TestReviewRoundTripMatrix —— 覆盖交错 / heading 有无 / 多段·列表·围栏 body /
// 多行 agent body / 七类 + 扩展 / 多条 omissions 顺序。
func TestReviewRoundTripMatrix(t *testing.T) {
	cases := []struct {
		name   string
		blocks []ReviewBlock
		oms    []ReviewOmission
	}{
		{"单来源无 heading", []ReviewBlock{rtSrc("", "L1-L1", "正文一。")}, nil},
		{"单来源带 heading", []ReviewBlock{rtSrc("第一节", "L1-L2", "正文一。")}, nil},
		{"source/agent 交错", []ReviewBlock{
			rtSrc("背景", "L1-L3", "背景正文。"),
			rtAgent("", "emphasis", "", "这里是重点。"),
			rtSrc("方法", "L4-L6", "方法正文。"),
		}, nil},
		{"多段 source body", []ReviewBlock{rtSrc("", "L1-L4", "第一段。\n\n第二段。")}, nil},
		{"列表 source body", []ReviewBlock{rtSrc("", "L1-L3", "- 甲\n- 乙\n- 丙")}, nil},
		{"围栏代码 source body", []ReviewBlock{rtSrc("", "L1-L4", "```go\nx := 1\n```")}, nil},
		{"多行 agent body 含内部空行", []ReviewBlock{
			rtSrc("", "L1-L1", "s"),
			rtAgent("", "reflection", "", "第一行。\n\n第三行。"),
		}, nil},
		{"agent body 内含引用前缀行", []ReviewBlock{
			rtSrc("", "L1-L1", "s"),
			rtAgent("", "supplement", "", "> 引用内引用\n普通行"),
		}, nil},
		{"扩展 annotation 带 label", []ReviewBlock{
			rtSrc("", "L1-L1", "s"),
			rtAgent("小节", "case_study", "案例研究", "扩展批注。"),
		}, nil},
		{"多条 omissions 顺序", []ReviewBlock{rtSrc("", "L1-L2", "s")}, []ReviewOmission{
			{SourceRef: "L3-L4", Reason: "页脚导航噪声"},
			{SourceRef: "L5-L6", Reason: "广告横幅"},
		}},
		// 自定义 label 只要求非空，可能含 marker 子串 "]** "：recoverAgentBlock 不得用「首次
		// close」猜 label（那会把标签截成 "A"），必须用锚点得出的 want 生成完整 marker 精确匹配。
		{"label 含 marker 子串", []ReviewBlock{
			rtSrc("", "L1-L1", "s"),
			rtAgent("", "case_x", "A]** B", "扩展批注。"),
		}, nil},
		// 内置七类的 label 规则与 plan 同为「TrimSpace 后非空才拒」：纯空白 label 是 plan 放行
		// 并落盘的合法值，reader/writer 必须原样 round-trip（不得静默把空白 label 归一成 ""）。
		{"内置 key 携纯空白 label", []ReviewBlock{
			rtSrc("", "L1-L1", "s"),
			rtAgent("", "guide", "   ", "批注。"),
		}, nil},
		// 自定义 label 合同仅要求 trim 后非空、不限字符集：含换行的 label 合法。可见标签必须
		// 确定性单行转义/显示，机器锚点仍保留**原始** label，parser 用同一显示函数核对后回读原值。
		{"自定义 label 含换行", []ReviewBlock{
			rtSrc("", "L1-L1", "s"),
			rtAgent("", "case_y", "上\n下", "扩展批注。"),
		}, nil},
		{"自定义 label 含 CRLF 与制表", []ReviewBlock{
			rtSrc("", "L1-L1", "s"),
			rtAgent("", "case_z", "甲\r\n乙\t丙", "扩展批注。"),
		}, nil},
		// source body 尾部仅含空格 / Tab 的真实行：writer 只 Trim 换行符逐字落盘，parser 只能
		// 剥掉模板自己产生的恰一个分隔空行，不能 TrimSpace 连续裁掉，否则丢掉这行破坏字节稳定。
		{"source body 尾部空格行", []ReviewBlock{rtSrc("", "L1-L2", "文本一。\n   ")}, nil},
		{"source body 尾部 Tab 行", []ReviewBlock{rtSrc("", "L1-L2", "文本一。\n\t")}, nil},
		{"source body 中段空行后尾部空格行", []ReviewBlock{rtSrc("", "L1-L3", "甲。\n\n乙。\n  ")}, nil},
		// agent body 尾部仅含空格的真实行：续行渲染只对**真正空行**用 ">"，含空白的行必须
		// 用 "> " + 原行保留，round-trip 才字节等价。
		{"agent body 尾部空格行", []ReviewBlock{
			rtSrc("", "L1-L1", "s"),
			rtAgent("", "supplement", "", "批注。\n   "),
		}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { assertRoundTrip(t, c.blocks, c.oms) })
	}
}

// TestReviewRoundTripAllBuiltinAnnotations —— 七类内置批注逐类 round-trip，
// 且渲染出的可见标签就是契约固定中文。
func TestReviewRoundTripAllBuiltinAnnotations(t *testing.T) {
	for _, kv := range []struct{ key, label string }{
		{"guide", "导读"}, {"supplement", "补充"}, {"emphasis", "强调"},
		{"summary", "总结"}, {"distinction", "辨析"}, {"verification", "待验证"},
		{"reflection", "反思"},
	} {
		t.Run(kv.key, func(t *testing.T) {
			blocks := []ReviewBlock{
				rtSrc("", "L1-L1", "来源。"),
				rtAgent("", kv.key, "", "批注文本。"),
			}
			rendered, err := RenderReviewNote(blocks, nil)
			if err != nil {
				t.Fatalf("渲染失败：%v", err)
			}
			want := "> **[Agent " + kv.label + "]** 批注文本。"
			if !strings.Contains(string(rendered), want) {
				t.Fatalf("内置 %s 应渲染固定标签 %q，实得：\n%s", kv.key, want, rendered)
			}
			assertRoundTrip(t, blocks, nil)
		})
	}
}

// TestReviewCustomLabelNewlineDisplayIsSingleLine —— 含换行的合法自定义 label：可见 Agent 标记
// 必须是**确定性单行**（换行 / 回车被转义、不真正断行），而机器锚点仍保留**原始** label；
// parser 用同一显示函数核对可见标记后，回读的 Label 必须字节等于原始含换行值。
func TestReviewCustomLabelNewlineDisplayIsSingleLine(t *testing.T) {
	label := "上\n下"
	blocks := []ReviewBlock{
		rtSrc("", "L1-L1", "来源。"),
		rtAgent("", "case_y", label, "扩展批注。"),
	}
	rendered, err := RenderReviewNote(blocks, nil)
	if err != nil {
		t.Fatalf("渲染失败：%v", err)
	}
	// 可见标记行确定性单行：换行被转义为字面 "\n"，整条标记落在一行、正文紧随其后。
	wantMarker := "> **[Agent 上\\n下]** 扩展批注。"
	if !strings.Contains(string(rendered), wantMarker) {
		t.Fatalf("含换行 label 的可见标记应确定性单行转义为 %q，实得：\n%s", wantMarker, rendered)
	}
	// 可见层不得出现真正断行后的裸标签尾（说明标记被换行劈成两行）。
	if strings.Contains(string(rendered), "上\n下]** ") {
		t.Fatalf("含换行 label 的可见标记被真实换行劈开（非单行）：\n%s", rendered)
	}
	// 机器锚点保留原始 label：parse 回读的 Label 必须字节等于原始含换行值。
	got, err := ParseReviewNote(rendered)
	if err != nil {
		t.Fatalf("解析失败：%v\n%s", err, rendered)
	}
	if len(got.Blocks) != 2 || got.Blocks[1].Label != label {
		t.Fatalf("机器锚点应回读原始含换行 label %q，实得 %q", label, got.Blocks[1].Label)
	}
	assertRoundTrip(t, blocks, nil)
}

// TestReviewWriterRejectsIllegalCombos —— writer 侧 fail closed：非法字段组合 / 无法解析的
// annotation / 空正文一律 error，绝不编造字节。
func TestReviewWriterRejectsIllegalCombos(t *testing.T) {
	cases := []struct {
		name   string
		blocks []ReviewBlock
	}{
		{"空 blocks", nil},
		{"source 带 annotation", []ReviewBlock{{Role: ReviewRoleSource, SourceRef: "L1-L1", Annotation: "supplement", Body: []byte("x")}}},
		{"source 带 label", []ReviewBlock{{Role: ReviewRoleSource, SourceRef: "L1-L1", Label: "标签", Body: []byte("x")}}},
		{"agent 带 source_ref", []ReviewBlock{{Role: ReviewRoleAgent, Annotation: "supplement", SourceRef: "L1-L1", Body: []byte("x")}}},
		{"agent 非法扩展 key", []ReviewBlock{{Role: ReviewRoleAgent, Annotation: "Bad Key", Label: "标签", Body: []byte("x")}}},
		{"agent 扩展 key 缺 label", []ReviewBlock{{Role: ReviewRoleAgent, Annotation: "custom_note", Body: []byte("x")}}},
		// 内置 key + trim 后非空 label：writer 必须拒绝——ResolveAgentLabel 对内置 key 无条件回固定
		// 标签、不看 label，若 writer 接受则该字节被 parser（validateAgentAnchorAnnotation）拒，
		// 破坏 writer/parser 互逆。writer 与 parser 复用同一判定后，此组合两侧都 error。
		{"agent 内置 key 携非空 label", []ReviewBlock{
			{Role: ReviewRoleSource, SourceRef: "L1-L1", Body: []byte("来源。")},
			{Role: ReviewRoleAgent, Annotation: "guide", Label: "擅自改写", Body: []byte("x")},
		}},
		{"正文为空", []ReviewBlock{{Role: ReviewRoleSource, SourceRef: "L1-L1", Body: []byte("  \n")}}},
		{"role 越界", []ReviewBlock{{Role: "mystery", Body: []byte("x")}}},
		{"source 缺 source_ref", []ReviewBlock{{Role: ReviewRoleSource, Body: []byte("x")}}},
		// source_ref 的「非空」按 validator 的 TrimSpace 口径：纯空白 source_ref 不是有效行段回指，
		// writer 公共边界必须拒绝（否则落盘一份 source_ref 形同虚设的整理正文）。
		{"source source_ref 纯空白", []ReviewBlock{{Role: ReviewRoleSource, SourceRef: "   ", Body: []byte("x")}}},
		{"通篇无 source 块", []ReviewBlock{{Role: ReviewRoleAgent, Annotation: "supplement", Body: []byte("x")}}},
		// 机器协议完整单行若原样保留进 source 正文，parser 会把它误当下一锚点：writer 必须检出
		// 保留字冲突并拒绝，而不是落盘一份读回来会串块的正文。
		{"source body 含机器锚点保留字", []ReviewBlock{{
			Role: ReviewRoleSource, SourceRef: "L1-L1",
			Body: []byte("正常一行\n" + craftAnchor(reviewAnchor{Kind: anchorKindSource, SourceRef: "L9-L9"}) + "\n更多"),
		}}},
		{"agent body 含机器锚点保留字", []ReviewBlock{
			{Role: ReviewRoleSource, SourceRef: "L1-L1", Body: []byte("来源。")},
			{Role: ReviewRoleAgent, Annotation: "supplement",
				Body: []byte("正常一行\n" + craftAnchor(reviewAnchor{Kind: anchorKindAgent, Annotation: "guide"}) + "\n更多")},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := RenderReviewNote(c.blocks, nil); err == nil {
				t.Fatalf("非法输入必须被 writer 拒绝，实得无 error")
			}
		})
	}
}

// craftAnchor 用未导出编码器造一条锚点行（供 fail-closed 构造合法结构但语义非法的输入）。
func craftAnchor(a reviewAnchor) string { return encodeReviewAnchor(a) }

// craftRawAnchor 把**任意** JSON 文本编码进锚点：用于造 Go struct 造不出的畸形载荷
// （顶层多个对象、重复键），验证 decodeReviewAnchor 的 io.EOF / 去重 fail-closed。
func craftRawAnchor(payload string) string {
	return reviewAnchorOpen + base64.RawURLEncoding.EncodeToString([]byte(payload)) + reviewAnchorClose
}

// validSourceUnit 是一段合法的 source 锚点 + 可见正文（供需要「前置至少一个 source」的
// 负向用例复用：让被测的非法点是唯一的失败原因，而不是撞上「通篇无 source」）。
func validSourceUnit() string {
	return craftAnchor(reviewAnchor{Kind: anchorKindSource, SourceRef: "L1-L1"}) + "\n来源正文。\n\n"
}

// TestReviewParserFailClosed —— parser 侧 fail closed：畸形 / 未知协议 / 非法元数据 /
// 可见与锚点不一致 / 截断 blockquote 一律 error。
func TestReviewParserFailClosed(t *testing.T) {
	// 未知字段的 base64 载荷（DisallowUnknownFields 必须拒绝）。
	unknownFieldAnchor := reviewAnchorOpen +
		base64.RawURLEncoding.EncodeToString([]byte(`{"k":"s","zzz":"x"}`)) + reviewAnchorClose

	cases := []struct {
		name string
		body string
	}{
		{"未知协议版本", "<!-- eg:nr:2 " + base64.RawURLEncoding.EncodeToString([]byte(`{"k":"s"}`)) + " -->\n正文\n"},
		{"非法 base64 载荷", reviewAnchorOpen + "!!!非base64!!!" + reviewAnchorClose + "\n正文\n"},
		{"载荷含未知字段", unknownFieldAnchor + "\n正文\n"},
		{"kind 越界", craftAnchor(reviewAnchor{Kind: "z"}) + "\n正文\n"},
		{"可见正文无前置锚点", "### 标题\n\n正文\n"},
		{"source 锚点携 annotation", craftAnchor(reviewAnchor{Kind: anchorKindSource, SourceRef: "L1-L1", Annotation: "supplement"}) + "\n正文\n"},
		{"agent 锚点携 source_ref", craftAnchor(reviewAnchor{Kind: anchorKindAgent, Annotation: "supplement", SourceRef: "L1-L1"}) + "\n> **[Agent 补充]** x\n"},
		{"agent 可见标签与锚点不一致", craftAnchor(reviewAnchor{Kind: anchorKindAgent, Annotation: "supplement"}) + "\n> **[Agent 强调]** x\n"},
		{"截断 blockquote 续行", craftAnchor(reviewAnchor{Kind: anchorKindAgent, Annotation: "supplement"}) + "\n> **[Agent 补充]** 一\n二无引用前缀\n"},
		{"heading 与锚点不一致", craftAnchor(reviewAnchor{Kind: anchorKindSource, SourceRef: "L1-L1", Heading: "真标题"}) + "\n### 假标题\n\n正文\n"},
		{"omission 后有可见正文", validSourceUnit() + craftAnchor(reviewAnchor{Kind: anchorKindOmission, SourceRef: "L2-L2", Reason: "噪声"}) + "\n不该出现的可见正文\n"},
		{"agent 缺标记", craftAnchor(reviewAnchor{Kind: anchorKindAgent, Annotation: "supplement"}) + "\n没有 Agent 标记的正文\n"},

		// —— 必填 / 字段互斥（point 2）：required-field 与 field-exclusivity 必须守住 ——
		{"空正文零块", ""},
		{"仅空白正文", "   \n\n"},
		{"只有 omission 无 source", craftAnchor(reviewAnchor{Kind: anchorKindOmission, SourceRef: "L1-L1", Reason: "噪声"}) + "\n"},
		{"通篇无 source 块", craftAnchor(reviewAnchor{Kind: anchorKindAgent, Annotation: "supplement"}) + "\n> **[Agent 补充]** x\n"},
		{"source 缺 source_ref", craftAnchor(reviewAnchor{Kind: anchorKindSource}) + "\n正文\n"},
		// source_ref / reason 的「非空」按 TrimSpace 口径：parser 公共边界同样拒纯空白。
		{"source source_ref 纯空白", craftAnchor(reviewAnchor{Kind: anchorKindSource, SourceRef: "   "}) + "\n正文\n"},
		{"source 携 reason", craftAnchor(reviewAnchor{Kind: anchorKindSource, SourceRef: "L1-L1", Reason: "噪声"}) + "\n正文\n"},
		{"source 携 label", craftAnchor(reviewAnchor{Kind: anchorKindSource, SourceRef: "L1-L1", Label: "标签"}) + "\n正文\n"},
		{"agent 缺 annotation", craftAnchor(reviewAnchor{Kind: anchorKindAgent}) + "\n> **[Agent 补充]** x\n"},
		{"agent 携 reason", craftAnchor(reviewAnchor{Kind: anchorKindAgent, Annotation: "supplement", Reason: "噪声"}) + "\n> **[Agent 补充]** x\n"},
		{"agent 内置 key 携非空 label", craftAnchor(reviewAnchor{Kind: anchorKindAgent, Annotation: "guide", Label: "擅自改写"}) + "\n> **[Agent 导读]** x\n"},
		{"agent 非法扩展 key", craftRawAnchor(`{"k":"a","a":"Bad Key","l":"标签"}`) + "\n> **[Agent 标签]** x\n"},
		{"agent 扩展缺 label", craftAnchor(reviewAnchor{Kind: anchorKindAgent, Annotation: "case_x"}) + "\n> **[Agent case_x]** x\n"},
		{"agent 扩展空白 label", craftRawAnchor(`{"k":"a","a":"case_x","l":"   "}`) + "\n> **[Agent    ]** x\n"},
		{"omission 缺 source_ref", validSourceUnit() + craftAnchor(reviewAnchor{Kind: anchorKindOmission, Reason: "噪声"}) + "\n"},
		{"omission 缺 reason", validSourceUnit() + craftAnchor(reviewAnchor{Kind: anchorKindOmission, SourceRef: "L2-L2"}) + "\n"},
		{"omission source_ref 纯空白", validSourceUnit() + craftAnchor(reviewAnchor{Kind: anchorKindOmission, SourceRef: "   ", Reason: "噪声"}) + "\n"},
		{"omission reason 纯空白", validSourceUnit() + craftAnchor(reviewAnchor{Kind: anchorKindOmission, SourceRef: "L2-L2", Reason: "   "}) + "\n"},
		{"omission 携 heading", validSourceUnit() + craftAnchor(reviewAnchor{Kind: anchorKindOmission, SourceRef: "L2-L2", Reason: "噪声", Heading: "h"}) + "\n"},
		{"omission 携 annotation", validSourceUnit() + craftRawAnchor(`{"k":"o","s":"L2-L2","r":"噪声","a":"guide"}`) + "\n"},

		// —— omission 之后不得再出现 block（point 3）：否则 parse→render 会把块重排到 omission 之前 ——
		{"omission 后再现 source 块", validSourceUnit() +
			craftAnchor(reviewAnchor{Kind: anchorKindOmission, SourceRef: "L2-L2", Reason: "噪声"}) + "\n\n" +
			craftAnchor(reviewAnchor{Kind: anchorKindSource, SourceRef: "L3-L3"}) + "\n又一段来源\n"},
		{"omission 后再现 agent 块", validSourceUnit() +
			craftAnchor(reviewAnchor{Kind: anchorKindOmission, SourceRef: "L2-L2", Reason: "噪声"}) + "\n\n" +
			craftAnchor(reviewAnchor{Kind: anchorKindAgent, Annotation: "supplement"}) + "\n> **[Agent 补充]** x\n"},

		// —— 顶层多余数据 / 重复键（point 1）：dec.More() 检不出，必须 io.EOF + 显式去重 ——
		{"载荷尾随第二对象", craftRawAnchor(`{"k":"s","s":"L1-L1"}{"k":"a"}`) + "\n正文\n"},
		{"重复键 k", craftRawAnchor(`{"k":"s","k":"s","s":"L1-L1"}`) + "\n正文\n"},
		{"重复键 s", craftRawAnchor(`{"k":"s","s":"L1-L1","s":"L1-L1"}`) + "\n正文\n"},
		{"重复键 h", craftRawAnchor(`{"k":"s","s":"L1-L1","h":"标题","h":"标题"}`) + "\n### 标题\n\n正文\n"},
		{"重复键 a", craftRawAnchor(`{"k":"a","a":"guide","a":"guide"}`) + "\n> **[Agent 导读]** x\n"},
		{"重复键 l", craftRawAnchor(`{"k":"a","a":"case_x","l":"标签","l":"标签"}`) + "\n> **[Agent 标签]** x\n"},
		{"重复键 r", validSourceUnit() + craftRawAnchor(`{"k":"o","s":"L2-L2","r":"噪声","r":"噪声"}`) + "\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := ParseReviewNote([]byte(c.body)); err == nil {
				t.Fatalf("畸形输入必须 fail closed，实得无 error：\n%s", c.body)
			}
		})
	}
}

// TestReviewWriterRejectsIllegalOmissions —— writer 侧 fail closed：omission 必填 source_ref/reason，
// 且 note 必须有来源块，否则 error（不落盘一份语义残缺的整理正文）。
func TestReviewWriterRejectsIllegalOmissions(t *testing.T) {
	src := []ReviewBlock{rtSrc("", "L1-L1", "来源。")}
	cases := []struct {
		name string
		oms  []ReviewOmission
	}{
		{"omission 缺 source_ref", []ReviewOmission{{Reason: "噪声"}}},
		{"omission 缺 reason", []ReviewOmission{{SourceRef: "L2-L2"}}},
		{"omission 两者皆缺", []ReviewOmission{{}}},
		// source_ref / reason 的「非空」按 validator 的 TrimSpace 口径：纯空白同样视为缺失。
		{"omission source_ref 纯空白", []ReviewOmission{{SourceRef: "   ", Reason: "噪声"}}},
		{"omission reason 纯空白", []ReviewOmission{{SourceRef: "L2-L2", Reason: "   "}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := RenderReviewNote(src, c.oms); err == nil {
				t.Fatalf("非法 omission 必须被 writer 拒绝，实得无 error")
			}
		})
	}
}

// TestReviewAnchorsAreSingleLineComments —— 每个块恰一个版本化单行 HTML 注释锚点，
// 且锚点行不含裸露的 `-->`（base64url 字母表天然安全）。
func TestReviewAnchorsAreSingleLineComments(t *testing.T) {
	blocks := []ReviewBlock{
		rtSrc("第一节", "L1-L2", "正文一。"),
		rtAgent("", "distinction", "", "辨析。"),
	}
	rendered, err := RenderReviewNote(blocks, []ReviewOmission{{SourceRef: "L3-L3", Reason: "噪声"}})
	if err != nil {
		t.Fatalf("渲染失败：%v", err)
	}
	anchors := 0
	for _, line := range strings.Split(string(rendered), "\n") {
		if strings.HasPrefix(line, "<!-- "+reviewAnchorTag+" ") && strings.HasSuffix(line, " -->") {
			anchors++
			// 单行：锚点行内部不得再嵌 "-->"（除结尾外）。
			if strings.Count(line, "-->") != 1 {
				t.Fatalf("锚点行含多个注释结束符（非单行安全）：%q", line)
			}
		}
	}
	// 2 块 + 1 omission = 3 个锚点。
	if anchors != 3 {
		t.Fatalf("应有 3 个版本化锚点（2 块 + 1 omission），实得 %d：\n%s", anchors, rendered)
	}
}
