package evergreencore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

const NoteCreateCommand = "note.create"

type NoteCreateSegment struct {
	SegmentID  LogicalID       `json:"segment_id"`
	Role       string          `json:"role"`
	Locator    *Locator        `json:"locator,omitempty"`
	Annotation *Annotation     `json:"annotation,omitempty"`
	Content    json.RawMessage `json:"content"`
}

type NoteCreateCandidate struct {
	CandidateID LogicalID       `json:"candidate_id"`
	ClaimKind   ClaimKind       `json:"claim_kind"`
	Title       string          `json:"title,omitempty"`
	LogicalSlug string          `json:"logical_slug,omitempty"`
	SegmentRefs []LogicalID     `json:"segment_refs"`
	Payload     json.RawMessage `json:"payload"`
	Relation    string          `json:"relation,omitempty"`
	Reason      string          `json:"reason,omitempty"`
	Tags        []string        `json:"tags,omitempty"`
}

type NoteCreateRequest struct {
	OperationID string                `json:"operation_id"`
	LogicalID   LogicalID             `json:"logical_id"`
	DocumentID  string                `json:"document_id"`
	Path        string                `json:"path"`
	SourceID    LogicalID             `json:"source_id"`
	Title       string                `json:"title"`
	Segments    []NoteCreateSegment   `json:"segments"`
	Candidates  []NoteCreateCandidate `json:"candidates"`
	Coverage    []CoverageModule      `json:"coverage"`
}

type PlannedNoteCreate struct {
	NoteID    LogicalID        `json:"note_id"`
	SourceID  LogicalID        `json:"source_id"`
	Path      string           `json:"path"`
	Operation PlannedOperation `json:"operation"`
	Snapshot  ReviewSnapshot   `json:"snapshot"`
}

func (s *AuthorityService) PlanNoteCreate(
	ctx context.Context,
	principal Principal,
	request NoteCreateRequest,
) (PlannedNoteCreate, error) {
	if s == nil || s.host == nil {
		return PlannedNoteCreate{}, validationError(CodeInvalidPlan, "host", "authority host is unavailable")
	}
	if err := validateNoteCreateRequest(request); err != nil {
		return PlannedNoteCreate{}, err
	}
	after, snapshot, err := buildNoteCreate(request, s.registry)
	if err != nil {
		return PlannedNoteCreate{}, err
	}
	operation, err := s.Plan(ctx, principal, PlanRequest{
		Protocol: ChangePlanProtocol, OperationID: request.OperationID,
		Command: NoteCreateCommand,
		Base:    []BaseRef{{LogicalID: request.LogicalID, Expected: "absent"}},
		Writes: []WriteInput{{
			LogicalID: request.LogicalID, Path: request.Path, After: after,
		}},
	})
	if err != nil {
		return PlannedNoteCreate{}, err
	}
	return PlannedNoteCreate{
		NoteID: request.LogicalID, SourceID: request.SourceID,
		Path: request.Path, Operation: operation, Snapshot: snapshot,
	}, nil
}

func validateNoteCreateRequest(request NoteCreateRequest) error {
	if strings.TrimSpace(request.OperationID) == "" {
		return validationError(CodeInvalidPlan, "operation_id", "operation ID is required")
	}
	if !logicalIDMatchesEntity(request.LogicalID, EntityNote) {
		return validationError(CodeInvalidEnvelope, "logical_id", "Note logical ID must use n-")
	}
	if strings.TrimSpace(request.DocumentID) == "" {
		return validationError(CodeInvalidEnvelope, "document_id", "Note document ID is required")
	}
	if err := validateAuthorityPath(request.Path); err != nil {
		return validationError(CodeInvalidPlan, "path", err.Error())
	}
	if !logicalIDMatchesEntity(request.SourceID, EntitySource) {
		return validationError(CodeInvalidEnvelope, "source_id", "Note Source ID must use s-")
	}
	if strings.TrimSpace(request.Title) == "" {
		return validationError(CodeInvalidEnvelope, "title", "Note title is required")
	}
	if len(request.Segments) == 0 {
		return validationError(CodeInvalidBlock, "segments", "Note requires at least one managed segment")
	}
	return nil
}

func buildNoteCreate(
	request NoteCreateRequest,
	registry *Registry,
) ([]byte, ReviewSnapshot, error) {
	if registry == nil {
		registry = DefaultRegistry()
	}
	segmentIDs := map[LogicalID]struct{}{}
	segmentHashes := map[LogicalID]string{}
	children := make([]json.RawMessage, 0, len(request.Segments)+len(request.Candidates))
	for index, input := range request.Segments {
		path := fmt.Sprintf("segments[%d]", index)
		if !hasLogicalPrefix(input.SegmentID, "seg-") {
			return nil, ReviewSnapshot{}, validationError(
				CodeInvalidBlock, path+".segment_id", "segment ID must use seg-")
		}
		if _, duplicate := segmentIDs[input.SegmentID]; duplicate {
			return nil, ReviewSnapshot{}, validationError(
				CodeDuplicateID, path+".segment_id", fmt.Sprintf("duplicate segment %q", input.SegmentID))
		}
		segmentIDs[input.SegmentID] = struct{}{}
		content, err := parseCreateNodeArray(input.Content, path+".content")
		if err != nil {
			return nil, ReviewSnapshot{}, err
		}
		metadata := &SegmentMetadata{SegmentID: input.SegmentID}
		switch input.Role {
		case "note_segment":
			if input.Locator == nil || input.Annotation != nil ||
				strings.TrimSpace(input.Locator.Kind) == "" ||
				strings.TrimSpace(input.Locator.Value) == "" {
				return nil, ReviewSnapshot{}, validationError(
					CodeInvalidBlock, path, "source segment requires a locator and no annotation")
			}
			metadata.SourceRef = SourceLocator{
				SourceID: request.SourceID,
				Locator:  *cloneLocator(input.Locator),
			}
		case "agent_annotation":
			if input.Locator != nil || input.Annotation == nil ||
				strings.TrimSpace(input.Annotation.Kind) == "" {
				return nil, ReviewSnapshot{}, validationError(
					CodeInvalidBlock, path, "agent annotation requires annotation metadata and no locator")
			}
			metadata.Annotation = cloneAnnotation(input.Annotation)
		default:
			return nil, ReviewSnapshot{}, validationError(
				CodeInvalidBlock, path+".role", fmt.Sprintf("unsupported segment role %q", input.Role))
		}
		node := managedCreateContainer(
			StablePhysicalID(string(request.LogicalID)+"/segment/"+string(input.SegmentID)),
			content,
		)
		metadata.NormalizedHash = segmentNodeHash(node)
		envelope, err := MarshalBlockEnvelope(&BlockEnvelope{
			Spec: BlockSpec, Role: input.Role, Segment: metadata,
		})
		if err != nil {
			return nil, ReviewSnapshot{}, err
		}
		node.object[evergreenJSONKey] = envelope
		raw, err := node.encode()
		if err != nil {
			return nil, ReviewSnapshot{}, err
		}
		children = append(children, raw)
		segmentHashes[input.SegmentID] = metadata.NormalizedHash
	}

	candidateIDs := map[LogicalID]struct{}{}
	for index, input := range request.Candidates {
		path := fmt.Sprintf("candidates[%d]", index)
		if !hasLogicalPrefix(input.CandidateID, "cand-") {
			return nil, ReviewSnapshot{}, validationError(
				CodeInvalidBlock, path+".candidate_id", "Candidate ID must use cand-")
		}
		if _, duplicate := candidateIDs[input.CandidateID]; duplicate {
			return nil, ReviewSnapshot{}, validationError(
				CodeDuplicateID, path+".candidate_id", fmt.Sprintf("duplicate Candidate %q", input.CandidateID))
		}
		candidateIDs[input.CandidateID] = struct{}{}
		descriptor, ok := registry.Descriptor(input.ClaimKind)
		if !ok {
			return nil, ReviewSnapshot{}, validationError(
				CodeClaimKindUnavailable, path+".claim_kind", "Candidate claim kind is unavailable")
		}
		if len(input.SegmentRefs) == 0 {
			return nil, ReviewSnapshot{}, validationError(
				CodeInvalidBlock, path+".segment_refs", "Candidate requires at least one segment")
		}
		refs := make([]LogicalID, 0, len(input.SegmentRefs))
		refHashes := make(map[LogicalID]string, len(input.SegmentRefs))
		for refIndex, id := range input.SegmentRefs {
			if _, duplicate := refHashes[id]; duplicate {
				return nil, ReviewSnapshot{}, validationError(
					CodeDuplicateID,
					fmt.Sprintf("%s.segment_refs[%d]", path, refIndex),
					fmt.Sprintf("duplicate Candidate segment reference %q", id),
				)
			}
			hash, exists := segmentHashes[id]
			if !exists {
				return nil, ReviewSnapshot{}, validationError(
					CodeInvalidBlock,
					fmt.Sprintf("%s.segment_refs[%d]", path, refIndex),
					fmt.Sprintf("Candidate references missing segment %q", id),
				)
			}
			refs = append(refs, id)
			refHashes[id] = hash
		}
		if input.Relation != "" && !containsString([]string{"support", "against", "context"}, input.Relation) {
			return nil, ReviewSnapshot{}, validationError(
				CodeInvalidBlock, path+".relation", fmt.Sprintf("invalid material relation %q", input.Relation))
		}
		payload, err := parseCreateNodeArray(input.Payload, path+".payload")
		if err != nil {
			return nil, ReviewSnapshot{}, err
		}
		node := managedCreateContainer(
			StablePhysicalID(string(request.LogicalID)+"/candidate/"+string(input.CandidateID)),
			payload,
		)
		metadata := &CandidateMetadata{
			CandidateID: input.CandidateID,
			ClaimKind:   input.ClaimKind, KindSchema: descriptor.Schema,
			Title: input.Title, LogicalSlug: input.LogicalSlug,
			SegmentRefs: refs, RefHashes: refHashes,
			Relation: input.Relation, Reason: input.Reason,
			Tags: nonNilStrings(input.Tags), State: "draft",
		}
		metadata.PayloadHash = candidatePayloadHash(node)
		envelope, err := MarshalBlockEnvelope(&BlockEnvelope{
			Spec: BlockSpec, Role: "candidate", Candidate: metadata,
		})
		if err != nil {
			return nil, ReviewSnapshot{}, err
		}
		node.object[evergreenJSONKey] = envelope
		raw, err := node.encode()
		if err != nil {
			return nil, ReviewSnapshot{}, err
		}
		children = append(children, raw)
	}

	coverage, err := canonicalCreateCoverage(request.Coverage)
	if err != nil {
		return nil, ReviewSnapshot{}, err
	}
	noteMetadata, _ := json.Marshal(map[string]any{"source": request.SourceID})
	envelope := &DocumentEnvelope{
		Spec: DocumentSpec,
		Entity: Entity{
			LogicalID: request.LogicalID, EntityType: EntityNote,
			Schema: NoteSchema, SemanticRevision: 1,
		},
		Relations: Relations{Outgoing: []TypedEdge{}},
		Review:    &NoteReview{Spec: NoteReviewSpec, Coverage: coverage},
		Extension: RawObject{"note": noteMetadata},
	}
	root, err := json.Marshal(map[string]any{
		"ID": request.DocumentID, "Type": "NodeDocument", "Spec": "5",
		"Properties": map[string]string{"title": request.Title},
		"Children":   children,
	})
	if err != nil {
		return nil, ReviewSnapshot{}, err
	}
	after, err := EncodeSY(root, envelope, registry)
	if err != nil {
		return nil, ReviewSnapshot{}, err
	}
	if err = validateDocumentPhysicalIDs(after); err != nil {
		return nil, ReviewSnapshot{}, err
	}
	snapshot, err := InspectNoteReview(after, registry)
	if err != nil {
		return nil, ReviewSnapshot{}, err
	}
	if err = validateReviewStructure(snapshot); err != nil {
		return nil, ReviewSnapshot{}, err
	}
	return after, snapshot, nil
}

func parseCreateNodeArray(raw json.RawMessage, path string) ([]*reviewNode, error) {
	var children []json.RawMessage
	if err := json.Unmarshal(raw, &children); err != nil || len(children) == 0 {
		return nil, validationError(CodeInvalidBlock, path, "native node array must be nonempty")
	}
	result := make([]*reviewNode, 0, len(children))
	for index, child := range children {
		node, err := parseReviewNode(child)
		if err != nil {
			return nil, validationError(
				CodeInvalidBlock, fmt.Sprintf("%s[%d]", path, index), err.Error())
		}
		if node.containsManagedEnvelope() {
			return nil, validationError(
				CodeUnauthorized, fmt.Sprintf("%s[%d].Evergreen", path, index),
				"new Note content may not supply managed metadata",
			)
		}
		var kind string
		_ = json.Unmarshal(node.object["Type"], &kind)
		if isSuperBlockMarker(kind) {
			return nil, validationError(
				CodeInvalidBlock, fmt.Sprintf("%s[%d].Type", path, index),
				"container markers are generated by the core",
			)
		}
		result = append(result, node)
	}
	return result, nil
}

func managedCreateContainer(id string, content []*reviewNode) *reviewNode {
	node := &reviewNode{object: RawObject{}}
	node.object["ID"], _ = json.Marshal(id)
	node.object["Type"], _ = json.Marshal("NodeSuperBlock")
	node.object["Properties"], _ = json.Marshal(map[string]string{"id": id})
	open, _ := parseReviewNode(json.RawMessage(`{"Type":"NodeSuperBlockOpenMarker"}`))
	layout, _ := parseReviewNode(json.RawMessage(`{"Type":"NodeSuperBlockLayoutMarker","Data":"row"}`))
	close, _ := parseReviewNode(json.RawMessage(`{"Type":"NodeSuperBlockCloseMarker"}`))
	node.children = append([]*reviewNode{open, layout}, content...)
	node.children = append(node.children, close)
	return node
}

func canonicalCreateCoverage(input []CoverageModule) ([]CoverageModule, error) {
	coverage := cloneCoverage(input)
	for index := range coverage {
		module := &coverage[index]
		if len(module.Extra) != 0 {
			return nil, validationError(
				CodeUnauthorized, fmt.Sprintf("coverage[%d]", index),
				"new coverage may not supply unknown metadata",
			)
		}
		ids := append([]LogicalID(nil), module.CandidateIDs...)
		if module.CandidateID != "" {
			ids = append(ids, module.CandidateID)
		}
		seen := map[LogicalID]struct{}{}
		for candidateIndex, id := range ids {
			if _, duplicate := seen[id]; duplicate {
				return nil, validationError(
					CodeDuplicateID,
					fmt.Sprintf("coverage[%d].candidate_ids[%d]", index, candidateIndex),
					fmt.Sprintf("duplicate coverage Candidate %q", id),
				)
			}
			seen[id] = struct{}{}
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		module.CandidateID = ""
		module.CandidateIDs = ids
		if len(ids) == 1 {
			module.CandidateID = ids[0]
			module.CandidateIDs = nil
		}
	}
	review := &NoteReview{Spec: NoteReviewSpec, Coverage: coverage}
	if err := validateNoteReviewEnvelope(review); err != nil {
		return nil, err
	}
	return coverage, nil
}

func authorizeNoteCreation(after *SYDocument, registry *Registry) error {
	if after == nil || after.Envelope == nil ||
		after.Envelope.Entity.EntityType != EntityNote ||
		after.Envelope.Entity.Schema != NoteSchema {
		return validationError(CodeUnauthorized, "command", "Note create requires a canonical Note target")
	}
	envelope := after.Envelope
	if envelope.Entity.SemanticRevision != 1 ||
		len(envelope.Entity.Extra) != 0 ||
		envelope.Claim != nil ||
		len(envelope.Provenance) != 0 ||
		len(envelope.Relations.Outgoing) != 0 ||
		len(envelope.Relations.Extra) != 0 ||
		envelope.Review == nil ||
		len(envelope.Review.Lineage) != 0 ||
		len(envelope.Review.Extra) != 0 ||
		len(envelope.Extra) != 0 ||
		len(envelope.Extension) != 1 {
		return validationError(CodeUnauthorized, "note", "new Note must use the canonical initial envelope")
	}
	sourceID, err := canonicalNoteSource(envelope)
	if err != nil {
		return err
	}
	root, err := parseReviewNode(after.Raw)
	if err != nil {
		return err
	}
	var rootType, rootSpec, documentID string
	_ = json.Unmarshal(root.object["Type"], &rootType)
	_ = json.Unmarshal(root.object["Spec"], &rootSpec)
	_ = json.Unmarshal(root.object["ID"], &documentID)
	properties, propertiesErr := decodeRawObject(root.object["Properties"])
	var title string
	if propertiesErr == nil {
		_ = json.Unmarshal(properties["title"], &title)
	}
	if rootType != "NodeDocument" || rootSpec != "5" ||
		strings.TrimSpace(documentID) == "" ||
		propertiesErr != nil || len(properties) != 1 ||
		strings.TrimSpace(title) == "" ||
		len(root.object) != 6 {
		return validationError(CodeUnauthorized, "document", "new Note must use the canonical native document shape")
	}
	tree, err := parseReviewTree(after.Raw, registry)
	if err != nil {
		return err
	}
	if len(root.children) != len(tree.segments)+len(tree.candidates) ||
		len(tree.segments) == 0 {
		return validationError(CodeUnauthorized, "Children", "new Note may contain only managed segments and Candidates")
	}
	for index, child := range root.children {
		envelopeRaw, exists := child.object[evergreenJSONKey]
		if !exists {
			return validationError(
				CodeUnauthorized, fmt.Sprintf("Children[%d]", index),
				"new Note may contain only managed blocks",
			)
		}
		block, decodeErr := UnmarshalBlockEnvelope(envelopeRaw)
		if decodeErr != nil {
			return decodeErr
		}
		if len(block.Extra) != 0 || len(block.Extension) != 0 ||
			(block.Segment != nil && len(block.Segment.Extra) != 0) ||
			(block.Candidate != nil && len(block.Candidate.Extra) != 0) {
			return validationError(CodeUnauthorized, "Children.Evergreen", "new managed blocks may not supply unknown metadata")
		}
		if !canonicalManagedCreateContainer(child) {
			return validationError(CodeUnauthorized, "Children", "managed block container is not canonical")
		}
		for _, nested := range child.children {
			if nested.containsManagedEnvelope() {
				return validationError(CodeUnauthorized, "Children.Evergreen", "managed metadata must remain on top-level containers")
			}
		}
		var blockID string
		_ = json.Unmarshal(child.object["ID"], &blockID)
		switch block.Role {
		case "note_segment", "agent_annotation":
			expected := StablePhysicalID(string(envelope.Entity.LogicalID) + "/segment/" + string(block.Segment.SegmentID))
			if blockID != expected || block.Segment.NormalizedHash != segmentNodeHash(child) {
				return validationError(CodeUnauthorized, "segment", "segment identity and normalized hash must be core-generated")
			}
			if block.Role == "note_segment" && block.Segment.SourceRef.SourceID != sourceID {
				return validationError(CodeUnauthorized, "segment.source_ref", "source segment must retain the Note Source binding")
			}
		case "candidate":
			candidate := block.Candidate
			expected := StablePhysicalID(string(envelope.Entity.LogicalID) + "/candidate/" + string(candidate.CandidateID))
			if blockID != expected ||
				candidate.State != "draft" ||
				candidate.MaterializedClaimID != "" ||
				candidate.PayloadHash != candidatePayloadHash(child) ||
				len(candidate.RefHashes) != len(candidate.SegmentRefs) {
				return validationError(CodeUnauthorized, "candidate", "Candidate hashes, draft state, and output must be core-generated")
			}
			for _, id := range candidate.SegmentRefs {
				segment := tree.segmentByID[id]
				if segment == nil || candidate.RefHashes[id] != segment.currentHash {
					return validationError(CodeUnauthorized, "candidate.ref_hashes", "Candidate ref hashes must match current segments")
				}
			}
		default:
			return validationError(CodeUnauthorized, "Children.Evergreen.role", "unsupported managed block role")
		}
	}
	snapshot, err := tree.snapshot(after.Raw)
	if err != nil {
		return err
	}
	if err = validateReviewStructure(snapshot); err != nil {
		return err
	}
	return validateDocumentPhysicalIDs(after.Raw)
}

func (s *AuthorityService) authorizeNoteCreateDependencies(
	ctx context.Context,
	operation PlannedOperation,
) error {
	if operation.Plan.Command != NoteCreateCommand {
		return nil
	}
	if len(operation.Images) != 1 {
		return validationError(CodeUnauthorized, "writes", "Note create requires exactly one Note write")
	}
	target := operation.Images[0]
	document, err := DecodeSY(target.Bytes, s.registry)
	if err != nil {
		return err
	}
	sourceID, err := canonicalNoteSource(document.Envelope)
	if err != nil {
		return err
	}
	sourceRaw, err := s.host.Load(ctx, sourceID)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return validationError(CodeInvalidEnvelope, "source_id", fmt.Sprintf("Source %q does not exist", sourceID))
		}
		return err
	}
	source, err := DecodeSY(sourceRaw, s.registry)
	if err != nil {
		return err
	}
	if source.ReadOnly {
		return &DiagnosticError{Diagnostics: source.Diagnostics}
	}
	if source.Envelope == nil || source.Envelope.Entity.EntityType != EntitySource {
		return validationError(CodeInvalidEnvelope, "source_id", fmt.Sprintf("%q is not a Source", sourceID))
	}

	targetPhysical, err := documentPhysicalIDs(target.Bytes)
	if err != nil {
		return err
	}
	ids, err := s.host.List(ctx)
	if err != nil {
		return err
	}
	seenPaths := map[string]struct{}{}
	for _, id := range ids {
		location, resolveErr := s.host.Resolve(ctx, id)
		if resolveErr != nil {
			return resolveErr
		}
		if _, duplicate := seenPaths[location.Path]; duplicate {
			continue
		}
		seenPaths[location.Path] = struct{}{}
		raw, loadErr := s.host.Load(ctx, id)
		if loadErr != nil {
			return loadErr
		}
		existing, decodeErr := DecodeSY(raw, s.registry)
		if decodeErr != nil {
			return decodeErr
		}
		if existing.Envelope != nil &&
			existing.Envelope.Entity.LogicalID == target.LogicalID {
			continue
		}
		if existing.Envelope != nil &&
			existing.Envelope.Entity.EntityType == EntityNote &&
			noteSourceID(existing.Envelope) == sourceID {
			return validationError(
				CodeDuplicateID, "source_id",
				fmt.Sprintf("Source %q already has Note %q", sourceID, existing.Envelope.Entity.LogicalID),
			)
		}
		existingPhysical, physicalErr := documentPhysicalIDs(raw)
		if physicalErr != nil {
			return physicalErr
		}
		for physicalID := range targetPhysical {
			if _, collision := existingPhysical[physicalID]; collision {
				return validationError(
					CodeDuplicateID, "document_id",
					fmt.Sprintf("physical ID %q is already occupied", physicalID),
				)
			}
		}
	}
	return nil
}

func canonicalNoteSource(envelope *DocumentEnvelope) (LogicalID, error) {
	if envelope == nil || len(envelope.Extension) != 1 {
		return "", validationError(CodeUnauthorized, "Evergreen.extension.note", "new Note requires one native Source binding")
	}
	raw, exists := envelope.Extension["note"]
	if !exists {
		return "", validationError(CodeUnauthorized, "Evergreen.extension.note", "new Note requires native note metadata")
	}
	object, err := decodeRawObject(raw)
	if err != nil || len(object) != 1 {
		return "", validationError(CodeUnauthorized, "Evergreen.extension.note", "new Note metadata must contain only source")
	}
	var sourceID LogicalID
	if err = json.Unmarshal(object["source"], &sourceID); err != nil ||
		!logicalIDMatchesEntity(sourceID, EntitySource) {
		return "", validationError(CodeInvalidEnvelope, "Evergreen.extension.note.source", "Note Source ID must use s-")
	}
	return sourceID, nil
}

func canonicalManagedCreateContainer(node *reviewNode) bool {
	if node == nil || len(node.children) < 4 {
		return false
	}
	var kind, propertyID, nodeID string
	_ = json.Unmarshal(node.object["Type"], &kind)
	_ = json.Unmarshal(node.object["ID"], &nodeID)
	properties, err := decodeRawObject(node.object["Properties"])
	if err != nil || len(properties) != 1 {
		return false
	}
	_ = json.Unmarshal(properties["id"], &propertyID)
	if kind != "NodeSuperBlock" || nodeID == "" || propertyID != nodeID ||
		len(node.object) != 5 {
		return false
	}
	var open, layout, close string
	_ = json.Unmarshal(node.children[0].object["Type"], &open)
	_ = json.Unmarshal(node.children[1].object["Type"], &layout)
	_ = json.Unmarshal(node.children[len(node.children)-1].object["Type"], &close)
	var layoutValue string
	_ = json.Unmarshal(node.children[1].object["Data"], &layoutValue)
	return open == "NodeSuperBlockOpenMarker" &&
		layout == "NodeSuperBlockLayoutMarker" &&
		layoutValue == "row" &&
		close == "NodeSuperBlockCloseMarker"
}

func isSuperBlockMarker(kind string) bool {
	switch kind {
	case "NodeSuperBlockOpenMarker", "NodeSuperBlockLayoutMarker", "NodeSuperBlockCloseMarker":
		return true
	default:
		return false
	}
}

func cloneLocator(locator *Locator) *Locator {
	if locator == nil {
		return nil
	}
	cloned := *locator
	cloned.Extra = cloneRawObject(locator.Extra)
	return &cloned
}

func cloneAnnotation(annotation *Annotation) *Annotation {
	if annotation == nil {
		return nil
	}
	cloned := *annotation
	cloned.Extra = cloneRawObject(annotation.Extra)
	return &cloned
}

func validateDocumentPhysicalIDs(raw []byte) error {
	_, err := documentPhysicalIDs(raw)
	return err
}

func documentPhysicalIDs(raw []byte) (map[string]struct{}, error) {
	root, err := parseReviewNode(raw)
	if err != nil {
		return nil, err
	}
	result := map[string]struct{}{}
	var walk func(*reviewNode) error
	walk = func(node *reviewNode) error {
		var id string
		if value := node.object["ID"]; value != nil {
			if err := json.Unmarshal(value, &id); err != nil {
				return err
			}
		}
		if id != "" {
			if _, duplicate := result[id]; duplicate {
				return validationError(CodeDuplicateID, "ID", fmt.Sprintf("duplicate physical block ID %q", id))
			}
			result[id] = struct{}{}
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
	return result, nil
}
