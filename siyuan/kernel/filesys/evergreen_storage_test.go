// SiYuan - From thought to insight, with agents
// Copyright (c) 2020-present, b3log.org
// SPDX-License-Identifier: AGPL-3.0-or-later

package filesys

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	evergreencore "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	kernelEvergreen "github.com/siyuan-note/siyuan/kernel/evergreen"
	"github.com/siyuan-note/siyuan/kernel/util"
)

func setupEvergreenStorage(t *testing.T) (string, string, string, []byte, *sync.RWMutex, *bool) {
	t.Helper()
	root := t.TempDir()
	normalBox, encryptedBox := "20261011120000-normal1", "20261011120000-crypt01"
	oldData, oldProvider := util.DataDir, DEKProvider
	oldAcquire, oldRelease, oldPolicy := DEKLockAcquire, DEKLockRelease, EvergreenIsEncryptedBox
	util.DataDir = root
	t.Cleanup(func() {
		util.DataDir, DEKProvider = oldData, oldProvider
		DEKLockAcquire, DEKLockRelease, EvergreenIsEncryptedBox = oldAcquire, oldRelease, oldPolicy
	})
	key := bytes.Repeat([]byte{13}, 32)
	lock, unlocked := &sync.RWMutex{}, new(bool)
	*unlocked = true
	EvergreenIsEncryptedBox = func(id string) bool { return id == encryptedBox }
	DEKLockAcquire = func(string) { lock.RLock() }
	DEKLockRelease = func(string) { lock.RUnlock() }
	DEKProvider = func(id string) ([]byte, error) {
		if id != encryptedBox {
			return nil, nil
		}
		if !*unlocked {
			return nil, errors.New("locked")
		}
		return append([]byte(nil), key...), nil
	}
	for _, boxID := range []string{normalBox, encryptedBox} {
		if err := os.MkdirAll(filepath.Join(root, boxID), 0700); err != nil {
			t.Fatal(err)
		}
	}
	return root, normalBox, encryptedBox, key, lock, unlocked
}

func evergreenStorageFixture(id, documentID string) []byte {
	return []byte(`{"ID":"` + documentID + `","Type":"NodeDocument","Spec":"5","Evergreen":{
		"spec":"evergreen.sy/v1","entity":{"logical_id":"` + id + `","entity_type":"claim",
		"schema":"evergreen.claim/v1","semantic_revision":1},"claim":{"claim_kind":"knowledge",
		"kind_schema":"evergreen.claim-kind.knowledge/v1","status":"active","tags":[],"kind_data":{}},"relations":{"outgoing":[]}
	},"Children":[{"ID":"20261011120001-body001","Type":"NodeParagraph","Children":[{"Type":"NodeText","Data":"private body"}]}]}`)
}

func TestEvergreenStorageScopePreservesNativeCiphertextAndLockBoundary(t *testing.T) {
	root, normalBox, encryptedBox, key, lock, unlocked := setupEvergreenStorage(t)
	normalPath := normalBox + "/20261011120100-normal2.sy"
	encryptedPath := encryptedBox + "/20261011120100-crypt02.sy"
	raw := evergreenStorageFixture("c-private", "20261011120100-crypt02")
	ciphertext, err := encryptDataWithDEK(encryptedBox, "/20261011120100-crypt02.sy", raw, key)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, encryptedPath), ciphertext, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, normalPath), evergreenStorageFixture("c-public", "20261011120100-normal2"), 0600); err != nil {
		t.Fatal(err)
	}
	public := EvergreenDocumentStorage{Root: root}
	repository, err := evergreencore.NewSYReadRepositoryWithStorage(context.Background(), public, nil)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := repository.List(context.Background())
	if err != nil || !reflect.DeepEqual(ids, []evergreencore.LogicalID{"c-public"}) {
		t.Fatalf("public scan exposed private notebook: %v %v", ids, err)
	}
	private := EvergreenDocumentStorage{Root: root, Boxes: []string{encryptedBox}}
	session, err := private.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if lock.TryLock() {
		lock.Unlock()
		session.Close()
		t.Fatal("storage session did not retain notebook crypto lease")
	}
	plain, err := fs.ReadFile(session.Filesystem(), encryptedPath)
	if err != nil || !bytes.Equal(plain, raw) {
		session.Close()
		t.Fatalf("native authenticated ciphertext read: %v", err)
	}
	newCiphertext, err := session.Encode(encryptedPath, raw)
	if err != nil || bytes.Equal(newCiphertext, raw) || bytes.Equal(newCiphertext, ciphertext) {
		session.Close()
		t.Fatalf("native encrypted write did not use fresh authenticated envelope: %v", err)
	}
	decoded, err := decryptDataWithDEK(encryptedBox, "/20261011120100-crypt02.sy", newCiphertext, key)
	if err != nil || !bytes.Equal(decoded, raw) {
		session.Close()
		t.Fatalf("native reader cannot read stored Evergreen image: %v", err)
	}
	moved := encryptedBox + "/20261011110000-parent1/20261011120100-crypt02.sy"
	if decoded, err = session.Decode(moved, ciphertext); err != nil || !bytes.Equal(decoded, raw) {
		session.Close()
		t.Fatal("parent move changed native AAD compatibility")
	}
	if _, err = session.Decode(encryptedBox+"/20261011120100-crypt03.sy", ciphertext); err == nil {
		session.Close()
		t.Fatal("different object accepted native ciphertext")
	}
	session.Close()
	if !lock.TryLock() {
		t.Fatal("storage session leaked notebook crypto lease")
	}
	*unlocked = false
	lock.Unlock()
	if _, err = private.Open(context.Background()); err == nil {
		t.Fatal("locked private storage opened")
	}
	if _, err = repository.Load(context.Background(), "c-private"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("global repository exposed encrypted logical identity")
	}
	actual, err := os.ReadFile(filepath.Join(root, encryptedPath))
	if err != nil || !bytes.Equal(actual, ciphertext) {
		t.Fatal("authenticated legacy read rewrote source ciphertext")
	}
}

func TestEvergreenStorageUsesRequestNotebookScope(t *testing.T) {
	root, normalBox, encryptedBox, key, lock, _ := setupEvergreenStorage(t)
	normalPath := normalBox + "/20261011120100-normal2.sy"
	encryptedPath := encryptedBox + "/20261011120100-crypt02.sy"
	raw := evergreenStorageFixture("c-private", "20261011120100-crypt02")
	ciphertext, err := encryptDataWithDEK(encryptedBox, "/20261011120100-crypt02.sy", raw, key)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, encryptedPath), ciphertext, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, normalPath),
		evergreenStorageFixture("c-public", "20261011120100-normal2"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, err := WithEvergreenNotebookScope(context.Background(), []string{encryptedBox, encryptedBox})
	if err != nil {
		t.Fatal(err)
	}
	session, err := (EvergreenDocumentStorage{Root: root}).Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scoped, ok := session.(evergreencore.DocumentStorageSnapshotScope)
	scope := ""
	if ok {
		scope = scoped.AuthoritySnapshotScope()
	}
	if scope != "selected:"+encryptedBox {
		session.Close()
		t.Fatalf("authenticated snapshot scope = %q, supported=%v", scope, ok)
	}
	if lock.TryLock() {
		lock.Unlock()
		session.Close()
		t.Fatal("request-scoped storage did not retain the encrypted notebook lease")
	}
	if got, readErr := fs.ReadFile(session.Filesystem(), encryptedPath); readErr != nil || !bytes.Equal(got, raw) {
		session.Close()
		t.Fatalf("request-scoped encrypted read: %v", readErr)
	}
	if _, readErr := fs.ReadFile(session.Filesystem(), normalPath); readErr == nil {
		session.Close()
		t.Fatalf("request scope exposed an unselected notebook: %v", readErr)
	}
	session.Close()
	if !lock.TryLock() {
		t.Fatal("request-scoped storage leaked the encrypted notebook lease")
	}
	lock.Unlock()

	fixed := EvergreenDocumentStorage{Root: root, Boxes: []string{normalBox}}
	if _, err = fixed.Open(ctx); err == nil {
		t.Fatal("request scope replaced a fixed storage scope")
	}
	if _, err = WithEvergreenNotebookScope(context.Background(), []string{"not-a-box"}); err == nil {
		t.Fatal("invalid request notebook scope was accepted")
	}
}

func TestEvergreenStorageAuthenticationFailureAndScopeForgery(t *testing.T) {
	root, normalBox, encryptedBox, key, _, _ := setupEvergreenStorage(t)
	path := encryptedBox + "/20261011120100-crypt02.sy"
	raw := evergreenStorageFixture("c-private", "20261011120100-crypt02")
	ciphertext, _ := encryptDataWithDEK(encryptedBox, "/20261011120100-crypt02.sy", raw, key)
	ciphertext[len(ciphertext)-1] ^= 1
	if err := os.WriteFile(filepath.Join(root, path), ciphertext, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := evergreencore.NewSYReadRepositoryWithStorage(context.Background(), EvergreenDocumentStorage{
		Root: root, Boxes: []string{encryptedBox},
	}, nil); err == nil {
		t.Fatal("corrupt ciphertext fell back to plaintext")
	}
	public, err := (EvergreenDocumentStorage{Root: root}).Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer public.Close()
	for _, path := range []string{
		path, "../outside.sy", normalBox + "/../../outside.sy", normalBox + "/not-a-document.sy",
	} {
		if _, err = public.Encode(path, raw); err == nil {
			t.Fatalf("unscoped or invalid path accepted: %s", path)
		}
	}
	outside := filepath.Join(t.TempDir(), "20261011120100-normal2.sy")
	if err = os.WriteFile(outside, evergreenStorageFixture("c-outside", "20261011120100-normal2"), 0600); err != nil {
		t.Fatal(err)
	}
	link := normalBox + "/20261011120100-normal2.sy"
	if err = os.Symlink(outside, filepath.Join(root, link)); err != nil {
		t.Fatal(err)
	}
	if _, err = fs.ReadFile(public.Filesystem(), link); err == nil {
		t.Fatal("symlink escaped authenticated authority scope")
	}
}

type evergreenStorageFault struct{ fired bool }

func (f *evergreenStorageFault) Fail(point string, index int) error {
	if !f.fired && point == evergreencore.FaultAfterPrepared {
		f.fired = true
		return errors.New("prepared interruption")
	}
	return nil
}

type evergreenStorageCommitter struct{ count int }

func (c *evergreenStorageCommitter) Commit(_ context.Context, _ evergreencore.CommitRequest) (string, error) {
	c.count++
	return "storage-commit", nil
}

type evergreenIslandFault struct {
	point string
	fired bool
}

func (f *evergreenIslandFault) Fail(point string, _ int) error {
	if !f.fired && point == f.point {
		f.fired = true
		return errors.New("island interruption")
	}
	return nil
}

func TestEvergreenRuntimeGlobalJournalGateKeepsLockedRecoveryIslands(t *testing.T) {
	for _, fault := range []string{
		evergreencore.FaultAfterPrepared, evergreencore.FaultAfterRename,
		evergreencore.FaultBeforeGit, evergreencore.FaultAfterGit, evergreencore.FaultBeforeEvent,
	} {
		t.Run(fault, func(t *testing.T) {
			root, normalBox, encryptedBox, key, lock, unlocked := setupEvergreenStorage(t)
			privatePath := encryptedBox + "/20261011120100-crypt02.sy"
			publicPath := normalBox + "/20261011120100-normal2.sy"
			storedBefore, err := encryptDataWithDEK(encryptedBox, "/20261011120100-crypt02.sy",
				evergreenStorageFixture("c-private", "20261011120100-crypt02"), key)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(root, privatePath), storedBefore, 0600); err != nil {
				t.Fatal(err)
			}
			publicBefore := evergreenStorageFixture("c-public", "20261011120100-normal2")
			if err = os.WriteFile(filepath.Join(root, publicPath), publicBefore, 0600); err != nil {
				t.Fatal(err)
			}
			evergreenIslandGit(t, root, "init", "-q")
			evergreenIslandGit(t, root, "config", "user.name", "Evergreen Test")
			evergreenIslandGit(t, root, "config", "user.email", "evergreen@example.invalid")
			evergreenIslandGit(t, root, "config", "core.hooksPath", ".git/hooks")
			evergreenIslandGit(t, root, "add", "--", privatePath, publicPath)
			evergreenIslandGit(t, root, "commit", "-m", "encrypted island baseline")
			committer, err := evergreencore.NewLocalGitCommitter(root)
			if err != nil {
				t.Fatal(err)
			}
			private, err := kernelEvergreen.NewRuntime(context.Background(), kernelEvergreen.RuntimeOptions{
				Root: root, Storage: EvergreenDocumentStorage{Root: root, Boxes: []string{encryptedBox}},
				Committer: committer, Faults: &evergreenIslandFault{point: fault},
			})
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := private.InspectClaim(context.Background(), "c-private")
			if err != nil {
				private.Close()
				t.Fatal(err)
			}
			user := evergreencore.Principal{Type: evergreencore.PrincipalUser, ID: "user"}
			tags := []string{"updated"}
			plan, err := private.PlanClaimEdit(context.Background(), user, evergreencore.ClaimEditRequest{
				OperationID: "op-private-island", LogicalID: "c-private", Base: snapshot.Base,
				Edit: evergreencore.ClaimEdit{Tags: &tags},
			})
			if err != nil {
				private.Close()
				t.Fatal(err)
			}
			record, err := private.Apply(context.Background(), user, evergreencore.ApplyRequest{Operation: plan})
			if fault == evergreencore.FaultBeforeEvent {
				if err != nil || !record.DerivedStale {
					private.Close()
					t.Fatalf("derived failure lost private authority: %+v %v", record, err)
				}
			} else if err == nil {
				private.Close()
				t.Fatal("private fault did not interrupt")
			}
			if err = private.Close(); err != nil {
				t.Fatal(err)
			}
			privateAfter, err := os.ReadFile(filepath.Join(root, privatePath))
			if err != nil {
				t.Fatal(err)
			}
			lock.Lock()
			*unlocked = false
			lock.Unlock()
			public, err := kernelEvergreen.NewRuntime(context.Background(), kernelEvergreen.RuntimeOptions{
				Root: root, Storage: EvergreenDocumentStorage{Root: root}, Committer: committer,
			})
			if err != nil {
				t.Fatalf("public startup tried to decrypt a locked private journal: %v", err)
			}
			snapshot, err = public.InspectClaim(context.Background(), "c-public")
			if err != nil {
				public.Close()
				t.Fatal(err)
			}
			publicPlan, err := public.PlanClaimEdit(context.Background(), user, evergreencore.ClaimEditRequest{
				OperationID: "op-public-island", LogicalID: "c-public", Base: snapshot.Base,
				Edit: evergreencore.ClaimEdit{Tags: &tags},
			})
			if err != nil {
				public.Close()
				t.Fatal(err)
			}
			if records, err := public.Recover(context.Background()); err != nil || len(records) != 0 {
				public.Close()
				t.Fatalf("public recovery crossed a locked island: %+v %v", records, err)
			}
			result, applyErr := public.Apply(context.Background(), user, evergreencore.ApplyRequest{Operation: publicPlan})
			if fault == evergreencore.FaultBeforeEvent {
				if applyErr != nil || result.State != evergreencore.OperationCompleted {
					public.Close()
					t.Fatalf("completed stale private journal blocked public authority: %+v %v", result, applyErr)
				}
			} else if !evergreencore.HasDiagnostic(applyErr, evergreencore.CodeTransactionBlocked) {
				public.Close()
				t.Fatalf("locked unfinished journal did not globally block: %v", applyErr)
			}
			actual, err := os.ReadFile(filepath.Join(root, privatePath))
			if err != nil || !bytes.Equal(actual, privateAfter) {
				public.Close()
				t.Fatal("public recovery rewrote private ciphertext")
			}
			public.Close()
			lock.Lock()
			*unlocked = true
			lock.Unlock()
			private, err = kernelEvergreen.NewRuntime(context.Background(), kernelEvergreen.RuntimeOptions{
				Root: root, Storage: EvergreenDocumentStorage{Root: root, Boxes: []string{encryptedBox}}, Committer: committer,
			})
			if err != nil {
				t.Fatalf("authenticated private recovery failed: %v", err)
			}
			retry, err := private.Apply(context.Background(), user, evergreencore.ApplyRequest{Operation: plan})
			private.Close()
			if err != nil || retry.State != evergreencore.OperationCompleted || retry.DerivedStale {
				t.Fatalf("private recovery did not complete its projection: %+v %v", retry, err)
			}
			lock.Lock()
			*unlocked = false
			lock.Unlock()
			public, err = kernelEvergreen.NewRuntime(context.Background(), kernelEvergreen.RuntimeOptions{
				Root: root, Storage: EvergreenDocumentStorage{Root: root}, Committer: committer,
			})
			if err != nil {
				t.Fatalf("completed private journal required its key: %v", err)
			}
			result, err = public.Apply(context.Background(), user, evergreencore.ApplyRequest{Operation: publicPlan})
			public.Close()
			if count := evergreenIslandGit(t, root, "rev-list", "--count", "HEAD"); err != nil || result.State != evergreencore.OperationCompleted || count != "3\n" {
				t.Fatalf("island retry omitted or duplicated commits: %+v %v count=%s", result, err, count)
			}
			for _, operationID := range []string{"op-private-island", "op-public-island"} {
				count := evergreenIslandGit(t, root, "log", "--all", "--fixed-strings",
					"--grep=Evergreen-Operation-ID: "+operationID, "--format=%H")
				if len(strings.Fields(count)) != 1 {
					t.Fatalf("operation %s did not have exactly one Git commit: %s", operationID, count)
				}
			}
			storedAfter, err := os.ReadFile(filepath.Join(root, privatePath))
			decoded, decodeErr := decryptDataWithDEK(encryptedBox, "/20261011120100-crypt02.sy", storedAfter, key)
			if err != nil || decodeErr != nil || !bytes.Equal(decoded, plan.Images[0].Bytes) {
				t.Fatal("native reader could not authenticate recovered private ciphertext")
			}
		})
	}
}

func evergreenIslandGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = root
	for _, variable := range os.Environ() {
		if !strings.HasPrefix(variable, "GIT_") {
			command.Env = append(command.Env, variable)
		}
	}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Git island fixture %v: %v %s", args, err, output)
	}
	return string(output)
}

func TestEvergreenRuntimeNativeStorageJournalRecovery(t *testing.T) {
	root, normalBox, encryptedBox, key, _, _ := setupEvergreenStorage(t)
	path := encryptedBox + "/20261011120100-crypt02.sy"
	raw := evergreenStorageFixture("c-private", "20261011120100-crypt02")
	storedBefore, err := encryptDataWithDEK(encryptedBox, "/20261011120100-crypt02.sy", raw, key)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, path), storedBefore, 0600); err != nil {
		t.Fatal(err)
	}
	publicPath := normalBox + "/20261011120100-normal2.sy"
	if err = os.WriteFile(filepath.Join(root, publicPath), evergreenStorageFixture("c-public", "20261011120100-normal2"), 0600); err != nil {
		t.Fatal(err)
	}
	committer := &evergreenStorageCommitter{}
	runtime, err := kernelEvergreen.NewRuntime(context.Background(), kernelEvergreen.RuntimeOptions{
		Root: root, Storage: EvergreenDocumentStorage{Root: root, Boxes: []string{encryptedBox}},
		Committer: committer, Faults: &evergreenStorageFault{},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if _, err = runtime.InspectClaim(context.Background(), "c-public"); err == nil {
		t.Fatal("private runtime crossed into normal notebook")
	}
	snapshot, err := runtime.InspectClaimByDocumentID(context.Background(), "20261011120100-crypt02")
	if err != nil {
		t.Fatal(err)
	}
	user := evergreencore.Principal{Type: evergreencore.PrincipalUser, ID: "user"}
	tags := []string{"updated"}
	planned, err := runtime.PlanClaimEdit(context.Background(), user, evergreencore.ClaimEditRequest{
		OperationID: "op-native-storage", LogicalID: "c-private", Base: snapshot.Base,
		Edit: evergreencore.ClaimEdit{Tags: &tags},
	})
	if err != nil {
		t.Fatal(err)
	}
	record, err := runtime.Apply(context.Background(), user, evergreencore.ApplyRequest{Operation: planned})
	if err == nil || record.State != evergreencore.OperationPrepared {
		t.Fatalf("native storage preparation fault: %+v %v", record, err)
	}
	journal := filepath.Join(root, filepath.FromSlash(evergreencore.RuntimeDirName), "transactions", "op-native-storage")
	storedAfter, err := os.ReadFile(filepath.Join(journal, "after", "0000"))
	if err != nil || bytes.Equal(storedAfter, planned.Images[0].Bytes) {
		t.Fatal("runtime wrote plaintext journal after-image")
	}
	for _, relative := range []string{"manifest.json", "before/0000", "after/0000"} {
		persisted, err := os.ReadFile(filepath.Join(journal, relative))
		if err != nil || bytes.Contains(persisted, []byte("private body")) {
			t.Fatal("runtime journal leaked plaintext body")
		}
	}
	decoded, err := decryptDataWithDEK(encryptedBox, "/20261011120100-crypt02.sy", storedAfter, key)
	if err != nil || !bytes.Equal(decoded, planned.Images[0].Bytes) {
		t.Fatal("journal does not use compatible native ciphertext")
	}
	if records, err := runtime.Recover(context.Background()); err != nil || len(records) != 1 {
		t.Fatalf("native recovery: %+v %v", records, err)
	}
	retry, err := runtime.Apply(context.Background(), user, evergreencore.ApplyRequest{Operation: planned})
	if err != nil || retry.State != evergreencore.OperationCompleted || committer.count != 1 {
		t.Fatalf("native retry: %+v %v commits=%d", retry, err, committer.count)
	}
	actual, err := os.ReadFile(filepath.Join(root, path))
	if err != nil || !bytes.Equal(actual, storedAfter) {
		t.Fatal("retry regenerated native ciphertext")
	}
}
