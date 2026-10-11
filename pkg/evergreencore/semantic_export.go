package evergreencore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

const CanonicalExportSpec = "evergreen.canonical-export/v1"

type CanonicalEntity struct {
	LogicalID LogicalID       `json:"logical_id"`
	Type      EntityType      `json:"entity_type"`
	Hash      string          `json:"semantic_hash"`
	Document  json.RawMessage `json:"document"`
}

// CanonicalArchive 是只读审阅产物，不参与运行时读取或写入决策。
type CanonicalArchive struct {
	Spec     string            `json:"spec"`
	Entities []CanonicalEntity `json:"entities"`
}

func CanonicalExport(ctx context.Context, repository Repository, registry *Registry) (CanonicalArchive, error) {
	ids, err := repository.List(ctx)
	if err != nil {
		return CanonicalArchive{}, err
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	archive := CanonicalArchive{Spec: CanonicalExportSpec, Entities: []CanonicalEntity{}}
	for _, id := range ids {
		raw, loadErr := repository.Load(ctx, id)
		if loadErr != nil {
			return CanonicalArchive{}, loadErr
		}
		document, decodeErr := DecodeSY(raw, registry)
		if decodeErr != nil {
			return CanonicalArchive{}, decodeErr
		}
		if document.Envelope == nil {
			return CanonicalArchive{}, validationError(CodeSchemaTooNew, string(id), "canonical export cannot interpret this document")
		}
		if document.Envelope.Entity.LogicalID != id {
			continue
		}
		var value any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err = decoder.Decode(&value); err != nil {
			return CanonicalArchive{}, err
		}
		normalizeSemanticValue(value, false)
		canonical, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			return CanonicalArchive{}, marshalErr
		}
		hash, hashErr := SemanticHash(canonical)
		if hashErr != nil {
			return CanonicalArchive{}, hashErr
		}
		archive.Entities = append(archive.Entities, CanonicalEntity{
			LogicalID: id, Type: document.Envelope.Entity.EntityType, Hash: hash, Document: canonical,
		})
	}
	return archive, nil
}

type SemanticChange struct {
	LogicalID LogicalID       `json:"logical_id"`
	Category  string          `json:"category"`
	Path      string          `json:"path"`
	Before    json.RawMessage `json:"before,omitempty"`
	After     json.RawMessage `json:"after,omitempty"`
}

func DiffCanonical(before, after CanonicalArchive) ([]SemanticChange, error) {
	if before.Spec != CanonicalExportSpec || after.Spec != CanonicalExportSpec {
		return nil, validationError(CodeProtocolUnsupported, "spec", "unsupported canonical archive")
	}
	left, err := archiveEntities(before)
	if err != nil {
		return nil, err
	}
	right, err := archiveEntities(after)
	if err != nil {
		return nil, err
	}
	ids := map[LogicalID]bool{}
	for id := range left {
		ids[id] = true
	}
	for id := range right {
		ids[id] = true
	}
	ordered := make([]LogicalID, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	changes := []SemanticChange{}
	for _, id := range ordered {
		l, lok := left[id]
		r, rok := right[id]
		if !lok || !rok {
			changes = append(changes, SemanticChange{LogicalID: id, Category: "entity", Path: "$", Before: l.Document, After: r.Document})
			continue
		}
		var lv, rv any
		ld := json.NewDecoder(bytes.NewReader(l.Document))
		rd := json.NewDecoder(bytes.NewReader(r.Document))
		ld.UseNumber()
		rd.UseNumber()
		if err = ld.Decode(&lv); err != nil {
			return nil, err
		}
		if err = rd.Decode(&rv); err != nil {
			return nil, err
		}
		diffSemanticValue(id, "$", lv, rv, &changes)
	}
	return changes, nil
}

func archiveEntities(archive CanonicalArchive) (map[LogicalID]CanonicalEntity, error) {
	result := map[LogicalID]CanonicalEntity{}
	for _, entity := range archive.Entities {
		if _, found := result[entity.LogicalID]; found {
			return nil, validationError(CodeDuplicateID, string(entity.LogicalID), "duplicate archive entity")
		}
		hash, err := SemanticHash(entity.Document)
		if err != nil || hash != entity.Hash {
			return nil, validationError(CodeBaseMismatch, string(entity.LogicalID), "archive checksum mismatch")
		}
		var value any
		decoder := json.NewDecoder(bytes.NewReader(entity.Document))
		decoder.UseNumber()
		if err = decoder.Decode(&value); err != nil {
			return nil, err
		}
		normalizeSemanticValue(value, false)
		entity.Document, err = json.Marshal(value)
		if err != nil {
			return nil, err
		}
		result[entity.LogicalID] = entity
	}
	return result, nil
}

func diffSemanticValue(id LogicalID, path string, left, right any, changes *[]SemanticChange) {
	lraw, _ := json.Marshal(left)
	rraw, _ := json.Marshal(right)
	if bytes.Equal(lraw, rraw) {
		return
	}
	lmap, lok := left.(map[string]any)
	rmap, rok := right.(map[string]any)
	if lok && rok {
		keys := map[string]bool{}
		for key := range lmap {
			keys[key] = true
		}
		for key := range rmap {
			keys[key] = true
		}
		ordered := make([]string, 0, len(keys))
		for key := range keys {
			ordered = append(ordered, key)
		}
		sort.Strings(ordered)
		for _, key := range ordered {
			diffSemanticValue(id, path+"."+key, lmap[key], rmap[key], changes)
		}
		return
	}
	category := "field"
	switch {
	case containsPath(path, "relations"):
		category = "edge"
	case containsPath(path, "coverage"):
		category = "coverage"
	case containsPath(path, "Children"):
		category = "body"
	}
	*changes = append(*changes, SemanticChange{LogicalID: id, Category: category, Path: path, Before: lraw, After: rraw})
}

func containsPath(path, field string) bool {
	return bytes.Contains([]byte(path), []byte("."+field))
}

// ArchiveDigest 绑定完整归档集合，供迁移 manifest 与重建证据使用。
func ArchiveDigest(archive CanonicalArchive) (string, error) {
	if _, err := archiveEntities(archive); err != nil {
		return "", err
	}
	raw, err := json.Marshal(archive)
	if err != nil {
		return "", fmt.Errorf("canonical archive: %w", err)
	}
	return SemanticHash(raw)
}
