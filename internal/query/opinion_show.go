package query

// [B2a] `opinion show` 的**单条观点视图**组装（知识/观点读路径拆分 · T-006-B2a）。
//
// 本文件是 **query-only** 投影：只经取数层（loadVault → 索引后端 / VaultScan）读字节，
// 不写文件、不调 git、不调模型、不发网络请求，也**不接任何 CLI**（B2a 边界：opinion CLI、
// card.go / rel.go / context.go、README / SKILL 一律不碰）。它把「按合法 o-id 读一条观点」
// 的结果折成与 `card show` 同构的视图，供后续 `eg opinion show` 批零分支映射专用 DTO。
//
// # 复用而非另造（对齐 card.go 的既有单点）
//
//   - 取数：opinionNeed → SelectBackend → loadVault（索引缺失/损坏/陈旧确定性降级为全量
//     扫描并留痕 W22|W23|W24 + Q5，与三条读命令同一套 backend.go / degrade.go）；
//   - 可见性：VisibleEndpoints（对端 deprecated 默认隐藏并计 Q4，已删除对端任何 flag 下都隐藏）；
//   - 排序：SortEdges 的四级全序（正向按 target、反向按 from）；
//   - 分页：ApplyPageGroups —— ApplyPagePair 从两段到六段的推广，仍是**一个**全局 limit/offset，
//     跨段截断只产恰一条 W25；
//   - 标记：markers.go 的 Markers（[失效]→[已删除]→[未过目] 固定序）。
//
// 本文件不重新实现其中任何一个（风险 R-25：口径分叉）。
//
// # 五分区与关系分组（观点契约 §3.2）
//
// 五分区取 mdfile.OpinionSections()（观点/论据与推理/条件与反例/待验证/用户补充，固定序）；
// validation / markers / sources 逐字取自权威 frontmatter。论证关系折成 supports / limits /
// opposing **三组**，每组各有「正向（以该观点为起点）」与「反向（以该观点为终点）」两段；
// derives（以及 replaced_by 等生命周期字段）**不进**这三组。
//
// # 端点写模型边界（B2a 最小定向检查结论）
//
// model.Relation.Target 的落盘类型是 model.CardID：观点是关系的**持有方**，正向边
// `o-* → k-*` 是当前写模型能表达的真实事实（OpinionEntry.Relations）。而「卡侧不写回」
// （scan.go 注释）＋ `add_relation` 的 from/target 都过 ParseCardID（只认 k-*）意味着当前
// 写模型**无法**令任何产物以某观点为 target。因此反向段按「全库扫描 target==o-id」的通用
// 机制实现，但在写模型忠实的语料上恒为空——端点扩展（让产物以观点为终点）留 B2c，
// 本批**不**改 model/store/plan/reconcile/index 写模型、eg rel 或 schema。

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// ErrOpinionNotFound 是 `opinion show` 的**观点不存在**（形态合法但库中无此 o-id → 退 1，零副作用）。
var ErrOpinionNotFound = errors.New("观点不存在")

// ErrInvalidOpinionID 是 `<o-id>` 形态非法（不是 o-YYYYMMDD-slug → 退 1）。
var ErrInvalidOpinionID = errors.New("观点 ID 形态非法")

// OpinionSections 是观点五分区正文的**有序**映射（键序固定为 OpinionSections() 声明序）。
//
// 与 card.go 的 Sections 同构但键集合不同（观点五分区 ≠ 知识卡五分区），因此另立一型而**不**
// 改动 card.go 的 Sections（B2a 边界：不碰 card.go）。键集合恒定：缺分区**键仍在、值为空串**，
// 绝不丢键；值是该分区的原始文本（逐字取字节，只裁首尾空白，不做 Markdown 再渲染）。
type OpinionSections struct {
	vals map[string]string
}

// NewOpinionSections 按五分区声明序归一：未给出的分区补空串。
func NewOpinionSections(vals map[string]string) OpinionSections {
	out := OpinionSections{vals: map[string]string{}}
	for _, name := range mdfile.OpinionSections() {
		out.vals[name] = vals[name]
	}
	return out
}

// Keys 返回固定键序（= mdfile.OpinionSections()）。
func (s OpinionSections) Keys() []string { return mdfile.OpinionSections() }

// Get 取某分区正文（缺分区为空串）。
func (s OpinionSections) Get(name string) string { return s.vals[name] }

// Missing 报告该分区是否缺失（值为空串）。
func (s OpinionSections) Missing(name string) bool { return s.vals[name] == "" }

// MarshalJSON 按固定键序输出（不能退化成 encoding/json 的字典序）。
func (s OpinionSections) MarshalJSON() ([]byte, error) {
	var b strings.Builder
	b.WriteString("{")
	for i, name := range s.Keys() {
		if i > 0 {
			b.WriteString(",")
		}
		key, err := jsonString(name)
		if err != nil {
			return nil, err
		}
		val, err := jsonString(s.vals[name])
		if err != nil {
			return nil, err
		}
		b.Write(key)
		b.WriteString(":")
		b.Write(val)
	}
	b.WriteString("}")
	return []byte(b.String()), nil
}

// RelationGroup 是一个论证关系分组的两段视图：Forward（以该观点为起点）+ Reverse（以其为终点）。
//
// 两段都恒为**非 nil** 数组（JSON `[]`，绝不 `null`）；段内已按 SortEdges 四级全序排定。
type RelationGroup struct {
	Forward []RelationEdge `json:"forward"`
	Reverse []RelationEdge `json:"reverse"`
}

// OpinionDetail 是单条观点视图的 data 载荷（**结构体字段序即键序**，见 OpinionDataKeys）。
type OpinionDetail struct {
	ID         string            `json:"id"`
	Title      string            `json:"title"`
	Domain     string            `json:"domain"`
	Status     string            `json:"status"`
	Deprecated bool              `json:"deprecated"`
	Validation string            `json:"validation"`
	CreatedAt  string            `json:"created_at"`
	UpdatedAt  string            `json:"updated_at"`
	Path       string            `json:"path"`
	Tags       []string          `json:"tags"`
	Markers    []string          `json:"markers"`
	Sections   OpinionSections   `json:"sections"`
	Sources    []model.SourceRef `json:"sources"`
	// Supports / Limits / Opposing 是三组论证关系（固定次序）；derives 不在其中。
	Supports RelationGroup `json:"supports"`
	Limits   RelationGroup `json:"limits"`
	Opposing RelationGroup `json:"opposing"`
	// Deleted 是删除维度的判定值（`deleted_at != null`）。**追加在键表末尾**，与 card show 同手法。
	Deleted bool `json:"deleted"`
}

// OpinionDataKeys 是 `opinion show` 的 data 键次序（= OpinionDetail 字段序）。
//
// 这是**新增**投影的键集合，与 card show（CardDataKeys）/ search（SearchDataKeys）解耦：
// 本批一个既有键都不改、不扩张，新键只落在这一处。
func OpinionDataKeys() []string {
	return []string{"id", "title", "domain", "status", "deprecated", "validation",
		"created_at", "updated_at", "path", "tags", "markers", "sections", "sources",
		"supports", "limits", "opposing", "deleted"}
}

// OpinionShowResult 是一次单条观点视图查询的产物。
//
// 命名与 CardShowResult 对称：函数叫 ShowOpinion、结果叫 OpinionShowResult。
type OpinionShowResult struct {
	Opinion      OpinionDetail
	Diagnostics  []Diagnostic
	ScannedFiles int
	SkippedFiles int
	// HiddenDeprecated / DeprecatedPeers 与 card show 同义：默认视图因对端 deprecated 隐藏的
	// 关系条目数（正 + 反合计，供 Q4）与已展示条目里对端为 deprecated 的对端 ID（升序去重）。
	HiddenDeprecated int
	DeprecatedPeers  []string
	// Page 是本次分页事实（三组 × 正反共六段的一个全局分页）；不进 data。
	Page Page
	// backend 是本次取数实际走的后端。**不进 data**：降级事实经 warnings[] 承载
	// （W22|W23|W24 + Q5），这一格只服务测试与排障的可判定性。
	backend Backend
}

// opinionNeed 是 `opinion show <o-id>` 的取数要求。
//
// 依据：观点视图要回权威解析**目标观点**的五分区正文 / sources / relations，而索引后端只有
// `plan.all`（FullCardFields）才回权威解析观点条目（index_backed.go：plan.focus 只解析知识卡，
// 不投观点）。因此这里取 FullCardFields = true：候选集**保守取全集**并逐个回权威解析（同
// searchNeed 的口径），宁可多读，绝不容许目标观点退化成缺正文 / 缺 sources 的摘要 stub ——
// 那会让健康索引与降级扫描两条路径分叉。窄化召回属后续性能工作，本批一行不做。
func opinionNeed(deps IndexDeps) Need {
	return Need{Path: "opinion show", FullCardFields: true, deps: deps,
		Why: "观点视图需回权威解析目标观点的五分区 / sources / relations，" +
			"且反向关系要在全库卡与观点面上收集：候选集取全集、逐个回权威解析，不窄化"}
}

// ShowOpinion 是不带分页的单条观点视图（零值 PageSpec == 不限量）。
func ShowOpinion(root string, id model.OpinionID, deps IndexDeps,
	opts ...VisibilityPolicy) (*OpinionShowResult, error) {
	return ShowOpinionPaged(root, id, deps, PageSpec{}, opts...)
}

// ShowOpinionPaged 组装带分页的单条观点视图：全库定位观点 → 五分区正文 → sources[] →
// supports / limits / opposing 三组（每组正 + 反两段）。
//
// 定位面是**全库**（观点 ID 全库唯一，不限定领域）。失效 / 已删除的观点照常**显式**展示并
// 置相应 markers（删除维度只影响它作为**关系对端**时是否被隐藏，不影响显式查看自身）。
func ShowOpinionPaged(root string, id model.OpinionID, deps IndexDeps, page PageSpec,
	opts ...VisibilityPolicy) (*OpinionShowResult, error) {
	if err := page.Validate(); err != nil {
		return nil, err
	}
	pol := DefaultVisibility()
	if len(opts) > 0 {
		pol = opts[0]
	}
	if !id.Valid() {
		return nil, fmt.Errorf("%w：%q 不是 o-YYYYMMDD-slug 形态的观点 ID",
			ErrInvalidOpinionID, string(id))
	}
	need := opinionNeed(deps)
	scan, backend, err := loadVault(root, ScanOptions{}, need, SelectBackend(root, need))
	if err != nil {
		return nil, err
	}
	// scan.Opinions 已按 path 升序稳定排序，故首个同 ID 命中即路径字典序最小者。
	var target *OpinionEntry
	for i := range scan.Opinions {
		if scan.Opinions[i].ID == string(id) {
			target = &scan.Opinions[i]
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("%w：%s 在库中不存在（已全库扫描 %d 个 .md）",
			ErrOpinionNotFound, string(id), scan.ScannedFiles)
	}

	// 可见性宇宙 = 知识卡 ∪ 观点（只取 ID / Deleted / Deprecated 三格，供 VisibleEndpoints
	// 判定对端可见性）。正向对端多为知识卡，反向来源两类都可能，故两类都纳入。
	universe := opinionVisibilityUniverse(scan)

	// 正向：观点自身 relations[]（排除 derives），按 target 升序。
	fwd := opinionForwardEdges(*target)
	// 反向：全库扫描 target==o-id（排除 derives、排除自身），按 from 升序。
	//   写模型忠实语料上恒空（「卡侧不写回」＋ target 只认 k-*），端点扩展留 B2c。
	rev := opinionReverseEdges(scan, string(id))

	// 可见性过滤（与 card show 同：默认隐藏对端已删除 + 对端 deprecated）。
	fwd, hiddenF := VisibleEndpoints(universe, fwd, func(e RelationEdge) string { return e.Target }, pol)
	rev, hiddenR := VisibleEndpoints(universe, rev, func(e RelationEdge) string { return e.From }, pol)
	hidden := hiddenF + hiddenR

	// 按类型分三组（derives 已在上一步排除）。
	sF, lF, oF := groupByRelationType(fwd)
	sR, lR, oR := groupByRelationType(rev)

	// 分页：一个全局 limit/offset 跨六段（group-major、正向在前），截断只产恰一条 W25。
	segs, pg := ApplyPageGroups([][]RelationEdge{sF, sR, lF, lR, oF, oR}, page)

	// 已展示条目里对端为 deprecated 的对端 ID（升序去重）：默认视图为空，仅 --include-deprecated 非空。
	shownFwd := append(append(append([]RelationEdge{}, segs[0]...), segs[2]...), segs[4]...)
	shownRev := append(append(append([]RelationEdge{}, segs[1]...), segs[3]...), segs[5]...)
	depPeers := mergeSortedUnique(
		deprecatedPeerSet(universe, shownFwd, func(e RelationEdge) string { return e.Target }),
		deprecatedPeerSet(universe, shownRev, func(e RelationEdge) string { return e.From }))

	res := &OpinionShowResult{
		Opinion: OpinionDetail{
			ID: target.ID, Title: target.Title, Domain: target.Domain,
			Status: target.Status, Deprecated: target.Deprecated,
			Validation: target.Validation,
			CreatedAt:  target.CreatedAt, UpdatedAt: target.UpdatedAt, Path: target.Path,
			Tags:     stringsOrEmpty(target.Tags),
			Markers:  Markers(MarkerState{Deprecated: target.Deprecated, Deleted: target.Deleted}),
			Sections: opinionSections(*target),
			Sources:  sourcesOrEmpty(target.Sources),
			Supports: RelationGroup{Forward: segs[0], Reverse: segs[1]},
			Limits:   RelationGroup{Forward: segs[2], Reverse: segs[3]},
			Opposing: RelationGroup{Forward: segs[4], Reverse: segs[5]},
			Deleted:  target.Deleted,
		},
		backend: backend,
		Diagnostics: withTruncationDiagnostic(withIndexDegradedDiagnostics(
			withDeprecatedHiddenDiagnostic(scan.Diagnostics, hidden, pol.IncludeDeprecated),
			degradeDiagnostics(backend)), pg.Truncated, pg, "关系条目"),
		Page:             pg,
		ScannedFiles:     scan.ScannedFiles,
		SkippedFiles:     scan.SkippedFiles,
		HiddenDeprecated: hidden,
		DeprecatedPeers:  depPeers,
	}
	return res, nil
}

// opinionVisibilityUniverse 把知识卡与观点折成 VisibleEndpoints 所需的 CardEntry 面
// （只读 ID / Deleted / Deprecated 三格）：正向对端多为知识卡，反向来源两类都可能。
func opinionVisibilityUniverse(scan *ScanResult) []CardEntry {
	u := make([]CardEntry, 0, len(scan.Cards)+len(scan.Opinions))
	u = append(u, scan.Cards...)
	for _, o := range scan.Opinions {
		u = append(u, CardEntry{ID: o.ID, Deleted: o.Deleted, Deprecated: o.Deprecated})
	}
	return u
}

// opinionForwardEdges 折出观点自身 relations[] 的正向边（**排除 derives**），按 target 升序
// （复用 SortEdges 四级全序；与 RelationsOut 逐字同构，只是端点是观点、且滤掉 derives）。
func opinionForwardEdges(o OpinionEntry) []RelationEdge {
	out := []RelationEdge{}
	for _, rel := range o.Relations {
		if rel.Type == model.RelationDerives {
			continue
		}
		out = append(out, RelationEdge{From: o.ID, Type: string(rel.Type),
			Target: string(rel.Target), Reason: rel.Reason, Path: o.Path})
	}
	SortEdges(out, func(e RelationEdge) string { return e.Target })
	return out
}

// opinionReverseEdges 扫描全库、折出以该观点为 target 的反向边（**排除 derives、排除自身**），
// 按 from 升序。知识卡与观点两类持有方都扫（与 RelationsIn 同构）。
//
// 写模型忠实语料上恒空：当前写模型无法令任何产物以观点为 target（见文件头端点边界）。
func opinionReverseEdges(scan *ScanResult, id string) []RelationEdge {
	out := []RelationEdge{}
	collect := func(from, path string, rels []model.Relation) {
		if from == id {
			return
		}
		for _, rel := range rels {
			if rel.Type == model.RelationDerives || string(rel.Target) != id {
				continue
			}
			out = append(out, RelationEdge{From: from, Type: string(rel.Type),
				Target: id, Reason: rel.Reason, Path: path})
		}
	}
	for _, c := range scan.Cards {
		collect(c.ID, c.Path, c.Relations)
	}
	for _, o := range scan.Opinions {
		collect(o.ID, o.Path, o.Relations)
	}
	SortEdges(out, func(e RelationEdge) string { return e.From })
	return out
}

// groupByRelationType 把（已排除 derives 的）关系边分到 supports / limits / opposing 三桶，
// 保序（上游 SortEdges 已排定）。未知类型一律跳过（与 summarizeOpinionRelations 同口径）。
func groupByRelationType(edges []RelationEdge) (supports, limits, opposing []RelationEdge) {
	supports, limits, opposing = []RelationEdge{}, []RelationEdge{}, []RelationEdge{}
	for _, e := range edges {
		switch e.Type {
		case string(model.RelationSupports):
			supports = append(supports, e)
		case string(model.RelationLimits):
			limits = append(limits, e)
		case string(model.RelationOpposing):
			opposing = append(opposing, e)
		}
	}
	return supports, limits, opposing
}

// opinionSections 取五分区正文：复用 mdfile 的分区索引，**不重写解析**。
// 缺分区置空串（键集合恒定）；分区缺失不是 Q 类（文件本身可解析）。
func opinionSections(o OpinionEntry) OpinionSections {
	vals := map[string]string{}
	if o.Doc == nil {
		return NewOpinionSections(vals)
	}
	for _, name := range mdfile.OpinionSections() {
		span, ok := o.Doc.Section(name)
		if !ok {
			continue
		}
		if span.Body > len(o.Raw) || span.End > len(o.Raw) || span.Body > span.End {
			continue
		}
		vals[name] = strings.TrimSpace(string(o.Raw[span.Body:span.End]))
	}
	return NewOpinionSections(vals)
}
