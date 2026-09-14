package proposal

// 影响面四项的**重算**与**可比对形态**（提案合同 §5.4；T-evergreen.s1_main_flow-158614-035）。
//
// §5.4 逐项（`impact` 的五个子键承载四项事实）：
//
//	① 会**退出默认视图**的具体内容      `exits_default_view`      直接扫描算出
//	② 会因此**没有剩余有效 support** 的卡 `cards_losing_support`   直接扫描算出
//	③ 受影响的**材料关系与论证关系**    `affected_material_rels` / `affected_relations`  计数即可
//	④ **可能失准的主题综述**            `stale_reviews`           S2 **只按 source_cards 命中给出提示**
//
// # 三条硬约束
//
//   - **S2 不依赖任何增量索引**：重算一律「直接扫描 Markdown 与 frontmatter」（§5.3 第 2 步），
//     读盘只经 store 的只读口（ScanIDs + Read），本包不建、不读任何派生缓存目录。
//   - **确定性**：扫描顺序按 ID 升序（ScanIDs 返回的是 map，必须排序后遍历）、
//     列表项去重后升序、计数按数值——同一语料两次重算逐字相同（TestRecomputeImpact_Deterministic）。
//   - **`stale_reviews` 只给提示**：命中判据**只有**「综述的 `source_cards` 里出现被删对象」这一条；
//     「综述失准的**自动判定**属 S3 之后」，本包不做任何失准推断（TestImpact_StaleReviewsHintOnly）。
//
// # 与 §5.5 的一致性
//
// 「删除执行后**没有任何知识卡的状态被自动改变**」：因此 `exits_default_view` 只收
// **被删对象自身**，`cards_losing_support` 只是「建议标记失效」的提示清单——
// 两者都不代表任何状态会被自动改写。

import (
	"fmt"
	"sort"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// ErrNoStore 表示重算入口没拿到只读 store（本包不自己打开文件系统）。
var ErrNoStore = fmt.Errorf("影响面重算需要一个只读 store")

// KeySourceCards 是主题综述 frontmatter 里承载取材卡清单的键名。
//
// §5.4 第 4 行逐字：「S2 只按 `source_cards` 命中给出提示」。综述产物本身属 S3 的
// 生成面，本包只**只读**这一个键，不解码综述的其余字段、不判定它是否失准。
const KeySourceCards = "source_cards"

// reviewRef 是一篇主题综述的**只读**投影：只取 ID 与取材卡清单两件事。
type reviewRef struct {
	ID          string   `yaml:"id"`
	SourceCards []string `yaml:"source_cards"`
}

// deletedProbe 是「是否已被逻辑删除」的只读投影（卡与笔记共用；提案没有这个键）。
type deletedProbe struct {
	DeletedAt string `yaml:"deleted_at"`
}

// vaultFacts 是一次全库只读扫描的事实集合（按 ID 升序，可复算）。
type vaultFacts struct {
	// cardIDs 是全部知识卡 ID，升序。
	cardIDs []string
	// cards 是知识卡 ID → 解码后的卡（materials / relations 由此取）。
	cards map[string]model.Card
	// deleted 是「已被逻辑删除」的产物 ID 集合（deleted_at 非空）。
	deleted map[string]bool
	// exists 是全部在库产物 ID 集合（含笔记 / 原文 / 综述；不含提案）。
	exists map[string]bool
	// reviews 是主题综述的只读投影，按 ID 升序。
	reviews []reviewRef
}

// scanVault 直接扫描 vault 的 Markdown 与 frontmatter，产出重算所需的全部事实。
//
// 只读：不修复、不重命名、不改写任何文件。**提案自身不是知识产物**，落在提案目录下的
// 文件一律排除（§10.3：提案不进知识扫描面）。无法按类型解码的文件被跳过——它的形态问题
// 由 `eg apply` / 校验命令报告，不在影响面重算里二次发码。
func scanVault(s *store.Store) (vaultFacts, error) {
	facts := vaultFacts{
		cards:   map[string]model.Card{},
		deleted: map[string]bool{},
		exists:  map[string]bool{},
	}
	idx, err := s.ScanIDs()
	if err != nil {
		return vaultFacts{}, err
	}
	ids := make([]string, 0, len(idx.ByID))
	for id := range idx.ByID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		rel := idx.ByID[id]
		if store.IsProposalRel(rel) {
			continue
		}
		f, err := s.Read(rel)
		if err != nil {
			return vaultFacts{}, err
		}
		doc, err := mdfile.Parse(f.Bytes)
		if err != nil {
			continue
		}
		facts.exists[id] = true
		var probe deletedProbe
		if err := doc.DecodeFM(&probe); err == nil && probe.DeletedAt != "" {
			facts.deleted[id] = true
		}
		switch {
		case model.CardID(id).Valid():
			// 只解 frontmatter：影响面事实全在 `sources[]` / `relations[]` /
			// `deleted_at` 三处，正文分区形态由 `eg apply` 的校验链负责，
			// 不在重算里二次判定（分区不齐的卡照样进影响面，避免漏算）。
			var card model.Card
			if err := doc.DecodeFM(&card); err != nil {
				continue
			}
			facts.cardIDs = append(facts.cardIDs, id)
			facts.cards[id] = card
		case isReviewID(id):
			var r reviewRef
			if err := doc.DecodeFM(&r); err != nil {
				continue
			}
			r.ID = id
			facts.reviews = append(facts.reviews, r)
		}
	}
	return facts, nil
}

// isReviewID 报告某个 ID 是否是主题综述 ID（前缀 `r-`）。
func isReviewID(id string) bool {
	p, err := model.ParseID(id)
	return err == nil && p.Prefix == model.PrefixReview
}

// RecomputeImpact 重算一次影响面（§5.4 四项），targets 是被删对象的 ID 清单。
//
// 全部结论**直接从 Markdown 扫描算出**，不依赖任何派生缓存；同一语料重复调用逐字相同。
// 四项的口径：
//
//	exits_default_view    ：targets 中**在库且尚未被逻辑删除**的对象（删除后它们退出默认视图；
//	                        §5.5「没有任何知识卡的状态被自动改变」，故其余产物不进本项）
//	cards_losing_support  ：删除前有 ≥1 条有效 `support`、删除后剩 0 条的**未被删卡**
//	affected_material_rels：端点或宿主命中 targets 的 `sources[]` 条目**总数**
//	affected_relations    ：端点或宿主命中 targets 的 `relations[]` 条目**总数**
//	stale_reviews         ：`source_cards` 命中任一 target 的综述 ID（**只提示**）
func RecomputeImpact(s *store.Store, targets []string) (Impact, error) {
	if s == nil {
		return Impact{}, ErrNoStore
	}
	set := targetSet(targets)
	facts, err := scanVault(s)
	if err != nil {
		return Impact{}, err
	}
	im := Impact{}
	for _, id := range sortedKeys(set) {
		if facts.exists[id] && !facts.deleted[id] {
			im.ExitsDefaultView = append(im.ExitsDefaultView, id)
		}
	}
	for _, id := range facts.cardIDs {
		card := facts.cards[id]
		hostHit := set[id]
		supportBefore, supportAfter := 0, 0
		for _, sr := range card.Sources {
			endpointHit := set[string(sr.Source)] || set[string(sr.Note)]
			if hostHit || endpointHit {
				im.AffectedMaterialRels++
			}
			if sr.Rel != model.MaterialSupport || hostHit || facts.deleted[id] {
				continue
			}
			if !effectiveEndpoint(facts, sr) {
				continue
			}
			supportBefore++
			if !endpointHit {
				supportAfter++
			}
		}
		for _, rel := range card.Relations {
			if hostHit || set[string(rel.Target)] {
				im.AffectedRelations++
			}
		}
		if !hostHit && !facts.deleted[id] && supportBefore > 0 && supportAfter == 0 {
			im.CardsLosingSupport = append(im.CardsLosingSupport, id)
		}
	}
	for _, r := range facts.reviews {
		if hitsAny(r.SourceCards, set) {
			im.StaleReviews = append(im.StaleReviews, r.ID)
		}
	}
	return NormalizeImpact(im), nil
}

// effectiveEndpoint 报告一条材料关系的端点当前是否**有效**：
// 两端（原文 / 笔记）都不得已被逻辑删除。端点不在库时按「无效」处理（缺项由 W 系警告承接）。
func effectiveEndpoint(facts vaultFacts, sr model.SourceRef) bool {
	for _, id := range []string{string(sr.Source), string(sr.Note)} {
		if id == "" {
			continue
		}
		if facts.deleted[id] || !facts.exists[id] {
			return false
		}
	}
	return true
}

// hitsAny 报告清单里是否有任一项落在集合内。
func hitsAny(items []string, set map[string]bool) bool {
	for _, it := range items {
		if set[it] {
			return true
		}
	}
	return false
}

// targetSet 把 targets 去空去重成集合。
func targetSet(targets []string) map[string]bool {
	set := make(map[string]bool, len(targets))
	for _, t := range targets {
		if t != "" {
			set[t] = true
		}
	}
	return set
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// NormalizeImpact 把影响面归一成**可比对形态**：三个列表键各自去重后按 ID 升序，
// 两个计数键原样保留（计数是数值，没有顺序问题）。
//
// 这是「比对口径确定性」的唯一实现：重算结果与提案里落盘的 `impact` 都先过这一道，
// 再逐项比较（ImpactEqual / ImpactDiff），因此比对不受书写顺序与重复项影响。
// Extra（未知子键）**不参与**比对：它逐字保留在文件里，但不是影响面事实。
func NormalizeImpact(im Impact) Impact {
	out := Impact{
		ExitsDefaultView:     sortUnique(im.ExitsDefaultView),
		CardsLosingSupport:   sortUnique(im.CardsLosingSupport),
		AffectedMaterialRels: im.AffectedMaterialRels,
		AffectedRelations:    im.AffectedRelations,
		StaleReviews:         sortUnique(im.StaleReviews),
	}
	return out
}

func sortUnique(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(items))
	for _, it := range items {
		if it == "" || seen[it] {
			continue
		}
		seen[it] = true
		out = append(out, it)
	}
	sort.Strings(out)
	return out
}

// ImpactEqual 报告两份影响面是否**逐项相等**（先归一，再列表逐项 + 计数数值比较）。
func ImpactEqual(a, b Impact) bool { return len(ImpactDiff(a, b)) == 0 }

// ImpactDiff 逐项比对两份影响面，返回**人类可读**的差异说明（顺序固定 = ImpactKeys()）。
//
// 差异为空即「影响面未变化」——`eg proposal approve` 的执行前重算据此判是否走触发②
// （DecideApprove）。差异说明进报告，供用户看清「原影响范围为何不再成立」。
func ImpactDiff(want, got Impact) []string {
	w, g := NormalizeImpact(want), NormalizeImpact(got)
	var diff []string
	for _, pair := range []struct {
		key      string
		old, cur []string
	}{
		{KeyExitsDefaultView, w.ExitsDefaultView, g.ExitsDefaultView},
		{KeyCardsLosingSupport, w.CardsLosingSupport, g.CardsLosingSupport},
	} {
		if !equalList(pair.old, pair.cur) {
			diff = append(diff, fmt.Sprintf("%s：提案记录 %v，重算 %v", pair.key, pair.old, pair.cur))
		}
	}
	if w.AffectedMaterialRels != g.AffectedMaterialRels {
		diff = append(diff, fmt.Sprintf("%s：提案记录 %d，重算 %d",
			KeyAffectedMaterialRels, w.AffectedMaterialRels, g.AffectedMaterialRels))
	}
	if w.AffectedRelations != g.AffectedRelations {
		diff = append(diff, fmt.Sprintf("%s：提案记录 %d，重算 %d",
			KeyAffectedRelations, w.AffectedRelations, g.AffectedRelations))
	}
	if !equalList(w.StaleReviews, g.StaleReviews) {
		diff = append(diff, fmt.Sprintf("%s：提案记录 %v，重算 %v",
			KeyStaleReviews, w.StaleReviews, g.StaleReviews))
	}
	return diff
}

func equalList(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
