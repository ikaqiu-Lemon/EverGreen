// SiYuan - From thought to insight, with agents
// Copyright (c) 2020-present, b3log.org
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package evergreen

import (
	"encoding/json"
	"testing"

	"github.com/88250/lute/ast"
	evergreencore "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
)

func TestCaptureAttachDetachInjectRoundTrip(t *testing.T) {
	raw := evergreenSYFixture("evergreen.sy/v1")
	captured, err := Capture(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(captured.Envelopes) != 2 {
		t.Fatalf("captured envelopes = %d, want 2", len(captured.Envelopes))
	}

	root := &ast.Node{Type: ast.NodeDocument, ID: "20261010120000-a1b2c3d"}
	child := &ast.Node{Type: ast.NodeParagraph, ID: "20261010120001-a1b2c3d"}
	root.AppendChild(child)
	if err = Attach(root, captured); err != nil {
		t.Fatal(err)
	}
	if !HasEnvelope(root) || !HasEnvelope(child) {
		t.Fatal("managed envelopes were not attached to AST nodes")
	}

	detached, restore, err := Detach(root)
	if err != nil {
		t.Fatal(err)
	}
	if HasEnvelope(root) || HasEnvelope(child) {
		t.Fatal("carrier leaked into renderable AST")
	}
	rendered := []byte(`{
		"ID":"20261010120000-a1b2c3d",
		"Type":"NodeDocument",
		"Spec":"5",
		"Children":[{
			"ID":"20261010120001-a1b2c3d",
			"Type":"NodeParagraph",
			"Data":"edited body"
		}]
	}`)
	injected, err := Inject(rendered, detached)
	if err != nil {
		t.Fatal(err)
	}
	restore()
	if !HasEnvelope(root) || !HasEnvelope(child) {
		t.Fatal("carrier was not restored after rendering")
	}

	document, err := evergreencore.DecodeSY(injected, evergreencore.DefaultRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if document.ReadOnly || document.Envelope.Entity.LogicalID != "c-20261010-claim" {
		t.Fatalf("document envelope did not survive: %+v", document)
	}
	var tree map[string]json.RawMessage
	if err = json.Unmarshal(injected, &tree); err != nil {
		t.Fatal(err)
	}
	var children []map[string]json.RawMessage
	if err = json.Unmarshal(tree["Children"], &children); err != nil {
		t.Fatal(err)
	}
	if _, ok := children[0]["Evergreen"]; !ok {
		t.Fatal("block envelope did not survive")
	}
}

func TestTooNewEnvelopeCanLoadButCannotRewrite(t *testing.T) {
	raw := evergreenSYFixture("evergreen.sy/v99")
	captured, err := Capture(raw)
	if err != nil {
		t.Fatal(err)
	}
	root := &ast.Node{Type: ast.NodeDocument, ID: "20261010120000-a1b2c3d"}
	child := &ast.Node{Type: ast.NodeParagraph, ID: "20261010120001-a1b2c3d"}
	root.AppendChild(child)
	if err = Attach(root, captured); err != nil {
		t.Fatal(err)
	}
	detached, restore, err := Detach(root)
	if err != nil {
		t.Fatal(err)
	}
	defer restore()
	if _, err = Inject([]byte(`{
		"ID":"20261010120000-a1b2c3d",
		"Type":"NodeDocument",
		"Spec":"5",
		"Children":[{"ID":"20261010120001-a1b2c3d","Type":"NodeParagraph"}]
	}`), detached); !evergreencore.HasDiagnostic(err, evergreencore.CodeSchemaTooNew) {
		t.Fatalf("too-new rewrite error = %v", err)
	}
}

func TestManagedBlockRequiresManagedDocument(t *testing.T) {
	raw := []byte(`{
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
		}]
	}`)
	if _, err := Capture(raw); err == nil {
		t.Fatal("managed block without a managed document must be rejected")
	}
}

func TestInjectRefreshesEditedReviewHashesAndStaleState(t *testing.T) {
	raw := evergreenReviewSYFixture(t)
	captured, err := Capture(raw)
	if err != nil {
		t.Fatal(err)
	}
	rendered := []byte(`{
		"ID":"20261010130000-review1",
		"Type":"NodeDocument",
		"Spec":"5",
		"Children":[
			{"ID":"20261010130001-segment","Type":"NodeParagraph","Data":"edited body"},
			{"ID":"20261010130002-candidate","Type":"NodeSuperBlock","Children":[
				{"ID":"20261010130003-payload","Type":"NodeParagraph","Data":"reviewed claim"}
			]}
		]
	}`)
	injected, err := Inject(rendered, captured)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := evergreencore.InspectNoteReview(injected, nil)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Segments[0].StoredHash != snapshot.Segments[0].CurrentHash ||
		!snapshot.Candidates[0].Stale ||
		snapshot.Candidates[0].Metadata.State != "stale" ||
		len(snapshot.Lineage) != 1 ||
		snapshot.Lineage[0].Mutation != "edit" {
		t.Fatalf("refreshed review snapshot = %+v", snapshot)
	}
}

func TestInjectNativeDefersReviewToOneCorePreparation(t *testing.T) {
	before := evergreenReviewSYFixture(t)
	captured, err := Capture(before)
	if err != nil {
		t.Fatal(err)
	}
	var rendered map[string]json.RawMessage
	if err = json.Unmarshal(before, &rendered); err != nil {
		t.Fatal(err)
	}
	var children []map[string]json.RawMessage
	if err = json.Unmarshal(rendered["Children"], &children); err != nil {
		t.Fatal(err)
	}
	delete(rendered, "Evergreen")
	for _, child := range children {
		delete(child, "Evergreen")
	}
	children[0]["Data"] = json.RawMessage(`"native segment edit"`)
	rendered["Children"], _ = json.Marshal(children)
	raw, _ := json.Marshal(rendered)
	injected, err := InjectNative(raw, captured)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := evergreencore.InspectNoteReview(injected, nil)
	if err != nil || len(snapshot.Lineage) != 0 || snapshot.Candidates[0].Metadata.State != "draft" {
		t.Fatalf("native rendering mutated review metadata: %+v %v", snapshot, err)
	}
	prepared, err := evergreencore.PrepareNativeDocumentEdit(before, injected, installedKindRegistry())
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err = evergreencore.InspectNoteReview(prepared, nil)
	if err != nil || len(snapshot.Lineage) != 1 || !snapshot.Candidates[0].Stale ||
		snapshot.Candidates[0].Metadata.State != "stale" {
		t.Fatalf("core did not refresh native review exactly once: %+v %v", snapshot, err)
	}
	document, err := evergreencore.DecodeSY(prepared, installedKindRegistry())
	if err != nil || document.Envelope.Entity.SemanticRevision != 2 {
		t.Fatalf("core native revision: %+v %v", document, err)
	}
}

func evergreenSYFixture(spec string) []byte {
	return []byte(`{
		"ID":"20261010120000-a1b2c3d",
		"Type":"NodeDocument",
		"Spec":"5",
		"Evergreen":{
			"spec":"` + spec + `",
			"entity":{
				"logical_id":"c-20261010-claim",
				"entity_type":"claim",
				"schema":"evergreen.claim/v1",
				"semantic_revision":1
			},
			"claim":{
				"claim_kind":"opinion",
				"kind_schema":"evergreen.claim-kind.opinion/v1",
				"status":"active",
				"tags":[],
				"kind_data":{"validation":{"status":"pending"}}
			},
			"provenance":[{
				"source_id":"s-20261010-source",
				"note_id":"n-20261010-note",
				"segment_refs":["seg-01"],
				"reason":"source"
			}],
			"relations":{"outgoing":[]}
		},
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
			},
			"Data":"body"
		}]
	}`)
}

func evergreenReviewSYFixture(t *testing.T) []byte {
	t.Helper()
	segmentNode := json.RawMessage(`{
		"ID":"20261010130001-segment",
		"Type":"NodeParagraph",
		"Data":"original body"
	}`)
	segmentHash, err := evergreencore.NormalizedSegmentHash(segmentNode)
	if err != nil {
		t.Fatal(err)
	}
	payload := json.RawMessage(`[{
		"ID":"20261010130003-payload",
		"Type":"NodeParagraph",
		"Data":"reviewed claim"
	}]`)
	payloadHash, err := evergreencore.CandidateSubtreeHash(payload)
	if err != nil {
		t.Fatal(err)
	}
	segmentEnvelope, err := evergreencore.MarshalBlockEnvelope(
		&evergreencore.BlockEnvelope{
			Spec: evergreencore.BlockSpec, Role: "note_segment",
			Segment: &evergreencore.SegmentMetadata{
				SegmentID: "seg-review",
				SourceRef: evergreencore.SourceLocator{
					SourceID: "s-review",
					Locator: evergreencore.Locator{
						Kind: "source-lines", Value: "L1-L2",
					},
				},
				NormalizedHash: segmentHash,
			},
		})
	if err != nil {
		t.Fatal(err)
	}
	candidateEnvelope, err := evergreencore.MarshalBlockEnvelope(
		&evergreencore.BlockEnvelope{
			Spec: evergreencore.BlockSpec, Role: "candidate",
			Candidate: &evergreencore.CandidateMetadata{
				CandidateID: "cand-review", ClaimKind: evergreencore.KnowledgeKind,
				KindSchema:  evergreencore.KnowledgeKindSchema,
				SegmentRefs: []evergreencore.LogicalID{"seg-review"},
				PayloadHash: payloadHash,
				RefHashes: map[evergreencore.LogicalID]string{
					"seg-review": segmentHash,
				},
				State: "draft",
			},
		})
	if err != nil {
		t.Fatal(err)
	}
	documentEnvelope, err := evergreencore.MarshalDocumentEnvelope(
		&evergreencore.DocumentEnvelope{
			Spec: evergreencore.DocumentSpec,
			Entity: evergreencore.Entity{
				LogicalID: "n-review", EntityType: evergreencore.EntityNote,
				Schema: "evergreen.note/v1", SemanticRevision: 1,
			},
			Relations: evergreencore.Relations{Outgoing: []evergreencore.TypedEdge{}},
			Review: &evergreencore.NoteReview{
				Spec: evergreencore.NoteReviewSpec,
				Coverage: []evergreencore.CoverageModule{{
					ModuleID: "coverage-review", Disposition: "candidate",
					SegmentRefs: []evergreencore.LogicalID{"seg-review"},
					CandidateID: "cand-review",
				}},
			},
		})
	if err != nil {
		t.Fatal(err)
	}
	var segment map[string]any
	_ = json.Unmarshal(segmentNode, &segment)
	segment["Evergreen"] = json.RawMessage(segmentEnvelope)
	var payloadNodes []any
	_ = json.Unmarshal(payload, &payloadNodes)
	root := map[string]any{
		"ID": "20261010130000-review1", "Type": "NodeDocument", "Spec": "5",
		"Evergreen": json.RawMessage(documentEnvelope),
		"Children": []any{
			segment,
			map[string]any{
				"ID": "20261010130002-candidate", "Type": "NodeSuperBlock",
				"Evergreen": json.RawMessage(candidateEnvelope),
				"Children":  payloadNodes,
			},
		},
	}
	raw, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
