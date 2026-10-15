//go:build fts5

package model

import (
	"bytes"
	"context"
	databaseSQL "database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	"github.com/siyuan-note/siyuan/kernel/cache"
	kernelEvergreen "github.com/siyuan-note/siyuan/kernel/evergreen"
	"github.com/siyuan-note/siyuan/kernel/filesys"
	"github.com/siyuan-note/siyuan/kernel/sql"
	"github.com/siyuan-note/siyuan/kernel/treenode"
	"github.com/siyuan-note/siyuan/kernel/util"
)

func TestEvergreenNativeMixedProjectionRefreshAndRecovery(t *testing.T) {
	if os.Getenv("SIYUAN_EG_NATIVE_PROJECTION_TEST") != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestEvergreenNativeMixedProjectionRefreshAndRecovery$", "-test.timeout=80s", "-test.v")
		command.Env = append(os.Environ(), "SIYUAN_EG_NATIVE_PROJECTION_TEST=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("isolated native projection: %v\n%s", err, output)
		}
		return
	}
	fixture := setupStructureTransactionTest(t)
	setupFoldTransactionDatabase(t, fixture)
	trees := []string{fixture.sourceID, fixture.targetID}
	before := [][]byte{}
	paths := []string{}
	for index, id := range trees {
		tree, err := LoadTreeByBlockID(id)
		if err != nil {
			t.Fatal(err)
		}
		p := tree.Box + "/" + strings.TrimPrefix(tree.Path, "/")
		raw, err := os.ReadFile(filepath.Join(util.DataDir, p))
		if err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			raw, err = core.EncodeSY(raw, &core.DocumentEnvelope{
				Spec: core.DocumentSpec,
				Entity: core.Entity{LogicalID: "c-mixed-projection", EntityType: core.EntityClaim,
					Schema: core.ClaimSchema, SemanticRevision: 1},
				Claim: &core.Claim{ClaimKind: core.KnowledgeKind, KindSchema: core.KnowledgeKindSchema,
					Status: "active", KindData: json.RawMessage(`{}`)},
				Relations: core.Relations{Outgoing: []core.TypedEdge{}},
			}, core.ApplicationRegistry())
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(util.DataDir, p), raw, 0600); err != nil {
				t.Fatal(err)
			}
		}
		before = append(before, raw)
		paths = append(paths, p)
	}
	if output, err := exec.Command("git", "-C", util.DataDir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("isolated Git: %v %s", err, output)
	}
	committer, err := core.NewLocalGitCommitter(util.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	rejectProjection := false
	sink := kernelEvergreen.IndexSink{
		Root: util.DataDir,
		RefreshDocuments: func(ctx context.Context, refs []core.AuthorityDocumentRef) error {
			if rejectProjection {
				db, err := databaseSQL.Open("sqlite3_extended", util.DBPath)
				if err != nil {
					return err
				}
				_, err = db.Exec("CREATE TRIGGER reject_native_projection BEFORE INSERT ON blocks BEGIN SELECT RAISE(ABORT, 'native projection failure'); END")
				db.Close()
				if err != nil {
					return err
				}
			}
			return RefreshEvergreenDocumentProjection(ctx, refs)
		},
	}
	runtime, err := kernelEvergreen.NewRuntime(context.Background(), kernelEvergreen.RuntimeOptions{
		Root: util.DataDir, Registry: core.ApplicationRegistry(),
		Storage: filesys.EvergreenDocumentStorage{Root: util.DataDir}, Committer: committer, IndexSink: sink,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	user := core.Principal{Type: core.PrincipalUser, ID: "test-owner", AuthSource: "workspace-session"}
	blockIDs := []string{"20261012122001-managed", "20261012122002-ordnary"}
	makeAfter := func(index int, needle string) []byte {
		t.Helper()
		var root map[string]json.RawMessage
		if err := json.Unmarshal(before[index], &root); err != nil {
			t.Fatal(err)
		}
		root["Children"] = json.RawMessage(`[{"ID":"` + blockIDs[index] + `","Type":"NodeParagraph","Properties":{"id":"` +
			blockIDs[index] + `"},"Children":[{"Type":"NodeText","Data":"` + needle + `"}]}]`)
		raw, err := json.Marshal(root)
		if err == nil && index == 0 {
			raw, err = core.PrepareNativeDocumentEdit(before[index], raw, core.ApplicationRegistry())
		}
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	apply := func(operationID string, managed, ordinary []byte) core.OperationRecord {
		t.Helper()
		base, _ := core.SemanticHash(before[0])
		record, err := runtime.ApplyNativeBatch(context.Background(), user, core.PlanRequest{
			Protocol: core.ChangePlanProtocol, OperationID: operationID,
			Base:   []core.BaseRef{{LogicalID: "c-mixed-projection", SemanticHash: base}},
			Writes: []core.WriteInput{{LogicalID: "c-mixed-projection", After: managed}},
		}, []core.NativeDocumentWrite{{Path: paths[1], Before: before[1], After: ordinary}})
		if err != nil {
			t.Fatalf("mixed apply: %+v %v", record, err)
		}
		return record
	}
	first := [][]byte{makeAfter(0, "managedmixedneedle"), makeAfter(1, "ordinarymixedneedle")}
	record := apply("op-native-mixed-projection", first[0], first[1])
	if record.DerivedStale || record.State != core.OperationCompleted {
		t.Fatalf("complete physical projection is stale: %+v", record)
	}
	assertProjection := func(needles []string) {
		t.Helper()
		db, err := databaseSQL.Open("sqlite3_extended", util.DBPath)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		for index, blockID := range blockIDs {
			if treenode.GetBlockTree(blockID) == nil {
				t.Fatalf("complete write-set omitted physical block %s", blockID)
			}
			if block := sql.GetBlock(blockID); block == nil || block.Content != needles[index] {
				t.Fatalf("SQL did not refresh complete native body: %+v", block)
			}
			var hits int
			if err = db.QueryRow("SELECT count(*) FROM blocks_fts WHERE blocks_fts MATCH ?", needles[index]).Scan(&hits); err != nil || hits != 1 {
				t.Fatalf("native FTS hits=%d err=%v", hits, err)
			}
			cached, exists := cache.GetTreeDataInBox(trees[index], fixture.box.ID)
			if !exists || !bytes.Contains(cached, []byte(needles[index])) {
				t.Fatal("native content cache did not refresh the complete write-set")
			}
		}
	}
	assertProjection([]string{"managedmixedneedle", "ordinarymixedneedle"})
	page, err := runtime.QueryAV(context.Background(), core.ClaimsViewID, core.AVQuery{})
	if err != nil || len(page.Rows) != 1 || page.Rows[0].Ref.LogicalID != "c-mixed-projection" {
		t.Fatalf("ordinary physical document entered logical index: %+v %v", page, err)
	}
	gitCount := func() string {
		t.Helper()
		output, err := exec.Command("git", "-C", util.DataDir, "rev-list", "--count", "HEAD").CombinedOutput()
		if err != nil {
			t.Fatalf("Git count: %v %s", err, output)
		}
		return strings.TrimSpace(string(output))
	}
	if gitCount() != "1" {
		t.Fatal("mixed write-set did not share one Git commit")
	}
	before = first
	second := [][]byte{makeAfter(0, "managedrecoveryneedle"), makeAfter(1, "ordinaryrecoveryneedle")}
	rejectProjection = true
	record = apply("op-native-mixed-stale", second[0], second[1])
	if !record.DerivedStale || record.State != core.OperationCompleted {
		t.Fatalf("failed complete projection was not recoverable: %+v", record)
	}
	db, err := databaseSQL.Open("sqlite3_extended", util.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("DROP TRIGGER reject_native_projection"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	rejectProjection = false
	records, err := RecoverEvergreenOperations(context.Background(), runtime)
	if err != nil || len(records) != 1 || records[0].DerivedStale {
		t.Fatalf("native projection recovery: %+v %v", records, err)
	}
	assertProjection([]string{"managedrecoveryneedle", "ordinaryrecoveryneedle"})
	if gitCount() != "2" {
		t.Fatal("native projection recovery duplicated Git")
	}
	for index, p := range paths {
		raw, err := os.ReadFile(filepath.Join(util.DataDir, p))
		if err != nil || !bytes.Equal(raw, second[index]) {
			t.Fatal("projection recovery rewrote authority")
		}
	}
}
