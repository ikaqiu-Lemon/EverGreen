//go:build fts5

package model

import (
	"bytes"
	"context"
	databaseSQL "database/sql"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/88250/lute/ast"
	"github.com/gin-gonic/gin"
	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	"github.com/siyuan-note/siyuan/kernel/av"
	kernelEvergreen "github.com/siyuan-note/siyuan/kernel/evergreen"
	"github.com/siyuan-note/siyuan/kernel/filesys"
	"github.com/siyuan-note/siyuan/kernel/sql"
	"github.com/siyuan-note/siyuan/kernel/treenode"
	"github.com/siyuan-note/siyuan/kernel/util"
)

type nativeTransactionRenameFault struct {
	fired bool
}

func (f *nativeTransactionRenameFault) Fail(point string, index int) error {
	if !f.fired && point == core.FaultAfterRename && index == 0 {
		f.fired = true
		return errors.New("injected native transaction rename interruption")
	}
	return nil
}

func TestEvergreenNativeTransactionFaultRecovery(t *testing.T) {
	for _, scenario := range []string{"rename", "git", "sqlite"} {
		t.Run(scenario, func(t *testing.T) {
			if os.Getenv("SIYUAN_EG_NATIVE_TX_FAULT") != scenario {
				ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				defer cancel()
				command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestEvergreenNativeTransactionFaultRecovery$/^"+scenario+"$",
					"-test.timeout=80s", "-test.v")
				command.Env = append(os.Environ(), "SIYUAN_EG_NATIVE_TX_FAULT="+scenario)
				if output, err := command.CombinedOutput(); err != nil {
					t.Fatalf("isolated native fault: %v\n%s", err, output)
				}
				return
			}
			testNativeTransactionFaultRecovery(t, scenario)
		})
	}
}

func testNativeTransactionFaultRecovery(t *testing.T, scenario string) {
	fixture := setupStructureTransactionTest(t)
	setupFoldTransactionDatabase(t, fixture)
	GlobalUndoLog.Clear("")
	t.Cleanup(func() {
		kernelEvergreen.CloseDefaultRuntime()
		GlobalUndoLog.Clear("")
	})
	paths := []string{fixture.box.ID + fixture.sourcePath, fixture.box.ID + fixture.targetPath}
	before := [][]byte{}
	ids := []string{}
	for index, p := range paths {
		raw, err := os.ReadFile(filepath.Join(util.DataDir, p))
		if err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			raw, err = core.EncodeSY(raw, &core.DocumentEnvelope{
				Spec: core.DocumentSpec,
				Entity: core.Entity{LogicalID: "c-native-fault", EntityType: core.EntityClaim, Schema: core.ClaimSchema,
					SemanticRevision: 1},
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
		tree, err := filesys.LoadTreeProjection(fixture.box.ID, strings.TrimPrefix(p, fixture.box.ID), util.NewLute())
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, tree.Root.FirstChild.ID)
		before = append(before, raw)
	}
	unrelated := treenode.NewTree(fixture.box.ID, "/20261012140000-other01.sy", "/Unrelated", "Unrelated")
	if _, err := filesys.WriteTree(unrelated); err != nil {
		t.Fatal(err)
	}
	treenode.UpsertBlockTree(unrelated)
	if err := sql.RefreshAuthorityTree(unrelated); err != nil {
		t.Fatal(err)
	}
	unrelatedPath := filepath.Join(util.DataDir, unrelated.Box, unrelated.Path)
	unrelatedBefore, err := os.ReadFile(unrelatedPath)
	if err != nil {
		t.Fatal(err)
	}
	const unrelatedAVID = "20261012140001-otherav"
	unrelatedAV := &av.AttributeView{ID: unrelatedAVID, Name: "Original", Spec: av.CurrentSpec,
		KeyValues: []*av.KeyValues{{Key: &av.Key{ID: ast.NewNodeID(), Type: av.KeyTypeBlock}}},
		Views:     []*av.View{}}
	unrelatedAVBefore, err := av.MarshalAttributeViewPersistence(unrelatedAV)
	if err != nil {
		t.Fatal(err)
	}
	unrelatedAVPath := filepath.Join(util.DataDir, "storage", "av", unrelatedAVID+".json")
	if err = os.MkdirAll(filepath.Dir(unrelatedAVPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(unrelatedAVPath, unrelatedAVBefore, 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("git", "-C", util.DataDir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("Git setup: %v %s", err, output)
	}
	ctx := context.Background()
	options := kernelEvergreen.RuntimeOptions{
		Root: util.DataDir, Storage: filesys.EvergreenDocumentStorage{Root: util.DataDir},
		IndexSink: kernelEvergreen.IndexSink{Root: util.DataDir, RefreshDocuments: RefreshEvergreenDocumentProjection},
	}
	if scenario == "rename" {
		options.Faults = &nativeTransactionRenameFault{}
	}
	runtime, err := kernelEvergreen.DefaultRuntime(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	gitLock := filepath.Join(util.DataDir, ".git", "HEAD.lock")
	if scenario == "git" {
		if err = os.WriteFile(gitLock, []byte("native transaction Git lock fault"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if scenario == "sqlite" {
		db, err := databaseSQL.Open("sqlite3_extended", util.DBPath)
		if err != nil {
			t.Fatal(err)
		}
		_, err = db.Exec("CREATE TRIGGER reject_native_tx BEFORE INSERT ON blocks BEGIN SELECT RAISE(ABORT, 'native transaction index failure'); END")
		db.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	tx := &Transaction{}
	for index, id := range ids {
		dom := `<div data-node-id="` + id + `" data-type="NodeParagraph" class="p"><div contenteditable="true">` +
			[]string{"managedfaultbody", "ordinaryfaultbody"}[index] + `</div></div>`
		tx.DoOperations = append(tx.DoOperations, &Operation{Action: "update", ID: id, Data: dom})
		tx.UndoOperations = append(tx.UndoOperations, &Operation{Action: "update", ID: id, Data: dom})
	}
	tx.MarkFromAPI()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/api/transactions", nil)
	c.Set(RoleContextKey, RoleAdministrator)
	recordEvergreenUserAuthentication(c, "workspace-session")
	BindEvergreenTransactionIdentity(c, tx)
	err = PerformTxSync(tx)
	if (err != nil) != (scenario != "sqlite") {
		t.Fatalf("native fault completion outcome for %s: %v", scenario, err)
	}
	if scenario != "sqlite" && GlobalUndoLog.Peek(fixture.sourceID) != nil {
		t.Fatal("unfinished authority operation entered the undo stack")
	}
	applied := 0
	for index, p := range paths {
		raw, err := os.ReadFile(filepath.Join(util.DataDir, p))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(raw, before[index]) {
			applied++
			if !bytes.Contains(raw, []byte([]string{"managedfaultbody", "ordinaryfaultbody"}[index])) {
				t.Fatal("failure compensation overwrote journal after-image")
			}
		}
	}
	if scenario == "rename" && applied != 1 || scenario != "rename" && applied != 2 {
		t.Fatalf("fault did not exercise real authority stage: applied=%d", applied)
	}
	if scenario != "sqlite" {
		avAttempt := &Transaction{DoOperations: []*Operation{{Action: "setAttrViewName", ID: unrelatedAVID, Data: "Overwritten"}}}
		avAttempt.MarkFromAPI()
		BindEvergreenTransactionIdentity(c, avAttempt)
		if writeErr := PerformTxSync(avAttempt); writeErr == nil || !strings.Contains(writeErr.Error(), string(core.CodeTransactionBlocked)) {
			t.Fatalf("unfinished journal admitted an ordinary AV transaction: %v", writeErr)
		}
		actualAV, readErr := os.ReadFile(unrelatedAVPath)
		if readErr != nil || !bytes.Equal(actualAV, unrelatedAVBefore) {
			t.Fatal("rejected ordinary AV transaction changed auxiliary authority")
		}
		for _, action := range []string{"update", "delete"} {
			op := &Operation{Action: action, ID: unrelated.ID}
			if action == "update" {
				op.ID = unrelated.Root.FirstChild.ID
				node := treenode.NewParagraph(op.ID)
				node.AppendChild(&ast.Node{Type: ast.NodeText, Tokens: []byte("unrelated overwrite")})
				op.Data = util.NewLute().RenderNodeBlockDOM(node)
			}
			attempt := &Transaction{DoOperations: []*Operation{op}}
			attempt.MarkFromAPI()
			BindEvergreenTransactionIdentity(c, attempt)
			if writeErr := PerformTxSync(attempt); writeErr == nil || !strings.Contains(writeErr.Error(), string(core.CodeTransactionBlocked)) {
				t.Fatalf("unfinished journal admitted unrelated ordinary transaction %s: %v", action, writeErr)
			}
			actual, readErr := os.ReadFile(unrelatedPath)
			if readErr != nil || !bytes.Equal(actual, unrelatedBefore) || treenode.GetBlockTree(unrelated.ID) == nil ||
				sql.GetBlock(unrelated.ID) == nil || GlobalUndoLog.Peek(unrelated.ID) != nil {
				t.Fatal("rejected ordinary transaction changed authority, SQL, blocktree or undo")
			}
		}
	}
	if scenario == "git" {
		if err = os.Remove(gitLock); err != nil {
			t.Fatal(err)
		}
	}
	if scenario == "sqlite" {
		db, err := databaseSQL.Open("sqlite3_extended", util.DBPath)
		if err != nil {
			t.Fatal(err)
		}
		_, err = db.Exec("DROP TRIGGER reject_native_tx")
		db.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	records, err := RecoverEvergreenOperations(ctx, runtime)
	if err != nil || len(records) != 1 || records[0].State != core.OperationCompleted || records[0].DerivedStale {
		t.Fatalf("native host journal did not recover: %+v %v", records, err)
	}
	for index, id := range ids {
		want := []string{"managedfaultbody", "ordinaryfaultbody"}[index]
		if block := sql.GetBlock(id); block == nil || block.Content != want {
			t.Fatalf("recovery did not rebuild real SQL: %+v", block)
		}
	}
	count, err := exec.Command("git", "-C", util.DataDir, "rev-list", "--count", "HEAD").CombinedOutput()
	if err != nil || strings.TrimSpace(string(count)) != "1" {
		t.Fatalf("recovery duplicated or omitted native Git commit: %v %s", err, count)
	}
	records, err = RecoverEvergreenOperations(ctx, runtime)
	if err != nil || len(records) != 0 {
		t.Fatalf("completed native recovery is not idempotent: %+v %v", records, err)
	}
}
