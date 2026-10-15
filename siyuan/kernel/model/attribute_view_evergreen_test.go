// SiYuan - From thought to insight, with agents
// Copyright (c) 2020-present, b3log.org
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package model

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/88250/lute/ast"
	evergreencore "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	"github.com/siyuan-note/siyuan/kernel/av"
	kernelEvergreen "github.com/siyuan-note/siyuan/kernel/evergreen"
	"github.com/siyuan-note/siyuan/kernel/util"
)

func TestRenderSourceBackedAttributeViewUsesTransientProjection(t *testing.T) {
	previousDataDir := util.DataDir
	util.DataDir = t.TempDir()
	t.Cleanup(func() { util.DataDir = previousDataDir })

	writeEvergreenRenderClaim(t, util.DataDir, "box/k.sy", "k-render", []evergreencore.TypedEdge{{
		ID: "edge-render", Schema: evergreencore.ArgumentSchema,
		Target: evergreencore.EntityRef{EntityType: evergreencore.EntityClaim, LogicalID: "o-render"},
		Type:   "supports", Reason: "Visible relation.",
	}})
	writeEvergreenRenderClaim(t, util.DataDir, "box/o.sy", "o-render", nil)
	runtime, err := kernelEvergreen.NewRuntime(context.Background(), kernelEvergreen.RuntimeOptions{
		Root: util.DataDir, Holder: "model-render-test", Committer: evergreenRenderCommitter{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	previousHydrator := hydrateEvergreenAttributeView
	hydrateEvergreenAttributeView = func(attrView *av.AttributeView) error {
		return runtime.HydrateAttributeView(context.Background(), attrView)
	}
	t.Cleanup(func() { hydrateEvergreenAttributeView = previousHydrator })

	schema, err := runtime.DescribeAV(context.Background(), evergreencore.ClaimsViewID)
	if err != nil {
		t.Fatal(err)
	}
	attrView := av.NewSourceBackedAttributeView(ast.NewNodeID(), schema)
	if err = av.SaveAttributeView(attrView); err != nil {
		t.Fatal(err)
	}
	viewable, _, err := RenderAttributeView("", attrView.ID, attrView.Views[0].ID, "", 1, -1, nil, false, false)
	if err != nil {
		t.Fatal(err)
	}
	table, ok := viewable.(*av.Table)
	if !ok || len(table.Rows) != 2 || table.Rows[0].ID != "k-render" {
		t.Fatalf("source-backed render = %#v", viewable)
	}
	outgoingColumn := table.GetColumn("arguments_outgoing")
	if outgoingColumn == nil || outgoingColumn.Binding == nil ||
		outgoingColumn.Binding.Direction != evergreencore.EdgeDirectionOutgoing {
		t.Fatalf("source-backed column binding was dropped: %#v", outgoingColumn)
	}
	var outgoing *av.Value
	for _, cell := range table.Rows[0].Cells {
		if cell.Value != nil && cell.Value.KeyID == "arguments_outgoing" {
			outgoing = cell.Value
			break
		}
	}
	if outgoing == nil || outgoing.Relation == nil || len(outgoing.Relation.Contents) != 1 ||
		len(outgoing.Relation.Edges) != 1 || outgoing.Relation.Contents[0].Block.Content != "Rendered claim" {
		t.Fatalf("source-backed relation display was dropped: %#v", outgoing)
	}
	persisted, err := os.ReadFile(av.GetAttributeViewDataPath(attrView.ID))
	if err != nil {
		t.Fatal(err)
	}
	var definition struct {
		Provider  *av.ProviderDefinition `json:"provider"`
		KeyValues []*av.KeyValues        `json:"keyValues"`
	}
	if err = json.Unmarshal(persisted, &definition); err != nil {
		t.Fatal(err)
	}
	if definition.Provider == nil || definition.Provider.Type != evergreencore.AVProviderType {
		t.Fatalf("provider definition missing: %s", persisted)
	}
	for _, keyValues := range definition.KeyValues {
		if len(keyValues.Values) != 0 {
			t.Fatalf("rendered rows leaked to AV JSON: %s", persisted)
		}
	}
}

type evergreenRenderCommitter struct{}

func (evergreenRenderCommitter) Head(context.Context) (string, error) { return "parent", nil }
func (evergreenRenderCommitter) Commit(
	_ context.Context,
	request evergreencore.CommitRequest,
) (string, error) {
	return "commit-" + request.OperationID, nil
}

func writeEvergreenRenderClaim(
	t *testing.T,
	root, relative string,
	id evergreencore.LogicalID,
	edges []evergreencore.TypedEdge,
) {
	t.Helper()
	base := []byte(`{
		"ID":"20261010120000-render",
		"Type":"NodeDocument",
		"Spec":"5",
		"Children":[{"ID":"20261010120001-child","Type":"NodeHeading","Data":"Rendered claim"}]
	}`)
	raw, err := evergreencore.EncodeSY(base, &evergreencore.DocumentEnvelope{
		Spec: evergreencore.DocumentSpec,
		Entity: evergreencore.Entity{
			LogicalID: id, EntityType: evergreencore.EntityClaim,
			Schema: evergreencore.ClaimSchema, SemanticRevision: 1,
		},
		Claim: &evergreencore.Claim{
			ClaimKind: evergreencore.KnowledgeKind, KindSchema: evergreencore.KnowledgeKindSchema,
			Status: "active", Tags: []string{}, KindData: json.RawMessage(`{}`),
		},
		Relations: evergreencore.Relations{Outgoing: edges},
	}, evergreencore.DefaultRegistry())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err = os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}
