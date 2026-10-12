package evergreencore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

const WorkspaceDoctorSpec = "evergreen.workspace-doctor/v1"

type WorkspaceDoctorCounts struct {
	Entities   int `json:"entities"`
	Claims     int `json:"claims"`
	Edges      int `json:"edges"`
	Segments   int `json:"segments"`
	Candidates int `json:"candidates"`
	Coverage   int `json:"coverage"`
}

type WorkspaceDoctorEntity struct {
	LogicalID    LogicalID  `json:"logical_id"`
	EntityType   EntityType `json:"entity_type"`
	DocumentID   string     `json:"document_id"`
	SemanticHash string     `json:"semantic_hash"`
}

type WorkspaceDoctorEdge struct {
	OwnerID   LogicalID `json:"owner_id"`
	OwnerHash string    `json:"owner_hash"`
	Edge      TypedEdge `json:"edge"`
}

type WorkspaceDoctorSnapshot struct {
	Spec          string                  `json:"spec"`
	Digest        string                  `json:"digest"`
	ArchiveDigest string                  `json:"archive_digest"`
	Counts        WorkspaceDoctorCounts   `json:"counts"`
	Entities      []WorkspaceDoctorEntity `json:"entities"`
	Edges         []WorkspaceDoctorEdge   `json:"edges"`
	Reviews       []ReviewSnapshot        `json:"reviews"`
	Projection    AVProjectionSnapshot    `json:"projection"`
	Queries       map[string]AVPage       `json:"queries"`
}

type WorkspaceDoctorReport struct {
	Spec         string                  `json:"spec"`
	RebuildIndex bool                    `json:"rebuild_index"`
	Parity       bool                    `json:"parity"`
	Before       WorkspaceDoctorSnapshot `json:"before"`
	After        WorkspaceDoctorSnapshot `json:"after"`
}

// InspectWorkspaceDoctor derives every reported set from authority documents.
// It never consults a host cache, SQLite table, reverse graph, or AV row store.
func InspectWorkspaceDoctor(
	ctx context.Context,
	repository Repository,
	registry *Registry,
) (WorkspaceDoctorSnapshot, error) {
	if registry == nil {
		registry = DefaultRegistry()
	}
	archive, err := CanonicalExport(ctx, repository, registry)
	if err != nil {
		return WorkspaceDoctorSnapshot{}, err
	}
	archiveDigest, err := ArchiveDigest(archive)
	if err != nil {
		return WorkspaceDoctorSnapshot{}, err
	}
	result := WorkspaceDoctorSnapshot{
		Spec: WorkspaceDoctorSpec, ArchiveDigest: archiveDigest,
		Entities: []WorkspaceDoctorEntity{}, Edges: []WorkspaceDoctorEdge{},
		Reviews: []ReviewSnapshot{}, Queries: map[string]AVPage{},
	}
	for _, entity := range archive.Entities {
		identity, identityErr := inspectDocumentIdentity(entity.Document)
		if identityErr != nil {
			return WorkspaceDoctorSnapshot{}, identityErr
		}
		result.Entities = append(result.Entities, WorkspaceDoctorEntity{
			LogicalID: entity.LogicalID, EntityType: entity.Type,
			DocumentID: identity.DocumentID, SemanticHash: entity.Hash,
		})
		result.Counts.Entities++
		if entity.Type == EntityClaim {
			result.Counts.Claims++
		}
		document, decodeErr := DecodeSY(entity.Document, registry)
		if decodeErr != nil {
			return WorkspaceDoctorSnapshot{}, decodeErr
		}
		if document.Envelope == nil {
			continue
		}
		for _, edge := range document.Envelope.Relations.Outgoing {
			result.Edges = append(result.Edges, WorkspaceDoctorEdge{
				OwnerID: entity.LogicalID, OwnerHash: entity.Hash, Edge: edge,
			})
			result.Counts.Edges++
		}
		if document.Envelope.Review != nil {
			review, reviewErr := InspectNoteReview(entity.Document, registry)
			if reviewErr != nil {
				return WorkspaceDoctorSnapshot{}, reviewErr
			}
			result.Reviews = append(result.Reviews, review)
			result.Counts.Segments += len(review.Segments)
			result.Counts.Candidates += len(review.Candidates)
			result.Counts.Coverage += len(review.Coverage)
		}
	}
	sort.Slice(result.Edges, func(i, j int) bool {
		if result.Edges[i].OwnerID == result.Edges[j].OwnerID {
			return result.Edges[i].Edge.ID < result.Edges[j].Edge.ID
		}
		return result.Edges[i].OwnerID < result.Edges[j].OwnerID
	})

	provider := NewClaimsAVProvider(repository, nil, registry)
	result.Projection, err = provider.Rebuild(ctx)
	if err != nil {
		return WorkspaceDoctorSnapshot{}, err
	}
	viewIDs := map[string]bool{ClaimsViewID: true}
	for _, kind := range CurrentSchemas(registry).Kinds {
		if kind.ViewHints.ViewID != "" {
			viewIDs[kind.ViewHints.ViewID] = true
		}
	}
	orderedViews := make([]string, 0, len(viewIDs))
	for viewID := range viewIDs {
		orderedViews = append(orderedViews, viewID)
	}
	sort.Strings(orderedViews)
	for _, viewID := range orderedViews {
		page, queryErr := provider.Query(ctx, viewID, AVQuery{})
		if queryErr != nil {
			return WorkspaceDoctorSnapshot{}, queryErr
		}
		result.Queries[viewID] = page
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return WorkspaceDoctorSnapshot{}, err
	}
	sum := sha256.Sum256(raw)
	result.Digest = "sha256:" + hex.EncodeToString(sum[:])
	return result, nil
}
