package store

import (
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// 本文件是 store 对上层的**只读结构口径**转发层：§13 依赖方向规定 internal/plan
// 只能依赖 model / query / rules / store，不得直连 mdfile，因此分区名、固定分区顺序、
// frontmatter 可解析性这些「文件结构事实」统一由本包转发，口径仍只有 mdfile 一份定义。
//
// 本文件不产生任何落盘字节：所有函数都只读入参，写形态仍只有
// CreateFile / AppendToSection / WriteGuarded 三个（B1）。

// Kind 是产物类型（口径同 mdfile.Kind，别名而非另一套枚举）。
type Kind = mdfile.Kind

// 产物类型。
const (
	KindSource = mdfile.KindSource
	KindNote   = mdfile.KindNote
	KindCard   = mdfile.KindCard
	// KindOpinion 是观点（Schema v2）。plan 层按此 Kind 取分区口径。
	KindOpinion = mdfile.KindOpinion
)

// 固定分区名（知识卡三分区 + 材料笔记四分区 + 观点五分区，Schema v2 §3.2）。
const (
	SecKnowledge  = mdfile.SecKnowledge
	SecBoundary   = mdfile.SecBoundary
	SecUserAppend = mdfile.SecUserAppend
	SecNoteBody   = mdfile.SecNoteBody
	SecExtraction = mdfile.SecExtraction
	SecOpenQuest  = mdfile.SecOpenQuest

	// —— 观点固定五分区（Schema v2 §3.2）——
	SecOpinionClaim = mdfile.SecOpinionClaim
	SecArgument     = mdfile.SecArgument
	SecCounter      = mdfile.SecCounter
	SecToVerify     = mdfile.SecToVerify

	// —— v1 存量分区名（v2 起不再是固定分区；仍需按名字定位存量文件）——
	SecRationale   = mdfile.SecRationale
	SecSelfCheck   = mdfile.SecSelfCheck
	SecDigest      = mdfile.SecDigest
	SecAgentReview = mdfile.SecAgentReview
	SecOutputCards = mdfile.SecOutputCards
)

// KnownSections 返回该类型的固定分区名（按 F5 固定顺序）。
func KnownSections(kind Kind) []string { return mdfile.KnownSections(kind) }

// CardSections 返回知识卡的固定三分区（顺序固定）。
func CardSections() []string { return mdfile.CardSections() }

// NoteSections 返回材料笔记的固定四分区（顺序固定）。
func NoteSections() []string { return mdfile.NoteSections() }

// LegacyV1Sections 返回该类型在 v1 是固定分区、v2 起不再是固定分区的分区名。
func LegacyV1Sections(kind Kind) []string { return mdfile.LegacyV1Sections(kind) }

// OpinionSections 返回观点的固定五分区（顺序固定）。
func OpinionSections() []string { return mdfile.OpinionSections() }

// NeverWriteSections 返回任何路径都不得写入的分区（安全底线 B2）。
//
// 转发的意义在于让 plan 层的授权判定与 mdfile 共用同一份口径：
// 新增观点后仍然只有「用户补充」一条，不因实体变多而分叉。
func NeverWriteSections() []string { return mdfile.NeverWriteSections() }

// AutoWritableSections 返回自动路径允许追加的分区（「用户补充」「存疑与待验证」除外）。
func AutoWritableSections(kind Kind) []string { return mdfile.AutoWritableSections(kind) }

// RequiredSection 返回该类型必须存在的分区名。
func RequiredSection(kind Kind) string { return mdfile.RequiredSection(kind) }

// CountBodyAnchors 返回一份文档正文里的 H2 + H3 标题数（去空标题、屏蔽围栏代码块）。
//
// 转发而非在上层重写：`W21` 结构覆盖诊断（Schema v2 契约 §4.3）要数原文的 `src_anchors`，
// 而「围栏代码块里的 `## 注释` 不算章节」这条口径依赖 mdfile 的围栏状态机与 frontmatter
// 边界。在 plan 层重写一遍的第一个后果就是带 Markdown 示例的技术文章被误报。
// 解析失败时返回 0，调用方据此**不判**诊断。
func CountBodyAnchors(raw []byte) int { return mdfile.CountBodyAnchors(raw) }

// SourceBody 返回一份文档 frontmatter 之后的正文字节切片（不复制、只读）。
//
// 转发而非在 plan 层直连 mdfile（§13 依赖方向）：v2 `write_note` 的 source_ref
// 覆盖校验要按「frontmatter 之后的正文物理行」计数，而 frontmatter 边界的判定
// （含 CRLF 分隔行、未闭合 frontmatter 等）依赖 mdfile 的索引口径。在 plan 层
// 重写一遍必然与 W21 的锚点口径漂移。frontmatter 未闭合等结构错误时返回 error，
// 上层据此判 E2（无法取得/解析 Source 正文即阻止 v2 blocks 落盘）。
func SourceBody(raw []byte) ([]byte, error) {
	doc, err := mdfile.Parse(raw)
	if err != nil {
		return nil, err
	}
	return doc.Raw[doc.BodyFrom:], nil
}

// PersistedSourceBody 返回一段原文正文 body 落盘为**新建 Source** 后、再经 SourceBody 取回
// 的正文字节 —— 即「这份 body 将来在盘上呈现成什么物理行布局」。
//
// 新建原文的完整字节由 sourceContent 拼装：frontmatter 外壳（document 恒以 "---\n\n" 收尾）
// 之后紧接 body，正文段的尾换行规则由同包 appendSourceBody 统一施加。mdfile.Parse 的 BodyFrom
// 落在闭合分隔行 "---\n" 之后，因此取回的正文以外壳残留的那个 \n 开头 —— 一个前导空白物理行
// （L1 空白 / L2 起为 body 正文），与既有 Source 的行号口径一致。
//
// 因此本函数只额外表达「外壳在 BodyFrom 之后残留的前导 \n」，正文尾换行完全委托 appendSourceBody，
// 与 sourceContent 共用同一实现，不再各自复制规则。
//
// 用途（§4.2.1）：同一 plan 内「先 add_source、后 write_note」时被引原文尚未落盘，其 source_ref
// 覆盖校验必须按这份**落盘后**布局计算行号；否则「当次按 op.Body 算 L1、落盘后重处理算 L2」
// 会整体漂移一行。它与 SourceBody(真实落盘文件) 的字节等价由持久化一致性测试锁死（外壳或尾换行
// 规则一旦改动，等价断言当场变红）。
func PersistedSourceBody(body []byte) []byte {
	out := make([]byte, 0, len(body)+2)
	out = append(out, '\n') // 外壳 "---\n\n" 在 BodyFrom 之后残留的前导空白物理行
	return appendSourceBody(out, body)
}

// FrontmatterInto 只读解析 raw 的 frontmatter 到 out：文档结构不合法或
// frontmatter YAML 不可解析时返回错误（上层据此判 E4）。
func FrontmatterInto(raw []byte, out interface{}) error {
	doc, err := mdfile.Parse(raw)
	if err != nil {
		return err
	}
	return doc.DecodeFM(out)
}

// CardOf 只读解析一张知识卡的 frontmatter 字段（含 relations[]）。
func CardOf(raw []byte) (model.Card, error) {
	_, card, err := mdfile.ParseCard(raw)
	return card, err
}

// OpinionOf 只读解析一条观点的 frontmatter 字段（含 relations[]）。
// 与 CardOf 对称：论证关系写链路对 o- 宿主取事实时经由本转发点，plan 层因此
// 不必直连 mdfile（§13 依赖方向）。
func OpinionOf(raw []byte) (model.Opinion, error) {
	_, op, err := mdfile.ParseOpinion(raw)
	return op, err
}

// RelationHost 是论证关系宿主（知识卡或观点）在**关系维度**的只读事实。
//
// 论证关系是跨类型的（端点前缀 k- / o-，见 model.RelationEndpoint）：宿主既可能是
// 知识卡也可能是观点，二者在 status / deleted_at / relations[] 三个字段上语义一致，
// 上层（plan 校验）只需要这三项即可完成 W3 判定、同对去重与命中计数，不必关心宿主是
// 哪一类实体。本结构把两类实体在关系维度共享的字段收敛成一个口径，避免调用方按类型
// 各写一套读取分支。
type RelationHost struct {
	Kind      Kind
	ID        string
	Status    model.Status
	Tombstone *model.Stamp // 逻辑删除墓碑（对应 frontmatter 的 deleted_at；nil = 未删）
	Relations []model.Relation
}

// RelationHostOf 按端点前缀（k- 走知识卡、o- 走观点）解析宿主的关系维度事实。
//
// 端点必须已是合法的关系端点（k- / o-）；非法或其它前缀直接返回错误（调用方据此拒绝），
// 绝不猜测宿主类型。解析仍是**只读**：不改写宿主一个字节。
func RelationHostOf(endpoint model.RelationEndpoint, raw []byte) (RelationHost, error) {
	if _, err := model.ParseRelationEndpoint(string(endpoint)); err != nil {
		return RelationHost{}, err
	}
	if strings.HasPrefix(string(endpoint), model.PrefixOpinion) {
		op, err := OpinionOf(raw)
		if err != nil {
			return RelationHost{}, err
		}
		return RelationHost{Kind: KindOpinion, ID: string(op.ID), Status: op.Status,
			Tombstone: op.DeletedAt, Relations: op.Relations}, nil
	}
	card, err := CardOf(raw)
	if err != nil {
		return RelationHost{}, err
	}
	return RelationHost{Kind: KindCard, ID: string(card.ID), Status: card.Status,
		Tombstone: card.DeletedAt, Relations: card.Relations}, nil
}
