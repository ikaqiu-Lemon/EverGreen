package query_test

// T-…-022 的 query 层机器判据：`eg card show` 的固定分区键序、sources[]、正向 / 反向关系、
// 显著标记、重复 ID 与悬空引用诊断、卡不存在，以及**只读零副作用**。
//
// 判据来源：M2 查询合同 `2026-09-19-m2-query-contract.md` §2.1（定位与退出口径）、
// §2.2（data 键表 / 固定分区键序 / markers）、§3.2（正反向取数）、§3.3（两级排序）、
// §5（Q 系列）、§6（只读零副作用）。
//
// 测试名统一含 `CardShow`，与 T-…-022 的 verify.test（`-run CardShow`）对齐。

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
)

// cardShowVault 造一个语料：A ↔ B 互指（A 两条正向：opposing / supports）、
// 一张失效卡、A 另有一条指向不存在卡的悬空引用、B 只写了「知识内容」一个分区。
func cardShowVault(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "domains/ai-infra/knowledge/k-20260901-a.md",
		"---\nid: k-20260901-a\nstatus: active\ncreated_at: '2026-09-01'\n"+
			"updated_at: '2026-09-12T10:00:00+08:00'\ntitle: 注意力机制的计算代价\n"+
			"tags:\n  - attention\nsources:\n"+
			"  - source: s-20260901-x\n    note: n-20260901-x\n    rel: support\n"+
			"    reason: 实测数据支持该结论\n"+
			"relations:\n"+
			"  - type: supports\n    target: k-20260902-b\n    reason: 支持 B\n"+
			"  - type: opposing\n    target: k-20260903-c\n    reason: 与 C 冲突\n"+
			"  - type: limits\n    target: k-20260909-none\n    reason: 指向不存在的卡\n"+
			"---\n\n## 知识内容\n\n结论正文。\n\n## 解释与依据\n\n依据正文。\n\n"+
			"## 条件与边界\n\n边界正文。\n\n## 用户补充\n\n用户写的。\n\n## 理解自检\n\n自检问题。\n")
	writeFile(t, root, "domains/ai-infra/knowledge/k-20260902-b.md",
		"---\nid: k-20260902-b\nstatus: active\ncreated_at: '2026-09-02'\n"+
			"updated_at: '2026-09-02T10:00:00+08:00'\ntitle: 被指向的卡\nsources: []\n"+
			"relations:\n  - type: derives\n    target: k-20260901-a\n    reason: 由 A 推出\n"+
			"---\n\n## 知识内容\n\n只写了这一个分区。\n")
	writeFile(t, root, "domains/ai-infra/knowledge/k-20260903-c.md",
		card("k-20260903-c", "失效的卡", "ai-infra", "deprecated",
			"2026-09-03", "2026-09-03T10:00:00+08:00", "", ""))
	return root
}

func mustShowCard(t *testing.T, root, id string) *query.CardShowResult {
	t.Helper()
	res, err := query.ShowCard(root, model.CardID(id))
	if err != nil {
		t.Fatalf("ShowCard(%s)：%v", id, err)
	}
	return res
}

// TestCardShowDataKeysMatchContract —— CardDetail 的 JSON 键序与合同 §2.2 键表**逐字相同**。
func TestCardShowDataKeysMatchContract(t *testing.T) {
	raw, err := json.Marshal(query.CardDetail{})
	if err != nil {
		t.Fatal(err)
	}
	got := jsonObjectKeys(string(raw))
	if !reflect.DeepEqual(got, query.CardDataKeys()) {
		t.Fatalf("data 键序 = %v，合同 §2.2 = %v（原文 %s）", got, query.CardDataKeys(), raw)
	}
}

// TestCardShowSectionsKeyOrderFixed —— sections 键序恒为 Knowledge 固定分区声明序（F5），
// 且「分区齐全」与「只写了知识内容」两张卡的键集合与键序**完全相同**（缺分区值为空串）。
//
// **Schema v2 · T-…-003 重钉（事实变了，判据形态不变）**：契约 D-7 把 Knowledge 从五分区
// 收敛为三分区（`知识内容` / `条件与边界` / `用户补充`），`解释与依据` / `理解自检` 不再是
// 固定分区。判据形态一格未动：仍是「键序 == 固定分区声明序」「缺分区键仍在、值为空串」
// 「两张卡键集合完全相同」，只是「五」变成「Keys() 的长度」，并额外钉住
// **被移除的两个 v1 分区不再出现在键表里**（否则等于模板没真切换）。
// 存量文件里这两段正文在 v2 的 `card show` 里不再可见——这是模板切换的既知读路径影响，
// 已登记为 I-…-007，由 T-…-006（读路径与 opinion 命令）收口，不在本 task 放宽。
func TestCardShowSectionsKeyOrderFixed(t *testing.T) {
	root := cardShowVault(t)
	full := mustShowCard(t, root, "k-20260901-a").Card.Sections
	partial := mustShowCard(t, root, "k-20260902-b").Card.Sections

	want := mdfile.CardSections()
	for _, s := range []query.Sections{full, partial} {
		raw, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		at := 0
		for _, name := range want {
			i := strings.Index(string(raw)[at:], `"`+name+`":`)
			if i < 0 {
				t.Fatalf("sections 缺分区 %s 或键序不是固定分区声明序：%s", name, raw)
			}
			at += i
			got = append(got, name)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("sections 键序 = %v，期望 %v", got, want)
		}
		if !reflect.DeepEqual(s.Keys(), want) {
			t.Fatalf("Keys() = %v，期望 %v", s.Keys(), want)
		}
	}
	if partial.Get(mdfile.SecKnowledge) == "" {
		t.Fatal("「知识内容」应有正文")
	}
	for _, name := range want[1:] {
		if partial.Get(name) != "" || !partial.Missing(name) {
			t.Fatalf("缺分区 %s 应为空串并判定为缺失，实际 %q", name, partial.Get(name))
		}
	}
	// 三分区齐全的卡：每个键都取得到正文、都不判缺失。
	for _, name := range want {
		if full.Get(name) == "" || full.Missing(name) {
			t.Fatalf("分区齐全的卡不应判定 %s 缺失，实际 %q", name, full.Get(name))
		}
	}
	// 被 D-7 移除的两个 v1 分区不得再出现在键表里（模板确实切换了的正面证据）。
	for _, legacy := range []string{mdfile.SecRationale, mdfile.SecSelfCheck} {
		for _, name := range want {
			if name == legacy {
				t.Fatalf("v2 固定分区不应再含 %s：%v", legacy, want)
			}
		}
	}
}

// TestCardShowSourcesPassThrough —— sources[] 四要素原样透出；无材料时是空数组不是 null。
func TestCardShowSourcesPassThrough(t *testing.T) {
	root := cardShowVault(t)
	a := mustShowCard(t, root, "k-20260901-a").Card
	if len(a.Sources) != 1 {
		t.Fatalf("sources 条数 = %d，期望 1", len(a.Sources))
	}
	s := a.Sources[0]
	if s.Source != "s-20260901-x" || s.Note != "n-20260901-x" ||
		string(s.Rel) != "support" || s.Reason != "实测数据支持该结论" {
		t.Fatalf("sources[0] 未原样透出：%+v", s)
	}
	b := mustShowCard(t, root, "k-20260902-b").Card
	raw, err := json.Marshal(b.Sources)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "[]" {
		t.Fatalf("无材料时 sources = %s，期望 []", raw)
	}
}

// TestCardShowRelationsOutSorted —— 正向关系恰为本卡 frontmatter relations[]，
// 按合同 §3.3 的 type 固定次序（opposing → limits → supports → derives）+ 对端 ID 升序。
//
// 【T-…-061 重钉（owner 裁决② A-38/A-39）】原断言期望默认视图含 opposing→k-20260903-c（C 失效）。
// 新合同：deprecated 对端**默认隐藏**（合同 §5.1 真值表「作为关系端点默认展示 🔴」），
// 故默认视图只剩 limits→none + supports→B；显式 --include-deprecated（IncludeDeprecated=true）后
// 三条全展示（次序不变）。用例名与 type 固定次序判据保留，只改被裁决推翻的那一格断言。
func TestCardShowRelationsOutSorted(t *testing.T) {
	root := cardShowVault(t)
	// 默认视图：opposing→C 因 C 为 deprecated 对端被隐藏（A-38/A-39 落地前该条可见）。
	out := mustShowCard(t, root, "k-20260901-a").Card.RelationsOut
	var got []string
	for _, e := range out {
		if e.From != "k-20260901-a" {
			t.Fatalf("正向关系 from = %s，应恒为本卡 ID", e.From)
		}
		got = append(got, e.Type+"→"+e.Target)
	}
	wantDefault := []string{"limits→k-20260909-none", "supports→k-20260902-b"}
	if !reflect.DeepEqual(got, wantDefault) {
		t.Fatalf("默认视图 relations_out = %v，期望 %v（deprecated 对端 C 默认隐藏，§5.1）", got, wantDefault)
	}
	// 显式放开 deprecated：三条全展示，type 固定次序（opposing 在前）不变。
	res, err := query.ShowCard(root, model.CardID("k-20260901-a"),
		query.VisibilityPolicy{IncludeDeprecated: true})
	if err != nil {
		t.Fatalf("ShowCard(include-deprecated)：%v", err)
	}
	var all []string
	for _, e := range res.Card.RelationsOut {
		all = append(all, e.Type+"→"+e.Target)
	}
	wantAll := []string{"opposing→k-20260903-c", "limits→k-20260909-none", "supports→k-20260902-b"}
	if !reflect.DeepEqual(all, wantAll) {
		t.Fatalf("--include-deprecated relations_out = %v，期望 %v（合同 §3.3 固定次序）", all, wantAll)
	}
}

// TestCardShowRelationsInFromFullScan —— 反向关系来自全库扫描：B 卡上看得见 A 指过来的一条，
// C 卡（他人 opposing 的对端）同样只在 relations_in[] 出现（读路径不补对称条目）。
func TestCardShowRelationsInFromFullScan(t *testing.T) {
	root := cardShowVault(t)
	b := mustShowCard(t, root, "k-20260902-b").Card
	if len(b.RelationsIn) != 1 {
		t.Fatalf("B 的 relations_in 条数 = %d，期望 1（%+v）", len(b.RelationsIn), b.RelationsIn)
	}
	in := b.RelationsIn[0]
	if in.From != "k-20260901-a" || in.Type != "supports" || in.Target != "k-20260902-b" ||
		in.Path != "domains/ai-infra/knowledge/k-20260901-a.md" {
		t.Fatalf("反向条目不符合合同 §3.2：%+v", in)
	}
	c := mustShowCard(t, root, "k-20260903-c").Card
	if len(c.RelationsOut) != 0 {
		t.Fatalf("opposing 单向存储：对端不得在 relations_out 补出对称条目，实际 %+v", c.RelationsOut)
	}
	if len(c.RelationsIn) != 1 || c.RelationsIn[0].Type != "opposing" {
		t.Fatalf("C 的 relations_in = %+v，期望恰一条 opposing", c.RelationsIn)
	}
}

// TestCardShowDeprecatedMarkers —— 失效卡 deprecated == true 且 markers == ["[失效]"]；
// 未失效卡 markers 是空数组（不是 null），M2 不出现其它标记。
func TestCardShowDeprecatedMarkers(t *testing.T) {
	root := cardShowVault(t)
	dep := mustShowCard(t, root, "k-20260903-c").Card
	if !dep.Deprecated || dep.Status != "deprecated" {
		t.Fatalf("失效卡的 status / deprecated = %q / %t", dep.Status, dep.Deprecated)
	}
	if !reflect.DeepEqual(dep.Markers, []string{query.MarkerDeprecated}) {
		t.Fatalf("markers = %v，期望 [%s]", dep.Markers, query.MarkerDeprecated)
	}
	active := mustShowCard(t, root, "k-20260901-a").Card
	raw, err := json.Marshal(active.Markers)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "[]" {
		t.Fatalf("未失效卡 markers = %s，期望 []", raw)
	}
}

// TestCardShowDanglingRelationQ2 —— 悬空引用：条目仍在 relations_out[] 里，
// 同时进 MissingTargets 与一条 Q2 诊断（合同 §5.1），不静默隐藏。
func TestCardShowDanglingRelationQ2(t *testing.T) {
	root := cardShowVault(t)
	res := mustShowCard(t, root, "k-20260901-a")
	found := false
	for _, e := range res.Card.RelationsOut {
		if e.Target == "k-20260909-none" {
			found = true
		}
	}
	if !found {
		t.Fatalf("悬空条目不得从 relations_out 中消失：%+v", res.Card.RelationsOut)
	}
	if !reflect.DeepEqual(res.MissingTargets, []string{"k-20260909-none"}) {
		t.Fatalf("MissingTargets = %v，期望 [k-20260909-none]", res.MissingTargets)
	}
	q2 := 0
	for _, d := range res.Diagnostics {
		if d.Code == query.CodeQ2 && strings.Contains(d.Message, "k-20260909-none") {
			q2++
		}
	}
	if q2 != 1 {
		t.Fatalf("Q2 条数 = %d，期望恰一条（诊断：%v）", q2, res.Diagnostics)
	}
}

// TestCardShowDuplicateIDTakesSmallestPathWithQ1 —— 同 ID 出现在两处：取路径字典序最小者，
// 并有一条 Q1 如实说明重复（合同 §2.1，不静默择一）。
func TestCardShowDuplicateIDTakesSmallestPathWithQ1(t *testing.T) {
	root := t.TempDir()
	dup := card("k-20260905-d", "重复卡", "ai-infra", "active",
		"2026-09-05", "2026-09-05T10:00:00+08:00", "", "")
	writeFile(t, root, "domains/ai-infra/knowledge/b-later.md", dup)
	writeFile(t, root, "domains/ai-infra/knowledge/a-first.md", dup)
	res := mustShowCard(t, root, "k-20260905-d")
	if res.Card.Path != "domains/ai-infra/knowledge/a-first.md" {
		t.Fatalf("重复 ID 应取路径字典序最小者，实际 %s", res.Card.Path)
	}
	if countCode(res.Diagnostics, query.CodeQ1) != 1 {
		t.Fatalf("重复 ID 应恰一条 Q1，实际诊断 %v", diagCodes(res.Diagnostics))
	}
}

// TestCardShowNotFoundAndInvalidID —— 卡不存在 / ID 形态非法各自返回可判定的哨兵错误
// （CLI 据此退 1）；错误路径同样零副作用。
func TestCardShowNotFoundAndInvalidID(t *testing.T) {
	root := cardShowVault(t)
	before := treeSnapshot(t, root)
	if _, err := query.ShowCard(root, model.CardID("k-20260909-none")); !errors.Is(err, query.ErrCardNotFound) {
		t.Fatalf("卡不存在应返回 ErrCardNotFound，实际 %v", err)
	}
	for _, bad := range []string{"", "s-20260901-x", "k-2026-09-01-x", "not-an-id"} {
		if _, err := query.ShowCard(root, model.CardID(bad)); !errors.Is(err, query.ErrInvalidCardID) {
			t.Fatalf("ID %q 应返回 ErrInvalidCardID，实际 %v", bad, err)
		}
	}
	if after := treeSnapshot(t, root); after != before {
		t.Fatalf("错误路径改动了文件：\n%s\n%s", before, after)
	}
}

// TestCardShowUnparsableFileEmitsQ1AndKeepsResult —— 库里有坏 .md 时：目标卡照常展示、
// 坏文件记 Q1 并计入 SkippedFiles、计数守恒（合同 §5.3），Q3 汇总恰一条。
func TestCardShowUnparsableFileEmitsQ1AndKeepsResult(t *testing.T) {
	root := cardShowVault(t)
	writeFile(t, root, "domains/ai-infra/knowledge/broken.md", "---\n- 1\n---\n\n# 坏卡\n")
	res := mustShowCard(t, root, "k-20260901-a")
	if res.SkippedFiles != 1 {
		t.Fatalf("skipped_files = %d，期望 1", res.SkippedFiles)
	}
	if countCode(res.Diagnostics, query.CodeQ1) < 1 {
		t.Fatalf("坏文件必须记 Q1，实际 %v", diagCodes(res.Diagnostics))
	}
	if countCode(res.Diagnostics, query.CodeQ3) != 1 {
		t.Fatalf("存在 Q1/Q2 时 Q3 汇总恰一条，实际 %v", diagCodes(res.Diagnostics))
	}
	if res.ScannedFiles != 4 {
		t.Fatalf("scanned_files = %d，期望 4（3 张卡 + 1 个坏文件）", res.ScannedFiles)
	}
}

// TestCardShowIsDeterministic —— 同一语料两次调用输出逐字相同（含关系次序与诊断次序）。
func TestCardShowIsDeterministic(t *testing.T) {
	root := cardShowVault(t)
	first, err := json.Marshal(mustShowCard(t, root, "k-20260901-a").Card)
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(mustShowCard(t, root, "k-20260901-a").Card)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("两次输出不同：\n%s\n%s", first, second)
	}
}

// TestCardShowIsReadOnly —— 合同 §6：调用前后文件内容 hash 与 mtime 逐字不变。
func TestCardShowIsReadOnly(t *testing.T) {
	root := cardShowVault(t)
	before := treeSnapshot(t, root)
	mustShowCard(t, root, "k-20260901-a")
	mustShowCard(t, root, "k-20260903-c")
	if after := treeSnapshot(t, root); after != before {
		t.Fatalf("card show 改动了文件：\n%s\n%s", before, after)
	}
}
