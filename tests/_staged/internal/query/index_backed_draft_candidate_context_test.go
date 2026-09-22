package query_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/index"
	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

func draftCandidateFixture(t *testing.T) (string, model.NoteID, string) {
	t.Helper()
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
	return root, noteID, rel
}

func TestContextProjectsDraftCandidatesFromAuthoritativeNote(t *testing.T) {
	root, noteID, rel := draftCandidateFixture(t)
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

func TestContextCandidateSidecarHealthyAndFallbackEquivalent(t *testing.T) {
	root, noteID, _ := draftCandidateFixture(t)
	req := query.Request{Note: string(noteID)}

	direct := build(t, root, req)
	assertCandidateSidecarCodes(t, direct, index.CodeIndexMissing, query.CodeQ5)

	scan, err := query.VaultScan(root, query.ScanOptions{
		Domains: []string{"ai-infra"}, IncludeNotes: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	docs := query.CandidateBlockDocuments(scan.Notes, store.ContentHash)
	if _, err := index.SyncBlocks(index.DirPath(root), docs); err != nil {
		t.Fatal(err)
	}
	sidecar := build(t, root, req)
	if !reflect.DeepEqual(sidecar.DraftCandidates, direct.DraftCandidates) {
		t.Fatalf("sidecar 与直接扫描 candidate 不等价：\nsidecar=%+v\ndirect=%+v",
			sidecar.DraftCandidates, direct.DraftCandidates)
	}
	assertCandidateSidecarCodes(t, sidecar)

	path := filepath.Join(index.BlocksDirPath(root), string(noteID)+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var tampered index.BlockDocument
	if err := json.Unmarshal(raw, &tampered); err != nil {
		t.Fatal(err)
	}
	tampered.Candidates[0].Title = "篡改标题"
	raw, err = index.RenderBlockDocument(tampered)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	stale := build(t, root, req)
	if !reflect.DeepEqual(stale.DraftCandidates, direct.DraftCandidates) {
		t.Fatalf("合法 JSON 篡改不得污染查询：%+v", stale.DraftCandidates)
	}
	assertCandidateSidecarCodes(t, stale, index.CodeIndexStale, query.CodeQ5)

	if err := os.WriteFile(path, []byte("{bad json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	corrupt := build(t, root, req)
	if !reflect.DeepEqual(corrupt.DraftCandidates, direct.DraftCandidates) {
		t.Fatalf("损坏 sidecar 不得污染查询：%+v", corrupt.DraftCandidates)
	}
	assertCandidateSidecarCodes(t, corrupt, index.CodeIndexCorrupt, query.CodeQ5)

	if err := os.RemoveAll(index.BlocksDirPath(root)); err != nil {
		t.Fatal(err)
	}
	missing := build(t, root, req)
	if !reflect.DeepEqual(missing.DraftCandidates, direct.DraftCandidates) {
		t.Fatalf("删除 sidecar 后直接扫描结果漂移：%+v", missing.DraftCandidates)
	}
	assertCandidateSidecarCodes(t, missing, index.CodeIndexMissing, query.CodeQ5)
}

func TestContextCandidateSidecarPreservesParseDiagnostics(t *testing.T) {
	root, noteID, rel := draftCandidateFixture(t)
	notePath := filepath.Join(root, filepath.FromSlash(rel))
	raw, err := os.ReadFile(notePath)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte("eg:cd:1 "), []byte("eg:cd:9 "), 1)
	if err := os.WriteFile(notePath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	scan, err := query.VaultScan(root, query.ScanOptions{
		Domains: []string{"ai-infra"}, IncludeNotes: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	docs := query.CandidateBlockDocuments(scan.Notes, store.ContentHash)
	var target *index.BlockDocument
	for i := range docs {
		if docs[i].NoteID == string(noteID) {
			target = &docs[i]
			break
		}
	}
	if target == nil || len(target.Candidates) != 0 ||
		len(target.Diagnostics) != 1 || target.Diagnostics[0].Code != query.CodeQ1 {
		t.Fatalf("畸形 candidate 的 sidecar 诊断投影不对：%+v", docs)
	}
	if _, err := index.SyncBlocks(index.DirPath(root), docs); err != nil {
		t.Fatal(err)
	}
	healthy := build(t, root, query.Request{Note: string(noteID)})
	if got := diagnosticSignature(healthy.Diagnostics); !reflect.DeepEqual(
		got, []string{query.CodeQ1 + ":" + rel, query.CodeQ3 + ":(汇总)"}) {
		t.Fatalf("健康 sidecar 未复现直接扫描诊断：%v", got)
	}

	if err := os.RemoveAll(index.BlocksDirPath(root)); err != nil {
		t.Fatal(err)
	}
	fallback := build(t, root, query.Request{Note: string(noteID)})
	if got := businessDiagnosticSignature(fallback.Diagnostics); !reflect.DeepEqual(
		got, diagnosticSignature(healthy.Diagnostics)) {
		t.Fatalf("sidecar 回落改变业务诊断：healthy=%v fallback=%v",
			diagnosticSignature(healthy.Diagnostics), got)
	}
	assertCandidateSidecarCodes(t, fallback, index.CodeIndexMissing, query.CodeQ5)
}

func diagnosticSignature(diags []query.Diagnostic) []string {
	out := make([]string, 0, len(diags))
	for _, diag := range diags {
		out = append(out, diag.Code+":"+diag.Path)
	}
	return out
}

func businessDiagnosticSignature(diags []query.Diagnostic) []string {
	var out []query.Diagnostic
	for _, diag := range diags {
		switch diag.Code {
		case index.CodeIndexMissing, index.CodeIndexStale, index.CodeIndexCorrupt, query.CodeQ5:
			continue
		default:
			out = append(out, diag)
		}
	}
	return diagnosticSignature(out)
}

func assertCandidateSidecarCodes(t *testing.T, ctx *query.Context, want ...string) {
	t.Helper()
	got := make([]string, 0, len(ctx.Diagnostics))
	for _, diag := range ctx.Diagnostics {
		if diag.Code == index.CodeIndexMissing || diag.Code == index.CodeIndexStale ||
			diag.Code == index.CodeIndexCorrupt || diag.Code == query.CodeQ5 {
			got = append(got, diag.Code)
		}
	}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("sidecar 诊断码=%v，期望 %v；全部诊断=%+v", got, want, ctx.Diagnostics)
	}
}
