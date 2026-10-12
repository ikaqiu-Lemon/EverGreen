package evergreencore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	SourceCaptureCommand = "source.capture"
	sourceBodyMinBytes   = 8
)

type SourceMetadata struct {
	URL     string    `json:"url"`
	SavedAt string    `json:"saved_at"`
	Reasons []string  `json:"reasons"`
	Tags    []string  `json:"tags"`
	Domain  string    `json:"domain"`
	Extra   RawObject `json:"-"`
}

type SourceCaptureRequest struct {
	OperationID string    `json:"operation_id"`
	LogicalID   LogicalID `json:"logical_id"`
	DocumentID  string    `json:"document_id"`
	Path        string    `json:"path"`
	URL         string    `json:"url"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	Reason      string    `json:"reason"`
	Domain      string    `json:"domain"`
	Tags        []string  `json:"tags"`
	CapturedAt  string    `json:"captured_at"`
	Reprocess   bool      `json:"reprocess,omitempty"`
}

type SourceSnapshot struct {
	LogicalID  LogicalID      `json:"logical_id"`
	DocumentID string         `json:"document_id"`
	Path       string         `json:"path"`
	Base       string         `json:"base"`
	Title      string         `json:"title"`
	Metadata   SourceMetadata `json:"metadata"`
	Body       []byte         `json:"body"`
	raw        []byte
}

type PlannedSourceCapture struct {
	SourceID       LogicalID         `json:"source_id"`
	Path           string            `json:"path"`
	Deduped        bool              `json:"deduped"`
	MatchedBy      string            `json:"matched_by,omitempty"`
	ReasonAppended bool              `json:"reason_appended"`
	HasNote        bool              `json:"has_note"`
	NoteID         LogicalID         `json:"note_id,omitempty"`
	Operation      *PlannedOperation `json:"operation,omitempty"`
}

func (s *AuthorityService) InspectSource(ctx context.Context, id LogicalID) (SourceSnapshot, error) {
	raw, err := s.host.Load(ctx, id)
	if err != nil {
		return SourceSnapshot{}, err
	}
	location, err := s.host.Resolve(ctx, id)
	if err != nil {
		return SourceSnapshot{}, err
	}
	return sourceSnapshot(raw, location, s.registry)
}

func (s *AuthorityService) PlanSourceCapture(
	ctx context.Context,
	principal Principal,
	request SourceCaptureRequest,
) (PlannedSourceCapture, error) {
	if err := validateSourceCaptureRequest(request); err != nil {
		return PlannedSourceCapture{}, err
	}
	hit, matchedBy, err := s.findSourceDuplicate(ctx, request.URL, request.Title)
	if err != nil {
		return PlannedSourceCapture{}, err
	}
	if hit.LogicalID != "" {
		after, appended, err := appendSourceReason(hit, request.Reason, s.registry)
		if err != nil {
			return PlannedSourceCapture{}, err
		}
		result := PlannedSourceCapture{
			SourceID: hit.LogicalID, Path: hit.Path, Deduped: true,
			MatchedBy: matchedBy, ReasonAppended: appended,
		}
		result.NoteID, result.HasNote, err = s.noteForSource(ctx, hit.LogicalID)
		if err != nil {
			return PlannedSourceCapture{}, err
		}
		if !appended {
			return result, nil
		}
		operation, err := s.Plan(ctx, principal, PlanRequest{
			Protocol: ChangePlanProtocol, OperationID: request.OperationID, Command: SourceCaptureCommand,
			Base:   []BaseRef{{LogicalID: hit.LogicalID, SemanticHash: hit.Base}},
			Writes: []WriteInput{{LogicalID: hit.LogicalID, After: after}},
		})
		if err != nil {
			return PlannedSourceCapture{}, err
		}
		result.Operation = &operation
		return result, nil
	}

	metadataRaw, _ := json.Marshal(map[string]any{
		"url": request.URL, "saved_at": request.CapturedAt,
		"reasons": []string{request.Reason}, "tags": nonNilStrings(request.Tags), "domain": request.Domain,
	})
	children, err := MarkdownChildren([]byte(request.Body), string(request.LogicalID))
	if err != nil {
		return PlannedSourceCapture{}, err
	}
	root := mustMarshalObject(nil, map[string]any{
		"ID": request.DocumentID, "Type": "NodeDocument", "Spec": "5",
		"Properties": map[string]string{"title": request.Title}, "Children": children,
	})
	after, err := EncodeSY(root, &DocumentEnvelope{
		Spec: DocumentSpec,
		Entity: Entity{
			LogicalID: request.LogicalID, EntityType: EntitySource,
			Schema: SourceSchema, SemanticRevision: 1,
		},
		Relations: Relations{Outgoing: []TypedEdge{}},
		Extension: RawObject{"source": metadataRaw},
	}, s.registry)
	if err != nil {
		return PlannedSourceCapture{}, err
	}
	operation, err := s.Plan(ctx, principal, PlanRequest{
		Protocol: ChangePlanProtocol, OperationID: request.OperationID, Command: SourceCaptureCommand,
		Base:   []BaseRef{{LogicalID: request.LogicalID, Expected: "absent"}},
		Writes: []WriteInput{{LogicalID: request.LogicalID, Path: request.Path, After: after}},
	})
	if err != nil {
		return PlannedSourceCapture{}, err
	}
	return PlannedSourceCapture{
		SourceID: request.LogicalID, Path: request.Path, ReasonAppended: true, Operation: &operation,
	}, nil
}

func validateSourceCaptureRequest(request SourceCaptureRequest) error {
	if strings.TrimSpace(request.OperationID) == "" {
		return validationError(CodeInvalidPlan, "operation_id", "operation ID is required")
	}
	if !logicalIDMatchesEntity(request.LogicalID, EntitySource) {
		return validationError(CodeInvalidEnvelope, "logical_id", "Source logical ID must use s-")
	}
	if strings.TrimSpace(request.DocumentID) == "" {
		return validationError(CodeInvalidEnvelope, "document_id", "Source document ID is required")
	}
	if err := validateAuthorityPath(request.Path); err != nil {
		return validationError(CodeInvalidPlan, "path", err.Error())
	}
	if NormalizeSourceURL(request.URL) == "" && NormalizeSourceTitle(request.Title) == "" {
		return validationError(CodeInvalidEnvelope, "url|title", "Source capture requires a URL or title")
	}
	if len(bytes.TrimSpace([]byte(request.Body))) < sourceBodyMinBytes {
		return validationError(CodeInvalidEnvelope, "body", "cleaned Source body is empty or too short")
	}
	if strings.TrimSpace(request.Reason) == "" {
		return validationError(CodeInvalidEnvelope, "reason", "Source capture reason is required")
	}
	if _, err := time.Parse(time.RFC3339, request.CapturedAt); err != nil {
		return validationError(CodeInvalidEnvelope, "captured_at", "Source capture timestamp must be RFC3339 with a timezone")
	}
	return nil
}

func (s *AuthorityService) findSourceDuplicate(
	ctx context.Context,
	rawURL string,
	title string,
) (SourceSnapshot, string, error) {
	sources, err := s.sourceSnapshots(ctx)
	if err != nil {
		return SourceSnapshot{}, "", err
	}
	urlKey := NormalizeSourceURL(rawURL)
	if urlKey != "" {
		for _, source := range sources {
			if existing := NormalizeSourceURL(source.Metadata.URL); existing != "" && existing == urlKey {
				return source, "url", nil
			}
		}
	}
	titleKey := NormalizeSourceTitle(title)
	if titleKey != "" {
		for _, source := range sources {
			if existing := NormalizeSourceTitle(source.Title); existing != "" && existing == titleKey {
				return source, "title", nil
			}
		}
	}
	return SourceSnapshot{}, "", nil
}

func (s *AuthorityService) sourceSnapshots(ctx context.Context) ([]SourceSnapshot, error) {
	ids, err := s.host.List(ctx)
	if err != nil {
		return nil, err
	}
	result := []SourceSnapshot{}
	for _, id := range ids {
		raw, err := s.host.Load(ctx, id)
		if err != nil {
			return nil, err
		}
		document, err := DecodeSY(raw, s.registry)
		if err != nil {
			return nil, err
		}
		if document.Envelope == nil {
			identity, identityErr := inspectDocumentIdentity(raw)
			if identityErr != nil {
				return nil, identityErr
			}
			if identity.EntityType == EntitySource {
				return nil, &DiagnosticError{Diagnostics: document.Diagnostics}
			}
			continue
		}
		if document.Envelope.Entity.EntityType != EntitySource {
			continue
		}
		if document.ReadOnly {
			return nil, &DiagnosticError{Diagnostics: document.Diagnostics}
		}
		location, err := s.host.Resolve(ctx, id)
		if err != nil {
			return nil, err
		}
		snapshot, err := sourceSnapshot(raw, location, s.registry)
		if err != nil {
			return nil, err
		}
		result = append(result, snapshot)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].LogicalID < result[j].LogicalID })
	return result, nil
}

func sourceSnapshot(raw []byte, location LogicalLocation, registry *Registry) (SourceSnapshot, error) {
	document, err := DecodeSY(raw, registry)
	if err != nil {
		return SourceSnapshot{}, err
	}
	if document.ReadOnly {
		return SourceSnapshot{}, &DiagnosticError{Diagnostics: document.Diagnostics}
	}
	if document.Envelope == nil || document.Envelope.Entity.EntityType != EntitySource {
		return SourceSnapshot{}, validationError(CodeInvalidEnvelope, "logical_id", "document is not a Source")
	}
	metadata, _, err := sourceMetadata(document.Envelope)
	if err != nil {
		return SourceSnapshot{}, err
	}
	root, err := decodeRawObject(raw)
	if err != nil {
		return SourceSnapshot{}, err
	}
	title, err := documentTitle(root)
	if err != nil {
		return SourceSnapshot{}, err
	}
	body, err := MarkdownText(nonNilArrayRaw(root["Children"]))
	if err != nil {
		return SourceSnapshot{}, err
	}
	hash, err := SemanticHash(raw)
	if err != nil {
		return SourceSnapshot{}, err
	}
	var documentID string
	if err = json.Unmarshal(root["ID"], &documentID); err != nil {
		return SourceSnapshot{}, err
	}
	return SourceSnapshot{
		LogicalID: document.Envelope.Entity.LogicalID, DocumentID: documentID,
		Path: location.Path, Base: hash, Title: title, Metadata: metadata, Body: body,
		raw: append([]byte(nil), raw...),
	}, nil
}

func documentTitle(root RawObject) (string, error) {
	properties, err := decodeRawObject(root["Properties"])
	if err != nil {
		return "", err
	}
	var title string
	if raw, ok := properties["title"]; ok {
		if err = json.Unmarshal(raw, &title); err != nil {
			return "", err
		}
	}
	return title, nil
}

func sourceMetadata(envelope *DocumentEnvelope) (SourceMetadata, string, error) {
	if envelope == nil {
		return SourceMetadata{}, "", validationError(CodeInvalidEnvelope, "Evergreen", "Source envelope is required")
	}
	native, hasNative := envelope.Extension["source"]
	legacy, hasLegacy := envelope.Extra["legacy_metadata"]
	if hasNative == hasLegacy {
		return SourceMetadata{}, "", validationError(
			CodeInvalidEnvelope, "Evergreen.extension.source",
			"Source requires exactly one native or legacy metadata partition",
		)
	}
	raw, location := native, "native"
	if hasLegacy {
		raw, location = legacy, "legacy"
	}
	object, err := decodeRawObject(raw)
	if err != nil {
		return SourceMetadata{}, "", validationError(CodeInvalidEnvelope, "Evergreen.source", err.Error())
	}
	var metadata SourceMetadata
	for key, target := range map[string]any{
		"url": &metadata.URL, "saved_at": &metadata.SavedAt,
		"reasons": &metadata.Reasons, "tags": &metadata.Tags, "domain": &metadata.Domain,
	} {
		if value, ok := object[key]; ok {
			if err = json.Unmarshal(value, target); err != nil {
				return SourceMetadata{}, "", validationError(CodeInvalidEnvelope, "Evergreen.source."+key, err.Error())
			}
		}
	}
	metadata.Reasons = nonNilStrings(metadata.Reasons)
	metadata.Tags = nonNilStrings(metadata.Tags)
	metadata.Extra = unknownFields(object, "url", "saved_at", "reasons", "tags", "domain")
	return metadata, location, nil
}

func appendSourceReason(
	before SourceSnapshot,
	reason string,
	registry *Registry,
) ([]byte, bool, error) {
	for _, existing := range before.Metadata.Reasons {
		if existing == reason {
			raw := beforeRawSource(before)
			if raw == nil {
				return nil, false, validationError(CodeInvalidEnvelope, "source", "Source snapshot has no authority bytes")
			}
			return raw, false, nil
		}
	}
	raw := beforeRawSource(before)
	if raw == nil {
		return nil, false, validationError(CodeInvalidEnvelope, "source", "Source snapshot has no authority bytes")
	}
	reasons := append(append([]string(nil), before.Metadata.Reasons...), reason)
	after, err := setSourceReasons(raw, reasons, 0, registry)
	return after, err == nil, err
}

func beforeRawSource(snapshot SourceSnapshot) []byte {
	return append([]byte(nil), snapshot.raw...)
}

func setSourceReasons(raw []byte, reasons []string, revision uint64, registry *Registry) ([]byte, error) {
	document, err := DecodeSY(raw, registry)
	if err != nil {
		return nil, err
	}
	_, location, err := sourceMetadata(document.Envelope)
	if err != nil {
		return nil, err
	}
	metadataRaw := document.Envelope.Extension["source"]
	if location == "legacy" {
		metadataRaw = document.Envelope.Extra["legacy_metadata"]
	}
	object, err := decodeRawObject(metadataRaw)
	if err != nil {
		return nil, err
	}
	object["reasons"], _ = json.Marshal(nonNilStrings(reasons))
	encoded, _ := json.Marshal(object)
	if location == "native" {
		document.Envelope.Extension["source"] = encoded
	} else {
		document.Envelope.Extra["legacy_metadata"] = encoded
	}
	if revision == 0 {
		document.Envelope.Entity.SemanticRevision++
	} else {
		document.Envelope.Entity.SemanticRevision = revision
	}
	return EncodeSY(raw, document.Envelope, registry)
}

func (s *AuthorityService) noteForSource(ctx context.Context, sourceID LogicalID) (LogicalID, bool, error) {
	ids, err := s.host.List(ctx)
	if err != nil {
		return "", false, err
	}
	for _, id := range ids {
		raw, err := s.host.Load(ctx, id)
		if err != nil {
			return "", false, err
		}
		document, err := DecodeSY(raw, s.registry)
		if err != nil {
			return "", false, err
		}
		if document.Envelope == nil || document.Envelope.Entity.EntityType != EntityNote {
			continue
		}
		if noteSourceID(document.Envelope) == sourceID {
			return id, true, nil
		}
	}
	return "", false, nil
}

func noteSourceID(envelope *DocumentEnvelope) LogicalID {
	for _, raw := range []json.RawMessage{envelope.Extension["note"], envelope.Extra["legacy_metadata"]} {
		if len(raw) == 0 {
			continue
		}
		object, err := decodeRawObject(raw)
		if err != nil {
			continue
		}
		var source LogicalID
		_ = json.Unmarshal(object["source"], &source)
		if source != "" {
			return source
		}
	}
	return ""
}

func SourceTrackingParams() []string {
	return []string{"utm_", "spm", "fbclid", "gclid", "ref", "from", "source"}
}

func NormalizeSourceURL(raw string) string {
	value := string(bytes.TrimSpace([]byte(raw)))
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return value
	}
	parsed.Scheme = sourceFoldASCII(parsed.Scheme)
	parsed.Host = sourceFoldASCII(parsed.Host)
	if (parsed.Scheme == "http" && strings.HasSuffix(parsed.Host, ":80")) ||
		(parsed.Scheme == "https" && strings.HasSuffix(parsed.Host, ":443")) {
		parsed.Host = parsed.Host[:strings.LastIndex(parsed.Host, ":")]
	}
	parsed.Fragment, parsed.RawFragment = "", ""
	query := parsed.Query()
	for key := range query {
		folded := sourceFoldASCII(key)
		for _, candidate := range SourceTrackingParams() {
			if folded == candidate || (strings.HasSuffix(candidate, "_") && strings.HasPrefix(folded, candidate)) {
				query.Del(key)
				break
			}
		}
	}
	parsed.RawQuery = query.Encode()
	if len(parsed.Path) > 1 {
		parsed.Path = strings.TrimRight(parsed.Path, "/")
	}
	return parsed.String()
}

func NormalizeSourceTitle(raw string) string {
	return string(bytes.TrimSpace([]byte(raw)))
}

func sourceFoldASCII(value string) string {
	raw := []byte(value)
	result := make([]byte, len(raw))
	for index, current := range raw {
		if current >= 'A' && current <= 'Z' {
			current += 'a' - 'A'
		}
		result[index] = current
	}
	return string(result)
}

func (s *AuthorityService) authorizeSourceCaptureUniqueness(
	ctx context.Context,
	operation PlannedOperation,
) error {
	if operation.Plan.Command != SourceCaptureCommand {
		return nil
	}
	if len(operation.Images) != 1 {
		return validationError(CodeUnauthorized, "writes", "Source capture requires exactly one Source write")
	}
	target := operation.Images[0]
	document, err := DecodeSY(target.Bytes, s.registry)
	if err != nil {
		return err
	}
	metadata, _, err := sourceMetadata(document.Envelope)
	if err != nil {
		return err
	}
	root, err := decodeRawObject(target.Bytes)
	if err != nil {
		return err
	}
	title, err := documentTitle(root)
	if err != nil {
		return err
	}
	sources, err := s.sourceSnapshots(ctx)
	if err != nil {
		return err
	}
	urlKey, titleKey := NormalizeSourceURL(metadata.URL), NormalizeSourceTitle(title)
	for _, source := range sources {
		if source.LogicalID == target.LogicalID {
			continue
		}
		urlDuplicate := urlKey != "" && NormalizeSourceURL(source.Metadata.URL) == urlKey
		titleDuplicate := titleKey != "" && NormalizeSourceTitle(source.Title) == titleKey
		if urlDuplicate || titleDuplicate {
			return validationError(
				CodeDuplicateID, "source",
				fmt.Sprintf("Source %q duplicates existing Source %q", target.LogicalID, source.LogicalID),
			)
		}
	}
	return nil
}
