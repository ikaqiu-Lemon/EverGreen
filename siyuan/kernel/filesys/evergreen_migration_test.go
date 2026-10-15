package filesys

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/88250/lute/render"
	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	"github.com/siyuan-note/siyuan/kernel/util"
)

func TestEvergreenMigrationContractNativeASTSurvivesKernelRender(t *testing.T) {
	original := util.DataDir
	util.DataDir = t.TempDir()
	t.Cleanup(func() { util.DataDir = original })
	envelope := &core.DocumentEnvelope{Spec: core.DocumentSpec,
		Entity: core.Entity{LogicalID: "k-migration", EntityType: core.EntityClaim, Schema: "evergreen.claim/v1", SemanticRevision: 1},
		Claim: &core.Claim{ClaimKind: core.KnowledgeKind, KindSchema: core.KnowledgeKindSchema,
			Status: "active", Tags: []string{"native"}, KindData: json.RawMessage(`{}`)},
		Relations: core.Relations{Outgoing: []core.TypedEdge{}},
		Extra:     core.RawObject{"future_field": json.RawMessage(`{"keep":true}`)}}
	raw, err := core.NewMarkdownDocument("k-migration", "Migration",
		[]byte("# Title\n\n**Important** paragraph.\n\n| A | B |\n|---|---|\n| 1 | 2 |\n\n```go\nx := 1\n```\n"), envelope, nil)
	if err != nil {
		t.Fatal(err)
	}
	checkMigrationNativeTree(t, raw, "20000101000000-egvault")
	if fixtureRoot := os.Getenv("EG_MIGRATION_FIXTURE_DATA_DIR"); fixtureRoot != "" {
		count := 0
		err := filepath.WalkDir(fixtureRoot, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() && strings.HasSuffix(path, ".sy") {
				raw, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				checkMigrationNativeTree(t, raw, "20000101000000-egvault")
				count++
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if count != 31 {
			t.Fatalf("frozen real workspace documents=%d, want 31", count)
		}
		t.Logf("real migrated native ASTs checked: %d", count)
	}
}

func checkMigrationNativeTree(t *testing.T, raw []byte, box string) {
	t.Helper()
	var root struct{ ID string }
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	tree, err := LoadTreeByData(raw, box, "/"+root.ID+".sy", util.NewLute())
	if err != nil {
		t.Fatal(err)
	}
	lute := util.NewLute()
	dom := render.NewProtyleRenderer(tree, lute.RenderOptions, lute.ParseOptions).Render()
	if len(dom) == 0 {
		t.Fatal("native migration generated empty Protyle DOM")
	}
	after, _, err := prepareWriteTree(tree)
	if err != nil {
		t.Fatal(err)
	}
	beforeDoc, err := core.DecodeSY(raw, core.ApplicationRegistry())
	if err != nil {
		t.Fatal(err)
	}
	afterDoc, err := core.DecodeSY(after, core.ApplicationRegistry())
	if err != nil {
		t.Fatal(err)
	}
	beforeEnvelope, _ := core.MarshalDocumentEnvelope(beforeDoc.Envelope)
	afterEnvelope, _ := core.MarshalDocumentEnvelope(afterDoc.Envelope)
	if beforeDoc.Envelope.Review == nil && string(beforeEnvelope) != string(afterEnvelope) {
		t.Fatalf("kernel rewrote Claim metadata: %s", root.ID)
	}
	if beforeDoc.Envelope.Review != nil {
		beforeReview, err := core.InspectNoteReview(raw, core.ApplicationRegistry())
		if err != nil {
			t.Fatal(err)
		}
		afterReview, err := core.InspectNoteReview(after, core.ApplicationRegistry())
		if err != nil {
			t.Fatal(err)
		}
		if beforeReview.Summary != afterReview.Summary || beforeReview.CanMaterialize != afterReview.CanMaterialize {
			t.Fatalf("kernel changed migration review: %s before=%+v after=%+v",
				root.ID, beforeReview.Summary, afterReview.Summary)
		}
		for index, candidate := range beforeReview.Candidates {
			if candidate.Stale != afterReview.Candidates[index].Stale {
				t.Fatalf("kernel made migrated Candidate stale: %s", candidate.CandidateID)
			}
		}
	}
}
