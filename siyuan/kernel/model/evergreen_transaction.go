package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/88250/lute/ast"
	"github.com/88250/lute/parse"
	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	"github.com/siyuan-note/siyuan/kernel/av"
	kernelEvergreen "github.com/siyuan-note/siyuan/kernel/evergreen"
	"github.com/siyuan-note/siyuan/kernel/filesys"
	"github.com/siyuan-note/siyuan/kernel/sql"
	"github.com/siyuan-note/siyuan/kernel/treenode"
	"github.com/siyuan-note/siyuan/kernel/util"
)

type nativeTreeSnapshot struct {
	path string
	raw  []byte
	tree *parse.Tree
}

type nativeAVBindingExpectation struct {
	rootID string
	value  string
}

type nativeTreeTransaction struct {
	ctx        context.Context
	session    core.DocumentStorageSession
	snapshots  map[string]nativeTreeSnapshot
	dirty      map[string]bool
	created    map[string]bool
	deleted    map[string]bool
	files      map[string]core.NativeFileWrite
	finalRaw   map[string][]byte
	avs        map[string]nativeAVSnapshot
	avErr      error
	avBindings map[string]nativeAVBindingExpectation
}

// TransactionNotebookScope resolves every notebook named by a native transaction before authority access.
// The API uses the same set for response leases; storage uses it only when an encrypted notebook is present.
func TransactionNotebookScope(tx *Transaction) []string {
	if tx == nil {
		return nil
	}
	boxes := map[string]struct{}{}
	addBox := func(boxID string) {
		if ast.IsNodeIDPattern(boxID) && (Conf.Box(boxID) != nil || IsEncryptedBox(boxID)) {
			boxes[boxID] = struct{}{}
		}
	}
	addBlock := func(blockID string) {
		if blockID == "" {
			return
		}
		block := treenode.GetBlockTree(blockID)
		if block == nil {
			for _, encryptedBoxID := range treenode.GetOpenedEncryptedBoxIDs() {
				if block = treenode.GetBlockTreeInBox(blockID, encryptedBoxID); block != nil {
					break
				}
			}
		}
		if block != nil {
			addBox(block.BoxID)
			return
		}
		addBox(blockID)
	}
	for _, operation := range tx.DoOperations {
		if operation == nil {
			continue
		}
		for _, id := range []string{
			operation.ID, operation.RootID, operation.ParentID, operation.PreviousID,
			operation.NextID, operation.BlockID,
		} {
			addBlock(id)
		}
		for _, id := range operation.BlockIDs {
			addBlock(id)
		}
		for _, id := range operation.SrcIDs {
			addBlock(id)
		}
		for _, src := range operation.Srcs {
			if id, ok := src["id"].(string); ok {
				addBlock(id)
			}
		}
		if operation.Tree != nil {
			addBox(operation.Tree.Box)
		}
		if _, boxID := av.FindAttributeViewPath(operation.AvID); boxID != "" {
			addBox(boxID)
		}
		for _, write := range operation.nativeFileWrites {
			if boxID, _, found := strings.Cut(write.Path, "/"); found {
				addBox(boxID)
			}
		}
		for _, key := range []string{"notebook", "box", "boxID", "rootID", "blockID"} {
			if id, ok := operation.Context[key].(string); ok {
				addBlock(id)
			}
		}
	}
	result := make([]string, 0, len(boxes))
	for boxID := range boxes {
		result = append(result, boxID)
	}
	sort.Strings(result)
	return result
}

// prepareNativeTransaction 在执行动作前读取完整原始版本；受管正文只接收当前人工认证身份。
func (tx *Transaction) prepareNativeTransaction() error {
	for _, op := range tx.DoOperations {
		if op == nil {
			return fmt.Errorf("invalid transaction operation")
		}
		if data, ok := op.Data.(string); ok && strings.Contains(strings.ToLower(data),
			strings.ToLower(kernelEvergreen.CarrierAttribute)) {
			return evergreenUnauthorized("internal Evergreen carrier cannot enter transaction input")
		}
		if op.Tree != nil && kernelEvergreen.HasEnvelope(op.Tree.Root) {
			return evergreenUnauthorized("managed document snapshots require an authorized domain command")
		}
	}
	hasDocument := false
	hasAV := false
	for _, op := range tx.DoOperations {
		hasAV = hasAV || nativeAVAction(op.Action)
		if op.Tree != nil {
			hasDocument = true
		}
		for _, id := range []string{op.ID, op.RootID, op.ParentID, op.PreviousID, op.NextID, op.BlockID} {
			if bt := treenode.GetBlockTree(id); bt != nil {
				hasDocument = true
			}
		}
	}
	if !hasDocument && !(hasAV && tx.nativeIdentity != nil) {
		return nil
	}
	scopeBoxes := TransactionNotebookScope(tx)
	hasEncryptedScope := false
	for _, boxID := range scopeBoxes {
		hasEncryptedScope = hasEncryptedScope || IsEncryptedBox(boxID)
	}
	if hasEncryptedScope && tx.nativeIdentity == nil {
		// Preserve existing ordinary encrypted editing for non-Evergreen callers. Protected documents
		// remain guarded by filesys; authenticated human requests take the journaled path below.
		return nil
	}
	requestContext := context.Background()
	var err error
	if hasEncryptedScope {
		requestContext, err = filesys.WithEvergreenNotebookScope(requestContext, scopeBoxes)
		if err != nil {
			return err
		}
	}
	session, err := (filesys.EvergreenDocumentStorage{Root: util.DataDir}).Open(requestContext)
	if err != nil {
		return err
	}
	native := &nativeTreeTransaction{ctx: requestContext, session: session,
		snapshots: map[string]nativeTreeSnapshot{}, dirty: map[string]bool{},
		created: map[string]bool{}, deleted: map[string]bool{}, files: map[string]core.NativeFileWrite{},
		finalRaw:   map[string][]byte{},
		avs:        map[string]nativeAVSnapshot{},
		avBindings: map[string]nativeAVBindingExpectation{}}
	keep := false
	defer func() {
		if !keep {
			session.Close()
		}
	}()
	managed := false
	for _, op := range tx.DoOperations {
		for _, id := range []string{op.ID, op.RootID, op.ParentID, op.PreviousID, op.NextID, op.BlockID} {
			bt := treenode.GetBlockTree(id)
			if bt == nil {
				continue
			}
			snapshot, err := tx.readNativeSnapshot(native, bt)
			if err != nil {
				return err
			}
			document, err := core.DecodeSY(snapshot.raw, core.ApplicationRegistry())
			if err != nil {
				return err
			}
			if document.ReadOnly {
				return &core.DiagnosticError{Diagnostics: document.Diagnostics}
			}
			if document.Envelope != nil && document.Envelope.Entity.EntityType == core.EntitySource {
				return evergreenUnauthorized("native Source body is read-only")
			}
			managed = managed || kernelEvergreen.HasEnvelope(snapshot.tree.Root)
		}
	}
	replayNative := false
	if tx.isReplay {
		for _, op := range tx.DoOperations {
			replayNative = replayNative || len(op.nativeRawHistory) != 0 || len(op.nativeFileWrites) != 0
		}
	}
	if !managed && !replayNative && !hasEncryptedScope && !(hasAV && tx.nativeIdentity != nil) {
		return nil
	}
	if tx.nativeIdentity == nil {
		return evergreenUnauthorized("managed native editing requires the current authenticated human")
	}
	// AV、配置、资产及文档创建删除必须先进入辅助写集；结构编辑只修改事务局部树。
	for _, op := range tx.DoOperations {
		switch op.Action {
		case "update", "doUpdateUpdated", "setAttrs", "insert", "appendInsert", "prependInsert",
			"delete", "move", "append", "swapBlockRef", "create", "restoreCreatedDoc", "removeCreatedDoc",
			"setAttrViewName":
		default:
			if !nativeAVAction(op.Action) {
				return evergreenUnauthorized("native transaction action is not yet atomic: " + op.Action)
			}
		}
		if tree := nativeTreeForBlock(native, op.ID); tree != nil {
			if op.Action != "setAttrViewName" {
				if err = validateNativeBodySubtreeWithBindings(treenode.GetNodeInTree(tree, op.ID), true); err != nil {
					return err
				}
			}
			if op.Action == "delete" && tree.ID == op.ID {
				if kernelEvergreen.HasEnvelope(tree.Root) {
					return evergreenUnauthorized("managed document deletion requires a domain command")
				}
			}
		}
		switch op.Action {
		case "setAttrs":
			data, ok := op.Data.(string)
			if !ok {
				return fmt.Errorf("native attribute data is invalid")
			}
			var attrs map[string]string
			if err = json.Unmarshal([]byte(data), &attrs); err != nil {
				return err
			}
			for name := range attrs {
				name = strings.ToLower(name)
				if name == "id" || name == av.NodeAttrNameAvs || name == av.NodeAttrViewNames ||
					strings.HasPrefix(name, av.NodeAttrViewStaticText) || name == NodeAttrRiffDecks {
					return evergreenUnauthorized("native attribute requires a dedicated atomic command: " + name)
				}
			}
		case "update", "insert", "appendInsert", "prependInsert":
			data, ok := op.Data.(string)
			if !ok || data == "" {
				return fmt.Errorf("native block data is invalid")
			}
			input := tx.luteEngine.BlockDOM2Tree(data).Root
			ast.Walk(input, func(node *ast.Node, entering bool) ast.WalkStatus {
				if entering {
					for _, attr := range node.KramdownIAL {
						if len(attr) > 0 && strings.EqualFold(attr[0], kernelEvergreen.CarrierAttribute) {
							err = evergreenUnauthorized("internal Evergreen carrier cannot enter transaction input")
							return ast.WalkStop
						}
					}
				}
				return ast.WalkContinue
			})
			if err != nil {
				return err
			}
			if err = validateNativeBodySubtreeWithBindings(input, true); err != nil {
				return err
			}
		}
	}
	for _, snapshot := range native.snapshots {
		if err = validateNativeBodySubtreeWithBindings(snapshot.tree.Root, true); err != nil {
			return err
		}
	}
	tx.nativeTrees = native
	keep = true
	if tx.isReplay {
		for _, op := range tx.DoOperations {
			for _, file := range op.nativeFileWrites {
				native.files[file.Path] = file
			}
		}
	}
	for id, snapshot := range native.snapshots {
		tx.trees[id] = snapshot.tree
		tx.captureListMindmapSummarySiblings(snapshot.tree)
	}
	for _, snapshot := range native.snapshots {
		if err = tx.validateNativeAVBindings(snapshot.tree); err != nil {
			return err
		}
	}
	return nil
}

func (tx *Transaction) setNativeAVName(operation *Operation) error {
	id := operation.ID
	if !ast.IsNodeIDPattern(id) {
		return fmt.Errorf("invalid native AV identity")
	}
	attrView, err := tx.parseNativeAV(id, operation.BlockID)
	if err != nil {
		return err
	}
	if attrView.Provider != nil {
		return evergreenUnauthorized("managed AV definition requires a domain command")
	}
	name, ok := operation.Data.(string)
	if !ok {
		return fmt.Errorf("invalid native AV name")
	}
	name = strings.ReplaceAll(strings.TrimSpace(name), "\n", " ")
	if utf8.RuneCountInString(name) > 512 {
		name = string([]rune(name)[:512])
	}
	attrView.Name = name
	if err = tx.saveNativeAV(attrView); err != nil {
		return err
	}
	// 绑定行的文档只从事务认证树取值，AV 名称变化不得提前保存任何文档。
	if values := attrView.GetBlockKeyValues(); values != nil {
		for _, value := range values.Values {
			if value == nil || value.IsDetached || value.Block == nil || value.Block.ID == "" {
				continue
			}
			tree, err := tx.loadTree(value.Block.ID)
			if err != nil {
				return err
			}
			node := treenode.GetNodeInTree(tree, value.Block.ID)
			if node == nil {
				return ErrBlockNotFound
			}
			names, err := tx.transactionAVNames(node.IALAttr(av.NodeAttrNameAvs))
			if err != nil {
				return err
			}
			node.SetIALAttr(av.NodeAttrViewNames, names)
			tx.writeTree(tree)
		}
	}
	return nil
}

func (tx *Transaction) registerNativeCreation(tree *parse.Tree) error {
	if tree == nil || tree.Root == nil || tree.ID != tree.Root.ID || IsEncryptedBox(tree.Box) {
		return fmt.Errorf("invalid native document creation")
	}
	if err := validateNativeBodySubtree(tree.Root); err != nil {
		return err
	}
	if kernelEvergreen.HasEnvelope(tree.Root) {
		return evergreenUnauthorized("ordinary creation cannot introduce managed metadata")
	}
	p := tree.Box + "/" + strings.TrimPrefix(tree.Path, "/")
	if err := tx.nativeTrees.session.Allow(p); err != nil {
		return err
	}
	if _, exists := tx.nativeTrees.snapshots[tree.ID]; exists {
		return fmt.Errorf("native document creation identity already exists")
	}
	if _, err := fs.ReadFile(tx.nativeTrees.session.Filesystem(), p); !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("native document creation path is not absent")
	}
	var previous []byte
	if tx.isReplay {
		for _, op := range tx.DoOperations {
			for _, raw := range op.nativeRawHistory {
				var root struct{ ID string }
				if err := json.Unmarshal(raw, &root); err != nil {
					return err
				}
				if root.ID == tree.ID {
					previous = raw
				}
			}
		}
	}
	raw, err := filesys.RenderNativeTree(tree, previous)
	if err != nil {
		return err
	}
	clone, err := filesys.LoadTreeProjectionByData(raw, tree.Box, tree.Path, tx.luteEngine)
	if err != nil {
		return err
	}
	tx.degradeCrossBoundaryBlockRefs(clone.Root, clone.Box)
	tx.nativeTrees.snapshots[tree.ID] = nativeTreeSnapshot{p, previous, clone}
	tx.nativeTrees.created[tree.ID] = true
	tx.writeTree(clone)
	return nil
}

func (tx *Transaction) registerNativeDeletion(tree *parse.Tree) error {
	if kernelEvergreen.HasEnvelope(tree.Root) {
		return evergreenUnauthorized("managed document deletion requires a domain command")
	}
	if err := validateNativeBodySubtree(tree.Root); err != nil {
		return err
	}
	tx.nativeTrees.deleted[tree.ID] = true
	tx.nativeTrees.dirty[tree.ID] = true
	return nil
}

func (tx *Transaction) registerNativeIcon(boxID, icon string) error {
	p := boxID + "/.siyuan/conf.json"
	write, exists := tx.nativeTrees.files[p]
	if !exists {
		raw, err := fs.ReadFile(tx.nativeTrees.session.Filesystem(), p)
		if err != nil {
			return err
		}
		write = core.NativeFileWrite{Path: p, Kind: core.NativeFileNotebookConf, Before: raw, After: raw}
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(write.After, &fields); err != nil {
		return err
	}
	fields["icon"], _ = json.Marshal(icon)
	var err error
	write.After, err = json.Marshal(fields)
	if err != nil {
		return err
	}
	tx.nativeTrees.files[p] = write
	return nil
}

func (tx *Transaction) registerNativeAssets(tree *parse.Tree) error {
	dir := getAssetsDir(filepath.Join(util.DataDir, tree.Box),
		filepath.Dir(filepath.Join(util.DataDir, tree.Box, tree.Path)))
	if dir == filepath.Join(util.DataDir, "assets") {
		return nil
	}
	var result error
	ast.Walk(tree.Root, func(node *ast.Node, entering bool) ast.WalkStatus {
		if !entering {
			return ast.WalkContinue
		}
		source := ""
		if node.Type == ast.NodeLinkDest {
			source = string(node.Tokens)
		} else if node.Type == ast.NodeTextMark {
			source = node.TextMarkAHref
		}
		if !strings.HasPrefix(source, "assets/") {
			return ast.WalkContinue
		}
		if !fs.ValidPath(source) || strings.Contains(source, "\\") {
			result = fmt.Errorf("invalid native asset path")
			return ast.WalkStop
		}
		destination, err := filepath.Rel(util.DataDir, filepath.Join(dir, filepath.Base(source)))
		if err != nil {
			result = err
			return ast.WalkStop
		}
		destination = filepath.ToSlash(destination)
		if _, tracked := tx.nativeTrees.files[destination]; tracked {
			return ast.WalkContinue
		}
		if _, err = fs.ReadFile(tx.nativeTrees.session.Filesystem(), destination); err == nil {
			return ast.WalkContinue
		} else if !errors.Is(err, fs.ErrNotExist) {
			result = err
			return ast.WalkStop
		}
		raw, err := fs.ReadFile(tx.nativeTrees.session.Filesystem(), source)
		if errors.Is(err, fs.ErrNotExist) {
			return ast.WalkContinue
		}
		if err != nil {
			result = err
			return ast.WalkStop
		}
		tx.nativeTrees.files[destination] = core.NativeFileWrite{
			Path: destination, Kind: core.NativeFileAsset, Create: true, After: raw}
		tx.nativeTrees.files[source] = core.NativeFileWrite{
			Path: source, Kind: core.NativeFileAsset, Before: raw, Delete: true}
		return ast.WalkContinue
	})
	return result
}

func (tx *Transaction) validateNativeInsertion(root *ast.Node) error {
	if tx.nativeTrees == nil {
		return nil
	}
	seen := map[string]bool{}
	var err error
	ast.Walk(root, func(node *ast.Node, entering bool) ast.WalkStatus {
		if !entering || node.ID == "" || node.Type == ast.NodeDocument || node.Type == ast.NodeKramdownBlockIAL {
			return ast.WalkContinue
		}
		if seen[node.ID] {
			err = evergreenUnauthorized("native insert contains duplicate physical identities")
			return ast.WalkStop
		}
		seen[node.ID] = true
		for _, tree := range tx.trees {
			if treenode.GetNodeInTree(tree, node.ID) != nil {
				err = evergreenUnauthorized("native insert physical identity already exists")
				return ast.WalkStop
			}
		}
		if bt := treenode.GetBlockTree(node.ID); bt != nil {
			if _, tracked := tx.nativeTrees.snapshots[bt.RootID]; !tracked {
				err = evergreenUnauthorized("native insert physical identity exists outside the transaction")
				return ast.WalkStop
			}
		}
		return ast.WalkContinue
	})
	return err
}

func validateNativeBodySubtree(root *ast.Node) error {
	return validateNativeBodySubtreeWithBindings(root, false)
}

func validateNativeBodySubtreeWithBindings(root *ast.Node, allowBindings bool) error {
	if root == nil {
		return ErrBlockNotFound
	}
	var err error
	ast.Walk(root, func(node *ast.Node, entering bool) ast.WalkStatus {
		if !entering {
			return ast.WalkContinue
		}
		if !allowBindings && (node.Type == ast.NodeAttributeView || node.IALAttr(av.NodeAttrNameAvs) != "") {
			err = evergreenUnauthorized("native AV changes require an atomic auxiliary write-set")
			return ast.WalkStop
		}
		return ast.WalkContinue
	})
	return err
}

func nativeTreeForBlock(native *nativeTreeTransaction, id string) *parse.Tree {
	for _, snapshot := range native.snapshots {
		if treenode.GetNodeInTree(snapshot.tree, id) != nil {
			return snapshot.tree
		}
	}
	return nil
}

func (tx *Transaction) readNativeSnapshot(native *nativeTreeTransaction, bt *treenode.BlockTree) (nativeTreeSnapshot, error) {
	if snapshot, exists := native.snapshots[bt.RootID]; exists {
		if snapshot.path != bt.BoxID+"/"+strings.TrimPrefix(bt.Path, "/") {
			return nativeTreeSnapshot{}, fmt.Errorf("conflicting native document identity")
		}
		return snapshot, nil
	}
	p := bt.BoxID + "/" + strings.TrimPrefix(bt.Path, "/")
	raw, err := fs.ReadFile(native.session.Filesystem(), p)
	if err != nil {
		return nativeTreeSnapshot{}, err
	}
	tree, err := filesys.LoadTreeProjectionByData(raw, bt.BoxID, bt.Path, tx.luteEngine)
	if err != nil {
		return nativeTreeSnapshot{}, err
	}
	if tree.ID != bt.RootID {
		return nativeTreeSnapshot{}, fmt.Errorf("native authority physical identity mismatch")
	}
	if tx.nativeTrees != nil {
		document, decodeErr := core.DecodeSY(raw, core.ApplicationRegistry())
		if decodeErr != nil {
			return nativeTreeSnapshot{}, decodeErr
		}
		if document.ReadOnly {
			return nativeTreeSnapshot{}, &core.DiagnosticError{Diagnostics: document.Diagnostics}
		}
		if document.Envelope != nil && document.Envelope.Entity.EntityType == core.EntitySource {
			return nativeTreeSnapshot{}, evergreenUnauthorized("native Source body is read-only")
		}
		if err = validateNativeBodySubtreeWithBindings(tree.Root, true); err != nil {
			return nativeTreeSnapshot{}, err
		}
		if err = tx.validateNativeAVBindings(tree); err != nil {
			return nativeTreeSnapshot{}, err
		}
	}
	snapshot := nativeTreeSnapshot{p, append([]byte(nil), raw...), tree}
	native.snapshots[tree.ID] = snapshot
	return snapshot, nil
}

// restoreNativeUpdateCarriers 仅向保留物理 ID 的节点复制旧树元数据，输入不能创建 carrier。
func restoreNativeUpdateCarriers(old, next *ast.Node) error {
	pending := map[string]string{}
	attributeViews := map[string]string{}
	ast.Walk(old, func(node *ast.Node, entering bool) ast.WalkStatus {
		if entering && kernelEvergreen.HasEnvelope(node) {
			pending[node.ID] = node.IALAttr(kernelEvergreen.CarrierAttribute)
		}
		if entering && node.Type == ast.NodeAttributeView {
			attributeViews[node.ID] = node.AttributeViewID
		}
		return ast.WalkContinue
	})
	var err error
	seen := map[string]bool{}
	ast.Walk(next, func(node *ast.Node, entering bool) ast.WalkStatus {
		if !entering {
			return ast.WalkContinue
		}
		if node.ID != "" && seen[node.ID] {
			err = evergreenUnauthorized("native update contains duplicate physical identities")
			return ast.WalkStop
		}
		seen[node.ID] = true
		if value := pending[node.ID]; value != "" {
			node.SetIALAttr(kernelEvergreen.CarrierAttribute, value)
			delete(pending, node.ID)
		}
		if node.Type == ast.NodeAttributeView {
			id, exists := attributeViews[node.ID]
			if !exists || id != node.AttributeViewID {
				err = evergreenUnauthorized("native update cannot create or retarget an AV carrier")
				return ast.WalkStop
			}
			delete(attributeViews, node.ID)
		}
		return ast.WalkContinue
	})
	if err != nil {
		return err
	}
	if len(pending) != 0 {
		return evergreenUnauthorized("native update removed managed blocks; use a review-aware command")
	}
	if len(attributeViews) != 0 {
		return evergreenUnauthorized("native update removed an AV carrier; use a dedicated atomic command")
	}
	return nil
}

// collectNativeDynamicRefs 将受影响的引用树登记到同笔事务，禁止提前写盘。
func (tx *Transaction) collectNativeDynamicRefs() error {
	definitions := tx.nodes
	for range 7 {
		changed := map[string]*ast.Node{}
		for _, definition := range definitions {
			tree := tx.trees[treenode.TreeRoot(definition).ID]
			if tree == nil {
				return fmt.Errorf("native reference definition has no authority tree")
			}
			refs, _ := getRefsCacheByDefNode(definition, tree.Box)
			for _, ref := range refs {
				if ref.Type == sql.AttributeViewRefType {
					if err := tx.refreshNativeAVReference(ref, definitions); err != nil {
						return err
					}
					continue
				}
				refTree, err := tx.loadTree(ref.RootID)
				if err != nil {
					return err
				}
				if !IsSameCryptoBoundary(tree.Box, refTree.Box) {
					return evergreenUnauthorized("native reference crosses notebook encryption boundaries")
				}
				refNode := treenode.GetNodeInTree(refTree, ref.BlockID)
				if refNode == nil {
					continue
				}
				if err = validateNativeBodySubtreeWithBindings(refNode, true); err != nil {
					return err
				}
				if updated, _ := updateRefText(refNode, definitions); updated {
					changed[refNode.ID] = refNode
					tx.writeTree(refTree)
				} else if len(tx.crossTreeMoveRefRefreshes) > 0 {
					// 定义块移动后，即使锚文本相同，也需要提交回调重建引用的文档归属。
					tx.writeTree(refTree)
				}
			}
		}
		if len(changed) == 0 {
			break
		}
		definitions = changed
	}
	return nil
}

func (tx *Transaction) commitNativeTrees() error {
	if tx.nativeTrees.avErr != nil {
		return tx.nativeTrees.avErr
	}
	if err := tx.collectNativeDynamicRefs(); err != nil {
		return err
	}
	if err := tx.collectNativeAVChanges(); err != nil {
		return err
	}
	if err := tx.validateNativeAVBindingExpectations(tx.trees, "staged trees"); err != nil {
		return err
	}
	request := core.PlanRequest{Protocol: core.ChangePlanProtocol, OperationID: "op-native-" + ast.NewNodeID()}
	ordinary := []core.NativeDocumentWrite{}
	sources := make([][]byte, 0, len(tx.nativeTrees.snapshots))
	for _, source := range tx.nativeTrees.snapshots {
		if len(source.raw) != 0 {
			sources = append(sources, source.raw)
		}
	}
	history := [][]byte{}
	for _, operations := range [][]*Operation{tx.DoOperations, tx.UndoOperations} {
		for index, operation := range operations {
			if tx.isReplay {
				history = append(history, operation.nativeRawHistory...)
			} else if index == 0 {
				operation.nativeRawHistory = sources
			} else {
				operation.nativeRawHistory = nil
			}
		}
	}
	ids := make([]string, 0, len(tx.nativeTrees.dirty))
	for id := range tx.nativeTrees.dirty {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		snapshot := tx.nativeTrees.snapshots[id]
		if tx.nativeTrees.deleted[id] {
			if !tx.nativeTrees.created[id] {
				ordinary = append(ordinary, core.NativeDocumentWrite{Path: snapshot.path, Before: snapshot.raw, Delete: true})
			}
			continue
		}
		if tx.nativeTrees.created[id] {
			for _, operations := range [][]*Operation{tx.DoOperations, tx.UndoOperations} {
				for _, op := range operations {
					if op.ID == id && op.Tree != nil {
						raw, renderErr := filesys.RenderNativeTree(tx.trees[id], snapshot.raw)
						if renderErr != nil {
							return renderErr
						}
						clone, cloneErr := filesys.LoadTreeProjectionByData(raw, tx.trees[id].Box, tx.trees[id].Path, tx.luteEngine)
						if cloneErr != nil {
							return cloneErr
						}
						op.Tree = clone
					}
				}
			}
		}
		after, finalized := tx.nativeTrees.finalRaw[id]
		if !finalized {
			rendered, renderErr := filesys.RenderNativeTreeWithHistory(tx.trees[id], snapshot.raw, sources, history)
			if renderErr != nil {
				return renderErr
			}
			after = rendered
		}
		rendered, err := filesys.LoadTreeProjectionByData(after, tx.trees[id].Box, tx.trees[id].Path, tx.luteEngine)
		if err != nil {
			return err
		}
		if err = tx.validateNativeAVBindingExpectations(map[string]*parse.Tree{id: rendered}, "rendered trees"); err != nil {
			return err
		}
		if tx.nativeTrees.created[id] {
			ordinary = append(ordinary, core.NativeDocumentWrite{Path: snapshot.path, After: after, Create: true})
			continue
		}
		before, err := core.DecodeSY(snapshot.raw, core.ApplicationRegistry())
		if err != nil {
			return err
		}
		if before.Envelope == nil {
			ordinary = append(ordinary, core.NativeDocumentWrite{Path: snapshot.path, Before: snapshot.raw, After: after})
			continue
		}
		after, err = core.PrepareNativeDocumentEdit(snapshot.raw, after, core.ApplicationRegistry())
		if err != nil {
			return err
		}
		base, err := core.SemanticHash(snapshot.raw)
		if err != nil {
			return err
		}
		logicalID := before.Envelope.Entity.LogicalID
		request.Base = append(request.Base, core.BaseRef{LogicalID: logicalID, SemanticHash: base})
		request.Writes = append(request.Writes, core.WriteInput{LogicalID: logicalID, After: after})
	}
	runtime, err := evergreenRuntimeLocked(tx.nativeTrees.ctx)
	if err != nil {
		return err
	}
	files := make([]core.NativeFileWrite, 0, len(tx.nativeTrees.files))
	for _, file := range tx.nativeTrees.files {
		files = append(files, file)
	}
	if !tx.isReplay {
		inverse := make([]core.NativeFileWrite, 0, len(files))
		for _, file := range files {
			inverse = append(inverse, core.NativeFileWrite{Path: file.Path, Kind: file.Kind,
				Before: file.After, After: file.Before, Create: file.Delete, Delete: file.Create})
		}
		for _, item := range []struct {
			ops   []*Operation
			files []core.NativeFileWrite
		}{{tx.DoOperations, files}, {tx.UndoOperations, inverse}} {
			for index, op := range item.ops {
				op.nativeFileWrites = nil
				if index == 0 {
					op.nativeFileWrites = item.files
				}
			}
		}
	}
	record, err := runtime.ApplyNativeFiles(tx.nativeTrees.ctx, tx.nativeIdentity.principal, request, ordinary, files)
	if err != nil {
		// journal 已准备后的错误仅保留恢复状态，不把旧快照补偿写回权威。
		return err
	}
	tx.mutatedRootIDs = ids
	tx.changedRootIDs = ids
	tx.state.Store(2)
	GlobalUndoLog.Record(tx)
	for _, id := range ids {
		util.PushSaveDoc(id, "tx", []any{tx})
	}
	IncSync()
	tx.releaseCommittedResources()
	tx.m.Unlock()
	if record.DerivedStale {
		util.ReloadUI()
	}
	return nil
}
