package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/88250/lute/ast"
	"github.com/88250/lute/parse"
	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	"github.com/siyuan-note/siyuan/kernel/av"
	"github.com/siyuan-note/siyuan/kernel/filesys"
	"github.com/siyuan-note/siyuan/kernel/sql"
	"github.com/siyuan-note/siyuan/kernel/treenode"
	"github.com/siyuan-note/siyuan/kernel/util"
	"github.com/vmihailenco/msgpack/v5"
)

type nativeAVSnapshot struct {
	path  string
	boxID string
	raw   []byte
	known []byte
	view  *av.AttributeView
}

// 原生 AV 的读写只访问事务认证会话，内存缓存和数据库通知在 journal 提交后刷新。
func (tx *Transaction) parseNativeAV(id, blockID string) (*av.AttributeView, error) {
	if !ast.IsNodeIDPattern(id) {
		return nil, av.ErrInvalidAttributeViewID
	}
	if blockID != "" {
		tree, err := tx.loadTree(blockID)
		if err != nil {
			return nil, err
		}
		return tx.parseNativeAVInBox(id, tree.Box, true)
	}
	return tx.parseNativeAVInBox(id, "", false)
}

func (tx *Transaction) parseNativeAVInBox(id, boxID string, scoped bool) (*av.AttributeView, error) {
	if !ast.IsNodeIDPattern(id) {
		return nil, av.ErrInvalidAttributeViewID
	}
	var avPath, avBoxID string
	if scoped && IsEncryptedBox(boxID) {
		avPath, avBoxID = av.FindAttributeViewPathInBox(id, boxID)
	} else {
		avPath, avBoxID = av.FindAttributeViewPath(id)
		if scoped && avBoxID != "" {
			return nil, evergreenUnauthorized("native AV is outside the carrier notebook scope")
		}
	}
	if avPath == "" && scoped {
		return nil, av.ErrAttributeViewNotFound
	}
	if snapshot, exists := tx.nativeTrees.avs[id]; exists {
		if avPath != "" && snapshot.boxID != avBoxID {
			return nil, evergreenUnauthorized("native AV notebook scope changed during the transaction")
		}
		return snapshot.view, nil
	}
	if avPath == "" {
		avPath, avBoxID = av.FindAttributeViewPath(id)
	}
	if avPath == "" {
		return nil, av.ErrAttributeViewNotFound
	}
	p, err := filepath.Rel(util.DataDir, avPath)
	if err != nil {
		return nil, err
	}
	p = filepath.ToSlash(p)
	if !fs.ValidPath(p) || strings.HasPrefix(p, "../") {
		return nil, evergreenUnauthorized("native AV path is outside the authenticated workspace")
	}
	raw, err := fs.ReadFile(tx.nativeTrees.session.Filesystem(), p)
	if write, exists := tx.nativeTrees.files[p]; exists && !tx.isReplay {
		raw, err = write.After, nil
	}
	if err != nil {
		return nil, err
	}
	view, err := av.ParseAttributeViewData(id, raw)
	if err != nil {
		return nil, err
	}
	if view.ID != id || view.Provider != nil {
		return nil, evergreenUnauthorized("native AV editing requires an existing native definition")
	}
	known, err := av.MarshalAttributeViewPersistence(view)
	if err != nil {
		return nil, err
	}
	tx.nativeTrees.avs[id] = nativeAVSnapshot{
		path: p, boxID: avBoxID, raw: append([]byte(nil), raw...), known: known, view: view,
	}
	return view, nil
}

func (tx *Transaction) saveNativeAV(view *av.AttributeView) error {
	snapshot, exists := tx.nativeTrees.avs[view.ID]
	if !exists || snapshot.view != view {
		return fmt.Errorf("native AV was not read through the transaction")
	}
	if tx.isReplay {
		return nil
	}
	if err := view.ValidateListLayouts(); err != nil {
		return err
	}
	if err := av.ValidateProvider(view); err != nil {
		return err
	}
	if err := view.NormalizeRichText(); err != nil {
		return err
	}
	known, err := av.MarshalAttributeViewPersistence(view)
	if err != nil {
		return err
	}
	after, err := mergeNativeAVFields(snapshot.raw, snapshot.known, known)
	if err != nil {
		return err
	}
	p := snapshot.path
	write, exists := tx.nativeTrees.files[p]
	if !exists {
		write = core.NativeFileWrite{Path: p, Kind: core.NativeFileAttributeView, Before: snapshot.raw}
	}
	write.After = after
	tx.nativeTrees.files[p] = write
	return nil
}

// 仅应用已知结构的差异；有 ID 的数组按身份合并，保留嵌套未知字段和 JSON 数字原文。
func mergeNativeAVFields(raw, before, after []byte) ([]byte, error) {
	if bytes.Equal(before, after) {
		return append([]byte(nil), raw...), nil
	}
	var oldObject, nextObject, original map[string]json.RawMessage
	if json.Unmarshal(before, &oldObject) == nil && oldObject != nil &&
		json.Unmarshal(after, &nextObject) == nil && nextObject != nil &&
		json.Unmarshal(raw, &original) == nil && original != nil {
		for key, old := range oldObject {
			next, exists := nextObject[key]
			if !exists {
				delete(original, key)
				continue
			}
			if bytes.Equal(old, next) {
				continue
			}
			merged, err := mergeNativeAVFields(original[key], old, next)
			if err != nil {
				return nil, err
			}
			original[key] = merged
		}
		for key, next := range nextObject {
			if _, exists := oldObject[key]; !exists {
				original[key] = next
			}
		}
		return json.Marshal(original)
	}
	var oldArray, nextArray, originalArray []json.RawMessage
	if json.Unmarshal(before, &oldArray) == nil && json.Unmarshal(after, &nextArray) == nil &&
		json.Unmarshal(raw, &originalArray) == nil {
		identity := func(raw json.RawMessage) string {
			var object struct {
				ID  string `json:"id"`
				Key *struct {
					ID string `json:"id"`
				} `json:"key"`
			}
			if json.Unmarshal(raw, &object) != nil {
				return ""
			}
			if object.ID == "" && object.Key != nil {
				return object.Key.ID
			}
			return object.ID
		}
		byID := func(array []json.RawMessage) map[string]json.RawMessage {
			ret := map[string]json.RawMessage{}
			for _, item := range array {
				if id := identity(item); id != "" {
					ret[id] = item
				}
			}
			return ret
		}
		oldIDs, rawIDs := byID(oldArray), byID(originalArray)
		for index, next := range nextArray {
			id := identity(next)
			old, exists := oldIDs[id]
			if id == "" || !exists {
				continue
			}
			merged, err := mergeNativeAVFields(rawIDs[id], old, next)
			if err != nil {
				return nil, err
			}
			nextArray[index] = merged
		}
		return json.Marshal(nextArray)
	}
	if !json.Valid(after) {
		return nil, fmt.Errorf("invalid native AV field")
	}
	return append([]byte(nil), after...), nil
}

func (tx *Transaction) validateNativeAVBindings(tree *parse.Tree) error {
	var err error
	ast.Walk(tree.Root, func(node *ast.Node, entering bool) ast.WalkStatus {
		if !entering {
			return ast.WalkContinue
		}
		ids := []string{}
		if node.Type == ast.NodeAttributeView {
			ids = append(ids, node.AttributeViewID)
		}
		if bound := node.IALAttr(av.NodeAttrNameAvs); bound != "" {
			ids = append(ids, strings.Split(bound, ",")...)
		}
		for _, id := range ids {
			if _, err = tx.parseNativeAVInBox(id, tree.Box, true); err != nil {
				return ast.WalkStop
			}
		}
		return ast.WalkContinue
	})
	return err
}

func (tx *Transaction) parseTransactionAV(id, blockID string) (*av.AttributeView, error) {
	if tx != nil && tx.nativeTrees != nil {
		return tx.parseNativeAV(id, blockID)
	}
	return avParseView(id, blockID)
}

func (tx *Transaction) saveTransactionAV(view *av.AttributeView, blockID string) error {
	if tx != nil && tx.nativeTrees != nil {
		return tx.saveNativeAV(view)
	}
	return avSaveView(view, blockID)
}

func (tx *Transaction) transactionAVNames(ids string) (string, error) {
	names := []string{}
	for _, id := range strings.Split(ids, ",") {
		if id == "" {
			continue
		}
		view, err := tx.parseTransactionAV(id, "")
		if err != nil {
			return "", err
		}
		name := view.Name
		if name == "" {
			name = Conf.language(105)
		}
		tpl := strings.ReplaceAll(attrAvNameTpl, "${avID}", id)
		names = append(names, strings.ReplaceAll(tpl, "${avName}", util.EscapeHTML(name)))
	}
	return strings.Join(names, "&nbsp;"), nil
}

func (tx *Transaction) nativeAVOperationView(view *av.AttributeView, operation *Operation) (*av.View, error) {
	if operation.ViewID != "" {
		if result := view.GetView(operation.ViewID); result != nil {
			return result, nil
		}
		return nil, av.ErrViewNotFound
	}
	if operation.BlockID != "" {
		tree, err := tx.loadTree(operation.BlockID)
		if err != nil {
			return nil, err
		}
		if node := treenode.GetNodeInTree(tree, operation.BlockID); node != nil && node.Type == ast.NodeAttributeView {
			if node.AttributeViewID != view.ID {
				return nil, av.ErrAttributeViewNotFound
			}
			if result := view.GetView(node.IALAttr(av.NodeAttrView)); result != nil {
				return result, nil
			}
		}
	}
	return view.GetFirstView()
}

func nativeAVAction(action string) bool {
	switch action {
	case "setAttrViewName", "updateAttrViewCell", "updateAttrViewCells", "changeAttrViewLayout",
		"setAttrViewBlockView", "setAttrViewBlockVisibleViews", "setAttrViewColHidden",
		"setAttrViewColWidth", "setAttrViewColsWidth", "setAttrViewColAlign", "setAttrViewColWrap",
		"setAttrViewColPin", "sortAttrViewBinding", "setAttrViewViewName", "setAttrViewViewIcon",
		"setAttrViewViewDesc", "setAttrViewPageSize", "setAttrViewNewItemTemplates":
		return true
	}
	return false
}

func (tx *Transaction) doNativeAVOperation(operation *Operation) error {
	switch operation.Action {
	case "setAttrViewName":
		return tx.setNativeAVName(operation)
	case "updateAttrViewCell":
		_, err := updateAttributeViewCellInBlock(tx, operation.AvID, operation.BlockID,
			operation.KeyID, operation.RowID, operation.Data)
		return err
	case "updateAttrViewCells":
		if err := tx.doUpdateAttrViewCells(operation); err != nil {
			return err
		}
		return nil
	case "sortAttrViewBinding":
		tree, err := tx.loadTree(operation.ID)
		if err != nil {
			return err
		}
		node := treenode.GetNodeInTree(tree, operation.ID)
		ids, err := sortAttrViewBindingIDs(strings.Split(node.IALAttr(av.NodeAttrNameAvs), ","),
			operation.AvID, operation.PreviousID)
		if err != nil {
			return err
		}
		names, err := tx.transactionAVNames(strings.Join(ids, ","))
		if err != nil {
			return err
		}
		return setNodeAttrsWithTx(tx, node, tree, map[string]string{
			av.NodeAttrNameAvs: strings.Join(ids, ","), av.NodeAttrViewNames: names})
	}
	definition, err := tx.parseNativeAV(operation.AvID, operation.BlockID)
	if err != nil {
		return err
	}
	if operation.Action == "setAttrViewNewItemTemplates" {
		raw, err := json.Marshal(operation.Data)
		if err != nil {
			return err
		}
		var config av.NewItemTemplatesConfig
		if err = json.Unmarshal(raw, &config); err != nil {
			return err
		}
		if err = definition.SetNewItemTemplates(&config); err != nil {
			return err
		}
		return tx.saveNativeAV(definition)
	}
	view, err := tx.nativeAVOperationView(definition, operation)
	if err != nil {
		return err
	}
	switch operation.Action {
	case "changeAttrViewLayout":
		if err = changeAttrViewLayout(definition, view, operation.Layout); err != nil {
			return err
		}
		mirrors := append([]string{operation.BlockID}, treenode.GetMirrorAttrViewBlockIDs(definition.ID)...)
		for _, id := range mirrors {
			if id == "" {
				continue
			}
			if _, err = tx.loadTree(id); err != nil {
				return err
			}
		}
		for _, snapshot := range tx.nativeTrees.snapshots {
			ast.Walk(snapshot.tree.Root, func(node *ast.Node, entering bool) ast.WalkStatus {
				if !entering || node.Type != ast.NodeAttributeView {
					return ast.WalkContinue
				}
				if node.AttributeViewID == definition.ID &&
					(node.ID == operation.BlockID || node.IALAttr(av.NodeAttrView) == view.ID) {
					node.AttributeViewType = string(view.LayoutType)
					if node.ID == operation.BlockID {
						node.SetIALAttr(av.NodeAttrView, view.ID)
					}
					tx.writeTree(snapshot.tree)
				}
				return ast.WalkContinue
			})
		}
	case "setAttrViewBlockView", "setAttrViewBlockVisibleViews":
		tree, err := tx.loadTree(operation.BlockID)
		if err != nil {
			return err
		}
		node := treenode.GetNodeInTree(tree, operation.BlockID)
		if node == nil || node.Type != ast.NodeAttributeView || node.AttributeViewID != definition.ID {
			return av.ErrAttributeViewNotFound
		}
		if operation.Action == "setAttrViewBlockView" {
			selected := definition.GetView(operation.ID)
			if selected == nil {
				return av.ErrViewNotFound
			}
			node.SetIALAttr(av.NodeAttrView, selected.ID)
			node.AttributeViewType = string(selected.LayoutType)
		} else {
			if len(operation.ViewIDs) == 0 {
				return fmt.Errorf("at least one visible view is required")
			}
			selected := map[string]bool{}
			for _, id := range operation.ViewIDs {
				if definition.GetView(id) == nil {
					return av.ErrViewNotFound
				}
				selected[id] = true
			}
			ids := []string{}
			for _, candidate := range definition.Views {
				if selected[candidate.ID] {
					ids = append(ids, candidate.ID)
				}
			}
			node.SetIALAttr(av.NodeAttrVisibleViewIDs, strings.Join(ids, ","))
		}
		tx.writeTree(tree)
		return nil
	case "setAttrViewColHidden":
		hidden, ok := operation.Data.(bool)
		if !ok {
			return fmt.Errorf("invalid native AV visibility")
		}
		ids := operation.ViewIDs
		if len(ids) == 0 {
			ids = []string{view.ID}
		}
		if err = setAttributeViewFieldsHidden(definition, operation.ID, ids, hidden); err != nil {
			return err
		}
	case "setAttrViewColWidth", "setAttrViewColsWidth", "setAttrViewColAlign", "setAttrViewColWrap", "setAttrViewColPin":
		field := getAttributeViewField(view, operation.ID)
		if field == nil && operation.Action != "setAttrViewColsWidth" {
			return av.ErrKeyNotFound
		}
		switch operation.Action {
		case "setAttrViewColWidth":
			width, ok := operation.Data.(string)
			if !ok {
				return fmt.Errorf("invalid native AV width")
			}
			if view.LayoutType == av.LayoutTypeTable {
				for _, column := range view.Table.Columns {
					if column.ID == operation.ID {
						column.Width = av.FilterWidthValue(width)
					}
				}
			}
		case "setAttrViewColsWidth":
			widths, ok := operation.Data.(map[string]any)
			if !ok {
				return fmt.Errorf("invalid native AV widths")
			}
			for id, value := range widths {
				width, ok := value.(string)
				if !ok {
					return fmt.Errorf("invalid native AV width")
				}
				if view.LayoutType == av.LayoutTypeTable {
					for _, column := range view.Table.Columns {
						if column.ID == id {
							column.Width = av.FilterWidthValue(width)
						}
					}
				}
			}
		case "setAttrViewColAlign":
			align, ok := operation.Data.(string)
			if !ok || !av.TableColumnAlign(align).IsValid() || view.GetTableLayout() == nil {
				return av.ErrInvalidColumnAlign
			}
			for _, column := range view.GetTableLayout().Columns {
				if column.ID == operation.ID {
					column.Align = av.TableColumnAlign(align)
				}
			}
		case "setAttrViewColWrap":
			wrap, ok := operation.Data.(bool)
			if !ok {
				return fmt.Errorf("invalid native AV wrapping")
			}
			field.Wrap = wrap
			allWrap := true
			for _, id := range attributeViewFieldIDs(view) {
				allWrap = allWrap && getAttributeViewField(view, id).Wrap
			}
			switch view.LayoutType {
			case av.LayoutTypeTable, av.LayoutTypeList, av.LayoutTypeCalendar:
				view.GetTableLayout().WrapField = allWrap
			case av.LayoutTypeGallery:
				view.Gallery.WrapField = allWrap
			case av.LayoutTypeKanban:
				view.Kanban.WrapField = allWrap
			}
		case "setAttrViewColPin":
			pin, ok := operation.Data.(bool)
			if !ok || view.GetTableLayout() == nil {
				return fmt.Errorf("invalid native AV pin")
			}
			for _, column := range view.GetTableLayout().Columns {
				column.Pin = pin && column.ID == operation.ID
			}
		}
	case "setAttrViewViewName", "setAttrViewViewIcon", "setAttrViewViewDesc":
		value, ok := operation.Data.(string)
		if !ok {
			return fmt.Errorf("invalid native AV view text")
		}
		switch operation.Action {
		case "setAttrViewViewName":
			view.Name = strings.TrimSpace(value)
		case "setAttrViewViewIcon":
			view.Icon = filterAttrViewIconValue(value)
		case "setAttrViewViewDesc":
			view.Desc = value
		}
	case "setAttrViewPageSize":
		number, err := getAttrViewOperationNumber(operation)
		if err != nil || number < 1 {
			return fmt.Errorf("invalid native AV page size")
		}
		view.PageSize = int(number)
	default:
		return evergreenUnauthorized("native AV action is not yet atomic: " + operation.Action)
	}
	regenAttrViewGroups(definition)
	return tx.saveNativeAV(definition)
}

func (tx *Transaction) refreshNativeAVReference(ref *sql.Ref, definitions map[string]*ast.Node) error {
	tree, err := tx.loadTree(ref.BlockID)
	if err != nil {
		return err
	}
	if tree.Box != ref.Box || tree.Path != ref.Path {
		return fmt.Errorf("native AV reference source identity changed")
	}
	node := treenode.GetNodeInTree(tree, ref.BlockID)
	if node == nil || node.Type != ast.NodeAttributeView {
		return av.ErrAttributeViewNotFound
	}
	view, err := tx.parseNativeAV(node.AttributeViewID, ref.BlockID)
	if err != nil {
		return err
	}
	if updateAttributeViewDynamicRefTexts(view, definitions) {
		return tx.saveNativeAV(view)
	}
	return nil
}

func (tx *Transaction) setNativeAVBinding(id, blockID string, bind bool) error {
	if tx.nativeTrees.avErr != nil {
		return tx.nativeTrees.avErr
	}
	tree, err := tx.loadTree(blockID)
	if err != nil {
		return err
	}
	node := treenode.GetNodeInTree(tree, blockID)
	if node == nil {
		return ErrBlockNotFound
	}
	if _, err = tx.parseNativeAV(id, blockID); err != nil {
		return err
	}
	ids := []string{}
	for _, bound := range strings.Split(node.IALAttr(av.NodeAttrNameAvs), ",") {
		if bound != "" && bound != id {
			ids = append(ids, bound)
		}
	}
	if bind {
		ids = append(ids, id)
	}
	value := strings.Join(ids, ",")
	names, err := tx.transactionAVNames(value)
	if err != nil {
		return err
	}
	if value == "" {
		node.RemoveIALAttr(av.NodeAttrNameAvs)
		node.RemoveIALAttr(av.NodeAttrViewNames)
	} else {
		node.SetIALAttr(av.NodeAttrNameAvs, value)
		node.SetIALAttr(av.NodeAttrViewNames, names)
	}
	snapshot := tx.nativeTrees.snapshots[tree.ID]
	raw, err := filesys.RenderNativeTree(tree, snapshot.raw)
	if err != nil {
		return err
	}
	tree, err = filesys.LoadTreeProjectionByData(raw, tree.Box, tree.Path, tx.luteEngine)
	if err != nil {
		return err
	}
	if persisted := treenode.GetNodeInTree(tree, blockID); persisted == nil ||
		persisted.IALAttr(av.NodeAttrNameAvs) != value {
		return fmt.Errorf("native AV binding did not survive canonical rendering")
	}
	tx.nativeTrees.avBindings[blockID] = nativeAVBindingExpectation{rootID: tree.ID, value: value}
	tx.writeTree(tree)
	return nil
}

func (tx *Transaction) validateNativeAVBindingExpectations(trees map[string]*parse.Tree, phase string) error {
	for blockID, expected := range tx.nativeTrees.avBindings {
		tree := trees[expected.rootID]
		if tree == nil {
			continue
		}
		node := treenode.GetNodeInTree(tree, blockID)
		if node == nil {
			return fmt.Errorf("native AV binding block [%s] is absent from %s", blockID, phase)
		}
		if actual := node.IALAttr(av.NodeAttrNameAvs); actual != expected.value {
			return fmt.Errorf("native AV binding changed in %s: block [%s] has [%s], expected [%s]",
				phase, blockID, actual, expected.value)
		}
	}
	return nil
}

func (tx *Transaction) collectNativeAVChanges() error {
	if tx.nativeTrees.avErr != nil {
		return tx.nativeTrees.avErr
	}
	if tx.isReplay {
		// 撤销重做使用内部捕获的完整辅助 after-image，不重新计算时间或反向关联。
		return nil
	}
	for _, id := range tx.relatedAvIDs {
		view, err := tx.parseNativeAV(id, "")
		if err != nil {
			return err
		}
		regenAttrViewGroups(view)
		if err = tx.saveNativeAV(view); err != nil {
			return err
		}
	}
	// 定义块的名称、图标和静态锚文本更新，同笔提交对应的 AV 主键投影。
	for _, definition := range tx.nodes {
		for _, id := range strings.Split(definition.IALAttr(av.NodeAttrNameAvs), ",") {
			if id == "" {
				continue
			}
			view, err := tx.parseNativeAV(id, "")
			if err != nil {
				return err
			}
			if value := view.GetBlockValueByBoundID(definition.ID); value != nil {
				icon, content := getNodeAvBlockText(definition, id)
				value.Block.Icon, value.Block.Content = icon, util.UnescapeHTML(content)
				value.Block.RefSubtype = getNodeAvBlockRefSubtype(definition, id)
				if err = tx.saveNativeAV(view); err != nil {
					return err
				}
			}
		}
	}
	// 读取过的 AV 定义成为 CAS 依赖，防止绑定名称从并发版本取值。
	for _, snapshot := range tx.nativeTrees.avs {
		p := snapshot.path
		if _, exists := tx.nativeTrees.files[p]; !exists {
			tx.nativeTrees.files[p] = core.NativeFileWrite{
				Path: p, Kind: core.NativeFileAttributeView, Before: snapshot.raw, After: snapshot.raw}
		}
	}
	return tx.stageNativeAVCaches()
}

func (tx *Transaction) stageNativeAVCaches() error {
	boxes := map[string]struct{}{}
	for id := range tx.nativeTrees.dirty {
		snapshot := tx.nativeTrees.snapshots[id]
		boxes[nativeAVCacheBox(snapshot.tree.Box)] = struct{}{}
	}
	boxIDs := make([]string, 0, len(boxes))
	for boxID := range boxes {
		boxIDs = append(boxIDs, boxID)
	}
	sort.Strings(boxIDs)
	for _, boxID := range boxIDs {
		if err := tx.stageNativeAVMirrorCache(boxID); err != nil {
			return err
		}
	}
	if err := tx.stageNativeAVRelationCache(); err != nil {
		return err
	}
	finalized := map[string]bool{}
	for _, expected := range tx.nativeTrees.avBindings {
		if finalized[expected.rootID] {
			continue
		}
		finalized[expected.rootID] = true
		tree := tx.trees[expected.rootID]
		snapshot := tx.nativeTrees.snapshots[expected.rootID]
		raw, renderErr := filesys.RenderNativeTree(tree, snapshot.raw)
		if renderErr != nil {
			return renderErr
		}
		clone, loadErr := filesys.LoadTreeProjectionByData(raw, tree.Box, tree.Path, tx.luteEngine)
		if loadErr != nil {
			return loadErr
		}
		if err := tx.validateNativeAVBindingExpectations(map[string]*parse.Tree{tree.ID: clone},
			"finalized trees"); err != nil {
			return err
		}
		tx.nativeTrees.finalRaw[tree.ID] = raw
	}
	return nil
}

func (tx *Transaction) stageNativeAVMirrorCache(boxID string) error {
	mirrorPath := nativeAVCachePath(boxID, "blocks.msgpack")
	before, err := fs.ReadFile(tx.nativeTrees.session.Filesystem(), mirrorPath)
	create := errors.Is(err, fs.ErrNotExist)
	if err != nil && !create {
		return err
	}
	mirrors := map[string][]string{}
	if !create {
		if err = msgpack.Unmarshal(before, &mirrors); err != nil {
			return err
		}
	}
	original := cloneNativeAVIndex(mirrors)
	for id := range tx.nativeTrees.dirty {
		snapshot := tx.nativeTrees.snapshots[id]
		if nativeAVCacheBox(snapshot.tree.Box) != boxID {
			continue
		}
		if len(snapshot.raw) != 0 {
			tree, loadErr := filesys.LoadTreeProjectionByData(snapshot.raw, snapshot.tree.Box,
				snapshot.tree.Path, tx.luteEngine)
			if loadErr != nil {
				return loadErr
			}
			removeNativeAVTreeMirrors(mirrors, tree.Root)
		}
		if tx.nativeTrees.deleted[id] {
			continue
		}
		addNativeAVTreeMirrors(mirrors, tx.trees[id].Root)
	}
	normalizeNativeAVIndex(mirrors)
	if !reflect.DeepEqual(original, mirrors) {
		after, marshalErr := msgpack.Marshal(mirrors)
		if marshalErr != nil {
			return marshalErr
		}
		tx.nativeTrees.files[mirrorPath] = core.NativeFileWrite{
			Path: mirrorPath, Kind: core.NativeFileAVCache, Before: before, After: after, Create: create}
	}
	return nil
}

func (tx *Transaction) stageNativeAVRelationCache() error {
	changedByBox := map[string]map[string]*av.AttributeView{}
	for p, write := range tx.nativeTrees.files {
		if write.Kind != core.NativeFileAttributeView || bytes.Equal(write.Before, write.After) {
			continue
		}
		id := strings.TrimSuffix(path.Base(p), ".json")
		snapshot, exists := tx.nativeTrees.avs[id]
		if exists {
			changed := changedByBox[snapshot.boxID]
			if changed == nil {
				changed = map[string]*av.AttributeView{}
				changedByBox[snapshot.boxID] = changed
			}
			changed[id] = snapshot.view
		}
	}
	boxIDs := make([]string, 0, len(changedByBox))
	for boxID := range changedByBox {
		boxIDs = append(boxIDs, boxID)
	}
	sort.Strings(boxIDs)
	for _, boxID := range boxIDs {
		if err := tx.stageNativeAVRelationCacheForBox(boxID, changedByBox[boxID]); err != nil {
			return err
		}
	}
	return nil
}

func (tx *Transaction) stageNativeAVRelationCacheForBox(boxID string, changed map[string]*av.AttributeView) error {
	relationPath := nativeAVCachePath(boxID, "relations.msgpack")
	before, err := fs.ReadFile(tx.nativeTrees.session.Filesystem(), relationPath)
	create := errors.Is(err, fs.ErrNotExist)
	if err != nil && !create {
		return err
	}
	relations := map[string][]string{}
	if !create {
		if err = msgpack.Unmarshal(before, &relations); err != nil {
			return err
		}
	}
	original := cloneNativeAVIndex(relations)
	for source, view := range changed {
		for target, sources := range relations {
			relations[target] = removeNativeAVIndexValue(sources, source)
		}
		for _, values := range view.KeyValues {
			if values == nil || values.Key == nil || values.Key.Type != av.KeyTypeRelation ||
				values.Key.Relation == nil || values.Key.Relation.AvID == "" {
				continue
			}
			target := values.Key.Relation.AvID
			targetSnapshot, exists := tx.nativeTrees.avs[target]
			if exists && targetSnapshot.boxID != boxID {
				return evergreenUnauthorized("native relation target is outside the source AV notebook scope")
			}
			if !exists {
				_, targetBoxID := av.FindAttributeViewPath(target)
				if targetBoxID != boxID {
					return evergreenUnauthorized("native relation target is outside the source AV notebook scope")
				}
			}
			relations[target] = append(relations[target], source)
		}
	}
	normalizeNativeAVIndex(relations)
	if reflect.DeepEqual(original, relations) {
		return nil
	}
	after, err := msgpack.Marshal(relations)
	if err != nil {
		return err
	}
	tx.nativeTrees.files[relationPath] = core.NativeFileWrite{
		Path: relationPath, Kind: core.NativeFileAVCache, Before: before, After: after, Create: create}
	return nil
}

func nativeAVCacheBox(boxID string) string {
	if IsEncryptedBox(boxID) {
		return boxID
	}
	return ""
}

func nativeAVCachePath(boxID, name string) string {
	if boxID != "" {
		return path.Join(boxID, "storage", "av", name)
	}
	return path.Join("storage", "av", name)
}

func cloneNativeAVIndex(source map[string][]string) map[string][]string {
	ret := make(map[string][]string, len(source))
	for key, values := range source {
		ret[key] = append([]string(nil), values...)
	}
	normalizeNativeAVIndex(ret)
	return ret
}

func normalizeNativeAVIndex(index map[string][]string) {
	for key, values := range index {
		seen := map[string]bool{}
		normalized := values[:0]
		for _, value := range values {
			if value != "" && !seen[value] {
				seen[value] = true
				normalized = append(normalized, value)
			}
		}
		sort.Strings(normalized)
		index[key] = normalized
	}
}

func removeNativeAVIndexValue(values []string, target string) []string {
	ret := values[:0]
	for _, value := range values {
		if value != target {
			ret = append(ret, value)
		}
	}
	return ret
}

func removeNativeAVTreeMirrors(mirrors map[string][]string, root *ast.Node) {
	ast.Walk(root, func(node *ast.Node, entering bool) ast.WalkStatus {
		if entering && node.Type == ast.NodeAttributeView {
			mirrors[node.AttributeViewID] = removeNativeAVIndexValue(mirrors[node.AttributeViewID], node.ID)
		}
		return ast.WalkContinue
	})
}

func addNativeAVTreeMirrors(mirrors map[string][]string, root *ast.Node) {
	ast.Walk(root, func(node *ast.Node, entering bool) ast.WalkStatus {
		if entering && node.Type == ast.NodeAttributeView && node.AttributeViewID != "" && node.ID != "" {
			mirrors[node.AttributeViewID] = append(mirrors[node.AttributeViewID], node.ID)
		}
		return ast.WalkContinue
	})
}
