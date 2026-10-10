package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	evergreencore "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
)

func TestSYHostUsesSharedCoreForOfflineReadWriteAndScan(t *testing.T) {
	host := NewSYHost(nil)
	envelope := &evergreencore.DocumentEnvelope{
		Spec: evergreencore.DocumentSpec,
		Entity: evergreencore.Entity{
			LogicalID:        "c-20261010-offline",
			EntityType:       evergreencore.EntityClaim,
			Schema:           evergreencore.ClaimSchema,
			SemanticRevision: 1,
		},
		Claim: &evergreencore.Claim{
			ClaimKind:  evergreencore.OpinionKind,
			KindSchema: evergreencore.OpinionKindSchema,
			Status:     "active",
			Tags:       []string{"offline"},
			KindData:   json.RawMessage(`{"validation":{"status":"pending"}}`),
		},
		Provenance: []evergreencore.Provenance{{
			SourceID:    "s-20261010-source",
			NoteID:      "n-20261010-note",
			SegmentRefs: []evergreencore.LogicalID{"seg-01"},
			Reason:      "source",
		}},
		Relations: evergreencore.Relations{Outgoing: []evergreencore.TypedEdge{}},
	}
	base := []byte(`{
		"ID":"20261010120000-a1b2c3d",
		"Type":"NodeDocument",
		"Spec":"5",
		"Children":[]
	}`)
	encoded, err := host.Encode(base, envelope)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := host.Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Envelope.Entity.LogicalID != envelope.Entity.LogicalID {
		t.Fatalf("logical ID = %q", decoded.Envelope.Entity.LogicalID)
	}
	firstHash, err := host.SemanticHash(encoded)
	if err != nil {
		t.Fatal(err)
	}
	secondHash, err := evergreencore.SemanticHash(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if firstHash != secondHash {
		t.Fatalf("host hash %q differs from shared core %q", firstHash, secondHash)
	}

	root := t.TempDir()
	if err = os.WriteFile(filepath.Join(root, "claim.sy"), encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	index, err := host.Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if location, ok := index.Locations["c-20261010-offline"]; !ok || location.Path != "claim.sy" {
		t.Fatalf("offline logical index = %+v", index.Locations)
	}
}
