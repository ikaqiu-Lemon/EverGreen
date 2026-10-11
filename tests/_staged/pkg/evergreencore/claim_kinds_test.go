package evergreencore

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func newThreeKindService(t *testing.T) (*AuthorityService, *FilesystemAuthority, *Registry, string) {
	t.Helper()
	root := t.TempDir()
	registry := ApplicationRegistry()
	provenance := []Provenance{{SourceID: "s-one", NoteID: "n-one", SegmentRefs: []LogicalID{"seg-one"}, Reason: "Evidence"}}
	knowledge := claimAVFixture(t, "k-alpha", KnowledgeKind, "Knowledge", "active", json.RawMessage(`{}`), nil, provenance, []TypedEdge{
		{ID: "edge-k-o", Schema: ArgumentSchema, Target: EntityRef{EntityType: EntityClaim, LogicalID: "o-beta"}, Type: "supports", Reason: "Supports"},
		{ID: "edge-k-h", Schema: ArgumentSchema, Target: EntityRef{EntityType: EntityClaim, LogicalID: "c-hypothesis"}, Type: "limits", Reason: "Boundary"},
	})
	opinion := claimAVFixture(t, "o-beta", OpinionKind, "Opinion", "active",
		json.RawMessage(`{"validation":{"status":"pending","method":"review","future":{"preserve":true}}}`), nil, provenance, []TypedEdge{
			{ID: "edge-o-h", Schema: ArgumentSchema, Target: EntityRef{EntityType: EntityClaim, LogicalID: "c-hypothesis"}, Type: "supports", Reason: "Motivation"},
		})
	hypothesis := syFixture("c-hypothesis", "Hypothesis")
	document, err := DecodeSY(hypothesis, registry)
	if err != nil {
		t.Fatal(err)
	}
	document.Envelope.Claim = &Claim{
		ClaimKind: HypothesisKind, KindSchema: HypothesisKindSchema, Status: "proposed",
		KindData: json.RawMessage(`{"statement":"CAS avoids lost updates","test_method":"Run concurrent writes","future":{"preserve":true}}`),
	}
	document.Envelope.Provenance = provenance
	document.Envelope.Relations.Outgoing = []TypedEdge{
		{ID: "edge-h-k", Schema: ArgumentSchema, Target: EntityRef{EntityType: EntityClaim, LogicalID: "k-alpha"}, Type: "derives", Reason: "Prediction"},
	}
	hypothesis, err = EncodeSY(hypothesis, document.Envelope, registry)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, "box/knowledge.sy", knowledge)
	writeFile(t, root, "box/opinion.sy", opinion)
	writeFile(t, root, "box/hypothesis.sy", hypothesis)
	authority, err := NewFilesystemAuthority(FilesystemAuthorityOptions{Root: root, Registry: registry, Committer: newRecordingCommitter()})
	if err != nil {
		t.Fatal(err)
	}
	return NewAuthorityService(authority, allowAllAuthorizer{}, registry), authority, registry, root
}

func TestThreeKindsViewsShareAuthorityAndCrossKindGraph(t *testing.T) {
	service, authority, registry, _ := newThreeKindService(t)
	provider := NewClaimsAVProvider(authority, service, registry)
	ctx := context.Background()
	for view, ids := range map[string][]LogicalID{
		ClaimsViewID:    {"c-hypothesis", "k-alpha", "o-beta"},
		KnowledgeViewID: {"k-alpha"}, OpinionViewID: {"o-beta"}, "hypothesis": {"c-hypothesis"},
	} {
		page, err := provider.Query(ctx, view, AVQuery{})
		if err != nil {
			t.Fatal(err)
		}
		assertRowIDs(t, page.Rows, ids...)
		for _, row := range page.Rows {
			snapshot, err := service.InspectClaim(ctx, row.Ref.LogicalID)
			if err != nil || snapshot.Base != row.Ref.SemanticHash {
				t.Fatalf("view %s authority mismatch: %v", view, err)
			}
		}
	}
	page, err := provider.Query(ctx, "hypothesis", AVQuery{Filters: []AVFilter{
		{Column: "test_method", Operator: "contains", Value: "concurrent"},
	}, GroupBy: "status"})
	if err != nil || page.Total != 1 || page.GroupCounts["proposed"] != 1 {
		t.Fatalf("kind fields query: %+v %v", page, err)
	}
	row := page.Rows[0]
	if row.Cells["arguments_incoming"].EdgeCount != 2 || row.Cells["arguments_outgoing"].EdgeCount != 1 {
		t.Fatalf("cross-kind graph: %+v", row.Cells)
	}
	before, _ := provider.Rebuild(ctx)
	provider.DropDerived()
	after, err := provider.Rebuild(ctx)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("graph rebuild differs: %+v %+v %v", before, after, err)
	}
}

func TestKindEditorRejectsInvalidDataAndPreservesUnknownFields(t *testing.T) {
	service, _, _, _ := newThreeKindService(t)
	ctx := context.Background()
	user := Principal{Type: PrincipalUser, ID: "user"}
	snapshot, _ := service.InspectClaim(ctx, "c-hypothesis")
	for _, edit := range []ClaimEdit{
		{KindData: json.RawMessage(`{"test_method":""}`)},
		{KindData: json.RawMessage(`{"statement":17}`)},
		{Status: stringPointer("active")},
	} {
		_, err := service.PlanClaimEdit(ctx, user, ClaimEditRequest{
			OperationID: "op-invalid-kind", LogicalID: snapshot.LogicalID, Base: snapshot.Base, Edit: edit,
		})
		if !HasDiagnostic(err, CodeInvalidClaim) {
			t.Fatalf("invalid kind editor: %v", err)
		}
	}
	planned, err := service.PlanClaimEdit(ctx, user, ClaimEditRequest{
		OperationID: "op-valid-kind", LogicalID: snapshot.LogicalID, Base: snapshot.Base,
		Edit: ClaimEdit{KindData: json.RawMessage(`{"test_method":"Race the writers"}`), Status: stringPointer("testing")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Apply(ctx, user, ApplyRequest{Operation: planned}); err != nil {
		t.Fatal(err)
	}
	updated, _ := service.InspectClaim(ctx, snapshot.LogicalID)
	if !bytes.Contains(updated.Envelope.Claim.KindData, []byte(`"preserve":true`)) ||
		updated.Envelope.Claim.Status != "testing" {
		t.Fatalf("schema editor dropped unknown data: %s", updated.Envelope.Claim.KindData)
	}
}

func TestValidationAuthorizationCannotBeBypassedByAfterImage(t *testing.T) {
	service, authority, registry, root := newThreeKindService(t)
	ctx := context.Background()
	raw := readFile(t, root, "box/opinion.sy")
	document, _ := DecodeSY(raw, registry)
	document.Envelope.Claim.KindData = json.RawMessage(`{"validation":{"status":"validated"}}`)
	after, _ := EncodeSY(raw, document.Envelope, registry)
	hash, _ := SemanticHash(raw)
	request := PlanRequest{
		Protocol: ChangePlanProtocol, OperationID: "op-forged-validation", Command: "claim.update",
		Base:   []BaseRef{{LogicalID: "o-beta", SemanticHash: hash}},
		Writes: []WriteInput{{LogicalID: "o-beta", After: after}},
	}
	agent := Principal{Type: PrincipalAgent, ID: "agent", AuthSource: "fixture", RequestReason: "Test permissions"}
	if _, err := service.Plan(ctx, agent, request); !HasDiagnostic(err, CodeUnauthorized) {
		t.Fatalf("raw agent plan bypassed validation authorization: %v", err)
	}
	user := Principal{Type: PrincipalUser, ID: "user"}
	planned, err := service.Plan(ctx, user, request)
	if err != nil {
		t.Fatal(err)
	}
	planned.Plan.Principal = agent
	planned.PlanHash = ComputePlanHash(planned.Plan, planned.Images)
	if _, err = service.Apply(ctx, agent, ApplyRequest{Operation: planned}); !HasDiagnostic(err, CodeUnauthorized) {
		t.Fatalf("forged apply bypassed validation authorization: %v", err)
	}
	actual, _ := authority.Load(ctx, "o-beta")
	if !bytes.Equal(raw, actual) {
		t.Fatal("denied validation changed authority")
	}
	if _, err = service.Apply(ctx, user, ApplyRequest{Operation: mustUserPlan(t, service, request)}); err != nil {
		t.Fatal(err)
	}
}

func mustUserPlan(t *testing.T, service *AuthorityService, request PlanRequest) PlannedOperation {
	t.Helper()
	planned, err := service.Plan(context.Background(), Principal{Type: PrincipalUser, ID: "user"}, request)
	if err != nil {
		t.Fatal(err)
	}
	return planned
}

func TestMixedKindBulkIntersectionIsAtomic(t *testing.T) {
	service, authority, registry, root := newThreeKindService(t)
	ctx := context.Background()
	user := Principal{Type: PrincipalUser, ID: "user"}
	provider := NewClaimsAVProvider(authority, service, registry)
	page, _ := provider.Query(ctx, ClaimsViewID, AVQuery{})
	rows := []AVRowRef{}
	for _, row := range page.Rows {
		rows = append(rows, row.Ref)
	}
	capabilities, err := service.BulkCapabilities(ctx, user, rows)
	if err != nil || !reflect.DeepEqual(capabilities.Statuses, []string{"archived"}) {
		t.Fatalf("bulk intersection: %+v %v", capabilities, err)
	}
	before := readFile(t, root, "box/hypothesis.sy")
	if _, err = service.PlanBulkClaims(ctx, user, BulkClaimRequest{
		OperationID: "op-invalid-bulk", Rows: rows, Edit: ClaimEdit{Status: stringPointer("active")},
	}); !HasDiagnostic(err, CodeBulkCapability) {
		t.Fatalf("inapplicable bulk status: %v", err)
	}
	if !bytes.Equal(before, readFile(t, root, "box/hypothesis.sy")) {
		t.Fatal("rejected bulk wrote a partial state")
	}
	tags := []string{"verified"}
	planned, err := service.PlanBulkClaims(ctx, user, BulkClaimRequest{
		OperationID: "op-bulk", Rows: rows, Edit: ClaimEdit{Status: stringPointer("archived"), Tags: &tags},
	})
	if err != nil || len(planned.Images) != 3 {
		t.Fatalf("bulk plan: %+v %v", planned, err)
	}
	if _, err = service.Apply(ctx, user, ApplyRequest{Operation: planned}); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		snapshot, _ := service.InspectClaim(ctx, row.LogicalID)
		if snapshot.Envelope.Claim.Status != "archived" || !reflect.DeepEqual(snapshot.Envelope.Claim.Tags, tags) {
			t.Fatalf("partial bulk: %+v", snapshot)
		}
	}
}

func TestRemovedKindRemainsLosslessQueryableAndFailsAllWrites(t *testing.T) {
	service, authority, registry, root := newThreeKindService(t)
	ctx := context.Background()
	provider := NewClaimsAVProvider(authority, service, registry)
	_, _ = provider.Query(ctx, ClaimsViewID, AVQuery{})
	before := readFile(t, root, "box/hypothesis.sy")
	registry.Unregister(HypothesisKind)
	snapshot, err := service.InspectClaim(ctx, "c-hypothesis")
	if err != nil || !snapshot.ReadOnly || !bytes.Contains(snapshot.Envelope.Claim.KindData, []byte(`"preserve":true`)) {
		t.Fatalf("unavailable kind snapshot: %+v %v", snapshot, err)
	}
	page, err := provider.Query(ctx, ClaimsViewID, AVQuery{})
	if err != nil || !rowByID(t, page.Rows, "c-hypothesis").ReadOnly {
		t.Fatalf("unavailable kind omitted from Claims: %+v %v", page, err)
	}
	if _, err = provider.Query(ctx, "hypothesis", AVQuery{}); !HasDiagnostic(err, CodeClaimKindUnavailable) {
		t.Fatalf("unavailable dedicated view: %v", err)
	}
	user := Principal{Type: PrincipalUser, ID: "user"}
	tags := []string{"attempt"}
	if _, err = service.PlanClaimEdit(ctx, user, ClaimEditRequest{
		OperationID: "op-unavailable", LogicalID: snapshot.LogicalID, Base: snapshot.Base, Edit: ClaimEdit{Tags: &tags},
	}); !HasDiagnostic(err, CodeClaimKindUnavailable) {
		t.Fatalf("unavailable kind write: %v", err)
	}
	if !bytes.Equal(before, readFile(t, root, "box/hypothesis.sy")) {
		t.Fatal("unavailable kind lost original bytes")
	}
}

func TestHypothesisCreationAndDescriptorMaterialization(t *testing.T) {
	service, _, registry, root := newThreeKindService(t)
	ctx := context.Background()
	user := Principal{Type: PrincipalUser, ID: "user"}
	provenance := []Provenance{{SourceID: "s-one", NoteID: "n-one", SegmentRefs: []LogicalID{"seg-one"}, Reason: "Evidence"}}
	request := ClaimCreateRequest{
		OperationID: "op-create-hypothesis", LogicalID: "c-new", DocumentID: "20261010140000-new0001",
		Path: "box/new.sy", Title: "New hypothesis", Kind: HypothesisKind,
		Body:     json.RawMessage(`[{"ID":"20261010140001-new0002","Type":"NodeParagraph","Data":"Prediction"}]`),
		KindData: json.RawMessage(`{"statement":"Prediction","test_method":"Measure"}`), Provenance: provenance,
	}
	created, err := service.PlanClaimCreate(ctx, user, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Apply(ctx, user, ApplyRequest{Operation: created}); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := service.InspectClaim(ctx, "c-new")
	if snapshot.Envelope.Claim.Status != "proposed" {
		t.Fatalf("hypothesis initial status: %+v", snapshot)
	}
	note := bytes.ReplaceAll(reviewSYFixture(t), []byte(`"knowledge"`), []byte(`"hypothesis"`))
	note = bytes.ReplaceAll(note, []byte(KnowledgeKindSchema), []byte(HypothesisKindSchema))
	writeFile(t, root, "box/note.sy", note)
	base, _ := SemanticHash(note)
	materialize := ReviewMaterializeRequest{
		OperationID: "op-hypothesis-materialize", NoteID: "n-review", CandidateID: "cand-review",
		NoteBase: base, ClaimID: "c-materialized", ClaimPath: "box/materialized.sy",
		ClaimDocumentID: "20261010150000-mat0001", CreatedAt: "2026-10-10T15:00:00Z",
		KindData: json.RawMessage(`{"statement":"Candidate prediction","test_method":"Run fault injection"}`),
	}
	first, err := service.PlanReviewMaterialization(ctx, user, materialize)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.PlanReviewMaterialization(ctx, user, materialize)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("kind materializer not deterministic: %v", err)
	}
	if _, err = service.Apply(ctx, user, ApplyRequest{Operation: first.Operation}); err != nil {
		t.Fatal(err)
	}
	raw := readFile(t, root, "box/materialized.sy")
	document, err := DecodeSY(raw, registry)
	if err != nil || document.Envelope.Claim.Status != "proposed" || !bytes.Contains(raw, []byte("Candidate prediction")) {
		t.Fatalf("descriptor materialization: %+v %v", document, err)
	}
}

func stringPointer(value string) *string { return &value }

func TestOpinionLifecycleAndRegistryCatalogRemainStable(t *testing.T) {
	registry := DefaultRegistry()
	descriptor, _ := registry.Descriptor(OpinionKind)
	descriptor.AllowedStatuses[0] = "wrong"
	delete(descriptor.KindDataSchema.Properties, "validation")
	catalog := CurrentSchemas(registry)
	catalog.Kinds[1].KindDataSchema.Properties["validation"] = JSONSchema{Type: "number"}
	stored, _ := registry.Descriptor(OpinionKind)
	if stored.AllowedStatuses[0] != "active" || stored.KindDataSchema.Properties["validation"].Type != "object" {
		t.Fatal("descriptor catalog mutation changed authoritative rules")
	}
	for _, transition := range []struct {
		from, to string
		allowed  bool
	}{
		{"pending", "validated", true}, {"pending", "rejected", true},
		{"validated", "rejected", true}, {"validated", "pending", true},
		{"rejected", "pending", true}, {"rejected", "validated", false},
	} {
		before := &DocumentEnvelope{
			Entity: Entity{LogicalID: "o-one", EntityType: EntityClaim},
			Claim: &Claim{ClaimKind: OpinionKind, KindSchema: OpinionKindSchema, Status: "active",
				KindData: json.RawMessage(`{"validation":{"status":"` + transition.from + `"}}`)},
		}
		after := *before
		after.Claim = &Claim{ClaimKind: OpinionKind, KindSchema: OpinionKindSchema, Status: "active",
			KindData: json.RawMessage(`{"validation":{"status":"` + transition.to + `"}}`)}
		err := AuthorizeClaimChange(Principal{Type: PrincipalUser, ID: "user"}, before, &after, registry)
		if (err == nil) != transition.allowed {
			t.Fatalf("validation %s -> %s: %v", transition.from, transition.to, err)
		}
		if err = AuthorizeClaimChange(Principal{Type: PrincipalAgent, ID: "agent"}, before, &after, registry); !HasDiagnostic(err, CodeUnauthorized) {
			t.Fatalf("agent validation %s -> %s: %v", transition.from, transition.to, err)
		}
	}
}

func TestRematerializationPreservesKnowledgeAndOpinionLifecycle(t *testing.T) {
	for _, kind := range []ClaimKind{KnowledgeKind, OpinionKind} {
		t.Run(string(kind), func(t *testing.T) {
			registry := DefaultRegistry()
			root := t.TempDir()
			note := reviewSYFixture(t)
			if kind == OpinionKind {
				note = bytes.ReplaceAll(note, []byte(`"knowledge"`), []byte(`"opinion"`))
				note = bytes.ReplaceAll(note, []byte(KnowledgeKindSchema), []byte(OpinionKindSchema))
			}
			writeFile(t, root, "box/note.sy", note)
			schema := KnowledgeKindSchema
			data := json.RawMessage(`{"future":{"preserve":true}}`)
			if kind == OpinionKind {
				schema = OpinionKindSchema
				data = json.RawMessage(`{"validation":{"status":"validated","evidence":[{"id":"edge-one"}]},"future":{"preserve":true}}`)
			}
			claim := syFixture("c-existing", "Existing")
			document, _ := DecodeSY(claim, registry)
			document.Envelope.Claim = &Claim{ClaimKind: kind, KindSchema: schema, Status: "deprecated", KindData: data}
			document.Envelope.Extension = RawObject{"example/v1": json.RawMessage(`{"kept":true}`)}
			claim, err := EncodeSY(claim, document.Envelope, registry)
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, root, "box/claim.sy", claim)
			authority, _ := NewFilesystemAuthority(FilesystemAuthorityOptions{Root: root, Registry: registry, Committer: newRecordingCommitter()})
			service := NewAuthorityService(authority, allowAllAuthorizer{}, registry)
			noteBase, _ := SemanticHash(note)
			claimBase, _ := SemanticHash(claim)
			principal := Principal{Type: PrincipalUser, ID: "user"}
			planned, err := service.PlanReviewMaterialization(context.Background(), principal, ReviewMaterializeRequest{
				OperationID: "op-rematerialize", NoteID: "n-review", CandidateID: "cand-review",
				NoteBase: noteBase, ClaimID: "c-existing", ClaimBase: claimBase, CreatedAt: "2026-10-10T15:00:00Z",
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = service.Apply(context.Background(), principal, ApplyRequest{Operation: planned.Operation}); err != nil {
				t.Fatal(err)
			}
			updated, _ := service.InspectClaim(context.Background(), "c-existing")
			if updated.Envelope.Claim.Status != "deprecated" ||
				!semanticValuesEqual(updated.Envelope.Claim.KindData, data) ||
				!reflect.DeepEqual(updated.Envelope.Extension, document.Envelope.Extension) {
				t.Fatalf("rematerialization lost lifecycle or extensions: %+v", updated.Envelope)
			}
		})
	}
}
