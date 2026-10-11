package evergreencore

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCanonicalExportIncludesDocumentsOnceAndDiffUsesSemanticSets(t *testing.T) {
	root := t.TempDir()
	envelope := &DocumentEnvelope{Spec: DocumentSpec,
		Entity: Entity{LogicalID: "k-export", EntityType: EntityClaim, Schema: "evergreen.claim/v1", SemanticRevision: 1},
		Claim:  &Claim{ClaimKind: KnowledgeKind, KindSchema: KnowledgeKindSchema, Status: "active", Tags: []string{"z", "a"}, KindData: json.RawMessage(`{}`)},
		Relations: Relations{Outgoing: []TypedEdge{
			{ID: "edge-z", Schema: ArgumentSchema, Target: EntityRef{EntityType: EntityClaim, LogicalID: "o-other"}, Type: "supports", Reason: "Explains the result."},
			{ID: "edge-a", Schema: ArgumentSchema, Target: EntityRef{EntityType: EntityClaim, LogicalID: "o-other"}, Type: "limits", Reason: "Restricts the result."},
		}}}
	raw, err := NewMarkdownDocument("k-export", "Export", []byte("# Native body\n\n| A | B |\n|---|---|\n| 1 | 2 |\n"), envelope, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "export.sy"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	repository, err := NewFilesystemAuthority(FilesystemAuthorityOptions{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	first, err := CanonicalExport(context.Background(), repository, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Entities) != 1 {
		t.Fatal("document duplicated")
	}
	envelope.Claim.Tags = []string{"a", "z"}
	envelope.Relations.Outgoing[0], envelope.Relations.Outgoing[1] = envelope.Relations.Outgoing[1], envelope.Relations.Outgoing[0]
	reordered, err := EncodeSY(raw, envelope, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "export.sy"), reordered, 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := CanonicalExport(context.Background(), repository, nil)
	if err != nil {
		t.Fatal(err)
	}
	diff, err := DiffCanonical(first, second)
	if err != nil || len(diff) != 0 {
		t.Fatalf("set reorder differs: %+v, %v", diff, err)
	}
	envelope.Relations.Outgoing[0].Reason = "Updated explanatory evidence."
	changed, _ := EncodeSY(raw, envelope, nil)
	hash, _ := SemanticHash(changed)
	second.Entities[0].Document, second.Entities[0].Hash = changed, hash
	diff, err = DiffCanonical(first, second)
	if err != nil || len(diff) != 1 || diff[0].Category != "edge" {
		t.Fatalf("edge reason diff: %+v %v", diff, err)
	}
	second.Entities[0].Hash = "sha256:invalid"
	if _, err = DiffCanonical(first, second); !HasDiagnostic(err, CodeBaseMismatch) {
		t.Fatalf("archive checksum must reject tampering: %v", err)
	}
}

func TestMarkdownCreatesNativeRenderableBlocksAndDeterministicIDs(t *testing.T) {
	body := []byte("# Heading\n\n![diagram](assets/diagram.png)\n\n```go\nx := 1\n```\n\n| A | B |\n|---|---|\n| 1 | 2 |\n")
	first, err := MarkdownChildren(body, "stable-seed")
	if err != nil {
		t.Fatal(err)
	}
	second, err := MarkdownChildren(body, "stable-seed")
	if err != nil {
		t.Fatal(err)
	}
	lraw, _ := json.Marshal(first)
	rraw, _ := json.Marshal(second)
	if string(lraw) != string(rraw) {
		t.Fatal("native import generates unstable IDs")
	}
	for _, kind := range []string{"NodeHeading", "NodeImage", "NodeCodeBlock", "NodeTable", "NodeText"} {
		if !strings.Contains(string(lraw), kind) {
			t.Fatalf("native %s missing", kind)
		}
	}
	container := SuperBlockChildren(first)
	if string(container[0]) != `{"Type":"NodeSuperBlockOpenMarker"}` ||
		string(container[len(container)-1]) != `{"Type":"NodeSuperBlockCloseMarker"}` {
		t.Fatal("native superblock syntax missing")
	}
}

func TestMarkdownExportDoesNotInsertSpacesAtTextMarkBoundaries(t *testing.T) {
	body := []byte("**Library**：约束边界。\n\n`PreToolUse`与`PostToolUse`。\n")
	nodes, err := MarkdownChildren(body, "spacing")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(nodes)
	exported, err := MarkdownText(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(exported), "**Library**：") ||
		!strings.Contains(string(exported), "`PreToolUse`与`PostToolUse`") {
		t.Fatalf("export inserted presentation spacing: %s", exported)
	}
}
