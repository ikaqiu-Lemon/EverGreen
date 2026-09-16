package reconcile

// R4 —— 结构一致性的三项**只读**检查（对账合同 §6；M4 · T-…-052）。
//
// 三项（`check` 值 / 诊断码 / severity 一律取自 check.go 的唯一真源表，本文件不另写字面量）：
//
//	duplicate_id  E11 error   同一知识 ID 落在两个及以上文件（§6.1）
//	dangling_ref  E12 error   笔记 / 知识卡的 frontmatter 引用指向不存在的对象（§6.2，恰两类）
//	orphan        W17 warning 孤儿三子类型合并为一个 check，由 detail 区分（§6.3）
//
// # 本文件**不做**的事（结构上做不到，不靠自律）
//
//   - 不写盘、不改名、不删除、不移动、不改 frontmatter 一个键：本文件不引 os 的任何写 API，
//     也不引子进程包；R4 是**只报告**项（合同 §1.3 写口归属表第 4 行「其余一切：无人写」），
//     因此**零 RepairSpec** —— 产 RepairSpec 的只有 R2（T-…-051）与 R6（T-…-055）。
//     物理改名 / 物理删除属 U-01，任何阶段任何授权都不做。
//   - 不自己扫描 vault：落盘事实以 query.ScanResult（M2 的全量扫描底座）+ `sources/` 分区
//     快照的形态从 Input 进来，本包不另写扫描器（合同 §17 第 7 条）。
//   - 不调用展示面的可见性过滤入口（姊妹合同 §4.1 那个端点过滤函数）：孤儿判定看**落盘事实**
//     （`relations[]` 原始条目 + 全库反向扫描），不看展示面过滤结果 —— 否则「deprecated 端点
//     默认隐藏」会把一张只有失效邻居的正常卡误判成孤儿（合同 §6.3 末段 / 姊妹合同 §4.2 的
//     交叉硬约束，由本 task 与 T-…-061 双侧用例锁死）。
//   - 不判定 R1 / R2 / R3 / R5 / R6 / R7 任何一项：关系条目的 target 缺失 / 前缀非法属 R3
//     （`relation_target_missing` / `relation_prefix_invalid`，归 T-…-053），**不走**
//     `dangling_ref` —— 一件事不许两码重复计（§6.2 末句）。
//   - 不注册任何命令、不动报告体（归 T-…-058 / T-…-059 / T-…-057）。
//
// # 与查询域 Q1 / Q2 的关系（同事实不同域，禁止双计数）
//
// 「同 ID 重复」与「悬空引用」在**查询域**已有 Q1 / Q2（M2 查询合同 §5.1：只用于只读查询命令、
// 不进 E/W/I 表）。本文件产的是**对账域** finding：可进报告、可被 `eg check` 阻断（A-31 退 2）。
// 两域各记一次自己的事实，但**同一个输出里不计两次** —— 本文件因此：
//   - 只读 Input.Scan 的 Cards / Notes 两个事实面，扫描结果里承载 Q 系列诊断的那个字段
//     **一个字节都不读**，也不复制、不改写、不透传它（用例逐字 grep 反证）；
//   - 同一个重复 ID 恒产**一条** finding（全部冲突文件进 targets），不按文件产多条。
//
// # 判定面（对账域 = 四类落盘对象）
//
// vault 五分区里，`sources/` + `domains/<d>/notes/` + `domains/<d>/knowledge/` +
// `domains/<d>/opinions/` 四个分区是**对象 ID 域**；`proposals/**` 与 `unprocessed.md`
// 不在内（与 R2 判定第 1 条同口径：提案是控制面、收件区是条目而不是对象）。
// 判定**不做任何 status / deleted_at 过滤**：
// 「排除失效 / 已删除对象」本身就是一种展示面过滤，与 §6.3 末段的解耦要求相悖。

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
)

// R4 是本检查项的 R 编号（与 checkTable 内三行 R4 的 R 列同值）。
const R4 = "R4"

// 孤儿的封闭三子类型串（合同 §6.3 逐字）。三值由 detail 承载，**不新增第四个 check**。
const (
	// OrphanNoteWithoutSource 笔记不属任何材料：frontmatter 未声明 source。
	OrphanNoteWithoutSource = "note_without_source"
	// OrphanSourceWithoutNote 材料没有任何派生笔记：全库无笔记的 source 指向它。
	OrphanSourceWithoutNote = "source_without_note"
	// OrphanCardWithoutRelation 知识卡零关系：relations_out 与 relations_in 同时为空。
	OrphanCardWithoutRelation = "card_without_relation"
)

// OrphanSubtypeCount 是孤儿子类型的封闭基数：恰 3。
const OrphanSubtypeCount = 3

// orphanSubtypeTable 是子类型的封闭全集（长度固定数组：第四个子类型加不进来），
// 顺序 = 合同 §6.3 的三条判定行序，同时是 orphan finding 的产出顺序。
var orphanSubtypeTable = [OrphanSubtypeCount]string{
	OrphanNoteWithoutSource,
	OrphanSourceWithoutNote,
	OrphanCardWithoutRelation,
}

// OrphanSubtypes 返回封闭三子类型的副本（顺序即合同 §6.3 行序）。
func OrphanSubtypes() []string {
	out := make([]string, 0, OrphanSubtypeCount)
	out = append(out, orphanSubtypeTable[:]...)
	return out
}

// IsKnownOrphanSubtype 报告取值是否落在封闭三值内（第四个取值一律 false）。
func IsKnownOrphanSubtype(s string) bool {
	for _, v := range orphanSubtypeTable {
		if v == s {
			return true
		}
	}
	return false
}

// 对象类别（对账域落盘对象的机器串；中文标签只作 detail 事实，不参与判定）。
//
// KindOpinion 是 schema v2 追加的第四类：观点与知识卡分居两个目录、ID 前缀不同，
// 因此**必须**是独立类别 —— 若混进 KindCard，关系 target 的存在性判定（R3 的 E13）
// 就会把「指向一条观点」当成「指向一张存在的卡」放过去。
const (
	KindCard    = "card"
	KindNote    = "note"
	KindSource  = "source"
	KindOpinion = "opinion"
)

// kindLabel 是类别的中文标签（只用于 detail 渲染，不参与任何判定）。
var kindLabel = map[string]string{
	KindCard:    "知识卡",
	KindNote:    "材料笔记",
	KindSource:  "原文",
	KindOpinion: "观点",
}

// SourceFact 是 `sources/` 分区一条原文的只读落盘事实。
//
// 只有两个可定位字段：本包不需要原文正文，也不该持有它（对账只看结构，不看内容）。
type SourceFact struct {
	// ID 是原文 ID（`s-` 前缀），逐字取自 frontmatter，不做任何归一化。
	ID string
	// Path 是 vault 相对路径（/ 分隔），与 query 扫描面的 Path 口径一致。
	Path string
}

// StructureIndex 是 R4 建的**一次性只读索引**：三类落盘对象的 ID → 路径 / 类别，
// 加上两张反向表（卡的入边、原文的派生笔记）。
//
// 下游 T-…-053（R3）/ T-…-055（R6）/ T-…-056（R7）复用同一份索引，不各扫各的、
// 也不各建一套反向表 —— 「同一事实两处实现」在 M4 内因此不会发生。
type StructureIndex struct {
	// ObjectPaths 是 ID → 命中的全部 vault 相对路径（去重 + 字典序升序）。
	// 长度 ≥ 2 即「同一 ID 落在多个文件」= duplicate_id 的判定条件。
	ObjectPaths map[string][]string
	// ObjectKinds 是 ID → 命中的对象类别（去重 + 升序）。跨类别撞同一 ID 同样算重复。
	ObjectKinds map[string][]string
	// RelationsIn 是**落盘事实**的反向表：目标卡 ID → 指向它的来源卡 ID（去重 + 升序）。
	// 口径 = 全库遍历每张卡的 `relations[]` 原始条目，**不过滤**对端的 status / deleted_at。
	RelationsIn map[string][]string
	// NotesBySource 是原文 ID → 派生笔记 ID（去重 + 升序），同样只看落盘事实。
	NotesBySource map[string][]string
	// SourcesSampled 报告本次是否采样了 `sources/` 分区（Input.Sources 非 nil）。
	// 未采样时两条源侧判定整体跳过：不得把「没采样」误报成「不存在」
	// （同 query 侧「Q2 只在全库扫描面判定」的诚实性口径）。
	SourcesSampled bool

	// outDegree 是卡 ID → 落盘出边条数（`relations[]` 的非空 target 条目数）。
	// 不导出：出边度数只留 OutDegree 一个读口，杜绝调用方自己再数一遍导致口径分叉。
	outDegree map[string]int
}

// Has 报告该 ID 是否以指定类别落在 vault 内（存在性一律看落盘事实）。
func (x StructureIndex) Has(id, kind string) bool {
	for _, k := range x.ObjectKinds[id] {
		if k == kind {
			return true
		}
	}
	return false
}

// OutDegree 报告该卡 `relations[]` 的落盘条目数（含指向不存在目标的条目）。
//
// 为什么悬空的出边也算出边：那属 R3 的 relation_target_missing（E13），这张卡在落盘事实上
// **并非孤立**；若把它算成零出边，同一件事会被 W17 与 E13 各记一次。
func (x StructureIndex) OutDegree(id string) int { return x.outDegree[id] }

// InDegree 报告指向该卡的落盘入边条数（来源卡去重后的个数）。
func (x StructureIndex) InDegree(id string) int { return len(x.RelationsIn[id]) }

// NewStructureIndex 从只读输入建索引（纯函数：不改入参、不做任何 IO）。
func NewStructureIndex(in Input) StructureIndex {
	x := StructureIndex{
		ObjectPaths:    map[string][]string{},
		ObjectKinds:    map[string][]string{},
		RelationsIn:    map[string][]string{},
		NotesBySource:  map[string][]string{},
		SourcesSampled: in.Sources != nil,
		outDegree:      map[string]int{},
	}
	if in.Scan != nil {
		for _, c := range in.Scan.Cards {
			x.add(c.ID, KindCard, c.Path)
			x.addRelations(c.ID, c.Relations)
		}
		for _, n := range in.Scan.Notes {
			x.add(n.ID, KindNote, n.Path)
			if src := strings.TrimSpace(n.Source); src != "" {
				x.NotesBySource[src] = append(x.NotesBySource[src], strings.TrimSpace(n.ID))
			}
		}
		// 观点同样是落盘对象，其 `relations[]` 同样是落盘出边：
		// 「只被观点引用的知识卡」在落盘事实上并非孤立，因此入边表必须收下这些边 ——
		// 漏收的直接后果是这类卡被 W17 的 card_without_relation 误报。
		for _, o := range in.Scan.Opinions {
			x.add(o.ID, KindOpinion, o.Path)
			x.addRelations(o.ID, o.Relations)
		}
	}
	for _, s := range in.Sources {
		x.add(s.ID, KindSource, s.Path)
	}
	for id := range x.RelationsIn {
		x.RelationsIn[id] = NormalizeTargets(x.RelationsIn[id])
	}
	for id := range x.NotesBySource {
		x.NotesBySource[id] = NormalizeTargets(x.NotesBySource[id])
	}
	for id := range x.ObjectPaths {
		x.ObjectPaths[id] = NormalizeTargets(x.ObjectPaths[id])
		x.ObjectKinds[id] = NormalizeTargets(x.ObjectKinds[id])
	}
	return x
}

// add 登记一条落盘对象事实。
//
// 空 ID 一律丢弃：扫描层已为「缺 id 的文件」记过 Q1 并计入 SkippedFiles，
// 对账域不给一个无法定位的对象发 finding。
func (x StructureIndex) add(id, kind, path string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	x.ObjectPaths[id] = append(x.ObjectPaths[id], strings.TrimSpace(path))
	x.ObjectKinds[id] = append(x.ObjectKinds[id], kind)
}

// addRelations 登记一个关系持有方的落盘出边（知识卡与观点共用，口径逐字相同）。
//
// 空 target 不是一条可判定的边（形态问题归 R3），因此既不计出度也不进入边表。
func (x StructureIndex) addRelations(holder string, rels []model.Relation) {
	from := strings.TrimSpace(holder)
	if from == "" {
		return // 无法定位的持有方不产生任何边（同 add 的口径）
	}
	for _, rel := range rels {
		target := strings.TrimSpace(string(rel.Target))
		if target == "" {
			continue
		}
		x.outDegree[from]++
		x.RelationsIn[target] = append(x.RelationsIn[target], from)
	}
}

// sortedIDs 返回索引内全部对象 ID 的升序副本（map 迭代序不得泄漏到输出）。
func (x StructureIndex) sortedIDs() []string {
	out := make([]string, 0, len(x.ObjectPaths))
	for id := range x.ObjectPaths {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// —— 三类对象按 ID 去重（同 ID 多文件时视为**一个**对象，孤儿因此不会被按文件重复报）——
//
// 代表项取**路径字典序最小**的那一份（与 M2「同 ID 重复时由调用方按路径字典序取小者」的
// 既有口径同源），因此结果与扫描返回序无关；「同 ID 落在两个文件」这件事由 duplicate_id
// （E11）单独报，不在孤儿里重复出现。

// objectFact 是去重后的一个对账域对象（只保留判定用得到的三个事实字段）。
type objectFact struct {
	id     string
	path   string
	source string // 只对材料笔记有意义：frontmatter `source` 的逐字原值
}

// dedupByID 按 ID 归并同 ID 的多份落盘条目：路径取最小者，
// source 取「任一份声明了非空值」的那个（有一份声明即视为已声明所属材料）。
func dedupByID(in []objectFact) []objectFact {
	byID := map[string]objectFact{}
	ids := make([]string, 0, len(in))
	for _, f := range in {
		f.id = strings.TrimSpace(f.id)
		if f.id == "" {
			continue
		}
		cur, ok := byID[f.id]
		if !ok {
			byID[f.id] = f
			ids = append(ids, f.id)
			continue
		}
		if f.path < cur.path {
			cur.path = f.path
		}
		if strings.TrimSpace(cur.source) == "" {
			cur.source = f.source
		}
		byID[f.id] = cur
	}
	sort.Strings(ids)
	out := make([]objectFact, 0, len(ids))
	for _, id := range ids {
		out = append(out, byID[id])
	}
	return out
}

// scanCards / scanNotes / scanSources 把三个只读事实面折成同构的 objectFact 集合。
func scanCards(scan *query.ScanResult) []objectFact {
	if scan == nil {
		return nil
	}
	out := make([]objectFact, 0, len(scan.Cards))
	for _, c := range scan.Cards {
		out = append(out, objectFact{id: c.ID, path: c.Path})
	}
	return dedupByID(out)
}

func scanNotes(scan *query.ScanResult) []objectFact {
	if scan == nil {
		return nil
	}
	out := make([]objectFact, 0, len(scan.Notes))
	for _, n := range scan.Notes {
		out = append(out, objectFact{id: n.ID, path: n.Path, source: n.Source})
	}
	return dedupByID(out)
}

func scanSources(sources []SourceFact) []objectFact {
	out := make([]objectFact, 0, len(sources))
	for _, s := range sources {
		out = append(out, objectFact{id: s.ID, path: s.Path})
	}
	return dedupByID(out)
}

// —— duplicate_id（E11 / error，合同 §6.1）——

// checkR4DuplicateID 对每个「命中 ≥ 2 个文件」的 ID 产**恰一条** finding：
// targets = **全部**冲突文件的 vault 相对路径（去重 + 字典序升序，不只报第一个）。
//
// 一个 ID 一条（不是一文件一条）：这正是「与查询域 Q1 零双计数」的落点 ——
// Q1 是「第 2、第 3 个文件各记一条」的查询域诊断，对账域这里恒 1 条。
func checkR4DuplicateID(x StructureIndex) []Finding {
	var out []Finding
	for _, id := range x.sortedIDs() {
		paths := x.ObjectPaths[id]
		if len(paths) < 2 {
			continue
		}
		f, err := NewFinding(CheckDuplicateID, paths, duplicateDetail(id, x))
		if err != nil {
			continue // 防御性丢弃：只读检查不该让进程死在检查器里
		}
		out = append(out, f)
	}
	return out
}

// duplicateDetail 渲染 E11 的 detail：ID + 文件数 + 全部路径 + 对象类别（足以复算）。
func duplicateDetail(id string, x StructureIndex) string {
	paths := x.ObjectPaths[id]
	kinds := make([]string, 0, len(x.ObjectKinds[id]))
	for _, k := range x.ObjectKinds[id] {
		kinds = append(kinds, labelOf(k))
	}
	return fmt.Sprintf(
		"知识 ID %s 在 vault 内出现于 %d 个文件（%s；对象类别：%s）：同一 ID 只许一处落盘，"+
			"本次只报告——不自动改名、不自动删除、不自动移动（合同 §6.1）",
		id, len(paths), strings.Join(paths, "、"), strings.Join(kinds, "、"))
}

// labelOf 取类别的中文标签（未知类别原样返回，保证 detail 恒非空）。
func labelOf(kind string) string {
	if s, ok := kindLabel[kind]; ok {
		return s
	}
	return kind
}

// —— dangling_ref（E12 / error，合同 §6.2）——

// DanglingRefKindCount 是本码覆盖的引用类别数：**恰六类**。
//
// 历史 M4 合同 §6.2 只冻结了前两类（note.source / card.sources[].note）；
// I-evergreen.system_assurance-158614-019 的实现侧完整修复枚举并冻结了知识卡 / 笔记侧
// **全部**引用承载字段（另加 `card.sources[].source→原文` 与 `replaced_by.target→知识卡`）。
// schema v2 的观点带来第三个引用承载方，于是本码在**同一件事同一码**的原则下追加两类：
// `opinion.sources[].note→材料笔记` 与 `opinion.sources[].source→原文`；
// 观点的 `replaced_by.target` 与知识卡是同一个字段键、同一个目标类型（知识卡），
// 因此**复用第四类**、不另立类别。观点 `relations[]` 的 target 仍归 R3（E13 / E14），
// 一件事不许两码重复计。
const DanglingRefKindCount = 6

// 六类引用的机器串（进 detail，供逐条复算）。
const (
	danglingNoteSource    = "note.source→原文"
	danglingCardNote      = "card.sources[].note→材料笔记"
	danglingCardSource    = "card.sources[].source→原文"
	danglingReplacedBy    = "replaced_by.target→知识卡"
	danglingOpinionNote   = "opinion.sources[].note→材料笔记"
	danglingOpinionSource = "opinion.sources[].source→原文"
)

// refFact 是一条待判定的引用事实（引用方 / 目标 / 类别 / 引用方落盘路径）。
type refFact struct{ from, to, kind, path string }

// materialRefs 归集一个引用承载方（知识卡或观点）的 `sources[]` 两端与 `replaced_by.target`
// 三处引用事实。两类产物的字段键与目标类型逐字相同，因此判定只有这一份实现；
// 类别串由调用方按引用方传入，detail 里不会把观点的字段说成卡的字段。
//
// 三处判定口径：
//   - 来源笔记端：存在性看落盘事实（不做删除过滤，删除维度归 R7 的 W20）；
//   - 原文端：仅在 `sources/` 已采样时判定（不把「没采样」说成「不存在」）；
//   - 替代指针：`replaced_by.target` 必须指向一张真实存在的**知识卡**，
//     类别串两类产物共用（同一字段键、同一目标类型）。
func materialRefs(x StructureIndex, holder, path string, sources []model.SourceRef,
	replacedBy, noteKind, sourceKind string) []refFact {
	from := strings.TrimSpace(holder)
	if from == "" {
		return nil // 无法定位的引用方不发 finding（同 StructureIndex.add 的口径）
	}
	var out []refFact
	for _, s := range sources {
		if note := strings.TrimSpace(string(s.Note)); note != "" && !x.Has(note, KindNote) {
			out = append(out, refFact{from: from, to: note, kind: noteKind, path: path})
		}
		if src := strings.TrimSpace(string(s.Source)); src != "" &&
			x.SourcesSampled && !x.Has(src, KindSource) {
			out = append(out, refFact{from: from, to: src, kind: sourceKind, path: path})
		}
	}
	if tgt := strings.TrimSpace(replacedBy); tgt != "" && !x.Has(tgt, KindCard) {
		out = append(out, refFact{from: from, to: tgt, kind: danglingReplacedBy, path: path})
	}
	return out
}

// checkR4DanglingRef 覆盖**恰六类**引用承载字段：
//
//	① 材料笔记 frontmatter 的 `source` 指向的原文不存在；
//	② 知识卡 frontmatter `sources[].note` 指向的来源笔记不存在；
//	③ 知识卡 frontmatter `sources[].source` 指向的原文不存在；
//	④ 知识卡 / 观点 frontmatter `replaced_by.target` 指向的替代卡不存在；
//	⑤ 观点 frontmatter `sources[].note` 指向的来源笔记不存在；
//	⑥ 观点 frontmatter `sources[].source` 指向的原文不存在。
//
// `targets[]` = `[引用方 ID, 缺失的目标 ID]`。两元素经 NormalizeTargets 去重升序。
// 指向**原文**的三类（① / ③ / ⑥）只在 `sources/` 分区已采样时判定：绝不把「没采样」
// 说成「不存在」（与 R7 / query 侧 Q2 逐字同源的诚实性口径）。
//
// **关系条目的 target 缺失不走这条**：那属 R3 的 relation_target_missing（E13，归 T-…-053）——
// 一件事不许两码重复计。同理，③ 一旦被本码承载，R7 对「原文端缺失」整体让位（不再报 W20）。
func checkR4DanglingRef(x StructureIndex, in Input) []Finding {
	var refs []refFact
	if in.Scan != nil {
		for _, n := range in.Scan.Notes {
			src := strings.TrimSpace(n.Source)
			switch {
			case src == "":
				continue // 未声明所属材料属 orphan 的 note_without_source，不是悬空引用
			case !x.SourcesSampled:
				continue // sources/ 分区未采样：不判，绝不把「没采样」说成「不存在」
			case x.Has(src, KindSource):
				continue
			}
			refs = append(refs, refFact{from: strings.TrimSpace(n.ID), to: src,
				kind: danglingNoteSource, path: n.Path})
		}
		for _, c := range in.Scan.Cards {
			refs = append(refs, materialRefs(x, c.ID, c.Path, c.Sources,
				c.ReplacedByTarget, danglingCardNote, danglingCardSource)...)
		}
		// 观点的引用承载字段与知识卡同构（`sources[]` 两端 + `replaced_by.target`），
		// 因此复用同一段判定：类别串按引用方如实取，避免把观点的字段说成卡的字段。
		for _, o := range in.Scan.Opinions {
			refs = append(refs, materialRefs(x, o.ID, o.Path, o.Sources,
				o.ReplacedByTarget, danglingOpinionNote, danglingOpinionSource)...)
		}
	}
	// 去重：同一 (引用方, 缺失目标) 只报一条（一张卡的 sources[] 两条指向同一缺失笔记
	// 是**一件**事实），并按 (引用方, 缺失目标) 升序输出，与 map 迭代序无关。
	uniq := map[string]refFact{}
	keys := make([]string, 0, len(refs))
	for _, r := range refs {
		if r.from == "" || r.to == "" {
			continue
		}
		key := r.from + "\x00" + r.to
		if _, ok := uniq[key]; ok {
			continue
		}
		uniq[key] = r
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var out []Finding
	for _, key := range keys {
		r := uniq[key]
		f, err := NewFinding(CheckDanglingRef, []string{r.from, r.to},
			danglingDetail(r.from, r.to, r.kind, r.path))
		if err != nil {
			continue
		}
		out = append(out, f)
	}
	return out
}

// danglingDetail 渲染 E12 的 detail：引用方 + 引用类别 + 缺失目标 + 引用方路径（足以复算）。
func danglingDetail(from, to, kind, path string) string {
	return fmt.Sprintf(
		"悬空引用（封闭 %d 类之一 · %s）：%s 的 frontmatter 引用了 %s，该目标在 vault 内不存在；"+
			"引用方落盘于 %s。关系条目的 target 缺失不走本码（属 R3，避免两码重复计一件事）",
		DanglingRefKindCount, kind, from, to, path)
}

// —— orphan（W17 / warning，合同 §6.3）——

// checkR4Orphan 产孤儿 finding：三子类型合并为一个 check，由 detail 区分。
//
// `targets[]` = `[对象 ID]`（恰一个可定位标识；路径与子类型进 detail）。
// 产出顺序 = 子类型表行序 → 对象 ID 升序，可逐字复算。
//
// **观点不在本码的判定面内**：合同 §6.3 的子类型集合封闭为三值，三者的判定对象分别是
// 材料笔记 / 原文 / 知识卡；给观点造第四个子类型会改动这个封闭基数，不属结构一致性
// 这一格能自证的事（「观点该不该孤立存在」是论证进度问题，归 validation 生命周期批次）。
// 观点的出边**仍然**参与判定：它们已进入落盘入边表，因此「只被观点引用的知识卡」
// 不会被误判成 card_without_relation。
//
// 判定一律看**落盘事实**：
//   - 不看展示面过滤结果（唯一邻居是 deprecated / 已逻辑删除的卡**不算**孤儿）；
//   - 不因对象自身 status / deleted_at 而豁免判定（那同样是展示面口径）。
func checkR4Orphan(x StructureIndex, in Input) []Finding {
	buckets := map[string][]objectFact{}
	details := map[string]string{}
	// ① 笔记不属任何材料：frontmatter 未声明 source。
	// 已声明但目标缺失的笔记**不在**本子类型内 —— 那是 dangling_ref（E12），
	// 同一件事不许 W17 与 E12 各记一次。
	for _, n := range scanNotes(in.Scan) {
		if strings.TrimSpace(n.source) != "" {
			continue
		}
		buckets[OrphanNoteWithoutSource] = append(buckets[OrphanNoteWithoutSource], n)
		details[OrphanNoteWithoutSource+"\x00"+n.id] = fmt.Sprintf(
			"该材料笔记的 frontmatter 未声明所属材料（`source` 键缺失或为空），不属任何材料；"+
				"落盘于 %s", n.path)
	}
	// ② 材料没有任何派生笔记：仅在 `sources/` 分区已采样时判定。
	if x.SourcesSampled {
		for _, s := range scanSources(in.Sources) {
			if len(x.NotesBySource[s.id]) != 0 {
				continue
			}
			buckets[OrphanSourceWithoutNote] = append(buckets[OrphanSourceWithoutNote], s)
			details[OrphanSourceWithoutNote+"\x00"+s.id] = fmt.Sprintf(
				"该原文在全库内没有任何派生笔记（无笔记的 frontmatter source 指向它；统计只看"+
					"落盘事实，不排除已删除或失效的笔记）；落盘于 %s", s.path)
		}
	}
	// ③ 知识卡零关系：relations_out 与 relations_in **同时**为空（落盘事实）。
	for _, c := range scanCards(in.Scan) {
		outDeg, inDeg := x.OutDegree(c.id), x.InDegree(c.id)
		if outDeg != 0 || inDeg != 0 {
			continue
		}
		buckets[OrphanCardWithoutRelation] = append(buckets[OrphanCardWithoutRelation], c)
		details[OrphanCardWithoutRelation+"\x00"+c.id] = fmt.Sprintf(
			"该知识卡的 relations_out 与 relations_in 同时为空（落盘出边 %d 条 / 全库反向扫描"+
				"入边 %d 条，未做任何可见性或删除过滤）；落盘于 %s", outDeg, inDeg, c.path)
	}
	var out []Finding
	for _, subtype := range orphanSubtypeTable {
		items := buckets[subtype]
		sort.SliceStable(items, func(i, j int) bool { return items[i].id < items[j].id })
		for _, it := range items {
			f, err := NewFinding(CheckOrphan, []string{it.id},
				orphanDetail(subtype, it.id, details[subtype+"\x00"+it.id]))
			if err != nil {
				continue
			}
			out = append(out, f)
		}
	}
	return out
}

// orphanDetail 渲染 W17 的 detail：封闭子类型串 + 对象 ID + 该子类型自己的事实句。
func orphanDetail(subtype, id, fact string) string {
	return fmt.Sprintf("孤儿（子类型 %s，封闭 %d 值之一）：%s —— %s（判定看落盘事实，"+
		"不看展示面过滤结果，合同 §6.3 末段）", subtype, OrphanSubtypeCount, id, fact)
}

// —— 汇总与注册 ——

// checkR4Structure 是 R4 检查项本体：一次建索引，跑三项判定，产 finding、**零 RepairSpec**。
//
// 纯函数：同一 Input 恒得同一输出（含顺序）；不改入参、不产生任何副作用。
// Scan 为 nil（未扫描）且 Sources 为 nil（未采样）时返回空集合 —— 「没取数」不产 finding。
func checkR4Structure(in Input) ([]Finding, []RepairSpec) {
	x := NewStructureIndex(in)
	var out []Finding
	out = append(out, checkR4DuplicateID(x)...)
	out = append(out, checkR4DanglingRef(x, in)...)
	out = append(out, checkR4Orphan(x, in)...)
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// R4ScanOf 是给命令层与用例的便利函数：把 vault 快照折成 R4 需要的 Input。
//
// 存在的理由：R4 只需要「扫描结果 + `sources/` 分区快照」，不需要 Git 状态（Status 恒零值），
// 让调用方不必为了跑 R4 先取一次 Git 状态 —— 但**扫描底座仍复用 internal/query**，
// 本包不另写扫描器。sources 传 nil 表示该分区未采样（两条源侧判定整体跳过）。
func R4ScanOf(vaultRoot string, scan *query.ScanResult, sources []SourceFact) Input {
	return Input{VaultRoot: vaultRoot, Scan: scan, Sources: sources}
}

// 注册：R4 是本包第二个落地的检查项（R1 属 T-…-050）。
//
// 三项判定合并成**一项注册**：它们共用同一份索引，且合同 §6 把三者定义为同一条 R 编号。
// R2 / R3 / R5 / R6 / R7 分属 T-…-051 / 053 / 054 / 055 / 056，本文件因此**恰追加一项**。
func init() { checkers = append(checkers, checkR4Structure) }
