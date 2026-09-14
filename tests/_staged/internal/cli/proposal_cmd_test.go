package cli

// `eg proposal` 四条子命令的机器判据（T-…-040 Acceptance）。
//
// 覆盖：new 允许 Agent 调用 / 影响面四项 / W9 缺项照常创建、reject 的 status 与
// decision.result 一致、list 与 show 的零副作用、子命令集合恰五个、U-13 提案不阻塞。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/proposal"
)

// runProposalCLI 在 dir 这个 vault 上跑一次 `eg proposal …`（--json 信封）。
func runProposalCLI(t *testing.T, dir string, args ...string) (int, Envelope, string) {
	t.Helper()
	setGitIdentity(t)
	r := newTestRoot(t, dir)
	r.Now = func() time.Time { return captureAt(t) }
	full := append([]string{"proposal"}, args...)
	full = append(full, "--vault", dir, "--json")
	code, out, errOut := runCLI(t, r, full...)
	var env Envelope
	if strings.TrimSpace(out) != "" {
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("--json 输出不可解析：%v\n%s", err, out)
		}
	}
	return code, env, errOut
}

// proposalVault 建一个含一张卡的 vault（提案的删除目标）。
func proposalVault(t *testing.T) string {
	t.Helper()
	dir := applyVault(t)
	applyNoteAndCard(t, dir)
	return dir
}

// newProposalOnce 跑一次 `eg proposal new`，返回落盘的相对路径。
func newProposalOnce(t *testing.T, dir string) string {
	t.Helper()
	code, env, errOut := runProposalCLI(t, dir,
		"new", "--type", string(proposal.TypeLogicalDelete), "--target", applyCardID,
		"--reason", "该卡已过时")
	if code != ExitOK {
		t.Fatalf("eg proposal new 退出码 = %d，期望 0：%s", code, errOut)
	}
	rep := applyReport(t, env)
	if len(rep.Proposals) != 1 {
		t.Fatalf("报告 proposals[] 必须恰 1 条真实值，实际 %d 条：%v", len(rep.Proposals), rep.Proposals)
	}
	return rep.Proposals[0].Path
}

// —— ① new：Agent 可调用，落盘 pending / not_started，恰一次 proposal( commit ——

func TestProposalNew_AgentAllowed(t *testing.T) {
	dir := proposalVault(t)
	before := gitLogCount(t, dir)

	// 不带 --user-request：§10.3「可提不可执」——提案的创建对 Agent 开放。
	rel := newProposalOnce(t, dir)

	raw := mustRead(t, filepath.Join(dir, filepath.FromSlash(rel)))
	pf, err := proposal.Parse(raw)
	if err != nil {
		t.Fatalf("落盘的提案不可解析：%v", err)
	}
	if pf.P.Status != proposal.StatusPending {
		t.Fatalf("status = %q，期望 %q", pf.P.Status, proposal.StatusPending)
	}
	if pf.P.Execution.Status != proposal.ExecNotStarted {
		t.Fatalf("execution.status = %q，期望 %q", pf.P.Execution.Status, proposal.ExecNotStarted)
	}
	if got := gitLogCount(t, dir) - before; got != 1 {
		t.Fatalf("commit 数 = %d，期望恰 1 次", got)
	}
	subject := strings.TrimSpace(gitOut(t, dir, "log", "-1", "--pretty=%s"))
	if !strings.HasPrefix(subject, "proposal(") {
		t.Fatalf("commit 主题 = %q，期望以 %q 开头（verb = proposal）", subject, "proposal(")
	}
}

// —— ② new：影响面四项（+ stale_reviews）全部落盘 ——

func TestProposalNew_ImpactFourFields(t *testing.T) {
	dir := proposalVault(t)
	rel := newProposalOnce(t, dir)
	raw := string(mustRead(t, filepath.Join(dir, filepath.FromSlash(rel))))
	for _, key := range []string{
		proposal.KeyExitsDefaultView, proposal.KeyCardsLosingSupport,
		proposal.KeyAffectedMaterialRels, proposal.KeyAffectedRelations,
	} {
		if !strings.Contains(raw, "  "+key+":") {
			t.Fatalf("impact 缺子字段 %s：\n%s", key, raw)
		}
	}
	pf, err := proposal.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	// 目标卡在库且未被删除 → 它必须出现在 exits_default_view（口径由 RecomputeImpact 给）。
	if len(pf.P.Impact.ExitsDefaultView) != 1 || pf.P.Impact.ExitsDefaultView[0] != applyCardID {
		t.Fatalf("exits_default_view = %v，期望恰 [%s]", pf.P.Impact.ExitsDefaultView, applyCardID)
	}
}

// —— ③ W9：十项必备缺项照常创建，只进 warnings[] ——

func TestProposalNew_W9MissingFieldsStillCreated(t *testing.T) {
	dir := proposalVault(t)
	code, env, errOut := runProposalCLI(t, dir,
		"new", "--type", string(proposal.TypeLogicalDelete), "--target", applyCardID)
	if code != ExitOK {
		t.Fatalf("缺项不得拒绝创建，退出码 = %d：%s", code, errOut)
	}
	rep := applyReport(t, env)
	if len(rep.Proposals) != 1 {
		t.Fatalf("proposals[] 必须恰 1 条，实际 %v", rep.Proposals)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rep.Proposals[0].Path))); err != nil {
		t.Fatalf("提案文件必须已生成：%v", err)
	}
	var sawW9 bool
	for _, w := range rep.Warnings {
		if w.Code == "W9" {
			sawW9 = true
		}
	}
	if !sawW9 {
		t.Fatalf("warnings[] 必须含 W9（十项必备缺项），实际 %v", rep.Warnings)
	}
}

// —— ④ reject：status 与 decision.result 一致 ——

func TestProposalReject(t *testing.T) {
	dir := proposalVault(t)
	rel := newProposalOnce(t, dir)
	id := strings.TrimSuffix(filepath.Base(rel), ".md")

	code, env, errOut := runProposalCLI(t, dir, "reject", id, "--reason", "证据不足")
	if code != ExitOK {
		t.Fatalf("eg proposal reject 退出码 = %d，期望 0：%s", code, errOut)
	}
	pf, err := proposal.Parse(mustRead(t, filepath.Join(dir, filepath.FromSlash(rel))))
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if pf.P.Status != proposal.StatusRejected {
		t.Fatalf("status = %q，期望 rejected", pf.P.Status)
	}
	if pf.P.Decision.Result != proposal.StatusRejected {
		t.Fatalf("decision.result = %q，必须与 status 逐字一致", pf.P.Decision.Result)
	}
	if pf.P.Decision.Reason != "证据不足" {
		t.Fatalf("decision.reason = %q，期望写入拒绝理由", pf.P.Decision.Reason)
	}
	rep := applyReport(t, env)
	if len(rep.Proposals) != 1 || rep.Proposals[0].Status != string(proposal.StatusRejected) {
		t.Fatalf("报告 proposals[] 必须产出真实值：%v", rep.Proposals)
	}
	// rejected 是终态：再拒一次命中 §2.4 的自环非法边 → 退 2、零写入。
	before := mustRead(t, filepath.Join(dir, filepath.FromSlash(rel)))
	code, _, _ = runProposalCLI(t, dir, "reject", id, "--reason", "再拒一次")
	if code != ExitValidation {
		t.Fatalf("终态出边必须退 2（E9），实退 %d", code)
	}
	after := mustRead(t, filepath.Join(dir, filepath.FromSlash(rel)))
	if string(after) != string(before) {
		t.Fatal("被拒后提案字节必须不变（零写入）")
	}
}

// —— ⑤ list / show：只读、零副作用 ——

func TestProposalList_NoSideEffect(t *testing.T) {
	dir := proposalVault(t)
	rel := newProposalOnce(t, dir)
	assertProposalReadOnly(t, dir, rel, []string{"list"},
		[]string{"list", "--status", string(proposal.StatusPending)},
		[]string{"list", "--execution", string(proposal.ExecNotStarted)},
		[]string{"list", "--status", string(proposal.StatusPending),
			"--execution", string(proposal.ExecNotStarted)})
}

func TestProposalShow_NoSideEffect(t *testing.T) {
	dir := proposalVault(t)
	rel := newProposalOnce(t, dir)
	id := strings.TrimSuffix(filepath.Base(rel), ".md")
	assertProposalReadOnly(t, dir, rel, []string{"show", id})

	code, env, errOut := runProposalCLI(t, dir, "show", id)
	if code != ExitOK {
		t.Fatalf("eg proposal show 退出码 = %d：%s", code, errOut)
	}
	for _, key := range []string{"status", "execution", "decision", "impact", "sections"} {
		if _, ok := env.Data[key]; !ok {
			t.Fatalf("show 的 data 缺 %q：%v", key, env.Data)
		}
	}
	secs, ok := env.Data["sections"].([]interface{})
	if !ok || len(secs) != len(proposal.BodySections()) {
		t.Fatalf("show 必须输出七个 H2 正文，实际 %v", env.Data["sections"])
	}
}

// assertProposalReadOnly 断言若干只读调用退 0、工作区无变化、提案字节逐字不变。
//
// 提案没有 reviewed_at / updated_at 两键（M-6），因此「未更新时间戳」的判据就是
// **整份文件字节不变** —— 比逐键比对更强，也不会被新增键绕过。
func assertProposalReadOnly(t *testing.T, dir, rel string, invocations ...[]string) {
	t.Helper()
	abs := filepath.Join(dir, filepath.FromSlash(rel))
	before := string(mustRead(t, abs))
	statusBefore := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain"))
	logBefore := gitLogCount(t, dir)

	for _, args := range invocations {
		code, _, errOut := runProposalCLI(t, dir, args...)
		if code != ExitOK {
			t.Fatalf("eg proposal %v 退出码 = %d，期望 0：%s", args, code, errOut)
		}
		if got := string(mustRead(t, abs)); got != before {
			t.Fatalf("eg proposal %v 改动了提案字节（只读命令必须零写入）", args)
		}
		if got := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); got != statusBefore {
			t.Fatalf("eg proposal %v 改变了工作区：%q → %q", args, statusBefore, got)
		}
		if got := gitLogCount(t, dir); got != logBefore {
			t.Fatalf("eg proposal %v 产生了 commit（只读命令零 commit）", args)
		}
	}
}

// —— ⑥ 子命令集合恰五个 ——

func TestProposalSubcommands_ExactlyFive(t *testing.T) {
	cmd := New().Lookup("proposal")
	if cmd == nil {
		t.Fatal("eg proposal 必须已注册")
	}
	want := map[string]bool{"new": true, "list": true, "show": true, "approve": true, "reject": true}
	if len(cmd.Subs) != len(want) {
		t.Fatalf("子命令数 = %d（%v），期望恰 %d 个", len(cmd.Subs), cmd.Subs, len(want))
	}
	for _, s := range cmd.Subs {
		if !want[s] {
			t.Fatalf("子命令集合出现 %q，不属 {new,list,show,approve,reject}", s)
		}
		delete(want, s)
	}
	if len(want) != 0 {
		t.Fatalf("子命令集合缺 %v", want)
	}
	// 五个子命令的 --help 一律退 0（approve 的实现属下一层，但壳必须可用）。
	for _, sub := range cmd.Subs {
		dir := t.TempDir()
		r := newTestRoot(t, dir)
		code, out, errOut := runCLI(t, r, "proposal", sub, "--help")
		if code != ExitOK {
			t.Fatalf("eg proposal %s --help 退出码 = %d，期望 0：%s", sub, code, errOut)
		}
		if !strings.Contains(out, "eg proposal") {
			t.Fatalf("eg proposal %s --help 未把用法写进 stdout", sub)
		}
	}
}

// —— ⑦ U-13：存在 pending 提案时 eg apply 仍退 0 ——
//
// 与既有的 TestPendingProposalDoesNotAffectExitCode 同义但取证角度不同：那条用直接渲染的
// 提案夹具，这条走**真实的 `eg proposal new`**，因此同时证明「新建提案这条链路本身
// 也不给后续命令留下阻塞」。两条都保留，覆盖面只增不减。
func TestPendingProposalDoesNotBlock(t *testing.T) {
	dir := proposalVault(t)
	rel := newProposalOnce(t, dir)
	pf, err := proposal.Parse(mustRead(t, filepath.Join(dir, filepath.FromSlash(rel))))
	if err != nil {
		t.Fatalf("前置条件：提案必须可解析：%v", err)
	}
	if pf.P.Status != proposal.StatusPending {
		t.Fatalf("前置条件：提案必须是 pending，实为 %q", pf.P.Status)
	}

	code, _, errOut := runApplyPlan(t, dir, cardPlan("k-20260901-blk", ""))
	if code != ExitOK {
		t.Fatalf("未处理提案不得阻塞 eg apply（U-13），实退 %d：%s", code, errOut)
	}
}
