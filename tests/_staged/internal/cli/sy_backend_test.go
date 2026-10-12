package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
)

func TestSYCLISearchContextShowRelationsUseAuthorityAndOnlineTransport(t *testing.T) {
	root := t.TempDir()
	envelope := &core.DocumentEnvelope{Spec: core.DocumentSpec,
		Entity: core.Entity{LogicalID: "k-cli", EntityType: core.EntityClaim, Schema: "evergreen.claim/v1", SemanticRevision: 1},
		Claim: &core.Claim{ClaimKind: core.KnowledgeKind, KindSchema: core.KnowledgeKindSchema,
			Status: "active", Tags: []string{"native"}, KindData: json.RawMessage(`{}`)},
		Extra: core.RawObject{"legacy_metadata": json.RawMessage(`{"domain":"test","created_at":"2026-10-10","updated_at":"2026-10-10T12:00:00Z"}`)},
		Relations: core.Relations{Outgoing: []core.TypedEdge{{ID: "edge-cli", Schema: core.ArgumentSchema,
			Type: "supports", Reason: "Evidence establishes this relation.", Target: core.EntityRef{EntityType: core.EntityClaim, LogicalID: "o-peer"}}}}}
	raw, err := core.NewMarkdownDocument("k-cli", "Native CLI", []byte("# Native CLI\n\nEvidence body.\n"), envelope, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "cli.sy"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	sourceRaw, err := core.NewMarkdownDocument("s-cli", "CLI source", []byte("Source evidence.\n"),
		&core.DocumentEnvelope{Spec: core.DocumentSpec,
			Entity:    core.Entity{LogicalID: "s-cli", EntityType: core.EntitySource, Schema: "evergreen.source/v1", SemanticRevision: 1},
			Relations: core.Relations{Outgoing: []core.TypedEdge{}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "source.sy"), sourceRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	repository, err := core.NewSYReadRepository(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := core.CanonicalExport(context.Background(), repository, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/evergreen/v1/capabilities":
			_ = json.NewEncoder(w).Encode(core.APIResponse[core.Capabilities]{OK: true, Data: core.CurrentCapabilities()})
		case "/api/evergreen/v1/workspace/export":
			_ = json.NewEncoder(w).Encode(core.APIResponse[core.CanonicalArchive]{OK: true, Data: archive})
		case "/api/evergreen/v1/sources/plan-capture":
			if request.Header.Get("X-Evergreen-Caller") != "agent" ||
				request.Header.Get("X-Evergreen-Agent-ID") != "test-agent" ||
				request.Header.Get("X-Evergreen-Request-Reason") != "read authority parity" {
				t.Fatalf("online capture identity headers = %v", request.Header)
			}
			var capture core.SourceCaptureRequest
			if err := json.NewDecoder(request.Body).Decode(&capture); err != nil {
				t.Fatal(err)
			}
			if capture.Body != "Online cleaned body.\n" || capture.Reason != "online capture" {
				t.Fatalf("online capture request = %+v", capture)
			}
			_ = json.NewEncoder(w).Encode(core.APIResponse[core.PlannedSourceCapture]{
				OK: true,
				Data: core.PlannedSourceCapture{
					SourceID: "s-remote", Path: "remote/source.sy", Deduped: true, MatchedBy: "url",
				},
			})
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()
	for _, args := range [][]string{
		{"search", "Evidence", "--kind", "all", "--limit", "0"},
		{"card", "show", "k-cli"},
		{"rel", "k-cli"},
		{"context", "--source", "s-cli"},
	} {
		t.Setenv("EG_KERNEL_URL", "")
		offline := runSYCLI(t, root, args)
		t.Setenv("EG_KERNEL_URL", server.URL)
		t.Setenv("EG_AGENT_ID", "test-agent")
		t.Setenv("EG_REQUEST_REASON", "read authority parity")
		online := runSYCLI(t, root, args)
		if !reflect.DeepEqual(offline, online) {
			t.Fatalf("online/offline differs for %v", args)
		}
	}
	bodyPath := filepath.Join(t.TempDir(), "online.md")
	if err = os.WriteFile(bodyPath, []byte("Online cleaned body.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	beforeFiles, _ := filepath.Glob(filepath.Join(root, "*.sy"))
	captured := runSYCLI(t, root, []string{
		"capture", "--url", "https://example.com/remote", "--title", "Remote Source",
		"--body-file", bodyPath, "--reason", "online capture",
		"--captured-at", "2026-10-12T18:00:00+08:00",
	})
	data := captured["data"].(map[string]any)
	if data["source_id"] != "s-remote" || data["deduped"] != true || data["operation"] != nil {
		t.Fatalf("online capture result = %#v", data)
	}
	afterFiles, _ := filepath.Glob(filepath.Join(root, "*.sy"))
	if !reflect.DeepEqual(beforeFiles, afterFiles) {
		t.Fatalf("online capture wrote the local workspace: before=%v after=%v", beforeFiles, afterFiles)
	}
	t.Setenv("EG_KERNEL_URL", server.URL+"/unavailable")
	var output bytes.Buffer
	code := New().Run([]string{"search", "Evidence", "--backend", "sy", "--vault", root, "--json"}, &output, &output)
	if code == 0 {
		t.Fatal("failed online transport fell back to direct reads")
	}
	if _, err = os.Stat(filepath.Join(root, core.RuntimeDirName)); !os.IsNotExist(err) {
		t.Fatal("read-only CLI created a runtime lease")
	}
}

func TestSYCLICaptureUsesSharedCoreOfflineAndDeduplicates(t *testing.T) {
	setGitIdentity(t)
	root := t.TempDir()
	gitOut(t, root, "init", "-q")
	if err := os.WriteFile(filepath.Join(root, "seed.txt"), []byte("seed"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitOut(t, root, "add", ".")
	gitOut(t, root, "commit", "-qm", "initial")
	bodyDir := t.TempDir()
	firstBody := filepath.Join(bodyDir, "first.md")
	secondBody := filepath.Join(bodyDir, "second.md")
	if err := os.WriteFile(firstBody, []byte("Original cleaned Source body.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondBody, []byte("Replacement body must not overwrite authority.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EG_KERNEL_URL", "")
	t.Setenv("EG_AGENT_ID", "capture-agent")
	t.Setenv("EG_REQUEST_REASON", "capture source through shared core")

	first := runSYCLI(t, root, []string{
		"capture", "--url", "https://EXAMPLE.com/article/?utm_source=test",
		"--title", "Original title", "--body-file", firstBody, "--reason", "first reason",
		"--domain", "systems", "--captured-at", "2026-10-12T10:00:00+08:00",
	})
	if first["ok"] != true {
		t.Fatalf("first capture = %#v", first)
	}
	second := runSYCLI(t, root, []string{
		"capture", "--url", "https://example.com/article", "--title", "Conflicting title",
		"--body-file", secondBody, "--reason", "second reason", "--domain", "other",
		"--captured-at", "2026-10-12T10:01:00+08:00",
	})
	if second["ok"] != true {
		t.Fatalf("duplicate capture = %#v", second)
	}
	data := second["data"].(map[string]any)
	if data["deduped"] != true || data["source_id"] != "s-20261012-original-title" {
		t.Fatalf("duplicate capture data = %#v", data)
	}
	paths, err := filepath.Glob(filepath.Join(root, "20000101000000-egvault", "*.sy"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("Source files = %#v, err=%v", paths, err)
	}
	raw, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	document, err := core.DecodeSY(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	var metadata struct {
		URL     string   `json:"url"`
		Reasons []string `json:"reasons"`
		Domain  string   `json:"domain"`
	}
	if err = json.Unmarshal(document.Envelope.Extension["source"], &metadata); err != nil {
		t.Fatal(err)
	}
	rootObject := map[string]json.RawMessage{}
	_ = json.Unmarshal(raw, &rootObject)
	body, err := core.MarkdownText(rootObject["Children"])
	if err != nil {
		t.Fatal(err)
	}
	if metadata.URL != "https://EXAMPLE.com/article/?utm_source=test" ||
		metadata.Domain != "systems" ||
		!reflect.DeepEqual(metadata.Reasons, []string{"first reason", "second reason"}) ||
		string(body) != "Original cleaned Source body.\n" {
		t.Fatalf("duplicate capture rewrote Source: metadata=%+v body=%q", metadata, body)
	}
	if got := gitLogCount(t, root); got != 3 {
		t.Fatalf("capture Git commits = %d, want initial + create + reason append", got)
	}
}

func TestSYCLINoteCreateUsesSharedCoreOfflineAndOnlinePlanning(t *testing.T) {
	setGitIdentity(t)
	root := t.TempDir()
	gitOut(t, root, "init", "-q")
	if err := os.WriteFile(filepath.Join(root, "seed.txt"), []byte("seed"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitOut(t, root, "add", ".")
	gitOut(t, root, "commit", "-qm", "initial")
	bodyPath := filepath.Join(t.TempDir(), "source.md")
	if err := os.WriteFile(bodyPath, []byte("Source content for a managed Note.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EG_KERNEL_URL", "")
	t.Setenv("EG_AGENT_ID", "note-cli-agent")
	t.Setenv("EG_REQUEST_REASON", "create reviewed Note")
	captured := runSYCLI(t, root, []string{
		"capture", "--url", "https://example.com/note-cli", "--title", "CLI Note Source",
		"--body-file", bodyPath, "--reason", "prepare Note",
		"--captured-at", "2026-10-12T18:00:00+08:00",
	})
	sourceID := core.LogicalID(captured["data"].(map[string]any)["source_id"].(string))
	request := core.NoteCreateRequest{
		OperationID: "op-note-cli", LogicalID: "n-note-cli",
		DocumentID: "20261012180100-note001", Path: "box/note.sy",
		SourceID: sourceID, Title: "CLI reviewed Note",
		Segments: []core.NoteCreateSegment{{
			SegmentID: "seg-note-cli", Role: "note_segment",
			Locator: &core.Locator{Kind: "source-lines", Value: "L1-L1"},
			Content: json.RawMessage(`[
				{"ID":"20261012180101-seg0001","Type":"NodeParagraph","Data":"Source content"}
			]`),
		}},
		Candidates: []core.NoteCreateCandidate{{
			CandidateID: "cand-note-cli", ClaimKind: core.KnowledgeKind,
			Title: "CLI claim", SegmentRefs: []core.LogicalID{"seg-note-cli"},
			Reason: "The selected source content supports this claim.",
			Payload: json.RawMessage(`[
				{"ID":"20261012180102-payload","Type":"NodeParagraph","Data":"CLI claim body"}
			]`),
		}},
		Coverage: []core.CoverageModule{{
			ModuleID: "coverage-note-cli", Disposition: "candidate",
			SegmentRefs: []core.LogicalID{"seg-note-cli"}, CandidateID: "cand-note-cli",
		}},
	}
	requestRaw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	requestPath := filepath.Join(t.TempDir(), "note-create.json")
	if err = os.WriteFile(requestPath, requestRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	created := runSYCLI(t, root, []string{"note", "create", "--request", requestPath})
	data := created["data"].(map[string]any)
	if data["note_id"] != string(request.LogicalID) ||
		data["source_id"] != string(sourceID) ||
		data["path"] != request.Path {
		t.Fatalf("offline Note create = %#v", data)
	}
	repository, err := core.NewSYReadRepository(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	noteRaw, err := repository.Load(context.Background(), request.LogicalID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := core.InspectNoteReview(noteRaw, nil)
	if err != nil || !snapshot.CanMaterialize ||
		snapshot.Candidates[0].Metadata.State != "draft" {
		t.Fatalf("offline Note snapshot = %+v err=%v", snapshot, err)
	}
	if got := gitLogCount(t, root); got != 3 {
		t.Fatalf("Note CLI Git commits = %d, want initial + Source + Note", got)
	}

	archive, err := core.CanonicalExport(context.Background(), repository, nil)
	if err != nil {
		t.Fatal(err)
	}
	var transported core.NoteCreateRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, httpRequest *http.Request) {
		switch httpRequest.URL.Path {
		case "/api/evergreen/v1/capabilities":
			_ = json.NewEncoder(w).Encode(core.APIResponse[core.Capabilities]{
				OK: true, Data: core.CurrentCapabilities(),
			})
		case "/api/evergreen/v1/workspace/export":
			_ = json.NewEncoder(w).Encode(core.APIResponse[core.CanonicalArchive]{
				OK: true, Data: archive,
			})
		case "/api/evergreen/v1/notes/plan-create":
			if httpRequest.Header.Get("X-Evergreen-Caller") != "agent" ||
				httpRequest.Header.Get("X-Evergreen-Agent-ID") != "online-note-agent" ||
				httpRequest.Header.Get("X-Evergreen-Request-Reason") != "plan Note online" {
				t.Fatalf("online Note identity headers = %v", httpRequest.Header)
			}
			if err := json.NewDecoder(httpRequest.Body).Decode(&transported); err != nil {
				t.Fatal(err)
			}
			_ = json.NewEncoder(w).Encode(core.APIResponse[core.PlannedNoteCreate]{
				OK: true,
				Data: core.PlannedNoteCreate{
					NoteID: request.LogicalID, SourceID: sourceID, Path: request.Path,
				},
			})
		default:
			http.NotFound(w, httpRequest)
		}
	}))
	defer server.Close()
	t.Setenv("EG_KERNEL_URL", server.URL)
	t.Setenv("EG_AGENT_ID", "online-note-agent")
	t.Setenv("EG_REQUEST_REASON", "plan Note online")
	planned := runSYCLI(t, root, []string{
		"note", "create", "--request", requestPath, "--dry-run",
	})
	if planned["ok"] != true || transported.OperationID != request.OperationID ||
		transported.SourceID != sourceID ||
		transported.Candidates[0].CandidateID != "cand-note-cli" {
		t.Fatalf("online Note request = %+v result=%#v", transported, planned)
	}
}

func TestSYCLIMaintenanceRequiresEvidenceAndNeverFallsBackFromOnline(t *testing.T) {
	root := t.TempDir()
	raw := claimAVFixtureForCLI(t)
	if err := os.WriteFile(filepath.Join(root, "claim.sy"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	process := exec.Command("sh", "-c", "exit 0")
	if err := process.Run(); err != nil {
		t.Fatal(err)
	}
	record := core.WriterLeaseRecord{
		Mode: "kernel", Nonce: "0123456789abcdef0123456789abcdef",
		Holder: "crashed-kernel", PID: process.ProcessState.Pid(),
		AcquiredAt: "2000-01-01T00:00:00Z",
	}
	leasePath := filepath.Join(root, filepath.FromSlash(core.RuntimeDirName), core.WriterLeaseFileName)
	if err := os.MkdirAll(filepath.Dir(leasePath), 0o700); err != nil {
		t.Fatal(err)
	}
	leaseRaw, _ := json.Marshal(record)
	if err := os.WriteFile(leasePath, leaseRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EG_KERNEL_URL", "")
	var output bytes.Buffer
	code := New().Run([]string{
		"recover", "lease", "--backend", "sy", "--vault", root, "--json",
		"--expected-pid", strconv.Itoa(record.PID), "--expected-nonce", "wrong",
	}, &output, &output)
	if code == 0 {
		t.Fatalf("mismatched lease evidence succeeded: %s", output.String())
	}
	after, err := os.ReadFile(leasePath)
	if err != nil || !reflect.DeepEqual(after, leaseRaw) {
		t.Fatalf("mismatched evidence changed lease: err=%v before=%s after=%s", err, leaseRaw, after)
	}
	recovered := runSYCLI(t, root, []string{
		"recover", "lease", "--expected-pid", strconv.Itoa(record.PID),
		"--expected-nonce", record.Nonce,
	})
	if recovered["ok"] != true {
		t.Fatalf("lease recovery = %#v", recovered)
	}
	doctor := runSYCLI(t, root, []string{"doctor"})
	report := doctor["data"].(map[string]any)["doctor"].(map[string]any)
	if report["parity"] != true || report["rebuild_index"] != false {
		t.Fatalf("offline doctor = %#v", report)
	}

	staleOnline := append([]byte(nil), leaseRaw...)
	if err = os.WriteFile(leasePath, staleOnline, 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/evergreen/v1/capabilities" {
			_ = json.NewEncoder(w).Encode(core.APIResponse[core.Capabilities]{
				OK: true, Data: core.CurrentCapabilities(),
			})
			return
		}
		http.Error(w, "offline", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	t.Setenv("EG_KERNEL_URL", server.URL)
	output.Reset()
	code = New().Run([]string{
		"recover", "journal", "--backend", "sy", "--vault", root, "--json",
	}, &output, &output)
	if code == 0 {
		t.Fatalf("failed online recovery fell back offline: %s", output.String())
	}
	after, err = os.ReadFile(leasePath)
	if err != nil || !reflect.DeepEqual(after, staleOnline) {
		t.Fatalf("online transport failure touched local lease: err=%v before=%s after=%s", err, staleOnline, after)
	}
}

func claimAVFixtureForCLI(t *testing.T) []byte {
	t.Helper()
	envelope := &core.DocumentEnvelope{
		Spec: core.DocumentSpec,
		Entity: core.Entity{
			LogicalID: "k-doctor", EntityType: core.EntityClaim,
			Schema: core.ClaimSchema, SemanticRevision: 1,
		},
		Claim: &core.Claim{
			ClaimKind: core.KnowledgeKind, KindSchema: core.KnowledgeKindSchema,
			Status: "active", KindData: json.RawMessage(`{}`),
		},
		Relations: core.Relations{Outgoing: []core.TypedEdge{}},
	}
	raw, err := core.NewMarkdownDocument("k-doctor", "Doctor", []byte("Doctor authority.\n"), envelope, nil)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func runSYCLI(t *testing.T, root string, args []string) map[string]any {
	t.Helper()
	var output bytes.Buffer
	args = append(append([]string{}, args...), "--backend", "sy", "--vault", root, "--json")
	if code := New().Run(args, &output, &output); code != 0 {
		t.Fatalf("CLI %v = %d: %s", args, code, output.String())
	}
	var envelope map[string]any
	if err := json.Unmarshal(output.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope
}

func TestSYCLIBackupCreateFailsClosedBeforeCopy(t *testing.T) {
	t.Run("active writer", func(t *testing.T) {
		workspace, data := backupWorkspaceFixture(t)
		lease, err := core.AcquireKernelLease(data, "backup-red-writer")
		if err != nil {
			t.Fatal(err)
		}
		defer lease.Release()
		before := backupTreeDigest(t, workspace)
		output := filepath.Join(t.TempDir(), "backup")
		code, envelope := runBackupCLI(t, "backup", "create", "--backend", "sy",
			"--vault", data, "--output", output)
		requireBackupCode(t, code, envelope, "EG_WORKSPACE_WRITER_ACTIVE")
		if backupTreeDigest(t, workspace) != before {
			t.Fatal("rejected backup changed the source workspace")
		}
		if _, err = os.Lstat(output); !os.IsNotExist(err) {
			t.Fatalf("rejected backup published output: %v", err)
		}
	})

	t.Run("unfinished journal", func(t *testing.T) {
		_, data := backupWorkspaceFixture(t)
		pending := filepath.Join(data, filepath.FromSlash(core.RuntimeDirName),
			core.TransactionsDirName, "op-backup-pending")
		if err := os.MkdirAll(pending, 0o700); err != nil {
			t.Fatal(err)
		}
		output := filepath.Join(t.TempDir(), "backup")
		code, envelope := runBackupCLI(t, "backup", "create", "--backend", "sy",
			"--vault", data, "--output", output)
		requireBackupCode(t, code, envelope, "EG_TRANSACTION_BLOCKED")
		if _, err := os.Lstat(output); !os.IsNotExist(err) {
			t.Fatalf("journal-blocked backup published output: %v", err)
		}
	})

	for _, test := range []struct {
		name string
		path func(string, string) string
	}{
		{"git", func(_ string, data string) string { return filepath.Join(data, ".git") }},
		{"journal", func(_ string, data string) string {
			return filepath.Join(data, filepath.FromSlash(core.RuntimeDirName), core.TransactionsDirName)
		}},
		{"assets", func(_ string, data string) string { return filepath.Join(data, "assets") }},
		{"config", func(workspace, _ string) string { return filepath.Join(workspace, "conf", "conf.json") }},
	} {
		t.Run("missing "+test.name, func(t *testing.T) {
			workspace, data := backupWorkspaceFixture(t)
			if err := os.RemoveAll(test.path(workspace, data)); err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(t.TempDir(), "backup")
			code, envelope := runBackupCLI(t, "backup", "create", "--backend", "sy",
				"--vault", data, "--output", output)
			requireBackupCode(t, code, envelope, "EG_BACKUP_INVALID")
			if _, err := os.Lstat(output); !os.IsNotExist(err) {
				t.Fatalf("incomplete backup published output: %v", err)
			}
		})
	}

	t.Run("configured online endpoint never falls back", func(t *testing.T) {
		workspace, data := backupWorkspaceFixture(t)
		before := backupTreeDigest(t, workspace)
		t.Setenv("EG_KERNEL_URL", "http://127.0.0.1:1/unavailable")
		output := filepath.Join(t.TempDir(), "backup")
		code, envelope := runBackupCLI(t, "backup", "create", "--backend", "sy",
			"--vault", data, "--output", output)
		requireBackupCode(t, code, envelope, "EG_BACKUP_OFFLINE_REQUIRED")
		if backupTreeDigest(t, workspace) != before {
			t.Fatal("online refusal changed the local workspace")
		}
		if _, err := os.Lstat(output); !os.IsNotExist(err) {
			t.Fatalf("online refusal published output: %v", err)
		}
	})

	t.Run("output inside source is zero effect", func(t *testing.T) {
		workspace, data := backupWorkspaceFixture(t)
		before := backupTreeDigest(t, workspace)
		output := filepath.Join(workspace, "nested", "backup")
		code, envelope := runBackupCLI(t, "backup", "create", "--backend", "sy",
			"--vault", data, "--output", output)
		requireBackupCode(t, code, envelope, "EG_BACKUP_INVALID")
		if backupTreeDigest(t, workspace) != before {
			t.Fatal("unsafe output validation created paths inside the source workspace")
		}
	})
}

func TestSYCLIBackupRejectsMissingOrMismatchedCryptoMaterials(t *testing.T) {
	for _, test := range []struct {
		name       string
		mismatched bool
	}{
		{name: "missing"},
		{name: "mismatched", mismatched: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			workspace, data := backupWorkspaceFixture(t)
			addBackupEncryptedNotebook(t, workspace, test.mismatched)
			output := filepath.Join(t.TempDir(), "backup")
			code, envelope := runBackupCLI(t, "backup", "create", "--backend", "sy",
				"--vault", data, "--output", output)
			requireBackupCode(t, code, envelope, "EG_BACKUP_CRYPTO_MATERIAL_INVALID")
			if _, err := os.Lstat(output); !os.IsNotExist(err) {
				t.Fatalf("invalid crypto backup published output: %v", err)
			}
		})
	}
}

func TestSYCLIBackupRestoreRejectsCorruptionAndNonemptyTarget(t *testing.T) {
	t.Setenv("EG_KERNEL_URL", "")
	_, data := backupWorkspaceFixture(t)
	archive := filepath.Join(t.TempDir(), "backup")
	code, envelope := runBackupCLI(t, "backup", "create", "--backend", "sy",
		"--vault", data, "--output", archive)
	requireBackupSuccess(t, code, envelope)

	t.Run("checksum mismatch", func(t *testing.T) {
		asset := filepath.Join(archive, "data", "assets", "evidence.txt")
		if err := os.WriteFile(asset, []byte("tampered\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(t.TempDir(), "restored")
		code, envelope := runBackupCLI(t, "backup", "restore", "--backend", "sy",
			"--input", archive, "--target", target)
		requireBackupCode(t, code, envelope, "EG_BACKUP_CHECKSUM_MISMATCH")
		if _, err := os.Lstat(target); !os.IsNotExist(err) {
			t.Fatalf("corrupt restore published target: %v", err)
		}
	})

	t.Run("nonempty target", func(t *testing.T) {
		_, cleanData := backupWorkspaceFixture(t)
		cleanArchive := filepath.Join(t.TempDir(), "backup")
		code, envelope := runBackupCLI(t, "backup", "create", "--backend", "sy",
			"--vault", cleanData, "--output", cleanArchive)
		requireBackupSuccess(t, code, envelope)
		target := filepath.Join(t.TempDir(), "restored")
		if err := os.MkdirAll(target, 0o700); err != nil {
			t.Fatal(err)
		}
		keep := filepath.Join(target, "keep.txt")
		if err := os.WriteFile(keep, []byte("owner data\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		code, envelope = runBackupCLI(t, "backup", "restore", "--backend", "sy",
			"--input", cleanArchive, "--target", target)
		requireBackupCode(t, code, envelope, "EG_BACKUP_TARGET_NOT_EMPTY")
		if raw, err := os.ReadFile(keep); err != nil || string(raw) != "owner data\n" {
			t.Fatalf("nonempty target was changed: %q, %v", raw, err)
		}
	})
}

func TestSYCLIBackupRestorePreservesWorkspaceAndDoctorParity(t *testing.T) {
	t.Setenv("EG_KERNEL_URL", "")
	workspace, data := backupWorkspaceFixture(t)
	sourceBefore := backupTreeDigest(t, workspace)
	archive := filepath.Join(t.TempDir(), "backup")
	code, created := runBackupCLI(t, "backup", "create", "--backend", "sy",
		"--vault", data, "--output", archive)
	requireBackupSuccess(t, code, created)
	if backupTreeDigest(t, workspace) != sourceBefore {
		t.Fatal("successful backup changed the source workspace")
	}
	if _, err := os.Stat(filepath.Join(archive, "backup-manifest.json")); err != nil {
		t.Fatalf("backup manifest missing: %v", err)
	}

	target := filepath.Join(t.TempDir(), "restored-workspace")
	code, restored := runBackupCLI(t, "backup", "restore", "--backend", "sy",
		"--input", archive, "--target", target)
	requireBackupSuccess(t, code, restored)
	createData := created["data"].(map[string]any)["backup"].(map[string]any)
	restoreData := restored["data"].(map[string]any)["restore"].(map[string]any)
	if restoreData["parity"] != true ||
		restoreData["doctor_digest"] != createData["doctor_digest"] ||
		restoreData["archive_digest"] != createData["archive_digest"] ||
		restoreData["git_head"] != createData["git_head"] {
		t.Fatalf("restore parity mismatch: create=%#v restore=%#v", createData, restoreData)
	}
	for _, rel := range []string{
		"data/claim.sy",
		"data/assets/evidence.txt",
		"data/.git/HEAD",
		"data/.siyuan/evergreen/transactions/.keep",
		"conf/conf.json",
	} {
		before, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		after, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(rel)))
		if err != nil || !bytes.Equal(after, before) {
			t.Fatalf("restored %s differs: err=%v", rel, err)
		}
	}
	if got := strings.TrimSpace(gitOut(t, filepath.Join(target, "data"), "rev-parse", "HEAD")); got != restoreData["git_head"] {
		t.Fatalf("restored Git HEAD = %q, want %q", got, restoreData["git_head"])
	}
}

func TestSYCLIBackupRestorePreservesEncryptedRecoveryMaterialsWithoutFalseParity(t *testing.T) {
	t.Setenv("EG_KERNEL_URL", "")
	workspace, data := backupWorkspaceFixture(t)
	addBackupEncryptedNotebook(t, workspace, true)
	boxID := "20261012190000-crypt01"
	boxConfPath := filepath.Join(data, boxID, ".siyuan", "conf.json")
	var boxConf map[string]json.RawMessage
	raw, err := os.ReadFile(boxConfPath)
	if err != nil || json.Unmarshal(raw, &boxConf) != nil {
		t.Fatalf("read encrypted box fixture: %v", err)
	}
	if err = os.WriteFile(filepath.Join(data, boxID, ".siyuan", "notebook-crypto-backup.json"),
		boxConf["boxCrypt"], 0o600); err != nil {
		t.Fatal(err)
	}

	archive := filepath.Join(t.TempDir(), "backup")
	code, created := runBackupCLI(t, "backup", "create", "--backend", "sy",
		"--vault", data, "--output", archive)
	requireBackupSuccess(t, code, created)
	createData := created["data"].(map[string]any)["backup"].(map[string]any)
	if createData["requires_host_doctor"] != true ||
		createData["doctor_digest"] != nil ||
		!reflect.DeepEqual(createData["encrypted_notebooks"], []any{boxID}) {
		t.Fatalf("encrypted backup falsely reported offline doctor parity: %#v", createData)
	}

	target := filepath.Join(t.TempDir(), "restored-workspace")
	code, restored := runBackupCLI(t, "backup", "restore", "--backend", "sy",
		"--input", archive, "--target", target)
	requireBackupSuccess(t, code, restored)
	restoreData := restored["data"].(map[string]any)["restore"].(map[string]any)
	if restoreData["requires_host_doctor"] != true || restoreData["parity"] != false ||
		restoreData["doctor_digest"] != nil {
		t.Fatalf("encrypted restore fabricated semantic parity: %#v", restoreData)
	}
	for _, rel := range []string{
		"data/.siyuan/data-crypto-backup.json",
		"data/" + boxID + "/.siyuan/conf.json",
		"data/" + boxID + "/.siyuan/notebook-crypto-backup.json",
		"data/" + boxID + "/secret.sy",
		"conf/conf.json",
	} {
		before, readErr := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(rel)))
		if readErr != nil {
			t.Fatal(readErr)
		}
		after, readErr := os.ReadFile(filepath.Join(target, filepath.FromSlash(rel)))
		if readErr != nil || !bytes.Equal(after, before) {
			t.Fatalf("encrypted restore changed %s: %v", rel, readErr)
		}
	}
}

func backupWorkspaceFixture(t *testing.T) (workspace, data string) {
	t.Helper()
	setGitIdentity(t)
	workspace = t.TempDir()
	data = filepath.Join(workspace, "data")
	for _, dir := range []string{
		filepath.Join(data, "assets"),
		filepath.Join(data, filepath.FromSlash(core.RuntimeDirName), core.TransactionsDirName),
		filepath.Join(workspace, "conf"),
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(data, "claim.sy"), claimAVFixtureForCLI(t), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "assets", "evidence.txt"), []byte("asset evidence\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, filepath.FromSlash(core.RuntimeDirName),
		core.TransactionsDirName, ".keep"), []byte("journal root\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "conf", "conf.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitOut(t, data, "init", "-q")
	gitOut(t, data, "add", ".")
	gitOut(t, data, "commit", "-qm", "backup fixture")
	return workspace, data
}

type backupArgon2Fixture struct {
	Memory      uint32 `json:"memory"`
	Iterations  uint32 `json:"iterations"`
	Parallelism uint8  `json:"parallelism"`
	KeyLength   uint32 `json:"keyLength"`
}

type backupCryptoFixture struct {
	Enabled         bool                `json:"enabled"`
	MasterSalt      []byte              `json:"masterSalt"`
	KDFParams       backupArgon2Fixture `json:"kdfParams"`
	KEKVerifier     []byte              `json:"kekVerifier"`
	VerifierNonce   []byte              `json:"verifierNonce"`
	AutoLockMinutes int                 `json:"autoLockMinutes"`
	Spec            int                 `json:"spec"`
	BackupID        string              `json:"backupID,omitempty"`
	CreatedAt       int64               `json:"createdAt,omitempty"`
	Checksum        string              `json:"checksum,omitempty"`
	KEKMAC          []byte              `json:"kekMAC,omitempty"`
	HistoryKEKs     [][]byte            `json:"historyKEKs,omitempty"`
}

type backupBoxCryptoFixture struct {
	Spec       int    `json:"spec"`
	WrappedDEK []byte `json:"wrappedDEK"`
	WrapNonce  []byte `json:"wrapNonce"`
	Metadata   []byte `json:"metadata,omitempty"`
	CreatedAt  int64  `json:"createdAt"`
}

func addBackupEncryptedNotebook(t *testing.T, workspace string, mismatched bool) {
	t.Helper()
	data := filepath.Join(workspace, "data")
	boxID := "20261012190000-crypt01"
	boxDir := filepath.Join(data, boxID)
	if err := os.MkdirAll(filepath.Join(boxDir, ".siyuan"), 0o700); err != nil {
		t.Fatal(err)
	}
	global := backupCryptoFixture{
		Enabled: true, MasterSalt: bytes.Repeat([]byte{1}, 16),
		KDFParams:   backupArgon2Fixture{Memory: 65536, Iterations: 3, Parallelism: 4, KeyLength: 32},
		KEKVerifier: backupSENCEnvelope(2), VerifierNonce: bytes.Repeat([]byte{2}, 12),
		AutoLockMinutes: 5, Spec: 1, BackupID: "backup-crypto-fixture", CreatedAt: 1,
		KEKMAC: bytes.Repeat([]byte{3}, 32),
	}
	global.Checksum = backupCryptoChecksum(global)
	globalRaw, _ := json.Marshal(global)
	if err := os.WriteFile(filepath.Join(data, ".siyuan", "data-crypto-backup.json"), globalRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	rootConf, _ := json.Marshal(map[string]any{"notebookCrypto": global})
	if err := os.WriteFile(filepath.Join(workspace, "conf", "conf.json"), rootConf, 0o600); err != nil {
		t.Fatal(err)
	}
	box := backupBoxCryptoFixture{
		Spec: 1, WrappedDEK: backupSENCEnvelope(4), WrapNonce: bytes.Repeat([]byte{4}, 12),
		Metadata: backupSENCEnvelope(5), CreatedAt: 1,
	}
	boxConf, _ := json.Marshal(map[string]any{"encrypted": true, "boxCrypt": box})
	if err := os.WriteFile(filepath.Join(boxDir, ".siyuan", "conf.json"), boxConf, 0o600); err != nil {
		t.Fatal(err)
	}
	backupBox := box
	if mismatched {
		backupBox.CreatedAt++
	}
	boxBackup, _ := json.Marshal(backupBox)
	if err := os.WriteFile(filepath.Join(boxDir, ".siyuan", "notebook-crypto-backup.json"), boxBackup, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(boxDir, "secret.sy"), backupSENCEnvelope(6), 0o600); err != nil {
		t.Fatal(err)
	}
	if !mismatched {
		if err := os.Remove(filepath.Join(data, ".siyuan", "data-crypto-backup.json")); err != nil {
			t.Fatal(err)
		}
	}
}

func backupCryptoChecksum(value backupCryptoFixture) string {
	value.AutoLockMinutes = 0
	value.Checksum = ""
	value.KEKMAC = nil
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func backupSENCEnvelope(marker byte) []byte {
	nonce := bytes.Repeat([]byte{marker}, 12)
	ret := []byte{'S', 'E', 'N', 'C', 1, 1, byte(len(nonce))}
	ret = append(ret, nonce...)
	return append(ret, bytes.Repeat([]byte{marker + 1}, 16)...)
}

func runBackupCLI(t *testing.T, args ...string) (int, map[string]any) {
	t.Helper()
	args = append(append([]string{}, args...), "--json")
	var output bytes.Buffer
	code := New().Run(args, &output, &output)
	var envelope map[string]any
	if err := json.Unmarshal(output.Bytes(), &envelope); err != nil {
		t.Fatalf("backup CLI %v produced invalid JSON (%d): %s", args, code, output.String())
	}
	return code, envelope
}

func requireBackupSuccess(t *testing.T, code int, envelope map[string]any) {
	t.Helper()
	if code != 0 || envelope["ok"] != true {
		t.Fatalf("backup command failed: code=%d envelope=%#v", code, envelope)
	}
}

func requireBackupCode(t *testing.T, code int, envelope map[string]any, want string) {
	t.Helper()
	if code == 0 {
		t.Fatalf("backup command unexpectedly succeeded: %#v", envelope)
	}
	data, ok := envelope["data"].(map[string]any)
	if !ok {
		t.Fatalf("backup error envelope has no data object: code=%d envelope=%#v", code, envelope)
	}
	items, ok := data["errors"].([]any)
	if !ok {
		t.Fatalf("backup error envelope has no errors array: code=%d envelope=%#v", code, envelope)
	}
	for _, item := range items {
		diagnostic, ok := item.(map[string]any)
		if ok && diagnostic["code"] == want {
			return
		}
	}
	t.Fatalf("backup error code = %#v, want %s", items, want)
}

func backupTreeDigest(t *testing.T, root string) string {
	t.Helper()
	var rows []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		row := filepath.ToSlash(rel) + "\x00" + info.Mode().String()
		if !entry.IsDir() {
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(raw)
			row += "\x00" + hex.EncodeToString(sum[:])
		}
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(rows)
	sum := sha256.Sum256([]byte(strings.Join(rows, "\n")))
	return hex.EncodeToString(sum[:])
}
