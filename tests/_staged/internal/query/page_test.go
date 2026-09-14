package query

// [S4] 读路径**分页与截断**的机器判据（M5 索引架构合同 §8.2 A-47 + `M-005` 判据 12；T-…-068）。
//
// 合同 §8.2 的五行边界表逐行有对应用例，另加两条本 task 自己立的硬判据：
//
//	§8.2 表行                          → 用例
//	--limit 0 == 不限量、永不产 W25      → TestLimitZeroMeansNoLimit
//	--offset 超界 → 空结果 + 退 0        → TestOffsetBeyondEndEmptyNotError
//	负数 / 非整数 → 参数错（退 4）        → TestPageInvalidParamsRejected（码在 cli 层断言）
//	total > offset+limit → 恰一条 W25    → TestTruncationEmitsW25
//	翻页无重无漏、等于一次性全量          → TestPaginationNoDupNoGap
//	参数集合封闭（恰两个）               → TestPageParamsClosedSet
//
//	【本 task 加的两条】
//	`--limit` 是**一次读的全局上限**      → TestRelLimitIsGlobalNotPerList
//	正反双列表分页 ≡ 合并序列全局分页      → TestPagePairEqualsGlobalApplyPage
//
// 最后两条是这次纠偏的核心：`card show` / `rel` 的 data 里有**两个**关系列表，
// 若两个列表各自分页，`--limit 50` 会最多返回 100 条 —— 合同写的是「最多返回的条数」，
// 那样做等于把一个上限偷偷变成两个。因此实现取「先合并成一条确定序列、再全局分页」，
// 并用 TestPagePairEqualsGlobalApplyPage 机器证明二者**恒等**（不是「大致等价」）。

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// —— 语料：焦点卡有 3 条正向 + 3 条反向可见关系（对端全为 active，默认视图一条不隐藏）——

// pgVault 造一个正反向条数都 > 1 的语料，用来观察「两个列表 + 一个 limit」的行为。
//
// 焦点卡 hub：正向 → p1 / p2 / p3（三条 supports），反向 ← s1 / s2 / s3（三条 derives）。
// 对端全部 active：默认可见性一条都不隐藏，因此 total 恒为 6，分页判据不被可见性噪声干扰。
func pgVault(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	k := "domains/ai-infra/knowledge/"
	bkWrite(t, root, k+"k-20261201-hub.md", bkCard("k-20261201-hub", "枢纽卡",
		"active", "2026-12-05T10:00:00+08:00", "  - 枢纽\n",
		"  - type: supports\n    target: k-20261201-p1\n    reason: 支撑一\n"+
			"  - type: supports\n    target: k-20261201-p2\n    reason: 支撑二\n"+
			"  - type: supports\n    target: k-20261201-p3\n    reason: 支撑三\n"))
	for _, id := range []string{"k-20261201-p1", "k-20261201-p2", "k-20261201-p3"} {
		bkWrite(t, root, k+id+".md", bkCard(id, "对端 "+id, "active",
			"2026-12-05T10:00:00+08:00", "  - 枢纽\n", ""))
	}
	for _, id := range []string{"k-20261201-s1", "k-20261201-s2", "k-20261201-s3"} {
		bkWrite(t, root, k+id+".md", bkCard(id, "来源 "+id, "active",
			"2026-12-05T10:00:00+08:00", "  - 枢纽\n",
			"  - type: derives\n    target: k-20261201-hub\n    reason: 派生自枢纽\n"))
	}
	return root
}

// pgCount 数某个诊断码出现的次数（「恰一条」是逐字判据，只能数）。
func pgCount(diags []Diagnostic, code string) int {
	n := 0
	for _, d := range diags {
		if d.Code == code {
			n++
		}
	}
	return n
}

// pgEdgeKeys 给出条目的可比对签名（用于翻页拼接的无重无漏判定）。
func pgEdgeKeys(edges ...[]RelationEdge) []string {
	out := []string{}
	for _, list := range edges {
		for _, e := range list {
			out = append(out, e.From+"|"+e.Type+"|"+e.Target+"|"+e.Reason+"|"+e.Path)
		}
	}
	return out
}

// —— 参数集合封闭 ——

func TestPageParamsClosedSet(t *testing.T) {
	want := []string{"limit", "offset"}
	if got := PageParams(); !reflect.DeepEqual(got, want) {
		t.Fatalf("分页参数集合 = %v，期望恰 %v（封闭，A-47 排除 --page/--page-size）", got, want)
	}
	if PageLimitFlag != "limit" || PageOffsetFlag != "offset" {
		t.Fatalf("分页 flag 名漂移：limit=%q offset=%q", PageLimitFlag, PageOffsetFlag)
	}
	if PageDefaultLimit != 50 {
		t.Fatalf("默认页大小 = %d，合同 §8.2 写的是 50", PageDefaultLimit)
	}
	// 库层零值必须是「不限量」：M1–M4 的既有调用方一字不受影响。
	var zero PageSpec
	if !zero.Unlimited() {
		t.Fatalf("PageSpec 零值不是不限量：库内既有调用方行为会被静默改写")
	}
	if err := zero.Validate(); err != nil {
		t.Fatalf("PageSpec 零值应合法，实际 %v", err)
	}
}

// —— --limit 0 == 不限量、永不产 W25 ——

func TestLimitZeroMeansNoLimit(t *testing.T) {
	items := []int{1, 2, 3, 4, 5}
	got, pg := ApplyPage(items, PageSpec{Limit: 0})
	if !reflect.DeepEqual(got, items) {
		t.Fatalf("--limit 0 应返回全部，实际 %v", got)
	}
	if pg.Truncated || pg.Total != 5 || pg.Returned != 5 {
		t.Fatalf("--limit 0 的分页事实错：%+v", pg)
	}
	// offset 与 limit 0 可以叠加：跳过 2 条、其余全给，且仍然不算截断。
	got, pg = ApplyPage(items, PageSpec{Limit: 0, Offset: 2})
	if !reflect.DeepEqual(got, []int{3, 4, 5}) || pg.Truncated {
		t.Fatalf("--limit 0 --offset 2 错：got=%v pg=%+v", got, pg)
	}

	// 端到端：三条读路径在 --limit 0 下一条 W25 都不许有。
	root := pgVault(t)
	res, err := bkRel(root, RelRequest{ID: model.CardID("k-20261201-hub"), Page: PageSpec{Limit: 0}})
	if err != nil {
		t.Fatalf("rel --limit 0：%v", err)
	}
	if n := len(res.Data.RelationsOut) + len(res.Data.RelationsIn); n != 6 {
		t.Fatalf("rel --limit 0 应返回全部 6 条，实际 %d", n)
	}
	if got := pgCount(res.Diagnostics, CodeResultTruncated); got != 0 {
		t.Fatalf("--limit 0 产了 %d 条 W25（合同：永不产出）", got)
	}
	sres, err := bkSearch(root, SearchRequest{Query: "枢纽", Page: PageSpec{Limit: 0}})
	if err != nil {
		t.Fatalf("search --limit 0：%v", err)
	}
	if len(sres.Hits) != sres.Total || pgCount(sres.Diagnostics, CodeResultTruncated) != 0 {
		t.Fatalf("search --limit 0：hits=%d total=%d W25=%d",
			len(sres.Hits), sres.Total, pgCount(sres.Diagnostics, CodeResultTruncated))
	}
}

// —— --offset 超界 → 空结果，不是错误 ——

func TestOffsetBeyondEndEmptyNotError(t *testing.T) {
	items := []int{1, 2, 3}
	got, pg := ApplyPage(items, PageSpec{Limit: 2, Offset: 99})
	if len(got) != 0 || got == nil {
		t.Fatalf("offset 超界应返回**空切片**（非 nil），实际 %#v", got)
	}
	if pg.Total != 3 || pg.Returned != 0 {
		t.Fatalf("offset 超界的分页事实错：%+v（total 必须仍是分页前总数）", pg)
	}

	root := pgVault(t)
	// rel：正反向都为空、无错误；total 仍是库里的条数事实。
	res, err := bkRel(root, RelRequest{ID: model.CardID("k-20261201-hub"),
		Page: PageSpec{Limit: 2, Offset: 100}})
	if err != nil {
		t.Fatalf("offset 超界不应是错误，实际 %v", err)
	}
	if len(res.Data.RelationsOut) != 0 || len(res.Data.RelationsIn) != 0 {
		t.Fatalf("offset 超界应两个列表都空，实际 out=%d in=%d",
			len(res.Data.RelationsOut), len(res.Data.RelationsIn))
	}
	if res.Page.Total != 6 {
		t.Fatalf("offset 超界时 total 被改写成 %d（应恒为分页前的 6）", res.Page.Total)
	}
	// card show 同理（同一份 ApplyPagePair，两条读路径不许有两套语义）。
	view, err := ShowCardPaged(root, model.CardID("k-20261201-hub"), bkDeps,
		PageSpec{Limit: 2, Offset: 100})
	if err != nil {
		t.Fatalf("card show offset 超界不应是错误，实际 %v", err)
	}
	if len(view.Card.RelationsOut) != 0 || len(view.Card.RelationsIn) != 0 {
		t.Fatalf("card show offset 超界应两个列表都空")
	}
	// search 同理：空 hits、total 不变、退 0（无 error）。
	sres, err := bkSearch(root, SearchRequest{Query: "枢纽", Page: PageSpec{Limit: 2, Offset: 100}})
	if err != nil {
		t.Fatalf("search offset 超界不应是错误，实际 %v", err)
	}
	if len(sres.Hits) != 0 || sres.Total == 0 {
		t.Fatalf("search offset 超界：hits=%d total=%d", len(sres.Hits), sres.Total)
	}
}

// —— total > offset + limit → 恰一条 W25 ——

func TestTruncationEmitsW25(t *testing.T) {
	root := pgVault(t)

	// ① rel：6 条条目、limit 2 —— 正反两个列表都被截断，仍必须**恰一条** W25。
	res, err := bkRel(root, RelRequest{ID: model.CardID("k-20261201-hub"), Page: PageSpec{Limit: 2}})
	if err != nil {
		t.Fatalf("rel：%v", err)
	}
	if got := pgCount(res.Diagnostics, CodeResultTruncated); got != 1 {
		t.Fatalf("rel 截断应产恰一条 W25，实际 %d 条：%v", got, res.Diagnostics)
	}
	if !res.Page.Truncated || res.Page.Total != 6 {
		t.Fatalf("rel 截断事实错：%+v", res.Page)
	}
	// W25 的文案必须说出「共几条 / 本页几条」，否则截断结果会被读成完整结果。
	for _, d := range res.Diagnostics {
		if d.Code != CodeResultTruncated {
			continue
		}
		if d.Path != diagSummaryPath {
			t.Fatalf("W25 的 path 应为汇总口径，实际 %q", d.Path)
		}
		for _, kw := range []string{"共 6 条", "返回 2 条", "--limit"} {
			if !strings.Contains(d.Message, kw) {
				t.Fatalf("W25 文案缺 %q：%s", kw, d.Message)
			}
		}
	}

	// ② card show：同一份语料、同一个 limit，同样恰一条。
	view, err := ShowCardPaged(root, model.CardID("k-20261201-hub"), bkDeps, PageSpec{Limit: 2})
	if err != nil {
		t.Fatalf("card show：%v", err)
	}
	if got := pgCount(view.Diagnostics, CodeResultTruncated); got != 1 {
		t.Fatalf("card show 截断应产恰一条 W25，实际 %d 条", got)
	}

	// ③ 边界：total == offset + limit 恰好不截断（合同判据是严格大于）。
	res, err = bkRel(root, RelRequest{ID: model.CardID("k-20261201-hub"),
		Page: PageSpec{Limit: 4, Offset: 2}})
	if err != nil {
		t.Fatalf("rel：%v", err)
	}
	if res.Page.Truncated || pgCount(res.Diagnostics, CodeResultTruncated) != 0 {
		t.Fatalf("total == offset+limit 不应判截断：%+v", res.Page)
	}
}

// —— 翻页无重无漏，且等于一次性全量 ——

func TestPaginationNoDupNoGap(t *testing.T) {
	root := pgVault(t)
	id := model.CardID("k-20261201-hub")

	full, err := bkRel(root, RelRequest{ID: id, Page: PageSpec{Limit: 0}})
	if err != nil {
		t.Fatalf("全量 rel：%v", err)
	}
	want := pgEdgeKeys(full.Data.RelationsOut, full.Data.RelationsIn)
	if len(want) != 6 {
		t.Fatalf("语料应有 6 条可见条目，实际 %d", len(want))
	}

	for _, size := range []int{1, 2, 4, 5, 6, 7} {
		got := []string{}
		for offset := 0; ; offset += size {
			res, err := bkRel(root, RelRequest{ID: id, Page: PageSpec{Limit: size, Offset: offset}})
			if err != nil {
				t.Fatalf("rel(limit=%d offset=%d)：%v", size, offset, err)
			}
			page := pgEdgeKeys(res.Data.RelationsOut, res.Data.RelationsIn)
			if len(page) > size {
				t.Fatalf("limit=%d 却返回了 %d 条（limit 必须是全局上限）", size, len(page))
			}
			if len(page) == 0 {
				break
			}
			got = append(got, page...)
			if offset > 100 { // 防御：分页不推进就当场失败，别把用例挂死
				t.Fatalf("分页未推进")
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("limit=%d 逐页拼接 != 一次性全量\n got =%v\n want=%v", size, got, want)
		}
	}

	// search 同样必须无重无漏（它只有一个列表，是分页语义的对照组）。
	fullS, err := bkSearch(root, SearchRequest{Query: "枢纽", Page: PageSpec{Limit: 0}})
	if err != nil {
		t.Fatalf("全量 search：%v", err)
	}
	wantIDs := []string{}
	for _, h := range fullS.Hits {
		wantIDs = append(wantIDs, h.ID)
	}
	gotIDs := []string{}
	for offset := 0; offset < len(wantIDs); offset += 2 {
		res, err := bkSearch(root, SearchRequest{Query: "枢纽", Page: PageSpec{Limit: 2, Offset: offset}})
		if err != nil {
			t.Fatalf("search(offset=%d)：%v", offset, err)
		}
		for _, h := range res.Hits {
			gotIDs = append(gotIDs, h.ID)
		}
	}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("search 翻页拼接 != 全量\n got =%v\n want=%v", gotIDs, wantIDs)
	}
}

// —— 【纠偏判据一】--limit 是一次读的**全局**上限，不是每个列表各一份 ——

// TestRelLimitIsGlobalNotPerList 逐个 limit 值反证「返回条数 == min(limit, total)」。
//
// 这条判据针对的错误实现是：对 relations_out 与 relations_in **各自** ApplyPage。
// 那样 `--limit 2` 在本语料（正反各 3 条）会返回 4 条 —— 合同 §8.2 写的是
// 「最多返回的条数」，一次读只有一个上限。用例对 limit ∈ [1,7] 全覆盖，
// 任何「各自分页」的实现都会在 limit ∈ [1,5] 处当场变红。
func TestRelLimitIsGlobalNotPerList(t *testing.T) {
	root := pgVault(t)
	id := model.CardID("k-20261201-hub")
	const total = 6 // 正向 3 + 反向 3，对端全 active

	for limit := 1; limit <= 7; limit++ {
		want := limit
		if want > total {
			want = total
		}
		res, err := bkRel(root, RelRequest{ID: id, Page: PageSpec{Limit: limit}})
		if err != nil {
			t.Fatalf("rel(limit=%d)：%v", limit, err)
		}
		got := len(res.Data.RelationsOut) + len(res.Data.RelationsIn)
		if got != want {
			t.Fatalf("rel(limit=%d)：返回 %d 条（out=%d in=%d），期望 min(limit,total)=%d —— "+
				"limit 被当成了每个列表各自的上限（最多 2*limit）",
				limit, got, len(res.Data.RelationsOut), len(res.Data.RelationsIn), want)
		}
		if res.Page.Returned != got || res.Page.Total != total {
			t.Fatalf("rel(limit=%d) 分页事实与实际条数不一致：%+v，实际 %d 条", limit, res.Page, got)
		}

		view, err := ShowCardPaged(root, id, bkDeps, PageSpec{Limit: limit})
		if err != nil {
			t.Fatalf("card show(limit=%d)：%v", limit, err)
		}
		gotShow := len(view.Card.RelationsOut) + len(view.Card.RelationsIn)
		if gotShow != want {
			t.Fatalf("card show(limit=%d)：返回 %d 条（out=%d in=%d），期望 %d",
				limit, gotShow, len(view.Card.RelationsOut), len(view.Card.RelationsIn), want)
		}
		// 两条读路径必须给出**同一份**分页事实（同一个 ApplyPagePair，不许有两套语义）。
		if view.Page != res.Page {
			t.Fatalf("card show 与 rel 的分页事实不一致：%+v vs %+v", view.Page, res.Page)
		}
	}
}

// —— 【纠偏判据二】双列表分页 ≡ 合并序列上的一次全局分页 ——

// TestPagePairEqualsGlobalApplyPage 是「与合同一致且可机器证明的等价实现」的那份证明：
// 对一大批 (len(out), len(in), limit, offset) 组合，逐格断言
//
//	PagePairSequence(ApplyPagePair(...)) == ApplyPage(PagePairSequence(out,in), p) 的结果
//
// 且 Page 事实两者逐格相同。等价性因此不是注释里的说法，而是被穷举过的性质。
func TestPagePairEqualsGlobalApplyPage(t *testing.T) {
	mk := func(prefix string, n int) []RelationEdge {
		out := []RelationEdge{}
		for i := 0; i < n; i++ {
			out = append(out, rkEdge(prefix, "supports", prefix+string(rune('a'+i)), "r", "p.md"))
		}
		return out
	}
	for lo := 0; lo <= 4; lo++ {
		for li := 0; li <= 4; li++ {
			out, in := mk("out", lo), mk("in", li)
			seq := PagePairSequence(out, in)
			if len(seq) != lo+li {
				t.Fatalf("合并序列长度 = %d，期望 %d", len(seq), lo+li)
			}
			for _, limit := range []int{0, 1, 2, 3, 5, 9} {
				for offset := 0; offset <= lo+li+1; offset++ {
					p := PageSpec{Limit: limit, Offset: offset}
					gotOut, gotIn, gotPage := ApplyPagePair(out, in, p)
					wantSeq, wantPage := ApplyPage(seq, p)
					if !reflect.DeepEqual(PagePairSequence(gotOut, gotIn), wantSeq) {
						t.Fatalf("out=%d in=%d limit=%d offset=%d：分段结果 != 全局分页结果\n got =%v\n want=%v",
							lo, li, limit, offset, PagePairSequence(gotOut, gotIn), wantSeq)
					}
					if gotPage != wantPage {
						t.Fatalf("out=%d in=%d limit=%d offset=%d：分页事实不同 %+v vs %+v",
							lo, li, limit, offset, gotPage, wantPage)
					}
					if limit > 0 && gotPage.Returned > limit {
						t.Fatalf("out=%d in=%d limit=%d offset=%d：返回 %d 条超过全局上限",
							lo, li, limit, offset, gotPage.Returned)
					}
					// 分段必须保序且不串段：out 段全部来自 out、in 段全部来自 in。
					for _, e := range gotOut {
						if e.From != "out" {
							t.Fatalf("out 段里混入了反向条目：%+v", e)
						}
					}
					for _, e := range gotIn {
						if e.From != "in" {
							t.Fatalf("in 段里混入了正向条目：%+v", e)
						}
					}
				}
			}
		}
	}
}

// —— 负数参数是错误族（退出码由命令层给 4，本层只保证错误类型）——

// isInvalidPage 判定错误是否属分页参数错族（ErrInvalidPage）。
func isInvalidPage(err error) bool { return errors.Is(err, ErrInvalidPage) }

func TestPageInvalidParamsRejected(t *testing.T) {
	root := pgVault(t)
	id := model.CardID("k-20261201-hub")
	bad := []PageSpec{{Limit: -1}, {Offset: -1}, {Limit: -3, Offset: -2}}
	for _, p := range bad {
		if err := p.Validate(); err == nil || !isInvalidPage(err) {
			t.Fatalf("PageSpec%+v 应判非法（ErrInvalidPage），实际 %v", p, err)
		}
		if _, err := bkRel(root, RelRequest{ID: id, Page: p}); !isInvalidPage(err) {
			t.Fatalf("rel 未拒绝 %+v：%v", p, err)
		}
		if _, err := ShowCardPaged(root, id, bkDeps, p); !isInvalidPage(err) {
			t.Fatalf("card show 未拒绝 %+v：%v", p, err)
		}
		if _, err := bkSearch(root, SearchRequest{Query: "枢纽", Page: p}); !isInvalidPage(err) {
			t.Fatalf("search 未拒绝 %+v：%v", p, err)
		}
	}
}
