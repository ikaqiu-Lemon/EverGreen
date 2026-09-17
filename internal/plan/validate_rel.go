package plan

// 两类关系的校验（合同 §3.5 / §3.6；技术方案 §1.2 F4、§6.1、§6.2）。
//
// 关系类型集合是**封闭**的：材料 `rel` 恰三值 `support` / `against` / `context`，
// 论证 `type` 恰四值 `derives` / `supports` / `limits` / `opposing`。
// 材料 `support` 与论证 `supports` 只差一个字母，写混即两组关系互串——这正是 E3 的立论基础：
// `s-` 写进 `relations[].target` → E3；`k-` 写进 `sources[].source` → E3，**代码级硬拦**。
// 关系只引用 ID、永不引用路径（F2），因此文件改名 / 移动不影响关系有效性。

import (
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/rules"
)

// materialRefs 校验一组材料关系四要素（create_card 自带的 sources[]）。
func (v *validator) materialRefs(op *Op) []MaterialRef {
	var out []MaterialRef
	for i, ref := range op.Sources {
		path := fmt.Sprintf("ops[%d].sources[%d]", op.Index, i)
		if v.materialRef(op, path, ref) {
			out = append(out, ref)
		}
	}
	return out
}

// materialRef 校验单条材料关系四要素：
// 四要素不全 / reason 为空或等于关系名本身 → W2（照写 + 进报告，S1 不拦）；
// `k-` 写进 source → E3；ID 无法解析 → E2；rel 取值出封闭三值 → E5（集合外一律拒绝）。
func (v *validator) materialRef(op *Op, path string, ref MaterialRef) bool {
	ok := true
	var missing []string
	if ref.Source == "" {
		missing = append(missing, "source")
	} else if model.CardID(ref.Source).Valid() {
		v.add(errorAt(E3, op.Index, path+".source",
			"sources[].source 只接受原文 ID（s-…），实际是知识卡 ID %s：ID 类型写混一律拒绝", ref.Source))
		ok = false
	} else if _, err := model.ParseSourceID(ref.Source); err != nil {
		v.add(errorAt(E2, op.Index, path+".source", "source 无法解析：%v", err))
		ok = false
	} else if _, found := v.resolve(ref.Source); !found {
		v.add(errorAt(E2, op.Index, path+".source", "source %s 不存在于全库：关系无法定位", ref.Source))
		ok = false
	}
	if ref.Note == "" {
		missing = append(missing, "note")
	} else if _, err := model.ParseNoteID(ref.Note); err != nil {
		v.add(errorAt(E2, op.Index, path+".note", "note 无法解析（四要素存笔记 ID、不存路径）：%v", err))
		ok = false
	} else if _, found := v.resolve(ref.Note); !found {
		v.add(errorAt(E2, op.Index, path+".note", "note %s 不存在于全库：关系无法定位", ref.Note))
		ok = false
	}
	if ref.Rel == "" {
		missing = append(missing, "rel")
	} else if _, err := model.ParseMaterialRel(ref.Rel); err != nil {
		v.add(errorAt(E5, op.Index, path+".rel",
			"rel=%q 不在材料关系封闭三值内：合法取值恰为 %v", ref.Rel, model.ValidMaterialRels()))
		ok = false
	}
	if ref.Reason == "" {
		missing = append(missing, "reason")
	} else if ref.Reason == ref.Rel {
		v.add(warnAt(W2, op.Index, path+".reason",
			"reason 等于关系名本身（%q），未说明理由；关系照常写入", ref.Reason))
	}
	if len(missing) > 0 {
		v.add(warnAt(W2, op.Index, path,
			"材料关系四要素不全（缺 %v）：关系照常写入 + 进报告（S5 起才严格化）", missing))
	}
	return ok
}

// materialRel 校验并展开 add_material_rel（写入知识卡 frontmatter 的 sources[]）。
func (v *validator) materialRel(op *Op) {
	rel, ok := v.cardTarget(op, "card", op.Card)
	if !ok {
		return
	}
	// 矩阵 #9「sources[]」两格均 ✅：Agent 自动路径可追加材料关系（允许目标为 deprecated 卡）。
	if !v.matrixGate(op, ObjectCard, FieldSources) {
		return
	}
	ref := MaterialRef{Source: op.Source, Note: op.Note, Rel: op.Rel, Reason: op.Reason}
	if !v.materialRef(op, opPath(op.Index, ""), ref) {
		return
	}
	act := Action{Kind: ActMaterialRel, OpIndex: op.Index, Op: op, ID: op.Card,
		Path: rel, Material: &ref}
	v.baseCheck(op, op.Card, rel, &act)
	v.res.Actions = append(v.res.Actions, act)
}

// relation 校验并展开 add_relation（写入来源卡 frontmatter 的 relations[]）。
func (v *validator) relation(op *Op) {
	if op.Target != "" && model.SourceID(op.Target).Valid() {
		v.add(errorAt(E3, op.Index, opPath(op.Index, "target"),
			"relations[].target 只连论证性产物（知识卡 k-… 或观点 o-…），实际是原文 ID %s：ID 类型写混一律拒绝", op.Target))
		return
	}
	if op.Type == "" {
		v.add(errorAt(E5, op.Index, opPath(op.Index, "type"),
			"add_relation 缺 type：合法取值恰为 %v", model.ValidRelationTypes()))
		return
	}
	relType, err := model.ParseRelationType(op.Type)
	if err != nil {
		v.add(errorAt(E5, op.Index, opPath(op.Index, "type"),
			"type=%q 不在论证关系封闭四值内：合法取值恰为 %v", op.Type, model.ValidRelationTypes()))
		return
	}
	// I-…-010：自环是**纯静态**的 plan 级语义约束（不读盘即可判定），必须与 E2/E3/E4 同阶段
	// 拦下并退 2。原实现只在 store 写入层用 ErrSelfRelation 拦，于是自环必然穿透到写入阶段，
	// 被折成「写入失败」→ 退 3 / status=partial，而事实是零写入零 commit：调用方会据此去做
	// 回滚 / 重放 / 对账等补偿，补偿本身成了新风险。store 层的 ErrSelfRelation 保留作纵深防御。
	//
	// 编号取 **E5**（「op 字段不成立」族，见合同 §2 表第 1 行 / §3 的 url|title 同缺 / question
	// 为空）：`from` 与 `target` 两个已解析字段的**取值组合**不成立。不用 E2 —— 两端 ID 都可解析
	// 且存在；也不自创编号（error 编号集合恰 E1–E10，封闭）。
	if op.From != "" && op.From == op.Target {
		v.add(errorAt(E5, op.Index, opPath(op.Index, "target"),
			"关系两端不得是同一张卡：from 与 target 同为 %s（自环无论证意义）；"+
				"本次零写入、无 commit", op.From))
		return
	}
	fromRel, ok := v.relationEndpoint(op, "from", op.From)
	if !ok {
		return
	}
	if _, ok := v.relationEndpoint(op, "target", op.Target); !ok {
		return
	}
	targetRel, _ := v.resolve(op.Target)
	// 矩阵 #10「relations[] 新增」两格均 ✅——与 #11「relations[] 删除」P-A 🔴 成对：
	// 加关系是可回退的增量，删关系不是，这正是 N-3 只约束删除侧的原因。
	if !v.matrixGate(op, ObjectCard, FieldRelationsAdd) {
		return
	}
	if op.Reason == "" || op.Reason == op.Type {
		v.add(warnAt(W2, op.Index, opPath(op.Index, "reason"),
			"论证关系的 reason 缺失或等于关系名本身：关系照常写入 + 进报告（S5 起才严格化）"))
	}
	// W3 自 M3 起真正判定（§4.5.1「S2 起判定、S5 起 error」）：两端任一不是 active
	// 或已被逻辑删除都记 warning，关系仍照写、退出码不受影响。
	v.relationEndW3(op, "from", op.From, fromRel)
	v.relationEndW3(op, "target", op.Target, targetRel)

	write := RelationWrite{Type: string(relType), Target: op.Target, Reason: op.Reason}
	id, rel := op.From, fromRel
	if relType == model.RelationOpposing {
		pair := rules.Opposing(model.RelationEndpoint(op.From), model.RelationEndpoint(op.Target))
		if pair.Normalized {
			d := warnAt(W8, op.Index, opPath(op.Index, "from"),
				"opposing 方向未规范化：已按字典序改为 from=%s / target=%s（单向存储，一对一条记录）",
				pair.From, pair.Target)
			d.Target = string(pair.Target)
			v.add(d)
			write.Target = string(pair.Target)
			id = string(pair.From)
			newRel, found := v.resolve(id)
			if !found {
				v.add(errorAt(E2, op.Index, opPath(op.Index, "from"),
					"规范化后的写入端 %s 不存在于全库：关系无法定位", id))
				return
			}
			rel = newRel
		}
		if v.opposingExists(id, rel, model.RelationEndpoint(write.Target)) {
			write.Duplicate = true
			d := warnAt(W8, op.Index, opPath(op.Index, "target"),
				"opposing 同对已存在：幂等跳过，不产生第二条（reason 的更新属 S2 块替换，"+
					"自动路径不做替换，B1）")
			d.Target = write.Target
			v.add(d)
		}
	}

	act := Action{Kind: ActRelation, OpIndex: op.Index, Op: op, ID: id, Path: rel,
		Relation: &write}
	v.baseCheck(op, id, rel, &act)
	v.res.Actions = append(v.res.Actions, act)
}

// opposingExists 报告该宿主的 relations[] 里是否已有同对 opposing（同对判定与方向无关）。
func (v *validator) opposingExists(id, rel string, target model.RelationEndpoint) bool {
	host, ok := v.endpointFacts(id, rel)
	if !ok {
		return false
	}
	return rules.OpposingDuplicate(host.Relations,
		rules.Opposing(model.RelationEndpoint(host.ID), target))
}

// relationEndpoint 解析并解析路径一个论证关系端点（from / target）。
//
// 论证关系是**跨类型**的：端点只接受知识卡（k-）或观点（o-）。与 cardTarget 的区别是
// 接受面从「只 k-」放宽到「k- / o-」，其余判定（缺失、无法解析、悬空、E4、W1 领域）逐一对齐。
// s-（原文）/ n-（笔记）/ r-（综述）/ p-（提案）以及不可解析 ID 一律拒绝——论证关系永远
// 发生在两条论证性产物之间。set_replaced_by 的宿主与指向端同样是跨类型端点（k-/o-），也走本
// 函数；仍是 card-only 的业务（deprecate/restore/undelete/delete/add_material_rel 等）走 cardTarget。
func (v *validator) relationEndpoint(op *Op, field, id string) (string, bool) {
	if id == "" {
		v.add(errorAt(E2, op.Index, opPath(op.Index, field), "%s 缺 %s：关系端点无法定位", op.Name, field))
		return "", false
	}
	if _, err := model.ParseRelationEndpoint(id); err != nil {
		v.add(errorAt(E2, op.Index, opPath(op.Index, field),
			"%s 无法解析为论证关系端点（只接受知识卡 %s 或观点 %s）：%v",
			field, model.PrefixCard, model.PrefixOpinion, err))
		return "", false
	}
	rel, ok := v.resolve(id)
	if !ok {
		v.add(errorAt(E2, op.Index, opPath(op.Index, field), "%s %s 不存在于全库：关系无法定位", field, id))
		return "", false
	}
	if !v.frontmatterCheck(op, field, rel) {
		return "", false
	}
	v.domainCheck(op, field, rel)
	return rel, true
}

// RelationStatusWarning 构造 W3 诊断（论证关系某端不是 `status: active`）。
//
// **W3 判定属 S2 起**（§4.5.1 原文「S2 起判定、S5 起 error」）：S1 无 `deprecate` 命令、
// 无失效卡，正常链路不产生该 warning。本函数只锁定它的分级与诊断形态，
// S1 主链路**不调用**它，也不实现 S2 的判定语义。
func RelationStatusWarning(opIndex int, field, target string, status model.Status) Diagnostic {
	d := warnAt(W3, opIndex, opPath(opIndex, field),
		"论证关系的一端 status=%s 不是 active：关系照写 + 进报告，不拦截（S2 起判定、S5 起 error）",
		status)
	d.Target = target
	return d
}

// StateOpWarning 构造 W7 诊断（状态类 op 的相关告警）。
//
// **S1 无状态类 op**（`deprecate` / `restore` / `set_replaced_by` / `delete` / `undelete`
// 等一律属 S2，未知 op 直接 E5），因此正常链路不产生 W7。本函数只保留分级与诊断形态。
func StateOpWarning(opIndex int, field, message string) Diagnostic {
	return warnAt(W7, opIndex, opPath(opIndex, field), "%s", message)
}
