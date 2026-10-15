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
	"strings"

	evergreencore "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	"github.com/siyuan-note/siyuan/kernel/av"
)

func (r *Runtime) HydrateAttributeView(ctx context.Context, attrView *av.AttributeView) error {
	if attrView == nil || !attrView.IsSourceBacked() {
		return nil
	}
	viewID := r.registry.ViewID(attrView.Provider.Query.ClaimKind)
	page, err := r.QueryAV(ctx, viewID, evergreencore.AVQuery{})
	if err != nil {
		return err
	}
	if kind := attrView.Provider.Query.ClaimKind; kind != "" {
		rows := page.Rows[:0]
		for _, row := range page.Rows {
			if row.Cells["claim_kind"].Text == string(kind) {
				rows = append(rows, row)
			}
		}
		page.Rows = rows
	}
	titles := map[evergreencore.LogicalID]string{}
	for _, row := range page.Rows {
		titles[row.Ref.LogicalID] = row.Cells["title"].Text
	}
	keyValues := make([]*av.KeyValues, 0, len(attrView.KeyValues))
	for _, existing := range attrView.KeyValues {
		if existing == nil || existing.Key == nil {
			continue
		}
		values := make([]*av.Value, 0, len(page.Rows))
		for _, row := range page.Rows {
			cell, cellErr := r.GetAVCell(ctx, row.Ref, *existing.Key.Binding)
			if cellErr != nil {
				return cellErr
			}
			values = append(values, projectedAVValue(existing.Key, row, cell, titles))
		}
		keyValues = append(keyValues, &av.KeyValues{Key: existing.Key, Values: values})
	}
	attrView.KeyValues = keyValues
	itemIDs := make([]string, 0, len(page.Rows))
	for _, row := range page.Rows {
		itemIDs = append(itemIDs, string(row.Ref.LogicalID))
	}
	for _, view := range attrView.Views {
		if view != nil {
			view.ItemIDs = append([]string(nil), itemIDs...)
		}
	}
	return nil
}

func projectedAVValue(
	key *av.Key,
	row evergreencore.AVRow,
	cell evergreencore.AVCellValue,
	titles map[evergreencore.LogicalID]string,
) *av.Value {
	value := &av.Value{
		ID: av.NewProviderValueID(), KeyID: key.ID, BlockID: string(row.Ref.LogicalID), Type: key.Type,
		ProviderBase: row.Ref.SemanticHash, ProviderReadOnly: row.ReadOnly,
	}
	switch key.Type {
	case av.KeyTypeBlock:
		value.IsDetached = true
		value.Block = &av.ValueBlock{Content: cell.Text}
	case av.KeyTypeMSelect, av.KeyTypeSelect:
		for _, item := range cell.Texts {
			value.MSelect = append(value.MSelect, &av.ValueSelect{Content: item})
		}
	case av.KeyTypeRelation:
		value.Relation = &av.ValueRelation{
			Edges:     append([]av.TypedRelationEdge(nil), cell.Edges...),
			EdgeCount: cell.EdgeCount, UniqueTargetCount: cell.UniqueTargetCount,
		}
		for _, edge := range cell.Edges {
			relatedID := edge.Target.LogicalID
			if edge.Direction == evergreencore.EdgeDirectionIncoming {
				relatedID = edge.OwnerID
			}
			value.Relation.BlockIDs = append(value.Relation.BlockIDs, string(relatedID))
			title := titles[relatedID]
			if title == "" {
				title = string(relatedID)
			}
			value.Relation.Contents = append(value.Relation.Contents, &av.Value{
				ID: av.NewProviderValueID(), BlockID: string(relatedID), Type: av.KeyTypeBlock,
				IsDetached: true, Block: &av.ValueBlock{Content: title},
			})
		}
	default:
		text := cell.Text
		if cell.Kind == "provenance" {
			sources := make([]string, 0, len(cell.Provenance))
			for _, provenance := range cell.Provenance {
				sources = append(sources, string(provenance.SourceID))
			}
			text = strings.Join(sources, ", ")
		}
		value.Text = &av.ValueText{Content: text}
	}
	return value
}
