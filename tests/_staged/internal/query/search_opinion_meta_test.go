package query

// T-…-006-B1b-q 的 query 层机器判据：`query.Search` 的**观点命中**携带后续 CLI 批
// （`eg opinion search`）所需的专属投影事实 —— `validation` 论证进度 + 该观点自身
// relations[] 中 supports / limits / opposing 三类的**确定性计数**（固定次序），
// 而这些事实**绝不进** `eg search` 的任何 JSON 输出（SearchHitKeys / SearchDataKeys 一字不变）。
//
// 合同（本批行为，设计 §5.2 检索拆分 / §6.4 论证关系）：
//   - 观点命中：SearchHit.Opinion 非空，Validation 取 frontmatter 逐字原值（pending|
//     validated|rejected 三态都召回，validation 绝不作隐式过滤），关系摘要只数
//     supports/limits/opposing（derives / replaced_by 一律忽略），固定次序
//     supports → limits → opposing。
//   - 知识命中：SearchHit.Opinion 恒为 nil（知识元数据为空）。
//   - 两条后端逐字等价：healthy 索引与 missing fallback 的命中顺序 / total / 每条命中的
//     validation 与三类计数完全一致；healthy 无降级诊断、missing 恰 W23 + Q5。
//   - json.Marshal(SearchHit)：新增事实经 `json:"-"` 承载，**一个新键都不泄漏**，
//     且 hits[] 键序仍逐字等于 SearchHitKeys()（合同 §1.2）。
//
// 纪律：语料走**真实扫描 / 索引链路**（bkoBuildIndexWithOpinions 用产品唯一的 VaultScan +
// index.Build），不以 mock DTO 代替；本文件是**包内**测试（package query），因为要断言的
// SearchHit.Opinion 是后续 CLI 才映射的内部投影事实，且要读 res.backend 这一不进 data 的口径。

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// —— 观点语料脚手架（token 一律落 title，score 干净；relations 逐条拼入 frontmatter）——

// omRel 描述一条要拼进 frontmatter 的论证关系。
type omRel struct{ typ, target string }

// omOpinion 造一条最小合法观点：validation / relations 成为唯一变量，检索令牌恒在 title。
func omOpinion(id, title, updated, validation string, rels []omRel) string {
	fm := "---\nid: " + id + "\nstatus: active\ncreated_at: '2026-09-01'\nupdated_at: '" +
		updated + "'\ntitle: " + title + "\nvalidation: " + validation + "\nsources: []\n"
	if len(rels) > 0 {
		fm += "relations:\n"
		for _, r := range rels {
			fm += "  - type: " + r.typ + "\n    target: " + r.target +
				"\n    reason: 关系占位理由\n"
		}
	}
	return fm + "---\n\n## 观点\n\n占位主张，令牌只在标题。\n"
}

// omWrite 把一条观点写进 vault（复用 backend_test.go 的 bkWrite）。
func omWrite(t *testing.T, root, domain, id, title, updated, validation string, rels []omRel) {
	t.Helper()
	bkWrite(t, root, "domains/"+domain+"/opinions/"+id+".md",
		omOpinion(id, title, updated, validation, rels))
}

// omReq 组装一次 kind=opinion 的检索（令牌 ometoken）。
func omReq() SearchRequest { return SearchRequest{Query: "ometoken", Kind: SearchKindOpinion} }

// omHitByID 取指定命中（找不到即 fatal，避免后续 nil 解引用误导判据）。
func omHitByID(t *testing.T, res *SearchResult, id string) SearchHit {
	t.Helper()
	for _, h := range res.Hits {
		if h.ID == id {
			return h
		}
	}
	t.Fatalf("结果里没有命中 %s：%v", id, skIDs(res))
	return SearchHit{}
}

// —— ① 观点命中携带 validation + 三类关系确定性计数（derives 被忽略）——

func TestSearchOpinionHitCarriesValidationAndRelationCounts(t *testing.T) {
	root := t.TempDir()
	// 混合关系：2 supports + 1 limits + 3 opposing + 1 derives（derives 不进摘要）。
	omWrite(t, root, "ai-infra", "o-20260910-mix", "ometoken mix",
		"2026-09-10T10:00:00+08:00", "validated", []omRel{
			{"supports", "k-a"}, {"supports", "k-b"},
			{"limits", "k-c"},
			{"opposing", "k-d"}, {"opposing", "k-e"}, {"opposing", "k-f"},
			{"derives", "k-g"}, // 论证摘要绝不计入 derives
		})
	res, err := bkSearch(root, omReq())
	if err != nil {
		t.Fatalf("opinion 检索：%v", err)
	}
	h := omHitByID(t, res, "o-20260910-mix")
	if h.Opinion == nil {
		t.Fatal("观点命中必须携带 Opinion 元数据，实际为 nil")
	}
	if h.Opinion.Validation != "validated" {
		t.Fatalf("validation = %q，期望 validated（逐字原值）", h.Opinion.Validation)
	}
	want := OpinionRelationSummary{Supports: 2, Limits: 1, Opposing: 3}
	if h.Opinion.Relations != want {
		t.Fatalf("关系摘要 = %+v，期望 %+v（只数 supports/limits/opposing，derives 忽略）",
			h.Opinion.Relations, want)
	}
}

// —— ② validation 三态都召回，且各自的 validation 逐字正确（validation 非隐式过滤）——

func TestSearchOpinionValidationAllStatesCarried(t *testing.T) {
	root := t.TempDir()
	omWrite(t, root, "ai-infra", "o-20260910-p", "ometoken pend",
		"2026-09-10T10:00:00+08:00", "pending", nil)
	omWrite(t, root, "ai-infra", "o-20260909-v", "ometoken val",
		"2026-09-09T10:00:00+08:00", "validated", nil)
	omWrite(t, root, "ai-infra", "o-20260908-r", "ometoken rej",
		"2026-09-08T10:00:00+08:00", "rejected", nil)
	res, err := bkSearch(root, omReq())
	if err != nil {
		t.Fatalf("opinion 检索：%v", err)
	}
	if len(res.Hits) != 3 {
		t.Fatalf("三态观点都应召回，实际 %d 条：%v", len(res.Hits), skIDs(res))
	}
	want := map[string]string{
		"o-20260910-p": "pending",
		"o-20260909-v": "validated",
		"o-20260908-r": "rejected",
	}
	for id, v := range want {
		h := omHitByID(t, res, id)
		if h.Opinion == nil || h.Opinion.Validation != v {
			t.Fatalf("命中 %s 的 validation = %v，期望 %q", id, h.Opinion, v)
		}
		// 无 relations 的观点：三类计数恒 0，但摘要结构仍在（不是 nil 元数据）。
		if h.Opinion.Relations != (OpinionRelationSummary{}) {
			t.Fatalf("命中 %s 无关系时摘要应全 0，实际 %+v", id, h.Opinion.Relations)
		}
	}
}

// —— ③ 知识命中的元数据为空（Opinion 恒 nil），且 --kind all 下两类各自正确 ——

func TestSearchKnowledgeHitHasNoOpinionMeta(t *testing.T) {
	root := t.TempDir()
	// 一张知识卡 + 一条观点，令牌都在 title，走 --kind all 混排。
	bkWrite(t, root, "domains/ai-infra/knowledge/k-20260910-kc.md",
		bkCard("k-20260910-kc", "ometoken card", "active",
			"2026-09-10T10:00:00+08:00", "", ""))
	omWrite(t, root, "ai-infra", "o-20260909-oc", "ometoken opinion",
		"2026-09-09T10:00:00+08:00", "pending", []omRel{{"supports", "k-20260910-kc"}})
	res, err := bkSearch(root, SearchRequest{Query: "ometoken", Kind: SearchKindAll})
	if err != nil {
		t.Fatalf("all 检索：%v", err)
	}
	kh := omHitByID(t, res, "k-20260910-kc")
	if kh.Opinion != nil {
		t.Fatalf("知识命中不得携带 Opinion 元数据，实际 %+v", kh.Opinion)
	}
	oh := omHitByID(t, res, "o-20260909-oc")
	if oh.Opinion == nil || oh.Opinion.Relations.Supports != 1 {
		t.Fatalf("观点命中应带 supports=1 的元数据，实际 %+v", oh.Opinion)
	}
}

// —— ④ 两条后端逐字等价：healthy 索引 vs missing fallback ——

func TestSearchOpinionMetaBackendEquivalence(t *testing.T) {
	seed := func(t *testing.T) string {
		root := t.TempDir()
		omWrite(t, root, "ai-infra", "o-20260910-a", "ometoken alpha",
			"2026-09-10T10:00:00+08:00", "validated",
			[]omRel{{"supports", "k-a"}, {"limits", "k-b"}})
		omWrite(t, root, "ai-infra", "o-20260909-b", "ometoken beta",
			"2026-09-09T10:00:00+08:00", "pending",
			[]omRel{{"opposing", "k-c"}, {"derives", "k-d"}})
		omWrite(t, root, "ai-infra", "o-20260908-c", "ometoken gamma",
			"2026-09-08T10:00:00+08:00", "rejected", nil)
		return root
	}

	// healthy 索引：本 kind 的唯一真值。
	healthyRoot := seed(t)
	bkoBuildIndexWithOpinions(t, healthyRoot)
	healthy, err := bkSearch(healthyRoot, omReq())
	if err != nil {
		t.Fatalf("healthy：%v", err)
	}
	if healthy.backend.Kind != BackendIndex {
		t.Fatalf("前置：healthy 必须走索引后端，实际 %s", healthy.backend.Kind)
	}
	if hc := skDiagCounts(healthy); hc["W22"]+hc["W23"]+hc["W24"]+hc[CodeQ5] != 0 {
		t.Fatalf("healthy 不应有降级诊断，实际 %v", healthy.Diagnostics)
	}

	// missing fallback：删索引 → 扫描后端 + 恰 W23 + Q5。
	fbRoot := seed(t)
	bkoBuildIndexWithOpinions(t, fbRoot)
	bkDropIndex(t, fbRoot)
	got, err := bkSearch(fbRoot, omReq())
	if err != nil {
		t.Fatalf("fallback：%v", err)
	}
	if got.backend.Kind != BackendScan {
		t.Fatalf("前置：fallback 必须降级为扫描后端，实际 %s", got.backend.Kind)
	}
	dc := skDiagCounts(got)
	if dc["W23"] != 1 || dc[CodeQ5] != 1 || len(got.Diagnostics) != 2 {
		t.Fatalf("missing fallback 应恰 W23 + Q5（共 2 条），实际 %v", got.Diagnostics)
	}

	// 顺序 / total 逐字一致。
	if !reflect.DeepEqual(skIDs(got), skIDs(healthy)) {
		t.Fatalf("命中顺序分叉：scan=%v index=%v", skIDs(got), skIDs(healthy))
	}
	if got.Total != healthy.Total {
		t.Fatalf("total 分叉：scan=%d index=%d", got.Total, healthy.Total)
	}
	// 每条命中的 validation 与三类计数逐字一致（两条后端都从权威 Markdown 取观点事实）。
	for _, h := range healthy.Hits {
		g := omHitByID(t, got, h.ID)
		if h.Opinion == nil || g.Opinion == nil {
			t.Fatalf("命中 %s 两后端都必须带元数据：index=%+v scan=%+v", h.ID, h.Opinion, g.Opinion)
		}
		if !reflect.DeepEqual(*h.Opinion, *g.Opinion) {
			t.Fatalf("命中 %s 元数据分叉：index=%+v scan=%+v", h.ID, *h.Opinion, *g.Opinion)
		}
	}
	// 抽验一条已知计数，防止「两边一起错成 0」的空等价。
	a := omHitByID(t, healthy, "o-20260910-a")
	if a.Opinion.Validation != "validated" ||
		a.Opinion.Relations != (OpinionRelationSummary{Supports: 1, Limits: 1}) {
		t.Fatalf("o-20260910-a 元数据非预期：%+v", a.Opinion)
	}
}

// —— ⑤ 新增事实不泄漏进 eg search JSON；hits[] 键序仍逐字等于 SearchHitKeys ——

func TestSearchOpinionMetaNotInSearchJSON(t *testing.T) {
	// 即便 Opinion 元数据**非空**，json.Marshal 也不得多出任何新键。
	h := SearchHit{
		ID: "o-1", Title: "t", Domain: "d", Tags: []string{}, MatchedFields: []string{},
		Opinion: &OpinionHitMeta{
			Validation: "validated",
			Relations:  OpinionRelationSummary{Supports: 3, Limits: 2, Opposing: 1},
		},
	}
	raw, err := json.Marshal(h)
	if err != nil {
		t.Fatalf("marshal：%v", err)
	}
	got := jsonObjectKeysList(string(raw))
	if !reflect.DeepEqual(got, SearchHitKeys()) {
		t.Fatalf("hits[] 键序 = %v，期望逐字等于 SearchHitKeys() = %v（原文 %s）",
			got, SearchHitKeys(), raw)
	}
	for _, leak := range []string{"validation", "supports", "limits", "opposing",
		"relations", "opinion", "Opinion"} {
		if strings.Contains(string(raw), "\""+leak+"\"") {
			t.Fatalf("eg search JSON 泄漏了观点专属键 %q：%s", leak, raw)
		}
	}
	// SearchDataKeys 同样不扩张（观点元数据经专用 DTO 承载，不进 search 的 data）。
	if !reflect.DeepEqual(SearchDataKeys(),
		[]string{"hits", "total", "scanned_files", "skipped_files"}) {
		t.Fatalf("SearchDataKeys 被改动：%v", SearchDataKeys())
	}
}

// —— ⑥ 关系摘要的固定次序 supports → limits → opposing（供 CLI 零分支渲染）——

func TestOpinionRelationSummaryOrderedIsFixed(t *testing.T) {
	got := OpinionRelationSummary{Supports: 7, Limits: 5, Opposing: 3}.Ordered()
	want := []OpinionRelationCount{
		{Type: "supports", Count: 7},
		{Type: "limits", Count: 5},
		{Type: "opposing", Count: 3},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Ordered() = %v，期望固定次序 supports,limits,opposing = %v", got, want)
	}
}

// jsonObjectKeysList 按**出现次序**取一个平坦 JSON 对象的键（本文件自持：与 query_test 包的
// jsonObjectKeys 不同包，不能跨包复用；SearchHit 是平坦对象，正则取键即可逐字反证键序）。
func jsonObjectKeysList(s string) []string {
	out := []string{}
	for _, m := range regexp.MustCompile(`"([a-z_]+)":`).FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}
