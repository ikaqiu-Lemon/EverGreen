package reconcile

// R3 —— 关系一致性的四项**只读**检查（对账合同 §7；M4 · T-…-053）。
//
// 四项（`check` 值 / 诊断码 / severity 一律取自 check.go 的唯一真源表，本文件不另写字面量）：
//
//	relation_target_missing       E13 error   关系 target 在 vault 中查无此对象
//	relation_prefix_invalid       E14 error   关系 target 不满足既有 ID 规则（前缀 / 形态）
//	relation_opposing_asymmetric  W15 warning `opposing` 方向不对称（按 ID 字典序规范化后判定）
//	relation_duplicate            W16 warning 规范化 `(from,type,target)` 重复（同一文件内同一条边
//	                                          写了 ≥ 2 次，或同一对 `opposing` 两向各有记录）
//
// # 本文件**不做**的事（结构上做不到，不靠自律）
//
//   - **零写入、零自动修**：不补反向关系、不去重、不物理移除、不改任何 frontmatter 一个键、
//     不发提交。本文件不引 os 的任何写 API、不引子进程包；R3 是**只报告**项
//     （合同 §7「一律不自动补反向关系、不自动去重、不物理移除」+ §1.3 写口归属表
//     「其余一切：无人写」），因此**零 RepairSpec** —— 产 RepairSpec 的只有 R2 与 R6。
//     修复动作显式交回用户的 `eg rel add` / `eg rel remove`（M2 / M3 既有命令）。
//   - **不改 F4**：关系类型集合仍恰 8 值封闭（材料三值 + 论证四值 + 生命周期 `replaced_by`），
//     不新增关系类型、不新增关系字段（反证见 F4RelationValues 与用例 TestR3RelationTypesStillEight）。
//   - **不新造 ID 规则**：前缀 / 形态校验一律委派 `github.com/ikaqiu-Lemon/EverGreen/internal/model` 的现成入口
//     （ValidRelationTarget → CardID.Valid → ParseID），本文件零正则、零前缀字面量表。
//   - 不自己扫描 vault：关系事实以 query.ScanResult（M2 的全量扫描底座）的形态从 Input 进来，
//     本包不另写扫描器（合同 §17 第 7 条）。存在性索引复用 R4 交付的 StructureIndex，
//     不再各扫各的、不各建一套反向表。
//   - 不判定 R1 / R2 / R4 / R5 / R6 / R7 任何一项，也不注册任何命令
//     （`eg check` / `eg reconcile` 属 T-…-059 / T-…-058）。
//
// # 与 R4 的 `dangling_ref`（E12）分工：一件事不许两码重复计
//
// 合同 §6.2 末句逐字：「关系条目的 target 缺失不走这条 —— 它属 R3 的
// `relation_target_missing`」。因此本文件的判定面**恰是** `relations[]` 条目，
// 而 R4 的 E12 判定面**恰是** frontmatter 的两类引用（`note.source` 与 `card.sources[].note`）：
// 两个判定面在集合上不相交，同一条落盘事实只会被一个码报一次
// （双侧反证：本文件的 TestR3NoDoubleCountWithR4DanglingRef + R4 侧的
// TestR4RelationTargetNotInDanglingRef）。
//
// # `opposing` 的规范化口径（与 A-24 一字不放宽）
//
// A-24（2026-10-13 m3 前置裁决 §7 第 2 条）逐字：`opposing` 先按 ID 字典序规范化；
// 该口径与既有**单向存储**语义对齐 —— 写侧唯一实现按两端稳定 ID 字典序取小者为写入端，
// 落盘侧另有「必须写在字典序较小的一端」的不变量（未规范化即拒写）。于是落盘面上一条
// 合规的 `opposing` 恒是「较小端 → 较大端」**恰一条**记录。R3 只读地复算这条不变量：
//
//	较小端 → 较大端     恰 1 条          → 干净（**不报** W15：单向存储是规范形态，
//	                                      若把它判成「缺反向」，每个正常 vault 都会误报）
//	较大端 → 较小端     且规范方向缺失   → W15（方向不对称：记录落在非规范的那一端，
//	                                      规范方向那条缺失；只可能来自绕过写侧的外部编辑）
//	两个方向各有记录                     → W16（同对两条记录 = 重复对；写侧「已存在则只更新
//	                                      reason、不产生第二条」的读侧对应物）
//
// 本包**不 import** 写侧的 `opposing` 规则包：合同 §1.2 的依赖方向只允许
// reconcile → {model, mdfile, git 只读 API, query}。NormalizeOpposingPair 因此只复刻
// 同一条字符串比较式（两处口径若分叉，本文件用例与写侧用例会双侧判红）。
//
// # 判定的诚实性边界
//
// target 存在性看**落盘事实**（StructureIndex 的对象索引），不做任何 status / deleted_at /
// 可见性过滤 —— 与 R4 §6.3 末段同源口径：展示面过滤不得渗进对账判定。
// Input.Scan 为 nil（未取数）时四项整体零产出；判定面按合同以**全库**扫描快照为准
// （受限扫描面会把「没扫到」误判成「不存在」，取数口径由调用方保证，与查询侧
// 「Q2 只在全库扫描面判定」的诚实性口径一致）。

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
)

// R3 是本检查项的 R 编号（与 checkTable 内四行 R3 的 R 列同值）。
const R3 = "R3"

// R3SubcheckCount 是 R3 的子检查数：恰 4（E13 / E14 / W15 / W16）。
const R3SubcheckCount = 4

// r3SubcheckTable 是四个子检查的封闭全集（长度固定数组：第五个子检查加不进来），
// 顺序 = 合同 §7 判定表行序 = 合同 §3 表格内四行 R3 的行序 = 本文件的 finding 产出顺序。
var r3SubcheckTable = [R3SubcheckCount]string{
	CheckRelationTargetMissing,
	CheckRelationPrefixInvalid,
	CheckRelationOpposingAsymmetric,
	CheckRelationDuplicate,
}

// R3Subchecks 返回四个子检查的 check 值副本（顺序即合同 §7 判定表行序）。
func R3Subchecks() []string {
	out := make([]string, 0, R3SubcheckCount)
	out = append(out, r3SubcheckTable[:]...)
	return out
}

// F4LifecycleRelation 是冻结合同 F4 里的生命周期关系键（`replaced_by`）。
// 它是**卡上的字段键**而不是论证关系取值，故不在 model.ValidRelationTypes() 内。
const F4LifecycleRelation = "replaced_by"

// F4RelationValueCount 是冻结合同 F4 的关系类型封闭基数：恰 **8**
// （材料 `support` / `against` / `context` 三值 + 论证 `derives` / `supports` / `limits` /
// `opposing` 四值 + 生命周期 `replaced_by` 一值）。
//
// 本常量只作「R3 不改 F4」的反证锚点，**不参与任何判定**：R3 的判定只读关系条目的
// `type` / `target` 两个落盘事实，一个关系类型都不新增、一个关系字段都不新增。
const F4RelationValueCount = 3 + 4 + 1

// F4RelationValues 返回 F4 全集的副本（材料三值 → 论证四值 → 生命周期一值）。
// 取值一律来自 model 的封闭枚举，本文件不另写字面量（`replaced_by` 除外，它是字段键）。
func F4RelationValues() []string {
	out := make([]string, 0, F4RelationValueCount)
	for _, r := range model.ValidMaterialRels() {
		out = append(out, string(r))
	}
	for _, t := range model.ValidRelationTypes() {
		out = append(out, string(t))
	}
	return append(out, F4LifecycleRelation)
}

// ValidRelationTarget 报告关系 `target` 是否满足**既有** ID 规则。
//
// **零新造规则**：判定整体委派给 model 的现成入口 —— 关系 target 的落盘类型是知识卡 ID
// （`Relation.Target` 是 model.CardID），故合法性 == CardID.Valid()（内部走 ParseID：
// 前缀 `k-` + 8 位 yyyymmdd + 非空 slug）。本文件因此没有任何正则、没有任何前缀字面量表：
// 前缀规则若在 model 侧变更，本判定自动随之变更，不会出现「两处规则」。
//
// 笔记 / 原文 ID（`n-` / `s-`）出现在关系 target 上同样是**非法前缀**：关系只连知识卡
// （与 R4 侧 T-…-052 用例「出边 target 前缀非法（n- 前缀）」逐字同口径）。
func ValidRelationTarget(target string) bool {
	return model.CardID(strings.TrimSpace(target)).Valid()
}

// NormalizeOpposingPair 按两端 ID 字典序规范化 `opposing` 的 `(from,target)`：
// 返回 `(较小端, 较大端)`。两端相同时原样返回（自反关系由写侧拒绝，读侧只如实规范化）。
//
// 口径与 A-24 逐字一致（「`opposing` 先按 ID 字典序规范化」），也与写侧「取小者为写入端」
// 的唯一实现同源；**只读**：本函数不改入参、不落盘、不发提交。
func NormalizeOpposingPair(from, target string) (string, string) {
	a, b := strings.TrimSpace(from), strings.TrimSpace(target)
	if b < a {
		return b, a
	}
	return a, b
}

// relationFact 是一条待判定的关系落盘事实（逐字原值，不做任何补默认）。
type relationFact struct {
	// from 是持有该条关系的知识卡 ID（`relations[]` 落在来源卡，单向一条）。
	from string
	// typ 是关系类型的逐字原值（封闭四值之一；集合外取值在扫描层就已被拒收）。
	typ string
	// target 是关系目标的逐字原值（可能非法、可能为空、可能指向不存在的对象）。
	target string
	// path 是持有卡的 vault 相对路径（进 detail，供逐条定位复算）。
	path string
	// seq 是该条关系在持有卡 `relations[]` 内的 1-based 序号（进 detail）。
	seq int
}

// isOpposing 报告本条关系是否是 `opposing`（取值来自 model 的封闭枚举）。
func (f relationFact) isOpposing() bool { return f.typ == string(model.RelationOpposing) }

// collectRelationFacts 把扫描快照折成关系事实序列（纯函数：不改入参、零 IO）。
//
// 顺序 = 卡的扫描序（query 侧已按 path 升序稳定排序）→ 卡内 `relations[]` 落盘序，
// 因此同一份快照恒得同一序列。缺 id 的卡在扫描层已记 Q1 并计入 SkippedFiles，
// 这里只丢弃空 ID 的持有卡（对账域不给无法定位的引用方发 finding）。
func collectRelationFacts(scan *query.ScanResult) []relationFact {
	if scan == nil {
		return nil
	}
	out := make([]relationFact, 0)
	for _, c := range scan.Cards {
		from := strings.TrimSpace(c.ID)
		if from == "" {
			continue
		}
		for i, rel := range c.Relations {
			out = append(out, relationFact{
				from:   from,
				typ:    strings.TrimSpace(string(rel.Type)),
				target: strings.TrimSpace(string(rel.Target)),
				path:   strings.TrimSpace(c.Path),
				seq:    i + 1,
			})
		}
	}
	return out
}

// —— E13 / E14：target 的存在性与形态（合同 §7 判定表第 1 / 2 行）——

// targetAgg 是同一 `(from,target)` 上的聚合事实（同一件事只报一条）。
type targetAgg struct {
	fact    relationFact
	entries int
	types   []string
	paths   []string
}

// checkR3TargetMissingAndPrefix 判定 E13 与 E14，并保证两码**互斥**：
//
//	target 形态非法（含空串）        → 恰 E14 一条（形态不合法时「存在性」无从判定）
//	target 形态合法但对象不在库内    → 恰 E13 一条
//	target 形态合法且对象在库内      → 两码都不产
//
// 判定粒度 = 去重后的 `(from,target)`：同一张卡用两条不同类型的关系指向同一个缺失目标
// 是**一件**事实（条目数与类型集合进 detail，供逐条复算），不按条目报多条。
// 产出顺序 = `(from,target)` 升序，与 map 迭代序无关。
func checkR3TargetMissingAndPrefix(facts []relationFact, x StructureIndex) []Finding {
	uniq := map[string]*targetAgg{}
	keys := make([]string, 0, len(facts))
	for _, f := range facts {
		key := f.from + "\x00" + f.target
		agg, ok := uniq[key]
		if !ok {
			agg = &targetAgg{fact: f}
			uniq[key] = agg
			keys = append(keys, key)
		}
		agg.entries++
		agg.types = append(agg.types, f.typ)
		agg.paths = append(agg.paths, f.path)
	}
	sort.Strings(keys)
	var missing, invalid []Finding
	for _, key := range keys {
		agg := uniq[key]
		f := agg.fact
		switch {
		case !ValidRelationTarget(f.target):
			fd, err := NewFinding(CheckRelationPrefixInvalid, []string{f.from, f.target},
				prefixInvalidDetail(agg))
			if err != nil {
				continue // 防御性丢弃：只读检查不该让进程死在检查器里
			}
			invalid = append(invalid, fd)
		case !x.Has(f.target, KindCard):
			fd, err := NewFinding(CheckRelationTargetMissing, []string{f.from, f.target},
				targetMissingDetail(agg))
			if err != nil {
				continue
			}
			missing = append(missing, fd)
		}
	}
	// 表格行序：E13（第 6 行）在 E14（第 7 行）之前。
	return append(missing, invalid...)
}

// targetMissingDetail 渲染 E13 的 detail（引用方 + 缺失目标 + 条目数 + 类型 + 落盘路径）。
func targetMissingDetail(agg *targetAgg) string {
	return fmt.Sprintf(
		"关系 target 不存在：知识卡 %s 的 relations[] 指向 %s，该目标在 vault 内查无此对象"+
			"（共 %d 条条目，类型 %s；持有卡落盘于 %s）。存在性只看落盘事实，不做 status / "+
			"deleted_at / 可见性过滤；本码只报告——不自动补建目标卡、不移除该关系条目，"+
			"修复走 `eg rel remove` / `eg rel add`。关系条目的 target 缺失只走本码，"+
			"**不走** R4 的 dangling_ref（E12），避免两码重复计一件事（合同 §6.2 末句）",
		agg.fact.from, agg.fact.target, agg.entries,
		strings.Join(NormalizeTargets(agg.types), "、"),
		strings.Join(NormalizeTargets(agg.paths), "、"))
}

// prefixInvalidDetail 渲染 E14 的 detail（引用方 + 非法 target 原值 + 条目序号 + 路径）。
func prefixInvalidDetail(agg *targetAgg) string {
	return fmt.Sprintf(
		"关系 target 的 ID 前缀 / 形态不合法：知识卡 %s 的 relations[] 第 %d 条 target = %q"+
			"（共 %d 条同目标条目，类型 %s），不满足既有 ID 规则（知识卡 ID 恒 `k-` + 8 位 "+
			"yyyymmdd + 非空 slug；判定直接调用 internal/model 的现成校验，本检查零新造规则）；"+
			"持有卡落盘于 %s。形态非法时不再判 target 存在性（E13），一件事只报一码；"+
			"只报告——不自动改写 target、不移除该条目",
		agg.fact.from, agg.fact.seq, agg.fact.target, agg.entries,
		strings.Join(NormalizeTargets(agg.types), "、"),
		strings.Join(NormalizeTargets(agg.paths), "、"))
}

// —— W15：`opposing` 方向不对称（合同 §7 判定表第 3 行）——

// opposingPair 是规范化后的一对 `opposing` 的落盘统计。
type opposingPair struct {
	// small / large 是按 ID 字典序规范化后的两端（A-24 口径）。
	small, large string
	// canonical 是规范方向（较小端 → 较大端）的落盘条目数。
	canonical int
	// reverse 是非规范方向（较大端 → 较小端）的落盘条目数。
	reverse int
	// paths 是相关条目的落盘路径（去重升序，进 detail）。
	paths []string
}

// opposingPairs 归并全部**可判定**的 `opposing` 条目：两端都必须是合法且在库内的知识卡 ID。
//
// 为什么跳过不可判定的条目：target 形态非法已由 E14 报、target 不在库内已由 E13 报，
// 「对端不存在时缺不缺反向」不是一件可复算的事实 —— 若照判，同一条脏数据会被两个码各记
// 一次（用例 TestR3OpposingAsymmetricNormalized 的「缺失对端只产 E13」分支逐字锁死）。
func opposingPairs(facts []relationFact, x StructureIndex) (map[string]*opposingPair, []string) {
	pairs := map[string]*opposingPair{}
	keys := make([]string, 0, len(facts))
	for _, f := range facts {
		if !f.isOpposing() {
			continue
		}
		if !ValidRelationTarget(f.from) || !ValidRelationTarget(f.target) {
			continue
		}
		if !x.Has(f.target, KindCard) {
			continue
		}
		small, large := NormalizeOpposingPair(f.from, f.target)
		key := small + "\x00" + large
		p, ok := pairs[key]
		if !ok {
			p = &opposingPair{small: small, large: large}
			pairs[key] = p
			keys = append(keys, key)
		}
		if f.from == small {
			p.canonical++
		} else {
			p.reverse++
		}
		p.paths = append(p.paths, f.path)
	}
	for _, p := range pairs {
		p.paths = NormalizeTargets(p.paths)
	}
	sort.Strings(keys)
	return pairs, keys
}

// checkR3OpposingAsymmetric 判定 W15：规范方向（较小端 → 较大端）的记录**缺失**，
// 而非规范方向有记录 —— 即该对的落盘形态违反 `opposing` 单向存储的规范化不变量。
//
// 反面两种形态都不在本码内：只有规范方向那一条 = 干净（单向存储的规范形态）；
// 两个方向各有记录 = 重复对（W16），不是「缺方向」。
// 自反对（两端同一张卡）的规范方向恒等于自身，因此恒不产 W15。
//
// `targets[]` = 规范化后的 `[较小端, 较大端]`（合同 §2 的去重升序恒等于该次序）。
func checkR3OpposingAsymmetric(facts []relationFact, x StructureIndex) []Finding {
	pairs, keys := opposingPairs(facts, x)
	var out []Finding
	for _, key := range keys {
		p := pairs[key]
		if p.canonical != 0 || p.reverse == 0 {
			continue
		}
		f, err := NewFinding(CheckRelationOpposingAsymmetric, []string{p.small, p.large},
			opposingAsymmetricDetail(p))
		if err != nil {
			continue
		}
		out = append(out, f)
	}
	return out
}

// opposingAsymmetricDetail 渲染 W15 的 detail（规范化两端 + 两个方向的条目数 + 落盘路径）。
func opposingAsymmetricDetail(p *opposingPair) string {
	return fmt.Sprintf(
		"opposing 方向不对称：该对的落盘记录只出现在非规范方向（%s → %s 共 %d 条），"+
			"规范方向（按两端 ID 字典序取小者为写入端，即 %s → %s）零条记录；"+
			"规范化口径与 A-24 逐字一致（opposing 先按 ID 字典序规范化、单向存储恰一条），"+
			"该形态只可能来自绕过写侧校验的外部编辑。记录落盘于 %s。只报告——不自动补反向、"+
			"不自动搬移记录，修复走 `eg rel remove` + `eg rel add`",
		p.large, p.small, p.reverse, p.small, p.large, strings.Join(p.paths, "、"))
}

// —— W16：重复关系对（合同 §7 判定表第 4 行）——

// dupGroup 是同一个规范化三元组上的落盘统计。
type dupGroup struct {
	// from / typ / target 是规范化后的三元组（`opposing` 的两端已按字典序归一）。
	from, typ, target string
	// entries 是命中该三元组的落盘条目数（进 detail 复算，**不单独**作为判定阈值：
	// 判定阈值见 perFile / bidirectional 两格，理由在 checkR3Duplicate 的注释里）。
	entries int
	// perFile 是「同一落盘文件 + 同一方向」上的条目数（键 = 路径 + 方向）。
	// 判定只认这一格 ≥ 2 —— 即**同一份文件内**重复写了同一条边。
	perFile map[string]int
	// paths 是相关条目的落盘路径（去重升序，进 detail）。
	paths []string
	// bidirectional 报告该 `opposing` 对是否两个方向各有记录（进 detail 区分子形态）。
	bidirectional bool
	// forward / backward 是 `opposing` 对两个方向的条目数（只对 opposing 有意义）。
	forward, backward int
}

// inFileDuplicate 报告是否存在「同一落盘文件 + 同一方向」上 ≥ 2 条的同一条边。
func (g *dupGroup) inFileDuplicate() bool {
	for _, n := range g.perFile {
		if n >= 2 {
			return true
		}
	}
	return false
}

// checkR3Duplicate 判定 W16：规范化 `(from,type,target)` 出现 ≥ 2 条落盘记录。
//
// 规范化口径按类型分两支（合同 §7 + A-24）：
//   - `opposing`：无向语义，同对判定**与方向无关** —— 先按两端 ID 字典序归一再计数，
//     因此「两个方向各一条」= 同一对出现 2 条 = 重复（写侧「已存在则只更新 reason、
//     不产生第二条」的读侧对应物）；
//   - 其余三类论证关系：有向，`(from,type,target)` 逐字比对，只有同一张卡上重复写了
//     同一条边才算重复。
//
// 形态非法的 target（已由 E14 报）不参与判重：非法串的「规范化」不是可复算的事实，
// 且同一条脏数据不该被两个码各记一次。target 合法但**不在库内**仍参与判重 ——
// 「同一条边写了两遍」与「目标不存在」是两件独立的事实（前者 W16、后者 E13）。
//
// # 判定阈值为什么按「同一落盘文件 + 同一方向」而不是简单的条目总数
//
// 一个 ID 若同时落在两个文件里（那是 R4 的 `duplicate_id` / E11），两份文件的**镜像**
// 关系条目会让同一个规范化三元组凑出 2 条 —— 那不是「重复关系对」，而是同一件 ID 冲突
// 事实的投影。若照条目总数判，同一件事就会被 E11 与 W16 各记一次（正是合同禁止的重复
// 计数）。因此 W16 的成立条件恰是下面两支之一：
//
//	同一份落盘文件的同一方向上出现 ≥ 2 条同一条边  → 写重了（有向关系的唯一形态）
//	同一对 `opposing` 两个方向各有记录             → 同对两条（A-24 规范化后属同一对）
//
// 「同 ID 多文件的镜像条目」两支都不满足，故只被 E11 报一次
// （反证：TestR3NoDoubleCountWithR4DuplicateID + e2e `m4_r4_structure.sh` 的 finding
// 总数恒 3 —— R3 落地后那条等号一格未动）。
//
// `targets[]` = `[from, target]`（`opposing` 为规范化后的 `[较小端, 较大端]`），
// 经合同 §2 的去重升序归一；条目数、路径、方向分布进 detail。
func checkR3Duplicate(facts []relationFact) []Finding {
	groups := map[string]*dupGroup{}
	keys := make([]string, 0, len(facts))
	for _, f := range facts {
		if !ValidRelationTarget(f.target) {
			continue
		}
		from, target := f.from, f.target
		if f.isOpposing() {
			from, target = NormalizeOpposingPair(f.from, f.target)
		}
		key := f.typ + "\x00" + from + "\x00" + target
		g, ok := groups[key]
		if !ok {
			g = &dupGroup{from: from, typ: f.typ, target: target, perFile: map[string]int{}}
			groups[key] = g
			keys = append(keys, key)
		}
		g.entries++
		g.paths = append(g.paths, f.path)
		// 方向位只对 `opposing` 有意义（有向三类的 from 恒是持有卡，方向恒一致）。
		dir := "0"
		if f.isOpposing() && f.from != from {
			dir = "1"
		}
		g.perFile[f.path+"\x00"+dir]++
		if f.isOpposing() {
			if f.from == from {
				g.forward++
			} else {
				g.backward++
			}
		}
	}
	sort.Strings(keys)
	var out []Finding
	for _, key := range keys {
		g := groups[key]
		g.bidirectional = g.forward != 0 && g.backward != 0
		if !g.inFileDuplicate() && !g.bidirectional {
			continue
		}
		g.paths = NormalizeTargets(g.paths)
		f, err := NewFinding(CheckRelationDuplicate, []string{g.from, g.target},
			duplicateRelationDetail(g))
		if err != nil {
			continue
		}
		out = append(out, f)
	}
	return out
}

// duplicateRelationDetail 渲染 W16 的 detail（规范化三元组 + 条目数 + 方向分布 + 路径）。
func duplicateRelationDetail(g *dupGroup) string {
	shape := "同一张卡的 relations[] 内重复写了同一条边"
	if g.bidirectional {
		shape = fmt.Sprintf("同一对 opposing 两个方向各有记录（%s → %s 共 %d 条、%s → %s 共 %d 条），"+
			"按 A-24 的字典序规范化后属同一对", g.from, g.target, g.forward, g.target, g.from, g.backward)
	}
	return fmt.Sprintf(
		"重复关系对：规范化 (from,type,target) = (%s,%s,%s) 在落盘面上出现 %d 条记录 —— %s；"+
			"opposing 的同对判定与方向无关（先按两端 ID 字典序规范化，口径与 A-24 一致），"+
			"其余三类论证关系按方向逐字比对。写侧 `eg rel add` 的幂等去重只在写侧生效，"+
			"外部编辑绕过写侧故读侧仍需检查。落盘于 %s。只报告——不自动去重、不物理移除",
		g.from, g.typ, g.target, g.entries, shape, strings.Join(g.paths, "、"))
}

// —— 汇总与注册 ——

// checkR3Relation 是 R3 检查项本体：一次建索引 + 一次事实归集，跑四项判定，
// 产 finding、**零 RepairSpec**。
//
// 纯函数：同一 Input 恒得同一输出（含顺序）；不改入参、不产生任何副作用。
// 产出顺序 = 合同 §7 判定表行序（E13 → E14 → W15 → W16），组内按判定键升序。
// Scan 为 nil（未取数）时返回空集合 —— 「没取数」不产 finding。
func checkR3Relation(in Input) ([]Finding, []RepairSpec) {
	facts := collectRelationFacts(in.Scan)
	if len(facts) == 0 {
		return nil, nil
	}
	x := NewStructureIndex(in)
	var out []Finding
	out = append(out, checkR3TargetMissingAndPrefix(facts, x)...)
	out = append(out, checkR3OpposingAsymmetric(facts, x)...)
	out = append(out, checkR3Duplicate(facts)...)
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// R3ScanOf 是给命令层与用例的便利函数：把 vault 快照折成 R3 需要的 Input。
//
// R3 只需要「全库扫描结果」：关系事实与 target 存在性都在知识卡这一个事实面上
// （`sources/` 分区与 Git 状态与本项无关，故 Sources / Status 保持零值）。
// **扫描底座仍复用 internal/query**，本包不另写扫描器。
func R3ScanOf(vaultRoot string, scan *query.ScanResult) Input {
	return Input{VaultRoot: vaultRoot, Scan: scan}
}

// 注册：R3 是本包第三个落地的检查项（R1 属 T-…-050、R2 属 T-…-051、R4 属 T-…-052）。
//
// 四项判定合并成**一项注册**：它们共用同一份关系事实序列与同一份对象索引，
// 且合同 §7 把四者定义为同一条 R 编号。R5 / R6 / R7 分属 T-…-054 / 055 / 056，
// 本文件因此**恰追加一项**（注册项数的加法等式见 r1_git_test.go 的自守用例）。
func init() { checkers = append(checkers, checkR3Relation) }
