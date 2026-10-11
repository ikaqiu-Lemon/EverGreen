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

func TestAuthorityAuthorizationRunsUnderWriterGate(t *testing.T) {
	root := t.TempDir()
	writeSYFixture(t, root, "box/a.sy", "c-a", "before")
	commits := newRecordingCommitter()
	host, err := NewFilesystemAuthority(FilesystemAuthorityOptions{Root: root, Committer: commits})
	if err != nil {
		t.Fatal(err)
	}
	authorizer := &gateCheckingAuthorizer{host: host}
	service := NewAuthorityService(host, authorizer, nil)
	user := Principal{Type: PrincipalUser, ID: "user"}
	planned, err := service.Plan(context.Background(), user, planFixture(t, root, "op-gated", map[LogicalID]string{"c-a": "after"}))
	if err != nil {
		t.Fatal(err)
	}
	authorizer.checkGate = true
	if _, err = service.Apply(context.Background(), user, ApplyRequest{Operation: planned}); err != nil {
		t.Fatal(err)
	}
	if commits.Count("op-gated") != 1 {
		t.Fatal("locked authorization did not commit once")
	}
}

type gateCheckingAuthorizer struct {
	host      *FilesystemAuthority
	checkGate bool
}

func (a *gateCheckingAuthorizer) Authorize(context.Context, Principal, string, []LogicalID) error {
	if a.checkGate && a.host.writeGate.TryLock() {
		a.host.writeGate.Unlock()
		return errors.New("authorization ran outside the authority writer gate")
	}
	return nil
}

func TestCompletedRetryUsesOriginalBeforeAfterLaterAuthorizedEdit(t *testing.T) {
	registry := DefaultRegistry()
	root := t.TempDir()
	raw := losslessClaimFixture(t, registry)
	writeFile(t, root, "box/a.sy", raw)
	commits := newRecordingCommitter()
	host, err := NewFilesystemAuthority(FilesystemAuthorityOptions{Root: root, Committer: commits, Registry: registry})
	if err != nil {
		t.Fatal(err)
	}
	service := NewAuthorityService(host, allowAllAuthorizer{}, registry)
	agent := Principal{Type: PrincipalAgent, ID: "agent", AuthSource: "fixture", RequestReason: "Edit tags"}
	tags := []string{"first"}
	first, err := service.PlanClaimEdit(context.Background(), agent, ClaimEditRequest{
		OperationID: "op-first", LogicalID: "o-one", Base: semanticHashBytes(t, raw), Edit: ClaimEdit{Tags: &tags},
	})
	if err != nil {
		t.Fatal(err)
	}
	record, err := service.Apply(context.Background(), agent, ApplyRequest{Operation: first})
	if err != nil {
		t.Fatal(err)
	}
	current := readFile(t, root, "box/a.sy")
	user := Principal{Type: PrincipalUser, ID: "user"}
	second, err := service.PlanClaimEdit(context.Background(), user, ClaimEditRequest{
		OperationID: "op-second", LogicalID: "o-one", Base: semanticHashBytes(t, current),
		Edit: ClaimEdit{KindData: json.RawMessage(`{"validation":{"status":"validated"}}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Apply(context.Background(), user, ApplyRequest{Operation: second}); err != nil {
		t.Fatal(err)
	}
	later := readFile(t, root, "box/a.sy")
	retry, err := service.Apply(context.Background(), agent, ApplyRequest{Operation: first})
	if err != nil || retry.GitCommit != record.GitCommit {
		t.Fatalf("original retry must return original result: %+v %v", retry, err)
	}
	assertFileEquals(t, root, "box/a.sy", later)
	if commits.Count("op-first") != 1 || commits.Count("op-second") != 1 {
		t.Fatal("retry duplicated a commit")
	}
}

func TestJournalCorruptionBlocksApplyAndRecoveryWithoutWrites(t *testing.T) {
	for _, mutation := range []string{
		"record hash", "principal", "file count", "logical id", "before ref", "after ref",
		"after checksum", "before checksum", "before bytes", "after bytes", "after symlink",
	} {
		t.Run(mutation, func(t *testing.T) {
			root := t.TempDir()
			writeSYFixture(t, root, "box/a.sy", "c-a", "before")
			before := readFile(t, root, "box/a.sy")
			commits := newRecordingCommitter()
			host, err := NewFilesystemAuthority(FilesystemAuthorityOptions{
				Root: root, Committer: commits, Faults: &oneShotFault{point: FaultAfterPrepared, index: -1},
			})
			if err != nil {
				t.Fatal(err)
			}
			service := NewAuthorityService(host, allowAllAuthorizer{}, nil)
			user := Principal{Type: PrincipalUser, ID: "user"}
			operation, err := service.Plan(context.Background(), user, planFixture(t, root, "op-corrupt", map[LogicalID]string{"c-a": "after"}))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = service.Apply(context.Background(), user, ApplyRequest{Operation: operation}); err == nil {
				t.Fatal("fixture did not stop after preparation")
			}
			journal := filepath.ToSlash(filepath.Join(RuntimeDirName, TransactionsDirName, "op-corrupt"))
			var manifest operationManifest
			if err = json.Unmarshal(readFile(t, root, journal+"/manifest.json"), &manifest); err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "record hash":
				manifest.Record.PlanHash = "sha256:forged"
			case "principal":
				manifest.Operation.Plan.Principal.ID = "forged"
			case "file count":
				manifest.Files = nil
			case "logical id":
				manifest.Files[0].LogicalID = "c-other"
			case "before ref":
				manifest.Files[0].BeforeRef = "../../outside"
			case "after ref":
				manifest.Files[0].AfterRef = "../../outside"
			case "after checksum":
				manifest.Files[0].AfterContent = "sha256:forged"
			case "before checksum":
				manifest.Files[0].BeforeContent = "sha256:forged"
			case "before bytes":
				writeFile(t, root, journal+"/before/0000", syFixture("c-a", "forged before"))
			case "after bytes":
				writeFile(t, root, journal+"/after/0000", syFixture("c-a", "forged after"))
			case "after symlink":
				outside := filepath.Join(t.TempDir(), "outside")
				if err = os.WriteFile(outside, operation.Images[0].Bytes, 0o600); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(root, filepath.FromSlash(journal), "after", "0000")
				if err = os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err = os.Symlink(outside, path); err != nil {
					t.Fatal(err)
				}
			}
			body, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, root, journal+"/manifest.json", body)
			for _, action := range []string{"apply", "recover"} {
				if action == "apply" {
					_, err = service.Apply(context.Background(), user, ApplyRequest{Operation: operation})
				} else {
					_, err = service.Recover(context.Background())
				}
				if !HasDiagnostic(err, CodeTransactionBlocked) {
					t.Errorf("%s accepted corrupt journal: %v", action, err)
				}
				assertFileEquals(t, root, "box/a.sy", before)
				if commits.Count("op-corrupt") != 0 {
					t.Fatal("corrupt journal created a commit")
				}
			}
		})
	}
}

func TestCreateRejectsOccupiedPathAndPhysicalIDAtPlanAndApply(t *testing.T) {
	for _, occupied := range []string{"path", "physical id"} {
		for _, transport := range []string{"plan", "apply"} {
			t.Run(occupied+"/"+transport, func(t *testing.T) {
				root := t.TempDir()
				existing := syFixture("c-other", "existing")
				writeFile(t, root, "box/a.sy", existing)
				provenance := []Provenance{{SourceID: "s-one", NoteID: "n-one", SegmentRefs: []LogicalID{"seg-one"}, Reason: "Evidence"}}
				raw := syFixture("c-new", "new")
				document, err := DecodeSY(raw, nil)
				if err != nil {
					t.Fatal(err)
				}
				document.Envelope.Provenance = provenance
				raw, err = EncodeSY(raw, document.Envelope, nil)
				if err != nil {
					t.Fatal(err)
				}
				path := "box/new.sy"
				if occupied == "path" {
					path = "box/a.sy"
				} else {
					object, _ := decodeRawObject(raw)
					old, _ := decodeRawObject(existing)
					object["ID"] = old["ID"]
					raw, _ = json.Marshal(object)
				}
				commits := newRecordingCommitter()
				host, err := NewFilesystemAuthority(FilesystemAuthorityOptions{Root: root, Committer: commits})
				if err != nil {
					t.Fatal(err)
				}
				service := NewAuthorityService(host, allowAllAuthorizer{}, nil)
				user := Principal{Type: PrincipalUser, ID: "user"}
				request := PlanRequest{
					OperationID: "op-occupied", Protocol: ChangePlanProtocol, Command: "claim.update",
					Base:   []BaseRef{{LogicalID: "c-new", Expected: "absent"}},
					Writes: []WriteInput{{LogicalID: "c-new", Path: path, After: raw}},
				}
				if transport == "plan" {
					_, err = service.Plan(context.Background(), user, request)
				} else {
					hash := semanticHashBytes(t, raw)
					operation := PlannedOperation{
						Plan: ChangePlan{
							OperationID: request.OperationID, Protocol: request.Protocol, Command: request.Command,
							Principal: user, Base: request.Base, Writes: []PlannedWrite{{LogicalID: "c-new", After: hash}},
						},
						Images: []AfterImage{{LogicalID: "c-new", Path: path, Bytes: raw, SemanticHash: hash, ContentHash: contentHash(raw), Create: true}},
					}
					operation.PlanHash = ComputePlanHash(operation.Plan, operation.Images)
					_, err = service.Apply(context.Background(), user, ApplyRequest{Operation: operation})
				}
				expected := CodeBaseMismatch
				if occupied == "physical id" {
					expected = CodeDuplicateID
				}
				if !HasDiagnostic(err, expected) {
					t.Fatalf("occupied target accepted: %v", err)
				}
				assertFileEquals(t, root, "box/a.sy", existing)
				if commits.Count(request.OperationID) != 0 {
					t.Fatal("occupied target committed")
				}
				dir := filepath.Join(root, filepath.FromSlash(RuntimeDirName), TransactionsDirName, request.OperationID)
				if _, err = os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("occupied target created a journal")
				}
			})
		}
	}
}

func TestCreateRetryAfterPrepareCommitsOnce(t *testing.T) {
	root := t.TempDir()
	commits := newRecordingCommitter()
	host, err := NewFilesystemAuthority(FilesystemAuthorityOptions{
		Root: root, Committer: commits, Faults: &oneShotFault{point: FaultAfterPrepared, index: -1},
	})
	if err != nil {
		t.Fatal(err)
	}
	service := NewAuthorityService(host, allowAllAuthorizer{}, nil)
	user := Principal{Type: PrincipalUser, ID: "user"}
	planned, err := service.PlanClaimCreate(context.Background(), user, ClaimCreateRequest{
		OperationID: "op-create", LogicalID: "c-created", DocumentID: "20261010140000-new0001",
		Path: "box/new.sy", Title: "Created", Kind: KnowledgeKind,
		Body:       json.RawMessage(`[{"ID":"20261010140001-new0002","Type":"NodeParagraph","Data":"Created body"}]`),
		Provenance: []Provenance{{SourceID: "s-one", NoteID: "n-one", SegmentRefs: []LogicalID{"seg-one"}, Reason: "Evidence"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Apply(context.Background(), user, ApplyRequest{Operation: planned}); err == nil {
		t.Fatal("create fixture did not stop")
	}
	first, err := service.Apply(context.Background(), user, ApplyRequest{Operation: planned})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Apply(context.Background(), user, ApplyRequest{Operation: planned})
	if err != nil || first.GitCommit != second.GitCommit || commits.Count("op-create") != 1 {
		t.Fatalf("create retry = %+v %v", second, err)
	}
	if !bytes.Equal(readFile(t, root, "box/new.sy"), planned.Images[0].Bytes) {
		t.Fatal("create retry changed after-image")
	}
}

func TestHostRequiresValidatorAndPreservesIncompleteJournal(t *testing.T) {
	root := t.TempDir()
	writeSYFixture(t, root, "box/a.sy", "c-a", "before")
	before := readFile(t, root, "box/a.sy")
	commits := newRecordingCommitter()
	host, err := NewFilesystemAuthority(FilesystemAuthorityOptions{Root: root, Committer: commits})
	if err != nil {
		t.Fatal(err)
	}
	service := NewAuthorityService(host, allowAllAuthorizer{}, nil)
	user := Principal{Type: PrincipalUser, ID: "user"}
	planned, err := service.Plan(context.Background(), user, planFixture(t, root, "op-incomplete", map[LogicalID]string{"c-a": "after"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = host.ApplyOperation(context.Background(), planned, nil); !HasDiagnostic(err, CodeUnauthorized) {
		t.Fatalf("host without validator: %v", err)
	}
	if _, err = host.RecoverOperations(context.Background(), nil); !HasDiagnostic(err, CodeUnauthorized) {
		t.Fatalf("recovery without validator: %v", err)
	}
	dir := filepath.Join(root, filepath.FromSlash(RuntimeDirName), TransactionsDirName, planned.Plan.OperationID)
	if _, err = os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing validator created a journal")
	}
	writeFile(t, root, filepath.ToSlash(filepath.Join(RuntimeDirName, TransactionsDirName, planned.Plan.OperationID, "state")), []byte("PLANNED\n"))
	if _, err = service.Apply(context.Background(), user, ApplyRequest{Operation: planned}); !HasDiagnostic(err, CodeTransactionBlocked) {
		t.Fatalf("incomplete journal was treated as a new operation: %v", err)
	}
	if _, err = service.Recover(context.Background()); !HasDiagnostic(err, CodeTransactionBlocked) {
		t.Fatalf("incomplete journal recovery: %v", err)
	}
	assertFileEquals(t, root, "box/a.sy", before)
	if commits.Count(planned.Plan.OperationID) != 0 {
		t.Fatal("incomplete journal committed")
	}
}
