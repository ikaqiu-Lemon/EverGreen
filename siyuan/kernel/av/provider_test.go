// SiYuan - From thought to insight, with agents
// Copyright (c) 2020-present, b3log.org
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package av

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/88250/lute/ast"
	evergreencore "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
)

func TestSourceBackedAttributeViewPersistenceStripsProjectedRows(t *testing.T) {
	schema := evergreencore.AVViewSchema{Definition: evergreencore.AVViewDefinition{
		ID: "claims", Name: "Claims",
		Provider: evergreencore.AVProviderDefinition{
			Type: evergreencore.AVProviderType, Protocol: evergreencore.AVProviderProtocol,
			Query: evergreencore.AVEntityQuery{EntityType: evergreencore.EntityClaim},
		},
		Columns: []evergreencore.AVColumnDefinition{
			{
				ID: "title", Name: "Claim", Value: "text",
				Binding: evergreencore.AVColumnBinding{Field: "claim.body.title"},
			},
			{
				ID: "arguments", Name: "Arguments", Value: "relation",
				Binding: evergreencore.AVColumnBinding{
					Direction:  evergreencore.EdgeDirectionOutgoing,
					EdgeSchema: evergreencore.ArgumentSchema,
				},
			},
		},
	}}
	attrView := NewSourceBackedAttributeView("20261010120000-avclaim", schema)
	attrView.KeyValues[0].Values = []*Value{{
		ID: ast.NewNodeID(), KeyID: "title", BlockID: "k-alpha",
		Type: KeyTypeText, Text: &ValueText{Content: "Projected title"},
	}}
	attrView.KeyValues[1].Values = []*Value{{
		ID: ast.NewNodeID(), KeyID: "arguments", BlockID: "k-alpha",
		Type: KeyTypeRelation, Relation: &ValueRelation{
			BlockIDs: []string{"o-beta"},
			Edges: []TypedRelationEdge{{
				ID: "edge-1", OwnerID: "k-alpha", Direction: evergreencore.EdgeDirectionOutgoing,
				Schema: evergreencore.ArgumentSchema,
				Target: evergreencore.EntityRef{EntityType: evergreencore.EntityClaim, LogicalID: "o-beta"},
				Type:   "supports", Reason: "Projected runtime value.",
			}},
			EdgeCount: 1, UniqueTargetCount: 1,
		},
	}}

	data, err := MarshalAttributeViewPersistence(attrView)
	if err != nil {
		t.Fatal(err)
	}
	if len(attrView.KeyValues[0].Values) != 1 || len(attrView.KeyValues[1].Values) != 1 {
		t.Fatal("persistence serialization mutated the in-memory projection")
	}
	var persisted struct {
		Provider  *ProviderDefinition `json:"provider"`
		KeyValues []*KeyValues        `json:"keyValues"`
	}
	if err = json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.Provider == nil || persisted.Provider.Type != evergreencore.AVProviderType {
		t.Fatalf("provider definition missing: %s", data)
	}
	for _, keyValues := range persisted.KeyValues {
		if len(keyValues.Values) != 0 {
			t.Fatalf("source-backed rows leaked into AV JSON: %s", data)
		}
		if keyValues.Key.Binding == nil {
			t.Fatalf("column binding missing: %s", data)
		}
	}
}

func TestNativeAVProviderPreservesBasicRelationSemantics(t *testing.T) {
	rowID := ast.NewNodeID()
	targetID := ast.NewNodeID()
	blockKey := NewKey(ast.NewNodeID(), "Name", "", KeyTypeBlock)
	relationKey := NewKey(ast.NewNodeID(), "Related", "", KeyTypeRelation)
	attrView := &AttributeView{
		ID: ast.NewNodeID(), Name: "Native",
		KeyValues: []*KeyValues{
			{Key: blockKey, Values: []*Value{{
				ID: ast.NewNodeID(), KeyID: blockKey.ID, BlockID: rowID,
				Type: KeyTypeBlock, Block: &ValueBlock{Content: "Native row"},
			}}},
			{Key: relationKey, Values: []*Value{{
				ID: ast.NewNodeID(), KeyID: relationKey.ID, BlockID: rowID,
				Type: KeyTypeRelation, Relation: &ValueRelation{
					BlockIDs: []string{targetID},
				},
			}}},
		},
	}
	provider := NewNativeAVProvider(attrView)
	page, err := provider.Query(context.Background(), "", evergreencore.AVQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Rows) != 1 {
		t.Fatalf("native row count = %d", len(page.Rows))
	}
	cell := page.Rows[0].Cells[relationKey.ID]
	if cell.EdgeCount != 1 || cell.UniqueTargetCount != 1 ||
		cell.Edges[0].Schema != evergreencore.BasicSchema ||
		cell.Edges[0].Target.BlockID != targetID {
		t.Fatalf("native basic relation changed semantics: %#v", cell)
	}
	if attrView.Provider != nil || attrView.KeyValues[1].Values[0].Relation.BlockIDs[0] != targetID {
		t.Fatal("native provider rewrote the AV or migrated basic relation data")
	}
}

func TestValidateProviderRejectsTooNewProtocol(t *testing.T) {
	attrView := NewSourceBackedAttributeView(ast.NewNodeID(), evergreencore.AVViewSchema{
		Definition: evergreencore.AVViewDefinition{
			Name: "Claims",
			Provider: evergreencore.AVProviderDefinition{
				Type: evergreencore.AVProviderType, Protocol: "evergreen.sy-provider/v2",
				Query: evergreencore.AVEntityQuery{EntityType: evergreencore.EntityClaim},
			},
			Columns: []evergreencore.AVColumnDefinition{{
				ID: "title", Name: "Claim", Value: "text",
				Binding: evergreencore.AVColumnBinding{Field: "claim.body.title"},
			}},
		},
	})
	if err := ValidateProvider(attrView); !evergreencore.HasDiagnostic(err, evergreencore.CodeSchemaTooNew) {
		t.Fatalf("too-new provider protocol should return EG_SCHEMA_TOO_NEW, got %v", err)
	}
}
