package query_test

// [M3] internal/query/markers_test.go：显著标记与四象限可见性的机器判据
// （提案与状态合同 §5.1 真值表 / §6.2 标记表；T-evergreen.s1_main_flow-158614-043）。
//
// 三组判据：
//   - TestMarkers_JSONFields：三个文本标记与三个 JSON 布尔字段**一一对应**、顺序固定；
//   - TestDeletedExcludedFromDefaultView：4 行 × 5 列表驱动，逐格与 §5.1 真值表一致，
//     并把「已删除退出默认视图 / 收敛 / 关系端点 / 取材，仅可显式查看」落到真实行为上；
//   - TestRelationEndpointFiltering_RecordsUntouched：端点过滤生效 **且** 源文件
//     relations[] 条目数与字节一字不变（过滤在查询层，记录不动）。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
	"github.com/ikaqiu-Lemon/EverGreen/internal/query/filter"
)

// markerVault 造一份四象限齐备的语料：四张卡分别落在 §5.1 的四行上。
//
// 过目维度单独控制：`reviewed_at` 与 `updated_at` 相等即「已过目」（严格大于才算未过目），
// 缺省即「从未过目」——三个维度在语料层就互不耦合。
func markerVault(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	// ① active + 未删除（已过目）：第一象限，指向另外三张卡各一条关系。
	writeFile(t, root, "domains/ai-infra/knowledge/k-20260901-live.md",
		"---\nid: k-20260901-live\nstatus: active\ncreated_at: '2026-09-01'\n"+
			"updated_at: '2026-09-01T10:00:00+08:00'\n"+
			"reviewed_at: '2026-09-01T10:00:00+08:00'\ntitle: 在册的卡 注意力\nsources: []\n"+
			"relations:\n"+
			"  - type: supports\n    target: k-20260902-dep\n    reason: 支持失效卡\n"+
			"  - type: limits\n    target: k-20260903-del\n    reason: 限制已删除卡\n"+
			"  - type: derives\n    target: k-20260904-both\n    reason: 由双标记卡推出\n"+
			"---\n\n## 知识内容\n\n正文占位 注意力。\n")
	// ② deprecated + 未删除（已过目）：第二象限。
	writeFile(t, root, "domains/ai-infra/knowledge/k-20260902-dep.md",
		"---\nid: k-20260902-dep\nstatus: deprecated\ncreated_at: '2026-09-02'\n"+
			"updated_at: '2026-09-02T10:00:00+08:00'\n"+
			"reviewed_at: '2026-09-02T10:00:00+08:00'\ntitle: 失效的卡 注意力\nsources: []\n"+
			"---\n\n## 知识内容\n\n正文占位 注意力。\n")
	// ③ active + 已删除（已过目）：第三象限。
	writeFile(t, root, "domains/ai-infra/knowledge/k-20260903-del.md",
		"---\nid: k-20260903-del\nstatus: active\ncreated_at: '2026-09-03'\n"+
			"updated_at: '2026-09-03T10:00:00+08:00'\n"+
			"reviewed_at: '2026-09-03T10:00:00+08:00'\n"+
			"deleted_at: '2026-09-05T10:00:00+08:00'\ndeleted_reason: 用户判断这条已过时\n"+
			"title: 已删除的卡 注意力\nsources: []\n"+
			"relations:\n  - type: supports\n    target: k-20260901-live\n    reason: 支持在册卡\n"+
			"---\n\n## 知识内容\n\n正文占位 注意力。\n")
	// ④ deprecated + 已删除 + 未过目（缺 reviewed_at）：第四象限，显式查看时三标记齐出。
	writeFile(t, root, "domains/ai-infra/knowledge/k-20260904-both.md",
		"---\nid: k-20260904-both\nstatus: deprecated\ncreated_at: '2026-09-04'\n"+
			"updated_at: '2026-09-04T10:00:00+08:00'\n"+
			"deleted_at: '2026-09-06T10:00:00+08:00'\ndeleted_reason: 失效后又被删除\n"+
			"title: 双标记的卡 注意力\nsources: []\n"+
			"---\n\n## 知识内容\n\n正文占位 注意力。\n")
	return root
}

// TestMarkers_JSONFields —— 三个文本标记与三个 JSON 布尔字段一一对应（合同 §6.2 标记表），
// 且顺序恒为 `[失效]` → `[已删除]` → `[未过目]`（本合同新定，逐字）。
func TestMarkers_JSONFields(t *testing.T) {
	// ① 文本 ↔ 字段一一对应：逐个标记只点亮它自己那一个字段。
	cases := []struct {
		marker string
		field  string
		state  query.MarkerState
	}{
		{query.MarkerDeprecated, query.FieldDeprecated, query.MarkerState{Deprecated: true}},
		{query.MarkerDeleted, query.FieldDeleted, query.MarkerState{Deleted: true}},
		{query.MarkerUnreviewed, query.FieldUnreviewed, query.MarkerState{Unreviewed: true}},
	}
	for _, c := range cases {
		got := query.Markers(c.state)
		if !reflect.DeepEqual(got, []string{c.marker}) {
			t.Fatalf("state %+v 的 markers = %v，期望 [%s]", c.state, got, c.marker)
		}
		field, ok := query.MarkerFieldOf(c.marker)
		if !ok || field != c.field {
			t.Fatalf("标记 %s 对应字段 = %q（ok=%t），期望 %q", c.marker, field, ok, c.field)
		}
		fields := query.MarkerFields(c.state)
		if len(fields) != 3 {
			t.Fatalf("字段集合恒为 3 键（false 也不丢键），实际 %v", fields)
		}
		for k, v := range fields {
			if want := k == c.field; v != want {
				t.Fatalf("state %+v 的字段 %s = %t，期望 %t", c.state, k, v, want)
			}
		}
	}
	// ② 顺序与字段次序一一对应，且三者齐出时是逐字的完整形态。
	if !reflect.DeepEqual(query.MarkerOrder(),
		[]string{query.MarkerDeprecated, query.MarkerDeleted, query.MarkerUnreviewed}) {
		t.Fatalf("标记顺序 = %v，与合同 §6.2 新定顺序不符", query.MarkerOrder())
	}
	if !reflect.DeepEqual(query.MarkerFieldKeys(),
		[]string{"deprecated", "deleted", model.FieldUnreviewed}) {
		t.Fatalf("字段次序 = %v，与标记顺序不一一对应", query.MarkerFieldKeys())
	}
	all := query.MarkerState{Deprecated: true, Deleted: true, Unreviewed: true}
	if got := query.MarkerPrefix(all); got != "[失效][已删除][未过目]" {
		t.Fatalf("三标记前缀 = %q，期望 [失效][已删除][未过目]", got)
	}
	if got := query.MarkerPrefix(query.MarkerState{}); got != "" {
		t.Fatalf("零维度命中时前缀应为空串，实际 %q", got)
	}

	// ③ card show 的 data 里三个字段与 markers 同源同事实（显式查看第四象限）。
	root := markerVault(t)
	view, err := query.ShowCard(root, model.CardID("k-20260904-both"))
	if err != nil {
		t.Fatalf("ShowCard：%v", err)
	}
	// 过目维度按 ADR-20 只能由筛选器判定，这里模拟命令层：判完注入。
	unreviewed, err := filter.Unreviewed(filter.Candidate{
		ID: view.Card.ID, UpdatedAt: view.UpdatedAt, ReviewedAt: view.ReviewedAt,
	})
	if err != nil {
		t.Fatalf("filter.Unreviewed：%v", err)
	}
	detail := query.WithUnreviewed(view.Card, unreviewed)
	if !detail.Deprecated || !detail.Deleted || !detail.Unreviewed {
		t.Fatalf("第四象限 + 缺 reviewed_at 应三字段全 true，实际 %+v", detail)
	}
	if !reflect.DeepEqual(detail.Markers,
		[]string{query.MarkerDeprecated, query.MarkerDeleted, query.MarkerUnreviewed}) {
		t.Fatalf("markers = %v，期望三标记按固定顺序", detail.Markers)
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"deprecated":true`, `"deleted":true`, `"unreviewed":true`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("JSON 缺 %s：%s", want, raw)
		}
	}
	// data 键序里两个新字段在末尾追加（既有 14 键的名字与次序不动）。
	keys := query.CardDataKeys()
	if len(keys) != 16 || keys[14] != "deleted" || keys[15] != model.FieldUnreviewed {
		t.Fatalf("CardDataKeys 末两键应是 deleted / %s，实际 %v", model.FieldUnreviewed, keys)
	}
}

// TestDeletedExcludedFromDefaultView —— §5.1 四象限真值表**表驱动 4 行 × 5 列**。
//
// 五列：默认检索 / 参与收敛 / 关系端点默认展示 / 综述取材 / 可显式查看。
// 每一行先逐格比对 filter 里声明的真值表，再对**已删除两行**做真实行为反证
// （四列否 + 显式查看可用）——这是本用例名字所钉的那条判据。
func TestDeletedExcludedFromDefaultView(t *testing.T) {
	root := markerVault(t)
	rows := []struct {
		name string
		id   string
		q    filter.Quadrant
		want filter.Visibility
	}{
		{"active+未删除", "k-20260901-live", filter.Quadrant{},
			filter.Visibility{DefaultSearch: true, Converge: true,
				RelationEndpoint: true, Digest: true, ExplicitView: true}},
		{"deprecated+未删除", "k-20260902-dep", filter.Quadrant{Deprecated: true},
			filter.Visibility{DefaultSearch: true, ExplicitView: true}},
		{"active+已删除", "k-20260903-del", filter.Quadrant{Deleted: true},
			filter.Visibility{ExplicitView: true}},
		{"deprecated+已删除", "k-20260904-both",
			filter.Quadrant{Deprecated: true, Deleted: true},
			filter.Visibility{ExplicitView: true}},
	}
	for _, row := range rows {
		// —— 五列真值逐格比对（声明面）——
		if got := filter.VisibilityOf(row.q); got != row.want {
			t.Fatalf("%s 的五列可见性 = %+v，§5.1 真值表 = %+v", row.name, got, row.want)
		}
		// —— 默认检索（行为面）——
		res, err := query.Search(root, query.SearchRequest{Query: "注意力"})
		if err != nil {
			t.Fatalf("Search：%v", err)
		}
		if hitIDs(res)[row.id] != row.want.DefaultSearch {
			t.Fatalf("%s 在默认检索里出现 = %t，期望 %t", row.name,
				hitIDs(res)[row.id], row.want.DefaultSearch)
		}
		// —— 可显式查看（行为面）：四象限一律可显式查看，且已删除项带 [已删除] ——
		view, err := query.ShowCard(root, model.CardID(row.id))
		if err != nil {
			t.Fatalf("%s 显式查看失败（可显式查看列应为真）：%v", row.name, err)
		}
		if !row.want.ExplicitView {
			t.Fatalf("%s 的真值表第五列不为真，与「四象限均可显式查看」冲突", row.name)
		}
		if view.Card.Deleted != row.q.Deleted {
			t.Fatalf("%s 的 deleted 字段 = %t，期望 %t", row.name, view.Card.Deleted, row.q.Deleted)
		}
		if row.q.Deleted && !hasMarker(view.Card.Markers, query.MarkerDeleted) {
			t.Fatalf("%s 显式查看应标 %s，实际 markers=%v",
				row.name, query.MarkerDeleted, view.Card.Markers)
		}
		// —— 参与收敛 / 综述取材（行为面）：eg context 的收敛输入与候选集合 ——
		inConverge, inDigest := contextExposure(t, root, row.id)
		if inConverge != row.want.Converge {
			t.Fatalf("%s 进收敛输入 = %t，期望 %t", row.name, inConverge, row.want.Converge)
		}
		if inDigest != row.want.Digest {
			t.Fatalf("%s 进综述取材候选 = %t，期望 %t", row.name, inDigest, row.want.Digest)
		}
		// —— 关系端点默认展示（行为面）：只对**已删除**两行做行为反证。
		//
		// 第二行（deprecated + 未删除）的第三列在真值表里是 🔴，但 M2 §2.2 / §3 已冻结
		// 「失效卡同等可见、关系条目照实输出」，且本 task 要求 m2_card_show.sh /
		// m2_rel_query.sh 的输出逐字不回归——两条判据直接冲突，故本次只落地「端点已删除
		// 即退出展示」，失效端点保持 M2 口径。冲突已如实上报，待 owner 重钉。
		if row.q.Deleted {
			if endpointShown(t, root, row.id) {
				t.Fatalf("%s 不应作为关系端点默认展示（§5.1 第三列 🔴）", row.name)
			}
		}
	}
}

// hitIDs 把检索命中折成 ID 集合。
func hitIDs(res *query.SearchResult) map[string]bool {
	out := map[string]bool{}
	for _, h := range res.Hits {
		out[h.ID] = true
	}
	return out
}

// hasMarker 报告标记列表里是否含某个标记。
func hasMarker(markers []string, want string) bool {
	for _, m := range markers {
		if m == want {
			return true
		}
	}
	return false
}

// contextExposure 报告某张卡是否进「收敛输入」与「综述取材候选」两个集合。
//
// 两者都取自 eg context 的产物：Cards 是收敛输入（同领域可参与判断的卡），
// Candidates 是取材 / 去重候选。已删除与失效两类都不该出现在其中任何一个里。
func contextExposure(t *testing.T, root string, id string) (bool, bool) {
	t.Helper()
	writeFile(t, root, "sources/s-20260901-x/index.md",
		"---\nid: s-20260901-x\nurl: https://example.com/x\ntitle: 注意力 原文\n"+
			"captured_at: '2026-09-01T10:00:00+08:00'\nstatus: captured\n---\n\n## 原文\n\n正文。\n")
	writeFile(t, root, "domains/ai-infra/notes/n-20260901-x.md",
		"---\nid: n-20260901-x\nsource: s-20260901-x\ncreated_at: '2026-09-01'\n"+
			"updated_at: '2026-09-01T10:00:00+08:00'\n---\n\n## 摘录\n\n注意力 摘录。\n")
	ctx, err := query.Build(query.Request{
		Root: root, Domain: "ai-infra", Note: "n-20260901-x",
	}, func(b []byte) string { return "h" })
	if err != nil {
		t.Fatalf("query.Build：%v", err)
	}
	inConverge := false
	for _, c := range ctx.Cards {
		if c.ID == id {
			inConverge = true
		}
	}
	inDigest := false
	for _, c := range ctx.Candidates {
		if c.ID == id {
			inDigest = true
		}
	}
	return inConverge, inDigest
}

// endpointShown 报告某张卡是否作为关系端点出现在**默认**关系视图里
// （既查正向 relations_out[] 的对端，也查反向 relations_in[] 的来源端）。
func endpointShown(t *testing.T, root string, id string) bool {
	t.Helper()
	res, err := query.RelView(root, query.RelRequest{ID: model.RelationEndpoint("k-20260901-live")})
	if err != nil {
		t.Fatalf("RelView：%v", err)
	}
	for _, e := range res.Data.RelationsOut {
		if e.Target == id {
			return true
		}
	}
	for _, e := range res.Data.RelationsIn {
		if e.From == id {
			return true
		}
	}
	return false
}

// TestRelationEndpointFiltering_RecordsUntouched —— 端点过滤生效 **且** 记录不动：
// 已删除端点不出现在正反向视图里，同时源文件的 relations[] 条目数与字节一字不变。
func TestRelationEndpointFiltering_RecordsUntouched(t *testing.T) {
	root := markerVault(t)
	live := filepath.Join(root, "domains", "ai-infra", "knowledge", "k-20260901-live.md")
	before, err := os.ReadFile(live)
	if err != nil {
		t.Fatal(err)
	}
	delCard := filepath.Join(root, "domains", "ai-infra", "knowledge", "k-20260903-del.md")
	beforeDel, err := os.ReadFile(delCard)
	if err != nil {
		t.Fatal(err)
	}

	res, err := query.RelView(root, query.RelRequest{ID: model.RelationEndpoint("k-20260901-live")})
	if err != nil {
		t.Fatalf("RelView：%v", err)
	}
	// 【T-…-061 重钉（owner 裁决② A-38/A-39）】原断言期望默认视图仍留下指向失效卡
	// k-20260902-dep 的那条（「失效端点保持 M2 口径」）。新合同：deprecated 对端与已删除
	// 对端**默认一并隐藏**（§5.1 真值表「作为关系端点默认展示 🔴」，此前 K-043-01 只落地了
	// 删除维度）。live 的三条正向分别指向 dep（deprecated）/ del（deleted）/ both（两者），
	// 故默认视图正向为空；--include-deprecated 只放开 deprecated 维度，只剩指向 dep 的一条
	// （del / both 因「已删除」维度与该 flag 正交，任何取值下都隐藏）。
	if len(res.Data.RelationsOut) != 0 {
		t.Fatalf("默认视图正向应为空（三个对端分别 deprecated / deleted / 两者，均默认隐藏），实际 %+v",
			res.Data.RelationsOut)
	}
	// 反向：已删除卡写下的那条关系不作为端点展示（del → live 的 supports，对端 del 已删除）。
	for _, e := range res.Data.RelationsIn {
		if e.From == "k-20260903-del" {
			t.Fatalf("反向视图不应展示已删除端点写下的条目：%+v", e)
		}
	}
	// --include-deprecated：只放开 deprecated 维度——正向只剩 supports→dep 一条（del / both
	// 因已删除维度仍隐藏，正交性 §3.2）；反向仍不含已删除端点。
	resInc, err := query.RelView(root, query.RelRequest{
		ID: model.RelationEndpoint("k-20260901-live"), IncludeDeprecated: true})
	if err != nil {
		t.Fatalf("RelView(include-deprecated)：%v", err)
	}
	if len(resInc.Data.RelationsOut) != 1 || resInc.Data.RelationsOut[0].Target != "k-20260902-dep" {
		t.Fatalf("--include-deprecated 正向 = %+v，期望只剩 supports→k-20260902-dep（del/both 仍因已删除隐藏）",
			resInc.Data.RelationsOut)
	}
	for _, e := range resInc.Data.RelationsIn {
		if e.From == "k-20260903-del" {
			t.Fatalf("--include-deprecated 不放开已删除维度：反向仍不应展示 del 端点：%+v", e)
		}
	}

	// 记录不动：源文件字节逐字相同，frontmatter 的 relations[] 条目数不变。
	after, err := os.ReadFile(live)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("查询改动了源文件字节（U-01 无物理删除、只读零副作用）")
	}
	afterDel, err := os.ReadFile(delCard)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterDel) != string(beforeDel) {
		t.Fatalf("查询改动了已删除卡的字节：逻辑删除只在 frontmatter 留标记，记录不动")
	}
	scan, err := query.VaultScan(root, query.ScanOptions{})
	if err != nil {
		t.Fatalf("VaultScan：%v", err)
	}
	for _, c := range scan.Cards {
		switch c.ID {
		case "k-20260901-live":
			if len(c.Relations) != 3 {
				t.Fatalf("在册卡 relations[] 条目数 = %d，期望 3（过滤在查询层，记录不动）",
					len(c.Relations))
			}
		case "k-20260903-del":
			if len(c.Relations) != 1 {
				t.Fatalf("已删除卡 relations[] 条目数 = %d，期望 1（记录不被物理删除）",
					len(c.Relations))
			}
		}
	}
}
