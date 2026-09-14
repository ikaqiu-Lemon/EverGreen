package query

// [S4] `replaced_by` **正反双向查询**的机器判据（M5 索引架构合同 §8.4 + `M-005` 判据 13；T-…-068）。
//
// 判据分两层，刻意分开断言（这正是 §8.4 第三条「与可见性正交」的含义）：
//
//	取数层（reverse.go）：记录只有一份、写在失效卡身上，两个方向都必须能读出它 ——
//	                     `A.replaced_by = B` ⇒ A 的正向有一条、B 的反向有一条；
//	可见性层（card.go）： 过滤复用**既有**的逻辑删除 / deprecated 四象限策略，
//	                     不新增第二套规则；因此 B 的反向默认被隐藏并产 Q4
//	                     （对端 A 是 deprecated），加 --include-deprecated 才展示。
//
// 把两层混在一起断言就会得出「反向查不到」的错误结论：查不到的原因不是反向缺失，
// 而是既有可见性策略在起作用 —— 那是 M4 判据 12 验收过的单点，本 task 一格不改。
//
// 本文件同时钉住三条边界：条目仍恰五键、`type` 逐字 `replaced_by`、
// data 键集合不扩张（合同 §8.3），以及只读零副作用（不写一个字节到权威 Markdown）。

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// rvCard 造一张可带 `replaced_by` / `deleted_at` 的卡（脚手架，非产品写路径）。
//
// 语义方向逐字沿用 M3：`replaced_by` 写在**已失效**的那张卡上，读作「本卡已失效、被 target 取代」。
func rvCard(id, title, status, replacedTarget, replacedReason string, deleted bool) string {
	fm := "---\nid: " + id + "\nstatus: " + status +
		"\ncreated_at: '2026-12-01'\nupdated_at: '2026-12-05T10:00:00+08:00'" +
		"\ntitle: " + title + "\nsources: []\n"
	if replacedTarget != "" {
		fm += "replaced_by:\n  target: " + replacedTarget + "\n  reason: " + replacedReason + "\n"
	}
	if deleted {
		fm += "deleted_at: '2026-12-06T10:00:00+08:00'\ndeleted_reason: 用例造的逻辑删除\n"
	}
	return fm + "---\n\n# " + title + "\n\n## 知识内容\n\n正文占位。\n"
}

// rvScan 给出语料的全库扫描结果（取数层判据直接吃它，不经可见性层）。
func rvScan(t *testing.T, root string) []CardEntry {
	t.Helper()
	scan, err := VaultScan(root, ScanOptions{})
	if err != nil {
		t.Fatalf("VaultScan：%v", err)
	}
	return scan.Cards
}

// rvSig 把条目压成可逐格比对的签名（from|type|target|reason）。
func rvSig(edges []RelationEdge) []string {
	out := []string{}
	for _, e := range edges {
		out = append(out, e.From+"|"+e.Type+"|"+e.Target+"|"+e.Reason)
	}
	return out
}

// rvVaultAB 造最小语料：A（deprecated，replaced_by → B）+ B（active）。
func rvVaultAB(t *testing.T, deletedA bool) string {
	t.Helper()
	root := t.TempDir()
	k := "domains/ai-infra/knowledge/"
	bkWrite(t, root, k+"k-20261201-old.md", rvCard("k-20261201-old", "旧卡",
		"deprecated", "k-20261201-new", "结论已被新证据推翻", deletedA))
	bkWrite(t, root, k+"k-20261201-new.md", rvCard("k-20261201-new", "新卡", "active", "", "", false))
	return root
}

// —— 判据 13①：正反双向互相可见（取数层）——

func TestReplacedByReverseLookup(t *testing.T) {
	root := rvVaultAB(t, false)
	before := rvSnapshot(t, root)
	cards := rvScan(t, root)

	fwd := ReplacedByForward(root, cards, "k-20261201-old")
	wantFwd := []string{"k-20261201-old|replaced_by|k-20261201-new|结论已被新证据推翻"}
	if got := rvSig(fwd); !reflect.DeepEqual(got, wantFwd) {
		t.Fatalf("旧卡的正向（谁取代了我）= %v，期望 %v", got, wantFwd)
	}
	rev := ReplacedByReverse(root, cards, "k-20261201-new")
	if got := rvSig(rev); !reflect.DeepEqual(got, wantFwd) {
		t.Fatalf("新卡的反向（我取代了谁）= %v，期望与正向同一条记录 %v", got, wantFwd)
	}
	// 同一条记录、两个方向：五格逐字相同（记录只有一份，反向不是第二条记录）。
	if !SameEdgeOutput(fwd[0], rev[0]) {
		t.Fatalf("正反两个方向读出的不是同一条记录：%+v vs %+v", fwd[0], rev[0])
	}
	// 方向不可颠倒：新卡没有正向（没人取代它），旧卡没有反向（它没取代任何人）。
	if got := ReplacedByForward(root, cards, "k-20261201-new"); len(got) != 0 {
		t.Fatalf("新卡不应有正向替代指针，实际 %v", rvSig(got))
	}
	if got := ReplacedByReverse(root, cards, "k-20261201-old"); len(got) != 0 {
		t.Fatalf("旧卡不应有反向条目，实际 %v", rvSig(got))
	}
	// 自指不构造：即便有人写了自引用也不出现在任一方向（写路径已拦，读路径也不造）。
	self := t.TempDir()
	bkWrite(t, self, "domains/ai-infra/knowledge/k-20261201-self.md",
		rvCard("k-20261201-self", "自引用", "deprecated", "k-20261201-self", "自指", false))
	if got := ReplacedByReverse(self, rvScan(t, self), "k-20261201-self"); len(got) != 0 {
		t.Fatalf("自指不应出现在反向结果里，实际 %v", rvSig(got))
	}

	// 端到端（可见性层）：新卡的反向对端是 deprecated 的旧卡 —— 默认隐藏 + Q4，
	// 显式放开后逐字可见。这两件事**同时**成立才是 §8.4 与既有策略的正交。
	def, err := bkRel(root, RelRequest{ID: model.CardID("k-20261201-new"), ReplacedBy: true})
	if err != nil {
		t.Fatalf("rel --replaced-by：%v", err)
	}
	if len(def.Data.RelationsIn) != 0 || def.HiddenDeprecated != 1 {
		t.Fatalf("默认视图应隐藏 deprecated 对端并计 1 条，实际 in=%d hidden=%d",
			len(def.Data.RelationsIn), def.HiddenDeprecated)
	}
	opened, err := bkRel(root, RelRequest{ID: model.CardID("k-20261201-new"),
		ReplacedBy: true, IncludeDeprecated: true})
	if err != nil {
		t.Fatalf("rel --replaced-by --include-deprecated：%v", err)
	}
	if got := rvSig(opened.Data.RelationsIn); !reflect.DeepEqual(got, wantFwd) {
		t.Fatalf("显式放开后新卡的反向 = %v，期望 %v", got, wantFwd)
	}
	// data 键集合不扩张、条目仍恰五键（合同 §8.3）。
	if got, want := RelDataKeys(), []string{"id", "relations_out", "relations_in",
		"scanned_files", "skipped_files"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rel 的 data 键集合被替代指针视图改写：%v != %v", got, want)
	}
	rt := reflect.TypeOf(RelationEdge{})
	if rt.NumField() != 5 {
		t.Fatalf("关系条目不再是恰五键：%d 格", rt.NumField())
	}
	if opened.Data.RelationsIn[0].Type != EdgeTypeReplacedBy {
		t.Fatalf("条目 type 应逐字为 %q，实际 %q", EdgeTypeReplacedBy, opened.Data.RelationsIn[0].Type)
	}
	// 只读零副作用：全部权威文件的字节与调用前逐字相同（快照在本用例开头取）。
	if after := rvSnapshot(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("替代指针查询改写了权威 Markdown：\n before=%v\n after =%v", before, after)
	}
}

// rvSnapshot 取 vault 内全部 .md 的字节快照（只读判据的对照物）。
func rvSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(p) != ".md" {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatalf("取字节快照失败：%v", err)
	}
	return out
}

// —— 判据 13②：链式 A → B → C，中间那张卡两个方向各一条 ——

func TestReplacedByChainBothDirections(t *testing.T) {
	root := t.TempDir()
	k := "domains/ai-infra/knowledge/"
	bkWrite(t, root, k+"k-20261201-a.md", rvCard("k-20261201-a", "A 卡",
		"deprecated", "k-20261201-b", "A 被 B 取代", false))
	bkWrite(t, root, k+"k-20261201-b.md", rvCard("k-20261201-b", "B 卡",
		"deprecated", "k-20261201-c", "B 又被 C 取代", false))
	bkWrite(t, root, k+"k-20261201-c.md", rvCard("k-20261201-c", "C 卡", "active", "", "", false))
	cards := rvScan(t, root)

	// 中间卡 B：正向恰一条（谁取代了 B = C）、反向恰一条（B 取代了谁 = A）。
	if got, want := rvSig(ReplacedByForward(root, cards, "k-20261201-b")),
		[]string{"k-20261201-b|replaced_by|k-20261201-c|B 又被 C 取代"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("B 的正向 = %v，期望 %v", got, want)
	}
	if got, want := rvSig(ReplacedByReverse(root, cards, "k-20261201-b")),
		[]string{"k-20261201-a|replaced_by|k-20261201-b|A 被 B 取代"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("B 的反向 = %v，期望 %v", got, want)
	}
	// 链**不传递**：C 的反向只有 B，没有 A（一次查询只看一跳，跨跳由调用方自己走）。
	if got, want := rvSig(ReplacedByReverse(root, cards, "k-20261201-c")),
		[]string{"k-20261201-b|replaced_by|k-20261201-c|B 又被 C 取代"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("C 的反向 = %v，期望只含 B 这一跳 %v", got, want)
	}
	if got := ReplacedByForward(root, cards, "k-20261201-a"); len(got) != 1 {
		t.Fatalf("A 的正向应恰一条，实际 %v", rvSig(got))
	}

	// 端到端：B 的两个方向在 --include-deprecated 下同时出现，且各恰一条。
	res, err := bkRel(root, RelRequest{ID: model.CardID("k-20261201-b"),
		ReplacedBy: true, IncludeDeprecated: true})
	if err != nil {
		t.Fatalf("rel：%v", err)
	}
	if len(res.Data.RelationsOut) != 1 || len(res.Data.RelationsIn) != 1 {
		t.Fatalf("链中间卡应正反各一条，实际 out=%v in=%v",
			rvSig(res.Data.RelationsOut), rvSig(res.Data.RelationsIn))
	}
}

// —— 判据 13③：与逻辑删除维度正交（删除对端在任何 flag 下都隐藏）——

func TestReplacedByReverseOrthogonalToDeleted(t *testing.T) {
	root := rvVaultAB(t, true) // 旧卡同时被逻辑删除
	cards := rvScan(t, root)

	// 取数层：删除**不影响**记录本身能被读出（删除是可见性维度，不是取数维度）。
	if got := ReplacedByReverse(root, cards, "k-20261201-new"); len(got) != 1 {
		t.Fatalf("取数层应仍读出那条记录（删除只影响可见性），实际 %v", rvSig(got))
	}

	// 可见性层：对端已删除 ⇒ 任何 flag 下都隐藏，且**不计入** Q4 的 deprecated 计数。
	for _, incl := range []bool{false, true} {
		res, err := bkRel(root, RelRequest{ID: model.CardID("k-20261201-new"),
			ReplacedBy: true, IncludeDeprecated: incl})
		if err != nil {
			t.Fatalf("rel(include-deprecated=%t)：%v", incl, err)
		}
		if len(res.Data.RelationsIn) != 0 {
			t.Fatalf("include-deprecated=%t：对端已删除仍被展示 %v", incl, rvSig(res.Data.RelationsIn))
		}
		if res.HiddenDeprecated != 0 {
			t.Fatalf("include-deprecated=%t：已删除对端不应计入 deprecated 隐藏数，实际 %d",
				incl, res.HiddenDeprecated)
		}
		if n := pgCount(res.Diagnostics, CodeQ4); n != 0 {
			t.Fatalf("include-deprecated=%t：已删除对端不应触发 Q4，实际 %d 条", incl, n)
		}
	}
	// 反方向：旧卡自己（已删除 + deprecated）的正向对端是 active 的新卡 ⇒ 照常可见。
	// 这正是「四象限正交」：筛的是**对端**的状态，不是被查询卡自身的状态。
	res, err := bkRel(root, RelRequest{ID: model.CardID("k-20261201-old"), ReplacedBy: true})
	if err != nil {
		t.Fatalf("rel(old)：%v", err)
	}
	if len(res.Data.RelationsOut) != 1 {
		t.Fatalf("已删除卡的正向（对端 active）应照常可见，实际 %v", rvSig(res.Data.RelationsOut))
	}
}

// —— 判据 13④：deprecated 策略复用既有单点（默认隐藏 + Q4，显式放开才展示）——

func TestReplacedByReverseRespectsDeprecatedPolicy(t *testing.T) {
	root := rvVaultAB(t, false)
	id := model.CardID("k-20261201-new")

	def, err := bkRel(root, RelRequest{ID: id, ReplacedBy: true})
	if err != nil {
		t.Fatalf("rel 默认：%v", err)
	}
	if n := pgCount(def.Diagnostics, CodeQ4); n != 1 {
		t.Fatalf("默认隐藏 deprecated 对端应产恰一条 Q4，实际 %d 条：%v", n, def.Diagnostics)
	}
	if len(def.DeprecatedPeers) != 0 {
		t.Fatalf("未放开时不应有已展示的 deprecated 对端，实际 %v", def.DeprecatedPeers)
	}

	opened, err := bkRel(root, RelRequest{ID: id, ReplacedBy: true, IncludeDeprecated: true})
	if err != nil {
		t.Fatalf("rel --include-deprecated：%v", err)
	}
	if n := pgCount(opened.Diagnostics, CodeQ4); n != 0 {
		t.Fatalf("显式放开后不应再产 Q4，实际 %d 条", n)
	}
	if want := []string{"k-20261201-old"}; !reflect.DeepEqual(opened.DeprecatedPeers, want) {
		t.Fatalf("已展示的 deprecated 对端 = %v，期望 %v（渲染层据此标 [失效]）",
			opened.DeprecatedPeers, want)
	}
	// 可见性与分页正交：放开 deprecated 后再限量 1 条，仍恰 1 条、且分页事实自洽。
	paged, err := bkRel(root, RelRequest{ID: id, ReplacedBy: true, IncludeDeprecated: true,
		Page: PageSpec{Limit: 1}})
	if err != nil {
		t.Fatalf("rel --include-deprecated --limit 1：%v", err)
	}
	if n := len(paged.Data.RelationsOut) + len(paged.Data.RelationsIn); n != 1 {
		t.Fatalf("limit 1 应恰返回 1 条，实际 %d 条", n)
	}
	if paged.Page.Total != 1 || paged.Page.Truncated {
		t.Fatalf("可见 1 条时不应判截断：%+v", paged.Page)
	}
}
