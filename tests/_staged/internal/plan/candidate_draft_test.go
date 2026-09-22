package plan

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

func draftSection(name, body string) string {
	return fmt.Sprintf(`{"name":%q,"body":%q}`, name, body)
}

func draftCandidate(key, kind, title string, refs, sections []string) string {
	return fmt.Sprintf(
		`{"key":%q,"kind":%q,"title":%q,"source_refs":%s,"rel":"support",`+
			`"reason":"原文直接支持","tags":["draft"],"sections":[%s]}`,
		key, kind, title, ncJSONArr(refs), strings.Join(sections, ","))
}

func draftCoverage(module string, refs []string, disposition string,
	candidates []string, reason string) string {
	return fmt.Sprintf(
		`{"module":%q,"source_refs":%s,"summary":"模块摘要","disposition":%q,`+
			`"candidates":%s,"reason":%q}`,
		module, ncJSONArr(refs), disposition, ncJSONArr(candidates), reason)
}

func draftWriteNote(noteID string, drafts, coverage []string) string {
	blocks := []string{covSB("L1-L2", "甲行\n乙行"), covSB("L3-L4", "丙行\n丁行")}
	return fmt.Sprintf(
		`{"op":"write_note","source":"%s","note_id":%q,"title":"候选笔记",`+
			`"blocks":[%s],"omissions":[],"candidate_drafts":[%s],"candidate_coverage":[%s]}`,
		covSrcID, noteID, strings.Join(blocks, ","), strings.Join(drafts, ","), strings.Join(coverage, ","))
}

func legalDrafts() []string {
	return []string{
		draftCandidate("cand-knowledge", "knowledge", "知识候选", []string{"L1-L2"},
			[]string{draftSection(mdfile.SecKnowledge, "知识正文。"),
				draftSection(mdfile.SecBoundary, "知识边界。")}),
		draftCandidate("cand-opinion", "opinion", "观点候选", []string{"L3-L4"},
			[]string{draftSection(mdfile.SecOpinionClaim, "观点正文。"),
				draftSection(mdfile.SecArgument, "论据。"),
				draftSection(mdfile.SecCounter, "反例。"),
				draftSection(mdfile.SecToVerify, "待验证证据。")}),
	}
}

func legalDraftCoverage() []string {
	return []string{
		draftCoverage("m-1", []string{"L1-L2"}, mdfile.CandidateCoverageCandidate,
			[]string{"cand-knowledge"}, ""),
		draftCoverage("m-2", []string{"L3-L4"}, mdfile.CandidateCoverageCandidate,
			[]string{"cand-opinion"}, ""),
	}
}

func TestParseCandidateDraftFieldsInOrder(t *testing.T) {
	body := `{"ops":[` + draftWriteNote(
		"n-20260922-draft-parse", legalDrafts(), legalDraftCoverage()) + `]}`
	p := parsePlan(t, body)
	if len(p.Diags) != 0 {
		t.Fatalf("合法 candidate 字段不应产生解析诊断：%v", p.Diags)
	}
	op := firstOp(t, p)
	if !op.CandidateDraftsGiven || !op.CandidateCoverageGiven {
		t.Fatalf("candidate Given 标志未保留：drafts=%v coverage=%v",
			op.CandidateDraftsGiven, op.CandidateCoverageGiven)
	}
	if len(op.CandidateDrafts) != 2 || op.CandidateDrafts[0].Key != "cand-knowledge" ||
		op.CandidateDrafts[1].Key != "cand-opinion" {
		t.Fatalf("candidate_drafts 顺序未保留：%+v", op.CandidateDrafts)
	}
	if got := string(op.CandidateDrafts[0].Sections[1].Body); got != "知识边界。" {
		t.Fatalf("candidate section body 未逐字承载：%q", got)
	}
	if len(op.CandidateCoverage) != 2 ||
		strings.Join(op.CandidateCoverage[1].Candidates, ",") != "cand-opinion" {
		t.Fatalf("candidate_coverage 未按序承载：%+v", op.CandidateCoverage)
	}
}

func TestParseCandidateDraftMalformedNestedFields(t *testing.T) {
	cases := []struct {
		name string
		op   string
		path string
	}{
		{"drafts null", `{"op":"write_note","candidate_drafts":null}`,
			"ops[0].candidate_drafts"},
		{"draft item scalar", `{"op":"write_note","candidate_drafts":[7]}`,
			"ops[0].candidate_drafts[0]"},
		{"sections scalar", `{"op":"write_note","candidate_drafts":[{"sections":"x"}]}`,
			"ops[0].candidate_drafts[0].sections"},
		{"coverage candidates scalar",
			`{"op":"write_note","candidate_coverage":[{"candidates":"cand-x"}]}`,
			"ops[0].candidate_coverage[0].candidates"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := parsePlan(t, `{"ops":[`+tc.op+`]}`)
			requireErrorAt(t, &Result{Errors: p.Diags}, E5, tc.path)
		})
	}
}

func TestParseCandidateDraftUnknownNestedFieldsAreI1(t *testing.T) {
	op := draftWriteNote("n-20260922-extra", legalDrafts(), legalDraftCoverage())
	op = strings.Replace(op, `"sections":[`, `"future_draft":"x","sections":[`, 1)
	op = strings.Replace(op, `"body":"知识正文。"`, `"body":"知识正文。","future_section":"y"`, 1)
	op = strings.Replace(op, `"reason":""}`, `"reason":"","future_coverage":"z"}`, 1)
	p := parsePlan(t, `{"ops":[`+op+`]}`)
	var infos int
	for _, d := range p.Diags {
		if d.Code == I1 && strings.Contains(d.Path, "future_") {
			infos++
		}
	}
	if infos != 3 {
		t.Fatalf("三处 candidate 未知字段应各产生 I1，实得 %d：%v", infos, p.Diags)
	}
}

func TestCandidateDraftWriteNotePersistsWithoutFinalOutputs(t *testing.T) {
	files := covSourceFile(covBody4)
	op := draftWriteNote("n-20260922-draft-e2e", legalDrafts(), legalDraftCoverage())
	res := v2Run(t, files, op)
	requireNoError(t, res)
	if len(res.Actions) != 1 || res.Actions[0].Extraction != nil {
		t.Fatalf("candidate 路径应只有 note action 且不伪造 final extraction：%+v", res.Actions)
	}
	if len(res.Actions[0].Sections) != 2 ||
		res.Actions[0].Sections[1].Section != store.SecExtraction {
		t.Fatalf("candidate 草稿应作为提取结果的第二个 section write：%+v", res.Actions[0].Sections)
	}

	dir, out := execOn(t, files, res)
	if len(out.Written) == 0 {
		t.Fatal("candidate write_note 应真实落盘")
	}
	raw := []byte(readVaultFile(t, dir,
		"domains/ai-infra/notes/n-20260922-draft-e2e.md"))
	if bytes.Contains(raw, []byte("### Knowledge")) ||
		bytes.Contains(raw, []byte("### Opinion")) ||
		bytes.Contains(raw, []byte("### 覆盖矩阵")) {
		t.Fatalf("草稿 Note 不得伪造 final outputs/extraction coverage：\n%s", raw)
	}
	candidates, err := mdfile.ParseCandidates(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 || candidates[0].Anchor.Output != "" ||
		candidates[1].Anchor.Output != "" {
		t.Fatalf("落盘 candidate 数量或 output 错误：%+v", candidates)
	}
	at := bytes.Index(raw, []byte("### 候选覆盖"))
	if at < 0 {
		t.Fatalf("落盘 Note 缺候选覆盖：\n%s", raw)
	}
	end := bytes.Index(raw[at:], []byte("\n## "))
	if end < 0 {
		t.Fatal("无法定位候选覆盖矩阵结尾")
	}
	coverage, err := mdfile.ParseCandidateCoverageMatrix(raw[at : at+end])
	if err != nil {
		t.Fatal(err)
	}
	if len(coverage) != 2 {
		t.Fatalf("候选覆盖条目数=%d", len(coverage))
	}
}

func TestCandidateDraftAllowsUnresolvedButKeepsItDraftOnly(t *testing.T) {
	drafts := legalDrafts()[:1]
	coverage := []string{
		draftCoverage("m-1", []string{"L1-L2"}, mdfile.CandidateCoverageCandidate,
			[]string{"cand-knowledge"}, ""),
		draftCoverage("m-2", []string{"L3-L4"}, mdfile.CandidateCoverageUnresolved,
			nil, "尚缺观点候选"),
	}
	res := covValidate(t, covBody4,
		draftWriteNote("n-20260922-draft-unresolved", drafts, coverage))
	requireNoError(t, res)
	if len(res.Actions) != 1 || res.Actions[0].Extraction != nil {
		t.Fatalf("unresolved 只能保存为 draft，不能生成 final extraction：%+v", res.Actions)
	}
}

func TestCandidateDraftRejectsFinalStateFields(t *testing.T) {
	base := draftWriteNote("n-20260922-draft-mixed", legalDrafts(), legalDraftCoverage())
	cases := map[string]struct {
		op   string
		path string
	}{
		"output cards": {
			op: strings.TrimSuffix(base, "}") +
				`,"output_cards":[{"card":"k-20260922-fake","mode":"新建"}]}`,
			path: "ops[0].output_cards",
		},
		"final coverage": {
			op: strings.TrimSuffix(base, "}") +
				`,"extraction_coverage":[{"module":"m","source_refs":["L1-L2"],` +
				`"summary":"x","disposition":"outputs","outputs":["k-20260922-fake"]}]}`,
			path: "ops[0].extraction_coverage",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			res := covValidate(t, covBody4, tc.op)
			requireErrorAt(t, res, E2, tc.path)
			if len(res.Actions) != 0 {
				t.Fatal("draft/final 混用必须零 action")
			}
		})
	}
}

func TestCandidateDraftValidationRejectsInvalidReferencesAndDispositions(t *testing.T) {
	drafts := legalDrafts()
	cases := map[string]struct {
		drafts   []string
		coverage []string
		path     string
	}{
		"missing coverage": {
			drafts: drafts, coverage: nil, path: "ops[0].candidate_coverage",
		},
		"dangling draft source ref": {
			drafts: []string{draftCandidate("cand-bad-ref", "knowledge", "坏引用",
				[]string{"L9-L9"}, []string{draftSection(mdfile.SecKnowledge, "正文。")})},
			coverage: []string{draftCoverage("m-1", []string{"L1-L4"},
				mdfile.CandidateCoverageCandidate, []string{"cand-bad-ref"}, "")},
			path: "ops[0].candidate_drafts[0].source_refs[0]",
		},
		"dangling candidate key": {
			drafts: drafts,
			coverage: []string{
				draftCoverage("m-1", []string{"L1-L2"}, mdfile.CandidateCoverageCandidate,
					[]string{"cand-missing"}, ""),
				draftCoverage("m-2", []string{"L3-L4"}, mdfile.CandidateCoverageNoteOnly,
					nil, "仅记录"),
			},
			path: "ops[0].candidate_coverage[0].candidates[0]",
		},
		"candidate with reason": {
			drafts: drafts,
			coverage: []string{
				draftCoverage("m-1", []string{"L1-L2"}, mdfile.CandidateCoverageCandidate,
					[]string{"cand-knowledge"}, "不应有"),
				draftCoverage("m-2", []string{"L3-L4"}, mdfile.CandidateCoverageCandidate,
					[]string{"cand-opinion"}, ""),
			},
			path: "ops[0].candidate_coverage[0].reason",
		},
		"unresolved without reason": {
			drafts: drafts,
			coverage: []string{
				draftCoverage("m-1", []string{"L1-L2"}, mdfile.CandidateCoverageCandidate,
					[]string{"cand-knowledge"}, ""),
				draftCoverage("m-2", []string{"L3-L4"}, mdfile.CandidateCoverageUnresolved,
					nil, " "),
			},
			path: "ops[0].candidate_coverage[1].reason",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			res := covValidate(t, covBody4,
				draftWriteNote("n-20260922-invalid-"+strings.ReplaceAll(name, " ", "-"),
					tc.drafts, tc.coverage))
			requireErrorAt(t, res, E2, tc.path)
			if len(res.Actions) != 0 {
				t.Fatal("非法 candidate draft 必须零 action")
			}
		})
	}
}
