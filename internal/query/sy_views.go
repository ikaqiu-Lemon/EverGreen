package query

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
)

// ContextSY 使用共同候选评分规则；逻辑身份和 semantic base 来自 .sy 权威。
func ContextSY(ctx context.Context, repository core.Repository, registry *core.Registry, id core.LogicalID, domain string) (*Context, error) {
	items, err := core.QueryWorkspace(ctx, repository, registry, "")
	if err != nil {
		return nil, err
	}
	result := &Context{Domain: domain, Notes: []NoteView{}, NoteSegmentations: []NoteSegmentationView{},
		Cards: []CardView{}, DraftCandidates: []DraftCandidate{}, KnowledgeCandidates: []Candidate{},
		OpinionCandidates: []Candidate{}, Proposals: []ProposalSummary{}, Base: map[string]string{}}
	sourceID := string(id)
	found := false
	for _, item := range items {
		if item.ID == id {
			found = true
			if item.Envelope.Entity.EntityType == core.EntityNote {
				sourceID = syText(syMetadata(item), "source")
				if domain == "" {
					result.Domain = syText(syMetadata(item), "domain")
				}
			}
		}
	}
	if !found {
		return nil, fmt.Errorf("%w: %s", ErrTargetNotFound, id)
	}
	var knowledge, opinions []CardEntry
	for _, item := range items {
		metadata := syMetadata(item)
		if string(item.ID) == sourceID && item.Envelope.Entity.EntityType == core.EntitySource {
			body, err := core.MarkdownText(item.Body)
			if err != nil {
				return nil, err
			}
			result.Source = &SourceView{ID: sourceID, Path: sourceID, Title: item.Title,
				URL: syText(metadata, "url"), SavedAt: syText(metadata, "saved_at"), Body: string(body)}
			result.Base[sourceID] = item.Hash
		}
		if result.Domain != "" && syText(metadata, "domain") != result.Domain {
			continue
		}
		if item.Envelope.Entity.EntityType == core.EntityNote && syText(metadata, "source") == sourceID {
			result.Notes = append(result.Notes, NoteView{ID: string(item.ID), Path: string(item.ID), Source: sourceID})
			result.Base[string(item.ID)] = item.Hash
			if item.Envelope.Review != nil {
				raw, err := repository.Load(ctx, item.ID)
				if err != nil {
					return nil, err
				}
				review, err := core.InspectNoteReview(raw, registry)
				if err != nil {
					return nil, err
				}
				for _, candidate := range review.Candidates {
					var key string
					_ = json.Unmarshal(candidate.Metadata.Extra["legacy_key"], &key)
					if key == "" {
						key = string(candidate.CandidateID)
					}
					status := "unmaterialized"
					if candidate.Metadata.MaterializedClaimID != "" {
						status = "materialized"
					}
					result.DraftCandidates = append(result.DraftCandidates, DraftCandidate{
						Note: string(item.ID), Path: string(item.ID), Key: key, Kind: string(candidate.Metadata.ClaimKind),
						LogicalSlug: candidate.Metadata.LogicalSlug, Title: candidate.Metadata.Title, Status: status,
						Output: string(candidate.Metadata.MaterializedClaimID), Stale: candidate.Stale,
						PayloadHash: candidate.Metadata.PayloadHash})
				}
			}
		}
		if item.Envelope.Claim != nil {
			entry, err := syCardEntry(item)
			if err != nil {
				return nil, err
			}
			switch item.Envelope.Claim.ClaimKind {
			case core.KnowledgeKind:
				knowledge = append(knowledge, entry)
				if !entry.Deprecated && !entry.Deleted {
					result.Cards = append(result.Cards, CardView{ID: entry.ID, Path: entry.Path, Title: entry.Title, Tags: entry.Tags})
				}
			case core.OpinionKind:
				opinions = append(opinions, entry)
			}
		}
	}
	if result.Source == nil {
		return nil, fmt.Errorf("%w: %s", ErrTargetNotFound, sourceID)
	}
	result.KnowledgeCandidates = candidates(result.Source.Title, knowledge)
	result.OpinionCandidates = candidates(result.Source.Title, opinions)
	for _, candidate := range append(append([]Candidate{}, result.KnowledgeCandidates...), result.OpinionCandidates...) {
		for _, item := range items {
			if string(item.ID) == candidate.ID {
				result.Base[candidate.ID] = item.Hash
			}
		}
	}
	return result, nil
}

// RelSY 沿用论证/替代视图、排序、对端可见性与全局分页。
func RelSY(ctx context.Context, repository core.Repository, registry *core.Registry, request RelRequest) (*RelResult, error) {
	if err := request.Page.Validate(); err != nil {
		return nil, err
	}
	items, err := core.QueryWorkspace(ctx, repository, registry, "")
	if err != nil {
		return nil, err
	}
	out, in := []RelationEdge{}, []RelationEdge{}
	universe := []CardEntry{}
	found := false
	schema := core.ArgumentSchema
	if request.ReplacedBy {
		schema = core.ReplacementSchema
	}
	for _, item := range items {
		if item.Envelope.Claim == nil {
			continue
		}
		entry, err := syCardEntry(item)
		if err != nil {
			return nil, err
		}
		universe = append(universe, entry)
		found = found || string(item.ID) == string(request.ID)
		for _, edge := range item.Envelope.Relations.Outgoing {
			if edge.Schema != schema {
				continue
			}
			projected := RelationEdge{From: string(item.ID), Target: string(edge.Target.LogicalID),
				Type: edge.Type, Reason: edge.Reason, Path: string(item.ID)}
			if string(item.ID) == string(request.ID) {
				out = append(out, projected)
			}
			if edge.Target.LogicalID == core.LogicalID(request.ID) {
				in = append(in, projected)
			}
		}
	}
	if !found {
		return nil, fmt.Errorf("%w: %s", ErrEndpointNotFound, request.ID)
	}
	if request.To != "" {
		out = filterEdges(out, func(edge RelationEdge) bool { return edge.Target == request.To })
		in = filterEdges(in, func(edge RelationEdge) bool { return edge.From == request.To })
	}
	SortEdges(out, func(edge RelationEdge) string { return edge.Target })
	SortEdges(in, func(edge RelationEdge) string { return edge.From })
	policy := VisibilityPolicy{IncludeDeprecated: request.IncludeDeprecated}
	out, hiddenOut := VisibleEndpoints(universe, out, func(edge RelationEdge) string { return edge.Target }, policy)
	in, hiddenIn := VisibleEndpoints(universe, in, func(edge RelationEdge) string { return edge.From }, policy)
	diagnostics := []Diagnostic{}
	if request.To != "" && !hasEndpoint(universe, request.To) {
		out, in = []RelationEdge{}, []RelationEdge{}
		diagnostics = append(diagnostics, newQ2(string(request.ID), "--to target %s does not exist", request.To))
	}
	out, in, page := ApplyPagePair(out, in, request.Page)
	return &RelResult{Data: RelData{ID: string(request.ID), RelationsOut: out, RelationsIn: in, ScannedFiles: len(items)},
		Page: page, HiddenDeprecated: hiddenOut + hiddenIn,
		Diagnostics: withTruncationDiagnostic(withDeprecatedHiddenDiagnostic(diagnostics,
			hiddenOut+hiddenIn, request.IncludeDeprecated), page.Truncated, page, "关系条目")}, nil
}

func SYSourceRefs(envelope *core.DocumentEnvelope) []model.SourceRef {
	sources := []model.SourceRef{}
	for _, edge := range envelope.Relations.Outgoing {
		if edge.Schema == core.MaterialSchema {
			sources = append(sources, model.SourceRef{Source: model.SourceID(edge.Target.LogicalID),
				Note: model.NoteID(edge.Context.NoteID), Rel: model.MaterialRel(edge.Type), Reason: edge.Reason})
		}
	}
	return sources
}

func ClaimDetailSY(item core.WorkspaceEntity, relations *RelResult) (*CardShowResult, error) {
	entry, err := syCardEntry(item)
	if err != nil {
		return nil, err
	}
	metadata := syMetadata(item)
	return &CardShowResult{Card: CardDetail{ID: entry.ID, Title: entry.Title, Domain: entry.Domain,
		Status: entry.Status, Deprecated: entry.Deprecated, CreatedAt: entry.CreatedAt, UpdatedAt: entry.UpdatedAt,
		Path: entry.Path, Tags: stringsOrEmpty(entry.Tags), Sections: cardSections(entry),
		UnknownSections: unknownSections(entry.Doc, entry.Raw, mdfile.KindCard), Sources: SYSourceRefs(item.Envelope),
		RelationsOut: relations.Data.RelationsOut, RelationsIn: relations.Data.RelationsIn, Deleted: entry.Deleted},
		UpdatedAt: entry.UpdatedAt, ReviewedAt: syText(metadata, "reviewed_at"), Page: relations.Page,
		Diagnostics: relations.Diagnostics}, nil
}
