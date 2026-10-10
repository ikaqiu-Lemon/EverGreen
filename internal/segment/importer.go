package segment

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
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
	var segmentation model.NoteSegmentation
	var candidates []store.Candidate
	var coverageState store.CandidateCoverageState
	if len(workspaceRaw) == 0 {
		_, candidates, coverageState, _, err = store.ParseMaterializationNote(noteRaw)
		segmentation = model.NoteSegmentation{Note: note.ID, NoteHash: store.ContentHash(noteRaw),
			Title: string(note.ID), CreatedAt: note.CreatedAt, UpdatedAt: note.UpdatedAt}
		if title, ok := note.Extra["title"].(string); ok {
			segmentation.Title = title
		}
	} else {
		_, segmentation, candidates, coverageState, _, err =
			store.ParseMaterializationWorkspace(noteRaw, workspaceRaw)
	}
	if err != nil {
		return ImportResult{}, err
	}
	body, ok := noteDoc.Section(mdfile.SecNoteBody)
	if !ok {
		return ImportResult{}, fmt.Errorf("legacy Note has no %q section", mdfile.SecNoteBody)
	}
	review, err := mdfile.ParseReviewNote(noteRaw[body.Body:body.End])
	if err != nil {
		if manifest, found, manifestErr := mdfile.ParseNoteBlockManifest(workspaceRaw); manifestErr == nil && found {
			review, err = mdfile.ParsePlainReviewNote(noteRaw[body.Body:body.End], manifest)
		}
		if err != nil {
			return ImportResult{}, err
		}
	}
	legacyCoverage, _ := json.Marshal(coverageState)
	if len(workspaceRaw) == 0 {
		candidates, coverageState, err = mapEmbeddedReview(review, candidates, coverageState)
		if err != nil {
			return ImportResult{}, err
		}
	} else if coverageState.Finalized {
		coverageState, err = mapFinalCoverage(candidates, coverageState)
		if err != nil {
			return ImportResult{}, err
		}
	}
	noteRefs := make(map[string]evergreencore.LogicalID, len(review.Blocks))
	hashes := make(map[evergreencore.LogicalID]string, len(review.Blocks))
	prefix, err := mdfile.PlainExport(noteRaw[noteDoc.BodyFrom:body.Body])
	if err != nil {
		return ImportResult{}, err
	}
	children, err := evergreencore.MarkdownChildren(prefix, string(note.ID)+"/prefix")
	if err != nil {
		return ImportResult{}, err
	}
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
		visible, renderErr := mdfile.RenderPlainReviewNote([]mdfile.ReviewBlock{block}, nil)
		if block.Role == mdfile.ReviewRoleAgent {
			label, labelErr := mdfile.ResolveAgentLabel(block.Annotation, block.Label)
			if labelErr != nil {
				return ImportResult{}, labelErr
			}
			visible = []byte("> **[Agent " + label + "]** " + strings.ReplaceAll(string(block.Body), "\n", "\n> ") + "\n")
			if block.Heading != "" {
				visible = append([]byte("### "+block.Heading+"\n\n"), visible...)
			}
			renderErr = nil
		}
		if renderErr != nil {
			return ImportResult{}, renderErr
		}
		nested, renderErr := evergreencore.MarkdownChildren(visible, blockID)
		if renderErr != nil {
			return ImportResult{}, renderErr
		}
		node := map[string]any{
			"ID": blockID, "Type": "NodeSuperBlock", "Children": evergreencore.SuperBlockChildren(nested),
			"Properties": map[string]string{"id": blockID},
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

	suffix, err := mdfile.PlainExport(noteRaw[body.End:])
	if len(workspaceRaw) == 0 {
		extraction, exists := noteDoc.Section(mdfile.SecExtraction)
		if !exists {
			return ImportResult{}, fmt.Errorf("embedded review has no extraction section")
		}
		// Candidate subtree 成为唯一 payload；候选之后的历史摘要仍保留为正文。
		afterExtraction, exportErr := mdfile.PlainExport(noteRaw[extraction.End:])
		if exportErr != nil {
			return ImportResult{}, exportErr
		}
		tail, exportErr := mdfile.PlainExport(noteRaw[candidates[len(candidates)-1].End:extraction.End])
		if exportErr != nil {
			return ImportResult{}, exportErr
		}
		suffix = append(tail, afterExtraction...)
		err = nil
	}
	if err != nil {
		return ImportResult{}, err
	}
	trailing, err := evergreencore.MarkdownChildren(suffix, string(note.ID)+"/suffix")
	if err != nil {
		return ImportResult{}, err
	}
	children = append(children, trailing...)
	candidateIDs := map[string]evergreencore.LogicalID{}
	for _, candidate := range candidates {
		candidateIDs[candidate.Key] = evergreencore.LogicalID("cand-" +
			strings.TrimPrefix(evergreencore.StablePhysicalID(string(note.ID)+"/"+candidate.Key), "20000101000000-"))
	}
	for index, candidate := range candidates {
		candidateID := candidateIDs[candidate.Key]
		blockID := legacyPhysicalID(string(note.ID), "candidate", index)
		refs := candidate.Anchor.NoteRefs
		if len(refs) == 0 {
			refs = candidate.Anchor.SourceRefs
		}
		segmentIDs, refHashes, err := mapLegacyRefs(refs, noteRefs, hashes)
		if err != nil {
			return ImportResult{}, fmt.Errorf("candidate %s: %w", candidate.Key, err)
		}
		var markdown strings.Builder
		for _, section := range candidate.Sections {
			markdown.WriteString("## " + section.Name + "\n")
			markdown.Write(section.Payload)
		}
		payload, err := evergreencore.MarkdownChildren([]byte(markdown.String()), string(candidateID))
		if err != nil {
			return ImportResult{}, err
		}
		payload = evergreencore.SuperBlockChildren(payload)
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
			Extra: evergreencore.RawObject{
				"legacy_key": jsonString(candidate.Key),
			},
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
			"Properties": map[string]string{"id": blockID},
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
	for _, item := range coverageState.Draft {
		refs := item.NoteRefs
		if len(refs) == 0 {
			refs = item.SourceRefs
		}
		segmentIDs, _, err := mapLegacyRefs(refs, noteRefs, hashes)
		if err != nil {
			return ImportResult{}, fmt.Errorf("coverage %s: %w", item.Module, err)
		}
		module := evergreencore.CoverageModule{
			ModuleID: evergreencore.LogicalID("coverage-" +
				strings.TrimPrefix(evergreencore.StablePhysicalID(string(note.ID)+"/"+item.Module+"/"+strings.Join(refs, ",")), "20000101000000-")),
			Disposition: item.Disposition, SegmentRefs: segmentIDs,
			Reason: item.Reason,
			Extra:  evergreencore.RawObject{"legacy_module": jsonString(item.Module), "summary": jsonString(item.Summary)},
		}
		for _, key := range item.Candidates {
			module.CandidateIDs = append(module.CandidateIDs,
				candidateIDs[key])
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
	if len(workspaceRaw) == 0 {
		documentEnvelope.Review.Extra = evergreencore.RawObject{"legacy_source_coverage": legacyCoverage}
	}
	if coverageState.Finalized {
		final, _ := json.Marshal(coverageState.Final)
		if documentEnvelope.Review.Extra == nil {
			documentEnvelope.Review.Extra = evergreencore.RawObject{}
		}
		documentEnvelope.Review.Extra["legacy_final_coverage"] = final
	}
	if len(review.Omissions) > 0 {
		raw, _ := json.Marshal(review.Omissions)
		if documentEnvelope.Review.Extra == nil {
			documentEnvelope.Review.Extra = evergreencore.RawObject{}
		}
		documentEnvelope.Review.Extra["source_omissions"] = raw
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
	base, err := json.Marshal(root)
	if err != nil {
		return ImportResult{}, err
	}
	result.SY, err = evergreencore.EncodeSY(base, documentEnvelope, nil)
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

func jsonString(value string) json.RawMessage {
	raw, _ := json.Marshal(value)
	return raw
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
