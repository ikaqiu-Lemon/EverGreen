package evergreencore

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNoteCreateBuildsCanonicalReviewFromExistingSource(t *testing.T) {
	service, host := sourceCaptureService(t)
	principal := noteCreatePrincipal()
	sourceRaw := createNoteSource(t, service, host, principal)
	request := noteCreateRequest("op-note-create", "n-created", "20261012180000-note001", "box/note.sy")

	first, err := service.PlanNoteCreate(context.Background(), principal, request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.PlanNoteCreate(context.Background(), principal, request)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("Note create plan is not deterministic")
	}
	if first.Operation.Plan.Command != NoteCreateCommand ||
		first.NoteID != request.LogicalID ||
		first.SourceID != request.SourceID ||
		first.Snapshot.Summary.SegmentCount != 2 ||
		first.Snapshot.Summary.CandidateCount != 1 ||
		first.Snapshot.Summary.CoveredSegments != 2 ||
		!first.Snapshot.CanMaterialize {
		t.Fatalf("planned Note = %+v", first)
	}
	for range 2 {
		if _, err = service.Apply(context.Background(), principal,
			ApplyRequest{Operation: first.Operation}); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := host.Load(context.Background(), request.LogicalID)
	if err != nil {
		t.Fatal(err)
	}
	document, err := DecodeSY(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if noteSourceID(document.Envelope) != request.SourceID ||
		document.Envelope.Entity.SemanticRevision != 1 {
		t.Fatalf("Note source binding or revision = %+v", document.Envelope)
	}
	snapshot, err := InspectNoteReview(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !semanticValuesEqual(snapshot.Coverage, request.Coverage) ||
		snapshot.Candidates[0].Metadata.State != "draft" ||
		snapshot.Candidates[0].Metadata.MaterializedClaimID != "" ||
		snapshot.Candidates[0].Metadata.PayloadHash == "" ||
		snapshot.Candidates[0].Metadata.RefHashes["seg-source"] == "" {
		t.Fatalf("committed Note review = %+v", snapshot)
	}
	afterSource, err := host.Load(context.Background(), request.SourceID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterSource, sourceRaw) {
		t.Fatal("Note create rewrote its Source")
	}
	if got := gitCommand(t, host.Root(), "rev-list", "--count", "HEAD"); got != "3\n" {
		t.Fatalf("Source + Note create commit count = %q", got)
	}
}

func TestNoteCreateRejectsMissingSourceAndInvalidReviewIdentity(t *testing.T) {
	t.Run("missing Source", func(t *testing.T) {
		service, _ := sourceCaptureService(t)
		_, err := service.PlanNoteCreate(
			context.Background(), noteCreatePrincipal(),
			noteCreateRequest("op-note-no-source", "n-created", "20261012180000-note001", "box/note.sy"))
		if !HasDiagnostic(err, CodeInvalidEnvelope) {
			t.Fatalf("missing Source error = %v", err)
		}
	})

	tests := []struct {
		name string
		code string
		edit func(*NoteCreateRequest)
	}{
		{
			name: "duplicate segment ID", code: CodeDuplicateID,
			edit: func(request *NoteCreateRequest) {
				request.Segments[1].SegmentID = request.Segments[0].SegmentID
			},
		},
		{
			name: "duplicate Candidate ID", code: CodeDuplicateID,
			edit: func(request *NoteCreateRequest) {
				request.Candidates = append(request.Candidates, request.Candidates[0])
			},
		},
		{
			name: "missing coverage", code: CodeCoverageMissing,
			edit: func(request *NoteCreateRequest) {
				request.Coverage = request.Coverage[:1]
			},
		},
		{
			name: "duplicate coverage", code: CodeCoverageDuplicate,
			edit: func(request *NoteCreateRequest) {
				request.Coverage[1].SegmentRefs = []LogicalID{"seg-source"}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, host := sourceCaptureService(t)
			principal := noteCreatePrincipal()
			createNoteSource(t, service, host, principal)
			request := cloneNoteCreateRequest(t,
				noteCreateRequest("op-note-invalid", "n-created", "20261012180000-note001", "box/note.sy"))
			test.edit(&request)
			if _, err := service.PlanNoteCreate(context.Background(), principal, request); !HasDiagnostic(err, test.code) {
				t.Fatalf("%s error = %v", test.name, err)
			}
			if _, err := os.Stat(filepath.Join(host.Root(), filepath.FromSlash(request.Path))); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("%s wrote authority: %v", test.name, err)
			}
		})
	}
}

func TestNoteCreateRejectsOccupiedLogicalPathAndPhysicalIdentity(t *testing.T) {
	for _, occupied := range []string{"logical ID", "path", "physical ID"} {
		t.Run(occupied, func(t *testing.T) {
			service, host := sourceCaptureService(t)
			principal := noteCreatePrincipal()
			createNoteSource(t, service, host, principal)
			existingRequest := noteCreateRequest(
				"op-note-existing", "n-existing", "20261012180001-note001", "box/existing.sy")
			existing, err := service.PlanNoteCreate(context.Background(), principal, existingRequest)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = service.Apply(context.Background(), principal,
				ApplyRequest{Operation: existing.Operation}); err != nil {
				t.Fatal(err)
			}

			request := noteCreateRequest(
				"op-note-occupied", "n-new", "20261012180002-note002", "box/new.sy")
			switch occupied {
			case "logical ID":
				request.LogicalID = existingRequest.LogicalID
			case "path":
				request.Path = existingRequest.Path
			case "physical ID":
				request.DocumentID = existingRequest.DocumentID
			}
			_, err = service.PlanNoteCreate(context.Background(), principal, request)
			expected := CodeBaseMismatch
			if occupied == "physical ID" {
				expected = CodeDuplicateID
			}
			if !HasDiagnostic(err, expected) {
				t.Fatalf("occupied %s error = %v", occupied, err)
			}
			if _, err = os.Stat(filepath.Join(host.Root(), "box/new.sy")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("occupied %s wrote a new Note: %v", occupied, err)
			}
		})
	}
}

func TestNoteCreateRechecksSourceAndOneNoteBindingInsideWriterGate(t *testing.T) {
	t.Run("Source removed after planning", func(t *testing.T) {
		service, host := sourceCaptureService(t)
		principal := noteCreatePrincipal()
		createNoteSource(t, service, host, principal)
		request := noteCreateRequest(
			"op-note-source-race", "n-created", "20261012180000-note001", "box/note.sy")
		planned, err := service.PlanNoteCreate(context.Background(), principal, request)
		if err != nil {
			t.Fatal(err)
		}
		sourceLocation, err := host.Resolve(context.Background(), request.SourceID)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.Remove(filepath.Join(host.Root(), filepath.FromSlash(sourceLocation.Path))); err != nil {
			t.Fatal(err)
		}
		if _, err = service.Apply(context.Background(), principal,
			ApplyRequest{Operation: planned.Operation}); !HasDiagnostic(err, CodeInvalidEnvelope) {
			t.Fatalf("missing Source at apply error = %v", err)
		}
		if _, err = os.Stat(filepath.Join(host.Root(), filepath.FromSlash(request.Path))); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("missing Source race wrote Note: %v", err)
		}
	})

	t.Run("two Notes planned for one Source", func(t *testing.T) {
		service, host := sourceCaptureService(t)
		principal := noteCreatePrincipal()
		createNoteSource(t, service, host, principal)
		leftRequest := noteCreateRequest(
			"op-note-left", "n-left", "20261012180000-note001", "box/left.sy")
		rightRequest := noteCreateRequest(
			"op-note-right", "n-right", "20261012180001-note002", "box/right.sy")
		left, err := service.PlanNoteCreate(context.Background(), principal, leftRequest)
		if err != nil {
			t.Fatal(err)
		}
		right, err := service.PlanNoteCreate(context.Background(), principal, rightRequest)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = service.Apply(context.Background(), principal,
			ApplyRequest{Operation: left.Operation}); err != nil {
			t.Fatal(err)
		}
		if _, err = service.Apply(context.Background(), principal,
			ApplyRequest{Operation: right.Operation}); !HasDiagnostic(err, CodeDuplicateID) {
			t.Fatalf("duplicate Source binding at apply error = %v", err)
		}
		if _, err = os.Stat(filepath.Join(host.Root(), filepath.FromSlash(rightRequest.Path))); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("duplicate Source binding wrote second Note: %v", err)
		}
	})
}

func TestNoteCreateAuthorizationRejectsForgedStateAndCrossCommandRawPlans(t *testing.T) {
	service, host := sourceCaptureService(t)
	principal := noteCreatePrincipal()
	createNoteSource(t, service, host, principal)
	request := noteCreateRequest(
		"op-note-forged", "n-created", "20261012180000-note001", "box/note.sy")
	planned, err := service.PlanNoteCreate(context.Background(), principal, request)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := parseReviewTree(planned.Operation.Images[0].Bytes, nil)
	if err != nil {
		t.Fatal(err)
	}
	tree.candidates[0].envelope.Candidate.State = "materialized"
	tree.candidates[0].envelope.Candidate.MaterializedClaimID = "c-forged"
	if err = tree.candidates[0].writeEnvelope(); err != nil {
		t.Fatal(err)
	}
	forged, err := tree.encode()
	if err != nil {
		t.Fatal(err)
	}
	for _, transport := range []string{"plan", "raw apply"} {
		t.Run(transport, func(t *testing.T) {
			operation := rawNoteCreateOperation(t, principal, request, forged)
			var err error
			if transport == "plan" {
				_, err = service.Plan(context.Background(), principal, PlanRequest{
					Protocol: ChangePlanProtocol, OperationID: request.OperationID,
					Command: NoteCreateCommand,
					Base:    operation.Plan.Base,
					Writes: []WriteInput{{
						LogicalID: request.LogicalID, Path: request.Path, After: forged,
					}},
				})
			} else {
				_, err = service.Apply(context.Background(), principal, ApplyRequest{Operation: operation})
			}
			if !HasDiagnostic(err, CodeUnauthorized) {
				t.Fatalf("forged materialized state accepted: %v", err)
			}
			if _, err = os.Stat(filepath.Join(host.Root(), filepath.FromSlash(request.Path))); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("forged state wrote Note: %v", err)
			}
		})
	}

	valid := planned.Operation.Images[0].Bytes
	for _, command := range []string{"claim.update", "note.review.update"} {
		assertOperationDenied(t, PrincipalAgent, command, [][]byte{nil}, [][]byte{valid})
	}
}

func noteCreatePrincipal() Principal {
	return Principal{
		Type: PrincipalAgent, ID: "note-agent",
		AuthSource: "fixture", RequestReason: "create reviewed Note",
	}
}

func createNoteSource(
	t *testing.T,
	service *AuthorityService,
	host *FilesystemAuthority,
	principal Principal,
) []byte {
	t.Helper()
	request := SourceCaptureRequest{
		OperationID: "op-note-source", LogicalID: "s-note-source",
		DocumentID: "20261012175900-src0001", Path: "box/source.sy",
		URL: "https://example.com/note-source", Title: "Note source",
		Body:   "Source paragraph used to create a reviewed Note.",
		Reason: "Prepare Note creation", Domain: "systems", Tags: []string{"note"},
		CapturedAt: "2026-10-12T17:59:00+08:00",
	}
	planned, err := service.PlanSourceCapture(context.Background(), principal, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Apply(context.Background(), principal,
		ApplyRequest{Operation: *planned.Operation}); err != nil {
		t.Fatal(err)
	}
	raw, err := host.Load(context.Background(), request.LogicalID)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func noteCreateRequest(operationID string, noteID LogicalID, documentID, path string) NoteCreateRequest {
	return NoteCreateRequest{
		OperationID: operationID, LogicalID: noteID, DocumentID: documentID,
		Path: path, SourceID: "s-note-source", Title: "Reviewed Note",
		Segments: []NoteCreateSegment{
			{
				SegmentID: "seg-source", Role: "note_segment",
				Locator: &Locator{Kind: "source-lines", Value: "L1-L1"},
				Content: json.RawMessage(`[
					{"ID":"20261012180001-seg0001","Type":"NodeParagraph","Data":"Source paragraph"}
				]`),
			},
			{
				SegmentID: "seg-annotation", Role: "agent_annotation",
				Annotation: &Annotation{Kind: "emphasis", Label: "Agent emphasis"},
				Content: json.RawMessage(`[
					{"ID":"20261012180002-seg0002","Type":"NodeParagraph","Data":"Agent distinction"}
				]`),
			},
		},
		Candidates: []NoteCreateCandidate{{
			CandidateID: "cand-created", ClaimKind: KnowledgeKind,
			Title: "Self-contained claim", LogicalSlug: "self-contained-claim",
			SegmentRefs: []LogicalID{"seg-source"}, Relation: "support",
			Reason: "The source segment directly supports this claim.",
			Tags:   []string{"review"},
			Payload: json.RawMessage(`[
				{"ID":"20261012180003-payload","Type":"NodeParagraph","Data":"A self-contained claim."}
			]`),
		}},
		Coverage: []CoverageModule{
			{
				ModuleID: "coverage-candidate", Disposition: "candidate",
				SegmentRefs: []LogicalID{"seg-source"}, CandidateID: "cand-created",
			},
			{
				ModuleID: "coverage-annotation", Disposition: "note_only",
				SegmentRefs: []LogicalID{"seg-annotation"},
				Reason:      "Agent annotation remains contextual to the Note.",
			},
		},
	}
}

func cloneNoteCreateRequest(t *testing.T, request NoteCreateRequest) NoteCreateRequest {
	t.Helper()
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var cloned NoteCreateRequest
	if err = json.Unmarshal(raw, &cloned); err != nil {
		t.Fatal(err)
	}
	return cloned
}

func rawNoteCreateOperation(
	t *testing.T,
	principal Principal,
	request NoteCreateRequest,
	after []byte,
) PlannedOperation {
	t.Helper()
	hash := semanticHashBytes(t, after)
	operation := PlannedOperation{
		Plan: ChangePlan{
			Protocol: ChangePlanProtocol, OperationID: request.OperationID,
			Principal: principal, Command: NoteCreateCommand,
			Base:   []BaseRef{{LogicalID: request.LogicalID, Expected: "absent"}},
			Writes: []PlannedWrite{{LogicalID: request.LogicalID, After: hash}},
		},
		Images: []AfterImage{{
			LogicalID: request.LogicalID, Path: request.Path, Bytes: after,
			SemanticHash: hash, ContentHash: contentHash(after), Create: true,
		}},
	}
	operation.PlanHash = ComputePlanHash(operation.Plan, operation.Images)
	return operation
}
