package mdfile

import (
	"bytes"
	"testing"
)

func TestPlainExportPreservesVisibleCandidateBytes(t *testing.T) {
	draft := candidateDraft("cand-plain", CandidateKindKnowledge, "Plain candidate")
	draft.Sections = []CandidateDraftSection{
		{Name: SecKnowledge, Body: []byte("第一段。\n\n- 列表\n")},
		{Name: SecBoundary, Body: []byte("边界正文。\n")},
	}
	rendered, err := RenderCandidateDraft(draft)
	if err != nil {
		t.Fatal(err)
	}
	coverage, err := RenderCandidateCoverageMatrix([]CandidateCoverage{{
		Module: "m-plain", SourceRefs: []string{"L1-L1"}, Summary: "摘要",
		Disposition: CandidateCoverageCandidate, Candidates: []string{"cand-plain"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	raw := candidateNote(
		[]byte("<!-- eg:nr:1 machine -->\n> [Agent 补充] 可见批注。\n\n"),
		rendered,
		coverage,
	)
	plain, err := PlainExport(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range [][]byte{
		[]byte("<!-- eg:nr:"), []byte("<!-- eg:cd:"),
		[]byte("<!-- eg:cc:"), []byte("#cand-plain .eg-candidate"),
	} {
		if bytes.Contains(plain, forbidden) {
			t.Fatalf("plain export 残留专有协议 %q：\n%s", forbidden, plain)
		}
	}
	for _, visible := range [][]byte{
		[]byte("> [Agent 补充] 可见批注。"),
		[]byte("### Plain candidate\n"),
		[]byte("#### 知识内容\n\n第一段。\n\n- 列表\n"),
		[]byte("#### 条件与边界\n\n边界正文。\n"),
		[]byte("### 候选覆盖\n"),
	} {
		if !bytes.Contains(plain, visible) {
			t.Fatalf("plain export 丢失或改写可见字节 %q：\n%s", visible, plain)
		}
	}
}

func TestPlainExportStripsL2FencesButKeepsPayload(t *testing.T) {
	raw := []byte("before\n\n<!-- eg:cd:1 opaque -->\n" +
		":::: {#cand-l2 .eg-candidate .opinion}\n" +
		"### 可见标题\n\n正文逐字保留。\n" +
		":::::\n\nafter\n")
	plain, err := PlainExport(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte("before\n\n### 可见标题\n\n正文逐字保留。\n\nafter\n")
	if !bytes.Equal(plain, want) {
		t.Fatalf("L2 plain export 字节不一致：\nwant=%q\n got=%q", want, plain)
	}
}

func TestPlainExportDoesNotStripProtocolExamplesInCode(t *testing.T) {
	raw := []byte("```markdown\n<!-- eg:cd:9 example -->\n" +
		"::: {#cand-example .eg-candidate .knowledge}\n:::\n```\n")
	plain, err := PlainExport(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plain, raw) {
		t.Fatalf("代码围栏里的协议示例不是机器协议，不得改写：\n%s", plain)
	}
}

func TestPlainExportRejectsMalformedReservedL2(t *testing.T) {
	raw := []byte("::: {#cand-bad .eg-candidate}\npayload\n:::\n")
	if _, err := PlainExport(raw); err == nil {
		t.Fatal("保留字命中的畸形 L2 candidate 必须 fail closed")
	}
}
