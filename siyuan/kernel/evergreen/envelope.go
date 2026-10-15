// SiYuan - From thought to insight, with agents
// Copyright (c) 2020-present, b3log.org
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Affero General Public License for more details.

// Package evergreen bridges SiYuan's Lute AST with the shared Evergreen
// semantic core. The carrier attribute exists only in memory and is removed
// before JSON rendering; persisted .sy files contain native Evergreen fields.
package evergreen

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/88250/lute/ast"
	evergreencore "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
)

const CarrierAttribute = "custom-eg-internal-envelope"

type Captured struct {
	Envelopes map[string]json.RawMessage
}

type Host struct {
	registry *evergreencore.Registry
}

func NewHost(registry *evergreencore.Registry) *Host {
	if registry == nil {
		registry = installedKindRegistry()
	}
	return &Host{registry: registry}
}

func (h *Host) Decode(data []byte) (*evergreencore.SYDocument, error) {
	return evergreencore.DecodeSY(data, h.registry)
}

func (h *Host) SetDocument(root *ast.Node, envelope *evergreencore.DocumentEnvelope) error {
	if err := evergreencore.ValidateDocument(envelope, h.registry); err != nil {
		return err
	}
	raw, err := evergreencore.MarshalDocumentEnvelope(envelope)
	if err != nil {
		return err
	}
	return setEnvelope(root, raw)
}

func (h *Host) Document(root *ast.Node) (*evergreencore.DocumentEnvelope, bool, error) {
	raw, ok, err := envelope(root)
	if err != nil || !ok {
		return nil, ok, err
	}
	decoded, err := evergreencore.UnmarshalDocumentEnvelope(raw)
	if err != nil {
		return nil, true, err
	}
	if err = evergreencore.ValidateDocument(decoded, h.registry); err != nil {
		return nil, true, err
	}
	return decoded, true, nil
}

func (h *Host) SetBlock(node *ast.Node, envelope *evergreencore.BlockEnvelope) error {
	if err := evergreencore.ValidateBlock(envelope, h.registry); err != nil {
		return err
	}
	raw, err := evergreencore.MarshalBlockEnvelope(envelope)
	if err != nil {
		return err
	}
	return setEnvelope(node, raw)
}

func (h *Host) Block(node *ast.Node) (*evergreencore.BlockEnvelope, bool, error) {
	raw, ok, err := envelope(node)
	if err != nil || !ok {
		return nil, ok, err
	}
	decoded, err := evergreencore.UnmarshalBlockEnvelope(raw)
	if err != nil {
		return nil, true, err
	}
	if err = evergreencore.ValidateBlock(decoded, h.registry); err != nil {
		return nil, true, err
	}
	return decoded, true, nil
}

func Capture(data []byte) (Captured, error) {
	if !bytes.Contains(data, []byte(`"Evergreen"`)) {
		return Captured{Envelopes: map[string]json.RawMessage{}}, nil
	}
	if _, err := evergreencore.DecodeSY(data, installedKindRegistry()); err != nil {
		return Captured{}, err
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return Captured{}, err
	}
	captured := Captured{Envelopes: map[string]json.RawMessage{}}
	if err := captureNode(root, &captured); err != nil {
		return Captured{}, err
	}
	var rootID string
	if err := json.Unmarshal(root["ID"], &rootID); err != nil {
		return Captured{}, err
	}
	if len(captured.Envelopes) > 0 {
		if _, managedRoot := captured.Envelopes[rootID]; !managedRoot {
			return Captured{}, errors.New("managed Evergreen block requires a managed document envelope")
		}
	}
	return captured, nil
}

func captureNode(node map[string]json.RawMessage, captured *Captured) error {
	var id string
	if rawID, ok := node["ID"]; ok {
		if err := json.Unmarshal(rawID, &id); err != nil {
			return err
		}
	}
	if raw, ok := node["Evergreen"]; ok {
		if id == "" {
			return errors.New("managed Evergreen node has no physical ID")
		}
		if _, duplicate := captured.Envelopes[id]; duplicate {
			return fmt.Errorf("duplicate managed physical ID %q", id)
		}
		if !json.Valid(raw) {
			return fmt.Errorf("Evergreen envelope on %q is invalid JSON", id)
		}
		captured.Envelopes[id] = append(json.RawMessage(nil), raw...)
	}
	if rawChildren, ok := node["Children"]; ok {
		var children []map[string]json.RawMessage
		if err := json.Unmarshal(rawChildren, &children); err != nil {
			return err
		}
		for _, child := range children {
			if err := captureNode(child, captured); err != nil {
				return err
			}
		}
	}
	return nil
}

func Attach(root *ast.Node, captured Captured) error {
	if root == nil {
		return errors.New("cannot attach Evergreen envelopes to a nil AST")
	}
	pending := cloneEnvelopes(captured.Envelopes)
	var attachErr error
	ast.Walk(root, func(node *ast.Node, entering bool) ast.WalkStatus {
		if !entering || attachErr != nil {
			return ast.WalkContinue
		}
		raw, ok := pending[node.ID]
		if !ok {
			return ast.WalkContinue
		}
		attachErr = setEnvelope(node, raw)
		delete(pending, node.ID)
		return ast.WalkContinue
	})
	if attachErr != nil {
		return attachErr
	}
	if len(pending) > 0 {
		return fmt.Errorf("managed Evergreen nodes were dropped by AST parsing: %v", sortedEnvelopeIDs(pending))
	}
	return nil
}

func Detach(root *ast.Node) (Captured, func(), error) {
	if root == nil {
		return Captured{}, func() {}, errors.New("cannot detach Evergreen envelopes from a nil AST")
	}
	if !HasEnvelope(root) {
		return Captured{Envelopes: map[string]json.RawMessage{}}, func() {}, nil
	}
	captured := Captured{Envelopes: map[string]json.RawMessage{}}
	type saved struct {
		node  *ast.Node
		value string
	}
	var values []saved
	var detachErr error
	ast.Walk(root, func(node *ast.Node, entering bool) ast.WalkStatus {
		if !entering || detachErr != nil {
			return ast.WalkContinue
		}
		value := node.IALAttr(CarrierAttribute)
		if value == "" {
			return ast.WalkContinue
		}
		raw, err := decodeCarrier(value)
		if err != nil {
			detachErr = fmt.Errorf("decode Evergreen carrier on %q: %w", node.ID, err)
			return ast.WalkContinue
		}
		if _, duplicate := captured.Envelopes[node.ID]; duplicate {
			detachErr = fmt.Errorf("duplicate managed AST node ID %q", node.ID)
			return ast.WalkContinue
		}
		captured.Envelopes[node.ID] = raw
		values = append(values, saved{node: node, value: value})
		node.RemoveIALAttr(CarrierAttribute)
		return ast.WalkContinue
	})
	var once sync.Once
	restore := func() {
		once.Do(func() {
			for _, value := range values {
				value.node.SetIALAttr(CarrierAttribute, value.value)
			}
		})
	}
	if detachErr != nil {
		restore()
		return Captured{}, func() {}, detachErr
	}
	return captured, restore, nil
}

func Inject(data []byte, captured Captured) ([]byte, error) {
	return inject(data, captured, true)
}

// InjectNative 保留事务读取的受管元数据，审阅状态由 core 在整笔提交时统一更新。
func InjectNative(data []byte, captured Captured) ([]byte, error) {
	return inject(data, captured, false)
}

func inject(data []byte, captured Captured, refreshReview bool) ([]byte, error) {
	if len(captured.Envelopes) == 0 {
		return data, nil
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	pending := cloneEnvelopes(captured.Envelopes)
	if err := injectNode(root, pending); err != nil {
		return nil, err
	}
	if len(pending) > 0 {
		return nil, fmt.Errorf("rendered AST dropped managed Evergreen nodes: %v", sortedEnvelopeIDs(pending))
	}
	injected, err := json.Marshal(root)
	if err != nil {
		return nil, err
	}
	if refreshReview {
		injected, _, err = evergreencore.RefreshNoteReview(injected, installedKindRegistry())
		if err != nil {
			return nil, err
		}
	}
	document, err := evergreencore.DecodeSY(injected, installedKindRegistry())
	if err != nil {
		return nil, err
	}
	if document.ReadOnly {
		return nil, &evergreencore.DiagnosticError{Diagnostics: document.Diagnostics}
	}
	return injected, nil
}

func injectNode(node map[string]json.RawMessage, pending map[string]json.RawMessage) error {
	var id string
	if rawID, ok := node["ID"]; ok {
		if err := json.Unmarshal(rawID, &id); err != nil {
			return err
		}
	}
	if raw, ok := pending[id]; ok {
		node["Evergreen"] = append(json.RawMessage(nil), raw...)
		delete(pending, id)
	}
	if rawChildren, ok := node["Children"]; ok {
		var children []map[string]json.RawMessage
		if err := json.Unmarshal(rawChildren, &children); err != nil {
			return err
		}
		for _, child := range children {
			if err := injectNode(child, pending); err != nil {
				return err
			}
		}
		encoded, err := json.Marshal(children)
		if err != nil {
			return err
		}
		node["Children"] = encoded
	}
	return nil
}

func HasEnvelope(node *ast.Node) bool {
	return node != nil && node.IALAttr(CarrierAttribute) != ""
}

func envelope(node *ast.Node) (json.RawMessage, bool, error) {
	if node == nil {
		return nil, false, nil
	}
	value := node.IALAttr(CarrierAttribute)
	if value == "" {
		return nil, false, nil
	}
	raw, err := decodeCarrier(value)
	return raw, true, err
}

func setEnvelope(node *ast.Node, raw json.RawMessage) error {
	if node == nil || node.ID == "" {
		return errors.New("managed Evergreen AST node requires a physical ID")
	}
	if !json.Valid(raw) {
		return errors.New("Evergreen envelope is invalid JSON")
	}
	node.SetIALAttr(CarrierAttribute, base64.RawStdEncoding.EncodeToString(raw))
	return nil
}

func decodeCarrier(value string) (json.RawMessage, error) {
	decoded, err := base64.RawStdEncoding.DecodeString(value)
	if err != nil {
		return nil, err
	}
	if !json.Valid(decoded) {
		return nil, errors.New("Evergreen carrier payload is invalid JSON")
	}
	return json.RawMessage(decoded), nil
}

func cloneEnvelopes(source map[string]json.RawMessage) map[string]json.RawMessage {
	cloned := make(map[string]json.RawMessage, len(source))
	for id, raw := range source {
		cloned[id] = append(json.RawMessage(nil), raw...)
	}
	return cloned
}

func sortedEnvelopeIDs(envelopes map[string]json.RawMessage) []string {
	ids := make([]string, 0, len(envelopes))
	for id := range envelopes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
