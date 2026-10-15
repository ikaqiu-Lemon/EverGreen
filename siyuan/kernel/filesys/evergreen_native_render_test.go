// SiYuan - From thought to insight, with agents
// Copyright (c) 2020-present, b3log.org
// SPDX-License-Identifier: AGPL-3.0-or-later

package filesys

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/88250/lute/ast"
	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	"github.com/siyuan-note/siyuan/kernel/cache"
	"github.com/siyuan-note/siyuan/kernel/treenode"
	"github.com/siyuan-note/siyuan/kernel/util"
)

func TestEvergreenNativeRenderPreservesUnknownNumbersAndDefersReview(t *testing.T) {
	_, box, _, _, _, _ := setupEvergreenStorage(t)
	const documentID = "20261011120100-normal2"
	const p = "/" + documentID + ".sy"
	raw := evergreenStorageFixture("c-native-render", documentID)
	raw = bytes.Replace(raw, []byte(`"Spec":"5"`), []byte(`"Spec":"5","future":{"integer":9007199254740993}`), 1)
	raw = bytes.Replace(raw, []byte(`"Type":"NodeParagraph"`),
		[]byte(`"Type":"NodeParagraph","future":{"integer":9007199254740995}`), 1)
	raw = bytes.Replace(raw, []byte(`"Type":"NodeText"`),
		[]byte(`"Type":"NodeText","future":{"integer":9007199254740997}`), 1)
	tree, err := LoadTreeProjectionByData(raw, box, p, util.NewLute())
	if err != nil {
		t.Fatal(err)
	}
	tree.Root.FirstChild.FirstChild.Tokens = []byte("native edited body")
	after, err := RenderNativeTree(tree, raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range [][]byte{
		[]byte("9007199254740993"), []byte("9007199254740995"), []byte("9007199254740997"),
		[]byte("native edited body"),
	} {
		if !bytes.Contains(after, value) {
			t.Fatalf("native renderer lost authoritative value %s: %s", value, after)
		}
	}
	document, err := core.DecodeSY(after, nil)
	if err != nil || document.Envelope.Entity.SemanticRevision != 1 {
		t.Fatalf("render changed managed revision before planning: %+v %v", document, err)
	}
	prepared, err := core.PrepareNativeDocumentEdit(raw, after, core.ApplicationRegistry())
	if err != nil {
		t.Fatalf("rendered edit is not admissible to core: %v", err)
	}
	document, err = core.DecodeSY(prepared, nil)
	if err != nil || document.Envelope.Entity.SemanticRevision != 2 {
		t.Fatalf("core did not advance revision exactly once: %+v %v", document, err)
	}
	if bytes.Contains(after, []byte("custom-eg-internal-envelope")) {
		t.Fatal("internal carrier leaked into persisted JSON")
	}
	var a, b map[string]json.RawMessage
	_ = json.Unmarshal(raw, &a)
	_ = json.Unmarshal(after, &b)
	if !bytes.Equal(a["Evergreen"], b["Evergreen"]) {
		var x, y any
		_ = json.Unmarshal(a["Evergreen"], &x)
		_ = json.Unmarshal(b["Evergreen"], &y)
		ax, _ := json.Marshal(x)
		by, _ := json.Marshal(y)
		if !bytes.Equal(ax, by) {
			t.Fatal("render changed trusted metadata before core planning")
		}
	}
}

func TestEvergreenNativeRenderDoesNotCreateDirectoryOrPublishTree(t *testing.T) {
	root, box, _, _, _, _ := setupEvergreenStorage(t)
	const documentID = "20261012121000-newtree"
	const parentID = "20261012120000-parent1"
	p := "/" + parentID + "/" + documentID + ".sy"
	tree := treenode.NewTree(box, p, "/Parent/New", "New")
	tree.Root.AppendChild(&ast.Node{Type: ast.NodeParagraph, ID: "20261012121001-newpara"})
	after, err := RenderNativeTree(tree, nil)
	if err != nil || !json.Valid(after) {
		t.Fatalf("render create: %s %v", after, err)
	}
	if _, err = os.Stat(filepath.Join(root, box, parentID)); !os.IsNotExist(err) {
		t.Fatalf("native preflight created a directory: %v", err)
	}
	if treenode.GetBlockTreeInBox(tree.Root.FirstChild.ID, box) != nil {
		t.Fatal("native preflight published uncommitted blocks")
	}
	if _, exists := cache.GetTreeDataInBox(tree.ID, box); exists {
		t.Fatal("native preflight published uncommitted content")
	}
}
