package plan

// Opinion 写口的校验与展开（Schema v2 契约 §4.4 / §4.5 / §3.3 / §3.4）。
//
// # 为什么 Opinion 不复用 createKnowledge / appendKnowledge
//
// 两者的字段表、必写分区、可追加分区与 frontmatter 键集合各不相同：
// Knowledge 必写「知识内容」且不认 `validation`，Opinion 必写「观点」且是
// `validation` 的唯一持有者。用一个函数加 `if kind == …` 分叉，等于把两套判据
// 交织进同一条控制流——最先出问题的地方是「Opinion 缺观点时报的却是缺知识内容」。
//
// # 共用的部分**确实**共用
//
// 分区载荷展开（sectionPayloads）、领域落位（opDomain）、ID 声明去重（declare）、
// base 覆盖（baseCheck）、矩阵门闸（matrixGate）全部复用既有实现，一行未抄。
// Opinion 与 Knowledge 在这些维度上本来就是同一套规则，抄一份才是真的分叉。

import (
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// createOpinion 校验并展开 create_opinion（新建 `o-*`，`validation: pending`）。
//
// 五道判据，次序不可调：
//
//	① title 必给（H1 与 slug 来源，同 create_knowledge）
//	② sources[] 必给且非空 —— 观点也是从材料里长出来的，凭空的判断没有讨论的起点
//	③ validation 若显式给出，只允许 pending：创建即验证等于绕过用户授权（§4.5）
//	④ 「观点」必写 —— 没有主张就不成其为观点（§4.5 逐字）
//	⑤ 领域可定位、ID 不重复
func (v *validator) createOpinion(op *Op) {
	if op.Title == "" {
		v.add(errorAt(E5, op.Index, opPath(op.Index, "title"),
			"create_opinion 缺 title：观点标题是 H1 与 slug 来源"))
		return
	}
	// 观点与知识同源同据：EG-SRC-04 的「每次知识加工必须有可回读文章作依据」
	// 对观点同样成立，且更强——一个没有材料出处的判断连反驳都无处着手。
	if !op.SourcesGiven || len(op.Sources) == 0 {
		v.add(errorAt(E5, op.Index, opPath(op.Index, "sources"),
			"create_opinion 缺 sources[]（或为空数组）：观点也必须有可回读文章作依据，"+
				"否则它既无法被验证也无法被反驳"))
		return
	}
	if !v.opinionCreateValidation(op) {
		return
	}
	domain := v.opDomain(op)
	id := op.OpinionID
	if id != "" {
		if _, err := model.ParseOpinionID(id); err != nil {
			v.add(errorAt(E1, op.Index, opPath(op.Index, "opinion_id"),
				"opinion_id 格式非法：%v", err))
			return
		}
	}
	// 新建时可写四个分区：`用户补充` 永不写（矩阵新增行，两格同 🔴）。
	sections := v.sectionPayloads(op, store.KindOpinion, []string{store.SecOpinionClaim,
		store.SecArgument, store.SecCounter, store.SecToVerify})
	if !hasSection(sections, store.SecOpinionClaim) {
		v.add(errorAt(E2, op.Index, opPath(op.Index, "sections"),
			"create_opinion 缺「%s」：观点没有主张就不成立", store.SecOpinionClaim))
		return
	}
	refs := v.materialRefs(op)
	if v.res.Failed() {
		return
	}
	if domain == "" {
		v.add(errorAt(E5, op.Index, opPath(op.Index, "domain"),
			"无法确定落位领域：plan.domain 与 default_domain 都未给出（CLI 绝不自选领域）"))
		return
	}
	if id == "" {
		id = v.opinionTargetID(op)
	}
	act := Action{Kind: ActOpinionNew, OpIndex: op.Index, Op: op, ID: id, Domain: domain,
		Path: store.OpinionRel(domain, id), Sections: sections}
	if len(refs) > 0 {
		act.Material = &refs[0]
	}
	if !v.declare(op, "opinion_id", id, act.Path) {
		return
	}
	v.res.Actions = append(v.res.Actions, act)
}

// opinionTargetID 算出 `create_opinion` 的落点 ID：显式 `opinion_id` 优先，否则由
// 日期 + title 派生（与 createKnowledge 的口径同构）。
//
// 单列成函数是因为它有**两个**调用方：展开时要用它定落点，`提取结果` 的预扫
// （planOpinionStates）也要用它认出「本 plan 内将新建的观点」。两处各写一遍派生规则，
// 第一个后果就是「plan 里不写 opinion_id 时清单行拿不到验证状态」这种只在特定写法下
// 复现的缺陷。
func (v *validator) opinionTargetID(op *Op) string {
	if op.OpinionID != "" {
		return op.OpinionID
	}
	if op.Title == "" {
		return "" // 缺 title 的 op 已被 createOpinion 判 E5，此处不再重复发声。
	}
	return string(model.NewOpinionID(v.date(""), op.Title))
}

// planOpinionStates 预扫 ops[]，登记本 plan 内将新建的观点 → 新建时的验证状态。
//
// 只登记 `create_opinion`：`append_opinion` 的目标是**已有**观点（其状态的真源在盘上，
// 且 append 不得改写 validation，见 appendOpinion 的 E6 分支），把它也登进来就会用
// 「新建默认值」覆盖盘上真值。
//
// 本函数**不产出任何诊断**：它在 ops 循环之前跑，此刻还没做过任何字段校验，
// 在这里发声会让同一个问题被报两遍（一遍来自预扫、一遍来自 createOpinion 的判据），
// 且预扫看不到上下文，措辞必然更差。
func (v *validator) planOpinionStates() map[string]model.Validation {
	out := map[string]model.Validation{}
	for _, op := range v.p.Ops {
		if op == nil || op.Name != OpCreateOpinion {
			continue
		}
		if id := v.opinionTargetID(op); id != "" {
			out[id] = model.ValidationPending
		}
	}
	return out
}

// opinionCreateValidation 落地 §4.5 的「不得在 plan 里直接设 validated/rejected」。
//
// 缺键 → 默认 pending（由 writer 写出）；显式 `pending` → 允许（冗余但无害，
// 它与默认值一致）；`validated` / `rejected` → E2；其余取值 → E2（枚举越界）。
//
// 为什么是 error 而不是「照写 + 进报告」：本仓的宽松口径针对的是「数据仍可解析、
// 关系仍可定位」的瑕疵。而「创建即已验证」不是瑕疵，是**权限**问题——
// §6.3 把 validation 的流转限定在用户显式路径，plan 是 Agent 自动路径的载体。
// 放过它等于让 Agent 自己给自己的判断盖章，这与「Agent 可提不可执」同类。
func (v *validator) opinionCreateValidation(op *Op) bool {
	if !op.ValidationGiven {
		return true
	}
	got, err := model.ParseValidation(op.Validation)
	if err != nil {
		v.add(errorAt(E2, op.Index, opPath(op.Index, "validation"),
			"validation 取值不成立：%v", err))
		return false
	}
	if got != model.ValidationPending {
		v.add(errorAt(E2, op.Index, opPath(op.Index, "validation"),
			"create_opinion 不得把 validation 直接设为 %s：创建即验证等于绕过用户授权"+
				"（契约 §4.5 / §6.3——验证状态只能由用户显式路径流转）；"+
				"新建观点恒为 %s，验证请走 eg opinion validate / reject",
			got, model.ValidationPending))
		return false
	}
	v.add(infoAt(op.Index, opPath(op.Index, "validation"),
		"validation: %s 与新建默认值相同，可省略", model.ValidationPending))
	return true
}

// appendOpinion 校验并展开 append_opinion（对已有观点只追加三分区）。
//
// 可追加分区恰 `论据与推理` / `条件与反例` / `待验证`（§3.3 / §4.5）：
// `观点` 沿用「知识内容」的口径（已有观点的主张不得由自动路径改写——
// 改写主张不是补充论据，而是换一个观点），`用户补充` 永不写。
func (v *validator) appendOpinion(op *Op) {
	rel, ok := v.opinionTarget(op)
	if !ok {
		return
	}
	// 「观点」分区与矩阵的条件解锁行同构：目标恒为已有观点，故 P-A 侧恒取 🔴 子情形。
	// 这一判定必须在 sectionPayloads 之前发声——只有这里点得出矩阵行号与两列取值。
	if _, wants := op.Sections[store.SecOpinionClaim]; wants && !v.opinionClaimGate(op) {
		return
	}
	sections := v.sectionPayloads(op, store.KindOpinion,
		store.AutoWritableSections(store.KindOpinion))
	if v.res.Failed() {
		return
	}
	// 剩下三个分区逐个查表：今天全是 ✅，翻一格立刻被拦。
	if !v.opinionSectionGate(op) {
		return
	}
	if len(sections) == 0 {
		v.add(errorAt(E5, op.Index, opPath(op.Index, "sections"),
			"append_opinion 缺 sections：至少要追加一个分区（%v）",
			store.AutoWritableSections(store.KindOpinion)))
		return
	}
	if op.ValidationGiven {
		v.add(errorAt(E6, op.Index, opPath(op.Index, "validation"),
			"append_opinion 不得改写 validation：验证状态只能由用户显式路径流转"+
				"（契约 §6.3）；追加论据与改判结论是两件事"))
		return
	}
	act := Action{Kind: ActOpinionAppend, OpIndex: op.Index, Op: op, ID: op.Opinion,
		Domain: store.DomainOf(rel), Path: rel, Sections: sections}
	v.baseCheck(op, op.Opinion, rel, &act)
	v.res.Actions = append(v.res.Actions, act)
}

// opinionTarget 解析 append_opinion 的目标观点：ID 形态 + 全库可定位 + frontmatter 可解析。
func (v *validator) opinionTarget(op *Op) (string, bool) {
	if op.Opinion == "" {
		v.add(errorAt(E2, op.Index, opPath(op.Index, "opinion"),
			"append_opinion 缺 opinion：追加的落点必须是已有观点"))
		return "", false
	}
	if _, err := model.ParseOpinionID(op.Opinion); err != nil {
		v.add(errorAt(E2, op.Index, opPath(op.Index, "opinion"), "opinion 无法解析：%v", err))
		return "", false
	}
	rel, ok := v.resolve(op.Opinion)
	if !ok {
		v.add(errorAt(E2, op.Index, opPath(op.Index, "opinion"),
			"opinion %s 不存在于全库：无法定位落点", op.Opinion))
		return "", false
	}
	if !v.frontmatterCheck(op, "opinion", rel) {
		return "", false
	}
	v.domainCheck(op, "opinion", rel)
	return rel, true
}
