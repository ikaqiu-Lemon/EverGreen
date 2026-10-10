package evergreencore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// materializedPayload gives the Claim its own block identity and remaps
// internal references without changing external references or prose.
func materializedPayload(nodes []*reviewNode, documentID string) ([]json.RawMessage, error) {
	if len(documentID) < 14 {
		return nil, validationError(CodeInvalidPlan, "claim_document_id", "Claim document ID requires a timestamp prefix")
	}
	copies := make([]*reviewNode, 0, len(nodes))
	ids := map[string]string{}
	used := map[string]bool{documentID: true}
	var collect func(*reviewNode) error
	collect = func(node *reviewNode) error {
		var oldID string
		_ = json.Unmarshal(node.object["ID"], &oldID)
		if oldID != "" {
			if _, exists := ids[oldID]; exists {
				return validationError(CodeDuplicateID, "candidate.payload.ID", "Candidate payload has duplicate block IDs")
			}
			for salt := 0; ; salt++ {
				hash := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%d", documentID, oldID, salt)))
				newID := documentID[:14] + "-" + hex.EncodeToString(hash[:])[:7]
				if used[newID] || newID == oldID {
					continue
				}
				used[newID] = true
				ids[oldID] = newID
				break
			}
		}
		for _, child := range node.children {
			if err := collect(child); err != nil {
				return err
			}
		}
		return nil
	}
	for _, node := range nodes {
		raw, err := node.encode()
		if err != nil {
			return nil, err
		}
		copied, err := parseReviewNode(raw)
		if err != nil {
			return nil, err
		}
		if err = collect(copied); err != nil {
			return nil, err
		}
		copies = append(copies, copied)
	}
	var remap func(*reviewNode) error
	remap = func(node *reviewNode) error {
		for _, field := range []string{"ID", "NodeBlockRefID", "TextMarkBlockRefID"} {
			var oldID string
			_ = json.Unmarshal(node.object[field], &oldID)
			if newID, exists := ids[oldID]; exists {
				node.object[field], _ = json.Marshal(newID)
			}
		}
		if raw := node.object["Properties"]; raw != nil {
			var properties map[string]json.RawMessage
			if err := json.Unmarshal(raw, &properties); err != nil {
				return err
			}
			var oldID string
			_ = json.Unmarshal(properties["id"], &oldID)
			if newID, exists := ids[oldID]; exists {
				properties["id"], _ = json.Marshal(newID)
				node.object["Properties"], _ = json.Marshal(properties)
			}
		}
		for _, child := range node.children {
			if err := remap(child); err != nil {
				return err
			}
		}
		return nil
	}
	children := make([]json.RawMessage, 0, len(copies))
	for _, child := range copies {
		if err := remap(child); err != nil {
			return nil, err
		}
		raw, err := child.encode()
		if err != nil {
			return nil, err
		}
		children = append(children, raw)
	}
	return children, nil
}
