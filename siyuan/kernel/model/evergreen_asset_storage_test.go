package model

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/siyuan-note/siyuan/kernel/filesys"
	"github.com/siyuan-note/siyuan/kernel/util"
)

func TestEvergreenEncryptedAssetStoragePreservesLegacyNameAndRecovery(t *testing.T) {
	const boxID, diskName = "20261012130000-asset01", "resource.bin"
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, boxID, "assets"), 0700); err != nil {
		t.Fatal(err)
	}
	key, content := bytes.Repeat([]byte{29}, 32), []byte("private asset content")
	originalName := "original attachment.bin"
	oldData, oldPolicy, oldProvider := util.DataDir, filesys.EvergreenIsEncryptedBox, filesys.DEKProvider
	oldAcquire, oldRelease := filesys.DEKLockAcquire, filesys.DEKLockRelease
	util.DataDir = root
	filesys.EvergreenIsEncryptedBox = func(string) bool { return true }
	filesys.DEKProvider = func(string) ([]byte, error) { return append([]byte(nil), key...), nil }
	filesys.DEKLockAcquire, filesys.DEKLockRelease = nil, nil
	defer func() {
		util.DataDir, filesys.EvergreenIsEncryptedBox, filesys.DEKProvider = oldData, oldPolicy, oldProvider
		filesys.DEKLockAcquire, filesys.DEKLockRelease = oldAcquire, oldRelease
	}()
	for _, legacy := range []bool{true, false} {
		var stored []byte
		var err error
		if legacy {
			stored = legacyEncryptedAssetFixture(t, boxID, diskName, originalName, key, content)
		} else {
			stored, err = EncryptAsset(boxID, diskName, originalName, key, content)
			if err != nil {
				t.Fatal(err)
			}
		}
		p := boxID + "/assets/" + diskName
		if err = os.WriteFile(filepath.Join(root, p), stored, 0600); err != nil {
			t.Fatal(err)
		}
		session, err := (filesys.EvergreenDocumentStorage{Root: root, Boxes: []string{boxID}}).Open(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		raw, err := session.Decode(p, stored)
		if err != nil {
			t.Fatal(err)
		}
		var image filesys.EvergreenAssetContent
		if err = json.Unmarshal(raw, &image); err != nil || image.Name != originalName || !bytes.Equal(image.Content, content) {
			t.Fatal("asset storage lost authenticated name or bytes")
		}
		image.Content = []byte("updated asset bytes")
		after, _ := json.Marshal(image)
		current, err := session.Encode(p, after)
		if err != nil {
			t.Fatal(err)
		}
		decoded, name, err := DecryptAssetWithName(boxID, diskName, key, current)
		if err != nil || name != originalName || !bytes.Equal(decoded, image.Content) {
			t.Fatal("journal asset after-image changed name or native format")
		}
		fresh, err := (filesys.EvergreenDocumentStorage{Root: root, Boxes: []string{boxID}}).Open(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		decoded, err = fresh.Decode(p, current)
		fresh.Close()
		if err != nil || !bytes.Equal(decoded, after) {
			t.Fatal("asset CAS representation changed across recovery sessions")
		}
		corrupt := append([]byte(nil), stored...)
		corrupt[len(corrupt)/2] ^= 1
		if _, err = session.Decode(p, corrupt); err == nil {
			t.Fatal("asset authentication failure accepted plaintext")
		}
		if _, err = session.Encode(p, image.Content); err == nil {
			t.Fatal("encrypted asset write accepted missing authenticated name")
		}
		session.Close()
		actual, err := os.ReadFile(filepath.Join(root, p))
		if err != nil || !bytes.Equal(actual, stored) {
			t.Fatal("asset authenticated read changed legacy ciphertext")
		}
	}
}
