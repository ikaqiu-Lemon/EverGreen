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
