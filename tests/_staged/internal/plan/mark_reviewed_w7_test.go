package plan

// A-15 窄口径的机器判据：`mark_reviewed` 缺 `initiator=user` 时 W7 只判 **warning**，
// 不与 `deprecate`/`restore`/`set_replaced_by`/`delete`/`undelete` 五个状态类 op 同级
// （授权合同 §9 A-15 / N-6）。
//
// 本文件只锁「W7 的分级」这一件事。**能不能写**是另一件事：矩阵 #7 的 P-A 格是 🔴，
// 所以缺 initiator 的 mark_reviewed 仍会被 E6 拦下——那是矩阵判的，不是 W7 判的。
// 两件事混成一件，A-15 就会被静悄悄地改写成「反正也退 2，干脆升成 error」。

import "testing"

// TestMarkReviewed_W7IsWarningNotError 三段断言：
//
//	① mark_reviewed 缺 initiator → W7 是 warning，且**没有任何 W7 error**；
//	② 同一形态的 deprecate 缺 initiator → W7 是 error（退 2、零写入），
//	   两者的分级必须不同，A-15 才算落地；
//	③ mark_reviewed 授权表达完整（initiator=user + 命令行佐证）→ 零 error、零 W7，
//	   即「W7 只在授权表达缺失时发声」，不会常态污染报告。
func TestMarkReviewed_W7IsWarningNotError(t *testing.T) {
	files := m3Files()
	const target = `"target":"k-20260901-attention"`

	// ① mark_reviewed 缺 initiator：W7 只能是 warning。
	res := m3Run(t, files, `{"op":"mark_reviewed",`+target+`}`)
	if d, ok := find(res.Errors, W7); ok {
		t.Fatalf("A-15：mark_reviewed 缺 initiator 不得判 W7 error，实得 %+v", d)
	}
	w7, ok := find(res.Warnings, W7)
	if !ok {
		t.Fatalf("期望 mark_reviewed 的 W7 warning，实际 warnings=%v", codes(res.Warnings))
	}
	if w7.Level != LevelWarning {
		t.Fatalf("mark_reviewed 的 W7 级别 = %q，期望 %q", w7.Level, LevelWarning)
	}
	if IsStateOp(OpMarkReviewed) {
		t.Fatalf("%s 不得进入「W7 升 error」的状态类 op 集合 %v", OpMarkReviewed, StateOpNames())
	}
	// 缺 initiator 时唯一可能的 error 是矩阵 #7（P-A 🔴）判的 E6：这条 op 零写入，
	// 但它与 W7 的分级无关——不得因为「反正退 2」就把 W7 升成 error。
	for _, d := range res.Errors {
		if d.Code != E6 {
			t.Fatalf("缺 initiator 的 mark_reviewed 只可能被矩阵（E6）拦下，实得 %v", codes(res.Errors))
		}
	}

	// ② 反向：deprecate 缺 initiator → W7 是 error（五个状态类 op 的分级）。
	resDep := m3Run(t, files, `{"op":"deprecate",`+target+`,"reason":"结论已被新证据取代"}`)
	depErr, ok := find(resDep.Errors, W7)
	if !ok {
		t.Fatalf("期望 deprecate 缺 initiator 的 W7 error，实际 errors=%v", codes(resDep.Errors))
	}
	if depErr.Level != LevelError {
		t.Fatalf("deprecate 的 W7 级别 = %q，期望 %q", depErr.Level, LevelError)
	}
	if _, warned := find(resDep.Warnings, W7); warned {
		t.Fatal("deprecate 的 W7 只能以 error 形态出现，不得同时留一条 warning")
	}
	if len(resDep.Actions) != 0 {
		t.Fatalf("W7 error 必须零写入：期望 0 条 action，实得 %d", len(resDep.Actions))
	}
	if !IsStateOp(OpDeprecate) {
		t.Fatalf("%s 必须在状态类 op 集合 %v 内", OpDeprecate, StateOpNames())
	}

	// ③ 授权表达完整（P-U）：零 error、零 W7；reviewed_at 的落盘由 eg mark-reviewed 承接，
	// 因此这里也不产出 action（校验通过 ≠ 本次写盘）。
	resOK := m3Run(t, files, `{"op":"mark_reviewed",`+target+`,"initiator":"user"}`)
	if len(resOK.Errors) != 0 {
		t.Fatalf("P-U 下的 mark_reviewed 必须零 error，实得 %v", codes(resOK.Errors))
	}
	if _, ok := find(resOK.Warnings, W7); ok {
		t.Fatalf("授权表达完整时不得再报 W7，实际 warnings=%v", codes(resOK.Warnings))
	}
}
