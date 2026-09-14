package model

// U-05「`status` 不得出现第三值」的反证（授权合同 §6 U-05；EG-KNW-01 / T-KNW-01）。
//
// 这条边界必须由**解析层**兜住，而不是靠调用方自觉：只要 ParseStatus 肯收
// `candidate`，「用逻辑删除表达不确定」的整套设计就会被一个字段悄悄绕开。

import "testing"

// TestStatus_OnlyTwoValues 断言 status 恰两值，`candidate` 等第三值解析失败。
func TestStatus_OnlyTwoValues(t *testing.T) {
	if n := len(ValidStatuses()); n != 2 {
		t.Fatalf("status 恰两值（active / deprecated），实得 %d：%v", n, ValidStatuses())
	}
	for _, s := range ValidStatuses() {
		got, err := ParseStatus(string(s))
		if err != nil || got != s {
			t.Fatalf("合法状态 %q 必须可解析，实得 (%q, %v)", s, got, err)
		}
	}
	// 第三值一律**主动拒绝**（合同 §1.3：不是靠约定回避，而是解析层报错）。
	for _, raw := range []string{"candidate", "draft", "archived", "Active", ""} {
		if got, err := ParseStatus(raw); err == nil {
			t.Fatalf("status=%q 必须解析失败（U-05），实得 %q", raw, got)
		}
	}
	if Status("candidate").Valid() {
		t.Fatal("Status(\"candidate\").Valid() 必须为 false")
	}
}
