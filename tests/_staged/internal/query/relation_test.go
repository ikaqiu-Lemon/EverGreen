package query_test

// T-…-023 的 query 层机器判据：`eg rel` 读路径的正向 / 反向取数、排序次序、`--to` 过滤、
// 悬空引用 Q2、坏文件 Q1 透传、卡不存在，以及**只读零副作用**。
//
// 判据来源：M2 查询合同 `2026-09-19-m2-query-contract.md` §3.1（参数与 data 键表）、
// §3.2（正反向取数与 opposing 单向存储）、§3.3（两级排序）、§5（Q 系列）、§6（只读零副作用）。
//
// 测试名统一以 `RelQuery` 开头，与 T-…-023 的 verify.test（`-run RelQuery`）对齐。

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
)

// relCard 造一张带 relations[] 的卡（时间戳固定，排序只由 type + 对端 ID 决定）。
func relCard(id, title, domain, status string, relations string) string {
	return card(id, title, domain, status, "2026-09-01",
		"2026-09-12T10:00:00+08:00", "", relations)
}

// seedRelVault 造关系语料（跨领域）：
//
//	A（ai-infra）→ supports B、→ opposing C
//	C（ops）     → limits   B、→ derives  k-20260909-missing（悬空）
//	B（ai-infra）无正向关系，因此是反向查询的观察点（A 与 C 都指向它）。
func seedRelVault(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "domains/ai-infra/knowledge/k-20260901-a.md",
		relCard("k-20260901-a", "A 卡", "ai-infra", "active",
			"  - type: supports\n    target: k-20260902-b\n    reason: 支持 B\n"+
				"  - type: opposing\n    target: k-20260903-c\n    reason: 与 C 冲突\n"))
	writeFile(t, root, "domains/ai-infra/knowledge/k-20260902-b.md",
		relCard("k-20260902-b", "B 卡", "ai-infra", "active", ""))
	writeFile(t, root, "domains/ops/knowledge/k-20260903-c.md",
		relCard("k-20260903-c", "C 卡", "ops", "active",
			"  - type: limits\n    target: k-20260902-b\n    reason: 限定 B 的适用范围\n"+
				"  - type: derives\n    target: k-20260909-missing\n    reason: 指向不存在的卡\n"))
	return root
}

func mustRelView(t *testing.T, root, id string, to string) *query.RelResult {
	t.Helper()
	res, err := query.RelView(root, query.RelRequest{ID: model.RelationEndpoint(id), To: to})
	if err != nil {
		t.Fatalf("RelView(%s, to=%q)：%v", id, to, err)
	}
	return res
}

func edgeSig(edges []query.RelationEdge, peer func(query.RelationEdge) string) []string {
	out := []string{}
	for _, e := range edges {
		out = append(out, e.Type+"("+peer(e)+")")
	}
	return out
}

// TestRelQueryDataKeysMatchContract —— data 与条目的 JSON 键序与合同 §3.1 **逐字相同**。
func TestRelQueryDataKeysMatchContract(t *testing.T) {
	raw, err := json.Marshal(query.RelData{})
	if err != nil {
		t.Fatal(err)
	}
	if got := jsonObjectKeys(string(raw)); !reflect.DeepEqual(got, query.RelDataKeys()) {
		t.Fatalf("data 键序 = %v，合同 §3.1 = %v（原文 %s）", got, query.RelDataKeys(), raw)
	}
	rawEdge, err := json.Marshal(query.RelationEdge{})
	if err != nil {
		t.Fatal(err)
	}
	if got := jsonObjectKeys(string(rawEdge)); !reflect.DeepEqual(got, query.RelEdgeKeys()) {
		t.Fatalf("条目键序 = %v，合同 §3.1 = %v（原文 %s）", got, query.RelEdgeKeys(), rawEdge)
	}
}

// TestRelQueryForward —— 正向恰为本卡 relations[]，opposing 排在 supports 前（合同 §3.3），
// from 恒为本卡 ID，条目键恰五项。
func TestRelQueryForward(t *testing.T) {
	root := seedRelVault(t)
	res := mustRelView(t, root, "k-20260901-a", "")
	got := edgeSig(res.Data.RelationsOut, func(e query.RelationEdge) string { return e.Target })
	want := []string{"opposing(k-20260903-c)", "supports(k-20260902-b)"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("relations_out = %v，期望 %v（opposing 在前）", got, want)
	}
	for _, e := range res.Data.RelationsOut {
		if e.From != "k-20260901-a" || e.Path != "domains/ai-infra/knowledge/k-20260901-a.md" {
			t.Fatalf("正向条目 from / path 不符：%+v", e)
		}
		if e.Reason == "" {
			t.Fatalf("正向条目缺 reason：%+v", e)
		}
	}
	if len(res.Data.RelationsIn) != 0 {
		t.Fatalf("A 无人指向，relations_in 应为空，实际 %+v", res.Data.RelationsIn)
	}
}

// TestRelQueryReverseScan —— 反向走全库扫描（跨领域可见）：A→B（supports）、C→B（limits）时，
// `rel B` 的 relations_in 顺序为 limits(C) → supports(A)（合同 §3.3 type 固定次序）。
func TestRelQueryReverseScan(t *testing.T) {
	root := seedRelVault(t)
	res := mustRelView(t, root, "k-20260902-b", "")
	got := edgeSig(res.Data.RelationsIn, func(e query.RelationEdge) string { return e.From })
	want := []string{"limits(k-20260903-c)", "supports(k-20260901-a)"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("relations_in = %v，期望 %v（limits 在 supports 前）", got, want)
	}
	for _, e := range res.Data.RelationsIn {
		if e.Target != "k-20260902-b" {
			t.Fatalf("反向条目 target 应恒为观察点：%+v", e)
		}
	}
	if res.Data.RelationsIn[0].Path != "domains/ops/knowledge/k-20260903-c.md" {
		t.Fatalf("跨领域反向条目 path 不符：%+v", res.Data.RelationsIn[0])
	}
	if len(res.Data.RelationsOut) != 0 {
		t.Fatalf("B 无正向关系，relations_out 应为空，实际 %+v", res.Data.RelationsOut)
	}
}

// TestRelQueryOpposingStaysOneWay —— opposing 单向存储：只在 A 的正向与 C 的反向各出现一次，
// 读路径不补对称条目、不去重合并。
func TestRelQueryOpposingStaysOneWay(t *testing.T) {
	root := seedRelVault(t)
	c := mustRelView(t, root, "k-20260903-c", "")
	in := edgeSig(c.Data.RelationsIn, func(e query.RelationEdge) string { return e.From })
	if !reflect.DeepEqual(in, []string{"opposing(k-20260901-a)"}) {
		t.Fatalf("C 的 relations_in = %v，期望恰一条 opposing(A)", in)
	}
	for _, e := range c.Data.RelationsOut {
		if e.Type == "opposing" {
			t.Fatalf("读路径不得给 opposing 补出对称正向条目：%+v", e)
		}
	}
}

// TestRelQueryToFilter —— `--to` 正反向同时过滤；对端 ID 不存在时结果为空且多一条 Q2（合同 §3.1）。
func TestRelQueryToFilter(t *testing.T) {
	root := seedRelVault(t)
	res := mustRelView(t, root, "k-20260902-b", "k-20260901-a")
	if len(res.Data.RelationsOut) != 0 || len(res.Data.RelationsIn) != 1 {
		t.Fatalf("--to 过滤结果不符：out=%+v in=%+v", res.Data.RelationsOut, res.Data.RelationsIn)
	}
	if res.Data.RelationsIn[0].From != "k-20260901-a" {
		t.Fatalf("--to 过滤后留下的对端不对：%+v", res.Data.RelationsIn[0])
	}
	res = mustRelView(t, root, "k-20260901-a", "k-20260902-b")
	if len(res.Data.RelationsOut) != 1 || res.Data.RelationsOut[0].Type != "supports" {
		t.Fatalf("--to 正向过滤结果不符：%+v", res.Data.RelationsOut)
	}
	// 不存在的对端：空结果 + 一条点名该 ID 的 Q2。
	res = mustRelView(t, root, "k-20260901-a", "k-20260909-missing")
	if len(res.Data.RelationsOut) != 0 || len(res.Data.RelationsIn) != 0 {
		t.Fatalf("--to 指向不存在的卡时结果应为空：%+v / %+v",
			res.Data.RelationsOut, res.Data.RelationsIn)
	}
	hit := 0
	for _, d := range res.Diagnostics {
		if d.Code == query.CodeQ2 && strings.Contains(d.Message, "--to") &&
			strings.Contains(d.Message, "k-20260909-missing") {
			hit++
		}
	}
	if hit != 1 {
		t.Fatalf("--to 不存在应恰一条 Q2，实际诊断 %v", res.Diagnostics)
	}
}

// TestRelQueryDanglingQ2 —— 悬空引用：条目仍在 relations_out[] 里，进 MissingTargets，
// 并有一条点名该目标 ID 的 Q2（合同 §5.1），不静默丢弃、不升级为 error。
func TestRelQueryDanglingQ2(t *testing.T) {
	root := seedRelVault(t)
	res := mustRelView(t, root, "k-20260903-c", "")
	found := false
	for _, e := range res.Data.RelationsOut {
		if e.Target == "k-20260909-missing" {
			found = true
		}
	}
	if !found {
		t.Fatalf("悬空条目不得从 relations_out 消失：%+v", res.Data.RelationsOut)
	}
	if !reflect.DeepEqual(res.MissingTargets, []string{"k-20260909-missing"}) {
		t.Fatalf("MissingTargets = %v，期望 [k-20260909-missing]", res.MissingTargets)
	}
	q2 := 0
	for _, d := range res.Diagnostics {
		if d.Code == query.CodeQ2 && strings.Contains(d.Message, "k-20260909-missing") {
			q2++
			if d.Level != query.DiagLevel {
				t.Fatalf("Q2 必须是 warning，实际 %s", d.Level)
			}
		}
	}
	if q2 != 1 {
		t.Fatalf("Q2 条数 = %d，期望恰一条（%v）", q2, res.Diagnostics)
	}
}

// TestRelQueryUnparsableQ1 —— 坏 .md 由扫描层产出 Q1，本层**透传不吞**，
// 结果仍返回、计数守恒，且有一条 Q3 汇总说明结果不完整。
func TestRelQueryUnparsableQ1(t *testing.T) {
	root := seedRelVault(t)
	writeFile(t, root, "domains/ai-infra/knowledge/broken.md", "---\n- 1\n---\n\n# 坏卡\n")
	res := mustRelView(t, root, "k-20260902-b", "")
	if res.Data.SkippedFiles != 1 || res.Data.ScannedFiles != 4 {
		t.Fatalf("计数不符：scanned=%d skipped=%d（期望 4 / 1）",
			res.Data.ScannedFiles, res.Data.SkippedFiles)
	}
	q1 := false
	for _, d := range res.Diagnostics {
		if d.Code == query.CodeQ1 && strings.Contains(d.Path, "broken.md") {
			q1 = true
		}
	}
	if !q1 {
		t.Fatalf("坏文件必须记 Q1 并带路径，实际 %v", res.Diagnostics)
	}
	if countCode(res.Diagnostics, query.CodeQ3) != 1 {
		t.Fatalf("应有恰一条 Q3 汇总，实际 %v", diagCodes(res.Diagnostics))
	}
	if len(res.Data.RelationsIn) != 2 {
		t.Fatalf("一个坏文件不得丢掉其余结果：%+v", res.Data.RelationsIn)
	}
}

// TestRelQueryNotFoundAndInvalidID —— 端点不存在 / 端点形态非法（含 --to）返回可判定的哨兵
// 错误，CLI 据此退 1；错误路径零副作用。关系端点只认 k- / o-：s- / n- / r- / p- 及畸形一律拒绝。
func TestRelQueryNotFoundAndInvalidID(t *testing.T) {
	root := seedRelVault(t)
	before := treeSnapshot(t, root)
	if _, err := query.RelView(root, query.RelRequest{ID: "k-20260909-missing"}); !errors.Is(err, query.ErrEndpointNotFound) {
		t.Fatalf("端点不存在应返回 ErrEndpointNotFound，实际 %v", err)
	}
	// 形态非法：空串、畸形、以及 s- / n- / r- / p- 前缀（论证关系不指向原文 / 笔记 / 综述 / 提案）。
	for _, bad := range []string{"", "not-an-id", "s-20260901-x", "n-20260901-x", "r-20260901-x", "p-20260901-x"} {
		if _, err := query.RelView(root, query.RelRequest{ID: model.RelationEndpoint(bad)}); !errors.Is(err, query.ErrInvalidEndpoint) {
			t.Fatalf("ID %q 应返回 ErrInvalidEndpoint，实际 %v", bad, err)
		}
	}
	// --to 形态非法同样拒绝（同一 k/o 端点校验）：s- / n- / r- / p- 与畸形都不是合法关系端点。
	for _, badTo := range []string{"not-an-id", "s-20260901-x", "n-20260901-x", "r-20260901-x", "p-20260901-x"} {
		if _, err := query.RelView(root, query.RelRequest{ID: "k-20260901-a", To: badTo}); !errors.Is(err, query.ErrInvalidEndpoint) {
			t.Fatalf("--to=%q 形态非法应返回 ErrInvalidEndpoint，实际 %v", badTo, err)
		}
	}
	if after := treeSnapshot(t, root); after != before {
		t.Fatalf("错误路径改动了文件：\n%s\n%s", before, after)
	}
}

// TestRelQueryOrderIsInputIndependent —— 排序稳定确定：两次查询输出逐字相同，
// 且与文件系统枚举顺序无关（同类型多条按对端 ID 升序）。
func TestRelQueryOrderIsInputIndependent(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "domains/ai-infra/knowledge/k-20260901-a.md",
		relCard("k-20260901-a", "A 卡", "ai-infra", "active",
			"  - type: supports\n    target: k-20260905-e\n    reason: e\n"+
				"  - type: supports\n    target: k-20260904-d\n    reason: d\n"+
				"  - type: derives\n    target: k-20260906-f\n    reason: f\n"+
				"  - type: limits\n    target: k-20260907-g\n    reason: g\n"))
	first, err := json.Marshal(mustRelView(t, root, "k-20260901-a", "").Data)
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(mustRelView(t, root, "k-20260901-a", "").Data)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("两次输出不同：\n%s\n%s", first, second)
	}
	got := edgeSig(mustRelView(t, root, "k-20260901-a", "").Data.RelationsOut,
		func(e query.RelationEdge) string { return e.Target })
	want := []string{"limits(k-20260907-g)", "supports(k-20260904-d)",
		"supports(k-20260905-e)", "derives(k-20260906-f)"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("排序 = %v，期望 %v（type 固定次序 → 对端 ID 升序）", got, want)
	}
}

// TestRelQueryIsReadOnly —— 合同 §6：查询前后文件内容 hash 与 mtime 逐字不变。
func TestRelQueryIsReadOnly(t *testing.T) {
	root := seedRelVault(t)
	before := treeSnapshot(t, root)
	mustRelView(t, root, "k-20260901-a", "")
	mustRelView(t, root, "k-20260902-b", "k-20260901-a")
	if after := treeSnapshot(t, root); after != before {
		t.Fatalf("rel 查询改动了文件：\n%s\n%s", before, after)
	}
}
