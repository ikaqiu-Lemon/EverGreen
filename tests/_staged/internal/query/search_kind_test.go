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

// skDiagCounts 按诊断 code 计数（用于「恰一条 W2x + 恰一条 Q5」这类逐码判据）。
// 返回 code → 出现次数；未出现的 code 取 map 零值 0，判据因此可直接比数。
func skDiagCounts(res *SearchResult) map[string]int {
	out := map[string]int{}
	for _, d := range res.Diagnostics {
		out[d.Code]++
	}
	return out
}

// skHas 报告结果集里是否含某个 ID（正交性判据里做成员存在性断言，避免依赖顺序）。
func skHas(res *SearchResult, id string) bool {
	for _, h := range res.Hits {
		if h.ID == id {
			return true
		}
	}
	return false
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
		name     string
		break_   func(*testing.T, string)
		wantCode string // 该降级原因**唯一**应产出的 W 码（missing→W23 / corrupt→W24 / stale→W22）
	}{
		{"missing", bkDropIndex, "W23"},
		{"corrupt", bkCorruptIndex, "W24"},
		{"stale", skStale, "W22"},
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
		// healthy 索引下：本 kind **恰无**任何降级诊断（合同 §6.3：健康索引不产 W22/W23/W24/Q5）。
		// 逐 kind 断言，避免「只测 knowledge 就宣称所有 kind 都干净」。
		if hc := skDiagCounts(healthy); hc["W22"]+hc["W23"]+hc["W24"]+hc[CodeQ5] != 0 {
			t.Fatalf("kind=%q healthy 不应出现降级诊断，实际 W22=%d W23=%d W24=%d Q5=%d（全部=%v）",
				kind, hc["W22"], hc["W23"], hc["W24"], hc[CodeQ5], healthy.Diagnostics)
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
			// —— 降级诊断逐码判据（本批补强：不再仅靠旧 default-search 测试间接宣称）——
			//
			// clean 语料（skVault 无坏卡、无悬空引用）下，每个 kind 在**扫描 fallback** 上
			// 都必须恰好留痕「一条原因码 + 一条 Q5」，且原因码与降级成因严格对应：
			//   missing→W23、corrupt→W24、stale→W22（合同 §6.3 / degrade.go 的成对留痕）。
			// 逐 kind × 逐成因断言，才能证明 opinion / all 也保留了同一套诊断、既不漏也不泄。
			dc := skDiagCounts(got)
			if dc[fb.wantCode] != 1 {
				t.Fatalf("kind=%q fallback=%s 期望恰一条 %s，实际 %d（全部诊断=%v）",
					kind, fb.name, fb.wantCode, dc[fb.wantCode], got.Diagnostics)
			}
			if dc[CodeQ5] != 1 {
				t.Fatalf("kind=%q fallback=%s 期望恰一条 Q5，实际 %d（全部诊断=%v）",
					kind, fb.name, dc[CodeQ5], got.Diagnostics)
			}
			for _, other := range []string{"W22", "W23", "W24"} {
				if other != fb.wantCode && dc[other] != 0 {
					t.Fatalf("kind=%q fallback=%s 多出无关原因码 %s（应仅 %s）：%v",
						kind, fb.name, other, fb.wantCode, got.Diagnostics)
				}
			}
			// clean 语料下诊断总数**恰 2**（W2x + Q5）：证明 kind 收窄没有顺带吞掉或伪造其它诊断。
			if len(got.Diagnostics) != 2 {
				t.Fatalf("kind=%q fallback=%s clean 语料降级诊断应恰 2 条(%s+Q5)，实际 %d 条：%v",
					kind, fb.name, fb.wantCode, len(got.Diagnostics), got.Diagnostics)
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

// —— ⑦ query 级正交性：kind 收窄与 validation / domain / tag / since / until /
//    include-deleted 相互正交，观点复用与知识卡**同一套** Filter / dropDeleted 口径 ——
//
// 本批补强：证明「收窄到观点」不会顺带引入任何隐式过滤（validation 三态都在），且五个
// 过滤维度对 kind=opinion 的取舍与知识卡逐字同口径。每条判据都先证 baseline 命中、再证
// 过滤后**确实**移除应移除的那条（非空断言）。

// skoOpinion 造一条最小合法观点，令 domain / updated_at / validation / tags / deleted_at
// 成为唯一变量：检索令牌 "orthtok" 一律落在 title（score 3、稳定命中），正文不含令牌，
// 因此命中与否只由过滤维度决定，排除「令牌命中面差异」这一混淆变量。
func skoOpinion(domain, id, title, updated, validation, tagsBlock, deletedAt string) (rel, body string) {
	fm := "---\nid: " + id + "\nstatus: active\ncreated_at: '2026-09-01'\nupdated_at: '" +
		updated + "'\ntitle: " + title + "\nvalidation: " + validation + "\nsources: []\n"
	if tagsBlock != "" {
		fm += "tags:\n" + tagsBlock
	}
	if deletedAt != "" {
		fm += "deleted_at: '" + deletedAt + "'\ndeleted_reason: 用例造的逻辑删除\n"
	}
	return "domains/" + domain + "/opinions/" + id + ".md", fm + "---\n\n## 观点\n\n占位主张，无检索令牌。\n"
}

// skoWrite 把一条 skoOpinion 写进 vault（复用 bkWrite；不建索引，读路径经 SelectBackend 走扫描）。
func skoWrite(t *testing.T, root, domain, id, title, updated, validation, tagsBlock, deletedAt string) {
	t.Helper()
	rel, body := skoOpinion(domain, id, title, updated, validation, tagsBlock, deletedAt)
	bkWrite(t, root, rel, body)
}

// skoReq 组装一次 kind=opinion 的正交性检索（令牌 orthtok；过滤维度由调用方填 req 其余字段）。
func skoReq() SearchRequest { return SearchRequest{Query: "orthtok", Kind: SearchKindOpinion} }

func TestSearchKindOpinionValidationIsOrthogonal(t *testing.T) {
	root := t.TempDir()
	// validation 三态齐备（提案与状态合同 §5：validation 是论证进度，与「是否可检索」正交）。
	skoWrite(t, root, "ai-infra", "o-20260910-vpend", "orthtok pending",
		"2026-09-10T10:00:00+08:00", "pending", "", "")
	skoWrite(t, root, "ai-infra", "o-20260909-vval", "orthtok validated",
		"2026-09-09T10:00:00+08:00", "validated", "", "")
	skoWrite(t, root, "ai-infra", "o-20260908-vrej", "orthtok rejected",
		"2026-09-08T10:00:00+08:00", "rejected", "", "")
	res, err := bkSearch(root, skoReq())
	if err != nil {
		t.Fatalf("opinion 检索：%v", err)
	}
	// 三态**都**必须召回：kind 收窄绝不把 validation 当隐式过滤器（否则 rejected/pending 会被吞）。
	for _, id := range []string{"o-20260910-vpend", "o-20260909-vval", "o-20260908-vrej"} {
		if !skHas(res, id) {
			t.Fatalf("validation 正交性：%s 未召回 —— validation 被当成隐式过滤器；实际 = %v", id, skIDs(res))
		}
	}
	if len(res.Hits) != 3 {
		t.Fatalf("期望恰 3 条(pending/validated/rejected)，实际 %d：%v", len(res.Hits), skIDs(res))
	}
}

func TestSearchKindOpinionFilterOrthogonality(t *testing.T) {
	// domain：--domain 对观点沿用 Filter 的 domain 口径（scan 面收窄 + Filter 复核）。
	t.Run("domain", func(t *testing.T) {
		root := t.TempDir()
		skoWrite(t, root, "alpha", "o-20260910-da", "orthtok a", "2026-09-10T10:00:00+08:00", "pending", "", "")
		skoWrite(t, root, "beta", "o-20260909-db", "orthtok b", "2026-09-09T10:00:00+08:00", "pending", "", "")
		base, err := bkSearch(root, skoReq())
		if err != nil {
			t.Fatalf("baseline：%v", err)
		}
		if !skHas(base, "o-20260910-da") || !skHas(base, "o-20260909-db") {
			t.Fatalf("domain baseline：两域观点都应命中，实际 %v", skIDs(base))
		}
		req := skoReq()
		req.Domain = "alpha"
		only, err := bkSearch(root, req)
		if err != nil {
			t.Fatalf("--domain alpha：%v", err)
		}
		if !skHas(only, "o-20260910-da") {
			t.Fatalf("--domain alpha 应保留 alpha 观点，实际 %v", skIDs(only))
		}
		if skHas(only, "o-20260909-db") {
			t.Fatalf("--domain alpha 未过滤掉 beta 观点，实际 %v", skIDs(only))
		}
		if len(only.Hits) != 1 {
			t.Fatalf("--domain alpha 期望恰 1 条，实际 %v", skIDs(only))
		}
	})
	// tag：--tag 对观点沿用 Filter 的 hasAllTags（多值 AND、逐字相等）。
	t.Run("tag", func(t *testing.T) {
		root := t.TempDir()
		skoWrite(t, root, "ai-infra", "o-20260910-tt", "orthtok tagged",
			"2026-09-10T10:00:00+08:00", "pending", "  - kept\n", "")
		skoWrite(t, root, "ai-infra", "o-20260909-tn", "orthtok notag",
			"2026-09-09T10:00:00+08:00", "pending", "", "")
		base, err := bkSearch(root, skoReq())
		if err != nil {
			t.Fatalf("baseline：%v", err)
		}
		if !skHas(base, "o-20260910-tt") || !skHas(base, "o-20260909-tn") {
			t.Fatalf("tag baseline：两条都应命中，实际 %v", skIDs(base))
		}
		req := skoReq()
		req.Tags = []string{"kept"}
		only, err := bkSearch(root, req)
		if err != nil {
			t.Fatalf("--tag kept：%v", err)
		}
		if !skHas(only, "o-20260910-tt") {
			t.Fatalf("--tag kept 应保留带标签观点，实际 %v", skIDs(only))
		}
		if skHas(only, "o-20260909-tn") {
			t.Fatalf("--tag kept 未过滤掉无标签观点，实际 %v", skIDs(only))
		}
		if len(only.Hits) != 1 {
			t.Fatalf("--tag kept 期望恰 1 条，实际 %v", skIDs(only))
		}
	})
	// since / until：对观点沿用 Filter 的 dateOf(updated_at) 闭区间口径。
	t.Run("since_until", func(t *testing.T) {
		root := t.TempDir()
		skoWrite(t, root, "ai-infra", "o-20260905-old", "orthtok old",
			"2026-09-05T10:00:00+08:00", "pending", "", "")
		skoWrite(t, root, "ai-infra", "o-20260915-new", "orthtok new",
			"2026-09-15T10:00:00+08:00", "pending", "", "")
		base, err := bkSearch(root, skoReq())
		if err != nil {
			t.Fatalf("baseline：%v", err)
		}
		if !skHas(base, "o-20260905-old") || !skHas(base, "o-20260915-new") {
			t.Fatalf("since/until baseline：两条都应命中，实际 %v", skIDs(base))
		}
		sinceReq := skoReq()
		sinceReq.Since = "2026-09-10"
		since, err := bkSearch(root, sinceReq)
		if err != nil {
			t.Fatalf("--since：%v", err)
		}
		if !skHas(since, "o-20260915-new") || skHas(since, "o-20260905-old") || len(since.Hits) != 1 {
			t.Fatalf("--since 2026-09-10 应只留 new(09-15)，实际 %v", skIDs(since))
		}
		untilReq := skoReq()
		untilReq.Until = "2026-09-10"
		until, err := bkSearch(root, untilReq)
		if err != nil {
			t.Fatalf("--until：%v", err)
		}
		if !skHas(until, "o-20260905-old") || skHas(until, "o-20260915-new") || len(until.Hits) != 1 {
			t.Fatalf("--until 2026-09-10 应只留 old(09-05)，实际 %v", skIDs(until))
		}
	})
	// include-deleted：对观点沿用 dropDeleted 口径（默认剔除，显式开关带回，且带回项标 Deleted）。
	t.Run("include_deleted", func(t *testing.T) {
		root := t.TempDir()
		skoWrite(t, root, "ai-infra", "o-20260910-live", "orthtok live",
			"2026-09-10T10:00:00+08:00", "pending", "", "")
		skoWrite(t, root, "ai-infra", "o-20260909-del", "orthtok dead",
			"2026-09-09T10:00:00+08:00", "pending", "", "2026-09-09T12:00:00+08:00")
		def, err := bkSearch(root, skoReq())
		if err != nil {
			t.Fatalf("default：%v", err)
		}
		if !skHas(def, "o-20260910-live") {
			t.Fatalf("默认视图应保留未删除观点，实际 %v", skIDs(def))
		}
		if skHas(def, "o-20260909-del") {
			t.Fatalf("默认视图未 dropDeleted 掉已删除观点，实际 %v", skIDs(def))
		}
		if len(def.Hits) != 1 {
			t.Fatalf("默认视图期望恰 1 条，实际 %v", skIDs(def))
		}
		req := skoReq()
		req.IncludeDeleted = true
		inc, err := bkSearch(root, req)
		if err != nil {
			t.Fatalf("--include-deleted：%v", err)
		}
		if !skHas(inc, "o-20260910-live") || !skHas(inc, "o-20260909-del") {
			t.Fatalf("--include-deleted 应把已删除观点带回，实际 %v", skIDs(inc))
		}
		// 带回的已删除观点必须标 Deleted（与知识卡同一 deleted 维度：DeletedFromStamp）。
		var sawDel bool
		for _, h := range inc.Hits {
			if h.ID == "o-20260909-del" {
				sawDel = true
				if !h.Deleted {
					t.Fatalf("已删除观点 %s 未标 Deleted（deleted 维度未沿用知识卡口径）", h.ID)
				}
			}
		}
		if !sawDel {
			t.Fatal("--include-deleted 未召回已删除观点，无法证明 deleted 维度口径")
		}
	})
}
