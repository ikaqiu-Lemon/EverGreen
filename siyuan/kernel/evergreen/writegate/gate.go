// SiYuan - From thought to insight, with agents
// Copyright (c) 2020-present, b3log.org
// SPDX-License-Identifier: AGPL-3.0-or-later

package writegate

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"

	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
)

var workspaceGates sync.Map

// ForWorkspace 让同工作区的普通写入和所有认证范围共享写门禁。
func ForWorkspace(root string) (*sync.Mutex, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("workspace root is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, err
	}
	gate, _ := workspaceGates.LoadOrStore(canonical, &sync.Mutex{})
	return gate.(*sync.Mutex), nil
}

// Lock 串行化会触发订正的读取；调用方只在实际写入前检查 journal。
func Lock(root string) (func(), error) {
	gate, err := ForWorkspace(root)
	if err != nil {
		return nil, err
	}
	gate.Lock()
	return gate.Unlock, nil
}

// Check 必须在工作区门禁内调用，且不打开加密笔记本或自动恢复事务。
func Check(ctx context.Context, root string) error {
	authority, err := core.NewFilesystemAuthority(core.FilesystemAuthorityOptions{Root: root})
	if err != nil {
		return err
	}
	return authority.CheckBackgroundWrite(ctx)
}

// Acquire 在任何普通写入副作用前获取门禁，并保留到落盘与缓存更新结束。
func Acquire(ctx context.Context, root string) (func(), error) {
	release, err := Lock(root)
	if err != nil {
		return nil, err
	}
	if err = Check(ctx, root); err != nil {
		release()
		return nil, err
	}
	return release, nil
}
