package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	kernelEvergreen "github.com/siyuan-note/siyuan/kernel/evergreen"
)

func TestEvergreenWorkspaceExportContractIncludesUnknownAndNoWrite(t *testing.T) {
	root := t.TempDir()
	writeEvergreenAPIFixture(t, root, "box/a.sy", "c-a", "before")
	raw := readEvergreenAPIFile(t, root, "box/a.sy")
	document, err := core.DecodeSY(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	document.Envelope.Extra = core.RawObject{"future_field": json.RawMessage(`{"keep":true}`)}
	raw, err = core.EncodeSY(raw, document.Envelope, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "box/a.sy"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	runtime, err := kernelEvergreen.NewRuntime(context.Background(), kernelEvergreen.RuntimeOptions{
		Root: root, Holder: "export-contract", Committer: &apiRecordingCommitter{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	oldProvider := evergreenRuntimeProvider
	evergreenRuntimeProvider = func(context.Context) (*kernelEvergreen.Runtime, error) { return runtime, nil }
	t.Cleanup(func() { evergreenRuntimeProvider = oldProvider })
	engine := evergreenTestEngine(t)
	engine.POST("/api/evergreen/v1/workspace/export", evergreenWorkspaceExport)
	response := performEvergreenAPI[core.CanonicalArchive](t, engine, http.MethodPost,
		"/api/evergreen/v1/workspace/export", map[string]any{}, nil)
	if !response.OK || response.Data.Spec != core.CanonicalExportSpec || len(response.Data.Entities) != 1 {
		t.Fatalf("archive = %+v", response)
	}
	decoded, err := core.DecodeSY(response.Data.Entities[0].Document, nil)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Envelope.Extra["future_field"] == nil {
		t.Fatal("export dropped unknown field")
	}
	if string(readEvergreenAPIFile(t, root, "box/a.sy")) != string(raw) {
		t.Fatal("read-only export mutated authority")
	}
}
