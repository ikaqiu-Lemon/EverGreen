package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	evergreencore "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	kernelEvergreen "github.com/siyuan-note/siyuan/kernel/evergreen"
	"github.com/siyuan-note/siyuan/kernel/model"
)

func TestEvergreenNoteCreateAPIUsesAuthenticatedSharedCore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	committer := &apiRecordingCommitter{}
	runtime, err := kernelEvergreen.NewRuntime(context.Background(), kernelEvergreen.RuntimeOptions{
		Root: root, Holder: "note-api-test", Committer: committer,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	oldProvider := evergreenRuntimeProvider
	evergreenRuntimeProvider = func(context.Context) (*kernelEvergreen.Runtime, error) { return runtime, nil }
	t.Cleanup(func() { evergreenRuntimeProvider = oldProvider })

	model.InitJwtKey()
	agentJWT, err := model.CreatePluginJWT("note-agent")
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{
		model.XAuthTokenKey: agentJWT, evergreenCallerHeader: "agent",
		evergreenAgentIDHeader: "note-agent", evergreenRequestReasonHeader: "create reviewed Note",
	}
	engine := evergreenTestEngine(t)
	source := evergreencore.SourceCaptureRequest{
		OperationID: "op-note-api-source", LogicalID: "s-note-api",
		DocumentID: "20261012190000-src0001", Path: "box/source.sy",
		URL: "https://example.com/note-api", Title: "Note API Source",
		Body: "Source body for authenticated Note creation.", Reason: "prepare Note",
		Domain: "systems", Tags: []string{"api"},
		CapturedAt: "2026-10-12T19:00:00+08:00",
	}
	sourcePlan := performEvergreenAPI[evergreencore.PlannedSourceCapture](
		t, engine, http.MethodPost, "/api/evergreen/v1/sources/plan-capture", source, headers)
	if !sourcePlan.OK || sourcePlan.Data.Operation == nil {
		t.Fatalf("Source plan = %+v", sourcePlan)
	}
	sourceApplied := performEvergreenAPI[evergreencore.OperationRecord](
		t, engine, http.MethodPost, "/api/evergreen/v1/apply",
		evergreencore.ApplyRequest{Operation: *sourcePlan.Data.Operation}, headers)
	if !sourceApplied.OK {
		t.Fatalf("Source apply = %+v", sourceApplied)
	}
	sourceBefore, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(source.Path)))
	if err != nil {
		t.Fatal(err)
	}

	request := evergreencore.NoteCreateRequest{
		OperationID: "op-note-api-create", LogicalID: "n-note-api",
		DocumentID: "20261012190100-note001", Path: "box/note.sy",
		SourceID: source.LogicalID, Title: "API reviewed Note",
		Segments: []evergreencore.NoteCreateSegment{{
			SegmentID: "seg-note-api", Role: "note_segment",
			Locator: &evergreencore.Locator{Kind: "source-lines", Value: "L1-L1"},
			Content: json.RawMessage(`[
				{"ID":"20261012190101-seg0001","Type":"NodeParagraph","Data":"Source body"}
			]`),
		}},
		Candidates: []evergreencore.NoteCreateCandidate{{
			CandidateID: "cand-note-api", ClaimKind: evergreencore.KnowledgeKind,
			Title: "API claim", SegmentRefs: []evergreencore.LogicalID{"seg-note-api"},
			Reason: "The Source segment supports the Candidate.",
			Payload: json.RawMessage(`[
				{"ID":"20261012190102-payload","Type":"NodeParagraph","Data":"API claim body"}
			]`),
		}},
		Coverage: []evergreencore.CoverageModule{{
			ModuleID: "coverage-note-api", Disposition: "candidate",
			SegmentRefs: []evergreencore.LogicalID{"seg-note-api"}, CandidateID: "cand-note-api",
		}},
	}
	planned := performEvergreenAPI[evergreencore.PlannedNoteCreate](
		t, engine, http.MethodPost, "/api/evergreen/v1/notes/plan-create", request, headers)
	if !planned.OK ||
		planned.Data.Operation.Plan.Command != evergreencore.NoteCreateCommand ||
		planned.Data.Operation.Plan.Principal.Type != evergreencore.PrincipalAgent ||
		planned.Data.Operation.Plan.Principal.ID != "note-agent" ||
		planned.Data.Operation.Plan.Principal.AuthSource != "kernel-plugin-jwt" ||
		planned.Data.NoteID != request.LogicalID ||
		!planned.Data.Snapshot.CanMaterialize {
		t.Fatalf("authenticated Note plan = %+v", planned)
	}
	applied := performEvergreenAPI[evergreencore.OperationRecord](
		t, engine, http.MethodPost, "/api/evergreen/v1/apply",
		evergreencore.ApplyRequest{Operation: planned.Data.Operation}, headers)
	if !applied.OK || applied.Data.State != evergreencore.OperationCompleted {
		t.Fatalf("Note apply = %+v", applied)
	}
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(request.Path)))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := evergreencore.InspectNoteReview(raw, nil)
	if err != nil ||
		snapshot.NoteID != request.LogicalID ||
		snapshot.Candidates[0].Metadata.State != "draft" ||
		snapshot.Candidates[0].Metadata.MaterializedClaimID != "" {
		t.Fatalf("committed Note = %+v err=%v", snapshot, err)
	}
	sourceAfter, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(source.Path)))
	if err != nil {
		t.Fatal(err)
	}
	if string(sourceAfter) != string(sourceBefore) {
		t.Fatal("Note API rewrote its Source")
	}
	if committer.count != 2 ||
		committer.requests[0].Command != evergreencore.SourceCaptureCommand ||
		committer.requests[1].Command != evergreencore.NoteCreateCommand {
		t.Fatalf("Source/Note commits = %+v", committer.requests)
	}
}
