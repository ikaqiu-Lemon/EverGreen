package cli

// `eg delete` 的命令侧机器判据（T-evergreen.s1_main_flow-158614-041 Acceptance）。
//
// 四组断言，逐条对应合同里最容易被做丢的不变式：
//   ① status **绝不**被自动改变 + 报告逐字含「本次删除没有自动改变任何知识卡的状态」（§5.5 / §9）；
//   ② 必须引用 status=approved 的提案（W7 后半 ≡ V10）：无 --proposal / pending / rejected
//      三行表驱动，均退 2 且目标文件**字节不变**；
//   ③ 缺 --confirm → 退 6，权威 Markdown **零变化**（提案与目标字节、git status、git log 逐字相同）；
//   ④ 中途失败保留现状（U-02）：已写文件保持已写、未写文件字节不变、execution=failed
//      并逐路径在册，且不做任何 Git 层面的还原（commit 数与 HEAD 不变）。

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/proposal"
	"github.com/ikaqiu-Lemon/EverGreen/internal/report"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// runDeleteCLI 在 dir 这个 vault 上跑一次 `eg delete …`（--json 信封）。
//
// stdin 恒为已关闭状态：确认点恰 1 处（EG-CFM-05）——`--confirm` 之后任何再读 stdin 都会红。
func runDeleteCLI(t *testing.T, r *Root, dir string, args ...string) (int, Envelope, string) {
	t.Helper()
	setGitIdentity(t)
	r.Now = func() time.Time { return captureAt(t) }
	r.In = closedStdin{}
	full := append([]string{"delete"}, args...)
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

// deleteVault 建一个含原文 + 笔记 + 一张卡（卡的 sources[] 上有一条 support）的干净 vault。
func deleteVault(t *testing.T) (string, string, string) {
	t.Helper()
	dir := applyVault(t)
	noteRel, cardRel := applyNoteAndCard(t, dir)
	return dir, noteRel, cardRel
}

// approvedProposalFor 直接在库里造一份 **status=approved** 的提案（targets 可多个）。
//
// 为什么不用 `eg proposal new` + `approve`：那条路径的 `--target` 只收单值，
// 而「逐文件 = 非强原子」这条不变式必须在**多于一个**影响文件上才能被证否。
// 影响面取 RecomputeImpact 的真实结果，因此 delete 的执行前重算与它逐项一致（不触发 9a）。
func approvedProposalFor(t *testing.T, dir string, targets []string) proposal.Record {
	t.Helper()
	st := store.New(dir)
	im, err := proposal.RecomputeImpact(st, targets)
	if err != nil {
		t.Fatalf("前置：重算影响面失败：%v", err)
	}
	date := model.NewDate(captureAt(t))
	id, err := proposal.NewID(date, 1)
	if err != nil {
		t.Fatalf("前置：提案 ID 生成失败：%v", err)
	}
	res, err := proposal.Create(st, proposal.Template{
		ID: id, Title: "逻辑删除 " + strings.Join(targets, " / "),
		CreatedAt: date.String(), Targets: targets, Impact: im,
	})
	if err != nil {
		t.Fatalf("前置：提案落盘失败：%v", err)
	}
	if _, err := proposal.MarkApproved(proposal.ApproveSpec{
		Store: st, Original: id, OriginalRel: res.Path, Reason: "用例前置：用户已批准",
	}); err != nil {
		t.Fatalf("前置：提案批准失败：%v", err)
	}
	// 前置动作自己提交干净，delete 的「恰 +1 条 commit」才数得准。
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-m", "proposal(-): 用例前置")
	rec, err := proposal.LoadRel(st, res.Path)
	if err != nil {
		t.Fatalf("前置：提案读回失败：%v", err)
	}
	return rec
}

// —— ① status 逐字未变 + 报告逐字串 ——

func TestDelete_NoAutoStatusChange(t *testing.T) {
	dir, noteRel, cardRel := deleteVault(t)
	// 删的是**笔记**：卡因此失去唯一一条有效 support，正是「建议标记 deprecated」的场景。
	rec := approvedProposalFor(t, dir, []string{applyNoteID})

	cardBefore := string(mustRead(t, absIn(dir, cardRel)))
	beforeCommits := gitLogCount(t, dir)

	code, env, output := runDeleteCLI(t, newTestRoot(t, dir), dir,
		"--target", applyNoteID, "--reason", "笔记已过时", "--proposal", string(rec.ID),
		"--confirm", "--user-request")
	if code != ExitOK {
		t.Fatalf("eg delete 退出码 = %d，期望 0：%s", code, output)
	}

	// 1) 全部卡的 status 逐字未变：卡文件一个字节都没被碰过（不级联、不改状态）。
	if got := string(mustRead(t, absIn(dir, cardRel))); got != cardBefore {
		t.Fatalf("知识卡字节发生变化：删除不得自动改变任何卡的状态、也不得级联改关系\n前：%q\n后：%q",
			cardBefore, got)
	}
	card, err := store.CardOf(mustRead(t, absIn(dir, cardRel)))
	if err != nil {
		t.Fatalf("卡不可解析：%v", err)
	}
	if card.Status != model.StatusActive {
		t.Fatalf("卡 status = %q，期望仍是 %q（用户不处理时它仍是 active）",
			card.Status, model.StatusActive)
	}

	// 2) 目标笔记真实写上了删除标记（只这两个键）。
	noteRaw := string(mustRead(t, absIn(dir, noteRel)))
	for _, key := range []string{"deleted_at:", "deleted_reason:"} {
		if !strings.Contains(noteRaw, key) {
			t.Fatalf("目标笔记缺 %s：逻辑删除必须写这两个键\n%s", key, noteRaw)
		}
	}
	if strings.Contains(noteRaw, "status: deprecated") {
		t.Fatal("删除不得顺带改 status（status 与删除是两个正交维度）")
	}

	// 3) 报告逐字含那句说明（一字不差）。
	if !strings.Contains(output, report.NoAutoStatusChangeNotice) {
		t.Fatalf("报告缺逐字串 %q：\n%s", report.NoAutoStatusChangeNotice, output)
	}

	// 4) support_check[]：失去有效 support 的卡拿到「建议标记 deprecated」，且 status 仍 active。
	// 5) 取数走**信封原始 JSON** 的真实路径 .data.report.support_check[]，与验收 jq 同源。
	entries := supportCheckEntries(t, env)
	if len(entries) != 1 {
		t.Fatalf("support_check[] 期望恰 1 条（受影响的卡 %s），实际 %d 条：%+v",
			applyCardID, len(entries), entries)
	}
	entry := entries[0]
	if entry.ID != applyCardID || entry.Recommendation != report.RecommendDeprecateMark {
		t.Fatalf("support_check[0] = %+v，期望 %s → %q", entry, applyCardID, report.RecommendDeprecateMark)
	}
	if entry.Status != string(model.StatusActive) {
		t.Fatalf("support_check[0].status = %q，期望 active（系统绝不自动改状态）", entry.Status)
	}

	// 6) 第 10 步：恰一条 commit，verb = delete。
	if got := gitLogCount(t, dir); got != beforeCommits+1 {
		t.Fatalf("commit 数 %d → %d，期望恰 +1", beforeCommits, got)
	}
	if head := gitOut(t, dir, "log", "--oneline", "-1"); !strings.Contains(head, "delete(") {
		t.Fatalf("最后一条 commit 主题 = %q，期望 delete(<domain>): …", head)
	}

	// 7) 提案侧：status 仍 approved、execution = succeeded。
	after, err := proposal.LoadRel(store.New(dir), rec.Rel)
	if err != nil {
		t.Fatalf("提案读回失败：%v", err)
	}
	if after.Proposal().Status != proposal.StatusApproved {
		t.Fatalf("提案 status = %q，期望仍是 approved", after.Proposal().Status)
	}
	if after.Proposal().Execution.Status != proposal.ExecSucceeded {
		t.Fatalf("提案 execution.status = %q，期望 %q",
			after.Proposal().Execution.Status, proposal.ExecSucceeded)
	}
}

// supportCheckEntries 走**信封原始 JSON**取 `.data.report.support_check[]`，
// 与验收命令 `jq -e '.data.report.support_check[]?.recommendation'` 同一条路径
// （信封数据键是 `.data.report.*`，不是 `.data.*`：如实按真实路径断言，不改信封契约）。
func supportCheckEntries(t *testing.T, env Envelope) []report.SupportCheckEntry {
	t.Helper()
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("信封不可序列化：%v", err)
	}
	var probe struct {
		Data struct {
			Report struct {
				SupportCheck []report.SupportCheckEntry `json:"support_check"`
			} `json:"report"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("信封不可解析：%v\n%s", err, raw)
	}
	if len(probe.Data.Report.SupportCheck) == 0 {
		t.Fatalf(".data.report.support_check[] 为空：%s", raw)
	}
	return probe.Data.Report.SupportCheck
}

// absIn 把 vault 内相对路径拼成绝对路径。
func absIn(dir, rel string) string {
	return dir + "/" + rel
}

// —— ② 必须引用 approved 提案：三行表驱动，均退 2 且目标文件字节不变 ——

func TestDelete_RequiresApprovedProposal(t *testing.T) {
	cases := []struct {
		name string
		// proposalOf 返回本行要传给 --proposal 的值（空串 = 不传该参数）。
		proposalOf func(t *testing.T, dir string) string
	}{
		{
			name:       "无 --proposal",
			proposalOf: func(*testing.T, string) string { return "" },
		},
		{
			name: "提案仍是 pending",
			proposalOf: func(t *testing.T, dir string) string {
				rel := newProposalOnce(t, dir)
				return strings.TrimSuffix(strings.TrimPrefix(rel, proposal.DirProposals+"/"), ".md")
			},
		},
		{
			name: "提案已 rejected",
			proposalOf: func(t *testing.T, dir string) string {
				rel := newProposalOnce(t, dir)
				id := strings.TrimSuffix(strings.TrimPrefix(rel, proposal.DirProposals+"/"), ".md")
				code, _, errOut := runProposalCLI(t, dir, "reject", id, "--reason", "先不删")
				if code != ExitOK {
					t.Fatalf("前置：eg proposal reject 退出码 = %d：%s", code, errOut)
				}
				return id
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, _, cardRel := deleteVault(t)
			pid := tc.proposalOf(t, dir)
			targetAbs := absIn(dir, cardRel)
			before := string(mustRead(t, targetAbs))
			beforeCommits := gitLogCount(t, dir)

			args := []string{"--target", applyCardID, "--reason", "该卡已过时",
				"--confirm", "--user-request"}
			if pid != "" {
				args = append(args, "--proposal", pid)
			}
			code, _, output := runDeleteCLI(t, newTestRoot(t, dir), dir, args...)
			if code != ExitValidation {
				t.Fatalf("退出码 = %d，期望 %d（W7 后半 ≡ V10 升 error）：%s",
					code, ExitValidation, output)
			}
			if got := string(mustRead(t, targetAbs)); got != before {
				t.Fatalf("目标文件字节发生变化：无 approved 提案背书时必须零写入\n前：%q\n后：%q",
					before, got)
			}
			if got := gitLogCount(t, dir); got != beforeCommits {
				t.Fatalf("commit 数 %d → %d：校验失败不得产生 commit", beforeCommits, got)
			}
		})
	}
}

// —— ③ 缺 --confirm → 退 6，权威 Markdown 零变化 ——

func TestDelete_ExitCode6(t *testing.T) {
	dir, _, cardRel := deleteVault(t)
	rec := approvedProposalFor(t, dir, []string{applyCardID})

	targetAbs := absIn(dir, cardRel)
	proposalAbs := absIn(dir, rec.Rel)
	beforeTarget := string(mustRead(t, targetAbs))
	beforeProposal := string(mustRead(t, proposalAbs))
	beforePorcelain := gitOut(t, dir, "status", "--porcelain")
	beforeLog := gitOut(t, dir, "log", "--oneline", "-1")
	beforeCommits := gitLogCount(t, dir)

	code, _, output := runDeleteCLI(t, newTestRoot(t, dir), dir,
		"--target", applyCardID, "--reason", "该卡已过时",
		"--proposal", string(rec.ID), "--user-request")
	if code != ExitNeedConfirm {
		t.Fatalf("缺 --confirm 的退出码 = %d，期望 %d：%s", code, ExitNeedConfirm, output)
	}
	if !strings.Contains(output, "--confirm") {
		t.Fatalf("输出必须提示补 --confirm：%s", output)
	}
	if got := string(mustRead(t, targetAbs)); got != beforeTarget {
		t.Fatalf("目标卡字节发生变化：退出码 6 要求权威 Markdown 完全不变")
	}
	if got := string(mustRead(t, proposalAbs)); got != beforeProposal {
		t.Fatalf("提案字节发生变化：退出码 6 要求零写入")
	}
	if got := gitOut(t, dir, "status", "--porcelain"); got != beforePorcelain {
		t.Fatalf("git status --porcelain 变化：%q → %q", beforePorcelain, got)
	}
	if got := gitOut(t, dir, "log", "--oneline", "-1"); got != beforeLog {
		t.Fatalf("git log -1 变化：%q → %q", beforeLog, got)
	}
	if got := gitLogCount(t, dir); got != beforeCommits {
		t.Fatalf("commit 数 %d → %d：退出码 6 不得产生 commit", beforeCommits, got)
	}
}

// —— ④ 预演期写失败：全有或全无（M6 · T-…-072 C3a），零生效、零 commit ——
//
// M3 形态是「已写的留着、未写的字节不变、execution=failed 逐路径在册」；M6 把删除的原子域
// 定为「**全部** target + 提案 execution」，因此**第二个**文件写失败时第一个也绝不能落盘：
// overlay 整体丢弃 ⇒ 连事务都没开、Git 没跑、提案一个字节没动（更不会被改判成 failed ——
// 那是在记录一件没发生过的部分执行）。退出码仍是 3，含义从「部分写入」收紧为「未生效」。
func TestDelete_PartialFailureKeepsDisk(t *testing.T) {
	dir, noteRel, _ := deleteVault(t)
	// 第二张卡让影响文件全集恰 2 个：原子域覆盖多于一个文件时，才谈得上「全有或全无」。
	code, _, errOut := runApplyPlan(t, dir, cardPlan(applyCard2ID, ""))
	if code != ExitOK {
		t.Fatalf("前置：落第二张卡退出码 = %d：%s", code, errOut)
	}
	card2Rel := store.CardRel("ai-infra", applyCard2ID)
	rec := approvedProposalFor(t, dir, []string{applyNoteID, applyCard2ID})

	noteAbs, card2Abs := absIn(dir, noteRel), absIn(dir, card2Rel)
	proposalAbs := absIn(dir, rec.Rel)
	beforeNote := string(mustRead(t, noteAbs))
	beforeCard2 := string(mustRead(t, card2Abs))
	beforeProposal := string(mustRead(t, proposalAbs))
	beforeExec := rec.Proposal().Execution.Status
	beforeCommits := gitLogCount(t, dir)
	beforeLog := gitOut(t, dir, "log", "--oneline", "-1")

	// 注入：**第二个**文件写失败（第一个在 overlay 里已经 stage 成功）。
	r := newTestRoot(t, dir)
	calls := 0
	r.StateWrite = func(st *store.Store, spec store.StateWriteSpec) (store.Result, error) {
		calls++
		if calls == 2 {
			return store.Result{Path: spec.Rel}, errInjectedWriteFailure
		}
		return st.ApplyStateWrite(spec)
	}

	code, _, output := runDeleteCLI(t, r, dir,
		"--target", applyNoteID, "--reason", "批量清理", "--proposal", string(rec.ID),
		"--confirm", "--user-request")
	if code != ExitPartialWrite {
		t.Fatalf("预演期写失败退出码 = %d，期望 %d（本次零生效）：%s", code, ExitPartialWrite, output)
	}

	// ① 全部目标字节不变：**包括**那个先写成功的（它只进过 overlay，从未落盘）。
	if got := string(mustRead(t, noteAbs)); got != beforeNote {
		t.Fatalf("stage 成功的目标也必须零变化（原子域全有或全无）\n前：%q\n后：%q", beforeNote, got)
	}
	if strings.Contains(string(mustRead(t, noteAbs)), "deleted_at:") {
		t.Fatalf("本次未提交，%s 不得出现 deleted_at", noteRel)
	}
	if got := string(mustRead(t, card2Abs)); got != beforeCard2 {
		t.Fatalf("写失败的文件必须字节不变\n前：%q\n后：%q", beforeCard2, got)
	}

	// ② 提案字节零变化：不写 execution=succeeded，也**不**二次写成 failed。
	if got := string(mustRead(t, proposalAbs)); got != beforeProposal {
		t.Fatalf("提案必须字节不变（本次一个字节都没生效，不得改判 execution）\n前：%q\n后：%q",
			beforeProposal, got)
	}
	after, err := proposal.LoadRel(store.New(dir), rec.Rel)
	if err != nil {
		t.Fatalf("提案读回失败：%v", err)
	}
	if got := after.Proposal().Execution.Status; got != beforeExec {
		t.Fatalf("execution.status = %q，期望保持 %q（禁止 %q 这次二次写）",
			got, beforeExec, proposal.ExecFailed)
	}

	// ③ 事务与 Git 都没发生：commit 数与 HEAD 逐字不变，也没有还原动作可做。
	if got := gitLogCount(t, dir); got != beforeCommits {
		t.Fatalf("commit 数 %d → %d：预演期失败不提交", beforeCommits, got)
	}
	if got := gitOut(t, dir, "log", "--oneline", "-1"); got != beforeLog {
		t.Fatalf("git log -1 变化：%q → %q（禁止破坏性还原）", beforeLog, got)
	}

	// ④ 报告逐路径点名两个目标都没写，且给出失败原因。
	for _, rel := range []string{noteRel, card2Rel} {
		if !strings.Contains(output, rel) {
			t.Fatalf("输出必须逐路径点名未写文件 %s：\n%s", rel, output)
		}
	}
}

// errInjectedWriteFailure 是注入的写失败：只用于覆盖「第 N 个文件写失败」分支。
var errInjectedWriteFailure = errors.New("注入的写失败：第二个文件落盘失败（用例专用）")

// TestDeleteLeavesNoDirtyProposal 钉死 A-32 / K-041-01：succeeded 不再回写 git_commit 后，
// 提案 execution 回写与各文件 deleted_at 落进**同一次** commit——删除成功后工作区**零脏提案**：
//
//	① `git status --porcelain` 恰 0 行（不再有 commit 之后的二次脏写）；
//	② `git log` 恰 +1（仍是单次 commit）；
//	③ 该次 commit **包含**提案文件（execution 回写进了历史，而非留在盘上）；
//	④ 提案 execution.status = succeeded，且落盘字节里**不再出现** git_commit 键。
func TestDeleteLeavesNoDirtyProposal(t *testing.T) {
	dir, noteRel, _ := deleteVault(t)
	_ = noteRel
	rec := approvedProposalFor(t, dir, []string{applyNoteID})

	// 前置提交后工作区必须是干净的，才能把「delete 之后是否留脏」这件事数准。
	if got := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); got != "" {
		t.Fatalf("前置未干净，无法判定 delete 是否留脏：\n%s", got)
	}
	beforeCommits := gitLogCount(t, dir)

	code, _, output := runDeleteCLI(t, newTestRoot(t, dir), dir,
		"--target", applyNoteID, "--reason", "笔记已过时", "--proposal", string(rec.ID),
		"--confirm", "--user-request")
	if code != ExitOK {
		t.Fatalf("eg delete 退出码 = %d，期望 0：%s", code, output)
	}

	// ① 零脏提案：删除后工作区无任何未提交改动。
	if got := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); got != "" {
		t.Fatalf("删除后工作区留有脏改动（K-041-01：不得在 commit 之后二次脏写提案）：\n%s", got)
	}
	// ② 单次 commit。
	if got := gitLogCount(t, dir); got != beforeCommits+1 {
		t.Fatalf("commit 数 %d → %d，期望恰 +1", beforeCommits, got)
	}
	// ③ 该次 commit 覆盖提案文件（execution 回写进了历史，不是 commit 之后的脏写）。
	head := gitOut(t, dir, "show", "--name-only", "--pretty=format:", "HEAD")
	if !strings.Contains(head, rec.Rel) {
		t.Fatalf("HEAD commit 未覆盖提案文件 %s：execution 回写必须与 deleted_at 同进一次历史\n%s",
			rec.Rel, head)
	}
	// ④ 提案 execution.status = succeeded，且落盘不再出现 git_commit 键。
	after, err := proposal.LoadRel(store.New(dir), rec.Rel)
	if err != nil {
		t.Fatalf("删除后读回提案失败：%v", err)
	}
	if after.Proposal().Execution.Status != proposal.ExecSucceeded {
		t.Fatalf("提案 execution.status = %q，期望 %q",
			after.Proposal().Execution.Status, proposal.ExecSucceeded)
	}
	if raw := string(mustRead(t, absIn(dir, rec.Rel))); strings.Contains(raw, "git_commit") {
		t.Fatalf("A-32「整键删除」：提案落盘字节里不得再出现 git_commit 键：\n%s", raw)
	}
}
