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
	if before.Entity.LogicalID != after.Entity.LogicalID || before.Entity.EntityType != after.Entity.EntityType ||
		before.Claim.ClaimKind != after.Claim.ClaimKind || before.Claim.KindSchema != after.Claim.KindSchema {
		return validationError(CodeUnauthorized, "claim.claim_kind", "Claim identity and kind require an explicit migration")
	}
	changed := []struct {
		field string
		left  any
		right any
	}{
		{"claim.status", before.Claim.Status, after.Claim.Status},
		{"claim.tags", before.Claim.Tags, after.Claim.Tags},
		{"claim.kind_data", before.Claim.KindData, after.Claim.KindData},
		{"claim.provenance", before.Provenance, after.Provenance},
		{"claim.relations", before.Relations, after.Relations},
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

func semanticValuesEqual(left, right any) bool {
	leftRaw, _ := json.Marshal(left)
	rightRaw, _ := json.Marshal(right)
	var leftValue, rightValue any
	if json.Unmarshal(leftRaw, &leftValue) != nil || json.Unmarshal(rightRaw, &rightValue) != nil {
		return bytes.Equal(leftRaw, rightRaw)
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
