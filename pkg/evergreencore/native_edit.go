package evergreencore

import (
	"context"
	"encoding/json"
	"strings"
)

const NativeEditCommand = "native.document.edit"

// PlanNativeEdit 由宿主在验证人工编辑身份后调用，授权凭证不进入请求或计划 JSON。
func (s *AuthorityService) PlanNativeEdit(ctx context.Context, principal Principal, request PlanRequest) (PlannedOperation, error) {
	if principal.Type != PrincipalUser {
		return PlannedOperation{}, validationError(CodeUnauthorized, "principal", "native editing requires a verified user")
	}
	if request.Command != "" && request.Command != NativeEditCommand {
		return PlannedOperation{}, validationError(CodeUnauthorized, "command", "native editing cannot admit another command")
	}
	request.Command = NativeEditCommand
	request.nativeEditor = true
	return s.Plan(ctx, principal, request)
}

// PrepareNativeDocumentEdit 保留受管身份，将正文变化导致的 hash、lineage 和 stale 计算放在同一写集。
func PrepareNativeDocumentEdit(previous, edited []byte, registry *Registry) ([]byte, error) {
	before, err := DecodeSY(previous, registry)
	if err != nil {
		return nil, err
	}
	if before.ReadOnly {
		return nil, &DiagnosticError{Diagnostics: before.Diagnostics}
	}
	if before.Envelope == nil || (before.Envelope.Entity.EntityType != EntityClaim &&
		before.Envelope.Entity.EntityType != EntityNote) {
		return nil, validationError(CodeUnauthorized, "entity", "native editing requires an existing Note or Claim")
	}
	after, err := DecodeSY(edited, registry)
	if err != nil {
		return nil, err
	}
	if after.ReadOnly {
		return nil, &DiagnosticError{Diagnostics: after.Diagnostics}
	}
	if err = authorizeDocumentIdentity(before, after); err != nil {
		return nil, err
	}
	a, err := decodeRawObject(previous)
	if err != nil {
		return nil, err
	}
	b, err := decodeRawObject(edited)
	if err != nil {
		return nil, err
	}
	for _, object := range []RawObject{a, b} {
		delete(object, "Children")
		delete(object, "Properties")
		delete(object, evergreenJSONKey)
	}
	if !semanticValuesEqual(a, b) {
		return nil, validationError(CodeUnauthorized, "document", "native editing must preserve unknown document fields")
	}
	left, err := parseReviewNode(previous)
	if err != nil {
		return nil, err
	}
	right, err := parseReviewNode(edited)
	if err != nil {
		return nil, err
	}
	if err = validateNativeNodeIdentity(right); err != nil {
		return nil, err
	}
	oldNodes, nextNodes := nativeNodeEnvelopes(left), nativeNodeEnvelopes(right)
	if len(oldNodes) != len(nextNodes) {
		return nil, validationError(CodeUnauthorized, "Evergreen", "native editing must preserve managed nodes")
	}
	for id, old := range oldNodes {
		next, exists := nextNodes[id]
		if !exists || !semanticValuesEqual(old, next) {
			return nil, validationError(CodeUnauthorized, "Evergreen", "native editing cannot alter managed metadata")
		}
	}
	result, _, err := RefreshNoteReview(edited, registry)
	if err != nil {
		return nil, err
	}
	document, err := DecodeSY(result, registry)
	if err != nil {
		return nil, err
	}
	document.Envelope.Entity.SemanticRevision = before.Envelope.Entity.SemanticRevision + 1
	return EncodeSY(result, document.Envelope, registry)
}

func validateNativeNodeIdentity(root *reviewNode) error {
	seen := map[string]bool{}
	var walk func(*reviewNode) error
	walk = func(node *reviewNode) error {
		var id string
		_ = json.Unmarshal(node.object["ID"], &id)
		if id != "" {
			if seen[id] {
				return validationError(CodeDuplicateID, "ID", "native tree contains a duplicate physical ID")
			}
			seen[id] = true
		}
		if raw := node.object["Properties"]; raw != nil {
			properties, err := decodeRawObject(raw)
			if err != nil {
				return err
			}
			for key := range properties {
				if strings.EqualFold(key, "custom-eg-internal-envelope") {
					return validationError(CodeUnauthorized, "Properties", "internal carrier cannot be persisted")
				}
			}
		}
		for _, child := range node.children {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(root)
}

func nativeNodeEnvelopes(root *reviewNode) map[string]json.RawMessage {
	result := map[string]json.RawMessage{}
	var walk func(*reviewNode)
	walk = func(node *reviewNode) {
		if raw, exists := node.object[evergreenJSONKey]; exists {
			var id string
			_ = json.Unmarshal(node.object["ID"], &id)
			result[id] = raw
		}
		for _, child := range node.children {
			walk(child)
		}
	}
	walk(root)
	return result
}

func (s *AuthorityService) authorizeNativeEdit(operation PlannedOperation, before, after *SYDocument, create bool) error {
	if !operation.nativeEditor || operation.Plan.Principal.Type != PrincipalUser || create || before == nil {
		return validationError(CodeUnauthorized, "command", "native editing requires trusted host admission and an existing document")
	}
	// apply 在写锁中从权威 before 重算整份 after，恢复也使用相同验证器。
	edited, err := parseReviewNode(after.Raw)
	if err != nil {
		return err
	}
	prior, err := parseReviewNode(before.Raw)
	if err != nil {
		return err
	}
	oldEnvelopes := nativeNodeEnvelopes(prior)
	var restore func(*reviewNode) error
	restore = func(node *reviewNode) error {
		var id string
		_ = json.Unmarshal(node.object["ID"], &id)
		if _, managed := node.object[evergreenJSONKey]; managed {
			raw, exists := oldEnvelopes[id]
			if !exists {
				return validationError(CodeUnauthorized, "Evergreen", "native editing cannot create managed nodes")
			}
			node.object[evergreenJSONKey] = raw
		}
		for _, child := range node.children {
			if err := restore(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err = restore(edited); err != nil {
		return err
	}
	raw, err := edited.encode()
	if err != nil {
		return err
	}
	expected, err := PrepareNativeDocumentEdit(before.Raw, raw, s.registry)
	if err != nil {
		return err
	}
	if !semanticValuesEqual(json.RawMessage(expected), json.RawMessage(after.Raw)) {
		return validationError(CodeUnauthorized, "Evergreen", "native after-image must match deterministic review refresh")
	}
	return nil
}
