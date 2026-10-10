// Package evergreencore defines the storage-independent Evergreen semantic
// contract shared by the CLI and the SiYuan kernel.
package evergreencore

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DocumentSpec        SchemaRef = "evergreen.sy/v1"
	BlockSpec           SchemaRef = "evergreen.block/v1"
	ClaimSchema         SchemaRef = "evergreen.claim/v1"
	KnowledgeKindSchema SchemaRef = "evergreen.claim-kind.knowledge/v1"
	OpinionKindSchema   SchemaRef = "evergreen.claim-kind.opinion/v1"

	ArgumentSchema    SchemaRef = "evergreen.argument/v1"
	MaterialSchema    SchemaRef = "evergreen.material/v1"
	ReplacementSchema SchemaRef = "evergreen.replacement/v1"
	BasicSchema       SchemaRef = "siyuan.basic/v1"

	ChangePlanProtocol = "evergreen.changeplan/v2"
)

const (
	CodeBaseMismatch         = "EG_CONFLICT_BASE_MISMATCH"
	CodeSchemaTooNew         = "EG_SCHEMA_TOO_NEW"
	CodeClaimKindUnavailable = "EG_CLAIM_KIND_UNAVAILABLE"
	CodeEdgeReasonRequired   = "EG_EDGE_REASON_REQUIRED"
	CodeDuplicateID          = "EG_DUPLICATE_ID"
	CodeInvalidClaim         = "EG_INVALID_CLAIM"
	CodeInvalidEdge          = "EG_INVALID_EDGE"
	CodeInvalidEnvelope      = "EG_INVALID_ENVELOPE"
	CodeInvalidBlock         = "EG_INVALID_BLOCK"
	CodeInvalidPlan          = "EG_INVALID_CHANGEPLAN"
)

type SchemaRef string
type LogicalID string
type EdgeID string
type ClaimKind string
type EntityType string

const (
	EntitySource EntityType = "source"
	EntityNote   EntityType = "note"
	EntityClaim  EntityType = "claim"
)

const (
	KnowledgeKind ClaimKind = "knowledge"
	OpinionKind   ClaimKind = "opinion"
)

type RawObject map[string]json.RawMessage

type Entity struct {
	LogicalID        LogicalID  `json:"logical_id"`
	EntityType       EntityType `json:"entity_type"`
	Schema           SchemaRef  `json:"schema"`
	SemanticRevision uint64     `json:"semantic_revision"`
	Extra            RawObject  `json:"-"`
}

type Claim struct {
	ClaimKind  ClaimKind       `json:"claim_kind"`
	KindSchema SchemaRef       `json:"kind_schema"`
	Status     string          `json:"status"`
	Tags       []string        `json:"tags"`
	KindData   json.RawMessage `json:"kind_data"`
	Extra      RawObject       `json:"-"`
}

type Provenance struct {
	SourceID    LogicalID   `json:"source_id"`
	NoteID      LogicalID   `json:"note_id"`
	SegmentRefs []LogicalID `json:"segment_refs"`
	Reason      string      `json:"reason"`
	Extra       RawObject   `json:"-"`
}

type Relations struct {
	Outgoing []TypedEdge `json:"outgoing"`
	Extra    RawObject   `json:"-"`
}

type TypedEdge struct {
	ID        EdgeID      `json:"edge_id"`
	Schema    SchemaRef   `json:"schema"`
	Target    EntityRef   `json:"target"`
	Type      string      `json:"type"`
	Reason    string      `json:"reason"`
	Context   EdgeContext `json:"context,omitempty"`
	Extension RawObject   `json:"extension,omitempty"`
	CreatedAt string      `json:"created_at,omitempty"`
	UpdatedAt string      `json:"updated_at,omitempty"`
	Extra     RawObject   `json:"-"`
}

type EntityRef struct {
	EntityType EntityType `json:"entity_type"`
	LogicalID  LogicalID  `json:"logical_id"`
	BlockID    string     `json:"block_id,omitempty"`
	Extra      RawObject  `json:"-"`
}

type EdgeContext struct {
	NoteID      LogicalID   `json:"note_id,omitempty"`
	SegmentRefs []LogicalID `json:"segment_refs,omitempty"`
	QuoteHash   string      `json:"quote_hash,omitempty"`
	Data        RawObject   `json:"data,omitempty"`
	Extra       RawObject   `json:"-"`
}

type DocumentEnvelope struct {
	Spec       SchemaRef    `json:"spec"`
	Entity     Entity       `json:"entity"`
	Claim      *Claim       `json:"claim,omitempty"`
	Provenance []Provenance `json:"provenance,omitempty"`
	Relations  Relations    `json:"relations"`
	Review     *NoteReview  `json:"review,omitempty"`
	Extension  RawObject    `json:"extension,omitempty"`
	Extra      RawObject    `json:"-"`
}

type NoteReview struct {
	Spec     SchemaRef        `json:"spec"`
	Coverage []CoverageModule `json:"coverage"`
	Extra    RawObject        `json:"-"`
}

type CoverageModule struct {
	ModuleID    LogicalID   `json:"module_id"`
	Disposition string      `json:"disposition"`
	SegmentRefs []LogicalID `json:"segment_refs"`
	CandidateID LogicalID   `json:"candidate_id,omitempty"`
	Reason      string      `json:"reason,omitempty"`
	Extra       RawObject   `json:"-"`
}

type BlockEnvelope struct {
	Spec      SchemaRef          `json:"spec"`
	Role      string             `json:"role"`
	Segment   *SegmentMetadata   `json:"segment,omitempty"`
	Candidate *CandidateMetadata `json:"candidate,omitempty"`
	Extension RawObject          `json:"extension,omitempty"`
	Extra     RawObject          `json:"-"`
}

type SegmentMetadata struct {
	SegmentID      LogicalID     `json:"segment_id"`
	SourceRef      SourceLocator `json:"source_ref"`
	NormalizedHash string        `json:"normalized_hash"`
	Extra          RawObject     `json:"-"`
}

type SourceLocator struct {
	SourceID LogicalID `json:"source_id"`
	Locator  Locator   `json:"locator"`
	Extra    RawObject `json:"-"`
}

type Locator struct {
	Kind  string    `json:"kind"`
	Value string    `json:"value"`
	Extra RawObject `json:"-"`
}

type CandidateMetadata struct {
	CandidateID         LogicalID            `json:"candidate_id"`
	ClaimKind           ClaimKind            `json:"claim_kind"`
	KindSchema          SchemaRef            `json:"kind_schema"`
	SegmentRefs         []LogicalID          `json:"segment_refs"`
	PayloadHash         string               `json:"payload_hash"`
	RefHashes           map[LogicalID]string `json:"ref_hashes"`
	State               string               `json:"state"`
	MaterializedClaimID LogicalID            `json:"materialized_claim_id,omitempty"`
	Extra               RawObject            `json:"-"`
}

type Diagnostic struct {
	Code    string `json:"code"`
	Level   string `json:"level"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

type DiagnosticError struct {
	Diagnostics []Diagnostic
}

func (e *DiagnosticError) Error() string {
	if len(e.Diagnostics) == 0 {
		return "evergreen validation failed"
	}
	first := e.Diagnostics[0]
	return fmt.Sprintf("%s at %s: %s", first.Code, first.Path, first.Message)
}

func HasDiagnostic(err error, code string) bool {
	var diagnostics *DiagnosticError
	if !errors.As(err, &diagnostics) {
		return false
	}
	for _, diagnostic := range diagnostics.Diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}

func validationError(code, path, message string) error {
	return &DiagnosticError{Diagnostics: []Diagnostic{{
		Code: code, Level: "error", Path: path, Message: message,
	}}}
}

type KindDescriptor struct {
	Kind               ClaimKind
	Schema             SchemaRef
	AllowedStatuses    []string
	AllowedEdgeSchemas []SchemaRef
	ValidateKindData   func(json.RawMessage) error
}

type Registry struct {
	mu     sync.RWMutex
	byKind map[ClaimKind]KindDescriptor
}

func NewRegistry() *Registry {
	return &Registry{byKind: map[ClaimKind]KindDescriptor{}}
}

func DefaultRegistry() *Registry {
	registry := NewRegistry()
	mustRegister := func(descriptor KindDescriptor) {
		if err := registry.Register(descriptor); err != nil {
			panic(err)
		}
	}
	mustRegister(KindDescriptor{
		Kind:               KnowledgeKind,
		Schema:             KnowledgeKindSchema,
		AllowedStatuses:    []string{"active", "deprecated", "superseded", "archived"},
		AllowedEdgeSchemas: []SchemaRef{ArgumentSchema, MaterialSchema, ReplacementSchema},
		ValidateKindData:   validateJSONObject,
	})
	mustRegister(KindDescriptor{
		Kind:               OpinionKind,
		Schema:             OpinionKindSchema,
		AllowedStatuses:    []string{"active", "deprecated", "superseded", "archived"},
		AllowedEdgeSchemas: []SchemaRef{ArgumentSchema, MaterialSchema, ReplacementSchema},
		ValidateKindData:   validateOpinionData,
	})
	return registry
}

func (r *Registry) Register(descriptor KindDescriptor) error {
	if r == nil {
		return errors.New("nil claim kind registry")
	}
	if strings.TrimSpace(string(descriptor.Kind)) == "" {
		return errors.New("claim kind is required")
	}
	if descriptor.Schema == "" {
		return errors.New("claim kind schema is required")
	}
	major, err := schemaMajor(descriptor.Schema)
	if err != nil || major < 1 {
		return fmt.Errorf("invalid claim kind schema %q", descriptor.Schema)
	}
	if len(descriptor.AllowedStatuses) == 0 {
		return errors.New("at least one claim status is required")
	}
	statuses := append([]string(nil), descriptor.AllowedStatuses...)
	sort.Strings(statuses)
	for i, status := range statuses {
		if strings.TrimSpace(status) == "" || (i > 0 && statuses[i-1] == status) {
			return fmt.Errorf("invalid or duplicate claim status %q", status)
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.byKind[descriptor.Kind]; exists {
		return fmt.Errorf("claim kind %q is already registered", descriptor.Kind)
	}
	r.byKind[descriptor.Kind] = descriptor
	return nil
}

func (r *Registry) Descriptor(kind ClaimKind) (KindDescriptor, bool) {
	if r == nil {
		return KindDescriptor{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	descriptor, ok := r.byKind[kind]
	return descriptor, ok
}

func validateJSONObject(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var object map[string]json.RawMessage
	return json.Unmarshal(raw, &object)
}

func validateOpinionData(raw json.RawMessage) error {
	var data struct {
		Validation *struct {
			Status string `json:"status"`
		} `json:"validation"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return err
	}
	if data.Validation == nil {
		return errors.New("validation is required")
	}
	switch data.Validation.Status {
	case "pending", "validated", "rejected":
		return nil
	default:
		return fmt.Errorf("invalid validation status %q", data.Validation.Status)
	}
}

func ValidateDocument(envelope *DocumentEnvelope, registry *Registry) error {
	if envelope == nil {
		return validationError(CodeInvalidEnvelope, "Evergreen", "document envelope is required")
	}
	if err := requireCurrentSchema(envelope.Spec, DocumentSpec, "Evergreen.spec"); err != nil {
		return err
	}
	if envelope.Entity.LogicalID == "" {
		return validationError(CodeInvalidEnvelope, "Evergreen.entity.logical_id", "logical ID is required")
	}
	if !logicalIDMatchesEntity(envelope.Entity.LogicalID, envelope.Entity.EntityType) {
		return validationError(
			CodeInvalidEnvelope,
			"Evergreen.entity.logical_id",
			fmt.Sprintf("logical ID %q is not valid for entity type %q", envelope.Entity.LogicalID, envelope.Entity.EntityType),
		)
	}
	if envelope.Entity.Schema == "" {
		return validationError(CodeInvalidEnvelope, "Evergreen.entity.schema", "entity schema is required")
	}
	if envelope.Entity.EntityType == EntityClaim {
		if envelope.Claim == nil {
			return validationError(CodeInvalidClaim, "Evergreen.claim", "claim metadata is required")
		}
		if err := validateClaim(envelope, registry); err != nil {
			return err
		}
	} else if envelope.Claim != nil {
		return validationError(CodeInvalidClaim, "Evergreen.claim", "claim metadata is only valid on claim entities")
	}
	for index, provenance := range envelope.Provenance {
		path := fmt.Sprintf("Evergreen.provenance[%d]", index)
		if !hasLogicalPrefix(provenance.SourceID, "s-") {
			return validationError(CodeInvalidEnvelope, path+".source_id", "source ID must use s-")
		}
		if !hasLogicalPrefix(provenance.NoteID, "n-") {
			return validationError(CodeInvalidEnvelope, path+".note_id", "note ID must use n-")
		}
		if len(provenance.SegmentRefs) == 0 {
			return validationError(CodeInvalidEnvelope, path+".segment_refs", "at least one segment reference is required")
		}
		if strings.TrimSpace(provenance.Reason) == "" {
			return validationError(CodeInvalidEnvelope, path+".reason", "provenance reason is required")
		}
	}
	return validateEdges(envelope, registry)
}

func validateClaim(envelope *DocumentEnvelope, registry *Registry) error {
	claim := envelope.Claim
	descriptor, ok := registry.Descriptor(claim.ClaimKind)
	if !ok {
		return validationError(
			CodeClaimKindUnavailable,
			"Evergreen.claim.claim_kind",
			fmt.Sprintf("claim kind %q is not registered", claim.ClaimKind),
		)
	}
	if claim.KindSchema != descriptor.Schema {
		return validationError(
			CodeInvalidClaim,
			"Evergreen.claim.kind_schema",
			fmt.Sprintf("schema %q does not match registered schema %q", claim.KindSchema, descriptor.Schema),
		)
	}
	if !containsString(descriptor.AllowedStatuses, claim.Status) {
		return validationError(
			CodeInvalidClaim,
			"Evergreen.claim.status",
			fmt.Sprintf("status %q is not allowed for claim kind %q", claim.Status, claim.ClaimKind),
		)
	}
	if descriptor.ValidateKindData != nil {
		if err := descriptor.ValidateKindData(claim.KindData); err != nil {
			return validationError(CodeInvalidClaim, "Evergreen.claim.kind_data", err.Error())
		}
	}
	return nil
}

func validateEdges(envelope *DocumentEnvelope, registry *Registry) error {
	seen := map[EdgeID]struct{}{}
	replacements := 0
	for index, edge := range envelope.Relations.Outgoing {
		path := fmt.Sprintf("Evergreen.relations.outgoing[%d]", index)
		if strings.TrimSpace(string(edge.ID)) == "" {
			return validationError(CodeInvalidEdge, path+".edge_id", "edge ID is required")
		}
		if _, exists := seen[edge.ID]; exists {
			return validationError(CodeDuplicateID, path+".edge_id", fmt.Sprintf("duplicate edge ID %q", edge.ID))
		}
		seen[edge.ID] = struct{}{}
		if strings.TrimSpace(edge.Reason) == "" && edge.Schema != BasicSchema {
			return validationError(CodeEdgeReasonRequired, path+".reason", "managed edge reason is required")
		}
		if err := validateTimestamp(edge.CreatedAt); err != nil {
			return validationError(CodeInvalidEdge, path+".created_at", err.Error())
		}
		if err := validateTimestamp(edge.UpdatedAt); err != nil {
			return validationError(CodeInvalidEdge, path+".updated_at", err.Error())
		}
		if envelope.Claim != nil {
			descriptor, _ := registry.Descriptor(envelope.Claim.ClaimKind)
			if !containsSchema(descriptor.AllowedEdgeSchemas, edge.Schema) && edge.Schema != BasicSchema {
				return validationError(CodeInvalidEdge, path+".schema", fmt.Sprintf(
					"edge schema %q is not allowed for claim kind %q", edge.Schema, envelope.Claim.ClaimKind))
			}
		}
		switch edge.Schema {
		case ArgumentSchema:
			if envelope.Entity.EntityType != EntityClaim || edge.Target.EntityType != EntityClaim ||
				!isClaimID(edge.Target.LogicalID) {
				return validationError(CodeInvalidEdge, path+".target", "argument edge must connect claim to claim")
			}
			if !containsString([]string{"derives", "supports", "limits", "opposing"}, edge.Type) {
				return validationError(CodeInvalidEdge, path+".type", fmt.Sprintf("invalid argument edge type %q", edge.Type))
			}
			if edge.Type == "opposing" && string(envelope.Entity.LogicalID) > string(edge.Target.LogicalID) {
				return validationError(CodeInvalidEdge, path+".target", "opposing edge must be stored on the canonical lower logical ID")
			}
		case MaterialSchema:
			if envelope.Entity.EntityType != EntityClaim || edge.Target.EntityType != EntitySource ||
				!hasLogicalPrefix(edge.Target.LogicalID, "s-") {
				return validationError(CodeInvalidEdge, path+".target", "material edge must connect claim to source")
			}
			if !containsString([]string{"support", "against", "context"}, edge.Type) {
				return validationError(CodeInvalidEdge, path+".type", fmt.Sprintf("invalid material edge type %q", edge.Type))
			}
			if !hasLogicalPrefix(edge.Context.NoteID, "n-") || len(edge.Context.SegmentRefs) == 0 {
				return validationError(CodeInvalidEdge, path+".context", "material edge requires note and segment context")
			}
		case ReplacementSchema:
			replacements++
			if replacements > 1 {
				return validationError(CodeInvalidEdge, path, "a claim can have at most one active replacement edge")
			}
			if envelope.Entity.EntityType != EntityClaim || edge.Target.EntityType != EntityClaim ||
				!isClaimID(edge.Target.LogicalID) || edge.Type != "replaced_by" {
				return validationError(CodeInvalidEdge, path, "replacement edge must be replaced_by from claim to claim")
			}
		case BasicSchema:
			if edge.Target.LogicalID == "" && edge.Target.BlockID == "" {
				return validationError(CodeInvalidEdge, path+".target", "basic edge requires a target")
			}
		default:
			return validationError(CodeInvalidEdge, path+".schema", fmt.Sprintf("unknown edge schema %q", edge.Schema))
		}
	}
	return nil
}

func ValidateBlock(envelope *BlockEnvelope, registry *Registry) error {
	if envelope == nil {
		return validationError(CodeInvalidBlock, "Evergreen", "block envelope is required")
	}
	if err := requireCurrentSchema(envelope.Spec, BlockSpec, "Evergreen.spec"); err != nil {
		return err
	}
	switch envelope.Role {
	case "note_segment":
		if envelope.Segment == nil || envelope.Candidate != nil {
			return validationError(CodeInvalidBlock, "Evergreen", "note_segment requires segment metadata only")
		}
		segment := envelope.Segment
		if !hasLogicalPrefix(segment.SegmentID, "seg-") {
			return validationError(CodeInvalidBlock, "Evergreen.segment.segment_id", "segment ID must use seg-")
		}
		if !hasLogicalPrefix(segment.SourceRef.SourceID, "s-") ||
			strings.TrimSpace(segment.SourceRef.Locator.Kind) == "" ||
			strings.TrimSpace(segment.SourceRef.Locator.Value) == "" ||
			strings.TrimSpace(segment.NormalizedHash) == "" {
			return validationError(CodeInvalidBlock, "Evergreen.segment", "segment source locator and normalized hash are required")
		}
	case "candidate":
		if envelope.Candidate == nil || envelope.Segment != nil {
			return validationError(CodeInvalidBlock, "Evergreen", "candidate requires candidate metadata only")
		}
		candidate := envelope.Candidate
		if !hasLogicalPrefix(candidate.CandidateID, "cand-") {
			return validationError(CodeInvalidBlock, "Evergreen.candidate.candidate_id", "candidate ID must use cand-")
		}
		descriptor, ok := registry.Descriptor(candidate.ClaimKind)
		if !ok || descriptor.Schema != candidate.KindSchema {
			return validationError(CodeClaimKindUnavailable, "Evergreen.candidate.claim_kind", "candidate claim kind is unavailable")
		}
		if len(candidate.SegmentRefs) == 0 || strings.TrimSpace(candidate.PayloadHash) == "" ||
			strings.TrimSpace(candidate.State) == "" {
			return validationError(CodeInvalidBlock, "Evergreen.candidate", "candidate refs, payload hash, and state are required")
		}
		for _, segmentID := range candidate.SegmentRefs {
			if !hasLogicalPrefix(segmentID, "seg-") || strings.TrimSpace(candidate.RefHashes[segmentID]) == "" {
				return validationError(CodeInvalidBlock, "Evergreen.candidate.ref_hashes", "every candidate segment requires a captured hash")
			}
		}
	default:
		return validationError(CodeInvalidBlock, "Evergreen.role", fmt.Sprintf("unsupported managed block role %q", envelope.Role))
	}
	return nil
}

func requireCurrentSchema(got, current SchemaRef, path string) error {
	gotMajor, err := schemaMajor(got)
	if err != nil {
		return validationError(CodeInvalidEnvelope, path, err.Error())
	}
	currentMajor, _ := schemaMajor(current)
	if gotMajor > currentMajor {
		return validationError(CodeSchemaTooNew, path, fmt.Sprintf("schema %q is newer than supported %q", got, current))
	}
	if got != current {
		return validationError(CodeInvalidEnvelope, path, fmt.Sprintf("schema %q requires an explicit migration to %q", got, current))
	}
	return nil
}

func schemaMajor(schema SchemaRef) (int, error) {
	raw := string(schema)
	index := strings.LastIndex(raw, "/v")
	if index < 1 || index+2 == len(raw) {
		return 0, fmt.Errorf("schema %q has no /v<major> suffix", schema)
	}
	major, err := strconv.Atoi(raw[index+2:])
	if err != nil || major < 1 {
		return 0, fmt.Errorf("schema %q has an invalid major version", schema)
	}
	return major, nil
}

func logicalIDMatchesEntity(id LogicalID, entityType EntityType) bool {
	switch entityType {
	case EntitySource:
		return hasLogicalPrefix(id, "s-")
	case EntityNote:
		return hasLogicalPrefix(id, "n-")
	case EntityClaim:
		return isClaimID(id)
	default:
		return false
	}
}

func isClaimID(id LogicalID) bool {
	return hasLogicalPrefix(id, "c-") || hasLogicalPrefix(id, "k-") || hasLogicalPrefix(id, "o-")
}

func hasLogicalPrefix(id LogicalID, prefix string) bool {
	raw := string(id)
	return strings.HasPrefix(raw, prefix) && len(raw) > len(prefix) &&
		!strings.ContainsAny(raw, " \t\r\n/")
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsSchema(values []SchemaRef, want SchemaRef) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func validateTimestamp(raw string) error {
	if raw == "" {
		return nil
	}
	if _, err := time.Parse(time.RFC3339Nano, raw); err != nil {
		return fmt.Errorf("timestamp %q must be RFC3339", raw)
	}
	return nil
}
