package model

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// 稳定 ID（冻结合同 F2）：
//
//	原文 s-<yyyymmdd>-<slug>   笔记 n-<yyyymmdd>-<slug>   知识 k-<yyyymmdd>-<slug>
//	观点 o-<yyyymmdd>-<slug>   综述 r-（S2）              提案 p-<yyyymmdd>-<seq>（S2）
//
// 四个 ID 类型**互不可赋值**（各自是独立的命名类型，跨类型赋值是编译期错误），
// 构造入口一律做前缀校验。`slug` **仅供人眼可读，不参与任何判定**：解析结果里的
// 领域判定、关系定位、去重全部只看前缀 + 日期 + ID 全串本身。
//
// **类型只由 ID 前缀 + 目录表达**（Schema v2 §3.1）：Knowledge 与 Opinion 的区分靠
// `k-` / `o-` 与 `knowledge/` / `opinions/`，不靠 frontmatter 里的 type 字段——
// 冗余元数据会让真源漂移（同 EG-DOM-01 对 domain 的处置）。
//
// 关系一律引用 ID、不引用路径，因此文件允许被重命名或移动（id → path 由扫描完成）。
const (
	PrefixSource = "s-"
	PrefixNote   = "n-"
	PrefixCard   = "k-"

	// PrefixOpinion 是观点前缀（Schema v2）。观点与知识是**同级**产物，
	// 不是知识的子类型，因此拿到独立前缀而非 `k-` 上的一个字段。
	PrefixOpinion = "o-"

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

// OpinionID 是观点 ID（前缀 o-）。
type OpinionID string

func (id SourceID) String() string  { return string(id) }
func (id NoteID) String() string    { return string(id) }
func (id CardID) String() string    { return string(id) }
func (id OpinionID) String() string { return string(id) }

// ParsedID 是 ID 的结构化解析结果。Slug 只作人眼可读信息随附，不参与任何判定。
type ParsedID struct {
	Prefix string // "s-" / "n-" / "k-" / "o-" / "r-" / "p-"
	Date   string // yyyymmdd
	Slug   string // 仅供人眼可读
}

// KnownPrefixes 返回全部已纳管的 ID 前缀（顺序稳定，供解析与错误信息共用）。
//
// 单一定义点：ParseID 的遍历与错误信息都读它，新增实体只需在此登记一次，
// 不会出现「解析认了但错误信息没提」这种半纳管状态。
func KnownPrefixes() []string {
	return []string{PrefixSource, PrefixNote, PrefixCard, PrefixOpinion,
		PrefixReview, PrefixProposal}
}

// ParseID 解析任意产物 ID：`<prefix>-<yyyymmdd>-<slug>`。
func ParseID(raw string) (ParsedID, error) {
	for _, p := range KnownPrefixes() {
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
		"非法 ID %q：前缀必须是 %s / %s / %s / %s（S2 预留 %s / %s）",
		raw, PrefixSource, PrefixNote, PrefixCard, PrefixOpinion,
		PrefixReview, PrefixProposal)
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
		return ParsedID{}, fmt.Errorf("ID %q 的前缀是 %q，期望 %q（四类 ID 互不可混用，冻结合同 F2）",
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

// ParseOpinionID 解析并校验前缀，防止 k-/n-/s- 串被当成观点 ID 使用。
func ParseOpinionID(raw string) (OpinionID, error) {
	if _, err := parseWithPrefix(raw, PrefixOpinion); err != nil {
		return "", err
	}
	return OpinionID(raw), nil
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

// Valid 报告 ID 前缀与形态是否合法。
func (id OpinionID) Valid() bool {
	_, err := parseWithPrefix(string(id), PrefixOpinion)
	return err == nil
}

// RelationEndpoint 是论证关系的一个端点 ID（`relations[].target` 的落盘类型）。
//
// 论证关系是**跨类型**的：它可连知识卡（k-）或观点（o-）——两者是同级论证性产物，
// 因此端点不再收窄成 CardID。端点**只接受** k- / o- 两种前缀：s- / n- / r- / p-
// 以及任何不可解析 ID 一律拒绝（论证关系永远发生在两条论证性产物之间，不指向
// 原文 / 笔记 / 综述 / 提案）。底层是 string，YAML/JSON 落盘仍是一个 `target: <id>`
// 标量，键形态与旧版逐字一致（本次只泛化类型，不动 schema）。
type RelationEndpoint string

func (e RelationEndpoint) String() string { return string(e) }

// RelationEndpointPrefixes 返回关系端点允许的前缀（k- / o-，顺序稳定）。
func RelationEndpointPrefixes() []string {
	return []string{PrefixCard, PrefixOpinion}
}

// ParseRelationEndpoint 解析并校验关系端点：形态合法且前缀恰为 k- / o-。
// 其余前缀（s- / n- / r- / p-）与不可解析 ID 一律拒绝。
func ParseRelationEndpoint(raw string) (RelationEndpoint, error) {
	p, err := ParseID(raw)
	if err != nil {
		return "", err
	}
	if p.Prefix != PrefixCard && p.Prefix != PrefixOpinion {
		return "", fmt.Errorf(
			"关系端点 %q 的前缀是 %q，期望 %s / %s（论证关系只连知识卡或观点）",
			raw, p.Prefix, PrefixCard, PrefixOpinion)
	}
	return RelationEndpoint(raw), nil
}

// Valid 报告关系端点是否形态合法且前缀在 { k-, o- } 内。
func (e RelationEndpoint) Valid() bool {
	_, err := ParseRelationEndpoint(string(e))
	return err == nil
}

// NewSourceID / NewNoteID / NewCardID / NewOpinionID 生成稳定 ID。同一 (日期, 标题) 幂等。
func NewSourceID(d Date, title string) SourceID { return SourceID(newID(PrefixSource, d, title)) }
func NewNoteID(d Date, title string) NoteID     { return NoteID(newID(PrefixNote, d, title)) }
func NewCardID(d Date, title string) CardID     { return CardID(newID(PrefixCard, d, title)) }
func NewOpinionID(d Date, title string) OpinionID {
	return OpinionID(newID(PrefixOpinion, d, title))
}

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
