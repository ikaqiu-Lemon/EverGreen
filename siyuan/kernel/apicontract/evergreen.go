package apicontract

type EvergreenDiagnostic struct {
	Code    string `json:"code"`
	Level   string `json:"level"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

type EvergreenCapabilities struct {
	APIVersion          string   `json:"api_version"`
	ChangePlanProtocols []string `json:"changeplan_protocols" api:"nonnullable"`
	DocumentSpecs       []string `json:"document_specs" api:"nonnullable"`
	BlockSpecs          []string `json:"block_specs" api:"nonnullable"`
	Commands            []string `json:"commands" api:"nonnullable"`
}

type EvergreenKindSchema struct {
	Kind                 string                 `json:"kind"`
	Schema               string                 `json:"schema"`
	AllowedStatuses      []string               `json:"allowed_statuses" api:"nonnullable"`
	AllowedEdgeSchemas   []string               `json:"allowed_edge_schemas" api:"nonnullable"`
	KindDataSchema       EvergreenJSONSchema    `json:"kind_data_schema"`
	RequiredCapabilities map[string][]string    `json:"required_capabilities,omitempty" api:"optional"`
	DefaultStatus        string                 `json:"default_status,omitempty" api:"optional"`
	DefaultKindData      JSONValue              `json:"default_kind_data,omitempty" api:"optional"`
	RequireProvenance    bool                   `json:"require_provenance"`
	Materializer         string                 `json:"materializer,omitempty" api:"optional"`
	ViewHints            EvergreenKindViewHints `json:"view_hints"`
}

// 内核发布的声明式字段 schema；未声明的 kind_data 值只读保留。
type EvergreenJSONSchema struct {
	Type       string                         `json:"type"`
	Title      string                         `json:"title,omitempty" api:"optional"`
	Properties map[string]EvergreenJSONSchema `json:"properties,omitempty" api:"optional"`
	Required   []string                       `json:"required,omitempty" api:"optional,nonnullable"`
	Enum       []string                       `json:"enum,omitempty" api:"optional,nonnullable"`
	MinLength  int                            `json:"minLength,omitempty" api:"optional"`
	Items      *EvergreenJSONSchema           `json:"items,omitempty" api:"optional"`
}

type EvergreenKindViewHints struct {
	Name    string   `json:"name"`
	Icon    string   `json:"icon,omitempty" api:"optional"`
	ViewID  string   `json:"view_id,omitempty" api:"optional"`
	Columns []string `json:"columns,omitempty" api:"optional,nonnullable"`
}

type EvergreenSchemaCatalog struct {
	APIVersion string                `json:"api_version"`
	Document   string                `json:"document"`
	Block      string                `json:"block"`
	Kinds      []EvergreenKindSchema `json:"kinds" api:"nonnullable"`
	Edges      []string              `json:"edges" api:"nonnullable"`
}

type EvergreenPrincipal struct {
	Type          string `json:"type"`
	ID            string `json:"id"`
	AuthSource    string `json:"auth_source,omitempty" api:"optional"`
	RequestReason string `json:"request_reason,omitempty" api:"optional"`
}

type EvergreenBaseRef struct {
	LogicalID    string `json:"logical_id"`
	SemanticHash string `json:"semantic_hash,omitempty" api:"optional"`
	Expected     string `json:"expected,omitempty" api:"optional,enum=absent"`
}

type EvergreenWriteInput struct {
	LogicalID string               `json:"logical_id"`
	Path      string               `json:"path,omitempty" api:"optional"`
	After     map[string]JSONValue `json:"after"`
}

type EvergreenPlanRequest struct {
	Protocol    string                `json:"protocol"`
	OperationID string                `json:"operation_id"`
	Command     string                `json:"command"`
	Base        []EvergreenBaseRef    `json:"base" api:"nonnullable"`
	Writes      []EvergreenWriteInput `json:"writes" api:"nonnullable"`
}

type EvergreenPlannedWrite struct {
	LogicalID string `json:"logical_id"`
	Before    string `json:"before,omitempty" api:"optional"`
	After     string `json:"after"`
}

type EvergreenChangePlan struct {
	OperationID string                  `json:"operation_id"`
	Protocol    string                  `json:"protocol"`
	Principal   EvergreenPrincipal      `json:"principal"`
	Command     string                  `json:"command"`
	Base        []EvergreenBaseRef      `json:"base"`
	Writes      []EvergreenPlannedWrite `json:"writes"`
	Diagnostics []EvergreenDiagnostic   `json:"diagnostics,omitempty" api:"optional,nonnullable"`
}

type EvergreenAfterImage struct {
	LogicalID    string `json:"logical_id"`
	Path         string `json:"path"`
	Bytes        string `json:"bytes"`
	SemanticHash string `json:"semantic_hash"`
	ContentHash  string `json:"content_hash"`
	Create       bool   `json:"create,omitempty" api:"optional"`
}

type EvergreenPlannedOperation struct {
	Plan     EvergreenChangePlan   `json:"plan"`
	Images   []EvergreenAfterImage `json:"images"`
	PlanHash string                `json:"plan_hash"`
}

type EvergreenApplyRequest struct {
	Operation EvergreenPlannedOperation `json:"operation"`
}

type EvergreenOperationRecord struct {
	OperationID  string                `json:"operation_id"`
	State        string                `json:"state"`
	PlanHash     string                `json:"plan_hash"`
	GitCommit    string                `json:"git_commit,omitempty" api:"optional"`
	DerivedStale bool                  `json:"derived_stale,omitempty" api:"optional"`
	Diagnostics  []EvergreenDiagnostic `json:"diagnostics,omitempty" api:"optional,nonnullable"`
}

type EvergreenCapabilitiesEnvelope struct {
	OK          bool                  `json:"ok"`
	Data        EvergreenCapabilities `json:"data"`
	Diagnostics []EvergreenDiagnostic `json:"diagnostics,omitempty" api:"optional,nonnullable"`
}

type EvergreenSchemasEnvelope struct {
	OK          bool                   `json:"ok"`
	Data        EvergreenSchemaCatalog `json:"data"`
	Diagnostics []EvergreenDiagnostic  `json:"diagnostics,omitempty" api:"optional,nonnullable"`
}

type EvergreenPlanEnvelope struct {
	OK          bool                      `json:"ok"`
	Data        EvergreenPlannedOperation `json:"data"`
	Diagnostics []EvergreenDiagnostic     `json:"diagnostics,omitempty" api:"optional,nonnullable"`
}

type EvergreenOperationEnvelope struct {
	OK          bool                     `json:"ok"`
	Data        EvergreenOperationRecord `json:"data"`
	Diagnostics []EvergreenDiagnostic    `json:"diagnostics,omitempty" api:"optional,nonnullable"`
}

type EvergreenRecoverEnvelope struct {
	OK          bool                       `json:"ok"`
	Data        []EvergreenOperationRecord `json:"data"`
	Diagnostics []EvergreenDiagnostic      `json:"diagnostics,omitempty" api:"optional,nonnullable"`
}

type EvergreenDoctorRequest struct {
	RebuildIndex bool `json:"rebuild_index,omitempty" api:"optional"`
}

type EvergreenDoctorEnvelope struct {
	OK          bool                  `json:"ok"`
	Data        JSONValue             `json:"data"`
	Diagnostics []EvergreenDiagnostic `json:"diagnostics,omitempty" api:"optional,nonnullable"`
}

type EvergreenReviewGetRequest struct {
	NoteID     string `json:"note_id,omitempty" api:"optional"`
	DocumentID string `json:"document_id,omitempty" api:"optional"`
}

type EvergreenSourceLocatorValue struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type EvergreenSourceLocator struct {
	SourceID string                      `json:"source_id"`
	Locator  EvergreenSourceLocatorValue `json:"locator"`
}

type EvergreenAnnotation struct {
	Kind  string `json:"kind"`
	Label string `json:"label,omitempty" api:"optional"`
}

type EvergreenReviewSegment struct {
	SegmentID   string                 `json:"segment_id"`
	BlockID     string                 `json:"block_id"`
	Role        string                 `json:"role"`
	SourceRef   EvergreenSourceLocator `json:"source_ref,omitempty" api:"optional"`
	Annotation  *EvergreenAnnotation   `json:"annotation,omitempty" api:"optional"`
	StoredHash  string                 `json:"stored_hash"`
	CurrentHash string                 `json:"current_hash"`
	Order       int                    `json:"order"`
}

type EvergreenCandidateMetadata struct {
	CandidateID         string            `json:"candidate_id"`
	ClaimKind           string            `json:"claim_kind"`
	KindSchema          string            `json:"kind_schema"`
	Title               string            `json:"title,omitempty" api:"optional"`
	LogicalSlug         string            `json:"logical_slug,omitempty" api:"optional"`
	SegmentRefs         []string          `json:"segment_refs" api:"nonnullable"`
	PayloadHash         string            `json:"payload_hash"`
	RefHashes           map[string]string `json:"ref_hashes"`
	Relation            string            `json:"relation,omitempty" api:"optional"`
	Reason              string            `json:"reason,omitempty" api:"optional"`
	Tags                []string          `json:"tags,omitempty" api:"optional,nonnullable"`
	State               string            `json:"state"`
	MaterializedClaimID string            `json:"materialized_claim_id,omitempty" api:"optional"`
}

type EvergreenReviewCandidate struct {
	CandidateID        string                     `json:"candidate_id"`
	BlockID            string                     `json:"block_id"`
	Metadata           EvergreenCandidateMetadata `json:"metadata"`
	Payload            []JSONValue                `json:"payload" api:"nonnullable"`
	CurrentPayloadHash string                     `json:"current_payload_hash"`
	CurrentRefHashes   map[string]string          `json:"current_ref_hashes"`
	Stale              bool                       `json:"stale"`
	StaleRefs          []string                   `json:"stale_refs" api:"nonnullable"`
	Order              int                        `json:"order"`
}

type EvergreenCoverageModule struct {
	ModuleID     string   `json:"module_id"`
	Disposition  string   `json:"disposition" api:"enum=candidate|note_only|unresolved"`
	SegmentRefs  []string `json:"segment_refs" api:"nonnullable"`
	CandidateID  string   `json:"candidate_id,omitempty" api:"optional"`
	CandidateIDs []string `json:"candidate_ids,omitempty" api:"optional,nonnullable"`
	Reason       string   `json:"reason,omitempty" api:"optional"`
}

type EvergreenSegmentLineage struct {
	EventID     string   `json:"event_id"`
	Mutation    string   `json:"mutation" api:"enum=edit|split|merge|delete"`
	PreviousIDs []string `json:"previous_ids" api:"nonnullable"`
	NextIDs     []string `json:"next_ids" api:"nonnullable"`
}

type EvergreenCoverageSummary struct {
	SegmentCount       int `json:"segment_count"`
	CandidateCount     int `json:"candidate_count"`
	CoveredSegments    int `json:"covered_segments"`
	MissingSegments    int `json:"missing_segments"`
	DuplicateSegments  int `json:"duplicate_segments"`
	UnresolvedModules  int `json:"unresolved_modules"`
	UnreferencedDrafts int `json:"unreferenced_candidates"`
}

type EvergreenReviewSnapshot struct {
	NoteID         string                     `json:"note_id"`
	DocumentID     string                     `json:"document_id"`
	SemanticHash   string                     `json:"semantic_hash"`
	Revision       uint64                     `json:"revision"`
	Segments       []EvergreenReviewSegment   `json:"segments" api:"nonnullable"`
	Candidates     []EvergreenReviewCandidate `json:"candidates" api:"nonnullable"`
	Coverage       []EvergreenCoverageModule  `json:"coverage" api:"nonnullable"`
	Lineage        []EvergreenSegmentLineage  `json:"lineage,omitempty" api:"optional,nonnullable"`
	Summary        EvergreenCoverageSummary   `json:"summary"`
	CanMaterialize bool                       `json:"can_materialize"`
	Diagnostics    []EvergreenDiagnostic      `json:"diagnostics,omitempty" api:"optional,nonnullable"`
}

type EvergreenRefHashDiff struct {
	SegmentID string `json:"segment_id"`
	Before    string `json:"before,omitempty" api:"optional"`
	After     string `json:"after,omitempty" api:"optional"`
}

type EvergreenReviewDiff struct {
	CandidateID string                 `json:"candidate_id,omitempty" api:"optional"`
	RefHashes   []EvergreenRefHashDiff `json:"ref_hashes,omitempty" api:"optional,nonnullable"`
}

type EvergreenReviewCommand struct {
	Action      string                      `json:"action" api:"enum=candidate.update|candidate.rebase|candidate.confirm|candidate.discard|coverage.update"`
	CandidateID string                      `json:"candidate_id,omitempty" api:"optional"`
	Metadata    *EvergreenCandidateMetadata `json:"metadata,omitempty" api:"optional"`
	Payload     []JSONValue                 `json:"payload,omitempty" api:"optional,nonnullable"`
	Coverage    []EvergreenCoverageModule   `json:"coverage,omitempty" api:"optional,nonnullable"`
}

type EvergreenReviewPlanRequest struct {
	OperationID string                 `json:"operation_id"`
	NoteID      string                 `json:"note_id"`
	Base        string                 `json:"base"`
	Command     EvergreenReviewCommand `json:"command"`
}

type EvergreenReviewEditPlanRequest struct {
	OperationID string                    `json:"operation_id"`
	NoteID      string                    `json:"note_id"`
	Base        string                    `json:"base"`
	Edited      map[string]JSONValue      `json:"edited"`
	Lineage     []EvergreenSegmentLineage `json:"lineage,omitempty" api:"optional,nonnullable"`
}

type EvergreenReviewMaterializeRequest struct {
	OperationID     string    `json:"operation_id"`
	NoteID          string    `json:"note_id"`
	CandidateID     string    `json:"candidate_id"`
	NoteBase        string    `json:"note_base"`
	ClaimID         string    `json:"claim_id"`
	ClaimBase       string    `json:"claim_base,omitempty" api:"optional"`
	ClaimPath       string    `json:"claim_path,omitempty" api:"optional"`
	ClaimDocumentID string    `json:"claim_document_id,omitempty" api:"optional"`
	CreatedAt       string    `json:"created_at"`
	KindData        JSONValue `json:"kind_data,omitempty" api:"optional"`
}

type EvergreenPlannedReview struct {
	Operation EvergreenPlannedOperation `json:"operation"`
	Snapshot  EvergreenReviewSnapshot   `json:"snapshot"`
	Diff      EvergreenReviewDiff       `json:"diff"`
}

type EvergreenPlannedMaterialization struct {
	Operation EvergreenPlannedOperation `json:"operation"`
	Snapshot  EvergreenReviewSnapshot   `json:"snapshot"`
	ClaimID   string                    `json:"claim_id"`
}

type EvergreenReviewEnvelope struct {
	OK          bool                     `json:"ok"`
	Data        *EvergreenReviewSnapshot `json:"data,omitempty" api:"optional"`
	Diagnostics []EvergreenDiagnostic    `json:"diagnostics,omitempty" api:"optional,nonnullable"`
}

type EvergreenReviewPlanEnvelope struct {
	OK          bool                    `json:"ok"`
	Data        *EvergreenPlannedReview `json:"data,omitempty" api:"optional"`
	Diagnostics []EvergreenDiagnostic   `json:"diagnostics,omitempty" api:"optional,nonnullable"`
}

type EvergreenMaterializePlanEnvelope struct {
	OK          bool                             `json:"ok"`
	Data        *EvergreenPlannedMaterialization `json:"data,omitempty" api:"optional"`
	Diagnostics []EvergreenDiagnostic            `json:"diagnostics,omitempty" api:"optional,nonnullable"`
}

type EvergreenAVFilter struct {
	Column   string `json:"column"`
	Operator string `json:"operator" api:"enum=eq|contains|empty|not_empty"`
	Value    string `json:"value,omitempty" api:"optional"`
}

type EvergreenAVSort struct {
	Column    string `json:"column"`
	Direction string `json:"direction" api:"enum=asc|desc"`
}

type EvergreenAVQuery struct {
	Filters []EvergreenAVFilter `json:"filters,omitempty" api:"optional,nonnullable"`
	Sorts   []EvergreenAVSort   `json:"sorts,omitempty" api:"optional,nonnullable"`
	GroupBy string              `json:"group_by,omitempty" api:"optional"`
	Offset  int                 `json:"offset,omitempty" api:"optional"`
	Limit   int                 `json:"limit,omitempty" api:"optional"`
}

type EvergreenAVQueryRequest struct {
	ViewID string           `json:"view_id"`
	Query  EvergreenAVQuery `json:"query"`
}

type EvergreenAVCreateViewRequest struct {
	ID string `json:"id"`
	// Preset 必须来自已注册 descriptor 的 ViewHints，包含内置三视图。
	Preset string `json:"preset"`
}

type EvergreenAVCreatedView struct {
	ID     string `json:"id"`
	ViewID string `json:"view_id"`
	Preset string `json:"preset"`
}

type EvergreenAVCreatedViewEnvelope struct {
	OK          bool                   `json:"ok"`
	Data        EvergreenAVCreatedView `json:"data"`
	Diagnostics []EvergreenDiagnostic  `json:"diagnostics,omitempty" api:"optional,nonnullable"`
}

type EvergreenAVRowRef struct {
	LogicalID    string `json:"logical_id"`
	SemanticHash string `json:"semantic_hash"`
}

type EvergreenAVProvenance struct {
	SourceID    string   `json:"source_id"`
	NoteID      string   `json:"note_id"`
	SegmentRefs []string `json:"segment_refs" api:"nonnullable"`
	Reason      string   `json:"reason"`
}

type EvergreenAVCellValue struct {
	Kind              string                  `json:"kind"`
	Text              string                  `json:"text,omitempty" api:"optional"`
	Texts             []string                `json:"texts,omitempty" api:"optional,nonnullable"`
	JSON              JSONValue               `json:"json,omitempty" api:"optional"`
	Provenance        []EvergreenAVProvenance `json:"provenance,omitempty" api:"optional,nonnullable"`
	Edges             []AVTypedRelationEdge   `json:"edges,omitempty" api:"optional,nonnullable"`
	EdgeCount         int                     `json:"edge_count,omitempty" api:"optional"`
	UniqueTargetCount int                     `json:"unique_target_count,omitempty" api:"optional"`
}

type EvergreenAVRow struct {
	Ref         EvergreenAVRowRef               `json:"ref"`
	Revision    uint64                          `json:"revision"`
	ReadOnly    bool                            `json:"read_only,omitempty" api:"optional"`
	Diagnostics []EvergreenDiagnostic           `json:"diagnostics,omitempty" api:"optional,nonnullable"`
	Cells       map[string]EvergreenAVCellValue `json:"cells"`
}

type EvergreenAVPage struct {
	Rows        []EvergreenAVRow `json:"rows" api:"nonnullable"`
	Total       int              `json:"total"`
	GroupCounts map[string]int   `json:"group_counts,omitempty" api:"optional"`
	Snapshot    string           `json:"snapshot"`
}

type EvergreenAVColumnDefinition struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Value    string          `json:"value"`
	Editable bool            `json:"editable"`
	Binding  AVColumnBinding `json:"binding"`
}

type EvergreenAVViewDefinition struct {
	Spec     string                        `json:"spec"`
	ID       string                        `json:"id"`
	Name     string                        `json:"name"`
	Provider AVProviderDefinition          `json:"provider"`
	Columns  []EvergreenAVColumnDefinition `json:"columns" api:"nonnullable"`
	Filters  []EvergreenAVFilter           `json:"filter,omitempty" api:"optional,nonnullable"`
	Sorts    []EvergreenAVSort             `json:"sort,omitempty" api:"optional,nonnullable"`
	GroupBy  string                        `json:"group,omitempty" api:"optional"`
	Layout   map[string]JSONValue          `json:"layout,omitempty" api:"optional"`
}

type EvergreenAVViewSchema struct {
	Definition EvergreenAVViewDefinition `json:"definition"`
	ReadOnly   bool                      `json:"read_only,omitempty" api:"optional"`
}

type EvergreenAVDescribeEnvelope struct {
	OK          bool                  `json:"ok"`
	Data        EvergreenAVViewSchema `json:"data"`
	Diagnostics []EvergreenDiagnostic `json:"diagnostics,omitempty" api:"optional,nonnullable"`
}

type EvergreenAVPageEnvelope struct {
	OK          bool                  `json:"ok"`
	Data        EvergreenAVPage       `json:"data"`
	Diagnostics []EvergreenDiagnostic `json:"diagnostics,omitempty" api:"optional,nonnullable"`
}

type EvergreenAVCellRequest struct {
	Row    EvergreenAVRowRef `json:"row"`
	Column AVColumnBinding   `json:"column"`
}

type EvergreenAVCellEnvelope struct {
	OK          bool                  `json:"ok"`
	Data        EvergreenAVCellValue  `json:"data"`
	Diagnostics []EvergreenDiagnostic `json:"diagnostics,omitempty" api:"optional,nonnullable"`
}

type EvergreenTypedEdge struct {
	ID        string               `json:"edge_id"`
	Schema    string               `json:"schema"`
	Target    AVEntityRef          `json:"target"`
	Type      string               `json:"type"`
	Reason    string               `json:"reason"`
	Context   AVEdgeContext        `json:"context,omitempty" api:"optional"`
	Extension map[string]JSONValue `json:"extension,omitempty" api:"optional"`
	CreatedAt string               `json:"created_at,omitempty" api:"optional"`
	UpdatedAt string               `json:"updated_at,omitempty" api:"optional"`
}

type EvergreenAVEdgePatch struct {
	Action  string             `json:"action" api:"enum=add|update|delete|reorder"`
	OwnerID string             `json:"owner_id,omitempty" api:"optional"`
	Edge    EvergreenTypedEdge `json:"edge,omitempty" api:"optional"`
	Order   []string           `json:"order,omitempty" api:"optional,nonnullable"`
}

type EvergreenAVPatch struct {
	OperationID string                `json:"operation_id"`
	RowID       string                `json:"row_id"`
	Base        EvergreenBaseRef      `json:"base"`
	Column      AVColumnBinding       `json:"column"`
	Value       *EvergreenAVCellValue `json:"value,omitempty" api:"optional"`
	Edge        EvergreenAVEdgePatch  `json:"edge,omitempty" api:"optional"`
}

type EvergreenAVProjectionSnapshot struct {
	Digest       string `json:"digest"`
	ClaimCount   int    `json:"claim_count"`
	EdgeCount    int    `json:"edge_count"`
	ReverseCount int    `json:"reverse_count"`
}

type EvergreenAVSnapshotEnvelope struct {
	OK          bool                          `json:"ok"`
	Data        EvergreenAVProjectionSnapshot `json:"data"`
	Diagnostics []EvergreenDiagnostic         `json:"diagnostics,omitempty" api:"optional,nonnullable"`
}
