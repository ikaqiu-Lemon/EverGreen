package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
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
