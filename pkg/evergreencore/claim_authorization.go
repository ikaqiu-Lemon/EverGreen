package evergreencore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// AuthorizeClaimChange applies field permissions to both raw after-images and
// schema-driven edits, so a client cannot bypass them by changing transports.
func AuthorizeClaimChange(principal Principal, before, after *DocumentEnvelope, registry *Registry) error {
	if after == nil || after.Claim == nil {
		if before != nil && before.Claim != nil {
			return validationError(CodeUnauthorized, "claim", "a Claim cannot lose its canonical metadata")
		}
		return nil
	}
	if registry == nil {
		registry = DefaultRegistry()
	}
	descriptor, ok := registry.Descriptor(after.Claim.ClaimKind)
	if !ok {
		return validationError(CodeClaimKindUnavailable, "claim.claim_kind", "kind descriptor is unavailable")
	}
	if before == nil || before.Claim == nil {
		if descriptor.RequireProvenance && len(after.Provenance) == 0 {
			return validationError(CodeInvalidClaim, "claim.provenance", "Claim creation requires provenance")
		}
		if descriptor.DefaultStatus != "" && after.Claim.Status != descriptor.DefaultStatus {
			return validationError(CodeInvalidClaim, "claim.status", "new Claim must use its initial status")
		}
		if validation, present := descriptor.KindDataSchema.Properties["validation"]; present {
			if status, exists := validation.Properties["status"]; exists && len(status.Enum) > 0 {
				_, actual := claimValidation(after.Claim.KindData)
				if actual != status.Enum[0] {
					return validationError(CodeInvalidClaim, "claim.kind_data.validation.status", "new Claim must start pending")
				}
			}
		}
		return nil
	}
	if before.Spec != after.Spec || before.Entity.LogicalID != after.Entity.LogicalID ||
		before.Entity.EntityType != after.Entity.EntityType || before.Entity.Schema != after.Entity.Schema ||
		before.Claim.ClaimKind != after.Claim.ClaimKind || before.Claim.KindSchema != after.Claim.KindSchema {
		return validationError(CodeUnauthorized, "claim.claim_kind", "Claim identity and kind require an explicit migration")
	}
	// Unknown metadata has no editable contract. Ordinary Claim commands must
	// retain it; registered migrations can define changes to these partitions.
	for _, protected := range []struct {
		field string
		left  RawObject
		right RawObject
	}{
		{"entity", before.Entity.Extra, after.Entity.Extra},
		{"claim", before.Claim.Extra, after.Claim.Extra},
		{"Evergreen", before.Extra, after.Extra},
		{"extension", before.Extension, after.Extension},
	} {
		if !semanticValuesEqual(protected.left, protected.right) {
			return validationError(CodeUnauthorized, protected.field, "unknown metadata must be preserved")
		}
	}
	// The domain structs omit nested Extra from ordinary json.Marshal. Compare
	// the lossless envelope representation, including provenance and every edge
	// partition, rather than serializing those structs independently.
	left, err := claimAuthorizationFields(before)
	if err != nil {
		return err
	}
	right, err := claimAuthorizationFields(after)
	if err != nil {
		return err
	}
	var leftClaim, rightClaim RawObject
	if err = json.Unmarshal(left["claim"], &leftClaim); err != nil {
		return err
	}
	if err = json.Unmarshal(right["claim"], &rightClaim); err != nil {
		return err
	}
	changed := []struct {
		field string
		left  any
		right any
	}{
		{"claim.status", leftClaim["status"], rightClaim["status"]},
		{"claim.tags", leftClaim["tags"], rightClaim["tags"]},
		{"claim.kind_data", leftClaim["kind_data"], rightClaim["kind_data"]},
		{"claim.provenance", left["provenance"], right["provenance"]},
		{"claim.relations", left["relations"], right["relations"]},
	}
	for _, change := range changed {
		if !semanticValuesEqual(change.left, change.right) && !fieldAllowed(descriptor, principal.Type, change.field) {
			return validationError(CodeUnauthorized, change.field, fmt.Sprintf("field requires an authorized user for kind %q", descriptor.Kind))
		}
	}
	fields := make([]string, 0, len(descriptor.RequiredCapabilities))
	for field := range descriptor.RequiredCapabilities {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	for _, field := range fields {
		const prefix = "claim.kind_data."
		if len(field) <= len(prefix) || field[:len(prefix)] != prefix {
			continue
		}
		left := kindDataField(before.Claim.KindData, field[len(prefix):])
		right := kindDataField(after.Claim.KindData, field[len(prefix):])
		if !semanticValuesEqual(left, right) && !fieldAllowed(descriptor, principal.Type, field) {
			return validationError(CodeUnauthorized, field, "kind-specific field is user-only")
		}
	}
	oldValidation, newValidation := validationStatus(before.Claim.KindData), validationStatus(after.Claim.KindData)
	if _, exists := descriptor.KindDataSchema.Properties["validation"]; exists &&
		oldValidation == "rejected" && newValidation == "validated" {
		return validationError(CodeInvalidClaim, "claim.kind_data.validation.status", "rejected validation must be reopened before validating")
	}
	return nil
}

func claimAuthorizationFields(envelope *DocumentEnvelope) (RawObject, error) {
	raw, err := MarshalDocumentEnvelope(envelope)
	if err != nil {
		return nil, err
	}
	return decodeRawObject(raw)
}

func semanticValuesEqual(left, right any) bool {
	leftRaw, err := json.Marshal(left)
	if err != nil {
		return false
	}
	rightRaw, err := json.Marshal(right)
	if err != nil {
		return false
	}
	leftValue, err := decodeJSONValue(leftRaw)
	if err != nil {
		return false
	}
	rightValue, err := decodeJSONValue(rightRaw)
	if err != nil {
		return false
	}
	leftRaw, _ = json.Marshal(leftValue)
	rightRaw, _ = json.Marshal(rightValue)
	return bytes.Equal(leftRaw, rightRaw)
}

func validationStatus(raw json.RawMessage) string {
	_, status := claimValidation(raw)
	return status
}

func kindDataField(raw json.RawMessage, field string) json.RawMessage {
	for _, part := range strings.Split(field, ".") {
		var object map[string]json.RawMessage
		if json.Unmarshal(raw, &object) != nil {
			return nil
		}
		raw = object[part]
	}
	return raw
}
