package evergreencore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	ReviewActionCandidateUpdate  = "candidate.update"
	ReviewActionCandidateRebase  = "candidate.rebase"
	ReviewActionCandidateConfirm = "candidate.confirm"
	ReviewActionCandidateDiscard = "candidate.discard"
	ReviewActionCoverageUpdate   = "coverage.update"
)

type ReviewSegment struct {
	SegmentID   LogicalID     `json:"segment_id"`
	BlockID     string        `json:"block_id"`
	Role        string        `json:"role"`
	SourceRef   SourceLocator `json:"source_ref,omitempty"`
	Annotation  *Annotation   `json:"annotation,omitempty"`
	StoredHash  string        `json:"stored_hash"`
	CurrentHash string        `json:"current_hash"`
	Order       int           `json:"order"`
}

type ReviewCandidate struct {
	CandidateID        LogicalID            `json:"candidate_id"`
	BlockID            string               `json:"block_id"`
	Metadata           CandidateMetadata    `json:"metadata"`
	Payload            json.RawMessage      `json:"payload"`
	CurrentPayloadHash string               `json:"current_payload_hash"`
	CurrentRefHashes   map[LogicalID]string `json:"current_ref_hashes"`
	Stale              bool                 `json:"stale"`
	StaleRefs          []LogicalID          `json:"stale_refs"`
	Order              int                  `json:"order"`
}

type CoverageSummary struct {
	SegmentCount       int `json:"segment_count"`
	CandidateCount     int `json:"candidate_count"`
	CoveredSegments    int `json:"covered_segments"`
	MissingSegments    int `json:"missing_segments"`
	DuplicateSegments  int `json:"duplicate_segments"`
	UnresolvedModules  int `json:"unresolved_modules"`
	UnreferencedDrafts int `json:"unreferenced_candidates"`
}

type ReviewSnapshot struct {
	NoteID         LogicalID         `json:"note_id"`
	DocumentID     string            `json:"document_id"`
	SemanticHash   string            `json:"semantic_hash"`
	Revision       uint64            `json:"revision"`
	Segments       []ReviewSegment   `json:"segments"`
	Candidates     []ReviewCandidate `json:"candidates"`
	Coverage       []CoverageModule  `json:"coverage"`
	Lineage        []SegmentLineage  `json:"lineage,omitempty"`
	Summary        CoverageSummary   `json:"summary"`
	CanMaterialize bool              `json:"can_materialize"`
	Diagnostics    []Diagnostic      `json:"diagnostics,omitempty"`
}

type RefHashDiff struct {
	SegmentID LogicalID `json:"segment_id"`
	Before    string    `json:"before,omitempty"`
	After     string    `json:"after,omitempty"`
}

type ReviewDiff struct {
	CandidateID LogicalID     `json:"candidate_id,omitempty"`
	RefHashes   []RefHashDiff `json:"ref_hashes,omitempty"`
}

type ReviewCommand struct {
	Action      string             `json:"action"`
	CandidateID LogicalID          `json:"candidate_id,omitempty"`
	Metadata    *CandidateMetadata `json:"metadata,omitempty"`
	// Payload is a JSON array of native SiYuan nodes for the managed
	// Candidate container subtree.
	Payload  json.RawMessage  `json:"payload,omitempty"`
	Coverage []CoverageModule `json:"coverage,omitempty"`
}

type ReviewPlanRequest struct {
	OperationID string        `json:"operation_id"`
	NoteID      LogicalID     `json:"note_id"`
	Base        string        `json:"base"`
	Command     ReviewCommand `json:"command"`
}

type PlannedReview struct {
	Operation PlannedOperation `json:"operation"`
	Snapshot  ReviewSnapshot   `json:"snapshot"`
	Diff      ReviewDiff       `json:"diff"`
}

type ReviewEditPlanRequest struct {
	OperationID string           `json:"operation_id"`
	NoteID      LogicalID        `json:"note_id"`
	Base        string           `json:"base"`
	Edited      json.RawMessage  `json:"edited"`
	Lineage     []SegmentLineage `json:"lineage,omitempty"`
}

type SegmentEditResult struct {
	After    []byte           `json:"after"`
	Snapshot ReviewSnapshot   `json:"snapshot"`
	Lineage  []SegmentLineage `json:"lineage,omitempty"`
}

// InspectNoteReview derives all review state from a Note .sy document. It
// never trusts a secondary index or AV row.
func InspectNoteReview(data []byte, registry *Registry) (ReviewSnapshot, error) {
	tree, err := parseReviewTree(data, registry)
	if err != nil {
		return ReviewSnapshot{}, err
	}
	return tree.snapshot(data)
}

func ValidateReviewForMaterialization(snapshot ReviewSnapshot) error {
	var blocking []Diagnostic
	for _, diagnostic := range snapshot.Diagnostics {
		if diagnostic.Level == "error" {
			blocking = append(blocking, diagnostic)
		}
	}
	if len(blocking) > 0 {
		return &DiagnosticError{Diagnostics: blocking}
	}
	return nil
}

func NormalizedSegmentHash(node json.RawMessage) (string, error) {
	parsed, err := parseReviewNode(node)
	if err != nil {
		return "", err
	}
	return segmentNodeHash(parsed), nil
}

func CandidateSubtreeHash(payload json.RawMessage) (string, error) {
	var children []json.RawMessage
	if err := json.Unmarshal(payload, &children); err != nil {
		return "", err
	}
	container := &reviewNode{}
	for _, child := range children {
		parsed, err := parseReviewNode(child)
		if err != nil {
			return "", err
		}
		container.children = append(container.children, parsed)
	}
	return candidatePayloadHash(container), nil
}

// RefreshNoteReview is the native-editor save hook. It updates changed
// segment hashes and marks affected Candidates stale in the same .sy image.
// Documents without a managed Note review pass through unchanged.
func RefreshNoteReview(data []byte, registry *Registry) ([]byte, bool, error) {
	document, err := DecodeSY(data, registry)
	if err != nil {
		return nil, false, err
	}
	if document.Envelope == nil || document.Envelope.Entity.EntityType != EntityNote ||
		document.Envelope.Review == nil {
		return data, false, nil
	}
	tree, err := parseReviewTree(data, registry)
	if err != nil {
		return nil, false, err
	}
	changed := false
	for _, segment := range tree.segments {
		if segment.envelope.Segment.NormalizedHash == segment.currentHash {
			continue
		}
		tree.document.Envelope.Review.Lineage = append(
			tree.document.Envelope.Review.Lineage,
			SegmentLineage{
				EventID: deterministicLineageID(
					"edit",
					[]LogicalID{segment.envelope.Segment.SegmentID},
					[]LogicalID{segment.envelope.Segment.SegmentID},
					segment.currentHash,
				),
				Mutation:    "edit",
				PreviousIDs: []LogicalID{segment.envelope.Segment.SegmentID},
				NextIDs:     []LogicalID{segment.envelope.Segment.SegmentID},
			},
		)
		segment.envelope.Segment.NormalizedHash = segment.currentHash
		if err = segment.writeEnvelope(); err != nil {
			return nil, false, err
		}
		changed = true
	}

	missing := map[LogicalID]struct{}{}
	for _, module := range tree.document.Envelope.Review.Coverage {
		for _, id := range module.SegmentRefs {
			if _, exists := tree.segmentByID[id]; !exists {
				missing[id] = struct{}{}
			}
		}
	}
	for _, candidate := range tree.candidates {
		for _, id := range candidate.envelope.Candidate.SegmentRefs {
			if _, exists := tree.segmentByID[id]; !exists {
				missing[id] = struct{}{}
			}
		}
		if candidate.envelope.Candidate.State != "discarded" &&
			candidate.isStale(tree.segmentByID) &&
			candidate.envelope.Candidate.State != "stale" {
			candidate.envelope.Candidate.State = "stale"
			if err = candidate.writeEnvelope(); err != nil {
				return nil, false, err
			}
			changed = true
		}
	}
	recordedDeleted := map[LogicalID]bool{}
	for _, event := range tree.document.Envelope.Review.Lineage {
		if event.Mutation == "delete" {
			for _, id := range event.PreviousIDs {
				recordedDeleted[id] = true
			}
		}
	}
	deleted := make([]LogicalID, 0, len(missing))
	for id := range missing {
		if !recordedDeleted[id] {
			deleted = append(deleted, id)
		}
	}
	sort.Slice(deleted, func(i, j int) bool { return deleted[i] < deleted[j] })
	for _, id := range deleted {
		tree.document.Envelope.Review.Lineage = append(
			tree.document.Envelope.Review.Lineage,
			SegmentLineage{
				EventID:     deterministicLineageID("delete", []LogicalID{id}, nil, ""),
				Mutation:    "delete",
				PreviousIDs: []LogicalID{id},
				NextIDs:     []LogicalID{},
			},
		)
		changed = true
	}
	if !changed {
		return data, false, nil
	}
	tree.document.Envelope.Entity.SemanticRevision++
	if err = tree.writeDocumentEnvelope(); err != nil {
		return nil, false, err
	}
	encoded, err := tree.encode()
	if err != nil {
		return nil, false, err
	}
	return encoded, true, nil
}

// NormalizeNoteReviewEdit reconciles a native SiYuan block edit with
// Evergreen hashes and stale state. Move keeps identity and emits no lineage;
// content edits retain the segment ID and emit edit lineage. Split, merge and
// delete require explicit lineage so identity changes cannot be inferred
// silently.
func NormalizeNoteReviewEdit(
	previous, edited []byte,
	lineage []SegmentLineage,
	registry *Registry,
) (SegmentEditResult, error) {
	before, err := parseReviewTree(previous, registry)
	if err != nil {
		return SegmentEditResult{}, err
	}
	after, err := parseReviewTree(edited, registry)
	if err != nil {
		return SegmentEditResult{}, err
	}
	if before.document.Envelope.Entity.LogicalID != after.document.Envelope.Entity.LogicalID {
		return SegmentEditResult{}, validationError(
			CodeInvalidPlan, "note_id", "edited Note logical ID does not match the previous document")
	}

	previousIDs := map[LogicalID]struct{}{}
	nextIDs := map[LogicalID]struct{}{}
	for id := range before.segmentByID {
		previousIDs[id] = struct{}{}
	}
	for id := range after.segmentByID {
		nextIDs[id] = struct{}{}
	}
	coveredPrevious := map[LogicalID]struct{}{}
	coveredNext := map[LogicalID]struct{}{}
	for _, event := range lineage {
		for _, id := range event.PreviousIDs {
			if _, ok := previousIDs[id]; !ok {
				return SegmentEditResult{}, validationError(
					CodeSegmentLineage, "lineage.previous_ids", fmt.Sprintf("unknown predecessor %q", id))
			}
			coveredPrevious[id] = struct{}{}
		}
		for _, id := range event.NextIDs {
			if _, ok := nextIDs[id]; !ok {
				return SegmentEditResult{}, validationError(
					CodeSegmentLineage, "lineage.next_ids", fmt.Sprintf("unknown successor %q", id))
			}
			coveredNext[id] = struct{}{}
		}
	}
	for id := range previousIDs {
		if _, stillPresent := nextIDs[id]; !stillPresent {
			if _, covered := coveredPrevious[id]; !covered {
				return SegmentEditResult{}, validationError(
					CodeSegmentLineage, "lineage", fmt.Sprintf("removed segment %q requires split, merge, or delete lineage", id))
			}
		}
	}
	for id := range nextIDs {
		if _, existed := previousIDs[id]; !existed {
			if _, covered := coveredNext[id]; !covered {
				return SegmentEditResult{}, validationError(
					CodeSegmentLineage, "lineage", fmt.Sprintf("new segment %q requires split or merge lineage", id))
			}
		}
	}

	appliedLineage := append([]SegmentLineage(nil), lineage...)
	for id, current := range after.segmentByID {
		current.envelope.Segment.NormalizedHash = current.currentHash
		if prior, ok := before.segmentByID[id]; ok && prior.currentHash != current.currentHash {
			event := SegmentLineage{
				EventID:     deterministicLineageID("edit", []LogicalID{id}, []LogicalID{id}, current.currentHash),
				Mutation:    "edit",
				PreviousIDs: []LogicalID{id},
				NextIDs:     []LogicalID{id},
			}
			appliedLineage = append(appliedLineage, event)
		}
		if err = current.writeEnvelope(); err != nil {
			return SegmentEditResult{}, err
		}
	}
	after.document.Envelope.Review.Lineage = append(
		after.document.Envelope.Review.Lineage, appliedLineage...)
	if err = validateNoteReviewEnvelope(after.document.Envelope.Review); err != nil {
		return SegmentEditResult{}, err
	}
	after.markStaleCandidates()
	after.document.Envelope.Entity.SemanticRevision++
	if err = after.writeDocumentEnvelope(); err != nil {
		return SegmentEditResult{}, err
	}
	encoded, err := after.encode()
	if err != nil {
		return SegmentEditResult{}, err
	}
	snapshot, err := InspectNoteReview(encoded, registry)
	if err != nil {
		return SegmentEditResult{}, err
	}
	return SegmentEditResult{After: encoded, Snapshot: snapshot, Lineage: appliedLineage}, nil
}

func ApplyReviewCommand(data []byte, command ReviewCommand, registry *Registry) ([]byte, ReviewDiff, error) {
	tree, err := parseReviewTree(data, registry)
	if err != nil {
		return nil, ReviewDiff{}, err
	}
	diff := ReviewDiff{CandidateID: command.CandidateID}
	switch command.Action {
	case ReviewActionCoverageUpdate:
		tree.document.Envelope.Review.Coverage = cloneCoverage(command.Coverage)
	case ReviewActionCandidateUpdate:
		candidate, ok := tree.candidateByID[command.CandidateID]
		if !ok {
			return nil, diff, validationError(CodeCandidateMissing, "candidate_id", "candidate does not exist")
		}
		if command.Metadata != nil {
			if command.Metadata.CandidateID != command.CandidateID {
				return nil, diff, validationError(CodeInvalidPlan, "metadata.candidate_id", "candidate IDs do not match")
			}
			candidate.envelope.Candidate = cloneCandidateMetadata(command.Metadata)
		}
		if len(command.Payload) > 0 {
			var children []json.RawMessage
			if err = json.Unmarshal(command.Payload, &children); err != nil {
				return nil, diff, validationError(CodeInvalidPlan, "payload", "candidate payload must be a JSON node array")
			}
			parsed := make([]*reviewNode, 0, len(children))
			for index, child := range children {
				node, parseErr := parseReviewNode(child)
				if parseErr != nil {
					return nil, diff, validationError(
						CodeInvalidPlan, fmt.Sprintf("payload[%d]", index), parseErr.Error())
				}
				if node.containsManagedEnvelope() {
					return nil, diff, validationError(
						CodeInvalidPlan, fmt.Sprintf("payload[%d]", index), "candidate payload may not contain nested managed envelopes")
				}
				parsed = append(parsed, node)
			}
			candidate.node.children = parsed
		}
		candidate.refreshPayloadHash()
		candidate.captureCurrentRefs(tree.segmentByID, &diff)
		candidate.envelope.Candidate.State = "draft"
		if err = candidate.writeEnvelope(); err != nil {
			return nil, diff, err
		}
	case ReviewActionCandidateRebase, ReviewActionCandidateConfirm:
		candidate, ok := tree.candidateByID[command.CandidateID]
		if !ok {
			return nil, diff, validationError(CodeCandidateMissing, "candidate_id", "candidate does not exist")
		}
		candidate.captureCurrentRefs(tree.segmentByID, &diff)
		candidate.refreshPayloadHash()
		if command.Action == ReviewActionCandidateConfirm {
			candidate.envelope.Candidate.State = "confirmed"
		} else {
			candidate.envelope.Candidate.State = "draft"
		}
		if err = candidate.writeEnvelope(); err != nil {
			return nil, diff, err
		}
	case ReviewActionCandidateDiscard:
		candidate, ok := tree.candidateByID[command.CandidateID]
		if !ok {
			return nil, diff, validationError(CodeCandidateMissing, "candidate_id", "candidate does not exist")
		}
		candidate.envelope.Candidate.State = "discarded"
		if err = candidate.writeEnvelope(); err != nil {
			return nil, diff, err
		}
		for index := range tree.document.Envelope.Review.Coverage {
			module := &tree.document.Envelope.Review.Coverage[index]
			ids := coverageCandidateIDs(*module)
			filtered := ids[:0]
			for _, id := range ids {
				if id != command.CandidateID {
					filtered = append(filtered, id)
				}
			}
			if len(filtered) == len(ids) {
				continue
			}
			module.CandidateID = ""
			module.CandidateIDs = append([]LogicalID(nil), filtered...)
			if len(filtered) == 0 {
				module.Disposition = "unresolved"
				module.Reason = "Referenced candidate was discarded and requires review."
			}
		}
	default:
		return nil, diff, validationError(CodeInvalidPlan, "action", fmt.Sprintf("unsupported review action %q", command.Action))
	}

	if err = validateNoteReviewEnvelope(tree.document.Envelope.Review); err != nil {
		return nil, diff, err
	}
	tree.document.Envelope.Entity.SemanticRevision++
	if err = tree.writeDocumentEnvelope(); err != nil {
		return nil, diff, err
	}
	encoded, err := tree.encode()
	if err != nil {
		return nil, diff, err
	}
	snapshot, err := InspectNoteReview(encoded, registry)
	if err != nil {
		return nil, diff, err
	}
	if command.Action == ReviewActionCoverageUpdate ||
		command.Action == ReviewActionCandidateUpdate ||
		command.Action == ReviewActionCandidateRebase ||
		command.Action == ReviewActionCandidateConfirm {
		if err = validateReviewStructure(snapshot); err != nil {
			return nil, diff, err
		}
	}
	return encoded, diff, nil
}

func (s *AuthorityService) PlanReview(
	ctx context.Context,
	principal Principal,
	request ReviewPlanRequest,
) (PlannedReview, error) {
	if s == nil || s.host == nil {
		return PlannedReview{}, validationError(CodeInvalidPlan, "host", "authority host is unavailable")
	}
	current, err := s.host.Load(ctx, request.NoteID)
	if err != nil {
		return PlannedReview{}, err
	}
	currentHash, err := SemanticHash(current)
	if err != nil {
		return PlannedReview{}, err
	}
	if request.Base != currentHash {
		return PlannedReview{}, validationError(CodeBaseMismatch, "base", "Note semantic base hash is stale")
	}
	after, diff, err := ApplyReviewCommand(current, request.Command, s.registry)
	if err != nil {
		return PlannedReview{}, err
	}
	operation, err := s.Plan(ctx, principal, PlanRequest{
		Protocol: ChangePlanProtocol, OperationID: request.OperationID,
		Command: "note.review.update",
		Base:    []BaseRef{{LogicalID: request.NoteID, SemanticHash: request.Base}},
		Writes:  []WriteInput{{LogicalID: request.NoteID, After: after}},
	})
	if err != nil {
		return PlannedReview{}, err
	}
	snapshot, err := InspectNoteReview(after, s.registry)
	if err != nil {
		return PlannedReview{}, err
	}
	return PlannedReview{Operation: operation, Snapshot: snapshot, Diff: diff}, nil
}

func (s *AuthorityService) InspectReview(
	ctx context.Context,
	noteID LogicalID,
) (ReviewSnapshot, error) {
	if s == nil || s.host == nil {
		return ReviewSnapshot{}, validationError(CodeInvalidPlan, "host", "authority host is unavailable")
	}
	current, err := s.host.Load(ctx, noteID)
	if err != nil {
		return ReviewSnapshot{}, err
	}
	return InspectNoteReview(current, s.registry)
}

func (s *AuthorityService) InspectReviewByDocumentID(
	ctx context.Context,
	documentID string,
) (ReviewSnapshot, error) {
	if s == nil || s.host == nil {
		return ReviewSnapshot{}, validationError(CodeInvalidPlan, "host", "authority host is unavailable")
	}
	if strings.TrimSpace(documentID) == "" {
		return ReviewSnapshot{}, validationError(CodeInvalidPlan, "document_id", "SiYuan document ID is required")
	}
	ids, err := s.host.List(ctx)
	if err != nil {
		return ReviewSnapshot{}, err
	}
	for _, id := range ids {
		raw, loadErr := s.host.Load(ctx, id)
		if loadErr != nil {
			return ReviewSnapshot{}, loadErr
		}
		document, decodeErr := DecodeSY(raw, s.registry)
		if decodeErr != nil || document.Envelope == nil ||
			document.Envelope.Entity.EntityType != EntityNote ||
			document.Envelope.Review == nil {
			continue
		}
		root, decodeErr := decodeRawObject(raw)
		if decodeErr != nil {
			return ReviewSnapshot{}, decodeErr
		}
		var physicalID string
		if decodeErr = json.Unmarshal(root["ID"], &physicalID); decodeErr != nil {
			return ReviewSnapshot{}, decodeErr
		}
		if physicalID == documentID {
			return InspectNoteReview(raw, s.registry)
		}
	}
	return ReviewSnapshot{}, validationError(
		CodeInvalidEnvelope, "document_id", fmt.Sprintf("managed Evergreen Note %q was not found", documentID))
}

func (s *AuthorityService) PlanReviewEdit(
	ctx context.Context,
	principal Principal,
	request ReviewEditPlanRequest,
) (PlannedReview, error) {
	if s == nil || s.host == nil {
		return PlannedReview{}, validationError(CodeInvalidPlan, "host", "authority host is unavailable")
	}
	current, err := s.host.Load(ctx, request.NoteID)
	if err != nil {
		return PlannedReview{}, err
	}
	currentHash, err := SemanticHash(current)
	if err != nil {
		return PlannedReview{}, err
	}
	if request.Base != currentHash {
		return PlannedReview{}, validationError(CodeBaseMismatch, "base", "Note semantic base hash is stale")
	}
	result, err := NormalizeNoteReviewEdit(current, request.Edited, request.Lineage, s.registry)
	if err != nil {
		return PlannedReview{}, err
	}
	operation, err := s.Plan(ctx, principal, PlanRequest{
		Protocol: ChangePlanProtocol, OperationID: request.OperationID,
		Command: "note.review.update",
		Base:    []BaseRef{{LogicalID: request.NoteID, SemanticHash: request.Base}},
		Writes:  []WriteInput{{LogicalID: request.NoteID, After: result.After}},
	})
	if err != nil {
		return PlannedReview{}, err
	}
	return PlannedReview{
		Operation: operation, Snapshot: result.Snapshot,
	}, nil
}

type ReviewMaterializeRequest struct {
	OperationID     string    `json:"operation_id"`
	NoteID          LogicalID `json:"note_id"`
	CandidateID     LogicalID `json:"candidate_id"`
	NoteBase        string    `json:"note_base"`
	ClaimID         LogicalID `json:"claim_id"`
	ClaimBase       string    `json:"claim_base,omitempty"`
	ClaimPath       string    `json:"claim_path,omitempty"`
	ClaimDocumentID string    `json:"claim_document_id,omitempty"`
	CreatedAt       string    `json:"created_at"`
}

type PlannedMaterialization struct {
	Operation PlannedOperation `json:"operation"`
	Snapshot  ReviewSnapshot   `json:"snapshot"`
	ClaimID   LogicalID        `json:"claim_id"`
}

func (s *AuthorityService) PlanReviewMaterialization(
	ctx context.Context,
	principal Principal,
	request ReviewMaterializeRequest,
) (PlannedMaterialization, error) {
	if s == nil || s.host == nil {
		return PlannedMaterialization{}, validationError(CodeInvalidPlan, "host", "authority host is unavailable")
	}
	noteRaw, err := s.host.Load(ctx, request.NoteID)
	if err != nil {
		return PlannedMaterialization{}, err
	}
	noteHash, err := SemanticHash(noteRaw)
	if err != nil {
		return PlannedMaterialization{}, err
	}
	if noteHash != request.NoteBase {
		return PlannedMaterialization{}, validationError(CodeBaseMismatch, "note_base", "Note semantic base hash is stale")
	}
	tree, err := parseReviewTree(noteRaw, s.registry)
	if err != nil {
		return PlannedMaterialization{}, err
	}
	snapshot, err := tree.snapshot(noteRaw)
	if err != nil {
		return PlannedMaterialization{}, err
	}
	if err = ValidateReviewForMaterialization(snapshot); err != nil {
		return PlannedMaterialization{}, err
	}
	candidate, ok := tree.candidateByID[request.CandidateID]
	if !ok || candidate.envelope.Candidate.State == "discarded" {
		return PlannedMaterialization{}, validationError(CodeCandidateMissing, "candidate_id", "candidate does not exist")
	}
	if candidate.isStale(tree.segmentByID) {
		return PlannedMaterialization{}, validationError(CodeCandidateStale, "candidate_id", "stale candidate cannot be materialized")
	}
	if request.ClaimID == "" {
		return PlannedMaterialization{}, validationError(CodeInvalidPlan, "claim_id", "claim ID is required")
	}
	if err = validateTimestamp(request.CreatedAt); err != nil {
		return PlannedMaterialization{}, validationError(CodeInvalidPlan, "created_at", err.Error())
	}

	claimRaw, claimCreate, err := tree.materializedClaim(ctx, s.host, candidate, request)
	if err != nil {
		return PlannedMaterialization{}, err
	}
	candidate.envelope.Candidate.State = "materialized"
	candidate.envelope.Candidate.MaterializedClaimID = request.ClaimID
	if err = candidate.writeEnvelope(); err != nil {
		return PlannedMaterialization{}, err
	}
	tree.document.Envelope.Entity.SemanticRevision++
	if err = tree.writeDocumentEnvelope(); err != nil {
		return PlannedMaterialization{}, err
	}
	noteAfter, err := tree.encode()
	if err != nil {
		return PlannedMaterialization{}, err
	}

	base := []BaseRef{{LogicalID: request.NoteID, SemanticHash: request.NoteBase}}
	if claimCreate {
		base = append(base, BaseRef{LogicalID: request.ClaimID, Expected: "absent"})
	} else {
		base = append(base, BaseRef{LogicalID: request.ClaimID, SemanticHash: request.ClaimBase})
	}
	writes := []WriteInput{
		{LogicalID: request.NoteID, After: noteAfter},
		{LogicalID: request.ClaimID, Path: request.ClaimPath, After: claimRaw},
	}
	operation, err := s.Plan(ctx, principal, PlanRequest{
		Protocol: ChangePlanProtocol, OperationID: request.OperationID,
		Command: "note.review.materialize", Base: base, Writes: writes,
	})
	if err != nil {
		return PlannedMaterialization{}, err
	}
	afterSnapshot, err := InspectNoteReview(noteAfter, s.registry)
	if err != nil {
		return PlannedMaterialization{}, err
	}
	return PlannedMaterialization{Operation: operation, Snapshot: afterSnapshot, ClaimID: request.ClaimID}, nil
}

type reviewTree struct {
	root          *reviewNode
	document      *SYDocument
	registry      *Registry
	segmentByID   map[LogicalID]*reviewSegmentNode
	candidateByID map[LogicalID]*reviewCandidateNode
	segments      []*reviewSegmentNode
	candidates    []*reviewCandidateNode
}

type reviewNode struct {
	object   RawObject
	children []*reviewNode
}

type reviewSegmentNode struct {
	node        *reviewNode
	envelope    *BlockEnvelope
	blockID     string
	currentHash string
	order       int
}

type reviewCandidateNode struct {
	node        *reviewNode
	envelope    *BlockEnvelope
	blockID     string
	payloadHash string
	order       int
}

func parseReviewTree(data []byte, registry *Registry) (*reviewTree, error) {
	if registry == nil {
		registry = DefaultRegistry()
	}
	document, err := DecodeSY(data, registry)
	if err != nil {
		return nil, err
	}
	if document.ReadOnly {
		return nil, &DiagnosticError{Diagnostics: document.Diagnostics}
	}
	if document.Envelope == nil || document.Envelope.Entity.EntityType != EntityNote ||
		document.Envelope.Review == nil {
		return nil, validationError(CodeInvalidEnvelope, "Evergreen.review", "managed Note review is required")
	}
	root, err := parseReviewNode(data)
	if err != nil {
		return nil, err
	}
	tree := &reviewTree{
		root: root, document: document, registry: registry,
		segmentByID:   map[LogicalID]*reviewSegmentNode{},
		candidateByID: map[LogicalID]*reviewCandidateNode{},
	}
	var walk func(*reviewNode) error
	walk = func(node *reviewNode) error {
		if raw, ok := node.object[evergreenJSONKey]; ok {
			envelope, decodeErr := UnmarshalBlockEnvelope(raw)
			if decodeErr == nil {
				var blockID string
				_ = json.Unmarshal(node.object["ID"], &blockID)
				switch envelope.Role {
				case "note_segment", "agent_annotation":
					if _, duplicate := tree.segmentByID[envelope.Segment.SegmentID]; duplicate {
						return validationError(CodeDuplicateID, "segment_id", fmt.Sprintf("duplicate segment %q", envelope.Segment.SegmentID))
					}
					item := &reviewSegmentNode{
						node: node, envelope: envelope, blockID: blockID,
						currentHash: segmentNodeHash(node), order: len(tree.segments),
					}
					tree.segmentByID[envelope.Segment.SegmentID] = item
					tree.segments = append(tree.segments, item)
				case "candidate":
					if _, duplicate := tree.candidateByID[envelope.Candidate.CandidateID]; duplicate {
						return validationError(CodeDuplicateID, "candidate_id", fmt.Sprintf("duplicate candidate %q", envelope.Candidate.CandidateID))
					}
					item := &reviewCandidateNode{
						node: node, envelope: envelope, blockID: blockID,
						payloadHash: candidatePayloadHash(node), order: len(tree.candidates),
					}
					tree.candidateByID[envelope.Candidate.CandidateID] = item
					tree.candidates = append(tree.candidates, item)
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
	if err = walk(root); err != nil {
		return nil, err
	}
	return tree, nil
}

func (t *reviewTree) snapshot(raw []byte) (ReviewSnapshot, error) {
	hash, _ := SemanticHash(raw)
	var documentID string
	_ = json.Unmarshal(t.root.object["ID"], &documentID)
	snapshot := ReviewSnapshot{
		NoteID: t.document.Envelope.Entity.LogicalID, DocumentID: documentID,
		SemanticHash: hash, Revision: t.document.Envelope.Entity.SemanticRevision,
		Segments:   []ReviewSegment{},
		Candidates: []ReviewCandidate{},
		Coverage:   cloneCoverage(t.document.Envelope.Review.Coverage),
		Lineage:    append([]SegmentLineage(nil), t.document.Envelope.Review.Lineage...),
	}
	for _, item := range t.segments {
		snapshot.Segments = append(snapshot.Segments, ReviewSegment{
			SegmentID: item.envelope.Segment.SegmentID, BlockID: item.blockID,
			Role: item.envelope.Role, SourceRef: item.envelope.Segment.SourceRef,
			Annotation: item.envelope.Segment.Annotation,
			StoredHash: item.envelope.Segment.NormalizedHash, CurrentHash: item.currentHash,
			Order: item.order,
		})
	}
	for _, item := range t.candidates {
		currentRefs := map[LogicalID]string{}
		staleRefs := []LogicalID{}
		for _, id := range item.envelope.Candidate.SegmentRefs {
			if segment, ok := t.segmentByID[id]; ok {
				currentRefs[id] = segment.currentHash
				if item.envelope.Candidate.RefHashes[id] != segment.currentHash {
					staleRefs = append(staleRefs, id)
				}
			} else {
				staleRefs = append(staleRefs, id)
			}
		}
		stale := item.envelope.Candidate.State == "stale" ||
			item.envelope.Candidate.PayloadHash != item.payloadHash || len(staleRefs) > 0
		payload, err := candidatePayload(item.node)
		if err != nil {
			return ReviewSnapshot{}, err
		}
		snapshot.Candidates = append(snapshot.Candidates, ReviewCandidate{
			CandidateID: item.envelope.Candidate.CandidateID, BlockID: item.blockID,
			Metadata:           *cloneCandidateMetadata(item.envelope.Candidate),
			Payload:            payload,
			CurrentPayloadHash: item.payloadHash, CurrentRefHashes: currentRefs,
			Stale: stale, StaleRefs: staleRefs, Order: item.order,
		})
		if stale && item.envelope.Candidate.State != "discarded" {
			snapshot.Diagnostics = append(snapshot.Diagnostics, Diagnostic{
				Code: CodeCandidateStale, Level: "error",
				Path:    "candidate." + string(item.envelope.Candidate.CandidateID),
				Message: "candidate payload or referenced segment hashes changed",
			})
		}
		if item.envelope.Candidate.PayloadHash != item.payloadHash {
			snapshot.Diagnostics = append(snapshot.Diagnostics, Diagnostic{
				Code: CodeCandidatePayload, Level: "error",
				Path:    "candidate." + string(item.envelope.Candidate.CandidateID) + ".payload_hash",
				Message: "candidate payload hash does not match its authoritative subtree",
			})
		}
	}
	t.appendCoverageDiagnostics(&snapshot)
	snapshot.CanMaterialize = true
	for _, diagnostic := range snapshot.Diagnostics {
		if diagnostic.Level == "error" {
			snapshot.CanMaterialize = false
			break
		}
	}
	return snapshot, nil
}

func (t *reviewTree) appendCoverageDiagnostics(snapshot *ReviewSnapshot) {
	counts := map[LogicalID]int{}
	referenced := map[LogicalID]bool{}
	unresolved := 0
	for index, module := range t.document.Envelope.Review.Coverage {
		path := fmt.Sprintf("Evergreen.review.coverage[%d]", index)
		for _, id := range module.SegmentRefs {
			if _, exists := t.segmentByID[id]; !exists {
				snapshot.Diagnostics = append(snapshot.Diagnostics, Diagnostic{
					Code: CodeCoverageInvalid, Level: "error", Path: path + ".segment_refs",
					Message: fmt.Sprintf("coverage references missing segment %q", id),
				})
				continue
			}
			counts[id]++
		}
		for _, id := range coverageCandidateIDs(module) {
			if _, exists := t.candidateByID[id]; !exists {
				snapshot.Diagnostics = append(snapshot.Diagnostics, Diagnostic{
					Code: CodeCandidateMissing, Level: "error", Path: path + ".candidate_ids",
					Message: fmt.Sprintf("coverage references missing candidate %q", id),
				})
			} else {
				referenced[id] = true
			}
		}
		if module.Disposition == "unresolved" {
			unresolved++
			snapshot.Diagnostics = append(snapshot.Diagnostics, Diagnostic{
				Code: CodeReviewUnresolved, Level: "error", Path: path,
				Message: "unresolved coverage blocks materialization",
			})
		}
	}
	missing, duplicates, covered := 0, 0, 0
	for id := range t.segmentByID {
		switch counts[id] {
		case 0:
			missing++
			snapshot.Diagnostics = append(snapshot.Diagnostics, Diagnostic{
				Code: CodeCoverageMissing, Level: "error", Path: "Evergreen.review.coverage",
				Message: fmt.Sprintf("segment %q is not covered", id),
			})
		case 1:
			covered++
		default:
			duplicates++
			snapshot.Diagnostics = append(snapshot.Diagnostics, Diagnostic{
				Code: CodeCoverageDuplicate, Level: "error", Path: "Evergreen.review.coverage",
				Message: fmt.Sprintf("segment %q is covered %d times", id, counts[id]),
			})
		}
	}
	unreferenced := 0
	for id, candidate := range t.candidateByID {
		if candidate.envelope.Candidate.State == "discarded" {
			continue
		}
		if !referenced[id] {
			unreferenced++
			snapshot.Diagnostics = append(snapshot.Diagnostics, Diagnostic{
				Code: CodeCoverageInvalid, Level: "error", Path: "Evergreen.review.coverage",
				Message: fmt.Sprintf("candidate %q is not referenced by coverage", id),
			})
		}
	}
	snapshot.Summary = CoverageSummary{
		SegmentCount: len(t.segments), CandidateCount: len(t.candidates),
		CoveredSegments: covered, MissingSegments: missing,
		DuplicateSegments: duplicates, UnresolvedModules: unresolved,
		UnreferencedDrafts: unreferenced,
	}
}

func validateReviewStructure(snapshot ReviewSnapshot) error {
	var diagnostics []Diagnostic
	for _, diagnostic := range snapshot.Diagnostics {
		if diagnostic.Level == "error" &&
			diagnostic.Code != CodeCandidateStale &&
			diagnostic.Code != CodeCandidatePayload &&
			diagnostic.Code != CodeReviewUnresolved {
			diagnostics = append(diagnostics, diagnostic)
		}
	}
	if len(diagnostics) > 0 {
		return &DiagnosticError{Diagnostics: diagnostics}
	}
	return nil
}

func (t *reviewTree) markStaleCandidates() {
	for _, candidate := range t.candidates {
		if candidate.envelope.Candidate.State == "discarded" {
			continue
		}
		if candidate.isStale(t.segmentByID) {
			candidate.envelope.Candidate.State = "stale"
			_ = candidate.writeEnvelope()
		}
	}
}

func (c *reviewCandidateNode) isStale(segments map[LogicalID]*reviewSegmentNode) bool {
	if c.envelope.Candidate.PayloadHash != candidatePayloadHash(c.node) {
		return true
	}
	for _, id := range c.envelope.Candidate.SegmentRefs {
		segment, ok := segments[id]
		if !ok || c.envelope.Candidate.RefHashes[id] != segment.currentHash {
			return true
		}
	}
	return false
}

func (c *reviewCandidateNode) refreshPayloadHash() {
	c.payloadHash = candidatePayloadHash(c.node)
	c.envelope.Candidate.PayloadHash = c.payloadHash
}

func (c *reviewCandidateNode) captureCurrentRefs(
	segments map[LogicalID]*reviewSegmentNode,
	diff *ReviewDiff,
) {
	refHashes := make(map[LogicalID]string, len(c.envelope.Candidate.SegmentRefs))
	for _, id := range c.envelope.Candidate.SegmentRefs {
		before := c.envelope.Candidate.RefHashes[id]
		after := ""
		if segment, ok := segments[id]; ok {
			after = segment.currentHash
		}
		if before != after {
			diff.RefHashes = append(diff.RefHashes, RefHashDiff{SegmentID: id, Before: before, After: after})
		}
		refHashes[id] = after
	}
	c.envelope.Candidate.RefHashes = refHashes
}

func (t *reviewTree) materializedClaim(
	ctx context.Context,
	repository Repository,
	candidate *reviewCandidateNode,
	request ReviewMaterializeRequest,
) ([]byte, bool, error) {
	var root RawObject
	create := request.ClaimBase == ""
	var existingEnvelope *DocumentEnvelope
	if create {
		if strings.TrimSpace(request.ClaimPath) == "" || strings.TrimSpace(request.ClaimDocumentID) == "" {
			return nil, false, validationError(CodeInvalidPlan, "claim_path", "new claim requires claim path and document ID")
		}
		root = RawObject{}
		root["ID"], _ = json.Marshal(request.ClaimDocumentID)
		root["Type"], _ = json.Marshal("NodeDocument")
		root["Spec"], _ = json.Marshal("5")
	} else {
		raw, err := repository.Load(ctx, request.ClaimID)
		if err != nil {
			return nil, false, err
		}
		hash, err := SemanticHash(raw)
		if err != nil {
			return nil, false, err
		}
		if hash != request.ClaimBase {
			return nil, false, validationError(CodeBaseMismatch, "claim_base", "Claim semantic base hash is stale")
		}
		document, err := DecodeSY(raw, t.registry)
		if err != nil {
			return nil, false, err
		}
		existingEnvelope = document.Envelope
		root, err = decodeRawObject(raw)
		if err != nil {
			return nil, false, err
		}
	}

	var claimDocumentID string
	if err := json.Unmarshal(root["ID"], &claimDocumentID); err != nil {
		return nil, false, err
	}
	children, err := materializedPayload(candidate.node.children, claimDocumentID)
	if err != nil {
		return nil, false, err
	}
	root["Children"], _ = json.Marshal(children)
	properties := map[string]any{"title": candidate.envelope.Candidate.Title}
	root["Properties"], _ = json.Marshal(properties)

	provenanceBySource := map[LogicalID][]LogicalID{}
	for _, segmentID := range candidate.envelope.Candidate.SegmentRefs {
		segment := t.segmentByID[segmentID]
		if segment == nil || segment.envelope.Segment.SourceRef.SourceID == "" {
			continue
		}
		sourceID := segment.envelope.Segment.SourceRef.SourceID
		provenanceBySource[sourceID] = append(provenanceBySource[sourceID], segmentID)
	}
	if len(provenanceBySource) == 0 {
		return nil, false, validationError(CodeInvalidPlan, "candidate.segment_refs", "candidate has no source-backed segment")
	}
	reason := strings.TrimSpace(candidate.envelope.Candidate.Reason)
	if reason == "" {
		reason = "Materialized from reviewed Candidate " + string(candidate.envelope.Candidate.CandidateID)
	}
	sources := make([]LogicalID, 0, len(provenanceBySource))
	for sourceID := range provenanceBySource {
		sources = append(sources, sourceID)
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i] < sources[j] })
	provenance := make([]Provenance, 0, len(sources))
	materialEdges := make([]TypedEdge, 0, len(sources))
	for _, sourceID := range sources {
		refs := provenanceBySource[sourceID]
		provenance = append(provenance, Provenance{
			SourceID: sourceID, NoteID: request.NoteID,
			SegmentRefs: append([]LogicalID(nil), refs...), Reason: reason,
		})
		materialEdges = append(materialEdges, TypedEdge{
			ID:     EdgeID(deterministicEdgeID(request.ClaimID, sourceID, refs)),
			Schema: MaterialSchema,
			Target: EntityRef{EntityType: EntitySource, LogicalID: sourceID},
			Type:   "support", Reason: reason,
			Context:   EdgeContext{NoteID: request.NoteID, SegmentRefs: append([]LogicalID(nil), refs...)},
			CreatedAt: request.CreatedAt, UpdatedAt: request.CreatedAt,
		})
	}
	kindData := json.RawMessage(`{}`)
	if candidate.envelope.Candidate.ClaimKind == OpinionKind {
		kindData = json.RawMessage(`{"validation":{"status":"pending"}}`)
	}
	relations := Relations{Outgoing: materialEdges}
	revision := uint64(1)
	if existingEnvelope != nil {
		revision = existingEnvelope.Entity.SemanticRevision + 1
		for _, edge := range existingEnvelope.Relations.Outgoing {
			if edge.Schema != MaterialSchema {
				relations.Outgoing = append(relations.Outgoing, edge)
			}
		}
	}
	envelope := &DocumentEnvelope{
		Spec: DocumentSpec,
		Entity: Entity{
			LogicalID: request.ClaimID, EntityType: EntityClaim,
			Schema: ClaimSchema, SemanticRevision: revision,
		},
		Claim: &Claim{
			ClaimKind:  candidate.envelope.Candidate.ClaimKind,
			KindSchema: candidate.envelope.Candidate.KindSchema,
			Status:     "active", Tags: append([]string(nil), candidate.envelope.Candidate.Tags...),
			KindData: kindData,
		},
		Provenance: provenance, Relations: relations,
	}
	envelopeRaw, err := MarshalDocumentEnvelope(envelope)
	if err != nil {
		return nil, false, err
	}
	root[evergreenJSONKey] = envelopeRaw
	encoded, err := json.Marshal(root)
	return encoded, create, err
}

func parseReviewNode(raw []byte) (*reviewNode, error) {
	object, err := decodeRawObject(raw)
	if err != nil {
		return nil, err
	}
	node := &reviewNode{object: object}
	if childrenRaw, ok := object["Children"]; ok {
		var children []json.RawMessage
		if err = json.Unmarshal(childrenRaw, &children); err != nil {
			return nil, err
		}
		for _, child := range children {
			parsed, parseErr := parseReviewNode(child)
			if parseErr != nil {
				return nil, parseErr
			}
			node.children = append(node.children, parsed)
		}
	}
	return node, nil
}

func (n *reviewNode) encode() ([]byte, error) {
	object := cloneRawObject(n.object)
	if len(n.children) > 0 || object["Children"] != nil {
		children := make([]json.RawMessage, 0, len(n.children))
		for _, child := range n.children {
			raw, err := child.encode()
			if err != nil {
				return nil, err
			}
			children = append(children, raw)
		}
		object["Children"], _ = json.Marshal(children)
	}
	return json.Marshal(object)
}

func (n *reviewNode) containsManagedEnvelope() bool {
	if _, exists := n.object[evergreenJSONKey]; exists {
		return true
	}
	for _, child := range n.children {
		if child.containsManagedEnvelope() {
			return true
		}
	}
	return false
}

func (t *reviewTree) encode() ([]byte, error) {
	return t.root.encode()
}

func (t *reviewTree) writeDocumentEnvelope() error {
	raw, err := MarshalDocumentEnvelope(t.document.Envelope)
	if err != nil {
		return err
	}
	t.root.object[evergreenJSONKey] = raw
	return nil
}

func (s *reviewSegmentNode) writeEnvelope() error {
	raw, err := MarshalBlockEnvelope(s.envelope)
	if err != nil {
		return err
	}
	s.node.object[evergreenJSONKey] = raw
	return nil
}

func (c *reviewCandidateNode) writeEnvelope() error {
	raw, err := MarshalBlockEnvelope(c.envelope)
	if err != nil {
		return err
	}
	c.node.object[evergreenJSONKey] = raw
	return nil
}

func segmentNodeHash(node *reviewNode) string {
	value := nodeSemanticValue(node, true)
	return canonicalValueHash(value)
}

func candidatePayloadHash(node *reviewNode) string {
	children := make([]any, 0, len(node.children))
	for _, child := range node.children {
		children = append(children, nodeSemanticValue(child, true))
	}
	return canonicalValueHash(children)
}

func candidatePayload(node *reviewNode) (json.RawMessage, error) {
	children := make([]json.RawMessage, 0, len(node.children))
	for _, child := range node.children {
		raw, err := child.encode()
		if err != nil {
			return nil, err
		}
		children = append(children, raw)
	}
	raw, err := json.Marshal(children)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

func nodeSemanticValue(node *reviewNode, includeSelf bool) any {
	object := map[string]any{}
	if includeSelf {
		for key, raw := range node.object {
			switch key {
			case "ID", "Evergreen", "Updated", "Created", "Hash", "Children":
				continue
			}
			var value any
			if json.Unmarshal(raw, &value) == nil {
				object[key] = value
			}
		}
	}
	children := make([]any, 0, len(node.children))
	for _, child := range node.children {
		children = append(children, nodeSemanticValue(child, true))
	}
	if includeSelf {
		if len(children) > 0 {
			object["Children"] = children
		}
		return object
	}
	return children
}

func canonicalValueHash(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func deterministicLineageID(mutation string, previous, next []LogicalID, hash string) LogicalID {
	raw, _ := json.Marshal([]any{mutation, previous, next, hash})
	sum := sha256.Sum256(raw)
	return LogicalID("lineage-" + hex.EncodeToString(sum[:8]))
}

func deterministicEdgeID(claimID, sourceID LogicalID, refs []LogicalID) string {
	raw, _ := json.Marshal([]any{claimID, sourceID, refs})
	sum := sha256.Sum256(raw)
	return "edge-" + hex.EncodeToString(sum[:12])
}

func coverageCandidateIDs(module CoverageModule) []LogicalID {
	ids := append([]LogicalID(nil), module.CandidateIDs...)
	if module.CandidateID != "" {
		ids = append(ids, module.CandidateID)
	}
	seen := map[LogicalID]struct{}{}
	out := ids[:0]
	for _, id := range ids {
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func cloneCoverage(in []CoverageModule) []CoverageModule {
	out := make([]CoverageModule, len(in))
	for index := range in {
		out[index] = in[index]
		out[index].SegmentRefs = append([]LogicalID(nil), in[index].SegmentRefs...)
		out[index].CandidateIDs = append([]LogicalID(nil), in[index].CandidateIDs...)
		out[index].Extra = cloneRawObject(in[index].Extra)
	}
	return out
}

func cloneCandidateMetadata(in *CandidateMetadata) *CandidateMetadata {
	if in == nil {
		return nil
	}
	out := *in
	out.SegmentRefs = append([]LogicalID(nil), in.SegmentRefs...)
	out.Tags = append([]string(nil), in.Tags...)
	out.RefHashes = make(map[LogicalID]string, len(in.RefHashes))
	for id, hash := range in.RefHashes {
		out.RefHashes[id] = hash
	}
	out.Extra = cloneRawObject(in.Extra)
	return &out
}
