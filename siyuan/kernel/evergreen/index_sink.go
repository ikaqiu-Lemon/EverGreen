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
	"time"

	evergreencore "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	"github.com/siyuan-note/siyuan/kernel/util"
)

type IndexSink struct {
	Root             string
	Refresh          func(context.Context, []evergreencore.LogicalID) error
	RefreshDocuments func(context.Context, []evergreencore.AuthorityDocumentRef) error
}

func (s IndexSink) Invalidate(ctx context.Context, ids []evergreencore.LogicalID) error {
	return s.invalidate(ctx, ids, nil)
}

// InvalidateDocuments 将普通物理文档和受管实体的完整写集交给宿主重建。
func (s IndexSink) InvalidateDocuments(ctx context.Context, refs []evergreencore.AuthorityDocumentRef) error {
	ids := make([]evergreencore.LogicalID, 0, len(refs))
	for _, ref := range refs {
		if ref.LogicalID != "" {
			ids = append(ids, ref.LogicalID)
		}
	}
	return s.invalidate(ctx, ids, refs)
}

func (s IndexSink) invalidate(ctx context.Context, ids []evergreencore.LogicalID, refs []evergreencore.AuthorityDocumentRef) error {
	state := struct {
		UpdatedAt  string                               `json:"updated_at"`
		LogicalIDs []evergreencore.LogicalID            `json:"logical_ids"`
		Documents  []evergreencore.AuthorityDocumentRef `json:"documents,omitempty"`
	}{UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano), LogicalIDs: ids, Documents: refs}
	body, err := json.Marshal(state)
	if err != nil {
		return err
	}
	dir := filepath.Join(s.Root, filepath.FromSlash(evergreencore.RuntimeDirName))
	if err = os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "derived-invalidation.json")
	temp := path + ".tmp"
	if err = os.WriteFile(temp, body, 0o644); err != nil {
		return err
	}
	if err = os.Rename(temp, path); err != nil {
		return err
	}
	if refs != nil && s.RefreshDocuments != nil {
		if err = s.RefreshDocuments(ctx, refs); err != nil {
			return err
		}
	} else if len(refs) != len(ids) && refs != nil {
		return errors.New("native document projection refresh is unavailable")
	} else if s.Refresh != nil {
		if err = s.Refresh(ctx, ids); err != nil {
			return err
		}
	}
	util.BroadcastByType("main", "evergreenAuthorityChanged", 0, "", map[string]any{
		"logicalIDs": ids,
		"documents":  refs,
	})
	return nil
}
