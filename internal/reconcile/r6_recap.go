package reconcile

// R6 —— 综述可能失准标记的**只读**判定（对账合同 §9；M4 · T-…-055 阶段 2）。
//
// 唯一职责：把「这篇主题综述所引用的知识卡里，至少有一张已经变了」这件落盘事实，
// 判定成一条 `recap_stale` finding（**W19 / warning**，分级与诊断码一律取自 check.go 的
// 唯一真源表）+ **恰一条** RepairSpec（待写键集合恒两键，见 recapStaleKeyTable）。
//
// # M4 唯一触发条件（合同 §9 逐字，**不得自行扩张**）
//
// 综述所引用的知识卡中，**存在**至少一张满足下列任一条的卡：
//
//	① 该卡的 `updated_at` **晚于**综述的 `updated_at`；
//	② 该卡被**逻辑删除**（`deleted_at` 非空）或**失效**（`status: deprecated`）。
//
// 两条是**析取**（任一条成立即命中），一条都不成立即不命中。合同已把 R6 在 M4 收窄到
// 这**一条**触发条件：**其余失准情形（语义漂移、覆盖不足等）明确不命中**——综述标题 /
// 正文 / 标签怎么改、库里新增了多少张没被引用的卡、引用集合覆盖得够不够全，
// 在本文件里一律不是判据（反向行见 r6_recap_test.go 的 TestR6SingleTriggerOnly）。
//
// # `stale_reason` 的封闭三值与「取第一个命中值」（可复算）
//
// 三个取值逐字是 `引用卡已更新` / `引用卡已逻辑删除` / `引用卡已失效`，
// **真源恒是 model 的封闭枚举**（`model.ValidStaleReasons()`）——本文件一个取值字面量都不写、
// 也不写死顺序：判定直接**按真源切片的声明顺序**逐个求值，第一个命中的因就是最终取值
// （合同 §9「多因并存时按此顺序取第一个命中值」）。于是三件事同时成立：
//
//   - 取值集合封闭：第四种理由在本文件结构上产不出来（求值只遍历真源三值）；
//   - 顺序即真源：想改优先级只能去改 model 的声明顺序，两处口径不可能分叉；
//   - 可复算：同一份输入恒得同一个取值，与卡的扫描序、引用书写序无关。
//
// # 本文件**不做**的事（结构上做不到，不靠自律）
//
//   - **不写盘、不发提交、不起子进程**：本文件不引 os 的任何写 API、不引子进程包，
//     也**不持有** Git 仓库句柄或落盘层句柄。R6 的动作是「改综述的 frontmatter」，
//     因此它**只**产出 RepairSpec（修复意向的**描述**），真正的落盘由命令层编排成
//     **内存 ChangePlan**、经计划层的完整校验链交落盘层写入（合同 §1.3 写口归属表第 3 行）。
//   - **永不重算综述**：本包读不到综述正文——RecapFact 只有 ID / 路径 / `updated_at` /
//     引用卡清单 / 落盘标记五组事实，**没有正文、没有原始字节、没有文档句柄**，
//     「重算一篇综述」在本文件里不可表达（反证见 r6_recap_test.go 的第 5 组用例）。
//   - **永不自动清除 `stale`**：不命中即**零 finding、零 RepairSpec**，不会产出任何
//     「把标记去掉」的意向；待写键集合恒是那两个键、值恒是「标记 + 三值之一」。
//     清除属用户重新生成综述的路径，M4 不做（合同 §9 末条）。
//   - **不豁免 B3**：写前内容比对由写入侧负责，hash 过期属 `skipped[kind=file_changed]`；
//     「已跳过」如实追加进同一条 finding 的 detail 复用 R2 定稿的 WithSkipNotice，
//     本文件不另造第二套文案、不新增 kind（合同 §11 末条）。
//   - **不碰状态维度**：条件② 里的「失效」只是**读**扫描面已展开的失效位，
//     本文件一个字节都不写状态维度、也不引状态写口的任何符号（与 R7 同一条纪律）。
//   - 不判定 R1 / R2 / R3 / R4 / R5 / R7 任何一项，不注册任何命令、不动报告体。
//
// # 「综述引用了哪些知识卡」这一事实：复用既有底座，不另造第二套遍历
//
// 引用清单本身是综述 frontmatter 上的**取材卡清单**（S2 的既有键，语义与只读口径在
// 影响面重算里已定稿），以 Input.Recaps 快照的形态从**包外**采样进来（理由同 Sources /
// Edits / Renames：本包不另写扫描器、不持有落盘句柄）。清单里每一项的**形态与存在性**
// 一律走 T-…-053（R3）定稿的同一套口径，本文件零 ID 规则、零前缀字面量、零第二份索引：
//
//	形态合法性  →  ValidRelationTarget（整体委派 model 的现成 ID 校验）
//	存在性      →  StructureIndex.Has(id, KindCard)（R4 建的同一份只读索引，R3 也在用）
//	卡的事实    →  同一份 query.ScanResult 扫描面（`updated_at` / 删除维度 / 失效位）
//
// **未采样即不判**：Input.Recaps 为 nil（综述分区未采样）或 Input.Scan 为 nil（未扫描）时
// R6 整体零产出 —— 绝不把「没采样」当成「原文不存在」或「引用集合为空」
// （诚实性口径与 R4 两条源侧判定、R7 的 `sources/` 未采样逐字同源）。
//
// # 与 R1–R5 / R7 不重复计数（同一件落盘事实只许一个码报一次）
//
//   - **引用项形态非法 / 目标不在库**：这两类是**引用与结构域**的事实 —— 落在知识卡的
//     `relations[]` 上时由 R3 的 `relation_prefix_invalid` / `relation_target_missing`
//     独家承载，落在卡 / 笔记 frontmatter 的两类引用上时由 R4 的 `dangling_ref` 独家承载。
//     综述 frontmatter 的取材卡清单**不在**合同 §3 十二值里任何一个 check 的判定面内
//     （M4 没给它分配码），故 R6 对这类引用项一律**只让位、不发码**：既不重复计数，
//     也不把「引用项本身有问题」误判成「综述失准」（后者的判据只有那唯一一条触发条件）。
//     让位不等于「永不报」——同一篇综述里只要还有一张可判定且命中的卡，W19 照报。
//   - **同一卡 ID 落在 ≥ 2 个文件**：那是 R4 的 `duplicate_id`（E11）这**一件**事实的投影，
//     两份文件上的 `updated_at` / 删除维度可能分叉，「哪一份是事实」不是可复算的结论 ——
//     故该引用项整体让位给 E11（口径与 R5 / R7 的同一条让位逐字相同）。
//   - **R1（W13）/ R2（W14）/ R5（W18）**：三者分别看未提交改动、过目信号滞后、跨领域移动
//     —— 本文件不读 Git 未提交状态快照、不读 Input.Edits、不读 Input.Renames 一个字节。
//   - **R4 的 `orphan`（W17）/ R7 的 `support_insufficient`（W20）**：那两项报的是**知识卡**
//     的关系面 / 材料面，R6 报的是**综述**这一篇产物的失准标记 —— 判定面（对象类）不同、
//     targets 不同、写入面不同（R6 是产 RepairSpec 的两项之一，W17 / W20 恒零 RepairSpec），
//     同一份输入里并存**不是**重复计数（逐条反证见 TestR6NoDoubleCountWithOtherChecks）。

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
)

// R6 是本检查项的 R 编号（与 checkTable 内 CheckRecapStale 行的 R 列同值）。
const R6 = "R6"

// RecapStaleKeyCount 是 R6 允许写的 frontmatter 键数：**恰 2**（逐键封闭，合同 §9）。
const RecapStaleKeyCount = 2

// recapStaleKeyTable 是待写键的封闭全集。类型是**长度固定的数组**而不是 slice：
// 想在 R6 的修复面里顺手加第三个键（例如状态维度或删除维度那格），编译期就过不去。
//
// 两个键名一律取自 model 的键名常量 —— 键名字面量在全仓**只有一处**（那条判据由
// model 侧的 TestStaleKeyLiteralsAppearOnce 机械锁死），本文件因此零键名字面量。
var recapStaleKeyTable = [RecapStaleKeyCount]string{model.FMKeyStale, model.FMKeyStaleReason}

// RecapStaleKeys 返回封闭待写键集合的副本（恒恰两个元素，去重 + 升序）。
func RecapStaleKeys() []string {
	out := make([]string, 0, RecapStaleKeyCount)
	out = append(out, recapStaleKeyTable[:]...)
	return NormalizeTargets(out)
}

// RecapStaleReasonCount 是 `stale_reason` 的封闭基数：**恰 3**（合同 §9）。
//
// 只作反证锚点：取值与顺序的真源恒是 model 的封闭枚举，本常量不参与任何判定
// （若真源哪天多出第四个取值，TestR6StaleReasonClosedThreeValues 立刻变红）。
const RecapStaleReasonCount = 3

// RecapStaleReasons 转手封闭三值（顺序 = 合同 §9 的首个命中顺序，调用方不得重排）。
func RecapStaleReasons() []model.StaleReason { return model.ValidStaleReasons() }

// 引用项**不可判定**的封闭三值让位原因（进 detail，供逐条复算；不是新 check、不是新诊断码）。
const (
	// RecapRefPrefixInvalid 引用项不满足既有 ID 规则（形态 / 前缀）—— R6 只让位，不发码。
	RecapRefPrefixInvalid = "ref_prefix_invalid"
	// RecapRefMissing 引用项形态合法但该知识卡在 vault 内查无此对象 —— R6 只让位，不发码。
	RecapRefMissing = "ref_missing"
	// RecapRefDuplicateID 引用的卡 ID 落在 ≥ 2 个文件 —— 让位 R4 的 E11（事实分叉，不可复算）。
	RecapRefDuplicateID = "ref_duplicate_id"
)

// RecapYieldReasonCount 是让位原因的封闭基数：恰 3。
const RecapYieldReasonCount = 3

// recapYieldTable 是让位原因的封闭全集（长度固定数组：第四个原因加不进来），
// 顺序 = 判定的求值行序，同时是 detail 内的渲染顺序。
var recapYieldTable = [RecapYieldReasonCount]string{
	RecapRefPrefixInvalid,
	RecapRefMissing,
	RecapRefDuplicateID,
}

// RecapYieldReasons 返回封闭三值让位原因的副本（顺序即求值行序）。
func RecapYieldReasons() []string {
	out := make([]string, 0, RecapYieldReasonCount)
	out = append(out, recapYieldTable[:]...)
	return out
}

// IsKnownRecapYieldReason 报告取值是否落在封闭三值内（第四个取值一律 false）。
func IsKnownRecapYieldReason(s string) bool {
	for _, v := range recapYieldTable {
		if v == s {
			return true
		}
	}
	return false
}

// recapYieldLabel 是让位原因的中文标签（只用于 detail 渲染，不参与任何判定）。
var recapYieldLabel = map[string]string{
	RecapRefPrefixInvalid: "引用项不满足既有 ID 规则（形态类事实不由 R6 发码）",
	RecapRefMissing:       "引用的知识卡在 vault 内不存在（引用类事实不由 R6 发码）",
	RecapRefDuplicateID:   "引用的卡 ID 落在多个文件（该事实归 R4 的 duplicate_id）",
}

// recapReasonLabel 是三个触发因的**判据说明**（只用于 detail 渲染，不参与任何判定）。
// 键是 model 的封闭三值，本文件因此不写任何取值字面量。
var recapReasonLabel = map[model.StaleReason]string{
	model.StaleReasonUpdated:    "该卡的 updated_at 晚于综述的 updated_at",
	model.StaleReasonDeleted:    "该卡已被逻辑删除（删除维度的落盘键非空）",
	model.StaleReasonDeprecated: "该卡已失效（状态维度为 deprecated）",
}

// RecapFact 是一篇主题综述的只读落盘事实（**纯数据**：无函数字段、无句柄、无写方法）。
//
// 为什么由调用方采样后传进来：M2 的扫描底座只覆盖知识卡与材料笔记两类对象，
// 综述分区不在其内，而本包**不许另写扫描器**（合同 §17 第 7 条）。
// **nil = 该分区未采样**：R6 整体不判定（见 RecapTargets 的两条前提）。
//
// 字段刻意只有五组可复算事实 —— **没有正文、没有原始字节、没有文档句柄**：
// 「重算综述」在本包内因此不可表达（合同 §9「不自动重算综述」的结构性落点）。
type RecapFact struct {
	// ID 是综述 ID（`r-` 前缀），逐字取自 frontmatter，不做任何归一化。
	ID string
	// Path 是 vault 相对路径（/ 分隔），与 query 扫描面的 Path 口径一致。
	Path string
	// UpdatedAt 是综述 `updated_at` 的**逐字原值**（缺省即空串，不回填默认值）。
	UpdatedAt string
	// SourceCards 是综述引用的知识卡 ID 清单的**逐字原值**（顺序即落盘序，不做过滤）。
	SourceCards []string
	// Marked 是失准标记键的落盘原值（`true` 即已标记）；缺省即 false。
	Marked bool
	// MarkedReason 是失准理由键的**逐字原值**（缺省即空串，不代入默认理由）。
	MarkedReason string
}

// RecapCauseHit 是一条「触发因 → 命中该因的引用卡」的可复算事实。
type RecapCauseHit struct {
	// Reason 是封闭三值之一（真源 = model 的封闭枚举）。
	Reason model.StaleReason
	// Cards 是命中该因的引用卡 ID（去重 + 升序，恒非空）。
	Cards []string
}

// RecapYield 是一条被让位（不参与 R6 判定）的引用项。
type RecapYield struct {
	// Ref 是引用项的逐字原值。
	Ref string
	// Why 是让位原因（封闭三值之一，见 recapYieldTable）。
	Why string
}

// RecapTarget 是一篇命中 R6 的综述的只读判定事实（**纯数据**：无函数字段、无句柄、无写方法）。
//
// 为什么导出：写入侧（命令层）需要「综述 ID + 落盘路径 + 那个封闭三值」才能编排 ChangePlan，
// 而 RepairSpec 按合同只承载 `check / path / keys / reason` 四键、不带 ID 也不带机读取值
// （体例与 R2 的 ReviewedTarget 逐字相同：**不扩 RepairSpec 的键**，多出来的事实走这里）。
// 两者同源同事实：RecapTargets 与 checkR6RecapStale 走的是同一个判定函数。
type RecapTarget struct {
	// ID / Path 是综述的两个可定位标识（进 finding 的 targets）。
	ID   string
	Path string
	// UpdatedAt 是综述 `updated_at` 的逐字原值（进 detail，供逐条复算）。
	UpdatedAt string
	// Reason 是最终取值 = **第一个**命中的因（顺序 = 封闭三值声明顺序）。
	Reason model.StaleReason
	// Hits 是全部命中的因（封闭三值的子集，顺序 = 封闭三值声明顺序，恒非空）。
	// Reason 恒等于 Hits[0].Reason —— 「取第一个」在结构上就是取这里的第一项。
	Hits []RecapCauseHit
	// Refs 是引用清单去重 + 升序后的全集（进 detail：引用了几张）。
	Refs []string
	// Judged 是**参与判定**的引用卡（形态合法 + 在库 + 非重复 ID），去重 + 升序。
	Judged []string
	// Yielded 是让位的引用项（按引用项升序），每条带封闭三值之一的原因。
	Yielded []RecapYield
	// AlreadyMarked 报告落盘上是否**已是**该标记且理由**逐字相同**。
	// 为 true 时写入侧零写入、零 commit（幂等），但 **finding 仍产出**（合同 §9）。
	AlreadyMarked bool
}

// RecapTargets 返回全部命中 R6 唯一触发条件的综述（按 (path, id) 升序，可逐字复算）。
//
// 纯函数：同一 Input 恒得同一输出；不改入参、不做任何 IO。
// 两条「不判」的前提（返回空集合，绝不拿缺失的事实当证据）：
//   - Scan 为 nil：没扫描 → 拿不到任何引用卡的落盘事实；
//   - Recaps 为 nil：综述分区未采样 → 不得把「没采样」当成「没有综述」或「引用集合为空」。
func RecapTargets(in Input) []RecapTarget {
	if in.Scan == nil || in.Recaps == nil {
		return nil
	}
	x := NewStructureIndex(in)
	cards := recapCardIndex(in.Scan)
	out := make([]RecapTarget, 0, len(in.Recaps))
	seen := make(map[string]bool, len(in.Recaps))
	for _, r := range in.Recaps {
		id, path := strings.TrimSpace(r.ID), strings.TrimSpace(r.Path)
		if id == "" || path == "" {
			continue // 空 ID / 空路径的综述无法定位，对账域不给它发 finding
		}
		key := id + "\x00" + path
		if seen[key] {
			continue // 同一份落盘文件只判一次（采样重复不产生第二条 finding）
		}
		seen[key] = true
		if t, ok := recapHit(r, cards, x); ok {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// recapHit 是唯一触发条件的**唯一**实现（析取；一条因都不成立即 false）。
func recapHit(r RecapFact, cards map[string]query.CardEntry, x StructureIndex) (RecapTarget, bool) {
	t := RecapTarget{
		ID:        strings.TrimSpace(r.ID),
		Path:      strings.TrimSpace(r.Path),
		UpdatedAt: strings.TrimSpace(r.UpdatedAt),
		Refs:      NormalizeTargets(r.SourceCards),
	}
	byReason := make(map[model.StaleReason][]string, RecapStaleReasonCount)
	for _, ref := range t.Refs {
		c, why, ok := recapJudgeableCard(ref, cards, x)
		if !ok {
			t.Yielded = append(t.Yielded, RecapYield{Ref: ref, Why: why})
			continue
		}
		t.Judged = append(t.Judged, ref)
		for _, cause := range RecapCauses(c, t.UpdatedAt) {
			byReason[cause] = append(byReason[cause], ref)
		}
	}
	// 顺序 = 封闭三值的**声明顺序**（真源在 model）：命中集合因此天然按合同 §9 排列。
	for _, reason := range model.ValidStaleReasons() {
		if hit := NormalizeTargets(byReason[reason]); len(hit) != 0 {
			t.Hits = append(t.Hits, RecapCauseHit{Reason: reason, Cards: hit})
		}
	}
	if len(t.Hits) == 0 {
		// 一条因都不成立：**零 finding、零 RepairSpec** —— 于是「不自动清除标记」在结构上
		// 成立（本包产不出任何「把标记去掉」的意向），干净库上重跑也零写入零 commit。
		return RecapTarget{}, false
	}
	t.Reason = t.Hits[0].Reason
	t.AlreadyMarked = r.Marked && strings.TrimSpace(r.MarkedReason) == string(t.Reason)
	return t, true
}

// recapJudgeableCard 取一条引用项对应的**可判定**知识卡。
//
// 不可判定时返回封闭三值之一的让位原因（求值顺序 = recapYieldTable 行序，
// 一条引用项只归因**第一个**命中的原因，可复算）：形态非法 → 不在库 → 同 ID 多文件。
// 三种情形一律只让位、不发码（诊断码归属见本文件顶部「不重复计数」一节）。
func recapJudgeableCard(ref string, cards map[string]query.CardEntry,
	x StructureIndex) (query.CardEntry, string, bool) {
	switch {
	case !ValidRelationTarget(ref):
		return query.CardEntry{}, RecapRefPrefixInvalid, false
	case !x.Has(ref, KindCard):
		return query.CardEntry{}, RecapRefMissing, false
	case len(x.ObjectPaths[ref]) >= 2:
		return query.CardEntry{}, RecapRefDuplicateID, false
	}
	c, ok := cards[ref]
	if !ok {
		// 索引里有、扫描面上取不到（空 ID 之类）：同样按「不在库」如实让位，不猜。
		return query.CardEntry{}, RecapRefMissing, false
	}
	return c, "", true
}

// RecapCauses 返回该引用卡命中的**全部**触发因（封闭三值的子集，顺序 = 三值声明顺序）。
//
// 纯函数：不改入参、零 IO。求值只遍历 model 的封闭三值 —— 第四种理由在结构上产不出来。
func RecapCauses(c query.CardEntry, recapUpdatedAt string) []model.StaleReason {
	out := make([]model.StaleReason, 0, RecapStaleReasonCount)
	for _, reason := range model.ValidStaleReasons() {
		if recapCauseHolds(reason, c, recapUpdatedAt) {
			out = append(out, reason)
		}
	}
	return out
}

// recapCauseHolds 是三条触发因的**唯一**判定实现（封闭三值逐个求值；表外取值恒 false）。
//
// 三条的诚实性口径：
//   - 「更新」是**严格晚于**：两个时刻缺一个、或任一个读不成时刻 → **不命中**
//     （合同 §9 的判据是「晚于」，不是「不等于」；宁可不报也不误报，口径同 R2 的第 3 条）；
//   - 「逻辑删除」看删除维度的落盘键：扫描面已展开的删除位与键的非空原值**取或** ——
//     布尔位若因形态异常没展开，键非空仍如实命中（不漏报），但绝不由状态维度反推删除维度；
//   - 「失效」看状态维度**已展开**的失效位：本文件只读它，一个字节都不写状态维度，
//     也不引状态写口的任何符号（与 R7 同一条纪律，整包非测试源恒零命中）。
func recapCauseHolds(reason model.StaleReason, c query.CardEntry, recapUpdatedAt string) bool {
	switch reason {
	case model.StaleReasonUpdated:
		return recapOutdatedBy(c.UpdatedAt, recapUpdatedAt)
	case model.StaleReasonDeleted:
		return c.Deleted || strings.TrimSpace(c.DeletedAt) != ""
	case model.StaleReasonDeprecated:
		return c.Deprecated
	}
	return false
}

// recapOutdatedBy 判定条件①：引用卡的 `updated_at` 是否**严格晚于**综述的 `updated_at`。
//
// 任一侧为空或读不成时刻 → false（两个时刻缺一个就无法比对，不把缺事实当证据）。
// 相等 → false（合同逐字是「晚于」）。
func recapOutdatedBy(cardUpdatedAt, recapUpdatedAt string) bool {
	cs, err := model.ParseStamp(strings.TrimSpace(cardUpdatedAt))
	if err != nil {
		return false
	}
	rs, err := model.ParseStamp(strings.TrimSpace(recapUpdatedAt))
	if err != nil {
		return false
	}
	return rs.Before(cs)
}

// recapCardIndex 折出「知识卡 ID → 扫描面条目」的只读索引（同 ID 取扫描序第一份）。
//
// 扫描面已按 path 升序稳定排序，故「第一份」= 路径字典序最小者（与 M2「同 ID 重复时
// 按路径字典序取小者」的既有口径同源）。同 ID 多文件本身由 R4 的 E11 独家承载，
// R6 对这类引用项整体让位（见 recapJudgeableCard），因此这里取哪一份都不影响结论。
func recapCardIndex(scan *query.ScanResult) map[string]query.CardEntry {
	out := make(map[string]query.CardEntry, len(scan.Cards))
	for _, c := range scan.Cards {
		id := strings.TrimSpace(c.ID)
		if id == "" {
			continue // 缺 id 的文件已由扫描层记 Q1，对账域不给无法定位的对象发 finding
		}
		if _, ok := out[id]; !ok {
			out[id] = c
		}
	}
	return out
}

// —— finding / RepairSpec 的渲染 ——

// RecapIdempotentNotice 是「落盘已是同一标记与同一理由」的**固定文案**（措辞固定便于逐字断言）。
//
// 合同 §9 的幂等条：零写入、零 commit，**finding 仍产出** —— 不是删掉这条 finding、
// 也不是降级，只是在同一条 detail 里如实注明本次不会产生任何字节改动。
const RecapIdempotentNotice = "；落盘上**已是**同一标记与同一理由，故本次**零写入、零 commit**" +
	"（幂等），本条 finding 仍如实产出"

// r6Detail 渲染 W19 的 detail：综述 ID + 路径 + 两侧时刻 + 引用面统计 + 逐因命中卡 +
// 取值与取值顺序 + 让位清单 + 「不重算 / 不清除」的边界（足以逐条复算）。
func r6Detail(t RecapTarget) string {
	extra := ""
	if len(t.Yielded) > 0 {
		extra = fmt.Sprintf("；另有 %d 条引用项不参与判定（%s）", len(t.Yielded),
			strings.Join(recapYieldLabels(t.Yielded), "、"))
	}
	if t.AlreadyMarked {
		extra += RecapIdempotentNotice
	}
	return fmt.Sprintf(
		"主题综述 %s（%s，updated_at=%s）引用的 %d 张知识卡里有 %d 张参与判定，"+
			"其中命中失准触发因：%s；按封闭三值的声明顺序取**第一个**命中值 %q 作为失准理由"+
			"（合同 §9：多因并存时顺序固定、结论可复算）。本次只写失准标记那**两个**综述专属键，"+
			"正文与其余 frontmatter 一个字节都不动 —— **不重算综述、不清除标记**%s",
		t.ID, t.Path, stampOrNone(t.UpdatedAt), len(t.Refs), len(t.Judged),
		strings.Join(recapHitLabels(t.Hits), "；"), string(t.Reason), extra)
}

// r6Reason 渲染 RepairSpec 的 reason（与同一条 finding 的 detail 同源同事实）。
func r6Reason(t RecapTarget) string {
	return fmt.Sprintf("主题综述 %s 的引用卡%s（%s）：写失准标记两键，理由取封闭三值的首个命中值 %q",
		t.ID, recapReasonLabel[t.Reason], strings.Join(t.Hits[0].Cards, "、"), string(t.Reason))
}

// recapHitLabels 把逐因命中折成「判据说明（命中卡清单）」的中文串（顺序 = 三值声明顺序）。
func recapHitLabels(hits []RecapCauseHit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		label, ok := recapReasonLabel[h.Reason]
		if !ok {
			label = string(h.Reason) // 表外取值原样返回，保证 detail 恒非空
		}
		out = append(out, fmt.Sprintf("%s（%s）", label, strings.Join(h.Cards, "、")))
	}
	return out
}

// recapYieldLabels 把让位清单折成中文串（未知取值原样返回，保证 detail 恒非空）。
func recapYieldLabels(ys []RecapYield) []string {
	out := make([]string, 0, len(ys))
	for _, y := range ys {
		label, ok := recapYieldLabel[y.Why]
		if !ok {
			label = y.Why
		}
		out = append(out, fmt.Sprintf("%s：%s", y.Ref, label))
	}
	return out
}

// —— 检查项本体与注册 ——

// checkR6RecapStale 是 R6 检查项本体：每篇命中的综述产**恰一条** finding + **恰一条**
// RepairSpec（`targets` = [路径, 综述 ID] 去重升序；`keys` = 恰那两个综述专属键）。
//
// 纯函数：同一 Input 恒得同一输出（含顺序），不改入参、不产生任何副作用。
// 零命中 → 空集合：于是报告侧 `recap_stale` 条数为 0、写入侧零写入零 commit
// （干净库上重跑既不写盘也不产生空 commit，也不会去清除任何既有标记）。
func checkR6RecapStale(in Input) ([]Finding, []RepairSpec) {
	targets := RecapTargets(in)
	if len(targets) == 0 {
		return nil, nil
	}
	fs := make([]Finding, 0, len(targets))
	rs := make([]RepairSpec, 0, len(targets))
	for _, t := range targets {
		f, err := NewFinding(CheckRecapStale, []string{t.Path, t.ID}, r6Detail(t))
		if err != nil {
			// 只有「未知 check / 空 detail」两种构造错误，两者在本文件都不可能发生
			// （check 取自封闭表常量、r6Detail 恒产非空串）。防御性丢弃而不 panic：
			// 对账是只读检查，任何情况下都不该让进程死在检查器里。
			continue
		}
		r, err := NewRepairSpec(CheckRecapStale, t.Path, RecapStaleKeys(), r6Reason(t))
		if err != nil {
			continue
		}
		fs = append(fs, f)
		rs = append(rs, r)
	}
	return fs, rs
}

// 注册：R6 是本包第七个落地的检查项（R1 / R2 / R4 / R3 / R5 / R7 分属 T-…-050 / 051 /
// 052 / 053 / 054 / 056），也是**第二个产 RepairSpec** 的检查项（另一个是 R2）。
//
// 注册表 checkers 在 reconcile.go 内声明，一个 task 只在自己的文件里追加自己那一项 ——
// 本文件因此**恰追加一项**（注册项数的加法等式见 r1_git_test.go 的自守用例）。
func init() { checkers = append(checkers, checkR6RecapStale) }

// R6ScanOf 是给命令层与用例的便利函数：把扫描快照 + 综述分区快照折成 Input。
//
// 存在的理由同 R2ScanOf / R7ScanOf：R6 只需要「扫描结果 + 综述分区快照」两样，
// 不需要 Git 未提交状态快照（恒零值）、不需要编辑与 rename 事实（Edits / Renames 恒 nil）
// —— 但扫描底座仍复用 internal/query，本包不另写扫描器（合同 §0.1 第 1 条）。
// recaps 传 nil 表示该分区未采样：R6 整体不判定。
func R6ScanOf(vaultRoot string, scan *query.ScanResult, recaps []RecapFact) Input {
	return Input{VaultRoot: vaultRoot, Scan: scan, Recaps: recaps}
}
