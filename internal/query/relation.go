package query

// `eg rel` 读路径的正向 / 反向关系视图（M2 查询合同
// `2026-09-19-m2-query-contract.md` §3；T-…-023）。
//
// 只读：本文件只经取数层（loadVault → 索引后端 / VaultScan）读字节，
// 不写文件、不调 git、不调模型、不发网络请求。
//
// **复用 T-…-020 的扫描底座**：一次全库 VaultScan（`Domains: nil`）同时喂正向与反向
// （`walkMarkdown` 是全包唯一遍历入口），正向走 RelationsOut、反向走 RelationsIn、
// 排序走 SortEdges（§3.3），诊断走 Q1–Q3（§5）。
// **不建索引、不引 SQLite / FTS5**（均属 S4）；不做多跳展开（S2）、不做截断分页（S4）。

import (
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// RelRequest 是一次关系查询的输入（合同 §3.1 参数表）。
//
// To 对应 `--to <id>`：只保留**对端** == 该 ID 的条目（正向比 target、反向比 from）；
// 空 = 不过滤。
// IncludeDeprecated 对应 `--include-deprecated`（T-…-061，owner 裁决②）：默认 false =
// 隐藏对端 deprecated 的条目；true 只放开 deprecated 维度，**不影响**已删除维度（正交）。
type RelRequest struct {
	ID                model.CardID
	To                string
	IncludeDeprecated bool
	// Index 是 A-44 水位线判定所需的注入口径（S4，见 backend.go 的 IndexDeps）。
	// 零值合法：证不出新鲜度即走全量扫描（无诊断码、无 Q5），结果一字不差。
	Index IndexDeps
	// Page 是分页口径（S4 · T-…-068，合同 §8.2）：**零值 = 不限量**，正反两个列表各自分页。
	Page PageSpec
	// ReplacedBy 对应 `--replaced-by`（S4 · T-…-068，合同 §8.4）：把视图从
	// **论证关系**切到**替代指针**——正向 = 谁取代了本卡（至多一条），
	// 反向 = 本卡取代了谁（0..N 条）。data 键与元素键一格不变（仍各恰五键），
	// 可见性 / 诊断 / 排序全部复用既有单点，不引入第二套规则。
	ReplacedBy bool
}

// RelData 是 `eg rel` 的 data 载荷（合同 §3.1 键表，**结构体字段序即键序**）。
type RelData struct {
	ID           string         `json:"id"`
	RelationsOut []RelationEdge `json:"relations_out"`
	RelationsIn  []RelationEdge `json:"relations_in"`
	ScannedFiles int            `json:"scanned_files"`
	SkippedFiles int            `json:"skipped_files"`
}

// RelDataKeys 是 `eg rel` 的 data 键次序（合同 §3.1）。
func RelDataKeys() []string {
	return []string{"id", "relations_out", "relations_in", "scanned_files", "skipped_files"}
}

// RelEdgeKeys 是关系条目的键次序（合同 §3.1：元素键恰五项，正反向同构）。
func RelEdgeKeys() []string {
	return []string{"from", "type", "target", "reason", "path"}
}

// RelResult 是一次关系查询的产物。
//
// MissingTargets 是正向条目里**目标不存在**的对端 ID（升序去重）：条目本身照常输出
// （合同 §3.1 不允许给元素加第六个键），人类可读渲染据此标注「目标不存在」，
// 同时 Q2 诊断进 warnings[]。
type RelResult struct {
	Data           RelData
	MissingTargets []string
	Diagnostics    []Diagnostic
	// backend 是本次取数实际走的后端（M5 · T-…-067）。**不进 data**：合同 §6.4 定死
	// data 键集合不扩张，降级事实经 warnings[] 承载（W22|W23|W24 + Q5）。
	// 这一格只服务测试与排障的可判定性。
	backend Backend
	// HiddenDeprecated 是因对端 deprecated 而在默认视图被隐藏的条目数（正向 + 反向合计，T-…-061）。
	// 不进 data（A-38：data 键集合不扩张），只供渲染层决定是否产 Q4。
	HiddenDeprecated int
	// DeprecatedPeers 是**已展示**条目里对端为 deprecated 的对端 ID（升序去重）：
	// 只在 --include-deprecated 放开后非空，供人类可读渲染标注 [失效]。
	DeprecatedPeers []string
	// Page 是本次分页的合计事实（正向 + 反向；S4 · T-…-068）。不进 data（合同 §8.3）。
	Page Page
}

// RelView 组装关系视图：全库扫描 → 正向读本卡 frontmatter → 反向反查「谁指向了我」。
//
// 正向 `relations_out[]`：只读本卡 frontmatter 的 `relations[]`，`from` 恒为本卡 ID。
// 反向 `relations_in[]`：**全库 Markdown 反向扫描**，`from` 为写下该条关系的卡。
// `opposing` **单向存储**（EG-CVG-05）：库里只有一条记录，读路径**不补对称条目、不去重合并**。
func RelView(root string, req RelRequest) (*RelResult, error) {
	if !req.ID.Valid() {
		return nil, fmt.Errorf("%w：%q 不是 k-YYYYMMDD-slug 形态的知识卡 ID",
			ErrInvalidCardID, string(req.ID))
	}
	if err := req.Page.Validate(); err != nil {
		return nil, err
	}
	if req.To != "" && !model.CardID(req.To).Valid() {
		return nil, fmt.Errorf("%w：--to=%q 不是 k-YYYYMMDD-slug 形态的知识卡 ID",
			ErrInvalidCardID, req.To)
	}
	// 取数：后端选择走**唯一单点**（索引健康 → 索引后端按 relations.dst_id 精确反查出
	// 来源卡再回权威取逐字 reason；缺失 / 损坏 / 陈旧 → 确定性降级为全量扫描 + W2x + Q5）。
	need := relNeed(string(req.ID), req.Index)
	scan, backend, err := loadVault(root, ScanOptions{}, need, SelectBackend(root, need))
	if err != nil {
		return nil, err
	}
	// scan.Cards 已按 path 升序稳定排序：同 ID 重复时首个命中即路径字典序最小者
	// （重复本身由扫描层记 Q1，不静默择一）。
	var target *CardEntry
	for i := range scan.Cards {
		if scan.Cards[i].ID == string(req.ID) {
			target = &scan.Cards[i]
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("%w：%s 在库中不存在（已全库扫描 %d 个 .md）",
			ErrCardNotFound, string(req.ID), scan.ScannedFiles)
	}

	// 顺序要点（T-…-061 修正）：**先按 --to 收窄原始边，再做可见性过滤 / 计数**。
	// 两者正交：--to 收窄「看哪个对端」，--include-deprecated 决定「deprecated 对端可见性」。
	// 旧实现先算 hidden 再在 --to 分支无条件 hidden=0，导致 `eg rel X --to <deprecated>` 默认视图
	// 明明把对端隐藏了却不报 Q4（违反 Q4 的 N≥1 条件与正交性）；这里改为收窄在前、可见性在后。
	// 视图切换（T-…-068）：默认是论证关系 relations[]；`--replaced-by` 换成替代指针的
	// 正反双向（reverse.go）。两种视图**同构**：都是五键条目、同一套排序、同一套可见性。
	rawOut, rawIn := RelationsOut(*target), RelationsInAll(scan, string(req.ID))
	if req.ReplacedBy {
		rawOut = ReplacedByForward(root, scan.Cards, string(req.ID))
		rawIn = ReplacedByReverse(root, scan.Cards, string(req.ID))
	}
	if req.To != "" {
		rawOut = filterEdges(rawOut, func(e RelationEdge) bool { return e.Target == req.To })
		rawIn = filterEdges(rawIn, func(e RelationEdge) bool { return e.From == req.To })
	}
	// 关系端点按可见性策略过滤（合同 §5.1 真值表「作为关系端点默认展示」列）：
	// 默认隐藏对端 deprecated 与对端已删除；--include-deprecated 只放开 deprecated（T-…-061）。
	// **记录不动**：过滤只在读路径发生，源文件 relations[] 的条目数与字节一字不改。
	//
	// 端点宇宙 = 知识卡 ∪ 观点（endpointUniverse，与 card show / opinion show 共用）：反向来源
	// 与正向对端在 schema v2 下都可能横跨 k/o，故可见性 / 悬空 / deprecated 计数统一用折叠宇宙，
	// 反向扫描用 RelationsInAll（卡 ∪ 观点），`o-* → k-*` 的反向边因此在 `eg rel` 里如实现身。
	pol := VisibilityPolicy{IncludeDeprecated: req.IncludeDeprecated}
	universe := endpointUniverse(scan)
	out, hiddenOut := VisibleEndpoints(universe, rawOut, func(e RelationEdge) string {
		return e.Target
	}, pol)
	in, hiddenIn := VisibleEndpoints(universe, rawIn, func(e RelationEdge) string {
		return e.From
	}, pol)
	hidden := hiddenOut + hiddenIn
	diags := scan.Diagnostics
	if req.To != "" && !hasCard(scan.Cards, req.To) {
		// 合同 §3.1：`--to` 指向不存在的 ID → **结果为空** + 一条 Q2（如实说明，不静默返回空）。
		// 置空是逐字判据：即使本卡有一条悬空 relations[] 恰好指向这个不存在的 ID，
		// 也不能因此让 relations_out[] 非空——「对端卡不存在」的口径优于「条目照常输出」。
		// 对端不存在者不在库、不可能 deprecated，故隐藏计数在此归零（不产 Q4，只产该 Q2）。
		out, in = []RelationEdge{}, []RelationEdge{}
		hidden = 0
		diags = finalizeDiagnostics(append(append([]Diagnostic{}, scan.Diagnostics...),
			newQ2(target.Path, "--to 指定的对端卡 %s 在库中不存在：正反向结果均为空", req.To)))
	}
	// 分页施加在可见性过滤**之后**（默认视图看到几条，就从这几条里分页），并且是
	// **一个全局** limit/offset：正反两个列表合并成一条确定序列后全局取区间，再切回两段
	// （ApplyPagePair）。`--limit N` 因此是「本次最多返回 N 条」，不会因为有两个列表而给到
	// 2N 条；截断只判一次，只产**恰一条** W25（合同 §8.2）。
	out, in, pg := ApplyPagePair(out, in, req.Page)
	depPeers := mergeSortedUnique(
		deprecatedPeerSet(universe, out, func(e RelationEdge) string { return e.Target }),
		deprecatedPeerSet(universe, in, func(e RelationEdge) string { return e.From }))
	return &RelResult{
		Data: RelData{
			ID: string(req.ID), RelationsOut: out, RelationsIn: in,
			ScannedFiles: scan.ScannedFiles, SkippedFiles: scan.SkippedFiles,
		},
		MissingTargets: missingTargets(universe, out),
		Diagnostics: withTruncationDiagnostic(withIndexDegradedDiagnostics(
			withDeprecatedHiddenDiagnostic(diags, hidden, req.IncludeDeprecated),
			degradeDiagnostics(backend)), pg.Truncated, pg, "关系条目"),
		Page:             pg,
		HiddenDeprecated: hidden,
		DeprecatedPeers:  depPeers,
		backend:          backend,
	}, nil
}

// filterEdges 按谓词过滤并保序（排序在 RelationsOut / RelationsIn 里已完成）。
func filterEdges(edges []RelationEdge, keep func(RelationEdge) bool) []RelationEdge {
	out := []RelationEdge{}
	for _, e := range edges {
		if keep(e) {
			out = append(out, e)
		}
	}
	return out
}

// hasCard 报告库中是否存在该 ID 的卡。
// 线性判定：M2 没有索引（索引属 S4），一次命令一次扫描，不设性能门槛。
func hasCard(cards []CardEntry, id string) bool {
	for _, c := range cards {
		if c.ID == id {
			return true
		}
	}
	return false
}
