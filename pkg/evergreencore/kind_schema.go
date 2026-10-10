package evergreencore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// JSONSchema is the core's supported, declarative field-schema subset. Unknown
// payload fields remain intact; only declared fields are exposed to editors.
type JSONSchema struct {
	Type       string                `json:"type"`
	Title      string                `json:"title,omitempty"`
	Properties map[string]JSONSchema `json:"properties,omitempty"`
	Required   []string              `json:"required,omitempty"`
	Enum       []string              `json:"enum,omitempty"`
	MinLength  int                   `json:"minLength,omitempty"`
	Items      *JSONSchema           `json:"items,omitempty"`
}

type KindViewHints struct {
	Name    string   `json:"name"`
	Icon    string   `json:"icon,omitempty"`
	ViewID  string   `json:"view_id,omitempty"`
	Columns []string `json:"columns,omitempty"`
}

type CapabilityMatrix map[string][]PrincipalType

type MaterializationInput struct {
	Title      string
	KindData   json.RawMessage
	Provenance []Provenance
	Existing   *Claim
}

type MaterializationResult struct {
	Status   string
	KindData json.RawMessage
}

type KindMaterializer func(MaterializationInput) (MaterializationResult, error)

func cloneKindDescriptor(source KindDescriptor) KindDescriptor {
	cloned := source
	cloned.AllowedStatuses = append([]string(nil), source.AllowedStatuses...)
	cloned.AllowedEdgeSchemas = append([]SchemaRef(nil), source.AllowedEdgeSchemas...)
	cloned.DefaultKindData = append(json.RawMessage(nil), source.DefaultKindData...)
	cloned.ViewHints.Columns = append([]string(nil), source.ViewHints.Columns...)
	raw, _ := json.Marshal(source.KindDataSchema)
	cloned.KindDataSchema = JSONSchema{}
	_ = json.Unmarshal(raw, &cloned.KindDataSchema)
	if source.RequiredCapabilities != nil {
		cloned.RequiredCapabilities = CapabilityMatrix{}
		for field, principals := range source.RequiredCapabilities {
			cloned.RequiredCapabilities[field] = append([]PrincipalType(nil), principals...)
		}
	}
	return cloned
}

func fieldAllowed(descriptor KindDescriptor, principal PrincipalType, field string) bool {
	permissions, exists := descriptor.RequiredCapabilities[field]
	if !exists {
		// Existing descriptors without an explicit matrix remain user editable.
		return principal == PrincipalUser
	}
	for _, allowed := range permissions {
		if allowed == principal {
			return true
		}
	}
	return false
}

func validateSchemaValue(schema JSONSchema, raw json.RawMessage, path string) error {
	if schema.Type == "" {
		return nil
	}
	fail := func(message string) error { return validationError(CodeInvalidClaim, path, message) }
	switch schema.Type {
	case "object":
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil || object == nil {
			return fail("expected an object")
		}
		for _, key := range schema.Required {
			if _, ok := object[key]; !ok {
				return validationError(CodeInvalidClaim, path+"."+key, "required field is missing")
			}
		}
		keys := make([]string, 0, len(schema.Properties))
		for key := range schema.Properties {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if value, ok := object[key]; ok {
				if err := validateSchemaValue(schema.Properties[key], value, path+"."+key); err != nil {
					return err
				}
			}
		}
	case "string":
		var value string
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &value) != nil {
			return fail("expected a string")
		}
		if len([]rune(strings.TrimSpace(value))) < schema.MinLength {
			return fail(fmt.Sprintf("at least %d nonblank characters are required", schema.MinLength))
		}
		if len(schema.Enum) > 0 && !containsString(schema.Enum, value) {
			return fail(fmt.Sprintf("unsupported value %q", value))
		}
	case "array":
		var values []json.RawMessage
		if json.Unmarshal(raw, &values) != nil || values == nil {
			return fail("expected an array")
		}
		if schema.Items != nil {
			for index, value := range values {
				if err := validateSchemaValue(*schema.Items, value, fmt.Sprintf("%s[%d]", path, index)); err != nil {
					return err
				}
			}
		}
	case "boolean":
		var value bool
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &value) != nil {
			return fail("expected a boolean")
		}
	case "number":
		var value float64
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &value) != nil {
			return fail("expected a number")
		}
	default:
		return fail(fmt.Sprintf("unsupported schema type %q", schema.Type))
	}
	return nil
}

func validateSchemaDefinition(schema JSONSchema) error {
	if schema.Type == "" {
		return nil
	}
	if !containsString([]string{"object", "string", "array", "boolean", "number"}, schema.Type) {
		return fmt.Errorf("unsupported schema type %q", schema.Type)
	}
	for _, key := range schema.Required {
		if _, exists := schema.Properties[key]; !exists {
			return fmt.Errorf("required property %q is not declared", key)
		}
	}
	for _, child := range schema.Properties {
		if err := validateSchemaDefinition(child); err != nil {
			return err
		}
	}
	if schema.Items != nil {
		return validateSchemaDefinition(*schema.Items)
	}
	return nil
}

func builtinKindDescriptor(kind ClaimKind) KindDescriptor {
	descriptor := KindDescriptor{
		Kind: kind, AllowedStatuses: []string{"active", "deprecated", "superseded", "archived"},
		AllowedEdgeSchemas: []SchemaRef{ArgumentSchema, MaterialSchema, ReplacementSchema},
		KindDataSchema:     JSONSchema{Type: "object"}, DefaultStatus: "active",
		DefaultKindData: json.RawMessage(`{}`), MaterializerRef: "evergreen.review-copy/v1",
		RequiredCapabilities: CapabilityMatrix{
			"claim.status": {PrincipalUser}, "claim.tags": {PrincipalUser, PrincipalAgent},
			"claim.kind_data": {PrincipalUser, PrincipalAgent}, "claim.provenance": {PrincipalUser},
			"claim.relations": {PrincipalUser, PrincipalAgent},
		},
		RequireProvenance: true,
	}
	switch kind {
	case KnowledgeKind:
		descriptor.Schema = KnowledgeKindSchema
		descriptor.ViewHints = KindViewHints{Name: "Knowledge", Icon: "iconFile", ViewID: KnowledgeViewID}
		descriptor.ValidateKindData = validateJSONObject
	case OpinionKind:
		descriptor.Schema = OpinionKindSchema
		descriptor.ViewHints = KindViewHints{
			Name: "Opinion", Icon: "iconInfo", ViewID: OpinionViewID, Columns: []string{"validation"},
		}
		descriptor.DefaultKindData = json.RawMessage(`{"validation":{"status":"pending"}}`)
		descriptor.KindDataSchema = JSONSchema{
			Type: "object", Required: []string{"validation"},
			Properties: map[string]JSONSchema{
				"validation": {
					Type: "object", Title: "Validation", Required: []string{"status"},
					Properties: map[string]JSONSchema{
						"status":   {Type: "string", Title: "Status", Enum: []string{"pending", "validated", "rejected"}},
						"method":   {Type: "string", Title: "Method"},
						"evidence": {Type: "array", Title: "Evidence"},
					},
				},
			},
		}
		descriptor.RequiredCapabilities["claim.kind_data.validation"] = []PrincipalType{PrincipalUser}
		descriptor.ValidateKindData = validateOpinionData
	}
	descriptor.Materialize = func(input MaterializationInput) (MaterializationResult, error) {
		if len(input.Provenance) == 0 {
			return MaterializationResult{}, validationError(CodeInvalidClaim, "claim.provenance", "materialization requires provenance")
		}
		if input.Existing != nil {
			return MaterializationResult{Status: input.Existing.Status, KindData: input.Existing.KindData}, nil
		}
		return MaterializationResult{Status: descriptor.DefaultStatus, KindData: descriptor.DefaultKindData}, nil
	}
	return descriptor
}

func (r *Registry) Unregister(kind ClaimKind) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byKind, kind)
}

// ViewDefinition only adds presentation fields from a registered descriptor.
// The provider continues to read the same canonical documents for every view.
func (r *Registry) ViewDefinition(viewID string) (AVViewDefinition, error) {
	definition, err := claimsViewDefinition(viewID)
	if err == nil {
		return definition, nil
	}
	if r == nil {
		return AVViewDefinition{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, descriptor := range r.byKind {
		if descriptor.ViewHints.ViewID != viewID || viewID == "" {
			continue
		}
		definition, _ = claimsViewDefinition(ClaimsViewID)
		definition.ID = viewID
		definition.Name = descriptor.ViewHints.Name
		definition.Provider.Query.ClaimKind = descriptor.Kind
		columns := make([]AVColumnDefinition, 0, len(definition.Columns))
		for _, column := range definition.Columns {
			if column.ID != "validation" {
				columns = append(columns, column)
			}
		}
		for _, field := range descriptor.ViewHints.Columns {
			schema, ok := descriptor.KindDataSchema.Properties[field]
			if !ok {
				continue
			}
			columns = append(columns, AVColumnDefinition{
				ID: field, Name: schema.Title, Value: "json", Editable: true,
				Binding: AVColumnBinding{Field: "claim.kind_data." + field},
			})
		}
		definition.Columns = columns
		return definition, nil
	}
	return AVViewDefinition{}, validationError(CodeClaimKindUnavailable, "view_id", "view descriptor is unavailable")
}

func (r *Registry) ViewID(kind ClaimKind) string {
	if kind == "" {
		return ClaimsViewID
	}
	if descriptor, ok := r.Descriptor(kind); ok && descriptor.ViewHints.ViewID != "" {
		return descriptor.ViewHints.ViewID
	}
	return ClaimsViewID
}
