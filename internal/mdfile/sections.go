package mdfile

// 五分区固定名与顺序（冻结合同 F5；技术方案 §4.2 / §4.3）。
//
// 名称**逐字固定**、顺序**固定**；缺失分区按空处理；第六个及以后的 H2 是用户自建分区，
// 一律原样保留——不报错、不删除、不重排、**不写入其中**。
// 分区改名即解析失败（EG-NOTE-01：分区是内容归属与合并单元，改名等于重切正文）。

import (
	"fmt"
	"strings"
)

// Kind 是文档类型。
type Kind string

// 文档类型。
const (
	KindCard   Kind = "card"
	KindNote   Kind = "note"
	KindSource Kind = "source"
)

// 知识卡五分区（顺序固定）。
const (
	SecKnowledge   = "知识内容"
	SecRationale   = "解释与依据"
	SecBoundary    = "条件与边界"
	SecUserAppend  = "用户补充"
	SecSelfCheck   = "理解自检"
	SecDigest      = "材料提炼"
	SecAgentReview = "Agent 分析"
	SecOpenQuest   = "存疑与待验证"
	SecOutputCards = "产出知识卡"
)

// CardSections 是知识卡五分区，顺序固定（F5）。
func CardSections() []string {
	return []string{SecKnowledge, SecRationale, SecBoundary, SecUserAppend, SecSelfCheck}
}

// NoteSections 是材料笔记五分区，顺序固定（F5）。
// 材料笔记**没有**「理解自检」——该分区是知识卡独有（EG-NOTE-01）。
func NoteSections() []string {
	return []string{SecDigest, SecAgentReview, SecUserAppend, SecOpenQuest, SecOutputCards}
}

// KnownSections 返回该类型的固定分区名。
func KnownSections(kind Kind) []string {
	switch kind {
	case KindCard:
		return CardSections()
	case KindNote:
		return NoteSections()
	default:
		return nil
	}
}

// RequiredSection 返回该类型**必须存在**的分区：知识卡「知识内容」必写；
// 材料笔记「材料提炼」是加工产出的落点。分区改名会导致这一必需分区缺失，
// 从而在 ValidateSections 处报错。
func RequiredSection(kind Kind) string {
	switch kind {
	case KindCard:
		return SecKnowledge
	case KindNote:
		return SecDigest
	default:
		return ""
	}
}

// NeverWriteSections 是**任何时候都不得写入**的分区（安全底线 B2 / §4.2）。
func NeverWriteSections() []string { return []string{SecUserAppend} }

// AutoWritableSections 返回对**已有**产物允许自动追加的分区（§4.2）。
// 知识卡：只允许「解释与依据」「条件与边界」「理解自检」；「用户补充」永不写。
// 材料笔记：允许「材料提炼」「Agent 分析」「存疑与待验证」「产出知识卡」。
func AutoWritableSections(kind Kind) []string {
	switch kind {
	case KindCard:
		return []string{SecRationale, SecBoundary, SecSelfCheck}
	case KindNote:
		return []string{SecDigest, SecAgentReview, SecOpenQuest, SecOutputCards}
	default:
		return nil
	}
}

// SectionError 是分区结构错误：带期望分区名与位置（字节偏移 + 行号）。
type SectionError struct {
	Kind     Kind
	Expected string // 期望的分区名
	Got      string // 实际读到的分区名（可空）
	Offset   int    // 位置：字节偏移
	Line     int    // 位置：行号（1 起）
	Reason   string
}

func (e *SectionError) Error() string {
	loc := fmt.Sprintf("偏移 %d（第 %d 行）", e.Offset, e.Line)
	if e.Got == "" {
		return fmt.Sprintf("%s 分区结构错误：%s；期望分区「%s」，位置 %s；五分区名称逐字固定、顺序固定（F5）",
			e.Kind, e.Reason, e.Expected, loc)
	}
	return fmt.Sprintf("%s 分区结构错误：%s；期望分区「%s」，实际读到「%s」，位置 %s；"+
		"五分区名称逐字固定、顺序固定（F5）", e.Kind, e.Reason, e.Expected, e.Got, loc)
}

// ValidateSections 校验固定分区的存在性与顺序。
//
// 判定口径：
//   - 必需分区缺失 → 报错（分区改名会走到这里：改名后固定名找不到）；
//   - 固定分区重复 → 报错；
//   - 固定分区之间顺序颠倒 → 报错；
//   - 其余 H2（第六个及以后 / 用户自建）→ **不报错**，由 UnknownSections 返回供报告记 info。
func (d *Doc) ValidateSections(kind Kind) error {
	known := KnownSections(kind)
	if len(known) == 0 {
		return nil
	}
	rank := map[string]int{}
	for i, n := range known {
		rank[n] = i
	}
	seen := map[string]int{}
	last := -1
	lastName := ""
	for _, s := range d.Sections {
		r, ok := rank[s.Name]
		if !ok {
			continue
		}
		if _, dup := seen[s.Name]; dup {
			return &SectionError{Kind: kind, Expected: s.Name, Got: s.Name,
				Offset: s.Start, Line: lineNumber(d.Raw, s.Start),
				Reason: "固定分区重复出现"}
		}
		seen[s.Name] = r
		if r < last {
			return &SectionError{Kind: kind, Expected: known[last], Got: s.Name,
				Offset: s.Start, Line: lineNumber(d.Raw, s.Start),
				Reason: fmt.Sprintf("固定分区顺序颠倒（「%s」出现在「%s」之后）", s.Name, lastName)}
		}
		last, lastName = r, s.Name
	}
	if req := RequiredSection(kind); req != "" {
		if _, ok := seen[req]; !ok {
			got, off := d.firstUnknownSection(kind)
			return &SectionError{Kind: kind, Expected: req, Got: got,
				Offset: off, Line: lineNumber(d.Raw, off),
				Reason: fmt.Sprintf("必需分区缺失（分区名被改写即等于重切正文；固定五分区为 %s）",
					strings.Join(known, " / "))}
		}
	}
	return nil
}

// firstUnknownSection 返回第一个非固定分区名及其偏移，用于错误定位；没有则回落到正文起始。
func (d *Doc) firstUnknownSection(kind Kind) (string, int) {
	known := map[string]bool{}
	for _, n := range KnownSections(kind) {
		known[n] = true
	}
	for _, s := range d.Sections {
		if !known[s.Name] {
			return s.Name, s.Start
		}
	}
	return "", d.BodyFrom
}

// UnknownSections 返回不属于固定五分区的 H2（用户自建分区）：原样保留，只记 info。
func (d *Doc) UnknownSections(kind Kind) []Span {
	known := map[string]bool{}
	for _, n := range KnownSections(kind) {
		known[n] = true
	}
	var out []Span
	for _, s := range d.Sections {
		if !known[s.Name] {
			out = append(out, s)
		}
	}
	return out
}

// lineNumber 返回 off 所在行号（1 起）。只读统计，不影响落盘字节。
func lineNumber(raw []byte, off int) int {
	if off > len(raw) {
		off = len(raw)
	}
	n := 1
	for i := 0; i < off; i++ {
		if raw[i] == '\n' {
			n++
		}
	}
	return n
}
