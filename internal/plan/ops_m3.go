package plan

// M3 新增 8 个 op 的字段定义与注册（提案合同 `2026-10-10-m3-proposal-state-contract.md` §8.1）。
//
// **op 是开放集合**（ChangePlan 合同 §4.5）：`ops[].op` 的取值随阶段增补，
// 未知 op 仍报 error（E5）且**整条 op 不执行**。本文件只做「注册 + 字段表」；
// 校验链在 validate_m3.go / replace_block.go，落盘形态在 internal/store。
//
// 阶段口径：S1 七个 op（schema.go 的 OpNames，合同 §3 冻结，**一个不加**）+ 本文件 8 个
// = M3 可派发的 15 个，再加 A-13 的 `edit_section` = **M3 期恰 16**（AllOpNames）。
// **M4 期**按 A-33 追加 `set_stale` 一个（本文件下半段），派发全集因此是
// 「M3 期 16 + M4 新增 1 = **17**」——M3 期恒 16 是历史事实，不改写，只做加法。
// 归属未知（A-18）的 `set_tags` / `reprocess_note` /
// `save_review` 仍留在 s2OpNames 里按未知 op 拒绝（授权合同 §9 A-18，本阶段不定义字段）。
//
// 授权口径（授权合同 §9 A-15 窄口径）：
//   - 「状态类 op」= `deprecate` / `restore` / `set_replaced_by` / `delete` / `undelete`
//     **恰五个**（StateOpNames），缺 `initiator=user` 或缺 `reason` → **W7 升 error**；
//   - 另三个（`mark_reviewed` / `replace_block` / `remove_relation`）缺 `initiator=user`
//     → **W7 的 warning 形态**，不拦截、退出码不变。
//
// owner 裁决留痕：`remove_relation` 的落盘语义 = **物理移除**，逐条口径见
// `docs/specs/2026-10-13-m3-prestart-adjudication.md` §7（A-24）与提案合同 §8.5.2；
// 提案控制面走 CLI 直写例外（A-23）故**不新增提案类 op**，本文件的 op 集合恰 8 个。

// M3 新增的 8 个 op 名（声明顺序 = 提案合同 §8.1 表格顺序）。
const (
	// OpDeprecate 用户显式失效一张卡（status → deprecated）。
	OpDeprecate = "deprecate" // M3 / S2
	// OpRestore 用户显式恢复一张失效卡（status → active）。
	OpRestore = "restore" // M3 / S2
	// OpSetReplacedBy 给失效卡挂替代指针 replaced_by。
	OpSetReplacedBy = "set_replaced_by" // M3 / S2
	// OpDelete 逻辑删除（必须引用 status=approved 提案）。
	OpDelete = "delete" // M3 / S2
	// OpUndelete 撤销逻辑删除。
	OpUndelete = "undelete" // M3 / S2
	// OpMarkReviewed 标记已过目（写 reviewed_at）。
	OpMarkReviewed = "mark_reviewed" // M3 / S2
	// OpReplaceBlock 替换「理解自检」的当前有效问题块（必带 base_block_hash）。
	OpReplaceBlock = "replace_block" // M3 / S2
	// OpRemoveRelation 物理移除论证关系记录（A-24：不留墓碑）。
	OpRemoveRelation = "remove_relation" // M3 / S2
)

// M3OpNames 是 M3 新增的 op 名（**恰 8 个**，顺序即合同 §8.1 表格顺序）。
func M3OpNames() []string {
	return []string{OpDeprecate, OpRestore, OpSetReplacedBy, OpDelete, OpUndelete,
		OpMarkReviewed, OpReplaceBlock, OpRemoveRelation}
}

// AllOpNames 是可派发的全部 op（集合外一律 E5）：
// S1 七个（合同 §3 冻结）+ 提案与状态合同 §8.1 的八个 + 授权合同 A-13 的一个
// （`edit_section`，`eg edit` 的载体，见 edit_section.go）+ M4 对账合同 A-33 的一个
// （`set_stale`，R6 综述失准标记，见 validate_m4.go）。
//
// 计数口径是**加法等式**（M3 期结论不改写、不放宽）：
// **M3 期 7 + 8 + 1 = 16** ＋ **M4 新增 1** = **17**；
// Schema v2（T-…-003）按契约 §4.4 给主链路加 `create_opinion` / `append_opinion`
// 两个 Opinion 写口，主链路由 7 变 **9**（`create_card` / `append_card` 只是改名，
// 旧名以别名保留、不计入名册），故全集由 17 变 **19**。
func AllOpNames() []string {
	out := append(OpNames(), M3OpNames()...)
	out = append(out, EditOpNames()...)
	return append(out, M4OpNames()...)
}

// OpSetStale 是 M4 期新增的唯一 op 名：给综述写失准标记（对账合同 §9 / R6；裁决 **A-33**）。
//
// # 为什么它不进 M3OpNames，也不进 StateOpNames
//
//   - **不进 M3OpNames**：那个集合逐字对应提案与状态合同 §8.1 表格的 8 行，「§8.1 有几行」
//     是 M3 期的历史事实（恒 8），塞进去会让这个可复算事实与代码不再一致。
//   - **不进 StateOpNames**：A-15 窄口径的五个状态类 op 是「缺 `initiator=user` / 缺 `reason`
//     即把 W7 升 error」的适用面（恒 5）。**A-34 明确 R6 写 `stale` 不需要 `--user-request`**，
//     它由对账自动写入，因此绝不能落进那个 W7 error 收紧面——那会让 R6 在自动路径上必然退 2。
const OpSetStale = "set_stale" // M4 / S3

// M4OpNames 是 M4 新增的 op 名（**恰 1 个**：A-33 的 `set_stale`）。
func M4OpNames() []string { return []string{OpSetStale} }

// IsM4Op 报告该 op 名是否属 M4 新增的一个。
func IsM4Op(name string) bool { return inList(M4OpNames(), name) }

// M4OpFields 是 `set_stale` 的字段名集合（`op` 自身不计）。
//
// 恰两格且**没有 initiator**：A-34 已裁决 R6 不需要 `--user-request`，多给一格授权字段
// 只会诱导调用方以为「写了 initiator 就更有权」；`reason` 的取值封闭在
// model.ValidStaleReasons() 的三值内（校验在 validate_m4.go，落盘再校验一次在 store）。
func M4OpFields() []string { return []string{"target", "reason"} }

// m4OpKnownKeys 是 `set_stale` 的顶层键集合；集合外的键落进 Extra + I1（前向兼容）。
func m4OpKnownKeys(name string) []string {
	if name != OpSetStale {
		return nil
	}
	return append([]string{"op"}, M4OpFields()...)
}

// StateOpNames 是「状态类 op」的**窄口径**成员（授权合同 §9 A-15：**恰五个**）。
//
// 只有这五个的 `initiator=user` / `reason` 缺失才把 W7 判成 error；
// `mark_reviewed` / `replace_block` / `remove_relation` **不在此列**（N-6），
// M4 新增的 `set_stale` 同样**不在此列**（A-34：R6 写 `stale` 不需要 `--user-request`）。
func StateOpNames() []string {
	return []string{OpDeprecate, OpRestore, OpSetReplacedBy, OpDelete, OpUndelete}
}

// IsM3Op 报告该 op 名是否属 M3 新增的 8 个。
func IsM3Op(name string) bool { return inList(M3OpNames(), name) }

// IsStateOp 报告该 op 名是否属 A-15 窄口径的五个状态类 op。
func IsStateOp(name string) bool { return inList(StateOpNames(), name) }

// InitiatorUser 是「用户发起」的逐字取值（授权合同：`initiator: user`）。
const InitiatorUser = "user"

// SelfCheckSection 是 `replace_block` 唯一允许的分区名（合同 §8.1 第 7 行 `section: 理解自检`）。
const SelfCheckSection = "理解自检"

// M3OpFields 是每个 M3 op 的**字段名集合**，与提案合同 §8.1「字段」列逐字一致
// （不含固定的 `op` 自身；嵌套字段写点分路径，如 `replaced_by.target`）。
//
// 表驱动用例 TestM3Ops 逐行比对本表与合同原文。
func M3OpFields(name string) []string {
	switch name {
	case OpDeprecate, OpRestore, OpUndelete:
		return []string{"target", "reason", "initiator"}
	case OpSetReplacedBy:
		return []string{"target", "replaced_by.target", "replaced_by.reason", "initiator"}
	case OpDelete:
		return []string{"target", "reason", "proposal", "initiator"}
	case OpMarkReviewed:
		return []string{"target", "initiator"}
	case OpReplaceBlock:
		return []string{"target", "section", "block", "base_block_hash"}
	case OpRemoveRelation:
		return []string{"from", "type", "target", "reason", "initiator"}
	default:
		return nil
	}
}

// m3OpKnownKeys 是每个 M3 op 的**顶层键**集合（嵌套字段折叠成其顶层键 + `op` 自身）。
// 集合外的键落进 Extra，原样忽略并产出 I1（前向兼容规则第 1 条）。
func m3OpKnownKeys(name string) []string {
	switch name {
	case OpDeprecate, OpRestore, OpUndelete:
		return []string{"op", "target", "reason", "initiator"}
	case OpSetReplacedBy:
		return []string{"op", "target", "replaced_by", "initiator"}
	case OpDelete:
		return []string{"op", "target", "reason", "proposal", "initiator"}
	case OpMarkReviewed:
		return []string{"op", "target", "initiator"}
	case OpReplaceBlock:
		return []string{"op", "target", "section", "block", "base_block_hash"}
	case OpRemoveRelation:
		return []string{"op", "from", "type", "target", "reason", "initiator"}
	default:
		return nil
	}
}

// ReplacedBy 是 `set_replaced_by` 的替代指针（两个子字段，合同 §8.1 第 3 行）。
type ReplacedBy struct {
	Target string
	Reason string
	// Given 区分「缺 replaced_by 字段」与「给了但子字段为空」。
	Given bool
}

// parseM3Fields 解析 M3 op 独有的字段。与 S1 字段同一条只读路径：
// 只做类型断言，不做任何规范化，用户内容（block）一律以 []byte 交给写入侧。
func parseM3Fields(op *Op, m map[string]interface{}, diags []Diagnostic) []Diagnostic {
	if v, ok := m["initiator"]; ok {
		op.InitiatorGiven = true
		op.Initiator, _ = asString(v)
	}
	op.Proposal, _ = asString(m["proposal"])
	op.Section, _ = asString(m["section"])
	op.BaseBlockHash, _ = asString(m["base_block_hash"])
	if v, ok := m["block"]; ok {
		op.BlockGiven = true
		s, _ := asString(v)
		op.Block = []byte(s)
	}
	if v, ok := m["replaced_by"]; ok {
		rb := &ReplacedBy{Given: true}
		sub, ok := asMap(v)
		if !ok {
			diags = append(diags, errorAt(E5, op.Index, opPath(op.Index, "replaced_by"),
				"replaced_by 必须是含 target / reason 两个子字段的对象"))
		} else {
			rb.Target, _ = asString(sub["target"])
			rb.Reason, _ = asString(sub["reason"])
			for k := range sub {
				if k == "target" || k == "reason" {
					continue
				}
				diags = append(diags, infoAt(op.Index,
					opPath(op.Index, "replaced_by."+k), "未知附加字段已原样忽略（前向兼容）"))
			}
		}
		op.ReplacedBy = rb
	}
	return diags
}

func inList(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
