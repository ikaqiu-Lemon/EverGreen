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

	"github.com/88250/lute/ast"
	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	"github.com/siyuan-note/siyuan/kernel/cache"
	kernelEvergreen "github.com/siyuan-note/siyuan/kernel/evergreen"
	"github.com/siyuan-note/siyuan/kernel/filesys"
	"github.com/siyuan-note/siyuan/kernel/sql"
	"github.com/siyuan-note/siyuan/kernel/treenode"
	"github.com/siyuan-note/siyuan/kernel/util"
)

func TestEvergreenCommittedProjectionRefreshAndRetry(t *testing.T) {
	if os.Getenv("SIYUAN_EG_PROJECTION_TEST") != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestEvergreenCommittedProjectionRefreshAndRetry$", "-test.timeout=80s", "-test.v")
		command.Env = append(os.Environ(), "SIYUAN_EG_PROJECTION_TEST=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("isolated projection: %v\n%s", err, output)
		}
		return
	}
	fixture := setupStructureTransactionTest(t)
	setupFoldTransactionDatabase(t, fixture)
	tree, err := LoadTreeByBlockID(fixture.sourceID)
	if err != nil {
		t.Fatal(err)
	}
	oldID := tree.Root.FirstChild.ID
	tree.Root.FirstChild.AppendChild(&ast.Node{Type: ast.NodeText, Tokens: []byte("beforeauthority")})
	if _, err = filesys.WriteTree(tree); err != nil {
		t.Fatal(err)
	}
	abs := filepath.Join(util.DataDir, fixture.box.ID, tree.Path)
	raw, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = core.EncodeSY(raw, &core.DocumentEnvelope{
		Spec:      core.DocumentSpec,
		Entity:    core.Entity{LogicalID: "c-projection", EntityType: core.EntityClaim, Schema: core.ClaimSchema, SemanticRevision: 1},
		Claim:     &core.Claim{ClaimKind: core.KnowledgeKind, KindSchema: core.KnowledgeKindSchema, Status: "active", KindData: json.RawMessage(`{}`)},
		Relations: core.Relations{Outgoing: []core.TypedEdge{}},
	}, core.ApplicationRegistry())
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte(`"spec":"evergreen.sy/v1"`),
		[]byte(`"spec":"evergreen.sy/v1","future":{"integer":9007199254740993}`), 1)
	if err = os.WriteFile(abs, raw, 0600); err != nil {
		t.Fatal(err)
	}
	cache.RemoveTreeDataInBox(tree.ID, tree.Box)
	oldTree, err := filesys.LoadTreeProjectionByData(raw, tree.Box, tree.Path, util.NewLute())
	if err != nil {
		t.Fatal(err)
	}
	if err = treenode.RefreshAuthorityBlockTree(oldTree); err != nil {
		t.Fatal(err)
	}
	if err = sql.RefreshAuthorityTree(oldTree); err != nil {
		t.Fatal(err)
	}
	if output, initErr := exec.Command("git", "-C", util.DataDir, "init", "-q").CombinedOutput(); initErr != nil {
		t.Fatalf("initialize isolated Git: %v %s", initErr, output)
	}
	committer, err := core.NewLocalGitCommitter(util.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	failRefresh := false
	refresh := func(ctx context.Context, ids []core.LogicalID) error {
		if failRefresh {
			db, err := databaseSQL.Open("sqlite3_extended", util.DBPath)
			if err != nil {
				return err
			}
			_, err = db.Exec("CREATE TRIGGER reject_projection BEFORE INSERT ON blocks BEGIN SELECT RAISE(ABORT, 'injected projection failure'); END")
			db.Close()
			if err != nil {
				return err
			}
		}
		return RefreshEvergreenProjection(ctx, ids)
	}
	runtime, err := kernelEvergreen.NewRuntime(context.Background(), kernelEvergreen.RuntimeOptions{
		Root: util.DataDir, Storage: filesys.EvergreenDocumentStorage{Root: util.DataDir},
		Committer: committer, IndexSink: kernelEvergreen.IndexSink{Root: util.DataDir, Refresh: refresh},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	user := core.Principal{Type: core.PrincipalUser, ID: "test-owner", AuthSource: "workspace-session"}
	const newID = "20261012120000-newpara"
	var object map[string]json.RawMessage
	if err = json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	object["Children"] = json.RawMessage(`[{"ID":"` + newID + `","Type":"NodeParagraph","Properties":{"id":"` +
		newID + `"},"Children":[{"Type":"NodeText","Data":"committedsearchneedle"}]}]`)
	after, _ := json.Marshal(object)
	document, err := core.DecodeSY(after, core.ApplicationRegistry())
	if err != nil {
		t.Fatal(err)
	}
	document.Envelope.Entity.SemanticRevision++
	after, err = core.EncodeSY(after, document.Envelope, core.ApplicationRegistry())
	if err != nil {
		t.Fatal(err)
	}
	base, _ := core.SemanticHash(raw)
	planned, err := runtime.Plan(context.Background(), user, core.PlanRequest{
		Protocol: core.ChangePlanProtocol, OperationID: "op-projection-refresh", Command: "claim.update",
		Base:   []core.BaseRef{{LogicalID: "c-projection", SemanticHash: base}},
		Writes: []core.WriteInput{{LogicalID: "c-projection", After: after}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// 延迟保存携带旧正文，提交后及再次 flush 均必须返回最新权威投影。
	sql.IndexTreeQueue(oldTree)
	sql.UpsertTreeQueue(oldTree)
	record, err := runtime.Apply(context.Background(), user, core.ApplyRequest{Operation: planned})
	if err != nil || record.DerivedStale {
		t.Fatalf("apply=%+v err=%v", record, err)
	}
	assertProjection := func() {
		t.Helper()
		if treenode.GetBlockTree(oldID) != nil || treenode.GetBlockTree(newID) == nil {
			t.Fatal("blocktree kept deleted blocks or lost committed blocks")
		}
		if block := sql.GetBlock(newID); block == nil || block.Content != "committedsearchneedle" {
			t.Fatalf("SQL did not refresh committed body: %+v", block)
		}
		db, err := databaseSQL.Open("sqlite3_extended", util.DBPath)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var hits, oldHits int
		if err = db.QueryRow("SELECT count(*) FROM blocks_fts WHERE blocks_fts MATCH 'committedsearchneedle'").Scan(&hits); err != nil || hits != 1 {
			t.Fatalf("committed FTS hits=%d err=%v", hits, err)
		}
		if err = db.QueryRow("SELECT count(*) FROM blocks_fts WHERE blocks_fts MATCH 'beforeauthority'").Scan(&oldHits); err != nil || oldHits != 0 {
			t.Fatalf("old FTS hits=%d err=%v", oldHits, err)
		}
		reopened, err := LoadTreeByBlockID(newID)
		if err != nil || reopened == nil || treenode.GetNodeInTree(reopened, oldID) != nil {
			t.Fatalf("reopened Protyle tree is stale: %v", err)
		}
	}
	assertProjection()
	sql.FlushQueue()
	assertProjection()
	committedBytes, _ := os.ReadFile(abs)
	if !bytes.Equal(committedBytes, planned.Images[0].Bytes) || !bytes.Contains(committedBytes, []byte("9007199254740993")) {
		t.Fatal("projection rewrote authority or lost unknown numbers")
	}
	git := func(args ...string) string {
		t.Helper()
		output, err := exec.Command("git", append([]string{"-C", util.DataDir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, output)
		}
		return string(output)
	}
	if count := strings.TrimSpace(git("rev-list", "--count", "HEAD")); count != "1" {
		t.Fatalf("operation commit count=%s", count)
	}
	if !strings.Contains(git("log", "-1", "--format=%B"), "Evergreen-Operation-ID: op-projection-refresh") {
		t.Fatal("unique commit lost the operation audit")
	}
	snapshot, err := runtime.InspectClaim(context.Background(), "c-projection")
	if err != nil {
		t.Fatal(err)
	}
	tags := []string{"retry"}
	second, err := runtime.PlanClaimEdit(context.Background(), user, core.ClaimEditRequest{
		OperationID: "op-projection-retry", LogicalID: "c-projection", Base: snapshot.Base, Edit: core.ClaimEdit{Tags: &tags},
	})
	if err != nil {
		t.Fatal(err)
	}
	failRefresh = true
	record, err = runtime.Apply(context.Background(), user, core.ApplyRequest{Operation: second})
	if err != nil || !record.DerivedStale || record.State != core.OperationCompleted {
		t.Fatalf("projection failure was not recorded: %+v %v", record, err)
	}
	failRefresh = false
	db, err := databaseSQL.Open("sqlite3_extended", util.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("DROP TRIGGER reject_projection"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	records, err := runtime.Recover(context.Background())
	if err != nil || len(records) != 1 || records[0].DerivedStale {
		t.Fatalf("projection retry=%+v err=%v", records, err)
	}
	if count := strings.TrimSpace(git("rev-list", "--count", "HEAD")); count != "2" {
		t.Fatalf("projection recovery duplicated Git commit: %s", count)
	}
	got, _ := os.ReadFile(abs)
	if !bytes.Equal(got, second.Images[0].Bytes) {
		t.Fatal("projection recovery rewrote authority")
	}
}
