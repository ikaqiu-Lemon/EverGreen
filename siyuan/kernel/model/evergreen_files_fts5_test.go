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
	"github.com/siyuan-note/siyuan/kernel/av"
	"github.com/siyuan-note/siyuan/kernel/cache"
	kernelEvergreen "github.com/siyuan-note/siyuan/kernel/evergreen"
	"github.com/siyuan-note/siyuan/kernel/filesys"
	"github.com/siyuan-note/siyuan/kernel/sql"
	"github.com/siyuan-note/siyuan/kernel/treenode"
	"github.com/siyuan-note/siyuan/kernel/util"
)

func TestEvergreenNativeFilesCompleteProjectionAndRecovery(t *testing.T) {
	if os.Getenv("SIYUAN_EG_FILES_TEST") != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, os.Args[0],
			"-test.run=^TestEvergreenNativeFilesCompleteProjectionAndRecovery$", "-test.timeout=80s", "-test.v")
		command.Env = append(os.Environ(), "SIYUAN_EG_FILES_TEST=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("isolated native files: %v\n%s", err, output)
		}
		return
	}
	fixture := setupStructureTransactionTest(t)
	setupFoldTransactionDatabase(t, fixture)
	read := func(p string) []byte {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(util.DataDir, p))
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	write := func(p string, raw []byte) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(util.DataDir, p)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(util.DataDir, p), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	managedPath := fixture.box.ID + "/" + strings.TrimPrefix(fixture.sourcePath, "/")
	deletedPath := fixture.box.ID + "/" + strings.TrimPrefix(fixture.targetPath, "/")
	before, err := core.EncodeSY(read(managedPath), &core.DocumentEnvelope{
		Spec: core.DocumentSpec,
		Entity: core.Entity{LogicalID: "c-files-projection", EntityType: core.EntityClaim,
			Schema: core.ClaimSchema, SemanticRevision: 1},
		Claim: &core.Claim{ClaimKind: core.KnowledgeKind, KindSchema: core.KnowledgeKindSchema,
			Status: "active", KindData: json.RawMessage(`{}`)},
		Relations: core.Relations{Outgoing: []core.TypedEdge{}},
	}, core.ApplicationRegistry())
	if err != nil {
		t.Fatal(err)
	}
	write(managedPath, before)
	deletedBefore := read(deletedPath)
	deletedTree, err := filesys.LoadTreeProjection(fixture.box.ID, fixture.targetPath, util.NewLute())
	if err != nil {
		t.Fatal(err)
	}
	if err = sql.RefreshAuthorityTree(deletedTree); err != nil {
		t.Fatal(err)
	}
	avID := "20261012124500-native1"
	avPath, assetPath := "storage/av/"+avID+".json", "assets/atomic.bin"
	confPath := fixture.box.ID + "/.siyuan/conf.json"
	avBefore := []byte(`{"id":"` + avID + `","name":"before","spec":10,"future":{"counter":9007199254740993}}`)
	avAfter := bytes.Replace(avBefore, []byte(`"before"`), []byte(`"after"`), 1)
	confBefore := []byte(`{"name":"notebook","icon":"before","sort":0,"future":9007199254740993}`)
	confAfter := bytes.Replace(confBefore, []byte(`"before"`), []byte(`"after"`), 1)
	write(avPath, avBefore)
	write(confPath, confBefore)
	cache.SetAVData(avID, avBefore)
	var object map[string]json.RawMessage
	if err = json.Unmarshal(before, &object); err != nil {
		t.Fatal(err)
	}
	blockID := "20261012124501-managed"
	object["Children"] = json.RawMessage(`[{"ID":"` + blockID + `","Type":"NodeParagraph","Properties":{"id":"` +
		blockID + `"},"Children":[{"Type":"NodeText","Data":"filesprojectionneedle"}]}]`)
	after, _ := json.Marshal(object)
	after, err = core.PrepareNativeDocumentEdit(before, after, core.ApplicationRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("git", "-C", util.DataDir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("Git init: %v %s", err, output)
	}
	if output, err := exec.Command("git", "-C", util.DataDir, "add", "--", deletedPath).CombinedOutput(); err != nil {
		t.Fatalf("Git track deletion fixture: %v %s", err, output)
	}
	if output, err := exec.Command("git", "-C", util.DataDir, "-c", "user.name=test-owner",
		"-c", "user.email=test-owner@example.invalid", "-c", "core.hooksPath=.git/hooks",
		"commit", "-q", "-m", "Seed tracked deletion fixture").CombinedOutput(); err != nil {
		t.Fatalf("Git seed: %v %s", err, output)
	}
	reject := true
	runtime, err := kernelEvergreen.NewRuntime(context.Background(), kernelEvergreen.RuntimeOptions{
		Root: util.DataDir, Storage: filesys.EvergreenDocumentStorage{Root: util.DataDir},
		IndexSink: kernelEvergreen.IndexSink{Root: util.DataDir,
			RefreshDocuments: func(ctx context.Context, refs []core.AuthorityDocumentRef) error {
				if reject {
					db, err := databaseSQL.Open("sqlite3_extended", util.DBPath)
					if err != nil {
						return err
					}
					_, err = db.Exec("CREATE TRIGGER reject_file_delete BEFORE DELETE ON blocks BEGIN SELECT RAISE(ABORT, 'delete projection failure'); END")
					db.Close()
					if err != nil {
						return err
					}
				}
				return RefreshEvergreenDocumentProjection(ctx, refs)
			}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	base, _ := core.SemanticHash(before)
	user := core.Principal{Type: core.PrincipalUser, ID: "test-owner", AuthSource: "workspace-session"}
	record, err := runtime.ApplyNativeFiles(context.Background(), user, core.PlanRequest{
		Protocol: core.ChangePlanProtocol, OperationID: "op-host-native-files",
		Base:   []core.BaseRef{{LogicalID: "c-files-projection", SemanticHash: base}},
		Writes: []core.WriteInput{{LogicalID: "c-files-projection", After: after}},
	}, []core.NativeDocumentWrite{{Path: deletedPath, Before: deletedBefore, Delete: true}}, []core.NativeFileWrite{
		{Path: avPath, Kind: core.NativeFileAttributeView, Before: avBefore, After: avAfter},
		{Path: confPath, Kind: core.NativeFileNotebookConf, Before: confBefore, After: confAfter},
		{Path: assetPath, Kind: core.NativeFileAsset, Create: true, After: []byte("atomic asset bytes")},
	})
	if err != nil || record.State != core.OperationCompleted || !record.DerivedStale {
		t.Fatalf("projection error was not retained by journal: %+v %v", record, err)
	}
	if _, err = os.Stat(filepath.Join(util.DataDir, deletedPath)); !os.IsNotExist(err) {
		t.Fatal("ordinary document was not physically deleted")
	}
	for p, expected := range map[string][]byte{managedPath: after, avPath: avAfter, confPath: confAfter,
		assetPath: []byte("atomic asset bytes")} {
		if !bytes.Equal(read(p), expected) {
			t.Fatalf("complete write-set omitted %s", p)
		}
	}
	db, err := databaseSQL.Open("sqlite3_extended", util.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("DROP TRIGGER reject_file_delete"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	reject = false
	records, err := runtime.Recover(context.Background())
	if err != nil || len(records) != 1 || records[0].DerivedStale {
		t.Fatalf("full deletion projection recovery: %+v %v", records, err)
	}
	if treenode.GetBlockTree(fixture.targetID) != nil || sql.GetBlock(fixture.targetID) != nil {
		t.Fatal("deleted document remained in real blocktree or SQL")
	}
	if block := sql.GetBlock(blockID); block == nil || block.Content != "filesprojectionneedle" {
		t.Fatalf("managed companion projection missing: %+v", block)
	}
	if _, exists := cache.GetAVData(avID); exists {
		t.Fatal("stale native AV cache survived complete projection refresh")
	}
	if info, err := av.GetAttributeViewName(avID); err != nil || info != "after" {
		t.Fatalf("native AV existing reader did not see authority: %s %v", info, err)
	}
	var deletedHits int
	db, _ = databaseSQL.Open("sqlite3_extended", util.DBPath)
	if err = db.QueryRow("SELECT count(*) FROM blocks_fts WHERE root_id = ?", fixture.targetID).Scan(&deletedHits); err != nil || deletedHits != 0 {
		t.Fatalf("deleted document remains in FTS: %d %v", deletedHits, err)
	}
	db.Close()
	output, err := exec.Command("git", "-C", util.DataDir, "rev-list", "--count", "HEAD").CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != "2" {
		t.Fatalf("complete files/recovery duplicated Git: %v %s", err, output)
	}
	output, err = exec.Command("git", "-C", util.DataDir, "show", "--format=", "--name-only", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{managedPath, deletedPath, avPath, confPath, assetPath} {
		if !strings.Contains(string(output), p) {
			t.Fatalf("unique commit omitted %s: %s", p, output)
		}
	}
}
