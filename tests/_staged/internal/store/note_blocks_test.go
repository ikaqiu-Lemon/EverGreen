package store

// note_blocks_test.go —— Note「整理正文」有序块落盘形态的验收判据
// （T-evergreen.knowledge_opinion_split-158614-004 · T-004-A；契约 §4.2 第 3 条 / §5.1）。
//
// 本文件只回答一个问题：`write_note` 的有序块落到盘上之后，**读路径还能不能把它们切回块**。
// 这条判据不能用「渲染函数返回的字符串里有那个标记」来证明——那只证明写侧拼对了字面量。
// Agent 补充块之所以选 blockquote + 粗体标记（契约 §5.1 理由 c），要的是「它在读路径上
// 是一个**独立的块**」：日后 `replace_block` 要能只替换其中一段而不碰来源正文，
// 前提就是切块结果里 Agent 补充与来源正文分属两个块。因此判据必须走完
// 渲染 → 落盘 → mdfile.Parse → Blocks 这条整链，并落在**块边界的字节**上。
//
// 分区名与块型都用常量引用（SecNoteBody / mdfile.Block*），不写死中文字面量：
// 分区一旦改名，用例应当随之解析失败，而不是继续绿着找不到分区。

import (
	"bytes"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
)

// blockFixture 是本文件共用的三块夹具：带 heading 的来源块、无 heading 的 Agent 补充块
// （正文含一个空行，用来验证 blockquote 不被空行截断）、以及一个两项的来源列表块。
//
// 刻意让 Agent 块**不带** heading：契约 §4.2 第 3 条只说「heading 非空时渲染 H3」，
// 空 heading 必须一个字节都不写——否则会落出一个 `### ` 空标题，把读路径切出一个空块。
func blockFixture() []NoteBlock {
	return []NoteBlock{
		{Role: NoteBlockSource, Heading: "第一节 并行注意力",
			Body: []byte("原文主张并行注意力替代递归。\n第二行仍属同一段。")},
		{Role: NoteBlockAgent,
			Body: []byte("该主张限定在自注意力可并行的结构。\n\n第二段补充：递归结构不适用。")},
		{Role: NoteBlockSource,
			Body: []byte("- 原文要点一\n- 原文要点二")},
	}
}

// wantAgentQuote 是 Agent 补充块的**逐字**落盘形态：首行带标记，空行渲染成 `>`，
// 续行带 `> `。空行若原样落成真空行，blockquote 会被切成两个块 —— 那正是本用例要拦的回归。
func wantAgentQuote() string {
	return AgentBlockMarker + "该主张限定在自注意力可并行的结构。\n" +
		">\n" +
		"> 第二段补充：递归结构不适用。\n"
}

// noteWithBlocks 把有序块渲染后真实落盘成一篇笔记，返回文件字节。
func noteWithBlocks(t *testing.T, blocks []NoteBlock) []byte {
	t.Helper()
	body, err := NoteBlockBytes(blocks)
	if err != nil {
		t.Fatalf("NoteBlockBytes 不应失败：%v", err)
	}
	s, _ := newVault(t)
	spec := NoteSpec{
		Rel:      NoteRel("ai-infra", "n-20261017-blocks"),
		ID:       "n-20261017-blocks",
		SourceID: "s-20260901-attention",
		Title:    "并行注意力（整理版）",
		Date:     day(t, "2026-10-17"),
		Stamp:    stamp(t, "2026-10-17T10:00:00+08:00"),
		Sections: []SectionAppend{{Section: SecNoteBody, Payload: body}},
		// 收件区不在本判据范围内：Detached 让本次完全不动 unprocessed.md，
		// 免得「收件区没有该条目」这类无关失败掩盖切块判定。
		Inbox: InboxSpec{Detached: true},
	}
	if _, err := s.ApplyNote(spec); err != nil {
		t.Fatalf("ApplyNote 失败：%v", err)
	}
	f, err := s.Read(spec.Rel)
	if err != nil {
		t.Fatalf("读回笔记失败：%v", err)
	}
	return f.Bytes
}

// TestNoteAgentBlockIsIndependentlySplittable —— Agent 补充块在读路径上是**独立一块**。
//
// 判据分三层，缺一层都留有后门：
//   - 块型与块数：`整理正文` 切出 heading3 / paragraph / paragraph / list_item / list_item 五块
//     （两个列表项本就是两块，见 mdfile 的块定义），顺序即数组顺序；
//   - 边界字节：Agent 块的字节**恰好等于** wantAgentQuote()，既不多吞前一块的正文，
//     也不被内部空行截断；
//   - 隔离性：来源块的字节里不含 Agent 标记，且把 Agent 块整段切掉之后，
//     两个来源块的正文仍逐字完好 —— 这正是 `replace_block` 日后能只替换一段的前提。
func TestNoteAgentBlockIsIndependentlySplittable(t *testing.T) {
	raw := noteWithBlocks(t, blockFixture())
	doc, err := mdfile.Parse(raw)
	if err != nil {
		t.Fatalf("落盘笔记必须可解析：%v\n%s", err, raw)
	}
	blocks, err := doc.Blocks(SecNoteBody)
	if err != nil {
		t.Fatalf("「%s」必须存在且可切块：%v\n%s", SecNoteBody, err, raw)
	}

	wantKinds := []mdfile.BlockKind{mdfile.BlockHeading3, mdfile.BlockParagraph,
		mdfile.BlockParagraph, mdfile.BlockListItem, mdfile.BlockListItem}
	if len(blocks) != len(wantKinds) {
		var got []string
		for _, b := range blocks {
			got = append(got, string(b.Kind)+"="+string(b.Bytes(raw)))
		}
		t.Fatalf("「%s」应切出 %d 块，实得 %d：%q\n%s",
			SecNoteBody, len(wantKinds), len(blocks), got, raw)
	}
	for i, want := range wantKinds {
		if blocks[i].Kind != want {
			t.Fatalf("第 %d 块块型应为 %s，实得 %s（%q）",
				i, want, blocks[i].Kind, blocks[i].Bytes(raw))
		}
	}

	if got, want := string(blocks[0].Bytes(raw)), "### 第一节 并行注意力\n"; got != want {
		t.Fatalf("非空 heading 应渲染为 H3 且自成一块：want %q，got %q", want, got)
	}
	source1 := string(blocks[1].Bytes(raw))
	if want := "原文主张并行注意力替代递归。\n第二行仍属同一段。\n"; source1 != want {
		t.Fatalf("来源块应逐字落盘且自成一块：want %q，got %q", want, source1)
	}
	if bytes.Contains([]byte(source1), []byte(AgentBlockMarker)) {
		t.Fatalf("来源块竟与 Agent 补充块粘成一块：%q", source1)
	}
	agent := string(blocks[2].Bytes(raw))
	if agent != wantAgentQuote() {
		t.Fatalf("Agent 补充块的块边界字节不符：\nwant %q\ngot  %q", wantAgentQuote(), agent)
	}

	// 空 heading 一个字节都不写：整篇笔记里不得出现空的 H3。
	if bytes.Contains(raw, []byte("### \n")) || bytes.Contains(raw, []byte("###\n")) {
		t.Fatalf("空 heading 不得渲染出空 H3：\n%s", raw)
	}

	// 把 Agent 块整段切掉：两个来源块必须逐字完好（块边界没有互相吞字节）。
	cut := append(append([]byte(nil), raw[:blocks[2].Start]...), raw[blocks[2].End:]...)
	if bytes.Contains(cut, []byte(AgentBlockMarker)) {
		t.Fatalf("切掉 Agent 块后仍残留标记，说明标记跨越了块边界：\n%s", cut)
	}
	for _, keep := range []string{"原文主张并行注意力替代递归。", "第二行仍属同一段。",
		"- 原文要点一", "- 原文要点二"} {
		if !bytes.Contains(cut, []byte(keep)) {
			t.Fatalf("切掉 Agent 块后来源正文 %q 丢失：\n%s", keep, cut)
		}
	}
}

// TestNoteBlockOrderIsRenderedVerbatim —— 数组序即落盘序，块之间恰一个空行。
//
// 与 plan 侧「落盘顺序 == 数组顺序」的验收不重复：那支用例证的是 plan 不重排数组，
// 本支证的是渲染层的**分隔符**——块之间恰一个空行（mdfile 的块分隔符）。
// 少一个空行会把两块粘成一块，多一个空行会在读路径上多切出空块，两种都要拦。
func TestNoteBlockOrderIsRenderedVerbatim(t *testing.T) {
	body, err := NoteBlockBytes([]NoteBlock{
		{Role: NoteBlockSource, Body: []byte("甲段。")},
		{Role: NoteBlockAgent, Body: []byte("乙段（我补的）。")},
		{Role: NoteBlockSource, Heading: "丙节", Body: []byte("丙段。")},
	})
	if err != nil {
		t.Fatalf("NoteBlockBytes 不应失败：%v", err)
	}
	want := "甲段。\n" +
		"\n" +
		AgentBlockMarker + "乙段（我补的）。\n" +
		"\n" +
		"### 丙节\n" +
		"\n" +
		"丙段。\n"
	if got := string(body); got != want {
		t.Fatalf("渲染字节不符：\nwant %q\ngot  %q", want, got)
	}
	if bytes.Contains(body, []byte("\n\n\n")) {
		t.Fatalf("块之间不得出现连续空行（会切出空块）：%q", body)
	}
}
