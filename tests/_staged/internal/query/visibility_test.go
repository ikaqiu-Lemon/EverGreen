package query_test

// [M4 / T-…-061] internal/query/visibility_test.go：owner 裁决②（deprecated 关系端点默认
// 隐藏 + `--include-deprecated` 显式筛选，关闭 K-043-01）的 **query 侧**十组矩阵中的
// G1 ~ G6、G10（G7 ~ G9 属 CLI 侧，见 internal/cli/visibility_matrix_test.go）。
//
// 判据来源：
//   - M4 可见性合同 docs/specs/2026-11-12-m4-visibility-and-execution-contract.md
//     §3.2（四象限真值表 + 正交性）、§3.3（「对端」定义与 replaced_by 特例）、§4.4（十组矩阵）；
//   - M2 查询合同 docs/specs/2026-09-19-m2-query-contract.md §5.1（被裁决取代的那一格）。
//
// 每一组都**同时覆盖 eg rel（RelView）与 eg card show（ShowCard）** 两条读路径，
// 从而把 VisibleEndpoints 的四个调用点（rel 正/反、card 正/反）全部压到。
// 「对端」是 peer：正向比 target、反向比 from；筛选看 peer 的 status，不看被查询卡自身。

import (
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
)

// visCard 造一张带 relations[] 的卡，可选 deleted_at（给了即「逻辑删除」）。
// status 取 active / deprecated；deletedAt 非空即在 frontmatter 落删除标记（记录不动，逻辑删）。
func visCard(id, status, deletedAt, relations string) string {
	fm := "---\nid: " + id + "\nstatus: " + status +
		"\ncreated_at: '2026-09-01'\nupdated_at: '2026-09-12T10:00:00+08:00'\n" +
		"reviewed_at: '2026-09-12T10:00:00+08:00'\ntitle: " + id + "\nsources: []\n"
	if deletedAt != "" {
		fm += "deleted_at: '" + deletedAt + "'\ndeleted_reason: 用户判断已过时\n"
	}
	if relations != "" {
		fm += "relations:\n" + relations
	}
	return fm + "---\n\n## 知识内容\n\n正文占位。\n"
}

// visVault 造一份四象限齐备 + replaced_by 链 + 全隐藏 / 仅删除两类特例的语料。
//
//	hub(active)      → supports act / opposing dep / limits del / derives both
//	act(active)      → supports hub          （hub 反向里的 active 对端）
//	dep(deprecated)  → supports hub          （hub 反向里的 deprecated 对端）
//	del(active+已删) → supports hub          （hub 反向里的 deleted 对端，任何 flag 下都隐藏）
//	both(dep+已删)   （无正向）
//	old(deprecated)  → supports new          （replaced_by 链：旧卡看新卡=active 对端→可见）
//	new(active)      （无正向；反向被 old 指向=deprecated 对端→默认隐藏）
//	allhid(active)   → opposing dep / limits del   （G6：所有对端都隐藏 → 默认正向为空）
//	delonly(active)  → supports del                （G10：唯一对端仅「已删除」→与 deprecated 维度无关）
func visVault(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	kn := "domains/ai-infra/knowledge/"
	writeFile(t, root, kn+"k-20260901-hub.md", visCard("k-20260901-hub", "active", "",
		"  - type: supports\n    target: k-20260902-act\n    reason: 支持 act\n"+
			"  - type: opposing\n    target: k-20260903-dep\n    reason: 与 dep 冲突\n"+
			"  - type: limits\n    target: k-20260904-del\n    reason: 限制 del\n"+
			"  - type: derives\n    target: k-20260905-both\n    reason: 由 both 推出\n"))
	writeFile(t, root, kn+"k-20260902-act.md", visCard("k-20260902-act", "active", "",
		"  - type: supports\n    target: k-20260901-hub\n    reason: 支持 hub\n"))
	writeFile(t, root, kn+"k-20260903-dep.md", visCard("k-20260903-dep", "deprecated", "",
		"  - type: supports\n    target: k-20260901-hub\n    reason: 支持 hub\n"))
	writeFile(t, root, kn+"k-20260904-del.md", visCard("k-20260904-del", "active",
		"2026-09-20T10:00:00+08:00",
		"  - type: supports\n    target: k-20260901-hub\n    reason: 支持 hub\n"))
	writeFile(t, root, kn+"k-20260905-both.md", visCard("k-20260905-both", "deprecated",
		"2026-09-21T10:00:00+08:00", ""))
	writeFile(t, root, kn+"k-20260906-old.md", visCard("k-20260906-old", "deprecated", "",
		"  - type: supports\n    target: k-20260907-new\n    reason: 被 new 取代\n"))
	writeFile(t, root, kn+"k-20260907-new.md", visCard("k-20260907-new", "active", "", ""))
	writeFile(t, root, kn+"k-20260908-allhid.md", visCard("k-20260908-allhid", "active", "",
		"  - type: opposing\n    target: k-20260903-dep\n    reason: 与 dep 冲突\n"+
			"  - type: limits\n    target: k-20260904-del\n    reason: 限制 del\n"))
	writeFile(t, root, kn+"k-20260909-delonly.md", visCard("k-20260909-delonly", "active", "",
		"  - type: supports\n    target: k-20260904-del\n    reason: 支持 del\n"))
	return root
}

// relOut / relIn 抽出 RelView 正 / 反向的对端 ID 列表（保序）。
func relOut(res *query.RelResult) []string { return targetsOf(res.Data.RelationsOut) }
func relIn(res *query.RelResult) []string  { return fromsOf(res.Data.RelationsIn) }

// cardOut / cardIn 抽出 ShowCard 正 / 反向的对端 ID 列表（保序）。
func cardOut(res *query.CardShowResult) []string { return targetsOf(res.Card.RelationsOut) }
func cardIn(res *query.CardShowResult) []string  { return fromsOf(res.Card.RelationsIn) }

func targetsOf(edges []query.RelationEdge) []string {
	out := []string{}
	for _, e := range edges {
		out = append(out, e.Target)
	}
	return out
}

func fromsOf(edges []query.RelationEdge) []string {
	out := []string{}
	for _, e := range edges {
		out = append(out, e.From)
	}
	return out
}

func hasStr(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// showDefault / showInc 是 ShowCard 的两种可见性策略速写。
func showDefault(t *testing.T, root, id string) *query.CardShowResult {
	t.Helper()
	res, err := query.ShowCard(root, model.CardID(id))
	if err != nil {
		t.Fatalf("ShowCard(%s)：%v", id, err)
	}
	return res
}

func showInc(t *testing.T, root, id string) *query.CardShowResult {
	t.Helper()
	res, err := query.ShowCard(root, model.CardID(id), query.VisibilityPolicy{IncludeDeprecated: true})
	if err != nil {
		t.Fatalf("ShowCard(%s, include)：%v", id, err)
	}
	return res
}

func relDefault(t *testing.T, root, id string) *query.RelResult {
	t.Helper()
	res, err := query.RelView(root, query.RelRequest{ID: model.CardID(id)})
	if err != nil {
		t.Fatalf("RelView(%s)：%v", id, err)
	}
	return res
}

func relInc(t *testing.T, root, id string) *query.RelResult {
	t.Helper()
	res, err := query.RelView(root, query.RelRequest{ID: model.CardID(id), IncludeDeprecated: true})
	if err != nil {
		t.Fatalf("RelView(%s, include)：%v", id, err)
	}
	return res
}

// TestG1DeprecatedHiddenByDefault —— G1：默认视图隐藏 deprecated 对端。
// hub 的正向 opposing→dep 与反向 dep→hub 在默认视图里都不出现（rel 与 card show 同）。
func TestG1DeprecatedHiddenByDefault(t *testing.T) {
	root := visVault(t)

	// eg rel 侧：正向不含 dep、反向不含 dep（默认）。
	rel := relDefault(t, root, "k-20260901-hub")
	if hasStr(relOut(rel), "k-20260903-dep") {
		t.Fatalf("默认 rel 正向不应含 deprecated 对端 dep：%v", relOut(rel))
	}
	if hasStr(relIn(rel), "k-20260903-dep") {
		t.Fatalf("默认 rel 反向不应含 deprecated 对端 dep：%v", relIn(rel))
	}
	// active 对端 act 仍在（只动 deprecated 一个维度）。
	if !hasStr(relOut(rel), "k-20260902-act") || !hasStr(relIn(rel), "k-20260902-act") {
		t.Fatalf("active 对端 act 不应被隐藏：out=%v in=%v", relOut(rel), relIn(rel))
	}

	// eg card show 侧：同样默认隐藏 dep。
	card := showDefault(t, root, "k-20260901-hub")
	if hasStr(cardOut(card), "k-20260903-dep") || hasStr(cardIn(card), "k-20260903-dep") {
		t.Fatalf("默认 card show 不应含 deprecated 对端 dep：out=%v in=%v", cardOut(card), cardIn(card))
	}
	if !hasStr(cardOut(card), "k-20260902-act") {
		t.Fatalf("card show 默认应仍含 active 对端 act：%v", cardOut(card))
	}
}

// TestG2IncludeDeprecatedShowsWithMarker —— G2：--include-deprecated 后 deprecated 对端展示，
// 且 query 层把它记入 DeprecatedPeers（CLI 据此渲染 [失效]）。rel 与 card show 同。
func TestG2IncludeDeprecatedShowsWithMarker(t *testing.T) {
	root := visVault(t)

	rel := relInc(t, root, "k-20260901-hub")
	if !hasStr(relOut(rel), "k-20260903-dep") || !hasStr(relIn(rel), "k-20260903-dep") {
		t.Fatalf("--include-deprecated 后 rel 正反向都应含 dep：out=%v in=%v", relOut(rel), relIn(rel))
	}
	if !hasStr(rel.DeprecatedPeers, "k-20260903-dep") {
		t.Fatalf("rel.DeprecatedPeers 应含 dep（供渲染 [失效]）：%v", rel.DeprecatedPeers)
	}

	card := showInc(t, root, "k-20260901-hub")
	if !hasStr(cardOut(card), "k-20260903-dep") {
		t.Fatalf("--include-deprecated 后 card show 正向应含 dep：%v", cardOut(card))
	}
	if !hasStr(card.DeprecatedPeers, "k-20260903-dep") {
		t.Fatalf("card.DeprecatedPeers 应含 dep（供渲染 [失效]）：%v", card.DeprecatedPeers)
	}
	// 标记文案本身逐字复用 M2 §2.2，不改。
	if query.MarkerDeprecated != "[失效]" {
		t.Fatalf("[失效] 标记文案被改动：%q", query.MarkerDeprecated)
	}
}

// TestG3ForwardBackwardSamePolicy —— G3：正向与反向套用同一策略，不得只改一侧。
// hub 同时有「正向指向 dep」与「反向被 dep 指向」；默认两向都隐藏 dep，include 后两向都现。
func TestG3ForwardBackwardSamePolicy(t *testing.T) {
	root := visVault(t)

	relDef := relDefault(t, root, "k-20260901-hub")
	if hasStr(relOut(relDef), "k-20260903-dep") || hasStr(relIn(relDef), "k-20260903-dep") {
		t.Fatalf("默认下正反向必须一致隐藏 dep：out=%v in=%v", relOut(relDef), relIn(relDef))
	}
	relOn := relInc(t, root, "k-20260901-hub")
	if !hasStr(relOut(relOn), "k-20260903-dep") || !hasStr(relIn(relOn), "k-20260903-dep") {
		t.Fatalf("include 下正反向必须一致展示 dep：out=%v in=%v", relOut(relOn), relIn(relOn))
	}
	// 隐藏计数是正 + 反合计（正向 opposing→dep 一条 + 反向 dep→hub 一条 = 2）。
	if relDef.HiddenDeprecated != 2 {
		t.Fatalf("默认隐藏计数 = %d，期望 2（正反各一条 dep）", relDef.HiddenDeprecated)
	}

	// card show 侧同样：正反向套同一策略。
	cardDef := showDefault(t, root, "k-20260901-hub")
	if hasStr(cardOut(cardDef), "k-20260903-dep") || hasStr(cardIn(cardDef), "k-20260903-dep") {
		t.Fatalf("card show 默认正反向必须一致隐藏 dep：out=%v in=%v", cardOut(cardDef), cardIn(cardDef))
	}
	cardOn := showInc(t, root, "k-20260901-hub")
	if !hasStr(cardOut(cardOn), "k-20260903-dep") || !hasStr(cardIn(cardOn), "k-20260903-dep") {
		t.Fatalf("card show include 正反向必须一致展示 dep：out=%v in=%v", cardOut(cardOn), cardIn(cardOn))
	}
}

// TestG4DeletedOrthogonalStillHidden —— G4：正交性——--include-deprecated 只放开 deprecated
// 维度，已删除对端在任何 flag 下都隐藏。hub 的 limits→del（active+已删）与 derives→both（dep+已删）
// 在默认与 include 下都不出现；del 在反向同理。
func TestG4DeletedOrthogonalStillHidden(t *testing.T) {
	root := visVault(t)

	for _, mode := range []string{"default", "include"} {
		var rel *query.RelResult
		var card *query.CardShowResult
		if mode == "default" {
			rel, card = relDefault(t, root, "k-20260901-hub"), showDefault(t, root, "k-20260901-hub")
		} else {
			rel, card = relInc(t, root, "k-20260901-hub"), showInc(t, root, "k-20260901-hub")
		}
		// 已删除对端（del / both）任何 flag 下都不出现。
		for _, del := range []string{"k-20260904-del", "k-20260905-both"} {
			if hasStr(relOut(rel), del) {
				t.Fatalf("[%s] rel 正向不应含已删除对端 %s：%v", mode, del, relOut(rel))
			}
			if hasStr(cardOut(card), del) {
				t.Fatalf("[%s] card 正向不应含已删除对端 %s：%v", mode, del, cardOut(card))
			}
		}
		// del 的反向（del→hub）也任何 flag 下都不出现。
		if hasStr(relIn(rel), "k-20260904-del") || hasStr(cardIn(card), "k-20260904-del") {
			t.Fatalf("[%s] 反向不应含已删除对端 del：relIn=%v cardIn=%v", mode, relIn(rel), cardIn(card))
		}
	}
}

// TestG5ReplacedByChainBothDirections —— G5：replaced_by 链两方向（§3.3）——
// 旧卡（deprecated）看新卡（active）默认可见；新卡看旧卡默认隐藏、include 后可见。
// 关键：筛选看**对端**，不看被查询卡自身（old 自身 deprecated 不影响它看 active 的 new）。
func TestG5ReplacedByChainBothDirections(t *testing.T) {
	root := visVault(t)

	// 旧卡 old（自身 deprecated）看新卡 new（active 对端）：默认就可见。
	relOld := relDefault(t, root, "k-20260906-old")
	if !hasStr(relOut(relOld), "k-20260907-new") {
		t.Fatalf("旧卡默认应能看到 active 的新卡 new（看对端不看自身）：%v", relOut(relOld))
	}
	cardOld := showDefault(t, root, "k-20260906-old")
	if !hasStr(cardOut(cardOld), "k-20260907-new") {
		t.Fatalf("card show：旧卡默认应能看到新卡 new：%v", cardOut(cardOld))
	}

	// 新卡 new 看旧卡 old（deprecated 对端）：默认隐藏。
	relNewDef := relDefault(t, root, "k-20260907-new")
	if hasStr(relIn(relNewDef), "k-20260906-old") {
		t.Fatalf("新卡默认反向不应含 deprecated 的旧卡 old：%v", relIn(relNewDef))
	}
	cardNewDef := showDefault(t, root, "k-20260907-new")
	if hasStr(cardIn(cardNewDef), "k-20260906-old") {
		t.Fatalf("card show：新卡默认反向不应含旧卡 old：%v", cardIn(cardNewDef))
	}

	// 新卡 --include-deprecated 后能看到旧卡，且记入 DeprecatedPeers。
	relNewInc := relInc(t, root, "k-20260907-new")
	if !hasStr(relIn(relNewInc), "k-20260906-old") || !hasStr(relNewInc.DeprecatedPeers, "k-20260906-old") {
		t.Fatalf("新卡 include 后反向应含 old 且记 DeprecatedPeers：in=%v peers=%v",
			relIn(relNewInc), relNewInc.DeprecatedPeers)
	}
	cardNewInc := showInc(t, root, "k-20260907-new")
	if !hasStr(cardIn(cardNewInc), "k-20260906-old") {
		t.Fatalf("card show：新卡 include 后反向应含 old：%v", cardIn(cardNewInc))
	}
}

// TestG6OrphanCheckOrthogonalToVisibility —— G6：孤儿判定与可见性正交（与 052 的
// TestR4OrphanUsesOnDiskFacts 双侧锁）。**两条断言必须在同一用例里同时成立**：
//  1. 不误判 orphan：可见性过滤是读路径的展示面，落盘事实（VaultScan 的 relations[]）一条不少，
//     被指向的对端仍在事实里「被引用」，孤儿判定读事实故不会误判；
//  2. 默认 relations_out 为空：allhid 的两个对端分别 deprecated / 已删除，默认视图正向为空。
func TestG6OrphanCheckOrthogonalToVisibility(t *testing.T) {
	root := visVault(t)

	// —— 断言 2：默认视图正向为空（rel 与 card show 同）——
	relDef := relDefault(t, root, "k-20260908-allhid")
	if len(relDef.Data.RelationsOut) != 0 {
		t.Fatalf("allhid 默认 rel 正向应为空（对端全隐藏）：%v", relOut(relDef))
	}
	cardDef := showDefault(t, root, "k-20260908-allhid")
	if len(cardDef.Card.RelationsOut) != 0 {
		t.Fatalf("allhid 默认 card 正向应为空（对端全隐藏）：%v", cardOut(cardDef))
	}

	// —— 断言 1：落盘事实未被过滤触碰（孤儿判定读事实，不读展示面）——
	scan, err := query.VaultScan(root, query.ScanOptions{})
	if err != nil {
		t.Fatalf("VaultScan：%v", err)
	}
	var allhid *query.CardEntry
	present := map[string]bool{}
	for i := range scan.Cards {
		present[scan.Cards[i].ID] = true
		if scan.Cards[i].ID == "k-20260908-allhid" {
			allhid = &scan.Cards[i]
		}
	}
	if allhid == nil {
		t.Fatal("allhid 应在扫描结果里")
	}
	facts := query.RelationsOut(*allhid) // 未过滤的落盘正向事实
	if len(facts) != 2 {
		t.Fatalf("落盘事实应仍有 2 条正向关系（过滤在展示面、记录不动）：%v", targetsOf(facts))
	}
	// 被指向的对端仍真实存在于库中 → 孤儿判定（看落盘事实）不会因「视图里看不见」而误判。
	for _, peer := range []string{"k-20260903-dep", "k-20260904-del"} {
		if !hasStr(targetsOf(facts), peer) {
			t.Fatalf("落盘事实应仍引用对端 %s：%v", peer, targetsOf(facts))
		}
		if !present[peer] {
			t.Fatalf("对端 %s 应真实存在（不是孤儿 / 悬空）", peer)
		}
	}
}

// TestG10DeletedDimensionNotReplaced —— G10：已删除维度未被 deprecated 维度「替换」。
// delonly 的唯一对端只是「已删除」（非 deprecated）：默认与 include 下都隐藏，
// 且因 deprecated 被隐藏的计数恒为 0（证明删除有独立的隐藏路径，不是被 deprecated 顶替）。
func TestG10DeletedDimensionNotReplaced(t *testing.T) {
	root := visVault(t)

	for _, mode := range []string{"default", "include"} {
		var rel *query.RelResult
		var card *query.CardShowResult
		if mode == "default" {
			rel, card = relDefault(t, root, "k-20260909-delonly"), showDefault(t, root, "k-20260909-delonly")
		} else {
			rel, card = relInc(t, root, "k-20260909-delonly"), showInc(t, root, "k-20260909-delonly")
		}
		if len(rel.Data.RelationsOut) != 0 {
			t.Fatalf("[%s] delonly rel 正向应为空（唯一对端已删除）：%v", mode, relOut(rel))
		}
		if len(card.Card.RelationsOut) != 0 {
			t.Fatalf("[%s] delonly card 正向应为空（唯一对端已删除）：%v", mode, cardOut(card))
		}
		// 已删除不计入「因 deprecated 被隐藏」的计数——两个维度是独立路径。
		if rel.HiddenDeprecated != 0 {
			t.Fatalf("[%s] 仅删除对端不得计入 deprecated 隐藏计数，实际 %d", mode, rel.HiddenDeprecated)
		}
	}

	// 反证删除维度确实还在：card show 里 del 卡自身 deleted 字段为真（M3 口径一字未动）。
	delCard := showDefault(t, root, "k-20260904-del")
	if !delCard.Card.Deleted {
		t.Fatalf("已删除卡的 Deleted 字段应为真（删除维度未被移除）：%+v", delCard.Card)
	}
}
