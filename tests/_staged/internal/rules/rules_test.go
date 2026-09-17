package rules

import (
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// ---------- 收敛三维度判据（EG-CVG-01 宁拆勿并） ----------

func TestConvergeSplitsWhenAnyDimensionDiffers(t *testing.T) {
	cases := []Dims{
		{Core: Different, Conditions: Same, ReusePurpose: Same},
		{Core: Same, Conditions: Different, ReusePurpose: Same},
		{Core: Same, Conditions: Same, ReusePurpose: Different},
	}
	for i, d := range cases {
		if got := Converge(d); got != SplitTwoCards {
			t.Fatalf("用例 %d：任一维度不同必须拆两张卡，实际 %s", i, got)
		}
		if !Determined(d) {
			t.Fatalf("用例 %d：三维度齐全应视为判定有依据", i)
		}
	}
}

func TestConvergeReusesWhenAllSame(t *testing.T) {
	d := Dims{Core: Same, Conditions: Same, ReusePurpose: Same}
	if got := Converge(d); got != ReuseExisting {
		t.Fatalf("三维度全同才允许复用，实际 %s", got)
	}
}

func TestConvergeSplitsWhenUndetermined(t *testing.T) {
	cases := []Dims{
		{},
		{Core: Same},
		{Core: ParseVerdict("maybe"), Conditions: Same, ReusePurpose: Same},
	}
	for i, d := range cases {
		if Determined(d) {
			t.Fatalf("用例 %d：缺失 / 非法取值必须视为判不出", i)
		}
		if got := Converge(d); got != SplitTwoCards {
			t.Fatalf("用例 %d：判不出同样拆两张卡，实际 %s", i, got)
		}
	}
}

func TestConsistentWithDims(t *testing.T) {
	allSame := Dims{Core: Same, Conditions: Same, ReusePurpose: Same}
	oneDiff := Dims{Core: Different, Conditions: Same, ReusePurpose: Same}
	if !ConsistentWithDims("same_semantics", allSame) {
		t.Fatal("三维度全同应与 same_semantics 一致")
	}
	if ConsistentWithDims("core_change", allSame) {
		t.Fatal("三维度全同却填 core_change 属矛盾")
	}
	if !ConsistentWithDims("core_change", oneDiff) {
		t.Fatal("存在不同维度时 core_change 一致")
	}
	if ConsistentWithDims("same_semantics", oneDiff) {
		t.Fatal("存在不同维度却填 same_semantics 属矛盾")
	}
	if !ConsistentWithDims("core_change", Dims{Core: Same}) {
		t.Fatal("三维度不齐时不重复告警（一致性比对返回真）")
	}
}

// ---------- opposing 规范化与同对去重（EG-CVG-05，T-…-014；跨类型端点 T-…-006-B2c Phase 1） ----------

func TestOpposingNormalizesToLexicalSmallerEnd(t *testing.T) {
	small, big := model.RelationEndpoint("k-20260815-rnn"), model.RelationEndpoint("k-20260901-attention")
	for _, c := range []struct {
		from, target model.RelationEndpoint
		flipped      bool
	}{
		{from: small, target: big, flipped: false},
		{from: big, target: small, flipped: true},
	} {
		pair := Opposing(c.from, c.target)
		if pair.From != small || pair.Target != big {
			t.Fatalf("输入 (%s, %s)：规范方向应是 (%s, %s)，实得 (%s, %s)",
				c.from, c.target, small, big, pair.From, pair.Target)
		}
		if pair.Normalized != c.flipped {
			t.Fatalf("输入 (%s, %s)：Normalized 应为 %v（上层据此登记 W8）", c.from, c.target, c.flipped)
		}
	}
}

// TestOpposingNormalizesAcrossKinds：k / o 端点混连时，规范化只比 ID 全串字典序，
// 与端点是知识卡还是观点无关（k↔k / k↔o / o↔k / o↔o 四组合同一套规则）。
func TestOpposingNormalizesAcrossKinds(t *testing.T) {
	// "k-…" < "o-…"（'k' < 'o'），故任一 (k, o) 对的规范写入端恒为 k 端。
	kEnd := model.RelationEndpoint("k-20260901-attention")
	oEnd := model.RelationEndpoint("o-20260901-scaling")
	oSmall := model.RelationEndpoint("o-20260101-alpha")
	oBig := model.RelationEndpoint("o-20260901-scaling")
	for _, c := range []struct {
		name         string
		from, target model.RelationEndpoint
		wantFrom     model.RelationEndpoint
		wantTarget   model.RelationEndpoint
		flipped      bool
	}{
		{"k->o 已规范", kEnd, oEnd, kEnd, oEnd, false},
		{"o->k 需调换", oEnd, kEnd, kEnd, oEnd, true},
		{"o->o 已规范", oSmall, oBig, oSmall, oBig, false},
		{"o->o 需调换", oBig, oSmall, oSmall, oBig, true},
	} {
		pair := Opposing(c.from, c.target)
		if pair.From != c.wantFrom || pair.Target != c.wantTarget {
			t.Fatalf("%s：输入 (%s, %s) 规范方向应是 (%s, %s)，实得 (%s, %s)",
				c.name, c.from, c.target, c.wantFrom, c.wantTarget, pair.From, pair.Target)
		}
		if pair.Normalized != c.flipped {
			t.Fatalf("%s：Normalized 应为 %v", c.name, c.flipped)
		}
	}
}

func TestOpposingDuplicateIgnoresDirection(t *testing.T) {
	small, big := model.RelationEndpoint("k-20260815-rnn"), model.RelationEndpoint("k-20260901-attention")
	pair := Opposing(big, small)
	existing := []model.Relation{
		{Type: model.RelationLimits, Target: big, Reason: "另一类关系不算同对"},
	}
	if OpposingDuplicate(existing, pair) {
		t.Fatal("只有 opposing 才算同对")
	}
	// 历史上写成非规范方向的同对，也必须判为已存在（不产生第二条）
	for _, target := range []model.RelationEndpoint{small, big} {
		got := append(existing, model.Relation{Type: model.RelationOpposing, Target: target})
		if !OpposingDuplicate(got, pair) {
			t.Fatalf("同对判定与方向无关：target=%s 应判为已存在", target)
		}
	}
}

// TestOpposingDuplicateAcrossKinds：跨类型同对（k↔o）同样幂等——无论既有记录里
// 存的是 k 端还是 o 端，命中任一端都判为已存在，不产生第二条。
func TestOpposingDuplicateAcrossKinds(t *testing.T) {
	kEnd := model.RelationEndpoint("k-20260901-attention")
	oEnd := model.RelationEndpoint("o-20260901-scaling")
	pair := Opposing(oEnd, kEnd) // 规范化后 From=k、Target=o
	for _, stored := range []model.RelationEndpoint{kEnd, oEnd} {
		existing := []model.Relation{{Type: model.RelationOpposing, Target: stored}}
		if !OpposingDuplicate(existing, pair) {
			t.Fatalf("跨类型同对：既有 target=%s 应判为已存在（幂等，不产生第二条）", stored)
		}
	}
}

func TestOpposingSelfPairIsLeftToCaller(t *testing.T) {
	same := model.RelationEndpoint("k-20260901-attention")
	pair := Opposing(same, same)
	if pair.From != same || pair.Target != same || pair.Normalized {
		t.Fatalf("两端相同应原样返回，由上层拒绝自反关系：%+v", pair)
	}
}
