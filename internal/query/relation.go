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
	"errors"
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// ErrInvalidEndpoint 是 `eg rel <id>` / `--to <id>` 的**关系端点形态非法**：不是 k-/o- 前缀
// 的合法 ID（s- / n- / r- / p- 及任何不可解析 ID 一律拒绝）。论证关系只发生在两条论证性
// 产物（知识卡 / 观点）之间，故端点只认 k- / o- 两种前缀（model.ParseRelationEndpoint）。
var ErrInvalidEndpoint = errors.New("关系端点 ID 形态非法")

// ErrEndpointNotFound 是**端点形态合法但库中不存在**（焦点实体在 Knowledge ∪ Opinion
// 全库都找不到 → 退 1、零副作用）。
var ErrEndpointNotFound = errors.New("关系端点不存在")

// RelRequest 是一次关系查询的输入（合同 §3.1 参数表）。
//
// To 对应 `--to <id>`：只保留**对端** == 该 ID 的条目（正向比 target、反向比 from）；
// 空 = 不过滤。
// IncludeDeprecated 对应 `--include-deprecated`（T-…-061，owner 裁决②）：默认 false =
// 隐藏对端 deprecated 的条目；true 只放开 deprecated 维度，**不影响**已删除维度（正交）。
type RelRequest struct {
	// ID 是焦点实体端点（k- / o-）：论证关系跨类型，焦点既可以是知识卡也可以是观点，
	// 故类型是 model.RelationEndpoint 而非 CardID。`RelView(o-id)` 因此可读一条观点的
	// 正向（自身 relations[]）与反向（全库谁指向了它）。落盘 / JSON 键形态一格不变。
	ID                model.RelationEndpoint
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

// RelView 组装关系视图：全库扫描 → 正向读焦点实体 frontmatter → 反向反查「谁指向了我」。
//
// 焦点实体端点跨类型（k- / o-）：ParseRelationEndpoint 只接受知识卡 / 观点，其余前缀
// （s- / n- / r- / p-）与畸形 ID 一律拒绝。焦点从 Knowledge ∪ Opinion 全库定位并读取其
// **自身** relations[] 作为正向；反向走 RelationsInAll（卡 ∪ 观点全库反查）。因此
// `RelView(o-id)` 的正反向都可用（o→k / o→o 正向、`*→o` 反向都如实现身）。
//
// 正向 `relations_out[]`：只读焦点实体 frontmatter 的 `relations[]`，`from` 恒为焦点 ID。
// 反向 `relations_in[]`：**全库 Markdown 反向扫描**，`from` 为写下该条关系的卡 / 观点。
// `opposing` **单向存储**（EG-CVG-05）：库里只有一条记录，读路径**不补对称条目、不去重合并**。
func RelView(root string, req RelRequest) (*RelResult, error) {
	if _, err := model.ParseRelationEndpoint(string(req.ID)); err != nil {
		return nil, fmt.Errorf("%w：%q 不是 k-/o-YYYYMMDD-slug 形态的关系端点（论证关系只连知识卡或观点）",
			ErrInvalidEndpoint, string(req.ID))
	}
	if err := req.Page.Validate(); err != nil {
		return nil, err
	}
	if req.To != "" {
		if _, err := model.ParseRelationEndpoint(req.To); err != nil {
			return nil, fmt.Errorf("%w：--to=%q 不是 k-/o-YYYYMMDD-slug 形态的关系端点",
				ErrInvalidEndpoint, req.To)
		}
	}
	// 取数：后端选择走**唯一单点**（索引健康 → 索引后端按 relations.dst_id 精确反查出
	// 来源再回权威取逐字 reason，焦点实体自身也回权威解析；缺失 / 损坏 / 陈旧 → 确定性降级
	// 为全量扫描 + W2x + Q5）。
	need := relNeed(string(req.ID), req.Index)
	scan, backend, err := loadVault(root, ScanOptions{}, need, SelectBackend(root, need))
	if err != nil {
		return nil, err
	}
	// 焦点从 Knowledge ∪ Opinion 定位（ID 全库唯一，k-* 与 o-* 不撞号）：先在卡面找、再在
	// 观点面找。两面均已按 path 升序稳定排序，同 ID 重复时首个命中即路径字典序最小者
	// （重复本身由扫描层记 Q1，不静默择一）。正向边取焦点实体自身 relations[]（relationEdgesFrom
	// 对卡 / 观点逐字同构）。
	var rawOut []RelationEdge
	var focusPath string
	var focusOpinion *OpinionEntry
	found := false
	for i := range scan.Cards {
		if scan.Cards[i].ID == string(req.ID) {
			rawOut = RelationsOut(scan.Cards[i])
			focusPath = scan.Cards[i].Path
			found = true
			break
		}
	}
	if !found {
		for i := range scan.Opinions {
			if scan.Opinions[i].ID == string(req.ID) {
				rawOut = relationEdgesFrom(scan.Opinions[i].ID, scan.Opinions[i].Path,
					scan.Opinions[i].Relations)
				focusPath = scan.Opinions[i].Path
				focusOpinion = &scan.Opinions[i]
				found = true
				break
			}
		}
	}
	if !found {
		return nil, fmt.Errorf("%w：%s 在库中不存在（已全库扫描 %d 个 .md）",
			ErrEndpointNotFound, string(req.ID), scan.ScannedFiles)
	}

	// 顺序要点（T-…-061 修正）：**先按 --to 收窄原始边，再做可见性过滤 / 计数**。
	// 两者正交：--to 收窄「看哪个对端」，--include-deprecated 决定「deprecated 对端可见性」。
	// 旧实现先算 hidden 再在 --to 分支无条件 hidden=0，导致 `eg rel X --to <deprecated>` 默认视图
	// 明明把对端隐藏了却不报 Q4（违反 Q4 的 N≥1 条件与正交性）；这里改为收窄在前、可见性在后。
	// 视图切换（T-…-068）：默认是论证关系 relations[]；`--replaced-by` 换成替代指针的
	// 正反双向（reverse.go）。两种视图**同构**：都是五键条目、同一套排序、同一套可见性。
	rawIn := RelationsInAll(scan, string(req.ID))
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
	// 焦点是观点时补齐**观点持有方**的悬空引用（Q2）：卡持有方的悬空由 scan.go
	// danglingDiagnostics 在全库扫描面统一判定并已进 scan.Diagnostics，但观点自身
	// relations[] 指向库中不存在对端的悬空**不在**那一步（见 danglingDiagnostics 注释：
	// 观点持有方交由各消费者按 opinion show 同口径补齐）。若不在此补齐，`eg rel <o-id>`
	// 会对焦点观点的悬空边漏报 Q2。复用 opinion_show 的 opinionDanglingRefs / knownIDSet
	// 逐字同口径；合并时先 dropQ3 再 finalize，按合并后结果重算恰一条 Q3，绝不双计。
	if focusOpinion != nil {
		if _, danglingQ2 := opinionDanglingRefs(*focusOpinion, knownIDSet(scan)); len(danglingQ2) > 0 {
			diags = finalizeDiagnostics(append(dropQ3(diags), danglingQ2...))
		}
	}
	if req.To != "" && !hasEndpoint(universe, req.To) {
		// 合同 §3.1：`--to` 指向不存在的 ID → **结果为空** + 一条 Q2（如实说明，不静默返回空）。
		// 置空是逐字判据：即使焦点有一条悬空 relations[] 恰好指向这个不存在的 ID，
		// 也不能因此让 relations_out[] 非空——「对端不存在」的口径优于「条目照常输出」。
		// 存在性判定走**统一端点宇宙**（Knowledge ∪ Opinion）：`--to o-id` 指向真实观点即命中，
		// 指向不存在的 k/o 端点才置空。对端不存在者不在库、不可能 deprecated，故隐藏计数在此
		// 归零（不产 Q4，只产该 Q2）。焦点观点自身的悬空 Q2 已并入 diags，此处 dropQ3 后再追加
		// --to 的 Q2 并 finalize，Q3 按合并后结果重算恰一条。
		out, in = []RelationEdge{}, []RelationEdge{}
		hidden = 0
		diags = finalizeDiagnostics(append(dropQ3(diags),
			newQ2(focusPath, "--to 指定的对端 %s 在库中不存在：正反向结果均为空", req.To)))
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

// hasEndpoint 报告统一端点宇宙（Knowledge ∪ Opinion，endpointUniverse 折叠而来）里是否
// 存在该 ID 的端点。`--to <id>` 的存在性判定据此裁决：`--to o-id` 指向真实观点即命中，
// 只有指向库中不存在的 k/o 端点才把结果置空并记一条 Q2。
// 线性判定：读路径一次命令一次扫描，不设性能门槛（索引路径已在取数层折算完毕）。
func hasEndpoint(universe []CardEntry, id string) bool {
	for _, c := range universe {
		if c.ID == id {
			return true
		}
	}
	return false
}
