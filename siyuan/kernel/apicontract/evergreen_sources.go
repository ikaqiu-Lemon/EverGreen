package apicontract

type EvergreenSourceCaptureRequest struct {
	OperationID string   `json:"operation_id"`
	LogicalID   string   `json:"logical_id"`
	DocumentID  string   `json:"document_id"`
	Path        string   `json:"path"`
	URL         string   `json:"url"`
	Title       string   `json:"title"`
	Body        string   `json:"body"`
	Reason      string   `json:"reason"`
	Domain      string   `json:"domain"`
	Tags        []string `json:"tags" api:"nonnullable"`
	CapturedAt  string   `json:"captured_at"`
	Reprocess   bool     `json:"reprocess,omitempty" api:"optional"`
}

type EvergreenPlannedSourceCapture struct {
	SourceID       string                     `json:"source_id"`
	Path           string                     `json:"path"`
	Deduped        bool                       `json:"deduped"`
	MatchedBy      string                     `json:"matched_by,omitempty" api:"optional,enum=url|title"`
	ReasonAppended bool                       `json:"reason_appended"`
	HasNote        bool                       `json:"has_note"`
	NoteID         string                     `json:"note_id,omitempty" api:"optional"`
	Operation      *EvergreenPlannedOperation `json:"operation,omitempty" api:"optional"`
}

type EvergreenSourceCaptureEnvelope struct {
	OK          bool                          `json:"ok"`
	Data        EvergreenPlannedSourceCapture `json:"data"`
	Diagnostics []EvergreenDiagnostic         `json:"diagnostics,omitempty" api:"optional,nonnullable"`
}
