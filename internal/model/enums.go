package model

import (
	"encoding/json"
	"fmt"
	"strings"
)

// —— 状态（冻结合同 F3；EG-KNW-01）——
//
// Status 只有两个值。第三值（历史上的 `candidate`）属「不得复活的已废弃设计」，
// 反序列化即报错，错误信息带上合法取值集合。
type Status string

const (
	StatusActive     Status = "active"
	StatusDeprecated Status = "deprecated"
)

// ValidStatuses 返回全部合法状态（顺序稳定，供错误信息与文档使用）。
func ValidStatuses() []Status { return []Status{StatusActive, StatusDeprecated} }

// Valid 报告 s 是否为合法状态。
func (s Status) Valid() bool { return s == StatusActive || s == StatusDeprecated }

func (s Status) String() string { return string(s) }

// Deprecated 报告是否应打显著标记 [失效]（S1 唯一可用的显著标记）。
func (s Status) Deprecated() bool { return s == StatusDeprecated }

// ParseStatus 严格解析状态值。
func ParseStatus(raw string) (Status, error) {
	s := Status(raw)
	if !s.Valid() {
		return "", fmt.Errorf("非法 status %q：合法取值仅 %s（第三值属已废弃设计，不得复活）",
			raw, joinStatuses(ValidStatuses()))
	}
	return s, nil
}

func joinStatuses(ss []Status) string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, string(s))
	}
	return strings.Join(out, " / ")
}

// UnmarshalYAML 实现 yaml.v3 仍支持的 v2 风格 Unmarshaler，使本包无需 import YAML 库。
func (s *Status) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var raw string
	if err := unmarshal(&raw); err != nil {
		return err
	}
	v, err := ParseStatus(raw)
	if err != nil {
		return err
	}
	*s = v
	return nil
}

func (s *Status) UnmarshalJSON(b []byte) error {
	var raw string
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	v, err := ParseStatus(raw)
	if err != nil {
		return err
	}
	*s = v
	return nil
}

// —— 关系类型两组（冻结合同 F4）——

// MaterialRel 是材料关系：知识卡 sources[].rel 的取值。
type MaterialRel string

const (
	MaterialSupport MaterialRel = "support"
	MaterialAgainst MaterialRel = "against"
	MaterialContext MaterialRel = "context"
)

// ValidMaterialRels 返回材料关系全集。
// 注意：查询层 context 与 support 严格分离，任何「证据」类统计不含 context（§6）。
func ValidMaterialRels() []MaterialRel {
	return []MaterialRel{MaterialSupport, MaterialAgainst, MaterialContext}
}

func (r MaterialRel) Valid() bool {
	return r == MaterialSupport || r == MaterialAgainst || r == MaterialContext
}

func (r MaterialRel) String() string { return string(r) }

// ParseMaterialRel 严格解析材料关系。
func ParseMaterialRel(raw string) (MaterialRel, error) {
	r := MaterialRel(raw)
	if !r.Valid() {
		names := make([]string, 0, 3)
		for _, v := range ValidMaterialRels() {
			names = append(names, string(v))
		}
		return "", fmt.Errorf("非法材料关系 rel %q：合法取值仅 %s（冻结合同 F4）",
			raw, strings.Join(names, " / "))
	}
	return r, nil
}

func (r *MaterialRel) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var raw string
	if err := unmarshal(&raw); err != nil {
		return err
	}
	v, err := ParseMaterialRel(raw)
	if err != nil {
		return err
	}
	*r = v
	return nil
}

func (r *MaterialRel) UnmarshalJSON(b []byte) error {
	var raw string
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	v, err := ParseMaterialRel(raw)
	if err != nil {
		return err
	}
	*r = v
	return nil
}

// RelationType 是论证关系：知识卡 relations[].type 的取值。
type RelationType string

const (
	RelationDerives  RelationType = "derives"
	RelationSupports RelationType = "supports"
	RelationLimits   RelationType = "limits"
	RelationOpposing RelationType = "opposing"
)

// ValidRelationTypes 返回论证关系全集（`replaced_by` 属生命周期字段，不在本集合内）。
func ValidRelationTypes() []RelationType {
	return []RelationType{RelationDerives, RelationSupports, RelationLimits, RelationOpposing}
}

func (t RelationType) Valid() bool {
	switch t {
	case RelationDerives, RelationSupports, RelationLimits, RelationOpposing:
		return true
	}
	return false
}

func (t RelationType) String() string { return string(t) }

// ParseRelationType 严格解析论证关系。
func ParseRelationType(raw string) (RelationType, error) {
	t := RelationType(raw)
	if !t.Valid() {
		names := make([]string, 0, 4)
		for _, v := range ValidRelationTypes() {
			names = append(names, string(v))
		}
		return "", fmt.Errorf("非法论证关系 type %q：合法取值仅 %s（冻结合同 F4）",
			raw, strings.Join(names, " / "))
	}
	return t, nil
}

func (t *RelationType) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var raw string
	if err := unmarshal(&raw); err != nil {
		return err
	}
	v, err := ParseRelationType(raw)
	if err != nil {
		return err
	}
	*t = v
	return nil
}

func (t *RelationType) UnmarshalJSON(b []byte) error {
	var raw string
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	v, err := ParseRelationType(raw)
	if err != nil {
		return err
	}
	*t = v
	return nil
}

// —— commit verb 与领域 ——

// Verb 是 Git 提交动词。S1 会产生的 verb 恰五个（合同 2026-09-01-eg-cli-contract.md §6）。
//
// VerbReconcile 是 §7.1 `eg config get|set` 行的提交动词，**≠** S3 的 `eg reconcile`
// 对账命令，也 **≠** §4.6 报告字段 `reconcile`——三者同名分属三层。
type Verb string

const (
	VerbInit      Verb = "init"
	VerbReconcile Verb = "reconcile"
	VerbCapture   Verb = "capture"
	VerbProcess   Verb = "process"
	VerbReprocess Verb = "reprocess"

	// VerbRelate 属 M2（`eg rel add|remove`），M1 不产生。
	VerbRelate Verb = "relate"
)

// KnownVerbs 返回 M1 会产生的 verb 集合（不含 M2 的 relate）。
func KnownVerbs() []Verb {
	return []Verb{VerbInit, VerbReconcile, VerbCapture, VerbProcess, VerbReprocess}
}

// Known 报告 v 是否属 M1 已知 verb。
func (v Verb) Known() bool {
	for _, k := range KnownVerbs() {
		if v == k {
			return true
		}
	}
	return false
}

func (v Verb) String() string { return string(v) }

// NormalizeVerb 把任意输入归一化为可提交的 verb：
// 未知值（含空值与 M2 的 relate）**退化为 process** 并返回 false，供上层出 warning。
// 语义出处：§4.5「verb 未知 → warning 并退化为 process」。
func NormalizeVerb(raw string) (Verb, bool) {
	v := Verb(raw)
	if v.Known() {
		return v, true
	}
	return VerbProcess, false
}

// Domain 是领域名。领域由**目录**唯一决定（EG-DOM-01），因此 domain 绝不写进产物
// frontmatter 顶层（见 blacklist.go）；plan.domain / --domain / target_domain 是白名单用法。
type Domain string

func (d Domain) String() string { return string(d) }

// Empty 报告是否为空领域（plan 的全局操作填 null）。
func (d Domain) Empty() bool { return strings.TrimSpace(string(d)) == "" }
