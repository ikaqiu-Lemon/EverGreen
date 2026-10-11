package migrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/segment"
	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
)

func TestAssetReviewRewriteUpdatesManifestWithoutClearingStale(t *testing.T) {
	source := t.TempDir()
	writeFixture(t, source, "assets/chart.png", []byte("image fixture"))
	payload, err := core.MarkdownChildren([]byte("![chart](assets/chart.png)\n"), "image-fixture")
	if err != nil {
		t.Fatal(err)
	}
	nodes := core.SuperBlockChildren(payload)
	segmentNode := map[string]any{"ID": core.StablePhysicalID("seg-image"), "Type": "NodeSuperBlock", "Children": nodes}
	base, _ := json.Marshal(segmentNode)
	hash, _ := core.NormalizedSegmentHash(base)
	segmentNode["Evergreen"], _ = core.MarshalBlockEnvelope(&core.BlockEnvelope{Spec: core.BlockSpec, Role: "note_segment",
		Segment: &core.SegmentMetadata{SegmentID: "seg-image", NormalizedHash: hash,
			SourceRef: core.SourceLocator{SourceID: "s-image", Locator: core.Locator{Kind: "source-lines", Value: "L1"}}}})
	payloadRaw, _ := json.Marshal(nodes)
	payloadHash, _ := core.CandidateSubtreeHash(payloadRaw)
	candidateEnvelope, _ := core.MarshalBlockEnvelope(&core.BlockEnvelope{Spec: core.BlockSpec, Role: "candidate",
		Candidate: &core.CandidateMetadata{CandidateID: "cand-image", ClaimKind: core.KnowledgeKind,
			KindSchema: core.KnowledgeKindSchema, Title: "Image claim", State: "stale",
			SegmentRefs: []core.LogicalID{"seg-image"}, RefHashes: map[core.LogicalID]string{"seg-image": hash},
			PayloadHash: payloadHash, Relation: "support", Reason: "Image evidence"}})
	candidateNode := map[string]any{"ID": core.StablePhysicalID("cand-image"), "Type": "NodeSuperBlock",
		"Children": nodes, "Evergreen": json.RawMessage(candidateEnvelope)}
	segmentRaw, _ := json.Marshal(segmentNode)
	candidateRaw, _ := json.Marshal(candidateNode)
	envelope := &core.DocumentEnvelope{Spec: core.DocumentSpec,
		Entity: core.Entity{LogicalID: "n-image", EntityType: core.EntityNote, Schema: "evergreen.note/v1", SemanticRevision: 1},
		Review: &core.NoteReview{Spec: core.NoteReviewSpec, Coverage: []core.CoverageModule{{
			ModuleID: "coverage-image", Disposition: "candidate", SegmentRefs: []core.LogicalID{"seg-image"}, CandidateID: "cand-image"}}},
		Relations: core.Relations{Outgoing: []core.TypedEdge{}}}
	node, _ := json.Marshal(map[string]any{"ID": core.StablePhysicalID("n-image"), "Type": "NodeDocument",
		"Spec": "5", "Children": []json.RawMessage{segmentRaw, candidateRaw}})
	raw, err := core.EncodeSY(node, envelope, nil)
	if err != nil {
		t.Fatal(err)
	}
	prepared := Prepared{Manifest: Manifest{Counts: map[string]int{}, Files: []File{{Path: "assets/chart.png"}},
		Entities: []Entity{{LogicalID: "n-image", Source: "notes/n-image.md", Path: "box/n-image.sy"}},
		Reviews: []segment.LegacyParityInventory{{NoteID: "n-image", Stale: true,
			Segments:   []segment.LegacySegmentParity{{SegmentID: "seg-image", Hash: hash}},
			Candidates: []segment.LegacyCandidateParity{{CandidateID: "cand-image", PayloadHash: payloadHash}}}}},
		Images: map[string][]byte{"box/n-image.sy": raw}}
	if err := prepared.migrateAssets(source); err != nil {
		t.Fatal(err)
	}
	after, err := core.InspectNoteReview(prepared.Images["box/n-image.sy"], nil)
	if err != nil {
		t.Fatal(err)
	}
	if after.Segments[0].StoredHash == hash || after.Candidates[0].Metadata.PayloadHash == payloadHash {
		t.Fatal("asset paths did not change the review hashes")
	}
	if prepared.Manifest.Reviews[0].Segments[0].Hash != after.Segments[0].StoredHash ||
		prepared.Manifest.Reviews[0].Candidates[0].PayloadHash != after.Candidates[0].Metadata.PayloadHash {
		t.Fatal("manifest retained hashes from before asset rewriting")
	}
	if !after.Candidates[0].Stale || !prepared.Manifest.Reviews[0].Stale {
		t.Fatal("asset migration cleared pre-existing stale state")
	}
	if len(prepared.Manifest.Quarantine) != 0 || len(prepared.Manifest.Assets) != 1 {
		t.Fatalf("asset inventory incomplete: %+v", prepared.Manifest)
	}
	if _, err := os.Stat(filepath.Join(source, "assets/chart.png")); err != nil {
		t.Fatal("source blob changed")
	}
}
