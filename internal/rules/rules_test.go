package rules

import "testing"

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
