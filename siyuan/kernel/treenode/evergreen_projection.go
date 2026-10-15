// SiYuan - From thought to insight, with agents
// Copyright (c) 2020-present, b3log.org
// SPDX-License-Identifier: AGPL-3.0-or-later

package treenode

import (
	"errors"

	"github.com/88250/lute/ast"
	"github.com/88250/lute/parse"
)

// RemoveAuthorityBlockTree 在权威删除提交后清除映射，数据库错误交由 journal 恢复。
func RemoveAuthorityBlockTree(boxID, rootID string) error {
	if boxID == "" || rootID == "" {
		return errors.New("invalid authority block tree deletion")
	}
	_, err := execForBox(boxID, "DELETE FROM blocktrees WHERE root_id = ? AND box_id = ?", rootID, boxID)
	return err
}

// RefreshAuthorityBlockTree 原子替换一份已认证文档的全部块映射，并向调用方返回索引失败。
func RefreshAuthorityBlockTree(tree *parse.Tree) error {
	if tree == nil || tree.Root == nil || tree.ID == "" || tree.Box == "" {
		return errors.New("invalid authority block tree")
	}
	tx, err := beginTxForBox(tree.Box)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("DELETE FROM blocktrees WHERE root_id = ? AND box_id = ?", tree.ID, tree.Box); err != nil {
		return err
	}
	stmt, err := tx.Prepare("INSERT INTO blocktrees (id, root_id, parent_id, box_id, path, hpath, updated, type) VALUES (?, ?, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()
	ast.Walk(tree.Root, func(node *ast.Node, entering bool) ast.WalkStatus {
		if err != nil {
			return ast.WalkStop
		}
		if !entering || !node.IsBlock() || node.ID == "" {
			return ast.WalkContinue
		}
		parentID := ""
		if node.Parent != nil {
			parentID = node.Parent.ID
		}
		_, err = stmt.Exec(node.ID, tree.ID, parentID, tree.Box, tree.Path, tree.HPath,
			node.IALAttr("updated"), TypeAbbr(node.Type.String()))
		return ast.WalkContinue
	})
	if err != nil {
		return err
	}
	return tx.Commit()
}
