package evergreencore

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
)

type WorkspaceEntity struct {
	ID       LogicalID         `json:"id"`
	Title    string            `json:"title"`
	Envelope *DocumentEnvelope `json:"envelope"`
	Body     json.RawMessage   `json:"body"`
	Hash     string            `json:"semantic_hash"`
}

// QueryWorkspace 只读取文档权威，块身份和派生缓存不会被误计为实体。
func QueryWorkspace(ctx context.Context, repository Repository, registry *Registry, query string) ([]WorkspaceEntity, error) {
	archive, err := CanonicalExport(ctx, repository, registry)
	if err != nil {
		return nil, err
	}
	items := []WorkspaceEntity{}
	query = strings.ToLower(query)
	for _, entity := range archive.Entities {
		document, err := DecodeSY(entity.Document, registry)
		if err != nil {
			return nil, err
		}
		if query != "" && !strings.Contains(strings.ToLower(string(entity.Document)), query) {
			continue
		}
		root, _ := decodeRawObject(entity.Document)
		items = append(items, WorkspaceEntity{ID: entity.LogicalID, Title: claimTitle(entity.Document, entity.LogicalID),
			Envelope: document.Envelope, Body: nonNilArrayRaw(root["Children"]), Hash: entity.Hash})
	}
	return items, nil
}

type WorkspaceContext struct {
	Entity WorkspaceEntity      `json:"entity"`
	Notes  []WorkspaceEntity    `json:"notes"`
	Claims []WorkspaceEntity    `json:"claims"`
	Review *ReviewSnapshot      `json:"review,omitempty"`
	Base   map[LogicalID]string `json:"base"`
}

func ContextWorkspace(ctx context.Context, repository Repository, registry *Registry, id LogicalID) (WorkspaceContext, error) {
	items, err := QueryWorkspace(ctx, repository, registry, "")
	if err != nil {
		return WorkspaceContext{}, err
	}
	result := WorkspaceContext{Notes: []WorkspaceEntity{}, Claims: []WorkspaceEntity{}, Base: map[LogicalID]string{}}
	for _, item := range items {
		if item.ID == id {
			result.Entity = item
		}
	}
	if result.Entity.ID == "" {
		return result, validationError(CodeInvalidEnvelope, "logical_id", "entity does not exist")
	}
	source := id
	if result.Entity.Envelope.Entity.EntityType == EntityNote {
		var metadata map[string]any
		_ = json.Unmarshal(result.Entity.Envelope.Extra["legacy_metadata"], &metadata)
		if value, ok := metadata["source"].(string); ok {
			source = LogicalID(value)
		}
	}
	result.Base[id] = result.Entity.Hash
	for _, item := range items {
		if item.Envelope.Entity.EntityType == EntityNote {
			var metadata map[string]any
			_ = json.Unmarshal(item.Envelope.Extra["legacy_metadata"], &metadata)
			if metadata["source"] == string(source) {
				result.Notes = append(result.Notes, item)
				result.Base[item.ID] = item.Hash
			}
		}
		for _, provenance := range item.Envelope.Provenance {
			if provenance.SourceID == source || provenance.NoteID == id {
				result.Claims = append(result.Claims, item)
				result.Base[item.ID] = item.Hash
				break
			}
		}
	}
	if result.Entity.Envelope.Review != nil {
		raw, err := repository.Load(ctx, id)
		if err != nil {
			return result, err
		}
		review, err := InspectNoteReview(raw, registry)
		if err != nil {
			return result, err
		}
		result.Review = &review
	}
	return result, nil
}

func RelationsWorkspace(ctx context.Context, repository Repository, registry *Registry, id LogicalID) ([]AVEdgeValue, error) {
	items, err := QueryWorkspace(ctx, repository, registry, "")
	if err != nil {
		return nil, err
	}
	edges := []AVEdgeValue{}
	for _, item := range items {
		for _, edge := range item.Envelope.Relations.Outgoing {
			switch {
			case item.ID == id:
				edges = append(edges, projectEdge(edge, item.ID, item.Hash, EdgeDirectionOutgoing))
			case edge.Target.LogicalID == id:
				edges = append(edges, projectEdge(edge, item.ID, item.Hash, EdgeDirectionIncoming))
			}
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].OwnerID == edges[j].OwnerID {
			return edges[i].ID < edges[j].ID
		}
		return edges[i].OwnerID < edges[j].OwnerID
	})
	return edges, nil
}
