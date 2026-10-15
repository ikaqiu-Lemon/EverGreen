package apicontract

import (
	"encoding/json"
	"reflect"
	"strings"
)

type EvergreenClaimEntity struct {
	LogicalID        string `json:"logical_id"`
	EntityType       string `json:"entity_type"`
	Schema           string `json:"schema"`
	SemanticRevision uint64 `json:"semantic_revision"`
}

type EvergreenClaimMetadata struct {
	ClaimKind  string               `json:"claim_kind"`
	KindSchema string               `json:"kind_schema"`
	Status     string               `json:"status"`
	Tags       []string             `json:"tags" api:"nonnullable"`
	KindData   map[string]JSONValue `json:"kind_data"`
}

type EvergreenClaimRelations struct {
	Outgoing []EvergreenTypedEdge `json:"outgoing" api:"nonnullable"`
}

type EvergreenClaimDocumentEnvelopeFields struct {
	Spec       string                  `json:"spec"`
	Entity     EvergreenClaimEntity    `json:"entity"`
	Claim      EvergreenClaimMetadata  `json:"claim"`
	Provenance []EvergreenAVProvenance `json:"provenance,omitempty" api:"optional,nonnullable"`
	Relations  EvergreenClaimRelations `json:"relations"`
	Extension  map[string]JSONValue    `json:"extension,omitempty" api:"optional"`
}

// 只读 envelope 保留所有层级的同 major 未知字段，编辑入口仍使用独立的结构化命令。
type EvergreenClaimDocumentEnvelope struct{ raw json.RawMessage }

func (v EvergreenClaimDocumentEnvelope) MarshalJSON() ([]byte, error) {
	if len(v.raw) == 0 {
		return []byte("null"), nil
	}
	return v.raw, nil
}

func (v *EvergreenClaimDocumentEnvelope) UnmarshalJSON(data []byte) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return err
	}
	v.raw = append(v.raw[:0], data...)
	return nil
}

func evergreenClaimEnvelopeSchema(b *schemaBuilder, input bool) (*Schema, error) {
	base, err := b.schema(reflect.TypeFor[EvergreenClaimDocumentEnvelopeFields](), input)
	if err != nil {
		return nil, err
	}
	additional, err := b.schema(reflect.TypeFor[JSONValue](), input)
	if err != nil {
		return nil, err
	}
	visited := map[string]bool{}
	var extend func(*Schema) *Schema
	extend = func(source *Schema) *Schema {
		if source == nil {
			return nil
		}
		copySchema := *source
		schema := &copySchema
		if strings.HasPrefix(schema.Ref, "#/$defs/Evergreen") ||
			schema.Ref == "#/$defs/AVEntityRef" || schema.Ref == "#/$defs/AVEdgeContext" {
			name := strings.TrimPrefix(schema.Ref, "#/$defs/")
			extendedName := "EvergreenClaimRead" + name
			if !visited[name] {
				visited[name] = true
				b.definitions[extendedName] = extend(b.definitions[name])
			}
			schema.Ref = "#/$defs/" + extendedName
			return schema
		}
		if schema.Type == "object" && len(schema.Properties) > 0 {
			schema.AdditionalProperties = additional
		}
		if schema.Properties != nil {
			schema.Properties = make(map[string]*Schema, len(source.Properties))
			for key, value := range source.Properties {
				schema.Properties[key] = extend(value)
			}
		}
		if schema.AnyOf != nil {
			schema.AnyOf = make([]*Schema, len(source.AnyOf))
			for index, value := range source.AnyOf {
				schema.AnyOf[index] = extend(value)
			}
		}
		schema.Items = extend(source.Items)
		return schema
	}
	return extend(base), nil
}

// 正文是 SiYuan 原生 AST；kind_data 保留 descriptor 未声明的扩展内容。
type EvergreenClaimSnapshot struct {
	LogicalID   string                         `json:"logical_id"`
	DocumentID  string                         `json:"document_id"`
	Base        string                         `json:"base"`
	Title       string                         `json:"title"`
	Envelope    EvergreenClaimDocumentEnvelope `json:"envelope"`
	Body        []JSONValue                    `json:"body" api:"nonnullable"`
	ReadOnly    bool                           `json:"read_only"`
	Diagnostics []EvergreenDiagnostic          `json:"diagnostics,omitempty" api:"optional,nonnullable"`
}

type EvergreenClaimGetRequest struct {
	// 逻辑身份或物理文档身份二选一，返回同一权威 Claim。
	LogicalID  string `json:"logical_id,omitempty" api:"optional"`
	DocumentID string `json:"document_id,omitempty" api:"optional"`
}

type EvergreenClaimEnvelope struct {
	OK          bool                    `json:"ok"`
	Data        *EvergreenClaimSnapshot `json:"data"`
	Diagnostics []EvergreenDiagnostic   `json:"diagnostics,omitempty" api:"optional,nonnullable"`
}

// 只对给出的字段进行编辑；kind_data 使用合并语义，避免丢弃未知字段。
type EvergreenClaimEdit struct {
	Status     *string                  `json:"status,omitempty" api:"optional"`
	Tags       *[]string                `json:"tags,omitempty" api:"optional,nonnullable"`
	KindData   map[string]JSONValue     `json:"kind_data,omitempty" api:"optional"`
	Provenance *[]EvergreenAVProvenance `json:"provenance,omitempty" api:"optional,nonnullable"`
}

type EvergreenClaimEditRequest struct {
	OperationID string             `json:"operation_id"`
	LogicalID   string             `json:"logical_id"`
	Base        string             `json:"base"`
	Edit        EvergreenClaimEdit `json:"edit"`
}

// 创建必须使用 descriptor 初始状态；Opinion 初始 validation 必须为 pending。
type EvergreenClaimCreateRequest struct {
	OperationID string                  `json:"operation_id"`
	LogicalID   string                  `json:"logical_id"`
	DocumentID  string                  `json:"document_id"`
	Path        string                  `json:"path"`
	Title       string                  `json:"title"`
	Body        []JSONValue             `json:"body" api:"nonnullable"`
	Kind        string                  `json:"kind"`
	Tags        []string                `json:"tags" api:"nonnullable"`
	KindData    map[string]JSONValue    `json:"kind_data"`
	Provenance  []EvergreenAVProvenance `json:"provenance" api:"nonnullable"`
}

type EvergreenBulkCapabilitiesRequest struct {
	Rows []EvergreenAVRowRef `json:"rows" api:"nonnullable"`
}

type EvergreenBulkCapabilities struct {
	Fields   []string `json:"fields" api:"nonnullable"`
	Statuses []string `json:"statuses" api:"nonnullable"`
}

type EvergreenBulkCapabilitiesEnvelope struct {
	OK          bool                       `json:"ok"`
	Data        *EvergreenBulkCapabilities `json:"data"`
	Diagnostics []EvergreenDiagnostic      `json:"diagnostics,omitempty" api:"optional,nonnullable"`
}

// 混合 kind 批量修改只接受能力和状态交集；失败不产生部分写入。
type EvergreenBulkClaimRequest struct {
	OperationID string              `json:"operation_id"`
	Rows        []EvergreenAVRowRef `json:"rows" api:"nonnullable"`
	Edit        EvergreenClaimEdit  `json:"edit"`
}
