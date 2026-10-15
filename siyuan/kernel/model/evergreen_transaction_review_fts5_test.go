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
	"github.com/gin-gonic/gin"
	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	kernelEvergreen "github.com/siyuan-note/siyuan/kernel/evergreen"
	"github.com/siyuan-note/siyuan/kernel/filesys"
	"github.com/siyuan-note/siyuan/kernel/treenode"
	"github.com/siyuan-note/siyuan/kernel/util"
)

func TestEvergreenNativeTransactionReviewStaleAndCAS(t *testing.T) {
	if os.Getenv("SIYUAN_EG_NATIVE_TX_REVIEW_TEST") != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestEvergreenNativeTransactionReviewStaleAndCAS$",
			"-test.timeout=80s", "-test.v")
		command.Env = append(os.Environ(), "SIYUAN_EG_NATIVE_TX_REVIEW_TEST=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("isolated native Note transaction: %v\n%s", err, output)
		}
		return
	}
	fixture := setupStructureTransactionTest(t)
	setupFoldTransactionDatabase(t, fixture)
	t.Cleanup(func() { kernelEvergreen.CloseDefaultRuntime() })
	tree, err := LoadTreeByBlockID(fixture.sourceID)
	if err != nil {
		t.Fatal(err)
	}
	segmentID := tree.Root.FirstChild.ID
	tree.Root.FirstChild.AppendChild(&ast.Node{Type: ast.NodeText, Tokens: []byte("originalsourcesegment")})
	candidateID := ast.NewNodeID()
	candidate := &ast.Node{ID: candidateID, Type: ast.NodeBlockquote}
	candidate.SetIALAttr("id", candidateID)
	candidate.AppendChild(&ast.Node{Type: ast.NodeBlockquoteMarker, Tokens: []byte(">")})
	payload := treenode.NewParagraph(ast.NewNodeID())
	payload.AppendChild(&ast.Node{Type: ast.NodeText, Tokens: []byte("candidatepayload")})
	candidate.AppendChild(payload)
	tree.Root.AppendChild(candidate)
	raw, err := filesys.RenderNativeTree(tree, nil)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]json.RawMessage
	var children []map[string]json.RawMessage
	_ = json.Unmarshal(raw, &root)
	_ = json.Unmarshal(root["Children"], &children)
	segmentRaw, _ := json.Marshal(children[0])
	segmentHash, err := core.NormalizedSegmentHash(segmentRaw)
	if err != nil {
		t.Fatal(err)
	}
	payloadHash, err := core.CandidateSubtreeHash(children[1]["Children"])
	if err != nil {
		t.Fatal(err)
	}
	segment, err := core.MarshalBlockEnvelope(&core.BlockEnvelope{
		Spec: core.BlockSpec, Role: "note_segment", Segment: &core.SegmentMetadata{
			SegmentID: "seg-native-review", SourceRef: core.SourceLocator{
				SourceID: "s-native-review", Locator: core.Locator{Kind: "source-block", Value: "body"}},
			NormalizedHash: segmentHash},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidateEnvelope, err := core.MarshalBlockEnvelope(&core.BlockEnvelope{
		Spec: core.BlockSpec, Role: "candidate", Candidate: &core.CandidateMetadata{
			CandidateID: "cand-native-review", ClaimKind: core.KnowledgeKind, KindSchema: core.KnowledgeKindSchema,
			SegmentRefs: []core.LogicalID{"seg-native-review"}, RefHashes: map[core.LogicalID]string{"seg-native-review": segmentHash},
			PayloadHash: payloadHash, State: "draft", Relation: "support", Reason: "source explains the reviewed fact"},
	})
	if err != nil {
		t.Fatal(err)
	}
	children[0]["Evergreen"], children[1]["Evergreen"] = segment, candidateEnvelope
	root["Children"], _ = json.Marshal(children)
	raw, _ = json.Marshal(root)
	raw, err = core.EncodeSY(raw, &core.DocumentEnvelope{
		Spec: core.DocumentSpec, Entity: core.Entity{LogicalID: "n-native-review", EntityType: core.EntityNote,
			Schema: "evergreen.note/v1", SemanticRevision: 1}, Relations: core.Relations{Outgoing: []core.TypedEdge{}},
		Review: &core.NoteReview{Spec: core.NoteReviewSpec, Coverage: []core.CoverageModule{{
			ModuleID: "coverage-native-review", Disposition: "candidate", SegmentRefs: []core.LogicalID{"seg-native-review"},
			CandidateID: "cand-native-review",
		}}},
	}, core.ApplicationRegistry())
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(util.DataDir, fixture.box.ID, fixture.sourcePath)
	if err = os.WriteFile(p, raw, 0600); err != nil {
		t.Fatal(err)
	}
	treenode.UpsertBlockTree(tree)
	if output, err := exec.Command("git", "-C", util.DataDir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("Git setup: %v %s", err, output)
	}
	if _, err = EvergreenRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
	bind := func(tx *Transaction) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/api/transactions", nil)
		c.Set(RoleContextKey, RoleAdministrator)
		recordEvergreenUserAuthentication(c, "workspace-session")
		BindEvergreenTransactionIdentity(c, tx)
	}
	update := func(text string) *Transaction {
		tx := &Transaction{DoOperations: []*Operation{{Action: "update", ID: segmentID,
			Data: `<div data-node-id="` + segmentID + `" data-type="NodeParagraph" class="p"><div contenteditable="true">` + text + `</div></div>`}}}
		bind(tx)
		return tx
	}
	tx := update("editedsourcesegment")
	if err = PerformTxSync(tx); err != nil {
		t.Fatalf("native Note edit failed: %v", err)
	}
	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	review, err := core.InspectNoteReview(after, core.ApplicationRegistry())
	if err != nil || len(review.Lineage) != 1 || len(review.Candidates) != 1 ||
		review.Candidates[0].Metadata.State != "stale" {
		t.Fatalf("native Note did not atomically update stale/lineage: %+v %v", review, err)
	}
	document, err := core.DecodeSY(after, core.ApplicationRegistry())
	if err != nil || document.Envelope.Entity.SemanticRevision != 2 {
		t.Fatalf("native Note revision did not advance once: %v", err)
	}
	tx = update("staleattempt")
	tx.m = &sync.Mutex{}
	flushLock.Lock()
	if err = tx.begin(); err != nil {
		flushLock.Unlock()
		t.Fatal(err)
	}
	if err = tx.prepareNativeTransaction(); err == nil {
		if txErr := tx.doUpdate(tx.DoOperations[0]); txErr != nil {
			err = txErr
		}
	}
	if err != nil {
		tx.rollback()
		flushLock.Unlock()
		t.Fatal(err)
	}
	external := bytes.ReplaceAll(after, []byte("editedsourcesegment"), []byte("externalcurrentbody"))
	if err = os.WriteFile(p, external, 0600); err != nil {
		tx.rollback()
		flushLock.Unlock()
		t.Fatal(err)
	}
	err = tx.commit()
	tx.rollback()
	flushLock.Unlock()
	if !core.HasDiagnostic(err, core.CodeBaseMismatch) {
		t.Fatalf("transaction did not compare original authority version: %v", err)
	}
	retained, _ := os.ReadFile(p)
	if !bytes.Equal(retained, external) {
		t.Fatal("stale native transaction overwrote current authority")
	}
	count, err := exec.Command("git", "-C", util.DataDir, "rev-list", "--count", "HEAD").CombinedOutput()
	if err != nil || strings.TrimSpace(string(count)) != "1" {
		t.Fatalf("stale native transaction created a Git commit: %v %s", err, count)
	}
}
