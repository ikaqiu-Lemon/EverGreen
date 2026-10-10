package evergreencore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

const (
	AVViewSpec         = "evergreen.av-view/v1"
	AVProviderProtocol = "evergreen.sy-provider/v1"
	AVProviderType     = "evergreen.sy"

	ClaimsViewID    = "claims"
	KnowledgeViewID = "knowledge"
	OpinionViewID   = "opinion"

	EdgeDirectionOutgoing = "outgoing"
	EdgeDirectionIncoming = "incoming"

	AVEdgeAdd     = "add"
	AVEdgeUpdate  = "update"
	AVEdgeDelete  = "delete"
	AVEdgeReorder = "reorder"
	AVScalarSet   = "set"
)

type AVDataProvider interface {
	Describe(context.Context, string) (AVViewSchema, error)
	Query(context.Context, string, AVQuery) (AVPage, error)
	GetCell(context.Context, AVRowRef, AVColumnBinding) (AVCellValue, error)
	PlanPatch(context.Context, Principal, AVPatch) (PlannedOperation, error)
}

type AVPlanner interface {
	Plan(context.Context, Principal, PlanRequest) (PlannedOperation, error)
}

type AVViewDefinition struct {
	Spec     string               `json:"spec"`
	ID       string               `json:"id"`
	Name     string               `json:"name"`
	Provider AVProviderDefinition `json:"provider"`
	Columns  []AVColumnDefinition `json:"columns"`
	Filters  []AVFilter           `json:"filter,omitempty"`
	Sorts    []AVSort             `json:"sort,omitempty"`
	GroupBy  string               `json:"group,omitempty"`
	Layout   RawObject            `json:"layout,omitempty"`
}

type AVProviderDefinition struct {
	Type     string        `json:"type"`
	Protocol string        `json:"protocol"`
	Query    AVEntityQuery `json:"query"`
}

type AVEntityQuery struct {
	EntityType EntityType `json:"entity_type"`
	ClaimKind  ClaimKind  `json:"claim_kind,omitempty"`
}

type AVColumnDefinition struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Value    string          `json:"value"`
	Editable bool            `json:"editable"`
	Binding  AVColumnBinding `json:"binding"`
}

type AVColumnBinding struct {
	Field       string    `json:"field,omitempty"`
	Direction   string    `json:"direction,omitempty"`
	EdgeSchema  SchemaRef `json:"edge_schema,omitempty"`
	EdgeType    string    `json:"edge_type,omitempty"`
	TargetKind  ClaimKind `json:"target_kind,omitempty"`
	Aggregation string    `json:"aggregation,omitempty"`
}

type AVViewSchema struct {
	Definition AVViewDefinition `json:"definition"`
	ReadOnly   bool             `json:"read_only,omitempty"`
}

type AVQuery struct {
	Filters []AVFilter `json:"filters,omitempty"`
	Sorts   []AVSort   `json:"sorts,omitempty"`
	GroupBy string     `json:"group_by,omitempty"`
	Offset  int        `json:"offset,omitempty"`
	Limit   int        `json:"limit,omitempty"`
}

type AVFilter struct {
	Column   string `json:"column"`
	Operator string `json:"operator"`
	Value    string `json:"value,omitempty"`
}

type AVSort struct {
	Column    string `json:"column"`
	Direction string `json:"direction"`
}

type AVRowRef struct {
	LogicalID    LogicalID `json:"logical_id"`
	SemanticHash string    `json:"semantic_hash"`
}

type AVRow struct {
	Ref      AVRowRef               `json:"ref"`
	Revision uint64                 `json:"revision"`
	ReadOnly bool                   `json:"read_only,omitempty"`
	Cells    map[string]AVCellValue `json:"cells"`
}

type AVCellValue struct {
	Kind              string          `json:"kind"`
	Text              string          `json:"text,omitempty"`
	Texts             []string        `json:"texts,omitempty"`
	JSON              json.RawMessage `json:"json,omitempty"`
	Provenance        []Provenance    `json:"provenance,omitempty"`
	Edges             []AVEdgeValue   `json:"edges,omitempty"`
	EdgeCount         int             `json:"edge_count,omitempty"`
	UniqueTargetCount int             `json:"unique_target_count,omitempty"`
}

type AVEdgeValue struct {
	ID        EdgeID      `json:"edge_id"`
	OwnerID   LogicalID   `json:"owner_id"`
	OwnerHash string      `json:"owner_hash"`
	Direction string      `json:"direction"`
	Schema    SchemaRef   `json:"schema"`
	Target    EntityRef   `json:"target"`
	Type      string      `json:"type"`
	Reason    string      `json:"reason"`
	Context   EdgeContext `json:"context,omitempty"`
}

type AVPage struct {
	Rows        []AVRow        `json:"rows"`
	Total       int            `json:"total"`
	GroupCounts map[string]int `json:"group_counts,omitempty"`
	Snapshot    string         `json:"snapshot"`
}

type AVPatch struct {
	OperationID string          `json:"operation_id"`
	RowID       LogicalID       `json:"row_id"`
	Base        BaseRef         `json:"base"`
	Column      AVColumnBinding `json:"column"`
	Value       *AVCellValue    `json:"value,omitempty"`
	Edge        AVEdgePatch     `json:"edge,omitempty"`
}

type AVEdgePatch struct {
	Action  string    `json:"action"`
	OwnerID LogicalID `json:"owner_id,omitempty"`
	Edge    TypedEdge `json:"edge,omitempty"`
	Order   []EdgeID  `json:"order,omitempty"`
}

type AVProjectionSnapshot struct {
	Digest       string `json:"digest"`
	ClaimCount   int    `json:"claim_count"`
	EdgeCount    int    `json:"edge_count"`
	ReverseCount int    `json:"reverse_count"`
}

type claimsAVRecord struct {
	raw      []byte
	document *SYDocument
	hash     string
	title    string
}

type claimsAVSnapshot struct {
	public  AVProjectionSnapshot
	claims  map[LogicalID]*claimsAVRecord
	reverse map[LogicalID][]AVEdgeValue
}

type ClaimsAVProvider struct {
	repository Repository
	planner    AVPlanner
	registry   *Registry

	mu       sync.RWMutex
	snapshot *claimsAVSnapshot
}

func NewClaimsAVProvider(repository Repository, planner AVPlanner, registry *Registry) *ClaimsAVProvider {
	if registry == nil {
		registry = DefaultRegistry()
	}
	return &ClaimsAVProvider{repository: repository, planner: planner, registry: registry}
}

func (p *ClaimsAVProvider) Describe(_ context.Context, viewID string) (AVViewSchema, error) {
	definition, err := p.registry.ViewDefinition(viewID)
	if err != nil {
		return AVViewSchema{}, err
	}
	return AVViewSchema{Definition: definition}, nil
}

func claimsViewDefinition(viewID string) (AVViewDefinition, error) {
	definition := AVViewDefinition{
		Spec: AVViewSpec,
		ID:   viewID,
		Provider: AVProviderDefinition{
			Type: AVProviderType, Protocol: AVProviderProtocol,
			Query: AVEntityQuery{EntityType: EntityClaim},
		},
		Layout: RawObject{},
		Columns: []AVColumnDefinition{
			{ID: "title", Name: "Claim", Value: "text", Binding: AVColumnBinding{Field: "claim.body.title"}},
			{ID: "logical_id", Name: "Logical ID", Value: "text", Binding: AVColumnBinding{Field: "entity.logical_id"}},
			{ID: "claim_kind", Name: "Kind", Value: "text", Binding: AVColumnBinding{Field: "claim.claim_kind"}},
			{ID: "status", Name: "Status", Value: "text", Editable: true, Binding: AVColumnBinding{Field: "claim.status"}},
			{ID: "tags", Name: "Tags", Value: "tags", Editable: true, Binding: AVColumnBinding{Field: "claim.tags"}},
			{ID: "validation", Name: "Validation", Value: "json", Editable: true, Binding: AVColumnBinding{Field: "claim.kind_data.validation"}},
			{ID: "provenance", Name: "Sources", Value: "provenance", Binding: AVColumnBinding{Field: "claim.provenance"}},
			{
				ID: "arguments_outgoing", Name: "Outgoing arguments", Value: "relation", Editable: true,
				Binding: AVColumnBinding{Direction: EdgeDirectionOutgoing, EdgeSchema: ArgumentSchema},
			},
			{
				ID: "arguments_incoming", Name: "Incoming arguments", Value: "relation", Editable: true,
				Binding: AVColumnBinding{Direction: EdgeDirectionIncoming, EdgeSchema: ArgumentSchema},
			},
		},
	}
	switch viewID {
	case ClaimsViewID:
		definition.Name = "Claims"
	case KnowledgeViewID:
		definition.Name = "Knowledge"
		definition.Provider.Query.ClaimKind = KnowledgeKind
	case OpinionViewID:
		definition.Name = "Opinion"
		definition.Provider.Query.ClaimKind = OpinionKind
	default:
		return AVViewDefinition{}, validationError(CodeInvalidPlan, "view_id", fmt.Sprintf("unknown Evergreen view %q", viewID))
	}
	return definition, nil
}

func (p *ClaimsAVProvider) Query(ctx context.Context, viewID string, query AVQuery) (AVPage, error) {
	definition, err := p.registry.ViewDefinition(viewID)
	if err != nil {
		return AVPage{}, err
	}
	snapshot, err := p.currentSnapshot(ctx)
	if err != nil {
		return AVPage{}, err
	}
	for _, record := range snapshot.claims {
		_, available := p.registry.Descriptor(record.document.Envelope.Claim.ClaimKind)
		if (!available && !record.document.ReadOnly) ||
			(available && record.document.ReadOnly && onlyKindUnavailable(record.document.Diagnostics)) {
			if _, err = p.Rebuild(ctx); err != nil {
				return AVPage{}, err
			}
			snapshot, err = p.currentSnapshot(ctx)
			if err != nil {
				return AVPage{}, err
			}
			break
		}
	}
	if stale, verifyErr := p.snapshotStale(ctx, snapshot); verifyErr != nil {
		return AVPage{}, verifyErr
	} else if stale {
		if _, err = p.Rebuild(ctx); err != nil {
			return AVPage{}, err
		}
		snapshot, err = p.currentSnapshot(ctx)
		if err != nil {
			return AVPage{}, err
		}
	}

	ids := make([]LogicalID, 0, len(snapshot.claims))
	for id, record := range snapshot.claims {
		if definition.Provider.Query.ClaimKind != "" &&
			record.document.Envelope.Claim.ClaimKind != definition.Provider.Query.ClaimKind {
			continue
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	rows := make([]AVRow, 0, len(ids))
	for _, id := range ids {
		row := projectClaimRow(snapshot, id)
		if _, available := p.registry.Descriptor(snapshot.claims[id].document.Envelope.Claim.ClaimKind); !available {
			row.ReadOnly = true
		}
		for _, column := range definition.Columns {
			if strings.HasPrefix(column.Binding.Field, "claim.kind_data.") {
				cell, projectErr := projectBinding(row, column.Binding)
				if projectErr != nil {
					return AVPage{}, projectErr
				}
				row.Cells[column.ID] = cell
			}
		}
		if matchesAVFilters(row, query.Filters) {
			rows = append(rows, row)
		}
	}
	sortAVRows(rows, query.Sorts)
	total := len(rows)
	groups := groupAVRows(rows, query.GroupBy)
	start := query.Offset
	if start < 0 {
		start = 0
	}
	if start > len(rows) {
		start = len(rows)
	}
	end := len(rows)
	if query.Limit > 0 && start+query.Limit < end {
		end = start + query.Limit
	}
	return AVPage{
		Rows: rows[start:end], Total: total, GroupCounts: groups,
		Snapshot: snapshot.public.Digest,
	}, nil
}

func (p *ClaimsAVProvider) GetCell(ctx context.Context, row AVRowRef, column AVColumnBinding) (AVCellValue, error) {
	snapshot, err := p.currentSnapshot(ctx)
	if err != nil {
		return AVCellValue{}, err
	}
	record, ok := snapshot.claims[row.LogicalID]
	if !ok {
		return AVCellValue{}, validationError(CodeInvalidClaim, "row.logical_id", "claim row does not exist")
	}
	current, err := p.repository.Load(ctx, row.LogicalID)
	if err != nil {
		return AVCellValue{}, err
	}
	hash, err := SemanticHash(current)
	if err != nil {
		return AVCellValue{}, err
	}
	if row.SemanticHash != hash || record.hash != hash {
		return AVCellValue{}, validationError(
			CodeBaseMismatch, "row.semantic_hash",
			fmt.Sprintf("row base %q does not match current %q", row.SemanticHash, hash),
		)
	}
	projected := projectClaimRow(snapshot, row.LogicalID)
	return projectBinding(projected, column)
}

func (p *ClaimsAVProvider) PlanPatch(
	ctx context.Context,
	principal Principal,
	patch AVPatch,
) (PlannedOperation, error) {
	if strings.TrimSpace(patch.OperationID) == "" {
		return PlannedOperation{}, validationError(CodeInvalidPlan, "operation_id", "operation ID is required")
	}
	if p.planner == nil {
		return PlannedOperation{}, validationError(CodeInvalidPlan, "planner", "AV planner is unavailable")
	}
	if patch.RowID == "" {
		return PlannedOperation{}, validationError(CodeInvalidPlan, "row_id", "row logical ID is required")
	}
	if patch.Edge.Action != "" {
		return p.planEdgePatch(ctx, principal, patch)
	}
	if patch.Value == nil || patch.Edge.Action != "" {
		return PlannedOperation{}, validationError(CodeInvalidPlan, "patch", "cell patch value or edge action is required")
	}
	return p.planScalarPatch(ctx, principal, patch)
}

func (p *ClaimsAVProvider) planEdgePatch(
	ctx context.Context,
	principal Principal,
	patch AVPatch,
) (PlannedOperation, error) {
	if patch.Column.Direction != EdgeDirectionOutgoing && patch.Column.Direction != EdgeDirectionIncoming {
		return PlannedOperation{}, validationError(CodeInvalidPlan, "column.direction", "relation direction must be outgoing or incoming")
	}
	ownerID := patch.RowID
	if patch.Column.Direction == EdgeDirectionIncoming {
		ownerID = patch.Edge.OwnerID
		if ownerID == "" {
			return PlannedOperation{}, validationError(CodeInvalidPlan, "edge.owner_id", "incoming edge patch requires the authority owner")
		}
	}
	if patch.Base.LogicalID != ownerID {
		return PlannedOperation{}, validationError(CodeBaseMismatch, "base.logical_id", "base must identify the edge owner")
	}
	raw, document, hash, err := p.loadWritableClaim(ctx, ownerID)
	if err != nil {
		return PlannedOperation{}, err
	}
	if patch.Base.SemanticHash != hash {
		return PlannedOperation{}, validationError(
			CodeBaseMismatch, "base.semantic_hash",
			fmt.Sprintf("semantic base hash %q does not match current %q", patch.Base.SemanticHash, hash),
		)
	}
	edges := append([]TypedEdge(nil), document.Envelope.Relations.Outgoing...)
	switch patch.Edge.Action {
	case AVEdgeAdd:
		edge := patch.Edge.Edge
		if edge.ID == "" {
			return PlannedOperation{}, validationError(CodeInvalidEdge, "edge.edge_id", "edge ID is required")
		}
		if patch.Column.Direction == EdgeDirectionIncoming {
			edge.Target = EntityRef{EntityType: EntityClaim, LogicalID: patch.RowID}
		}
		if err = validateEdgeBinding(edge, patch.Column); err != nil {
			return PlannedOperation{}, err
		}
		if _, found := findEdge(edges, edge.ID); found {
			return PlannedOperation{}, validationError(CodeDuplicateID, "edge.edge_id", "edge ID already exists")
		}
		if err = p.validateEdgeTarget(ctx, edge, patch.Column); err != nil {
			return PlannedOperation{}, err
		}
		if opposingOwnerMoves(ownerID, edge) {
			return p.planOpposingEdgeMove(ctx, principal, patch, raw, document, hash, edges, -1, edge)
		}
		edges = append(edges, edge)
	case AVEdgeUpdate:
		index, found := findEdge(edges, patch.Edge.Edge.ID)
		if !found {
			return PlannedOperation{}, validationError(CodeInvalidEdge, "edge.edge_id", "edge does not exist on owner")
		}
		current := edges[index]
		edge := patch.Edge.Edge
		if patch.Column.Direction == EdgeDirectionIncoming && current.Target.LogicalID != patch.RowID {
			return PlannedOperation{}, validationError(CodeInvalidEdge, "edge.target", "incoming edge does not target the projected row")
		}
		if edge.CreatedAt == "" {
			edge.CreatedAt = current.CreatedAt
		}
		if edge.UpdatedAt == "" {
			edge.UpdatedAt = current.UpdatedAt
		}
		if edge.Extension == nil {
			edge.Extension = current.Extension
		}
		if edge.Extra == nil {
			edge.Extra = current.Extra
		}
		if edge.Target.Extra == nil {
			edge.Target.Extra = current.Target.Extra
		}
		if edge.Context.Data == nil {
			edge.Context.Data = current.Context.Data
		}
		if edge.Context.Extra == nil {
			edge.Context.Extra = current.Context.Extra
		}
		if err = validateEdgeBinding(edge, patch.Column); err != nil {
			return PlannedOperation{}, err
		}
		if err = p.validateEdgeTarget(ctx, edge, patch.Column); err != nil {
			return PlannedOperation{}, err
		}
		if opposingOwnerMoves(ownerID, edge) {
			return p.planOpposingEdgeMove(ctx, principal, patch, raw, document, hash, edges, index, edge)
		}
		edges[index] = edge
	case AVEdgeDelete:
		index, found := findEdge(edges, patch.Edge.Edge.ID)
		if !found {
			return PlannedOperation{}, validationError(CodeInvalidEdge, "edge.edge_id", "edge does not exist on owner")
		}
		if patch.Column.Direction == EdgeDirectionIncoming && edges[index].Target.LogicalID != patch.RowID {
			return PlannedOperation{}, validationError(CodeInvalidEdge, "edge.target", "incoming edge does not target the projected row")
		}
		edges = append(edges[:index], edges[index+1:]...)
	case AVEdgeReorder:
		var reorderErr error
		edges, reorderErr = reorderEdges(edges, patch.Column, patch.Edge.Order)
		if reorderErr != nil {
			return PlannedOperation{}, reorderErr
		}
	default:
		return PlannedOperation{}, validationError(CodeInvalidPlan, "edge.action", fmt.Sprintf("unknown edge action %q", patch.Edge.Action))
	}
	document.Envelope.Relations.Outgoing = edges
	if patch.Edge.Action != AVEdgeReorder {
		document.Envelope.Entity.SemanticRevision++
	}
	after, err := EncodeSY(raw, document.Envelope, p.registry)
	if err != nil {
		return PlannedOperation{}, err
	}
	return p.planner.Plan(ctx, principal, PlanRequest{
		Protocol: ChangePlanProtocol, OperationID: patch.OperationID, Command: "claim.edge.update",
		Base:   []BaseRef{{LogicalID: ownerID, SemanticHash: hash}},
		Writes: []WriteInput{{LogicalID: ownerID, After: after}},
	})
}

func opposingOwnerMoves(ownerID LogicalID, edge TypedEdge) bool {
	return edge.Schema == ArgumentSchema && edge.Type == "opposing" &&
		edge.Target.LogicalID != "" && ownerID > edge.Target.LogicalID
}

func (p *ClaimsAVProvider) planOpposingEdgeMove(
	ctx context.Context,
	principal Principal,
	patch AVPatch,
	currentRaw []byte,
	current *SYDocument,
	currentHash string,
	currentEdges []TypedEdge,
	currentIndex int,
	edge TypedEdge,
) (PlannedOperation, error) {
	currentOwner := current.Envelope.Entity.LogicalID
	newOwner := edge.Target.LogicalID
	newRaw, newDocument, newHash, err := p.loadWritableClaim(ctx, newOwner)
	if err != nil {
		return PlannedOperation{}, err
	}
	if _, duplicate := findEdge(newDocument.Envelope.Relations.Outgoing, edge.ID); duplicate {
		return PlannedOperation{}, validationError(CodeDuplicateID, "edge.edge_id", "edge ID already exists on canonical opposing owner")
	}
	moved := edge
	moved.Target = EntityRef{EntityType: EntityClaim, LogicalID: currentOwner}
	if err = validateEdgeBinding(moved, patch.Column); err != nil {
		return PlannedOperation{}, err
	}
	if err = p.validateEdgeTarget(ctx, moved, patch.Column); err != nil {
		return PlannedOperation{}, err
	}

	currentAfter := currentRaw
	if currentIndex >= 0 {
		current.Envelope.Relations.Outgoing = append(currentEdges[:currentIndex], currentEdges[currentIndex+1:]...)
		current.Envelope.Entity.SemanticRevision++
		currentAfter, err = EncodeSY(currentRaw, current.Envelope, p.registry)
		if err != nil {
			return PlannedOperation{}, err
		}
	}
	newDocument.Envelope.Relations.Outgoing = append(newDocument.Envelope.Relations.Outgoing, moved)
	newDocument.Envelope.Entity.SemanticRevision++
	newAfter, err := EncodeSY(newRaw, newDocument.Envelope, p.registry)
	if err != nil {
		return PlannedOperation{}, err
	}

	type ownerWrite struct {
		id    LogicalID
		hash  string
		after []byte
	}
	owners := []ownerWrite{
		{id: currentOwner, hash: currentHash, after: currentAfter},
		{id: newOwner, hash: newHash, after: newAfter},
	}
	sort.Slice(owners, func(i, j int) bool { return owners[i].id < owners[j].id })
	request := PlanRequest{
		Protocol: ChangePlanProtocol, OperationID: patch.OperationID, Command: "claim.edge.update",
		Base: make([]BaseRef, 0, len(owners)), Writes: make([]WriteInput, 0, len(owners)),
	}
	for _, owner := range owners {
		request.Base = append(request.Base, BaseRef{LogicalID: owner.id, SemanticHash: owner.hash})
		request.Writes = append(request.Writes, WriteInput{LogicalID: owner.id, After: owner.after})
	}
	return p.planner.Plan(ctx, principal, request)
}

func (p *ClaimsAVProvider) planScalarPatch(
	ctx context.Context,
	principal Principal,
	patch AVPatch,
) (PlannedOperation, error) {
	if patch.Base.LogicalID != patch.RowID {
		return PlannedOperation{}, validationError(CodeBaseMismatch, "base.logical_id", "base must identify the edited claim")
	}
	raw, document, hash, err := p.loadWritableClaim(ctx, patch.RowID)
	if err != nil {
		return PlannedOperation{}, err
	}
	if patch.Base.SemanticHash != hash {
		return PlannedOperation{}, validationError(
			CodeBaseMismatch, "base.semantic_hash",
			fmt.Sprintf("semantic base hash %q does not match current %q", patch.Base.SemanticHash, hash),
		)
	}
	switch patch.Column.Field {
	case "claim.status":
		document.Envelope.Claim.Status = patch.Value.Text
	case "claim.tags":
		document.Envelope.Claim.Tags = append([]string(nil), patch.Value.Texts...)
	default:
		const prefix = "claim.kind_data."
		if !strings.HasPrefix(patch.Column.Field, prefix) {
			return PlannedOperation{}, validationError(CodeInvalidPlan, "column.field", fmt.Sprintf("field %q is read-only or unknown", patch.Column.Field))
		}
		field := strings.TrimPrefix(patch.Column.Field, prefix)
		descriptor, _ := p.registry.Descriptor(document.Envelope.Claim.ClaimKind)
		if _, exists := descriptor.KindDataSchema.Properties[field]; !exists {
			return PlannedOperation{}, validationError(CodeInvalidPlan, "column.field", "field is not declared by the kind schema")
		}
		var kindData map[string]json.RawMessage
		if err = json.Unmarshal(document.Envelope.Claim.KindData, &kindData); err != nil {
			return PlannedOperation{}, validationError(CodeInvalidClaim, "claim.kind_data", err.Error())
		}
		if kindData == nil {
			kindData = map[string]json.RawMessage{}
		}
		if !json.Valid(patch.Value.JSON) {
			return PlannedOperation{}, validationError(CodeInvalidClaim, "claim.kind_data.validation", "validation JSON is invalid")
		}
		kindData[field] = append(json.RawMessage(nil), patch.Value.JSON...)
		document.Envelope.Claim.KindData, err = json.Marshal(kindData)
		if err != nil {
			return PlannedOperation{}, err
		}
	}
	before, err := DecodeSY(raw, p.registry)
	if err != nil {
		return PlannedOperation{}, err
	}
	if err = AuthorizeClaimChange(principal, before.Envelope, document.Envelope, p.registry); err != nil {
		return PlannedOperation{}, err
	}
	document.Envelope.Entity.SemanticRevision++
	after, err := EncodeSY(raw, document.Envelope, p.registry)
	if err != nil {
		return PlannedOperation{}, err
	}
	return p.planner.Plan(ctx, principal, PlanRequest{
		Protocol: ChangePlanProtocol, OperationID: patch.OperationID, Command: "claim.update",
		Base:   []BaseRef{{LogicalID: patch.RowID, SemanticHash: hash}},
		Writes: []WriteInput{{LogicalID: patch.RowID, After: after}},
	})
}

func (p *ClaimsAVProvider) loadWritableClaim(
	ctx context.Context,
	id LogicalID,
) ([]byte, *SYDocument, string, error) {
	raw, err := p.repository.Load(ctx, id)
	if err != nil {
		return nil, nil, "", err
	}
	document, err := DecodeSY(raw, p.registry)
	if err != nil {
		return nil, nil, "", err
	}
	if document.ReadOnly {
		return nil, nil, "", &DiagnosticError{Diagnostics: document.Diagnostics}
	}
	if document.Envelope == nil || document.Envelope.Entity.EntityType != EntityClaim || document.Envelope.Claim == nil {
		return nil, nil, "", validationError(CodeInvalidClaim, "row.logical_id", "row is not a claim")
	}
	hash, err := SemanticHash(raw)
	if err != nil {
		return nil, nil, "", err
	}
	return raw, document, hash, nil
}

func (p *ClaimsAVProvider) validateEdgeTarget(
	ctx context.Context,
	edge TypedEdge,
	binding AVColumnBinding,
) error {
	if edge.Schema == BasicSchema && edge.Target.LogicalID == "" {
		return nil
	}
	raw, err := p.repository.Load(ctx, edge.Target.LogicalID)
	if err != nil {
		return validationError(CodeInvalidEdge, "edge.target", "target entity does not exist")
	}
	document, err := DecodeSY(raw, p.registry)
	if err != nil {
		return err
	}
	if document.Envelope == nil || document.Envelope.Entity.EntityType != edge.Target.EntityType {
		return validationError(CodeInvalidEdge, "edge.target", "target entity type does not match authority document")
	}
	if binding.TargetKind != "" {
		targetKind := ClaimKind("")
		if document.Envelope.Claim != nil {
			targetKind = document.Envelope.Claim.ClaimKind
		}
		if targetKind != binding.TargetKind {
			return validationError(CodeInvalidEdge, "edge.target", fmt.Sprintf(
				"target claim kind %q does not match column target kind %q",
				targetKind,
				binding.TargetKind,
			))
		}
	}
	return nil
}

func (p *ClaimsAVProvider) Rebuild(ctx context.Context) (AVProjectionSnapshot, error) {
	if p.repository == nil {
		return AVProjectionSnapshot{}, validationError(CodeInvalidPlan, "repository", "Claims repository is unavailable")
	}
	ids, err := p.repository.List(ctx)
	if err != nil {
		return AVProjectionSnapshot{}, err
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	snapshot := &claimsAVSnapshot{
		claims:  map[LogicalID]*claimsAVRecord{},
		reverse: map[LogicalID][]AVEdgeValue{},
	}
	type digestClaim struct {
		ID    LogicalID `json:"id"`
		Hash  string    `json:"hash"`
		Edges []EdgeID  `json:"edges"`
	}
	digestClaims := make([]digestClaim, 0, len(ids))
	for _, id := range ids {
		raw, loadErr := p.repository.Load(ctx, id)
		if loadErr != nil {
			return AVProjectionSnapshot{}, loadErr
		}
		document, decodeErr := DecodeSY(raw, p.registry)
		if decodeErr != nil {
			return AVProjectionSnapshot{}, decodeErr
		}
		if document.Envelope == nil || document.Envelope.Entity.EntityType != EntityClaim || document.Envelope.Claim == nil {
			continue
		}
		hash, hashErr := SemanticHash(raw)
		if hashErr != nil {
			return AVProjectionSnapshot{}, hashErr
		}
		record := &claimsAVRecord{
			raw: append([]byte(nil), raw...), document: document, hash: hash, title: claimTitle(raw, id),
		}
		snapshot.claims[id] = record
		digestItem := digestClaim{ID: id, Hash: hash}
		for _, edge := range document.Envelope.Relations.Outgoing {
			digestItem.Edges = append(digestItem.Edges, edge.ID)
			projected := projectEdge(edge, id, hash, EdgeDirectionIncoming)
			snapshot.reverse[edge.Target.LogicalID] = append(snapshot.reverse[edge.Target.LogicalID], projected)
			snapshot.public.EdgeCount++
		}
		sort.Slice(digestItem.Edges, func(i, j int) bool { return digestItem.Edges[i] < digestItem.Edges[j] })
		digestClaims = append(digestClaims, digestItem)
	}
	for target := range snapshot.reverse {
		sort.SliceStable(snapshot.reverse[target], func(i, j int) bool {
			left, right := snapshot.reverse[target][i], snapshot.reverse[target][j]
			if left.OwnerID == right.OwnerID {
				return left.ID < right.ID
			}
			return left.OwnerID < right.OwnerID
		})
		snapshot.public.ReverseCount += len(snapshot.reverse[target])
	}
	snapshot.public.ClaimCount = len(snapshot.claims)
	digestBytes, _ := json.Marshal(digestClaims)
	sum := sha256.Sum256(digestBytes)
	snapshot.public.Digest = "sha256:" + hex.EncodeToString(sum[:])

	p.mu.Lock()
	p.snapshot = snapshot
	p.mu.Unlock()
	return snapshot.public, nil
}

func (p *ClaimsAVProvider) DropDerived() {
	p.mu.Lock()
	p.snapshot = nil
	p.mu.Unlock()
}

func (p *ClaimsAVProvider) currentSnapshot(ctx context.Context) (*claimsAVSnapshot, error) {
	p.mu.RLock()
	snapshot := p.snapshot
	p.mu.RUnlock()
	if snapshot != nil {
		return snapshot, nil
	}
	if _, err := p.Rebuild(ctx); err != nil {
		return nil, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.snapshot, nil
}

func (p *ClaimsAVProvider) snapshotStale(ctx context.Context, snapshot *claimsAVSnapshot) (bool, error) {
	ids, err := p.repository.List(ctx)
	if err != nil {
		return false, err
	}
	claimIDs := make([]LogicalID, 0, len(snapshot.claims))
	for id := range snapshot.claims {
		claimIDs = append(claimIDs, id)
	}
	sort.Slice(claimIDs, func(i, j int) bool { return claimIDs[i] < claimIDs[j] })
	var currentClaimIDs []LogicalID
	for _, id := range ids {
		raw, loadErr := p.repository.Load(ctx, id)
		if loadErr != nil {
			return false, loadErr
		}
		document, decodeErr := DecodeSY(raw, p.registry)
		if decodeErr != nil {
			return false, decodeErr
		}
		if document.Envelope != nil && document.Envelope.Entity.EntityType == EntityClaim && document.Envelope.Claim != nil {
			currentClaimIDs = append(currentClaimIDs, id)
		}
	}
	sort.Slice(currentClaimIDs, func(i, j int) bool { return currentClaimIDs[i] < currentClaimIDs[j] })
	if !equalLogicalIDs(claimIDs, currentClaimIDs) {
		return true, nil
	}
	for id, record := range snapshot.claims {
		raw, loadErr := p.repository.Load(ctx, id)
		if loadErr != nil {
			return true, nil
		}
		hash, hashErr := SemanticHash(raw)
		if hashErr != nil {
			return false, hashErr
		}
		if hash != record.hash {
			return true, nil
		}
	}
	return false, nil
}

func equalLogicalIDs(left, right []LogicalID) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func projectClaimRow(snapshot *claimsAVSnapshot, id LogicalID) AVRow {
	record := snapshot.claims[id]
	envelope := record.document.Envelope
	claim := envelope.Claim
	validationJSON, validationStatus := claimValidation(claim.KindData)
	row := AVRow{
		Ref:      AVRowRef{LogicalID: id, SemanticHash: record.hash},
		Revision: envelope.Entity.SemanticRevision,
		ReadOnly: record.document.ReadOnly,
		Cells: map[string]AVCellValue{
			"title":      {Kind: "text", Text: record.title},
			"logical_id": {Kind: "text", Text: string(id)},
			"claim_kind": {Kind: "text", Text: string(claim.ClaimKind)},
			"status":     {Kind: "text", Text: claim.Status},
			"tags":       {Kind: "tags", Texts: append([]string(nil), claim.Tags...)},
			"validation": {Kind: "json", Text: validationStatus, JSON: validationJSON},
			"kind_data":  {Kind: "json", JSON: append(json.RawMessage(nil), claim.KindData...)},
			"provenance": {
				Kind: "provenance", Provenance: append([]Provenance(nil), envelope.Provenance...),
			},
		},
	}
	outgoing := make([]AVEdgeValue, 0, len(envelope.Relations.Outgoing))
	for _, edge := range envelope.Relations.Outgoing {
		if edge.Schema == ArgumentSchema {
			outgoing = append(outgoing, projectEdge(edge, id, record.hash, EdgeDirectionOutgoing))
		}
	}
	incoming := append([]AVEdgeValue(nil), snapshot.reverse[id]...)
	row.Cells["arguments_outgoing"] = relationCell(outgoing)
	row.Cells["arguments_incoming"] = relationCell(filterProjectedEdges(incoming, AVColumnBinding{EdgeSchema: ArgumentSchema}))
	return row
}

func projectBinding(row AVRow, binding AVColumnBinding) (AVCellValue, error) {
	if binding.Direction != "" {
		key := "arguments_" + binding.Direction
		cell, ok := row.Cells[key]
		if !ok {
			return AVCellValue{}, validationError(CodeInvalidPlan, "column.binding", "relation binding is not projected")
		}
		edges := filterProjectedEdges(cell.Edges, binding)
		return relationCell(edges), nil
	}
	if strings.HasPrefix(binding.Field, "claim.kind_data.") {
		value := kindDataField(row.Cells["kind_data"].JSON, strings.TrimPrefix(binding.Field, "claim.kind_data."))
		if len(value) == 0 {
			return AVCellValue{Kind: "empty"}, nil
		}
		var text string
		if json.Unmarshal(value, &text) != nil {
			text = string(value)
		}
		if binding.Field == "claim.kind_data.validation" {
			text = row.Cells["validation"].Text
		}
		return AVCellValue{Kind: "json", JSON: value, Text: text}, nil
	}
	key := ""
	switch binding.Field {
	case "claim.body.title":
		key = "title"
	case "entity.logical_id":
		key = "logical_id"
	case "claim.claim_kind":
		key = "claim_kind"
	case "claim.status":
		key = "status"
	case "claim.tags":
		key = "tags"
	case "claim.kind_data":
		key = "kind_data"
	case "claim.kind_data.validation":
		key = "validation"
	case "claim.provenance":
		key = "provenance"
	}
	if key == "" {
		return AVCellValue{}, validationError(CodeInvalidPlan, "column.binding", fmt.Sprintf("unknown field binding %q", binding.Field))
	}
	return row.Cells[key], nil
}

func projectEdge(edge TypedEdge, owner LogicalID, ownerHash, direction string) AVEdgeValue {
	return AVEdgeValue{
		ID: edge.ID, OwnerID: owner, OwnerHash: ownerHash, Direction: direction,
		Schema: edge.Schema, Target: edge.Target, Type: edge.Type, Reason: edge.Reason, Context: edge.Context,
	}
}

func relationCell(edges []AVEdgeValue) AVCellValue {
	unique := map[LogicalID]struct{}{}
	for _, edge := range edges {
		id := edge.Target.LogicalID
		if edge.Direction == EdgeDirectionIncoming {
			id = edge.OwnerID
		}
		unique[id] = struct{}{}
	}
	return AVCellValue{
		Kind: "relation", Edges: edges, EdgeCount: len(edges), UniqueTargetCount: len(unique),
	}
}

func filterProjectedEdges(edges []AVEdgeValue, binding AVColumnBinding) []AVEdgeValue {
	ret := make([]AVEdgeValue, 0, len(edges))
	for _, edge := range edges {
		if binding.EdgeSchema != "" && edge.Schema != binding.EdgeSchema {
			continue
		}
		if binding.EdgeType != "" && edge.Type != binding.EdgeType {
			continue
		}
		ret = append(ret, edge)
	}
	return ret
}

func validateEdgeBinding(edge TypedEdge, binding AVColumnBinding) error {
	if binding.EdgeSchema != "" && edge.Schema != binding.EdgeSchema {
		return validationError(CodeInvalidEdge, "edge.schema", fmt.Sprintf(
			"edge schema %q does not match column schema %q", edge.Schema, binding.EdgeSchema))
	}
	if binding.EdgeType != "" && edge.Type != binding.EdgeType {
		return validationError(CodeInvalidEdge, "edge.type", fmt.Sprintf(
			"edge type %q does not match column type %q", edge.Type, binding.EdgeType))
	}
	return nil
}

func findEdge(edges []TypedEdge, id EdgeID) (int, bool) {
	for index, edge := range edges {
		if edge.ID == id {
			return index, true
		}
	}
	return -1, false
}

func reorderEdges(edges []TypedEdge, binding AVColumnBinding, order []EdgeID) ([]TypedEdge, error) {
	matching := map[EdgeID]TypedEdge{}
	for _, edge := range edges {
		if (binding.EdgeSchema == "" || edge.Schema == binding.EdgeSchema) &&
			(binding.EdgeType == "" || edge.Type == binding.EdgeType) {
			matching[edge.ID] = edge
		}
	}
	if len(order) != len(matching) {
		return nil, validationError(CodeInvalidEdge, "edge.order", "reorder must include every edge in the bound relation cell")
	}
	ordered := make([]TypedEdge, 0, len(order))
	seen := map[EdgeID]struct{}{}
	for _, id := range order {
		edge, ok := matching[id]
		if !ok {
			return nil, validationError(CodeInvalidEdge, "edge.order", fmt.Sprintf("edge %q is not in the bound relation cell", id))
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, validationError(CodeDuplicateID, "edge.order", fmt.Sprintf("edge %q appears more than once", id))
		}
		seen[id] = struct{}{}
		ordered = append(ordered, edge)
	}
	next := make([]TypedEdge, 0, len(edges))
	inserted := false
	for _, edge := range edges {
		_, matches := matching[edge.ID]
		if matches {
			if !inserted {
				next = append(next, ordered...)
				inserted = true
			}
			continue
		}
		next = append(next, edge)
	}
	if !inserted {
		next = append(next, ordered...)
	}
	return next, nil
}

func claimValidation(kindData json.RawMessage) (json.RawMessage, string) {
	var data map[string]json.RawMessage
	if json.Unmarshal(kindData, &data) != nil {
		return nil, ""
	}
	validation := append(json.RawMessage(nil), data["validation"]...)
	var value struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(validation, &value)
	return validation, value.Status
}

func claimTitle(raw []byte, fallback LogicalID) string {
	var root map[string]json.RawMessage
	if json.Unmarshal(raw, &root) != nil {
		return string(fallback)
	}
	if propertiesRaw, ok := root["Properties"]; ok {
		var properties map[string]string
		if json.Unmarshal(propertiesRaw, &properties) == nil {
			if title := strings.TrimSpace(properties["title"]); title != "" {
				return title
			}
		}
	}
	if childrenRaw, ok := root["Children"]; ok {
		var children []json.RawMessage
		if json.Unmarshal(childrenRaw, &children) == nil {
			if title := firstNodeData(children); title != "" {
				return title
			}
		}
	}
	return string(fallback)
}

func firstNodeData(nodes []json.RawMessage) string {
	for _, raw := range nodes {
		var node map[string]json.RawMessage
		if json.Unmarshal(raw, &node) != nil {
			continue
		}
		if dataRaw, ok := node["Data"]; ok {
			var data string
			if json.Unmarshal(dataRaw, &data) == nil && strings.TrimSpace(data) != "" {
				return strings.TrimSpace(data)
			}
		}
		if childrenRaw, ok := node["Children"]; ok {
			var children []json.RawMessage
			if json.Unmarshal(childrenRaw, &children) == nil {
				if data := firstNodeData(children); data != "" {
					return data
				}
			}
		}
	}
	return ""
}

func matchesAVFilters(row AVRow, filters []AVFilter) bool {
	for _, filter := range filters {
		cell, ok := row.Cells[filter.Column]
		if !ok {
			return false
		}
		switch filter.Operator {
		case "eq":
			if cell.Text != filter.Value && !containsString(cell.Texts, filter.Value) {
				return false
			}
		case "contains":
			found := strings.Contains(strings.ToLower(cell.Text), strings.ToLower(filter.Value))
			for _, value := range cell.Texts {
				found = found || strings.Contains(strings.ToLower(value), strings.ToLower(filter.Value))
			}
			if !found {
				return false
			}
		case "empty":
			if cell.Text != "" || len(cell.Texts) > 0 || len(cell.Edges) > 0 || len(cell.Provenance) > 0 {
				return false
			}
		case "not_empty":
			if cell.Text == "" && len(cell.Texts) == 0 && len(cell.Edges) == 0 && len(cell.Provenance) == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func sortAVRows(rows []AVRow, sorts []AVSort) {
	if len(sorts) == 0 {
		return
	}
	sort.SliceStable(rows, func(i, j int) bool {
		for _, item := range sorts {
			left, right := comparableCell(rows[i].Cells[item.Column]), comparableCell(rows[j].Cells[item.Column])
			if left == right {
				continue
			}
			if strings.EqualFold(item.Direction, "desc") {
				return left > right
			}
			return left < right
		}
		return rows[i].Ref.LogicalID < rows[j].Ref.LogicalID
	})
}

func comparableCell(cell AVCellValue) string {
	if cell.Kind == "relation" {
		return fmt.Sprintf("%020d:%020d", cell.EdgeCount, cell.UniqueTargetCount)
	}
	if cell.Text != "" {
		return strings.ToLower(cell.Text)
	}
	return strings.ToLower(strings.Join(cell.Texts, "\x00"))
}

func groupAVRows(rows []AVRow, column string) map[string]int {
	if column == "" {
		return nil
	}
	groups := map[string]int{}
	for _, row := range rows {
		cell, ok := row.Cells[column]
		if !ok {
			groups[""]++
			continue
		}
		if len(cell.Texts) > 0 {
			for _, value := range cell.Texts {
				groups[value]++
			}
			continue
		}
		groups[cell.Text]++
	}
	return groups
}
