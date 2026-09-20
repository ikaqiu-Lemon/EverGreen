package plan

// `write_note` 的 v2 校验（有序 `blocks[]`）与 v1 兼容映射，以及 `W21` 结构覆盖诊断
// （Schema v2 契约 §4.2 / §4.3 / §4.1）。
//
// # 三条路径，互不重叠
//
//	plan_version: 2 + blocks[]     → noteBlockWrites：有序块渲染成「整理正文」
//	plan_version: 1 + sections{}   → legacyNoteWrites：v1 分区名按固定映射落到 v2 分区
//	两者同时给出                    → E2，零写入
//
// 为什么不允许同时给：`blocks[]` 与 `sections{}` 都会写「整理正文」，两者同时生效
// 就必须定义谁先谁后、以及块顺序如何与固定分区顺序交织。任何一种定义都是凭空发明的
// 第三套语义，而 Note 的全部价值在于「顺序忠实于原文」（契约 §2.1）——
// 用一条 `E2` 让写 plan 的人自己选一种口径，比让工具替他猜要诚实得多。

import (
	"fmt"
	"math"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// NoteExtraction 是 Note「提取结果」分区的两组清单（契约 §5.1）。
//
// **类型别名**而不是新结构体，理由同 NoteBlock：渲染的唯一实现在 store，
// 在此再定义一份同形结构体就必须写一个逐字段拷贝的转换函数，而那正是两处漂移的起点。
// 分组本身只发生在 plan 侧（noteExtraction）——它会产出 I1 诊断，store 不产出诊断。
type NoteExtraction = store.NoteExtraction

// v1LegacyNoteSectionMap 是 v1 `write_note.sections` 键 → v2 分区的**固定映射**
// （契约 §4.1「v1 走旧口径」在 v2 模板下的唯一落地方式）。
//
// 映射本体就是 D-7 / §3.2 的两处分区合并（不是 convergence[] 那个「收敛」，
// 这里刻意不写那个词加全角冒号的组合：CLI 侧有一条 grep 反证，要求逐卡收敛结论的
// 渲染字面量在 internal/ 下只出现在 internal/cli/convergence.go 一处）：
//
//	材料提炼      → 整理正文     （v1 的加工产出落点即 v2 的正文）
//	Agent 分析    → 整理正文     （紧随「材料提炼」之后，保持 v1 的固定分区顺序）
//	存疑与待验证  → 存疑与待验证 （分区名未变）
//	产出知识卡    → 提取结果     （更名扩展：v2 同时列 Knowledge 与 Opinion）
//
// # 为什么不给 v1 的「Agent 分析」加 `[Agent 补充]` 标记
//
// 加标记看起来更贴近 v2 的语义，但那是在**替 plan 作者声明来源归属**。
// v1 的 `sections` 里没有 `role` 字段，「这段是 Agent 写的」只是分区名的暗示，
// 不是作者的显式声明；把暗示落成字节里的显式标记，等于工具替人认领了一段话的作者。
// 兼容期的正确行为是：字节逐字搬过去，顺序按 v1 固定分区序，把「谁写的」留给迁移
// （T-…-009）时由人显式声明。
func v1LegacyNoteSectionMap() map[string]string {
	return map[string]string{
		store.SecDigest:      store.SecNoteBody,
		store.SecAgentReview: store.SecNoteBody,
		store.SecOpenQuest:   store.SecOpenQuest,
		store.SecOutputCards: store.SecExtraction,
	}
}

// v1LegacyNoteOrder 是 v1 固定分区的**声明顺序**（合并到同一个 v2 分区时的先后由它决定）。
//
// 逐字取 v1 的 `NoteSections()`：`材料提炼` / `Agent 分析` / `用户补充` /
// `存疑与待验证` / `产出知识卡`。这里只列可写的四个——`用户补充` 永不写（B2），
// 它由 sectionPayloads 的 E6 分支单独拦，不参与顺序。
func v1LegacyNoteOrder() []string {
	return []string{store.SecDigest, store.SecAgentReview,
		store.SecOpenQuest, store.SecOutputCards}
}

// noteWrites 按 plan 版本与字段形态选出唯一一条写入口径，并返回分区写入序列。
//
// 返回的 ok 为 false 表示已登记 error、整条 op 不执行（零写入）。
func (v *validator) noteWrites(op *Op) ([]SectionWrite, bool) {
	hasSections := len(op.Sections) > 0
	switch {
	case op.BlocksGiven && hasSections:
		v.add(errorAt(E2, op.Index, opPath(op.Index, "blocks"),
			"blocks[] 与 sections{} 互斥（契约 §4.2 第 6 条）：两者都会写「%s」，"+
				"同时给出就必须凭空定义交织顺序，而 Note 的价值恰在顺序忠实于原文；"+
				"请二选一（v2 用 blocks[]，兼容期的 v1 plan 用 sections{}）",
			store.SecNoteBody))
		return nil, false
	case op.BlocksGiven:
		return v.noteBlockWrites(op)
	case v.p.Version == PlanVersionV1 && hasSections:
		return v.legacyNoteWrites(op), true
	case hasSections:
		// v2 plan 仍在用 sections{}：不是错误（顶层键与字段表都还认它），
		// 但要如实说明它走的是兼容映射，否则作者会以为自己写的是 v2 口径。
		v.add(infoAt(op.Index, opPath(op.Index, "sections"),
			"plan_version=%d 的 write_note 仍在用 v1 的 sections{}：已按固定映射落到 v2 分区；"+
				"v2 的写法是有序 blocks[]（契约 §4.2）", v.p.Version))
		return v.legacyNoteWrites(op), true
	default:
		return nil, true
	}
}

// legacyNoteWrites 把 v1 的固定分区 payload 映射到 v2 分区。
//
// 合并语义：`材料提炼` 与 `Agent 分析` 都落到「整理正文」，按 v1 固定分区顺序前后相接，
// 中间隔一个空行（store 转发的 mdfile.SplitBlocks 口径里的块分隔符——不隔开会把两段粘成一个块，
// 日后 `replace_block` 就无法只替换其中一段）。
func (v *validator) legacyNoteWrites(op *Op) []SectionWrite {
	mapping := v1LegacyNoteSectionMap()
	writable := set(store.AutoWritableSections(store.KindNote))
	merged := map[string][]byte{}
	var order []string
	for _, legacy := range v1LegacyNoteOrder() {
		payload, ok := op.Sections[legacy]
		if !ok {
			continue
		}
		target := mapping[legacy]
		if !writable[target] {
			v.add(errorAt(E6, op.Index, opPath(op.Index, "sections."+legacy),
				"「%s」映射到的 v2 分区「%s」对自动路径只读：本 op 只允许写 %v",
				legacy, target, store.AutoWritableSections(store.KindNote)))
			continue
		}
		if len(payload) == 0 {
			v.add(infoAt(op.Index, opPath(op.Index, "sections."+legacy),
				"分区载荷为空，已跳过该分区"))
			continue
		}
		if _, seen := merged[target]; !seen {
			order = append(order, target)
		} else {
			merged[target] = append(merged[target], '\n')
		}
		merged[target] = append(merged[target], lineTerminated(payload)...)
		if target != legacy {
			v.add(infoAt(op.Index, opPath(op.Index, "sections."+legacy),
				"v1 分区「%s」已按固定映射写入 v2 分区「%s」（契约 §4.1 兼容期；字节逐字不变）",
				legacy, target))
		}
	}
	// 「用户补充」与未知分区仍要照 v1 口径发声：前者 E6（B2 安全底线），后者 I1。
	v.legacyNoteLeftovers(op, mapping)
	out := make([]SectionWrite, 0, len(order))
	for _, name := range order {
		out = append(out, SectionWrite{Section: name, Payload: merged[name]})
	}
	return out
}

// legacyNoteLeftovers 对映射表之外的 v1 分区键发声：`用户补充` 判 E6，其余记 I1。
func (v *validator) legacyNoteLeftovers(op *Op, mapping map[string]string) {
	for name := range op.Sections {
		switch {
		case name == store.SecUserAppend:
			v.add(errorAt(E6, op.Index, opPath(op.Index, "sections.用户补充"),
				"「用户补充」任何时候都不得写入（安全底线 B2）：目标文件字节不变"))
		case mapping[name] != "":
			continue
		case set(store.KnownSections(store.KindNote))[name]:
			// v2 的固定分区名直接写在 v1 形态里：允许，按同名落位。
			continue
		default:
			v.add(infoAt(op.Index, opPath(op.Index, "sections."+name),
				"未知分区名已原样忽略（%s 的固定分区是 %v，v1 存量分区是 %v）",
				store.KindNote, store.KnownSections(store.KindNote),
				store.LegacyV1Sections(store.KindNote)))
		}
	}
}

// noteBlockWrites 校验并渲染 v2 的有序块（契约 §4.2 规则 1/2/3/7）。
func (v *validator) noteBlockWrites(op *Op) ([]SectionWrite, bool) {
	if len(op.Blocks) == 0 {
		v.add(errorAt(E2, op.Index, opPath(op.Index, "blocks"),
			"blocks[] 为空：Note 必须有来源内容（契约 §4.2 第 7 条）——"+
				"一篇没有正文的 Note 不是整理版文章，只是一个空壳"))
		return nil, false
	}
	sourceBlocks := 0
	for i, b := range op.Blocks {
		if !b.Role.Valid() {
			v.add(errorAt(E2, op.Index, blockPath(op.Index, i, "role"),
				"role=%q 不在封闭二值枚举内 %v：来源内容与 Agent 推导必须可区分，"+
					"第三种取值等于放弃这条区分", b.Role, NoteBlockRoles()))
			return nil, false
		}
		if b.Role == NoteBlockSource {
			sourceBlocks++
		}
	}
	if sourceBlocks == 0 {
		v.add(errorAt(E2, op.Index, opPath(op.Index, "blocks"),
			"blocks[] 全为 role: %s：Note 必须有来源内容（契约 §4.2 第 7 条）——"+
				"通篇只有 Agent 推导的文件是分析笔记，不是原文的整理版",
			NoteBlockAgent))
		return nil, false
	}
	body, err := store.NoteBlockBytes(op.Blocks)
	if err != nil {
		v.add(errorAt(E2, op.Index, opPath(op.Index, "blocks"), "blocks[] 不成立：%v", err))
		return nil, false
	}
	// v2 加严（契约 §4.2 第 4/5 条）：source_ref + omissions + Source 快照覆盖校验。
	// 只作用于当前版本的 plan；兼容期 v1 plan 即便用了 blocks[] 也走旧的 W21-only 口径，
	// 不被新校验波及（v1 sections / v2 sections 的兼容路径同样一字节不变）。
	if v.p.Version == PlanVersion {
		snap, ok := v.noteSourceValidate(op)
		if !ok {
			return nil, false
		}
		v.coverageDiagnosisRaw(op, sourceBlocks, snap.raw)
		return []SectionWrite{{Section: store.SecNoteBody, Payload: body}}, true
	}
	v.coverageDiagnosis(op, sourceBlocks)
	return []SectionWrite{{Section: store.SecNoteBody, Payload: body}}, true
}

// coverageDiagnosis 登记 `W21` 结构覆盖诊断（契约 §4.3 算法逐字）。
//
//	src_anchors        = Source 正文里的 H2 + H3 标题数（去空标题）
//	note_source_blocks = blocks[] 中 role == source 的块数
//	src_anchors >= 3 且 note_source_blocks < ceil(src_anchors / 2) → W21
//
// `src_anchors < 3` 不判：短文与无标题原文没有可比的结构，对它们报覆盖不足是误报。
// 读不到 Source 也不判——诊断的前提是拿到了原文结构，猜一个数字比不报更糟。
func (v *validator) coverageDiagnosis(op *Op, sourceBlocks int) {
	rel, ok := v.resolve(op.Source)
	if !ok {
		return
	}
	raw, ok := v.readExisting(rel)
	if !ok {
		return
	}
	v.coverageDiagnosisRaw(op, sourceBlocks, raw)
}

// coverageDiagnosisRaw 在**已取到**的 Source 字节上登记 W21（v2 路径复用 source_ref
// 覆盖校验读过的同一份快照，避免同一校验对同一原文两次读取、在并发落盘下取到漂移的字节）。
func (v *validator) coverageDiagnosisRaw(op *Op, sourceBlocks int, raw []byte) {
	anchors := store.CountBodyAnchors(raw)
	if anchors < CoverageAnchorFloor {
		return
	}
	want := int(math.Ceil(float64(anchors) / 2))
	if sourceBlocks >= want {
		return
	}
	d := warnAt(W21, op.Index, opPath(op.Index, "blocks"),
		"整理正文的来源块数 %d 显著少于原文章节数 %d：疑似退化为摘要"+
			"（阈值 ceil(%d/2)=%d）。Note 应保持原文的章节顺序与论证顺序，"+
			"而不是把文章压成几条要点；本诊断恒为 warning，--strict 下也不升级为 error，"+
			"照常写入（契约 §4.3 / D-6）",
		sourceBlocks, anchors, anchors, want)
	d.Target = op.Source
	v.add(d)
}

// CoverageAnchorFloor 是 `W21` 的判定门槛：原文标题数少于它时**不判**（契约 §4.3）。
const CoverageAnchorFloor = 3

// CoverageThreshold 复算某个原文标题数对应的最少来源块数（`ceil(anchors/2)`）。
//
// 单列成导出函数：诊断文案、迁移工具（T-…-009 要判「迁移后不再触发 W21」）与用例
// 都读同一份算法。三处各写一遍 `ceil` 必然出现「阈值差一」的分歧。
func CoverageThreshold(anchors int) int {
	if anchors < CoverageAnchorFloor {
		return 0
	}
	return int(math.Ceil(float64(anchors) / 2))
}

// noteExtraction 把 `output_cards` 按 ID 前缀拆成 Knowledge / Opinion 两组
// （契约 §5.1「提取结果」两个 H3 小节）。
//
// 分组只看 ID 前缀（`k-` / `o-`），不看 mode、不看顺序、不做任何推断：
// 前缀是类型的唯一真源（§3.1），据它分组才不会与目录布局对不上。
// 前缀既非 `k-` 也非 `o-` 的条目归入 Knowledge 一组并记一条 I1——
// 静默丢弃会让「产出了什么」在笔记里消失。
//
// Opinion 行额外带 `[<validation>]` 标记（§5.1）：读者在笔记里就能看出这条判断
// 当时论证到哪一步，而不必逐个点开 `o-*`。Knowledge 行**不带**——`validation` 是
// Opinion 独有的 frontmatter 键（§3.4），给知识卡也盖一个标记等于把「论证进度」
// 扩散到不持有它的实体上。
func (v *validator) noteExtraction(op *Op) *NoteExtraction {
	var knowledge, opinions []string
	for i, c := range op.OutputCards {
		if c.Card == "" {
			continue
		}
		item := c.Card
		if c.Mode != "" {
			item = fmt.Sprintf("%s（%s）", c.Card, c.Mode)
		}
		path := fmt.Sprintf("ops[%d].output_cards[%d].card", op.Index, i)
		switch {
		case hasIDPrefix(c.Card, model.PrefixOpinion):
			opinions = append(opinions, v.opinionItem(op, path, c.Card, item))
		case hasIDPrefix(c.Card, model.PrefixCard):
			knowledge = append(knowledge, item)
		default:
			v.add(infoAt(op.Index, path,
				"%q 既不是 %s 也不是 %s 前缀：已归入「%s」组照常列出（类型只由 ID 前缀表达，§3.1）",
				c.Card, model.PrefixCard, model.PrefixOpinion, store.ExtractionKnowledgeHeading))
			knowledge = append(knowledge, item)
		}
	}
	return &NoteExtraction{Knowledge: knowledge, Opinions: opinions}
}

// opinionItem 给一条 Opinion 清单项补上 `[<validation>]` 标记。
//
// 状态**只从真源取**（opinionValidation）：取不到时不补标记，并记一条 I1 点名该 ID。
// 为什么不退化成「取不到就写 pending」：那是在替读者断言「这条观点还没被验证」，
// 而事实是「这条观点此刻定位不到」。快照里的每个标记都会被后来的人当成当时的事实读，
// 凭默认值填出来的事实是伪造，且伪造之后再也无法与真的 pending 区分。
func (v *validator) opinionItem(op *Op, path, id, item string) string {
	val, ok := v.opinionValidation(id)
	if !ok {
		v.add(infoAt(op.Index, path,
			"观点 %s 既不在本 plan 内新建、也无法从全库读到 %s：「%s」照常列出该条目，"+
				"但**不补**验证状态标记（凭默认值填一个 %s 等于伪造当时的事实）；"+
				"悬空引用由 eg reconcile 的关系 / 结构检查负责检出",
			id, model.FMKeyValidation, store.SecExtraction, model.ValidationPending))
		return item
	}
	return item + " " + store.ExtractionValidationMark(val)
}

// opinionValidation 取一条观点在**本次加工时**的验证状态。
//
// 两个真源，次序不可换：
//
//	① 本 plan 内由 create_opinion 新建 → 新建默认值 pending（§4.5：创建即验证被 E2 拦在门外，
//	   因此这里不必也不应去看 op 里显式给的值——能通过校验的只有 pending）；
//	② 库里已有 `o-*` → 读盘上 frontmatter 的 `validation`（用户显式路径流转的结果，§6.3）。
//
// ① 必须在 ② 之前：本 plan 新建的 ID 若已在库里存在，declare 早已判 E1，
// 两个真源不可能同时命中；但顺序写反会让「先读盘、读不到再看 plan」在 ops 顺序为
// 「write_note 在 create_opinion 之前」时取不到值（新建产物此刻还没落盘）。
//
// 读不到、解析不了、或 `validation` 越界一律返回 false：本函数不猜、不补默认值。
func (v *validator) opinionValidation(id string) (model.Validation, bool) {
	if val, ok := v.planOpinions[id]; ok {
		return val, true
	}
	rel, ok := v.resolve(id)
	if !ok {
		return "", false
	}
	raw, ok := v.readExisting(rel)
	if !ok {
		return "", false
	}
	var fm struct {
		Validation string `yaml:"validation"`
	}
	if err := store.FrontmatterInto(raw, &fm); err != nil {
		return "", false
	}
	val, err := model.ParseValidation(fm.Validation)
	if err != nil {
		return "", false
	}
	return val, true
}

// hasIDPrefix 报告 ID 是否以某个前缀开头（前缀判定不用 ParseID：这里只分组，
// 形态合法性由各自的 Parse*ID 在专门的判据里负责，两件事不合并）。
func hasIDPrefix(id, prefix string) bool {
	return len(id) >= len(prefix) && id[:len(prefix)] == prefix
}
