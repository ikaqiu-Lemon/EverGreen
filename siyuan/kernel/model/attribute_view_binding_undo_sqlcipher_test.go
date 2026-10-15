//go:build fts5 && (sqlcipher || libsqlcipher)

package model

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/88250/lute/parse"
	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	"github.com/siyuan-note/siyuan/kernel/av"
	"github.com/siyuan-note/siyuan/kernel/cache"
	"github.com/siyuan-note/siyuan/kernel/conf"
	kernelEvergreen "github.com/siyuan-note/siyuan/kernel/evergreen"
	"github.com/siyuan-note/siyuan/kernel/filesys"
	"github.com/siyuan-note/siyuan/kernel/treenode"
	"github.com/siyuan-note/siyuan/kernel/util"
)

func TestAttributeViewEncryptedNativeReplay(t *testing.T) {
	fixture, before, _ := setupAttributeViewBindingUndoTest(t, true)
	originalName := before.Name
	tx := &Transaction{
		DoOperations: []*Operation{{
			Action: "setAttrViewName", ID: before.ID, BlockID: fixture.sourceID, Data: "Encrypted database",
		}},
		UndoOperations: []*Operation{{
			Action: "setAttrViewName", ID: before.ID, BlockID: fixture.sourceID, Data: originalName,
		}},
		fromAPI: true,
	}
	_ = kernelEvergreen.CloseDefaultRuntime()
	t.Cleanup(func() { _ = kernelEvergreen.CloseDefaultRuntime() })
	boxID := fixture.box.ID
	var trees []*parse.Tree
	for _, id := range []string{fixture.sourceID, fixture.targetID} {
		tree, err := LoadTreeByBlockID(id)
		if err != nil {
			t.Fatal(err)
		}
		trees = append(trees, tree)
	}
	markRuntimeEncryptedBox(boxID)
	dek := bytes.Repeat([]byte{0x63}, 32)
	setDEKForTest(boxID, dek)
	boxConf := conf.NewBoxConf()
	boxConf.Encrypted = true
	boxConf.BoxCrypt = &conf.BoxEncryption{Spec: boxEncryptionSpec}
	if err := encryptBoxMetadata(boxID, boxConf, dek); err != nil {
		t.Fatal(err)
	}
	configData, err := json.Marshal(boxConf)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(util.DataDir, boxID, ".siyuan", "conf.json"), configData, 0600); err != nil {
		t.Fatal(err)
	}
	mountedEncryptedBoxes.Store(boxID, true)
	oldTempDir := util.TempDir
	util.TempDir = t.TempDir()
	t.Cleanup(func() {
		treenode.CloseEncryptedBlockTreeDB(boxID)
		util.TempDir = oldTempDir
	})
	if err = treenode.OpenEncryptedBlockTreeDB(boxID, dek); err != nil {
		t.Fatal(err)
	}
	av.SetAVBoxID(before.ID, boxID)
	t.Cleanup(func() {
		mountedEncryptedBoxes.Delete(boxID)
		av.SetAVBoxID(before.ID, "")
		forgetRuntimeEncryptedBox(boxID)
		encryptedBoxLifecycles.Delete(boxID)
		cachedDEKsLock.Lock()
		delete(cachedDEKs, boxID)
		cachedDEKsLock.Unlock()
	})
	for _, tree := range trees {
		cache.RemoveTreeDataInBox(tree.ID, tree.Box)
		if err := os.Remove(filepath.Join(util.DataDir, tree.Box, tree.Path)); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if _, err := filesys.WriteTree(tree); err != nil {
			t.Fatal(err)
		}
		treenode.UpsertBlockTree(tree)
	}
	if err := av.SaveAttributeView(before); err != nil {
		t.Fatal(err)
	}
	plainPath := filepath.Join(util.DataDir, "storage", "av", before.ID+".json")
	if err := os.Remove(plainPath); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		t.Helper()
		output, gitErr := exec.Command("git", append([]string{"-C", util.DataDir}, args...)...).CombinedOutput()
		if gitErr != nil {
			t.Fatalf("git %v: %v\n%s", args, gitErr, output)
		}
		return strings.TrimSpace(string(output))
	}
	git("init", "-q")
	git("config", "user.name", "Evergreen Test")
	git("config", "user.email", "evergreen@example.invalid")
	git("add", "--", boxID)
	git("commit", "-m", "encrypted AV baseline")
	bindNative := func(transaction *Transaction) {
		transaction.nativeIdentity = &nativeTransactionIdentity{principal: core.Principal{
			Type: core.PrincipalUser, ID: "workspace-owner", AuthSource: "workspace-session",
		}}
	}
	bindNative(tx)
	if err := PerformTxSync(tx); err != nil {
		t.Fatal(err)
	}
	for _, operations := range [][]*Operation{cloneOperations(tx.UndoOperations), cloneOperations(tx.DoOperations)} {
		replay := &Transaction{DoOperations: operations, isReplay: true}
		bindNative(replay)
		if err := PerformTxSync(replay); err != nil {
			t.Fatal(err)
		}
	}
	if commits := git("rev-list", "--count", "HEAD"); commits != "4" {
		t.Fatalf("encrypted edit/undo/redo produced %s commits, want 4 including baseline", commits)
	}
	documentCiphertext, err := os.ReadFile(filepath.Join(util.DataDir, boxID, trees[1].Path))
	if err != nil || !util.IsCiphertext(documentCiphertext) {
		t.Fatalf("bound document was not committed as ciphertext: %v", err)
	}
	path := filepath.Join(util.DataDir, boxID, "storage", "av", before.ID+".json")
	ciphertext, err := os.ReadFile(path)
	if err != nil || !util.IsCiphertext(ciphertext) {
		t.Fatalf("binding replay did not preserve encryption: %v", err)
	}
	if _, err = os.Stat(plainPath); !os.IsNotExist(err) {
		t.Fatalf("binding replay produced plaintext: %v", err)
	}
	for _, failure := range []string{"locked", "corrupted"} {
		t.Run(failure, func(t *testing.T) {
			if failure == "locked" {
				cachedDEKsLock.Lock()
				delete(cachedDEKs, boxID)
				cachedDEKsLock.Unlock()
				defer setDEKForTest(boxID, bytes.Repeat([]byte{0x63}, 32))
			} else {
				ciphertext[len(ciphertext)-1] ^= 1
				if err = os.WriteFile(path, ciphertext, 0600); err != nil {
					t.Fatal(err)
				}
			}
			cache.ClearAVCache()
			replay := &Transaction{DoOperations: cloneOperations(tx.UndoOperations), isReplay: true}
			bindNative(replay)
			if err = PerformTxSync(replay); err == nil {
				t.Fatal("binding replay accepted inaccessible encrypted data")
			}
			preserved, readErr := os.ReadFile(path)
			if readErr != nil || !bytes.Equal(ciphertext, preserved) {
				t.Fatalf("failed replay changed encrypted data: %v", readErr)
			}
		})
	}
}
