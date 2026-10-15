// SiYuan - From thought to insight, with agents
// Copyright (c) 2020-present, b3log.org
// SPDX-License-Identifier: AGPL-3.0-or-later

package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/88250/lute/ast"
	"github.com/88250/lute/parse"
	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	"github.com/siyuan-note/siyuan/kernel/cache"
	kernelEvergreen "github.com/siyuan-note/siyuan/kernel/evergreen"
	"github.com/siyuan-note/siyuan/kernel/filesys"
	"github.com/siyuan-note/siyuan/kernel/sql"
	"github.com/siyuan-note/siyuan/kernel/treenode"
	"github.com/siyuan-note/siyuan/kernel/util"
)

// EvergreenRuntime 统一 API 与原生编辑的运行时，并在 authority 提交后刷新实际派生数据。
func EvergreenRuntime(ctx context.Context) (*kernelEvergreen.Runtime, error) {
	flushLock.Lock()
	defer flushLock.Unlock()
	return evergreenRuntimeLocked(ctx)
}

func evergreenRuntimeLocked(ctx context.Context) (*kernelEvergreen.Runtime, error) {
	return kernelEvergreen.DefaultRuntime(ctx, kernelEvergreen.RuntimeOptions{
		Root: util.DataDir, Holder: "siyuan-kernel",
		Storage: filesys.EvergreenDocumentStorage{Root: util.DataDir},
		IndexSink: kernelEvergreen.IndexSink{Root: util.DataDir, Refresh: RefreshEvergreenProjection,
			RefreshDocuments: RefreshEvergreenDocumentProjection},
		InspectDerived: InspectEvergreenDerivedProjection,
		RebuildDerived: RebuildEvergreenDocumentProjection,
	})
}

// ApplyEvergreenOperation 与原生事务遵循 flushLock、runtime gate、authority gate 的锁序。
func ApplyEvergreenOperation(
	ctx context.Context, runtime *kernelEvergreen.Runtime, principal core.Principal, request core.ApplyRequest,
) (core.OperationRecord, error) {
	flushLock.Lock()
	defer flushLock.Unlock()
	return runtime.Apply(ctx, principal, request)
}

// RecoverEvergreenOperations 串行恢复权威，不允许原生事务在恢复过程中使用旧树。
func RecoverEvergreenOperations(ctx context.Context, runtime *kernelEvergreen.Runtime) ([]core.OperationRecord, error) {
	flushLock.Lock()
	defer flushLock.Unlock()
	return runtime.Recover(ctx)
}

// DoctorEvergreenWorkspace serializes journal inspection and derived rebuild
// with native transactions so the before/after parity snapshots see one authority revision.
func DoctorEvergreenWorkspace(
	ctx context.Context,
	runtime *kernelEvergreen.Runtime,
	rebuildIndex bool,
) (core.WorkspaceDoctorReport, error) {
	flushLock.Lock()
	defer flushLock.Unlock()
	return runtime.Doctor(ctx, rebuildIndex)
}

// RefreshEvergreenProjection 不获取原生 flushLock；调用方已经持有 runtime writer gate。
func RefreshEvergreenProjection(ctx context.Context, ids []core.LogicalID) error {
	session, err := (filesys.EvergreenDocumentStorage{Root: util.DataDir}).Open(ctx)
	if err != nil {
		return err
	}
	defer session.Close()
	index, err := core.ScanFS(session.Filesystem(), core.ApplicationRegistry())
	if err != nil {
		return err
	}
	refs := make([]core.AuthorityDocumentRef, 0, len(ids))
	for _, id := range ids {
		location, exists := index.Locations[id]
		if !exists {
			return fmt.Errorf("committed Evergreen entity %q cannot be resolved", id)
		}
		refs = append(refs, core.AuthorityDocumentRef{LogicalID: id, DocumentID: location.DocumentID, Path: location.Path})
	}
	return refreshEvergreenDocumentProjection(session.Filesystem(), refs)
}

// RefreshEvergreenDocumentProjection 在刷新前认证完整写集，普通文档不进入逻辑实体索引。
func RefreshEvergreenDocumentProjection(ctx context.Context, refs []core.AuthorityDocumentRef) error {
	session, err := (filesys.EvergreenDocumentStorage{Root: util.DataDir}).Open(ctx)
	if err != nil {
		return err
	}
	defer session.Close()
	return refreshEvergreenDocumentProjection(session.Filesystem(), refs)
}

func RebuildEvergreenDocumentProjection(ctx context.Context, refs []core.AuthorityDocumentRef) error {
	session, err := (filesys.EvergreenDocumentStorage{
		Root: util.DataDir, Boxes: evergreenDocumentBoxes(refs),
	}).Open(ctx)
	if err != nil {
		return err
	}
	defer session.Close()
	return refreshEvergreenDocumentProjection(session.Filesystem(), refs)
}

type evergreenProjectionIdentity struct {
	Layer      string `json:"layer"`
	BlockID    string `json:"block_id"`
	DocumentID string `json:"document_id"`
	Box        string `json:"box"`
	Path       string `json:"path"`
}

// InspectEvergreenDerivedProjection compares complete SQL and blocktree block
// identity sets with a fresh authenticated parse of the current .sy files.
func InspectEvergreenDerivedProjection(
	ctx context.Context,
	refs []core.AuthorityDocumentRef,
) (core.DerivedProjectionStatus, error) {
	status := core.DerivedProjectionStatus{Available: true, ExpectedDocuments: len(refs)}
	session, err := (filesys.EvergreenDocumentStorage{
		Root: util.DataDir, Boxes: evergreenDocumentBoxes(refs),
	}).Open(ctx)
	if err != nil {
		return status, err
	}
	defer session.Close()
	expected := []evergreenProjectionIdentity{}
	actual := []evergreenProjectionIdentity{}
	for _, ref := range refs {
		if err = session.Allow(ref.Path); err != nil {
			return status, err
		}
		raw, readErr := fs.ReadFile(session.Filesystem(), ref.Path)
		if readErr != nil {
			return status, readErr
		}
		boxID, p, _ := strings.Cut(ref.Path, "/")
		tree, loadErr := filesys.LoadTreeProjectionByData(raw, boxID, "/"+p, util.NewLute())
		if loadErr != nil {
			return status, loadErr
		}
		if tree.ID != ref.DocumentID {
			return status, fmt.Errorf("authority document identity mismatch for %q", ref.Path)
		}
		ast.Walk(tree.Root, func(node *ast.Node, entering bool) ast.WalkStatus {
			if entering && node.IsBlock() && node.ID != "" {
				for _, layer := range []string{"blocktree", "fts", "sql"} {
					expected = append(expected, evergreenProjectionIdentity{
						Layer: layer, BlockID: node.ID, DocumentID: tree.ID,
						Box: tree.Box, Path: tree.Path,
					})
				}
				status.ExpectedBlocks++
			}
			return ast.WalkContinue
		})

		sqlIDs, queryErr := sql.AuthorityProjectionBlockIDs(ctx, tree.Box, tree.ID)
		if queryErr != nil {
			return status, queryErr
		}
		status.ActualBlocks += len(sqlIDs)
		sqlHasRoot := false
		for _, id := range sqlIDs {
			block := sql.GetBlockInBox(id, tree.Box)
			if block == nil {
				return status, fmt.Errorf("SQL projection row %q disappeared during doctor inspection", id)
			}
			actual = append(actual, evergreenProjectionIdentity{
				Layer: "sql", BlockID: block.ID, DocumentID: block.RootID,
				Box: block.Box, Path: block.Path,
			})
			sqlHasRoot = sqlHasRoot || block.ID == tree.ID
		}
		ftsIDs, queryErr := sql.AuthorityProjectionFTSBlockIDs(ctx, tree.Box, tree.ID)
		if queryErr != nil {
			return status, queryErr
		}
		for _, id := range ftsIDs {
			actual = append(actual, evergreenProjectionIdentity{
				Layer: "fts", BlockID: id, DocumentID: tree.ID,
				Box: tree.Box, Path: tree.Path,
			})
		}
		blocktrees := treenode.GetBlockTreesByRootIDInBox(tree.ID, tree.Box)
		treeHasRoot := false
		for _, block := range blocktrees {
			actual = append(actual, evergreenProjectionIdentity{
				Layer: "blocktree", BlockID: block.ID, DocumentID: block.RootID,
				Box: block.BoxID, Path: block.Path,
			})
			treeHasRoot = treeHasRoot || block.ID == tree.ID
		}
		if sqlHasRoot && treeHasRoot {
			status.ActualDocuments++
		}
	}
	status.ExpectedDigest = evergreenProjectionDigest(expected)
	status.ActualDigest = evergreenProjectionDigest(actual)
	status.Healthy = status.ExpectedDigest == status.ActualDigest
	return status, nil
}

func evergreenDocumentBoxes(refs []core.AuthorityDocumentRef) []string {
	seen := map[string]bool{}
	boxes := []string{}
	for _, ref := range refs {
		box, _, found := strings.Cut(ref.Path, "/")
		if found && box != "" && !seen[box] {
			seen[box] = true
			boxes = append(boxes, box)
		}
	}
	sort.Strings(boxes)
	return boxes
}

func evergreenProjectionDigest(items []evergreenProjectionIdentity) string {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Layer != items[j].Layer {
			return items[i].Layer < items[j].Layer
		}
		if items[i].Box != items[j].Box {
			return items[i].Box < items[j].Box
		}
		if items[i].DocumentID != items[j].DocumentID {
			return items[i].DocumentID < items[j].DocumentID
		}
		return items[i].BlockID < items[j].BlockID
	})
	raw, _ := json.Marshal(items)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func refreshEvergreenDocumentProjection(authority fs.FS, refs []core.AuthorityDocumentRef) error {
	type projection struct {
		tree *parse.Tree
		raw  []byte
	}
	projections := []projection{}
	deletions := []core.AuthorityDocumentRef{}
	auxiliary := map[core.AuthorityDocumentRef][]byte{}
	seen := map[string]core.AuthorityDocumentRef{}
	for _, ref := range refs {
		if previous, exists := seen[ref.Path]; exists {
			if previous != ref {
				return fmt.Errorf("conflicting projection identities for %q", ref.Path)
			}
			continue
		}
		seen[ref.Path] = ref
		if !fs.ValidPath(ref.Path) {
			return fmt.Errorf("invalid authority projection path")
		}
		if err := core.ValidateTransactionFilePath(ref.Path); err != nil {
			return err
		}
		raw, err := fs.ReadFile(authority, ref.Path)
		if ref.Delete {
			if !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("deleted authority path still exists or cannot be checked: %s", ref.Path)
			}
			if ref.Auxiliary != "" {
				auxiliary[ref] = nil
			} else {
				if ref.LogicalID != "" || ref.DocumentID != util.GetTreeID(ref.Path) {
					return fmt.Errorf("invalid ordinary authority deletion identity")
				}
				deletions = append(deletions, ref)
			}
			continue
		}
		if err != nil {
			return err
		}
		if ref.Auxiliary != "" {
			if ref.LogicalID != "" || ref.DocumentID != "" {
				return fmt.Errorf("auxiliary projection contains a document identity")
			}
			switch ref.Auxiliary {
			case core.NativeFileAttributeView:
				if !strings.Contains(ref.Path, "storage/av/") || !strings.HasSuffix(ref.Path, ".json") {
					return fmt.Errorf("invalid native AV projection path")
				}
				var object map[string]json.RawMessage
				if err = json.Unmarshal(raw, &object); err != nil {
					return err
				}
				var id string
				if err = json.Unmarshal(object["id"], &id); err != nil || id+".json" != path.Base(ref.Path) {
					return fmt.Errorf("native AV projection identity mismatch")
				}
			case core.NativeFileNotebookConf:
				if !strings.HasSuffix(ref.Path, "/.siyuan/conf.json") || !json.Valid(raw) {
					return fmt.Errorf("invalid notebook configuration projection")
				}
			case core.NativeFileAsset, core.NativeFileAVCache:
			default:
				return fmt.Errorf("unknown auxiliary projection kind")
			}
			auxiliary[ref] = raw
			continue
		}
		document, err := core.DecodeSY(raw, core.ApplicationRegistry())
		if err != nil {
			return err
		}
		if ref.LogicalID != "" {
			if document.Envelope == nil || document.Envelope.Entity.LogicalID != ref.LogicalID {
				return fmt.Errorf("committed logical identity does not match %q", ref.Path)
			}
		} else if document.Envelope != nil {
			return fmt.Errorf("ordinary projection contains a managed entity")
		}
		boxID, p, _ := strings.Cut(ref.Path, "/")
		tree, err := filesys.LoadTreeProjectionByData(raw, boxID, "/"+p, util.NewLute())
		if err != nil {
			return err
		}
		if tree.ID != util.GetTreeID(p) || ref.DocumentID != "" && tree.ID != ref.DocumentID {
			return fmt.Errorf("committed physical identity does not match %q", ref.Path)
		}
		projections = append(projections, projection{tree, raw})
	}
	for _, ref := range deletions {
		boxID, p, _ := strings.Cut(ref.Path, "/")
		cache.RemoveTreeDataInBox(ref.DocumentID, boxID)
		cache.RemoveDocIALInBox("/"+p, boxID)
		for _, block := range treenode.GetBlockTreesByRootIDInBox(ref.DocumentID, boxID) {
			cache.RemoveBlockIALInBox(block.ID, boxID)
			if !IsEncryptedBox(boxID) {
				cache.RemoveBlockIALInBox(block.ID, "")
			}
		}
		if err := sql.RemoveAuthorityTree(boxID, ref.DocumentID, "/"+p); err != nil {
			return err
		}
		if err := treenode.RemoveAuthorityBlockTree(boxID, ref.DocumentID); err != nil {
			return err
		}
		util.PushReloadProtyle(ref.DocumentID)
	}
	for _, item := range projections {
		tree := item.tree
		cache.RemoveTreeDataInBox(tree.ID, tree.Box)
		cache.RemoveDocIALInBox(tree.Path, tree.Box)
		for _, block := range treenode.GetBlockTreesByRootIDInBox(tree.ID, tree.Box) {
			cache.RemoveBlockIALInBox(block.ID, tree.Box)
			if !IsEncryptedBox(tree.Box) {
				cache.RemoveBlockIALInBox(block.ID, "")
			}
		}
		ast.Walk(tree.Root, func(node *ast.Node, entering bool) ast.WalkStatus {
			if entering && node.ID != "" {
				cache.RemoveBlockIALInBox(node.ID, tree.Box)
				if !IsEncryptedBox(tree.Box) {
					cache.RemoveBlockIALInBox(node.ID, "")
				}
			}
			return ast.WalkContinue
		})
		if err := treenode.RefreshAuthorityBlockTree(tree); err != nil {
			return err
		}
	}
	// 全部物理归属先更新，随后生成 SQL 引用，避免引用文档先于移动目标刷新而保留旧 root。
	for _, item := range projections {
		tree := item.tree
		if err := sql.RefreshAuthorityTree(tree); err != nil {
			return err
		}
		cache.SetTreeDataInBox(tree.ID, tree.Box, item.raw)
		refreshDocInfo0(tree, uint64(len(item.raw)))
		util.PushReloadProtyle(tree.ID)
	}
	for ref, raw := range auxiliary {
		switch ref.Auxiliary {
		case core.NativeFileAttributeView:
			id := strings.TrimSuffix(path.Base(ref.Path), ".json")
			cache.RemoveAVData(id)
			ReloadAttrView(id)
		case core.NativeFileNotebookConf:
			var fields struct {
				Icon string `json:"icon"`
			}
			if err := json.Unmarshal(raw, &fields); err != nil {
				return err
			}
			boxID, _, _ := strings.Cut(ref.Path, "/")
			pushNotebookIconChanged(boxID, fields.Icon)
		}
	}
	return nil
}
