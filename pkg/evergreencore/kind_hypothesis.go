package evergreencore

import (
	"encoding/json"
)

const HypothesisKind ClaimKind = "hypothesis"
const HypothesisKindSchema SchemaRef = "evergreen.claim-kind.hypothesis/v1"

// HypothesisDescriptor is an optional core registration. Neither the Claim
// codec nor the provider contains a hypothesis-specific storage branch.
func HypothesisDescriptor() KindDescriptor {
	descriptor := KindDescriptor{
		Kind: HypothesisKind, Schema: HypothesisKindSchema,
		AllowedStatuses: []string{"proposed", "testing", "confirmed", "refuted", "archived"},
		DefaultStatus:   "proposed", MaterializerRef: "evergreen.hypothesis-materialize/v1",
		AllowedEdgeSchemas: []SchemaRef{ArgumentSchema, MaterialSchema, ReplacementSchema},
		KindDataSchema: JSONSchema{
			Type: "object", Required: []string{"statement", "test_method"},
			Properties: map[string]JSONSchema{
				"statement":   {Type: "string", Title: "Statement", MinLength: 1},
				"test_method": {Type: "string", Title: "Test method", MinLength: 1},
				"result":      {Type: "string", Title: "Result"},
			},
		},
		RequiredCapabilities: CapabilityMatrix{
			"claim.status": {PrincipalUser}, "claim.tags": {PrincipalUser, PrincipalAgent},
			"claim.kind_data": {PrincipalUser, PrincipalAgent}, "claim.provenance": {PrincipalUser},
			"claim.relations": {PrincipalUser, PrincipalAgent},
		},
		ViewHints: KindViewHints{
			Name: "Hypotheses", Icon: "iconInfo", ViewID: "hypothesis",
			Columns: []string{"statement", "test_method", "result"},
		},
		RequireProvenance: true,
	}
	descriptor.Materialize = func(input MaterializationInput) (MaterializationResult, error) {
		if len(input.Provenance) == 0 {
			return MaterializationResult{}, validationError(CodeInvalidClaim, "claim.provenance", "hypothesis requires source evidence")
		}
		data := input.KindData
		status := descriptor.DefaultStatus
		if input.Existing != nil {
			if len(data) == 0 {
				data = input.Existing.KindData
			}
			status = input.Existing.Status
		}
		if err := validateSchemaValue(descriptor.KindDataSchema, data, "claim.kind_data"); err != nil {
			return MaterializationResult{}, err
		}
		return MaterializationResult{Status: status, KindData: append(json.RawMessage(nil), data...)}, nil
	}
	return descriptor
}

// ApplicationRegistry includes the optional example kind used by the SiYuan
// host; callers can use DefaultRegistry when the extension is unavailable.
func ApplicationRegistry() *Registry {
	registry := DefaultRegistry()
	if err := registry.Register(HypothesisDescriptor()); err != nil {
		panic(err)
	}
	return registry
}
