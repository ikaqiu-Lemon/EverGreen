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

// —— 观点验证状态（Schema v2 §6.1）——
//
// Validation 是 Opinion 唯一新增的 frontmatter 键的取值域，封闭三值。
// 它与 Status 是**两个正交维度**：Status 说「这条产物还算不算数」（active / deprecated），
// Validation 说「这个判断的论证走到哪一步」（pending / validated / rejected）。
// 一条被验证为不成立的观点仍然可以是 active——记录「已确认不成立」本身就是知识资产，
// 因此 rejected 绝不等于 deprecated，两者不得互相推导。
//
// 第四值（如 confirmed / partial）一律反序列化即报错：多一档就必然要回答
// 「它与另外三档的边界在哪」，而契约没有给出可机械判定的边界。
type Validation string

const (
	// ValidationPending 是新建观点的默认值：论据已记录，但尚未定论。
	ValidationPending Validation = "pending"
	// ValidationValidated 表示经用户显式确认成立。
	ValidationValidated Validation = "validated"
	// ValidationRejected 表示经用户显式确认不成立。
	ValidationRejected Validation = "rejected"
)

// ValidValidations 返回全部合法验证状态（顺序稳定：pending → validated / rejected，
// 与状态机的推进方向一致，供错误信息与文档使用）。
func ValidValidations() []Validation {
	return []Validation{ValidationPending, ValidationValidated, ValidationRejected}
}

// Valid 报告 v 是否为合法验证状态。
func (v Validation) Valid() bool {
	switch v {
	case ValidationPending, ValidationValidated, ValidationRejected:
		return true
	}
	return false
}

func (v Validation) String() string { return string(v) }

// Settled 报告论证是否已定论（validated 或 rejected）。
// pending 之外的两档都算已定论——「确认不成立」同样是结论。
func (v Validation) Settled() bool {
	return v == ValidationValidated || v == ValidationRejected
}

// ParseValidation 严格解析验证状态。
func ParseValidation(raw string) (Validation, error) {
	v := Validation(raw)
	if !v.Valid() {
		names := make([]string, 0, 3)
		for _, x := range ValidValidations() {
			names = append(names, string(x))
		}
		return "", fmt.Errorf("非法 validation %q：合法取值仅 %s（封闭三值，不接受第四档）",
			raw, strings.Join(names, " / "))
	}
	return v, nil
}

func (v *Validation) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var raw string
	if err := unmarshal(&raw); err != nil {
		return err
	}
	got, err := ParseValidation(raw)
	if err != nil {
		return err
	}
	*v = got
	return nil
}

func (v *Validation) UnmarshalJSON(b []byte) error {
	var raw string
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	got, err := ParseValidation(raw)
	if err != nil {
		return err
	}
	*v = got
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

// Verb 是 Git 提交动词。S1 会产生的 verb 恰六个（合同 2026-09-01-eg-cli-contract.md §6；
// 第六个 `relate` 自 M2 的 `eg rel add` 起真实产生，见 M2 查询与关系写入合同 §4.4）。
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

	// VerbRelate 是 `eg rel add` 的提交动词（M2 查询与关系写入合同 §4.4：
	// 一次 rel add = 一次 relate commit）。M1 期它被排除在 KnownVerbs 之外，
	// T-…-024 起纳入，否则主题行会退化成 process(<domain>)。
	VerbRelate Verb = "relate"

	// VerbProposal 是 `eg proposal new` / `reject` 的提交动词（提案合同 §10.2 时序第 5 步
	// `commit proposal(delete)`：`verb = proposal`）。纳入 KnownVerbs 的理由与 relate 同源：
	// 不纳入则 Message.Build 会把主题退化成 `process(<domain>)`，与合同时序表逐字不符。
	VerbProposal Verb = "proposal"

	// VerbDelete 是 `eg delete` 的提交动词（提案与状态合同 §5.3 时序第 10 步
	// `commit delete(<domain>)`：`verb = delete`）。纳入 KnownVerbs 的理由与
	// relate / proposal 同源：不纳入则 NormalizeVerb 会把它退化成 process，
	// 主题行与合同时序表逐字不符。
	VerbDelete Verb = "delete"
)

// KnownVerbs 返回本仓会产生的 verb 集合（S1 六个 + M3 的 proposal 与 delete）。
func KnownVerbs() []Verb {
	return []Verb{VerbInit, VerbReconcile, VerbCapture, VerbProcess, VerbReprocess,
		VerbRelate, VerbProposal, VerbDelete}
}

// Known 报告 v 是否属已知 verb。
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
// 未知值（含空值）**退化为 process** 并返回 false，供上层出 warning；
// `relate` 自 M2 起属已知 verb，不再退化。
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
