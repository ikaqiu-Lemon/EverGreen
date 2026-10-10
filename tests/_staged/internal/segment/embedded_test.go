package segment

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
)

func TestEmbeddedFinalReviewRetainsOutputsAndOverlappingSourceDispositions(t *testing.T) {
	blocks := []mdfile.ReviewBlock{
		{Role: mdfile.ReviewRoleSource, SourceRef: "L1-L4", Body: []byte("A fact and a time-sensitive observation.")},
		{Role: mdfile.ReviewRoleAgent, Annotation: "summary", Body: []byte("Agent summary.")},
	}
	body, err := mdfile.RenderReviewNote(blocks, nil)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := mdfile.RenderCandidateDraft(mdfile.CandidateDraft{Key: "cand-embedded",
		Kind: mdfile.CandidateKindKnowledge, Title: "Embedded claim", SourceRefs: []string{"L1-L4"},
		Rel: "support", Reason: "Existing fact establishes the claim", Output: "k-20261010-embedded",
		Sections: []mdfile.CandidateDraftSection{{Name: "知识内容", Body: []byte("A fact.\n")},
			{Name: "条件与边界", Body: []byte("Within this context.\n")}}})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := mdfile.RenderCandidateCoverageMatrix([]mdfile.CandidateCoverage{{
		Module: "fact", SourceRefs: []string{"L1-L4"}, Summary: "A fact", Disposition: "candidate", Candidates: []string{"cand-embedded"}}})
	if err != nil {
		t.Fatal(err)
	}
	note := []byte("---\nid: n-20261010-embedded\nsource: s-20261010-embedded\ncreated_at: 2026-10-10\nupdated_at: 2026-10-10T12:00:00Z\n---\n## 整理正文\n\n")
	note = append(note, body...)
	note = append(note, []byte("\n## 提取结果\n\n")...)
	note = append(note, candidate...)
	note = append(note, draft...)
	note = append(note, []byte("\n## 存疑与待验证\n\nKeep the question.\n\n## 用户补充\n\nKeep user text.\n")...)
	final, err := mdfile.RenderCoverageMatrix([]mdfile.ReviewCoverage{
		{Module: "fact", SourceRefs: []string{"L1-L4"}, Summary: "A fact", Disposition: "outputs", Outputs: []string{"k-20261010-embedded"}},
		{Module: "observation", SourceRefs: []string{"L1-L4"}, Summary: "Time-sensitive observation", Disposition: "note_only", Reason: "Preserve in Note"},
	})
	if err != nil {
		t.Fatal(err)
	}
	finalized, err := mdfile.FinalizeCandidateExtraction(note, final)
	if err != nil {
		t.Fatal(err)
	}
	first, err := ImportLegacyWorkspace(finalized, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ImportLegacyWorkspace(finalized, nil)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("embedded mapping not deterministic: %v", err)
	}
	snapshot, err := core.InspectNoteReview(first.SY, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Segments) != 2 || len(snapshot.Candidates) != 1 || len(snapshot.Coverage) != 2 ||
		snapshot.Candidates[0].Metadata.State != "materialized" ||
		snapshot.Candidates[0].Metadata.MaterializedClaimID != "k-20261010-embedded" ||
		snapshot.Summary.MissingSegments != 0 || snapshot.Summary.DuplicateSegments != 0 {
		t.Fatalf("embedded state lost: %+v", snapshot)
	}
	decoded, _ := core.DecodeSY(first.SY, nil)
	var legacy map[string]json.RawMessage
	if err := json.Unmarshal(decoded.Envelope.Review.Extra["legacy_source_coverage"], &legacy); err != nil ||
		!strings.Contains(string(legacy["Final"]), "Time-sensitive observation") {
		t.Fatalf("fine-grained final coverage lost: %v", err)
	}
	var root map[string]json.RawMessage
	_ = json.Unmarshal(first.SY, &root)
	markdown, err := core.MarkdownText(root["Children"])
	if err != nil || !strings.Contains(string(markdown), "Keep user text.") ||
		!strings.Contains(string(markdown), "Keep the question.") || strings.Count(string(markdown), "A fact.") != 1 {
		t.Fatalf("body/payload duplication or loss: %s %v", markdown, err)
	}
}
