package evergreencore

import (
	"context"
	"encoding/json"
	"testing"
)

func TestFreshWorkspaceMainFlowRequiresHumanCandidateConfirmation(t *testing.T) {
	ctx := context.Background()
	service, host := sourceCaptureService(t)
	agent := noteCreatePrincipal()
	user := Principal{Type: PrincipalUser, ID: "review-owner", AuthSource: "fixture"}

	createNoteSource(t, service, host, agent)
	noteRequest := noteCreateRequest(
		"op-flow-note", "n-main-flow", "20261012210000-note001", "box/main-flow-note.sy")
	noteRequest.Candidates = append(noteRequest.Candidates, NoteCreateCandidate{
		CandidateID: "cand-opinion", ClaimKind: OpinionKind,
		Title: "Reviewed opinion", LogicalSlug: "reviewed-opinion",
		SegmentRefs: []LogicalID{"seg-source"}, Relation: "context",
		Reason: "The source segment provides context for this opinion.",
		Payload: json.RawMessage(`[
			{"ID":"20261012210004-opinion","Type":"NodeParagraph","Data":"A reviewed opinion."}
		]`),
	})
	noteRequest.Coverage[0].CandidateID = ""
	noteRequest.Coverage[0].CandidateIDs = []LogicalID{"cand-created", "cand-opinion"}
	notePlan, err := service.PlanNoteCreate(ctx, agent, noteRequest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Apply(ctx, agent, ApplyRequest{Operation: notePlan.Operation}); err != nil {
		t.Fatal(err)
	}

	noteRaw, err := host.Load(ctx, noteRequest.LogicalID)
	if err != nil {
		t.Fatal(err)
	}
	noteBase, err := SemanticHash(noteRaw)
	if err != nil {
		t.Fatal(err)
	}
	edited := mutateReviewSegment(t, noteRaw, "seg-source", "Edited source paragraph")
	editPlan, err := service.PlanReviewEdit(ctx, user, ReviewEditPlanRequest{
		OperationID: "op-flow-edit", NoteID: noteRequest.LogicalID,
		Base: noteBase, Edited: edited,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Apply(ctx, user, ApplyRequest{Operation: editPlan.Operation}); err != nil {
		t.Fatal(err)
	}

	snapshot, err := service.InspectReview(ctx, noteRequest.LogicalID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Candidates) != 2 ||
		!snapshot.Candidates[0].Stale || !snapshot.Candidates[1].Stale {
		t.Fatalf("native edit did not stale both Candidates: %+v", snapshot.Candidates)
	}
	agentConfirm := ReviewPlanRequest{
		OperationID: "op-flow-agent-confirm", NoteID: noteRequest.LogicalID,
		Base: snapshot.SemanticHash,
		Command: ReviewCommand{
			Action: ReviewActionCandidateConfirm, CandidateID: "cand-created",
		},
	}
	if _, err = service.PlanReview(ctx, agent, agentConfirm); !HasDiagnostic(err, CodeUnauthorized) {
		t.Fatalf("Agent replaced required human Candidate confirmation: %v", err)
	}
	userConfirm, err := service.PlanReview(ctx, user, ReviewPlanRequest{
		OperationID: "op-flow-raw-agent-confirm", NoteID: noteRequest.LogicalID,
		Base: snapshot.SemanticHash,
		Command: ReviewCommand{
			Action: ReviewActionCandidateConfirm, CandidateID: "cand-created",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	forged := userConfirm.Operation
	forged.Plan.Principal = agent
	forged.PlanHash = ComputePlanHash(forged.Plan, forged.Images)
	if _, err = service.Apply(ctx, agent, ApplyRequest{Operation: forged}); !HasDiagnostic(err, CodeUnauthorized) {
		t.Fatalf("Agent raw apply replaced required human Candidate confirmation: %v", err)
	}
	unchanged, err := service.InspectReview(ctx, noteRequest.LogicalID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.SemanticHash != snapshot.SemanticHash {
		t.Fatalf("denied Agent confirmation changed Note: before=%s after=%s",
			snapshot.SemanticHash, unchanged.SemanticHash)
	}

	confirm := func(operationID string, candidateID LogicalID) {
		t.Helper()
		current, inspectErr := service.InspectReview(ctx, noteRequest.LogicalID)
		if inspectErr != nil {
			t.Fatal(inspectErr)
		}
		planned, planErr := service.PlanReview(ctx, user, ReviewPlanRequest{
			OperationID: operationID, NoteID: noteRequest.LogicalID,
			Base: current.SemanticHash,
			Command: ReviewCommand{
				Action: ReviewActionCandidateConfirm, CandidateID: candidateID,
			},
		})
		if planErr != nil {
			t.Fatal(planErr)
		}
		if _, applyErr := service.Apply(ctx, user, ApplyRequest{Operation: planned.Operation}); applyErr != nil {
			t.Fatal(applyErr)
		}
	}
	confirm("op-flow-confirm-knowledge", "cand-created")
	confirm("op-flow-confirm-opinion", "cand-opinion")

	materialize := func(
		operationID string,
		candidateID, claimID LogicalID,
		documentID, path string,
	) OperationRecord {
		t.Helper()
		current, inspectErr := service.InspectReview(ctx, noteRequest.LogicalID)
		if inspectErr != nil {
			t.Fatal(inspectErr)
		}
		planned, planErr := service.PlanReviewMaterialization(ctx, user, ReviewMaterializeRequest{
			OperationID: operationID, NoteID: noteRequest.LogicalID,
			CandidateID: candidateID, NoteBase: current.SemanticHash,
			ClaimID: claimID, ClaimPath: path, ClaimDocumentID: documentID,
			CreatedAt: "2026-10-12T21:10:00+08:00",
		})
		if planErr != nil {
			t.Fatal(planErr)
		}
		record, applyErr := service.Apply(ctx, user, ApplyRequest{Operation: planned.Operation})
		if applyErr != nil {
			t.Fatal(applyErr)
		}
		retry, retryErr := service.Apply(ctx, user, ApplyRequest{Operation: planned.Operation})
		if retryErr != nil {
			t.Fatal(retryErr)
		}
		if retry.GitCommit != record.GitCommit {
			t.Fatalf("%s retry created a second commit: first=%+v retry=%+v",
				operationID, record, retry)
		}
		return record
	}
	materialize(
		"op-flow-materialize-knowledge", "cand-created", "c-main-knowledge",
		"20261012211000-claim01", "box/main-knowledge.sy")
	materialize(
		"op-flow-materialize-opinion", "cand-opinion", "c-main-opinion",
		"20261012211001-claim02", "box/main-opinion.sy")

	provider := NewClaimsAVProvider(host, service, nil)
	knowledge, err := service.InspectClaim(ctx, "c-main-knowledge")
	if err != nil {
		t.Fatal(err)
	}
	argument, err := provider.PlanPatch(ctx, agent, AVPatch{
		OperationID: "op-flow-argument", RowID: knowledge.LogicalID,
		Base: BaseRef{LogicalID: knowledge.LogicalID, SemanticHash: knowledge.Base},
		Column: AVColumnBinding{
			Direction: EdgeDirectionOutgoing, EdgeSchema: ArgumentSchema,
		},
		Edge: AVEdgePatch{
			Action: AVEdgeAdd,
			Edge: TypedEdge{
				ID: "edge-flow-argument", Schema: ArgumentSchema,
				Target: EntityRef{EntityType: EntityClaim, LogicalID: "c-main-opinion"},
				Type:   "supports", Reason: "The knowledge Claim supports the reviewed opinion.",
				CreatedAt: "2026-10-12T21:11:00+08:00",
				UpdatedAt: "2026-10-12T21:11:00+08:00",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Apply(ctx, agent, ApplyRequest{Operation: argument}); err != nil {
		t.Fatal(err)
	}

	opinion, err := service.InspectClaim(ctx, "c-main-opinion")
	if err != nil {
		t.Fatal(err)
	}
	validation, err := provider.PlanPatch(ctx, user, AVPatch{
		OperationID: "op-flow-validation", RowID: opinion.LogicalID,
		Base:   BaseRef{LogicalID: opinion.LogicalID, SemanticHash: opinion.Base},
		Column: AVColumnBinding{Field: "claim.kind_data.validation"},
		Value: &AVCellValue{
			Kind: "json",
			JSON: json.RawMessage(
				`{"status":"validated","method":"human-review","evidence":["edge-flow-argument"]}`),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Apply(ctx, user, ApplyRequest{Operation: validation}); err != nil {
		t.Fatal(err)
	}

	knowledge, err = service.InspectClaim(ctx, knowledge.LogicalID)
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := provider.PlanPatch(ctx, user, AVPatch{
		OperationID: "op-flow-replacement", RowID: knowledge.LogicalID,
		Base: BaseRef{LogicalID: knowledge.LogicalID, SemanticHash: knowledge.Base},
		Column: AVColumnBinding{
			Direction: EdgeDirectionOutgoing, EdgeSchema: ReplacementSchema,
		},
		Edge: AVEdgePatch{
			Action: AVEdgeAdd,
			Edge: TypedEdge{
				ID: "edge-flow-replacement", Schema: ReplacementSchema,
				Target: EntityRef{EntityType: EntityClaim, LogicalID: "c-main-opinion"},
				Type:   "replaced_by", Reason: "Human review selected the validated replacement.",
				CreatedAt: "2026-10-12T21:12:00+08:00",
				UpdatedAt: "2026-10-12T21:12:00+08:00",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	replaced, err := service.Apply(ctx, user, ApplyRequest{Operation: replacement})
	if err != nil {
		t.Fatal(err)
	}
	retried, err := service.Apply(ctx, user, ApplyRequest{Operation: replacement})
	if err != nil {
		t.Fatal(err)
	}
	if replaced.GitCommit != retried.GitCommit {
		t.Fatalf("replacement retry created a second commit: first=%+v retry=%+v", replaced, retried)
	}

	provider.DropDerived()
	claims, err := provider.Query(ctx, ClaimsViewID, AVQuery{})
	if err != nil {
		t.Fatal(err)
	}
	knowledgeRow := rowByID(t, claims.Rows, "c-main-knowledge")
	opinionRow := rowByID(t, claims.Rows, "c-main-opinion")
	if opinionRow.Cells["validation"].Text != "validated" ||
		opinionRow.Cells["arguments_incoming"].EdgeCount != 1 {
		t.Fatalf("final Opinion projection = %+v", opinionRow.Cells)
	}
	finalKnowledge, err := service.InspectClaim(ctx, knowledgeRow.Ref.LogicalID)
	if err != nil {
		t.Fatal(err)
	}
	var hasArgument, hasReplacement bool
	for _, edge := range finalKnowledge.Envelope.Relations.Outgoing {
		hasArgument = hasArgument || edge.ID == "edge-flow-argument"
		hasReplacement = hasReplacement || edge.ID == "edge-flow-replacement"
	}
	if !hasArgument || !hasReplacement {
		t.Fatalf("final relation set = %+v", finalKnowledge.Envelope.Relations.Outgoing)
	}
}
