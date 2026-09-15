package mdfile

// 固定分区名与顺序（冻结合同 F5；技术方案 §4.2 / §4.3；Schema v2 契约 §3.2）。
//
// 名称**逐字固定**、顺序**固定**；缺失分区按空处理；固定分区之外的 H2 是用户自建分区
// 或 v1 存量分区，一律原样保留——不报错、不删除、不重排、**不写入其中**。
// 分区改名即解析失败（EG-NOTE-01：分区是内容归属与合并单元，改名等于重切正文）。
//
// Schema v2 的分区模板分两步落地，本文件已完成第二步：
//   - 第一步（观点基座，T-…-002）：**纯加性**——新增 Opinion 与它的固定五分区。
//   - 第二步（ChangePlan v2 写口，T-…-003，本次）：Knowledge 收敛为三分区、
//     Note 合并为四分区，与授权矩阵、`plan` 校验、writers、CLI 文案**同一提交**切换。
//
// 为什么两步不能合并成一步、也不能再拆得更细：`AutoWritableSections` 与
// `RequiredSection` 是 `internal/plan` 授权矩阵与 `write_note` / `append_knowledge`
// 校验的唯一口径来源。单独改这里会让写口停在「模板已是 v2、校验仍是 v1」的状态，
// 落到测试上就是一批必然失败的用例——那等于把红留给下一个提交。
//
// v1 → v2 的两处收敛（契约 D-7 / §3.2）：
//   - Knowledge 移除 `解释与依据`（论证职责整体移交 Opinion 的 `论据与推理`）与
//     `理解自检`（正确性改由「是否忠实整理自 Note」保证）。
//   - Note 把 `材料提炼` + `Agent 分析` 合并为 `整理正文`（Note 是整理版文章而非摘要，
//     来源内容与 Agent 补充按原文顺序**就近内嵌**，不再按「谁写的」全局切成两段），
//     并把 `产出知识卡` 更名扩展为 `提取结果`（同时列 Knowledge 与 Opinion）。
//
// 被移除的四个分区名**仍是本包的常量**（见 LegacyV1Sections）：存量文件里的同名 H2
// 由 UnknownSections 承接，原样保留、只记 info、字节不变；`replace_block` 的
// 「理解自检」形态与 `eg edit` 的可编辑白名单也仍需按名字定位它们。

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

	// KindOpinion 是观点（Schema v2）。与 KindCard **同级**，不是它的子类型：
	// 观点不是「带倾向的知识卡」，两者的正文结构、写权限与生命周期都不同。
	KindOpinion Kind = "opinion"
)

// 分区名（Schema v2）。
//
// 「用户补充」被四类实体共用：它在任何实体上都是同一条安全底线（B2），
// 复制一份新常量只会让 NeverWriteSections 需要按类型分支。
const (
	SecKnowledge  = "知识内容"
	SecBoundary   = "条件与边界"
	SecUserAppend = "用户补充"
	SecNoteBody   = "整理正文"
	SecExtraction = "提取结果"
	SecOpenQuest  = "存疑与待验证"
)

// v1 存量分区名（Schema v2 起**不再是固定分区**，但常量必须保留）。
//
// 保留常量不是「留后门」：
//   - 存量文件里的同名 H2 要能被按名字定位，才能做到「原样保留、字节不变」并在
//     迁移（T-…-009）时被人工收口；
//   - `replace_block` 的历史记录块形态、`eg edit` 的可编辑白名单都以分区名为参数，
//     它们对**存量**文件仍必须可用；
//   - 授权矩阵是「规则全集」而不是「已实现动作清单」，被移除分区的授权行仍在册。
//
// 新建产物**不会**再产生这四个 H2：它们不在 KnownSections 里，写入侧的
// AutoWritableSections 白名单也不含它们。
const (
	// SecRationale 的职责已整体移交 Opinion 的「论据与推理」（契约 D-7）。
	SecRationale = "解释与依据"
	// SecSelfCheck 是「Agent 必须自证读懂了」时代的产物（契约 D-7）。
	SecSelfCheck = "理解自检"
	// SecDigest 与 SecAgentReview 已合并为「整理正文」（契约 §3.2）。
	SecDigest      = "材料提炼"
	SecAgentReview = "Agent 分析"
	// SecOutputCards 已更名扩展为「提取结果」（同时列 Knowledge 与 Opinion）。
	SecOutputCards = "产出知识卡"
)

// LegacyV1Sections 返回某类型在 v1 里是固定分区、v2 起**不再是**固定分区的分区名。
//
// 唯一真源：迁移工具（T-…-009）、报告 info 文案与门禁都读这一份，
// 不各自抄一份字面量清单——两份清单必然有一天对不上。
func LegacyV1Sections(kind Kind) []string {
	switch kind {
	case KindCard:
		return []string{SecRationale, SecSelfCheck}
	case KindNote:
		return []string{SecDigest, SecAgentReview, SecOutputCards}
	default:
		return nil
	}
}

// V1Sections 返回该类型在 **v1** 模板下的固定分区名与顺序。
//
// 为什么必须保留一份完整的 v1 顺序表，而不是只保留「被移除的分区名」：
// Note 的 v1 顺序是 `材料提炼 / Agent 分析 / 用户补充 / 存疑与待验证 / 产出知识卡`，
// v2 把 `用户补充` 挪到了末位。于是同一份 v1 存量笔记按 v2 顺序表校验时，
// `用户补充`（v2 rank 3）会出现在 `存疑与待验证`（v2 rank 2）之前——
// 那不是「文件坏了」，而是「用的是旧模板」。没有这张表，
// `ParseNote` 会对**每一份**存量笔记报「固定分区顺序颠倒」，
// 索引构建 / 查询 / `eg check` 全线打死，字节兼容底线也就无从谈起。
func V1Sections(kind Kind) []string {
	switch kind {
	case KindCard:
		return []string{SecKnowledge, SecRationale, SecBoundary, SecUserAppend, SecSelfCheck}
	case KindNote:
		return []string{SecDigest, SecAgentReview, SecUserAppend, SecOpenQuest, SecOutputCards}
	default:
		return nil
	}
}

// v1RequiredSection 返回 v1 模板下该类型的必需分区。
//
// 与 v2 的差别只在 Note：v1 的落点是 `材料提炼`，v2 是 `整理正文`（契约 §3.2）。
func v1RequiredSection(kind Kind) string {
	switch kind {
	case KindCard:
		return SecKnowledge
	case KindNote:
		return SecDigest
	default:
		return RequiredSection(kind)
	}
}

// SchemaVersion 是一份文档所用的**分区模板**版本（不是 ChangePlan 的 `plan_version`）。
type SchemaVersion int

// 分区模板版本。
const (
	// SchemaV1 是 v1 五分区模板（存量文件）。
	SchemaV1 SchemaVersion = 1
	// SchemaV2 是 Schema v2 模板：Knowledge 三分区、Note 四分区（契约 §3.2）。
	SchemaV2 SchemaVersion = 2
)

// SectionSchema 判定这份文档按哪一版模板校验：正文里出现任一 v1 专有分区即判 v1。
//
// 判据只看**分区名**、不看 frontmatter：模板版本是正文结构的属性，
// 而 v1 存量文件的 frontmatter 里并没有版本字段，补写一个就等于改字节
// （存量兼容底线要求字节零变化）。
//
// 判据是「v1 **专有**分区」而非「任一 v1 分区」：两版模板共有 `用户补充` 等分区名，
// 用共有名判定会把每一份 v2 文件也误判成 v1。
//
// 新建产物永不含 v1 专有分区（它们不在 KnownSections、也不在 AutoWritableSections），
// 因此这条判据不会把新文件拖回 v1 口径；迁移（T-…-009）把存量分区收口后，
// 对应文件自然转判 v2。
func (d *Doc) SectionSchema(kind Kind) SchemaVersion {
	legacy := map[string]bool{}
	for _, n := range LegacyV1Sections(kind) {
		legacy[n] = true
	}
	if len(legacy) == 0 {
		return SchemaV2
	}
	for _, s := range d.Sections {
		if legacy[s.Name] {
			return SchemaV1
		}
	}
	return SchemaV2
}

// 观点固定五分区（顺序固定；Schema v2 契约 §3.2）。
//
// 「用户补充」与知识卡共用同一个常量：它在任何实体上都是同一条安全底线（B2），
// 复制一份新常量只会让 NeverWriteSections 需要按类型分支。
const (
	SecOpinionClaim = "观点"
	SecArgument     = "论据与推理"
	SecCounter      = "条件与反例"
	SecToVerify     = "待验证"
)

// CardSections 是知识卡的固定三分区，顺序固定（F5 + 契约 §3.2 / D-7）。
//
// v1 的五分区收敛为三：`解释与依据` 与 `理解自检` 不再是固定分区
// （职责分别移交 Opinion 的 `论据与推理` 与「是否忠实整理自 Note」的机械检查）。
// 稳定知识不需要 Agent 自证，也不需要为「稳定」再写一段依据。
func CardSections() []string {
	return []string{SecKnowledge, SecBoundary, SecUserAppend}
}

// OpinionSections 是观点的固定五分区，顺序固定（契约 §3.2）。
//
// 与知识卡的差别不在数量而在职责：`论据与推理` 承接论证，
// `条件与反例` 记「在什么条件下成立、已知反例是什么」，
// `待验证` 记「还需要什么证据才能定论」——后两段是观点独有，
// 稳定知识不需要回答「还缺什么证据」。
func OpinionSections() []string {
	return []string{SecOpinionClaim, SecArgument, SecCounter, SecToVerify, SecUserAppend}
}

// NoteSections 是材料笔记的固定四分区，顺序固定（F5 + 契约 §3.2）。
//
// v1 的五分区合并为四：`材料提炼` + `Agent 分析` → `整理正文`，
// `产出知识卡` → `提取结果`。材料笔记**没有**「理解自检」——它在 v1 就是知识卡独有
// （EG-NOTE-01），v2 起知识卡也不再有。
//
// 「用户补充」在本模板里排**末位**（v1 排第三）：四张模板从此一致以它收尾，
// 使「用户的字永远在文件末尾、永不被 CLI 触碰」这条安全底线在版式上也成立。
func NoteSections() []string {
	return []string{SecNoteBody, SecExtraction, SecOpenQuest, SecUserAppend}
}

// KnownSections 返回该类型的固定分区名。
func KnownSections(kind Kind) []string {
	switch kind {
	case KindCard:
		return CardSections()
	case KindOpinion:
		return OpinionSections()
	case KindNote:
		return NoteSections()
	default:
		return nil
	}
}

// RequiredSection 返回该类型**必须存在**的分区：知识卡「知识内容」必写；
// 观点「观点」必写——没有主张就不成其为观点；
// 材料笔记「整理正文」必写——Note 是整理版文章，没有正文就只剩一份清单
// （契约 §2.1：Note 不是 summary、也不是 manifest）。分区改名会导致这一必需分区缺失，
// 从而在 ValidateSections 处报错。
func RequiredSection(kind Kind) string {
	switch kind {
	case KindCard:
		return SecKnowledge
	case KindOpinion:
		return SecOpinionClaim
	case KindNote:
		return SecNoteBody
	default:
		return ""
	}
}

// NeverWriteSections 是**任何时候都不得写入**的分区（安全底线 B2 / §4.2）。
// 对知识卡、观点、材料笔记一致生效：三张模板都以「用户补充」收尾。
func NeverWriteSections() []string { return []string{SecUserAppend} }

// AutoWritableSections 返回对**已有**产物允许自动追加的分区（§4.2 / 契约 §3.3）。
//
// 知识卡：v2 起只剩「条件与边界」一格。收窄不是本次新加的限制，而是模板收敛的算术结果——
// 「解释与依据」「理解自检」已不是固定分区（D-7），「知识内容」对已有卡只读
// （矩阵 #12 的 🔴 子情形），「用户补充」永不写（B2）。
// 观点：只允许「论据与推理」「条件与反例」「待验证」——论证与反例是可以持续补充的；
// 「观点」本身沿用「知识内容」的口径：创建时写定，已有观点不得由自动路径改写主张。
// 材料笔记：允许「整理正文」「提取结果」「存疑与待验证」。
func AutoWritableSections(kind Kind) []string {
	switch kind {
	case KindCard:
		return []string{SecBoundary}
	case KindOpinion:
		return []string{SecArgument, SecCounter, SecToVerify}
	case KindNote:
		return []string{SecNoteBody, SecExtraction, SecOpenQuest}
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
		return fmt.Sprintf("%s 分区结构错误：%s；期望分区「%s」，位置 %s；固定分区名称逐字固定、顺序固定（F5）",
			e.Kind, e.Reason, e.Expected, loc)
	}
	return fmt.Sprintf("%s 分区结构错误：%s；期望分区「%s」，实际读到「%s」，位置 %s；"+
		"固定分区名称逐字固定、顺序固定（F5）", e.Kind, e.Reason, e.Expected, e.Got, loc)
}

// ValidateSections 校验固定分区的存在性与顺序。
//
// 判定口径：
//   - 必需分区缺失 → 报错（分区改名会走到这里：改名后固定名找不到）；
//   - 固定分区重复 → 报错；
//   - 固定分区之间顺序颠倒 → 报错；
//   - 其余 H2（模板之外 / 用户自建）→ **不报错**，由 UnknownSections 返回供报告记 info。
//
// 顺序表按 SectionSchema 的判定分流：v1 存量文件按 V1Sections 校验，
// 其余按 KnownSections（v2）。分流只影响**结构校验**，不影响 UnknownSections——
// 存量文件里被移除的分区照旧算「非固定分区」，只记 info、原样保留、字节不变
// （契约 §3.2 + T-…-003 的存量兼容底线）。这两处口径必须不同：
// 若 UnknownSections 也跟着分流，v1 分区就会变成「已知分区」，
// 报告里那条「命中被移除分区、待迁移」的 info 会整批消失，迁移就失去了清单。
func (d *Doc) ValidateSections(kind Kind) error {
	known := KnownSections(kind)
	required := RequiredSection(kind)
	if d.SectionSchema(kind) == SchemaV1 {
		known = V1Sections(kind)
		required = v1RequiredSection(kind)
	}
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
	if req := required; req != "" {
		if _, ok := seen[req]; !ok {
			got, off := d.firstUnknownSection(kind)
			return &SectionError{Kind: kind, Expected: req, Got: got,
				Offset: off, Line: lineNumber(d.Raw, off),
				Reason: fmt.Sprintf("必需分区缺失（分区名被改写即等于重切正文；固定分区为 %s）",
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

// UnknownSections 返回不属于固定分区的 H2：原样保留，只记 info。
//
// 两类成员：用户自建分区，以及 v1 存量分区（LegacyV1Sections）。二者的处置口径完全相同
// ——不报错、不删除、不重排、不写入其中——因此本函数不区分它们。
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
