// SiYuan - From thought to insight, with agents
// Copyright (c) 2020-present, b3log.org
// SPDX-License-Identifier: AGPL-3.0-or-later

package filesys

import (
	"encoding/json"
	"errors"
	"os"

	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	"github.com/siyuan-note/filelock"
	kernelEvergreen "github.com/siyuan-note/siyuan/kernel/evergreen"
)

// guardEvergreenBackgroundWrite 对磁盘权威与待写树双向判定；受管语义变化只能走原生事务或 core。
func guardEvergreenBackgroundWrite(filePath, boxID, p string, after, dek []byte) (managed bool, err error) {
	stored, err := filelock.ReadFile(filePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	var before []byte
	if err == nil {
		before, err = decryptDataWithDEK(boxID, p, stored, dek)
		if err != nil {
			return false, err
		}
	}
	for _, raw := range [][]byte{before, after} {
		if len(raw) == 0 {
			continue
		}
		present, decodeErr := containsEvergreenEnvelope(raw)
		if decodeErr != nil {
			return false, decodeErr
		}
		managed = managed || present
	}
	if !managed {
		return false, nil
	}
	deny := func() (bool, error) {
		return true, &core.DiagnosticError{Diagnostics: []core.Diagnostic{{
			Code: core.CodeUnauthorized, Level: "error", Path: "document",
			Message: "managed document changes require a trusted journaled transaction",
		}}}
	}
	if len(before) == 0 {
		return deny()
	}
	for _, raw := range [][]byte{before, after} {
		document, decodeErr := kernelEvergreen.NewHost(nil).Decode(raw)
		if decodeErr != nil {
			return true, decodeErr
		}
		if document.ReadOnly {
			return true, &core.DiagnosticError{Diagnostics: document.Diagnostics}
		}
	}
	beforeHash, err := core.SemanticHash(before)
	if err != nil {
		return true, err
	}
	afterHash, err := core.SemanticHash(after)
	if err != nil {
		return true, err
	}
	if beforeHash != afterHash {
		return deny()
	}
	// 等价的渲染无需重写权威或重新生成密文 nonce。
	return true, nil
}

func containsEvergreenEnvelope(raw []byte) (bool, error) {
	var node map[string]json.RawMessage
	if err := json.Unmarshal(raw, &node); err != nil {
		return false, err
	}
	if _, exists := node["Evergreen"]; exists {
		return true, nil
	}
	if raw := node["Children"]; raw != nil {
		var children []json.RawMessage
		if err := json.Unmarshal(raw, &children); err != nil {
			return false, err
		}
		for _, child := range children {
			present, err := containsEvergreenEnvelope(child)
			if err != nil || present {
				return present, err
			}
		}
	}
	return false, nil
}
