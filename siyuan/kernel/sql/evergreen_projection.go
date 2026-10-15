// SiYuan - From thought to insight, with agents
// Copyright (c) 2020-present, b3log.org
// SPDX-License-Identifier: AGPL-3.0-or-later

package sql

import (
	"context"
	"errors"
	"sort"

	"github.com/88250/lute/ast"
	"github.com/88250/lute/parse"
	"github.com/siyuan-note/eventbus"
	"github.com/siyuan-note/siyuan/kernel/util"
)

// AuthorityProjectionBlockIDs returns the complete persisted SQL projection
// for one authority document, including stale extra rows.
func AuthorityProjectionBlockIDs(ctx context.Context, boxID, rootID string) ([]string, error) {
	return authorityProjectionIDs(ctx, boxID,
		"SELECT id FROM blocks WHERE root_id = ? ORDER BY id", rootID)
}

// AuthorityProjectionFTSBlockIDs exposes the complete FTS membership for the
// same document so doctor can detect missing or stale search rows.
func AuthorityProjectionFTSBlockIDs(ctx context.Context, boxID, rootID string) ([]string, error) {
	return authorityProjectionIDs(ctx, boxID,
		"SELECT id FROM blocks_fts WHERE root_id = ? ORDER BY id", rootID)
}

func authorityProjectionIDs(ctx context.Context, boxID, statement, rootID string) ([]string, error) {
	rows, err := queryForBoxContext(ctx, boxID,
		statement, rootID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	sort.Strings(ids)
	return ids, nil
}

// RemoveAuthorityTree 同步清除已提交删除的 SQL/FTS 和引用缓存，失败保留可恢复状态。
func RemoveAuthorityTree(boxID, rootID, path string) error {
	if boxID == "" || rootID == "" {
		return errors.New("invalid authority index deletion")
	}
	initDatabaseLock.Lock()
	defer initDatabaseLock.Unlock()
	tx, err := beginTxForBox(boxID)
	if err != nil {
		return err
	}
	defer func() {
		tx.Rollback()
		closeTxPreparedStmts(tx)
	}()
	for id := range queryBlockHashes(tx, rootID) {
		removeBlockCache(id)
	}
	removeRefCacheByPath(boxID, path)
	if err = deleteByRootID(tx, rootID, map[string]any{eventbus.CtxPushMsg: eventbus.CtxPushMsgToStatusBar}); err != nil {
		return err
	}
	if err = commitTx(tx); err != nil {
		return err
	}
	eventbus.Publish(eventbus.EvtEmbeddingDirty, rootID)
	return nil
}

// RefreshAuthorityTree 同步刷新提交后正文的 SQL/FTS，失败由 journal 标记 stale 后重试。
func RefreshAuthorityTree(tree *parse.Tree) error {
	if tree == nil || tree.Root == nil {
		return errors.New("invalid authority index tree")
	}
	initDatabaseLock.Lock()
	defer initDatabaseLock.Unlock()
	tx, err := beginTxForBox(tree.Box)
	if err != nil {
		return err
	}
	defer func() {
		tx.Rollback()
		closeTxPreparedStmts(tx)
		ast.Walk(tree.Root, func(node *ast.Node, entering bool) ast.WalkStatus {
			if entering && node.ID != "" {
				removeBlockCache(node.ID)
			}
			return ast.WalkContinue
		})
	}()
	// 清理新旧块的查询缓存，SQL 失败时也不能继续返回已提交前的内容。
	for id := range queryBlockHashes(tx, tree.ID) {
		removeBlockCache(id)
	}
	ast.Walk(tree.Root, func(node *ast.Node, entering bool) ast.WalkStatus {
		if entering && node.ID != "" {
			removeBlockCache(node.ID)
		}
		return ast.WalkContinue
	})
	removeRefCacheByPath(tree.Box, tree.Path)
	if isIndexIgnored(tree) {
		return nil
	}
	context := map[string]any{eventbus.CtxPushMsg: eventbus.CtxPushMsgToStatusBar}
	if err = deleteByRootID(tx, tree.ID, context); err != nil {
		return err
	}
	if err = indexTree(tx, tree, context); err != nil {
		return err
	}
	if err = commitTx(tx); err != nil {
		return err
	}
	eventbus.Publish(eventbus.EvtEmbeddingDirty, tree.ID)
	util.BroadcastByType("main", "databaseIndexCommit", 0, "", map[string]any{"rootIDs": []string{tree.ID}})
	return nil
}
