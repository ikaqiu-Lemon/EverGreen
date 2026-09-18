package query

// [S4 · Phase6C] `replaced_by` 读路径的 **k/o 端点覆盖**（M5 索引架构合同 §8.4；T-…-006）。
//
// reverse_test.go 钉的是 k→k 单类型语料；本文件把替代指针读路径的取数层判据补齐到
// schema v2 的**端点跨类型**面：宿主与目标各自可以是知识卡（k-*）或观点（o-*），四组合
// k→k / k→o / o→k / o→o 都要在正反两个方向如实现身，链式中间端点两个方向各一条，k/o 自指
// 两个方向都忽略，悬空目标照实输出。取数一律走**同一次全库 Scan**的统一端点投影
// （Knowledge ∪ Opinion），不为 o-* 再读文件、也不二次解码 model —— 这正是与论证关系读路径
// 同构的取数纪律（reverse.go）。
//
// 本文件**不 import internal/index**：它只验证扫描底座上的端点语义（正反向、链式、自指、
// 悬空、--to、分页、宿主/目标删除·失效正交）。四种索引后端的逐字等价另置于
// index_backed_replaced_by_test.go（架构边界只放行 index_backed* 前缀 import 索引包）。

import (
	"reflect"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// rvOpinion 造一条可带 `replaced_by` / `deleted_at` 的观点（脚手架，非产品写路径）。
//
// 语义与 rvCard 逐字对称：`replaced_by` 写在**已失效**的那个端点上（这里是观点），
// 读作「本端点已失效、被 target 取代」；target 可为 k-* 或 o-*。
func rvOpinion(id, title, status, validation, replacedTarget, replacedReason string, deleted bool) string {
	fm := "---\nid: " + id + "\nstatus: " + status +
		"\ncreated_at: '2026-12-01'\nupdated_at: '2026-12-05T10:00:00+08:00'" +
		"\ntitle: " + title + "\nvalidation: " + validation + "\nsources: []\n"
	if replacedTarget != "" {
		fm += "replaced_by:\n  target: " + replacedTarget + "\n  reason: " + replacedReason + "\n"
	}
	if deleted {
		fm += "deleted_at: '2026-12-06T10:00:00+08:00'\ndeleted_reason: 用例造的逻辑删除\n"
	}
	return fm + "---\n\n# " + title + "\n\n## 观点\n\n主张一句。\n"
}

// rvWriteK / rvWriteO 把知识卡 / 观点写进各自分区（domains/<d>/knowledge|opinions）。
func rvWriteK(t *testing.T, root, id, title, status, target, reason string, deleted bool) {
	t.Helper()
	bkWrite(t, root, "domains/ai-infra/knowledge/"+id+".md",
		rvCard(id, title, status, target, reason, deleted))
}

func rvWriteO(t *testing.T, root, id, title, status, validation, target, reason string, deleted bool) {
	t.Helper()
	bkWrite(t, root, store.OpinionRel("ai-infra", id),
		rvOpinion(id, title, status, validation, target, reason, deleted))
}

// koCombosVault 造覆盖四组合的语料：每组一个失效宿主端点 + 一个 active 目标端点。
//
//	k→k：kkold(k, deprecated) → kknew(k)
//	k→o：koold(k, deprecated) → konew(o)
//	o→k：okold(o, deprecated) → oknew(k)
//	o→o：ooold(o, deprecated) → oonew(o)
func koCombosVault(t *testing.T) *ScanResult {
	t.Helper()
	root := t.TempDir()
	rvWriteK(t, root, "k-20261201-kkold", "kk 旧", "deprecated", "k-20261201-kknew", "kk 理由", false)
	rvWriteK(t, root, "k-20261201-kknew", "kk 新", "active", "", "", false)
	rvWriteK(t, root, "k-20261201-koold", "ko 旧", "deprecated", "o-20261201-konew", "ko 理由", false)
	rvWriteO(t, root, "o-20261201-konew", "ko 新", "active", "validated", "", "", false)
	rvWriteO(t, root, "o-20261201-okold", "ok 旧", "deprecated", "rejected", "k-20261201-oknew", "ok 理由", false)
	rvWriteK(t, root, "k-20261201-oknew", "ok 新", "active", "", "", false)
	rvWriteO(t, root, "o-20261201-ooold", "oo 旧", "deprecated", "rejected", "o-20261201-oonew", "oo 理由", false)
	rvWriteO(t, root, "o-20261201-oonew", "oo 新", "active", "validated", "", "", false)
	return rvScan(t, root)
}

// —— 四组合：正向（宿主焦点）与反向（目标焦点）都如实现身 ——

func TestReplacedByForwardReverseKOCombos(t *testing.T) {
	scan := koCombosVault(t)
	cases := []struct {
		name, host, target, reason string
	}{
		{"k→k", "k-20261201-kkold", "k-20261201-kknew", "kk 理由"},
		{"k→o", "k-20261201-koold", "o-20261201-konew", "ko 理由"},
		{"o→k", "o-20261201-okold", "k-20261201-oknew", "ok 理由"},
		{"o→o", "o-20261201-ooold", "o-20261201-oonew", "oo 理由"},
	}
	for _, c := range cases {
		wantSig := []string{c.host + "|replaced_by|" + c.target + "|" + c.reason}
		// 正向：焦点 = 宿主端点，恰一条边（谁取代了宿主）。
		fwd := ReplacedByForward(scan, c.host)
		if got := rvSig(fwd); !reflect.DeepEqual(got, wantSig) {
			t.Fatalf("%s 正向(%s) = %v，期望 %v", c.name, c.host, got, wantSig)
		}
		// 反向：焦点 = 目标端点，恰一条边（宿主取代了谁）。宿主可为 k 或 o，两类都要扫到。
		rev := ReplacedByReverse(scan, c.target)
		if got := rvSig(rev); !reflect.DeepEqual(got, wantSig) {
			t.Fatalf("%s 反向(%s) = %v，期望 %v", c.name, c.target, got, wantSig)
		}
		// 同一条记录、两个方向五格逐字相同（记录只有一份，反向不是第二条记录）。
		if !SameEdgeOutput(fwd[0], rev[0]) {
			t.Fatalf("%s 正反两个方向读出的不是同一条记录：%+v vs %+v", c.name, fwd[0], rev[0])
		}
		// 方向不可颠倒：目标端点没有正向（没人取代它），宿主端点没有反向（它没被谁取代）。
		if got := ReplacedByForward(scan, c.target); len(got) != 0 {
			t.Fatalf("%s 目标端点不应有正向，实际 %v", c.name, rvSig(got))
		}
		if got := ReplacedByReverse(scan, c.host); len(got) != 0 {
			t.Fatalf("%s 宿主端点不应有反向，实际 %v", c.name, rvSig(got))
		}
	}
}

// —— 链式跨类型 k→o→k：中间观点两个方向各一条；链不传递 ——

func TestReplacedByChainAcrossKO(t *testing.T) {
	root := t.TempDir()
	rvWriteK(t, root, "k-20261201-cha", "链 A", "deprecated", "o-20261201-chb", "A 被 B 取代", false)
	rvWriteO(t, root, "o-20261201-chb", "链 B", "deprecated", "rejected", "k-20261201-chc", "B 又被 C 取代", false)
	rvWriteK(t, root, "k-20261201-chc", "链 C", "active", "", "", false)
	scan := rvScan(t, root)

	// 中间观点 B：正向恰一条（谁取代了 B = C）、反向恰一条（B 取代了谁 = A）。
	if got, want := rvSig(ReplacedByForward(scan, "o-20261201-chb")),
		[]string{"o-20261201-chb|replaced_by|k-20261201-chc|B 又被 C 取代"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("链中间观点 B 的正向 = %v，期望 %v", got, want)
	}
	if got, want := rvSig(ReplacedByReverse(scan, "o-20261201-chb")),
		[]string{"k-20261201-cha|replaced_by|o-20261201-chb|A 被 B 取代"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("链中间观点 B 的反向 = %v，期望 %v", got, want)
	}
	// 链不传递：C 的反向只有 B 这一跳。
	if got, want := rvSig(ReplacedByReverse(scan, "k-20261201-chc")),
		[]string{"o-20261201-chb|replaced_by|k-20261201-chc|B 又被 C 取代"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("C 的反向 = %v，期望只含 B 这一跳 %v", got, want)
	}
}

// —— k/o 自指两个方向都忽略；悬空目标照实输出 ——

func TestReplacedByKOSelfAndDangling(t *testing.T) {
	root := t.TempDir()
	// 观点自指：两个方向都不构造。
	rvWriteO(t, root, "o-20261201-self", "自指观点", "deprecated", "rejected", "o-20261201-self", "自指", false)
	// 悬空：宿主指向库中不存在的端点，正向照实输出该边（悬空由 RelView 按 Q2 登记）。
	rvWriteK(t, root, "k-20261201-dang", "悬空宿主", "deprecated", "o-20261201-ghost", "指向不存在的观点", false)
	scan := rvScan(t, root)

	if got := ReplacedByForward(scan, "o-20261201-self"); len(got) != 0 {
		t.Fatalf("观点自指不应有正向，实际 %v", rvSig(got))
	}
	if got := ReplacedByReverse(scan, "o-20261201-self"); len(got) != 0 {
		t.Fatalf("观点自指不应有反向，实际 %v", rvSig(got))
	}
	if got, want := rvSig(ReplacedByForward(scan, "k-20261201-dang")),
		[]string{"k-20261201-dang|replaced_by|o-20261201-ghost|指向不存在的观点"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("悬空目标应照实输出正向边，实际 %v，期望 %v", got, want)
	}
	// 不存在的端点自然没有反向来源（没人真的取代了一个不存在的目标之外的东西）。
	if got := ReplacedByReverse(scan, "o-20261201-ghost"); len(got) != 1 {
		t.Fatalf("悬空目标的反向应仍能从宿主一头读出那条记录，实际 %v", rvSig(got))
	}
}

// —— 端到端（RelView）：--to 过滤、分页、宿主为观点时可见性正交 ——

// koVaultForRelView 造一个新卡被多个失效端点（一个 k 宿主、一个 o 宿主）取代的语料，
// 便于验证反向的 --to / 分页 / 可见性在两类宿主上都成立。
func koVaultForRelView(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	rvWriteK(t, root, "k-20261201-new", "新卡", "active", "", "", false)
	// 两个失效宿主（一个知识卡、一个观点）都替代为同一张新卡：反向应有两条。
	rvWriteK(t, root, "k-20261201-oldk", "旧知识卡", "deprecated", "k-20261201-new", "知识卡宿主替代", false)
	rvWriteO(t, root, "o-20261201-oldo", "旧观点", "deprecated", "rejected", "k-20261201-new", "观点宿主替代", false)
	return root
}

func TestReplacedByRelViewToAndPaginationKO(t *testing.T) {
	root := koVaultForRelView(t)
	id := model.RelationEndpoint("k-20261201-new")

	// 反向两条宿主端点都是 deprecated：放开后可见，且 from 覆盖 k 宿主与 o 宿主两类。
	opened, err := bkRel(root, RelRequest{ID: id, ReplacedBy: true, IncludeDeprecated: true})
	if err != nil {
		t.Fatalf("rel --replaced-by --include-deprecated：%v", err)
	}
	wantFroms := []string{"k-20261201-oldk", "o-20261201-oldo"}
	gotFroms := []string{}
	for _, e := range opened.Data.RelationsIn {
		gotFroms = append(gotFroms, e.From)
	}
	if !reflect.DeepEqual(gotFroms, wantFroms) {
		t.Fatalf("反向 from（跨 k/o 宿主，按 from 升序）= %v，期望 %v", gotFroms, wantFroms)
	}

	// --to 收窄到观点宿主：只保留 from == o 宿主 的那条。
	toOpn, err := bkRel(root, RelRequest{ID: id, ReplacedBy: true, IncludeDeprecated: true,
		To: "o-20261201-oldo"})
	if err != nil {
		t.Fatalf("rel --to o：%v", err)
	}
	if len(toOpn.Data.RelationsIn) != 1 || toOpn.Data.RelationsIn[0].From != "o-20261201-oldo" {
		t.Fatalf("--to 观点宿主应只留一条，实际 %v", korFromsLocal(toOpn.Data.RelationsIn))
	}

	// 分页：放开 deprecated 后两条可见，--limit 1 恰返回一条且判截断（W25）。
	paged, err := bkRel(root, RelRequest{ID: id, ReplacedBy: true, IncludeDeprecated: true,
		Page: PageSpec{Limit: 1}})
	if err != nil {
		t.Fatalf("rel --limit 1：%v", err)
	}
	if n := len(paged.Data.RelationsOut) + len(paged.Data.RelationsIn); n != 1 {
		t.Fatalf("--limit 1 应恰返回 1 条，实际 %d", n)
	}
	if paged.Page.Total != 2 || !paged.Page.Truncated {
		t.Fatalf("两条可见、限量 1 应判截断：%+v", paged.Page)
	}

	// 默认视图：两个宿主都是 deprecated，全部隐藏并产 Q4；隐藏计数 = 2。
	def, err := bkRel(root, RelRequest{ID: id, ReplacedBy: true})
	if err != nil {
		t.Fatalf("rel 默认：%v", err)
	}
	if len(def.Data.RelationsIn) != 0 || def.HiddenDeprecated != 2 {
		t.Fatalf("默认应隐藏两个 deprecated 宿主，实际 in=%d hidden=%d",
			len(def.Data.RelationsIn), def.HiddenDeprecated)
	}
	if n := pgCount(def.Diagnostics, CodeQ4); n != 1 {
		t.Fatalf("隐藏 deprecated 对端应产恰一条 Q4，实际 %d", n)
	}
}

// —— 宿主/目标的删除·失效维度彼此正交（观点宿主一侧）——

func TestReplacedByKOVisibilityOrthogonal(t *testing.T) {
	root := t.TempDir()
	// 目标新卡 active；观点宿主同时 deprecated + 逻辑删除。
	rvWriteK(t, root, "k-20261201-tgt", "目标卡", "active", "", "", false)
	rvWriteO(t, root, "o-20261201-hostdel", "被删观点宿主", "deprecated", "rejected",
		"k-20261201-tgt", "观点宿主替代", true)

	// 目标端点的反向：宿主已删除 ⇒ 任何 flag 下都隐藏，且不计入 Q4 的 deprecated 计数。
	for _, incl := range []bool{false, true} {
		res, err := bkRel(root, RelRequest{ID: model.RelationEndpoint("k-20261201-tgt"),
			ReplacedBy: true, IncludeDeprecated: incl})
		if err != nil {
			t.Fatalf("rel(incl=%t)：%v", incl, err)
		}
		if len(res.Data.RelationsIn) != 0 {
			t.Fatalf("incl=%t：已删除的观点宿主仍被展示 %v", incl, korFromsLocal(res.Data.RelationsIn))
		}
		if res.HiddenDeprecated != 0 {
			t.Fatalf("incl=%t：已删除宿主不应计入 deprecated 隐藏数，实际 %d", incl, res.HiddenDeprecated)
		}
		if n := pgCount(res.Diagnostics, CodeQ4); n != 0 {
			t.Fatalf("incl=%t：已删除宿主不应触发 Q4，实际 %d", incl, n)
		}
	}
}

// korFromsLocal 取 relations_in 的 from 序列（本文件局部小工具，避免跨文件耦合）。
func korFromsLocal(edges []RelationEdge) []string {
	out := []string{}
	for _, e := range edges {
		out = append(out, e.From)
	}
	return out
}
