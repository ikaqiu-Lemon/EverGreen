// SiYuan - From thought to insight, with agents
// Copyright (c) 2020-present, b3log.org
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	evergreencore "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	kernelEvergreen "github.com/siyuan-note/siyuan/kernel/evergreen"
	"github.com/siyuan-note/siyuan/kernel/filesys"
	"github.com/siyuan-note/siyuan/kernel/model"
	"github.com/siyuan-note/siyuan/kernel/util"
)

func TestEvergreenAPIPlanApplyOperationAndPrincipalDerivation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	writeEvergreenAPIFixture(t, root, "box/a.sy", "c-a", "before")
	committer := &apiRecordingCommitter{}
	runtime, err := kernelEvergreen.NewRuntime(context.Background(), kernelEvergreen.RuntimeOptions{
		Root: root, Holder: "api-test", Committer: committer,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	oldProvider := evergreenRuntimeProvider
	evergreenRuntimeProvider = func(context.Context) (*kernelEvergreen.Runtime, error) { return runtime, nil }
	t.Cleanup(func() { evergreenRuntimeProvider = oldProvider })

	engine := evergreenTestEngine(t)
	capabilities := performEvergreenAPI[evergreencore.Capabilities](t, engine, http.MethodGet,
		"/api/evergreen/v1/capabilities", nil, map[string]string{evergreenCallerHeader: "cli"})
	if !capabilities.OK || capabilities.Data.APIVersion != evergreencore.APIVersion {
		t.Fatalf("capabilities = %+v", capabilities)
	}
	schemas := performEvergreenAPI[evergreencore.SchemaCatalog](t, engine, http.MethodGet,
		"/api/evergreen/v1/schemas", nil, map[string]string{evergreenCallerHeader: "ui"})
	if !schemas.OK || schemas.Data.Document != evergreencore.DocumentSpec {
		t.Fatalf("schemas = %+v", schemas)
	}

	before := readEvergreenAPIFile(t, root, "box/a.sy")
	beforeHash, _ := evergreencore.SemanticHash(before)
	document, err := evergreencore.DecodeSY(before, nil)
	if err != nil {
		t.Fatal(err)
	}
	document.Envelope.Claim.Tags = []string{"reviewed"}
	after, err := evergreencore.EncodeSY(before, document.Envelope, nil)
	if err != nil {
		t.Fatal(err)
	}
	requestBody := map[string]any{
		"protocol":     evergreencore.ChangePlanProtocol,
		"operation_id": "op-api",
		"command":      "claim.update",
		"principal":    map[string]any{"type": "user", "id": "forged-client-principal"},
		"base": []evergreencore.BaseRef{{
			LogicalID: "c-a", SemanticHash: beforeHash,
		}},
		"writes": []evergreencore.WriteInput{{LogicalID: "c-a", After: after}},
	}
	plannedResponse := performEvergreenAPI[evergreencore.PlannedOperation](t, engine, http.MethodPost,
		"/api/evergreen/v1/plan", requestBody, map[string]string{evergreenCallerHeader: "cli"})
	if !plannedResponse.OK {
		t.Fatalf("plan diagnostics = %+v", plannedResponse.Diagnostics)
	}
	planned := plannedResponse.Data
	if planned.Plan.Principal.ID == "forged-client-principal" ||
		planned.Plan.Principal.Type != evergreencore.PrincipalUser ||
		planned.Plan.Principal.AuthSource != "trusted-loopback" {
		t.Fatalf("kernel trusted client principal: %+v", planned.Plan.Principal)
	}
	uiPlan := performEvergreenAPI[evergreencore.PlannedOperation](t, engine, http.MethodPost,
		"/api/evergreen/v1/plan", requestBody, map[string]string{evergreenCallerHeader: "ui"})
	if !uiPlan.OK || !reflect.DeepEqual(uiPlan.Data, planned) {
		t.Fatalf("UI and CLI produced different plans:\ncli=%+v\nui=%+v", planned, uiPlan.Data)
	}

	model.InitJwtKey()
	agentJWT, err := model.CreatePluginJWT("agent-1")
	if err != nil {
		t.Fatal(err)
	}
	agentPlan := performEvergreenAPI[evergreencore.PlannedOperation](t, engine, http.MethodPost,
		"/api/evergreen/v1/plan", requestBody, map[string]string{
			model.XAuthTokenKey:   agentJWT,
			evergreenCallerHeader: "agent", evergreenAgentIDHeader: "agent-1",
			evergreenRequestReasonHeader: "review candidate",
		})
	if !agentPlan.OK || agentPlan.Data.Plan.Principal.Type != evergreencore.PrincipalAgent ||
		agentPlan.Data.Plan.Principal.AuthSource != "kernel-plugin-jwt" ||
		agentPlan.Data.Plan.Principal.RequestReason != "review candidate" {
		t.Fatalf("agent plan = %+v", agentPlan)
	}
	if !reflect.DeepEqual(agentPlan.Data.Plan.Writes, planned.Plan.Writes) ||
		agentPlan.Data.Images[0].SemanticHash != planned.Images[0].SemanticHash {
		t.Fatalf("callers produced different semantic plans:\ncli=%+v\nagent=%+v", planned, agentPlan.Data)
	}
	agentBodyRequest := map[string]any{
		"protocol": evergreencore.ChangePlanProtocol, "operation_id": "op-agent-body",
		"command": "claim.update",
		"base":    []evergreencore.BaseRef{{LogicalID: "c-a", SemanticHash: beforeHash}},
		"writes":  []evergreencore.WriteInput{{LogicalID: "c-a", After: mutateEvergreenAPIFixture(t, before, "forged core")}},
	}
	denied := performEvergreenAPI[evergreencore.PlannedOperation](t, engine, http.MethodPost,
		"/api/evergreen/v1/plan", agentBodyRequest, map[string]string{
			model.XAuthTokenKey:   agentJWT,
			evergreenCallerHeader: "agent", evergreenAgentIDHeader: "agent-1",
			evergreenRequestReasonHeader: "attempt core edit",
		})
	if denied.OK || len(denied.Diagnostics) != 1 || denied.Diagnostics[0].Code != evergreencore.CodeUnauthorized {
		t.Fatalf("Agent core edit was accepted: %+v", denied)
	}
	if !bytes.Equal(before, readEvergreenAPIFile(t, root, "box/a.sy")) || committer.count != 0 {
		t.Fatal("denied Agent core edit changed authority or committed")
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(evergreencore.RuntimeDirName), "transactions", "op-agent-body")); !os.IsNotExist(err) {
		t.Fatalf("denied Agent core edit created a journal: %v", err)
	}

	applyResponse := performEvergreenAPI[evergreencore.OperationRecord](t, engine, http.MethodPost,
		"/api/evergreen/v1/apply", evergreencore.ApplyRequest{Operation: planned},
		map[string]string{evergreenCallerHeader: "cli"})
	if !applyResponse.OK || applyResponse.Data.State != evergreencore.OperationCompleted {
		t.Fatalf("apply = %+v", applyResponse)
	}
	operationResponse := performEvergreenAPI[evergreencore.OperationRecord](t, engine, http.MethodGet,
		"/api/evergreen/v1/operations/op-api", nil, map[string]string{evergreenCallerHeader: "cli"})
	if !operationResponse.OK || operationResponse.Data.GitCommit == "" {
		t.Fatalf("operation = %+v", operationResponse)
	}
	if committer.count != 1 {
		t.Fatalf("commit count = %d, want 1", committer.count)
	}

	retry := performEvergreenAPI[evergreencore.OperationRecord](t, engine, http.MethodPost,
		"/api/evergreen/v1/apply", evergreencore.ApplyRequest{Operation: planned},
		map[string]string{evergreenCallerHeader: "cli"})
	if !retry.OK || committer.count != 1 {
		t.Fatalf("retry = %+v commits=%d", retry, committer.count)
	}
}

func TestEvergreenAPIAgentRequiresIdentityAndReason(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := evergreenTestEngine(t)
	response := performEvergreenAPI[evergreencore.PlannedOperation](t, engine, http.MethodPost,
		"/api/evergreen/v1/plan", map[string]any{
			"protocol": evergreencore.ChangePlanProtocol, "operation_id": "op-agent",
			"command": "claim.update", "base": []any{}, "writes": []any{},
		}, map[string]string{evergreenCallerHeader: "agent"})
	if response.OK || len(response.Diagnostics) != 1 ||
		response.Diagnostics[0].Code != evergreencore.CodeUnauthorized {
		t.Fatalf("agent authorization response = %+v", response)
	}
}

func TestEvergreenAPIRequestNotebookScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	boxID := "20261012170000-scope01"
	otherBoxID := "20261012170001-scope02"
	writeEvergreenAPIFixture(t, root, boxID+"/20261012170002-claim01.sy", "c-scoped", "scoped")
	if err := os.MkdirAll(filepath.Join(root, otherBoxID), 0700); err != nil {
		t.Fatal(err)
	}
	runtime, err := kernelEvergreen.NewRuntime(context.Background(), kernelEvergreen.RuntimeOptions{
		Root: root, Holder: "api-scope-test", Committer: &apiRecordingCommitter{},
		Storage: filesys.EvergreenDocumentStorage{Root: root},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	oldProvider := evergreenRuntimeProvider
	evergreenRuntimeProvider = func(context.Context) (*kernelEvergreen.Runtime, error) { return runtime, nil }
	t.Cleanup(func() { evergreenRuntimeProvider = oldProvider })

	engine := evergreenTestEngine(t)
	selected := performEvergreenAPI[evergreencore.ClaimSnapshot](t, engine, http.MethodPost,
		"/api/evergreen/v1/claims/get", map[string]string{"logical_id": "c-scoped"},
		map[string]string{evergreenNotebookScopeHeader: boxID})
	if !selected.OK || selected.Data.LogicalID != "c-scoped" {
		t.Fatalf("selected notebook scope failed: %+v", selected)
	}
	excluded := performEvergreenAPI[evergreencore.ClaimSnapshot](t, engine, http.MethodPost,
		"/api/evergreen/v1/claims/get", map[string]string{"logical_id": "c-scoped"},
		map[string]string{evergreenNotebookScopeHeader: otherBoxID})
	if excluded.OK || len(excluded.Diagnostics) != 1 {
		t.Fatalf("unselected notebook escaped request scope: %+v", excluded)
	}
	invalid := performEvergreenAPI[evergreencore.ClaimSnapshot](t, engine, http.MethodPost,
		"/api/evergreen/v1/claims/get", map[string]string{"logical_id": "c-scoped"},
		map[string]string{evergreenNotebookScopeHeader: "not-a-notebook"})
	if invalid.OK || len(invalid.Diagnostics) != 1 ||
		invalid.Diagnostics[0].Code != evergreencore.CodeInvalidPlan {
		t.Fatalf("invalid notebook scope was not rejected: %+v", invalid)
	}
	scopedView := performEvergreenAPI[map[string]string](t, engine, http.MethodPost,
		"/api/evergreen/v1/av/create-view",
		map[string]string{"id": "20261012170003-view001", "preset": "claims"},
		map[string]string{evergreenNotebookScopeHeader: boxID})
	if scopedView.OK || len(scopedView.Diagnostics) != 1 ||
		scopedView.Diagnostics[0].Code != evergreencore.CodeInvalidPlan {
		t.Fatalf("scoped request wrote a global AV definition: %+v", scopedView)
	}
}

func TestEvergreenAPIEncryptedRequestScopePlanAndApply(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	boxID := "20261012171000-crypt01"
	documentPath := boxID + "/20261012171001-claim01.sy"
	writeEvergreenAPIFixture(t, root, documentPath, "c-encrypted", "private scoped claim")
	plaintext := readEvergreenAPIFile(t, root, documentPath)
	key := bytes.Repeat([]byte{0x4d}, 32)
	fileKey := util.DeriveSubKey(key, "siyuan/file")
	aad, err := filesys.SyAAD(boxID, documentPath)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := util.EncryptWithAAD(fileKey, plaintext, []byte(aad))
	for i := range fileKey {
		fileKey[i] = 0
	}
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, filepath.FromSlash(documentPath)), ciphertext, 0600); err != nil {
		t.Fatal(err)
	}
	oldProvider, oldAcquire, oldRelease, oldPolicy := filesys.DEKProvider, filesys.DEKLockAcquire,
		filesys.DEKLockRelease, filesys.EvergreenIsEncryptedBox
	cryptoLock := &sync.RWMutex{}
	filesys.EvergreenIsEncryptedBox = func(id string) bool { return id == boxID }
	filesys.DEKLockAcquire = func(string) { cryptoLock.RLock() }
	filesys.DEKLockRelease = func(string) { cryptoLock.RUnlock() }
	filesys.DEKProvider = func(id string) ([]byte, error) {
		if id == boxID {
			return append([]byte(nil), key...), nil
		}
		return nil, nil
	}
	t.Cleanup(func() {
		filesys.DEKProvider, filesys.DEKLockAcquire = oldProvider, oldAcquire
		filesys.DEKLockRelease, filesys.EvergreenIsEncryptedBox = oldRelease, oldPolicy
	})
	runtime, err := kernelEvergreen.NewRuntime(context.Background(), kernelEvergreen.RuntimeOptions{
		Root: root, Holder: "api-encrypted-scope-test", Committer: &apiRecordingCommitter{},
		Storage: filesys.EvergreenDocumentStorage{Root: root},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	oldRuntimeProvider := evergreenRuntimeProvider
	evergreenRuntimeProvider = func(context.Context) (*kernelEvergreen.Runtime, error) { return runtime, nil }
	t.Cleanup(func() { evergreenRuntimeProvider = oldRuntimeProvider })

	engine := evergreenTestEngine(t)
	headers := map[string]string{
		evergreenCallerHeader: "ui", evergreenNotebookScopeHeader: boxID,
	}
	snapshot := performEvergreenAPI[evergreencore.ClaimSnapshot](t, engine, http.MethodPost,
		"/api/evergreen/v1/claims/get", map[string]string{"logical_id": "c-encrypted"}, headers)
	if !snapshot.OK || snapshot.Data.Title != "private scoped claim" {
		t.Fatalf("encrypted Claim read failed: %+v", snapshot)
	}
	tags := []string{"private-edit"}
	planned := performEvergreenAPI[evergreencore.PlannedOperation](t, engine, http.MethodPost,
		"/api/evergreen/v1/claims/plan-edit", evergreencore.ClaimEditRequest{
			OperationID: "op-encrypted-scope", LogicalID: "c-encrypted", Base: snapshot.Data.Base,
			Edit: evergreencore.ClaimEdit{Tags: &tags},
		}, headers)
	if !planned.OK {
		t.Fatalf("encrypted Claim plan failed: %+v", planned.Diagnostics)
	}
	applied := performEvergreenAPI[evergreencore.OperationRecord](t, engine, http.MethodPost,
		"/api/evergreen/v1/apply", evergreencore.ApplyRequest{Operation: planned.Data}, headers)
	if !applied.OK || applied.Data.State != evergreencore.OperationCompleted {
		t.Fatalf("encrypted Claim apply failed: %+v", applied)
	}
	stored := readEvergreenAPIFile(t, root, documentPath)
	if bytes.Contains(stored, []byte("private-edit")) || !util.IsCiphertext(stored) {
		t.Fatal("encrypted API apply persisted plaintext")
	}
	fileKey = util.DeriveSubKey(key, "siyuan/file")
	decrypted, err := util.DecryptWithAAD(fileKey, stored, []byte(aad))
	for i := range fileKey {
		fileKey[i] = 0
	}
	if err != nil || !bytes.Contains(decrypted, []byte("private-edit")) {
		t.Fatalf("encrypted API result did not authenticate: %v", err)
	}
}

func TestEvergreenAVAPIQueryCellPlanApplyConflictAndRebuild(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	previousDataDir := util.DataDir
	util.DataDir = root
	t.Cleanup(func() { util.DataDir = previousDataDir })
	writeEvergreenAPIFixture(t, root, "box/a.sy", "c-a", "Claim title")
	derivedHealthy := false
	runtime, err := kernelEvergreen.NewRuntime(context.Background(), kernelEvergreen.RuntimeOptions{
		Root: root, Holder: "api-av-test", Committer: &apiRecordingCommitter{},
		InspectDerived: func(context.Context, []evergreencore.AuthorityDocumentRef) (evergreencore.DerivedProjectionStatus, error) {
			return evergreencore.DerivedProjectionStatus{Available: true, Healthy: derivedHealthy}, nil
		},
		RebuildDerived: func(context.Context, []evergreencore.AuthorityDocumentRef) error {
			derivedHealthy = true
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	oldProvider := evergreenRuntimeProvider
	evergreenRuntimeProvider = func(context.Context) (*kernelEvergreen.Runtime, error) { return runtime, nil }
	t.Cleanup(func() { evergreenRuntimeProvider = oldProvider })

	engine := evergreenTestEngine(t)
	created := performEvergreenAPI[map[string]string](t, engine, http.MethodPost,
		"/api/evergreen/v1/av/create-view", map[string]any{
			"id": "20261010120000-avclaim", "preset": "claims",
		}, map[string]string{evergreenCallerHeader: "ui"})
	if !created.OK || created.Data["preset"] != "claims" || created.Data["view_id"] == "" {
		t.Fatalf("create view = %+v", created)
	}
	viewData := readEvergreenAPIFile(t, root, "storage/av/20261010120000-avclaim.json")
	if bytes.Contains(viewData, []byte(`"values"`)) || !bytes.Contains(viewData, []byte(`"provider"`)) {
		t.Fatalf("source-backed view persisted authority rows or omitted provider: %s", viewData)
	}
	describe := performEvergreenAPI[evergreencore.AVViewSchema](t, engine, http.MethodGet,
		"/api/evergreen/v1/av/views/claims", nil, map[string]string{evergreenCallerHeader: "ui"})
	if !describe.OK || describe.Data.Definition.Provider.Type != evergreencore.AVProviderType {
		t.Fatalf("describe = %+v", describe)
	}
	query := performEvergreenAPI[evergreencore.AVPage](t, engine, http.MethodPost,
		"/api/evergreen/v1/av/query", map[string]any{
			"view_id": "claims", "query": map[string]any{},
		}, map[string]string{evergreenCallerHeader: "ui"})
	if !query.OK || query.Data.Total != 1 {
		t.Fatalf("query = %+v", query)
	}
	row := query.Data.Rows[0]
	cell := performEvergreenAPI[evergreencore.AVCellValue](t, engine, http.MethodPost,
		"/api/evergreen/v1/av/cell", map[string]any{
			"row": row.Ref, "column": map[string]any{"field": "claim.status"},
		}, map[string]string{evergreenCallerHeader: "ui"})
	if !cell.OK || cell.Data.Text != "active" {
		t.Fatalf("cell = %+v", cell)
	}
	patch := map[string]any{
		"operation_id": "op-av-api",
		"row_id":       "c-a",
		"base":         row.Ref,
		"column":       map[string]any{"field": "claim.status"},
		"value":        map[string]any{"kind": "text", "text": "archived"},
	}
	planned := performEvergreenAPI[evergreencore.PlannedOperation](t, engine, http.MethodPost,
		"/api/evergreen/v1/av/plan-patch", patch, map[string]string{evergreenCallerHeader: "ui"})
	if !planned.OK || planned.Data.Plan.Command != "claim.update" {
		t.Fatalf("plan patch = %+v", planned)
	}
	applied := performEvergreenAPI[evergreencore.OperationRecord](t, engine, http.MethodPost,
		"/api/evergreen/v1/apply", evergreencore.ApplyRequest{Operation: planned.Data},
		map[string]string{evergreenCallerHeader: "ui"})
	if !applied.OK || applied.Data.State != evergreencore.OperationCompleted {
		t.Fatalf("apply = %+v", applied)
	}
	conflict := performEvergreenAPI[evergreencore.PlannedOperation](t, engine, http.MethodPost,
		"/api/evergreen/v1/av/plan-patch", patch, map[string]string{evergreenCallerHeader: "ui"})
	if conflict.OK || len(conflict.Diagnostics) == 0 ||
		conflict.Diagnostics[0].Code != evergreencore.CodeBaseMismatch {
		t.Fatalf("stale patch = %+v", conflict)
	}
	rebuild := performEvergreenAPI[evergreencore.AVProjectionSnapshot](t, engine, http.MethodPost,
		"/api/evergreen/v1/av/rebuild", nil, map[string]string{evergreenCallerHeader: "ui"})
	if !rebuild.OK || rebuild.Data.ClaimCount != 1 || rebuild.Data.Digest == "" {
		t.Fatalf("rebuild = %+v", rebuild)
	}
	doctor := performEvergreenAPI[evergreencore.WorkspaceDoctorReport](t, engine, http.MethodPost,
		"/api/evergreen/v1/doctor", map[string]any{"rebuild_index": true},
		map[string]string{evergreenCallerHeader: "ui"})
	if !doctor.OK || !doctor.Data.Parity || !doctor.Data.DerivedAfter.Healthy ||
		doctor.Data.Before.Counts.Claims != 1 {
		t.Fatalf("doctor = %+v", doctor)
	}
}

func TestEvergreenReviewAPIReviewConfirmAndMaterialize(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	writeEvergreenReviewAPIFixture(t, root, "box/note.sy")
	committer := &apiRecordingCommitter{}
	runtime, err := kernelEvergreen.NewRuntime(
		context.Background(), kernelEvergreen.RuntimeOptions{
			Root: root, Holder: "api-review-test", Committer: committer,
		})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	oldProvider := evergreenRuntimeProvider
	evergreenRuntimeProvider = func(context.Context) (*kernelEvergreen.Runtime, error) {
		return runtime, nil
	}
	t.Cleanup(func() { evergreenRuntimeProvider = oldProvider })

	engine := evergreenTestEngine(t)
	snapshot := performEvergreenAPI[evergreencore.ReviewSnapshot](
		t, engine, http.MethodPost, "/api/evergreen/v1/review/get",
		map[string]any{"document_id": "20261010140000-note001"},
		map[string]string{evergreenCallerHeader: "ui"})
	if !snapshot.OK || !snapshot.Data.CanMaterialize ||
		len(snapshot.Data.Candidates) != 1 {
		t.Fatalf("review snapshot = %+v", snapshot)
	}
	var candidatePayload []map[string]any
	if err = json.Unmarshal(snapshot.Data.Candidates[0].Payload, &candidatePayload); err != nil {
		t.Fatal(err)
	}
	if len(candidatePayload) != 1 ||
		candidatePayload[0]["Data"] != "reviewed claim" {
		t.Fatalf("candidate payload = %+v", candidatePayload)
	}

	model.InitJwtKey()
	agentJWT, err := model.CreatePluginJWT("review-agent")
	if err != nil {
		t.Fatal(err)
	}
	agentConfirm := performEvergreenAPI[evergreencore.PlannedReview](
		t, engine, http.MethodPost, "/api/evergreen/v1/review/plan",
		evergreencore.ReviewPlanRequest{
			OperationID: "op-review-agent-confirm", NoteID: "n-review-api",
			Base: snapshot.Data.SemanticHash,
			Command: evergreencore.ReviewCommand{
				Action:      evergreencore.ReviewActionCandidateConfirm,
				CandidateID: "cand-review-api",
			},
		}, map[string]string{
			model.XAuthTokenKey: agentJWT, evergreenCallerHeader: "agent",
			evergreenAgentIDHeader:       "review-agent",
			evergreenRequestReasonHeader: "confirm Candidate without a human",
		})
	if agentConfirm.OK || len(agentConfirm.Diagnostics) != 1 ||
		agentConfirm.Diagnostics[0].Code != evergreencore.CodeUnauthorized {
		t.Fatalf("Agent Candidate confirmation = %+v", agentConfirm)
	}

	confirm := performEvergreenAPI[evergreencore.PlannedReview](
		t, engine, http.MethodPost, "/api/evergreen/v1/review/plan",
		evergreencore.ReviewPlanRequest{
			OperationID: "op-review-confirm", NoteID: "n-review-api",
			Base: snapshot.Data.SemanticHash,
			Command: evergreencore.ReviewCommand{
				Action:      evergreencore.ReviewActionCandidateConfirm,
				CandidateID: "cand-review-api",
			},
		}, map[string]string{evergreenCallerHeader: "ui"})
	if !confirm.OK ||
		confirm.Data.Snapshot.Candidates[0].Metadata.State != "confirmed" {
		t.Fatalf("confirm plan = %+v", confirm)
	}
	confirmed := performEvergreenAPI[evergreencore.OperationRecord](
		t, engine, http.MethodPost, "/api/evergreen/v1/apply",
		evergreencore.ApplyRequest{Operation: confirm.Data.Operation},
		map[string]string{evergreenCallerHeader: "ui"})
	if !confirmed.OK || confirmed.Data.State != evergreencore.OperationCompleted {
		t.Fatalf("confirm apply = %+v", confirmed)
	}

	current := performEvergreenAPI[evergreencore.ReviewSnapshot](
		t, engine, http.MethodPost, "/api/evergreen/v1/review/get",
		map[string]any{"note_id": "n-review-api"},
		map[string]string{evergreenCallerHeader: "ui"})
	materialize := performEvergreenAPI[evergreencore.PlannedMaterialization](
		t, engine, http.MethodPost, "/api/evergreen/v1/review/plan-materialize",
		evergreencore.ReviewMaterializeRequest{
			OperationID: "op-review-materialize", NoteID: "n-review-api",
			CandidateID: "cand-review-api", NoteBase: current.Data.SemanticHash,
			ClaimID: "k-review-api", ClaimPath: "box/claim.sy",
			ClaimDocumentID: "20261010140000-claim01",
			CreatedAt:       "2026-10-10T14:00:00Z",
		}, map[string]string{evergreenCallerHeader: "ui"})
	if !materialize.OK || len(materialize.Data.Operation.Plan.Writes) != 2 {
		t.Fatalf("materialize plan = %+v", materialize)
	}
	applied := performEvergreenAPI[evergreencore.OperationRecord](
		t, engine, http.MethodPost, "/api/evergreen/v1/apply",
		evergreencore.ApplyRequest{Operation: materialize.Data.Operation},
		map[string]string{evergreenCallerHeader: "ui"})
	if !applied.OK || applied.Data.State != evergreencore.OperationCompleted {
		t.Fatalf("materialize apply = %+v", applied)
	}
	claim, err := evergreencore.DecodeSY(
		readEvergreenAPIFile(t, root, "box/claim.sy"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if claim.Envelope.Entity.LogicalID != "k-review-api" ||
		len(claim.Envelope.Provenance) != 1 ||
		committer.count != 2 {
		t.Fatalf("claim=%+v commits=%d", claim.Envelope, committer.count)
	}
}

func evergreenTestEngine(t *testing.T) *gin.Engine {
	t.Helper()
	oldConf := model.Conf
	model.Conf = model.NewAppConf()
	model.Conf.AccessAuthCode = ""
	t.Cleanup(func() { model.Conf = oldConf })
	engine := gin.New()
	engine.Use(boxLeaseMiddleware)
	engine.Use(func(c *gin.Context) {
		if token := model.ParseXAuthToken(c.Request); token != nil && token.Valid {
			claims := model.GetTokenClaims(token)
			c.Set(model.ClaimsContextKey, claims)
			c.Set(model.RoleContextKey, model.GetClaimRole(claims))
			model.RecordJWTAuthentication(c, token)
		}
		c.Next()
	}, model.CheckAuth)
	engine.GET("/api/evergreen/v1/capabilities", evergreenCapabilities)
	engine.GET("/api/evergreen/v1/schemas", evergreenSchemas)
	engine.POST("/api/evergreen/v1/sources/plan-capture", evergreenSourcePlanCapture)
	engine.POST("/api/evergreen/v1/notes/plan-create", evergreenNotePlanCreate)
	engine.POST("/api/evergreen/v1/claims/get", evergreenClaimGet)
	engine.POST("/api/evergreen/v1/claims/plan-edit", evergreenClaimPlanEdit)
	engine.POST("/api/evergreen/v1/claims/plan-create", evergreenClaimPlanCreate)
	engine.POST("/api/evergreen/v1/claims/bulk-capabilities", evergreenClaimBulkCapabilities)
	engine.POST("/api/evergreen/v1/claims/plan-bulk", evergreenClaimPlanBulk)
	engine.POST("/api/evergreen/v1/plan", evergreenPlan)
	engine.POST("/api/evergreen/v1/apply", evergreenApply)
	engine.GET("/api/evergreen/v1/operations/:id", evergreenOperation)
	engine.POST("/api/evergreen/v1/recover", evergreenRecover)
	engine.POST("/api/evergreen/v1/doctor", evergreenDoctor)
	engine.POST("/api/evergreen/v1/review/get", evergreenReviewGet)
	engine.POST("/api/evergreen/v1/review/plan", evergreenReviewPlan)
	engine.POST("/api/evergreen/v1/review/plan-edit", evergreenReviewPlanEdit)
	engine.POST("/api/evergreen/v1/review/plan-materialize", evergreenReviewPlanMaterialize)
	engine.GET("/api/evergreen/v1/av/views/:id", evergreenAVDescribe)
	engine.POST("/api/evergreen/v1/av/create-view", evergreenAVCreateView)
	engine.POST("/api/evergreen/v1/av/query", evergreenAVQuery)
	engine.POST("/api/evergreen/v1/av/cell", evergreenAVCell)
	engine.POST("/api/evergreen/v1/av/plan-patch", evergreenAVPlanPatch)
	engine.POST("/api/evergreen/v1/av/rebuild", evergreenAVRebuild)
	return engine
}

func performEvergreenAPI[T any](
	t *testing.T,
	engine http.Handler,
	method, path string,
	body any,
	headers map[string]string,
) evergreencore.APIResponse[T] {
	t.Helper()
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	request.RemoteAddr = "127.0.0.1:32100"
	request.Host = "127.0.0.1:6806"
	request.URL.Host = "127.0.0.1:6806"
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("%s %s status=%d body=%s", method, path, recorder.Code, recorder.Body.String())
	}
	contractPath := path
	if strings.HasPrefix(path, "/api/evergreen/v1/operations/") {
		contractPath = "/api/evergreen/v1/operations/:id"
	}
	if strings.HasPrefix(path, "/api/evergreen/v1/av/views/") {
		contractPath = "/api/evergreen/v1/av/views/:id"
	}
	requireAPIContract(t, method, contractPath, recorder)
	var response evergreencore.APIResponse[T]
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v\n%s", err, recorder.Body.String())
	}
	return response
}

type apiRecordingCommitter struct {
	count    int
	commits  map[string]string
	requests []evergreencore.CommitRequest
	delegate *evergreencore.LocalGitCommitter
}

func (c *apiRecordingCommitter) Head(ctx context.Context) (string, error) {
	if c.delegate != nil {
		return c.delegate.Head(ctx)
	}
	return "parent", nil
}

func (c *apiRecordingCommitter) Commit(ctx context.Context, request evergreencore.CommitRequest) (string, error) {
	if c.commits == nil {
		c.commits = map[string]string{}
	}
	if sha := c.commits[request.OperationID]; sha != "" {
		return sha, nil
	}
	sha := "commit-" + request.OperationID
	if c.delegate != nil {
		var err error
		if sha, err = c.delegate.Commit(ctx, request); err != nil {
			return "", err
		}
	}
	c.count++
	c.commits[request.OperationID] = sha
	c.requests = append(c.requests, request)
	return sha, nil
}

func writeEvergreenAPIFixture(t *testing.T, root, rel, id, content string) {
	t.Helper()
	data := []byte(fmt.Sprintf(`{
		"ID":"20261010120000-root",
		"Type":"NodeDocument",
		"Spec":"5",
		"Evergreen":{
			"spec":"evergreen.sy/v1",
			"entity":{"logical_id":%q,"entity_type":"claim","schema":"evergreen.claim/v1","semantic_revision":1},
			"claim":{"claim_kind":"knowledge","kind_schema":"evergreen.claim-kind.knowledge/v1","status":"active","tags":[],"kind_data":{}},
			"relations":{"outgoing":[]}
		},
		"Children":[{"ID":"20261010120001-child","Type":"NodeParagraph","Data":%q}]
	}`, id, content))
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeEvergreenReviewAPIFixture(t *testing.T, root, rel string) {
	t.Helper()
	segment := map[string]any{
		"ID":   "20261010140001-seg0001",
		"Type": "NodeParagraph", "Data": "source body",
	}
	segmentRaw, _ := json.Marshal(segment)
	segmentHash, err := evergreencore.NormalizedSegmentHash(segmentRaw)
	if err != nil {
		t.Fatal(err)
	}
	payload := []map[string]any{{
		"ID":   "20261010140003-payload",
		"Type": "NodeParagraph", "Data": "reviewed claim",
	}}
	payloadRaw, _ := json.Marshal(payload)
	payloadHash, err := evergreencore.CandidateSubtreeHash(payloadRaw)
	if err != nil {
		t.Fatal(err)
	}
	segmentEnvelope, err := evergreencore.MarshalBlockEnvelope(
		&evergreencore.BlockEnvelope{
			Spec: evergreencore.BlockSpec, Role: "note_segment",
			Segment: &evergreencore.SegmentMetadata{
				SegmentID: "seg-review-api",
				SourceRef: evergreencore.SourceLocator{
					SourceID: "s-review-api",
					Locator: evergreencore.Locator{
						Kind: "source-lines", Value: "L1-L2",
					},
				},
				NormalizedHash: segmentHash,
			},
		})
	if err != nil {
		t.Fatal(err)
	}
	segment["Evergreen"] = json.RawMessage(segmentEnvelope)
	candidateEnvelope, err := evergreencore.MarshalBlockEnvelope(
		&evergreencore.BlockEnvelope{
			Spec: evergreencore.BlockSpec, Role: "candidate",
			Candidate: &evergreencore.CandidateMetadata{
				CandidateID: "cand-review-api",
				ClaimKind:   evergreencore.KnowledgeKind,
				KindSchema:  evergreencore.KnowledgeKindSchema,
				Title:       "Review API claim", LogicalSlug: "review-api-claim",
				SegmentRefs: []evergreencore.LogicalID{"seg-review-api"},
				PayloadHash: payloadHash,
				RefHashes: map[evergreencore.LogicalID]string{
					"seg-review-api": segmentHash,
				},
				Relation: "support", Reason: "The source segment supports this claim.",
				State: "draft",
			},
		})
	if err != nil {
		t.Fatal(err)
	}
	documentEnvelope, err := evergreencore.MarshalDocumentEnvelope(
		&evergreencore.DocumentEnvelope{
			Spec: evergreencore.DocumentSpec,
			Entity: evergreencore.Entity{
				LogicalID: "n-review-api", EntityType: evergreencore.EntityNote,
				Schema: "evergreen.note/v1", SemanticRevision: 1,
			},
			Relations: evergreencore.Relations{Outgoing: []evergreencore.TypedEdge{}},
			Review: &evergreencore.NoteReview{
				Spec: evergreencore.NoteReviewSpec,
				Coverage: []evergreencore.CoverageModule{{
					ModuleID: "coverage-review-api", Disposition: "candidate",
					SegmentRefs: []evergreencore.LogicalID{"seg-review-api"},
					CandidateID: "cand-review-api",
				}},
			},
		})
	if err != nil {
		t.Fatal(err)
	}
	rootObject := map[string]any{
		"ID": "20261010140000-note001", "Type": "NodeDocument", "Spec": "5",
		"Evergreen": json.RawMessage(documentEnvelope),
		"Children": []any{
			segment,
			map[string]any{
				"ID":        "20261010140002-cand001",
				"Type":      "NodeSuperBlock",
				"Evergreen": json.RawMessage(candidateEnvelope),
				"Children":  payload,
			},
		},
	}
	data, err := json.Marshal(rootObject)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err = os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func mutateEvergreenAPIFixture(t *testing.T, data []byte, content string) []byte {
	t.Helper()
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	var children []map[string]json.RawMessage
	if err := json.Unmarshal(root["Children"], &children); err != nil {
		t.Fatal(err)
	}
	children[0]["Data"], _ = json.Marshal(content)
	root["Children"], _ = json.Marshal(children)
	result, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func readEvergreenAPIFile(t *testing.T, root, rel string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return data
}
