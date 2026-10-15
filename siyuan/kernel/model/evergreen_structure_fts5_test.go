//go:build fts5

package model

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/88250/lute/ast"
	"github.com/88250/lute/parse"
	"github.com/gin-gonic/gin"
	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	kernelEvergreen "github.com/siyuan-note/siyuan/kernel/evergreen"
	"github.com/siyuan-note/siyuan/kernel/filesys"
	"github.com/siyuan-note/siyuan/kernel/sql"
	"github.com/siyuan-note/siyuan/kernel/treenode"
	"github.com/siyuan-note/siyuan/kernel/util"
)

func TestEvergreenNativeStructureAuthorityAndReplay(t *testing.T) {
	for _, scenario := range []string{"insert", "move", "delete", "swap", "rename", "rejected"} {
		t.Run(scenario, func(t *testing.T) {
			if os.Getenv("SIYUAN_EG_STRUCTURE_TEST") != scenario {
				ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				defer cancel()
				command := exec.CommandContext(ctx, os.Args[0],
					"-test.run=^TestEvergreenNativeStructureAuthorityAndReplay$/^"+scenario+"$", "-test.timeout=80s", "-test.v")
				command.Env = append(os.Environ(), "SIYUAN_EG_STRUCTURE_TEST="+scenario)
				if output, err := command.CombinedOutput(); err != nil {
					t.Fatalf("isolated structure transaction: %v\n%s", err, output)
				}
				return
			}
			testEvergreenNativeStructure(t, scenario)
		})
	}
}

func testEvergreenNativeStructure(t *testing.T, scenario string) {
	fixture := setupStructureTransactionTest(t)
	setupFoldTransactionDatabase(t, fixture)
	GlobalUndoLog.Clear("")
	t.Cleanup(func() {
		kernelEvergreen.CloseDefaultRuntime()
		GlobalUndoLog.Clear("")
	})
	read := func(p string) []byte {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(util.DataDir, fixture.box.ID, p))
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	original := read(fixture.sourcePath)
	var root map[string]json.RawMessage
	if err := json.Unmarshal(original, &root); err != nil {
		t.Fatal(err)
	}
	var children []map[string]json.RawMessage
	if err := json.Unmarshal(root["Children"], &children); err != nil {
		t.Fatal(err)
	}
	children[0]["FutureMovedField"] = json.RawMessage(`{"sequence":9007199254740993}`)
	root["Children"], _ = json.Marshal(children)
	original, _ = json.Marshal(root)
	original, err := core.EncodeSY(original, &core.DocumentEnvelope{
		Spec: core.DocumentSpec,
		Entity: core.Entity{LogicalID: "c-native-structure", EntityType: core.EntityClaim,
			Schema: core.ClaimSchema, SemanticRevision: 1},
		Claim: &core.Claim{ClaimKind: core.KnowledgeKind, KindSchema: core.KnowledgeKindSchema,
			Status: "active", KindData: json.RawMessage(`{}`)},
		Relations: core.Relations{Outgoing: []core.TypedEdge{}},
	}, core.ApplicationRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(util.DataDir, fixture.box.ID, fixture.sourcePath), original, 0600); err != nil {
		t.Fatal(err)
	}
	target, err := LoadTreeByBlockID(fixture.targetID)
	if err != nil {
		t.Fatal(err)
	}
	targetBlock := target.Root.FirstChild.ID
	refID := ast.NewNodeID()
	if scenario == "move" || scenario == "rename" {
		ref := treenode.NewParagraph(refID)
		ref.AppendChild(&ast.Node{Type: ast.NodeTextMark, TextMarkType: "block-ref",
			TextMarkBlockRefID: fixture.childID, TextMarkBlockRefSubtype: "d", TextMarkTextContent: ""})
		target.Root.AppendChild(ref)
		if _, err = filesys.WriteTree(target); err != nil {
			t.Fatal(err)
		}
		treenode.UpsertBlockTree(target)
	}
	source, err := filesys.LoadTreeProjection(fixture.box.ID, fixture.sourcePath, util.NewLute())
	if err != nil {
		t.Fatal(err)
	}
	for _, tree := range []*parse.Tree{source, target} {
		if err = sql.RefreshAuthorityTree(tree); err != nil {
			t.Fatal(err)
		}
	}
	targetRaw := read(fixture.targetPath)
	var targetRoot map[string]json.RawMessage
	if err = json.Unmarshal(targetRaw, &targetRoot); err != nil {
		t.Fatal(err)
	}
	var targetChildren []map[string]json.RawMessage
	if err = json.Unmarshal(targetRoot["Children"], &targetChildren); err != nil {
		t.Fatal(err)
	}
	targetChildren[0]["FutureRestoredField"] = json.RawMessage(`{"sequence":9007199254740995}`)
	targetRoot["Children"], _ = json.Marshal(targetChildren)
	targetRaw, _ = json.Marshal(targetRoot)
	if err = os.WriteFile(filepath.Join(util.DataDir, fixture.box.ID, fixture.targetPath), targetRaw, 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("git", "-C", util.DataDir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("Git init: %v %s", err, output)
	}
	options := kernelEvergreen.RuntimeOptions{Root: util.DataDir,
		Storage:   filesys.EvergreenDocumentStorage{Root: util.DataDir},
		IndexSink: kernelEvergreen.IndexSink{Root: util.DataDir, RefreshDocuments: RefreshEvergreenDocumentProjection}}
	if scenario == "rename" {
		options.Faults = &nativeTransactionRenameFault{}
	}
	runtime, err := kernelEvergreen.DefaultRuntime(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	bind := func(tx *Transaction) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/api/transactions", nil)
		c.Set(RoleContextKey, RoleAdministrator)
		recordEvergreenUserAuthentication(c, "workspace-session")
		BindEvergreenTransactionIdentity(c, tx)
	}
	dom := func(id, text string) string {
		node := treenode.NewParagraph(id)
		node.AppendChild(&ast.Node{Type: ast.NodeText, Tokens: []byte(text)})
		return util.NewLute().RenderNodeBlockDOM(node)
	}
	count := func(expected string) {
		t.Helper()
		output, err := exec.Command("git", "-C", util.DataDir, "rev-list", "--count", "HEAD").CombinedOutput()
		if err != nil || strings.TrimSpace(string(output)) != expected {
			t.Fatalf("expected %s Git commits: %v %s", expected, err, output)
		}
	}
	apply := func(tx *Transaction) {
		t.Helper()
		bind(tx)
		tx.MarkFromAPI()
		if err := PerformTxSync(tx); err != nil {
			t.Fatalf("native %s failed: %v", scenario, err)
		}
	}
	assertPosition := func(id, rootID string) {
		t.Helper()
		bt := treenode.GetBlockTree(id)
		block := sql.GetBlock(id)
		if bt == nil || bt.RootID != rootID || block == nil || block.RootID != rootID {
			t.Fatalf("authority projection has wrong root for %s: blocktree=%+v SQL=%+v", id, bt, block)
		}
	}
	assertRefRoot := func(rootID string) {
		t.Helper()
		refs := sql.QueryRefsByDefIDsInBox([]string{fixture.childID}, fixture.box.ID)
		if len(refs) != 1 || refs[0].BlockID != refID || refs[0].DefBlockRootID != rootID {
			t.Fatalf("unchanged dynamic anchor retained wrong moved definition root: %+v", refs)
		}
	}
	replay := func(undo bool) {
		t.Helper()
		var entry *UndoEntry
		var tx *Transaction
		if undo {
			entry = GlobalUndoLog.Undo(fixture.sourceID)
			if entry == nil {
				t.Fatal("missing native undo")
			}
			tx = &Transaction{DoOperations: entry.UndoOperationsForReplay(), UndoOperations: entry.DoOperationsForReplay()}
		} else {
			entry = GlobalUndoLog.Redo(fixture.sourceID)
			if entry == nil {
				t.Fatal("missing native redo")
			}
			tx = &Transaction{DoOperations: entry.DoOperationsForReplay(), UndoOperations: entry.UndoOperationsForReplay()}
		}
		tx.MarkReplay()
		apply(tx)
		if undo {
			GlobalUndoLog.UndoCommit(entry, fixture.sourceID)
		} else {
			GlobalUndoLog.RedoCommit(entry, fixture.sourceID)
		}
	}
	switch scenario {
	case "insert":
		first, second := ast.NewNodeID(), ast.NewNodeID()
		tx := &Transaction{DoOperations: []*Operation{
			{Action: "insert", ID: first, ParentID: fixture.sourceID, PreviousID: fixture.childID, Data: dom(first, "firstinsert")},
			{Action: "insert", ID: second, PreviousID: first, Data: dom(second, "secondinsert")},
		}, UndoOperations: []*Operation{{Action: "delete", ID: second}, {Action: "delete", ID: first}}}
		apply(tx)
		assertPosition(first, fixture.sourceID)
		assertPosition(second, fixture.sourceID)
		count("1")
		replay(true)
		if treenode.GetBlockTree(first) != nil || sql.GetBlock(second) != nil {
			t.Fatal("undo left deleted inserted blocks in actual indexes")
		}
		replay(false)
		assertPosition(second, fixture.sourceID)
		count("3")
	case "move", "rename":
		tx := &Transaction{DoOperations: []*Operation{{Action: "move", ID: fixture.childID, ParentID: fixture.targetID}},
			UndoOperations: []*Operation{{Action: "move", ID: fixture.childID, ParentID: fixture.sourceID}}}
		if scenario == "rename" {
			bind(tx)
			if err = PerformTxSync(tx); err == nil {
				t.Fatal("rename interruption was reported as success")
			}
			records, err := RecoverEvergreenOperations(context.Background(), runtime)
			if err != nil || len(records) != 1 || records[0].State != core.OperationCompleted {
				t.Fatalf("structural recovery failed: %+v %v", records, err)
			}
		} else {
			apply(tx)
		}
		assertPosition(fixture.childID, fixture.targetID)
		assertRefRoot(fixture.targetID)
		if !bytes.Contains(read(fixture.targetPath), []byte("9007199254740993")) {
			t.Fatal("cross-document move lost unknown node number")
		}
		count("1")
		if scenario == "move" {
			replay(true)
			assertPosition(fixture.childID, fixture.sourceID)
			assertRefRoot(fixture.sourceID)
			if !bytes.Contains(read(fixture.sourcePath), []byte("9007199254740993")) {
				t.Fatal("undo lost moved unknown node field")
			}
			replay(false)
			assertPosition(fixture.childID, fixture.targetID)
			assertRefRoot(fixture.targetID)
			count("3")
		}
	case "delete":
		tx := &Transaction{DoOperations: []*Operation{
			{Action: "update", ID: fixture.childID, Data: dom(fixture.childID, "deletecompanion")},
			{Action: "delete", ID: targetBlock},
		}, UndoOperations: []*Operation{
			{Action: "insert", ID: targetBlock, ParentID: fixture.targetID, Data: dom(targetBlock, "ordinaryrestored")},
			{Action: "update", ID: fixture.childID, Data: dom(fixture.childID, "original")},
		}}
		apply(tx)
		if treenode.GetBlockTree(targetBlock) != nil || sql.GetBlock(targetBlock) != nil {
			t.Fatal("native deletion left ordinary block in SQL/blocktree")
		}
		replay(true)
		assertPosition(targetBlock, fixture.targetID)
		if !bytes.Contains(read(fixture.targetPath), []byte("9007199254740995")) {
			t.Fatal("delete undo lost unknown node data from private history")
		}
		replay(false)
		count("3")
	case "swap":
		tx := &Transaction{DoOperations: []*Operation{{Action: "swapBlockRef", ID: fixture.childID, BlockID: targetBlock,
			Data: map[string]bool{"includeChildren": false, "originalToEmbed": true}}}}
		apply(tx)
		assertPosition(fixture.childID, fixture.targetID)
		assertPosition(targetBlock, fixture.sourceID)
		count("1")
		replay(true)
		assertPosition(fixture.childID, fixture.sourceID)
		replay(false)
		assertPosition(targetBlock, fixture.sourceID)
		count("3")
	case "rejected":
		beforeTarget := read(fixture.targetPath)
		tx := &Transaction{DoOperations: []*Operation{{Action: "delete", ID: targetBlock},
			{Action: "insert", ID: fixture.childID, ParentID: fixture.sourceID, Data: dom(fixture.childID, "duplicate")}},
			m: &sync.Mutex{}}
		bind(tx)
		if err = tx.begin(); err != nil {
			t.Fatal(err)
		}
		if err = tx.prepareNativeTransaction(); err != nil {
			tx.rollback()
			t.Fatal(err)
		}
		if txErr := tx.doDelete(tx.DoOperations[0]); txErr != nil {
			tx.rollback()
			t.Fatal(txErr)
		}
		assertPosition(targetBlock, fixture.targetID)
		if txErr := tx.doInsert(tx.DoOperations[1]); txErr == nil {
			tx.rollback()
			t.Fatal("duplicate insert was admitted")
		}
		tx.rollback()
		if !bytes.Equal(original, read(fixture.sourcePath)) || !bytes.Equal(beforeTarget, read(fixture.targetPath)) {
			t.Fatal("rejected transaction changed authority")
		}
		assertPosition(targetBlock, fixture.targetID)
		if _, err := os.Stat(filepath.Join(util.DataDir, ".git", "refs", "heads", "master")); !os.IsNotExist(err) {
			t.Fatal("rejected transaction created a Git commit")
		}
	}
}
