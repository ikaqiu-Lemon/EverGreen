package cli

// `eg proposal approve <id> --confirm` 的命令侧机器判据（T-…-040 第三层 Acceptance）。
//
// 三组断言：
//   ① 缺 --confirm → 退 6，且**权威 Markdown 完全不变**（提案字节、git 工作区、git log 逐字相同）；
//   ② 有 --confirm → 退 0 且 status: approved 真实落盘；**用例以 stdin 关闭的方式运行**，
//      反证确认点恰 1 处（EG-CFM-05）：approve 之后任何再次读 stdin 都会让用例立刻红；
//   ③ U-12：缺 --user-request 的 Agent 路径 → 退 2 且零写入（N-1：文件内容不能自证）。

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/proposal"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// errClosedStdin 模拟**已关闭**的标准输入：任何读取都立刻失败。
// 用它当 r.In，「确认点恰 1 处」就成了可执行断言而不是代码评审结论。
var errClosedStdin = errors.New("stdin 已关闭：approve 之后不得再次读取标准输入（EG-CFM-05）")

type closedStdin struct{}

func (closedStdin) Read([]byte) (int, error) { return 0, errClosedStdin }

// runApproveCLI 跑一次 `eg proposal …`，**stdin 恒为已关闭状态**。
// 第三个返回值是 stdout + stderr 的合并文本：--json 模式下错误落在信封里，
// 断言文案时两处都要看，才不会因渲染出口不同而漏判。
func runApproveCLI(t *testing.T, dir string, args ...string) (int, Envelope, string) {
	t.Helper()
	setGitIdentity(t)
	r := newTestRoot(t, dir)
	r.Now = func() time.Time { return captureAt(t) }
	r.In = closedStdin{}
	full := append([]string{"proposal"}, args...)
	full = append(full, "--vault", dir, "--json")
	code, out, errOut := runCLI(t, r, full...)
	var env Envelope
	if strings.TrimSpace(out) != "" {
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("--json 输出不可解析：%v\n%s", err, out)
		}
	}
	return code, env, out + errOut
}

// —— ① 缺 --confirm → 退 6，权威 Markdown 完全不变 ——

func TestApprove_MissingConfirmExitsSixAndChangesNothing(t *testing.T) {
	dir := proposalVault(t)
	rel := newProposalOnce(t, dir)
	id := strings.TrimSuffix(filepath.Base(rel), ".md")
	abs := filepath.Join(dir, filepath.FromSlash(rel))

	lastReportAbs := filepath.Join(dir, filepath.FromSlash(LastReportFile))
	beforeBytes := string(mustRead(t, abs))
	beforeReport := string(mustRead(t, lastReportAbs))
	beforePorcelain := gitOut(t, dir, "status", "--porcelain")
	beforeLog := gitOut(t, dir, "log", "--oneline", "-1")
	beforeCount := gitLogCount(t, dir)

	code, _, output := runApproveCLI(t, dir, "approve", id, "--user-request")
	if code != ExitNeedConfirm {
		t.Fatalf("缺 --confirm 的退出码 = %d，期望 %d：%s", code, ExitNeedConfirm, output)
	}
	if !strings.Contains(output, "--confirm") {
		t.Fatalf("stderr 必须提示补 --confirm，实际：%s", output)
	}
	if got := string(mustRead(t, abs)); got != beforeBytes {
		t.Fatalf("提案字节发生变化：退出码 6 要求权威 Markdown 完全不变\n前：%q\n后：%q", beforeBytes, got)
	}
	if got := gitOut(t, dir, "status", "--porcelain"); got != beforePorcelain {
		t.Fatalf("git status --porcelain 变化：%q → %q", beforePorcelain, got)
	}
	if got := gitOut(t, dir, "log", "--oneline", "-1"); got != beforeLog {
		t.Fatalf("git log -1 变化：%q → %q", beforeLog, got)
	}
	if got := gitLogCount(t, dir); got != beforeCount {
		t.Fatalf("commit 数 %d → %d：退出码 6 不得产生 commit", beforeCount, got)
	}
	// last-report 同样不得被本次调用改写（它也是一次写入；此处比对字节而非存在性，
	// 因为前置的 eg proposal new 已经合法地留下过一份）。
	if got := string(mustRead(t, lastReportAbs)); got != beforeReport {
		t.Fatalf("缺 --confirm 时不得改写 %s：本次调用必须零写痕", LastReportFile)
	}
}

// —— ② --confirm 之后不再读 stdin：stdin 关闭仍必须退 0 ——

func TestApprove_NoSecondConfirmation(t *testing.T) {
	dir := proposalVault(t)
	rel := newProposalOnce(t, dir)
	id := strings.TrimSuffix(filepath.Base(rel), ".md")

	code, env, output := runApproveCLI(t, dir, "approve", id, "--confirm", "--user-request")
	if code != ExitOK {
		t.Fatalf("approve --confirm（stdin 已关闭）退出码 = %d，期望 0：%s", code, output)
	}
	if strings.Contains(output, errClosedStdin.Error()) {
		t.Fatalf("approve 之后仍读取了 stdin：确认点必须恰 1 处（EG-CFM-05）")
	}
	pf, err := proposal.Parse(mustRead(t, filepath.Join(dir, filepath.FromSlash(rel))))
	if err != nil {
		t.Fatalf("批准后的提案不可解析：%v", err)
	}
	if pf.P.Status != proposal.StatusApproved {
		t.Fatalf("status = %q，期望 %q（approved 必须真实落盘）", pf.P.Status, proposal.StatusApproved)
	}
	if pf.P.Decision.Result != proposal.StatusApproved {
		t.Fatalf("decision.result = %q，期望与 status 逐字一致", pf.P.Decision.Result)
	}
	// 阶段边界：批准 ≠ 已执行（execution 三态属 T-…-036，逐文件写入属 T-…-041）。
	if pf.P.Execution.Status != proposal.ExecNotStarted {
		t.Fatalf("execution.status = %q，期望 %q（本层不执行删除）",
			pf.P.Execution.Status, proposal.ExecNotStarted)
	}
	rep := applyReport(t, env)
	if len(rep.Proposals) != 1 {
		t.Fatalf("报告 proposals[] 必须恰 1 条真实值，实际 %d 条", len(rep.Proposals))
	}
	if rep.Proposals[0].Status != string(proposal.StatusApproved) {
		t.Fatalf("proposals[0].status = %q，期望 approved", rep.Proposals[0].Status)
	}
}

// —— ③ U-12：Agent 路径（无 --user-request 命令行佐证）→ 退 2 且零写入 ——

func TestApprove_AgentPathDeniedWithoutUserRequest(t *testing.T) {
	dir := proposalVault(t)
	rel := newProposalOnce(t, dir)
	id := strings.TrimSuffix(filepath.Base(rel), ".md")
	abs := filepath.Join(dir, filepath.FromSlash(rel))

	beforeBytes := string(mustRead(t, abs))
	beforeCount := gitLogCount(t, dir)

	// 带 --confirm 但**没有** --user-request：U-12 仍必须拒绝（确认参数不能替代授权佐证）。
	code, _, output := runApproveCLI(t, dir, "approve", id, "--confirm")
	if code != ExitValidation {
		t.Fatalf("Agent 路径 approve 的退出码 = %d，期望 %d：%s", code, ExitValidation, output)
	}
	if !strings.Contains(output, "U-12") {
		t.Fatalf("stderr 必须点名 U-12，实际：%s", output)
	}
	if got := string(mustRead(t, abs)); got != beforeBytes {
		t.Fatalf("U-12 拒绝时必须零写入，提案字节却变了")
	}
	if got := gitLogCount(t, dir); got != beforeCount {
		t.Fatalf("U-12 拒绝时不得产生 commit：%d → %d", beforeCount, got)
	}
}

// —— ④ S-②：前提已变 → 生成新提案 + 原提案 superseded + **不执行** ——

// TestApprove_ImpactChangedSupersedesWithoutExecuting 钉住 §10.2 第 8 步的改判分支在
// **命令侧**也接通：批准前重算发现前提已变，则不批准、不执行，只改判并生成新提案。
//
// 造「前提已变」的办法是直接给目标卡写上 deleted_at：它一旦逻辑删除，
// exits_default_view 就不再包含它 —— 重算结果与提案记录的 impact 不再一致。
func TestApprove_ImpactChangedSupersedesWithoutExecuting(t *testing.T) {
	dir := proposalVault(t)
	rel := newProposalOnce(t, dir)
	id := strings.TrimSuffix(filepath.Base(rel), ".md")

	cardAbs := filepath.Join(dir, filepath.FromSlash(store.CardRel("ai-infra", applyCardID)))
	raw := string(mustRead(t, cardAbs))
	const statusLine = "status: 'active'"
	changed := strings.Replace(raw, statusLine,
		statusLine+"\ndeleted_at: '2026-09-02T10:00:00+08:00'\ndeleted_reason: '语料已过时'", 1)
	if changed == raw {
		t.Fatalf("夹具前提不成立：卡片里没有 %s 可供改写", statusLine)
	}
	if err := os.WriteFile(cardAbs, []byte(changed), 0o644); err != nil {
		t.Fatalf("改写夹具：%v", err)
	}

	code, env, output := runApproveCLI(t, dir, "approve", id, "--confirm", "--user-request")
	if code != ExitOK {
		t.Fatalf("改判分支退出码 = %d，期望 0：%s", code, output)
	}
	pf, err := proposal.Parse(mustRead(t, filepath.Join(dir, filepath.FromSlash(rel))))
	if err != nil {
		t.Fatalf("原提案不可解析：%v", err)
	}
	if pf.P.Status != proposal.StatusSuperseded {
		t.Fatalf("原提案 status = %q，期望 %q（前提已变 → 改判，不得批准）",
			pf.P.Status, proposal.StatusSuperseded)
	}
	if pf.P.Decision.SupersededBy == "" {
		t.Fatal("decision.superseded_by 必须指向新提案（提案链不得断裂）")
	}
	rep := applyReport(t, env)
	if len(rep.Proposals) != 2 {
		t.Fatalf("报告 proposals[] 必须含新旧两条（数量守恒），实际 %d 条：%v",
			len(rep.Proposals), rep.Proposals)
	}
}
