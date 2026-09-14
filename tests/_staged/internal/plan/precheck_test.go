package plan

// internal/plan/precheck_test.go —— 写前强校验策略层（precheck.go）的**纯策略反证**
// （M6 · T-evergreen.s1_main_flow-158614-074 批次 B2a；合同 §9 A-56）。
//
// 这里只钉 Precheck 这一个纯函数的三条语义边界，不碰盘、不碰 CLI 接线（那部分在
// internal/cli/precheck_wire_test.go 的真实写路径 e2e 里钉）：
//   ① strict==false / res==nil：恒零值（默认路径零漂移）；
//   ② strict==true：升级面（W1/W2/W3/W4/W6）命中即 Failed，且**只改 severity、Code 逐字保留**；
//   ③ strict==true：升级面外的 W* / I* 一律不受影响（不失败、不进升级清单）。

import "testing"

// warn 造一条指定码的 warning 诊断（Level 恒 warning，供升级前后对照）。
func warn(code, path string) Diagnostic {
	return Diagnostic{Code: code, Level: LevelWarning, Path: path, OpIndex: NonOp,
		Message: "precheck 单测语料：" + code}
}

// TestPrecheckDefaultPathZeroDrift —— ①：strict==false 或 res==nil 时恒零值。
//
// 这是「默认宽松」的机器形态：即便计划里挂着一堆升级面 warning，只要没开 --strict，
// Precheck 就必须一条都不升、一律放行 —— M1~M5 的既有行为一字不变。
func TestPrecheckDefaultPathZeroDrift(t *testing.T) {
	res := &Result{Warnings: []Diagnostic{warn("W4", "ops[0]"), warn("W2", "ops[1]")}}

	if pc := Precheck(res, false); pc.Failed || len(pc.Upgraded) != 0 {
		t.Fatalf("strict==false 必须零漂移（不失败、无升级），实得 %+v", pc)
	}
	if pc := Precheck(nil, true); pc.Failed || len(pc.Upgraded) != 0 {
		t.Fatalf("res==nil 必须返回零值，实得 %+v", pc)
	}
}

// TestPrecheckStrictUpgradesFiveCodesOnly —— ②③：strict 下恰升级面五码升 error、码不变，
// 其余 warning 全不受影响。
func TestPrecheckStrictUpgradesFiveCodesOnly(t *testing.T) {
	// 升级面五码各一条 + 两条面外码（W5 明确不在升级面；I1 是 info 段）。
	res := &Result{Warnings: []Diagnostic{
		warn("W1", "ops[0]"), warn("W2", "ops[1]"), warn("W3", "ops[2]"),
		warn("W4", "ops[3]"), warn("W6", "ops[4]"),
		warn("W5", "ops[5]"), warn("I1", "ops[6]"),
	}}

	pc := Precheck(res, true)
	if !pc.Failed {
		t.Fatal("strict 下命中升级面必须 Failed")
	}
	if len(pc.Upgraded) != 5 {
		t.Fatalf("升级清单应恰含升级面五码，实得 %d 条：%+v", len(pc.Upgraded), pc.Upgraded)
	}
	wantCodes := map[string]bool{"W1": true, "W2": true, "W3": true, "W4": true, "W6": true}
	for _, u := range pc.Upgraded {
		if !wantCodes[u.Code] {
			t.Fatalf("升级清单混入非升级面码 %q：%+v", u.Code, pc.Upgraded)
		}
		if u.Level != LevelError {
			t.Fatalf("被升级项 %q 的 Level 必须是 error（只改 severity），实得 %q", u.Code, u.Level)
		}
	}

	// 源 Result 不被改写（纯策略无副作用）：原 warnings 的 Level 全部仍是 warning。
	for _, w := range res.Warnings {
		if w.Level != LevelWarning {
			t.Fatalf("Precheck 不得改写调用方的 Result：%q 的 Level 被改成了 %q", w.Code, w.Level)
		}
	}
}

// TestPrecheckStrictNoUpgradeCodeDoesNotFail —— ③ 边界：strict 下若只有升级面外的 warning，
// 则不失败、升级清单为空（只读只报、不拦写）。
func TestPrecheckStrictNoUpgradeCodeDoesNotFail(t *testing.T) {
	res := &Result{Warnings: []Diagnostic{warn("W5", "ops[0]"), warn("W7", "ops[1]")}}
	if pc := Precheck(res, true); pc.Failed || len(pc.Upgraded) != 0 {
		t.Fatalf("升级面外的 warning 不得触发写前强校验失败，实得 %+v", pc)
	}
}
