package query

// `eg card show` 的单卡视图组装（M2 查询合同 `2026-09-19-m2-query-contract.md` §2；T-…-022）。
//
// 只读：本文件只经取数层（loadVault → 索引后端 / VaultScan）读字节，
// 不写文件、不调 git、不调模型、不发网络请求。
//
// **复用 T-…-020 的扫描底座**：定位目标卡与反向关系都走全库 VaultScan
// （`walkMarkdown` 是全包唯一遍历入口），正向 / 反向排序走 SortEdges（§3.3），
// 诊断走 Q1–Q3（§5）。不建索引、不引数据库（均属 S4）。

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// ErrCardNotFound 是 `card show` 的**卡不存在**（合同 §2.1 / §2.3 → 退 1，零副作用）。
var ErrCardNotFound = errors.New("知识卡不存在")

// ErrInvalidCardID 是 `<k-id>` 形态非法（合同 §2.1 → 退 1）。
var ErrInvalidCardID = errors.New("知识卡 ID 形态非法")

// MarkerDeprecated 是 S1 起就冻结的失效卡标记（合同 §1.5 / §2.2），**口径不改**。
// S2 新增的两个标记（删除维度 / 过目维度）与其顺序见 markers.go；S3 才引入的第四个标记
// 在 M3 不判定、不输出。
const MarkerDeprecated = "[失效]"

// Sections 是五分区正文的**有序**映射（合同 §2.2：键序固定为五分区声明序 F5）。
//
// 键集合恒定：缺分区**键仍在、值为空串**，绝不丢键；值是该分区的原始文本
// （逐字取字节，只裁掉首尾空白，不做 Markdown 再渲染）。
type Sections struct {
	vals map[string]string
}

// NewSections 按五分区声明序归一：未给出的分区补空串。
func NewSections(vals map[string]string) Sections {
	out := Sections{vals: map[string]string{}}
	for _, name := range mdfile.CardSections() {
		out.vals[name] = vals[name]
	}
	return out
}

// Keys 返回固定键序（= mdfile.CardSections()，F5 声明序）。
func (s Sections) Keys() []string { return mdfile.CardSections() }

// Get 取某分区正文（缺分区为空串）。
func (s Sections) Get(name string) string { return s.vals[name] }

// Missing 报告该分区是否缺失（值为空串）：人类可读渲染据此标注「（本分区缺失）」。
// 分区缺失**不属于** Q 系列（文件本身可解析），因此不计入 skipped_files（合同 §2.2）。
func (s Sections) Missing(name string) bool { return s.vals[name] == "" }

// MarshalJSON 按固定键序输出（不能退化成 encoding/json 的字典序）。
func (s Sections) MarshalJSON() ([]byte, error) {
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

// CardDetail 是单卡视图的 data 载荷（合同 §2.2 键表，**结构体字段序即键序**）。
type CardDetail struct {
	ID           string            `json:"id"`
	Title        string            `json:"title"`
	Domain       string            `json:"domain"`
	Status       string            `json:"status"`
	Deprecated   bool              `json:"deprecated"`
	CreatedAt    string            `json:"created_at"`
	UpdatedAt    string            `json:"updated_at"`
	Path         string            `json:"path"`
	Tags         []string          `json:"tags"`
	Markers      []string          `json:"markers"`
	Sections     Sections          `json:"sections"`
	Sources      []model.SourceRef `json:"sources"`
	RelationsOut []RelationEdge    `json:"relations_out"`
	RelationsIn  []RelationEdge    `json:"relations_in"`
	// Deleted / Unreviewed 是 S2 两个新维度的判定值（合同 §6.2 的 `deleted` / `unreviewed`）。
	// **追加在键表末尾**：§2.2 既有 14 键的名字与次序一字不改，只多两个新维度的事实字段。
	// Unreviewed 由命令层经 WithUnreviewed 注入（判定只在 internal/query/filter，ADR-20）。
	Deleted    bool `json:"deleted"`
	Unreviewed bool `json:"unreviewed"`
}

// CardDataKeys 是 `card show` 的 data 键次序（合同 §2.2 键表次序）。
func CardDataKeys() []string {
	return []string{"id", "title", "domain", "status", "deprecated", "created_at",
		"updated_at", "path", "tags", "markers", "sections", "sources",
		"relations_out", "relations_in", FieldDeleted, FieldUnreviewed}
}

// CardShowResult 是一次单卡视图查询的产物。
//
// 命名说明：函数叫 ShowCard、结果叫 CardShowResult，是因为包内 CardView 这个名字
// 早在 M1 就被 eg context 的「同领域 active 卡」视图结构体占用（context.go），
// 不改 M1 已对外的类型名。
//
// MissingTargets 是 relations_out[] 里**目标不存在**的对端 ID（升序去重）：
// 条目本身照常展示（合同 §2.2 不允许给元素加第六个键），人类可读渲染据此标注
// 「目标不存在」，同时 Q2 诊断进 warnings[]。
type CardShowResult struct {
	Card           CardDetail
	MissingTargets []string
	Diagnostics    []Diagnostic
	ScannedFiles   int
	SkippedFiles   int
	// UpdatedAt / ReviewedAt 是过目维度判定所需的两个**逐字原值**（缺省即空串）。
	// 本包不比较它们：把原值交给命令层，由 internal/query/filter 判定后经 WithUnreviewed
	// 注入回 Card（ADR-20 要求那个比较只出现在筛选器一个文件里）。
	UpdatedAt  string
	ReviewedAt string
	// backend 是本次取数实际走的后端（M5 · T-…-067）。**不进 data**：合同 §6.4 定死
	// data 键集合不扩张，降级事实经 warnings[] 承载（W22|W23|W24 + Q5）。
	// 这一格只服务测试与排障的可判定性。
	backend Backend
	// DeletedAt / DeletedReason 是删除维度的原值：显式查看时如实交代「何时删、为什么删」。
	DeletedAt     string
	DeletedReason string
	// HiddenDeprecated 是**因对端 deprecated 而在默认视图被隐藏**的关系条目数（正向 + 反向合计，
	// T-…-061）。渲染层据此决定是否产出 Q4；不进 data（A-38：data 键集合不扩张）。
	HiddenDeprecated int
	// DeprecatedPeers 是**已展示**的关系条目里对端为 deprecated 的对端 ID（升序去重）：
	// 只在 --include-deprecated 放开后非空，供人类可读渲染标注 [失效]。
	DeprecatedPeers []string
	// Page 是本次分页的合计事实（正向 + 反向关系；S4 · T-…-068）。不进 data（合同 §8.3）。
	Page Page
}

// ShowCard 是 M2 冻结的单卡视图签名：**逐字不动**（S2 起的调用方与用例都在用它）。
//
// 它等价于「不注入 A-44 水位线口径的 ShowCardWith」：证不出索引新鲜度 ⇒ 取数走全量
// Markdown 扫描（不是降级，无诊断码、无 Q5），结果与索引后端一字不差。
// 生产读路径（`eg card show`）走 ShowCardWith 并注入口径，见 internal/cli/card.go。
func ShowCard(root string, id model.CardID, opts ...VisibilityPolicy) (*CardShowResult, error) {
	return ShowCardWith(root, id, IndexDeps{}, opts...)
}

// ShowCardWith 组装单卡视图：全库定位卡 → 五分区正文 → sources[] → 正向 / 反向关系。
//
// 定位面是**全库**（卡 ID 全库唯一，故不限定领域，合同 §2.1）。
// 同一 ID 出现在两处 → 取路径字典序最小者并由扫描层记一条 Q1（如实说明重复），
// **不静默择一**。失效卡照常展示并置 markers = ["[失效]"]（合同 §2.2）。
//
// deps 是 A-44 水位线判定的注入口径（见 backend.go 的 IndexDeps）：给了才可能走索引
// 后端。**为什么不是改 ShowCard 的签名**：M1–M4 的既有用例与调用方按原签名冻结，
// S4 不为了接一个参数去动历史签名 —— 新增注入形态作兄弟入口，两者共用这一份实现，
// 因此不可能出现「两条 card show 语义」。
func ShowCardWith(root string, id model.CardID, deps IndexDeps,
	opts ...VisibilityPolicy) (*CardShowResult, error) {
	return ShowCardPaged(root, id, deps, PageSpec{}, opts...)
}

// ShowCardPaged 是**带分页**的单卡视图（S4 · T-…-068，合同 §8.2：分页作用于关系列表）。
//
// 为什么另开一个入口而不是给 ShowCardWith 加参数：`ShowCard` / `ShowCardWith` 两个签名
// 自 M2 / M5-067 起就被大量调用方与用例消费，**签名逐字不动**是既有结论的一部分；
// 分页是新能力，就给新入口。零值 PageSpec == 不限量，因此两个老入口的行为一字不变。
func ShowCardPaged(root string, id model.CardID, deps IndexDeps, page PageSpec,
	opts ...VisibilityPolicy) (*CardShowResult, error) {
	if err := page.Validate(); err != nil {
		return nil, err
	}
	pol := DefaultVisibility()
	if len(opts) > 0 {
		pol = opts[0]
	}
	if !id.Valid() {
		return nil, fmt.Errorf("%w：%q 不是 k-YYYYMMDD-slug 形态的知识卡 ID", ErrInvalidCardID, string(id))
	}
	// 取数：后端选择走**唯一单点**（索引健康 → 索引后端按 relations.dst_id 收敛出
	// 「必须回权威解析的文件」；缺失 / 损坏 / 陈旧 → 确定性降级为全量扫描 + W2x + Q5）。
	need := cardNeed(string(id), deps)
	scan, backend, err := loadVault(root, ScanOptions{}, need, SelectBackend(root, need))
	if err != nil {
		return nil, err
	}
	// scan.Cards 已按 path 升序稳定排序，故首个同 ID 命中即路径字典序最小者。
	var target *CardEntry
	for i := range scan.Cards {
		if scan.Cards[i].ID == string(id) {
			target = &scan.Cards[i]
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("%w：%s 在库中不存在（已全库扫描 %d 个 .md）",
			ErrCardNotFound, string(id), scan.ScannedFiles)
	}

	// 关系端点按可见性策略过滤（合同 §5.1 真值表「作为关系端点默认展示」列）：
	// 默认隐藏对端 deprecated 与对端已删除；--include-deprecated 只放开 deprecated（T-…-061）。
	// **记录不动**：过滤只发生在读路径，源文件 frontmatter 的 relations[] 一条不少、一字不改。
	//
	// 端点宇宙（可见性 / 悬空 / deprecated 计数）= 知识卡 ∪ 观点（endpointUniverse）：schema v2
	// 的写路径允许一张卡以 k/o 端点为 target（k→o），而反向来源既可能是卡（k→k）也可能是观点
	// （o→k）。若仍只用 scan.Cards 作宇宙，k→o 的有效目标会被误判悬空、观点对端的 deprecated /
	// deleted 可见性判不出、o→k 反向边整条丢失——因此正向 target 侧、反向 from 侧统一用同一个
	// 折叠宇宙，反向扫描也从 RelationsIn（仅卡）升级为 RelationsInAll（卡 ∪ 观点）。
	universe := endpointUniverse(scan)
	out, hiddenOut := VisibleEndpoints(universe, RelationsOut(*target), func(e RelationEdge) string {
		return e.Target
	}, pol)
	in, hiddenIn := VisibleEndpoints(universe, RelationsInAll(scan, string(id)), func(e RelationEdge) string {
		return e.From
	}, pol)
	hidden := hiddenOut + hiddenIn
	// 分页施加在可见性过滤**之后**、组装 data 之前（合同 §8.2），且是**一个全局**
	// limit/offset：正反两个关系列表先合并成一条确定序列，全局取区间后再切回两段
	// （ApplyPagePair）。因此 `--limit N` 一次最多返回 N 条条目 —— 不是每段各 N 条、
	// 合计 2N —— 截断也只判一次，只产**恰一条** W25。
	out, in, pg := ApplyPagePair(out, in, page)
	depPeers := mergeSortedUnique(
		deprecatedPeerSet(universe, out, func(e RelationEdge) string { return e.Target }),
		deprecatedPeerSet(universe, in, func(e RelationEdge) string { return e.From }))
	res := &CardShowResult{
		Card: CardDetail{
			ID: target.ID, Title: target.Title, Domain: target.Domain,
			Status: target.Status, Deprecated: target.Deprecated,
			CreatedAt: target.CreatedAt, UpdatedAt: target.UpdatedAt, Path: target.Path,
			Tags: stringsOrEmpty(target.Tags), Markers: cardMarkers(*target),
			Sections: cardSections(*target), Sources: sourcesOrEmpty(target.Sources),
			RelationsOut: out, RelationsIn: in,
			Deleted: target.Deleted,
		},
		MissingTargets: missingTargets(universe, out),
		backend:        backend,
		Diagnostics: withTruncationDiagnostic(withIndexDegradedDiagnostics(
			withDeprecatedHiddenDiagnostic(scan.Diagnostics, hidden, pol.IncludeDeprecated),
			degradeDiagnostics(backend)), pg.Truncated, pg, "关系条目"),
		Page:             pg,
		ScannedFiles:     scan.ScannedFiles,
		SkippedFiles:     scan.SkippedFiles,
		UpdatedAt:        target.UpdatedAt,
		ReviewedAt:       target.ReviewedAt,
		DeletedAt:        target.DeletedAt,
		DeletedReason:    target.DeletedReason,
		HiddenDeprecated: hidden,
		DeprecatedPeers:  depPeers,
	}
	return res, nil
}

// mergeSortedUnique 合并两个已排序去重的 ID 切片，返回升序去重结果。
func mergeSortedUnique(a, b []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range append(append([]string{}, a...), b...) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// cardMarkers 给出显著标记：顺序与字面量的唯一来源是 markers.go（合同 §6.2）。
//
// 这里只带上本包能判定的两个维度；过目维度由命令层经 WithUnreviewed 注入后**重算**
// 同一份渲染函数，因此顺序在两条路径上必然一致。
func cardMarkers(c CardEntry) []string { return Markers(CardMarkerState(c)) }

// WithUnreviewed 返回注入了过目维度判定值的副本：`unreviewed` 字段与 markers 一并重算。
//
// 为什么是「注入」而不是「本包自己判」：ADR-20 把 `updated_at > reviewed_at` 定成只读信号，
// 判定隔离在 internal/query/filter 单文件内，而本包是排序 / 关系分析 / 综述取材的当前宿主，
// 依赖闭包里不得出现那个包。命令层判完把布尔值交回来，标记顺序仍由本包一处定义。
//
// 值语义（入参与返回都是副本）：不改任何入参，也不缓存任何状态。
func WithUnreviewed(c CardDetail, unreviewed bool) CardDetail {
	c.Unreviewed = unreviewed
	c.Markers = Markers(MarkerState{
		Deprecated: c.Deprecated, Deleted: c.Deleted, Unreviewed: unreviewed,
	})
	return c
}

// VisibilityPolicy 是关系端点可见性策略（T-…-061，owner 裁决②落地）。
//
// 默认策略（零值）= 隐藏**对端已删除** + 隐藏**对端 deprecated**（M3 §5.1 真值表第 2/3/4 行
// 的「作为关系端点默认展示 🔴」，此前 M2 只落地了删除维度，deprecated 维度是 K-043-01 缺口）。
// IncludeDeprecated=true **只**放开 deprecated 维度，**绝不**放开删除维度——两个维度正交，
// `--include-deprecated` 一个 flag 只影响一个维度（合同 §3.2 正交性）。
type VisibilityPolicy struct {
	// IncludeDeprecated 为真时展示对端 deprecated 的条目（仍带 [失效] 标记，见渲染层）。
	// 对端已删除者**任何取值下都隐藏**。
	IncludeDeprecated bool
}

// DefaultVisibility 返回默认策略：同时隐藏已删除与 deprecated 对端。
func DefaultVisibility() VisibilityPolicy { return VisibilityPolicy{} }

// VisibleEndpoints 按可见性策略过滤关系条目（保序，排序已在上游完成），
// 并返回**因 deprecated 被隐藏**的条目数（不含已删除——删除是任何 flag 下都隐藏的独立维度）。
//
// peerOf 给出「对端」：正向比 target、反向比 from。**筛选看对端 status，不看被查询卡自身**
// （合同 §3.3「对端」定义；`replaced_by` 链两方向的非对称即由此自然成立：旧卡看新卡=看 active 对端→可见，
// 新卡看旧卡=看 deprecated 对端→默认隐藏）。对端不在库中（悬空引用）时条目**照常保留**
// ——那是 Q2 诊断的职责，与端点可见性是两件不同的事，不能混成一次静默丢弃。
//
// 记录不动：本函数只过滤读出来的视图，源文件 relations[] 的条目数与字节不受影响（U-01）。
func VisibleEndpoints(cards []CardEntry, edges []RelationEdge, peerOf func(RelationEdge) string, pol VisibilityPolicy) ([]RelationEdge, int) {
	deleted := map[string]bool{}
	deprecated := map[string]bool{}
	for _, c := range cards {
		if c.Deleted {
			deleted[c.ID] = true
		}
		if c.Deprecated {
			deprecated[c.ID] = true
		}
	}
	out := []RelationEdge{}
	hiddenDeprecated := 0
	for _, e := range edges {
		peer := peerOf(e)
		if deleted[peer] {
			continue // 对端已删除：任何 flag 下都隐藏（删除维度优先，不计入 deprecated 计数）
		}
		if deprecated[peer] && !pol.IncludeDeprecated {
			hiddenDeprecated++
			continue // 对端 deprecated 且未显式放开：默认隐藏
		}
		out = append(out, e)
	}
	return out, hiddenDeprecated
}

// deprecatedPeerSet 收集 edges 中对端为 deprecated 的对端 ID（升序去重）。
// 供渲染层给「已显式放开而展示出来的失效对端」标注 [失效]（合同 §2.2 标记文案不变）。
func deprecatedPeerSet(cards []CardEntry, edges []RelationEdge, peerOf func(RelationEdge) string) []string {
	dep := map[string]bool{}
	for _, c := range cards {
		if c.Deprecated {
			dep[c.ID] = true
		}
	}
	seen := map[string]bool{}
	out := []string{}
	for _, e := range edges {
		p := peerOf(e)
		if dep[p] && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// cardSections 取五分区正文：复用 mdfile 的分区索引，**不重写解析**。
// 缺分区置空串（键集合恒定）；分区缺失不是 Q 类（文件本身可解析）。
func cardSections(c CardEntry) Sections {
	vals := map[string]string{}
	if c.Doc == nil {
		return NewSections(vals)
	}
	for _, name := range mdfile.CardSections() {
		span, ok := c.Doc.Section(name)
		if !ok {
			continue
		}
		if span.Body > len(c.Raw) || span.End > len(c.Raw) || span.Body > span.End {
			continue
		}
		vals[name] = strings.TrimSpace(string(c.Raw[span.Body:span.End]))
	}
	return NewSections(vals)
}

// missingTargets 收集正向关系里目标卡不存在的对端 ID（升序去重，确定性）。
func missingTargets(cards []CardEntry, out []RelationEdge) []string {
	known := map[string]bool{}
	for _, c := range cards {
		known[c.ID] = true
	}
	seen := map[string]bool{}
	miss := []string{}
	for _, e := range out {
		if e.Target == "" || known[e.Target] || seen[e.Target] {
			continue
		}
		seen[e.Target] = true
		miss = append(miss, e.Target)
	}
	sort.Strings(miss)
	return miss
}

// sourcesOrEmpty 把 nil 归一成空数组（合同 §2.2：无值时是 `[]` 而不是 `null`）。
func sourcesOrEmpty(in []model.SourceRef) []model.SourceRef {
	if in == nil {
		return []model.SourceRef{}
	}
	return in
}
