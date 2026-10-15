package apicontract

// Canonical export 是归档与客户端只读传输，文档字段包含原生 AST 和 lossless envelope。
// 它不作为运行时存储，不允许通过 archive 绕过 plan/apply 回写。
type EvergreenCanonicalEntity struct {
	LogicalID string               `json:"logical_id"`
	Type      string               `json:"entity_type"`
	Hash      string               `json:"semantic_hash"`
	Document  map[string]JSONValue `json:"document"`
}

type EvergreenCanonicalArchive struct {
	Spec     string                     `json:"spec"`
	Entities []EvergreenCanonicalEntity `json:"entities" api:"nonnullable"`
}

type EvergreenExportEnvelope struct {
	OK          bool                       `json:"ok"`
	Data        *EvergreenCanonicalArchive `json:"data"`
	Diagnostics []EvergreenDiagnostic      `json:"diagnostics,omitempty" api:"optional,nonnullable"`
}
