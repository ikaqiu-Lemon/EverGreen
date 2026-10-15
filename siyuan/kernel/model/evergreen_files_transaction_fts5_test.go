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
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/88250/lute/ast"
	"github.com/88250/lute/parse"
	"github.com/gin-gonic/gin"
	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	"github.com/siyuan-note/siyuan/kernel/av"
	kernelEvergreen "github.com/siyuan-note/siyuan/kernel/evergreen"
	"github.com/siyuan-note/siyuan/kernel/filesys"
	"github.com/siyuan-note/siyuan/kernel/sql"
	"github.com/siyuan-note/siyuan/kernel/treenode"
	"github.com/siyuan-note/siyuan/kernel/util"
)

func TestEvergreenNativeFileTransactionReplay(t *testing.T) {
	for _, scenario := range []string{"create", "remove", "asset", "av", "avcell", "avrelation", "avlayout", "conf"} {
		t.Run(scenario, func(t *testing.T) {
			if os.Getenv("SIYUAN_EG_FILES_REPLAY_TEST") != scenario {
				ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				defer cancel()
				command := exec.CommandContext(ctx, os.Args[0],
					"-test.run=^TestEvergreenNativeFileTransactionReplay$/^"+scenario+"$", "-test.timeout=80s", "-test.v")
				command.Env = append(os.Environ(), "SIYUAN_EG_FILES_REPLAY_TEST="+scenario)
				if output, err := command.CombinedOutput(); err != nil {
					t.Fatalf("isolated files replay: %v\n%s", err, output)
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
			p := filepath.Join(util.DataDir, fixture.box.ID, fixture.sourcePath)
			before, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			before, err = core.EncodeSY(before, &core.DocumentEnvelope{
				Spec: core.DocumentSpec,
				Entity: core.Entity{LogicalID: "c-files-replay", EntityType: core.EntityClaim,
					Schema: core.ClaimSchema, SemanticRevision: 1},
				Claim: &core.Claim{ClaimKind: core.KnowledgeKind, KindSchema: core.KnowledgeKindSchema,
					Status: "active", KindData: json.RawMessage(`{}`)},
				Relations: core.Relations{Outgoing: []core.TypedEdge{}},
			}, core.ApplicationRegistry())
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(p, before, 0600); err != nil {
				t.Fatal(err)
			}
			ordinary, err := filesys.LoadTreeProjection(fixture.box.ID, fixture.targetPath, util.NewLute())
			if err != nil {
				t.Fatal(err)
			}
			if err = sql.RefreshAuthorityTree(ordinary); err != nil {
				t.Fatal(err)
			}
			ordinaryPath := fixture.box.ID + "/" + strings.TrimPrefix(fixture.targetPath, "/")
			if output, err := exec.Command("git", "-C", util.DataDir, "init", "-q").CombinedOutput(); err != nil {
				t.Fatalf("Git init: %v %s", err, output)
			}
			if output, err := exec.Command("git", "-C", util.DataDir, "add", "--", ordinaryPath).CombinedOutput(); err != nil {
				t.Fatalf("Git fixture: %v %s", err, output)
			}
			if output, err := exec.Command("git", "-C", util.DataDir, "-c", "user.name=test-owner",
				"-c", "user.email=test-owner@example.invalid", "-c", "core.hooksPath=.git/hooks",
				"commit", "-q", "-m", "Seed replay fixture").CombinedOutput(); err != nil {
				t.Fatalf("Git fixture: %v %s", err, output)
			}
			dom := func(id, text string) string {
				node := treenode.NewParagraph(id)
				node.AppendChild(&ast.Node{Type: ast.NodeText, Tokens: []byte(text)})
				return util.NewLute().RenderNodeBlockDOM(node)
			}
			tableView := func(keyIDs ...string) *av.View {
				view := &av.View{ID: ast.NewNodeID(), Name: "Table", LayoutType: av.LayoutTypeTable,
					PageSize: av.ViewDefaultPageSize, Filters: []*av.ViewFilter{{Combination: av.FilterCombinationAnd}},
					Table: &av.LayoutTable{BaseLayout: &av.BaseLayout{ID: ast.NewNodeID(), ShowIcon: true}}}
				for _, keyID := range keyIDs {
					view.Table.Columns = append(view.Table.Columns, &av.ViewTableColumn{
						BaseField: &av.BaseField{ID: keyID}})
				}
				return view
			}
			tx := &Transaction{DoOperations: []*Operation{{Action: "update", ID: fixture.childID,
				Data: dom(fixture.childID, "filesreplaycompanion")}},
				UndoOperations: []*Operation{{Action: "update", ID: fixture.childID, Data: dom(fixture.childID, "original")}}}
			check := func(after bool) {}
			switch scenario {
			case "create":
				id, child := ast.NewNodeID(), ast.NewNodeID()
				root := &ast.Node{Type: ast.NodeDocument, ID: id}
				node := treenode.NewParagraph(child)
				node.AppendChild(&ast.Node{Type: ast.NodeText, Tokens: []byte("createdfilesneedle")})
				root.AppendChild(node)
				tree := &parse.Tree{ID: id, Box: fixture.box.ID, Path: "/" + id + ".sy", Root: root}
				tx.DoOperations = append(tx.DoOperations, &Operation{Action: "create", ID: id, Tree: tree})
				tx.UndoOperations = append([]*Operation{{Action: "removeCreatedDoc", ID: id, Tree: tree}}, tx.UndoOperations...)
				check = func(after bool) {
					t.Helper()
					_, statErr := os.Stat(filepath.Join(util.DataDir, fixture.box.ID, tree.Path))
					if after {
						if statErr != nil || treenode.GetBlockTree(child) == nil || sql.GetBlock(child) == nil {
							t.Fatal("created file or real projections missing")
						}
					} else if !os.IsNotExist(statErr) || treenode.GetBlockTree(child) != nil || sql.GetBlock(child) != nil {
						t.Fatal("creation undo left file or projections")
					}
				}
			case "remove":
				tx.DoOperations = append(tx.DoOperations, &Operation{Action: "removeCreatedDoc",
					ID: ordinary.ID, Tree: ordinary})
				tx.UndoOperations = append([]*Operation{{Action: "restoreCreatedDoc", ID: ordinary.ID,
					Tree: ordinary}}, tx.UndoOperations...)
				check = func(after bool) {
					t.Helper()
					_, statErr := os.Stat(filepath.Join(util.DataDir, ordinaryPath))
					if after {
						if !os.IsNotExist(statErr) || treenode.GetBlockTree(ordinary.ID) != nil || sql.GetBlock(ordinary.ID) != nil {
							t.Fatal("native removal left file or projections")
						}
					} else if statErr != nil || treenode.GetBlockTree(ordinary.ID) == nil || sql.GetBlock(ordinary.ID) == nil {
						t.Fatal("native removal undo did not restore file/projections")
					}
				}
			case "asset":
				global, local := "assets/atomic-replay.bin", fixture.box.ID+"/assets/atomic-replay.bin"
				if err = os.MkdirAll(filepath.Join(util.DataDir, fixture.box.ID, "assets"), 0700); err != nil {
					t.Fatal(err)
				}
				if err = os.MkdirAll(filepath.Join(util.DataDir, "assets"), 0700); err != nil {
					t.Fatal(err)
				}
				content := []byte("preserved atomic asset")
				if err = os.WriteFile(filepath.Join(util.DataDir, global), content, 0600); err != nil {
					t.Fatal(err)
				}
				if output, err := exec.Command("git", "-C", util.DataDir, "add", "--", global).CombinedOutput(); err != nil {
					t.Fatalf("Git asset: %v %s", err, output)
				}
				if output, err := exec.Command("git", "-C", util.DataDir, "-c", "user.name=test-owner",
					"-c", "user.email=test-owner@example.invalid", "-c", "core.hooksPath=.git/hooks",
					"commit", "-q", "-m", "Seed asset fixture").CombinedOutput(); err != nil {
					t.Fatalf("Git asset: %v %s", err, output)
				}
				id := ast.NewNodeID()
				node := treenode.NewParagraph(id)
				link := &ast.Node{Type: ast.NodeLink}
				link.AppendChild(&ast.Node{Type: ast.NodeLinkText, Tokens: []byte("attachment")})
				link.AppendChild(&ast.Node{Type: ast.NodeLinkDest, Tokens: []byte(global)})
				node.AppendChild(link)
				tx.DoOperations = append(tx.DoOperations, &Operation{Action: "insert", ID: id,
					ParentID: fixture.sourceID, Data: util.NewLute().RenderNodeBlockDOM(node)})
				tx.UndoOperations = append([]*Operation{{Action: "delete", ID: id}}, tx.UndoOperations...)
				check = func(after bool) {
					t.Helper()
					exists, absent := global, local
					if after {
						exists, absent = local, global
					}
					actual, err := os.ReadFile(filepath.Join(util.DataDir, exists))
					if err != nil || !bytes.Equal(actual, content) {
						t.Fatal("asset replay lost source bytes")
					}
					if _, err = os.Stat(filepath.Join(util.DataDir, absent)); !os.IsNotExist(err) {
						t.Fatal("asset replay retained duplicate location")
					}
				}
			case "av":
				id := ast.NewNodeID()
				row := ordinary.Root.FirstChild
				row.SetIALAttr(av.NodeAttrNameAvs, id)
				if _, err = filesys.WriteTree(ordinary); err != nil {
					t.Fatal(err)
				}
				view := &av.AttributeView{ID: id, Name: "original", Spec: av.CurrentSpec,
					KeyValues: []*av.KeyValues{{Key: &av.Key{ID: ast.NewNodeID(), Type: av.KeyTypeBlock}}},
					Views:     []*av.View{}}
				keyValues := view.GetBlockKeyValues()
				keyValues.Values = append(keyValues.Values, &av.Value{ID: ast.NewNodeID(), KeyID: keyValues.Key.ID,
					BlockID: row.ID, Type: av.KeyTypeBlock, Block: &av.ValueBlock{ID: row.ID, Content: "ordinary"}})
				raw, err := av.MarshalAttributeViewPersistence(view)
				if err != nil {
					t.Fatal(err)
				}
				var object map[string]json.RawMessage
				if err = json.Unmarshal(raw, &object); err != nil {
					t.Fatal(err)
				}
				object["future"] = json.RawMessage(`{"counter":9007199254740993}`)
				raw, _ = json.Marshal(object)
				avPath := filepath.Join(util.DataDir, "storage", "av", id+".json")
				if err = os.MkdirAll(filepath.Dir(avPath), 0700); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(avPath, raw, 0600); err != nil {
					t.Fatal(err)
				}
				tx.DoOperations = append(tx.DoOperations, &Operation{Action: "setAttrViewName", ID: id, Data: "changed"})
				tx.UndoOperations = append([]*Operation{{Action: "setAttrViewName", ID: id, Data: "original"}}, tx.UndoOperations...)
				check = func(after bool) {
					t.Helper()
					expected := "original"
					if after {
						expected = "changed"
					}
					raw, err := os.ReadFile(avPath)
					if err != nil || !bytes.Contains(raw, []byte("9007199254740993")) {
						t.Fatal("native AV edit lost unknown field")
					}
					if name, err := av.GetAttributeViewName(id); err != nil || name != expected {
						t.Fatalf("native AV authority name=%s err=%v", name, err)
					}
					current, err := filesys.LoadTreeProjection(ordinary.Box, ordinary.Path, util.NewLute())
					if err != nil {
						t.Fatal(err)
					}
					binding := treenode.GetNodeInTree(current, row.ID).IALAttr(av.NodeAttrNameAvs)
					if binding != id || !strings.Contains(getAvNames(binding), expected) || sql.GetBlock(row.ID) == nil {
						t.Fatalf("bound row binding or real AV projection changed: binding=%s expected=%s", binding, expected)
					}
				}
			case "avcell":
				id, blockKeyID, textKeyID, rowID := ast.NewNodeID(), ast.NewNodeID(), ast.NewNodeID(), ast.NewNodeID()
				row := ordinary.Root.FirstChild
				row.SetIALAttr(av.NodeAttrNameAvs, id)
				if _, err = filesys.WriteTree(ordinary); err != nil {
					t.Fatal(err)
				}
				view := tableView(blockKeyID, textKeyID)
				definition := &av.AttributeView{ID: id, Name: "Atomic cells", Spec: av.CurrentSpec,
					KeyIDs: []string{blockKeyID, textKeyID}, Views: []*av.View{view},
					KeyValues: []*av.KeyValues{
						{Key: &av.Key{ID: blockKeyID, Type: av.KeyTypeBlock}, Values: []*av.Value{{
							ID: ast.NewNodeID(), KeyID: blockKeyID, BlockID: rowID, Type: av.KeyTypeBlock,
							Block: &av.ValueBlock{ID: row.ID, Content: "ordinary"},
						}}},
						{Key: &av.Key{ID: textKeyID, Type: av.KeyTypeText}, Values: []*av.Value{{
							ID: ast.NewNodeID(), KeyID: textKeyID, BlockID: rowID, Type: av.KeyTypeText,
							Text: &av.ValueText{Content: "cell-before"},
						}}},
					}}
				raw, err := av.MarshalAttributeViewPersistence(definition)
				if err != nil {
					t.Fatal(err)
				}
				var object map[string]json.RawMessage
				if err = json.Unmarshal(raw, &object); err != nil {
					t.Fatal(err)
				}
				var keyValues []map[string]json.RawMessage
				if err = json.Unmarshal(object["keyValues"], &keyValues); err != nil {
					t.Fatal(err)
				}
				var values []map[string]json.RawMessage
				if err = json.Unmarshal(keyValues[1]["values"], &values); err != nil {
					t.Fatal(err)
				}
				values[0]["future"] = json.RawMessage(`{"counter":9007199254740993}`)
				keyValues[1]["values"], _ = json.Marshal(values)
				object["keyValues"], _ = json.Marshal(keyValues)
				object["future"] = json.RawMessage(`{"counter":9007199254740993}`)
				raw, _ = json.Marshal(object)
				avPath := filepath.Join(util.DataDir, "storage", "av", id+".json")
				if err = os.MkdirAll(filepath.Dir(avPath), 0700); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(avPath, raw, 0600); err != nil {
					t.Fatal(err)
				}
				cell := func(keyID string, data any) *Operation {
					return &Operation{Action: "updateAttrViewCell", AvID: id, KeyID: keyID, RowID: rowID, Data: data}
				}
				width := func(value string) *Operation {
					return &Operation{Action: "setAttrViewColWidth", AvID: id, ID: textKeyID, Data: value}
				}
				tx.DoOperations = append(tx.DoOperations,
					cell(blockKeyID, map[string]any{"block": map[string]any{
						"id": row.ID, "content": "static-after", "refSubtype": "s"}}),
					cell(textKeyID, map[string]any{"text": map[string]any{"content": "cell-after"}}),
					width("240px"))
				tx.UndoOperations = append([]*Operation{
					width(""),
					cell(textKeyID, map[string]any{"text": map[string]any{"content": "cell-before"}}),
					cell(blockKeyID, map[string]any{"block": map[string]any{
						"id": row.ID, "content": "", "refSubtype": "d"}}),
				}, tx.UndoOperations...)
				check = func(after bool) {
					t.Helper()
					currentRaw, err := os.ReadFile(avPath)
					if err != nil || bytes.Count(currentRaw, []byte("9007199254740993")) != 2 {
						t.Fatalf("native AV cell edit lost unknown fields: %v %s", err, currentRaw)
					}
					current, err := av.ParseAttributeViewData(id, currentRaw)
					if err != nil {
						t.Fatal(err)
					}
					wantText, wantWidth, wantStatic := "cell-before", "", ""
					if after {
						wantText, wantWidth, wantStatic = "cell-after", "240px", "static-after"
					}
					if got := current.GetValue(textKeyID, rowID).Text.Content; got != wantText {
						t.Fatalf("native AV cell=%q want=%q", got, wantText)
					}
					if got := current.Views[0].Table.Columns[1].Width; got != wantWidth {
						t.Fatalf("native AV width=%q want=%q", got, wantWidth)
					}
					currentTree, err := filesys.LoadTreeProjection(ordinary.Box, ordinary.Path, util.NewLute())
					if err != nil {
						t.Fatal(err)
					}
					if got := treenode.GetNodeInTree(currentTree, row.ID).
						IALAttr(av.NodeAttrViewStaticText + "-" + id); got != wantStatic {
						t.Fatalf("native AV bound document static text=%q want=%q", got, wantStatic)
					}
				}
			case "avrelation":
				srcID, destID := ast.NewNodeID(), ast.NewNodeID()
				srcBlockKey, srcRelationKey := ast.NewNodeID(), ast.NewNodeID()
				destBlockKey, destRelationKey := ast.NewNodeID(), ast.NewNodeID()
				srcRow, destRow := ast.NewNodeID(), ast.NewNodeID()
				detached := func(keyID, rowID, content string) *av.Value {
					return &av.Value{ID: ast.NewNodeID(), KeyID: keyID, BlockID: rowID, Type: av.KeyTypeBlock,
						IsDetached: true, Block: &av.ValueBlock{Content: content}}
				}
				relation := func(keyID, rowID string) *av.Value {
					return &av.Value{ID: ast.NewNodeID(), KeyID: keyID, BlockID: rowID, Type: av.KeyTypeRelation,
						Relation: &av.ValueRelation{BlockIDs: []string{}}}
				}
				src := &av.AttributeView{ID: srcID, Name: "Source", Spec: av.CurrentSpec,
					KeyIDs: []string{srcBlockKey, srcRelationKey}, Views: []*av.View{tableView(srcBlockKey, srcRelationKey)},
					KeyValues: []*av.KeyValues{
						{Key: &av.Key{ID: srcBlockKey, Type: av.KeyTypeBlock},
							Values: []*av.Value{detached(srcBlockKey, srcRow, "Source row")}},
						{Key: &av.Key{ID: srcRelationKey, Type: av.KeyTypeRelation,
							Relation: &av.Relation{AvID: destID, IsTwoWay: true, BackKeyID: destRelationKey}},
							Values: []*av.Value{relation(srcRelationKey, srcRow)}},
					}}
				dest := &av.AttributeView{ID: destID, Name: "Destination", Spec: av.CurrentSpec,
					KeyIDs: []string{destBlockKey, destRelationKey}, Views: []*av.View{tableView(destBlockKey, destRelationKey)},
					KeyValues: []*av.KeyValues{
						{Key: &av.Key{ID: destBlockKey, Type: av.KeyTypeBlock},
							Values: []*av.Value{detached(destBlockKey, destRow, "Destination row")}},
						{Key: &av.Key{ID: destRelationKey, Type: av.KeyTypeRelation,
							Relation: &av.Relation{AvID: srcID, IsTwoWay: true, BackKeyID: srcRelationKey}},
							Values: []*av.Value{relation(destRelationKey, destRow)}},
					}}
				paths := map[string]string{}
				for _, definition := range []*av.AttributeView{src, dest} {
					raw, marshalErr := av.MarshalAttributeViewPersistence(definition)
					if marshalErr != nil {
						t.Fatal(marshalErr)
					}
					var object map[string]json.RawMessage
					if err = json.Unmarshal(raw, &object); err != nil {
						t.Fatal(err)
					}
					object["future"] = json.RawMessage(`{"counter":9007199254740993}`)
					raw, _ = json.Marshal(object)
					avPath := filepath.Join(util.DataDir, "storage", "av", definition.ID+".json")
					if err = os.MkdirAll(filepath.Dir(avPath), 0700); err != nil {
						t.Fatal(err)
					}
					if err = os.WriteFile(avPath, raw, 0600); err != nil {
						t.Fatal(err)
					}
					paths[definition.ID] = avPath
				}
				relationValue := func(ids []string) map[string]any {
					return map[string]any{"relation": map[string]any{"blockIDs": ids}}
				}
				tx.DoOperations = append(tx.DoOperations, &Operation{Action: "updateAttrViewCell", AvID: srcID,
					KeyID: srcRelationKey, RowID: srcRow, Data: relationValue([]string{destRow})})
				tx.UndoOperations = append([]*Operation{{Action: "updateAttrViewCell", AvID: srcID,
					KeyID: srcRelationKey, RowID: srcRow, Data: relationValue([]string{})}}, tx.UndoOperations...)
				check = func(after bool) {
					t.Helper()
					expected := []string{}
					if after {
						expected = []string{destRow}
					}
					for _, currentID := range []string{srcID, destID} {
						raw, readErr := os.ReadFile(paths[currentID])
						if readErr != nil || !bytes.Contains(raw, []byte("9007199254740993")) {
							t.Fatalf("native two-way AV lost unknown fields: %v %s", readErr, raw)
						}
					}
					currentSrc, err := av.ParseAttributeView(srcID)
					if err != nil {
						t.Fatal(err)
					}
					currentDest, err := av.ParseAttributeView(destID)
					if err != nil {
						t.Fatal(err)
					}
					srcIDs := currentSrc.GetValue(srcRelationKey, srcRow).Relation.BlockIDs
					destIDs := currentDest.GetValue(destRelationKey, destRow).Relation.BlockIDs
					if !slices.Equal(srcIDs, expected) {
						t.Fatalf("native source relation=%v want=%v", srcIDs, expected)
					}
					expectedBack := []string{}
					if after {
						expectedBack = []string{srcRow}
					}
					if !slices.Equal(destIDs, expectedBack) {
						t.Fatalf("native destination relation=%v want=%v", destIDs, expectedBack)
					}
				}
			case "avlayout":
				setupAttributeViewRefI18n(t)
				for key, value := range map[string]string{
					"calendar": "Calendar", "gallery": "Gallery", "kanban": "Kanban", "list": "List",
				} {
					util.AttrViewLangs[util.Lang][key] = value
				}
				id, blockKeyID := ast.NewNodeID(), ast.NewNodeID()
				view := tableView(blockKeyID)
				definition := &av.AttributeView{ID: id, Name: "Mirrored layout", Spec: av.CurrentSpec,
					KeyIDs: []string{blockKeyID}, Views: []*av.View{view},
					KeyValues: []*av.KeyValues{{Key: &av.Key{ID: blockKeyID, Type: av.KeyTypeBlock}}}}
				raw, err := av.MarshalAttributeViewPersistence(definition)
				if err != nil {
					t.Fatal(err)
				}
				var object map[string]json.RawMessage
				if err = json.Unmarshal(raw, &object); err != nil {
					t.Fatal(err)
				}
				object["future"] = json.RawMessage(`{"counter":9007199254740993}`)
				raw, _ = json.Marshal(object)
				avPath := filepath.Join(util.DataDir, "storage", "av", id+".json")
				if err = os.MkdirAll(filepath.Dir(avPath), 0700); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(avPath, raw, 0600); err != nil {
					t.Fatal(err)
				}
				source, err := filesys.LoadTreeProjection(fixture.box.ID, fixture.sourcePath, util.NewLute())
				if err != nil {
					t.Fatal(err)
				}
				firstID, secondID := ast.NewNodeID(), ast.NewNodeID()
				databaseNode := func(nodeID string) *ast.Node {
					node := &ast.Node{Type: ast.NodeAttributeView, ID: nodeID, AttributeViewID: id,
						AttributeViewType: string(av.LayoutTypeTable)}
					node.SetIALAttr("id", nodeID)
					node.SetIALAttr(av.NodeAttrView, view.ID)
					return node
				}
				first, second := databaseNode(firstID), databaseNode(secondID)
				source.Root.AppendChild(first)
				ordinary.Root.AppendChild(second)
				for _, tree := range []*parse.Tree{source, ordinary} {
					treePath := filepath.Join(util.DataDir, tree.Box, tree.Path)
					previous, readErr := os.ReadFile(treePath)
					if readErr != nil {
						t.Fatal(readErr)
					}
					rendered, renderErr := filesys.RenderNativeTree(tree, previous)
					if renderErr != nil {
						t.Fatal(renderErr)
					}
					if err = os.WriteFile(treePath, rendered, 0600); err != nil {
						t.Fatal(err)
					}
					treenode.UpsertBlockTree(tree)
					if err = sql.RefreshAuthorityTree(tree); err != nil {
						t.Fatal(err)
					}
				}
				av.BatchUpsertBlockRel([]*ast.Node{first, second})
				cachePath := filepath.Join(util.DataDir, "storage", "av", "blocks.msgpack")
				for _, relative := range []string{
					fixture.box.ID + fixture.sourcePath,
					fixture.box.ID + fixture.targetPath,
					"storage/av/" + id + ".json",
					"storage/av/blocks.msgpack",
				} {
					if output, addErr := exec.Command("git", "-C", util.DataDir, "add", "--", relative).CombinedOutput(); addErr != nil {
						t.Fatalf("Git layout fixture: %v %s", addErr, output)
					}
				}
				if output, commitErr := exec.Command("git", "-C", util.DataDir, "-c", "user.name=test-owner",
					"-c", "user.email=test-owner@example.invalid", "-c", "core.hooksPath=.git/hooks",
					"commit", "-q", "-m", "Seed mirrored layout fixture").CombinedOutput(); commitErr != nil {
					t.Fatalf("Git layout fixture: %v %s", commitErr, output)
				}
				layout := func(value av.LayoutType) *Operation {
					return &Operation{Action: "changeAttrViewLayout", AvID: id, BlockID: firstID, Layout: value}
				}
				tx.DoOperations = append(tx.DoOperations, layout(av.LayoutTypeGallery))
				tx.UndoOperations = append([]*Operation{layout(av.LayoutTypeTable)}, tx.UndoOperations...)
				check = func(after bool) {
					t.Helper()
					expected := av.LayoutTypeTable
					if after {
						expected = av.LayoutTypeGallery
					}
					currentRaw, err := os.ReadFile(avPath)
					if err != nil || !bytes.Contains(currentRaw, []byte("9007199254740993")) {
						t.Fatalf("native AV layout lost unknown fields: %v %s", err, currentRaw)
					}
					current, err := av.ParseAttributeViewData(id, currentRaw)
					if err != nil || current.Views[0].LayoutType != expected {
						t.Fatalf("native AV layout=%v want=%v err=%v", current.Views[0].LayoutType, expected, err)
					}
					for _, item := range []struct {
						path   string
						nodeID string
					}{{fixture.sourcePath, firstID}, {fixture.targetPath, secondID}} {
						tree, loadErr := filesys.LoadTreeProjection(fixture.box.ID, item.path, util.NewLute())
						if loadErr != nil {
							t.Fatal(loadErr)
						}
						node := treenode.GetNodeInTree(tree, item.nodeID)
						if node == nil || node.AttributeViewType != string(expected) {
							t.Fatalf("mirrored database block [%s] layout=%v want=%v", item.nodeID, node, expected)
						}
					}
					rels, relErr := av.GetBlockRelsByAVIDs([]string{id})
					if relErr != nil || !slices.Equal(rels[id], []string{firstID, secondID}) &&
						!slices.Equal(rels[id], []string{secondID, firstID}) {
						t.Fatalf("mirrored database cache changed: %v %v", rels, relErr)
					}
					if _, statErr := os.Stat(cachePath); statErr != nil {
						t.Fatal(statErr)
					}
				}
			case "conf":
				boxTree := treenode.NewTree(fixture.box.ID, "/"+fixture.box.ID+".sy", "/Notebook", "Notebook")
				if _, err = filesys.WriteTree(boxTree); err != nil {
					t.Fatal(err)
				}
				treenode.UpsertBlockTree(boxTree)
				if err = sql.RefreshAuthorityTree(boxTree); err != nil {
					t.Fatal(err)
				}
				confPath := filepath.Join(util.DataDir, fixture.box.ID, ".siyuan", "conf.json")
				raw, err := os.ReadFile(confPath)
				if err != nil {
					t.Fatal(err)
				}
				var fields map[string]json.RawMessage
				if err = json.Unmarshal(raw, &fields); err != nil {
					t.Fatal(err)
				}
				fields["future"] = json.RawMessage(`{"counter":9007199254740993}`)
				fields["icon"] = json.RawMessage(`"1f34b"`)
				raw, _ = json.Marshal(fields)
				if err = os.WriteFile(confPath, raw, 0600); err != nil {
					t.Fatal(err)
				}
				tx.DoOperations = append(tx.DoOperations, &Operation{Action: "setAttrs", ID: boxTree.ID,
					Data: `{"icon":"1f34e"}`})
				tx.UndoOperations = append([]*Operation{{Action: "setAttrs", ID: boxTree.ID,
					Data: `{"icon":"1f34b"}`}}, tx.UndoOperations...)
				check = func(after bool) {
					t.Helper()
					expected := "1f34b"
					if after {
						expected = "1f34e"
					}
					current, err := os.ReadFile(confPath)
					if err != nil || !bytes.Contains(current, []byte("9007199254740993")) {
						t.Fatal("native config edit lost unknown fields")
					}
					if err = json.Unmarshal(current, &fields); err != nil || string(fields["icon"]) != `"`+expected+`"` {
						t.Fatalf("native configuration icon not in the atomic write-set: %s %v", current, err)
					}
				}
			}
			apply := func(tx *Transaction) {
				t.Helper()
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest("POST", "/api/transactions", nil)
				c.Set(RoleContextKey, RoleAdministrator)
				recordEvergreenUserAuthentication(c, "workspace-session")
				BindEvergreenTransactionIdentity(c, tx)
				tx.MarkFromAPI()
				if err := PerformTxSync(tx); err != nil {
					t.Fatalf("native file transaction: %v", err)
				}
			}
			apply(tx)
			check(true)
			if scenario == "av" {
				ids := tx.mutatedRootIDs
				if len(ids) != 2 {
					t.Fatalf("AV binding document omitted from transaction write-set: %v", ids)
				}
			}
			entry := GlobalUndoLog.Undo(fixture.sourceID)
			if entry == nil {
				t.Fatal("full files transaction missing undo")
			}
			undo := &Transaction{DoOperations: entry.UndoOperationsForReplay(), UndoOperations: entry.DoOperationsForReplay()}
			undo.MarkReplay()
			apply(undo)
			GlobalUndoLog.UndoCommit(entry, fixture.sourceID)
			check(false)
			entry = GlobalUndoLog.Redo(fixture.sourceID)
			if entry == nil {
				t.Fatal("full files transaction missing redo")
			}
			redo := &Transaction{DoOperations: entry.DoOperationsForReplay(), UndoOperations: entry.UndoOperationsForReplay()}
			redo.MarkReplay()
			apply(redo)
			GlobalUndoLog.RedoCommit(entry, fixture.sourceID)
			check(true)
			if scenario == "avlayout" {
				rejected := &Transaction{DoOperations: []*Operation{{
					Action: "delete", ID: tx.DoOperations[len(tx.DoOperations)-1].BlockID,
				}}}
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest("POST", "/api/transactions", nil)
				c.Set(RoleContextKey, RoleAdministrator)
				recordEvergreenUserAuthentication(c, "workspace-session")
				BindEvergreenTransactionIdentity(c, rejected)
				rejected.MarkFromAPI()
				if rejectErr := PerformTxSync(rejected); rejectErr == nil ||
					!strings.Contains(rejectErr.Error(), "dedicated atomic command") {
					t.Fatalf("native AV carrier deletion was not rejected: %v", rejectErr)
				}
				check(true)
			}
			output, err := exec.Command("git", "-C", util.DataDir, "log", "--format=%B").CombinedOutput()
			if err != nil || strings.Count(string(output), "Evergreen-Operation-ID:") != 3 {
				t.Fatalf("edit/undo/redo did not each produce one commit: %v %s", err, output)
			}
		})
	}
}
