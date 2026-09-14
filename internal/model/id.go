package model

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// 稳定 ID（冻结合同 F2）：
//
//	原文 s-<yyyymmdd>-<slug>   笔记 n-<yyyymmdd>-<slug>   知识卡 k-<yyyymmdd>-<slug>
//	综述 r-（S2）              提案 p-<yyyymmdd>-<seq>（S2）
//
// 三个 ID 类型**互不可赋值**（各自是独立的命名类型，跨类型赋值是编译期错误），
// 构造入口一律做前缀校验。`slug` **仅供人眼可读，不参与任何判定**：解析结果里的
// 领域判定、关系定位、去重全部只看前缀 + 日期 + ID 全串本身。
//
// 关系一律引用 ID、不引用路径，因此文件允许被重命名或移动（id → path 由扫描完成）。
const (
	PrefixSource = "s-"
	PrefixNote   = "n-"
	PrefixCard   = "k-"

	// S2 预留（本阶段只留常量，不实现业务）。
	PrefixReview   = "r-"
	PrefixProposal = "p-"
)

// SourceID 是原文 ID（前缀 s-）。
type SourceID string

// NoteID 是材料笔记 ID（前缀 n-）。
type NoteID string

// CardID 是知识卡 ID（前缀 k-）。
type CardID string

func (id SourceID) String() string { return string(id) }
func (id NoteID) String() string   { return string(id) }
func (id CardID) String() string   { return string(id) }

// ParsedID 是 ID 的结构化解析结果。Slug 只作人眼可读信息随附，不参与任何判定。
type ParsedID struct {
	Prefix string // "s-" / "n-" / "k-" / "r-" / "p-"
	Date   string // yyyymmdd
	Slug   string // 仅供人眼可读
}

// ParseID 解析任意产物 ID：`<prefix>-<yyyymmdd>-<slug>`。
func ParseID(raw string) (ParsedID, error) {
	for _, p := range []string{PrefixSource, PrefixNote, PrefixCard, PrefixReview, PrefixProposal} {
		if !strings.HasPrefix(raw, p) {
			continue
		}
		rest := raw[len(p):]
		i := strings.IndexByte(rest, '-')
		if i < 0 {
			return ParsedID{}, fmt.Errorf("非法 ID %q：缺少 <yyyymmdd>-<slug> 部分", raw)
		}
		date, slug := rest[:i], rest[i+1:]
		if len(date) != 8 || !allDigits(date) {
			return ParsedID{}, fmt.Errorf("非法 ID %q：日期段 %q 必须是 8 位 yyyymmdd", raw, date)
		}
		if slug == "" {
			return ParsedID{}, fmt.Errorf("非法 ID %q：slug 段为空", raw)
		}
		return ParsedID{Prefix: p, Date: date, Slug: slug}, nil
	}
	return ParsedID{}, fmt.Errorf(
		"非法 ID %q：前缀必须是 %s / %s / %s（S2 预留 %s / %s）",
		raw, PrefixSource, PrefixNote, PrefixCard, PrefixReview, PrefixProposal)
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func parseWithPrefix(raw, want string) (ParsedID, error) {
	p, err := ParseID(raw)
	if err != nil {
		return ParsedID{}, err
	}
	if p.Prefix != want {
		return ParsedID{}, fmt.Errorf("ID %q 的前缀是 %q，期望 %q（三类 ID 互不可混用，冻结合同 F2）",
			raw, p.Prefix, want)
	}
	return p, nil
}

// ParseSourceID 解析并校验前缀，防止 n-/k- 串被当成原文 ID 使用。
func ParseSourceID(raw string) (SourceID, error) {
	if _, err := parseWithPrefix(raw, PrefixSource); err != nil {
		return "", err
	}
	return SourceID(raw), nil
}

// ParseNoteID 解析并校验前缀。
func ParseNoteID(raw string) (NoteID, error) {
	if _, err := parseWithPrefix(raw, PrefixNote); err != nil {
		return "", err
	}
	return NoteID(raw), nil
}

// ParseCardID 解析并校验前缀。
func ParseCardID(raw string) (CardID, error) {
	if _, err := parseWithPrefix(raw, PrefixCard); err != nil {
		return "", err
	}
	return CardID(raw), nil
}

// Valid 报告 ID 前缀与形态是否合法。
func (id SourceID) Valid() bool {
	_, err := parseWithPrefix(string(id), PrefixSource)
	return err == nil
}

// Valid 报告 ID 前缀与形态是否合法。
func (id NoteID) Valid() bool { _, err := parseWithPrefix(string(id), PrefixNote); return err == nil }

// Valid 报告 ID 前缀与形态是否合法。
func (id CardID) Valid() bool { _, err := parseWithPrefix(string(id), PrefixCard); return err == nil }

// NewSourceID / NewNoteID / NewCardID 生成稳定 ID。同一 (日期, 标题) 幂等。
func NewSourceID(d Date, title string) SourceID { return SourceID(newID(PrefixSource, d, title)) }
func NewNoteID(d Date, title string) NoteID     { return NoteID(newID(PrefixNote, d, title)) }
func NewCardID(d Date, title string) CardID     { return CardID(newID(PrefixCard, d, title)) }

func newID(prefix string, d Date, title string) string {
	return prefix + d.Compact() + "-" + Slug(title)
}

// SlugMaxLen 是 slug 的最大长度（仅为文件名友好，不参与判定）。
const SlugMaxLen = 48

// Slug 把标题做 ASCII 化：输出只含 [a-z0-9-]，确定性且幂等。
//
// 实现只做**字节级**处理（不做 rune 迭代重建、不做 strings.* 规范化），因此非法 UTF-8
// 输入不会有损；标题里的非 ASCII 字节（如中文）无法音译，退化为标题 sha256 的前 8 位
// 十六进制，保证仍是纯 ASCII 且确定性。slug 仅供人眼可读，退化不影响任何判定。
func Slug(title string) string {
	src := []byte(title)
	out := make([]byte, 0, len(src))
	prevDash := false
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case c >= 'A' && c <= 'Z':
			out = append(out, c+('a'-'A'))
			prevDash = false
		case (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'):
			out = append(out, c)
			prevDash = false
		default:
			if !prevDash && len(out) > 0 {
				out = append(out, '-')
				prevDash = true
			}
		}
		if len(out) >= SlugMaxLen {
			break
		}
	}
	for len(out) > 0 && out[len(out)-1] == '-' {
		out = out[:len(out)-1]
	}
	if len(out) == 0 {
		sum := sha256.Sum256(src)
		return "x" + hex.EncodeToString(sum[:4])
	}
	return string(out)
}
