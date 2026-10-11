package evergreencore

import (
	"context"
	"encoding/json"
	"testing"
)

func TestNativeEditorAdmissionCannotBeSerializedOrSelfCertified(t *testing.T) {
	before := sectionClaimFixture(t)
	node, _ := parseReviewNode(before)
	node.children[7].object["Data"] = json.RawMessage(`"User edited their notes"`)
	edited, _ := node.encode()
	after, err := PrepareNativeDocumentEdit(before, edited, nil)
	if err != nil {
		t.Fatal(err)
	}
	service, host, principal, request, attack := operationAttackFixture(t, PrincipalUser, NativeEditCommand,
		[][]byte{before}, [][]byte{after})
	if _, err = service.Plan(context.Background(), principal, request); !HasDiagnostic(err, CodeUnauthorized) {
		t.Fatalf("self-certified command was admitted: %v", err)
	}
	principal.AuthSource = "workspace-session"
	requestJSON, _ := json.Marshal(request)
	requestJSON = append(requestJSON[:len(requestJSON)-1], []byte(`,"nativeEditor":true,"native_editor":true}`)...)
	if err = json.Unmarshal(requestJSON, &request); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Plan(context.Background(), principal, request); !HasDiagnostic(err, CodeUnauthorized) {
		t.Fatalf("request JSON created admission: %v", err)
	}
	attack.Plan.Principal = principal
	attack.PlanHash = ComputePlanHash(attack.Plan, attack.Images)
	if _, err = service.Apply(context.Background(), principal, ApplyRequest{Operation: attack}); !HasDiagnostic(err, CodeUnauthorized) {
		t.Fatalf("raw plan self-certified native editing: %v", err)
	}
	planned, err := service.PlanNativeEdit(context.Background(), principal, request)
	if err != nil {
		t.Fatal(err)
	}
	serialized, _ := json.Marshal(planned)
	var transported PlannedOperation
	if err = json.Unmarshal(serialized, &transported); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Apply(context.Background(), principal, ApplyRequest{Operation: transported}); !HasDiagnostic(err, CodeUnauthorized) {
		t.Fatalf("serialized plan retained host admission: %v", err)
	}
	assertFileEquals(t, host.Root(), planned.Images[0].Path, before)
	for range 2 {
		record, err := service.Apply(context.Background(), principal, ApplyRequest{Operation: planned})
		if err != nil || record.State != OperationCompleted {
			t.Fatalf("trusted native apply or retry: %+v %v", record, err)
		}
	}
	assertFileEquals(t, host.Root(), planned.Images[0].Path, after)
	if got := gitCommand(t, host.Root(), "rev-list", "--count", "HEAD"); got != "2\n" {
		t.Fatalf("native edit was not a single commit: %s", got)
	}
}

func TestNativeEditorReviewRefreshAndRecoveryShareAuthorityValidation(t *testing.T) {
	before := reviewSYFixture(t)
	tree, _ := parseReviewNode(before)
	tree.children = append(tree.children, plainAuthorityNode("user-note", "NodeParagraph", "User original"))
	before, _ = tree.encode()
	tree.children[len(tree.children)-1].object["Data"] = json.RawMessage(`"User edited"`)
	tree.children[0].object["Data"] = json.RawMessage(`"Changed source segment"`)
	edited, _ := tree.encode()
	after, err := PrepareNativeDocumentEdit(before, edited, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := InspectNoteReview(after, nil)
	if err != nil || len(snapshot.Lineage) != 1 || snapshot.Candidates[0].Metadata.State != "stale" {
		t.Fatalf("native edit lost deterministic stale/lineage: %+v %v", snapshot, err)
	}
	service, host, principal, request, _ := operationAttackFixture(t, PrincipalUser, NativeEditCommand,
		[][]byte{before}, [][]byte{after})
	planned, err := service.PlanNativeEdit(context.Background(), principal, request)
	if err != nil {
		t.Fatal(err)
	}
	host.faults = &oneShotFault{point: FaultAfterPrepared, index: -1}
	if _, err = service.Apply(context.Background(), principal, ApplyRequest{Operation: planned}); err == nil {
		t.Fatal("prepare fault did not stop native edit")
	}
	assertFileEquals(t, host.Root(), planned.Images[0].Path, before)
	reopened, err := NewFilesystemAuthority(FilesystemAuthorityOptions{Root: host.Root(), Committer: host.committer})
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := NewAuthorityService(reopened, CommandPolicy{}, nil).Recover(context.Background())
	if err != nil || len(recovered) != 1 || recovered[0].State != OperationCompleted {
		t.Fatalf("native recovery could not validate journal admission: %+v %v", recovered, err)
	}
	assertFileEquals(t, host.Root(), planned.Images[0].Path, after)
}

func TestNativeEditorRejectsSourceAgentAndMetadataAcrossCompleteWriteSet(t *testing.T) {
	before := sectionClaimFixture(t)
	valid, err := PrepareNativeDocumentEdit(before, mutateSYFixture(t, before, "User change"), nil)
	if err != nil {
		t.Fatal(err)
	}
	service, host, principal, request, _ := operationAttackFixture(t, PrincipalUser, NativeEditCommand,
		[][]byte{before}, [][]byte{valid})
	agent := principal
	agent.Type = PrincipalAgent
	if _, err = service.PlanNativeEdit(context.Background(), agent, request); !HasDiagnostic(err, CodeUnauthorized) {
		t.Fatalf("Agent got native admission: %v", err)
	}
	for _, mutation := range []func(*DocumentEnvelope){
		func(e *DocumentEnvelope) { e.Claim.Tags = []string{"forged"} },
		func(e *DocumentEnvelope) { e.Entity.SemanticRevision += 7 },
		func(e *DocumentEnvelope) {
			e.Provenance = []Provenance{{SourceID: "s-forged", NoteID: "n-forged",
				SegmentRefs: []LogicalID{"seg-forged"}, Reason: "forged"}}
		},
	} {
		document, _ := DecodeSY(valid, nil)
		mutation(document.Envelope)
		invalid, err := EncodeSY(valid, document.Envelope, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Writes[0].After = invalid
		if _, err = service.PlanNativeEdit(context.Background(), principal, request); !HasDiagnostic(err, CodeUnauthorized) {
			t.Fatalf("native metadata change admitted: %v", err)
		}
		_, _, _, _, attack := operationAttackFixture(t, PrincipalUser, NativeEditCommand,
			[][]byte{before}, [][]byte{invalid})
		attack.nativeEditor = true
		attack.Plan.Principal = principal
		attack.PlanHash = ComputePlanHash(attack.Plan, attack.Images)
		if _, err = service.Apply(context.Background(), principal, ApplyRequest{Operation: attack}); !HasDiagnostic(err, CodeUnauthorized) {
			t.Fatalf("native raw apply metadata change admitted: %v", err)
		}
		assertFileEquals(t, host.Root(), request.Writes[0].Path, before)
	}
	source, err := NewMarkdownDocument("s-a", "Source", []byte("Original source"), &DocumentEnvelope{
		Spec: DocumentSpec, Entity: Entity{LogicalID: "s-a", EntityType: EntitySource, Schema: "evergreen.source/v1"},
		Relations: Relations{Outgoing: []TypedEdge{}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = PrepareNativeDocumentEdit(source, source, nil); !HasDiagnostic(err, CodeUnauthorized) {
		t.Fatalf("Source native edit admitted: %v", err)
	}
	service, host, principal, request, attack := operationAttackFixture(t, PrincipalUser, NativeEditCommand,
		[][]byte{before, source}, [][]byte{valid, source})
	if _, err = service.PlanNativeEdit(context.Background(), principal, request); !HasDiagnostic(err, CodeUnauthorized) {
		t.Fatalf("mixed invalid write set admitted: %v", err)
	}
	attack.nativeEditor = true
	if _, err = service.Apply(context.Background(), principal, ApplyRequest{Operation: attack}); !HasDiagnostic(err, CodeUnauthorized) {
		t.Fatalf("mixed invalid raw write set admitted: %v", err)
	}
	assertFileEquals(t, host.Root(), "box/c-a.sy", before)
	assertFileEquals(t, host.Root(), "box/s-a.sy", source)
	if got := gitCommand(t, host.Root(), "rev-list", "--count", "HEAD"); got != "1\n" {
		t.Fatalf("failed preflight committed a subset: %s", got)
	}
}
