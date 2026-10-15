package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/gin-gonic/gin"
	evergreencore "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	kernelEvergreen "github.com/siyuan-note/siyuan/kernel/evergreen"
	"github.com/siyuan-note/siyuan/kernel/model"
)

func TestEvergreenSourceCaptureAPIUsesAuthenticatedSharedCore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	committer := &apiRecordingCommitter{}
	runtime, err := kernelEvergreen.NewRuntime(context.Background(), kernelEvergreen.RuntimeOptions{
		Root: root, Holder: "source-api-test", Committer: committer,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	oldProvider := evergreenRuntimeProvider
	evergreenRuntimeProvider = func(context.Context) (*kernelEvergreen.Runtime, error) { return runtime, nil }
	t.Cleanup(func() { evergreenRuntimeProvider = oldProvider })

	model.InitJwtKey()
	agentJWT, err := model.CreatePluginJWT("source-agent")
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{
		model.XAuthTokenKey: agentJWT, evergreenCallerHeader: "agent",
		evergreenAgentIDHeader: "source-agent", evergreenRequestReasonHeader: "capture cleaned source",
	}
	engine := evergreenTestEngine(t)
	first := evergreencore.SourceCaptureRequest{
		OperationID: "op-source-api-first", LogicalID: "s-20261012-api",
		DocumentID: "20261012170000-src0001", Path: "box/20261012170000-src0001.sy",
		URL: "https://EXAMPLE.com/source/?utm_source=test", Title: "API Source",
		Body: "Original Source body through authenticated API.", Reason: "first reason",
		Domain: "systems", Tags: []string{"api"}, CapturedAt: "2026-10-12T17:00:00+08:00",
	}
	created := performEvergreenAPI[evergreencore.PlannedSourceCapture](
		t, engine, http.MethodPost, "/api/evergreen/v1/sources/plan-capture", first, headers)
	if !created.OK || created.Data.Operation == nil ||
		created.Data.Operation.Plan.Principal.Type != evergreencore.PrincipalAgent ||
		created.Data.Operation.Plan.Principal.ID != "source-agent" ||
		created.Data.Operation.Plan.Principal.AuthSource != "kernel-plugin-jwt" {
		t.Fatalf("authenticated Source plan = %+v", created)
	}
	applied := performEvergreenAPI[evergreencore.OperationRecord](
		t, engine, http.MethodPost, "/api/evergreen/v1/apply",
		evergreencore.ApplyRequest{Operation: *created.Data.Operation}, headers)
	if !applied.OK || applied.Data.State != evergreencore.OperationCompleted {
		t.Fatalf("Source apply = %+v", applied)
	}
	firstRaw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(first.Path)))
	if err != nil {
		t.Fatal(err)
	}
	var firstRoot map[string]json.RawMessage
	if err = json.Unmarshal(firstRaw, &firstRoot); err != nil {
		t.Fatal(err)
	}

	second := first
	second.OperationID = "op-source-api-second"
	second.LogicalID = "s-20261012-duplicate"
	second.DocumentID = "20261012170001-src0002"
	second.Path = "box/20261012170001-src0002.sy"
	second.URL = "https://example.com/source"
	second.Title = "Conflicting title"
	second.Body = "Conflicting body that must not replace the first Source."
	second.Reason = "second reason"
	second.Domain = "other"
	second.Tags = []string{"replacement"}
	deduped := performEvergreenAPI[evergreencore.PlannedSourceCapture](
		t, engine, http.MethodPost, "/api/evergreen/v1/sources/plan-capture", second, headers)
	if !deduped.OK || !deduped.Data.Deduped || deduped.Data.MatchedBy != "url" ||
		deduped.Data.SourceID != first.LogicalID || deduped.Data.Operation == nil {
		t.Fatalf("duplicate Source plan = %+v", deduped)
	}
	applied = performEvergreenAPI[evergreencore.OperationRecord](
		t, engine, http.MethodPost, "/api/evergreen/v1/apply",
		evergreencore.ApplyRequest{Operation: *deduped.Data.Operation}, headers)
	if !applied.OK {
		t.Fatalf("duplicate reason apply = %+v", applied)
	}
	if committer.count != 2 ||
		committer.requests[0].Command != evergreencore.SourceCaptureCommand ||
		committer.requests[1].Command != evergreencore.SourceCaptureCommand {
		t.Fatalf("Source commits = %+v", committer.requests)
	}

	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(first.Path)))
	if err != nil {
		t.Fatal(err)
	}
	document, err := evergreencore.DecodeSY(raw, nil)
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
	var sourceRoot map[string]json.RawMessage
	_ = json.Unmarshal(raw, &sourceRoot)
	if metadata.URL != first.URL || metadata.Domain != first.Domain ||
		!reflect.DeepEqual(metadata.Reasons, []string{"first reason", "second reason"}) ||
		!reflect.DeepEqual(firstRoot["Children"], sourceRoot["Children"]) {
		t.Fatalf("duplicate API capture rewrote Source: metadata=%+v children=%s", metadata, sourceRoot["Children"])
	}
	if _, err = os.Stat(filepath.Join(root, filepath.FromSlash(second.Path))); !os.IsNotExist(err) {
		t.Fatalf("duplicate API capture created a second file: %v", err)
	}

	retry := second
	retry.OperationID = "op-source-api-retry"
	unchanged := performEvergreenAPI[evergreencore.PlannedSourceCapture](
		t, engine, http.MethodPost, "/api/evergreen/v1/sources/plan-capture", retry, headers)
	if !unchanged.OK || !unchanged.Data.Deduped || unchanged.Data.Operation != nil ||
		unchanged.Data.ReasonAppended || committer.count != 2 {
		t.Fatalf("idempotent Source retry = %+v commits=%d", unchanged, committer.count)
	}
}
