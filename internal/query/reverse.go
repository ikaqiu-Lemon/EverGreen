package query

// [S4] `replaced_by` 的**正反双向查询**（M5 索引架构合同 §8.4 + `M-005` 判据 13；T-…-068）。
//
// # 两个方向说的是两件事
//
//	正向（谁取代了 X）：读 X 自己 frontmatter 的 `replaced_by.target`
//	                    —— 至多一条（单向存储，M3 的 SetReplacedBy 只写失效卡自己）
//	反向（X 取代了谁）：全库反查 `replaced_by.target == X` 的卡
//	                    —— 0..N 条，**这正是 S4 之前做不到的那一半**
//
// 语义方向逐字沿用 M3：`A.replaced_by = B` 读作「**A 已失效，被 B 取代**」。
// 因此若 `A.replaced_by = B`，则「`A` 的正向」与「`B` 的反向」必须互相可见
// （合同 §8.4 / `TestReplacedByReverseLookup`）；链式 `A → B → C` 下 `B` 两个方向各有一条
// （`TestReplacedByChainBothDirections`）。
//
// # 为什么不新增表、不新增 data 键
//
//   - 数据来源就是权威 Markdown 的 `replaced_by` 字段（合同 §8.4：索引侧复用既有
//     `cards.replaced_by` 列，**不新增表**；本 task 不改 schema）；
//   - 输出形态复用 `RelationEdge` 的**同一套五键**（from / type / target / reason / path），
//     `type` 逐字为 `replaced_by` ⇒ `eg rel` 的 data 仍恰五键、元素仍恰五键（合同 §8.3）。
//
// # 与可见性**正交**（判据 13 的后半句）
//
// 本文件**不实现第二套可见性规则**：过滤一律交给 card.go 的 `VisibleEndpoints`
// （逻辑删除 / `deprecated` 的四象限矩阵是 M4 判据 12 验收过的单点），
// 本文件只负责「把边算出来」。被取代的卡通常正是 `deprecated` 卡，因此默认视图会隐藏它、
// 并按既有口径产 Q4 —— 那是既有策略的**结果**，不是本文件的新规则。
//
// # 取数口径：Markdown 恒为权威
//
// `CardEntry` 没有 `replaced_by` 这一格（它是 S2 的可选键，扫描底座的公共字段集里没有），
// 索引后端给出的摘要条目（stub）也不带正文。因此本文件对**权威文件**再解析一次
// frontmatter：宁可多读一次磁盘，绝不让「索引里有什么」决定「谁取代了谁」。
// 解析不动的文件一律当作**未设置**（Q1 已由扫描层登记过，本文件不替库治病、不再报一次）。

import (
	"os"
	"path/filepath"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// EdgeTypeReplacedBy 是替代指针在关系条目里的 `type` 取值（逐字，唯一字面量）。
//
// 它**不是**第五种论证关系类型：`model.RelationType` 的封闭四值（derives / supports /
// limits / opposing）一格不动，写路径也永远不会产生这个 type。它只出现在
// `eg rel --replaced-by` 这一条**只读**视图里，用来复用关系条目的五键形态。
const EdgeTypeReplacedBy = "replaced_by"

// ReplacedByFlag 是 `eg rel` 上开启替代指针视图的 flag 名（唯一字面量）。
const ReplacedByFlag = "replaced-by"

// replacedByOf 取一张卡的 `replaced_by`（target / reason 的逐字原值；未设置即两个空串）。
//
// 优先用已解析好的 Doc（扫描后端 / 已解析的焦点卡都有）；只有拿不到 Doc 时才回读
// 权威文件（索引后端的摘要条目）—— 两条路读的都是同一份权威字节，结果因此逐字相等。
func replacedByOf(root string, c CardEntry) (target, reason string) {
	doc := c.Doc
	if doc == nil {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(c.Path)))
		if err != nil {
			return "", ""
		}
		d, err := mdfile.Parse(raw)
		if err != nil {
			return "", ""
		}
		doc = d
	}
	var card model.Card
	if err := doc.DecodeFM(&card); err != nil {
		return "", ""
	}
	if card.ReplacedBy == nil {
		return "", ""
	}
	return string(card.ReplacedBy.Target), card.ReplacedBy.Reason
}

// ReplacedByForward 给出「**谁取代了 id**」：id 自己的替代指针，至多一条边。
//
// 边的形态：`from = id`、`target = replaced_by.target`、`reason = replaced_by.reason`、
// `path = id 所在文件`（记录确实写在它身上）。目标卡不存在时**照实输出**，
// 悬空由调用方按既有 Q2 口径登记（与 relations[] 的悬空处置同一条纪律）。
func ReplacedByForward(root string, cards []CardEntry, id string) []RelationEdge {
	out := []RelationEdge{}
	for _, c := range cards {
		if c.ID != id {
			continue
		}
		target, reason := replacedByOf(root, c)
		if target == "" {
			break
		}
		out = append(out, RelationEdge{From: c.ID, Type: EdgeTypeReplacedBy,
			Target: target, Reason: reason, Path: c.Path})
		break // 单向存储：一张卡至多一个替代指针
	}
	SortEdges(out, func(e RelationEdge) string { return e.Target })
	return out
}

// ReplacedByReverse 给出「**id 取代了谁**」：全库反查 `replaced_by.target == id` 的卡。
//
// 边的形态与正向**同构**（五键不变）：`from` = 被取代的那张卡、`target` = id、
// `reason` = 被取代卡上写的替代理由、`path` = 被取代卡的文件。
// 这是「反向」的全部含义：记录仍然只有一份（写在失效卡身上），本函数不补第二条记录、
// 不改任何字节，只是把同一条记录**从另一头读出来**。
func ReplacedByReverse(root string, cards []CardEntry, id string) []RelationEdge {
	out := []RelationEdge{}
	for _, c := range cards {
		if c.ID == id {
			continue // 自指不成立：一张卡不会取代自己（写路径已拦，读路径也不构造）
		}
		target, reason := replacedByOf(root, c)
		if target != id {
			continue
		}
		out = append(out, RelationEdge{From: c.ID, Type: EdgeTypeReplacedBy,
			Target: target, Reason: reason, Path: c.Path})
	}
	SortEdges(out, func(e RelationEdge) string { return e.From })
	return out
}
