// SiYuan - From thought to insight, with agents
// Copyright (c) 2020-present, b3log.org
// SPDX-License-Identifier: AGPL-3.0-or-later

package filesys

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/88250/lute/ast"
	"github.com/88250/lute/parse"
)

// RenderNativeTree 只渲染事务局部树，保留原始未知字段，不写目录、blocktree 或内容缓存。
// 受管元数据在此保持读取时的值，由 core 在完整 write-set 上统一规划和更新。
func RenderNativeTree(tree *parse.Tree, before []byte) ([]byte, error) {
	return RenderNativeTreeWithSources(tree, before, [][]byte{before})
}

// RenderNativeTreeWithSources 按物理 ID 保留整笔事务的未知节点字段，跨文档移动也不会丢失扩展。
func RenderNativeTreeWithSources(tree *parse.Tree, before []byte, sources [][]byte) ([]byte, error) {
	return RenderNativeTreeWithHistory(tree, before, sources, nil)
}

// RenderNativeTreeWithHistory 对已删除再恢复的节点使用内核撤销快照，现有节点始终以当前权威为准。
func RenderNativeTreeWithHistory(tree *parse.Tree, before []byte, sources, history [][]byte) ([]byte, error) {
	raw, _, err := renderWriteTree(tree, true)
	if err != nil || len(before) == 0 {
		return raw, err
	}
	var previous, next map[string]json.RawMessage
	if err = json.Unmarshal(before, &previous); err != nil {
		return nil, err
	}
	if err = json.Unmarshal(raw, &next); err != nil {
		return nil, err
	}
	oldNodes := map[string]map[string]json.RawMessage{}
	for _, source := range sources {
		if len(source) == 0 {
			continue
		}
		var root map[string]json.RawMessage
		if err = json.Unmarshal(source, &root); err != nil {
			return nil, err
		}
		if err = collectNativeRawNodes(root, oldNodes); err != nil {
			return nil, err
		}
	}
	for _, source := range history {
		var root map[string]json.RawMessage
		if err = json.Unmarshal(source, &root); err != nil {
			return nil, err
		}
		recovered := map[string]map[string]json.RawMessage{}
		if err = collectNativeRawNodes(root, recovered); err != nil {
			return nil, err
		}
		for id, node := range recovered {
			if oldNodes[id] == nil {
				oldNodes[id] = node
			}
		}
	}
	if err = preserveNativeRawNodes(next, previous, oldNodes); err != nil {
		return nil, err
	}
	// 读投影的 SiYuan spec 升级只用于内存兼容，原生正文编辑不迁移物理文档格式。
	if spec, exists := previous["Spec"]; exists {
		next["Spec"] = spec
	} else {
		delete(next, "Spec")
	}
	return json.Marshal(next)
}

func collectNativeRawNodes(node map[string]json.RawMessage, nodes map[string]map[string]json.RawMessage) error {
	var id string
	if err := json.Unmarshal(node["ID"], &id); node["ID"] != nil && err != nil {
		return err
	}
	if id != "" {
		if nodes[id] != nil {
			return fmt.Errorf("duplicate physical identity in native before-image")
		}
		nodes[id] = node
	}
	var children []map[string]json.RawMessage
	if err := json.Unmarshal(node["Children"], &children); node["Children"] != nil && err != nil {
		return err
	}
	for _, child := range children {
		if err := collectNativeRawNodes(child, nodes); err != nil {
			return err
		}
	}
	return nil
}

func preserveNativeRawNodes(node, positional map[string]json.RawMessage, oldNodes map[string]map[string]json.RawMessage) error {
	var id string
	_ = json.Unmarshal(node["ID"], &id)
	previous := oldNodes[id]
	if id == "" && string(node["Type"]) == string(positional["Type"]) {
		previous = positional
	}
	if previous != nil {
		if err := preserveUnknownJSONFields(node, previous, reflect.TypeOf(ast.Node{})); err != nil {
			return err
		}
	}
	var children, oldChildren []map[string]json.RawMessage
	if err := json.Unmarshal(node["Children"], &children); node["Children"] != nil && err != nil {
		return err
	}
	if previous != nil && previous["Children"] != nil {
		if err := json.Unmarshal(previous["Children"], &oldChildren); err != nil {
			return err
		}
	}
	for index, child := range children {
		var oldChild map[string]json.RawMessage
		if index < len(oldChildren) {
			oldChild = oldChildren[index]
		}
		if err := preserveNativeRawNodes(child, oldChild, oldNodes); err != nil {
			return err
		}
	}
	if node["Children"] != nil {
		raw, err := json.Marshal(children)
		if err != nil {
			return err
		}
		node["Children"] = raw
	}
	return nil
}

func preserveUnknownJSONFields(next, previous map[string]json.RawMessage, schema reflect.Type) error {
	fields := map[string]reflect.Type{}
	for index := 0; index < schema.NumField(); index++ {
		field := schema.Field(index)
		if field.PkgPath != "" {
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		fields[name] = field.Type
	}
	for key, raw := range previous {
		field, known := fields[key]
		if !known {
			// Evergreen 必须来自已附着的可信 carrier，不能用旧值掩盖丢失或篡改。
			if key != "Evergreen" {
				next[key] = append(json.RawMessage(nil), raw...)
			}
			continue
		}
		for field.Kind() == reflect.Pointer {
			field = field.Elem()
		}
		if field.Kind() != reflect.Struct || next[key] == nil || string(raw) == "null" || string(next[key]) == "null" {
			continue
		}
		var a, b map[string]json.RawMessage
		if err := json.Unmarshal(raw, &a); err != nil {
			return err
		}
		if err := json.Unmarshal(next[key], &b); err != nil {
			return err
		}
		if err := preserveUnknownJSONFields(b, a, field); err != nil {
			return err
		}
		encoded, err := json.Marshal(b)
		if err != nil {
			return err
		}
		next[key] = encoded
	}
	return nil
}
