package evergreencore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func TestAuthorityServicePlanApplyAndIdempotency(t *testing.T) {
	root := t.TempDir()
	writeSYFixture(t, root, "box/a.sy", "c-a", "first")
	writeSYFixture(t, root, "box/b.sy", "c-b", "second")

	commits := newRecordingCommitter()
	derived := &recordingIndexSink{}
	authority, err := NewFilesystemAuthority(FilesystemAuthorityOptions{
		Root: root, Committer: commits, IndexSink: derived,
	})
	if err != nil {
		t.Fatal(err)
	}
	service := NewAuthorityService(authority, allowAllAuthorizer{}, nil)
	principal := Principal{Type: PrincipalUser, ID: "user-1"}
	request := planFixture(t, root, "op-0001", map[LogicalID]string{
		"c-a": "updated first",
		"c-b": "updated second",
	})

	planned, err := service.Plan(context.Background(), principal, request)
	if err != nil {
		t.Fatal(err)
	}
	replanned, err := service.Plan(context.Background(), principal, request)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(planned, replanned) {
		t.Fatalf("plan is not deterministic:\nfirst=%+v\nsecond=%+v", planned, replanned)
	}

	record, err := service.Apply(context.Background(), principal, ApplyRequest{Operation: planned})
	if err != nil {
		t.Fatal(err)
	}
	if record.State != OperationCompleted || record.GitCommit == "" || record.DerivedStale {
		t.Fatalf("unexpected operation record: %+v", record)
	}
	if commits.Count("op-0001") != 1 {
		t.Fatalf("commits = %d, want 1", commits.Count("op-0001"))
	}
	if got := semanticHashAt(t, root, "box/a.sy"); got != planned.Plan.Writes[0].After {
		t.Fatalf("after semantic hash = %s, want %s", got, planned.Plan.Writes[0].After)
	}

	retried, err := service.Apply(context.Background(), principal, ApplyRequest{Operation: planned})
	if err != nil {
		t.Fatal(err)
	}
	if retried.GitCommit != record.GitCommit || commits.Count("op-0001") != 1 {
		t.Fatalf("retry was not idempotent: first=%+v retry=%+v commits=%d", record, retried, commits.Count("op-0001"))
	}

	changed := planned
	changed.Images = append([]AfterImage(nil), planned.Images...)
	changed.Images[0].Bytes = mutateSYFixture(t, changed.Images[0].Bytes, "different")
	changed.Images[0].SemanticHash = semanticHashBytes(t, changed.Images[0].Bytes)
	changed.Images[0].ContentHash = contentHash(changed.Images[0].Bytes)
	changed.Plan.Writes[0].After = changed.Images[0].SemanticHash
	changed.PlanHash = ComputePlanHash(changed.Plan, changed.Images)
	if _, err = service.Apply(context.Background(), principal, ApplyRequest{Operation: changed}); !HasDiagnostic(err, CodeOperationIDReused) {
		t.Fatalf("reused operation error = %v", err)
	}
}

func TestAuthorityServiceFailsClosedBeforeAuthorityWrites(t *testing.T) {
	t.Run("stale base", func(t *testing.T) {
		root := t.TempDir()
		writeSYFixture(t, root, "box/a.sy", "c-a", "first")
		before := readFile(t, root, "box/a.sy")
		authority, err := NewFilesystemAuthority(FilesystemAuthorityOptions{Root: root, Committer: newRecordingCommitter()})
		if err != nil {
			t.Fatal(err)
		}
		service := NewAuthorityService(authority, allowAllAuthorizer{}, nil)
		request := planFixture(t, root, "op-stale", map[LogicalID]string{"c-a": "updated"})
		request.Base[0].SemanticHash = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		if _, err = service.Plan(context.Background(), Principal{Type: PrincipalUser, ID: "u"}, request); !HasDiagnostic(err, CodeBaseMismatch) {
			t.Fatalf("stale base error = %v", err)
		}
		assertFileEquals(t, root, "box/a.sy", before)
	})

	t.Run("too new schema", func(t *testing.T) {
		root := t.TempDir()
		raw := syFixture("c-a", "first")
		raw = []byte(string(raw[:])[:])
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil {
			t.Fatal(err)
		}
		var envelope map[string]any
		if err := json.Unmarshal(object["Evergreen"], &envelope); err != nil {
			t.Fatal(err)
		}
		envelope["spec"] = "evergreen.sy/v99"
		object["Evergreen"], _ = json.Marshal(envelope)
		raw, _ = json.Marshal(object)
		writeFile(t, root, "box/a.sy", raw)
		before := append([]byte(nil), raw...)

		authority, err := NewFilesystemAuthority(FilesystemAuthorityOptions{Root: root, Committer: newRecordingCommitter()})
		if err != nil {
			t.Fatal(err)
		}
		service := NewAuthorityService(authority, allowAllAuthorizer{}, nil)
		request := PlanRequest{
			Protocol: ChangePlanProtocol, OperationID: "op-too-new", Command: "claim.update",
			Base:   []BaseRef{{LogicalID: "c-a", SemanticHash: semanticHashBytes(t, raw)}},
			Writes: []WriteInput{{LogicalID: "c-a", After: syFixture("c-a", "updated")}},
		}
		if _, err = service.Plan(context.Background(), Principal{Type: PrincipalUser, ID: "u"}, request); !HasDiagnostic(err, CodeSchemaTooNew) {
			t.Fatalf("too-new error = %v", err)
		}
		assertFileEquals(t, root, "box/a.sy", before)
	})

	t.Run("unauthorized principal", func(t *testing.T) {
		root := t.TempDir()
		writeSYFixture(t, root, "box/a.sy", "c-a", "first")
		before := readFile(t, root, "box/a.sy")
		authority, err := NewFilesystemAuthority(FilesystemAuthorityOptions{Root: root, Committer: newRecordingCommitter()})
		if err != nil {
			t.Fatal(err)
		}
		service := NewAuthorityService(authority, denyAllAuthorizer{}, nil)
		request := planFixture(t, root, "op-denied", map[LogicalID]string{"c-a": "updated"})
		if _, err = service.Plan(context.Background(), Principal{Type: PrincipalAgent, ID: "agent-1"}, request); !HasDiagnostic(err, CodeUnauthorized) {
			t.Fatalf("authorization error = %v", err)
		}
		assertFileEquals(t, root, "box/a.sy", before)
	})
}

func TestFilesystemAuthorityRecoversPartialRenameToCompleteAfterImage(t *testing.T) {
	root := t.TempDir()
	writeSYFixture(t, root, "box/a.sy", "c-a", "first")
	writeSYFixture(t, root, "box/b.sy", "c-b", "second")
	beforeA := readFile(t, root, "box/a.sy")
	beforeB := readFile(t, root, "box/b.sy")
	faults := &oneShotFault{point: FaultAfterRename, index: 0}
	commits := newRecordingCommitter()
	authority, err := NewFilesystemAuthority(FilesystemAuthorityOptions{
		Root: root, Committer: commits, Faults: faults,
	})
	if err != nil {
		t.Fatal(err)
	}
	service := NewAuthorityService(authority, allowAllAuthorizer{}, nil)
	principal := Principal{Type: PrincipalUser, ID: "user-1"}
	planned, err := service.Plan(context.Background(), principal, planFixture(t, root, "op-crash", map[LogicalID]string{
		"c-a": "after-a", "c-b": "after-b",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Apply(context.Background(), principal, ApplyRequest{Operation: planned}); err == nil {
		t.Fatal("fault injection did not stop apply")
	}
	currentA := readFile(t, root, "box/a.sy")
	currentB := readFile(t, root, "box/b.sy")
	if reflect.DeepEqual(currentA, beforeA) == reflect.DeepEqual(currentB, beforeB) {
		t.Fatalf("fixture did not stop at a partial rename state")
	}

	recovered, err := service.Recover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered) != 1 || recovered[0].State != OperationCompleted {
		t.Fatalf("recovered operations = %+v", recovered)
	}
	for index, image := range planned.Images {
		if got := semanticHashAt(t, root, pathForID(image.LogicalID)); got != planned.Plan.Writes[index].After {
			t.Fatalf("%s recovered hash = %s, want %s", image.LogicalID, got, planned.Plan.Writes[index].After)
		}
	}
	if commits.Count("op-crash") != 1 {
		t.Fatalf("recovery commits = %d, want 1", commits.Count("op-crash"))
	}
}

func TestFilesystemAuthorityGitFailureBlocksNewWritesAndRetryCommitsOnce(t *testing.T) {
	root := t.TempDir()
	writeSYFixture(t, root, "box/a.sy", "c-a", "first")
	commits := newRecordingCommitter()
	commits.failNext = errors.New("injected git failure")
	authority, err := NewFilesystemAuthority(FilesystemAuthorityOptions{Root: root, Committer: commits})
	if err != nil {
		t.Fatal(err)
	}
	service := NewAuthorityService(authority, allowAllAuthorizer{}, nil)
	principal := Principal{Type: PrincipalUser, ID: "user-1"}
	first, err := service.Plan(context.Background(), principal, planFixture(t, root, "op-git", map[LogicalID]string{"c-a": "after"}))
	if err != nil {
		t.Fatal(err)
	}
	record, err := service.Apply(context.Background(), principal, ApplyRequest{Operation: first})
	if err == nil || record.State != OperationFilesApplied {
		t.Fatalf("git failure record=%+v err=%v", record, err)
	}

	secondRequest := PlanRequest{
		Protocol: ChangePlanProtocol, OperationID: "op-second", Command: "claim.update",
		Base:   []BaseRef{{LogicalID: "c-a", SemanticHash: semanticHashAt(t, root, "box/a.sy")}},
		Writes: []WriteInput{{LogicalID: "c-a", After: mutateSYFixture(t, readFile(t, root, "box/a.sy"), "second")}},
	}
	second, err := service.Plan(context.Background(), principal, secondRequest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Apply(context.Background(), principal, ApplyRequest{Operation: second}); !HasDiagnostic(err, CodeTransactionBlocked) {
		t.Fatalf("new write while git is pending error = %v", err)
	}

	retried, err := service.Apply(context.Background(), principal, ApplyRequest{Operation: first})
	if err != nil {
		t.Fatal(err)
	}
	if retried.State != OperationCompleted || commits.Count("op-git") != 1 {
		t.Fatalf("retry record=%+v commits=%d", retried, commits.Count("op-git"))
	}
}

func TestFilesystemAuthorityEventFailureMarksDerivedStale(t *testing.T) {
	root := t.TempDir()
	writeSYFixture(t, root, "box/a.sy", "c-a", "first")
	derived := &recordingIndexSink{err: errors.New("injected event failure")}
	authority, err := NewFilesystemAuthority(FilesystemAuthorityOptions{
		Root: root, Committer: newRecordingCommitter(), IndexSink: derived,
	})
	if err != nil {
		t.Fatal(err)
	}
	service := NewAuthorityService(authority, allowAllAuthorizer{}, nil)
	principal := Principal{Type: PrincipalUser, ID: "user-1"}
	planned, err := service.Plan(context.Background(), principal, planFixture(t, root, "op-event", map[LogicalID]string{"c-a": "after"}))
	if err != nil {
		t.Fatal(err)
	}
	record, err := service.Apply(context.Background(), principal, ApplyRequest{Operation: planned})
	if err != nil {
		t.Fatal(err)
	}
	if record.State != OperationCompleted || !record.DerivedStale {
		t.Fatalf("event failure must complete authority and mark stale: %+v", record)
	}

	derived.err = nil
	recovered, err := service.Recover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered) != 1 || recovered[0].DerivedStale {
		t.Fatalf("derived recovery = %+v", recovered)
	}
}

func TestWorkspaceWriterLeaseRejectsOnlineOfflineOverlap(t *testing.T) {
	root := t.TempDir()
	kernel, err := AcquireKernelLease(root, "kernel-test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = AcquireOfflineLease(root, "offline-test"); !HasDiagnostic(err, CodeWorkspaceWriterActive) {
		t.Fatalf("offline overlap error = %v", err)
	}
	if err = kernel.Release(); err != nil {
		t.Fatal(err)
	}

	offline, err := AcquireOfflineLease(root, "offline-test")
	if err != nil {
		t.Fatal(err)
	}
	if err = offline.Release(); err != nil {
		t.Fatal(err)
	}

	writeFile(t, root, filepath.ToSlash(filepath.Join(RuntimeDirName, WriterLeaseFileName)), []byte(`{"mode":"kernel","nonce":"stale"}`))
	if _, err = AcquireOfflineLease(root, "offline-test"); !HasDiagnostic(err, CodeWorkspaceLeaseStale) {
		t.Fatalf("stale lease error = %v", err)
	}
	if err = RecoverStaleLease(root); err != nil {
		t.Fatal(err)
	}
	offline, err = AcquireOfflineLease(root, "offline-test")
	if err != nil {
		t.Fatal(err)
	}
	_ = offline.Release()
}

type allowAllAuthorizer struct{}

func (allowAllAuthorizer) Authorize(context.Context, Principal, string, []LogicalID) error {
	return nil
}

type denyAllAuthorizer struct{}

func (denyAllAuthorizer) Authorize(context.Context, Principal, string, []LogicalID) error {
	return errors.New("denied")
}

type recordingCommitter struct {
	mu       sync.Mutex
	commits  map[string]string
	calls    map[string]int
	failNext error
}

func newRecordingCommitter() *recordingCommitter {
	return &recordingCommitter{commits: map[string]string{}, calls: map[string]int{}}
}

func (c *recordingCommitter) Commit(_ context.Context, request CommitRequest) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if sha := c.commits[request.OperationID]; sha != "" {
		return sha, nil
	}
	if c.failNext != nil {
		err := c.failNext
		c.failNext = nil
		return "", err
	}
	c.calls[request.OperationID]++
	sha := fmt.Sprintf("commit-%s", request.OperationID)
	c.commits[request.OperationID] = sha
	return sha, nil
}

func (c *recordingCommitter) Count(operationID string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[operationID]
}

type recordingIndexSink struct {
	mu      sync.Mutex
	invalid []LogicalID
	err     error
}

func (s *recordingIndexSink) Invalidate(_ context.Context, ids []LogicalID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invalid = append(s.invalid, ids...)
	return s.err
}

type oneShotFault struct {
	mu    sync.Mutex
	point string
	index int
	fired bool
}

func (f *oneShotFault) Fail(point string, index int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.fired && point == f.point && index == f.index {
		f.fired = true
		return errors.New("injected crash")
	}
	return nil
}

func planFixture(t *testing.T, root, operationID string, contents map[LogicalID]string) PlanRequest {
	t.Helper()
	ids := make([]LogicalID, 0, len(contents))
	for id := range contents {
		ids = append(ids, id)
	}
	sortLogicalIDs(ids)
	request := PlanRequest{Protocol: ChangePlanProtocol, OperationID: operationID, Command: "claim.update"}
	for _, id := range ids {
		path := pathForID(id)
		current := readFile(t, root, path)
		request.Base = append(request.Base, BaseRef{LogicalID: id, SemanticHash: semanticHashBytes(t, current)})
		request.Writes = append(request.Writes, WriteInput{
			LogicalID: id,
			After:     mutateSYFixture(t, current, contents[id]),
		})
	}
	return request
}

func pathForID(id LogicalID) string {
	switch id {
	case "c-a":
		return "box/a.sy"
	case "c-b":
		return "box/b.sy"
	default:
		panic("unknown fixture logical ID " + id)
	}
}

func sortLogicalIDs(ids []LogicalID) {
	for i := range ids {
		for j := i + 1; j < len(ids); j++ {
			if ids[j] < ids[i] {
				ids[i], ids[j] = ids[j], ids[i]
			}
		}
	}
}

func writeSYFixture(t *testing.T, root, rel string, id LogicalID, content string) {
	t.Helper()
	writeFile(t, root, rel, syFixture(id, content))
}

func syFixture(id LogicalID, content string) []byte {
	return []byte(fmt.Sprintf(`{
		"ID":"20261010120000-%s",
		"Type":"NodeDocument",
		"Spec":"5",
		"Evergreen":{
			"spec":"evergreen.sy/v1",
			"entity":{"logical_id":%q,"entity_type":"claim","schema":"evergreen.claim/v1","semantic_revision":1},
			"claim":{"claim_kind":"knowledge","kind_schema":"evergreen.claim-kind.knowledge/v1","status":"active","tags":[],"kind_data":{}},
			"relations":{"outgoing":[]}
		},
		"Children":[{"ID":"20261010120001-child","Type":"NodeParagraph","Data":%q}]
	}`, string(id), id, content))
}

func mutateSYFixture(t *testing.T, raw []byte, content string) []byte {
	t.Helper()
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	var children []map[string]json.RawMessage
	if err := json.Unmarshal(root["Children"], &children); err != nil {
		t.Fatal(err)
	}
	children[0]["Data"], _ = json.Marshal(content)
	root["Children"], _ = json.Marshal(children)
	out, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func semanticHashAt(t *testing.T, root, rel string) string {
	t.Helper()
	return semanticHashBytes(t, readFile(t, root, rel))
}

func semanticHashBytes(t *testing.T, raw []byte) string {
	t.Helper()
	hash, err := SemanticHash(raw)
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

func writeFile(t *testing.T, root, rel string, data []byte) {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, root, rel string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func assertFileEquals(t *testing.T, root, rel string, want []byte) {
	t.Helper()
	if got := readFile(t, root, rel); !reflect.DeepEqual(got, want) {
		t.Fatalf("%s changed unexpectedly", rel)
	}
}
