package evergreencore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

func TestClaimsRoundTripAcrossKinds(t *testing.T) {
	registry := DefaultRegistry()
	if err := registry.Register(KindDescriptor{
		Kind:               "hypothesis",
		Schema:             "evergreen.claim-kind.hypothesis/v1",
		AllowedStatuses:    []string{"active", "retired"},
		AllowedEdgeSchemas: []SchemaRef{ArgumentSchema},
		ValidateKindData: func(raw json.RawMessage) error {
			var data struct {
				Confidence int `json:"confidence"`
			}
			if err := json.Unmarshal(raw, &data); err != nil {
				return err
			}
			if data.Confidence < 0 || data.Confidence > 100 {
				return errors.New("confidence must be between 0 and 100")
			}
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name     string
		id       LogicalID
		kind     ClaimKind
		schema   SchemaRef
		kindData json.RawMessage
	}{
		{
			name: "legacy knowledge id",
			id:   "k-20261010-storage",
			kind: KnowledgeKind, schema: KnowledgeKindSchema,
			kindData: json.RawMessage(`{}`),
		},
		{
			name: "legacy opinion id",
			id:   "o-20261010-authority",
			kind: OpinionKind, schema: OpinionKindSchema,
			kindData: json.RawMessage(`{"validation":{"status":"pending"}}`),
		},
		{
			name: "generic claim id",
			id:   "c-20261010-latency",
			kind: "hypothesis", schema: "evergreen.claim-kind.hypothesis/v1",
			kindData: json.RawMessage(`{"confidence":70}`),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			envelope := claimEnvelope(tc.id, tc.kind, tc.schema, tc.kindData)
			raw, err := EncodeSY(emptySY("20261010120000-a1b2c3d"), envelope, registry)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := DecodeSY(raw, registry)
			if err != nil {
				t.Fatal(err)
			}
			if decoded.ReadOnly {
				t.Fatalf("decoded current document is read-only: %+v", decoded.Diagnostics)
			}
			if got := decoded.Envelope.Claim.ClaimKind; got != tc.kind {
				t.Fatalf("kind = %q, want %q", got, tc.kind)
			}
			if got := decoded.Envelope.Entity.LogicalID; got != tc.id {
				t.Fatalf("logical id = %q, want %q", got, tc.id)
			}
		})
	}
}

func TestTypedEdgesValidateAndRoundTrip(t *testing.T) {
	registry := DefaultRegistry()
	envelope := claimEnvelope(
		"c-20261010-claim",
		OpinionKind,
		OpinionKindSchema,
		json.RawMessage(`{"validation":{"status":"pending"}}`),
	)
	envelope.Provenance = []Provenance{{
		SourceID: "s-20261010-source",
		NoteID:   "n-20261010-note",
		SegmentRefs: []LogicalID{
			"seg-01",
		},
		Reason: "The note contains the original argument.",
	}}
	envelope.Relations.Outgoing = []TypedEdge{
		{
			ID:     "edge-01",
			Schema: ArgumentSchema,
			Target: EntityRef{EntityType: EntityClaim, LogicalID: "k-20261010-target"},
			Type:   "supports",
			Reason: "The target provides the underlying invariant.",
		},
		{
			ID:     "edge-02",
			Schema: MaterialSchema,
			Target: EntityRef{EntityType: EntitySource, LogicalID: "s-20261010-source"},
			Type:   "support",
			Reason: "The source directly states the evidence.",
			Context: EdgeContext{
				NoteID:      "n-20261010-note",
				SegmentRefs: []LogicalID{"seg-01"},
			},
		},
		{
			ID:     "edge-03",
			Schema: ReplacementSchema,
			Target: EntityRef{EntityType: EntityClaim, LogicalID: "o-20261010-new"},
			Type:   "replaced_by",
			Reason: "A newer claim narrows the boundary.",
		},
	}

	raw, err := EncodeSY(emptySY("20261010120000-a1b2c3d"), envelope, registry)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeSY(raw, registry)
	if err != nil {
		t.Fatal(err)
	}
	if got := decoded.Envelope.Relations.Outgoing; len(got) != 3 ||
		got[0].ID != "edge-01" || got[1].Context.NoteID != "n-20261010-note" {
		t.Fatalf("typed edges did not round-trip: %+v", got)
	}

	decoded.Envelope.Relations.Outgoing[1].Reason = ""
	if _, err = EncodeSY(raw, decoded.Envelope, registry); !HasDiagnostic(err, CodeEdgeReasonRequired) {
		t.Fatalf("empty material reason error = %v", err)
	}
	decoded.Envelope.Relations.Outgoing[1].Reason = "restored"
	decoded.Envelope.Relations.Outgoing[2].ID = "edge-01"
	if _, err = EncodeSY(raw, decoded.Envelope, registry); !HasDiagnostic(err, CodeDuplicateID) {
		t.Fatalf("duplicate edge id error = %v", err)
	}
}

func TestOpinionValidationRequired(t *testing.T) {
	envelope := claimEnvelope(
		"c-20261010-opinion",
		OpinionKind,
		OpinionKindSchema,
		json.RawMessage(`{}`),
	)
	if _, err := EncodeSY(emptySY("20261010120000-a1b2c3d"), envelope, DefaultRegistry()); !HasDiagnostic(err, CodeInvalidClaim) {
		t.Fatalf("missing opinion validation error = %v", err)
	}
}

func TestUnknownFieldsPreservedAndTooNewIsReadOnly(t *testing.T) {
	registry := DefaultRegistry()
	raw := []byte(`{
		"ID":"20261010120000-a1b2c3d",
		"Type":"NodeDocument",
		"Spec":"5",
		"FutureRoot":{"enabled":true},
		"Evergreen":{
			"spec":"evergreen.sy/v1",
			"entity":{
				"logical_id":"c-20261010-unknown",
				"entity_type":"claim",
				"schema":"evergreen.claim/v1",
				"semantic_revision":1
			},
			"claim":{
				"claim_kind":"knowledge",
				"kind_schema":"evergreen.claim-kind.knowledge/v1",
				"status":"active",
				"tags":[],
				"kind_data":{},
				"future_claim":{"mode":"keep"}
			},
			"provenance":[{
				"source_id":"s-20261010-source",
				"note_id":"n-20261010-note",
				"segment_refs":["seg-01"],
				"reason":"source"
			}],
			"relations":{"outgoing":[]},
			"future_envelope":{"version":2}
		},
		"Children":[]
	}`)
	decoded, err := DecodeSY(raw, registry)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeSY(decoded.Raw, decoded.Envelope, registry)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]json.RawMessage
	if err = json.Unmarshal(encoded, &root); err != nil {
		t.Fatal(err)
	}
	if _, ok := root["FutureRoot"]; !ok {
		t.Fatal("unknown SiYuan root field was dropped")
	}
	var evergreen map[string]json.RawMessage
	if err = json.Unmarshal(root["Evergreen"], &evergreen); err != nil {
		t.Fatal(err)
	}
	if _, ok := evergreen["future_envelope"]; !ok {
		t.Fatal("unknown Evergreen envelope field was dropped")
	}
	var claim map[string]json.RawMessage
	if err = json.Unmarshal(evergreen["claim"], &claim); err != nil {
		t.Fatal(err)
	}
	if _, ok := claim["future_claim"]; !ok {
		t.Fatal("unknown Claim field was dropped")
	}

	var top map[string]json.RawMessage
	if err = json.Unmarshal(encoded, &top); err != nil {
		t.Fatal(err)
	}
	var env map[string]json.RawMessage
	if err = json.Unmarshal(top["Evergreen"], &env); err != nil {
		t.Fatal(err)
	}
	env["spec"] = json.RawMessage(`"evergreen.sy/v99"`)
	top["Evergreen"], _ = json.Marshal(env)
	tooNew, _ := json.Marshal(top)
	future, err := DecodeSY(tooNew, registry)
	if err != nil {
		t.Fatal(err)
	}
	if !future.ReadOnly || !hasDiagnostic(future.Diagnostics, CodeSchemaTooNew) {
		t.Fatalf("too-new document must be read-only: %+v", future)
	}
	if _, err = EncodeSY(future.Raw, future.Envelope, registry); !HasDiagnostic(err, CodeSchemaTooNew) {
		t.Fatalf("too-new rewrite error = %v", err)
	}
}

func TestSemanticHashCanonicalization(t *testing.T) {
	registry := DefaultRegistry()
	envelope := claimEnvelope(
		"c-20261010-hash",
		KnowledgeKind,
		KnowledgeKindSchema,
		json.RawMessage(`{}`),
	)
	envelope.Claim.Tags = []string{"z", "a"}
	envelope.Relations.Outgoing = []TypedEdge{
		{
			ID: "edge-b", Schema: ArgumentSchema,
			Target: EntityRef{EntityType: EntityClaim, LogicalID: "k-20261010-b"},
			Type:   "supports", Reason: "b",
		},
		{
			ID: "edge-a", Schema: ArgumentSchema,
			Target: EntityRef{EntityType: EntityClaim, LogicalID: "k-20261010-a"},
			Type:   "limits", Reason: "a",
		},
	}
	first, err := EncodeSY([]byte(`{
		"Spec":"5","Type":"NodeDocument","ID":"20261010120000-a1b2c3d",
		"Children":[{"Type":"NodeParagraph","ID":"20261010120001-a1b2c3d","Data":"body"}]
	}`), envelope, registry)
	if err != nil {
		t.Fatal(err)
	}
	envelope.Relations.Outgoing[0], envelope.Relations.Outgoing[1] =
		envelope.Relations.Outgoing[1], envelope.Relations.Outgoing[0]
	envelope.Claim.Tags[0], envelope.Claim.Tags[1] = envelope.Claim.Tags[1], envelope.Claim.Tags[0]
	second, err := EncodeSY([]byte(`{
		"Children":[{"Data":"body","ID":"20261010120001-a1b2c3d","Type":"NodeParagraph"}],
		"ID":"20261010120000-a1b2c3d","Type":"NodeDocument","Spec":"5"
	}`), envelope, registry)
	if err != nil {
		t.Fatal(err)
	}
	firstHash, err := SemanticHash(first)
	if err != nil {
		t.Fatal(err)
	}
	secondHash, err := SemanticHash(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstHash != secondHash {
		t.Fatalf("canonical hashes differ: %s != %s", firstHash, secondHash)
	}
	changed := bytes.Replace(second, []byte(`"body"`), []byte(`"changed"`), 1)
	changedHash, err := SemanticHash(changed)
	if err != nil {
		t.Fatal(err)
	}
	if changedHash == firstHash {
		t.Fatal("semantic body change did not change hash")
	}
}

func TestScanFSBuildsDocumentAndBlockMap(t *testing.T) {
	registry := DefaultRegistry()
	note := &DocumentEnvelope{
		Spec: DocumentSpec,
		Entity: Entity{
			LogicalID:        "n-20261010-note",
			EntityType:       EntityNote,
			Schema:           "evergreen.note/v1",
			SemanticRevision: 1,
		},
	}
	raw, err := EncodeSY([]byte(`{
		"ID":"20261010120000-a1b2c3d",
		"Type":"NodeDocument",
		"Spec":"5",
		"Children":[{
			"ID":"20261010120001-a1b2c3d",
			"Type":"NodeParagraph",
			"Evergreen":{
				"spec":"evergreen.block/v1",
				"role":"note_segment",
				"segment":{
					"segment_id":"seg-01",
					"source_ref":{
						"source_id":"s-20261010-source",
						"locator":{"kind":"source-block","value":"block-1"}
					},
					"normalized_hash":"sha256:segment"
				}
			}
		},{
			"ID":"20261010120002-a1b2c3d",
			"Type":"NodeBlockquote",
			"Evergreen":{
				"spec":"evergreen.block/v1",
				"role":"candidate",
				"candidate":{
					"candidate_id":"cand-01",
					"claim_kind":"knowledge",
					"kind_schema":"evergreen.claim-kind.knowledge/v1",
					"segment_refs":["seg-01"],
					"payload_hash":"sha256:payload",
					"ref_hashes":{"seg-01":"sha256:segment"},
					"state":"reviewable"
				}
			}
		}]
	}`), note, registry)
	if err != nil {
		t.Fatal(err)
	}
	index, err := ScanFS(fstest.MapFS{
		"notes/note.sy": &fstest.MapFile{Data: raw, Mode: 0o644},
	}, registry)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []LogicalID{"n-20261010-note", "seg-01", "cand-01"} {
		if _, ok := index.Locations[id]; !ok {
			t.Fatalf("logical location missing for %s: %+v", id, index.Locations)
		}
	}
	if got := index.Locations["seg-01"].BlockID; got != "20261010120001-a1b2c3d" {
		t.Fatalf("segment block id = %q", got)
	}

	_, err = ScanFS(fstest.MapFS{
		"a.sy": &fstest.MapFile{Data: raw, Mode: fs.FileMode(0o644)},
		"b.sy": &fstest.MapFile{Data: raw, Mode: fs.FileMode(0o644)},
	}, registry)
	if !HasDiagnostic(err, CodeDuplicateID) {
		t.Fatalf("duplicate logical id error = %v", err)
	}
}

func TestChangePlanAndHostPorts(t *testing.T) {
	plan := ChangePlan{
		OperationID: "op-01JTEST",
		Protocol:    ChangePlanProtocol,
		Principal:   Principal{Type: PrincipalUser, ID: "ikaqiu"},
		Command:     "claim.edge.update",
		Base: []BaseRef{{
			LogicalID:    "c-20261010-claim",
			SemanticHash: "sha256:before",
		}},
		Writes: []PlannedWrite{{
			LogicalID: "c-20261010-claim",
			Before:    "sha256:before",
			After:     "sha256:after",
		}},
	}
	if err := ValidateChangePlan(plan); err != nil {
		t.Fatal(err)
	}
	plan.Writes[0].Before = "sha256:stale"
	if err := ValidateChangePlan(plan); !HasDiagnostic(err, CodeBaseMismatch) {
		t.Fatalf("plan base mismatch error = %v", err)
	}

	var _ Repository = (*memoryRepository)(nil)
	var _ TransactionHost = (*memoryTransactionHost)(nil)
	var _ CommitSink = (*memoryCommitSink)(nil)
	var _ IndexSink = (*memoryIndexSink)(nil)
}

func claimEnvelope(id LogicalID, kind ClaimKind, schema SchemaRef, kindData json.RawMessage) *DocumentEnvelope {
	return &DocumentEnvelope{
		Spec: DocumentSpec,
		Entity: Entity{
			LogicalID:        id,
			EntityType:       EntityClaim,
			Schema:           ClaimSchema,
			SemanticRevision: 1,
		},
		Claim: &Claim{
			ClaimKind:  kind,
			KindSchema: schema,
			Status:     "active",
			Tags:       []string{},
			KindData:   kindData,
		},
		Provenance: []Provenance{{
			SourceID:    "s-20261010-source",
			NoteID:      "n-20261010-note",
			SegmentRefs: []LogicalID{"seg-01"},
			Reason:      "source",
		}},
		Relations: Relations{Outgoing: []TypedEdge{}},
	}
}

func emptySY(id string) []byte {
	return []byte(`{"ID":"` + id + `","Type":"NodeDocument","Spec":"5","Children":[]}`)
}

func hasDiagnostic(diagnostics []Diagnostic, code string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}

type memoryRepository struct{}

func (*memoryRepository) Load(context.Context, LogicalID) ([]byte, error) {
	return nil, nil
}

func (*memoryRepository) List(context.Context) ([]LogicalID, error) {
	return nil, nil
}

type memoryTransactionHost struct{}

func (*memoryTransactionHost) Apply(context.Context, ChangePlan) error {
	return nil
}

type memoryCommitSink struct{}

func (*memoryCommitSink) Commit(context.Context, ChangePlan) (string, error) {
	return "", nil
}

type memoryIndexSink struct{}

func (*memoryIndexSink) Invalidate(context.Context, []LogicalID) error {
	return nil
}

func TestLogicalIDKindIsNotDerivedFromPrefix(t *testing.T) {
	registry := DefaultRegistry()
	envelope := claimEnvelope(
		"k-20261010-legacy",
		OpinionKind,
		OpinionKindSchema,
		json.RawMessage(`{"validation":{"status":"pending"}}`),
	)
	raw, err := EncodeSY(emptySY("20261010120000-a1b2c3d"), envelope, registry)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeSY(raw, registry)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Envelope.Claim.ClaimKind != OpinionKind {
		t.Fatal("claim kind was inferred from legacy ID prefix")
	}
	if strings.HasPrefix(string(decoded.Envelope.Entity.LogicalID), "c-") {
		t.Fatal("legacy ID was rewritten")
	}
}
