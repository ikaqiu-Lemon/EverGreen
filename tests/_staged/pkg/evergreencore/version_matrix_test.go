package evergreencore

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"testing/fstest"
)

func TestHigherRegisteredKindSchemaIsLosslessReadOnly(t *testing.T) {
	supported := claimAVFixture(
		t, "c-kind-supported", KnowledgeKind, "Supported kind", "active",
		json.RawMessage(`{}`), nil, nil, nil)
	future := claimAVFixture(
		t, "c-kind-future", KnowledgeKind, "Future kind", "active",
		json.RawMessage(`{"future":{"large":9007199254740993,"nested":[null,{"kept":true}]}}`),
		nil, nil, nil)
	root, err := decodeRawObject(future)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := decodeRawObject(root[evergreenJSONKey])
	if err != nil {
		t.Fatal(err)
	}
	claim, err := decodeRawObject(envelope["claim"])
	if err != nil {
		t.Fatal(err)
	}
	claim["kind_schema"] = json.RawMessage(`"evergreen.claim-kind.knowledge/v99"`)
	envelope["claim"], _ = json.Marshal(claim)
	root[evergreenJSONKey], _ = json.Marshal(envelope)
	future, _ = json.Marshal(root)

	document, err := DecodeSY(future, DefaultRegistry())
	if err != nil {
		t.Fatalf("higher registered kind schema blocked read: %v", err)
	}
	if document.Envelope == nil || !document.ReadOnly ||
		!hasDiagnostic(document.Diagnostics, CodeSchemaTooNew) {
		t.Fatalf("higher registered kind schema = %+v", document)
	}
	if !semanticValuesEqual(
		document.Envelope.Claim.KindData,
		json.RawMessage(`{"future":{"large":9007199254740993,"nested":[null,{"kept":true}]}}`),
	) {
		t.Fatalf("future kind_data changed: %s", document.Envelope.Claim.KindData)
	}

	index, err := ScanFS(fstest.MapFS{
		"claims/supported.sy": &fstest.MapFile{Data: supported, Mode: 0o644},
		"claims/future.sy":    &fstest.MapFile{Data: future, Mode: 0o644},
	}, DefaultRegistry())
	if err != nil {
		t.Fatalf("mixed kind schema scan failed: %v", err)
	}
	if _, ok := index.Locations["c-kind-future"]; !ok {
		t.Fatalf("future kind logical location missing: %+v", index.Locations)
	}

	repository := documentMapRepository{
		"c-kind-supported": supported,
		"c-kind-future":    future,
	}
	provider := NewClaimsAVProvider(repository, staticPlanner{}, DefaultRegistry())
	page, err := provider.Query(context.Background(), ClaimsViewID, AVQuery{})
	if err != nil {
		t.Fatalf("mixed kind Claims query failed: %v", err)
	}
	knowledge, err := provider.Query(context.Background(), KnowledgeViewID, AVQuery{})
	if err != nil {
		t.Fatalf("mixed kind Knowledge query failed: %v", err)
	}
	if page.Total != 2 || knowledge.Total != 2 {
		t.Fatalf("mixed kind totals: claims=%d knowledge=%d", page.Total, knowledge.Total)
	}
	futureRow := rowByID(t, page.Rows, "c-kind-future")
	if !futureRow.ReadOnly || futureRow.Cells["claim_kind"].Text != "knowledge" ||
		!hasDiagnostic(futureRow.Diagnostics, CodeSchemaTooNew) {
		t.Fatalf("future kind row = %+v", futureRow)
	}
	_, err = provider.PlanPatch(context.Background(), Principal{Type: PrincipalUser, ID: "owner"}, AVPatch{
		OperationID: "op-future-kind", RowID: futureRow.Ref.LogicalID,
		Base: BaseRef{
			LogicalID: futureRow.Ref.LogicalID, SemanticHash: futureRow.Ref.SemanticHash,
		},
		Column: AVColumnBinding{Field: "claim.status"},
		Value:  &AVCellValue{Kind: "text", Text: "archived"},
	})
	if !HasDiagnostic(err, CodeSchemaTooNew) {
		t.Fatalf("future kind patch = %v", err)
	}
	after, err := repository.Load(context.Background(), "c-kind-future")
	if err != nil || !bytes.Equal(after, future) {
		t.Fatalf("future kind authority changed: err=%v", err)
	}
}

func TestHigherCandidateKindSchemaKeepsWorkspaceReadable(t *testing.T) {
	future := reviewSYFixture(t)
	tree, err := parseReviewTree(future, DefaultRegistry())
	if err != nil {
		t.Fatal(err)
	}
	tree.candidates[0].envelope.Candidate.KindSchema = "evergreen.claim-kind.knowledge/v99"
	if err = tree.candidates[0].writeEnvelope(); err != nil {
		t.Fatal(err)
	}
	future, err = tree.encode()
	if err != nil {
		t.Fatal(err)
	}

	document, err := DecodeSY(future, DefaultRegistry())
	if err != nil {
		t.Fatalf("higher Candidate kind schema blocked document read: %v", err)
	}
	if !document.ReadOnly || !hasDiagnostic(document.Diagnostics, CodeSchemaTooNew) {
		t.Fatalf("higher Candidate kind schema document = %+v", document)
	}
	index, err := ScanFS(fstest.MapFS{
		"notes/future.sy": &fstest.MapFile{Data: future, Mode: 0o644},
	}, DefaultRegistry())
	if err != nil {
		t.Fatalf("higher Candidate kind schema blocked workspace scan: %v", err)
	}
	if _, ok := index.Locations["n-review"]; !ok {
		t.Fatalf("future Note identity missing: %+v", index.Locations)
	}
	if _, ok := index.Locations["cand-review"]; !ok {
		t.Fatalf("future Candidate identity missing: %+v", index.Locations)
	}
	if _, err = InspectNoteReview(future, DefaultRegistry()); !HasDiagnostic(err, CodeSchemaTooNew) {
		t.Fatalf("future Candidate review error = %v", err)
	}
}

func TestDifferentKindSchemaFamilyRemainsInvalid(t *testing.T) {
	raw := claimAVFixture(
		t, "c-kind-invalid", KnowledgeKind, "Invalid family", "active",
		json.RawMessage(`{}`), nil, nil, nil)
	root, _ := decodeRawObject(raw)
	envelope, _ := decodeRawObject(root[evergreenJSONKey])
	claim, _ := decodeRawObject(envelope["claim"])
	claim["kind_schema"] = json.RawMessage(`"example.claim-kind.knowledge/v99"`)
	envelope["claim"], _ = json.Marshal(claim)
	root[evergreenJSONKey], _ = json.Marshal(envelope)
	raw, _ = json.Marshal(root)

	if _, err := DecodeSY(raw, DefaultRegistry()); !HasDiagnostic(err, CodeInvalidClaim) ||
		HasDiagnostic(err, CodeSchemaTooNew) {
		t.Fatalf("different schema family error = %v", err)
	}
}
