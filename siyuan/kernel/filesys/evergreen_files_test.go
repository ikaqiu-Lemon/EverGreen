package filesys

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/siyuan-note/siyuan/kernel/av"
)

func TestEvergreenAuxiliaryStorageScopeAndRawConfiguration(t *testing.T) {
	root, normalBox, encryptedBox, _, _, _ := setupEvergreenStorage(t)
	public, err := (EvergreenDocumentStorage{Root: root}).Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer public.Close()
	private, err := (EvergreenDocumentStorage{Root: root, Boxes: []string{encryptedBox}}).Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer private.Close()
	for _, p := range []string{
		"storage/av/20261012120000-native1.json", "storage/av/blocks.msgpack", "storage/av/relations.msgpack",
		"assets/resource.bin", normalBox + "/assets/resource.bin", normalBox + "/.siyuan/conf.json",
	} {
		raw := []byte(`{"name":"native","Encrypted":true,"unknown":9007199254740993}`)
		if err = os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(root, p), raw, 0600); err != nil {
			t.Fatal(err)
		}
		got, err := fs.ReadFile(public.Filesystem(), p)
		if err != nil || !bytes.Equal(got, raw) {
			t.Fatalf("public auxiliary read %s: %v", p, err)
		}
		stored, err := public.Encode(p, raw)
		if err != nil || !bytes.Equal(stored, raw) {
			t.Fatalf("public auxiliary encode %s: %v", p, err)
		}
		if err = private.Allow(p); err == nil {
			t.Fatalf("private notebook scope admitted global or ordinary file: %s", p)
		}
	}
	conf := encryptedBox + "/.siyuan/conf.json"
	raw := []byte(`{"Encrypted":true,"WrappedDEK":"original","unknown":9007199254740993}`)
	stored, err := private.Encode(conf, raw)
	if err != nil || !bytes.Equal(raw, stored) {
		t.Fatalf("notebook configuration was re-enveloped: %v", err)
	}
	for _, p := range []string{
		"storage/local.json", "storage/av/mirror.json", "assets/.private/resource.bin",
		normalBox + "/.siyuan/key.json", normalBox + "/assets/document.sy",
	} {
		if err = public.Allow(p); err == nil {
			t.Fatalf("unregistered auxiliary path admitted: %s", p)
		}
	}
	link := filepath.Join(root, "assets", "linked.bin")
	if err = os.Symlink(filepath.Join(t.TempDir(), "outside.bin"), link); err != nil {
		t.Fatal(err)
	}
	if err = public.Allow("assets/linked.bin"); err == nil {
		t.Fatal("auxiliary symlink escaped storage scope")
	}
}

func TestEvergreenAuxiliaryStorageAuthenticatesExistingAVFormats(t *testing.T) {
	root, _, boxID, key, _, _ := setupEvergreenStorage(t)
	session, err := (EvergreenDocumentStorage{Root: root, Boxes: []string{boxID}}).Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	oldProvider, oldAcquire, oldRelease := av.AVDEKProvider, av.AVLockAcquire, av.AVLockRelease
	av.AVDEKProvider = func(string) ([]byte, error) { return key, nil }
	av.AVLockAcquire, av.AVLockRelease = nil, nil
	defer func() {
		av.AVDEKProvider, av.AVLockAcquire, av.AVLockRelease = oldProvider, oldAcquire, oldRelease
	}()
	for _, item := range []struct{ name, object string }{
		{"20261012120000-native1.json", "20261012120000-native1"},
		{"blocks.msgpack", "mirror"}, {"relations.msgpack", "relation"},
	} {
		p := boxID + "/storage/av/" + item.name
		raw := []byte("authenticated native AV object")
		legacy, err := av.EncryptAVData(boxID, item.object, raw)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(root, p), legacy, 0600); err != nil {
			t.Fatal(err)
		}
		got, err := fs.ReadFile(session.Filesystem(), p)
		if err != nil || !bytes.Equal(got, raw) {
			t.Fatalf("existing AV object cannot authenticate: %s %v", p, err)
		}
		stored, err := session.Encode(p, raw)
		if err != nil || bytes.Equal(stored, raw) {
			t.Fatalf("AV stored in plaintext: %s %v", p, err)
		}
		got, err = av.DecryptAVData(boxID, item.object, stored)
		if err != nil || !bytes.Equal(got, raw) {
			t.Fatalf("existing AV reader cannot read journal output: %s %v", p, err)
		}
		corrupt := append([]byte(nil), legacy...)
		corrupt[len(corrupt)-1] ^= 1
		if _, err = session.Decode(p, corrupt); err == nil {
			t.Fatal("damaged AV authentication fell back to plaintext")
		}
		if _, err = session.Decode(boxID+"/storage/av/20261012120000-native2.json", legacy); err == nil {
			t.Fatal("AV object identity substitution authenticated")
		}
		got, err = os.ReadFile(filepath.Join(root, p))
		if err != nil || !bytes.Equal(got, legacy) {
			t.Fatal("existing AV read rewrote source ciphertext")
		}
	}
}
