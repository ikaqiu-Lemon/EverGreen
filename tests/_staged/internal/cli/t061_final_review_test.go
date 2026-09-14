package cli

// [M4 / T-…-061 最终审查②③] 两组定向补测：
//
//	② eg rel --to <deprecated-peer> 的正交性修复：--to 收窄对端、--include-deprecated 放开
//	   可见性，两者正交。默认视图对端 deprecated 被隐藏 → 结果为空且产恰一条 Q4（N≥1）；
//	   加 --include-deprecated 后显示该边且**无** Q4。修复前 RelView 在 req.To != "" 分支
//	   无条件 hidden=0，导致定向查询被隐藏却不报 Q4，违反 Q4 的 N≥1 条件与正交性。
//	③ --include-deprecated 是只读可见性 flag：注册在 rel 父命令上服务读路径，
//	   但绝不作用于写子命令。rel add / rel remove 传入该 flag → 退 1、零写入、零 commit，
//	   且错误逐字点名该 flag。

import (
	"strings"
	"testing"
)

// —— ② --to <deprecated> 与 --include-deprecated 正交 ——

// relToLen 从 rel --json 信封里取 relations_out 的条目数。
func relToLen(t *testing.T, env map[string]interface{}) int {
	t.Helper()
	data, _ := env["data"].(map[string]interface{})
	edges, _ := data["relations_out"].([]interface{})
	return len(edges)
}

// TestRelToDeprecatedPeerOrthogonal —— matrixVault 里 k-20260901-a 正向 opposing 一张
// deprecated 卡 k-20260903-c；用 --to 精确收窄到该 deprecated 对端：
//
//	默认视图：relations_out 为空（对端 deprecated 被隐藏）+ 恰一条 Q4；
//	--include-deprecated：relations_out 恰一条（显示该边）+ **无** Q4。
func TestRelToDeprecatedPeerOrthogonal(t *testing.T) {
	dir := matrixVault(t)
	const hub, dep = "k-20260901-a", "k-20260903-c"

	// 默认：--to 收窄到 deprecated 对端 → 空结果 + Q4。
	code, env, errOut := runRelJSON(t, dir, hub, "--to", dep)
	if code != ExitOK {
		t.Fatalf("只读查询应退 0，实际 %d（%s）", code, errOut)
	}
	if n := relToLen(t, env); n != 0 {
		t.Fatalf("默认视图 --to <deprecated> 应把对端隐藏（relations_out 空），实际 %d 条", n)
	}
	if got := countStr(warnCodes(t, env), "Q4"); got != 1 {
		t.Fatalf("默认视图 --to <deprecated> 隐藏了对端应产恰一条 Q4（N≥1），实际 Q4=%d，诊断 %v",
			got, warnCodes(t, env))
	}

	// --include-deprecated：显示该边 + 无 Q4。
	code, envInc, errOut := runRelJSON(t, dir, hub, "--to", dep, visFlag)
	if code != ExitOK {
		t.Fatalf("include 只读查询应退 0，实际 %d（%s）", code, errOut)
	}
	if n := relToLen(t, envInc); n != 1 {
		t.Fatalf("--include-deprecated 下 --to <deprecated> 应显示该边（relations_out 恰 1），实际 %d 条", n)
	}
	if got := countStr(warnCodes(t, envInc), "Q4"); got != 0 {
		t.Fatalf("--include-deprecated 放开可见性后不得再产 Q4，实际 Q4=%d，诊断 %v",
			got, warnCodes(t, envInc))
	}
}

// —— ③ --include-deprecated 不得作用于写子命令 rel add / rel remove ——

// TestRelAddRejectsReadOnlyVisibilityFlag —— rel add 传入 --include-deprecated →
// 退 1、错误点名该 flag、零写入零 commit（在参数形态校验阶段就被拦下，早于任何 plan/store 写口）。
// 走人类可读路径（非 --json），使 UsageError 文案落到 stderr 便于逐字断言。
func TestRelAddRejectsReadOnlyVisibilityFlag(t *testing.T) {
	dir := relAddVault(t)
	before := snapshot(t, dir)
	logBefore := gitLogCount(t, dir)

	r := newTestRoot(t, dir)
	code, out, errOut := runCLI(t, r, "--vault", dir, "rel", "add",
		relAddCardA, "supports", relAddCardB, "--reason", "本不该写入", visFlag)
	if code != ExitUsage {
		t.Fatalf("rel add 带 %s 应退 1（只读 flag 不作用于写路径），实际 %d（%s / %s）",
			visFlag, code, out, errOut)
	}
	if !strings.Contains(out+errOut, "include-deprecated") {
		t.Fatalf("拒绝 rel add 的错误应逐字点名 include-deprecated：%s / %s", out, errOut)
	}
	if after := snapshot(t, dir); after != before {
		t.Fatalf("rel add 被拒绝必须零写入：\n%s\n%s", before, after)
	}
	if got := gitLogCount(t, dir); got != logBefore {
		t.Fatalf("rel add 被拒绝必须零 commit：%d → %d", logBefore, got)
	}
}

// TestRelRemoveRejectsReadOnlyVisibilityFlag —— rel remove 传入 --include-deprecated →
// 退 1、错误点名该 flag、零写入零 commit。
func TestRelRemoveRejectsReadOnlyVisibilityFlag(t *testing.T) {
	dir := relRemoveVault(t)
	before := snapshot(t, dir)
	logBefore := gitLogCount(t, dir)

	r := newTestRoot(t, dir)
	code, out, errOut := runCLI(t, r, "--vault", dir, "rel", "remove",
		relAddCardA, "limits", relAddCardB, "--reason", "本不该改动", visFlag)
	if code != ExitUsage {
		t.Fatalf("rel remove 带 %s 应退 1（只读 flag 不作用于写路径），实际 %d（%s / %s）",
			visFlag, code, out, errOut)
	}
	if !strings.Contains(out+errOut, "include-deprecated") {
		t.Fatalf("拒绝 rel remove 的错误应逐字点名 include-deprecated：%s / %s", out, errOut)
	}
	if after := snapshot(t, dir); after != before {
		t.Fatalf("rel remove 被拒绝必须零写入：\n%s\n%s", before, after)
	}
	if got := gitLogCount(t, dir); got != logBefore {
		t.Fatalf("rel remove 被拒绝必须零 commit：%d → %d", logBefore, got)
	}
}
