package reconcile

// R7 —— 知识卡材料支撑不足的**只读实时判定**（对账合同 §10；M4 · T-…-056）。
//
// 注：本文件（以及整个对账包、落盘层）一次都不出现人类可读的那句方括号视图提示 ——
// 那句提示只属于命令层的对账渲染文件，只进 stdout。见下面「实时判定 = 零落盘」。
//
// 唯一职责：把「这张知识卡的**有效 support 材料关系数为 0**」这件事实，实时判定成一条
// `support_insufficient` finding（**W20 / warning**，分级与诊断码一律取自 check.go 的
// 唯一真源表），`targets[]` = `[知识卡 ID]`。
//
// # 判定（合同 §10 逐字）
//
//	有效 support 数 == 0  →  产出 support_insufficient
//	有效 = 该条 support 的**对端存在** 且 **未逻辑删除**
//
// 判定面 = 知识卡 frontmatter `sources[]` 里 `rel` 取 `support` 的条目：
// `against` / `context` 不是「支持」，不参与本判定（材料关系取值一律走 `internal/model`
// 的封闭三值，本文件零关系字面量）。「有效性」的两个对端各判各的落盘事实：
//
//   - **来源笔记端**（`sources[].note`）：既判存在性（本包索引的 note 类别），又判逻辑删除
//     （扫描面 `Deleted`，即 `deleted_at` 非空这一维度的展开）。同一笔记 ID 落在多个文件时，
//     **只要有一份未被逻辑删除就算对端仍在**（「重复 ID 里的一份被删」不等于对端消失，
//     重复本身由 R4 的 E11 独家承载）。
//   - **原文端**（`sources[].source`）：只判存在性。为什么不判逻辑删除：M3 的落盘模型里
//     删除维度只落在知识卡 / 材料笔记 / 提案三类产物上，`internal/model` 的原文结构体
//     **没有** `deleted_at` 键 —— 这不是把判据放宽一格，而是那条事实在落盘模型里不存在，
//     本文件如实不判（不把缺事实当证据，也不自己发明一个原文删除维度）。
//
// # 实时判定 = 零落盘（合同 §10 第 2 条）
//
//   - **永不改 `status`**：本文件**一个字节都不读、也不写**知识卡的状态维度 —— 卡是
//     `active` 还是失效，对 R7 的判定与输出没有任何影响（反证：r7_support_test.go 的
//     TestR7NeverTouchesStatus 既做「改状态 → 输出逐字不变、入参逐字不变」的单测，
//     又对本文件做「状态写口符号族零命中」的源码级 grep；状态维度的两个写口符号因此在
//     整包非测试源里恒 0）。
//   - **不落盘任何标记**：R7 不写 frontmatter、不写正文、不写任何文件，因此**零 RepairSpec**
//     （合同 §1.3 写口归属表第 4 行「R3 / R4 / R5 / R7：无人写」）。人类可读的视图提示
//     只在命令层的对账渲染文件里产出（`reconcile_render.go`，只进 stdout），
//     本包内该提示字符串一次都不出现。
//   - 不写盘、不发提交、不起子进程：本文件不引 os 的任何写 API、不引子进程包，
//     也不持有 Git 仓库句柄或落盘层句柄。
//
// # 本文件**不做**的事（结构上做不到，不靠自律）
//
//   - 不改任何状态、不写 `deprecated`、不写 `stale`（后者属 R6 / T-…-055）。
//   - 不判定 R1 / R2 / R3 / R4 / R5 / R6 任何一项，不注册任何命令、不动报告体
//     （归 T-…-058 / T-…-059 / T-…-057）。
//   - 不改写、不复用 M3 删除路径那张材料建议清单（报告包内按「本次删了谁」生成的建议表，
//     键名与文案是 M3 对外合同，见下面的路径分治）。
//   - 不引入第 13 个 `check`、不引入 `W21`、不引入信息级 severity。
//
// # 与 M3 删除路径那张材料建议清单的路径分治（A-28，合同 §10 第 3 条）
//
// 两者是**两套东西**，字段不同、路径不同、触发条件不同，互不覆盖：
//
//	M3 删除路径：报告包里那张**建议清单**（键名与两条建议文案都是 M3 对外合同，
//	            验收脚本用 jq 直读），只为「support 端点被**本次删除**影响到」的卡产出，
//	            语义是「建议标记 / 建议复核」，是删除命令报告体的一部分；
//	R7 对账域：  本文件的 support_insufficient finding —— 四键 schema 的**只读 finding**，
//	            与「本次删了谁」无关的**全库实时判定**，语义是「当前零有效材料支撑」，
//	            只报告 / 只做视图提示。
//
// 本包因此**不 import** 报告包（合同 §1.2 的依赖方向硬约束），本包全部源文件里也一次都不
// 出现那张清单的结构体名、键名与两条建议文案（反证：r7_support_test.go 的
// TestR7DisjointFromDeleteSupportCheck 对本包做符号族 grep，并逐字回读报告包那两条文案
// 仍在原处、一字未动）。
//
// # 与 R1 / R2 / R3 / R4 / R5 不重复计数
//
//   - **R1（W13）/ R2（W14）/ R5（W18）**：三者分别看未提交改动、过目信号滞后、跨领域移动
//     —— 本文件不读 Git 未提交状态快照、不读 `Input.Edits`、不读 `Input.Renames` 一个字节，
//     同一份输入里各报各自那一件事实。
//   - **R3（四个 relation 码）/ R4 的 `orphan`（W17）**：那两项看的是**论证关系** `relations[]`，
//     R7 看的是**材料关系** `sources[]` —— 两个不同的 frontmatter 键、两件不同的落盘事实。
//     一张「零关系又零材料支撑」的卡因此**同时**得到 W17 与 W20 各一条，这不是重复计数：
//     两条 finding 的事实来源字段不同（反证见 TestR7NoDoubleCountWithR4Orphan，同时锁住
//     「R7 的输出与 `relations[]` 完全无关」）。
//   - **R4 的 `duplicate_id`（E11）**：同一卡 ID 落在 ≥ 2 个文件时，**R7 整体跳过该 ID**
//     —— 那些文件的支持面差异只是 ID 冲突这**一件**事实的投影，归 E11 独家承载
//     （口径与 R5 逐字相同，见 r5_domain.go 的同一条让位）。
//   - **R4 的 `dangling_ref`（E12）**：`card.sources[].note` 指向的来源笔记缺失、
//     `card.sources[].source` 指向的原文缺失，**均已由 E12 独家承载**
//     （前者是历史合同 §6.2 第 ② 类；后者由 I-evergreen.system_assurance-158614-019
//     的实现侧完整修复纳入 E12 的第 ③ 类）。故本文件对「无效原因里含来源笔记缺失
//     **或**原文端缺失」的卡**让位不报 W20**：同一件落盘事实不许 error 与 warning 各记一次
//     （T-…-053 在 W16 侧、T-…-054 在 W18 侧踩过的同一个坑，修法同样是**收紧新检查器的
//     阈值**，而不是去放宽别人的判据）。让位不等于「永不报」：对端**存在但已逻辑删除**、
//     或材料四要素不全、或压根没有 support 条目时，W20 照报 —— 反证见
//     TestR7NoDoubleCountWithR4DanglingRef 的正反两向用例。
//
// # 取数前提：`sources/` 分区必须已采样
//
// 「对端存在」要同时查笔记与原文两类对象，而 M2 的扫描底座只覆盖 knowledge / notes 两类，
// 原文分区以 `Input.Sources` 快照的形态进来。**未采样（nil）→ R7 整体不判定**：
// 绝不把「没采样」当成「原文不存在」进而误报一堆 W20（诚实性口径与 R4 的两条源侧判定、
// query 侧「Q2 只在全库扫描面判定」逐字同源）。

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
)

// R7 是本检查项的 R 编号（与 checkTable 内 CheckSupportInsufficient 行的 R 列同值）。
const R7 = "R7"

// support 条目「无效」的封闭四值原因串（进 detail，供逐条复算；不是新 check、不是新诊断码）。
const (
	// SupportIneffectiveEndpointIncomplete 四要素不全：source / note 有一端为空，
	// 这条 support 压根不构成一条可判定的材料关系（要素完整性属 S1 既有校验，本文件只计无效）。
	SupportIneffectiveEndpointIncomplete = "endpoint_incomplete"
	// SupportIneffectiveNoteMissing 来源笔记端在 vault 内查无此对象（该事实归 R4 的 E12）。
	SupportIneffectiveNoteMissing = "note_missing"
	// SupportIneffectiveNoteDeleted 来源笔记端存在但**已逻辑删除**（`deleted_at` 非空）。
	SupportIneffectiveNoteDeleted = "note_deleted"
	// SupportIneffectiveSourceMissing 原文端在 `sources/` 分区内查无此对象。
	SupportIneffectiveSourceMissing = "source_missing"
)

// SupportIneffectiveReasonCount 是无效原因的封闭基数：恰 4。
const SupportIneffectiveReasonCount = 4

// supportIneffectiveTable 是无效原因的封闭全集（长度固定数组：第五个原因加不进来），
// 顺序 = 判定的求值行序，同时是 detail 内的渲染顺序。
var supportIneffectiveTable = [SupportIneffectiveReasonCount]string{
	SupportIneffectiveEndpointIncomplete,
	SupportIneffectiveNoteMissing,
	SupportIneffectiveNoteDeleted,
	SupportIneffectiveSourceMissing,
}

// SupportIneffectiveReasons 返回封闭四值原因集合的副本（顺序即判定求值行序）。
func SupportIneffectiveReasons() []string {
	out := make([]string, 0, SupportIneffectiveReasonCount)
	out = append(out, supportIneffectiveTable[:]...)
	return out
}

// IsKnownSupportIneffectiveReason 报告取值是否落在封闭四值内（第五个取值一律 false）。
func IsKnownSupportIneffectiveReason(s string) bool {
	for _, v := range supportIneffectiveTable {
		if v == s {
			return true
		}
	}
	return false
}

// supportIneffectiveLabel 是无效原因的中文标签（只用于 detail 渲染，不参与任何判定）。
var supportIneffectiveLabel = map[string]string{
	SupportIneffectiveEndpointIncomplete: "材料关系四要素不全（source / note 有一端为空）",
	SupportIneffectiveNoteMissing:        "来源笔记在 vault 内不存在",
	SupportIneffectiveNoteDeleted:        "来源笔记已逻辑删除",
	SupportIneffectiveSourceMissing:      "原文在 sources/ 分区内不存在",
}

// SupportFact 是一张知识卡的支持面只读事实（**纯数据**：无函数字段、无句柄、无写方法）。
//
// 为什么导出：报告侧（T-…-057）、命令渲染侧（T-…-058 / T-…-059）与 R6（T-…-055 复用
// 「有效性」判定口径）都需要「声明了几条、还剩几条有效、为什么无效」这三个事实，
// 而 Finding 按合同只有四键。两者同源同事实：SupportFacts 与 checkR7SupportInsufficient
// 走的是同一个判定函数。
type SupportFact struct {
	// ID 是知识卡 ID，逐字取自扫描面，不做任何改写。
	ID string
	// Path 是该卡的 vault 相对路径（/ 分隔）。
	Path string
	// Declared 是 `sources[]` 里 `rel` 取 support 的**落盘条目数**（不含 against / context）。
	Declared int
	// Effective 是其中**有效**的条数（对端存在且未逻辑删除）。Effective == 0 即命中 R7。
	Effective int
	// Ineffective 是命中的无效原因（封闭四值的子集，去重 + 升序；全部有效时为空）。
	Ineffective []string
	// OtherRels 是 `sources[]` 里**非** support 的条目数（against / context），只作 detail
	// 事实：它们不是「支持」，一条都不计入 Declared / Effective。
	OtherRels int
}

// Insufficient 报告这张卡是否命中 R7（有效 support 数为 0）。
func (f SupportFact) Insufficient() bool { return f.Effective == 0 }

// SupportInsufficientTargets 返回这条事实的 `targets[]`：**恰一元** `[知识卡 ID]`。
//
// 只有一个可定位标识：无效对端的 ID 与原因都进 detail —— targets 里混进笔记 / 原文 ID
// 会让脚本无法按位判断「谁材料不足」，也会和 E12 的 `[引用方 ID, 缺失目标 ID]` 撞口径。
func SupportInsufficientTargets(f SupportFact) []string { return []string{f.ID} }

// SupportFacts 返回全部**参与判定**的知识卡的支持面事实（按卡 ID 升序，可逐字复算）。
//
// 纯函数：同一 Input 恒得同一输出；不改入参、不做任何 IO。
// 两条「不判」的前提（返回空集合，绝不拿缺失的事实当证据）：
//   - Scan 为 nil：没扫描 → 没有任何卡；
//   - Sources 为 nil：`sources/` 分区未采样 → 无法判定原文端存在性。
func SupportFacts(in Input) []SupportFact {
	if in.Scan == nil || in.Sources == nil {
		return nil
	}
	x := NewStructureIndex(in)
	live := liveNotes(in.Scan)
	out := make([]SupportFact, 0, len(in.Scan.Cards))
	seen := make(map[string]bool, len(in.Scan.Cards))
	for _, c := range in.Scan.Cards {
		id := strings.TrimSpace(c.ID)
		switch {
		case id == "" || seen[id]:
			continue // 空 ID 已由扫描层记 Q1；同 ID 只判一次
		case len(x.ObjectPaths[id]) >= 2:
			continue // 与 R4 的 duplicate_id 不重复计数：整体让位给 E11
		case c.Deleted:
			continue // 卡自身已逻辑删除：删除维度不参与支持面提示（与状态维度无关）
		}
		seen[id] = true
		out = append(out, supportFactOf(id, c, x, live))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// supportFactOf 折算单张卡的支持面事实（判定的**唯一**实现）。
func supportFactOf(id string, c query.CardEntry, x StructureIndex,
	live map[string]bool) SupportFact {
	f := SupportFact{ID: id, Path: strings.TrimSpace(c.Path)}
	reasons := make([]string, 0, SupportIneffectiveReasonCount)
	for _, ref := range c.Sources {
		if ref.Rel != model.MaterialSupport {
			f.OtherRels++
			continue // against / context 不是「支持」（材料关系取值取自 internal/model）
		}
		f.Declared++
		if why, ok := supportIneffectiveReason(ref, x, live); ok {
			reasons = append(reasons, why)
			continue
		}
		f.Effective++
	}
	f.Ineffective = NormalizeTargets(reasons)
	return f
}

// supportIneffectiveReason 判定单条 support 是否无效：无效时返回封闭四值之一 + true。
//
// 求值顺序 = supportIneffectiveTable 的行序（要素完整性 → 笔记端存在性 → 笔记端删除维度
// → 原文端存在性），一条 support 只归因**第一个**命中的原因（可复算）。
func supportIneffectiveReason(ref model.SourceRef, x StructureIndex,
	live map[string]bool) (string, bool) {
	src := strings.TrimSpace(string(ref.Source))
	note := strings.TrimSpace(string(ref.Note))
	switch {
	case src == "" || note == "":
		return SupportIneffectiveEndpointIncomplete, true
	case !x.Has(note, KindNote):
		return SupportIneffectiveNoteMissing, true
	case !live[note]:
		return SupportIneffectiveNoteDeleted, true
	case !x.Has(src, KindSource):
		return SupportIneffectiveSourceMissing, true
	}
	return "", false
}

// liveNotes 折出「笔记 ID → 至少有一份未被逻辑删除」的只读索引。
//
// 为什么是「至少一份」：同一笔记 ID 落在多个文件（R4 的 E11）时，只要有一份仍未删除，
// 对端就还在 —— 不把 ID 冲突的副作用算成「材料支撑消失」。
func liveNotes(scan *query.ScanResult) map[string]bool {
	out := make(map[string]bool, len(scan.Notes))
	for _, n := range scan.Notes {
		id := strings.TrimSpace(n.ID)
		if id == "" {
			continue
		}
		if !n.Deleted {
			out[id] = true
		}
	}
	return out
}

// —— finding 的渲染 ——

// r7Detail 渲染 W20 的 detail：卡 ID + 路径 + 声明 / 有效条数 + 无效原因 + 非 support 条目数
// + 「只报告、零落盘、永不改状态」的边界（足以逐条复算）。
func r7Detail(f SupportFact) string {
	why := "该卡的 sources[] 内没有任何 rel=support 的条目"
	if f.Declared > 0 {
		why = fmt.Sprintf("声明了 %d 条 support 但全部无效（%s）", f.Declared,
			strings.Join(supportIneffectiveLabels(f.Ineffective), "、"))
	}
	extra := ""
	if f.OtherRels > 0 {
		extra = fmt.Sprintf("；另有 %d 条非 support 的材料关系（against / context 不是支持，"+
			"不计入有效支撑）", f.OtherRels)
	}
	return fmt.Sprintf(
		"知识卡 %s 的有效 support 材料关系数为 0：%s%s（有效 = 对端存在且未逻辑删除；"+
			"落盘于 %s）。本次为**实时判定**——只报告 / 只做视图提示，永不改该卡状态、"+
			"不写任何落盘标记（合同 §10）",
		f.ID, why, extra, f.Path)
}

// supportIneffectiveLabels 把无效原因串翻成中文标签（未知取值原样返回，保证 detail 恒非空）。
func supportIneffectiveLabels(reasons []string) []string {
	out := make([]string, 0, len(reasons))
	for _, r := range reasons {
		if s, ok := supportIneffectiveLabel[r]; ok {
			out = append(out, s)
			continue
		}
		out = append(out, r)
	}
	return out
}

// —— 检查项本体与注册 ——

// checkR7SupportInsufficient 是 R7 检查项本体：每张命中的卡产**恰一条** finding、
// **零 RepairSpec**（实时判定不落盘，因此也没有「幂等写」之说）。
//
// 纯函数：同一 Input 恒得同一输出（含顺序）；不改入参、不产生任何副作用。
// 让位规则（与 R4 的 E12 第 ② 类不重复计数）：无效原因里含「来源笔记缺失」的卡整体不报。
func checkR7SupportInsufficient(in Input) ([]Finding, []RepairSpec) {
	facts := SupportFacts(in)
	if len(facts) == 0 {
		return nil, nil
	}
	fs := make([]Finding, 0, len(facts))
	for _, f := range facts {
		if !f.Insufficient() || yieldsToDanglingRef(f) {
			continue
		}
		nf, err := NewFinding(CheckSupportInsufficient, SupportInsufficientTargets(f),
			r7Detail(f))
		if err != nil {
			// 两种构造错误（未知 check / 空 detail）在本文件都不可能发生：check 取自封闭表
			// 常量、r7Detail 恒产非空串、targets 恰一元且已在判定里保证非空。
			// 防御性丢弃而不 panic：对账是只读检查，任何情况下都不该让进程死在检查器里。
			continue
		}
		fs = append(fs, nf)
	}
	if len(fs) == 0 {
		return nil, nil
	}
	return fs, nil
}

// yieldsToDanglingRef 报告这张卡是否要让位给 R4 的 `dangling_ref`（E12）。
//
// 判据：无效原因里含 SupportIneffectiveNoteMissing **或** SupportIneffectiveSourceMissing ——
// 「知识卡 sources[].note 指向的来源笔记不存在」与「sources[].source 指向的原文不存在」
// 这两件落盘事实都由 E12 独家承载（前者是历史 §6.2 第 ② 类，后者是
// I-evergreen.system_assurance-158614-019 修复后新纳入 E12 的第 ③ 类）。同一件事不许
// error（E12）与 warning（W20）各记一次，故本卡整体让位、不报 W20。
//
// 让位只对「对端**缺失**」成立；对端**存在但已逻辑删除**（note_deleted）、或材料四要素
// 不全（endpoint_incomplete）、或压根没有 support 条目时不在 E12 覆盖内，W20 照报
// （反证见 TestR7NoDoubleCountWithR4DanglingRef 的正反两向用例）。
func yieldsToDanglingRef(f SupportFact) bool {
	for _, r := range f.Ineffective {
		if r == SupportIneffectiveNoteMissing || r == SupportIneffectiveSourceMissing {
			return true
		}
	}
	return false
}

// 注册：R7 是本包第六个落地的检查项（R1 / R2 / R4 / R3 / R5 分属 T-…-050 / 051 / 052 /
// 053 / 054）。
//
// 注册表 checkers 在 reconcile.go 内声明，一个 task 只在自己的文件里追加自己那一项 ——
// R6 属 T-…-055，本文件因此**恰追加一项**。
func init() { checkers = append(checkers, checkR7SupportInsufficient) }

// R7ScanOf 是给命令层与用例的便利函数：把扫描快照 + `sources/` 分区快照折成 Input。
//
// 存在的理由同 R2ScanOf / R4ScanOf / R5ScanOf：R7 只需要「扫描结果 +
// 原文分区快照」两样，不需要 Git 未提交状态快照（恒零值）、不需要编辑与 rename 事实
// （Edits / Renames 恒 nil）—— 但扫描底座仍复用 internal/query，本包不另写扫描器
// （合同 §0.1 第 1 条）。sources 传 nil 表示该分区未采样：R7 整体不判定。
func R7ScanOf(vaultRoot string, scan *query.ScanResult, sources []SourceFact) Input {
	return Input{VaultRoot: vaultRoot, Scan: scan, Sources: sources}
}
