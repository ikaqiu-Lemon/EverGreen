package evergreencore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func nativeFileFixture(t *testing.T) (*AuthorityService, *FilesystemAuthority, Principal, PlanRequest, NativeDocumentWrite, []NativeFileWrite) {
	t.Helper()
	service, host, user, request, ordinary := nativeBatchFixture(t)
	ordinary.Delete, ordinary.After = true, nil
	files := []NativeFileWrite{
		{Path: "storage/av/20261012150000-native3.json", Kind: NativeFileAttributeView,
			Before: []byte(`{"id":"20261012150000-native3","opaque":9007199254740993123456789,"keyValues":["old"]}`),
			After:  []byte(`{"id":"20261012150000-native3","opaque":9007199254740993123456789,"keyValues":["new"]}`)},
		{Path: "assets/deleted.bin", Kind: NativeFileAsset, Before: []byte{0, 1, 2, 255}, Delete: true},
		{Path: "assets/created.bin", Kind: NativeFileAsset, After: []byte{255, 0, 10, 13}, Create: true},
		{Path: "20261012150000-native4/.siyuan/conf.json", Kind: NativeFileNotebookConf,
			Before: []byte(`{"name":"old","encrypted":false,"boxCrypt":null,"unknown":9007199254740993123456789}`),
			After:  []byte(`{"name":"new","encrypted":false,"boxCrypt":null,"unknown":9007199254740993123456789}`)},
		{Path: "storage/av/blocks.msgpack", Kind: NativeFileAVCache, Before: []byte{1, 2}, After: []byte{}},
	}
	for _, file := range files {
		if !file.Create {
			writeFile(t, host.Root(), file.Path, file.Before)
			gitCommand(t, host.Root(), "add", file.Path)
		}
	}
	gitCommand(t, host.Root(), "commit", "-m", "auxiliary baseline")
	host.indexSink = &nativeDocumentSink{}
	return service, host, user, request, ordinary, files
}

func assertNativeFileResult(t *testing.T, host *FilesystemAuthority, planned PlannedOperation) {
	t.Helper()
	for _, image := range planned.Images {
		if image.Delete {
			if _, err := os.Lstat(filepath.Join(host.Root(), image.Path)); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("deleted path %s still exists: %v", image.Path, err)
			}
		} else {
			if !bytes.Equal(readFile(t, host.Root(), image.Path), image.Bytes) {
				t.Fatalf("native file after-image changed: %s", image.Path)
			}
		}
	}
}

func TestNativeFilesMixedDeleteAndAuxiliaryCrashRecovery(t *testing.T) {
	for _, fault := range []string{"", FaultAfterPrepared, FaultAfterRename, FaultBeforeGit, FaultAfterGit} {
		t.Run(fault, func(t *testing.T) {
			service, host, user, request, ordinary, files := nativeFileFixture(t)
			planned, err := service.PlanNativeFiles(context.Background(), user, request, []NativeDocumentWrite{ordinary}, files)
			if err != nil {
				t.Fatal(err)
			}
			if len(planned.Images) != 7 || len(planned.Plan.Base) != 7 {
				t.Fatal("complete native write set was not planned")
			}
			if fault != "" {
				index := -1
				if fault == FaultAfterRename {
					index = 3
				}
				host.faults = &oneShotFault{point: fault, index: index}
			}
			record, err := service.Apply(context.Background(), user, ApplyRequest{Operation: planned})
			if fault != "" {
				if err == nil {
					t.Fatal("fault did not interrupt the transaction")
				}
				other := planned
				other.Plan.OperationID = "op-other-files"
				other.PlanHash = ComputePlanHash(other.Plan, other.Images)
				if _, err = service.Apply(context.Background(), user, ApplyRequest{Operation: other}); !HasDiagnostic(err, CodeTransactionBlocked) {
					t.Fatalf("unfinished deletion did not block all new writes: %v", err)
				}
				reopened, err := NewFilesystemAuthority(FilesystemAuthorityOptions{
					Root: host.Root(), Committer: host.committer, IndexSink: host.indexSink,
				})
				if err != nil {
					t.Fatal(err)
				}
				records, err := NewAuthorityService(reopened, CommandPolicy{}, nil).Recover(context.Background())
				if err != nil || len(records) != 1 {
					t.Fatalf("mixed file recovery failed: %+v %v", records, err)
				}
				record = records[0]
			} else if err != nil {
				t.Fatal(err)
			}
			if record.State != OperationCompleted || record.DerivedStale {
				t.Fatalf("file transaction not completed: %+v", record)
			}
			assertNativeFileResult(t, host, planned)
			retry, err := service.Apply(context.Background(), user, ApplyRequest{Operation: planned})
			if err != nil || retry.GitCommit != record.GitCommit {
				t.Fatalf("delete replay failed: %+v %v", retry, err)
			}
			if got := gitCommand(t, host.Root(), "rev-list", "--count", "HEAD"); got != "4\n" {
				t.Fatalf("file transaction did not produce exactly one commit: %s", got)
			}
			names := gitCommand(t, host.Root(), "diff-tree", "--no-commit-id", "--name-only", "-r", "HEAD")
			for _, image := range planned.Images {
				if !strings.Contains(names, image.Path+"\n") {
					t.Fatalf("Git omitted an authority path %s: %s", image.Path, names)
				}
			}
			refs := host.indexSink.(*nativeDocumentSink).refs
			if len(refs) != 7 {
				t.Fatal("derived callback omitted auxiliary or deleted paths")
			}
			for _, ref := range refs {
				if ref.Path == ordinary.Path && (!ref.Delete || ref.DocumentID != ordinaryDocumentID) {
					t.Fatal("deleted document callback lost its identity")
				}
			}
		})
	}
}

func TestNativeFilesCASAdmissionAndDeletionCannotBeForged(t *testing.T) {
	for _, attack := range []string{"auxiliary bytes", "delete bytes", "JSON admission", "Agent", "hash", "managed delete"} {
		t.Run(attack, func(t *testing.T) {
			service, host, user, request, ordinary, files := nativeFileFixture(t)
			if attack == "managed delete" {
				ordinary.Before = readFile(t, host.Root(), request.Writes[0].Path)
				ordinary.Path = request.Writes[0].Path
				if _, err := service.PlanNativeFiles(context.Background(), user, request, []NativeDocumentWrite{ordinary}, files); err == nil {
					t.Fatal("physical delete removed managed metadata")
				}
				return
			}
			planned, err := service.PlanNativeFiles(context.Background(), user, request, []NativeDocumentWrite{ordinary}, files)
			if err != nil {
				t.Fatal(err)
			}
			switch attack {
			case "auxiliary bytes":
				files[0].Before = append(files[0].Before, '\n')
				writeFile(t, host.Root(), files[0].Path, files[0].Before)
			case "delete bytes":
				ordinary.Before = append(ordinary.Before, '\n')
				writeFile(t, host.Root(), ordinary.Path, ordinary.Before)
			case "JSON admission":
				transport, _ := json.Marshal(planned)
				_ = json.Unmarshal(transport, &planned)
			case "Agent":
				user.Type = PrincipalAgent
			case "hash":
				planned.Images[1].Delete = !planned.Images[1].Delete
			}
			if _, err = service.Apply(context.Background(), user, ApplyRequest{Operation: planned}); err == nil {
				t.Fatal("invalid native file transaction admitted")
			}
			assertFileEquals(t, host.Root(), ordinaryDocumentPath, ordinary.Before)
			for _, file := range files {
				if !file.Create {
					assertFileEquals(t, host.Root(), file.Path, file.Before)
				}
			}
			if _, err := os.Stat(host.operationDir(request.OperationID)); !errors.Is(err, fs.ErrNotExist) {
				t.Fatal("rejected native files created a journal")
			}
			if got := gitCommand(t, host.Root(), "rev-list", "--count", "HEAD"); got != "3\n" {
				t.Fatal("rejected native files committed")
			}
		})
	}
}

func TestNativeFilesRejectUnsupportedPathsAndProtectedConfiguration(t *testing.T) {
	for _, attack := range []string{"escape", "runtime", "Git", "managed AV", "AV unknown", "crypto", "unknown", "symlink", "wrong kind", "duplicate"} {
		t.Run(attack, func(t *testing.T) {
			service, host, user, request, ordinary, files := nativeFileFixture(t)
			switch attack {
			case "escape":
				files[0].Path = "../storage/av/20261012150000-native3.json"
			case "runtime":
				files[0].Path = RuntimeDirName + "/manifest.json"
			case "Git":
				files[0].Path = ".git/config"
			case "managed AV":
				files[0].After = []byte(`{"id":"20261012150000-native3","provider":{"type":"evergreen.sy"}}`)
			case "AV unknown":
				files[0].After = []byte(`{"id":"20261012150000-native3","opaque":9007199254740993123456790,"keyValues":["new"]}`)
			case "crypto":
				files[3].After = []byte(`{"name":"new","encrypted":true,"boxCrypt":null,"unknown":9007199254740993123456789}`)
			case "unknown":
				files[3].After = []byte(`{"name":"new","encrypted":false,"boxCrypt":null,"unknown":9007199254740993123456790}`)
			case "symlink":
				if err := os.Symlink(t.TempDir(), filepath.Join(host.Root(), "assets", "alias")); err != nil {
					t.Fatal(err)
				}
				files[2].Path = "assets/alias/created.bin"
			case "wrong kind":
				files[0].Kind = NativeFileAsset
			case "duplicate":
				files = append(files, files[0])
			}
			if _, err := service.PlanNativeFiles(context.Background(), user, request, []NativeDocumentWrite{ordinary}, files); err == nil {
				t.Fatal("invalid auxiliary write set admitted")
			}
			assertFileEquals(t, host.Root(), ordinary.Path, ordinary.Before)
			if got := gitCommand(t, host.Root(), "rev-list", "--count", "HEAD"); got != "3\n" {
				t.Fatal("invalid auxiliary write set committed")
			}
		})
	}
}

func TestNativeFilesInterruptedDeleteRejectsExternalRecreation(t *testing.T) {
	service, host, user, request, ordinary, files := nativeFileFixture(t)
	planned, err := service.PlanNativeFiles(context.Background(), user, request, []NativeDocumentWrite{ordinary}, files)
	if err != nil {
		t.Fatal(err)
	}
	host.faults = &oneShotFault{point: FaultAfterRename, index: 6}
	if _, err = service.Apply(context.Background(), user, ApplyRequest{Operation: planned}); err == nil {
		t.Fatal("delete fault did not interrupt")
	}
	external := []byte("externally recreated bytes")
	writeFile(t, host.Root(), ordinary.Path, external)
	if _, err = service.Recover(context.Background()); !HasDiagnostic(err, CodeTransactionBlocked) {
		t.Fatalf("recovery overwrote a recreated delete target: %v", err)
	}
	assertFileEquals(t, host.Root(), ordinary.Path, external)
	if got := gitCommand(t, host.Root(), "rev-list", "--count", "HEAD"); got != "3\n" {
		t.Fatal("conflicting delete recovery committed")
	}
}

func TestNativeFilesEncryptedDeleteKeepsBeforeCiphertextAndStableAfter(t *testing.T) {
	service, host, user, request, ordinary, files := nativeFileFixture(t)
	storage := &encryptedTestStorage{root: host.Root(), key: bytes.Repeat([]byte{7}, 32), open: true}
	for _, p := range []string{ordinary.Path, request.Writes[0].Path, files[0].Path, files[1].Path, files[3].Path, files[4].Path} {
		stored, err := (encryptedTestSession{storage}).Encode(p, readFile(t, host.Root(), p))
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, host.Root(), p, stored)
		gitCommand(t, host.Root(), "add", p)
	}
	gitCommand(t, host.Root(), "commit", "-m", "encrypted file baseline")
	host.storage = storage
	host.faults = &oneShotFault{point: FaultAfterRename, index: 6}
	planned, err := service.PlanNativeFiles(context.Background(), user, request, []NativeDocumentWrite{ordinary}, files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Apply(context.Background(), user, ApplyRequest{Operation: planned}); err == nil {
		t.Fatal("encrypted delete fault did not interrupt")
	}
	var manifest operationManifest
	_ = json.Unmarshal(readFile(t, host.operationDir(request.OperationID), "manifest.json"), &manifest)
	if manifest.JournalVersion != 3 {
		t.Fatal("delete and auxiliary write set did not use explicit journal v3")
	}
	for index, image := range planned.Images {
		if len(manifest.Operation.Images[index].Bytes) != 0 {
			t.Fatal("plaintext file persisted in manifest")
		}
		if image.Delete {
			if len(manifest.Files[index].StoredAfter) != 0 {
				t.Fatal("deleted file has a ciphertext after-image")
			}
			raw := readFile(t, host.operationDir(request.OperationID), manifest.Files[index].BeforeRef)
			decoded, err := (encryptedTestSession{storage}).Decode(image.Path, raw)
			if err != nil || len(decoded) == 0 {
				t.Fatal("deleted file before-image cannot authenticate")
			}
		}
	}
	storage.open = false
	if _, err = service.Recover(context.Background()); err == nil {
		t.Fatal("locked encrypted deletion recovered")
	}
	storage.open = true
	records, err := service.Recover(context.Background())
	if err != nil || len(records) != 1 || records[0].DerivedStale {
		t.Fatalf("encrypted file recovery failed: %+v %v", records, err)
	}
	for index, image := range planned.Images {
		if image.Delete {
			if _, err = os.Lstat(filepath.Join(host.Root(), image.Path)); !errors.Is(err, fs.ErrNotExist) {
				t.Fatal("encrypted delete target reappeared")
			}
		} else {
			assertFileEquals(t, host.Root(), image.Path, manifest.Files[index].StoredAfter)
			decoded, err := (encryptedTestSession{storage}).Decode(image.Path, readFile(t, host.Root(), image.Path))
			if err != nil || !bytes.Equal(decoded, image.Bytes) {
				t.Fatal("recovery changed authenticated auxiliary bytes")
			}
		}
	}
	if got := gitCommand(t, host.Root(), "rev-list", "--count", "HEAD"); got != "5\n" {
		t.Fatal("encrypted delete recovery duplicated Git")
	}
}

func TestNativeFilesOlderJournalVersionCannotAdmitDelete(t *testing.T) {
	service, host, user, request, ordinary, files := nativeFileFixture(t)
	planned, err := service.PlanNativeFiles(context.Background(), user, request, []NativeDocumentWrite{ordinary}, files)
	if err != nil {
		t.Fatal(err)
	}
	host.faults = &oneShotFault{point: FaultAfterPrepared, index: -1}
	if _, err = service.Apply(context.Background(), user, ApplyRequest{Operation: planned}); err == nil {
		t.Fatal("fixture did not stop before authority mutation")
	}
	var manifest operationManifest
	journal := host.operationDir(request.OperationID)
	_ = json.Unmarshal(readFile(t, journal, "manifest.json"), &manifest)
	manifest.JournalVersion = 2
	raw, _ := json.Marshal(manifest)
	writeFile(t, journal, "manifest.json", raw)
	if _, err = service.Recover(context.Background()); !HasDiagnostic(err, CodeTransactionBlocked) {
		t.Fatalf("older journal admitted an unsupported deletion: %v", err)
	}
	assertFileEquals(t, host.Root(), ordinary.Path, ordinary.Before)
	if got := gitCommand(t, host.Root(), "rev-list", "--count", "HEAD"); got != "3\n" {
		t.Fatal("unsupported journal version committed")
	}
}
