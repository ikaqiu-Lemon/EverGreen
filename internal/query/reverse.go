package query

// [S4] `replaced_by` 的**正反双向查询**（M5 索引架构合同 §8.4 + `M-005` 判据 13；T-…-068）。
//
// # 两个方向说的是两件事
//
//	正向（谁取代了 X）：读 X 自己 frontmatter 的 `replaced_by.target`
//	                    —— 至多一条（单向存储，M3 的 SetReplacedBy 只写失效端点自己）
//	反向（X 取代了谁）：全库反查 `replaced_by.target == X` 的端点
//	                    —— 0..N 条，**这正是 S4 之前做不到的那一半**
//
// 语义方向逐字沿用 M3：`A.replaced_by = B` 读作「**A 已失效，被 B 取代**」。
// 因此若 `A.replaced_by = B`，则「`A` 的正向」与「`B` 的反向」必须互相可见
// （合同 §8.4 / `TestReplacedByReverseLookup`）；链式 `A → B → C` 下 `B` 两个方向各有一条
// （`TestReplacedByChainBothDirections`）。
//
// # 端点跨类型（Knowledge 或 Opinion），两个方向都折同一个宇宙
//
// schema v2 起替代指针的宿主与目标都是关系端点（k-* 或 o-*）：焦点既可以是知识卡也可以是
// 观点，宿主既可以写在知识卡上也可以写在观点上。因此本文件把 `scan.Cards ∪ scan.Opinions`
// 折成**统一的替代指针端点投影**（replacedByEndpoints，与 `endpointUniverse` 折的是同一个
// Knowledge ∪ Opinion 宇宙，只是随行带上 `replaced_by` 的 target / reason），正向按焦点自身、
// 反向遍历两类宿主。k/o 自指（`target == 自身 ID`）在两个方向都忽略（写路径已拦，读路径也不造）。
//
// # 为什么不新增表、不新增 data 键、不回权威二次解码
//
//   - 数据来源就是权威 Markdown 的 `replaced_by` 字段，而 target / reason 已由扫描底座在
//     **同一次** frontmatter 解码里带出（scan.go 的 CardEntry / OpinionEntry.ReplacedByTarget /
//     ReplacedByReason）；本文件直接读这两个投影字段，**不再** `os.ReadFile` + `DecodeFM`
//     重解一次，o-* 与 k-* 一视同仁（这正是与论证关系读路径同构的取数纪律）；
//   - 索引后端复用既有 `cards.replaced_by` 列定位替代指针端点、按需回权威补齐逐字 reason
//     （index_backed.go），四后端（healthy / missing / stale / corrupt）结果因此逐字等价，
//     **不新增表**、不改 schema；
//   - 输出形态复用 `RelationEdge` 的**同一套五键**（from / type / target / reason / path），
//     `type` 逐字为 `replaced_by` ⇒ `eg rel` 的 data 仍恰五键、元素仍恰五键（合同 §8.3）。
//
// # 与可见性**正交**（判据 13 的后半句）
//
// 本文件**不实现第二套可见性规则**：过滤一律交给 card.go 的 `VisibleEndpoints`
// （逻辑删除 / `deprecated` 的四象限矩阵是 M4 判据 12 验收过的单点），
// 本文件只负责「把边算出来」。被取代的端点通常正是 `deprecated`，因此默认视图会隐藏它、
// 并按既有口径产 Q4 —— 那是既有策略的**结果**，不是本文件的新规则。宿主自身与目标端点的
// 删除 / 失效维度彼此正交，且与普通 relation 读路径同一条纪律。

// EdgeTypeReplacedBy 是替代指针在关系条目里的 `type` 取值（逐字，唯一字面量）。
//
// 它**不是**第五种论证关系类型：`model.RelationType` 的封闭四值（derives / supports /
// limits / opposing）一格不动，写路径也永远不会产生这个 type。它只出现在
// `eg rel --replaced-by` 这一条**只读**视图里，用来复用关系条目的五键形态。
const EdgeTypeReplacedBy = "replaced_by"

// ReplacedByFlag 是 `eg rel` 上开启替代指针视图的 flag 名（唯一字面量）。
const ReplacedByFlag = "replaced-by"

// replacedByEndpoint 是替代指针读路径要用到的**单个端点投影**：ID / 文件路径，
// 以及该端点自身 `replaced_by` 的 target / reason（缺省即空串）。
//
// 它是「统一端点投影」在本功能上的随行扩展：`endpointUniverse` 只需 ID / 删除 / 失效三格
// 做可见性判定，替代指针读路径额外要 path（进边的第五键）与 target / reason（进边的第三、
// 四键）——三者都已由扫描底座在同一次解码里带出，这里只是**读**，不二次解码。
type replacedByEndpoint struct {
	id, path, target, reason string
}

// replacedByEndpoints 把 `scan.Cards ∪ scan.Opinions` 折成替代指针端点投影（Knowledge 与
// Opinion 两类宿主同列一张表）。**零文件读、零 model 二次解码**：target / reason 直接取自
// 扫描投影字段，o-* 与 k-* 走同一条取数路径。
func replacedByEndpoints(scan *ScanResult) []replacedByEndpoint {
	out := make([]replacedByEndpoint, 0, len(scan.Cards)+len(scan.Opinions))
	for i := range scan.Cards {
		c := &scan.Cards[i]
		out = append(out, replacedByEndpoint{
			id: c.ID, path: c.Path, target: c.ReplacedByTarget, reason: c.ReplacedByReason})
	}
	for i := range scan.Opinions {
		o := &scan.Opinions[i]
		out = append(out, replacedByEndpoint{
			id: o.ID, path: o.Path, target: o.ReplacedByTarget, reason: o.ReplacedByReason})
	}
	return out
}

// ReplacedByForward 给出「**谁取代了 id**」：id 自己的替代指针，至多一条边。
//
// 边的形态：`from = id`、`target = replaced_by.target`、`reason = replaced_by.reason`、
// `path = id 所在文件`（记录确实写在它身上）。取数一律走同一次 Scan 结果里该端点**自身**的
// 投影字段（k-* 与 o-* 同源，不回权威二次解码）。目标端点不存在时**照实输出**，悬空由调用方
// 按既有 Q2 口径登记（与 relations[] 的悬空处置同一条纪律）；自指（target == id）忽略。
func ReplacedByForward(scan *ScanResult, id string) []RelationEdge {
	out := []RelationEdge{}
	for _, e := range replacedByEndpoints(scan) {
		if e.id != id {
			continue
		}
		// 未设置替代指针，或自指（一个端点不会取代自己）：不构造正向边。
		if e.target == "" || e.target == id {
			break
		}
		out = append(out, RelationEdge{From: e.id, Type: EdgeTypeReplacedBy,
			Target: e.target, Reason: e.reason, Path: e.path})
		break // 单向存储：一个端点至多一个替代指针
	}
	SortEdges(out, func(e RelationEdge) string { return e.Target })
	return out
}

// ReplacedByReverse 给出「**id 取代了谁**」：全库反查 `replaced_by.target == id` 的端点。
//
// 边的形态与正向**同构**（五键不变）：`from` = 被取代的那个端点、`target` = id、
// `reason` = 被取代端点上写的替代理由、`path` = 被取代端点的文件。宿主可以是知识卡也可以是
// 观点（两类都遍历）。这是「反向」的全部含义：记录仍然只有一份（写在失效端点身上），本函数
// 不补第二条记录、不改任何字节，只是把同一条记录**从另一头读出来**；自指忽略。
func ReplacedByReverse(scan *ScanResult, id string) []RelationEdge {
	out := []RelationEdge{}
	for _, e := range replacedByEndpoints(scan) {
		if e.id == id {
			continue // 自指不成立：一个端点不会取代自己（写路径已拦，读路径也不构造）
		}
		if e.target != id {
			continue
		}
		out = append(out, RelationEdge{From: e.id, Type: EdgeTypeReplacedBy,
			Target: id, Reason: e.reason, Path: e.path})
	}
	SortEdges(out, func(e RelationEdge) string { return e.From })
	return out
}
