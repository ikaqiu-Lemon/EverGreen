package plan

// M3 写权限矩阵的表驱动落地（授权合同 `2026-10-10-m3-user-authorization-contract.md`
// §2.1–§2.7 恰 43 行 / §2.8 计数规则 / §2.9 owner 裁决逐格对齐）。
//
// 本文件**只描述矩阵事实**，不做任何判定动作：判定入口唯一在 authorize.go
// （PathOf / matrixGate）。这样「盘上取值」与「怎么用它拦人」分成两层，
// 改一格符号只会改本文件，不会顺手把判定语义也改了。
//
// 阶段边界（合同 §8）：矩阵里有一部分格子在 M3 **没有任何落地命令**
// （如 #32 `save_review`（A-18 归属未知）、#41 `SKILL.md`、#43 Git 历史）。本文件仍逐行登记它们——
// 矩阵是「规则全集」，不是「已实现动作清单」；缺载体的行由 §8 负向清单管，
// 不得因为「本阶段用不到」就把它从 43 行里删掉，否则 §2.8 的三个计数当场不可复算。
//
// **计数一律由符号规则派生**：16 / 1 / 4 三个数字只出现在测试的期望值里，
// 实现侧一个都不写死（合同 §2.8「必须可机器复算，不许凭感觉数」）。
//
// [M4] 2026-09-07（T-…-055 阶段 1）：#33（`stale / stale_reason`）的 **P-A 一格**依裁决
// **A-34** 由 🔴 改 ✅ —— R6 由对账自动写入、不需要 `--user-request`。**行数恒 43**、
// 对象类恒 7、MatrixCells 仍由两集合长度相乘得出；派生计数按实测变为
// 严格解锁行 **16**（不变：#33 的 P-A 已不含 🔴，故它既不进严格解锁也不进条件解锁）、
// 条件解锁行 **1**（不变）、两路径同 🔴 由 **5 变 4**。
// 这一格是 T-048 登记「需 owner 复核」的一格：本次是**依 A-34 执行放开，仍待 owner 事后复核**。

import (
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// Verdict 是矩阵单元格里的**单个**符号（合同 §2 读法行，恰四值）。
type Verdict string

// 四个符号。取值逐字取合同 §2 的读法行，**不得**换成 ASCII 代号：
// 反证脚本与人眼校对都按原文符号逐格比对。
const (
	// VerdictAllow ✅ = 允许。
	VerdictAllow Verdict = "✅"
	// VerdictDeny 🔴 = 禁止（触发即拒绝写入）。
	VerdictDeny Verdict = "🔴"
	// VerdictReport 🟡 = 允许但必须进报告提示。
	VerdictReport Verdict = "🟡"
	// VerdictNA — = 该路径在 M3 无此动作。
	VerdictNA Verdict = "—"
)

// Verdicts 是符号封闭集合（恰四值，声明顺序即合同 §2 读法行顺序）。
func Verdicts() []Verdict { return []Verdict{VerdictAllow, VerdictDeny, VerdictReport, VerdictNA} }

// Cell 是一个判定格的**有序符号集合**。
//
// 为什么不是单值枚举：合同 §2.1 第 12 行的 P-A 格**同时**含 🔴 与 ✅
// （对已有卡 🔴 / `create_card` 新建 ✅，按子情形分叉），§2.8 因此把它单列为
// 「条件解锁行」。单值枚举装不下这一格，且会让 §2.8 的三个计数无法机器复算。
type Cell []Verdict

// Has 报告该格是否含某个符号。
func (c Cell) Has(v Verdict) bool {
	for _, got := range c {
		if got == v {
			return true
		}
	}
	return false
}

// String 是人类可读形态（多符号格渲染成 `🔴 + ✅`，与合同 §2.8 的行文一致）。
func (c Cell) String() string {
	out := ""
	for i, v := range c {
		if i > 0 {
			out += " + "
		}
		out += string(v)
	}
	return out
}

// Path 是写入路径（合同 §1：**恰两条，不存在第三条**）。
type Path string

// 两条路径。任何「第三条路径」的引入都直接违反合同 §1，
// 由 TestWritePermissionMatrix 的 len(Paths()) == 2 钉住。
const (
	// PathAgent 是 Agent 自动路径：coding agent 生成 ChangePlan、经 `eg apply` 执行。
	PathAgent Path = "P-A"
	// PathUser 是用户显式路径：`initiator: user` **且**命令行带 `--user-request` 佐证。
	PathUser Path = "P-U"
)

// Paths 返回两条路径（顺序即矩阵两列的列序）。
func Paths() []Path { return []Path{PathAgent, PathUser} }

// Object 是矩阵的对象类（合同 §2.8：**恰 7 个**）。
type Object string

// 七个对象类。取值同时用作诊断 message 里的人类可读名。
const (
	// ObjectCard 知识卡 `k-`（§2.1 + §2.2，共 18 行）。
	ObjectCard Object = "知识卡 k-"
	// ObjectNote 材料笔记 `n-`（§2.3，9 行）。
	ObjectNote Object = "材料笔记 n-"
	// ObjectSource 原文 `s-`（§2.4，4 行）。
	ObjectSource Object = "原文 s-"
	// ObjectReview 主题综述 `r-`（§2.5，3 行）。
	ObjectReview Object = "主题综述 r-"
	// ObjectProposal 提案 `p-`（§2.6，5 行）。
	ObjectProposal Object = "提案 p-"
	// ObjectProject 工程文件（evergreen.yml / SKILL.md / unprocessed.md，§2.7 前 3 行）。
	ObjectProject Object = "工程文件"
	// ObjectGit Git 历史（§2.7 第 4 行）。
	ObjectGit Object = "Git"
)

// 矩阵 Field 列的逐字取值（供 LookupRow 精确查表；调用点一律引用常量，不写裸字符串）。
const (
	FieldID              = "id"
	FieldTags            = "tags"
	FieldStatus          = "status"
	FieldReplacedBy      = "replaced_by"
	FieldDeleteMark      = "deleted_at / deleted_reason"
	FieldClearDeleteMark = "清空 deleted_at / deleted_reason"
	FieldReviewedAt      = "reviewed_at"
	FieldUpdatedAt       = "updated_at"
	FieldSources         = "sources[]"
	FieldRelationsAdd    = "relations[] 新增"
	FieldRelationsRemove = "relations[] 删除"
	FieldSixthH2         = "第六个 H2（用户自建分区）"

	FieldSelfCheckAppend  = "分区「理解自检」— 追加块"
	FieldSelfCheckReplace = "分区「理解自检」— 替换当前有效问题块"

	FieldSourceIdentity = "id / url / title / saved_at"
	FieldSourceBody     = "原文正文"
	FieldSourceReasons  = "收录理由列表"

	FieldReviewWhole = "文件整体（save_review）" // 只是矩阵字段名；该 op 归属未知（A-18），本阶段无载体
	FieldReviewStale = "stale / stale_reason"

	FieldProposalNew       = "新建提案（status: pending）"
	FieldProposalApproved  = "status → approved"
	FieldProposalRejected  = "status → rejected / superseded"
	FieldProposalDecision  = "decision.*"
	FieldProposalExecution = "execution.*"

	FieldConfigYML   = "vault/evergreen.yml"
	FieldSkillMD     = "vault/SKILL.md"
	FieldUnprocessed = "vault/unprocessed.md 条目"
	FieldGitHistory  = "Git 历史"
)

// SectionField 把分区名拼成矩阵 Field 列的逐字形态（合同 §2 写作「分区「X」」）。
// 「理解自检」有两行（追加块 / 替换当前有效问题块），不走本函数，用两个专用常量。
func SectionField(name string) string { return "分区「" + name + "」" }

// MatrixRow 是矩阵的一行（对象 × 字段/分区 × 两列取值）。
type MatrixRow struct {
	// Num 是合同 §2 的行号（1..43 连续，**逐字**，不得重排）。
	Num int
	// Object 是对象类（七值封闭枚举）。
	Object Object
	// Field 是字段 / 分区名（LookupRow 按此逐字匹配）。
	Field string
	// Auto 是 P-A（Agent 自动）列。
	Auto Cell
	// User 是 P-U（用户显式）列。
	User Cell
	// Note 是该行的落地口径备注（只进诊断自由文本，不参与任何判定）。
	Note string
}

// CellFor 取该行在某条路径上的判定格。
func (r MatrixRow) CellFor(p Path) Cell {
	switch p {
	case PathAgent:
		return r.Auto
	case PathUser:
		return r.User
	default:
		// 恰两条路径（合同 §1）：走到这里说明有人加了第三条，fail fast。
		panic(fmt.Sprintf("写权限矩阵只有两条路径 %v，得到 %q", Paths(), p))
	}
}

// String 是单行的人类可读形态（诊断 message 里点名矩阵行时使用）。
func (r MatrixRow) String() string {
	return fmt.Sprintf("#%d %s · %s（P-A=%s / P-U=%s）", r.Num, r.Object, r.Field, r.Auto, r.User)
}

// Matrix 返回写权限矩阵的**全部 43 行**，Num 逐字 1..43 连续。
//
// 逐行取值直接抄自合同 §2.1–§2.7，**一格都不得自行改动**：
// 任何一次符号翻转都会改变 §2.8 的三个计数，由 TestWritePermissionMatrix 双向兜住。
func Matrix() []MatrixRow {
	allow := Cell{VerdictAllow}
	deny := Cell{VerdictDeny}
	report := Cell{VerdictReport}
	return []MatrixRow{
		// —— §2.1 知识卡 k-（15 行）——
		{1, ObjectCard, FieldID, allow, deny,
			"P-A 仅 create_card 新建时可写；改已有 ID 两条路径都不行（N-2 / U-10）"},
		{2, ObjectCard, FieldTags, allow, allow, "新建时 create_card.tags；eg tag rename|merge 属 S2"},
		{3, ObjectCard, FieldStatus, deny, allow, "eg deprecate / eg restore；写口唯一（§5.4）"},
		{4, ObjectCard, FieldReplacedBy, deny, allow, "eg replaced-by；写口唯一 SetReplacedBy"},
		{5, ObjectCard, FieldDeleteMark, deny, allow, "eg delete，且必须先有 status=approved 提案"},
		{6, ObjectCard, FieldClearDeleteMark, deny, allow, "eg undelete --reason；status 原封不动"},
		{7, ObjectCard, FieldReviewedAt, deny, allow,
			"仅 eg mark-reviewed（EG-CFM-06：Agent 写入一律不更新；M3 只承接触发②，N-7）"},
		{8, ObjectCard, FieldUpdatedAt, allow, allow, "由 CLI 在实际写入时更新，两条路径无差别"},
		{9, ObjectCard, FieldSources, allow, allow, "add_material_rel 追加，允许目标为 deprecated 卡"},
		{10, ObjectCard, FieldRelationsAdd, allow, allow, "add_relation / eg rel add"},
		{11, ObjectCard, FieldRelationsRemove, deny, allow,
			"eg rel remove / remove_relation；Agent 自动路径不得删关系（N-3）"},
		{12, ObjectCard, SectionField(store.SecKnowledge), Cell{VerdictDeny, VerdictAllow}, allow,
			"**唯一的条件解锁行**：P-A 对已有卡 🔴、create_card 新建 ✅；" +
				"P-U ✅ 的载体是 eg edit（A-13），属 T-…-045"},
		{13, ObjectCard, SectionField(store.SecRationale), allow, allow, "P-A 只追加块"},
		{14, ObjectCard, SectionField(store.SecBoundary), allow, allow, "P-A 只追加块"},
		{15, ObjectCard, SectionField(store.SecUserAppend), deny, deny,
			"CLI 写入路径永不写（B2 / E6「任何时候」）；仅用户直接编辑 Markdown"},
		// —— §2.2 知识卡 k- 续（3 行）——
		{16, ObjectCard, FieldSelfCheckAppend, allow, allow, "只追加"},
		{17, ObjectCard, FieldSelfCheckReplace, allow, allow,
			"M3 起允许：replace_block op + base_block_hash；历史记录块只追加、永不改写"},
		{18, ObjectCard, FieldSixthH2, deny, allow, "用户自建分区原样保留：不报错、不删除、不重排、不写入其中"},
		// —— §2.3 材料笔记 n-（9 行）——
		{19, ObjectNote, FieldID, allow, deny, "P-A 仅 write_note 新建时；改已有 ID 不行（N-2）"},
		{20, ObjectNote, FieldTags, allow, allow, "新建时 write_note.tags"},
		{21, ObjectNote, FieldDeleteMark, deny, allow, "须已批准提案（eg delete）"},
		{22, ObjectNote, FieldReviewedAt, deny, allow, "仅 eg mark-reviewed"},
		{23, ObjectNote, SectionField(store.SecDigest), allow, allow, "write_note.sections"},
		{24, ObjectNote, SectionField(store.SecAgentReview), allow, allow, "write_note.sections"},
		{25, ObjectNote, SectionField(store.SecUserAppend), deny, deny,
			"CLI 永不写；仅用户直接编辑（B2 / EG-NOTE-02）"},
		{26, ObjectNote, SectionField(store.SecOpenQuest), allow, allow,
			"add_open_question 追加；B2 要求重新加工时逐字保留"},
		{27, ObjectNote, SectionField(store.SecOutputCards), allow, allow, "五分区之一"},
		// —— §2.4 原文 s-（4 行）——
		{28, ObjectSource, FieldSourceIdentity, allow, deny, "P-A 仅 add_source 首次收录；不得改已有 ID"},
		{29, ObjectSource, FieldSourceBody, deny, deny, "收录后不因任何加工被改写；CLI 无改写入口"},
		{30, ObjectSource, FieldSourceReasons, allow, allow, "命中判重时 --reason 追加"},
		{31, ObjectSource, FieldDeleteMark, deny, allow, "须已批准提案"},
		// —— §2.5 主题综述 r-（3 行）——
		{32, ObjectReview, FieldReviewWhole, deny, allow,
			"仅用户明确要求；载体 eg review save 归属未知（A-18），M3 不实现"},
		{33, ObjectReview, FieldReviewStale, allow, deny,
			"M4 · A-34：由对账（R6）计算并自动写入，无需 --user-request；" +
				"写口仍唯一（ChangePlan → internal/plan → store 第五形态 SetStale）。" +
				"P-U 侧一格未动（仍 🔴）：本次只净放开 A-34 授权的 P-A 那一格"},
		{34, ObjectReview, FieldDeleteMark, deny, allow, "须已批准提案"},
		// —— §2.6 提案 p-（5 行）——
		{35, ObjectProposal, FieldProposalNew, allow, allow,
			"允许 Agent 调用 eg proposal new（§10.3「可提不可执」；§2.9 锁定）"},
		{36, ObjectProposal, FieldProposalApproved, deny, allow,
			"eg proposal approve --confirm；V9 要求 initiator=user，error 级不随宽松口径放宽（U-12）"},
		{37, ObjectProposal, FieldProposalRejected, deny, allow, "eg proposal reject --reason / 两触发自动改判"},
		{38, ObjectProposal, FieldProposalDecision, deny, allow, "decision 是用户决定"},
		{39, ObjectProposal, FieldProposalExecution, deny, deny,
			"CLI 独占：execution 是实际执行结果，任何路径的手工改写都不被采信（N-4）"},
		// —— §2.7 工程文件与 Git（4 行）——
		{40, ObjectProject, FieldConfigYML, deny, allow, "eg config set"},
		{41, ObjectProject, FieldSkillMD, deny, report,
			"eg init 生成；用户可编辑但不得承载状态或数据（🟡：允许但必须进报告提示）"},
		{42, ObjectProject, FieldUnprocessed, allow, allow, "add_source 登记 / write_note 成功即移出"},
		{43, ObjectGit, FieldGitHistory, allow, allow, "CLI 一次 apply 一次 commit；用户可自行 git add / commit"},
	}
}

// MatrixCells 是判定格总数 = 行数 × 路径数（合同 §2.8：43 × 2 = 86）。
// 由两个集合的长度相乘得到，**不写死 86**。
func MatrixCells() int { return len(Matrix()) * len(Paths()) }

// MatrixObjects 返回矩阵覆盖的对象类（按首次出现顺序去重；合同 §2.8：恰 7）。
// 由行数据派生，**不另维护一份七值清单**——两份清单必然有一天对不上。
func MatrixObjects() []Object {
	seen := map[Object]bool{}
	var out []Object
	for _, row := range Matrix() {
		if seen[row.Object] {
			continue
		}
		seen[row.Object] = true
		out = append(out, row.Object)
	}
	return out
}

// StrictUnlockRows 是**严格解锁行**：P-A 格含 🔴 **且不含** ✅ **且** P-U 格含 ✅
// （合同 §2.8 计数规则逐字）。
//
// 注意 #41 的 P-U 是 🟡 不是 ✅，**不计入**；#12 的 P-A 同时含 🔴 与 ✅，
// 归 ConditionalUnlockRows，也**不计入**。
func StrictUnlockRows() []MatrixRow {
	var out []MatrixRow
	for _, row := range Matrix() {
		if row.Auto.Has(VerdictDeny) && !row.Auto.Has(VerdictAllow) && row.User.Has(VerdictAllow) {
			out = append(out, row)
		}
	}
	return out
}

// ConditionalUnlockRows 是**条件解锁行**：P-A 格**同时**含 🔴 与 ✅（按子情形分叉）
// **且** P-U 格含 ✅（合同 §2.8）。这一行才是 §16.1 M3 判据的唯一落点。
func ConditionalUnlockRows() []MatrixRow {
	var out []MatrixRow
	for _, row := range Matrix() {
		if row.Auto.Has(VerdictDeny) && row.Auto.Has(VerdictAllow) && row.User.Has(VerdictAllow) {
			out = append(out, row)
		}
	}
	return out
}

// BothDeniedRows 是**两条路径同为 🔴 的行**（授权也解不开，合同 §2.8）：
// 两格均含 🔴 且均不含 ✅。
func BothDeniedRows() []MatrixRow {
	var out []MatrixRow
	for _, row := range Matrix() {
		if row.Auto.Has(VerdictDeny) && !row.Auto.Has(VerdictAllow) &&
			row.User.Has(VerdictDeny) && !row.User.Has(VerdictAllow) {
			out = append(out, row)
		}
	}
	return out
}

// LookupRow 按「对象 + 字段逐字」精确查表。查不到返回 false——
// 调用方**必须**按「拒绝」处理（矩阵是封闭 43 行，查不到说明调用点与合同脱节）。
func LookupRow(obj Object, field string) (MatrixRow, bool) {
	for _, row := range Matrix() {
		if row.Object == obj && row.Field == field {
			return row, true
		}
	}
	return MatrixRow{}, false
}

// RowNum 按行号取行（供 §2.9 锁定条款与用例逐格断言）。
func RowNum(num int) (MatrixRow, bool) {
	for _, row := range Matrix() {
		if row.Num == num {
			return row, true
		}
	}
	return MatrixRow{}, false
}

// LockedCell 是 §2.9「锁定条款」钉死的一个判定格。
type LockedCell struct {
	Num  int
	Path Path
	Want Verdict
	// Why 是该格不得翻转的理由（owner 2026-09-02 对 A-23 的裁决第 5 条）。
	Why string
}

// AdjudicationLockedRows 是 §2.9 锁定条款覆盖的**五个矩阵行**（#35–#39）。
func AdjudicationLockedRows() []int { return []int{35, 36, 37, 38, 39} }

// AdjudicationLockedCells 把 owner 裁决第 5 条「Agent 可创建提案，但不可批准或执行」
// 在矩阵上的落点逐格钉死（合同 §2.9「锁定条款」）。
//
// #39 两格各锁一次，故本表条目数 = 5 行 + 1（#39 的 P-U 侧）= 6 个判定格。
// 任何一次符号翻转都会同时改变 §2.8 的严格解锁行计数，两处断言互为兜底。
func AdjudicationLockedCells() []LockedCell {
	return []LockedCell{
		{35, PathAgent, VerdictAllow, "不得由 ✅ 改 🔴：那会取消「Agent 可创建提案」，与裁决第 1 句冲突"},
		{36, PathAgent, VerdictDeny, "不得由 🔴 改 ✅：那会让 Agent 自批提案，违反 U-12 与 V9"},
		{37, PathAgent, VerdictDeny, "不得由 🔴 改 ✅：拒绝与改判同样是用户决定"},
		{38, PathAgent, VerdictDeny, "不得由 🔴 改 ✅：裁决结论字段属用户决定"},
		{39, PathAgent, VerdictDeny, "execution 恒为 CLI 回写（N-4）"},
		{39, PathUser, VerdictDeny, "execution 恒为 CLI 回写，本格严于裁决，按「只加严不放宽」保留"},
	}
}
