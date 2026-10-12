package evergreencore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type journalScopeStorage struct {
	*encryptedTestStorage
	scope string
}

type journalScopeSession struct {
	encryptedTestSession
	scope string
}

func (s journalScopeStorage) Open(ctx context.Context) (DocumentStorageSession, error) {
	session, err := s.encryptedTestStorage.Open(ctx)
	if err != nil {
		return nil, err
	}
	return journalScopeSession{session.(encryptedTestSession), s.scope}, nil
}

func (s journalScopeSession) InScope(path string) bool {
	return strings.HasPrefix(path, s.scope+"/")
}

func (s journalScopeSession) Allow(path string) error {
	if !s.InScope(path) {
		return errors.New("file outside authenticated scope")
	}
	return s.encryptedTestSession.Allow(path)
}

func (s journalScopeSession) Decode(path string, raw []byte) ([]byte, error) {
	if err := s.Allow(path); err != nil {
		return nil, err
	}
	return s.encryptedTestSession.Decode(path, raw)
}

func (s journalScopeSession) Filesystem() fs.FS {
	return journalScopeFS{session: s, root: os.DirFS(s.storage.root)}
}

type journalScopeFS struct {
	session journalScopeSession
	root    fs.FS
}

func (f journalScopeFS) Open(path string) (fs.File, error) {
	if path == "." {
		file, err := f.root.Open(path)
		if err != nil {
			return nil, err
		}
		return &journalScopeDirectory{ReadDirFile: file.(fs.ReadDirFile), scope: f.session.scope}, nil
	}
	if !f.session.InScope(path) && path != f.session.scope {
		return nil, fs.ErrNotExist
	}
	return f.root.Open(path)
}

func (f journalScopeFS) ReadFile(path string) ([]byte, error) {
	if err := f.session.Allow(path); err != nil {
		return nil, err
	}
	raw, err := fs.ReadFile(f.root, path)
	if err != nil {
		return nil, err
	}
	return f.session.Decode(path, raw)
}

type journalScopeDirectory struct {
	fs.ReadDirFile
	scope string
	read  bool
}

func (d *journalScopeDirectory) ReadDir(n int) ([]fs.DirEntry, error) {
	if d.read {
		if n > 0 {
			return nil, io.EOF
		}
		return nil, nil
	}
	d.read = true
	all, err := d.ReadDirFile.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	for _, entry := range all {
		if entry.Name() == d.scope {
			return []fs.DirEntry{entry}, nil
		}
	}
	return nil, nil
}

func scopeAuthority(t *testing.T, storage DocumentStorage, root string, commits OperationCommitter) *AuthorityService {
	t.Helper()
	host, err := NewFilesystemAuthority(FilesystemAuthorityOptions{Root: root, Storage: storage, Committer: commits})
	if err != nil {
		t.Fatal(err)
	}
	return NewAuthorityService(host, allowAllAuthorizer{}, nil)
}

func scopePlan(t *testing.T, service *AuthorityService, storage *encryptedTestStorage, scope, operationID string) PlannedOperation {
	t.Helper()
	id := LogicalID("c-" + scope)
	path := scope + "/claim.sy"
	stored := readFile(t, storage.root, path)
	raw, err := (encryptedTestSession{storage}).Decode(path, stored)
	if err != nil {
		t.Fatal(err)
	}
	planned, err := service.Plan(context.Background(), Principal{Type: PrincipalUser, ID: "user"}, PlanRequest{
		Protocol: ChangePlanProtocol, Command: "claim.update", OperationID: operationID,
		Base:   []BaseRef{{LogicalID: id, SemanticHash: semanticHashBytes(t, raw)}},
		Writes: []WriteInput{{LogicalID: id, After: mutateSYFixture(t, raw, operationID)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return planned
}

func TestJournalGlobalGateAndScopedRecoveryKeepLockedIslands(t *testing.T) {
	for _, fault := range []string{FaultAfterPrepared, FaultAfterRename, FaultBeforeGit, FaultAfterGit} {
		t.Run(fault, func(t *testing.T) {
			storage := &encryptedTestStorage{root: t.TempDir(), key: bytes.Repeat([]byte{9}, 32), open: true}
			for _, scope := range []string{"public", "private"} {
				path := scope + "/claim.sy"
				raw, err := (encryptedTestSession{storage}).Encode(path, syFixture(LogicalID("c-"+scope), scope+" before"))
				if err != nil {
					t.Fatal(err)
				}
				writeFile(t, storage.root, path, raw)
			}
			commits := newRecordingCommitter()
			unrestricted := scopeAuthority(t, storage, storage.root, commits)
			privatePlan := scopePlan(t, unrestricted, storage, "private", "op-private")
			publicPlan := scopePlan(t, unrestricted, storage, "public", "op-public")
			index := -1
			if fault == FaultAfterRename {
				index = 0
			}
			unrestricted.host.(*FilesystemAuthority).faults = &oneShotFault{point: fault, index: index}
			user := Principal{Type: PrincipalUser, ID: "user"}
			if _, err := unrestricted.Apply(context.Background(), user, ApplyRequest{Operation: privatePlan}); err == nil {
				t.Fatal("private fault did not interrupt")
			}
			public := scopeAuthority(t, journalScopeStorage{storage, "public"}, storage.root, commits)
			privateBefore := readFile(t, storage.root, "private/claim.sy")
			publicBefore := readFile(t, storage.root, "public/claim.sy")
			if _, err := public.Apply(context.Background(), user, ApplyRequest{Operation: publicPlan}); !HasDiagnostic(err, CodeTransactionBlocked) {
				t.Fatalf("unfinished private operation did not globally block with a stable diagnostic: %v", err)
			}
			records, err := public.Recover(context.Background())
			if err != nil || len(records) != 0 {
				t.Fatalf("public recovery crossed a locked island: %+v %v", records, err)
			}
			assertFileEquals(t, storage.root, "private/claim.sy", privateBefore)
			assertFileEquals(t, storage.root, "public/claim.sy", publicBefore)
			if _, err = public.Apply(context.Background(), user, ApplyRequest{Operation: publicPlan}); !HasDiagnostic(err, CodeTransactionBlocked) {
				t.Fatalf("skipping an inaccessible recovery cleared the global gate: %v", err)
			}
			private := scopeAuthority(t, journalScopeStorage{storage, "private"}, storage.root, commits)
			records, err = private.Recover(context.Background())
			if err != nil || len(records) != 1 || records[0].State != OperationCompleted {
				t.Fatalf("authenticated private recovery failed: %+v %v", records, err)
			}
			result, err := public.Apply(context.Background(), user, ApplyRequest{Operation: publicPlan})
			if err != nil || result.State != OperationCompleted {
				t.Fatalf("completed private ciphertext blocked a public operation: %+v %v", result, err)
			}
			if records, err = public.Recover(context.Background()); err != nil || len(records) != 0 {
				t.Fatalf("completed ciphertext required an unrelated key: %+v %v", records, err)
			}
			if commits.Count("op-private") != 1 || commits.Count("op-public") != 1 {
				t.Fatal("island recovery duplicated or omitted commits")
			}
		})
	}
}

func TestJournalScopedRecoveryRejectsPartialOperationBeforeWriting(t *testing.T) {
	storage := newEncryptedTestStorage(t)
	commits := newRecordingCommitter()
	service := scopeAuthority(t, storage, storage.root, commits)
	plan := storagePlan(t, service, Principal{Type: PrincipalUser, ID: "user"}, "op-partial-scope")
	service.host.(*FilesystemAuthority).faults = &oneShotFault{point: FaultAfterPrepared, index: -1}
	if _, err := service.Apply(context.Background(), plan.Plan.Principal, ApplyRequest{Operation: plan}); err == nil {
		t.Fatal("preparation fault did not interrupt")
	}
	beforeA, beforeB := readFile(t, storage.root, "box/a.sy"), readFile(t, storage.root, "box/b.sy")
	partial := scopeAuthority(t, partialJournalScope{storage}, storage.root, commits)
	if _, err := partial.Recover(context.Background()); !HasDiagnostic(err, CodeTransactionBlocked) {
		t.Fatalf("partial operation scope was admitted: %v", err)
	}
	assertFileEquals(t, storage.root, "box/a.sy", beforeA)
	assertFileEquals(t, storage.root, "box/b.sy", beforeB)
	if commits.Count("op-partial-scope") != 0 {
		t.Fatal("partial scope committed")
	}
}

type partialJournalScope struct{ *encryptedTestStorage }
type partialJournalSession struct{ encryptedTestSession }

func (s partialJournalScope) Open(ctx context.Context) (DocumentStorageSession, error) {
	session, err := s.encryptedTestStorage.Open(ctx)
	if err != nil {
		return nil, err
	}
	return partialJournalSession{session.(encryptedTestSession)}, nil
}

func (s partialJournalSession) InScope(path string) bool { return path == "box/a.sy" }

func TestJournalCompletedOutOfScopeCorruptionStillBlocks(t *testing.T) {
	for _, mode := range []string{"state", "after", "before", "plan", "missing manifest", "journal symlink"} {
		t.Run(mode, func(t *testing.T) {
			storage := newEncryptedTestStorage(t)
			commits := newRecordingCommitter()
			service := scopeAuthority(t, storage, storage.root, commits)
			user := Principal{Type: PrincipalUser, ID: "user"}
			plan := storagePlan(t, service, user, "op-completed")
			if _, err := service.Apply(context.Background(), user, ApplyRequest{Operation: plan}); err != nil {
				t.Fatal(err)
			}
			host := service.host.(*FilesystemAuthority)
			dir := host.operationDir("op-completed")
			switch mode {
			case "state":
				writeFile(t, dir, "state", []byte("PREPARED\n"))
			case "after", "before":
				writeFile(t, dir, mode+"/0000", []byte("corrupted ciphertext"))
			case "plan":
				raw := readFile(t, dir, "manifest.json")
				raw = bytes.Replace(raw, []byte(`"command": "claim.update"`), []byte(`"command": "claim.edge.update"`), 1)
				writeFile(t, dir, "manifest.json", raw)
			case "missing manifest":
				removeJournalFile(t, dir, "manifest.json")
			case "journal symlink":
				makeJournalSymlink(t, dir)
			}
			public := scopeAuthority(t, journalScopeStorage{storage, "public"}, storage.root, commits)
			if err := public.host.(*FilesystemAuthority).blockedByOtherOperation(context.Background(), "op-next"); !HasDiagnostic(err, CodeTransactionBlocked) {
				t.Fatalf("unreadable completed journal was silently ignored: %v", err)
			}
			if commits.Count("op-completed") != 1 {
				t.Fatal("journal inspection produced a commit")
			}
		})
	}
}

func removeJournalFile(t *testing.T, dir, path string) {
	t.Helper()
	if err := os.Remove(filepath.Join(dir, path)); err != nil {
		t.Fatal(err)
	}
}

func makeJournalSymlink(t *testing.T, dir string) {
	t.Helper()
	moved := dir + "-moved"
	if err := os.Rename(dir, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, dir); err != nil {
		t.Fatal(err)
	}
}
