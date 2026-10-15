package apicontract

type EvergreenNoteCreateSegment struct {
	SegmentID  string                       `json:"segment_id"`
	Role       string                       `json:"role" api:"enum=note_segment|agent_annotation"`
	Locator    *EvergreenSourceLocatorValue `json:"locator,omitempty" api:"optional"`
	Annotation *EvergreenAnnotation         `json:"annotation,omitempty" api:"optional"`
	Content    []JSONValue                  `json:"content" api:"nonnullable"`
}

type EvergreenNoteCreateCandidate struct {
	CandidateID string      `json:"candidate_id"`
	ClaimKind   string      `json:"claim_kind"`
	Title       string      `json:"title,omitempty" api:"optional"`
	LogicalSlug string      `json:"logical_slug,omitempty" api:"optional"`
	SegmentRefs []string    `json:"segment_refs" api:"nonnullable"`
	Payload     []JSONValue `json:"payload" api:"nonnullable"`
	Relation    string      `json:"relation,omitempty" api:"optional,enum=support|against|context"`
	Reason      string      `json:"reason,omitempty" api:"optional"`
	Tags        []string    `json:"tags,omitempty" api:"optional,nonnullable"`
}

type EvergreenNoteCreateRequest struct {
	OperationID string                         `json:"operation_id"`
	LogicalID   string                         `json:"logical_id"`
	DocumentID  string                         `json:"document_id"`
	Path        string                         `json:"path"`
	SourceID    string                         `json:"source_id"`
	Title       string                         `json:"title"`
	Segments    []EvergreenNoteCreateSegment   `json:"segments" api:"nonnullable"`
	Candidates  []EvergreenNoteCreateCandidate `json:"candidates" api:"nonnullable"`
	Coverage    []EvergreenCoverageModule      `json:"coverage" api:"nonnullable"`
}

type EvergreenPlannedNoteCreate struct {
	NoteID    string                    `json:"note_id"`
	SourceID  string                    `json:"source_id"`
	Path      string                    `json:"path"`
	Operation EvergreenPlannedOperation `json:"operation"`
	Snapshot  EvergreenReviewSnapshot   `json:"snapshot"`
}

type EvergreenNoteCreateEnvelope struct {
	OK          bool                       `json:"ok"`
	Data        EvergreenPlannedNoteCreate `json:"data"`
	Diagnostics []EvergreenDiagnostic      `json:"diagnostics,omitempty" api:"optional,nonnullable"`
}
