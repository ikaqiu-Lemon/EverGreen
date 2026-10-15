package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	kernelEvergreen "github.com/siyuan-note/siyuan/kernel/evergreen"
	"github.com/siyuan-note/siyuan/kernel/model"
)

func TestEvergreenFreshWorkspaceMainFlowThroughHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	committer := &apiRecordingCommitter{}
	runtime, err := kernelEvergreen.NewRuntime(
		context.Background(), kernelEvergreen.RuntimeOptions{
			Root: root, Holder: "api-main-flow", Committer: committer,
		})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	oldProvider := evergreenRuntimeProvider
	evergreenRuntimeProvider = func(context.Context) (*kernelEvergreen.Runtime, error) {
		return runtime, nil
	}
	t.Cleanup(func() { evergreenRuntimeProvider = oldProvider })

	model.InitJwtKey()
	agentJWT, err := model.CreatePluginJWT("main-flow-agent")
	if err != nil {
		t.Fatal(err)
	}
	agentHeaders := map[string]string{
		model.XAuthTokenKey: agentJWT, evergreenCallerHeader: "agent",
		evergreenAgentIDHeader:       "main-flow-agent",
		evergreenRequestReasonHeader: "build reviewed Claims from Source evidence",
	}
	userHeaders := map[string]string{evergreenCallerHeader: "ui"}
	engine := evergreenTestEngine(t)

	apply := func(operation core.PlannedOperation, headers map[string]string) core.OperationRecord {
		t.Helper()
		response := performEvergreenAPI[core.OperationRecord](
			t, engine, http.MethodPost, "/api/evergreen/v1/apply",
			core.ApplyRequest{Operation: operation}, headers)
		if !response.OK || response.Data.State != core.OperationCompleted {
			t.Fatalf("apply %s = %+v", operation.Plan.OperationID, response)
		}
		return response.Data
	}

	source := core.SourceCaptureRequest{
		OperationID: "op-http-flow-source", LogicalID: "s-http-main-flow",
		DocumentID: "20261013070000-src0001", Path: "box/source.sy",
		URL: "https://example.com/http-main-flow", Title: "HTTP main flow Source",
		Body:   "Complete Source evidence for the HTTP main flow.",
		Reason: "Create a reviewed Note", Domain: "systems", Tags: []string{"http"},
		CapturedAt: "2026-10-13T07:00:00+08:00",
	}
	sourcePlan := performEvergreenAPI[core.PlannedSourceCapture](
		t, engine, http.MethodPost, "/api/evergreen/v1/sources/plan-capture", source, agentHeaders)
	if !sourcePlan.OK || sourcePlan.Data.Operation == nil {
		t.Fatalf("Source plan = %+v", sourcePlan)
	}
	apply(*sourcePlan.Data.Operation, agentHeaders)

	note := core.NoteCreateRequest{
		OperationID: "op-http-flow-note", LogicalID: "n-http-main-flow",
		DocumentID: "20261013070100-note001", Path: "box/note.sy",
		SourceID: source.LogicalID, Title: "HTTP reviewed Note",
		Segments: []core.NoteCreateSegment{{
			SegmentID: "seg-http-main", Role: "note_segment",
			Locator: &core.Locator{Kind: "source-lines", Value: "L1-L1"},
			Content: json.RawMessage(`[
				{"ID":"20261013070101-seg0001","Type":"NodeParagraph","Data":"Complete Source evidence."}
			]`),
		}},
		Candidates: []core.NoteCreateCandidate{
			{
				CandidateID: "cand-http-knowledge", ClaimKind: core.KnowledgeKind,
				Title: "HTTP knowledge", SegmentRefs: []core.LogicalID{"seg-http-main"},
				Relation: "support", Reason: "The Source directly supports this Claim.",
				Payload: json.RawMessage(`[
					{"ID":"20261013070102-know001","Type":"NodeParagraph","Data":"HTTP knowledge body."}
				]`),
			},
			{
				CandidateID: "cand-http-opinion", ClaimKind: core.OpinionKind,
				Title: "HTTP opinion", SegmentRefs: []core.LogicalID{"seg-http-main"},
				Relation: "context", Reason: "The Source provides context for this opinion.",
				Payload: json.RawMessage(`[
					{"ID":"20261013070103-opin001","Type":"NodeParagraph","Data":"HTTP opinion body."}
				]`),
			},
		},
		Coverage: []core.CoverageModule{{
			ModuleID: "coverage-http-main", Disposition: "candidate",
			SegmentRefs: []core.LogicalID{"seg-http-main"},
			CandidateIDs: []core.LogicalID{
				"cand-http-knowledge", "cand-http-opinion",
			},
		}},
	}
	notePlan := performEvergreenAPI[core.PlannedNoteCreate](
		t, engine, http.MethodPost, "/api/evergreen/v1/notes/plan-create", note, agentHeaders)
	if !notePlan.OK || notePlan.Data.Snapshot.Summary.CandidateCount != 2 {
		t.Fatalf("Note plan = %+v", notePlan)
	}
	apply(notePlan.Data.Operation, agentHeaders)

	confirm := func(operationID string, candidateID core.LogicalID) {
		t.Helper()
		current := performEvergreenAPI[core.ReviewSnapshot](
			t, engine, http.MethodPost, "/api/evergreen/v1/review/get",
			map[string]any{"note_id": note.LogicalID}, userHeaders)
		planned := performEvergreenAPI[core.PlannedReview](
			t, engine, http.MethodPost, "/api/evergreen/v1/review/plan",
			core.ReviewPlanRequest{
				OperationID: operationID, NoteID: note.LogicalID,
				Base: current.Data.SemanticHash,
				Command: core.ReviewCommand{
					Action: core.ReviewActionCandidateConfirm, CandidateID: candidateID,
				},
			}, userHeaders)
		if !planned.OK {
			t.Fatalf("confirm %s = %+v", candidateID, planned)
		}
		apply(planned.Data.Operation, userHeaders)
	}
	confirm("op-http-confirm-knowledge", "cand-http-knowledge")
	confirm("op-http-confirm-opinion", "cand-http-opinion")

	materialize := func(
		operationID string,
		candidateID, claimID core.LogicalID,
		documentID, path string,
	) {
		t.Helper()
		current := performEvergreenAPI[core.ReviewSnapshot](
			t, engine, http.MethodPost, "/api/evergreen/v1/review/get",
			map[string]any{"note_id": note.LogicalID}, userHeaders)
		planned := performEvergreenAPI[core.PlannedMaterialization](
			t, engine, http.MethodPost, "/api/evergreen/v1/review/plan-materialize",
			core.ReviewMaterializeRequest{
				OperationID: operationID, NoteID: note.LogicalID,
				CandidateID: candidateID, NoteBase: current.Data.SemanticHash,
				ClaimID: claimID, ClaimPath: path, ClaimDocumentID: documentID,
				CreatedAt: "2026-10-13T07:10:00+08:00",
			}, userHeaders)
		if !planned.OK {
			t.Fatalf("materialize %s = %+v", candidateID, planned)
		}
		first := apply(planned.Data.Operation, userHeaders)
		retry := apply(planned.Data.Operation, userHeaders)
		if first.GitCommit != retry.GitCommit {
			t.Fatalf("materialize retry changed commit: first=%+v retry=%+v", first, retry)
		}
	}
	materialize(
		"op-http-materialize-knowledge", "cand-http-knowledge", "c-http-knowledge",
		"20261013071000-claim01", "box/knowledge.sy")
	materialize(
		"op-http-materialize-opinion", "cand-http-opinion", "c-http-opinion",
		"20261013071001-claim02", "box/opinion.sy")

	claim := func(id core.LogicalID) core.ClaimSnapshot {
		t.Helper()
		response := performEvergreenAPI[core.ClaimSnapshot](
			t, engine, http.MethodPost, "/api/evergreen/v1/claims/get",
			map[string]any{"logical_id": id}, userHeaders)
		if !response.OK {
			t.Fatalf("Claim %s = %+v", id, response)
		}
		return response.Data
	}

	knowledge := claim("c-http-knowledge")
	argumentPlan := performEvergreenAPI[core.PlannedOperation](
		t, engine, http.MethodPost, "/api/evergreen/v1/av/plan-patch",
		core.AVPatch{
			OperationID: "op-http-argument", RowID: knowledge.LogicalID,
			Base: core.BaseRef{
				LogicalID: knowledge.LogicalID, SemanticHash: knowledge.Base,
			},
			Column: core.AVColumnBinding{
				Direction: core.EdgeDirectionOutgoing, EdgeSchema: core.ArgumentSchema,
			},
			Edge: core.AVEdgePatch{
				Action: core.AVEdgeAdd,
				Edge: core.TypedEdge{
					ID: "edge-http-argument", Schema: core.ArgumentSchema,
					Target: core.EntityRef{
						EntityType: core.EntityClaim, LogicalID: "c-http-opinion",
					},
					Type: "supports", Reason: "The knowledge supports the opinion.",
					CreatedAt: "2026-10-13T07:11:00+08:00",
					UpdatedAt: "2026-10-13T07:11:00+08:00",
				},
			},
		}, agentHeaders)
	if !argumentPlan.OK {
		t.Fatalf("argument plan = %+v", argumentPlan)
	}
	apply(argumentPlan.Data, agentHeaders)

	opinion := claim("c-http-opinion")
	validationPlan := performEvergreenAPI[core.PlannedOperation](
		t, engine, http.MethodPost, "/api/evergreen/v1/av/plan-patch",
		map[string]any{
			"operation_id": "op-http-validation",
			"row_id":       opinion.LogicalID,
			"base": map[string]any{
				"logical_id": opinion.LogicalID, "semantic_hash": opinion.Base,
			},
			"column": map[string]any{"field": "claim.kind_data.validation"},
			"value": map[string]any{
				"kind": "json",
				"json": map[string]any{
					"status": "validated", "method": "human-review",
					"evidence": []string{"edge-http-argument"},
				},
			},
		}, userHeaders)
	if !validationPlan.OK {
		t.Fatalf("validation plan = %+v", validationPlan)
	}
	apply(validationPlan.Data, userHeaders)

	knowledge = claim("c-http-knowledge")
	replacementPlan := performEvergreenAPI[core.PlannedOperation](
		t, engine, http.MethodPost, "/api/evergreen/v1/av/plan-patch",
		core.AVPatch{
			OperationID: "op-http-replacement", RowID: knowledge.LogicalID,
			Base: core.BaseRef{
				LogicalID: knowledge.LogicalID, SemanticHash: knowledge.Base,
			},
			Column: core.AVColumnBinding{
				Direction: core.EdgeDirectionOutgoing, EdgeSchema: core.ReplacementSchema,
			},
			Edge: core.AVEdgePatch{
				Action: core.AVEdgeAdd,
				Edge: core.TypedEdge{
					ID: "edge-http-replacement", Schema: core.ReplacementSchema,
					Target: core.EntityRef{
						EntityType: core.EntityClaim, LogicalID: "c-http-opinion",
					},
					Type: "replaced_by", Reason: "The validated opinion replaces this Claim.",
					CreatedAt: "2026-10-13T07:12:00+08:00",
					UpdatedAt: "2026-10-13T07:12:00+08:00",
				},
			},
		}, userHeaders)
	if !replacementPlan.OK {
		t.Fatalf("replacement plan = %+v", replacementPlan)
	}
	firstReplacement := apply(replacementPlan.Data, userHeaders)
	retryReplacement := apply(replacementPlan.Data, userHeaders)
	if firstReplacement.GitCommit != retryReplacement.GitCommit {
		t.Fatalf("replacement retry changed commit: first=%+v retry=%+v",
			firstReplacement, retryReplacement)
	}

	page := performEvergreenAPI[core.AVPage](
		t, engine, http.MethodPost, "/api/evergreen/v1/av/query",
		map[string]any{"view_id": core.ClaimsViewID, "query": core.AVQuery{}}, userHeaders)
	if !page.OK || page.Data.Total != 2 {
		t.Fatalf("final Claims page = %+v", page)
	}
	var knowledgeRow, opinionRow *core.AVRow
	for index := range page.Data.Rows {
		row := &page.Data.Rows[index]
		switch row.Ref.LogicalID {
		case "c-http-knowledge":
			knowledgeRow = row
		case "c-http-opinion":
			opinionRow = row
		}
	}
	if knowledgeRow == nil || opinionRow == nil ||
		opinionRow.Cells["validation"].Text != "validated" ||
		opinionRow.Cells["arguments_incoming"].EdgeCount != 1 {
		t.Fatalf("final projections: knowledge=%+v opinion=%+v", knowledgeRow, opinionRow)
	}
	finalKnowledge := claim("c-http-knowledge")
	var hasArgument, hasReplacement bool
	for _, edge := range finalKnowledge.Envelope.Relations.Outgoing {
		hasArgument = hasArgument || edge.ID == "edge-http-argument"
		hasReplacement = hasReplacement || edge.ID == "edge-http-replacement"
	}
	if !hasArgument || !hasReplacement || committer.count != 9 {
		t.Fatalf("final authority=%+v commits=%d", finalKnowledge.Envelope.Relations, committer.count)
	}
}
