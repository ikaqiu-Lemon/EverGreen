// SiYuan - From thought to insight, with agents
// Copyright (c) 2020-present, b3log.org
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package evergreen

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	evergreencore "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	"github.com/siyuan-note/siyuan/kernel/av"
)

func TestRuntimeClaimsAVQueryPatchAndApplyUsesAuthority(t *testing.T) {
	root := t.TempDir()
	writeRuntimeClaim(t, root, "box/k.sy", runtimeClaimFixture(t, "k-alpha", evergreencore.KnowledgeKind, []evergreencore.TypedEdge{{
		ID: "edge-1", Schema: evergreencore.ArgumentSchema,
		Target: evergreencore.EntityRef{EntityType: evergreencore.EntityClaim, LogicalID: "o-beta"},
		Type:   "supports", Reason: "Initial support.",
	}}))
	writeRuntimeClaim(t, root, "box/o.sy", runtimeClaimFixture(t, "o-beta", evergreencore.OpinionKind, nil))

	committer := &runtimeAVCommitter{}
	runtime, err := NewRuntime(context.Background(), RuntimeOptions{
		Root: root, Holder: "kernel-av-test", Committer: committer,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	page, err := runtime.QueryAV(context.Background(), evergreencore.ClaimsViewID, evergreencore.AVQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Rows) != 2 {
		t.Fatalf("row count = %d", len(page.Rows))
	}
	alpha := runtimeAVRow(t, page.Rows, "k-alpha")
	planned, err := runtime.PlanAVPatch(context.Background(), evergreencore.Principal{
		Type: evergreencore.PrincipalUser, ID: "user-1",
	}, evergreencore.AVPatch{
		OperationID: "op-av-edge-update",
		RowID:       "k-alpha",
		Base:        evergreencore.BaseRef{LogicalID: "k-alpha", SemanticHash: alpha.Ref.SemanticHash},
		Column: evergreencore.AVColumnBinding{
			Direction:  evergreencore.EdgeDirectionOutgoing,
			EdgeSchema: evergreencore.ArgumentSchema,
		},
		Edge: evergreencore.AVEdgePatch{
			Action: evergreencore.AVEdgeUpdate,
			Edge: evergreencore.TypedEdge{
				ID: "edge-1", Schema: evergreencore.ArgumentSchema,
				Target: evergreencore.EntityRef{EntityType: evergreencore.EntityClaim, LogicalID: "o-beta"},
				Type:   "limits", Reason: "Updated through the source-backed AV provider.",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	record, err := runtime.Apply(context.Background(), evergreencore.Principal{
		Type: evergreencore.PrincipalUser, ID: "user-1",
	}, evergreencore.ApplyRequest{Operation: planned})
	if err != nil {
		t.Fatal(err)
	}
	if record.State != evergreencore.OperationCompleted || committer.count != 1 {
		t.Fatalf("operation = %#v commits=%d", record, committer.count)
	}
	if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(evergreencore.RuntimeDirName), "derived-invalidation.json")); statErr != nil {
		t.Fatalf("provider apply did not publish derived invalidation: %v", statErr)
	}
	updated, err := runtime.QueryAV(context.Background(), evergreencore.ClaimsViewID, evergreencore.AVQuery{})
	if err != nil {
		t.Fatal(err)
	}
	edge := runtimeAVRow(t, updated.Rows, "k-alpha").Cells["arguments_outgoing"].Edges[0]
	if edge.Type != "limits" || edge.Reason != "Updated through the source-backed AV provider." {
		t.Fatalf("updated edge = %#v", edge)
	}
	if _, statErr := os.Stat(filepath.Join(root, "storage", "av")); !os.IsNotExist(statErr) {
		t.Fatalf("source-backed patch created AV authority files: %v", statErr)
	}
}

func TestRuntimeClaimsAVPatchUsesKernelAuthorization(t *testing.T) {
	root := t.TempDir()
	writeRuntimeClaim(t, root, "box/k.sy", runtimeClaimFixture(t, "k-alpha", evergreencore.KnowledgeKind, nil))
	runtime, err := NewRuntime(context.Background(), RuntimeOptions{
		Root: root, Holder: "kernel-av-deny-test", Committer: &runtimeAVCommitter{},
		Authorizer: runtimeDenyAuthorizer{},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	page, err := runtime.QueryAV(context.Background(), evergreencore.ClaimsViewID, evergreencore.AVQuery{})
	if err != nil {
		t.Fatal(err)
	}
	alpha := runtimeAVRow(t, page.Rows, "k-alpha")
	_, err = runtime.PlanAVPatch(context.Background(), evergreencore.Principal{
		Type: evergreencore.PrincipalUser, ID: "user-1",
	}, evergreencore.AVPatch{
		OperationID: "op-av-denied",
		RowID:       "k-alpha",
		Base:        evergreencore.BaseRef{LogicalID: "k-alpha", SemanticHash: alpha.Ref.SemanticHash},
		Column:      evergreencore.AVColumnBinding{Field: "claim.status"},
		Value:       &evergreencore.AVCellValue{Kind: "text", Text: "archived"},
	})
	if !evergreencore.HasDiagnostic(err, evergreencore.CodeUnauthorized) {
		t.Fatalf("authorization error = %v", err)
	}
}

func TestRuntimeHydratesSourceBackedAttributeViewWithoutChangingDefinition(t *testing.T) {
	root := t.TempDir()
	edges := []evergreencore.TypedEdge{
		{
			ID: "edge-1", Schema: evergreencore.ArgumentSchema,
			Target: evergreencore.EntityRef{EntityType: evergreencore.EntityClaim, LogicalID: "o-beta"},
			Type:   "supports", Reason: "First support.",
		},
		{
			ID: "edge-2", Schema: evergreencore.ArgumentSchema,
			Target: evergreencore.EntityRef{EntityType: evergreencore.EntityClaim, LogicalID: "o-beta"},
			Type:   "limits", Reason: "Independent limit.",
		},
	}
	writeRuntimeClaim(t, root, "box/k.sy", runtimeClaimFixture(t, "k-alpha", evergreencore.KnowledgeKind, edges))
	writeRuntimeClaim(t, root, "box/o.sy", runtimeClaimFixture(t, "o-beta", evergreencore.OpinionKind, nil))
	runtime, err := NewRuntime(context.Background(), RuntimeOptions{
		Root: root, Holder: "kernel-av-hydrate-test", Committer: &runtimeAVCommitter{},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	schema, err := runtime.DescribeAV(context.Background(), evergreencore.ClaimsViewID)
	if err != nil {
		t.Fatal(err)
	}
	attrView := av.NewSourceBackedAttributeView("20261010120000-avclaim", schema)
	if err = runtime.HydrateAttributeView(context.Background(), attrView); err != nil {
		t.Fatal(err)
	}
	if attrView.Provider == nil || attrView.Provider.Type != evergreencore.AVProviderType {
		t.Fatal("hydration changed the provider definition")
	}
	outgoing := runtimeHydratedValue(t, attrView, "arguments_outgoing", "k-alpha").Relation
	if outgoing.EdgeCount != 2 || outgoing.UniqueTargetCount != 1 || len(outgoing.Edges) != 2 {
		t.Fatalf("hydrated outgoing relation = %#v", outgoing)
	}
	incoming := runtimeHydratedValue(t, attrView, "arguments_incoming", "o-beta").Relation
	if incoming.EdgeCount != 2 || incoming.UniqueTargetCount != 1 ||
		incoming.Edges[0].OwnerID != "k-alpha" {
		t.Fatalf("hydrated incoming relation = %#v", incoming)
	}
	title := runtimeHydratedValue(t, attrView, "title", "k-alpha")
	if title.ProviderBase == "" || title.Block == nil || title.Block.Content != "Runtime claim" {
		t.Fatalf("hydrated primary value = %#v", title)
	}
}

type runtimeAVCommitter struct {
	count int
}

func (c *runtimeAVCommitter) Head(context.Context) (string, error) { return "parent", nil }
func (c *runtimeAVCommitter) Commit(_ context.Context, request evergreencore.CommitRequest) (string, error) {
	c.count++
	return "commit-" + request.OperationID, nil
}

type runtimeDenyAuthorizer struct{}

func (runtimeDenyAuthorizer) Authorize(
	context.Context,
	evergreencore.Principal,
	string,
	[]evergreencore.LogicalID,
) error {
	return errors.New("denied by test policy")
}

func runtimeClaimFixture(
	t *testing.T,
	id evergreencore.LogicalID,
	kind evergreencore.ClaimKind,
	edges []evergreencore.TypedEdge,
) []byte {
	t.Helper()
	kindSchema := evergreencore.KnowledgeKindSchema
	kindData := json.RawMessage(`{}`)
	if kind == evergreencore.OpinionKind {
		kindSchema = evergreencore.OpinionKindSchema
		kindData = json.RawMessage(`{"validation":{"status":"validated"}}`)
	}
	base := []byte(`{
		"ID":"20261010120000-runtime",
		"Type":"NodeDocument",
		"Spec":"5",
		"Children":[{"ID":"20261010120001-child","Type":"NodeHeading","Data":"Runtime claim"}]
	}`)
	raw, err := evergreencore.EncodeSY(base, &evergreencore.DocumentEnvelope{
		Spec: evergreencore.DocumentSpec,
		Entity: evergreencore.Entity{
			LogicalID: id, EntityType: evergreencore.EntityClaim,
			Schema: evergreencore.ClaimSchema, SemanticRevision: 1,
		},
		Claim: &evergreencore.Claim{
			ClaimKind: kind, KindSchema: kindSchema, Status: "active",
			Tags: []string{}, KindData: kindData,
		},
		Relations: evergreencore.Relations{Outgoing: edges},
	}, evergreencore.DefaultRegistry())
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func writeRuntimeClaim(t *testing.T, root, relative string, raw []byte) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func runtimeAVRow(t *testing.T, rows []evergreencore.AVRow, id evergreencore.LogicalID) evergreencore.AVRow {
	t.Helper()
	for _, row := range rows {
		if row.Ref.LogicalID == id {
			return row
		}
	}
	t.Fatalf("row %q not found", id)
	return evergreencore.AVRow{}
}

func runtimeHydratedValue(t *testing.T, attrView *av.AttributeView, keyID, rowID string) *av.Value {
	t.Helper()
	keyValues, err := attrView.GetKeyValues(keyID)
	if err != nil {
		t.Fatal(err)
	}
	value := keyValues.GetValue(rowID)
	if value == nil {
		t.Fatalf("value %s/%s not found", keyID, rowID)
	}
	return value
}
