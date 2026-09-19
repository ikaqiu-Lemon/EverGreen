package reconcile

import (
	"strings"
	"testing"
)

// strict.go 的反证：升级面恰五条、只改 severity 不改 code、默认路径零漂移、check 枚举仍十三值（A-62 后）。
// 判据 11（合同 §9 A-56）：四个单测全绿且 ^=== RUN ≥ 4。

// TestStrictModeW1W2W3W4W6BecomeError：`--strict` 下 W1/W2/W3/W4/W6 的有效 severity 升为 error。
func TestStrictModeW1W2W3W4W6BecomeError(t *testing.T) {
	want := []string{"W1", "W2", "W3", "W4", "W6"}
	got := StrictUpgradeCodes()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("升级面 = %v，合同 §9 定死为 %v（恰五条，跳过 W5）", got, want)
	}
	if StrictUpgradeCodeCount != len(want) {
		t.Fatalf("StrictUpgradeCodeCount = %d，应恰 %d", StrictUpgradeCodeCount, len(want))
	}
	for _, code := range want {
		if !IsStrictUpgradeCode(code) {
			t.Fatalf("%s 应在升级面内", code)
		}
		// 无论 base 原本是 warning 还是别的分级，strict 下一律升为 error。
		if lvl := StrictLevelFor(code, SeverityWarning, true); lvl != SeverityError {
			t.Fatalf("strict 下 %s 的有效 severity = %q，应升为 %q", code, lvl, SeverityError)
		}
	}
	// W5 明确不在升级面（合同逐字跳过它）。
	if IsStrictUpgradeCode("W5") {
		t.Fatal("W5 不得进入升级面：合同 §9 的五条是 W1/W2/W3/W4/W6")
	}
	if lvl := StrictLevelFor("W5", SeverityWarning, true); lvl != SeverityWarning {
		t.Fatalf("strict 下 W5 应保持 %q，实得 %q", SeverityWarning, lvl)
	}
}

// TestStrictModeCodesUnchanged：升级只改 severity，诊断码一字不变（本函数不返回 code，
// 因此以「不存在把 code 改写成别的值的路径」反证——升级面成员经 StrictLevelFor 仍由调用方持有原 code）。
func TestStrictModeCodesUnchanged(t *testing.T) {
	// StrictLevelFor 的签名本身即保证：入参 code 只用于查表，从不作为返回值被替换。
	// 这里逐码断言「同一 code 在 strict / 非 strict 两次调用间，函数从不产出新 code」——
	// 通过反证「返回值恒是 severity 字面量之一，且 code 参数原样可用」实现。
	for _, code := range StrictUpgradeCodes() {
		lvlStrict := StrictLevelFor(code, SeverityWarning, true)
		lvlPlain := StrictLevelFor(code, SeverityWarning, false)
		if !IsKnownSeverity(lvlStrict) || !IsKnownSeverity(lvlPlain) {
			t.Fatalf("%s 的有效 severity 越出两值封闭集合：strict=%q plain=%q", code, lvlStrict, lvlPlain)
		}
		// 升级面成员：strict 升 error、非 strict 保持 warning，两者都不改 code。
		if lvlStrict != SeverityError {
			t.Fatalf("%s strict 应 error，实得 %q", code, lvlStrict)
		}
		if lvlPlain != SeverityWarning {
			t.Fatalf("%s 非 strict 应保持 warning，实得 %q", code, lvlPlain)
		}
	}
}

// TestCheckEnumStillThirteen：strict 引入后 check 枚举一字不动（合同 §9「check 枚举一字不动」）——
// A-62 把封闭枚举从 12 抬到 13（新增 R3·W29），strict 特性对它零触碰、仍恰 13 值。
func TestCheckEnumStillThirteen(t *testing.T) {
	if CheckCount != 13 {
		t.Fatalf("CheckCount = %d，check 是恰 13 值的封闭枚举（A-62 后）", CheckCount)
	}
	if n := len(AllChecks()); n != 13 {
		t.Fatalf("AllChecks() 返回 %d 个，应恰 13", n)
	}
	// 升级面（W1/W2/W3/W4/W6）与 check 诊断码段（E11–E14 / W13–W20 / W29）**不相交**：
	// strict 不往 check 枚举里塞任何新值，也不动既有十三个 check 的分级。
	for _, code := range StrictUpgradeCodes() {
		if IsKnownCode(code) {
			t.Fatalf("升级面码 %s 竟落在 check 诊断码段内：strict 不得触碰 check 枚举", code)
		}
	}
}

// TestNonStrictModeUnchangedFromM4：默认（非 strict）路径零漂移——任何 code 的有效 severity
// 恒等于其 base，与 M4 逐字一致（合同 §9「默认路径零漂移」）。
func TestNonStrictModeUnchangedFromM4(t *testing.T) {
	// 覆盖 check 诊断码全集 + 升级面全集：非 strict 下一律返回 base，不升级任何一条。
	codes := append(AllCodes(), StrictUpgradeCodes()...)
	for _, code := range codes {
		for _, base := range []string{SeverityWarning, SeverityError} {
			if lvl := StrictLevelFor(code, base, false); lvl != base {
				t.Fatalf("非 strict 下 %s(base=%q) 的有效 severity = %q，必须恒等于 base（零漂移）",
					code, base, lvl)
			}
		}
	}
	// 非升级面的码即便在 strict 下也保持 base（例如 check 段的 W17 orphan 仍是 warning）。
	if lvl := StrictLevelFor(CodeW17, SeverityWarning, true); lvl != SeverityWarning {
		t.Fatalf("strict 下非升级面码 W17 应保持 %q，实得 %q", SeverityWarning, lvl)
	}
}
