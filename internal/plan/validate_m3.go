package plan

// M3 新增 8 个 op 的校验链（提案合同 §8.1 / §8.2 / §8.4 / §8.5.2；授权合同 §9 A-14 / A-15）。
//
// 本文件只做「校验 + 展开」，不写盘、不 commit（与 validate.go 同源口径）。
// 落盘形态在 internal/store（`remove_relation` → ApplyRemoveRelation，
// `replace_block` → ApplyReplaceBlock）；五个状态类 op 与 `mark_reviewed` 的
// frontmatter 改写属 T-…-039 / 041 / 042，本阶段只到「校验通过 + 如实说明未落盘」。
//
// **M4 · T-…-051 的一处增量（A-33）**：`mark_reviewed` 自 M4 起**不再**是校验-only ——
// 它按 A-33 承载 R2（对账的过目信号补齐）的落盘，产出 ActMarkReviewed 这一种 action，
// 经 store 既有的唯一状态写入入口 `ApplyStateWrite` 的**既有** reviewed_at 形态落盘。
// op 名不新增（`AllOpNames` 恒 16）、写权限矩阵不新增行（命中既有 #7 / #22）、
// `ApplyStateWrite` 仍恰四形态。仍未落盘的只剩 `undelete` / `delete` 两个（归 T-…-041）。
//
// 分级纪律：
//   - **W7 自 M3 起对五个状态类 op 是 error**（缺 `initiator=user` / 缺 `reason` /
//     `delete` 未引用 `status=approved` 提案）→ 退 2、零写入；
//   - `mark_reviewed` / `replace_block` / `remove_relation` 缺 `initiator=user` →
//     **W7 的 warning 形态**（A-15 窄口径 N-6），退 0、不拦截；
//   - **W3 自 M3 起真正判定**（论证关系某端不是 active 或已被逻辑删除），仍是 warning；
//   - W9–W12 全是 warning，其中 W10 / W11 是**幂等 no-op**：该 op 零写入、不产生空 commit。

import (
	"bytes"
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/proposal"
	"github.com/ikaqiu-Lemon/EverGreen/internal/rules"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// M3 新增的四种写入形态 + M4 追加的第五种（`undelete` / `delete` 仍不产出 action：
// 它们的落盘归 T-041，本阶段如实上报未落盘而不静默成功）。
const (
	// ActRemoveRelation 物理移除 relations[] 里匹配的全部记录（A-24）。
	ActRemoveRelation ActionKind = "remove_relation_write" // M3 / S2
	// ActReplaceBlock 替换「理解自检」的当前有效问题块。
	ActReplaceBlock ActionKind = "replace_block_write" // M3 / S2
	// ActSetStatus 覆盖 status 单键。deprecate 与 restore **共用**这一种形态，
	// 由载荷里的目标状态区分——状态维度只有一格可写，形态多了反而给出第二条写路径。
	ActSetStatus ActionKind = "set_status_write" // M3 / S2
	// ActSetReplacedBy 在失效卡上写替代指针（单向存储：只动主体这一份文件）。
	ActSetReplacedBy ActionKind = "set_replaced_by_write" // M3 / S2
	// ActMarkReviewed 覆盖过目信号单键（M4 · A-33：R2 的补齐复用 `mark_reviewed`，
	// **不新增 op 名**）。写入面**逐键封闭**为那一个键：状态维度、删除维度、替代指针
	// 维度与内容一格不碰（合同 §5「三维不牵连」），值恒取本次执行的时刻戳
	// （ExecOptions.Stamp，与 `eg mark-reviewed` 同一 RFC3339 格式化口径），
	// 因此这一形态**没有载荷字段** —— 没有可供调用方塞第二个键的地方。
	ActMarkReviewed ActionKind = "mark_reviewed_write" // M4 / S3
)

// StatusWrite 是一次 status 单键覆盖的写入意图（取值封闭在 model.Status 的两值内）。
type StatusWrite struct {
	Status string
}

// ReplacedByWrite 是一次替代指针的写入意图。
//
// 与 plan.ReplacedBy（plan 文件里的 op 载荷）**不是同一个类型**：那个是解析出来的原样
// 输入（带 Given 等表达完整性标记），这个是校验后确定要落盘的值，两者不得互换。
type ReplacedByWrite struct {
	Target string
	Reason string
}

// RelationRemoval 是一次物理移除的写入意图（方向已按 internal/rules 规范化）。
type RelationRemoval struct {
	Type   string
	Target string
	// Matches 是宿主端点（k-/o-）上命中的记录条数（移除全部命中，reason 不参与匹配）。
	Matches int
}

// BlockReplace 是一次块替换的写入意图。
type BlockReplace struct {
	Section       string
	BaseBlockHash string
	Payload       []byte
	// Locator 是 `<id>#<分区>#<序号>`，**只进报告 / 诊断自由文本**，永不写进文件内容。
	Locator string
}

// —— 通用授权与幂等判据 ——

// requireInitiator 判 W7 前半（授权合同 §10.3 V9 ≡ W7 前半，A-14 不另起编号）。
//
// **判据自 T-…-038 起改为 PathOf**（授权合同 §1 + N-1）：不再只看 `op.Initiator`，
// 而是看「plan 内的 initiator **加上**命令行 `--user-request`」这一对条件。
// 只有 P-U 才算授权表达完整；三种落到 P-A 的形态各自照旧分级：
//
//	缺 initiator / initiator 非 user → 原措辞
//	initiator: user 但无命令行佐证    → **反伪造**分支（N-1），措辞见 ForgeryNotice
//
// 分级本身**一字未改**（沿用 W7，不新增编号）：五个状态类 op → error，返回 false
// 表示该 op 整条不执行；另三个 op → warning，返回 true（不拦截，退出码不变）。
func (v *validator) requireInitiator(op *Op) bool {
	if v.pathOf(op) == PathUser {
		return true
	}
	var why string
	if Forged(op, v.auth()) {
		why = fmt.Sprintf("%s 的授权佐证不成立：%s", op.Name, ForgeryNotice)
	} else {
		got := op.Initiator
		if !op.InitiatorGiven {
			got = "（缺该字段）"
		}
		why = fmt.Sprintf("%s 是用户发起专属 op：必须逐字给 initiator: %s，实得 %s",
			op.Name, InitiatorUser, got)
	}
	if IsStateOp(op.Name) {
		d := errorAt(W7, op.Index, opPath(op.Index, "initiator"),
			"%s（W7 自 S2 起是 error：退 2、零写入）", why)
		d.Target = op.Target
		v.add(d)
		return false
	}
	d := warnAt(W7, op.Index, opPath(op.Index, "initiator"),
		"%s：按 A-15 窄口径记 warning，不拦截、退出码不变（能不能写还要过写权限矩阵）", why)
	d.Target = op.Target
	v.add(d)
	return true
}

// requireStateReason 判 W7 后半之一：状态类 op 的 reason 必填（缺失 → error、零写入）。
func (v *validator) requireStateReason(op *Op) bool {
	if op.Reason != "" {
		return true
	}
	d := errorAt(W7, op.Index, opPath(op.Index, "reason"),
		"%s 缺 reason：状态类 op 必须写明理由（W7 自 S2 起是 error：退 2、零写入）", op.Name)
	d.Target = op.Target
	v.add(d)
	return false
}

// cardFacts 只读取回目标卡的 frontmatter 事实（状态 / 逻辑删除 / 关系）。
// 读不到（本 plan 内刚声明、或无库可读）返回 false，调用方跳过幂等判定。
func (v *validator) cardFacts(rel string) (model.Card, bool) {
	raw, ok := v.readExisting(rel)
	if !ok {
		return model.Card{}, false
	}
	card, err := store.CardOf(raw)
	if err != nil {
		return model.Card{}, false
	}
	return card, true
}

// endpointFacts 只读取回论证关系宿主（知识卡或观点，按端点 id 前缀 k-/o- 分流）的
// 关系维度事实（状态 / 逻辑删除 / relations[] / 宿主 ID）。
// 与 cardFacts 分开：论证关系与 replaced_by 都是跨类型的，宿主可能是观点，而 cardFacts
// 只认知识卡（对观点会因分区校验失败而读不到）。仍是 card-only 的业务
// （deprecate/restore/undelete/delete）走 cardFacts；本函数服务关系读侧（W3 判定 /
// 同对去重 / 命中计数）与 set_replaced_by 的两端事实（E10 墓碑 / W12 deprecated）。
func (v *validator) endpointFacts(id, rel string) (store.RelationHost, bool) {
	raw, ok := v.readExisting(rel)
	if !ok {
		return store.RelationHost{}, false
	}
	host, err := store.RelationHostOf(model.RelationEndpoint(id), raw)
	if err != nil {
		return store.RelationHost{}, false
	}
	return host, true
}

// idempotentNoOp 记一条 W11（三类幂等 no-op：该 op 零写入 + 进报告，不产生空 commit）。
func (v *validator) idempotentNoOp(op *Op, msg string, args ...interface{}) {
	d := warnAt(W11, op.Index, opPath(op.Index, "target"), msg, args...)
	d.Target = op.Target
	v.add(d)
}

// pendingWrite 如实说明「本 op 已通过校验，但落盘语义在后续 task 落地」。
//
// 这是**如实上报**而不是静默成功：本阶段（T-…-037）只交付 plan 层的 op 与诊断底座，
// 五个状态类 op 与 `mark_reviewed` 的 frontmatter 改写分别归 T-…-039 / 041 / 042。
// M4 · T-…-051 起 `mark_reviewed` 已落盘，落点因此**恰剩两处**（`undelete` / `delete`）。
func (v *validator) pendingWrite(op *Op, owner string) {
	d := infoAt(op.Index, opPath(op.Index, "op"),
		"%s 已通过 M3 校验链；其 frontmatter 落盘语义归 %s，本次零写入（op 未被拒绝，也未改动任何字节）",
		op.Name, owner)
	d.Target = op.Target
	v.add(d)
}

// —— ① deprecate / ② restore / ⑤ undelete ——

// deprecate 校验 deprecate（用户发起专属；W7 升 error、W11 幂等）。
func (v *validator) deprecate(op *Op) {
	rel, ok := v.cardTarget(op, "target", op.Target)
	if !ok {
		return
	}
	if !v.requireInitiator(op) || !v.requireStateReason(op) {
		return
	}
	// 矩阵 #3（status）：P-A 🔴 / P-U ✅——W7 管分级，矩阵管「这条路径能不能写这一格」。
	if !v.matrixGate(op, ObjectCard, FieldStatus) {
		return
	}
	if card, ok := v.cardFacts(rel); ok && card.Status == model.StatusDeprecated {
		v.idempotentNoOp(op, "目标 %s 已是 deprecated：幂等 no-op，本 op 零写入、不产生空 commit", op.Target)
		return
	}
	v.statusAction(op, rel, model.StatusDeprecated)
}

// restore 校验 restore（用户发起专属；W7 升 error、W11 幂等）。
func (v *validator) restore(op *Op) {
	rel, ok := v.cardTarget(op, "target", op.Target)
	if !ok {
		return
	}
	if !v.requireInitiator(op) || !v.requireStateReason(op) {
		return
	}
	// 矩阵 #3（status）：restore 与 deprecate 写同一格，取值同为 P-A 🔴 / P-U ✅。
	if !v.matrixGate(op, ObjectCard, FieldStatus) {
		return
	}
	if card, ok := v.cardFacts(rel); ok && card.Status == model.StatusActive {
		v.idempotentNoOp(op, "目标 %s 已是 active：幂等 no-op，本 op 零写入、不产生空 commit", op.Target)
		return
	}
	v.statusAction(op, rel, model.StatusActive)
}

// statusAction 产出 status 单键覆盖的 action（deprecate / restore 共用）。
//
// 次序不可调：W6 + B3 的 ExpectedHash 由 baseCheck 写入 action，base 未覆盖时该 action
// 被标记跳过（由 executor 计入 skipped[]，上层退 3），而**不是**在此处静默丢弃。
func (v *validator) statusAction(op *Op, rel string, status model.Status) {
	act := Action{Kind: ActSetStatus, OpIndex: op.Index, Op: op, ID: op.Target, Path: rel,
		Domain: store.DomainOf(rel), Status: &StatusWrite{Status: string(status)}}
	v.baseCheck(op, op.Target, rel, &act)
	v.res.Actions = append(v.res.Actions, act)
}

// UndeleteNoOp 是「清空删除标记」的 **W11 幂等判定**：目标没有 deleted_at 就没有标记可清。
//
// 为什么导出：plan 层的 undelete op 与 CLI 直写路径（`eg undelete`，A-23 例外）必须共用
// **同一个**判定口径，两处各判一遍必然漂移。判定只看 deleted_at 一个事实：
// status 与本判定无关（正交维度），deleted_reason 不单独存在（两键同生同灭）。
func UndeleteNoOp(card model.Card) bool {
	return card.DeletedAt == nil
}

// undelete 校验 undelete（用户发起专属；W7 升 error、W11 幂等）。
func (v *validator) undelete(op *Op) {
	rel, ok := v.cardTarget(op, "target", op.Target)
	if !ok {
		return
	}
	if !v.requireInitiator(op) || !v.requireStateReason(op) {
		return
	}
	// 矩阵 #6（清空 deleted_at / deleted_reason）：P-A 🔴 / P-U ✅。
	if !v.matrixGate(op, ObjectCard, FieldClearDeleteMark) {
		return
	}
	if card, ok := v.cardFacts(rel); ok && UndeleteNoOp(card) {
		v.idempotentNoOp(op, "目标 %s 未被逻辑删除：幂等 no-op，本 op 零写入、不产生空 commit", op.Target)
		return
	}
	v.pendingWrite(op, "T-…-041（逻辑删除十一步时序）")
}

// —— ③ set_replaced_by ——

// setReplacedBy 校验 set_replaced_by（W7 升 error、E10、W12、W2）。
//
// 宿主 `target` 与指向端 `replaced_by.target` 都是**跨类型端点**（k- 知识卡 / o- 观点）：
// 迁移会把一条判断从知识卡改成观点，此时原 k- 卡逻辑删除并用 replaced_by 指向新 o- 观点
// （Schema v2 §9.2 / T-009）。两端因此走 relationEndpoint 定位（接受 k/o、拒 s/n/r/p/畸形/
// 不存在），事实读取走 endpointFacts（宿主可能是观点，cardFacts 只认知识卡）。权限仍复用
// 矩阵 #4（ObjectCard + FieldReplacedBy）：宿主是知识卡还是观点，写权限口径一致。
func (v *validator) setReplacedBy(op *Op) {
	rel, ok := v.relationEndpoint(op, "target", op.Target)
	if !ok {
		return
	}
	if !v.requireInitiator(op) {
		return
	}
	// 矩阵 #4（replaced_by）：P-A 🔴 / P-U ✅。
	if !v.matrixGate(op, ObjectCard, FieldReplacedBy) {
		return
	}
	rb := op.ReplacedBy
	if rb == nil || !rb.Given || rb.Target == "" {
		v.add(errorAt(E2, op.Index, opPath(op.Index, "replaced_by.target"),
			"set_replaced_by 缺 replaced_by.target：替代指针无法定位"))
		return
	}
	pointeeRel, ok := v.relationEndpoint(op, "replaced_by.target", rb.Target)
	if !ok {
		return
	}
	if rb.Target == op.Target {
		v.add(errorAt(E2, op.Index, opPath(op.Index, "replaced_by.target"),
			"replaced_by.target 不得指向自身（%s）", rb.Target))
		return
	}
	if rb.Reason == "" {
		d := warnAt(W2, op.Index, opPath(op.Index, "replaced_by.reason"),
			"replaced_by.reason 缺失：替代关系的理由由它承载（照常写入 + 进报告）")
		d.Target = op.Target
		v.add(d)
	}
	// E10：§5.1 真值表「可作 replaced_by 目标」列的两个「已删除」象限均 🔴。
	// 合同 §8.2.1 的行文「set_replaced_by 的 target」在本 op 下有两种读法
	// （`target` 主体 / `replaced_by.target` 指向端），本实现取**两者的并集**（只加严）：
	// 已被逻辑删除的对象既不得作替代指针的主体，也不得作替代目标。两端事实都走
	// endpointFacts（宿主可能是观点），墓碑取 RelationHost.Tombstone（= frontmatter deleted_at）。
	if host, ok := v.endpointFacts(op.Target, rel); ok && host.Tombstone != nil {
		v.add(proposalStateError(E10, op.Index, opPath(op.Index, "target"), op.Target,
			"set_replaced_by 的 target %s 已被逻辑删除（deleted_at=%s）：已删除对象不得作替代指针的主体（先 undelete）",
			op.Target, host.Tombstone))
		return
	}
	pointee, pointeeKnown := v.endpointFacts(rb.Target, pointeeRel)
	if pointeeKnown && pointee.Tombstone != nil {
		v.add(proposalStateError(E10, op.Index, opPath(op.Index, "replaced_by.target"), rb.Target,
			"replaced_by.target %s 已被逻辑删除（deleted_at=%s）：已删除对象不得作 replaced_by 目标",
			rb.Target, pointee.Tombstone))
		return
	}
	// W12：替代目标是 deprecated 且未删除 → §5.1 该象限 🟡「允许但提示」：照常写入 + 进报告。
	if pointeeKnown && pointee.Status == model.StatusDeprecated {
		d := warnAt(W12, op.Index, opPath(op.Index, "replaced_by.target"),
			"replaced_by.target %s 是 deprecated 且未被删除：允许但提示，照常写入 + 进报告", rb.Target)
		d.Target = rb.Target
		v.add(d)
	}
	// 载荷取校验后的确定值，不做任何补全：store 侧要求 target 与 reason 同时给全，
	// rb.Reason 为空时由 store 拒写并经 executor 如实上报（不静默替换成别的字段）。
	act := Action{Kind: ActSetReplacedBy, OpIndex: op.Index, Op: op, ID: op.Target, Path: rel,
		Domain:   store.DomainOf(rel),
		Replaced: &ReplacedByWrite{Target: rb.Target, Reason: rb.Reason}}
	v.baseCheck(op, op.Target, rel, &act)
	v.res.Actions = append(v.res.Actions, act)
}

// —— ④ delete ——

// deleteOp 校验 delete（W7 升 error + E7 / E8 / E9 / W9）。
//
// `delete` 是唯一「用户发起专属 **+ 已批准提案**」的 op：提案控制面走 CLI 直写例外
// （A-23），但**批准后的知识数据修改仍走 ChangePlan**，即本 op。
func (v *validator) deleteOp(op *Op) {
	rel, ok := v.cardTarget(op, "target", op.Target)
	if !ok {
		return
	}
	_ = rel
	if !v.requireInitiator(op) || !v.requireStateReason(op) {
		return
	}
	// 矩阵 #5（deleted_at / deleted_reason）：P-A 🔴 / P-U ✅（且仍须已批准提案）。
	if !v.matrixGate(op, ObjectCard, FieldDeleteMark) {
		return
	}
	if op.Proposal == "" {
		d := errorAt(W7, op.Index, opPath(op.Index, "proposal"),
			"delete 未引用提案：必须引用一份 status=%s 的提案（W7 自 S2 起是 error：退 2、零写入）",
			proposal.StatusApproved)
		d.Target = op.Target
		v.add(d)
		return
	}
	f, ok := v.proposalOf(op)
	if !ok {
		return
	}
	if !v.proposalStateOK(op, f) {
		return
	}
	v.proposalGaps(op, f)
	if f.P.Status != proposal.StatusApproved {
		d := errorAt(W7, op.Index, opPath(op.Index, "proposal"),
			"提案 %s 的 status=%s，不是 %s：delete 必须引用已批准提案（W7 自 S2 起是 error：退 2、零写入）",
			op.Proposal, f.P.Status, proposal.StatusApproved)
		d.Target = op.Proposal
		v.add(d)
		return
	}
	v.pendingWrite(op, "T-…-041（逻辑删除十一步时序）")
}

// proposalOf 读并解析 delete 引用的提案（读不到 / 形态不成立 → 已登记诊断，返回 false）。
func (v *validator) proposalOf(op *Op) (*proposal.File, bool) {
	id := proposal.ID(op.Proposal)
	if !id.Valid() {
		v.add(errorAt(E2, op.Index, opPath(op.Index, "proposal"),
			"proposal %q 不是合法提案 ID（形如 p-YYYYMMDD-NNN）：提案无法定位", op.Proposal))
		return nil, false
	}
	rel := proposal.Rel(id)
	raw, ok := v.readExisting(rel)
	if !ok {
		v.add(errorAt(E2, op.Index, opPath(op.Index, "proposal"),
			"提案 %s 不存在（%s）：delete 引用的提案无法定位", op.Proposal, rel))
		return nil, false
	}
	f, err := proposal.Parse(raw)
	if err != nil {
		code := CodeForProposalViolation(err)
		if code == "" {
			code = E4
		}
		v.add(proposalStateError(code, op.Index, opPath(op.Index, "proposal"), op.Proposal,
			"提案 %s 形态不成立：%v", op.Proposal, err))
		return nil, false
	}
	return f, true
}

// proposalStateOK 判 E7 / E8 / E9（提案状态事实的三类越界），返回 false 表示已登记 error。
func (v *validator) proposalStateOK(op *Op, f *proposal.File) bool {
	path := opPath(op.Index, "proposal")
	// E8：status / execution.status 枚举越界。
	if err := proposal.CheckStatus(f.P.Status); err != nil {
		v.add(proposalStateError(CodeForProposalViolation(err), op.Index, path, op.Proposal,
			"提案 %s：%v", op.Proposal, err))
		return false
	}
	if !f.P.Execution.Status.Valid() {
		v.add(proposalStateError(E8, op.Index, path, op.Proposal,
			"提案 %s 的 execution.status=%q 不在三取值内 %v", op.Proposal,
			f.P.Execution.Status, proposal.ExecStatuses()))
		return false
	}
	// E8：decision.result 与权威的 status 不一致（pending 时非空亦算）。
	if err := proposal.CheckDecisionConsistency(f.P); err != nil {
		v.add(proposalStateError(CodeForProposalViolation(err), op.Index, path, op.Proposal,
			"提案 %s：%v", op.Proposal, err))
		return false
	}
	// E9：落盘的 status × execution 命中可达矩阵 🔴 / ⚠️ 格。
	if UnreachableStateCombo(f.P.Status, f.P.Execution.Status) {
		v.add(proposalStateError(E9, op.Index, path, op.Proposal,
			"提案 %s 的 status=%s × execution.status=%s 命中可达矩阵不可达格："+
				"只有 %s 行的三种执行结果可达，其余三态的 execution.status 必须是 %s",
			op.Proposal, f.P.Status, f.P.Execution.Status,
			proposal.StatusApproved, proposal.ExecNotStarted))
		return false
	}
	// E7：superseded 必须能解析到存在的后继提案。
	// 判定**唯一实现**在 internal/proposal（CheckSupersededChain，T-…-035）：
	// 本包只把它的结构化违规映射成编号，不另写一套链断裂判据。
	if err := proposal.CheckSupersededChain(f.P, func(id proposal.ID) bool {
		_, ok := v.readExisting(proposal.Rel(id))
		return ok
	}); err != nil {
		// 编号只由映射表给（CodeForProposalViolation：提案链断裂族 → E7）：
		// 这里**不写兜底编号**，否则映射表被改坏也照样报 E7，回归就抓不出来了。
		v.add(proposalStateError(CodeForProposalViolation(err), op.Index, path, op.Proposal,
			"提案 %s：%v", op.Proposal, err))
		return false
	}
	return true
}

// proposalGaps 判 W9（提案十项必备缺项）：**不拒绝**，只进 warnings[]。
func (v *validator) proposalGaps(op *Op, f *proposal.File) {
	var gaps []string
	if err := proposal.ValidateLayout(f); err != nil {
		gaps = append(gaps, fmt.Sprintf("frontmatter 键集合不齐（%v）", err))
	}
	for _, name := range proposal.BodySections() {
		if len(bytes.TrimSpace(f.SectionBody(name))) == 0 {
			gaps = append(gaps, fmt.Sprintf("正文分区「%s」为空", name))
		}
	}
	if f.P.Status != proposal.StatusPending && f.P.Decision.Reason == "" {
		gaps = append(gaps, "decision.reason 缺失")
	}
	if len(gaps) == 0 {
		return
	}
	d := warnAt(W9, op.Index, opPath(op.Index, "proposal"),
		"提案 %s 十项必备有缺项（%v）：按 warning 处理，不拒绝、不拦截其余 op", op.Proposal, gaps)
	d.Target = op.Proposal
	v.add(d)
}

// —— ⑥ mark_reviewed ——

// markReviewed 校验 mark_reviewed（A-15 窄口径：缺 initiator 只记 warning）。
func (v *validator) markReviewed(op *Op) {
	obj, rel, ok := v.reviewedTarget(op)
	if !ok {
		return
	}
	// W7 与矩阵在此**分工**：mark_reviewed 缺 initiator 只是 A-15 窄口径的 warning
	// （N-6），但矩阵 #7 / #22 的 P-A 格是 🔴——因此 P-A 下照样被 E6 拦住退 2、零写入。
	// 二者不矛盾：W7 回答「授权表达完整吗」，矩阵回答「这条路径能不能写这一格」。
	v.requireInitiator(op)
	if !v.matrixGate(op, obj, FieldReviewedAt) {
		return
	}
	// M4 · A-33：产出 ActMarkReviewed（过目信号单键覆盖）。次序不可调 ——
	// W6 + B3 的 ExpectedHash 由 baseCheck 写入 action，base 未覆盖该文件时这条 action
	// 被标记跳过（由 executor 计入 skipped[kind=file_changed]），而**不是**在此静默丢弃。
	act := Action{Kind: ActMarkReviewed, OpIndex: op.Index, Op: op, ID: op.Target, Path: rel,
		Domain: store.DomainOf(rel)}
	v.baseCheck(op, op.Target, rel, &act)
	v.res.Actions = append(v.res.Actions, act)
}

// reviewedTarget 解析 mark_reviewed 的 target：**知识卡或材料笔记**（合同 §5 判定第 1 条），
// 返回该对象在写权限矩阵里的对象类（#7 / #22 两行都已在册，矩阵不新增行）与落盘路径。
//
// 为什么这里不复用 cardTarget 一条路：M3 的触发②（`eg mark-reviewed`）只面向知识卡，
// 而 M4 的触发①（对账补齐）覆盖**知识卡与材料笔记两类**对象 —— 两类各有自己的矩阵行，
// 把笔记挡在 E2 之外等于让 R2 对半个对账域失效。ID 类型写混（例如原文 `s-`）仍是 E2。
func (v *validator) reviewedTarget(op *Op) (Object, string, bool) {
	id := op.Target
	if id == "" {
		v.add(errorAt(E2, op.Index, opPath(op.Index, "target"),
			"%s 缺 target：过目信号的落点无法定位", op.Name))
		return "", "", false
	}
	if _, err := model.ParseCardID(id); err == nil {
		rel, ok := v.cardTarget(op, "target", id)
		return ObjectCard, rel, ok
	}
	if _, err := model.ParseNoteID(id); err != nil {
		v.add(errorAt(E2, op.Index, opPath(op.Index, "target"),
			"%s 的 target %s 既不是知识卡 ID（k-…）也不是材料笔记 ID（n-…）：过目信号只落在这两类对象上",
			op.Name, id))
		return "", "", false
	}
	rel, ok := v.resolve(id)
	if !ok {
		v.add(errorAt(E2, op.Index, opPath(op.Index, "target"),
			"target %s 不存在于全库：过目信号的落点无法定位", id))
		return "", "", false
	}
	if !v.frontmatterCheck(op, "target", rel) {
		return "", "", false
	}
	v.domainCheck(op, "target", rel)
	return ObjectNote, rel, true
}

// —— ⑧ remove_relation ——

// removeRelation 校验并展开 remove_relation（W10、W2、W3、W8；A-24 物理移除语义）。
//
// A-24 口径 1 / 2 / 4 在本函数落地：
//   - 匹配键 = 规范化后的三元组 `(from, type, target)`，`reason` 不参与匹配；
//   - `opposing` 先按两端 ID 字典序规范化（唯一实现 internal/rules，与 add_relation 同一套）；
//   - 未命中 → **W10** 幂等：**不产出 action**（零写入、零 commit），不拦截其余 op。
func (v *validator) removeRelation(op *Op) {
	if op.Target != "" && model.SourceID(op.Target).Valid() {
		v.add(errorAt(E3, op.Index, opPath(op.Index, "target"),
			"relations[].target 只连论证性产物（知识卡 k-… 或观点 o-…），实际是原文 ID %s：ID 类型写混一律拒绝", op.Target))
		return
	}
	if op.Type == "" {
		v.add(errorAt(E5, op.Index, opPath(op.Index, "type"),
			"remove_relation 缺 type：合法取值恰为 %v", model.ValidRelationTypes()))
		return
	}
	relType, err := model.ParseRelationType(op.Type)
	if err != nil {
		v.add(errorAt(E5, op.Index, opPath(op.Index, "type"),
			"type=%q 不在论证关系封闭四值内：合法取值恰为 %v", op.Type, model.ValidRelationTypes()))
		return
	}
	// 自环与 add_relation 同阶段、同判据：from 与 target 同为一个论证端点是**纯静态**的
	// plan 级语义约束（不读盘即可判定），必须在端点解析 / W10 幂等分支**之前**拦下并退 2。
	// 若放到 W10 之后，未命中的自环会被折成 W10 幂等 no-op（退 0），把「无论证意义的自环」
	// 掩盖成「删了个不存在的关系」——两者语义不同：自环该硬拒，缺失关系才是幂等。
	// 编号取 **E5**（op 字段取值组合不成立），与 add_relation 自环守卫同级；path 指向 target。
	if op.From != "" && op.From == op.Target {
		v.add(errorAt(E5, op.Index, opPath(op.Index, "target"),
			"关系两端不得是同一论证端点：from 与 target 同为 %s（自环无论证意义）；"+
				"本次零写入、无 commit", op.From))
		return
	}
	fromRel, ok := v.relationEndpoint(op, "from", op.From)
	if !ok {
		return
	}
	targetRel, ok := v.relationEndpoint(op, "target", op.Target)
	if !ok {
		return
	}
	// 同 mark_reviewed 的分工：W7 在此只是 warning（A-15 窄口径），
	// 但矩阵 #11「relations[] 删除」的 P-A 格是 🔴（N-3 Agent 自动路径不得删关系），
	// 故 P-A 下由 E6 拦住退 2、零写入；P-U 下 ✅ 放行。
	v.requireInitiator(op)
	if !v.matrixGate(op, ObjectCard, FieldRelationsRemove) {
		return
	}
	if op.Reason == "" || op.Reason == op.Type {
		v.add(warnAt(W2, op.Index, opPath(op.Index, "reason"),
			"remove_relation 的 reason 缺失或等于关系名本身：移除照常执行 + 进报告"))
	}
	v.relationEndW3(op, "from", op.From, fromRel)
	v.relationEndW3(op, "target", op.Target, targetRel)

	// A-24 口径 2：opposing 先按字典序规范化，再匹配（删除方向无关）。
	removal := RelationRemoval{Type: string(relType), Target: op.Target}
	id, rel := op.From, fromRel
	if relType == model.RelationOpposing {
		pair := rules.Opposing(model.RelationEndpoint(op.From), model.RelationEndpoint(op.Target))
		if pair.Normalized {
			d := warnAt(W8, op.Index, opPath(op.Index, "from"),
				"opposing 方向未规范化：已按字典序改为 from=%s / target=%s 后匹配（单向存储，删除方向无关）",
				pair.From, pair.Target)
			d.Target = string(pair.Target)
			v.add(d)
			removal.Target = string(pair.Target)
			id = string(pair.From)
			newRel, found := v.resolve(id)
			if !found {
				v.add(errorAt(E2, op.Index, opPath(op.Index, "from"),
					"规范化后的宿主端 %s 不存在于全库：关系无法定位", id))
				return
			}
			rel = newRel
		}
	}
	removal.Matches = v.countRelations(id, rel, removal.Type, removal.Target)
	if removal.Matches == 0 {
		d := warnAt(W10, op.Index, opPath(op.Index, "target"),
			"remove_relation 未命中任何既有关系（%s → %s → %s）：幂等 no-op，"+
				"该 op 零写入、不产生空 commit，不拦截其余 op、退出码不受影响",
			id, removal.Type, removal.Target)
		d.Target = removal.Target
		v.add(d)
		return
	}
	act := Action{Kind: ActRemoveRelation, OpIndex: op.Index, Op: op, ID: id, Path: rel,
		Domain: store.DomainOf(rel), Removal: &removal}
	v.baseCheck(op, id, rel, &act)
	v.res.Actions = append(v.res.Actions, act)
}

// countRelations 数宿主上匹配 `(type, target)` 的记录条数（reason 不参与匹配）。
// id 是宿主端点（k-/o-），用于按类型分流读取宿主的 relations[]。
func (v *validator) countRelations(id, rel, relType, target string) int {
	host, ok := v.endpointFacts(id, rel)
	if !ok {
		return 0
	}
	n := 0
	for _, exist := range host.Relations {
		if string(exist.Type) == relType && string(exist.Target) == target {
			n++
		}
	}
	return n
}

// relationEndW3 判 W3（**M3 起真正判定**）：论证关系某端不是 active 或已被逻辑删除。
// 仍是 warning：关系照写 / 照删 + 进报告，不拦截（S5 起才 error）。
func (v *validator) relationEndW3(op *Op, field, id, rel string) {
	host, ok := v.endpointFacts(id, rel)
	if !ok {
		return
	}
	if host.Tombstone != nil {
		d := warnAt(W3, op.Index, opPath(op.Index, field),
			"论证关系的一端 %s 已被逻辑删除（deleted_at=%s）：照常处理 + 进报告，不拦截（S5 起 error）",
			id, host.Tombstone)
		d.Target = id
		v.add(d)
		return
	}
	if host.Status != model.StatusActive {
		d := RelationStatusWarning(op.Index, field, id, host.Status)
		v.add(d)
	}
}
