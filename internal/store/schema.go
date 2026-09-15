package store

import (
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

// 固定分区名（知识卡五分区 + 材料笔记五分区 + 观点五分区）。
const (
	SecKnowledge   = mdfile.SecKnowledge
	SecRationale   = mdfile.SecRationale
	SecBoundary    = mdfile.SecBoundary
	SecUserAppend  = mdfile.SecUserAppend
	SecSelfCheck   = mdfile.SecSelfCheck
	SecDigest      = mdfile.SecDigest
	SecAgentReview = mdfile.SecAgentReview
	SecOpenQuest   = mdfile.SecOpenQuest
	SecOutputCards = mdfile.SecOutputCards

	// —— 观点固定五分区（Schema v2 §3.2）——
	SecOpinionClaim = mdfile.SecOpinionClaim
	SecArgument     = mdfile.SecArgument
	SecCounter      = mdfile.SecCounter
	SecToVerify     = mdfile.SecToVerify
)

// KnownSections 返回该类型的固定分区名（按 F5 固定顺序）。
func KnownSections(kind Kind) []string { return mdfile.KnownSections(kind) }

// CardSections 返回知识卡五分区（顺序固定）。
func CardSections() []string { return mdfile.CardSections() }

// NoteSections 返回材料笔记五分区（顺序固定）。
func NoteSections() []string { return mdfile.NoteSections() }

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
