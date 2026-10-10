package segment

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
	"github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
)

type LegacySegmentParity struct {
	LegacyRef string                       `json:"legacy_ref"`
	SegmentID evergreencore.LogicalID      `json:"segment_id"`
	BlockID   string                       `json:"block_id"`
	Role      string                       `json:"role"`
	SourceRef *evergreencore.SourceLocator `json:"source_ref,omitempty"`
	Hash      string                       `json:"hash"`
	Order     int                          `json:"order"`
}

type LegacyCandidateParity struct {
	LegacyKey   string                    `json:"legacy_key"`
	CandidateID evergreencore.LogicalID   `json:"candidate_id"`
	BlockID     string                    `json:"block_id"`
	Kind        evergreencore.ClaimKind   `json:"kind"`
	Output      evergreencore.LogicalID   `json:"output,omitempty"`
	SegmentIDs  []evergreencore.LogicalID `json:"segment_ids"`
	PayloadHash string                    `json:"payload_hash"`
}

type LegacyParityInventory struct {
	NoteID          evergreencore.LogicalID `json:"note_id"`
	WorkspaceID     string                  `json:"workspace_id"`
	Stale           bool                    `json:"stale"`
	Segments        []LegacySegmentParity   `json:"segments"`
	Candidates      []LegacyCandidateParity `json:"candidates"`
	CoverageCount   int                     `json:"coverage_count"`
	UnresolvedCount int                     `json:"unresolved_count"`
}

type ImportResult struct {
	SY        []byte                `json:"sy"`
	Inventory LegacyParityInventory `json:"inventory"`
}

// ImportLegacyWorkspace converts an anchored n-* plus its physical ns-* review
// workspace into one authoritative Note .sy document. It is an explicit
// cutover adapter; the returned .sy never refers back to ns-* at runtime.
func ImportLegacyWorkspace(noteRaw, workspaceRaw []byte) (ImportResult, error) {
	noteDoc, note, err := mdfile.ParseNote(noteRaw)
	if err != nil {
		return ImportResult{}, err
	}
	_, segmentation, candidates, coverageState, _, err :=
		store.ParseMaterializationWorkspace(noteRaw, workspaceRaw)
	if err != nil {
		return ImportResult{}, err
	}
	if coverageState.Finalized {
		return ImportResult{}, fmt.Errorf("legacy finalized extraction is not a Candidate review workspace")
	}
	body, ok := noteDoc.Section(mdfile.SecNoteBody)
	if !ok {
		return ImportResult{}, fmt.Errorf("legacy Note has no %q section", mdfile.SecNoteBody)
	}
	review, err := mdfile.ParseReviewNote(noteRaw[body.Body:body.End])
	if err != nil {
		if _, found, manifestErr := mdfile.ParseNoteBlockManifest(workspaceRaw); manifestErr == nil && found {
			return ImportResult{}, fmt.Errorf(
				"anchorless Note import requires the exported review block payloads: %w", err)
		}
		return ImportResult{}, err
	}
	noteRefs := make(map[string]evergreencore.LogicalID, len(review.Blocks))
	hashes := make(map[evergreencore.LogicalID]string, len(review.Blocks))
	var children []json.RawMessage
	inventory := LegacyParityInventory{
		NoteID:      evergreencore.LogicalID(note.ID),
		WorkspaceID: string(segmentation.ID),
		Stale:       segmentation.NoteHash != store.ContentHash(noteRaw),
	}

	for index, block := range review.Blocks {
		ref := fmt.Sprintf("B%d", index+1)
		segmentID := legacySegmentID(string(note.ID), index)
		blockID := legacyPhysicalID(string(note.ID), "segment", index)
		noteRefs[ref] = segmentID
		role := "note_segment"
		metadata := &evergreencore.SegmentMetadata{SegmentID: segmentID}
		if block.Role == mdfile.ReviewRoleSource {
			metadata.SourceRef = evergreencore.SourceLocator{
				SourceID: evergreencore.LogicalID(note.Source),
				Locator: evergreencore.Locator{
					Kind: "source-lines", Value: block.SourceRef,
				},
			}
		} else {
			role = "agent_annotation"
			metadata.Annotation = &evergreencore.Annotation{
				Kind: block.Annotation, Label: block.Label,
			}
		}
		node := map[string]any{
			"ID": blockID, "Type": "NodeParagraph", "Data": string(block.Body),
		}
		nodeRaw, _ := json.Marshal(node)
		metadata.NormalizedHash, err = evergreencore.NormalizedSegmentHash(nodeRaw)
		if err != nil {
			return ImportResult{}, err
		}
		hashes[segmentID] = metadata.NormalizedHash
		envelopeRaw, err := evergreencore.MarshalBlockEnvelope(&evergreencore.BlockEnvelope{
			Spec: evergreencore.BlockSpec, Role: role, Segment: metadata,
		})
		if err != nil {
			return ImportResult{}, err
		}
		node["Evergreen"] = json.RawMessage(envelopeRaw)
		nodeRaw, _ = json.Marshal(node)
		children = append(children, nodeRaw)
		parity := LegacySegmentParity{
			LegacyRef: ref, SegmentID: segmentID, BlockID: blockID,
			Role: role, Hash: metadata.NormalizedHash, Order: index,
		}
		if role == "note_segment" {
			sourceRef := metadata.SourceRef
			parity.SourceRef = &sourceRef
		}
		inventory.Segments = append(inventory.Segments, parity)
	}

	for index, candidate := range candidates {
		candidateID := evergreencore.LogicalID(candidate.Key)
		blockID := legacyPhysicalID(string(note.ID), "candidate", index)
		refs := candidate.Anchor.NoteRefs
		if len(refs) == 0 {
			refs = candidate.Anchor.SourceRefs
		}
		segmentIDs, refHashes, err := mapLegacyRefs(refs, noteRefs, hashes)
		if err != nil {
			return ImportResult{}, fmt.Errorf("candidate %s: %w", candidate.Key, err)
		}
		payloadNode := map[string]any{
			"ID":         legacyPhysicalID(candidate.Key, "payload", 0),
			"Type":       "NodeCodeBlock",
			"Data":       string(candidate.Raw(workspaceRaw)),
			"Properties": map[string]any{"language": "markdown"},
		}
		payloadRaw, _ := json.Marshal(payloadNode)
		payload := []json.RawMessage{payloadRaw}
		payloadJSON, _ := json.Marshal(payload)
		payloadHash, err := evergreencore.CandidateSubtreeHash(payloadJSON)
		if err != nil {
			return ImportResult{}, err
		}
		kind := evergreencore.ClaimKind(candidate.Kind)
		kindSchema := evergreencore.KnowledgeKindSchema
		if kind == evergreencore.OpinionKind {
			kindSchema = evergreencore.OpinionKindSchema
		}
		state := "draft"
		if candidate.Anchor.Output != "" {
			state = "materialized"
		}
		if inventory.Stale {
			state = "stale"
		}
		metadata := &evergreencore.CandidateMetadata{
			CandidateID: candidateID, ClaimKind: kind, KindSchema: kindSchema,
			Title: candidate.Title, LogicalSlug: candidate.LogicalSlug,
			SegmentRefs: segmentIDs, PayloadHash: payloadHash, RefHashes: refHashes,
			Relation: candidate.Anchor.Rel, Reason: candidate.Anchor.Reason,
			Tags: append([]string(nil), candidate.Anchor.Tags...), State: state,
			MaterializedClaimID: evergreencore.LogicalID(candidate.Anchor.Output),
		}
		envelopeRaw, err := evergreencore.MarshalBlockEnvelope(&evergreencore.BlockEnvelope{
			Spec: evergreencore.BlockSpec, Role: "candidate", Candidate: metadata,
		})
		if err != nil {
			return ImportResult{}, err
		}
		node := map[string]any{
			"ID": blockID, "Type": "NodeSuperBlock",
			"Evergreen": json.RawMessage(envelopeRaw), "Children": payload,
		}
		nodeRaw, _ := json.Marshal(node)
		children = append(children, nodeRaw)
		inventory.Candidates = append(inventory.Candidates, LegacyCandidateParity{
			LegacyKey: candidate.Key, CandidateID: candidateID, BlockID: blockID,
			Kind: kind, Output: evergreencore.LogicalID(candidate.Anchor.Output),
			SegmentIDs:  append([]evergreencore.LogicalID(nil), segmentIDs...),
			PayloadHash: payloadHash,
		})
	}

	coverage := make([]evergreencore.CoverageModule, 0, len(coverageState.Draft))
	for index, item := range coverageState.Draft {
		refs := item.NoteRefs
		if len(refs) == 0 {
			refs = item.SourceRefs
		}
		segmentIDs, _, err := mapLegacyRefs(refs, noteRefs, hashes)
		if err != nil {
			return ImportResult{}, fmt.Errorf("coverage %s: %w", item.Module, err)
		}
		module := evergreencore.CoverageModule{
			ModuleID: evergreencore.LogicalID(
				fmt.Sprintf("coverage-legacy-%03d", index+1)),
			Disposition: item.Disposition, SegmentRefs: segmentIDs,
			Reason: item.Reason,
		}
		for _, key := range item.Candidates {
			module.CandidateIDs = append(module.CandidateIDs,
				evergreencore.LogicalID(key))
		}
		if len(module.CandidateIDs) == 1 {
			module.CandidateID = module.CandidateIDs[0]
			module.CandidateIDs = nil
		}
		if module.Disposition == "unresolved" {
			inventory.UnresolvedCount++
		}
		coverage = append(coverage, module)
	}
	inventory.CoverageCount = len(coverage)

	documentEnvelope := &evergreencore.DocumentEnvelope{
		Spec: evergreencore.DocumentSpec,
		Entity: evergreencore.Entity{
			LogicalID:  evergreencore.LogicalID(note.ID),
			EntityType: evergreencore.EntityNote,
			Schema:     "evergreen.note/v1", SemanticRevision: 1,
		},
		Relations: evergreencore.Relations{Outgoing: []evergreencore.TypedEdge{}},
		Review: &evergreencore.NoteReview{
			Spec: evergreencore.NoteReviewSpec, Coverage: coverage,
		},
	}
	envelopeRaw, err := evergreencore.MarshalDocumentEnvelope(documentEnvelope)
	if err != nil {
		return ImportResult{}, err
	}
	root := map[string]any{
		"ID":   legacyPhysicalID(string(note.ID), "document", 0),
		"Type": "NodeDocument", "Spec": "5",
		"Properties": map[string]any{"title": segmentation.Title},
		"Evergreen":  json.RawMessage(envelopeRaw), "Children": children,
	}
	result := ImportResult{Inventory: inventory}
	result.SY, err = json.Marshal(root)
	if err != nil {
		return ImportResult{}, err
	}
	snapshot, err := evergreencore.InspectNoteReview(result.SY, nil)
	if err != nil {
		return ImportResult{}, err
	}
	if snapshot.Summary.SegmentCount != len(inventory.Segments) ||
		snapshot.Summary.CandidateCount != len(inventory.Candidates) {
		return ImportResult{}, fmt.Errorf(
			"import inventory drift: segments %d/%d candidates %d/%d",
			snapshot.Summary.SegmentCount, len(inventory.Segments),
			snapshot.Summary.CandidateCount, len(inventory.Candidates))
	}
	return result, nil
}

func mapLegacyRefs(
	refs []string,
	noteRefs map[string]evergreencore.LogicalID,
	hashes map[evergreencore.LogicalID]string,
) ([]evergreencore.LogicalID, map[evergreencore.LogicalID]string, error) {
	segmentIDs := make([]evergreencore.LogicalID, 0, len(refs))
	refHashes := make(map[evergreencore.LogicalID]string, len(refs))
	for _, ref := range refs {
		id, ok := noteRefs[ref]
		if !ok {
			return nil, nil, fmt.Errorf("legacy Note ref %q does not exist", ref)
		}
		segmentIDs = append(segmentIDs, id)
		refHashes[id] = hashes[id]
	}
	return segmentIDs, refHashes, nil
}

func legacySegmentID(noteID string, index int) evergreencore.LogicalID {
	base := strings.TrimPrefix(noteID, "n-")
	return evergreencore.LogicalID(
		fmt.Sprintf("seg-%s-%03d", base, index+1))
}

func legacyPhysicalID(seed, role string, index int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d", seed, role, index)))
	return "20000101000000-" + hex.EncodeToString(sum[:])[:7]
}
