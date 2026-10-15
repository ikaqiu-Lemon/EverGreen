//go:build fts5

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/88250/lute/ast"
	ginSessions "github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	"github.com/siyuan-note/siyuan/kernel/conf"
	kernelEvergreen "github.com/siyuan-note/siyuan/kernel/evergreen"
	"github.com/siyuan-note/siyuan/kernel/filesys"
	"github.com/siyuan-note/siyuan/kernel/model"
	"github.com/siyuan-note/siyuan/kernel/sql"
	"github.com/siyuan-note/siyuan/kernel/treenode"
	"github.com/siyuan-note/siyuan/kernel/util"
)

func TestEvergreenNativeTransactionHTTPAndHistory(t *testing.T) {
	if os.Getenv("SIYUAN_EG_NATIVE_TX_HTTP_TEST") != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestEvergreenNativeTransactionHTTPAndHistory$",
			"-test.timeout=80s", "-test.v")
		command.Env = append(os.Environ(), "SIYUAN_EG_NATIVE_TX_HTTP_TEST=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("isolated native transaction HTTP: %v\n%s", err, output)
		}
		return
	}
	root := t.TempDir()
	util.DataDir, util.TempDir, util.ConfDir = filepath.Join(root, "data"), root, filepath.Join(root, "conf")
	util.QueueDir, util.HistoryDir = filepath.Join(root, "queue"), filepath.Join(root, "history")
	util.DBPath, util.HistoryDBPath = filepath.Join(root, "siyuan.db"), filepath.Join(root, "history.db")
	util.AssetContentDBPath, util.BlockTreeDBPath = filepath.Join(root, "asset_content.db"), filepath.Join(root, "blocktree.db")
	model.Conf = model.NewAppConf()
	model.Conf.Lang = "en"
	model.Conf.Api = conf.NewAPI()
	model.Conf.Api.Token = "native-api-token"
	model.Conf.AccessAuthCode = "native-human-password"
	model.Conf.FileTree, model.Conf.Sync, model.Conf.NotebookCrypto = conf.NewFileTree(), conf.NewSync(), conf.NewNotebookCrypto()
	model.Conf.Editor, model.Conf.Export, model.Conf.Search = conf.NewEditor(), conf.NewExport(), conf.NewSearch()
	lang, err := os.ReadFile(filepath.Join("..", "..", "app", "appearance", "langs", "en.json"))
	if err != nil {
		t.Fatal(err)
	}
	var language struct {
		Time map[string]any `json:"_time"`
	}
	if err = json.Unmarshal(lang, &language); err != nil {
		t.Fatal(err)
	}
	util.TimeLangs["en"] = language.Time
	box := &model.Box{ID: ast.NewNodeID()}
	boxConf := conf.NewBoxConf()
	boxConf.Closed = false
	if err = box.SaveConf(boxConf); err != nil {
		t.Fatal(err)
	}
	treenode.InitBlockTree(true)
	sql.InitDatabase(true)
	sql.InitHistoryDatabase(true)
	sql.InitAssetContentDatabase(true)
	t.Cleanup(func() {
		model.FlushTxQueue()
		kernelEvergreen.CloseDefaultRuntime()
		model.GlobalUndoLog.Clear("")
		sql.CloseDatabase()
		treenode.CloseDatabase()
	})
	tree := treenode.NewTree(box.ID, "/"+ast.NewNodeID()+".sy", "/Native HTTP", "Native HTTP")
	blockID := tree.Root.FirstChild.ID
	if _, err = filesys.WriteTree(tree); err != nil {
		t.Fatal(err)
	}
	treenode.UpsertBlockTree(tree)
	p := filepath.Join(util.DataDir, box.ID, tree.Path)
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var rootJSON map[string]json.RawMessage
	var children []map[string]json.RawMessage
	if err = json.Unmarshal(raw, &rootJSON); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(rootJSON["Children"], &children); err != nil {
		t.Fatal(err)
	}
	children[0]["Evergreen"] = json.RawMessage(`{"spec":"evergreen.block/v1","role":"note_segment",` +
		`"segment":{"segment_id":"seg-http-body","source_ref":{"source_id":"s-http-body",` +
		`"locator":{"kind":"source-block","value":"body"}},"normalized_hash":"sha256:original"}}`)
	rootJSON["Children"], _ = json.Marshal(children)
	raw, _ = json.Marshal(rootJSON)
	raw, err = core.EncodeSY(raw, &core.DocumentEnvelope{
		Spec: core.DocumentSpec, Entity: core.Entity{LogicalID: "c-native-http", EntityType: core.EntityClaim,
			Schema: core.ClaimSchema, SemanticRevision: 1},
		Claim: &core.Claim{ClaimKind: core.KnowledgeKind, KindSchema: core.KnowledgeKindSchema,
			Status: "active", KindData: json.RawMessage(`{}`)}, Relations: core.Relations{Outgoing: []core.TypedEdge{}},
	}, core.ApplicationRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(p, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("git", "-C", util.DataDir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("Git setup: %v %s", err, output)
	}
	util.SetBooted()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(ginSessions.Sessions("siyuan", cookie.NewStore([]byte("native-http-session-test-key"))))
	router.Use(boxLeaseMiddleware)
	router.POST("/api/transactions", model.CheckAuth, performTransactions)
	router.POST("/api/transactions/undo", model.CheckAuth, performUndo)
	router.POST("/api/transactions/redo", model.CheckAuth, performRedo)
	dom := func(text string) string {
		return `<div data-node-id="` + blockID + `" data-type="NodeParagraph" class="p">` +
			`<div contenteditable="true">` + text + `</div></div>`
	}
	post := func(endpoint string, payload any, human bool) *httptest.ResponseRecorder {
		t.Helper()
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
		request.RemoteAddr = "192.0.2.1:12345"
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Evergreen-Caller", "ui")
		if human {
			request.SetBasicAuth(util.WorkspaceName, "native-human-password")
		} else {
			request.Header.Set("Authorization", "Token native-api-token")
		}
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		requireAPIContract(t, http.MethodPost, endpoint, recorder)
		return recorder
	}
	payload := func(text string) map[string]any {
		return map[string]any{"reqId": 1, "app": "native-http", "session": "native-http", "transactions": []*model.Transaction{{
			DoOperations:   []*model.Operation{{Action: "update", ID: blockID, Data: dom(text)}},
			UndoOperations: []*model.Operation{{Action: "update", ID: blockID, Data: dom("originalnativehttp")}},
		}}}
	}
	rejected := post("/api/transactions", payload("tokencannotedit"), false)
	if !strings.Contains(rejected.Body.String(), `"code":-1`) {
		t.Fatalf("HTTP returned success for rejected native identity: %s", rejected.Body)
	}
	current, _ := os.ReadFile(p)
	if !bytes.Equal(current, raw) || model.GlobalUndoLog.Peek(tree.ID) != nil {
		t.Fatal("rejected HTTP transaction changed authority or undo history")
	}
	accepted := post("/api/transactions", payload("acceptednativehttp"), true)
	if !strings.Contains(accepted.Body.String(), `"code":0`) {
		t.Fatalf("verified HTTP native edit failed: %s", accepted.Body)
	}
	if block := sql.GetBlock(blockID); block == nil || block.Content != "acceptednativehttp" {
		t.Fatalf("HTTP success did not reflect actual SQL commit: %+v", block)
	}
	router.POST("/api/filetree/getDoc", model.CheckAuth, getDoc)
	opened := post("/api/filetree/getDoc", map[string]any{"id": tree.ID}, true)
	if !strings.Contains(opened.Body.String(), "acceptednativehttp") ||
		strings.Contains(opened.Body.String(), kernelEvergreen.CarrierAttribute) {
		t.Fatalf("opened native editor exposed private carrier or lost committed body: %s", opened.Body)
	}
	failedUndo := post("/api/transactions/undo", map[string]any{"rootID": tree.ID}, false)
	if !strings.Contains(failedUndo.Body.String(), `"failed":true`) || model.GlobalUndoLog.Peek(tree.ID) == nil {
		t.Fatalf("HTTP failed undo consumed history: %s", failedUndo.Body)
	}
	undo := post("/api/transactions/undo", map[string]any{"rootID": tree.ID}, true)
	if strings.Contains(undo.Body.String(), `"failed":true`) {
		t.Fatalf("verified native HTTP undo failed: %s", undo.Body)
	}
	if block := sql.GetBlock(blockID); block == nil || block.Content != "originalnativehttp" {
		t.Fatalf("HTTP undo did not commit authority: %+v", block)
	}
	redo := post("/api/transactions/redo", map[string]any{"rootID": tree.ID}, true)
	if strings.Contains(redo.Body.String(), `"failed":true`) {
		t.Fatalf("verified native HTTP redo failed: %s", redo.Body)
	}
	if block := sql.GetBlock(blockID); block == nil || block.Content != "acceptednativehttp" {
		t.Fatalf("HTTP redo did not commit authority: %+v", block)
	}
	count, err := exec.Command("git", "-C", util.DataDir, "rev-list", "--count", "HEAD").CombinedOutput()
	if err != nil || strings.TrimSpace(string(count)) != "3" {
		t.Fatalf("HTTP edit/undo/redo Git commits: %v %s", err, count)
	}
	insertID := ast.NewNodeID()
	insertDOM := `<div data-node-id="` + insertID + `" data-type="NodeParagraph" class="p">` +
		`<div contenteditable="true">structuralnativehttp</div></div>`
	structural := post("/api/transactions", map[string]any{
		"reqId": 2, "app": "native-http", "session": "native-http", "transactions": []*model.Transaction{{
			DoOperations: []*model.Operation{
				{Action: "insert", ID: insertID, PreviousID: blockID, ParentID: tree.ID, Data: insertDOM},
			},
			UndoOperations: []*model.Operation{{Action: "delete", ID: insertID}},
		}},
	}, true)
	if !strings.Contains(structural.Body.String(), `"code":0`) || sql.GetBlock(insertID) == nil {
		t.Fatalf("HTTP native insert failed: %s", structural.Body)
	}
	structuralUndo := post("/api/transactions/undo", map[string]any{"rootID": tree.ID}, true)
	if strings.Contains(structuralUndo.Body.String(), `"failed":true`) ||
		sql.GetBlock(insertID) != nil || treenode.GetBlockTree(insertID) != nil {
		t.Fatalf("HTTP native insert undo failed: %s", structuralUndo.Body)
	}
	structuralRedo := post("/api/transactions/redo", map[string]any{"rootID": tree.ID}, true)
	if strings.Contains(structuralRedo.Body.String(), `"failed":true`) || sql.GetBlock(insertID) == nil {
		t.Fatalf("HTTP native insert redo failed: %s", structuralRedo.Body)
	}
	count, err = exec.Command("git", "-C", util.DataDir, "rev-list", "--count", "HEAD").CombinedOutput()
	if err != nil || strings.TrimSpace(string(count)) != "6" {
		t.Fatalf("HTTP body/structure/history Git commits: %v %s", err, count)
	}
	ordinary := treenode.NewTree(box.ID, "/"+ast.NewNodeID()+".sy", "/Removable", "Removable")
	if _, err = filesys.WriteTree(ordinary); err != nil {
		t.Fatal(err)
	}
	treenode.UpsertBlockTree(ordinary)
	if err = sql.RefreshAuthorityTree(ordinary); err != nil {
		t.Fatal(err)
	}
	ordinaryPath := box.ID + "/" + strings.TrimPrefix(ordinary.Path, "/")
	if output, err := exec.Command("git", "-C", util.DataDir, "add", "--", ordinaryPath).CombinedOutput(); err != nil {
		t.Fatalf("track HTTP delete fixture: %v %s", err, output)
	}
	if output, err := exec.Command("git", "-C", util.DataDir, "-c", "user.name=test-owner",
		"-c", "user.email=test-owner@example.invalid", "-c", "core.hooksPath=.git/hooks",
		"commit", "-q", "-m", "Seed HTTP deletion fixture").CombinedOutput(); err != nil {
		t.Fatalf("seed HTTP delete fixture: %v %s", err, output)
	}
	removed := post("/api/transactions", map[string]any{"reqId": 3, "app": "native-http", "session": "native-http",
		"transactions": []*model.Transaction{{DoOperations: []*model.Operation{
			{Action: "update", ID: blockID, Data: dom("deletedcompanionhttp")},
			{Action: "delete", ID: ordinary.ID},
		}}}}, true)
	if !strings.Contains(removed.Body.String(), `"code":0`) {
		t.Fatalf("HTTP mixed ordinary document deletion failed: %s", removed.Body)
	}
	if _, err = os.Stat(filepath.Join(util.DataDir, ordinaryPath)); !os.IsNotExist(err) ||
		sql.GetBlock(ordinary.ID) != nil || treenode.GetBlockTree(ordinary.ID) != nil {
		t.Fatal("HTTP deletion left ordinary file or projections")
	}
	if block := sql.GetBlock(blockID); block == nil || block.Content != "deletedcompanionhttp" {
		t.Fatalf("HTTP deletion omitted managed companion: %+v", block)
	}
	count, err = exec.Command("git", "-C", util.DataDir, "rev-list", "--count", "HEAD").CombinedOutput()
	if err != nil || strings.TrimSpace(string(count)) != "8" {
		t.Fatalf("HTTP mixed deletion did not produce one commit: %v %s", err, count)
	}
}
