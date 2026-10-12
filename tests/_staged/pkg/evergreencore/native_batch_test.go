package evergreencore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type beforeGitRewriteFault struct {
	path    string
	content []byte
	fired   bool
}

func (f *beforeGitRewriteFault) Fail(point string, _ int) error {
	if !f.fired && point == FaultBeforeGit {
		f.fired = true
		return os.WriteFile(f.path, f.content, 0o644)
	}
	return nil
}

const ordinaryDocumentID = "20261010170000-native1"
const ordinaryDocumentPath = "box/" + ordinaryDocumentID + ".sy"

func nativeBatchFixture(t *testing.T) (*AuthorityService, *FilesystemAuthority, Principal, PlanRequest, NativeDocumentWrite) {
	t.Helper()
	before := sectionClaimFixture(t)
	after, err := PrepareNativeDocumentEdit(before, mutateSYFixture(t, before, "Edited Claim"), nil)
	if err != nil {
		t.Fatal(err)
	}
	service, host, principal, request, _ := operationAttackFixture(t, PrincipalUser, NativeEditCommand,
		[][]byte{before}, [][]byte{after})
	ordinary := []byte(`{"ID":"` + ordinaryDocumentID + `","Type":"NodeDocument","Spec":"1","Properties":{"title":"Ordinary"},"opaque":9007199254740993123456789,"Children":[{"ID":"20261010170001-native2","Type":"NodeParagraph","Data":"Original ordinary"}]}`)
	writeFile(t, host.Root(), ordinaryDocumentPath, ordinary)
	gitCommand(t, host.Root(), "add", ordinaryDocumentPath)
	gitCommand(t, host.Root(), "commit", "-m", "ordinary baseline")
	node, err := parseReviewNode(ordinary)
	if err != nil {
		t.Fatal(err)
	}
	node.children[0].object["Data"] = json.RawMessage(`"Edited ordinary"`)
	edited, err := node.encode()
	if err != nil {
		t.Fatal(err)
	}
	return service, host, principal, request, NativeDocumentWrite{
		Path: ordinaryDocumentPath, Before: ordinary, After: edited,
	}
}

func TestNativeBatchMixedCommitAndPartialRenameRecovery(t *testing.T) {
	for _, fault := range []string{"", FaultAfterRename, FaultBeforeGit, FaultAfterGit} {
		t.Run(fault, func(t *testing.T) {
			service, host, principal, request, ordinary := nativeBatchFixture(t)
			if fault != "" {
				index := -1
				if fault == FaultAfterRename {
					index = 0
				}
				host.faults = &oneShotFault{point: fault, index: index}
			}
			planned, err := service.PlanNativeBatch(context.Background(), principal, request, []NativeDocumentWrite{ordinary})
			if err != nil {
				t.Fatal(err)
			}
			if len(planned.Images) != 2 || len(planned.Plan.Base) != 2 {
				t.Fatalf("native batch omitted an authority file: %+v", planned)
			}
			record, err := service.Apply(context.Background(), principal, ApplyRequest{Operation: planned})
			if fault == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if err == nil {
					t.Fatal("fault did not interrupt native batch")
				}
				other := planned
				other.Plan.OperationID = "op-blocked-native"
				other.PlanHash = ComputePlanHash(other.Plan, other.Images)
				if _, err = service.Apply(context.Background(), principal, ApplyRequest{Operation: other}); !HasDiagnostic(err, CodeTransactionBlocked) {
					t.Fatalf("unfinished mixed journal did not block new writes: %v", err)
				}
				reopened, err := NewFilesystemAuthority(FilesystemAuthorityOptions{Root: host.Root(), Committer: host.committer})
				if err != nil {
					t.Fatal(err)
				}
				records, err := NewAuthorityService(reopened, CommandPolicy{}, nil).Recover(context.Background())
				if err != nil || len(records) != 1 {
					t.Fatalf("mixed recovery failed: %+v %v", records, err)
				}
				record = records[0]
			}
			if record.State != OperationCompleted {
				t.Fatalf("mixed journal not completed: %+v", record)
			}
			for _, image := range planned.Images {
				assertFileEquals(t, host.Root(), image.Path, image.Bytes)
			}
			retry, err := service.Apply(context.Background(), principal, ApplyRequest{Operation: planned})
			if err != nil || retry.GitCommit != record.GitCommit {
				t.Fatalf("mixed retry failed: %+v %v", retry, err)
			}
			if got := gitCommand(t, host.Root(), "rev-list", "--count", "HEAD"); got != "3\n" {
				t.Fatalf("mixed edit did not produce one Git commit: %s", got)
			}
			if got := gitCommand(t, host.Root(), "diff-tree", "--no-commit-id", "--name-only", "-r", "HEAD"); got != ordinaryDocumentPath+"\nbox/c-a.sy\n" {
				t.Fatalf("commit did not contain the complete write set: %s", got)
			}
		})
	}
}

func TestNativeBatchCommitsJournalAfterImageDespiteLateWorktreeRewrite(t *testing.T) {
	service, host, principal, request, ordinary := nativeBatchFixture(t)
	host.faults = &beforeGitRewriteFault{
		path: filepath.Join(host.Root(), filepath.FromSlash(ordinary.Path)), content: ordinary.Before,
	}
	planned, err := service.PlanNativeBatch(context.Background(), principal, request,
		[]NativeDocumentWrite{ordinary})
	if err != nil {
		t.Fatal(err)
	}
	record, err := service.Apply(context.Background(), principal, ApplyRequest{Operation: planned})
	if err != nil || record.State != OperationCompleted {
		t.Fatalf("late rewrite recovery failed: %+v %v", record, err)
	}
	assertFileEquals(t, host.Root(), ordinary.Path, ordinary.After)
	if committed := []byte(gitCommand(t, host.Root(), "show", "HEAD:"+ordinary.Path)); !bytes.Equal(committed, ordinary.After) {
		t.Fatalf("Git committed bytes outside the journal after-image:\n%s", committed)
	}
}

func TestNativeBatchAdmissionMetadataAndBaseCannotBeForged(t *testing.T) {
	service, host, principal, request, ordinary := nativeBatchFixture(t)
	managedBefore := readFile(t, host.Root(), request.Writes[0].Path)
	planned, err := service.PlanNativeBatch(context.Background(), principal, request, []NativeDocumentWrite{ordinary})
	if err != nil {
		t.Fatal(err)
	}
	transported, _ := json.Marshal(planned)
	var raw PlannedOperation
	if err = json.Unmarshal(transported, &raw); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Apply(context.Background(), principal, ApplyRequest{Operation: raw}); !HasDiagnostic(err, CodeUnauthorized) {
		t.Fatalf("JSON retained native batch admission: %v", err)
	}
	agent := principal
	agent.Type = PrincipalAgent
	if _, err = service.PlanNativeBatch(context.Background(), agent, request, []NativeDocumentWrite{ordinary}); !HasDiagnostic(err, CodeUnauthorized) {
		t.Fatalf("Agent admitted to native batch: %v", err)
	}
	image := planned.Images[1]
	identity := *image.Native
	identity.BeforeContentHash = "sha256:forged"
	planned.Images[1].Native = &identity
	if err = ValidatePlannedOperation(planned, nil); !HasDiagnostic(err, CodeInvalidPlan) {
		t.Fatalf("native base checksum was outside the plan hash: %v", err)
	}
	planned.PlanHash = ComputePlanHash(planned.Plan, planned.Images)
	if _, err = service.Apply(context.Background(), principal, ApplyRequest{Operation: planned}); !HasDiagnostic(err, CodeBaseMismatch) {
		t.Fatalf("forged ordinary before checksum admitted: %v", err)
	}
	assertFileEquals(t, host.Root(), ordinary.Path, ordinary.Before)
	assertFileEquals(t, host.Root(), request.Writes[0].Path, managedBefore)
	if _, err = os.Stat(host.operationDir(request.OperationID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed native admission created a journal")
	}
}

func TestNativeBatchOrdinarySemanticEquivalentBytesStillConflict(t *testing.T) {
	service, host, principal, request, ordinary := nativeBatchFixture(t)
	planned, err := service.PlanNativeBatch(context.Background(), principal, request, []NativeDocumentWrite{ordinary})
	if err != nil {
		t.Fatal(err)
	}
	reformatted := append(append([]byte(nil), ordinary.Before...), '\n')
	if semanticHashBytes(t, reformatted) != semanticHashBytes(t, ordinary.Before) {
		t.Fatal("fixture must change bytes without changing semantic hash")
	}
	writeFile(t, host.Root(), ordinary.Path, reformatted)
	if _, err = service.Apply(context.Background(), principal, ApplyRequest{Operation: planned}); !HasDiagnostic(err, CodeBaseMismatch) {
		t.Fatalf("ordinary native CAS ignored exact before bytes: %v", err)
	}
	assertFileEquals(t, host.Root(), ordinary.Path, reformatted)
	if _, err = os.Stat(host.operationDir(request.OperationID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("stale native base created a journal")
	}
}

func TestNativeBatchRejectsManagedSmugglingPhysicalMovesAndSymlinks(t *testing.T) {
	for _, attack := range []string{"root envelope", "block envelope", "carrier", "existing managed", "ID", "unknown", "path", "symlink", "duplicate"} {
		t.Run(attack, func(t *testing.T) {
			service, host, principal, request, ordinary := nativeBatchFixture(t)
			node, _ := parseReviewNode(ordinary.After)
			switch attack {
			case "root envelope":
				document, _ := DecodeSY(request.Writes[0].After, nil)
				node.object[evergreenJSONKey], _ = MarshalDocumentEnvelope(document.Envelope)
			case "block envelope":
				node.children[0].object[evergreenJSONKey] = json.RawMessage(`{"spec":"evergreen.block/v99"}`)
			case "carrier":
				node.children[0].object["Properties"] = json.RawMessage(`{"custom-eg-internal-envelope":"forged"}`)
			case "existing managed":
				previous, _ := parseReviewNode(ordinary.Before)
				previous.children[0].object[evergreenJSONKey] = json.RawMessage(`{"spec":"evergreen.block/v99"}`)
				ordinary.Before, _ = previous.encode()
				writeFile(t, host.Root(), ordinary.Path, ordinary.Before)
			case "ID":
				node.object["ID"] = json.RawMessage(`"20261010170000-other01"`)
			case "unknown":
				node.object["opaque"] = json.RawMessage(`9007199254740993123456790`)
			case "path":
				ordinary.Path = "../outside.sy"
			case "symlink":
				outside := t.TempDir()
				writeFile(t, outside, ordinaryDocumentID+".sy", ordinary.Before)
				if err := os.Symlink(outside, filepath.Join(host.Root(), "alias")); err != nil {
					t.Fatal(err)
				}
				ordinary.Path = "alias/" + ordinaryDocumentID + ".sy"
			case "duplicate":
			}
			ordinary.After, _ = node.encode()
			writes := []NativeDocumentWrite{ordinary}
			if attack == "duplicate" {
				writes = append(writes, ordinary)
			}
			if _, err := service.PlanNativeBatch(context.Background(), principal, request, writes); err == nil {
				t.Fatal("invalid native mixed write set admitted")
			}
			assertFileEquals(t, host.Root(), ordinaryDocumentPath, ordinary.Before)
			if got := gitCommand(t, host.Root(), "rev-list", "--count", "HEAD"); got != "2\n" {
				t.Fatalf("invalid batch committed authority: %s", got)
			}
		})
	}
}

func TestNativeBatchOrdinaryCreationSharesGitAndProjectionRecovery(t *testing.T) {
	service, host, principal, request, ordinary := nativeBatchFixture(t)
	ordinary.Create, ordinary.Before = true, nil
	if err := os.Remove(filepath.Join(host.Root(), ordinary.Path)); err != nil {
		t.Fatal(err)
	}
	host.indexSink = &nativeDocumentSink{fail: true}
	planned, err := service.PlanNativeBatch(context.Background(), principal, request, []NativeDocumentWrite{ordinary})
	if err != nil {
		t.Fatal(err)
	}
	record, err := service.Apply(context.Background(), principal, ApplyRequest{Operation: planned})
	if err != nil || !record.DerivedStale || record.State != OperationCompleted {
		t.Fatalf("native projection failure must retain committed authority: %+v %v", record, err)
	}
	sink := host.indexSink.(*nativeDocumentSink)
	if len(sink.refs) != 2 || sink.refs[1].DocumentID != ordinaryDocumentID || sink.refs[1].LogicalID != "" {
		t.Fatalf("projection did not receive complete physical refs: %+v", sink.refs)
	}
	sink.fail = false
	recovered, err := service.Recover(context.Background())
	if err != nil || len(recovered) != 1 || recovered[0].DerivedStale {
		t.Fatalf("native projection recovery failed: %+v %v", recovered, err)
	}
	assertFileEquals(t, host.Root(), ordinary.Path, ordinary.After)
	if got := gitCommand(t, host.Root(), "rev-list", "--count", "HEAD"); got != "3\n" {
		t.Fatalf("projection retry duplicated Git: %s", got)
	}
}

func TestNativeBatchOrdinaryOnlyRemainsOutsideLogicalEntityIndex(t *testing.T) {
	service, host, principal, request, ordinary := nativeBatchFixture(t)
	request.Base, request.Writes = nil, nil
	planned, err := service.PlanNativeBatch(context.Background(), principal, request, []NativeDocumentWrite{ordinary})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Apply(context.Background(), principal, ApplyRequest{Operation: planned}); err != nil {
		t.Fatal(err)
	}
	ids, err := host.List(context.Background())
	if err != nil || len(ids) != 1 || ids[0] != "c-a" {
		t.Fatalf("ordinary document became a logical Evergreen entity: %v %v", ids, err)
	}
	assertFileEquals(t, host.Root(), ordinary.Path, ordinary.After)
	if got := gitCommand(t, host.Root(), "rev-list", "--count", "HEAD"); got != "3\n" {
		t.Fatalf("ordinary native commit not unique: %s", got)
	}
}

func TestNativeBatchEncryptedPhysicalImagesStayCiphertextAcrossRecovery(t *testing.T) {
	service, host, principal, request, ordinary := nativeBatchFixture(t)
	storage := &encryptedTestStorage{root: host.Root(), key: bytes.Repeat([]byte{7}, 32), open: true}
	for _, relative := range []string{ordinary.Path, request.Writes[0].Path} {
		plaintext := readFile(t, host.Root(), relative)
		stored, err := (encryptedTestSession{storage}).Encode(relative, plaintext)
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, host.Root(), relative, stored)
	}
	gitCommand(t, host.Root(), "add", ordinary.Path, request.Writes[0].Path)
	gitCommand(t, host.Root(), "commit", "-m", "encrypted baseline")
	host.storage = storage
	host.faults = &oneShotFault{point: FaultAfterRename, index: 0}
	planned, err := service.PlanNativeBatch(context.Background(), principal, request, []NativeDocumentWrite{ordinary})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Apply(context.Background(), principal, ApplyRequest{Operation: planned}); err == nil {
		t.Fatal("encrypted mixed fault did not interrupt")
	}
	journalDir := host.operationDir(request.OperationID)
	var manifest operationManifest
	if err = json.Unmarshal(readFile(t, journalDir, "manifest.json"), &manifest); err != nil {
		t.Fatal(err)
	}
	for index, image := range manifest.Operation.Images {
		if len(image.Bytes) != 0 {
			t.Fatal("native plaintext persisted in manifest")
		}
		for _, relative := range []string{manifest.Files[index].BeforeRef, manifest.Files[index].AfterRef} {
			stored := readFile(t, journalDir, relative)
			if bytes.Contains(stored, []byte("Ordinary")) || bytes.Contains(stored, []byte("Edited ordinary")) {
				t.Fatal("native journal contains plaintext")
			}
		}
	}
	storage.mu.Lock()
	storage.open = false
	storage.mu.Unlock()
	if _, err = service.Recover(context.Background()); err == nil {
		t.Fatal("locked native journal recovered without authentication")
	}
	storage.mu.Lock()
	storage.open = true
	storage.mu.Unlock()
	reopened, err := NewFilesystemAuthority(FilesystemAuthorityOptions{
		Root: host.Root(), Storage: storage, Committer: host.committer,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = NewAuthorityService(reopened, CommandPolicy{}, nil).Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	for index, image := range planned.Images {
		assertFileEquals(t, host.Root(), image.Path, manifest.Files[index].StoredAfter)
		decoded, err := (encryptedTestSession{storage}).Decode(image.Path, readFile(t, host.Root(), image.Path))
		if err != nil || !bytes.Equal(decoded, image.Bytes) {
			t.Fatalf("native recovery changed authenticated content: %v", err)
		}
	}
	if got := gitCommand(t, host.Root(), "rev-list", "--count", "HEAD"); got != "4\n" {
		t.Fatalf("encrypted mixed recovery duplicated Git: %s", got)
	}
}

type nativeDocumentSink struct {
	fail bool
	refs []AuthorityDocumentRef
}

func (s *nativeDocumentSink) Invalidate(context.Context, []LogicalID) error {
	return errors.New("native documents must use complete physical projection refs")
}

func (s *nativeDocumentSink) InvalidateDocuments(_ context.Context, refs []AuthorityDocumentRef) error {
	s.refs = refs
	if s.fail {
		return errors.New("injected native projection failure")
	}
	return nil
}
