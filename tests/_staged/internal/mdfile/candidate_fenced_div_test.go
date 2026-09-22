package mdfile

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/yuin/goldmark"
)

func renderFencedCandidate(t testing.TB, draft CandidateDraft, width int) []byte {
	t.Helper()
	if err := validateCandidateDraft(draft); err != nil {
		t.Fatal(err)
	}
	colons := strings.Repeat(":", width)
	var out []byte
	out = append(out, encodeCandidateAnchor(candidateAnchorFromDraft(draft))...)
	out = append(out, '\n')
	out = append(out, colons...)
	out = append(out, " {#"...)
	out = append(out, draft.Key...)
	out = append(out, " .eg-candidate ."...)
	out = append(out, string(draft.Kind)...)
	out = append(out, '}', '\n')
	out = append(out, "### "...)
	out = append(out, draft.Title...)
	out = append(out, '\n', '\n')
	for _, section := range draft.Sections {
		out = append(out, "#### "...)
		out = append(out, section.Name...)
		out = append(out, '\n', '\n')
		out = append(out, section.Body...)
		out = append(out, '\n')
	}
	out = append(out, colons...)
	out = append(out, '\n')
	return out
}

func TestCandidateFencedDivEquivalentToH3Provider(t *testing.T) {
	draft := candidateDraft(
		"cand-l2-equivalent", CandidateKindOpinion, "跨小节候选")
	draft.Sections = append(draft.Sections,
		CandidateDraftSection{Name: "论据与推理", Body: []byte("证据 A。\n")},
		CandidateDraftSection{Name: "条件与反例", Body: []byte("边界 B。\n")},
		CandidateDraftSection{Name: "待验证", Body: []byte("问题 C。\n")})

	h3Body, err := RenderCandidateDraft(draft)
	if err != nil {
		t.Fatal(err)
	}
	h3, err := ParseCandidates(candidateNote(h3Body))
	if err != nil {
		t.Fatal(err)
	}
	l2Raw := candidateNote(renderFencedCandidate(t, draft, 4))
	l2, err := ParseCandidates(l2Raw)
	if err != nil {
		t.Fatalf("解析 L2 candidate：%v\n%s", err, l2Raw)
	}
	if len(h3) != 1 || len(l2) != 1 {
		t.Fatalf("candidate 数量不等：H3=%d L2=%d", len(h3), len(l2))
	}
	if l2[0].Syntax != CandidateSyntaxFencedDiv ||
		h3[0].Syntax != CandidateSyntaxH3 {
		t.Fatalf("Span provider 标记错误：H3=%q L2=%q",
			h3[0].Syntax, l2[0].Syntax)
	}
	if h3[0].Key != l2[0].Key || h3[0].Kind != l2[0].Kind ||
		h3[0].Title != l2[0].Title ||
		!reflect.DeepEqual(h3[0].Anchor, l2[0].Anchor) {
		t.Fatalf("H3/L2 candidate 元数据不等：\nH3=%+v\nL2=%+v", h3[0], l2[0])
	}
	if len(h3[0].Sections) != len(l2[0].Sections) {
		t.Fatalf("H3/L2 section 数量不等：%d != %d",
			len(h3[0].Sections), len(l2[0].Sections))
	}
	for i := range h3[0].Sections {
		if h3[0].Sections[i].Name != l2[0].Sections[i].Name ||
			!bytes.Equal(h3[0].Sections[i].Payload, l2[0].Sections[i].Payload) {
			t.Fatalf("H3/L2 section[%d] 不等：\nH3=%q\nL2=%q",
				i, h3[0].Sections[i].Payload, l2[0].Sections[i].Payload)
		}
	}
}

func TestCandidateFencedDivOwnsInnerHeadingsAndCodeFences(t *testing.T) {
	draft := candidateDraft(
		"cand-l2-structure", CandidateKindKnowledge, "跨标题知识")
	draft.Sections[0].Body = []byte(
		"第一段。\n\n## 内部 H2\n\n### 内部 H3\n\n##### 内部 H5\n\n" +
			"- 列表\n\n| A | B |\n| --- | --- |\n| 1 | 2 |\n\n" +
			"```markdown\n::: {#cand-code .eg-candidate .opinion}\n:::\n```\n")
	body := renderFencedCandidate(t, draft, 3)
	coverage, err := RenderCandidateCoverageMatrix([]CandidateCoverage{{
		Module: "m-l2", SourceRefs: []string{"L1-L4"}, Summary: "跨标题候选",
		Disposition: CandidateCoverageCandidate, Candidates: []string{draft.Key},
	}})
	if err != nil {
		t.Fatal(err)
	}
	raw := candidateNote(body, coverage)
	got, err := ParseCandidates(raw)
	if err != nil {
		t.Fatalf("L2 内部结构不应截断候选：%v\n%s", err, raw)
	}
	if len(got) != 1 || got[0].Key != draft.Key ||
		!bytes.Contains(got[0].Sections[0].Payload, []byte("## 内部 H2")) ||
		!bytes.Contains(got[0].Sections[0].Payload, []byte("::: {#cand-code")) {
		t.Fatalf("L2 payload 未完整保留内部结构：%+v", got)
	}
	state, err := ParseCandidateCoverageState(raw)
	if err != nil {
		t.Fatalf("内部 H2 不得截断提取结果分区：%v", err)
	}
	if state.Finalized || len(state.Draft) != 1 {
		t.Fatalf("L2 后候选覆盖未读回：%+v", state)
	}
}

func TestCandidateFencedDivEditAndOutputPreserveBoundary(t *testing.T) {
	draft := candidateDraft(
		"cand-l2-edit", CandidateKindKnowledge, "可编辑 L2")
	raw := candidateNote(renderFencedCandidate(t, draft, 5))
	before, err := ParseCandidates(raw)
	if err != nil {
		t.Fatal(err)
	}
	edited, err := ReplaceCandidateSection(
		raw, draft.Key, "知识内容", []byte("用户编辑后的正文。\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(edited, []byte("::::: {#cand-l2-edit .eg-candidate .knowledge}\n")) ||
		!bytes.Contains(edited, []byte("\n:::::\n")) {
		t.Fatal("candidate edit 改坏 L2 开闭围栏")
	}
	editedCandidates, err := ParseCandidates(edited)
	if err != nil || len(editedCandidates) != 1 {
		t.Fatalf("编辑后的 L2 candidate 不可解析：%v %+v", err, editedCandidates)
	}
	mapped, err := ReplaceCandidateOutputs(
		edited, map[string]string{draft.Key: "k-20260922-l2-edit"})
	if err != nil {
		t.Fatal(err)
	}
	after, err := ParseCandidates(mapped)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 || after[0].Anchor.Output != "k-20260922-l2-edit" ||
		!bytes.Equal(editedCandidates[0].Raw(edited), after[0].Raw(mapped)) {
		t.Fatalf("L2 output 映射失败：%+v", after)
	}
	if bytes.Equal(before[0].Raw(raw), after[0].Raw(mapped)) {
		t.Fatal("L2 candidate edit 没有改变目标 payload")
	}
	if !bytes.Contains(after[0].Raw(mapped), []byte("用户编辑后的正文。")) {
		t.Fatal("L2 candidate edit payload 未被 output 更新保留")
	}
}

func TestCandidateFencedDivPlainExportMatchesH3(t *testing.T) {
	draft := candidateDraft(
		"cand-l2-plain", CandidateKindKnowledge, "Plain Export")
	draft.Sections = append(draft.Sections,
		CandidateDraftSection{Name: "条件与边界", Body: []byte("边界正文。\n")})
	h3, err := RenderCandidateDraft(draft)
	if err != nil {
		t.Fatal(err)
	}
	h3Plain, err := PlainExport(candidateNote(h3))
	if err != nil {
		t.Fatal(err)
	}
	l2Plain, err := PlainExport(candidateNote(renderFencedCandidate(t, draft, 5)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(h3Plain, l2Plain) {
		t.Fatalf("H3/L2 plain export 不等价：\n--- H3 ---\n%s\n--- L2 ---\n%s",
			h3Plain, l2Plain)
	}
	for _, forbidden := range [][]byte{
		[]byte("eg:cd:"), []byte("#cand-l2-plain"), []byte("\n:::::"),
	} {
		if bytes.Contains(l2Plain, forbidden) {
			t.Fatalf("plain export 残留 L2 协议 %q：\n%s", forbidden, l2Plain)
		}
	}
}

func TestCandidateFencedDivCommonMarkFallbackKeepsVisibleContent(t *testing.T) {
	draft := candidateDraft(
		"cand-l2-commonmark", CandidateKindKnowledge, "跨小节降级")
	draft.Sections[0].Body = []byte(
		"第一段正文。\n\n- 列表正文\n\n| A | B |\n| --- | --- |\n| 表格正文 | 值 |\n")
	raw := renderFencedCandidate(t, draft, 4)
	var rendered bytes.Buffer
	if err := goldmark.Convert(raw, &rendered); err != nil {
		t.Fatal(err)
	}
	for _, visible := range []string{
		"跨小节降级", "知识内容", "第一段正文。", "列表正文", "表格正文",
	} {
		if !strings.Contains(rendered.String(), visible) {
			t.Fatalf("CommonMark 降级丢失可见正文 %q：\n%s", visible, rendered.String())
		}
	}
}

func TestCandidateFencedDivMixedOrderAndDuplicateKey(t *testing.T) {
	h3Draft := candidateDraft("cand-h3", CandidateKindKnowledge, "H3")
	h3, _ := RenderCandidateDraft(h3Draft)
	l2Draft := candidateDraft("cand-l2", CandidateKindOpinion, "L2")
	l2 := renderFencedCandidate(t, l2Draft, 3)
	got, err := ParseCandidates(candidateNote(h3, l2))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Key != "cand-h3" || got[1].Key != "cand-l2" {
		t.Fatalf("H3/L2 混排顺序错误：%+v", got)
	}

	l2Draft.Key = h3Draft.Key
	if _, err := ParseCandidates(
		candidateNote(h3, renderFencedCandidate(t, l2Draft, 3))); err == nil {
		t.Fatal("L1/L2 重复 candidate key 必须 fail closed")
	}
}

func TestCandidateFencedDivFailClosed(t *testing.T) {
	draft := candidateDraft(
		"cand-l2-bad", CandidateKindKnowledge, "L2 边界")
	valid := renderFencedCandidate(t, draft, 4)
	anchorEnd := bytes.IndexByte(valid, '\n') + 1
	openEnd := anchorEnd + bytes.IndexByte(valid[anchorEnd:], '\n') + 1
	closeStart := bytes.LastIndex(valid, []byte("::::\n"))

	cases := map[string][]byte{
		"missing anchor": valid[anchorEnd:],
		"anchor not adjacent": append(
			append([]byte(nil), valid[:anchorEnd]...),
			append([]byte("\n"), valid[anchorEnd:]...)...),
		"missing title": bytes.Replace(
			valid, []byte("### L2 边界\n"), []byte("正文无标题\n"), 1),
		"title not adjacent": append(
			append([]byte(nil), valid[:openEnd]...),
			append([]byte("\n"), valid[openEnd:]...)...),
		"title has attributes": bytes.Replace(
			valid, []byte("### L2 边界\n"),
			[]byte("### L2 边界 {#other}\n"), 1),
		"unclosed": valid[:closeStart],
		"short close": bytes.Replace(
			valid, []byte("\n::::\n"), []byte("\n:::\n"), 1),
		"nested": bytes.Replace(
			valid, []byte("#### 知识内容\n"),
			[]byte("::: {#cand-inner .eg-candidate .knowledge}\n"+
				"### Inner\n\n#### 知识内容\n\ninner\n:::\n\n#### 知识内容\n"), 1),
		"nested non-candidate div": bytes.Replace(
			valid, []byte("#### 知识内容\n"),
			[]byte("::: {.callout}\ninside\n:::\n\n#### 知识内容\n"), 1),
		"malformed attributes": bytes.Replace(
			valid, []byte("{#cand-l2-bad .eg-candidate .knowledge}"),
			[]byte("{#cand-l2-bad .eg-candidate .knowledge .extra}"), 1),
		"both kinds": bytes.Replace(
			valid, []byte(".knowledge}"), []byte(".knowledge .opinion}"), 1),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseCandidates(candidateNote(body)); err == nil {
				t.Fatalf("畸形 L2 candidate 必须 fail closed：\n%s", body)
			}
		})
	}
	if _, err := ParseCandidates(candidateNote([]byte(":::\n"))); err == nil {
		t.Fatal("孤立 L2 闭围栏必须 fail closed")
	}
}
