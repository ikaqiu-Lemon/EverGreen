package query_test

// T-…-021 的 query 层机器判据：`eg search` 的匹配分、过滤、四级全序与诊断透出。
//
// 判据来源：M2 查询合同 `2026-09-19-m2-query-contract.md` §1.1（参数与退出口径）、
// §1.2（hits[] 键表）、§1.3（匹配分）、§1.4（四级全序）、§1.5（失效卡同等可见）、§5（Q 系列）。
//
// 测试名统一含 `Search`，与 T-…-021 的 verify.test（`-run Search`）对齐。

import (
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
	"testing"

	"evergreen/internal/query"
)

// seedSearchVault 造一个三卡语料：标题命中 / 正文命中 / tags 命中各一张。
func seedSearchVault(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "domains/ai-infra/knowledge/k-20260901-t.md",
		card("k-20260901-t", "注意力机制的计算代价", "ai-infra", "active",
			"2026-09-01", "2026-09-01T10:00:00+08:00", "", ""))
	body := "---\nid: k-20260902-b\nstatus: active\ncreated_at: '2026-09-02'\n" +
		"updated_at: '2026-09-02T10:00:00+08:00'\ntitle: 无关标题\nsources: []\n---\n\n" +
		"## 知识内容\n\n正文里写了注意力两个字。\n"
	writeFile(t, root, "domains/ai-infra/knowledge/k-20260902-b.md", body)
	writeFile(t, root, "domains/ops/knowledge/k-20260903-g.md",
		card("k-20260903-g", "别的题目", "ops", "active",
			"2026-09-03", "2026-09-03T10:00:00+08:00", "  - 注意力\n", ""))
	return root
}

func searchIDs(res *query.SearchResult) []string {
	out := []string{}
	for _, h := range res.Hits {
		out = append(out, h.ID)
	}
	return out
}

func mustSearch(t *testing.T, root string, req query.SearchRequest) *query.SearchResult {
	t.Helper()
	res, err := query.Search(root, req)
	if err != nil {
		t.Fatalf("Search(%+v)：%v", req, err)
	}
	return res
}

// TestSearchHitKeysMatchContract —— hits[] 元素的 JSON 键**与合同 §1.2 表次序逐字相同**：
// 键集合与键序都比，实现不得自创键、不得漏键、不得改序。
func TestSearchHitKeysMatchContract(t *testing.T) {
	raw, err := json.Marshal(query.SearchHit{})
	if err != nil {
		t.Fatal(err)
	}
	got := jsonObjectKeys(string(raw))
	if !reflect.DeepEqual(got, query.SearchHitKeys()) {
		t.Fatalf("hits[] 键序 = %v，合同 §1.2 = %v（原文 %s）", got, query.SearchHitKeys(), raw)
	}
}

// TestSearchScoreFollowsContract —— 匹配分逐条对齐合同 §1.3：title +3 / tags +2 / body +1。
func TestSearchScoreFollowsContract(t *testing.T) {
	root := seedSearchVault(t)
	res := mustSearch(t, root, query.SearchRequest{Query: "注意力"})
	// 「注意力」按合同 §1.3 的切词口径切成**两个**二元组词（注意 / 意力），
	// 匹配分 = 各查询词得分之和，故每个字段的得分是单词得分 ×2：
	// title 3×2=6、tags 2×2=4、body 1×2=2。
	want := map[string]struct {
		score  int
		fields []string
	}{
		// 标题卡：标题命中 6，正文 `# 注意力机制的计算代价` 同样含该词 → +2。
		"k-20260901-t": {8, []string{"title", "body"}},
		"k-20260902-b": {2, []string{"body"}},
		"k-20260903-g": {4, []string{"tags"}},
	}
	if len(res.Hits) != 3 {
		t.Fatalf("命中数 = %d，期望 3（%v）", len(res.Hits), searchIDs(res))
	}
	for _, h := range res.Hits {
		w, ok := want[h.ID]
		if !ok {
			t.Fatalf("出现意外命中 %s", h.ID)
		}
		if h.Score != w.score {
			t.Errorf("%s 匹配分 = %d，期望 %d（合同 §1.3）", h.ID, h.Score, w.score)
		}
		if !reflect.DeepEqual(h.MatchedFields, w.fields) {
			t.Errorf("%s matched_fields = %v，期望 %v（title / tags / body 固定次序）",
				h.ID, h.MatchedFields, w.fields)
		}
	}
	if res.Total != len(res.Hits) {
		t.Errorf("total = %d，len(hits) = %d：合同 §1.2 要求两值恒等（不截断不分页）",
			res.Total, len(res.Hits))
	}
}

// TestSearchZeroScoreCardsExcluded —— 分为 0 的卡不进结果集（合同 §1.3 第 3 条）。
func TestSearchZeroScoreCardsExcluded(t *testing.T) {
	root := seedSearchVault(t)
	res := mustSearch(t, root, query.SearchRequest{Query: "不存在的词xyz"})
	if len(res.Hits) != 0 || res.Total != 0 {
		t.Fatalf("零命中期望 hits=[]、total=0，实际 %v / %d", searchIDs(res), res.Total)
	}
	if res.ScannedFiles != 3 {
		t.Errorf("scanned_files = %d，期望 3（零命中不影响扫描计数）", res.ScannedFiles)
	}
}

// TestSearchFilterDomainTagWindow —— 领域 / 标签 AND / updated_at 闭区间三类过滤。
func TestSearchFilterDomainTagWindow(t *testing.T) {
	root := seedSearchVault(t)
	if got := searchIDs(mustSearch(t, root,
		query.SearchRequest{Query: "注意力", Domain: "ops"})); !reflect.DeepEqual(got, []string{"k-20260903-g"}) {
		t.Errorf("--domain ops 命中 = %v，期望只有 ops 领域的卡", got)
	}
	if got := searchIDs(mustSearch(t, root,
		query.SearchRequest{Query: "注意力", Tags: []string{"注意力"}})); !reflect.DeepEqual(got, []string{"k-20260903-g"}) {
		t.Errorf("--tag 过滤命中 = %v，期望只有带该标签的卡", got)
	}
	if got := searchIDs(mustSearch(t, root, query.SearchRequest{
		Query: "注意力", Tags: []string{"注意力", "缺这个"}})); len(got) != 0 {
		t.Errorf("多个 --tag 是 AND 语义，命中应为空，实际 %v", got)
	}
	// 边界日**含**：since / until 都取 09-02 时恰好剩正文命中的那张。
	if got := searchIDs(mustSearch(t, root, query.SearchRequest{
		Query: "注意力", Since: "2026-09-02", Until: "2026-09-02"})); !reflect.DeepEqual(got, []string{"k-20260902-b"}) {
		t.Errorf("闭区间过滤命中 = %v，期望 [k-20260902-b]（边界日含）", got)
	}
}

// TestSearchInvalidArgsRejected —— 空检索词 / 非法日期 / 区间倒置一律 ErrInvalidQuery（CLI 退 1）。
func TestSearchInvalidArgsRejected(t *testing.T) {
	root := seedSearchVault(t)
	for _, req := range []query.SearchRequest{
		{Query: ""},
		{Query: "   "},
		{Query: "注意力", Since: "2026-9-2"},
		{Query: "注意力", Until: "20260902"},
		{Query: "注意力", Since: "2026-09-03", Until: "2026-09-02"},
	} {
		if _, err := query.Search(root, req); err == nil {
			t.Errorf("Search(%+v) 应报参数非法", req)
		} else if !isInvalidQuery(err) {
			t.Errorf("Search(%+v) 的错误未归入 ErrInvalidQuery：%v", req, err)
		}
	}
}

// TestSearchTotalOrderIsInputIndependent —— 四级全序的反证（合同 §1.4）：
// 打乱输入顺序（改文件名使 walk 序不同）与连续两次执行，ID 序列必须逐字相同。
func TestSearchTotalOrderIsInputIndependent(t *testing.T) {
	root := t.TempDir()
	// 同匹配分 + 同 updated_at + 同 created_at，只有 id 不同 → 必须按 id 升序。
	for _, id := range []string{"k-20260901-b", "k-20260901-a", "k-20260901-c"} {
		writeFile(t, root, "domains/ai-infra/knowledge/zz-"+id+".md",
			card(id, "同题", "ai-infra", "active", "2026-09-01", "2026-09-01T10:00:00+08:00", "", ""))
	}
	first := searchIDs(mustSearch(t, root, query.SearchRequest{Query: "同题"}))
	want := []string{"k-20260901-a", "k-20260901-b", "k-20260901-c"}
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("同分同时间的排序 = %v，期望按 id 升序 %v", first, want)
	}
	second := searchIDs(mustSearch(t, root, query.SearchRequest{Query: "同题"}))
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("同一语料两次执行结果不同：%v vs %v", first, second)
	}
}

// TestSearchDeprecatedCardsStayVisible —— 失效卡同等可见（合同 §1.5）：
// 照常进结果集、参与同一套排序（不降权不后置），deprecated 为派生真值。
func TestSearchDeprecatedCardsStayVisible(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "domains/ai-infra/knowledge/k-20260901-dep.md",
		card("k-20260901-dep", "失效的注意力卡", "ai-infra", "deprecated",
			"2026-09-01", "2026-09-09T10:00:00+08:00", "", ""))
	writeFile(t, root, "domains/ai-infra/knowledge/k-20260902-act.md",
		card("k-20260902-act", "在用的注意力卡", "ai-infra", "active",
			"2026-09-02", "2026-09-02T10:00:00+08:00", "", ""))
	res := mustSearch(t, root, query.SearchRequest{Query: "注意力"})
	// 两卡同分，失效卡的 updated_at 更新 → 排在前面（不后置才叫同等可见）。
	if got := searchIDs(res); !reflect.DeepEqual(got, []string{"k-20260901-dep", "k-20260902-act"}) {
		t.Fatalf("失效卡被降权 / 后置：%v", got)
	}
	if !res.Hits[0].Deprecated || res.Hits[0].Status != "deprecated" {
		t.Errorf("失效卡的 deprecated / status 不对：%+v", res.Hits[0])
	}
	if res.Hits[1].Deprecated {
		t.Errorf("active 卡不得标 deprecated：%+v", res.Hits[1])
	}
}

// TestSearchUnparsableFileEmitsQ1AndCountsConserve —— R-1 关闭判据 + 计数守恒（合同 §5.1 / §5.3）。
func TestSearchUnparsableFileEmitsQ1AndCountsConserve(t *testing.T) {
	root := seedSearchVault(t)
	writeFile(t, root, "domains/ai-infra/knowledge/broken.md", "---\n- 1\n---\n\n# 坏卡\n")
	res := mustSearch(t, root, query.SearchRequest{Query: "注意力"})
	if res.SkippedFiles != 1 {
		t.Fatalf("skipped_files = %d，期望 1", res.SkippedFiles)
	}
	if res.ScannedFiles != 4 {
		t.Fatalf("scanned_files = %d，期望 4", res.ScannedFiles)
	}
	var q1, q3 int
	for _, d := range res.Diagnostics {
		switch d.Code {
		case query.CodeQ1:
			q1++
			if d.Path != "domains/ai-infra/knowledge/broken.md" || d.Message == "" {
				t.Errorf("Q1 必须带坏文件路径与人类可读原因：%+v", d)
			}
		case query.CodeQ3:
			q3++
		}
	}
	if q1 != 1 || q3 != 1 {
		t.Fatalf("Q1 = %d、Q3 = %d，期望各 1（Q3 只在有 Q1/Q2 时出现恰一条）", q1, q3)
	}
	if len(res.Hits) != 3 {
		t.Errorf("一个坏文件不得丢掉其余结果：%v", searchIDs(res))
	}
	// 计数守恒：命中数 ≤ 进入结果集的卡数，进入结果集的卡数 + skipped == scanned。
	if len(res.Hits)+res.SkippedFiles != res.ScannedFiles {
		t.Errorf("计数不守恒：hits=%d + skipped=%d != scanned=%d",
			len(res.Hits), res.SkippedFiles, res.ScannedFiles)
	}
}

// TestSearchNoQ3WithoutQ1OrQ2 —— 反向可判：无 Q1 / Q2 时 warnings 里没有 Q3（合同 §5.1）。
func TestSearchNoQ3WithoutQ1OrQ2(t *testing.T) {
	root := seedSearchVault(t)
	res := mustSearch(t, root, query.SearchRequest{Query: "注意力"})
	for _, d := range res.Diagnostics {
		t.Errorf("干净语料不应有任何诊断，实际 %+v", d)
	}
}

// TestSearchIsReadOnly —— 只读：检索前后 vault 内每个文件的字节与 mtime 逐一不变（合同 §6）。
func TestSearchIsReadOnly(t *testing.T) {
	root := seedSearchVault(t)
	before := treeSnapshot(t, root)
	if _, err := query.Search(root, query.SearchRequest{Query: "注意力"}); err != nil {
		t.Fatal(err)
	}
	if after := treeSnapshot(t, root); after != before {
		t.Fatalf("search 改动了 vault：\n%s\n%s", before, after)
	}
}

// —— 测试辅助 ——

// jsonObjectKeys 按**出现次序**取一个平坦 JSON 对象的键（用于逐字反证键序）。
func jsonObjectKeys(raw string) []string {
	out := []string{}
	for _, m := range regexp.MustCompile(`"([a-z_]+)":`).FindAllStringSubmatch(raw, -1) {
		out = append(out, m[1])
	}
	return out
}

// isInvalidQuery 判错误是否归入 query.ErrInvalidQuery（CLI 据此退 1）。
func isInvalidQuery(err error) bool { return errors.Is(err, query.ErrInvalidQuery) }
