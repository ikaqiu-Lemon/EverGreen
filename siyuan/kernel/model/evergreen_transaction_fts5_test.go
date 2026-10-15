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
	"testing"
	"time"

	"github.com/88250/lute/ast"
	"github.com/gin-gonic/gin"
	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	"github.com/siyuan-note/siyuan/kernel/cache"
	kernelEvergreen "github.com/siyuan-note/siyuan/kernel/evergreen"
	"github.com/siyuan-note/siyuan/kernel/filesys"
	"github.com/siyuan-note/siyuan/kernel/sql"
	"github.com/siyuan-note/siyuan/kernel/treenode"
	"github.com/siyuan-note/siyuan/kernel/util"
)

func TestEvergreenNativeTransactionAuthorityAndReplay(t *testing.T) {
	if os.Getenv("SIYUAN_EG_NATIVE_TX_TEST") != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestEvergreenNativeTransactionAuthorityAndReplay$",
			"-test.timeout=80s", "-test.v")
		command.Env = append(os.Environ(), "SIYUAN_EG_NATIVE_TX_TEST=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("isolated native transaction: %v\n%s", err, output)
		}
		return
	}
	fixture := setupStructureTransactionTest(t)
	setupFoldTransactionDatabase(t, fixture)
	GlobalUndoLog.Clear("")
	t.Cleanup(func() {
		kernelEvergreen.CloseDefaultRuntime()
		GlobalUndoLog.Clear("")
	})
	tree, err := LoadTreeByBlockID(fixture.sourceID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(util.DataDir, tree.Box, tree.Path))
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]json.RawMessage
	if err = json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	root["FutureNativeField"] = json.RawMessage(`{"revision":9007199254740993}`)
	var children []map[string]json.RawMessage
	if err = json.Unmarshal(root["Children"], &children); err != nil {
		t.Fatal(err)
	}
	children[0]["Evergreen"] = json.RawMessage(`{"spec":"evergreen.block/v1","role":"note_segment",` +
		`"segment":{"segment_id":"seg-native-body","source_ref":{"source_id":"s-native-body",` +
		`"locator":{"kind":"source-block","value":"body"}},"normalized_hash":"sha256:original"}}`)
	root["Children"], _ = json.Marshal(children)
	raw, _ = json.Marshal(root)
	raw, err = core.EncodeSY(raw, &core.DocumentEnvelope{
		Spec: core.DocumentSpec,
		Entity: core.Entity{LogicalID: "c-native-transaction", EntityType: core.EntityClaim,
			Schema: core.ClaimSchema, SemanticRevision: 1},
		Claim: &core.Claim{ClaimKind: core.KnowledgeKind, KindSchema: core.KnowledgeKindSchema,
			Status: "active", KindData: json.RawMessage(`{}`)},
		Relations: core.Relations{Outgoing: []core.TypedEdge{}},
	}, core.ApplicationRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(util.DataDir, tree.Box, tree.Path), raw, 0600); err != nil {
		t.Fatal(err)
	}
	target, err := LoadTreeByBlockID(fixture.targetID)
	if err != nil {
		t.Fatal(err)
	}
	targetBlockID := target.Root.FirstChild.ID
	refParagraph := treenode.NewParagraph(ast.NewNodeID())
	refParagraph.AppendChild(&ast.Node{Type: ast.NodeTextMark, TextMarkType: "block-ref",
		TextMarkBlockRefID: fixture.childID, TextMarkBlockRefSubtype: "d", TextMarkTextContent: "initial"})
	target.Root.AppendChild(refParagraph)
	if _, err = filesys.WriteTree(target); err != nil {
		t.Fatal(err)
	}
	treenode.UpsertBlockTree(target)
	if err = sql.RefreshAuthorityTree(target); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("git", "-C", util.DataDir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("Git setup: %v %s", err, output)
	}
	ctx := context.Background()
	runtime, err := EvergreenRuntime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	dom := func(id, text string) string {
		node := treenode.NewParagraph(id)
		node.AppendChild(&ast.Node{Type: ast.NodeText, Tokens: []byte(text)})
		return util.NewLute().RenderNodeBlockDOM(node)
	}
	bind := func(tx *Transaction, source string) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/api/transactions", nil)
		c.Set(RoleContextKey, RoleAdministrator)
		recordEvergreenUserAuthentication(c, source)
		BindEvergreenTransactionIdentity(c, tx)
	}
	apply := func(text, ordinary string) *Transaction {
		tx := &Transaction{
			DoOperations: []*Operation{
				{Action: "update", ID: fixture.childID, Data: dom(fixture.childID, text)},
				{Action: "update", ID: targetBlockID, Data: dom(targetBlockID, ordinary)},
			},
			UndoOperations: []*Operation{
				{Action: "update", ID: fixture.childID, Data: dom(fixture.childID, "original")},
				{Action: "update", ID: targetBlockID, Data: dom(targetBlockID, "originalordinary")},
			},
		}
		bind(tx, "workspace-session")
		tx.MarkFromAPI()
		return tx
	}
	assertProjection := func(id, text string) {
		t.Helper()
		block := sql.GetBlock(id)
		if block == nil || block.Content != text || treenode.GetBlockTree(id) == nil {
			t.Fatalf("native commit did not refresh real SQL/blocktree: %+v", block)
		}
	}
	tx := apply("managednativebody", "ordinarynativebody")
	if err = PerformTxSync(tx); err != nil {
		t.Fatalf("verified native mixed transaction failed: %v", err)
	}
	assertProjection(fixture.childID, "managednativebody")
	assertProjection(targetBlockID, "ordinarynativebody")
	refTree, err := filesys.LoadTreeProjection(target.Box, target.Path, util.NewLute())
	if err != nil || treenode.GetNodeInTree(refTree, refParagraph.ID).FirstChild.TextMarkTextContent != "managednativebody" {
		t.Fatalf("dynamic reference did not share the native authority commit: %v", err)
	}
	readManaged := func() []byte {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(util.DataDir, tree.Box, tree.Path))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	first := readManaged()
	if !bytes.Contains(first, []byte("9007199254740993")) || bytes.Contains(first, []byte(kernelEvergreen.CarrierAttribute)) {
		t.Fatal("native transaction lost unknown integer or persisted a private carrier")
	}
	if !bytes.Contains(first, []byte(`"segment_id":"seg-native-body"`)) {
		t.Fatal("BlockDOM update dropped trusted managed segment metadata")
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
		t.Fatal("mixed native transaction did not produce exactly one Git commit")
	}
	entry := GlobalUndoLog.Undo(fixture.sourceID)
	if entry == nil || len(entry.MutatedRootIDs()) != 2 {
		t.Fatal("native mixed transaction did not enter both actual undo stacks")
	}
	replay := &Transaction{DoOperations: entry.UndoOperationsForReplay(), UndoOperations: entry.DoOperationsForReplay()}
	replay.MarkReplay()
	bind(replay, "api-token")
	if err = PerformTxSync(replay); err == nil {
		t.Fatal("API token inherited original human identity during undo")
	}
	GlobalUndoLog.UndoRollback(entry, fixture.sourceID)
	if !bytes.Equal(first, readManaged()) || gitCount() != "1" || GlobalUndoLog.Peek(fixture.sourceID) != entry {
		t.Fatal("rejected replay changed authority, Git or undo stack")
	}
	entry = GlobalUndoLog.Undo(fixture.sourceID)
	replay = &Transaction{DoOperations: entry.UndoOperationsForReplay(), UndoOperations: entry.DoOperationsForReplay()}
	replay.MarkReplay()
	bind(replay, "workspace-session")
	if err = PerformTxSync(replay); err != nil {
		t.Fatalf("native undo failed: %v", err)
	}
	GlobalUndoLog.UndoCommit(entry, fixture.sourceID)
	assertProjection(fixture.childID, "original")
	assertProjection(targetBlockID, "originalordinary")
	entry = GlobalUndoLog.Redo(fixture.sourceID)
	replay = &Transaction{DoOperations: entry.DoOperationsForReplay(), UndoOperations: entry.UndoOperationsForReplay()}
	replay.MarkReplay()
	bind(replay, "workspace-session")
	if err = PerformTxSync(replay); err != nil {
		t.Fatalf("native redo failed: %v", err)
	}
	GlobalUndoLog.RedoCommit(entry, fixture.sourceID)
	assertProjection(fixture.childID, "managednativebody")
	assertProjection(targetBlockID, "ordinarynativebody")
	if gitCount() != "3" {
		t.Fatal("native edit, undo and redo did not each produce one Git commit")
	}
	beforeRejected := readManaged()
	attack := apply("forged", "forgedordinary")
	attack.DoOperations[0].Data = strings.Replace(dom(fixture.childID, "forged"), "<div ",
		`<div `+kernelEvergreen.CarrierAttribute+`="forged" `, 1)
	if err = PerformTxSync(attack); err == nil || !bytes.Equal(beforeRejected, readManaged()) || gitCount() != "3" {
		t.Fatal("forged native carrier was not rejected before authority writes")
	}
	attack = apply("unsupported", "unsupportedordinary")
	attack.DoOperations = append(attack.DoOperations, &Operation{Action: "create", ID: targetBlockID})
	if err = PerformTxSync(attack); err == nil || !bytes.Equal(beforeRejected, readManaged()) {
		t.Fatal("unsupported auxiliary action was not rejected before the first edit")
	}
	cache.RemoveTreeDataInBox(fixture.sourceID, fixture.box.ID)
	snapshot, err := filesys.LoadTreeProjection(fixture.box.ID, tree.Path, util.NewLute())
	if err != nil || !kernelEvergreen.HasEnvelope(snapshot.Root) {
		t.Fatalf("committed native metadata cannot reload from authority: %v", err)
	}
	visible := renderCleanBlockDOMByNodes([]*ast.Node{snapshot.Root.FirstChild}, util.NewLute())
	if strings.Contains(visible, kernelEvergreen.CarrierAttribute) ||
		!kernelEvergreen.HasEnvelope(snapshot.Root.FirstChild) {
		t.Fatal("editor rendering exposed or removed the trusted internal segment carrier")
	}
	records, err := RecoverEvergreenOperations(ctx, runtime)
	if err != nil || len(records) != 0 || gitCount() != "3" {
		t.Fatalf("completed native transaction recovery repeated commits: %+v %v", records, err)
	}
	sourceDocument, err := core.DecodeSY(beforeRejected, core.ApplicationRegistry())
	if err != nil {
		t.Fatal(err)
	}
	sourceDocument.Envelope.Entity.EntityType = core.EntitySource
	sourceDocument.Envelope.Entity.LogicalID = "s-native-transaction"
	sourceDocument.Envelope.Entity.Schema = "evergreen.source/v1"
	sourceDocument.Envelope.Claim = nil
	sourceRaw, err := core.EncodeSY(beforeRejected, sourceDocument.Envelope, core.ApplicationRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(util.DataDir, tree.Box, tree.Path), sourceRaw, 0600); err != nil {
		t.Fatal(err)
	}
	attack = apply("sourcecannotedit", "sourcecannoteditordinary")
	if err = PerformTxSync(attack); err == nil || !bytes.Equal(sourceRaw, readManaged()) || gitCount() != "3" {
		t.Fatal("native Source edit was admitted or changed authority")
	}
}
