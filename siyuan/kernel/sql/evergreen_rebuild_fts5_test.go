//go:build fts5

package sql

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	"github.com/siyuan-note/siyuan/kernel/cache"
	"github.com/siyuan-note/siyuan/kernel/evergreen"
	"github.com/siyuan-note/siyuan/kernel/filesys"
	"github.com/siyuan-note/siyuan/kernel/treenode"
	"github.com/siyuan-note/siyuan/kernel/util"
)

func TestEvergreenMigrationFullDerivedRebuild(t *testing.T) {
	if os.Getenv("SIYUAN_TEST_EG_REBUILD") != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestEvergreenMigrationFullDerivedRebuild$", "-test.timeout=80s", "-test.v")
		command.Env = append(os.Environ(), "SIYUAN_TEST_EG_REBUILD=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("isolated rebuild: %v\n%s", err, output)
		}
		return
	}
	root := t.TempDir()
	util.WorkspaceDir = root
	util.DataDir, util.TempDir, util.ConfDir = filepath.Join(root, "data"), filepath.Join(root, "temp"), filepath.Join(root, "conf")
	util.QueueDir = filepath.Join(util.TempDir, "queue")
	util.DBPath, util.BlockTreeDBPath = filepath.Join(util.TempDir, util.DBName), filepath.Join(util.TempDir, "blocktree.db")
	util.HistoryDBPath, util.AssetContentDBPath = filepath.Join(util.TempDir, "history.db"), filepath.Join(util.TempDir, "asset_content.db")
	for _, directory := range []string{util.DataDir, util.TempDir, util.ConfDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	fixture := os.Getenv("EG_MIGRATION_FIXTURE_DATA_DIR")
	expectedDocuments, expectedLocations := 31, 247
	if fixture == "" {
		expectedDocuments, expectedLocations = 1, 1
		envelope := &core.DocumentEnvelope{Spec: core.DocumentSpec,
			Entity: core.Entity{LogicalID: "k-rebuild", EntityType: core.EntityClaim, Schema: "evergreen.claim/v1", SemanticRevision: 1},
			Claim: &core.Claim{ClaimKind: core.KnowledgeKind, KindSchema: core.KnowledgeKindSchema,
				Status: "active", KindData: json.RawMessage(`{}`)},
			Relations: core.Relations{Outgoing: []core.TypedEdge{}}}
		raw, err := core.NewMarkdownDocument("k-rebuild", "Harness rebuild", []byte("Harness rebuild content.\n"), envelope, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(util.DataDir, "20000101000000-egvault"), 0o700); err != nil {
			t.Fatal(err)
		}
		var document struct {
			ID string `json:"ID"`
		}
		if err := json.Unmarshal(raw, &document); err != nil || document.ID == "" {
			t.Fatalf("invalid generated fixture identity: %v", err)
		}
		if err := os.WriteFile(filepath.Join(util.DataDir, "20000101000000-egvault", document.ID+".sy"), raw, 0o600); err != nil {
			t.Fatal(err)
		}
		fixture = util.DataDir
	}
	count := 0
	if err := filepath.WalkDir(fixture, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".sy") {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(fixture, path)
		target := filepath.Join(util.DataDir, relative)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		count++
		return os.WriteFile(target, raw, 0o600)
	}); err != nil {
		t.Fatal(err)
	}
	if count != expectedDocuments {
		t.Fatalf("expected full frozen fixture, documents=%d", count)
	}
	ctx := context.Background()
	indexBefore, err := core.ScanFS(os.DirFS(util.DataDir), core.ApplicationRegistry())
	if err != nil || len(indexBefore.Locations) != expectedLocations {
		t.Fatalf("logical inventory=%v err=%v", indexBefore, err)
	}
	runtime, err := evergreen.NewRuntime(ctx, evergreen.RuntimeOptions{Root: util.DataDir})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	archiveBefore, err := runtime.CanonicalExport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	projectionBefore, err := runtime.RebuildAV(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rebuild := func() []string {
		InitDatabase(true)
		InitHistoryDatabase(true)
		InitAssetContentDatabase(true)
		treenode.InitBlockTree(true)
		IndexIgnoreCached, indexIgnore = true, nil
		ClearQueue()
		for _, entity := range archiveBefore.Entities {
			location := indexBefore.Locations[entity.LogicalID]
			box := strings.Split(location.Path, "/")[0]
			tree, err := filesys.LoadTreeByData(entity.Document, box, "/"+strings.TrimPrefix(location.Path, box+"/"), util.NewLute())
			if err != nil {
				t.Fatal(err)
			}
			treenode.IndexBlockTree(tree)
			IndexTreeQueue(tree)
		}
		FlushQueue()
		rows, err := db.Query("SELECT id FROM blocks ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		rows.Close()
		var hits int
		if err := db.QueryRow("SELECT COUNT(*) FROM blocks_fts WHERE blocks_fts MATCH 'Harness'").Scan(&hits); err != nil || hits == 0 {
			t.Fatalf("FTS not rebuilt: hits=%d err=%v", hits, err)
		}
		return ids
	}
	first := rebuild()
	CloseDatabase()
	cache.ClearAVCache()
	cache.ClearTreeCache()
	cache.ClearDocsIAL()
	cache.ClearBlocksIAL()
	ClearCache()
	runtime.DropAVDerived()
	for _, name := range []string{util.DBPath, util.BlockTreeDBPath, util.HistoryDBPath, util.AssetContentDBPath} {
		if err := os.Remove(name); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	second := rebuild()
	defer CloseDatabase()
	projectionAfter, err := runtime.RebuildAV(ctx)
	if err != nil {
		t.Fatal(err)
	}
	left, _ := json.Marshal(projectionBefore)
	right, _ := json.Marshal(projectionAfter)
	if !reflect.DeepEqual(first, second) || string(left) != string(right) {
		t.Fatal("derived rows or Claims/reverse projection changed after full deletion")
	}
	archiveAfter, err := runtime.CanonicalExport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	diff, err := core.DiffCanonical(archiveBefore, archiveAfter)
	if err != nil || len(diff) != 0 {
		t.Fatalf("rebuild changed authority: %v %v", diff, err)
	}
	t.Logf("rebuild parity: %d documents, %d logical locations, %d indexed blocks; FTS and AV/reverse rebuilt", count, len(indexBefore.Locations), len(first))
}
