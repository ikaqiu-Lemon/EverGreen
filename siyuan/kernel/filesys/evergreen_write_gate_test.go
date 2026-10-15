// SiYuan - From thought to insight, with agents
// Copyright (c) 2020-present, b3log.org
// SPDX-License-Identifier: AGPL-3.0-or-later

package filesys

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/88250/lute/ast"
	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	kernelEvergreen "github.com/siyuan-note/siyuan/kernel/evergreen"
	"github.com/siyuan-note/siyuan/kernel/treenode"
	"github.com/siyuan-note/siyuan/kernel/util"
)

func TestEvergreenBackgroundWritesCannotChangeOrStripAuthority(t *testing.T) {
	for _, mutation := range []string{"body", "strip envelope", "replace with plain tree", "conditional body"} {
		t.Run(mutation, func(t *testing.T) {
			root, box, _, _, _, _ := setupEvergreenStorage(t)
			const p = "/20261011120100-normal2.sy"
			raw := evergreenStorageFixture("c-protected", "20261011120100-normal2")
			abs := filepath.Join(root, box, p)
			if err := os.WriteFile(abs, raw, 0600); err != nil {
				t.Fatal(err)
			}
			tree, err := LoadTreeByData(raw, box, p, util.NewLute())
			if err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "strip envelope":
				tree.Root.RemoveIALAttr(kernelEvergreen.CarrierAttribute)
			case "replace with plain tree":
				tree = treenode.NewTree(box, p, "/Replacement", "Replacement")
			default:
				ast.Walk(tree.Root, func(node *ast.Node, entering bool) ast.WalkStatus {
					if entering && node.Type == ast.NodeText {
						node.Tokens = []byte("background overwrite")
					}
					return ast.WalkContinue
				})
			}
			if mutation == "conditional body" {
				_, err = WriteTreeIfUnchanged(tree, raw)
			} else {
				_, err = WriteTree(tree)
			}
			if err == nil || !strings.Contains(err.Error(), "EG_UNAUTHORIZED") {
				t.Errorf("background mutation bypassed semantic authority: %v", err)
			}
			actual, err := os.ReadFile(abs)
			if err != nil || !bytes.Equal(actual, raw) {
				t.Errorf("background write changed authority bytes: %v", err)
			}
		})
	}
}

func TestEvergreenEquivalentBackgroundWritePreservesCiphertext(t *testing.T) {
	root, _, box, key, _, _ := setupEvergreenStorage(t)
	const p = "/20261011120100-crypt02.sy"
	tree := treenode.NewTree(box, p, "/Protected", "Protected")
	document, err := core.DecodeSY(evergreenStorageFixture("c-protected", tree.ID), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = kernelEvergreen.NewHost(nil).SetDocument(tree.Root, document.Envelope); err != nil {
		t.Fatal(err)
	}
	raw, abs, err := prepareWriteTree(tree)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := encryptDataWithDEK(box, p, raw, key)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(abs, stored, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = WriteTree(tree); err != nil {
		t.Fatalf("equivalent managed background save failed: %v", err)
	}
	actual, err := os.ReadFile(filepath.Join(root, box, p))
	if err != nil || !bytes.Equal(actual, stored) {
		t.Fatal("equivalent background save regenerated ciphertext")
	}
}

func TestEvergreenReadRepairPreservesExistingAuthority(t *testing.T) {
	root, box, _, _, _, _ := setupEvergreenStorage(t)
	const p = "/20261011120100-normal2.sy"
	raw := bytes.Replace(evergreenStorageFixture("c-protected", "20261011120100-normal2"),
		[]byte(`"Spec":"5"`), []byte(`"Spec":"3"`), 1)
	abs := filepath.Join(root, box, p)
	if err := os.WriteFile(abs, raw, 0600); err != nil {
		t.Fatal(err)
	}
	fixed, needFix, err := fixTreeJSONData(box, p, raw, util.NewLute(), nil, false)
	if err != nil || !needFix || bytes.Equal(fixed, raw) {
		t.Fatalf("legacy fixture did not exercise read normalization: %v", err)
	}
	actual, err := os.ReadFile(abs)
	if err != nil || !bytes.Equal(actual, raw) {
		t.Fatal("read repair rewrote existing managed authority")
	}
}
