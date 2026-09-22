package mdfile

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func candidateDraft(key string, kind CandidateKind, title string) CandidateDraft {
	section := CandidateDraftSection{Name: "知识内容", Body: []byte("第一段。\n")}
	if kind == CandidateKindOpinion {
		section = CandidateDraftSection{Name: "观点", Body: []byte("这是主张。\n")}
	}
	return CandidateDraft{
		Key:        key,
		Kind:       kind,
		Title:      title,
		SourceRefs: []string{"L1-L4"},
		Rel:        "support",
		Reason:     "来源直接支持该结论",
		Tags:       []string{"agent", "runtime"},
		Sections:   []CandidateDraftSection{section},
	}
}

func candidateNote(parts ...[]byte) []byte {
	out := []byte("---\nid: n-20260922-candidate\nsource: s-20260922-source\n---\n\n" +
		"## 整理正文\n\n" +
		"### 来源里的普通 H3\n\n原文不动。\n\n" +
		"```markdown\n### 围栏里的伪候选 {#cand-fake .eg-candidate .knowledge}\n```\n\n" +
		"## 提取结果\n\n")
	for _, part := range parts {
		out = append(out, part...)
	}
	return append(out, []byte("## 存疑与待验证\n\n保留。\n\n## 用户补充\n\n用户文字。\n")...)
}

func TestCandidateRenderParseRoundTripAndScope(t *testing.T) {
	k := candidateDraft("cand-react-loop", CandidateKindKnowledge, "ReAct Loop 的执行流程")
	k.Sections = append(k.Sections,
		CandidateDraftSection{Name: "条件与边界", Body: []byte("失败时停止。\n")})
	o := candidateDraft("cand-runtime-claim", CandidateKindOpinion, "运行时隔离更可靠")
	o.Sections = append(o.Sections,
		CandidateDraftSection{Name: "论据与推理", Body: []byte("证据 A。\n")},
		CandidateDraftSection{Name: "待验证", Body: []byte("需要压力测试。\n")})

	kb, err := RenderCandidateDraft(k)
	if err != nil {
		t.Fatalf("渲染 Knowledge candidate：%v", err)
	}
	ob, err := RenderCandidateDraft(o)
	if err != nil {
		t.Fatalf("渲染 Opinion candidate：%v", err)
	}
	raw := candidateNote(kb, ob)

	got, err := ParseCandidates(raw)
	if err != nil {
		t.Fatalf("解析 candidate：%v\n%s", err, raw)
	}
	if len(got) != 2 {
		t.Fatalf("只应识别提取结果内两个 candidate，实得 %d：%+v", len(got), got)
	}
	if got[0].Key != k.Key || got[0].Kind != k.Kind || got[0].Title != k.Title {
		t.Fatalf("Knowledge candidate 元数据不一致：%+v", got[0])
	}
	if got[1].Key != o.Key || got[1].Kind != o.Kind || got[1].Title != o.Title {
		t.Fatalf("Opinion candidate 元数据不一致：%+v", got[1])
	}
	if !bytes.Equal(got[0].Sections[0].Payload, []byte("\n第一段。\n\n")) {
		t.Fatalf("H4 payload 必须包含标题后的原始字节区间：%q", got[0].Sections[0].Payload)
	}
	if !bytes.Equal(got[0].Sections[1].Payload, []byte("\n失败时停止。\n\n")) {
		t.Fatalf("末个 H4 payload 不得吞入下一 candidate 锚点：%q", got[0].Sections[1].Payload)
	}
	if !bytes.Equal(got[1].Sections[2].Payload, []byte("\n需要压力测试。\n\n")) {
		t.Fatalf("末个 candidate 的 H4 payload 边界错误：%q", got[1].Sections[2].Payload)
	}
	for i, c := range got {
		if !bytes.Equal(raw[c.HeadingStart:c.HeadingEnd],
			[]byte("### "+c.Title+" {#"+c.Key+" .eg-candidate ."+string(c.Kind)+"}\n")) {
			t.Fatalf("candidate[%d] 标题 span 不精确：%q", i, raw[c.HeadingStart:c.HeadingEnd])
		}
		if c.AnchorEnd != c.HeadingStart {
			t.Fatalf("candidate[%d] 锚点必须与 H3 逐行相邻：anchor_end=%d heading_start=%d",
				i, c.AnchorEnd, c.HeadingStart)
		}
	}
}

func TestCandidateAnchorRenderParseStable(t *testing.T) {
	in := candidateDraft("cand-stable", CandidateKindKnowledge, "稳定候选")
	body1, err := RenderCandidateDraft(in)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseCandidates(candidateNote(body1))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("candidate 数量=%d", len(got))
	}
	out := CandidateDraft{
		Key: got[0].Key, Kind: got[0].Kind, Title: got[0].Title,
		SourceRefs: got[0].Anchor.SourceRefs, Rel: got[0].Anchor.Rel,
		Reason: got[0].Anchor.Reason, Tags: got[0].Anchor.Tags, Output: got[0].Anchor.Output,
		Sections: []CandidateDraftSection{{Name: got[0].Sections[0].Name, Body: []byte("第一段。\n")}},
	}
	body2, err := RenderCandidateDraft(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body1, body2) {
		t.Fatalf("render→parse→render 不稳定：\n--- first ---\n%s--- second ---\n%s", body1, body2)
	}
}

func TestCandidateBoundaryStopsAtNextH3AndEOF(t *testing.T) {
	body, err := RenderCandidateDraft(candidateDraft(
		"cand-boundary", CandidateKindKnowledge, "边界"))
	if err != nil {
		t.Fatal(err)
	}
	raw := append([]byte("## 提取结果\n\n"), body...)
	raw = append(raw, []byte("### 普通后续标题\n\n正文。\n")...)
	got, err := ParseCandidates(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].End != bytes.Index(raw, []byte("### 普通后续标题")) {
		t.Fatalf("next H3 边界错误：%+v", got)
	}

	raw = append([]byte("## 提取结果\n\n"), body...)
	raw = append(raw, []byte("###\n\n空标题后的正文。\n")...)
	got, err = ParseCandidates(raw)
	if err != nil {
		t.Fatalf("空 H3 仍是合法边界，不应破坏定位：%v", err)
	}
	if len(got) != 1 || got[0].End != bytes.Index(raw, []byte("###\n")) {
		t.Fatalf("空 H3 边界错误：%+v", got)
	}

	raw = append([]byte("## 提取结果\n\n"), body...)
	got, err = ParseCandidates(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].End != len(raw) {
		t.Fatalf("EOF 边界错误：%+v len=%d", got, len(raw))
	}
}

func TestCandidateParserIgnoresProtocolExamplesInFence(t *testing.T) {
	fake := validCandidateBody(t)
	real, err := RenderCandidateDraft(candidateDraft(
		"cand-real", CandidateKindKnowledge, "真实候选"))
	if err != nil {
		t.Fatal(err)
	}
	code := append([]byte("```markdown\n"), fake...)
	code = append(code, []byte("```\n\n")...)
	got, err := ParseCandidates(candidateNote(code, real))
	if err != nil {
		t.Fatalf("代码围栏里的协议示例不得触发 candidate 校验：%v", err)
	}
	if len(got) != 1 || got[0].Key != "cand-real" {
		t.Fatalf("代码围栏里的伪 candidate 被识别：%+v", got)
	}
}

func TestCandidateEditPreservesAllOtherBytes(t *testing.T) {
	first, _ := RenderCandidateDraft(candidateDraft(
		"cand-first", CandidateKindKnowledge, "第一个候选"))
	secondDraft := candidateDraft("cand-second", CandidateKindOpinion, "第二个候选")
	second, _ := RenderCandidateDraft(secondDraft)
	raw := candidateNote(first, second)

	before, err := ParseCandidates(raw)
	if err != nil {
		t.Fatal(err)
	}
	target := before[0].Sections[0]
	content := []byte("替换后的第一行。\n\n第二段保留原始换行。\n")
	out, err := ReplaceCandidateSection(raw, "cand-first", "知识内容", content)
	if err != nil {
		t.Fatalf("替换 candidate H4：%v", err)
	}
	wantPayload := append([]byte("\n"), content...)
	wantPayload = append(wantPayload, '\n')
	if !bytes.Equal(out[target.BodyStart:target.BodyStart+len(wantPayload)], wantPayload) {
		t.Fatalf("替换后的 payload 不一致：%q", out[target.BodyStart:target.BodyStart+len(wantPayload)])
	}
	if !bytes.Equal(out[:target.BodyStart], raw[:target.BodyStart]) {
		t.Fatal("目标 payload 之前的字节发生变化")
	}
	if !bytes.Equal(out[target.BodyStart+len(wantPayload):], raw[target.End:]) {
		t.Fatal("目标 payload 之后的字节发生变化")
	}

	after, err := ParseCandidates(out)
	if err != nil {
		t.Fatalf("替换结果不可解析：%v", err)
	}
	if len(after) != 2 || !bytes.Equal(after[1].Raw(out), before[1].Raw(raw)) {
		t.Fatal("编辑第一个 candidate 改动了第二个 candidate")
	}
}

func TestCandidateEditRejectsMaterializedOrUnknownSection(t *testing.T) {
	d := candidateDraft("cand-done", CandidateKindKnowledge, "已物化")
	d.Output = "k-20260922-done"
	body, err := RenderCandidateDraft(d)
	if err != nil {
		t.Fatal(err)
	}
	raw := candidateNote(body)
	if _, err := ReplaceCandidateSection(raw, d.Key, "知识内容", []byte("新正文。\n")); err == nil {
		t.Fatal("已物化 candidate 不得通过命令式路径编辑")
	}

	d.Output = ""
	body, _ = RenderCandidateDraft(d)
	raw = candidateNote(body)
	if _, err := ReplaceCandidateSection(raw, d.Key, "不存在", []byte("新正文。\n")); err == nil {
		t.Fatal("不存在的 H4 分区应拒绝")
	}
}

func candidateAnchorLineJSON(payload string) string {
	return "<!-- eg:cd:1 " + base64.RawURLEncoding.EncodeToString([]byte(payload)) + " -->\n"
}

func validCandidateBody(t *testing.T) []byte {
	t.Helper()
	body, err := RenderCandidateDraft(candidateDraft(
		"cand-valid", CandidateKindKnowledge, "合法候选"))
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestCandidateParserFailClosed(t *testing.T) {
	valid := validCandidateBody(t)
	validAnchorEnd := bytes.IndexByte(valid, '\n') + 1
	validHeadingEnd := validAnchorEnd + bytes.IndexByte(valid[validAnchorEnd:], '\n') + 1
	validAnchor := string(valid[:validAnchorEnd])
	validHeading := string(valid[validAnchorEnd:validHeadingEnd])
	validPayload := string(valid[validHeadingEnd:])

	cases := map[string][]byte{
		"unknown anchor version": []byte(strings.Replace(validAnchor+validHeading+validPayload,
			"eg:cd:1", "eg:cd:2", 1)),
		"invalid base64": []byte("<!-- eg:cd:1 !!! -->\n" + validHeading + validPayload),
		"unknown JSON field": []byte(candidateAnchorLineJSON(
			`{"source_refs":["L1-L4"],"rel":"support","reason":"r","tags":[],"output":"","extra":1}`) +
			validHeading + validPayload),
		"duplicate JSON key": []byte(candidateAnchorLineJSON(
			`{"source_refs":["L1-L4"],"rel":"support","reason":"r","tags":[],"output":"","output":""}`) +
			validHeading + validPayload),
		"missing exact key": []byte(candidateAnchorLineJSON(
			`{"source_refs":["L1-L4"],"rel":"support","reason":"r","tags":[]}`) +
			validHeading + validPayload),
		"anchor not adjacent": []byte(validAnchor + "\n" + validHeading + validPayload),
		"missing anchor":      []byte(validHeading + validPayload),
		"bad key": []byte(validAnchor + strings.Replace(validHeading,
			"#cand-valid", "#Cand_INVALID", 1) + validPayload),
		"missing kind": []byte(validAnchor + strings.Replace(validHeading,
			" .knowledge", "", 1) + validPayload),
		"both kinds": []byte(validAnchor + strings.Replace(validHeading,
			" .knowledge}", " .knowledge .opinion}", 1) + validPayload),
		"extra attribute": []byte(validAnchor + strings.Replace(validHeading,
			"}", " data-x=value}", 1) + validPayload),
		"extra class": []byte(validAnchor + strings.Replace(validHeading,
			"}", " .extra}", 1) + validPayload),
		"duplicate id": []byte(validAnchor + strings.Replace(validHeading,
			"{#cand-valid", "{#cand-valid #cand-other", 1) + validPayload),
		"invalid relation": []byte(candidateAnchorLineJSON(
			`{"source_refs":["L1-L4"],"rel":"supports","reason":"r","tags":[],"output":""}`) +
			validHeading + validPayload),
		"wrong output kind": []byte(candidateAnchorLineJSON(
			`{"source_refs":["L1-L4"],"rel":"support","reason":"r","tags":[],"output":"o-20260922-wrong"}`) +
			validHeading + validPayload),
		"unknown H4": []byte(strings.Replace(validAnchor+validHeading+validPayload,
			"#### 知识内容", "#### 未知分区", 1)),
		"missing required H4": []byte(validAnchor + validHeading +
			"\n#### 条件与边界\n\n边界。\n\n"),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseCandidates(candidateNote(body)); err == nil {
				t.Fatalf("畸形 candidate 应 fail closed：\n%s", body)
			}
		})
	}
}

func TestCandidateParserRejectsDuplicateKeyAndIgnoresOtherSections(t *testing.T) {
	body := validCandidateBody(t)
	if _, err := ParseCandidates(candidateNote(body, body)); err == nil {
		t.Fatal("同一 Note 内重复 candidate key 应拒绝")
	}

	raw := append([]byte("## 整理正文\n\n"), body...)
	raw = append(raw, []byte("\n## 提取结果\n\n")...)
	got, err := ParseCandidates(raw)
	if err != nil {
		t.Fatalf("其它分区里的 H3 永远不是 candidate，不应报错：%v", err)
	}
	if len(got) != 0 {
		t.Fatalf("其它分区里的候选形态不应被识别：%+v", got)
	}
}

func TestCandidateRendererRejectsInvalidDrafts(t *testing.T) {
	cases := map[string]CandidateDraft{}
	d := candidateDraft("bad", CandidateKindKnowledge, "标题")
	cases["bad key"] = d
	d = candidateDraft("cand-x", CandidateKind("fact"), "标题")
	cases["bad kind"] = d
	d = candidateDraft("cand-x", CandidateKindKnowledge, " \n")
	cases["bad title"] = d
	d = candidateDraft("cand-x", CandidateKindKnowledge, "标题")
	d.SourceRefs = nil
	cases["missing source refs"] = d
	d = candidateDraft("cand-x", CandidateKindKnowledge, "标题")
	d.Rel = "supports"
	cases["bad rel"] = d
	d = candidateDraft("cand-x", CandidateKindKnowledge, "标题")
	d.Reason = " "
	cases["empty reason"] = d
	d = candidateDraft("cand-x", CandidateKindKnowledge, "标题")
	d.Sections[0].Body = []byte("无行结束")
	cases["unterminated body"] = d
	d = candidateDraft("cand-x", CandidateKindOpinion, "标题")
	d.Sections = []CandidateDraftSection{{Name: "论据与推理", Body: []byte("证据。\n")}}
	cases["missing required section"] = d

	for name, draft := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := RenderCandidateDraft(draft); err == nil {
				t.Fatalf("非法草稿应拒绝：%+v", draft)
			}
		})
	}
}

func TestCandidateCoverageRoundTrip(t *testing.T) {
	in := []CandidateCoverage{
		{Module: "m-002", SourceRefs: []string{"L3-L4", "L1-L2"}, Summary: "候选模块",
			Disposition: CandidateCoverageCandidate, Candidates: []string{"cand-b", "cand-a"}},
		{Module: "m-003", SourceRefs: []string{"L5-L6"}, Summary: "留在 Note",
			Disposition: CandidateCoverageNoteOnly, Reason: "没有独立复用价值"},
		{Module: "m-004", SourceRefs: []string{"L7-L8"}, Summary: "尚待处理",
			Disposition: CandidateCoverageUnresolved, Reason: "缺少反例分析"},
	}
	body, err := RenderCandidateCoverageMatrix(in)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseCandidateCoverageMatrix(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(in) {
		t.Fatalf("候选覆盖条目数 %d != %d", len(got), len(in))
	}
	for i := range in {
		if got[i].Module != in[i].Module || got[i].Summary != in[i].Summary ||
			got[i].Disposition != in[i].Disposition || got[i].Reason != in[i].Reason ||
			!eqStrs(got[i].SourceRefs, in[i].SourceRefs) ||
			!eqStrs(got[i].Candidates, in[i].Candidates) {
			t.Fatalf("candidate coverage[%d] 未逐字段保序读回：got=%+v want=%+v", i, got[i], in[i])
		}
	}
	body2, err := RenderCandidateCoverageMatrix(got)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, body2) {
		t.Fatalf("candidate coverage render→parse→render 不稳定：\n%s\n---\n%s", body, body2)
	}
}

func TestCandidateCoverageFailClosed(t *testing.T) {
	in := []CandidateCoverage{{
		Module: "m-1", SourceRefs: []string{"L1-L2"}, Summary: "候选",
		Disposition: CandidateCoverageCandidate, Candidates: []string{"cand-a"},
	}}
	body, err := RenderCandidateCoverageMatrix(in)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{
		"visible tamper":  bytes.Replace(body, []byte("候选"), []byte("篡改"), 1),
		"unknown version": bytes.Replace(body, []byte("eg:cc:1"), []byte("eg:cc:2"), 1),
		"missing anchor":  body[:bytes.Index(body, []byte("<!-- eg:cc:1"))],
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseCandidateCoverageMatrix(raw); err == nil {
				t.Fatalf("畸形 candidate coverage 应拒绝：\n%s", raw)
			}
		})
	}

	bad := in
	bad[0].Disposition = CandidateCoverageNoteOnly
	if _, err := RenderCandidateCoverageMatrix(bad); err == nil {
		t.Fatal("note_only 带 candidates 应拒绝")
	}
}

func TestReplaceCandidateOutputsPreservesCandidateAndNoteBytes(t *testing.T) {
	k := candidateDraft("cand-k", CandidateKindKnowledge, "知识候选")
	o := candidateDraft("cand-o", CandidateKindOpinion, "观点候选")
	kb, _ := RenderCandidateDraft(k)
	ob, _ := RenderCandidateDraft(o)
	coverage, err := RenderCandidateCoverageMatrix([]CandidateCoverage{{
		Module: "m-1", SourceRefs: []string{"L1-L4"}, Summary: "候选模块",
		Disposition: CandidateCoverageCandidate, Candidates: []string{"cand-k", "cand-o"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	raw := candidateNote(kb, ob, coverage)
	before, err := ParseCandidates(raw)
	if err != nil {
		t.Fatal(err)
	}

	out, err := ReplaceCandidateOutputs(raw, map[string]string{
		"cand-k": "k-20260922-knowledge",
		"cand-o": "o-20260922-opinion",
	})
	if err != nil {
		t.Fatal(err)
	}
	after, err := ParseCandidates(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 2 || after[0].Anchor.Output != "k-20260922-knowledge" ||
		after[1].Anchor.Output != "o-20260922-opinion" {
		t.Fatalf("output 映射未写入：%+v", after)
	}
	for i := range before {
		if !bytes.Equal(before[i].Raw(raw), after[i].Raw(out)) {
			t.Fatalf("candidate[%d] H3/payload 被改写", i)
		}
	}
	for _, marker := range []string{"原文不动。", "保留。", "用户文字。"} {
		if bytes.Count(out, []byte(marker)) != 1 {
			t.Fatalf("Note 非机器管理正文未逐字保留：%s", marker)
		}
	}
	if _, err := ReplaceCandidateOutputs(out,
		map[string]string{"cand-k": "k-20260923-other"}); err == nil {
		t.Fatal("已有 output 不得改写成另一 ID")
	}
}

func TestCandidateCoverageStateAndFinalization(t *testing.T) {
	d := candidateDraft("cand-final", CandidateKindKnowledge, "最终候选")
	d.Output = "k-20260922-final"
	body, err := RenderCandidateDraft(d)
	if err != nil {
		t.Fatal(err)
	}
	draftCoverage := []CandidateCoverage{{
		Module: "m-1", SourceRefs: []string{"L1-L4"}, Summary: "候选模块",
		Disposition: CandidateCoverageCandidate, Candidates: []string{"cand-final"},
	}}
	matrix, err := RenderCandidateCoverageMatrix(draftCoverage)
	if err != nil {
		t.Fatal(err)
	}
	raw := candidateNote(body, matrix)
	state, err := ParseCandidateCoverageState(raw)
	if err != nil {
		t.Fatal(err)
	}
	if state.Finalized || len(state.Draft) != 1 {
		t.Fatalf("应识别为草稿覆盖：%+v", state)
	}

	finalCoverage := []ReviewCoverage{{
		Module: "m-1", SourceRefs: []string{"L1-L4"}, Summary: "候选模块",
		Disposition: CoverageDispOutputs, Outputs: []string{"k-20260922-final"},
	}}
	finalMatrix, err := RenderCoverageMatrix(finalCoverage)
	if err != nil {
		t.Fatal(err)
	}
	final := append([]byte("### Knowledge\n\n- k-20260922-final\n\n"), finalMatrix...)
	out, err := FinalizeCandidateExtraction(raw, final)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseCandidateCoverageState(out)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Finalized || len(got.Final) != 1 ||
		got.Final[0].Outputs[0] != "k-20260922-final" {
		t.Fatalf("应识别为最终覆盖：%+v", got)
	}
	again, err := FinalizeCandidateExtraction(out, final)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again, out) {
		t.Fatal("相同最终提取结果重跑必须字节级 no-op")
	}
	drifted := bytes.Replace(out, []byte("- k-20260922-final"),
		[]byte("- k-20260922-other"), 1)
	if _, err := FinalizeCandidateExtraction(drifted, final); err == nil {
		t.Fatal("最终 output list 漂移必须 fail closed")
	}
}

func FuzzCandidateParser(f *testing.F) {
	body, err := RenderCandidateDraft(candidateDraft(
		"cand-fuzz", CandidateKindKnowledge, "Fuzz 候选"))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(candidateNote(body))
	f.Add([]byte("## 提取结果\n\n<!-- eg:cd:999 x -->\n### x\n"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, _ = ParseCandidates(raw)
	})
}
