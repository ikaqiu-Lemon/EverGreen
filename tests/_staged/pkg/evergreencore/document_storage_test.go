package evergreencore

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// 测试宿主用随机 nonce 的认证密文覆盖真实落盘和崩溃恢复，避免仅验证 mock 调用次数。
type encryptedTestStorage struct {
	root string
	key  []byte
	mu   sync.RWMutex
	open bool
}

type encryptedTestSession struct {
	storage *encryptedTestStorage
}

func (s *encryptedTestStorage) Open(context.Context) (DocumentStorageSession, error) {
	s.mu.RLock()
	if !s.open {
		s.mu.RUnlock()
		return nil, errors.New("notebook locked")
	}
	return encryptedTestSession{s}, nil
}

func (s encryptedTestSession) Close() { s.storage.mu.RUnlock() }
func (s encryptedTestSession) Filesystem() fs.FS {
	return encryptedTestFS{s, os.DirFS(s.storage.root)}
}
func (s encryptedTestSession) Allow(path string) error { return validateAuthorityPath(path) }
func (s encryptedTestSession) aead() cipher.AEAD {
	block, _ := aes.NewCipher(s.storage.key)
	aead, _ := cipher.NewGCM(block)
	return aead
}
func (s encryptedTestSession) Encode(path string, raw []byte) ([]byte, error) {
	if err := s.Allow(path); err != nil {
		return nil, err
	}
	aead := s.aead()
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, raw, []byte(path)), nil
}
func (s encryptedTestSession) Decode(path string, raw []byte) ([]byte, error) {
	if err := s.Allow(path); err != nil {
		return nil, err
	}
	aead := s.aead()
	if len(raw) < aead.NonceSize() {
		return nil, errors.New("invalid ciphertext")
	}
	return aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], []byte(path))
}

type encryptedTestFS struct {
	session encryptedTestSession
	base    fs.FS
}

func (f encryptedTestFS) Open(path string) (fs.File, error) { return f.base.Open(path) }
func (f encryptedTestFS) ReadFile(path string) ([]byte, error) {
	raw, err := fs.ReadFile(f.base, path)
	if err != nil {
		return nil, err
	}
	return f.session.Decode(path, raw)
}

func newEncryptedTestStorage(t *testing.T) *encryptedTestStorage {
	t.Helper()
	storage := &encryptedTestStorage{root: t.TempDir(), key: bytes.Repeat([]byte{7}, 32), open: true}
	for _, id := range []LogicalID{"c-a", "c-b"} {
		raw, err := (encryptedTestSession{storage}).Encode(pathForID(id), syFixture(id, "secret-before-"+string(id)))
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, storage.root, pathForID(id), raw)
	}
	return storage
}

func storagePlan(t *testing.T, service *AuthorityService, user Principal, operationID string) PlannedOperation {
	t.Helper()
	request := PlanRequest{Protocol: ChangePlanProtocol, Command: "claim.update", OperationID: operationID}
	for _, id := range []LogicalID{"c-a", "c-b"} {
		raw, err := service.host.Load(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		request.Base = append(request.Base, BaseRef{LogicalID: id, SemanticHash: semanticHashBytes(t, raw)})
		request.Writes = append(request.Writes, WriteInput{LogicalID: id, After: mutateSYFixture(t, raw, "secret-after-"+string(id))})
	}
	planned, err := service.Plan(context.Background(), user, request)
	if err != nil {
		t.Fatal(err)
	}
	return planned
}

func assertEncryptedJournal(t *testing.T, storage *encryptedTestStorage, operationID string) {
	t.Helper()
	dir := filepath.Join(storage.root, filepath.FromSlash(RuntimeDirName), TransactionsDirName, operationID)
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, marker := range []string{"secret-before", "secret-after"} {
			if bytes.Contains(raw, []byte(marker)) || bytes.Contains(raw, []byte("c2VjcmV0")) {
				t.Errorf("journal contains plaintext body: %s", filepath.Base(path))
			}
		}
		if entry.Name() == "manifest.json" {
			var manifest operationManifest
			if err = json.Unmarshal(raw, &manifest); err != nil {
				return err
			}
			if manifest.JournalVersion != 2 {
				t.Fatal("encrypted journal is not versioned")
			}
			for index, image := range manifest.Operation.Images {
				if len(image.Bytes) != 0 || bytes.Equal(manifest.Files[index].StoredAfter, syFixture(image.LogicalID, "secret-after-"+string(image.LogicalID))) {
					t.Fatal("plaintext after-image persisted in manifest")
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestEncryptedStorageRecoveryAndRetriesUseExactCiphertext(t *testing.T) {
	for _, point := range []string{FaultAfterPrepared, FaultAfterRename, FaultBeforeGit, FaultAfterGit, FaultBeforeEvent} {
		t.Run(point, func(t *testing.T) {
			storage := newEncryptedTestStorage(t)
			commits := newRecordingCommitter()
			index := -1
			if point == FaultAfterRename {
				index = 0
			}
			host, err := NewFilesystemAuthority(FilesystemAuthorityOptions{
				Root: storage.root, Storage: storage, Committer: commits,
				Faults: &oneShotFault{point: point, index: index},
			})
			if err != nil {
				t.Fatal(err)
			}
			service := NewAuthorityService(host, allowAllAuthorizer{}, nil)
			user := Principal{Type: PrincipalUser, ID: "user"}
			operation := storagePlan(t, service, user, "op-encrypted")
			first, applyErr := service.Apply(context.Background(), user, ApplyRequest{Operation: operation})
			if point == FaultBeforeEvent {
				if applyErr != nil || !first.DerivedStale {
					t.Fatalf("event failure must retain authority and mark stale: %+v %v", first, applyErr)
				}
			} else if applyErr == nil {
				t.Fatal("fault did not interrupt apply")
			}
			assertEncryptedJournal(t, storage, "op-encrypted")
			journalDir := filepath.Join(storage.root, filepath.FromSlash(RuntimeDirName), TransactionsDirName, "op-encrypted")
			stored := [][]byte{readFile(t, journalDir, "after/0000"), readFile(t, journalDir, "after/0001")}
			if records, err := service.Recover(context.Background()); err != nil || len(records) != 1 {
				t.Fatalf("recovery = %+v %v", records, err)
			}
			for index, image := range operation.Images {
				assertFileEquals(t, storage.root, image.Path, stored[index])
				plaintext, err := host.Load(context.Background(), image.LogicalID)
				if err != nil || !bytes.Equal(plaintext, image.Bytes) {
					t.Fatalf("authenticated read after recovery: %v", err)
				}
			}
			if _, err := service.Apply(context.Background(), user, ApplyRequest{Operation: operation}); err != nil {
				t.Fatal(err)
			}
			if commits.Count("op-encrypted") != 1 {
				t.Fatal("encrypted retry duplicated Git commit")
			}
			for index, image := range operation.Images {
				assertFileEquals(t, storage.root, image.Path, stored[index])
			}
			assertEncryptedJournal(t, storage, "op-encrypted")
		})
	}
}

func TestEncryptedStorageLockedAndCorruptFailClosed(t *testing.T) {
	for _, mode := range []string{"locked", "ciphertext", "journal ciphertext", "cross path"} {
		t.Run(mode, func(t *testing.T) {
			storage := newEncryptedTestStorage(t)
			commits := newRecordingCommitter()
			host, err := NewFilesystemAuthority(FilesystemAuthorityOptions{
				Root: storage.root, Storage: storage, Committer: commits,
				Faults: &oneShotFault{point: FaultAfterPrepared, index: -1},
			})
			if err != nil {
				t.Fatal(err)
			}
			service := NewAuthorityService(host, allowAllAuthorizer{}, nil)
			user := Principal{Type: PrincipalUser, ID: "user"}
			operation := storagePlan(t, service, user, "op-protected")
			if mode == "journal ciphertext" {
				if _, err = service.Apply(context.Background(), user, ApplyRequest{Operation: operation}); err == nil {
					t.Fatal("preparation fault did not interrupt")
				}
				dir := filepath.Join(storage.root, filepath.FromSlash(RuntimeDirName), TransactionsDirName, "op-protected")
				raw := readFile(t, dir, "after/0000")
				raw[len(raw)-1] ^= 1
				writeFile(t, dir, "after/0000", raw)
			} else if mode == "locked" {
				storage.mu.Lock()
				storage.open = false
				storage.mu.Unlock()
			} else {
				raw := readFile(t, storage.root, "box/a.sy")
				if mode == "ciphertext" {
					raw[len(raw)-1] ^= 1
				} else {
					raw = readFile(t, storage.root, "box/b.sy")
				}
				writeFile(t, storage.root, "box/a.sy", raw)
			}
			beforeA, beforeB := readFile(t, storage.root, "box/a.sy"), readFile(t, storage.root, "box/b.sy")
			if _, err = service.Apply(context.Background(), user, ApplyRequest{Operation: operation}); err == nil {
				t.Fatal("unsafe encrypted apply succeeded")
			}
			if mode == "locked" {
				if _, err = NewSYReadRepositoryWithStorage(context.Background(), storage, nil); err == nil {
					t.Fatal("locked repository exposed plaintext")
				}
			}
			assertFileEquals(t, storage.root, "box/a.sy", beforeA)
			assertFileEquals(t, storage.root, "box/b.sy", beforeB)
			if commits.Count("op-protected") != 0 {
				t.Fatal("unsafe encrypted operation committed")
			}
		})
	}
}

func TestJournalV1RecoveryPreservesSupportedPlaintextFormat(t *testing.T) {
	root := t.TempDir()
	writeSYFixture(t, root, "box/a.sy", "c-a", "before")
	commits := newRecordingCommitter()
	host, _ := NewFilesystemAuthority(FilesystemAuthorityOptions{
		Root: root, Committer: commits, Faults: &oneShotFault{point: FaultAfterPrepared, index: -1},
	})
	service := NewAuthorityService(host, allowAllAuthorizer{}, nil)
	user := Principal{Type: PrincipalUser, ID: "user"}
	operation, err := service.Plan(context.Background(), user, planFixture(t, root, "op-v1", map[LogicalID]string{"c-a": "after"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Apply(context.Background(), user, ApplyRequest{Operation: operation}); err == nil {
		t.Fatal("preparation fault did not interrupt")
	}
	dir := filepath.Join(root, filepath.FromSlash(RuntimeDirName), TransactionsDirName, "op-v1")
	var manifest operationManifest
	if err = json.Unmarshal(readFile(t, dir, "manifest.json"), &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.JournalVersion = 1
	manifest.Operation = operation
	for index := range manifest.Files {
		manifest.Files[index].StoredAfter = nil
	}
	raw, _ := json.Marshal(manifest)
	writeFile(t, dir, "manifest.json", raw)
	if _, err = service.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if commits.Count("op-v1") != 1 || !strings.Contains(string(readFile(t, root, "box/a.sy")), "after") {
		t.Fatal("legacy journal did not recover")
	}
}
