package evergreencore

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
)

// authorizeOperationChanges 从完整权威写集验证命令、实体与正文边界，不能由计划来源自证权限。
func (s *AuthorityService) authorizeOperationChanges(operation PlannedOperation, beforeImages [][]byte) error {
	before := make([]*SYDocument, len(operation.Images))
	after := make([]*SYDocument, len(operation.Images))
	for index, image := range operation.Images {
		var err error
		after[index], err = DecodeSY(image.Bytes, s.registry)
		if err != nil {
			return err
		}
		if !image.Create {
			before[index], err = DecodeSY(beforeImages[index], s.registry)
			if err != nil {
				return err
			}
			if before[index].ReadOnly {
				return &DiagnosticError{Diagnostics: before[index].Diagnostics}
			}
			if err = authorizeDocumentIdentity(before[index], after[index]); err != nil {
				return err
			}
		}
		switch operation.Plan.Command {
		case NativeEditCommand:
			if err = s.authorizeNativeEdit(operation, before[index], after[index], image.Create); err != nil {
				return err
			}
		case "claim.update", "claim.edge.update":
			if after[index].Envelope.Entity.EntityType != EntityClaim ||
				(operation.Plan.Command == "claim.edge.update" && image.Create) {
				return validationError(CodeUnauthorized, "command", "Claim command requires a canonical Claim target")
			}
			root, err := parseReviewNode(image.Bytes)
			if err != nil {
				return err
			}
			for _, child := range root.children {
				if child.containsManagedEnvelope() {
					return validationError(CodeUnauthorized, "Children.Evergreen", "Claim cannot contain Note review metadata")
				}
			}
			if after[index].Envelope.Review != nil {
				return validationError(CodeUnauthorized, "Evergreen.review", "Claim cannot contain Note review state")
			}
			if before[index] != nil {
				if err = authorizeNativeRoot(operation.Plan.Principal, before[index].Raw, after[index].Raw); err != nil {
					return err
				}
			}
			if operation.Plan.Command == "claim.edge.update" {
				if err = authorizeEdgeOnlyChange(before[index], after[index]); err != nil {
					return err
				}
			} else if err = authorizeClaimBody(operation.Plan.Principal, before[index], after[index]); err != nil {
				return err
			}
		case "note.review.update":
			if image.Create || after[index].Envelope.Entity.EntityType != EntityNote {
				return validationError(CodeUnauthorized, "command", "review update requires an existing Note")
			}
			if err := authorizeReviewChange(before[index], after[index], s.registry); err != nil {
				return err
			}
			if err := authorizeNativeRoot(operation.Plan.Principal, before[index].Raw, after[index].Raw); err != nil {
				return err
			}
		case "note.review.materialize":
			if operation.Plan.Principal.Type != PrincipalUser ||
				(after[index].Envelope.Entity.EntityType != EntityClaim && after[index].Envelope.Entity.EntityType != EntityNote) ||
				(image.Create && after[index].Envelope.Entity.EntityType != EntityClaim) {
				return validationError(CodeUnauthorized, "command", "materialization requires a user and a Note/Claim write set")
			}
		default:
			return validationError(CodeUnauthorized, "command", "no authority change contract exists for this command")
		}
	}
	if operation.Plan.Command == "note.review.materialize" {
		return s.authorizeMaterialization(operation, beforeImages, after)
	}
	return authorizeAgentEdges(operation.Plan.Principal, before, after)
}

type operationBeforeRepository map[LogicalID][]byte

func (r operationBeforeRepository) Load(_ context.Context, id LogicalID) ([]byte, error) {
	if raw, ok := r[id]; ok {
		return raw, nil
	}
	return nil, fs.ErrNotExist
}

func (r operationBeforeRepository) List(context.Context) ([]LogicalID, error) {
	ids := make([]LogicalID, 0, len(r))
	for id := range r {
		ids = append(ids, id)
	}
	return ids, nil
}

func (s *AuthorityService) authorizeMaterialization(
	operation PlannedOperation, beforeImages [][]byte, after []*SYDocument,
) error {
	deny := func() error {
		return validationError(CodeUnauthorized, "materialization", "write set must match the current Candidate's deterministic output")
	}
	if len(after) != 2 {
		return deny()
	}
	noteIndex, claimIndex := -1, -1
	repository := operationBeforeRepository{}
	for index, document := range after {
		switch document.Envelope.Entity.EntityType {
		case EntityNote:
			noteIndex = index
		case EntityClaim:
			claimIndex = index
		}
		if len(beforeImages[index]) > 0 {
			repository[operation.Images[index].LogicalID] = beforeImages[index]
		}
	}
	if noteIndex < 0 || claimIndex < 0 {
		return deny()
	}
	beforeNote, err := parseReviewTree(beforeImages[noteIndex], s.registry)
	if err != nil {
		return err
	}
	nextNote, err := parseReviewTree(after[noteIndex].Raw, s.registry)
	if err != nil {
		return err
	}
	snapshot, err := beforeNote.snapshot(beforeImages[noteIndex])
	if err != nil {
		return err
	}
	if err = ValidateReviewForMaterialization(snapshot); err != nil {
		return err
	}
	claimID := operation.Images[claimIndex].LogicalID
	var selected *reviewCandidateNode
	for id, candidate := range nextNote.candidateByID {
		if candidate.envelope.Candidate.MaterializedClaimID == claimID && candidate.envelope.Candidate.State == "materialized" {
			if selected != nil {
				return deny()
			}
			selected = beforeNote.candidateByID[id]
		}
	}
	if selected == nil || selected.envelope.Candidate.State == "discarded" ||
		selected.isStale(beforeNote.segmentByID) ||
		(selected.envelope.Candidate.MaterializedClaimID != "" && selected.envelope.Candidate.MaterializedClaimID != claimID) {
		return deny()
	}
	claim := after[claimIndex]
	var documentID string
	claimRoot, err := decodeRawObject(claim.Raw)
	if err != nil {
		return err
	}
	_ = json.Unmarshal(claimRoot["ID"], &documentID)
	createdAt := ""
	for _, edge := range claim.Envelope.Relations.Outgoing {
		if edge.Schema == MaterialSchema {
			createdAt = edge.CreatedAt
			break
		}
	}
	if validateTimestamp(createdAt) != nil {
		return deny()
	}
	request := ReviewMaterializeRequest{
		OperationID: operation.Plan.OperationID, NoteID: operation.Images[noteIndex].LogicalID,
		CandidateID: selected.envelope.Candidate.CandidateID, ClaimID: claimID,
		ClaimPath: operation.Images[claimIndex].Path, ClaimDocumentID: documentID,
		CreatedAt: createdAt, KindData: claim.Envelope.Claim.KindData,
	}
	if !operation.Images[claimIndex].Create {
		request.ClaimBase, err = SemanticHash(beforeImages[claimIndex])
		if err != nil {
			return err
		}
	}
	expectedClaim, _, err := beforeNote.materializedClaim(context.Background(), repository, selected, request)
	if err != nil {
		return err
	}
	if !semanticValuesEqual(json.RawMessage(expectedClaim), json.RawMessage(claim.Raw)) {
		return deny()
	}
	selected.envelope.Candidate.State = "materialized"
	selected.envelope.Candidate.MaterializedClaimID = claimID
	if err = selected.writeEnvelope(); err != nil {
		return err
	}
	beforeNote.document.Envelope.Entity.SemanticRevision++
	if err = beforeNote.writeDocumentEnvelope(); err != nil {
		return err
	}
	expectedNote, err := beforeNote.encode()
	if err != nil {
		return err
	}
	if !semanticValuesEqual(json.RawMessage(expectedNote), json.RawMessage(after[noteIndex].Raw)) {
		return deny()
	}
	return nil
}

func authorizeDocumentIdentity(before, after *SYDocument) error {
	if before.Envelope == nil || after.Envelope == nil {
		return validationError(CodeUnauthorized, "Evergreen.entity", "existing entity metadata must be preserved")
	}
	left, right := before.Envelope.Entity, after.Envelope.Entity
	if before.Envelope.Spec != after.Envelope.Spec || left.LogicalID != right.LogicalID ||
		left.EntityType != right.EntityType || left.Schema != right.Schema ||
		!semanticValuesEqual(left.Extra, right.Extra) {
		return validationError(CodeUnauthorized, "Evergreen.entity", "entity identity requires an explicit migration")
	}
	a, err := decodeRawObject(before.Raw)
	if err != nil {
		return err
	}
	b, err := decodeRawObject(after.Raw)
	if err != nil {
		return err
	}
	for _, key := range []string{"ID", "Type", "Spec"} {
		if !semanticValuesEqual(a[key], b[key]) {
			return validationError(CodeUnauthorized, key, "physical document identity must be preserved")
		}
	}
	return nil
}

func authorizeNativeRoot(principal Principal, before, after []byte) error {
	left, err := decodeRawObject(before)
	if err != nil {
		return err
	}
	right, err := decodeRawObject(after)
	if err != nil {
		return err
	}
	for _, object := range []RawObject{left, right} {
		delete(object, "Children")
		delete(object, evergreenJSONKey)
		if principal.Type == PrincipalUser && object["Properties"] != nil {
			properties, err := decodeRawObject(object["Properties"])
			if err != nil {
				return err
			}
			delete(properties, "title")
			if len(properties) == 0 {
				delete(object, "Properties")
			} else {
				object["Properties"], _ = json.Marshal(properties)
			}
		}
	}
	if !semanticValuesEqual(left, right) {
		return validationError(CodeUnauthorized, "document", "programmatic edits must preserve native metadata and unknown fields")
	}
	return nil
}

func authorizeEdgeOnlyChange(before, after *SYDocument) error {
	a, _ := decodeRawObject(before.Raw)
	b, _ := decodeRawObject(after.Raw)
	delete(a, evergreenJSONKey)
	delete(b, evergreenJSONKey)
	if !semanticValuesEqual(a, b) {
		return validationError(CodeUnauthorized, "command", "edge update must preserve document body and properties")
	}
	a, err := nonReviewEnvelopeFields(before.Envelope)
	if err != nil {
		return err
	}
	b, err = nonReviewEnvelopeFields(after.Envelope)
	if err != nil {
		return err
	}
	delete(a, "relations")
	delete(b, "relations")
	if !semanticValuesEqual(a, b) || !semanticValuesEqual(before.Envelope.Relations.Extra, after.Envelope.Relations.Extra) {
		return validationError(CodeUnauthorized, "Evergreen", "edge update may only change outgoing edges and revision")
	}
	return nil
}

func nonReviewEnvelopeFields(envelope *DocumentEnvelope) (RawObject, error) {
	raw, err := MarshalDocumentEnvelope(envelope)
	if err != nil {
		return nil, err
	}
	object, err := decodeRawObject(raw)
	if err != nil {
		return nil, err
	}
	entity, err := decodeRawObject(object["entity"])
	if err != nil {
		return nil, err
	}
	delete(entity, "semantic_revision")
	object["entity"], err = json.Marshal(entity)
	delete(object, "review")
	return object, err
}

type bodySection struct {
	name  string
	nodes []*reviewNode
}

func claimBodySections(root *reviewNode) []bodySection {
	sections := []bodySection{{}}
	for _, child := range root.children {
		if name, ok := headingSectionName(child); ok {
			sections = append(sections, bodySection{name: name})
		}
		last := &sections[len(sections)-1]
		last.nodes = append(last.nodes, child)
	}
	return sections
}

func headingSectionName(node *reviewNode) (string, bool) {
	var kind string
	var level int
	_ = json.Unmarshal(node.object["Type"], &kind)
	_ = json.Unmarshal(node.object["HeadingLevel"], &level)
	if kind != "NodeHeading" || level != 2 {
		return "", false
	}
	var text func(*reviewNode) string
	text = func(node *reviewNode) string {
		var kind, data string
		_ = json.Unmarshal(node.object["Type"], &kind)
		if strings.HasSuffix(kind, "Marker") {
			return ""
		}
		_ = json.Unmarshal(node.object["Data"], &data)
		for _, child := range node.children {
			data += text(child)
		}
		if data == "" {
			_ = json.Unmarshal(node.object["TextMarkTextContent"], &data)
		}
		return data
	}
	return strings.TrimSpace(text(node)), true
}

func knownClaimSection(name string) bool {
	switch name {
	case "知识内容", "条件与边界", "解释与依据", "理解自检", "观点", "论据与推理", "条件与反例", "待验证":
		return true
	}
	return false
}

func preserveMaterializedUserSections(previous *reviewNode, children []json.RawMessage) ([]json.RawMessage, error) {
	next := &reviewNode{}
	for _, raw := range children {
		node, err := parseReviewNode(raw)
		if err != nil {
			return nil, err
		}
		next.children = append(next.children, node)
	}
	oldProtected := []bodySection{}
	if previous != nil {
		for _, section := range claimBodySections(previous) {
			protected := section.name == "用户补充" || (section.name != "" && !knownClaimSection(section.name))
			if protected {
				oldProtected = append(oldProtected, section)
			} else if len(oldProtected) > 0 {
				return nil, validationError(CodeUnauthorized, "Children", "materialization cannot safely relocate user sections")
			}
		}
	}
	result := []json.RawMessage{}
	for _, section := range claimBodySections(next) {
		if section.name == "用户补充" {
			if len(section.nodes) > 1 {
				return nil, validationError(CodeUnauthorized, "Children", "Candidate cannot fabricate user notes")
			}
			if len(oldProtected) > 0 {
				continue
			}
		}
		for _, protected := range oldProtected {
			if section.name == protected.name {
				return nil, validationError(CodeUnauthorized, "Children", "Candidate cannot overwrite a user section")
			}
		}
		for _, node := range section.nodes {
			raw, err := node.encode()
			if err != nil {
				return nil, err
			}
			result = append(result, raw)
		}
	}
	for _, section := range oldProtected {
		for _, node := range section.nodes {
			raw, err := node.encode()
			if err != nil {
				return nil, err
			}
			result = append(result, raw)
		}
	}
	return result, nil
}

func appendableClaimSection(name string) bool {
	switch name {
	case "条件与边界", "解释与依据", "理解自检", "论据与推理", "条件与反例", "待验证":
		return true
	}
	return false
}

func authorityNodesEqual(a, b []*reviewNode) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		left, err := a[index].encode()
		if err != nil {
			return false
		}
		right, err := b[index].encode()
		if err != nil || !semanticValuesEqual(json.RawMessage(left), json.RawMessage(right)) {
			return false
		}
	}
	return true
}

func authorizeClaimBody(principal Principal, before, after *SYDocument) error {
	next, err := parseReviewNode(after.Raw)
	if err != nil {
		return err
	}
	nextSections := claimBodySections(next)
	if before == nil {
		for _, section := range nextSections {
			if section.name == "用户补充" && len(section.nodes) > 1 {
				return validationError(CodeUnauthorized, "Children", "programmatic creation cannot fabricate user notes")
			}
		}
		return nil
	}
	previous, err := parseReviewNode(before.Raw)
	if err != nil {
		return err
	}
	if authorityNodesEqual(previous.children, next.children) {
		return nil
	}
	oldSections := claimBodySections(previous)
	if len(oldSections) != len(nextSections) {
		return validationError(CodeUnauthorized, "Children", "programmatic edits must preserve existing body sections")
	}
	for index, old := range oldSections {
		newSection := nextSections[index]
		if old.name != newSection.name {
			return validationError(CodeUnauthorized, "Children", "body sections cannot be renamed or reordered")
		}
		protected := old.name == "用户补充" || (old.name != "" && !knownClaimSection(old.name))
		if protected || principal.Type == PrincipalAgent {
			allowed := authorityNodesEqual(old.nodes, newSection.nodes)
			if !protected && appendableClaimSection(old.name) && len(newSection.nodes) >= len(old.nodes) {
				allowed = authorityNodesEqual(old.nodes, newSection.nodes[:len(old.nodes)])
			}
			if !allowed {
				return validationError(CodeUnauthorized, "Children", "existing core and user blocks must retain their identity and content")
			}
		}
	}
	return nil
}

func authorizeAgentEdges(principal Principal, before, after []*SYDocument) error {
	if principal.Type != PrincipalAgent {
		return nil
	}
	type ownedEdge struct {
		owner LogicalID
		edge  TypedEdge
	}
	oldEdges, nextEdges := map[EdgeID]ownedEdge{}, map[EdgeID]ownedEdge{}
	for index, document := range after {
		for _, edge := range document.Envelope.Relations.Outgoing {
			if _, exists := nextEdges[edge.ID]; exists {
				return validationError(CodeDuplicateID, "edge_id", "edge identity is duplicated in write set")
			}
			nextEdges[edge.ID] = ownedEdge{document.Envelope.Entity.LogicalID, edge}
		}
		if before[index] != nil {
			for _, edge := range before[index].Envelope.Relations.Outgoing {
				oldEdges[edge.ID] = ownedEdge{before[index].Envelope.Entity.LogicalID, edge}
			}
		}
	}
	for id, old := range oldEdges {
		next, exists := nextEdges[id]
		if !exists {
			return validationError(CodeUnauthorized, "relations.outgoing", "Agent cannot delete an existing edge")
		}
		if old.owner != next.owner && !(old.edge.Schema == ArgumentSchema && next.edge.Schema == ArgumentSchema &&
			next.edge.Type == "opposing" && next.owner < next.edge.Target.LogicalID &&
			next.owner == old.edge.Target.LogicalID && next.edge.Target.LogicalID == old.owner) {
			return validationError(CodeUnauthorized, "relations.outgoing", "edge owner move requires canonical opposing ownership")
		}
	}
	for id, next := range nextEdges {
		old, existed := oldEdges[id]
		if next.edge.Schema == ReplacementSchema || (existed && old.edge.Schema == ReplacementSchema) {
			a, _ := marshalRelations(Relations{Outgoing: []TypedEdge{old.edge}})
			b, _ := marshalRelations(Relations{Outgoing: []TypedEdge{next.edge}})
			if !existed || old.owner != next.owner || !semanticValuesEqual(a, b) {
				return validationError(CodeUnauthorized, "relations.outgoing", "replacement changes require a user")
			}
		}
	}
	return nil
}

func authorizeReviewChange(before, after *SYDocument, registry *Registry) error {
	a, err := nonReviewEnvelopeFields(before.Envelope)
	if err != nil {
		return err
	}
	b, err := nonReviewEnvelopeFields(after.Envelope)
	if err != nil {
		return err
	}
	if !semanticValuesEqual(a, b) {
		return validationError(CodeUnauthorized, "Evergreen", "review update must preserve Note source mapping and metadata")
	}
	previous, err := parseReviewTree(before.Raw, registry)
	if err != nil {
		return err
	}
	next, err := parseReviewTree(after.Raw, registry)
	if err != nil {
		return err
	}
	snapshot, err := next.snapshot(after.Raw)
	if err != nil {
		return err
	}
	if err = validateReviewStructure(snapshot); err != nil {
		return err
	}
	for id, candidate := range next.candidateByID {
		old, existed := previous.candidateByID[id]
		oldOutput := LogicalID("")
		oldState := ""
		if existed {
			oldOutput = old.envelope.Candidate.MaterializedClaimID
			oldState = old.envelope.Candidate.State
			if !semanticValuesEqual(old.envelope.Extra, candidate.envelope.Extra) ||
				!semanticValuesEqual(old.envelope.Extension, candidate.envelope.Extension) ||
				!semanticValuesEqual(old.envelope.Candidate.Extra, candidate.envelope.Candidate.Extra) {
				return validationError(CodeUnauthorized, "candidate", "unknown Candidate metadata must be preserved")
			}
		}
		if candidate.envelope.Candidate.MaterializedClaimID != oldOutput ||
			(candidate.envelope.Candidate.State == "materialized" && oldState != "materialized") {
			return validationError(CodeUnauthorized, "candidate.materialized_claim_id", "only deterministic materialization may assign an output")
		}
	}
	for id, candidate := range previous.candidateByID {
		if candidate.envelope.Candidate.MaterializedClaimID != "" && next.candidateByID[id] == nil {
			return validationError(CodeUnauthorized, "candidate", "materialized output mapping must be retained")
		}
	}
	for id, segment := range previous.segmentByID {
		if retained := next.segmentByID[id]; retained != nil {
			oldRaw, _ := MarshalBlockEnvelope(segment.envelope)
			nextRaw, _ := MarshalBlockEnvelope(retained.envelope)
			oldObject, _ := decodeRawObject(oldRaw)
			nextObject, _ := decodeRawObject(nextRaw)
			oldSegment, _ := decodeRawObject(oldObject["segment"])
			nextSegment, _ := decodeRawObject(nextObject["segment"])
			delete(oldSegment, "normalized_hash")
			delete(nextSegment, "normalized_hash")
			oldObject["segment"], _ = json.Marshal(oldSegment)
			nextObject["segment"], _ = json.Marshal(nextSegment)
			if !semanticValuesEqual(oldObject, nextObject) {
				return validationError(CodeUnauthorized, "segment.source_ref", "retained segment source identity must be preserved")
			}
		}
	}
	for _, segment := range next.segments {
		if segment.envelope.Segment.NormalizedHash != segment.currentHash {
			return validationError(CodeUnauthorized, "segment.normalized_hash", "review edits must update segment hashes")
		}
	}
	if !semanticValuesEqual(previous.document.Envelope.Review.Extra, next.document.Envelope.Review.Extra) {
		return validationError(CodeUnauthorized, "review", "unknown review metadata must be preserved")
	}
	oldNative, nextNative := unmanagedNoteNodes(previous.root), unmanagedNoteNodes(next.root)
	for key, node := range oldNative {
		retained, exists := nextNative[key]
		if !exists || !authorityNodesEqual([]*reviewNode{node}, []*reviewNode{retained}) {
			return validationError(CodeUnauthorized, "Children", "user Note blocks must be preserved")
		}
	}
	return nil
}

func unmanagedNoteNodes(root *reviewNode) map[string]*reviewNode {
	result := map[string]*reviewNode{}
	var walk func(*reviewNode, string)
	walk = func(node *reviewNode, position string) {
		if _, managed := node.object[evergreenJSONKey]; managed {
			return
		}
		if !node.containsManagedEnvelope() {
			var id string
			_ = json.Unmarshal(node.object["ID"], &id)
			if id == "" {
				id = position
			}
			result[position+":"+id] = node
			return
		}
		for index, child := range node.children {
			walk(child, fmt.Sprintf("%s/%d", position, index))
		}
	}
	for index, child := range root.children {
		walk(child, fmt.Sprintf("%d", index))
	}
	return result
}
