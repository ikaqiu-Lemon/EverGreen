// SiYuan - From thought to insight, with agents
// Copyright (c) 2020-present, b3log.org
// SPDX-License-Identifier: AGPL-3.0-or-later

package filesys

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/88250/lute/parse"
	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	"github.com/siyuan-note/siyuan/kernel/av"
	"github.com/siyuan-note/siyuan/kernel/cache"
	kernelEvergreen "github.com/siyuan-note/siyuan/kernel/evergreen"
	"github.com/siyuan-note/siyuan/kernel/treenode"
	"github.com/siyuan-note/siyuan/kernel/util"
)

func TestEvergreenUnfinishedJournalBlocksUnrelatedBackgroundSideEffects(t *testing.T) {
	for _, fault := range []string{core.FaultAfterPrepared, core.FaultAfterRename, core.FaultBeforeGit, core.FaultAfterGit} {
		t.Run(fault, func(t *testing.T) {
			root, box, privateBox, key, lock, unlocked := setupEvergreenStorage(t)
			cache.ClearTreeCache()
			t.Cleanup(cache.ClearTreeCache)
			privatePath := privateBox + "/20261011120100-crypt02.sy"
			privateBefore := evergreenStorageFixture("c-private", "20261011120100-crypt02")
			stored, err := encryptDataWithDEK(privateBox, "/20261011120100-crypt02.sy", privateBefore, key)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(root, privatePath), stored, 0600); err != nil {
				t.Fatal(err)
			}
			const ordinaryPath = "/20261012130000-plain01.sy"
			ordinary := treenode.NewTree(box, ordinaryPath, "/Ordinary", "Ordinary")
			raw, err := RenderNativeTree(ordinary, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(root, box, ordinaryPath), raw, 0600); err != nil {
				t.Fatal(err)
			}
			const repairPath = "/20261012130001-repair1.sy"
			repairTree := treenode.NewTree(box, repairPath, "/Legacy", "Legacy")
			repairRaw, err := RenderNativeTree(repairTree, nil)
			if err != nil {
				t.Fatal(err)
			}
			var repairObject map[string]json.RawMessage
			if err = json.Unmarshal(repairRaw, &repairObject); err != nil {
				t.Fatal(err)
			}
			repairObject["Spec"] = json.RawMessage(`"3"`)
			repairObject["ID"] = json.RawMessage(`"20261012130006-wrong01"`)
			repairRaw, err = json.Marshal(repairObject)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(root, box, repairPath), repairRaw, 0600); err != nil {
				t.Fatal(err)
			}
			const orphanPath = "/20261012130002-parent1/20261012130003-orphan1.sy"
			orphan := treenode.NewTree(box, orphanPath, "/Missing/Orphan", "Orphan")
			orphanRaw, err := RenderNativeTree(orphan, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.MkdirAll(filepath.Join(root, box, "20261012130002-parent1"), 0700); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(root, box, orphanPath), orphanRaw, 0600); err != nil {
				t.Fatal(err)
			}
			const viewID = "20261012130007-view001"
			view := &av.AttributeView{ID: viewID, Name: "Original", Spec: av.CurrentSpec,
				KeyValues: []*av.KeyValues{{Key: &av.Key{ID: "20261012130008-key0001", Type: av.KeyTypeBlock}}},
				Views:     []*av.View{}}
			viewRaw, err := av.MarshalAttributeViewPersistence(view)
			if err != nil {
				t.Fatal(err)
			}
			viewPath := filepath.Join(root, "storage", "av", viewID+".json")
			if err = os.MkdirAll(filepath.Dir(viewPath), 0700); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(viewPath, viewRaw, 0600); err != nil {
				t.Fatal(err)
			}
			evergreenIslandGit(t, root, "init", "-q")
			evergreenIslandGit(t, root, "config", "user.name", "Evergreen Test")
			evergreenIslandGit(t, root, "config", "user.email", "evergreen@example.invalid")
			evergreenIslandGit(t, root, "config", "core.hooksPath", ".git/hooks")
			evergreenIslandGit(t, root, "add", "--", privateBox, box)
			evergreenIslandGit(t, root, "commit", "-m", "background gate baseline")
			committer, err := core.NewLocalGitCommitter(root)
			if err != nil {
				t.Fatal(err)
			}
			runtime, err := kernelEvergreen.NewRuntime(context.Background(), kernelEvergreen.RuntimeOptions{
				Root: root, Storage: EvergreenDocumentStorage{Root: root, Boxes: []string{privateBox}},
				Committer: committer, Faults: &evergreenIslandFault{point: fault},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer runtime.Close()
			snapshot, err := runtime.InspectClaim(context.Background(), "c-private")
			if err != nil {
				t.Fatal(err)
			}
			tags := []string{"changed"}
			user := core.Principal{Type: core.PrincipalUser, ID: "user"}
			plan, err := runtime.PlanClaimEdit(context.Background(), user, core.ClaimEditRequest{
				OperationID: "op-background-private", LogicalID: "c-private", Base: snapshot.Base,
				Edit: core.ClaimEdit{Tags: &tags},
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = runtime.Apply(context.Background(), user, core.ApplyRequest{Operation: plan}); err == nil {
				t.Fatal("journal interruption did not fire")
			}
			lock.Lock()
			*unlocked = false
			lock.Unlock()
			before := backgroundFilesSnapshot(t, root)
			changed := func() *parse.Tree {
				tree, err := LoadTreeProjectionByData(raw, box, ordinaryPath, util.NewLute())
				if err != nil {
					t.Fatal(err)
				}
				tree.Root.SetIALAttr("title", "Changed")
				return tree
			}
			actions := []struct {
				name string
				run  func() error
			}{
				{"save", func() error { _, err := WriteTree(changed()); return err }},
				{"conditional", func() error { _, err := WriteTreeIfUnchanged(changed(), raw); return err }},
				{"AV save", func() error {
					view.Name = "Changed"
					return av.SaveAttributeView(view)
				}},
				{"AV conditional", func() error { return av.SaveAttributeViewIfUnchanged(view, viewRaw) }},
				{"create", func() error {
					tree := treenode.NewTree(box, "/20261012130004-newdir1/20261012130005-newdoc1.sy", "/New/Doc", "Doc")
					tree.Root.FirstChild.Unlink()
					_, err := WriteTree(tree)
					if tree.Root.FirstChild != nil || treenode.GetBlockTree(tree.ID) != nil {
						t.Error("rejected creation mutated its tree or block index")
					}
					return err
				}},
				{"read repair", func() error {
					_, _, err := LoadTreeWithFix(box, repairPath, util.NewLute())
					return err
				}},
				{"parent repair", func() error {
					_, err := LoadTreeByData(orphanRaw, box, orphanPath, util.NewLute())
					return err
				}},
				{"cached parent repair", func() error {
					cache.SetTreeDataInBox(orphan.ID, box, orphanRaw)
					_, err := LoadTree(box, orphanPath, util.NewLute())
					cache.RemoveTreeDataInBox(orphan.ID, box)
					return err
				}},
			}
			for _, action := range actions {
				t.Run(action.name, func(t *testing.T) {
					if err := action.run(); !core.HasDiagnostic(err, core.CodeTransactionBlocked) {
						t.Errorf("unfinished private journal admitted an ordinary side effect: %v", err)
					}
					if !reflect.DeepEqual(before, backgroundFilesSnapshot(t, root)) {
						t.Error("rejected write changed authority, directories or journal")
					}
				})
			}
			for _, action := range []struct {
				name string
				run  func()
			}{
				{"AV mirror", func() { av.UpsertBlockRel(viewID, ordinary.ID) }},
				{"AV relation", func() { av.UpsertAvBackRel(viewID, viewID) }},
			} {
				t.Run(action.name, func(t *testing.T) {
					action.run()
					if !reflect.DeepEqual(before, backgroundFilesSnapshot(t, root)) {
						t.Error("AV cache writer changed files behind an unfinished journal")
					}
				})
			}
			if _, err = LoadTree(box, ordinaryPath, util.NewLute()); err != nil {
				t.Fatalf("ordinary read without repairs was blocked: %v", err)
			}
			lock.Lock()
			*unlocked = true
			lock.Unlock()
			if records, err := runtime.Recover(context.Background()); err != nil || len(records) != 1 {
				t.Fatalf("private recovery: %+v %v", records, err)
			}
			if _, err = runtime.Apply(context.Background(), user, core.ApplyRequest{Operation: plan}); err != nil {
				t.Fatal(err)
			}
			lock.Lock()
			*unlocked = false
			lock.Unlock()
			if _, err = WriteTreeIfUnchanged(changed(), raw); err != nil {
				t.Fatalf("completed private journal blocked conditional save: %v", err)
			}
			if err = av.SaveAttributeViewIfUnchanged(view, viewRaw); err != nil {
				t.Fatalf("completed private journal blocked ordinary AV save: %v", err)
			}
			if _, _, err = LoadTreeWithFix(box, repairPath, util.NewLute()); err != nil {
				t.Fatalf("completed private journal blocked legacy repair: %v", err)
			}
			if _, err = LoadTreeByData(orphanRaw, box, orphanPath, util.NewLute()); err != nil {
				t.Fatalf("completed private journal blocked parent repair: %v", err)
			}
			if count := evergreenIslandGit(t, root, "rev-list", "--count", "HEAD"); count != "2\n" {
				t.Fatalf("background save or recovery duplicated the managed commit: %s", count)
			}
			after, err := os.ReadFile(filepath.Join(root, privatePath))
			plain, decodeErr := decryptDataWithDEK(privateBox, "/20261011120100-crypt02.sy", after, key)
			if err != nil || decodeErr != nil || !bytes.Equal(plain, plan.Images[0].Bytes) {
				t.Fatal("ordinary saves damaged recovered private ciphertext")
			}
		})
	}
}

func backgroundFilesSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	if err := filepath.WalkDir(root, func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Name() == ".git" && entry.IsDir() {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		files[rel] = "directory"
		if !entry.IsDir() {
			raw, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			files[rel] = string(raw)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return files
}
