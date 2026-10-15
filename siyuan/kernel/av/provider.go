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
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/88250/lute/ast"
	evergreencore "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
)

const NativeProviderType = "siyuan.native"

type AVDataProvider = evergreencore.AVDataProvider
type ProviderDefinition = evergreencore.AVProviderDefinition
type ProviderQuery = evergreencore.AVEntityQuery
type ColumnBinding = evergreencore.AVColumnBinding
type TypedRelationEdge = evergreencore.AVEdgeValue

type EvergreenSYProvider struct {
	provider evergreencore.AVDataProvider
}

func NewEvergreenSYProvider(provider evergreencore.AVDataProvider) *EvergreenSYProvider {
	return &EvergreenSYProvider{provider: provider}
}

func (p *EvergreenSYProvider) Describe(
	ctx context.Context,
	viewID string,
) (evergreencore.AVViewSchema, error) {
	return p.provider.Describe(ctx, viewID)
}

func (p *EvergreenSYProvider) Query(
	ctx context.Context,
	viewID string,
	query evergreencore.AVQuery,
) (evergreencore.AVPage, error) {
	return p.provider.Query(ctx, viewID, query)
}

func (p *EvergreenSYProvider) GetCell(
	ctx context.Context,
	row evergreencore.AVRowRef,
	column evergreencore.AVColumnBinding,
) (evergreencore.AVCellValue, error) {
	return p.provider.GetCell(ctx, row, column)
}

func (p *EvergreenSYProvider) PlanPatch(
	ctx context.Context,
	principal evergreencore.Principal,
	patch evergreencore.AVPatch,
) (evergreencore.PlannedOperation, error) {
	return p.provider.PlanPatch(ctx, principal, patch)
}

type NativeAVProvider struct {
	attrView *AttributeView
}

func NewNativeAVProvider(attrView *AttributeView) *NativeAVProvider {
	return &NativeAVProvider{attrView: attrView}
}

func (p *NativeAVProvider) Describe(
	_ context.Context,
	viewID string,
) (evergreencore.AVViewSchema, error) {
	if p == nil || p.attrView == nil {
		return evergreencore.AVViewSchema{}, ErrAttributeViewNotFound
	}
	if viewID == "" {
		if view, err := p.attrView.GetFirstView(); err == nil {
			viewID = view.ID
		}
	}
	columns := make([]evergreencore.AVColumnDefinition, 0, len(p.attrView.KeyValues))
	for _, keyValues := range p.attrView.KeyValues {
		if keyValues == nil || keyValues.Key == nil {
			continue
		}
		columns = append(columns, evergreencore.AVColumnDefinition{
			ID: keyValues.Key.ID, Name: keyValues.Key.Name, Value: string(keyValues.Key.Type),
			Editable: true, Binding: evergreencore.AVColumnBinding{Field: "native." + keyValues.Key.ID},
		})
	}
	return evergreencore.AVViewSchema{Definition: evergreencore.AVViewDefinition{
		Spec: evergreencore.AVViewSpec, ID: viewID, Name: p.attrView.Name,
		Provider: evergreencore.AVProviderDefinition{Type: NativeProviderType},
		Columns:  columns,
	}}, nil
}

func (p *NativeAVProvider) Query(
	_ context.Context,
	_ string,
	query evergreencore.AVQuery,
) (evergreencore.AVPage, error) {
	if p == nil || p.attrView == nil {
		return evergreencore.AVPage{}, ErrAttributeViewNotFound
	}
	blockValues := p.attrView.GetBlockKeyValues()
	if blockValues == nil {
		return evergreencore.AVPage{Rows: []evergreencore.AVRow{}}, nil
	}
	rows := make([]evergreencore.AVRow, 0, len(blockValues.Values))
	for _, blockValue := range blockValues.Values {
		if blockValue == nil {
			continue
		}
		row := evergreencore.AVRow{
			Ref:   evergreencore.AVRowRef{LogicalID: evergreencore.LogicalID(blockValue.BlockID)},
			Cells: map[string]evergreencore.AVCellValue{},
		}
		for _, keyValues := range p.attrView.KeyValues {
			if keyValues == nil || keyValues.Key == nil {
				continue
			}
			row.Cells[keyValues.Key.ID] = nativeCellValue(
				keyValues.Key,
				keyValues.GetValue(blockValue.BlockID),
			)
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Ref.LogicalID < rows[j].Ref.LogicalID })
	total := len(rows)
	start := query.Offset
	if start < 0 {
		start = 0
	}
	if start > len(rows) {
		start = len(rows)
	}
	end := len(rows)
	if query.Limit > 0 && start+query.Limit < end {
		end = start + query.Limit
	}
	return evergreencore.AVPage{Rows: rows[start:end], Total: total}, nil
}

func (p *NativeAVProvider) GetCell(
	_ context.Context,
	row evergreencore.AVRowRef,
	column evergreencore.AVColumnBinding,
) (evergreencore.AVCellValue, error) {
	if p == nil || p.attrView == nil {
		return evergreencore.AVCellValue{}, ErrAttributeViewNotFound
	}
	keyID := column.Field
	const prefix = "native."
	if len(keyID) >= len(prefix) && keyID[:len(prefix)] == prefix {
		keyID = keyID[len(prefix):]
	}
	keyValues, err := p.attrView.GetKeyValues(keyID)
	if err != nil {
		return evergreencore.AVCellValue{}, err
	}
	return nativeCellValue(keyValues.Key, keyValues.GetValue(string(row.LogicalID))), nil
}

func (p *NativeAVProvider) PlanPatch(
	context.Context,
	evergreencore.Principal,
	evergreencore.AVPatch,
) (evergreencore.PlannedOperation, error) {
	return evergreencore.PlannedOperation{}, &evergreencore.DiagnosticError{
		Diagnostics: []evergreencore.Diagnostic{{
			Code: evergreencore.CodeInvalidPlan, Level: "error", Path: "provider",
			Message: "native AV edits remain on the SiYuan transaction and undo/redo path",
		}},
	}
}

func nativeCellValue(key *Key, value *Value) evergreencore.AVCellValue {
	if key == nil || value == nil {
		return evergreencore.AVCellValue{Kind: "empty"}
	}
	switch key.Type {
	case KeyTypeBlock:
		if value.Block != nil {
			return evergreencore.AVCellValue{Kind: "text", Text: value.Block.Content}
		}
	case KeyTypeText:
		if value.Text != nil {
			return evergreencore.AVCellValue{Kind: "text", Text: value.Text.Content}
		}
	case KeyTypeSelect, KeyTypeMSelect:
		cell := evergreencore.AVCellValue{Kind: "tags"}
		for _, selected := range value.MSelect {
			cell.Texts = append(cell.Texts, selected.Content)
		}
		return cell
	case KeyTypeRelation:
		if value.Relation == nil {
			return evergreencore.AVCellValue{Kind: "relation", Edges: []evergreencore.AVEdgeValue{}}
		}
		edges := make([]evergreencore.AVEdgeValue, 0, len(value.Relation.BlockIDs))
		for index, blockID := range value.Relation.BlockIDs {
			edges = append(edges, evergreencore.AVEdgeValue{
				ID:        evergreencore.EdgeID(fmt.Sprintf("native:%s:%s:%d", key.ID, value.BlockID, index)),
				OwnerID:   evergreencore.LogicalID(value.BlockID),
				Direction: evergreencore.EdgeDirectionOutgoing,
				Schema:    evergreencore.BasicSchema,
				Target: evergreencore.EntityRef{
					EntityType: "block", BlockID: blockID,
				},
				Type: "reference",
			})
		}
		return evergreencore.AVCellValue{
			Kind: "relation", Edges: edges, EdgeCount: len(edges), UniqueTargetCount: uniqueNativeTargets(edges),
		}
	}
	return evergreencore.AVCellValue{Kind: string(key.Type)}
}

func uniqueNativeTargets(edges []evergreencore.AVEdgeValue) int {
	targets := map[string]struct{}{}
	for _, edge := range edges {
		targets[edge.Target.BlockID] = struct{}{}
	}
	return len(targets)
}

func (av *AttributeView) IsSourceBacked() bool {
	return av != nil && av.Provider != nil &&
		av.Provider.Type == evergreencore.AVProviderType &&
		av.Provider.Protocol == evergreencore.AVProviderProtocol
}

func ValidateProvider(attrView *AttributeView) error {
	if attrView == nil || attrView.Provider == nil {
		return nil
	}
	provider := attrView.Provider
	if provider.Type != evergreencore.AVProviderType {
		return fmt.Errorf("unsupported attribute view provider %q", provider.Type)
	}
	if provider.Protocol != evergreencore.AVProviderProtocol {
		if providerProtocolIsTooNew(provider.Protocol, evergreencore.AVProviderProtocol) {
			return &evergreencore.DiagnosticError{Diagnostics: []evergreencore.Diagnostic{{
				Code: evergreencore.CodeSchemaTooNew, Level: "error",
				Path: "provider.protocol",
				Message: fmt.Sprintf(
					"provider protocol %q is newer than supported %q",
					provider.Protocol,
					evergreencore.AVProviderProtocol,
				),
			}}}
		}
		return fmt.Errorf("unsupported Evergreen provider protocol %q", provider.Protocol)
	}
	if provider.Query.EntityType != evergreencore.EntityClaim {
		return fmt.Errorf("Evergreen provider entity type %q is not supported", provider.Query.EntityType)
	}
	switch provider.Query.ClaimKind {
	case "", evergreencore.KnowledgeKind, evergreencore.OpinionKind:
	default:
		if strings.TrimSpace(string(provider.Query.ClaimKind)) == "" {
			return fmt.Errorf("Evergreen provider claim kind is invalid")
		}
	}
	for _, keyValues := range attrView.KeyValues {
		if keyValues == nil || keyValues.Key == nil || keyValues.Key.Binding == nil {
			return fmt.Errorf("source-backed attribute view columns require provider bindings")
		}
	}
	return nil
}

func providerProtocolIsTooNew(got, current string) bool {
	gotIndex, currentIndex := strings.LastIndex(got, "/v"), strings.LastIndex(current, "/v")
	if gotIndex < 1 || currentIndex < 1 ||
		got[:gotIndex] != current[:currentIndex] {
		return false
	}
	gotMajor, gotErr := strconv.Atoi(got[gotIndex+2:])
	currentMajor, currentErr := strconv.Atoi(current[currentIndex+2:])
	return gotErr == nil && currentErr == nil && gotMajor > currentMajor
}

func (av *AttributeView) suspendSourceBackedValues() func() {
	if !av.IsSourceBacked() {
		return func() {}
	}
	values := make([][]*Value, len(av.KeyValues))
	for index, keyValues := range av.KeyValues {
		if keyValues == nil {
			continue
		}
		values[index] = keyValues.Values
		keyValues.Values = nil
	}
	type viewRuntime struct {
		itemIDs      []string
		groupItemIDs []string
		groups       []*View
		groupKey     *Key
		groupVal     *Value
	}
	views := make([]viewRuntime, len(av.Views))
	for index, view := range av.Views {
		if view == nil {
			continue
		}
		views[index] = viewRuntime{
			itemIDs: view.ItemIDs, groupItemIDs: view.GroupItemIDs, groups: view.Groups,
			groupKey: view.GroupKey, groupVal: view.GroupVal,
		}
		view.ItemIDs = nil
		view.GroupItemIDs = nil
		view.Groups = nil
		view.GroupKey = nil
		view.GroupVal = nil
	}
	return func() {
		for index, keyValues := range av.KeyValues {
			if keyValues != nil {
				keyValues.Values = values[index]
			}
		}
		for index, view := range av.Views {
			if view == nil {
				continue
			}
			view.ItemIDs = views[index].itemIDs
			view.GroupItemIDs = views[index].groupItemIDs
			view.Groups = views[index].groups
			view.GroupKey = views[index].groupKey
			view.GroupVal = views[index].groupVal
		}
	}
}

func NewSourceBackedAttributeView(id string, schema evergreencore.AVViewSchema) *AttributeView {
	view := &View{
		ID: ast.NewNodeID(), Name: schema.Definition.Name,
		Filters: []*ViewFilter{{Combination: FilterCombinationAnd}},
		Sorts:   []*ViewSort{}, PageSize: ViewDefaultPageSize,
		LayoutType: LayoutTypeTable, Table: NewLayoutTable(),
	}
	attrView := &AttributeView{
		Spec: PlainTextSpec, ID: id, Name: schema.Definition.Name,
		Provider:  &schema.Definition.Provider,
		KeyValues: []*KeyValues{}, KeyIDs: []string{}, Views: []*View{view},
		RenderedViewables: map[string]Viewable{},
	}
	for index, column := range schema.Definition.Columns {
		keyType := KeyTypeText
		if index == 0 {
			keyType = KeyTypeBlock
		}
		switch column.Value {
		case "tags":
			if index != 0 {
				keyType = KeyTypeMSelect
			}
		case "relation":
			if index != 0 {
				keyType = KeyTypeRelation
			}
		}
		key := NewKey(column.ID, column.Name, "", keyType)
		binding := column.Binding
		key.Binding = &binding
		attrView.KeyValues = append(attrView.KeyValues, &KeyValues{Key: key})
		attrView.KeyIDs = append(attrView.KeyIDs, key.ID)
		view.Table.Columns = append(view.Table.Columns, &ViewTableColumn{BaseField: &BaseField{ID: key.ID}})
	}
	return attrView
}

func NewProviderValueID() string {
	return ast.NewNodeID()
}
