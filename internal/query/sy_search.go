package query

import (
	"context"
	"encoding/json"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
)

// SearchSY 将 Claim 权威投影到共同检索模型，复用既有过滤、打分、排序与分页。
func SearchSY(ctx context.Context, repository core.Repository, registry *core.Registry, request SearchRequest) (*SearchResult, error) {
	if err := ValidateSearchRequest(request); err != nil {
		return nil, err
	}
	if err := request.Page.Validate(); err != nil {
		return nil, err
	}
	items, err := core.QueryWorkspace(ctx, repository, registry, "")
	if err != nil {
		return nil, err
	}
	entries := []CardEntry{}
	for _, item := range items {
		claim := item.Envelope.Claim
		if claim == nil {
			continue
		}
		if request.Kind != SearchKindAll && string(claim.ClaimKind) != string(request.Kind) {
			continue
		}
		entry, err := syCardEntry(item)
		if err != nil {
			return nil, err
		}
		if !request.IncludeDeleted && entry.Deleted {
			continue
		}
		entries = append(entries, entry)
	}
	entries = Filter(entries, FilterSpec{Query: request.Query, Domain: request.Domain, Tags: request.Tags,
		Since: request.Since, Until: request.Until})
	SortEntries(entries)
	hits := []SearchHit{}
	for _, entry := range entries {
		hits = append(hits, SearchHit{ID: entry.ID, Title: entry.Title, Domain: entry.Domain, Tags: entry.Tags,
			Status: entry.Status, Deprecated: entry.Deprecated, CreatedAt: entry.CreatedAt, UpdatedAt: entry.UpdatedAt,
			Path: entry.Path, MatchedFields: entry.MatchedFields, Score: entry.Score, Deleted: entry.Deleted})
	}
	page, paging := ApplyPage(hits, request.Page)
	return &SearchResult{Hits: page, Total: len(hits), ScannedFiles: len(items), Page: paging,
		Diagnostics: withTruncationDiagnostic(nil, paging.Truncated, paging, "命中")}, nil
}

func syMetadata(item core.WorkspaceEntity) map[string]any {
	var metadata map[string]any
	_ = json.Unmarshal(item.Envelope.Extra["legacy_metadata"], &metadata)
	return metadata
}

func syText(metadata map[string]any, key string) string {
	value, _ := metadata[key].(string)
	return value
}

func syCardEntry(item core.WorkspaceEntity) (CardEntry, error) {
	metadata := syMetadata(item)
	body, err := core.MarkdownText(item.Body)
	if err != nil {
		return CardEntry{}, err
	}
	doc, err := mdfile.Parse(body)
	if err != nil {
		return CardEntry{}, err
	}
	claim := item.Envelope.Claim
	return CardEntry{ID: string(item.ID), Title: item.Title, Tags: claim.Tags, Status: claim.Status,
		Deprecated: claim.Status == "deprecated", Domain: syText(metadata, "domain"), CreatedAt: syText(metadata, "created_at"),
		UpdatedAt: syText(metadata, "updated_at"), DeletedAt: syText(metadata, "deleted_at"), Deleted: syText(metadata, "deleted_at") != "",
		Raw: body, Doc: doc, Path: string(item.ID)}, nil
}
