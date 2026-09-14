package proposal

// `status` × `execution` 的 4 × 3 可达矩阵（提案合同 §4.2、§9「可达矩阵 / approved 不等于已执行」
// 两行；T-evergreen.s1_main_flow-158614-036）。
//
// 用例名逐字采用 task Acceptance / verify 的约定形态：
//   - TestOrthogonality_ReachabilityMatrix   12 格全覆盖：✅ 恰 6 格，其余 6 格拒绝 + 零写入
//   - TestApprovedDoesNotImplySucceeded      批准后立即读盘，execution 仍是 not_started
//
// 矩阵**由算法生成**（4 × 3 全集），用例这一侧也不手写 12 行期望值：期望由同一份归约规则
// 独立算出（「只有 approved 行的三种执行结果全可达，其余三态只有 not_started 可达」），
// 两侧对不上就红 —— 手写枚举漏项在这里藏不住。

import (
	"testing"
)

// wantReachable 是矩阵期望值的**独立**归约（不复用产品代码的 CellFor）。
func wantReachable(st Status, ex ExecStatus) bool {
	return ex == ExecNotStarted || st == StatusApproved
}

// TestOrthogonality_ReachabilityMatrix 钉死 12 格：✅ 恰 6 格、拒绝恰 6 格，
// 每条拒绝都归到「非法迁移」诊断族（037 映射成 M3 那个既有 error 编号）、CLI 退 2、
// 且回写口对拒绝格**零写入**（磁盘字节逐字不变）。
func TestOrthogonality_ReachabilityMatrix(t *testing.T) {
	cells := ReachabilityMatrix()
	if want := len(Statuses()) * len(ExecStatuses()); len(cells) != want {
		t.Fatalf("矩阵 = %d 格，必须恰 %d 格（4 × 3 全集，不得漏项）", len(cells), want)
	}
	seen := map[string]bool{}
	okCount, newlyForbidden := 0, 0
	for _, c := range cells {
		key := string(c.Status) + "×" + string(c.Exec)
		if seen[key] {
			t.Fatalf("矩阵出现重复格 %s", key)
		}
		seen[key] = true
		if c.Reachable() != wantReachable(c.Status, c.Exec) {
			t.Fatalf("%s 判定 = %s，期望可达 = %v", key, c.Verdict, wantReachable(c.Status, c.Exec))
		}
		if c.Why == "" {
			t.Fatalf("%s 缺判定理由：报告要能说清为什么", key)
		}
		if Reachable(c.Status, c.Exec) != c.Reachable() {
			t.Fatalf("%s：Reachable 与 CellFor 结论不一致", key)
		}
		switch {
		case c.Reachable():
			okCount++
			if err := CheckReachable(c.Status, c.Exec); err != nil {
				t.Fatalf("%s 是 ✅ 格，CheckReachable 却报错：%v", key, err)
			}
		default:
			err := CheckReachable(c.Status, c.Exec)
			if err == nil {
				t.Fatalf("%s 是拒绝格，CheckReachable 必须报违规", key)
			}
			if kind := KindOf(err); kind != ViolationIllegalTransition {
				t.Fatalf("%s 的违规 kind = %q，期望 %q（与非法迁移同族，映射到同一个既有编号）",
					key, kind, ViolationIllegalTransition)
			}
			if got := DiagClassOf(KindOf(err)); got != DiagIllegalTransition {
				t.Fatalf("%s 的诊断族 = %q，期望 %q", key, got, DiagIllegalTransition)
			}
			if c.Verdict == VerdictNewlyForbidden {
				newlyForbidden++
			}
		}
	}
	if okCount != 6 {
		t.Fatalf("✅ = %d 格，必须恰 6 格", okCount)
	}
	if rejected := len(cells) - okCount; rejected != 6 {
		t.Fatalf("拒绝 = %d 格，必须恰 6 格", rejected)
	}
	// ⚠️「本合同新定禁止」恰一格：superseded × failed。
	if newlyForbidden != 1 {
		t.Fatalf("⚠️ 本合同新定禁止 = %d 格，必须恰 1 格", newlyForbidden)
	}
	if c := CellFor(StatusSuperseded, ExecFailed); c.Verdict != VerdictNewlyForbidden {
		t.Fatalf("%s × %s 应是本合同新定禁止的那一格，实得 %s", StatusSuperseded, ExecFailed, c.Verdict)
	}
	if ReachableCount() != okCount {
		t.Fatalf("ReachableCount = %d，与逐格统计 %d 不一致", ReachableCount(), okCount)
	}
	// 拒绝格对应的 CLI 行为：退 2（与 internal/cli 同值同语义）。
	if ExitCodeValidation != 2 {
		t.Fatalf("拒绝格的退出码 = %d，期望 2", ExitCodeValidation)
	}
	assertRejectedCellsWriteNothing(t)
}

// assertRejectedCellsWriteNothing 对每个拒绝格实跑一次回写：必须被拒且**磁盘零改动**。
//
// 每格都在真实 vault 上跑（真实 Markdown + guarded store），断言基于落盘字节。
func assertRejectedCellsWriteNothing(t *testing.T) {
	t.Helper()
	for _, c := range ReachabilityMatrix() {
		if c.Reachable() || c.Exec == ExecNotStarted {
			continue
		}
		key := string(c.Status) + "×" + string(c.Exec)
		t.Run(key, func(t *testing.T) {
			s, root := newFixtureVault(t)
			supersededBy := ID("")
			if c.Status == StatusSuperseded {
				supersededBy = fxNextID
				writeProposalFixture(t, root, fxNextID, StatusPending, fxTargets(), Impact{}, "")
			}
			before := writeProposalFixture(t, root, fxProposal, c.Status, fxTargets(),
				wantFixtureImpact(), supersededBy)
			led := newFullLedger(t, s)
			if c.Exec == ExecSucceeded {
				// succeeded 要求「无未写路径」：这里把全集标满，让**唯一**的违规理由是矩阵。
				for _, rel := range led.All() {
					if err := led.MarkWritten(rel); err != nil {
						t.Fatalf("MarkWritten：%v", err)
					}
				}
			}
			spec := RecordSpec{
				Store: s, ID: fxProposal, Status: c.Exec,
				AttemptedAt: fxStamp, Reason: "注入的失败原因", GitCommit: "0123456789abcdef",
				Ledger: led,
			}
			if _, err := RecordExecution(spec); err == nil {
				t.Fatalf("%s 是拒绝格，回写必须被拒", key)
			} else if KindOf(err) != ViolationIllegalTransition {
				t.Fatalf("%s 的拒写理由应是矩阵违规，实得 %v", key, err)
			}
			if got := mustBytes(t, root, Rel(fxProposal)); string(got) != string(before) {
				t.Fatalf("%s 被拒后提案字节发生了变化：拒绝格必须零写入", key)
			}
		})
	}
}

// TestApprovedDoesNotImplySucceeded 钉死正交的一半：**批准推不出已执行**。
//
// 走产品路径批准（MarkApproved）后**立即读盘**：status 已是 approved，
// 而 execution.status 仍是 not_started，两个路径数组仍是空数组。
func TestApprovedDoesNotImplySucceeded(t *testing.T) {
	s, root := newFixtureVault(t)
	writeProposalFixture(t, root, fxProposal, StatusPending, fxTargets(), wantFixtureImpact(), "")

	if _, err := MarkApproved(ApproveSpec{
		Store: s, Original: fxProposal, Reason: "用户已批准删除",
	}); err != nil {
		t.Fatalf("MarkApproved：%v", err)
	}
	got, _ := readProposal(t, s, fxProposal)
	if got.P.Status != StatusApproved {
		t.Fatalf("status = %s，期望 %s", got.P.Status, StatusApproved)
	}
	if got.P.Execution.Status != ExecNotStarted {
		t.Fatalf("批准后立即读盘，%s.%s = %s，必须仍是 %s（approved ≠ 已执行）",
			KeyExecBlock, KeyExecStatus, got.P.Execution.Status, ExecNotStarted)
	}
	for _, pair := range []struct {
		key   string
		items []string
	}{
		{KeyWrittenPaths, got.P.Execution.WrittenPaths},
		{KeyUnwrittenPaths, got.P.Execution.UnwrittenPaths},
	} {
		if len(pair.items) != 0 {
			t.Fatalf("批准不产生执行结果，%s 应仍为空，实得 %v", pair.key, pair.items)
		}
	}
	if got.P.Execution.AttemptedAt != "" || got.P.Execution.GitCommit != "" {
		t.Fatalf("批准不产生执行结果，%s / %s 应仍为空：%+v",
			KeyAttemptedAt, KeyGitCommit, got.P.Execution)
	}
	// 反向的另一半：执行结果不回写 status —— 记一次 succeeded 后 status 仍是 approved。
	led := newFullLedger(t, s)
	for _, rel := range led.All() {
		if err := led.MarkWritten(rel); err != nil {
			t.Fatalf("MarkWritten：%v", err)
		}
	}
	if _, err := RecordExecution(RecordSpec{
		Store: s, ID: fxProposal, Status: ExecSucceeded, AttemptedAt: fxStamp,
		GitCommit: "0123456789abcdef", Ledger: led,
	}); err != nil {
		t.Fatalf("RecordExecution（succeeded）：%v", err)
	}
	after, _ := readProposal(t, s, fxProposal)
	if after.P.Status != StatusApproved || after.P.Decision.Result != StatusApproved {
		t.Fatalf("执行成功不得回写 status / decision.result，实得 %s / %s",
			after.P.Status, after.P.Decision.Result)
	}
	if after.P.Execution.Status != ExecSucceeded {
		t.Fatalf("%s.%s = %s，期望 %s", KeyExecBlock, KeyExecStatus,
			after.P.Execution.Status, ExecSucceeded)
	}
	// 两维可达且互不推导：approved 行三格全可达。
	for _, ex := range ExecStatuses() {
		if !Reachable(StatusApproved, ex) {
			t.Fatalf("%s × %s 应可达", StatusApproved, ex)
		}
	}
}
