package query_test

import (
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

func TestContextProjectsDraftCandidatesFromAuthoritativeNote(t *testing.T) {
	root := fixture(t)
	review, err := store.NoteReviewBytes([]store.NoteBlock{{
		Role: store.NoteBlockSource, Body: []byte("候选来源。"), SourceRef: "L1-L1",
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	extraction, err := store.CandidateDraftBytes([]store.CandidateDraft{{
		Key: "cand-query", Kind: store.CandidateKindKnowledge, Title: "查询候选",
		SourceRefs: []string{"L1-L1"}, Rel: "support", Reason: "来源定义",
		Sections: []store.CandidateDraftSection{{
			Name: store.SecKnowledge, Body: []byte("候选正文。\n"),
		}},
	}}, []store.CandidateCoverage{{
		Module: "m-query", SourceRefs: []string{"L1-L1"}, Summary: "查询候选",
		Disposition: store.CandidateCoverageCandidate, Candidates: []string{"cand-query"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	date, _ := model.ParseDate("2026-09-22")
	stamp, _ := model.ParseStamp("2026-09-22T10:00:00+08:00")
	noteID := model.NoteID("n-20260922-query-candidate")
	rel := store.NoteRel("ai-infra", string(noteID))
	if _, err := store.New(root).ApplyNote(store.NoteSpec{
		Rel: rel, ID: noteID, SourceID: "s-20260412-demo",
		Title: "查询候选", Date: date, Stamp: stamp,
		Sections: []store.SectionAppend{
			{Section: store.SecNoteBody, Payload: review},
			{Section: store.SecExtraction, Payload: extraction},
		},
	}); err != nil {
		t.Fatal(err)
	}

	ctx := build(t, root, query.Request{Note: string(noteID)})
	if len(ctx.DraftCandidates) != 1 {
		t.Fatalf("draft_candidates 数量=%d，期望 1：%+v",
			len(ctx.DraftCandidates), ctx.DraftCandidates)
	}
	got := ctx.DraftCandidates[0]
	if got.Note != string(noteID) || got.Path != rel || got.Key != "cand-query" ||
		got.Kind != "knowledge" || got.Title != "查询候选" ||
		got.Status != "draft" || got.Output != "" {
		t.Fatalf("draft candidate 摘要不完整：%+v", got)
	}
	file, err := store.New(root).Read(rel)
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := mdfile.ParseCandidates(file.Bytes)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("读回 candidate：%v %+v", err, candidates)
	}
	wantHash := store.ContentHash(candidates[0].Raw(file.Bytes))
	if got.PayloadHash != wantHash {
		t.Fatalf("payload_hash=%s，期望 %s", got.PayloadHash, wantHash)
	}
	if len(ctx.KnowledgeCandidates) != 1 ||
		ctx.KnowledgeCandidates[0].ID != "c-20260412-attention" {
		t.Fatalf("Note 草稿不得混入已物化 Knowledge 候选：%+v", ctx.KnowledgeCandidates)
	}
}
