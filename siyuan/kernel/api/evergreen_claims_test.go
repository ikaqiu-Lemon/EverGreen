package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	evergreencore "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	kernelEvergreen "github.com/siyuan-note/siyuan/kernel/evergreen"
	"github.com/siyuan-note/siyuan/kernel/util"
)

func TestEvergreenClaimSnapshotContractPreservesUnknownEnvelopeFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	writeEvergreenAPIFixture(t, root, "box/knowledge.sy", "k-one", "Knowledge")
	path := filepath.Join(root, "box/knowledge.sy")
	raw := readEvergreenAPIFile(t, root, "box/knowledge.sy")
	raw = bytes.Replace(raw, []byte(`"spec":"evergreen.sy/v1",`),
		[]byte(`"spec":"evergreen.sy/v1","future_root":{"integer":9007199254740993},`), 1)
	raw = bytes.Replace(raw, []byte(`"semantic_revision":1`),
		[]byte(`"semantic_revision":1,"future_entity":{"kept":true}`), 1)
	raw = bytes.Replace(raw, []byte(`"kind_data":{}`),
		[]byte(`"kind_data":{},"future_claim":[1,{"kept":true}]`), 1)
	raw = bytes.Replace(raw, []byte(`"outgoing":[]`),
		[]byte(`"outgoing":[],"future_relations":null`), 1)
	raw = bytes.Replace(raw, []byte(`"outgoing":[]`), []byte(`"outgoing":[{
		"edge_id":"edge-future","schema":"evergreen.argument/v1","type":"supports","reason":"known relation",
		"target":{"entity_type":"claim","logical_id":"k-one","future_target":{"kept":true}},
		"context":{"note_id":"n-context","future_context":9007199254740993},
		"future_edge":[1,2]
	}]`), 1)
	raw = bytes.Replace(raw, []byte(`"relations":`), []byte(`"provenance":[{
		"source_id":"s-source","note_id":"n-context","segment_refs":["seg-one"],
		"reason":"known provenance","future_provenance":{"counter":9007199254740993,"values":[null,{"kept":true}]}
	},{
		"source_id":"s-second","note_id":"n-second","segment_refs":["seg-second"],
		"reason":"second provenance","future_provenance":{"counter":9007199254740992}
	},{
		"source_id":"s-delete","note_id":"n-delete","segment_refs":["seg-delete"],
		"reason":"delete provenance","future_provenance":{"owner":"delete-me"}
	}],"relations":`), 1)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	runtime, err := kernelEvergreen.NewRuntime(context.Background(), kernelEvergreen.RuntimeOptions{
		Root: root, Committer: &apiRecordingCommitter{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	oldProvider := evergreenRuntimeProvider
	evergreenRuntimeProvider = func(context.Context) (*kernelEvergreen.Runtime, error) { return runtime, nil }
	t.Cleanup(func() { evergreenRuntimeProvider = oldProvider })
	engine := evergreenTestEngine(t)
	headers := map[string]string{evergreenCallerHeader: "ui"}
	before, err := runtime.InspectClaim(context.Background(), "k-one")
	if err != nil {
		t.Fatal(err)
	}
	want, _ := evergreencore.MarshalDocumentEnvelope(before.Envelope)
	response := performEvergreenAPI[evergreencore.ClaimSnapshot](t, engine, http.MethodPost,
		"/api/evergreen/v1/claims/get", map[string]string{"logical_id": "k-one"}, headers)
	got, _ := evergreencore.MarshalDocumentEnvelope(response.Data.Envelope)
	if !response.OK || !bytes.Equal(want, got) {
		t.Fatalf("snapshot lost envelope fields:\nwant=%s\ngot=%s", want, got)
	}
	tags := []string{"edited"}
	plan := performEvergreenAPI[evergreencore.PlannedOperation](t, engine, http.MethodPost,
		"/api/evergreen/v1/claims/plan-edit", evergreencore.ClaimEditRequest{
			OperationID: "op-unknown-preserve", LogicalID: "k-one", Base: before.Base,
			Edit: evergreencore.ClaimEdit{Tags: &tags},
		}, headers)
	if !plan.OK {
		t.Fatalf("known-field edit rejected: %+v", plan.Diagnostics)
	}
	applyEvergreenClaimPlan(t, engine, plan.Data, headers)
	after := performEvergreenAPI[evergreencore.ClaimSnapshot](t, engine, http.MethodPost,
		"/api/evergreen/v1/claims/get", map[string]string{"logical_id": "k-one"}, headers)
	afterRaw, _ := evergreencore.MarshalDocumentEnvelope(after.Data.Envelope)
	for _, field := range []string{"future_root", "future_entity", "future_claim", "future_relations",
		"future_target", "future_context", "future_edge", "future_provenance",
		"9007199254740992", "9007199254740993"} {
		if !bytes.Contains(afterRaw, []byte(field)) {
			t.Fatalf("known-field edit removed %s: %s", field, afterRaw)
		}
	}
	storedAfterTag := readEvergreenAPIFile(t, root, "box/knowledge.sy")

	editedProvenance := []evergreencore.Provenance{
		{SourceID: "s-second", NoteID: "n-second", SegmentRefs: []evergreencore.LogicalID{"seg-second", "seg-new"}, Reason: "second edited"},
		{SourceID: "s-source", NoteID: "n-context", SegmentRefs: []evergreencore.LogicalID{"seg-one"}, Reason: "known edited"},
		{SourceID: "s-added", NoteID: "n-added", SegmentRefs: []evergreencore.LogicalID{"seg-added"}, Reason: "new provenance"},
	}
	stale := performEvergreenAPI[*evergreencore.PlannedOperation](t, engine, http.MethodPost,
		"/api/evergreen/v1/claims/plan-edit", evergreencore.ClaimEditRequest{
			OperationID: "op-provenance-stale", LogicalID: "k-one", Base: before.Base,
			Edit: evergreencore.ClaimEdit{Provenance: &editedProvenance},
		}, headers)
	if stale.OK || len(stale.Diagnostics) != 1 ||
		stale.Diagnostics[0].Code != evergreencore.CodeBaseMismatch {
		t.Fatalf("stale provenance draft = %+v", stale)
	}
	if got := readEvergreenAPIFile(t, root, "box/knowledge.sy"); !bytes.Equal(got, storedAfterTag) {
		t.Fatalf("stale provenance draft changed authority:\ngot=%s\nwant=%s", got, storedAfterTag)
	}
	provenancePlan := performEvergreenAPI[evergreencore.PlannedOperation](t, engine, http.MethodPost,
		"/api/evergreen/v1/claims/plan-edit", evergreencore.ClaimEditRequest{
			OperationID: "op-provenance-preserve", LogicalID: "k-one", Base: after.Data.Base,
			Edit: evergreencore.ClaimEdit{Provenance: &editedProvenance},
		}, headers)
	if !provenancePlan.OK {
		t.Fatalf("provenance plan rejected: %+v", provenancePlan.Diagnostics)
	}
	applyEvergreenClaimPlan(t, engine, provenancePlan.Data, headers)
	final := performEvergreenAPI[evergreencore.ClaimSnapshot](t, engine, http.MethodPost,
		"/api/evergreen/v1/claims/get", map[string]string{"logical_id": "k-one"}, headers)
	provenance := final.Data.Envelope.Provenance
	if !final.OK || len(provenance) != 3 ||
		provenance[0].SourceID != "s-second" || provenance[1].SourceID != "s-source" ||
		provenance[2].SourceID != "s-added" {
		t.Fatalf("final provenance order = %+v", final)
	}
	if string(provenance[0].Extra["future_provenance"]) != `{"counter":9007199254740992}` ||
		string(provenance[1].Extra["future_provenance"]) !=
			`{"counter":9007199254740993,"values":[null,{"kept":true}]}` ||
		len(provenance[2].Extra) != 0 {
		t.Fatalf("final provenance metadata = %+v", provenance)
	}
	bulkTags := []string{"bulk-preserve"}
	bulk := performEvergreenAPI[evergreencore.PlannedOperation](t, engine, http.MethodPost,
		"/api/evergreen/v1/claims/plan-bulk", evergreencore.BulkClaimRequest{
			OperationID: "op-provenance-bulk",
			Rows: []evergreencore.AVRowRef{{
				LogicalID: "k-one", SemanticHash: final.Data.Base,
			}},
			Edit: evergreencore.ClaimEdit{Tags: &bulkTags},
		}, headers)
	if !bulk.OK {
		t.Fatalf("provenance bulk plan rejected: %+v", bulk.Diagnostics)
	}
	applyEvergreenClaimPlan(t, engine, bulk.Data, headers)
	afterBulk := performEvergreenAPI[evergreencore.ClaimSnapshot](t, engine, http.MethodPost,
		"/api/evergreen/v1/claims/get", map[string]string{"logical_id": "k-one"}, headers)
	avPatch := performEvergreenAPI[evergreencore.PlannedOperation](t, engine, http.MethodPost,
		"/api/evergreen/v1/av/plan-patch", map[string]any{
			"operation_id": "op-provenance-av",
			"row_id":       "k-one",
			"base":         map[string]any{"logical_id": "k-one", "semantic_hash": afterBulk.Data.Base},
			"column":       map[string]any{"field": "claim.status"},
			"value":        map[string]any{"kind": "text", "text": "archived"},
		}, headers)
	if !avPatch.OK {
		t.Fatalf("provenance AV plan rejected: %+v", avPatch.Diagnostics)
	}
	applyEvergreenClaimPlan(t, engine, avPatch.Data, headers)
	afterAV := performEvergreenAPI[evergreencore.ClaimSnapshot](t, engine, http.MethodPost,
		"/api/evergreen/v1/claims/get", map[string]string{"logical_id": "k-one"}, headers)
	if !afterAV.OK || afterAV.Data.Envelope.Claim.Status != "archived" ||
		len(afterAV.Data.Envelope.Claim.Tags) != 1 || afterAV.Data.Envelope.Claim.Tags[0] != "bulk-preserve" ||
		string(afterAV.Data.Envelope.Provenance[0].Extra["future_provenance"]) != `{"counter":9007199254740992}` ||
		string(afterAV.Data.Envelope.Provenance[1].Extra["future_provenance"]) !=
			`{"counter":9007199254740993,"values":[null,{"kept":true}]}` {
		t.Fatalf("bulk/AV edit changed provenance metadata: %+v", afterAV)
	}
	finalRaw := readEvergreenAPIFile(t, root, "box/knowledge.sy")
	if bytes.Contains(finalRaw, []byte(`delete-me`)) ||
		!bytes.Contains(finalRaw, []byte(`9007199254740992`)) ||
		!bytes.Contains(finalRaw, []byte(`9007199254740993`)) {
		t.Fatalf("provenance add/delete or integers changed: %s", finalRaw)
	}
}

func TestEvergreenAPIMixedTooNewClaimRemainsVisibleAndReadOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	previousDataDir := util.DataDir
	util.DataDir = root
	t.Cleanup(func() { util.DataDir = previousDataDir })
	writeEvergreenAPIFixture(t, root, "box/supported.sy", "k-supported", "Supported")
	writeEvergreenAPIFixture(t, root, "box/future.sy", "k-future", "Future")
	futurePath := filepath.Join(root, "box/future.sy")
	future := readEvergreenAPIFile(t, root, "box/future.sy")
	future = bytes.Replace(future, []byte(`"20261010120000-root"`), []byte(`"20261010120000-future"`), 1)
	future = bytes.Replace(future, []byte(`"20261010120001-child"`), []byte(`"20261010120001-future"`), 1)
	future = bytes.Replace(future, []byte(`"evergreen.sy/v1"`), []byte(`"evergreen.sy/v99"`), 1)
	if err := os.WriteFile(futurePath, future, 0o600); err != nil {
		t.Fatal(err)
	}

	runtime, err := kernelEvergreen.NewRuntime(context.Background(), kernelEvergreen.RuntimeOptions{
		Root: root, Holder: "api-too-new-test", Committer: &apiRecordingCommitter{},
	})
	if err != nil {
		t.Fatalf("mixed workspace runtime failed: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	oldProvider := evergreenRuntimeProvider
	evergreenRuntimeProvider = func(context.Context) (*kernelEvergreen.Runtime, error) { return runtime, nil }
	t.Cleanup(func() { evergreenRuntimeProvider = oldProvider })
	engine := evergreenTestEngine(t)
	engine.POST("/api/evergreen/v1/workspace/export", evergreenWorkspaceExport)
	headers := map[string]string{evergreenCallerHeader: "ui"}

	exported := performEvergreenAPI[evergreencore.CanonicalArchive](t, engine, http.MethodPost,
		"/api/evergreen/v1/workspace/export", map[string]any{}, headers)
	if !exported.OK || len(exported.Data.Entities) != 2 {
		t.Fatalf("mixed workspace export = %+v", exported)
	}
	var exportedFuture json.RawMessage
	for _, entity := range exported.Data.Entities {
		if entity.LogicalID == "k-future" {
			exportedFuture = entity.Document
		}
	}
	exportedDocument, err := evergreencore.DecodeSY(exportedFuture, nil)
	if err != nil || !exportedDocument.ReadOnly || len(exportedDocument.Diagnostics) != 1 ||
		exportedDocument.Diagnostics[0].Code != evergreencore.CodeSchemaTooNew {
		t.Fatalf("exported too-new document = %+v err=%v", exportedDocument, err)
	}

	query := performEvergreenAPI[evergreencore.AVPage](t, engine, http.MethodPost,
		"/api/evergreen/v1/av/query", map[string]any{
			"view_id": "claims", "query": map[string]any{},
		}, headers)
	if !query.OK || query.Data.Total != 2 {
		t.Fatalf("mixed Claims query = %+v", query)
	}
	var futureRow evergreencore.AVRow
	for _, row := range query.Data.Rows {
		if row.Ref.LogicalID == "k-future" {
			futureRow = row
		}
	}
	if futureRow.Ref.LogicalID == "" || !futureRow.ReadOnly || len(futureRow.Diagnostics) != 1 ||
		futureRow.Diagnostics[0].Code != evergreencore.CodeSchemaTooNew {
		t.Fatalf("too-new row = %+v", futureRow)
	}

	detail := performEvergreenAPI[*evergreencore.ClaimSnapshot](t, engine, http.MethodPost,
		"/api/evergreen/v1/claims/get", map[string]string{"logical_id": "k-future"}, headers)
	if detail.OK || detail.Data != nil || len(detail.Diagnostics) != 1 ||
		detail.Diagnostics[0].Code != evergreencore.CodeSchemaTooNew {
		t.Fatalf("too-new detail = %+v", detail)
	}
	detailByDocument := performEvergreenAPI[*evergreencore.ClaimSnapshot](t, engine, http.MethodPost,
		"/api/evergreen/v1/claims/get", map[string]string{"document_id": "20261010120000-future"}, headers)
	if detailByDocument.OK || detailByDocument.Data != nil || len(detailByDocument.Diagnostics) != 1 ||
		detailByDocument.Diagnostics[0].Code != evergreencore.CodeSchemaTooNew {
		t.Fatalf("too-new detail by document = %+v", detailByDocument)
	}
	patch := performEvergreenAPI[*evergreencore.PlannedOperation](t, engine, http.MethodPost,
		"/api/evergreen/v1/av/plan-patch", map[string]any{
			"operation_id": "op-api-too-new",
			"row_id":       "k-future",
			"base":         futureRow.Ref,
			"column":       map[string]any{"field": "claim.status"},
			"value":        map[string]any{"kind": "text", "text": "deprecated"},
		}, headers)
	if patch.OK || len(patch.Diagnostics) != 1 ||
		patch.Diagnostics[0].Code != evergreencore.CodeSchemaTooNew {
		t.Fatalf("too-new patch = %+v", patch)
	}
	if got := readEvergreenAPIFile(t, root, "box/future.sy"); !bytes.Equal(got, future) {
		t.Fatalf("too-new API patch changed authority:\ngot=%s\nwant=%s", got, future)
	}
}

func TestEvergreenAPIHigherRegisteredKindSchemaIsReadOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	writeEvergreenAPIFixture(t, root, "box/supported.sy", "c-kind-supported", "Supported")
	writeEvergreenAPIFixture(t, root, "box/future.sy", "c-kind-future", "Future")
	futurePath := filepath.Join(root, "box/future.sy")
	future := readEvergreenAPIFile(t, root, "box/future.sy")
	future = bytes.Replace(
		future,
		[]byte(`"evergreen.claim-kind.knowledge/v1"`),
		[]byte(`"evergreen.claim-kind.knowledge/v99"`),
		1,
	)
	future = bytes.Replace(
		future,
		[]byte(`"kind_data":{}`),
		[]byte(`"kind_data":{"future":{"large":9007199254740993}}`),
		1,
	)
	if err := os.WriteFile(futurePath, future, 0o600); err != nil {
		t.Fatal(err)
	}

	runtime, err := kernelEvergreen.NewRuntime(context.Background(), kernelEvergreen.RuntimeOptions{
		Root: root, Holder: "api-future-kind-test", Committer: &apiRecordingCommitter{},
	})
	if err != nil {
		t.Fatalf("mixed kind workspace runtime failed: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	oldProvider := evergreenRuntimeProvider
	evergreenRuntimeProvider = func(context.Context) (*kernelEvergreen.Runtime, error) { return runtime, nil }
	t.Cleanup(func() { evergreenRuntimeProvider = oldProvider })
	engine := evergreenTestEngine(t)
	headers := map[string]string{evergreenCallerHeader: "ui"}

	query := performEvergreenAPI[evergreencore.AVPage](
		t, engine, http.MethodPost, "/api/evergreen/v1/av/query",
		map[string]any{"view_id": "knowledge", "query": map[string]any{}}, headers)
	if !query.OK || query.Data.Total != 2 {
		t.Fatalf("mixed kind query = %+v", query)
	}
	var futureRow evergreencore.AVRow
	for _, row := range query.Data.Rows {
		if row.Ref.LogicalID == "c-kind-future" {
			futureRow = row
		}
	}
	if !futureRow.ReadOnly || futureRow.Cells["claim_kind"].Text != "knowledge" ||
		len(futureRow.Diagnostics) != 1 ||
		futureRow.Diagnostics[0].Code != evergreencore.CodeSchemaTooNew {
		t.Fatalf("future kind row = %+v", futureRow)
	}

	detail := performEvergreenAPI[evergreencore.ClaimSnapshot](
		t, engine, http.MethodPost, "/api/evergreen/v1/claims/get",
		map[string]string{"logical_id": "c-kind-future"}, headers)
	if !detail.OK || !detail.Data.ReadOnly ||
		detail.Data.Envelope.Claim.KindSchema != "evergreen.claim-kind.knowledge/v99" ||
		!bytes.Contains(detail.Data.Envelope.Claim.KindData, []byte(`9007199254740993`)) {
		t.Fatalf("future kind detail = %+v", detail)
	}
	patch := performEvergreenAPI[*evergreencore.PlannedOperation](
		t, engine, http.MethodPost, "/api/evergreen/v1/av/plan-patch",
		map[string]any{
			"operation_id": "op-api-future-kind",
			"row_id":       "c-kind-future",
			"base":         futureRow.Ref,
			"column":       map[string]any{"field": "claim.status"},
			"value":        map[string]any{"kind": "text", "text": "archived"},
		}, headers)
	if patch.OK || len(patch.Diagnostics) != 1 ||
		patch.Diagnostics[0].Code != evergreencore.CodeSchemaTooNew {
		t.Fatalf("future kind patch = %+v", patch)
	}
	if got := readEvergreenAPIFile(t, root, "box/future.sy"); !bytes.Equal(got, future) {
		t.Fatalf("future kind patch changed authority:\ngot=%s\nwant=%s", got, future)
	}
}

func TestEvergreenClaimsContractCreateEditBulkAndUnavailableKind(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	previousDataDir := util.DataDir
	util.DataDir = root
	t.Cleanup(func() { util.DataDir = previousDataDir })
	writeEvergreenAPIFixture(t, root, "box/knowledge.sy", "k-one", "Knowledge")
	registry := evergreencore.ApplicationRegistry()
	runtime, err := kernelEvergreen.NewRuntime(context.Background(), kernelEvergreen.RuntimeOptions{
		Root: root, Registry: registry, Committer: &apiRecordingCommitter{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	oldProvider := evergreenRuntimeProvider
	evergreenRuntimeProvider = func(context.Context) (*kernelEvergreen.Runtime, error) { return runtime, nil }
	t.Cleanup(func() { evergreenRuntimeProvider = oldProvider })
	engine := evergreenTestEngine(t)
	headers := map[string]string{evergreenCallerHeader: "ui"}
	catalog := performEvergreenAPI[evergreencore.SchemaCatalog](t, engine, http.MethodGet, "/api/evergreen/v1/schemas", nil, headers)
	if !catalog.OK || len(catalog.Data.Kinds) != 3 || catalog.Data.Kinds[0].ViewHints.ViewID != "hypothesis" {
		t.Fatalf("descriptor catalog = %+v", catalog)
	}
	createdView := performEvergreenAPI[map[string]string](t, engine, http.MethodPost, "/api/evergreen/v1/av/create-view",
		map[string]string{"id": "20261010180000-hyp0001", "preset": "hypothesis"}, headers)
	if !createdView.OK || createdView.Data["preset"] != "hypothesis" {
		t.Fatalf("descriptor AV view = %+v", createdView)
	}
	create := evergreencore.ClaimCreateRequest{
		OperationID: "op-create-kind", LogicalID: "c-hypothesis", DocumentID: "20261010180000-hyp0002",
		Path: "box/hypothesis.sy", Title: "CAS prediction", Kind: evergreencore.HypothesisKind,
		Body:     json.RawMessage(`[{"ID":"20261010180001-hyp0003","Type":"NodeParagraph","Data":"CAS prevents lost updates"}]`),
		KindData: json.RawMessage(`{"statement":"CAS prevents lost updates","test_method":"Concurrent writes","future":{"kept":true}}`),
		Tags:     []string{},
		Provenance: []evergreencore.Provenance{{
			SourceID: "s-source", NoteID: "n-note", SegmentRefs: []evergreencore.LogicalID{"seg-one"}, Reason: "Source evidence",
		}},
	}
	planned := performEvergreenAPI[evergreencore.PlannedOperation](t, engine, http.MethodPost, "/api/evergreen/v1/claims/plan-create", create, headers)
	if !planned.OK {
		t.Fatalf("create plan = %+v", planned.Diagnostics)
	}
	applyEvergreenClaimPlan(t, engine, planned.Data, headers)
	snapshot := performEvergreenAPI[evergreencore.ClaimSnapshot](t, engine, http.MethodPost, "/api/evergreen/v1/claims/get",
		map[string]string{"logical_id": "c-hypothesis"}, headers)
	if !snapshot.OK || snapshot.Data.ReadOnly || snapshot.Data.Envelope.Claim.Status != "proposed" {
		t.Fatalf("Claim snapshot = %+v", snapshot)
	}
	byDocument := performEvergreenAPI[evergreencore.ClaimSnapshot](t, engine, http.MethodPost, "/api/evergreen/v1/claims/get",
		map[string]string{"document_id": create.DocumentID}, headers)
	if !byDocument.OK || byDocument.Data.LogicalID != create.LogicalID || byDocument.Data.Base != snapshot.Data.Base {
		t.Fatalf("canonical document entry = %+v", byDocument)
	}
	ambiguous := performEvergreenAPI[*evergreencore.ClaimSnapshot](t, engine, http.MethodPost, "/api/evergreen/v1/claims/get",
		map[string]string{"document_id": create.DocumentID, "logical_id": string(create.LogicalID)}, headers)
	if ambiguous.OK || ambiguous.Data != nil {
		t.Fatal("ambiguous Claim identity was accepted")
	}
	missing := performEvergreenAPI[*evergreencore.ClaimSnapshot](t, engine, http.MethodPost, "/api/evergreen/v1/claims/get",
		map[string]string{"document_id": "20261010180000-missing"}, headers)
	if missing.OK || missing.Data != nil {
		t.Fatal("ordinary document was treated as a Claim")
	}
	invalid := performEvergreenAPI[evergreencore.PlannedOperation](t, engine, http.MethodPost, "/api/evergreen/v1/claims/plan-edit",
		evergreencore.ClaimEditRequest{
			OperationID: "op-invalid-kind", LogicalID: "c-hypothesis", Base: snapshot.Data.Base,
			Edit: evergreencore.ClaimEdit{KindData: json.RawMessage(`{"test_method":""}`)},
		}, headers)
	if invalid.OK || invalid.Diagnostics[0].Code != evergreencore.CodeInvalidClaim {
		t.Fatalf("invalid kind data accepted: %+v", invalid)
	}
	edited := performEvergreenAPI[evergreencore.PlannedOperation](t, engine, http.MethodPost, "/api/evergreen/v1/claims/plan-edit",
		evergreencore.ClaimEditRequest{
			OperationID: "op-edit-kind", LogicalID: "c-hypothesis", Base: snapshot.Data.Base,
			Edit: evergreencore.ClaimEdit{KindData: json.RawMessage(`{"test_method":"Inject stale base"}`)},
		}, headers)
	if !edited.OK {
		t.Fatalf("kind edit = %+v", edited)
	}
	applyEvergreenClaimPlan(t, engine, edited.Data, headers)
	query := performEvergreenAPI[evergreencore.AVPage](t, engine, http.MethodPost, "/api/evergreen/v1/av/query",
		map[string]any{"view_id": "claims", "query": map[string]any{}}, headers)
	rows := []evergreencore.AVRowRef{}
	for _, row := range query.Data.Rows {
		rows = append(rows, row.Ref)
	}
	capabilities := performEvergreenAPI[evergreencore.BulkClaimCapabilities](t, engine, http.MethodPost,
		"/api/evergreen/v1/claims/bulk-capabilities", map[string]any{"rows": rows}, headers)
	if !capabilities.OK || len(capabilities.Data.Statuses) != 1 || capabilities.Data.Statuses[0] != "archived" {
		t.Fatalf("bulk capabilities = %+v", capabilities)
	}
	archived := "archived"
	bulk := performEvergreenAPI[evergreencore.PlannedOperation](t, engine, http.MethodPost, "/api/evergreen/v1/claims/plan-bulk",
		evergreencore.BulkClaimRequest{OperationID: "op-bulk-kind", Rows: rows, Edit: evergreencore.ClaimEdit{Status: &archived}}, headers)
	if !bulk.OK || len(bulk.Data.Images) != 2 {
		t.Fatalf("bulk plan = %+v", bulk)
	}
	applyEvergreenClaimPlan(t, engine, bulk.Data, headers)
	before := readEvergreenAPIFile(t, root, "box/hypothesis.sy")
	registry.Unregister(evergreencore.HypothesisKind)
	unavailable := performEvergreenAPI[evergreencore.ClaimSnapshot](t, engine, http.MethodPost, "/api/evergreen/v1/claims/get",
		map[string]string{"logical_id": "c-hypothesis"}, headers)
	if !unavailable.OK || !unavailable.Data.ReadOnly ||
		unavailable.Data.Diagnostics[0].Code != evergreencore.CodeClaimKindUnavailable ||
		!bytes.Contains(unavailable.Data.Envelope.Claim.KindData, []byte(`"kept":true`)) {
		t.Fatalf("unavailable Claim snapshot = %+v", unavailable)
	}
	unavailableBulk := performEvergreenAPI[*evergreencore.BulkClaimCapabilities](t, engine, http.MethodPost,
		"/api/evergreen/v1/claims/bulk-capabilities", map[string]any{"rows": []evergreencore.AVRowRef{{
			LogicalID: "c-hypothesis", SemanticHash: unavailable.Data.Base,
		}}}, headers)
	if unavailableBulk.OK || unavailableBulk.Data != nil ||
		unavailableBulk.Diagnostics[0].Code != evergreencore.CodeClaimKindUnavailable {
		t.Fatalf("unavailable bulk contract = %+v", unavailableBulk)
	}
	denied := performEvergreenAPI[evergreencore.PlannedOperation](t, engine, http.MethodPost, "/api/evergreen/v1/claims/plan-edit",
		evergreencore.ClaimEditRequest{
			OperationID: "op-unavailable-kind", LogicalID: "c-hypothesis", Base: unavailable.Data.Base,
			Edit: evergreencore.ClaimEdit{Status: &archived},
		}, headers)
	if denied.OK || denied.Diagnostics[0].Code != evergreencore.CodeClaimKindUnavailable ||
		!bytes.Equal(before, readEvergreenAPIFile(t, root, "box/hypothesis.sy")) {
		t.Fatalf("unavailable write = %+v", denied)
	}
}

func applyEvergreenClaimPlan(t *testing.T, engine http.Handler, plan evergreencore.PlannedOperation, headers map[string]string) {
	t.Helper()
	response := performEvergreenAPI[evergreencore.OperationRecord](t, engine, http.MethodPost, "/api/evergreen/v1/apply",
		evergreencore.ApplyRequest{Operation: plan}, headers)
	if !response.OK || response.Data.State != evergreencore.OperationCompleted {
		t.Fatalf("Claim apply = %+v", response)
	}
}
