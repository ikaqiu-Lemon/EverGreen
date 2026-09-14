package rules

// 收敛三维度判据（技术方案 §4.5.1；需求 EG-CVG-01 / EG-CVG-02 / EG-EXT-03）。
//
// 三维度：核心知识 / 成立条件 / 独立复用用途。**任一维度不同 → 拆两张卡**；
// **判不出（缺失或取值非法）同样拆两张卡**——宁拆勿并，合并是不可逆的信息损失。
// 本文件只做判据，不写盘、不产出诊断编号（W5 的登记在 internal/plan）。

import "strings"

// Verdict 是单个维度的判定值（封闭二值 + 判不出）。
type Verdict string

// 维度判定值。Unknown 表示缺失或取值非法——S1 不猜，按「判不出」处理。
const (
	Same      Verdict = "same"
	Different Verdict = "different"
	Unknown   Verdict = ""
)

// ParseVerdict 解析维度取值；集合外取值一律退化为 Unknown（由上层登记 W5）。
func ParseVerdict(raw string) Verdict {
	switch strings.TrimSpace(raw) {
	case string(Same):
		return Same
	case string(Different):
		return Different
	default:
		return Unknown
	}
}

// Dims 是一条 convergence 条目的三维度结论。
type Dims struct {
	Core         Verdict // 核心知识
	Conditions   Verdict // 成立条件
	ReusePurpose Verdict // 独立复用用途
}

// Complete 报告三维度是否齐全（三个都是封闭二值之一）。
func (d Dims) Complete() bool {
	return d.Core != Unknown && d.Conditions != Unknown && d.ReusePurpose != Unknown
}

// AllSame 报告三维度是否全为 same（唯一允许复用已有卡的形态）。
func (d Dims) AllSame() bool {
	return d.Core == Same && d.Conditions == Same && d.ReusePurpose == Same
}

// Decision 是收敛判定结果（封闭二值）。
type Decision string

// 判定结果。
const (
	// SplitTwoCards 拆两张卡：任一维度不同，或判不出。
	SplitTwoCards Decision = "split_two_cards"
	// ReuseExisting 复用已有卡：三维度全同。
	ReuseExisting Decision = "reuse_existing"
)

// Converge 给出收敛判定：三维度全同才允许复用，其余（含判不出）一律拆两张卡。
func Converge(d Dims) Decision {
	if d.Complete() && d.AllSame() {
		return ReuseExisting
	}
	return SplitTwoCards
}

// Determined 报告本次判定是否有充分依据（三维度齐全）。判不出时判定仍然成立
// （拆两张卡），只是上层需要登记 W5 并在报告里如实说明。
func Determined(d Dims) bool { return d.Complete() }

// ConsistentWithDims 报告处理关系与三维度结论是否一致。
//
// 口径只有一条：**「语义相同」等价于「三维度全同」**——
// `same_semantics` 必须三维度全同，其余六个取值必须存在至少一个维度不同。
// 三维度不齐时无法比对，返回 true（不齐本身已由 W5 登记，不重复告警）。
// relation 取值是否在七值封闭枚举内由 internal/plan 校验，本函数只做一致性比对。
func ConsistentWithDims(relation string, d Dims) bool {
	if !d.Complete() {
		return true
	}
	if strings.TrimSpace(relation) == "same_semantics" {
		return d.AllSame()
	}
	return !d.AllSame()
}
