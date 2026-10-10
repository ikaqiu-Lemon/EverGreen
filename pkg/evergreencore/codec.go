package evergreencore

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const evergreenJSONKey = "Evergreen"

type SYDocument struct {
	Raw         []byte
	Envelope    *DocumentEnvelope
	ReadOnly    bool
	Diagnostics []Diagnostic
}

func DecodeSY(data []byte, registry *Registry) (*SYDocument, error) {
	root, err := decodeRawObject(data)
	if err != nil {
		return nil, validationError(CodeInvalidEnvelope, "$", fmt.Sprintf("invalid .sy JSON: %v", err))
	}
	if err = validateSYRoot(root); err != nil {
		return nil, err
	}
	document := &SYDocument{Raw: append([]byte(nil), data...)}
	envelopeRaw, exists := root[evergreenJSONKey]
	if !exists {
		return document, nil
	}
	spec, err := envelopeSpec(envelopeRaw)
	if err != nil {
		return nil, err
	}
	major, err := schemaMajor(spec)
	if err != nil {
		return nil, validationError(CodeInvalidEnvelope, "Evergreen.spec", err.Error())
	}
	currentMajor, _ := schemaMajor(DocumentSpec)
	if major > currentMajor {
		document.ReadOnly = true
		document.Diagnostics = []Diagnostic{{
			Code: CodeSchemaTooNew, Level: "error", Path: "Evergreen.spec",
			Message: fmt.Sprintf("schema %q is newer than supported %q", spec, DocumentSpec),
		}}
		return document, nil
	}
	envelope, err := UnmarshalDocumentEnvelope(envelopeRaw)
	if err != nil {
		return nil, err
	}
	document.Envelope = envelope
	if err = ValidateDocument(envelope, registry); err != nil {
		if HasDiagnostic(err, CodeClaimKindUnavailable) {
			var diagnosticErr *DiagnosticError
			if errorsAsDiagnostic(err, &diagnosticErr) {
				document.ReadOnly = true
				document.Diagnostics = append(document.Diagnostics, diagnosticErr.Diagnostics...)
				return document, nil
			}
		}
		return nil, err
	}
	if diagnostics, blockErr := validateBlockEnvelopes(root, registry); blockErr != nil {
		return nil, blockErr
	} else {
		document.Diagnostics = append(document.Diagnostics, diagnostics...)
		for _, diagnostic := range diagnostics {
			if diagnostic.Code == CodeSchemaTooNew || diagnostic.Code == CodeClaimKindUnavailable {
				document.ReadOnly = true
			}
		}
	}
	return document, nil
}

func EncodeSY(base []byte, envelope *DocumentEnvelope, registry *Registry) ([]byte, error) {
	root, err := decodeRawObject(base)
	if err != nil {
		return nil, validationError(CodeInvalidEnvelope, "$", fmt.Sprintf("invalid .sy JSON: %v", err))
	}
	if err = validateSYRoot(root); err != nil {
		return nil, err
	}
	if raw, ok := root[evergreenJSONKey]; ok {
		spec, specErr := envelopeSpec(raw)
		if specErr != nil {
			return nil, specErr
		}
		major, _ := schemaMajor(spec)
		currentMajor, _ := schemaMajor(DocumentSpec)
		if major > currentMajor {
			return nil, validationError(
				CodeSchemaTooNew,
				"Evergreen.spec",
				fmt.Sprintf("refusing to overwrite too-new schema %q", spec),
			)
		}
	}
	if err = ValidateDocument(envelope, registry); err != nil {
		return nil, err
	}
	envelopeRaw, err := MarshalDocumentEnvelope(envelope)
	if err != nil {
		return nil, err
	}
	root[evergreenJSONKey] = envelopeRaw
	encoded, err := json.Marshal(root)
	if err != nil {
		return nil, err
	}
	reparsed, err := DecodeSY(encoded, registry)
	if err != nil {
		return nil, err
	}
	if reparsed.ReadOnly {
		return nil, &DiagnosticError{Diagnostics: reparsed.Diagnostics}
	}
	return encoded, nil
}

func ExtractEnvelopeJSON(data []byte) (json.RawMessage, bool, error) {
	root, err := decodeRawObject(data)
	if err != nil {
		return nil, false, err
	}
	raw, ok := root[evergreenJSONKey]
	if !ok {
		return nil, false, nil
	}
	return append(json.RawMessage(nil), raw...), true, nil
}

func InjectEnvelopeJSON(data []byte, envelope json.RawMessage) ([]byte, error) {
	root, err := decodeRawObject(data)
	if err != nil {
		return nil, err
	}
	if err = validateSYRoot(root); err != nil {
		return nil, err
	}
	if !json.Valid(envelope) {
		return nil, validationError(CodeInvalidEnvelope, "Evergreen", "envelope JSON is invalid")
	}
	root[evergreenJSONKey] = append(json.RawMessage(nil), envelope...)
	return json.Marshal(root)
}

func MarshalDocumentEnvelope(envelope *DocumentEnvelope) (json.RawMessage, error) {
	if envelope == nil {
		return nil, validationError(CodeInvalidEnvelope, "Evergreen", "document envelope is required")
	}
	entity, err := marshalObject(envelope.Entity.Extra, map[string]any{
		"logical_id":        envelope.Entity.LogicalID,
		"entity_type":       envelope.Entity.EntityType,
		"schema":            envelope.Entity.Schema,
		"semantic_revision": envelope.Entity.SemanticRevision,
	})
	if err != nil {
		return nil, err
	}
	relations, err := marshalRelations(envelope.Relations)
	if err != nil {
		return nil, err
	}
	fields := map[string]any{
		"spec":      envelope.Spec,
		"entity":    entity,
		"relations": relations,
	}
	if envelope.Claim != nil {
		claim, marshalErr := marshalObject(envelope.Claim.Extra, map[string]any{
			"claim_kind":  envelope.Claim.ClaimKind,
			"kind_schema": envelope.Claim.KindSchema,
			"status":      envelope.Claim.Status,
			"tags":        nonNilStrings(envelope.Claim.Tags),
			"kind_data":   nonNilRaw(envelope.Claim.KindData),
		})
		if marshalErr != nil {
			return nil, marshalErr
		}
		fields["claim"] = claim
	}
	if len(envelope.Provenance) > 0 {
		items := make([]json.RawMessage, 0, len(envelope.Provenance))
		for _, provenance := range envelope.Provenance {
			item, marshalErr := marshalObject(provenance.Extra, map[string]any{
				"source_id":    provenance.SourceID,
				"note_id":      provenance.NoteID,
				"segment_refs": nonNilLogicalIDs(provenance.SegmentRefs),
				"reason":       provenance.Reason,
			})
			if marshalErr != nil {
				return nil, marshalErr
			}
			items = append(items, item)
		}
		fields["provenance"] = items
	}
	if envelope.Review != nil {
		review, marshalErr := marshalNoteReview(*envelope.Review)
		if marshalErr != nil {
			return nil, marshalErr
		}
		fields["review"] = review
	}
	if len(envelope.Extension) > 0 {
		fields["extension"] = envelope.Extension
	}
	return marshalObject(envelope.Extra, fields)
}

func UnmarshalDocumentEnvelope(data []byte) (*DocumentEnvelope, error) {
	object, err := decodeRawObject(data)
	if err != nil {
		return nil, validationError(CodeInvalidEnvelope, "Evergreen", err.Error())
	}
	var envelope DocumentEnvelope
	if err = decodeRequired(object, "spec", &envelope.Spec); err != nil {
		return nil, err
	}
	entityRaw, err := requiredRaw(object, "entity")
	if err != nil {
		return nil, err
	}
	if envelope.Entity, err = unmarshalEntity(entityRaw); err != nil {
		return nil, err
	}
	if raw, ok := object["claim"]; ok {
		claim, claimErr := unmarshalClaim(raw)
		if claimErr != nil {
			return nil, claimErr
		}
		envelope.Claim = &claim
	}
	if raw, ok := object["provenance"]; ok {
		var items []json.RawMessage
		if err = json.Unmarshal(raw, &items); err != nil {
			return nil, validationError(CodeInvalidEnvelope, "Evergreen.provenance", err.Error())
		}
		envelope.Provenance = make([]Provenance, 0, len(items))
		for index, item := range items {
			provenance, itemErr := unmarshalProvenance(item)
			if itemErr != nil {
				return nil, withPath(itemErr, fmt.Sprintf("Evergreen.provenance[%d]", index))
			}
			envelope.Provenance = append(envelope.Provenance, provenance)
		}
	}
	if raw, ok := object["relations"]; ok {
		if envelope.Relations, err = unmarshalRelations(raw); err != nil {
			return nil, err
		}
	} else {
		envelope.Relations.Outgoing = []TypedEdge{}
	}
	if raw, ok := object["review"]; ok {
		review, reviewErr := unmarshalNoteReview(raw)
		if reviewErr != nil {
			return nil, reviewErr
		}
		envelope.Review = &review
	}
	if raw, ok := object["extension"]; ok {
		if err = json.Unmarshal(raw, &envelope.Extension); err != nil {
			return nil, validationError(CodeInvalidEnvelope, "Evergreen.extension", err.Error())
		}
	}
	envelope.Extra = unknownFields(object,
		"spec", "entity", "claim", "provenance", "relations", "review", "extension")
	return &envelope, nil
}

func MarshalBlockEnvelope(envelope *BlockEnvelope) (json.RawMessage, error) {
	if envelope == nil {
		return nil, validationError(CodeInvalidBlock, "Evergreen", "block envelope is required")
	}
	fields := map[string]any{"spec": envelope.Spec, "role": envelope.Role}
	if envelope.Segment != nil {
		segmentFields := map[string]any{
			"segment_id":      envelope.Segment.SegmentID,
			"normalized_hash": envelope.Segment.NormalizedHash,
		}
		if envelope.Segment.SourceRef.SourceID != "" {
			sourceRef, err := marshalObject(envelope.Segment.SourceRef.Extra, map[string]any{
				"source_id": envelope.Segment.SourceRef.SourceID,
				"locator": mustMarshalObject(envelope.Segment.SourceRef.Locator.Extra, map[string]any{
					"kind": envelope.Segment.SourceRef.Locator.Kind, "value": envelope.Segment.SourceRef.Locator.Value,
				}),
			})
			if err != nil {
				return nil, err
			}
			segmentFields["source_ref"] = sourceRef
		}
		if envelope.Segment.Annotation != nil {
			annotation, err := marshalObject(envelope.Segment.Annotation.Extra, map[string]any{
				"kind": envelope.Segment.Annotation.Kind, "label": envelope.Segment.Annotation.Label,
			})
			if err != nil {
				return nil, err
			}
			segmentFields["annotation"] = annotation
		}
		segment, err := marshalObject(envelope.Segment.Extra, segmentFields)
		if err != nil {
			return nil, err
		}
		fields["segment"] = segment
	}
	if envelope.Candidate != nil {
		candidate, err := marshalObject(envelope.Candidate.Extra, map[string]any{
			"candidate_id":          envelope.Candidate.CandidateID,
			"claim_kind":            envelope.Candidate.ClaimKind,
			"kind_schema":           envelope.Candidate.KindSchema,
			"title":                 envelope.Candidate.Title,
			"logical_slug":          envelope.Candidate.LogicalSlug,
			"segment_refs":          nonNilLogicalIDs(envelope.Candidate.SegmentRefs),
			"payload_hash":          envelope.Candidate.PayloadHash,
			"ref_hashes":            nonNilRefHashes(envelope.Candidate.RefHashes),
			"relation":              envelope.Candidate.Relation,
			"reason":                envelope.Candidate.Reason,
			"tags":                  nonNilStrings(envelope.Candidate.Tags),
			"state":                 envelope.Candidate.State,
			"materialized_claim_id": envelope.Candidate.MaterializedClaimID,
		})
		if err != nil {
			return nil, err
		}
		fields["candidate"] = candidate
	}
	if len(envelope.Extension) > 0 {
		fields["extension"] = envelope.Extension
	}
	return marshalObject(envelope.Extra, fields)
}

func UnmarshalBlockEnvelope(data []byte) (*BlockEnvelope, error) {
	object, err := decodeRawObject(data)
	if err != nil {
		return nil, validationError(CodeInvalidBlock, "Evergreen", err.Error())
	}
	var envelope BlockEnvelope
	if err = decodeRequired(object, "spec", &envelope.Spec); err != nil {
		return nil, err
	}
	if err = decodeRequired(object, "role", &envelope.Role); err != nil {
		return nil, err
	}
	if raw, ok := object["segment"]; ok {
		segment, segmentErr := unmarshalSegment(raw)
		if segmentErr != nil {
			return nil, segmentErr
		}
		envelope.Segment = &segment
	}
	if raw, ok := object["candidate"]; ok {
		candidate, candidateErr := unmarshalCandidate(raw)
		if candidateErr != nil {
			return nil, candidateErr
		}
		envelope.Candidate = &candidate
	}
	if raw, ok := object["extension"]; ok {
		if err = json.Unmarshal(raw, &envelope.Extension); err != nil {
			return nil, validationError(CodeInvalidBlock, "Evergreen.extension", err.Error())
		}
	}
	envelope.Extra = unknownFields(object, "spec", "role", "segment", "candidate", "extension")
	return &envelope, nil
}

func SemanticHash(data []byte) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var root any
	if err := decoder.Decode(&root); err != nil {
		return "", err
	}
	normalizeSemanticValue(root, false)
	canonical, err := json.Marshal(root)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func normalizeSemanticValue(value any, inEvergreen bool) {
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			managed := inEvergreen || key == evergreenJSONKey
			if managed && (key == "rendered_contents" || key == "index_epoch" || key == "cache") {
				delete(current, key)
				continue
			}
			normalizeSemanticValue(child, managed)
		}
		if inEvergreen {
			if tags, ok := current["tags"].([]any); ok {
				sort.SliceStable(tags, func(i, j int) bool {
					return fmt.Sprint(tags[i]) < fmt.Sprint(tags[j])
				})
			}
			if outgoing, ok := current["outgoing"].([]any); ok {
				sort.SliceStable(outgoing, func(i, j int) bool {
					return objectString(outgoing[i], "edge_id") < objectString(outgoing[j], "edge_id")
				})
			}
		}
	case []any:
		for _, child := range current {
			normalizeSemanticValue(child, inEvergreen)
		}
	}
}

func objectString(value any, key string) string {
	object, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	return fmt.Sprint(object[key])
}

func validateSYRoot(root RawObject) error {
	var nodeType string
	if err := decodeRequired(root, "Type", &nodeType); err != nil {
		return validationError(CodeInvalidEnvelope, "Type", "SiYuan document type is required")
	}
	if nodeType != "NodeDocument" {
		return validationError(CodeInvalidEnvelope, "Type", fmt.Sprintf("expected NodeDocument, got %q", nodeType))
	}
	var id string
	if err := decodeRequired(root, "ID", &id); err != nil || strings.TrimSpace(id) == "" {
		return validationError(CodeInvalidEnvelope, "ID", "SiYuan document ID is required")
	}
	return nil
}

func validateBlockEnvelopes(root RawObject, registry *Registry) ([]Diagnostic, error) {
	childrenRaw, ok := root["Children"]
	if !ok {
		return nil, nil
	}
	var children []json.RawMessage
	if err := json.Unmarshal(childrenRaw, &children); err != nil {
		return nil, validationError(CodeInvalidBlock, "Children", err.Error())
	}
	var diagnostics []Diagnostic
	var walk func([]json.RawMessage, string) error
	walk = func(nodes []json.RawMessage, prefix string) error {
		for index, nodeRaw := range nodes {
			path := fmt.Sprintf("%s[%d]", prefix, index)
			node, err := decodeRawObject(nodeRaw)
			if err != nil {
				return validationError(CodeInvalidBlock, path, err.Error())
			}
			if envelopeRaw, exists := node[evergreenJSONKey]; exists {
				spec, specErr := envelopeSpec(envelopeRaw)
				if specErr != nil {
					return withPath(specErr, path+".Evergreen")
				}
				major, _ := schemaMajor(spec)
				currentMajor, _ := schemaMajor(BlockSpec)
				if major > currentMajor {
					diagnostics = append(diagnostics, Diagnostic{
						Code: CodeSchemaTooNew, Level: "error", Path: path + ".Evergreen.spec",
						Message: fmt.Sprintf("block schema %q is newer than supported %q", spec, BlockSpec),
					})
				} else {
					envelope, decodeErr := UnmarshalBlockEnvelope(envelopeRaw)
					if decodeErr != nil {
						return withPath(decodeErr, path+".Evergreen")
					}
					if validateErr := ValidateBlock(envelope, registry); validateErr != nil {
						if HasDiagnostic(validateErr, CodeClaimKindUnavailable) {
							var diagnosticErr *DiagnosticError
							if errorsAsDiagnostic(validateErr, &diagnosticErr) {
								diagnostics = append(diagnostics, diagnosticErr.Diagnostics...)
								continue
							}
						}
						return withPath(validateErr, path+".Evergreen")
					}
				}
			}
			if nestedRaw, exists := node["Children"]; exists {
				var nested []json.RawMessage
				if err = json.Unmarshal(nestedRaw, &nested); err != nil {
					return validationError(CodeInvalidBlock, path+".Children", err.Error())
				}
				if err = walk(nested, path+".Children"); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return diagnostics, walk(children, "Children")
}

func envelopeSpec(raw json.RawMessage) (SchemaRef, error) {
	object, err := decodeRawObject(raw)
	if err != nil {
		return "", validationError(CodeInvalidEnvelope, "Evergreen", err.Error())
	}
	var spec SchemaRef
	if err = decodeRequired(object, "spec", &spec); err != nil {
		return "", validationError(CodeInvalidEnvelope, "Evergreen.spec", "schema spec is required")
	}
	return spec, nil
}

func unmarshalEntity(raw json.RawMessage) (Entity, error) {
	object, err := decodeRawObject(raw)
	if err != nil {
		return Entity{}, validationError(CodeInvalidEnvelope, "Evergreen.entity", err.Error())
	}
	var entity Entity
	for key, target := range map[string]any{
		"logical_id": &entity.LogicalID, "entity_type": &entity.EntityType,
		"schema": &entity.Schema, "semantic_revision": &entity.SemanticRevision,
	} {
		if err = decodeRequired(object, key, target); err != nil {
			return Entity{}, withPath(err, "Evergreen.entity")
		}
	}
	entity.Extra = unknownFields(object, "logical_id", "entity_type", "schema", "semantic_revision")
	return entity, nil
}

func unmarshalClaim(raw json.RawMessage) (Claim, error) {
	object, err := decodeRawObject(raw)
	if err != nil {
		return Claim{}, validationError(CodeInvalidClaim, "Evergreen.claim", err.Error())
	}
	var claim Claim
	for key, target := range map[string]any{
		"claim_kind": &claim.ClaimKind, "kind_schema": &claim.KindSchema,
		"status": &claim.Status, "tags": &claim.Tags, "kind_data": &claim.KindData,
	} {
		if err = decodeRequired(object, key, target); err != nil {
			return Claim{}, withPath(err, "Evergreen.claim")
		}
	}
	claim.Extra = unknownFields(object, "claim_kind", "kind_schema", "status", "tags", "kind_data")
	return claim, nil
}

func unmarshalProvenance(raw json.RawMessage) (Provenance, error) {
	object, err := decodeRawObject(raw)
	if err != nil {
		return Provenance{}, err
	}
	var provenance Provenance
	for key, target := range map[string]any{
		"source_id": &provenance.SourceID, "note_id": &provenance.NoteID,
		"segment_refs": &provenance.SegmentRefs, "reason": &provenance.Reason,
	} {
		if err = decodeRequired(object, key, target); err != nil {
			return Provenance{}, err
		}
	}
	provenance.Extra = unknownFields(object, "source_id", "note_id", "segment_refs", "reason")
	return provenance, nil
}

func marshalRelations(relations Relations) (json.RawMessage, error) {
	items := make([]json.RawMessage, 0, len(relations.Outgoing))
	for _, edge := range relations.Outgoing {
		target, err := marshalObject(edge.Target.Extra, map[string]any{
			"entity_type": edge.Target.EntityType,
			"logical_id":  edge.Target.LogicalID,
			"block_id":    edge.Target.BlockID,
		})
		if err != nil {
			return nil, err
		}
		context, err := marshalObject(edge.Context.Extra, map[string]any{
			"note_id":      edge.Context.NoteID,
			"segment_refs": nonNilLogicalIDs(edge.Context.SegmentRefs),
			"quote_hash":   edge.Context.QuoteHash,
			"data":         edge.Context.Data,
		})
		if err != nil {
			return nil, err
		}
		item, err := marshalObject(edge.Extra, map[string]any{
			"edge_id":    edge.ID,
			"schema":     edge.Schema,
			"target":     target,
			"type":       edge.Type,
			"reason":     edge.Reason,
			"context":    context,
			"extension":  edge.Extension,
			"created_at": edge.CreatedAt,
			"updated_at": edge.UpdatedAt,
		})
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return marshalObject(relations.Extra, map[string]any{"outgoing": items})
}

func unmarshalRelations(raw json.RawMessage) (Relations, error) {
	object, err := decodeRawObject(raw)
	if err != nil {
		return Relations{}, validationError(CodeInvalidEdge, "Evergreen.relations", err.Error())
	}
	var items []json.RawMessage
	if rawItems, ok := object["outgoing"]; ok {
		if err = json.Unmarshal(rawItems, &items); err != nil {
			return Relations{}, validationError(CodeInvalidEdge, "Evergreen.relations.outgoing", err.Error())
		}
	}
	relations := Relations{Outgoing: make([]TypedEdge, 0, len(items))}
	for index, item := range items {
		edge, edgeErr := unmarshalEdge(item)
		if edgeErr != nil {
			return Relations{}, withPath(edgeErr, fmt.Sprintf("Evergreen.relations.outgoing[%d]", index))
		}
		relations.Outgoing = append(relations.Outgoing, edge)
	}
	relations.Extra = unknownFields(object, "outgoing")
	return relations, nil
}

func unmarshalEdge(raw json.RawMessage) (TypedEdge, error) {
	object, err := decodeRawObject(raw)
	if err != nil {
		return TypedEdge{}, err
	}
	var edge TypedEdge
	for key, target := range map[string]any{
		"edge_id": &edge.ID, "schema": &edge.Schema, "type": &edge.Type,
		"reason": &edge.Reason, "created_at": &edge.CreatedAt, "updated_at": &edge.UpdatedAt,
	} {
		if rawValue, ok := object[key]; ok {
			if err = json.Unmarshal(rawValue, target); err != nil {
				return TypedEdge{}, err
			}
		}
	}
	if rawTarget, ok := object["target"]; ok {
		edge.Target, err = unmarshalEntityRef(rawTarget)
		if err != nil {
			return TypedEdge{}, err
		}
	}
	if rawContext, ok := object["context"]; ok {
		edge.Context, err = unmarshalEdgeContext(rawContext)
		if err != nil {
			return TypedEdge{}, err
		}
	}
	if rawExtension, ok := object["extension"]; ok {
		if err = json.Unmarshal(rawExtension, &edge.Extension); err != nil {
			return TypedEdge{}, err
		}
	}
	edge.Extra = unknownFields(object,
		"edge_id", "schema", "target", "type", "reason", "context", "extension", "created_at", "updated_at")
	return edge, nil
}

func unmarshalEntityRef(raw json.RawMessage) (EntityRef, error) {
	object, err := decodeRawObject(raw)
	if err != nil {
		return EntityRef{}, err
	}
	var ref EntityRef
	for key, target := range map[string]any{
		"entity_type": &ref.EntityType, "logical_id": &ref.LogicalID, "block_id": &ref.BlockID,
	} {
		if rawValue, ok := object[key]; ok {
			if err = json.Unmarshal(rawValue, target); err != nil {
				return EntityRef{}, err
			}
		}
	}
	ref.Extra = unknownFields(object, "entity_type", "logical_id", "block_id")
	return ref, nil
}

func unmarshalEdgeContext(raw json.RawMessage) (EdgeContext, error) {
	object, err := decodeRawObject(raw)
	if err != nil {
		return EdgeContext{}, err
	}
	var context EdgeContext
	for key, target := range map[string]any{
		"note_id": &context.NoteID, "segment_refs": &context.SegmentRefs,
		"quote_hash": &context.QuoteHash, "data": &context.Data,
	} {
		if rawValue, ok := object[key]; ok {
			if err = json.Unmarshal(rawValue, target); err != nil {
				return EdgeContext{}, err
			}
		}
	}
	context.Extra = unknownFields(object, "note_id", "segment_refs", "quote_hash", "data")
	return context, nil
}

func marshalNoteReview(review NoteReview) (json.RawMessage, error) {
	items := make([]json.RawMessage, 0, len(review.Coverage))
	for _, module := range review.Coverage {
		item, err := marshalObject(module.Extra, map[string]any{
			"module_id":     module.ModuleID,
			"disposition":   module.Disposition,
			"segment_refs":  nonNilLogicalIDs(module.SegmentRefs),
			"candidate_id":  module.CandidateID,
			"candidate_ids": nonNilLogicalIDs(module.CandidateIDs),
			"reason":        module.Reason,
		})
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	lineage := make([]json.RawMessage, 0, len(review.Lineage))
	for _, event := range review.Lineage {
		item, err := marshalObject(event.Extra, map[string]any{
			"event_id":     event.EventID,
			"mutation":     event.Mutation,
			"previous_ids": nonNilLogicalIDs(event.PreviousIDs),
			"next_ids":     nonNilLogicalIDs(event.NextIDs),
		})
		if err != nil {
			return nil, err
		}
		lineage = append(lineage, item)
	}
	return marshalObject(review.Extra, map[string]any{
		"spec": review.Spec, "coverage": items, "lineage": lineage,
	})
}

func unmarshalNoteReview(raw json.RawMessage) (NoteReview, error) {
	object, err := decodeRawObject(raw)
	if err != nil {
		return NoteReview{}, err
	}
	var review NoteReview
	if err = decodeRequired(object, "spec", &review.Spec); err != nil {
		return NoteReview{}, err
	}
	var items []json.RawMessage
	if rawItems, ok := object["coverage"]; ok {
		if err = json.Unmarshal(rawItems, &items); err != nil {
			return NoteReview{}, err
		}
	}
	for _, item := range items {
		moduleObject, decodeErr := decodeRawObject(item)
		if decodeErr != nil {
			return NoteReview{}, decodeErr
		}
		var module CoverageModule
		for key, target := range map[string]any{
			"module_id": &module.ModuleID, "disposition": &module.Disposition,
			"segment_refs": &module.SegmentRefs, "candidate_id": &module.CandidateID,
			"candidate_ids": &module.CandidateIDs, "reason": &module.Reason,
		} {
			if value, ok := moduleObject[key]; ok {
				if decodeErr = json.Unmarshal(value, target); decodeErr != nil {
					return NoteReview{}, decodeErr
				}
			}
		}
		module.Extra = unknownFields(moduleObject,
			"module_id", "disposition", "segment_refs", "candidate_id", "candidate_ids", "reason")
		review.Coverage = append(review.Coverage, module)
	}
	if rawItems, ok := object["lineage"]; ok {
		items = nil
		if err = json.Unmarshal(rawItems, &items); err != nil {
			return NoteReview{}, err
		}
		for _, item := range items {
			eventObject, decodeErr := decodeRawObject(item)
			if decodeErr != nil {
				return NoteReview{}, decodeErr
			}
			var event SegmentLineage
			for key, target := range map[string]any{
				"event_id": &event.EventID, "mutation": &event.Mutation,
				"previous_ids": &event.PreviousIDs, "next_ids": &event.NextIDs,
			} {
				if value, ok := eventObject[key]; ok {
					if decodeErr = json.Unmarshal(value, target); decodeErr != nil {
						return NoteReview{}, decodeErr
					}
				}
			}
			event.Extra = unknownFields(eventObject,
				"event_id", "mutation", "previous_ids", "next_ids")
			review.Lineage = append(review.Lineage, event)
		}
	}
	review.Extra = unknownFields(object, "spec", "coverage", "lineage")
	return review, nil
}

func unmarshalSegment(raw json.RawMessage) (SegmentMetadata, error) {
	object, err := decodeRawObject(raw)
	if err != nil {
		return SegmentMetadata{}, err
	}
	var segment SegmentMetadata
	for key, target := range map[string]any{
		"segment_id": &segment.SegmentID, "normalized_hash": &segment.NormalizedHash,
	} {
		if err = decodeRequired(object, key, target); err != nil {
			return SegmentMetadata{}, err
		}
	}
	if sourceRaw, ok := object["source_ref"]; ok {
		sourceObject, sourceErr := decodeRawObject(sourceRaw)
		if sourceErr != nil {
			return SegmentMetadata{}, sourceErr
		}
		if err = decodeRequired(sourceObject, "source_id", &segment.SourceRef.SourceID); err != nil {
			return SegmentMetadata{}, err
		}
		locatorRaw, locatorErr := requiredRaw(sourceObject, "locator")
		if locatorErr != nil {
			return SegmentMetadata{}, locatorErr
		}
		locatorObject, locatorErr := decodeRawObject(locatorRaw)
		if locatorErr != nil {
			return SegmentMetadata{}, locatorErr
		}
		if err = decodeRequired(locatorObject, "kind", &segment.SourceRef.Locator.Kind); err != nil {
			return SegmentMetadata{}, err
		}
		if err = decodeRequired(locatorObject, "value", &segment.SourceRef.Locator.Value); err != nil {
			return SegmentMetadata{}, err
		}
		segment.SourceRef.Locator.Extra = unknownFields(locatorObject, "kind", "value")
		segment.SourceRef.Extra = unknownFields(sourceObject, "source_id", "locator")
	}
	if annotationRaw, ok := object["annotation"]; ok {
		annotationObject, annotationErr := decodeRawObject(annotationRaw)
		if annotationErr != nil {
			return SegmentMetadata{}, annotationErr
		}
		annotation := &Annotation{}
		if err = decodeRequired(annotationObject, "kind", &annotation.Kind); err != nil {
			return SegmentMetadata{}, err
		}
		if rawLabel, exists := annotationObject["label"]; exists {
			if err = json.Unmarshal(rawLabel, &annotation.Label); err != nil {
				return SegmentMetadata{}, err
			}
		}
		annotation.Extra = unknownFields(annotationObject, "kind", "label")
		segment.Annotation = annotation
	}
	segment.Extra = unknownFields(object,
		"segment_id", "source_ref", "annotation", "normalized_hash")
	return segment, nil
}

func unmarshalCandidate(raw json.RawMessage) (CandidateMetadata, error) {
	object, err := decodeRawObject(raw)
	if err != nil {
		return CandidateMetadata{}, err
	}
	var candidate CandidateMetadata
	for key, target := range map[string]any{
		"candidate_id": &candidate.CandidateID, "claim_kind": &candidate.ClaimKind,
		"kind_schema": &candidate.KindSchema, "title": &candidate.Title,
		"logical_slug": &candidate.LogicalSlug, "segment_refs": &candidate.SegmentRefs,
		"payload_hash": &candidate.PayloadHash, "ref_hashes": &candidate.RefHashes,
		"relation": &candidate.Relation, "reason": &candidate.Reason, "tags": &candidate.Tags,
		"state": &candidate.State, "materialized_claim_id": &candidate.MaterializedClaimID,
	} {
		if rawValue, ok := object[key]; ok {
			if err = json.Unmarshal(rawValue, target); err != nil {
				return CandidateMetadata{}, err
			}
		}
	}
	candidate.Extra = unknownFields(object,
		"candidate_id", "claim_kind", "kind_schema", "title", "logical_slug",
		"segment_refs", "payload_hash", "ref_hashes", "relation", "reason", "tags",
		"state", "materialized_claim_id")
	return candidate, nil
}

func marshalObject(extra RawObject, fields map[string]any) (json.RawMessage, error) {
	object := cloneRawObject(extra)
	for key, value := range fields {
		if isEmptyOptional(value) {
			continue
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		object[key] = raw
	}
	return json.Marshal(object)
}

func mustMarshalObject(extra RawObject, fields map[string]any) json.RawMessage {
	raw, err := marshalObject(extra, fields)
	if err != nil {
		panic(err)
	}
	return raw
}

func isEmptyOptional(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return typed == ""
	case LogicalID:
		return typed == ""
	case RawObject:
		return len(typed) == 0
	}
	return false
}

func decodeRawObject(data []byte) (RawObject, error) {
	var object RawObject
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, err
	}
	if object == nil {
		return nil, errorsNewObject()
	}
	return object, nil
}

func errorsNewObject() error {
	return fmt.Errorf("expected JSON object")
}

func requiredRaw(object RawObject, key string) (json.RawMessage, error) {
	raw, ok := object[key]
	if !ok {
		return nil, validationError(CodeInvalidEnvelope, key, "required field is missing")
	}
	return raw, nil
}

func decodeRequired(object RawObject, key string, target any) error {
	raw, ok := object[key]
	if !ok {
		return validationError(CodeInvalidEnvelope, key, "required field is missing")
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return validationError(CodeInvalidEnvelope, key, err.Error())
	}
	return nil
}

func unknownFields(object RawObject, known ...string) RawObject {
	extra := cloneRawObject(object)
	for _, key := range known {
		delete(extra, key)
	}
	if len(extra) == 0 {
		return nil
	}
	return extra
}

func cloneRawObject(source RawObject) RawObject {
	cloned := RawObject{}
	for key, value := range source {
		cloned[key] = append(json.RawMessage(nil), value...)
	}
	return cloned
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func nonNilLogicalIDs(values []LogicalID) []LogicalID {
	if values == nil {
		return []LogicalID{}
	}
	return values
}

func nonNilRaw(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return json.RawMessage(`{}`)
	}
	return value
}

func nonNilRefHashes(values map[LogicalID]string) map[LogicalID]string {
	if values == nil {
		return map[LogicalID]string{}
	}
	return values
}

func errorsAsDiagnostic(err error, target **DiagnosticError) bool {
	typed, ok := err.(*DiagnosticError)
	if ok {
		*target = typed
	}
	return ok
}

func withPath(err error, prefix string) error {
	var diagnostics *DiagnosticError
	if !errorsAsDiagnostic(err, &diagnostics) {
		return err
	}
	cloned := make([]Diagnostic, len(diagnostics.Diagnostics))
	copy(cloned, diagnostics.Diagnostics)
	for index := range cloned {
		if cloned[index].Path == "" {
			cloned[index].Path = prefix
		} else if !strings.HasPrefix(cloned[index].Path, prefix) {
			cloned[index].Path = prefix + "." + cloned[index].Path
		}
	}
	return &DiagnosticError{Diagnostics: cloned}
}
