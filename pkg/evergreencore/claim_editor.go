package evergreencore

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const CodeBulkCapability = "EG_BULK_CAPABILITY_UNAVAILABLE"

type ClaimEdit struct {
	Status     *string         `json:"status,omitempty"`
	Tags       *[]string       `json:"tags,omitempty"`
	KindData   json.RawMessage `json:"kind_data,omitempty"`
	Provenance *[]Provenance   `json:"provenance,omitempty"`
}

type ClaimEditRequest struct {
	OperationID string    `json:"operation_id"`
	LogicalID   LogicalID `json:"logical_id"`
	Base        string    `json:"base"`
	Edit        ClaimEdit `json:"edit"`
}

type ClaimCreateRequest struct {
	OperationID string          `json:"operation_id"`
	LogicalID   LogicalID       `json:"logical_id"`
	DocumentID  string          `json:"document_id"`
	Path        string          `json:"path"`
	Title       string          `json:"title"`
	Body        json.RawMessage `json:"body"`
	Kind        ClaimKind       `json:"kind"`
	Tags        []string        `json:"tags"`
	KindData    json.RawMessage `json:"kind_data"`
	Provenance  []Provenance    `json:"provenance"`
}

type ClaimSnapshot struct {
	LogicalID   LogicalID         `json:"logical_id"`
	DocumentID  string            `json:"document_id"`
	Base        string            `json:"base"`
	Title       string            `json:"title"`
	Envelope    *DocumentEnvelope `json:"envelope"`
	Body        json.RawMessage   `json:"body"`
	ReadOnly    bool              `json:"read_only"`
	Diagnostics []Diagnostic      `json:"diagnostics,omitempty"`
}

type BulkClaimRequest struct {
	OperationID string     `json:"operation_id"`
	Rows        []AVRowRef `json:"rows"`
	Edit        ClaimEdit  `json:"edit"`
}

type BulkClaimCapabilities struct {
	Fields   []string `json:"fields"`
	Statuses []string `json:"statuses"`
}

func (s *AuthorityService) InspectClaim(ctx context.Context, id LogicalID) (ClaimSnapshot, error) {
	raw, err := s.host.Load(ctx, id)
	if err != nil {
		return ClaimSnapshot{}, err
	}
	document, err := DecodeSY(raw, s.registry)
	if err != nil {
		return ClaimSnapshot{}, err
	}
	if document.Envelope == nil || document.Envelope.Claim == nil {
		return ClaimSnapshot{}, validationError(CodeInvalidClaim, "logical_id", "document is not a Claim")
	}
	root, _ := decodeRawObject(raw)
	var physicalID string
	_ = json.Unmarshal(root["ID"], &physicalID)
	hash, err := SemanticHash(raw)
	if err != nil {
		return ClaimSnapshot{}, err
	}
	return ClaimSnapshot{
		LogicalID: id, DocumentID: physicalID, Base: hash, Title: claimTitle(raw, id),
		Envelope: document.Envelope, Body: nonNilArrayRaw(root["Children"]),
		ReadOnly: document.ReadOnly, Diagnostics: document.Diagnostics,
	}, nil
}

func (s *AuthorityService) PlanClaimEdit(ctx context.Context, principal Principal, request ClaimEditRequest) (PlannedOperation, error) {
	raw, err := s.host.Load(ctx, request.LogicalID)
	if err != nil {
		return PlannedOperation{}, err
	}
	after, err := editClaimDocument(raw, request.Edit, principal, s.registry)
	if err != nil {
		return PlannedOperation{}, err
	}
	return s.Plan(ctx, principal, PlanRequest{
		Protocol: ChangePlanProtocol, OperationID: request.OperationID, Command: "claim.update",
		Base:   []BaseRef{{LogicalID: request.LogicalID, SemanticHash: request.Base}},
		Writes: []WriteInput{{LogicalID: request.LogicalID, After: after}},
	})
}

func (s *AuthorityService) PlanClaimCreate(ctx context.Context, principal Principal, request ClaimCreateRequest) (PlannedOperation, error) {
	descriptor, ok := s.registry.Descriptor(request.Kind)
	if !ok {
		return PlannedOperation{}, validationError(CodeClaimKindUnavailable, "kind", "kind descriptor is unavailable")
	}
	if descriptor.DefaultStatus == "" {
		return PlannedOperation{}, validationError(CodeInvalidClaim, "kind", "kind has no creation policy")
	}
	if strings.TrimSpace(request.Title) == "" {
		return PlannedOperation{}, validationError(CodeInvalidClaim, "title", "Claim title is required")
	}
	var body []json.RawMessage
	if err := json.Unmarshal(request.Body, &body); err != nil || len(body) == 0 {
		return PlannedOperation{}, validationError(CodeInvalidClaim, "body", "Claim body requires native blocks")
	}
	root := mustMarshalObject(nil, map[string]any{
		"ID": request.DocumentID, "Type": "NodeDocument", "Spec": "5",
		"Properties": map[string]string{"title": request.Title}, "Children": body,
	})
	kindData := request.KindData
	if len(kindData) == 0 {
		kindData = descriptor.DefaultKindData
	}
	envelope := &DocumentEnvelope{
		Spec:   DocumentSpec,
		Entity: Entity{LogicalID: request.LogicalID, EntityType: EntityClaim, Schema: ClaimSchema, SemanticRevision: 1},
		Claim: &Claim{
			ClaimKind: descriptor.Kind, KindSchema: descriptor.Schema,
			Status: descriptor.DefaultStatus, Tags: request.Tags, KindData: kindData,
		},
		Provenance: request.Provenance, Relations: Relations{Outgoing: []TypedEdge{}},
	}
	after, err := EncodeSY(root, envelope, s.registry)
	if err != nil {
		return PlannedOperation{}, err
	}
	return s.Plan(ctx, principal, PlanRequest{
		Protocol: ChangePlanProtocol, OperationID: request.OperationID, Command: "claim.update",
		Base:   []BaseRef{{LogicalID: request.LogicalID, Expected: "absent"}},
		Writes: []WriteInput{{LogicalID: request.LogicalID, Path: request.Path, After: after}},
	})
}

func editClaimDocument(raw []byte, edit ClaimEdit, principal Principal, registry *Registry) ([]byte, error) {
	before, err := DecodeSY(raw, registry)
	if err != nil {
		return nil, err
	}
	if before.ReadOnly {
		return nil, &DiagnosticError{Diagnostics: before.Diagnostics}
	}
	if before.Envelope == nil || before.Envelope.Claim == nil {
		return nil, validationError(CodeInvalidClaim, "logical_id", "document is not a Claim")
	}
	after, err := DecodeSY(raw, registry)
	if err != nil {
		return nil, err
	}
	claim := after.Envelope.Claim
	if edit.Status != nil {
		claim.Status = *edit.Status
	}
	if edit.Tags != nil {
		claim.Tags = append([]string(nil), (*edit.Tags)...)
	}
	if len(edit.KindData) > 0 {
		// The editor sends a merge patch of declared fields. Undeclared values in
		// the authoritative payload cannot be silently erased by an older UI.
		claim.KindData, err = mergeKindData(claim.KindData, edit.KindData)
		if err != nil {
			return nil, validationError(CodeInvalidClaim, "claim.kind_data", err.Error())
		}
	}
	if edit.Provenance != nil {
		after.Envelope.Provenance = append([]Provenance(nil), (*edit.Provenance)...)
	}
	if err = AuthorizeClaimChange(principal, before.Envelope, after.Envelope, registry); err != nil {
		return nil, err
	}
	after.Envelope.Entity.SemanticRevision++
	return EncodeSY(raw, after.Envelope, registry)
}

func mergeKindData(base, patch json.RawMessage) (json.RawMessage, error) {
	current, err := decodeRawObject(nonNilRaw(base))
	if err != nil {
		return nil, err
	}
	changes, err := decodeRawObject(patch)
	if err != nil {
		return nil, err
	}
	for field, value := range changes {
		if len(value) > 0 && value[0] == '{' && len(current[field]) > 0 && current[field][0] == '{' {
			value, err = mergeKindData(current[field], value)
			if err != nil {
				return nil, err
			}
		}
		current[field] = value
	}
	return json.Marshal(current)
}

func (s *AuthorityService) BulkCapabilities(ctx context.Context, principal Principal, rows []AVRowRef) (BulkClaimCapabilities, error) {
	if len(rows) == 0 {
		return BulkClaimCapabilities{}, validationError(CodeInvalidPlan, "rows", "select at least one Claim")
	}
	var result BulkClaimCapabilities
	for index, row := range rows {
		snapshot, err := s.InspectClaim(ctx, row.LogicalID)
		if err != nil {
			return BulkClaimCapabilities{}, err
		}
		if snapshot.ReadOnly {
			return BulkClaimCapabilities{}, &DiagnosticError{Diagnostics: snapshot.Diagnostics}
		}
		if row.SemanticHash != snapshot.Base {
			return BulkClaimCapabilities{}, validationError(CodeBaseMismatch, fmt.Sprintf("rows[%d]", index), "Claim base is stale")
		}
		descriptor, _ := s.registry.Descriptor(snapshot.Envelope.Claim.ClaimKind)
		allowed := []string{}
		// kind_data and provenance are individually edited; their meanings are
		// not interchangeable merely because every Claim stores an object.
		for _, field := range []string{"claim.status", "claim.tags"} {
			if fieldAllowed(descriptor, principal.Type, field) {
				allowed = append(allowed, field)
			}
		}
		if index == 0 {
			result.Fields = allowed
			result.Statuses = append([]string(nil), descriptor.AllowedStatuses...)
		} else {
			result.Fields = stringIntersection(result.Fields, allowed)
			result.Statuses = stringIntersection(result.Statuses, descriptor.AllowedStatuses)
		}
	}
	sort.Strings(result.Fields)
	sort.Strings(result.Statuses)
	return result, nil
}

func (s *AuthorityService) PlanBulkClaims(ctx context.Context, principal Principal, request BulkClaimRequest) (PlannedOperation, error) {
	capabilities, err := s.BulkCapabilities(ctx, principal, request.Rows)
	if err != nil {
		return PlannedOperation{}, err
	}
	if request.Edit.Status != nil && (!containsString(capabilities.Fields, "claim.status") ||
		!containsString(capabilities.Statuses, *request.Edit.Status)) {
		return PlannedOperation{}, validationError(CodeBulkCapability, "edit.status", "status is not in the selected kinds' intersection")
	}
	if request.Edit.Tags != nil && !containsString(capabilities.Fields, "claim.tags") ||
		len(request.Edit.KindData) > 0 || request.Edit.Provenance != nil {
		return PlannedOperation{}, validationError(CodeBulkCapability, "edit", "field is not a shared bulk capability")
	}
	plan := PlanRequest{Protocol: ChangePlanProtocol, OperationID: request.OperationID, Command: "claim.update"}
	for _, row := range request.Rows {
		raw, loadErr := s.host.Load(ctx, row.LogicalID)
		if loadErr != nil {
			return PlannedOperation{}, loadErr
		}
		after, editErr := editClaimDocument(raw, request.Edit, principal, s.registry)
		if editErr != nil {
			return PlannedOperation{}, editErr
		}
		plan.Base = append(plan.Base, BaseRef{LogicalID: row.LogicalID, SemanticHash: row.SemanticHash})
		plan.Writes = append(plan.Writes, WriteInput{LogicalID: row.LogicalID, After: after})
	}
	return s.Plan(ctx, principal, plan)
}

func stringIntersection(left, right []string) []string {
	result := []string{}
	for _, value := range left {
		if containsString(right, value) {
			result = append(result, value)
		}
	}
	return result
}

func nonNilArrayRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`[]`)
	}
	return raw
}
