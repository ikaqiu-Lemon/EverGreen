package query

// [S4] 关系**四级排序键**的机器判据（M5 索引架构合同 §7.4 + `M-005` 判据 11；T-…-068）。
//
// 本文件只证四件事，每件都能逐格核对，不看「输出好像一样」：
//
//	① 键集合封闭：恰四级、次序固定（SortKeyOrder 可逐格比对）；
//	② 第五键 / 改名 / 重排一律被拒（ErrSortKeyRejected，不许悄悄按前四级排）；
//	③ 第 ④ 级是**全序封闭点**：四级全等 ⇒ 两条条目五格逐字相同
//	   —— 因此同键先后不可能改变输出，确定性**不依赖输入顺序**；
//	④ 同一多重集在任意输入排列下输出逐字相同，且索引后端与扫描后端逐字相等。
//
// 为什么是包内测试（package query）：③ 要直接调 `sortKeyCompare` 逐级取比较结果 ——
// 「四级全等」这件事是排序器的内部判断，只有从包内问它才是真的反证，
// 从包外只能对着输出猜。
//
// 本文件**不碰历史用例**：M2 的两级排序判据（relation_test.go / scan_test.go）一字不动，
// 本文件只追加 S4 新增的封闭性与全序判据。

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// rkEdge 造一条关系条目（五格全给，便于逐格构造同键 / 差一格的对照）。
func rkEdge(from, typ, target, reason, path string) RelationEdge {
	return RelationEdge{From: from, Type: typ, Target: target, Reason: reason, Path: path}
}

// rkByTarget / rkByFrom 是两种「对端」口径（正向比 target、反向比 from）。
func rkByTarget(e RelationEdge) string { return e.Target }
func rkByFrom(e RelationEdge) string   { return e.From }

// rkJSON 把条目序列序列化成逐字可比的字节（判据要的是「输出逐字相同」而不是 DeepEqual）。
func rkJSON(t *testing.T, edges []RelationEdge) string {
	t.Helper()
	b, err := json.Marshal(edges)
	if err != nil {
		t.Fatalf("序列化条目失败：%v", err)
	}
	return string(b)
}

// —— ① 键集合封闭：恰四级、次序固定 ——

func TestRelationSortFourKeysOrder(t *testing.T) {
	want := []string{"relation_type", "peer_id", "path", "edge_id"}
	got := SortKeyOrder()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("四级排序键 = %v，期望恰 %v（次序即优先级）", got, want)
	}
	if SortKeyCount != len(want) {
		t.Fatalf("SortKeyCount = %d，期望 %d（键集合封闭）", SortKeyCount, len(want))
	}
	// 返回的必须是副本：外部改不动本包的排序口径。
	got[0] = "被外部改过"
	if SortKeyOrder()[0] != want[0] {
		t.Fatalf("SortKeyOrder 返回了内部切片，外部可改排序口径")
	}
	// 第 ① 级的固定业务次序（合同 §3.3）：opposing 最影响判断故首位，不按字母序。
	wantTypes := []string{"opposing", "limits", "supports", "derives"}
	if gotTypes := RelationTypeOrder(); !reflect.DeepEqual(gotTypes, wantTypes) {
		t.Fatalf("关系类型固定次序 = %v，期望 %v", gotTypes, wantTypes)
	}
}

// —— ② 第五键 / 改名 / 重排一律被拒 ——

func TestRelationSortRejectsFifthKey(t *testing.T) {
	base := SortKeyOrder()
	cases := map[string][]string{
		"第五键":   append(append([]string{}, base...), "updated_at"),
		"少一级":   base[:3],
		"改名":    {"relation_type", "peer_id", "path", "row_id"},
		"重排前两级": {"peer_id", "relation_type", "path", "edge_id"},
		"空清单":   {},
	}
	for name, keys := range cases {
		edges := []RelationEdge{
			rkEdge("k-20261201-b", "derives", "k-20261201-a", "r", "p2.md"),
			rkEdge("k-20261201-a", "derives", "k-20261201-b", "r", "p1.md"),
		}
		before := rkJSON(t, edges)
		err := SortEdgesBy(edges, rkByTarget, keys)
		if !errors.Is(err, ErrSortKeyRejected) {
			t.Fatalf("%s：期望 ErrSortKeyRejected，实际 %v", name, err)
		}
		// 被拒之后**不许悄悄排一遍**：入参一格不动，调用方必须自己面对错误。
		if after := rkJSON(t, edges); after != before {
			t.Fatalf("%s：键集合被拒但入参已被重排\n before=%s\n after =%s", name, before, after)
		}
	}
	// 合法四级键必须仍然成功（反证上面的拒绝不是「一律报错」）。
	if err := SortEdgesBy([]RelationEdge{}, rkByTarget, SortKeyOrder()); err != nil {
		t.Fatalf("恰四级合法键被拒：%v", err)
	}
}

// —— ③ 第 ④ 级是全序封闭点 ——

// TestRelationSortIdentityCoversEveryOutputField 反证「第 ④ 级覆盖了除 path 外的每一个
// 输出字段」：`EdgeIdentityFields() ∪ {path}` 必须**恰等于** RelationEdge 的 JSON 键全集。
//
// 这是 ③ 的地基：少覆盖任何一格（例如漏掉 `reason`），就会存在「四级全等但输出不同」的
// 两条条目，它们的先后只能落回输入顺序 —— 判据 11 当场失守。
// 反过来，只要本用例成立，`RelationEdge` 将来加第六格时它会立刻变红，逼着回来补第 ④ 级。
func TestRelationSortIdentityCoversEveryOutputField(t *testing.T) {
	rt := reflect.TypeOf(RelationEdge{})
	all := map[string]bool{}
	for i := 0; i < rt.NumField(); i++ {
		tag := rt.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			t.Fatalf("RelationEdge.%s 没有 json 标签：输出键集合无法逐格核对", rt.Field(i).Name)
		}
		all[tag] = true
	}
	covered := map[string]bool{"path": true} // path = 第 ③ 级
	for _, f := range EdgeIdentityFields() {
		if !all[f] {
			t.Fatalf("EdgeIdentityFields 里的 %q 不是 RelationEdge 的输出键", f)
		}
		if covered[f] {
			t.Fatalf("EdgeIdentityFields 重复覆盖 %q", f)
		}
		covered[f] = true
	}
	if len(covered) != len(all) {
		t.Fatalf("排序键未覆盖全部输出字段：输出键 %v，已覆盖 %v（第 ④ 级不再是全序封闭点）",
			all, covered)
	}
}

// TestRelationSortTieImpliesIdenticalOutput 逐格反证「四级全等 ⇒ 输出逐字相同」。
//
// 语料刻意造出前三级全等、只有 `reason` 不同的两条重复关系（M2 明确「重复条目照实输出、
// 不折叠、不去重」）：旧口径的第 ④ 级只到 `from|target`，会把它们判成同键、顺序退回输入序；
// 现口径必须给出**确定**次序（reason 升序），且任何一对真同键必须五格逐字相同。
func TestRelationSortTieImpliesIdenticalOutput(t *testing.T) {
	// reason 刻意取 ASCII 的 "ra" < "rb"：判据要的是「次序确定且可复算」，
	// 因此断言的那个方向必须能一眼算出来（中文字面量的码点序不直观，不适合做断言锚点）。
	dupA := rkEdge("k-20261201-a", "supports", "k-20261201-b", "ra", "p.md")
	dupB := rkEdge("k-20261201-a", "supports", "k-20261201-b", "rb", "p.md")

	// 两条只差 reason 的条目：四级键**必须**分出先后，且与输入顺序无关。
	forward := []RelationEdge{dupA, dupB}
	backward := []RelationEdge{dupB, dupA}
	SortEdges(forward, rkByTarget)
	SortEdges(backward, rkByTarget)
	if rkJSON(t, forward) != rkJSON(t, backward) {
		t.Fatalf("只差 reason 的重复条目顺序依赖输入序（第 ④ 级不是全序封闭点）\n"+
			" 正序输入 → %s\n 逆序输入 → %s", rkJSON(t, forward), rkJSON(t, backward))
	}
	if forward[0].Reason != "ra" {
		t.Fatalf("同键兜底次序不是确定的 reason 升序，实际首条 reason=%q", forward[0].Reason)
	}

	// 全序性：排序结果里任何**相邻**两条，若四级键逐级全等，则必须五格逐字相同。
	edges := []RelationEdge{
		dupB, dupA,
		rkEdge("k-20261201-a", "supports", "k-20261201-b", "ra", "q.md"), // 只差 path（第 ③ 级）
		rkEdge("k-20261201-c", "supports", "k-20261201-b", "ra", "p.md"), // 只差 from（第 ④ 级）
		rkEdge("k-20261201-a", "opposing", "k-20261201-b", "ra", "p.md"), // 只差 type（第 ① 级）
		rkEdge("k-20261201-a", "supports", "k-20261201-z", "ra", "p.md"), // 只差对端（第 ② 级）
		dupA, // 与 dupA 完全相同的一条：真同键，允许相邻且逐字相同
	}
	SortEdges(edges, rkByTarget)
	ties := 0
	for i := 1; i < len(edges); i++ {
		same := true
		for _, key := range SortKeyOrder() {
			if sortKeyCompare(key, edges[i-1], edges[i], rkByTarget) != 0 {
				same = false
				break
			}
		}
		if !same {
			continue
		}
		ties++
		if !SameEdgeOutput(edges[i-1], edges[i]) {
			t.Fatalf("第 %d/%d 两条四级键全等却输出不同：%+v vs %+v", i, i+1, edges[i-1], edges[i])
		}
	}
	if ties == 0 {
		t.Fatalf("语料没有造出任何真同键对，本用例会变成空断言（请检查语料）")
	}
}

// —— ④ 输出与输入排列无关、与后端无关 ——

// TestRelationSortStableOnTies 用**同一多重集的多种输入排列**反证输出逐字相同。
//
// 这一条直接对应「同一语料两次执行输出逐字相同」的 M2 冻结口径：文件系统返回序、
// frontmatter 书写序、索引行返回序都可能不同，排序必须把这些差异全部吸收掉。
func TestRelationSortStableOnTies(t *testing.T) {
	base := []RelationEdge{
		rkEdge("k-20261201-a", "supports", "k-20261201-b", "理由乙", "p.md"),
		rkEdge("k-20261201-a", "supports", "k-20261201-b", "理由甲", "p.md"),
		rkEdge("k-20261201-a", "opposing", "k-20261201-b", "冲突", "p.md"),
		rkEdge("k-20261201-a", "derives", "k-20261201-b", "派生", "p.md"),
		rkEdge("k-20261201-a", "limits", "k-20261201-b", "限定", "p.md"),
		rkEdge("k-20261201-a", "supports", "k-20261201-b", "理由甲", "q.md"),
		rkEdge("k-20261201-z", "supports", "k-20261201-b", "理由甲", "p.md"),
	}
	sorted := append([]RelationEdge{}, base...)
	SortEdges(sorted, rkByTarget)
	want := rkJSON(t, sorted)

	// 旋转 + 反转，共 2n 种排列：每一种都必须给出逐字相同的输出。
	n := len(base)
	for shift := 0; shift < n; shift++ {
		for _, rev := range []bool{false, true} {
			perm := make([]RelationEdge, 0, n)
			for i := 0; i < n; i++ {
				perm = append(perm, base[(i+shift)%n])
			}
			if rev {
				for i, j := 0, len(perm)-1; i < j; i, j = i+1, j-1 {
					perm[i], perm[j] = perm[j], perm[i]
				}
			}
			SortEdges(perm, rkByTarget)
			if got := rkJSON(t, perm); got != want {
				t.Fatalf("输入排列（shift=%d rev=%t）改变了输出\n got =%s\n want=%s",
					shift, rev, got, want)
			}
		}
	}
	// 反向口径（比 from）同样必须与输入排列无关。
	rev := append([]RelationEdge{}, base...)
	SortEdges(rev, rkByFrom)
	revAgain := make([]RelationEdge, 0, n)
	for i := n - 1; i >= 0; i-- {
		revAgain = append(revAgain, base[i])
	}
	SortEdges(revAgain, rkByFrom)
	if rkJSON(t, rev) != rkJSON(t, revAgain) {
		t.Fatalf("反向口径的输出依赖输入序\n a=%s\n b=%s", rkJSON(t, rev), rkJSON(t, revAgain))
	}
}

// TestRelationSortIndexEqualsScan 在**真索引 / 无索引**两条后端上跑同一份语料，
// 要求关系列表逐字相等（合同 §7.4：FTS5 的 `rank` 一律不用，排序必须能在内存里复算）。
//
// 语料里刻意含「同一张卡对同一对端写了两条同型关系、只有 reason 不同」：
// 索引里 (src, verb, dst) 三列相同，两行不可区分，reason 只能回权威 Markdown 取 ——
// 若排序在这里退回输入顺序，两条后端的输出就会漂移，本用例即当场变红。
func TestRelationSortIndexEqualsScan(t *testing.T) {
	root := bkVault(t)
	// 追加一张只出现在本用例里的卡：两条同型同对端、reason 不同的重复关系。
	bkWrite(t, root, "domains/ai-infra/knowledge/k-20261201-dup.md",
		bkCard("k-20261201-dup", "重复关系", "active", "2026-12-08T10:00:00+08:00", "",
			"  - type: supports\n    target: k-20261201-attention\n    reason: 理由乙\n"+
				"  - type: supports\n    target: k-20261201-attention\n    reason: 理由甲\n"))
	bkBuildIndex(t, root)
	if b := SelectBackend(root, cardNeed("k-20261201-dup", bkDeps)); !b.UseIndex() {
		t.Fatalf("刚建好的索引应判健康，实际 kind=%s reason=%s", b.Kind, b.Reason)
	}

	for _, id := range []string{"k-20261201-dup", "k-20261201-attention"} {
		withIndex, err := bkRel(root, RelRequest{ID: model.CardID(id)})
		if err != nil {
			t.Fatalf("索引后端 rel(%s)：%v", id, err)
		}
		bkDropIndex(t, root)
		withScan, err := bkRel(root, RelRequest{ID: model.CardID(id)})
		if err != nil {
			t.Fatalf("扫描后端 rel(%s)：%v", id, err)
		}
		bkBuildIndex(t, root) // 复位，供下一轮比对
		if got, want := rkJSON(t, withIndex.Data.RelationsOut), rkJSON(t, withScan.Data.RelationsOut); got != want {
			t.Fatalf("%s 的正向关系两条后端不逐字相等\n index=%s\n scan =%s", id, got, want)
		}
		if got, want := rkJSON(t, withIndex.Data.RelationsIn), rkJSON(t, withScan.Data.RelationsIn); got != want {
			t.Fatalf("%s 的反向关系两条后端不逐字相等\n index=%s\n scan =%s", id, got, want)
		}
	}
}
