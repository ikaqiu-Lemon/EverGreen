package query

// T-…-006-A 的 query 层机器判据：`eg search` 的 **kind 收窄**在扫描 / 索引两后端上
// 具有一致语义（设计 §5.2 检索拆分）。
//
// 合同（本批行为）：
//   - query 层定义封闭 kind 口径 knowledge|opinion|all；SearchRequest 零值兼容为 knowledge。
//   - 默认 / knowledge：两后端都**绝不**返回 o-*。
//   - opinion：两后端**只**返回 o-*；all：两类都返回。
//   - all 必须在**合并后的统一候选集**上复用同一 Filter / 四级 SortEntries / 删除过滤 /
//     ApplyPage —— 不得先按 kind 各自排序 / 分页再拼接（证据：交错全序 + 逐页拼接一致）。
//   - 索引 healthy 与 scan fallback（missing / corrupt / stale）在**每个 kind** 上给出
//     逐字相同的结果 ID / 顺序 / score / total / 分页拼接。
//
// 纪律：语料走**真实扫描 / 索引链路**（bkoBuildIndexWithOpinions 用产品唯一的 VaultScan +
// index.Build），不以 mock DTO 代替；本文件不 import 索引包（取数层位置锁：只有
// backend* / index_backed* / degrade* 前缀的文件可 import index，含测试文件）。
//
// 测试名统一含 `Search`，与 verify.test 的 `-run Search` 对齐。

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// —— 交错语料：可匹配的 k-* 与 o-* 各两条，令 `all` 的四级全序**交错** ——
//
// 单 ASCII 令牌 "zeta"（整词匹配，无 CJK 二元组倍增），命中面各自单一，score 干净：
//
//	k1 = k-20260904-alpha   title 命中           score 3   updated 09-04
//	o1 = o-20260902-gamma   title 命中           score 3   updated 09-02
//	o2 = o-20260905-delta   tags 命中            score 2   updated 09-05
//	k2 = k-20260903-beta    body 命中            score 1   updated 09-03
//
// 四级全序（score↓ → updated↓ → created↓ → id↑）：
//
//	knowledge → [k1, k2]        opinion → [o1, o2]        all → [k1, o1, o2, k2]
//
// `all` 的次序 k(3) o(3) o(2) k(1) 是**交错**的：分组拼接只可能得到 [k1,k2,o1,o2] 或
// [o1,o2,k1,k2]，两者都不等于 [k1,o1,o2,k2] —— 因此该序列本身就是「统一全序，非分组拼接」的判据。
const (
	skK1 = "k-20260904-alpha"
	skK2 = "k-20260903-beta"
	skO1 = "o-20260902-gamma"
	skO2 = "o-20260905-delta"
)

func skKnowledge(id, title, updated, tags, body string) string {
	fm := "---\nid: " + id + "\nstatus: active\ncreated_at: '2026-09-01'\nupdated_at: '" +
		updated + "'\ntitle: " + title + "\nsources: []\n"
	if tags != "" {
		fm += "tags:\n" + tags
	}
	return fm + "---\n\n## 知识内容\n\n" + body + "\n"
}

func skOpinion(id, title, updated, validation, tags, body string) string {
	fm := "---\nid: " + id + "\nstatus: active\ncreated_at: '2026-09-01'\nupdated_at: '" +
		updated + "'\ntitle: " + title + "\nvalidation: " + validation + "\nsources: []\n"
	if tags != "" {
		fm += "tags:\n" + tags
	}
	return fm + "---\n\n## 观点\n\n" + body + "\n"
}

// skVault 造交错语料（复用 backend_test.go 的 bkWrite；不建索引）。
func skVault(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	k := "domains/ai-infra/knowledge/"
	o := "domains/ai-infra/opinions/"
	bkWrite(t, root, k+skK1+".md", skKnowledge(skK1, "zeta alpha",
		"2026-09-04T10:00:00+08:00", "", "占位正文，无令牌。"))
	bkWrite(t, root, k+skK2+".md", skKnowledge(skK2, "beta card",
		"2026-09-03T10:00:00+08:00", "", "正文里提到 zeta 一次。"))
	bkWrite(t, root, o+skO1+".md", skOpinion(skO1, "zeta gamma",
		"2026-09-02T10:00:00+08:00", "validated", "", "占位主张，无令牌。"))
	bkWrite(t, root, o+skO2+".md", skOpinion(skO2, "delta doc",
		"2026-09-05T10:00:00+08:00", "pending", "  - zeta\n", "占位主张，无令牌。"))
	return root
}

func skIDs(res *SearchResult) []string {
	out := []string{}
	for _, h := range res.Hits {
		out = append(out, h.ID)
	}
	return out
}

func skScores(res *SearchResult) map[string]int {
	out := map[string]int{}
	for _, h := range res.Hits {
		out[h.ID] = h.Score
	}
	return out
}

// skKindReq 组装一次 kind 检索请求（Index 口径注入交由 bkSearch）。
func skKindReq(kind SearchKind) SearchRequest {
	return SearchRequest{Query: "zeta", Kind: kind}
}

// —— ① query 合同：默认 / knowledge / opinion / all 的成员与顺序 ——

func TestSearchKindDefaultAndKnowledgeExcludeOpinions(t *testing.T) {
	root := skVault(t)
	// 零值（默认）与显式 knowledge 必须语义一致，且都不含 o-*。
	for _, kind := range []SearchKind{"", SearchKindKnowledge} {
		res, err := bkSearch(root, skKindReq(kind))
		if err != nil {
			t.Fatalf("kind=%q：%v", kind, err)
		}
		if got := skIDs(res); !reflect.DeepEqual(got, []string{skK1, skK2}) {
			t.Fatalf("kind=%q 命中 = %v，期望 [%s %s]", kind, got, skK1, skK2)
		}
		for _, id := range skIDs(res) {
			if strings.HasPrefix(id, "o-") {
				t.Fatalf("kind=%q 泄漏观点 %s", kind, id)
			}
		}
	}
}

func TestSearchKindOpinionOnlyReturnsOpinions(t *testing.T) {
	root := skVault(t)
	res, err := bkSearch(root, skKindReq(SearchKindOpinion))
	if err != nil {
		t.Fatalf("opinion：%v", err)
	}
	if got := skIDs(res); !reflect.DeepEqual(got, []string{skO1, skO2}) {
		t.Fatalf("--kind opinion 命中 = %v，期望 [%s %s]", got, skO1, skO2)
	}
	for _, id := range skIDs(res) {
		if !strings.HasPrefix(id, "o-") {
			t.Fatalf("--kind opinion 返回了非观点 %s", id)
		}
	}
}

func TestSearchKindAllIsUnifiedTotalOrderNotGroupConcat(t *testing.T) {
	root := skVault(t)
	res, err := bkSearch(root, skKindReq(SearchKindAll))
	if err != nil {
		t.Fatalf("all：%v", err)
	}
	// 交错全序，非分组拼接。
	want := []string{skK1, skO1, skO2, skK2}
	if got := skIDs(res); !reflect.DeepEqual(got, want) {
		t.Fatalf("--kind all 次序 = %v，期望统一四级全序 %v（分组拼接会得到 [k,k,o,o] 或 [o,o,k,k]）",
			got, want)
	}
	if res.Total != 4 {
		t.Fatalf("--kind all total = %d，期望 4", res.Total)
	}
	// score 复用同一套 Filter：与知识卡同口径打分。
	wantScore := map[string]int{skK1: 3, skO1: 3, skO2: 2, skK2: 1}
	if got := skScores(res); !reflect.DeepEqual(got, wantScore) {
		t.Fatalf("--kind all score = %v，期望 %v", got, wantScore)
	}
}

// —— ② 零值兼容：SearchRequest{} 的 Kind 空值 == knowledge ——

func TestSearchKindZeroValueCompatIsKnowledge(t *testing.T) {
	root := skVault(t)
	zero, err := bkSearch(root, SearchRequest{Query: "zeta"}) // Kind 未设 = 零值
	if err != nil {
		t.Fatalf("zero：%v", err)
	}
	known, err := bkSearch(root, skKindReq(SearchKindKnowledge))
	if err != nil {
		t.Fatalf("knowledge：%v", err)
	}
	if !reflect.DeepEqual(skIDs(zero), skIDs(known)) {
		t.Fatalf("零值 kind 与 knowledge 结果不一致：%v vs %v", skIDs(zero), skIDs(known))
	}
	if !reflect.DeepEqual(skIDs(zero), []string{skK1, skK2}) {
		t.Fatalf("零值 kind 结果 = %v，期望 knowledge 口径 [%s %s]", skIDs(zero), skK1, skK2)
	}
}

// —— ③ 非法 kind / 严格大小写空白口径 ——

func TestSearchKindInvalidRejected(t *testing.T) {
	root := skVault(t)
	// 严格逐字：仅 knowledge|opinion|all 合法；任何大小写 / 空白 / 复数变体一律非法。
	for _, bad := range []SearchKind{"Knowledge", "KNOWLEDGE", " knowledge", "knowledge ",
		"all ", " all", "ALL", "Opinion", "opinions", "know", "k", " "} {
		if _, err := bkSearch(root, skKindReq(bad)); err == nil {
			t.Errorf("kind=%q 应报参数非法", bad)
		} else if !errors.Is(err, ErrInvalidQuery) {
			t.Errorf("kind=%q 的错误未归入 ErrInvalidQuery：%v", bad, err)
		} else if !strings.Contains(err.Error(), SearchKindList()) {
			t.Errorf("kind=%q 的错误文案未列出封闭值 %q：%v", bad, SearchKindList(), err)
		}
	}
	// 封闭集合口径钉死：恰 knowledge|opinion|all，次序固定。
	if SearchKindList() != "knowledge|opinion|all" {
		t.Fatalf("封闭 kind 集合 = %q，期望 knowledge|opinion|all", SearchKindList())
	}
}

// —— ④ 两后端等价：healthy 索引 vs 强制 fallback（missing / corrupt / stale）——

// skStale 真改一张本语料的知识卡字节并把 mtime 推后（index_stale → W22）。
// 不复用 bkMakeStale（它钉死在 bkVault 的路径上，本语料没有那张卡）：这里就地对
// skVault 的 k1 做同样的「改字节 + 推 mtime」，令水位线快路径失效后回权威重算判陈旧。
func skStale(t *testing.T, root string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash("domains/ai-infra/knowledge/"+skK1+".md"))
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读卡失败：%v", err)
	}
	// 追加的正文不含检索令牌 zeta：陈旧只改新鲜度，不改 k1 的命中面与四级全序位次。
	if err := os.WriteFile(p, append(raw, []byte("\n补一段正文：门控占位。\n")...), 0o644); err != nil {
		t.Fatalf("改卡失败：%v", err)
	}
	later := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(p, later, later); err != nil {
		t.Fatalf("改 mtime 失败：%v", err)
	}
}

func TestSearchKindBackendEquivalence(t *testing.T) {
	kinds := []SearchKind{"", SearchKindKnowledge, SearchKindOpinion, SearchKindAll}
	fallbacks := []struct {
		name   string
		break_ func(*testing.T, string)
	}{
		{"missing", bkDropIndex},
		{"corrupt", bkCorruptIndex},
		{"stale", skStale},
	}
	for _, kind := range kinds {
		// 权威（healthy 索引）结果：本 kind 的唯一真值。
		healthyRoot := skVault(t)
		bkoBuildIndexWithOpinions(t, healthyRoot)
		healthy, err := bkSearch(healthyRoot, skKindReq(kind))
		if err != nil {
			t.Fatalf("kind=%q healthy：%v", kind, err)
		}
		if healthy.backend.Kind != BackendIndex {
			t.Fatalf("kind=%q 前置：healthy 必须走索引后端，实际 %s", kind, healthy.backend.Kind)
		}
		for _, fb := range fallbacks {
			root := skVault(t)
			bkoBuildIndexWithOpinions(t, root)
			fb.break_(t, root)
			got, err := bkSearch(root, skKindReq(kind))
			if err != nil {
				t.Fatalf("kind=%q fallback=%s：%v", kind, fb.name, err)
			}
			if got.backend.Kind != BackendScan {
				t.Fatalf("kind=%q fallback=%s 前置：必须降级为扫描后端，实际 %s",
					kind, fb.name, got.backend.Kind)
			}
			// ID / 顺序逐字一致。
			if !reflect.DeepEqual(skIDs(got), skIDs(healthy)) {
				t.Fatalf("kind=%q fallback=%s 结果 ID/顺序分叉：scan=%v vs index=%v",
					kind, fb.name, skIDs(got), skIDs(healthy))
			}
			// score / total 逐字一致。
			if !reflect.DeepEqual(skScores(got), skScores(healthy)) {
				t.Fatalf("kind=%q fallback=%s score 分叉：scan=%v vs index=%v",
					kind, fb.name, skScores(got), skScores(healthy))
			}
			if got.Total != healthy.Total {
				t.Fatalf("kind=%q fallback=%s total 分叉：scan=%d vs index=%d",
					kind, fb.name, got.Total, healthy.Total)
			}
			// scanned/skipped 同口径。
			if got.ScannedFiles != healthy.ScannedFiles || got.SkippedFiles != healthy.SkippedFiles {
				t.Fatalf("kind=%q fallback=%s scanned/skipped 分叉：scan=%d/%d vs index=%d/%d",
					kind, fb.name, got.ScannedFiles, got.SkippedFiles,
					healthy.ScannedFiles, healthy.SkippedFiles)
			}
		}
	}
}

// —— ⑤ all 的分页是「统一全序后切片」，逐页拼接无重无漏，两后端一致 ——

func TestSearchKindAllPaginationIsPostSortSlice(t *testing.T) {
	build := func(t *testing.T, degrade func(*testing.T, string)) string {
		root := skVault(t)
		bkoBuildIndexWithOpinions(t, root)
		if degrade != nil {
			degrade(t, root)
		}
		return root
	}
	for _, tc := range []struct {
		name    string
		degrade func(*testing.T, string)
		wantBk  string
	}{
		{"index", nil, BackendIndex},
		{"scan", bkDropIndex, BackendScan},
	} {
		root := build(t, tc.degrade)
		full, err := bkSearch(root, skKindReq(SearchKindAll))
		if err != nil {
			t.Fatalf("%s full：%v", tc.name, err)
		}
		if full.backend.Kind != tc.wantBk {
			t.Fatalf("%s 前置后端 = %s，期望 %s", tc.name, full.backend.Kind, tc.wantBk)
		}
		var paged []string
		for off := 0; off < full.Total; off += 2 {
			req := skKindReq(SearchKindAll)
			req.Page = PageSpec{Limit: 2, Offset: off}
			p, err := bkSearch(root, req)
			if err != nil {
				t.Fatalf("%s page off=%d：%v", tc.name, off, err)
			}
			if p.Total != full.Total {
				t.Fatalf("%s page off=%d total = %d，期望恒 %d（total 是分页前总数）",
					tc.name, off, p.Total, full.Total)
			}
			paged = append(paged, skIDs(p)...)
		}
		if !reflect.DeepEqual(paged, skIDs(full)) {
			t.Fatalf("%s 逐页拼接 = %v，期望 = 全量四级全序 %v（证明先全序再分页，非分组拼接）",
				tc.name, paged, skIDs(full))
		}
	}
}

// —— ⑥ 负控：证明「移除 opinion 候选纳入」或「移除 fallback 的默认收窄」会红 ——
//
// 这两条断言就是负控本体：
//   - opinion / all 必须实际召回 o-*（若实现不把 opinion 候选并入统一集合，此处红）；
//   - 默认 / knowledge 在**扫描 fallback** 下也绝不含 o-*（若 fallback 漏掉 kind 收窄，此处红）。

func TestSearchKindNegativeControls(t *testing.T) {
	// 负控 A：opinion 候选确实被纳入（两后端）。
	for _, tc := range []struct {
		name    string
		degrade func(*testing.T, string)
	}{{"index", nil}, {"scan", bkDropIndex}} {
		root := skVault(t)
		bkoBuildIndexWithOpinions(t, root)
		if tc.degrade != nil {
			tc.degrade(t, root)
		}
		res, err := bkSearch(root, skKindReq(SearchKindOpinion))
		if err != nil {
			t.Fatalf("%s opinion：%v", tc.name, err)
		}
		if len(res.Hits) == 0 {
			t.Fatalf("%s 负控 A：opinion 检索零命中 —— opinion 候选未被纳入统一集合", tc.name)
		}
		for _, id := range skIDs(res) {
			if !strings.HasPrefix(id, "o-") {
				t.Fatalf("%s 负控 A：opinion 检索混入非观点 %s", tc.name, id)
			}
		}
	}
	// 负控 B：默认收窄在扫描 fallback 下不漏（o-* 绝不泄漏）。
	root := skVault(t)
	bkoBuildIndexWithOpinions(t, root)
	bkCorruptIndex(t, root)
	res, err := bkSearch(root, skKindReq(SearchKindKnowledge))
	if err != nil {
		t.Fatalf("fallback knowledge：%v", err)
	}
	if res.backend.Kind != BackendScan {
		t.Fatalf("负控 B 前置：需扫描 fallback，实际 %s", res.backend.Kind)
	}
	if len(res.Hits) == 0 {
		t.Fatal("负控 B 前置：knowledge 检索应有命中，否则「不泄漏」是空断言")
	}
	for _, id := range skIDs(res) {
		if strings.HasPrefix(id, "o-") {
			t.Fatalf("负控 B：扫描 fallback 下 knowledge 检索泄漏观点 %s", id)
		}
	}
}
