package evergreencore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

const (
	APIVersion = "evergreen.api/v1"

	CodeProtocolUnsupported   = "EG_PROTOCOL_UNSUPPORTED"
	CodeUnauthorized          = "EG_UNAUTHORIZED"
	CodeOperationIDReused     = "EG_OPERATION_ID_REUSED"
	CodeTransactionBlocked    = "EG_TRANSACTION_BLOCKED"
	CodeWorkspaceWriterActive = "EG_WORKSPACE_WRITER_ACTIVE"
	CodeWorkspaceLeaseStale   = "EG_WORKSPACE_LEASE_STALE"
	CodeGitCommitFailed       = "EG_GIT_COMMIT_FAILED"
	CodeDerivedStale          = "EG_DERIVED_STALE"
)

type Capabilities struct {
	APIVersion          string      `json:"api_version"`
	ChangePlanProtocols []string    `json:"changeplan_protocols"`
	DocumentSpecs       []SchemaRef `json:"document_specs"`
	BlockSpecs          []SchemaRef `json:"block_specs"`
	Commands            []string    `json:"commands"`
}

type APIResponse[T any] struct {
	OK          bool         `json:"ok"`
	Data        T            `json:"data"`
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
}

func CurrentCapabilities() Capabilities {
	return Capabilities{
		APIVersion:          APIVersion,
		ChangePlanProtocols: []string{ChangePlanProtocol},
		DocumentSpecs:       []SchemaRef{DocumentSpec},
		BlockSpecs:          []SchemaRef{BlockSpec},
		Commands: []string{
			SourceCaptureCommand,
			NoteCreateCommand,
			"claim.update",
			"claim.edge.update",
			"note.review.update",
			"note.review.materialize",
		},
	}
}

type SchemaCatalog struct {
	APIVersion string                 `json:"api_version"`
	Document   SchemaRef              `json:"document"`
	Block      SchemaRef              `json:"block"`
	Kinds      []KindSchemaDescriptor `json:"kinds"`
	Edges      []SchemaRef            `json:"edges"`
}

type KindSchemaDescriptor struct {
	Kind                 ClaimKind        `json:"kind"`
	Schema               SchemaRef        `json:"schema"`
	AllowedStatuses      []string         `json:"allowed_statuses"`
	AllowedEdgeSchemas   []SchemaRef      `json:"allowed_edge_schemas"`
	KindDataSchema       JSONSchema       `json:"kind_data_schema"`
	RequiredCapabilities CapabilityMatrix `json:"required_capabilities,omitempty"`
	DefaultStatus        string           `json:"default_status,omitempty"`
	DefaultKindData      json.RawMessage  `json:"default_kind_data,omitempty"`
	RequireProvenance    bool             `json:"require_provenance"`
	Materializer         string           `json:"materializer,omitempty"`
	ViewHints            KindViewHints    `json:"view_hints"`
}

func CurrentSchemas(registry *Registry) SchemaCatalog {
	if registry == nil {
		registry = DefaultRegistry()
	}
	kinds := make([]KindSchemaDescriptor, 0, len(registry.byKind))
	registry.mu.RLock()
	for _, registered := range registry.byKind {
		descriptor := cloneKindDescriptor(registered)
		kinds = append(kinds, KindSchemaDescriptor{
			Kind: descriptor.Kind, Schema: descriptor.Schema,
			AllowedStatuses:      append([]string(nil), descriptor.AllowedStatuses...),
			AllowedEdgeSchemas:   append([]SchemaRef(nil), descriptor.AllowedEdgeSchemas...),
			KindDataSchema:       descriptor.KindDataSchema,
			RequiredCapabilities: descriptor.RequiredCapabilities,
			DefaultStatus:        descriptor.DefaultStatus, DefaultKindData: descriptor.DefaultKindData,
			RequireProvenance: descriptor.RequireProvenance,
			Materializer:      descriptor.MaterializerRef, ViewHints: descriptor.ViewHints,
		})
	}
	registry.mu.RUnlock()
	sort.Slice(kinds, func(i, j int) bool { return kinds[i].Kind < kinds[j].Kind })
	return SchemaCatalog{
		APIVersion: APIVersion,
		Document:   DocumentSpec,
		Block:      BlockSpec,
		Kinds:      kinds,
		Edges:      []SchemaRef{ArgumentSchema, MaterialSchema, ReplacementSchema, BasicSchema},
	}
}

type WriteInput struct {
	LogicalID LogicalID       `json:"logical_id"`
	Path      string          `json:"path,omitempty"`
	After     json.RawMessage `json:"after"`
}

type PlanRequest struct {
	Protocol    string       `json:"protocol"`
	OperationID string       `json:"operation_id"`
	Command     string       `json:"command"`
	Base        []BaseRef    `json:"base"`
	Writes      []WriteInput `json:"writes"`

	nativeEditor bool
}

type AfterImage struct {
	LogicalID    LogicalID               `json:"logical_id"`
	Path         string                  `json:"path"`
	Bytes        []byte                  `json:"bytes"`
	SemanticHash string                  `json:"semantic_hash"`
	ContentHash  string                  `json:"content_hash"`
	Create       bool                    `json:"create,omitempty"`
	Native       *NativePhysicalDocument `json:"native,omitempty"`
	Delete       bool                    `json:"delete,omitempty"`
	Auxiliary    *NativeAuxiliaryFile    `json:"auxiliary,omitempty"`
}

type PlannedOperation struct {
	Plan     ChangePlan   `json:"plan"`
	Images   []AfterImage `json:"images"`
	PlanHash string       `json:"plan_hash"`

	nativeEditor bool
}

// UnmarshalJSON 即使复用对象也清除宿主私有授权，传输数据不能继承内存中的 native admission。
func (operation *PlannedOperation) UnmarshalJSON(raw []byte) error {
	type wireOperation PlannedOperation
	var decoded wireOperation
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	*operation = PlannedOperation(decoded)
	return nil
}

type ApplyRequest struct {
	Operation PlannedOperation `json:"operation"`
}

type OperationState string

const (
	OperationPlanned      OperationState = "PLANNED"
	OperationPrepared     OperationState = "PREPARED"
	OperationFilesApplied OperationState = "FILES_APPLIED"
	OperationGitCommitted OperationState = "GIT_COMMITTED"
	OperationCompleted    OperationState = "COMPLETED"
)

type OperationRecord struct {
	OperationID  string         `json:"operation_id"`
	State        OperationState `json:"state"`
	PlanHash     string         `json:"plan_hash"`
	GitCommit    string         `json:"git_commit,omitempty"`
	DerivedStale bool           `json:"derived_stale,omitempty"`
	Diagnostics  []Diagnostic   `json:"diagnostics,omitempty"`
}

type CommitRequest struct {
	OperationID    string            `json:"operation_id"`
	Principal      Principal         `json:"principal"`
	Command        string            `json:"command"`
	PlanHash       string            `json:"plan_hash"`
	ExpectedParent string            `json:"expected_parent,omitempty"`
	Paths          []string          `json:"paths"`
	DeletedPaths   []string          `json:"deleted_paths,omitempty"`
	AfterImages    map[string][]byte `json:"-"`
}

type OperationCommitter interface {
	Commit(context.Context, CommitRequest) (string, error)
}

type FaultInjector interface {
	Fail(point string, index int) error
}

// OperationValidator receives the complete authority before-images in write
// order. Hosts must invoke it while holding their writer gate, before journal
// creation or resuming a validated journal.
type OperationValidator func(context.Context, PlannedOperation, [][]byte) error

type AuthorityHost interface {
	Repository
	Resolve(context.Context, LogicalID) (LogicalLocation, error)
	CheckCreate(context.Context, AfterImage) error
	ApplyOperation(context.Context, PlannedOperation, OperationValidator) (OperationRecord, error)
	Operation(context.Context, string) (OperationRecord, error)
	RecoverOperations(context.Context, OperationValidator) ([]OperationRecord, error)
}

type AuthorityService struct {
	host       AuthorityHost
	authorizer Authorizer
	registry   *Registry
}

func NewAuthorityService(host AuthorityHost, authorizer Authorizer, registry *Registry) *AuthorityService {
	if registry == nil {
		registry = DefaultRegistry()
	}
	return &AuthorityService{host: host, authorizer: authorizer, registry: registry}
}

func (s *AuthorityService) Capabilities() Capabilities {
	return CurrentCapabilities()
}

func (s *AuthorityService) Schemas() SchemaCatalog {
	return CurrentSchemas(s.registry)
}

func (s *AuthorityService) Plan(ctx context.Context, principal Principal, request PlanRequest) (PlannedOperation, error) {
	if request.Protocol != ChangePlanProtocol {
		return PlannedOperation{}, validationError(
			CodeProtocolUnsupported,
			"protocol",
			fmt.Sprintf("protocol %q is not supported; expected %q", request.Protocol, ChangePlanProtocol),
		)
	}
	if strings.TrimSpace(request.OperationID) == "" {
		return PlannedOperation{}, validationError(CodeInvalidPlan, "operation_id", "operation ID is required")
	}
	if strings.TrimSpace(request.Command) == "" {
		return PlannedOperation{}, validationError(CodeInvalidPlan, "command", "command is required")
	}
	if len(request.Writes) == 0 {
		return PlannedOperation{}, validationError(CodeInvalidPlan, "writes", "at least one write is required")
	}
	if s.host == nil {
		return PlannedOperation{}, validationError(CodeInvalidPlan, "host", "authority host is unavailable")
	}

	baseByID := make(map[LogicalID]BaseRef, len(request.Base))
	for index, base := range request.Base {
		if _, duplicate := baseByID[base.LogicalID]; duplicate {
			return PlannedOperation{}, validationError(CodeDuplicateID, fmt.Sprintf("base[%d].logical_id", index), "duplicate base logical ID")
		}
		baseByID[base.LogicalID] = base
	}
	writes := append([]WriteInput(nil), request.Writes...)
	sort.Slice(writes, func(i, j int) bool { return writes[i].LogicalID < writes[j].LogicalID })

	plan := ChangePlan{
		OperationID: request.OperationID,
		Protocol:    ChangePlanProtocol,
		Principal:   principal,
		Command:     request.Command,
		Base:        make([]BaseRef, 0, len(writes)),
		Writes:      make([]PlannedWrite, 0, len(writes)),
	}
	images := make([]AfterImage, 0, len(writes))
	beforeImages := make([][]byte, 0, len(writes))
	seen := make(map[LogicalID]struct{}, len(writes))
	for index, write := range writes {
		if write.LogicalID == "" {
			return PlannedOperation{}, validationError(CodeInvalidPlan, fmt.Sprintf("writes[%d].logical_id", index), "logical ID is required")
		}
		if _, duplicate := seen[write.LogicalID]; duplicate {
			return PlannedOperation{}, validationError(CodeDuplicateID, fmt.Sprintf("writes[%d].logical_id", index), "duplicate planned write")
		}
		seen[write.LogicalID] = struct{}{}

		base, hasBase := baseByID[write.LogicalID]
		if !hasBase {
			return PlannedOperation{}, validationError(CodeBaseMismatch, fmt.Sprintf("writes[%d]", index), "write has no base entry")
		}
		current, loadErr := s.host.Load(ctx, write.LogicalID)
		create := base.Expected == "absent"
		var path string
		var beforeHash string
		switch {
		case create && loadErr == nil:
			return PlannedOperation{}, validationError(CodeBaseMismatch, fmt.Sprintf("base[%d]", index), "expected absent but entity exists")
		case create:
			if !errors.Is(loadErr, fs.ErrNotExist) {
				return PlannedOperation{}, loadErr
			}
			path = write.Path
			if err := validateAuthorityPath(path); err != nil {
				return PlannedOperation{}, validationError(CodeInvalidPlan, fmt.Sprintf("writes[%d].path", index), err.Error())
			}
		case loadErr != nil:
			var diagnosticErr *DiagnosticError
			if errors.As(loadErr, &diagnosticErr) {
				return PlannedOperation{}, loadErr
			}
			return PlannedOperation{}, validationError(CodeBaseMismatch, fmt.Sprintf("base[%d]", index), "base entity cannot be loaded: "+loadErr.Error())
		default:
			currentDocument, decodeErr := DecodeSY(current, s.registry)
			if decodeErr != nil {
				return PlannedOperation{}, decodeErr
			}
			if currentDocument.ReadOnly {
				return PlannedOperation{}, &DiagnosticError{Diagnostics: currentDocument.Diagnostics}
			}
			beforeHash, decodeErr = SemanticHash(current)
			if decodeErr != nil {
				return PlannedOperation{}, decodeErr
			}
			if base.SemanticHash != beforeHash {
				return PlannedOperation{}, validationError(
					CodeBaseMismatch,
					fmt.Sprintf("base[%d].semantic_hash", index),
					fmt.Sprintf("semantic base hash %q does not match current %q", base.SemanticHash, beforeHash),
				)
			}
			location, resolveErr := s.host.Resolve(ctx, write.LogicalID)
			if resolveErr != nil {
				return PlannedOperation{}, resolveErr
			}
			path = location.Path
		}

		after := append([]byte(nil), write.After...)
		document, decodeErr := DecodeSY(after, s.registry)
		if decodeErr != nil {
			return PlannedOperation{}, decodeErr
		}
		if document.ReadOnly {
			return PlannedOperation{}, &DiagnosticError{Diagnostics: document.Diagnostics}
		}
		if document.Envelope == nil || document.Envelope.Entity.LogicalID != write.LogicalID {
			return PlannedOperation{}, validationError(
				CodeInvalidPlan,
				fmt.Sprintf("writes[%d].after", index),
				"after-image logical ID does not match planned write",
			)
		}
		afterHash, hashErr := SemanticHash(after)
		if hashErr != nil {
			return PlannedOperation{}, hashErr
		}
		image := AfterImage{
			LogicalID:    write.LogicalID,
			Path:         path,
			Bytes:        after,
			SemanticHash: afterHash,
			ContentHash:  contentHash(after),
			Create:       create,
		}
		if create {
			if err := s.host.CheckCreate(ctx, image); err != nil {
				return PlannedOperation{}, err
			}
		}
		plan.Base = append(plan.Base, base)
		plan.Writes = append(plan.Writes, PlannedWrite{LogicalID: write.LogicalID, Before: beforeHash, After: afterHash})
		images = append(images, image)
		if create {
			beforeImages = append(beforeImages, nil)
		} else {
			beforeImages = append(beforeImages, current)
		}
	}
	if len(baseByID) != len(writes) {
		return PlannedOperation{}, validationError(CodeInvalidPlan, "base", "base contains entities outside the write set")
	}
	if err := ValidateChangePlan(plan); err != nil {
		return PlannedOperation{}, err
	}
	planned := PlannedOperation{Plan: plan, Images: images, nativeEditor: request.nativeEditor}
	planned.PlanHash = ComputePlanHash(planned.Plan, planned.Images)
	if err := ValidatePlannedOperation(planned, s.registry); err != nil {
		return PlannedOperation{}, err
	}
	if err := s.validateOperation(ctx, planned, beforeImages); err != nil {
		return PlannedOperation{}, err
	}
	return planned, nil
}

func (s *AuthorityService) Apply(ctx context.Context, principal Principal, request ApplyRequest) (OperationRecord, error) {
	operation := request.Operation
	if operation.Plan.Command == NativeEditCommand && !operation.nativeEditor {
		return OperationRecord{}, validationError(CodeUnauthorized, "command", "native editing requires host admission")
	}
	if err := ValidatePlannedOperation(operation, s.registry); err != nil {
		return OperationRecord{}, err
	}
	if operation.Plan.Principal != principal {
		return OperationRecord{}, validationError(CodeUnauthorized, "principal", "authenticated principal does not match planned principal")
	}
	return s.host.ApplyOperation(ctx, operation, s.validateOperation)
}

func (s *AuthorityService) validateOperation(ctx context.Context, operation PlannedOperation, beforeImages [][]byte) error {
	if len(beforeImages) != len(operation.Images) {
		return validationError(CodeInvalidPlan, "before", "authority before-image count does not match writes")
	}
	ids := make([]LogicalID, 0, len(operation.Plan.Writes))
	for _, write := range operation.Plan.Writes {
		ids = append(ids, write.LogicalID)
	}
	if s.authorizer == nil {
		return validationError(CodeUnauthorized, "principal", "no authorizer is configured")
	}
	principal := operation.Plan.Principal
	if err := s.authorizer.Authorize(ctx, principal, operation.Plan.Command, ids); err != nil {
		return validationError(CodeUnauthorized, "principal", err.Error())
	}
	if err := s.authorizeOperationChanges(operation, beforeImages); err != nil {
		return err
	}
	if err := s.authorizeSourceCaptureUniqueness(ctx, operation); err != nil {
		return err
	}
	if err := s.authorizeNoteCreateDependencies(ctx, operation); err != nil {
		return err
	}
	for index, image := range operation.Images {
		if image.Native != nil || image.Auxiliary != nil {
			continue // 普通文档由可信原生写集验证，不进入 Claim 字段授权。
		}
		after, err := DecodeSY(image.Bytes, s.registry)
		if err != nil {
			return err
		}
		var before *DocumentEnvelope
		if !image.Create {
			if len(beforeImages[index]) == 0 {
				return validationError(CodeInvalidPlan, "before", "update requires an authority before-image")
			}
			document, decodeErr := DecodeSY(beforeImages[index], s.registry)
			if decodeErr != nil {
				return decodeErr
			}
			if document.ReadOnly {
				return &DiagnosticError{Diagnostics: document.Diagnostics}
			}
			before = document.Envelope
		} else if len(beforeImages[index]) != 0 {
			return validationError(CodeInvalidPlan, "before", "create requires an absent before-image")
		}
		if err = AuthorizeClaimChange(principal, before, after.Envelope, s.registry); err != nil {
			return err
		}
	}
	return nil
}

func (s *AuthorityService) Operation(ctx context.Context, operationID string) (OperationRecord, error) {
	return s.host.Operation(ctx, operationID)
}

func (s *AuthorityService) Recover(ctx context.Context) ([]OperationRecord, error) {
	return s.host.RecoverOperations(ctx, s.validateOperation)
}

func ValidatePlannedOperation(operation PlannedOperation, registry *Registry) error {
	if err := ValidateChangePlan(operation.Plan); err != nil {
		return err
	}
	if registry == nil {
		registry = DefaultRegistry()
	}
	if len(operation.Images) != len(operation.Plan.Writes) {
		return validationError(CodeInvalidPlan, "images", "after-image count does not match planned writes")
	}
	if len(operation.Plan.Base) != len(operation.Images) || len(operation.Images) == 0 {
		return validationError(CodeInvalidPlan, "base", "base must match the complete nonempty write set")
	}
	baseByID := map[LogicalID]BaseRef{}
	for _, base := range operation.Plan.Base {
		baseByID[base.LogicalID] = base
	}
	paths := map[string]bool{}
	physicalIDs := map[string]bool{}
	for index := range operation.Images {
		image := operation.Images[index]
		write := operation.Plan.Writes[index]
		if image.LogicalID != write.LogicalID {
			return validationError(CodeInvalidPlan, fmt.Sprintf("images[%d].logical_id", index), "after-image order does not match planned writes")
		}
		if err := ValidateTransactionFilePath(image.Path); err != nil {
			return validationError(CodeInvalidPlan, fmt.Sprintf("images[%d].path", index), err.Error())
		}
		if paths[image.Path] || image.Create != (baseByID[image.LogicalID].Expected == "absent") ||
			image.Delete != write.Delete || (image.Delete && image.Create) {
			return validationError(CodeInvalidPlan, fmt.Sprintf("images[%d]", index), "duplicate authority path or create/base mismatch")
		}
		paths[image.Path] = true
		if image.Auxiliary != nil {
			if err := validateNativeFileImage(operation, image); err != nil {
				return err
			}
			hash := contentHash(image.Bytes)
			if image.Delete {
				hash = "absent"
			}
			if image.SemanticHash != hash || write.After != hash || image.ContentHash != contentHash(image.Bytes) {
				return validationError(CodeInvalidPlan, image.Path, "auxiliary after-image hash mismatch")
			}
			continue
		}
		if err := validateAuthorityPath(image.Path); err != nil {
			return validationError(CodeInvalidPlan, image.Path, err.Error())
		}
		var document *SYDocument
		var err error
		if !image.Delete {
			document, err = DecodeSY(image.Bytes, registry)
		} else if image.Native == nil {
			return validationError(CodeUnauthorized, image.Path, "managed deletion requires a dedicated domain command")
		}
		if err != nil {
			return err
		}
		if document != nil && document.ReadOnly {
			return &DiagnosticError{Diagnostics: document.Diagnostics}
		}
		if image.Native != nil {
			if err = validateNativePhysicalImage(operation, image, document); err != nil {
				return err
			}
		} else if document.Envelope == nil || document.Envelope.Entity.LogicalID != image.LogicalID {
			return validationError(CodeInvalidPlan, fmt.Sprintf("images[%d].bytes", index), "after-image logical ID mismatch")
		}
		var physicalID string
		if image.Delete {
			physicalID = image.Native.DocumentID
		} else {
			root, err := decodeRawObject(image.Bytes)
			if err != nil {
				return err
			}
			if err = json.Unmarshal(root["ID"], &physicalID); err != nil {
				return err
			}
		}
		if physicalIDs[physicalID] {
			return validationError(CodeDuplicateID, fmt.Sprintf("images[%d].ID", index), "duplicate physical document ID in write set")
		}
		physicalIDs[physicalID] = true
		semanticHash := "absent"
		if !image.Delete {
			semanticHash, err = SemanticHash(image.Bytes)
		}
		if err != nil {
			return err
		}
		if semanticHash != image.SemanticHash || semanticHash != write.After {
			return validationError(CodeInvalidPlan, fmt.Sprintf("images[%d].semantic_hash", index), "after-image semantic hash mismatch")
		}
		if image.ContentHash != "" && image.ContentHash != contentHash(image.Bytes) {
			return validationError(CodeInvalidPlan, fmt.Sprintf("images[%d].content_hash", index), "after-image content hash mismatch")
		}
	}
	if got := ComputePlanHash(operation.Plan, operation.Images); got != operation.PlanHash {
		return validationError(CodeInvalidPlan, "plan_hash", fmt.Sprintf("plan hash %q does not match computed %q", operation.PlanHash, got))
	}
	return nil
}

func ComputePlanHash(plan ChangePlan, images []AfterImage) string {
	type imageDigest struct {
		LogicalID    LogicalID               `json:"logical_id"`
		Path         string                  `json:"path"`
		SemanticHash string                  `json:"semantic_hash"`
		ContentHash  string                  `json:"content_hash"`
		Create       bool                    `json:"create"`
		Native       *NativePhysicalDocument `json:"native,omitempty"`
		Delete       bool                    `json:"delete,omitempty"`
		Auxiliary    *NativeAuxiliaryFile    `json:"auxiliary,omitempty"`
	}
	digests := make([]imageDigest, 0, len(images))
	for _, image := range images {
		hash := image.ContentHash
		if hash == "" {
			hash = contentHash(image.Bytes)
		}
		digests = append(digests, imageDigest{
			LogicalID: image.LogicalID, Path: image.Path, SemanticHash: image.SemanticHash,
			ContentHash: hash, Create: image.Create, Native: image.Native, Delete: image.Delete, Auxiliary: image.Auxiliary,
		})
	}
	payload, _ := json.Marshal(struct {
		Plan   ChangePlan    `json:"plan"`
		Images []imageDigest `json:"images"`
	}{Plan: plan, Images: digests})
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func contentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validateAuthorityPath(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("authority path is required")
	}
	if strings.HasPrefix(path, "/") || strings.Contains(path, "\\") {
		return errors.New("authority path must be a slash-separated relative path")
	}
	parts := strings.Split(path, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return errors.New("authority path must be normalized and may not contain . or ..")
		}
	}
	if !strings.HasSuffix(strings.ToLower(path), ".sy") {
		return errors.New("authority path must name a .sy file")
	}
	return nil
}
