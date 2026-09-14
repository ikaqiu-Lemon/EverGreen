package cli

// T-…-036 的 CLI 侧验收：`execution=failed` 的逐路径回写与报告投影，
// 以及 I-…-002 升级为 DoR 后要求的「计数唯一来源」反证。
//
// 全部用例走**真实**的 store 写口与真实 git 仓：不打桩写入、不打桩校验，
// 唯一被注入的是时间（与 apply_test.go 同一口径）。
//
// 判据来源：提案合同 §4.3（failed 必须逐路径列出两个数组、并集 == 影响文件全集、
// unwritten_paths ⊇ 报告 skipped[] 中属本提案者）、§5.3 第 9b–10 步、§8.3（proposals[]
// 首次产出真实值），以及 §5 的 **B4**（部分写入如实记成部分写入，不做破坏性还原）。

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/proposal"
	"github.com/ikaqiu-Lemon/EverGreen/internal/report"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

const peProposalID = proposal.ID("p-20261020-001")

// peSeedTwoCards 落两张卡（都带真实的 sources[] 关系），返回两张卡的相对路径。
func peSeedTwoCards(t *testing.T, dir string) (string, string) {
	t.Helper()
	if _, cardRel := applyNoteAndCard(t, dir); cardRel == "" {
		t.Fatal("前置语料缺失：第一张卡未落盘")
	}
	code, _, errOut := runApplyPlan(t, dir, cardPlan(applyCard2ID, ""))
	if code != ExitOK {
		t.Fatalf("落第二张卡退出码 = %d：%s", code, errOut)
	}
	return store.CardRel("ai-infra", applyCardID), store.CardRel("ai-infra", applyCard2ID)
}

// peApprovedProposal 落一份 **approved** 提案，targets 恰为两张卡（= 影响文件全集）。
//
// 提案子命令的外壳属 T-…-040，这里直接用 internal/proposal 的模板与 T1 迁移落地：
// 都是生产实现，不是夹具字符串。
func peApprovedProposal(t *testing.T, dir string) string {
	t.Helper()
	raw, err := proposal.RenderTemplate(proposal.Template{
		ID: peProposalID, Title: "逻辑删除两张过时卡", CreatedAt: "2026-10-20",
		Targets: []string{applyCardID, applyCard2ID},
		Impact:  proposal.Impact{ExitsDefaultView: []string{applyCardID, applyCard2ID}},
	})
	if err != nil {
		t.Fatalf("RenderTemplate：%v", err)
	}
	rel := proposal.Rel(peProposalID)
	abs := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("建 proposals 目录：%v", err)
	}
	if err := os.WriteFile(abs, raw, 0o644); err != nil {
		t.Fatalf("写提案：%v", err)
	}
	if _, err := proposal.MarkApproved(proposal.ApproveSpec{
		Store: store.New(dir), Original: peProposalID, Reason: "用户已批准删除",
	}); err != nil {
		t.Fatalf("MarkApproved：%v", err)
	}
	return rel
}

// peReadProposal 读回提案的**磁盘事实**（只读解码，不改一个字节）。
func peReadProposal(t *testing.T, dir, rel string) *proposal.File {
	t.Helper()
	f, err := proposal.Parse(mustRead(t, filepath.Join(dir, filepath.FromSlash(rel))))
	if err != nil {
		t.Fatalf("提案形态不成立：%v", err)
	}
	return f
}

// —— ① 部分写入 → execution=failed 逐路径进提案与报告 ——

func TestApplyRecordsExecutionFailedPerPath(t *testing.T) {
	dir := applyVault(t)
	card1Rel, card2Rel := peSeedTwoCards(t, dir)
	prel := peApprovedProposal(t, dir)
	if got := peReadProposal(t, dir, prel).P.Execution.Status; got != proposal.ExecNotStarted {
		t.Fatalf("批准后立即读盘，execution.status = %q，期望 %q（approved ≠ 已执行）",
			got, proposal.ExecNotStarted)
	}
	card2Abs := filepath.Join(dir, filepath.FromSlash(card2Rel))
	card2Before := mustRead(t, card2Abs)

	// 第二张卡给一个**陈旧**的 content_hash → B3 跳过（file_changed）；第一张卡照常写入。
	plan := `{"plan_version":1,"verb":"process","domain":"ai-infra",
"reason":"按已批准提案处理两张卡，其中一张的凭据已过期",
"base":{"` + applyCardID + `":"` + hashOf(t, dir, card1Rel) + `",
"` + applyCard2ID + `":"sha256:0000000000000000000000000000000000000000000000000000000000000000"},
"ops":[{"op":"delete","target":"` + applyCardID + `","reason":"内容重复且无引用",
"initiator":"user","proposal":"` + string(peProposalID) + `"},
{"op":"append_card","card":"` + applyCardID + `","sections":{"解释与依据":"补一条依据。"}},
{"op":"append_card","card":"` + applyCard2ID + `","sections":{"解释与依据":"这一条不会被写入。"}}]}`
	// plan 内的 initiator: user 必须配上命令行 --user-request 才构成 P-U（N-1）。
	code, env, errOut := runApplyPlan(t, dir, plan, "--"+UserRequestFlag)
	if code != ExitPartialWrite {
		t.Fatalf("退出码 = %d，期望 3（部分写入）：%s", code, errOut)
	}
	rep := applyReport(t, env)

	// 报告侧：proposals[] 产出真实值，execution=failed 且两个数组逐路径齐全。
	if len(rep.Proposals) != 1 {
		t.Fatalf("proposals[] 长度 = %d，期望 1：%+v", len(rep.Proposals), rep.Proposals)
	}
	entry := rep.Proposals[0]
	if entry.ID != string(peProposalID) || entry.Path != prel {
		t.Fatalf("proposals[0] 定位错：%+v", entry)
	}
	if entry.Status != string(proposal.StatusApproved) {
		t.Fatalf("proposals[0].status = %q，执行结果不得回写 status（应仍是 %s）",
			entry.Status, proposal.StatusApproved)
	}
	if entry.Execution.Status != report.ExecutionFailed {
		t.Fatalf("execution.status = %q，期望 %q", entry.Execution.Status, report.ExecutionFailed)
	}
	if !strings.Contains(entry.Execution.Reason, "未做任何还原") {
		t.Fatalf("execution.reason 必须写明 B4 口径：%q", entry.Execution.Reason)
	}
	assertPartition(t, entry.Execution.WrittenPaths, entry.Execution.UnwrittenPaths,
		[]string{card1Rel, card2Rel})
	if !contains(entry.Execution.WrittenPaths, card1Rel) {
		t.Fatalf("已写的 %s 必须在 written_paths 里：%v", card1Rel, entry.Execution.WrittenPaths)
	}
	if !contains(entry.Execution.UnwrittenPaths, card2Rel) {
		t.Fatalf("未写的 %s 必须在 unwritten_paths 里：%v", card2Rel, entry.Execution.UnwrittenPaths)
	}
	// unwritten_paths ⊇ 报告 skipped[] 中属本提案者。
	for _, s := range rep.Skipped {
		if !contains([]string{card1Rel, card2Rel}, s.Locator) {
			continue
		}
		if !contains(entry.Execution.UnwrittenPaths, s.Locator) {
			t.Fatalf("报告把 %s 记成跳过，unwritten_paths 却没有它：%v",
				s.Locator, entry.Execution.UnwrittenPaths)
		}
	}

	// 磁盘侧：提案的两个数组与报告**逐字相同**（同一个账本，没有第二处计数）。
	after := peReadProposal(t, dir, prel)
	if after.P.Execution.Status != proposal.ExecFailed {
		t.Fatalf("磁盘上的 execution.status = %q，期望 %q", after.P.Execution.Status, proposal.ExecFailed)
	}
	if after.P.Status != proposal.StatusApproved {
		t.Fatalf("磁盘上的 status = %q：两维正交，执行结果不得回写用户决定", after.P.Status)
	}
	assertSameList(t, "written_paths", entry.Execution.WrittenPaths, after.P.Execution.WrittenPaths)
	assertSameList(t, "unwritten_paths", entry.Execution.UnwrittenPaths, after.P.Execution.UnwrittenPaths)
	if after.P.Execution.AttemptedAt == "" {
		t.Fatal("failed 必写 attempted_at")
	}

	// B4：已写内容留在磁盘、未写文件逐字节未变，且部分成功照常提交。
	if !strings.Contains(string(mustRead(t, filepath.Join(dir, filepath.FromSlash(card1Rel)))),
		"补一条依据。") {
		t.Fatal("B4 被违反：已写入的内容被还原了")
	}
	if store.ContentHash(mustRead(t, card2Abs)) != store.ContentHash(card2Before) {
		t.Fatal("未写入的文件必须逐字节不变")
	}
	if rep.Git.Commit == nil {
		t.Fatal("部分写入仍产生 commit，execution 的回写必须与本次写入同进一个 commit")
	}
	if got := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain", "--untracked-files=no")); got != "" {
		t.Fatalf("execution 回写必须进本次 commit，工作区却仍有改动：%q", got)
	}
}

// —— ② 没有真实执行痕迹时一个字节都不动（不假装尝试过）——

func TestApplyLeavesExecutionUntouchedWithoutRealAttempt(t *testing.T) {
	dir := applyVault(t)
	_, _ = peSeedTwoCards(t, dir)
	prel := peApprovedProposal(t, dir)
	before := mustRead(t, filepath.Join(dir, filepath.FromSlash(prel)))

	// 只有 delete 一个 op：它的逐文件落盘时序属 T-…-041，本次对提案影响文件零写入零跳过。
	plan := `{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"只走校验链",
"base":{},"ops":[{"op":"delete","target":"` + applyCardID + `","reason":"内容重复且无引用",
"initiator":"user","proposal":"` + string(peProposalID) + `"}]}`
	code, env, errOut := runApplyPlan(t, dir, plan, "--"+UserRequestFlag)
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0：%s", code, errOut)
	}
	rep := applyReport(t, env)
	if len(rep.Proposals) != 0 {
		t.Fatalf("本次没执行到提案影响文件，proposals[] 必须为空：%+v", rep.Proposals)
	}
	after := mustRead(t, filepath.Join(dir, filepath.FromSlash(prel)))
	if string(after) != string(before) {
		t.Fatal("没有真实执行痕迹时，提案必须逐字节不变（不假装尝试过）")
	}
	if peReadProposal(t, dir, prel).P.Execution.Status != proposal.ExecNotStarted {
		t.Fatal("未执行的提案 execution.status 必须仍是 not_started")
	}
}

// —— ③ I-…-002 的 DoR 反证：dry-run 与正式执行对同一 plan 数出同样多的卡与文件 ——
//
// I-…-002 的缺陷形态是「同一个事实由两条源各数一遍」：
// `--dry-run` 的卡计数来源是 `planned[]`（源 = pres.Actions 的展开结果），
// 正式执行的来源是 `cards.created` / `links[]`（源 = plan.Execute 的 ExecResult）。
// 本用例把两条源对**同一份 plan** 数出的卡数与文件集合钉成相等：任何一侧多数 / 少数即红。
//
// **边界**：I-…-002 的修复本体仍属 M2 侧（`applyDryRun()` 不填 `rep.Cards.Created`
// 却照打零卡说明），本 task **不修**、**不关闭**该 issue；这里只钉「两条源不得数出不同的量」，
// 并以此约束 T-…-036 新增的 `written_paths` / `unwritten_paths` 不得复制同一形态
// —— 那两个数组的唯一账本在 internal/proposal 的 PathLedger（见
// TestWrittenPaths_SingleSourceOfTruth）。
func TestApplyDryRunAndFormalCountTheSameCards(t *testing.T) {
	dir := applyVault(t)
	if code, _, errOut := runApplyPlan(t, dir, notePlan()); code != ExitOK {
		t.Fatalf("落笔记退出码 = %d：%s", code, errOut)
	}
	plan := cardPlan(applyCardID, "")

	_, dryEnv, errOut := runApplyPlan(t, dir, plan, "--dry-run")
	dryRep := applyReport(t, dryEnv)
	planned, ok := dryEnv.Data["planned"].([]interface{})
	if !ok {
		t.Fatalf("--dry-run 必须回带 planned[]：%v（%s）", dryEnv.Data["planned"], errOut)
	}
	dryCards := map[string]bool{}
	for _, item := range planned {
		row, ok := item.(map[string]interface{})
		if !ok {
			t.Fatalf("planned[] 条目形态不对：%v", item)
		}
		path, _ := row["path"].(string)
		if strings.Contains(path, "/knowledge/") {
			dryCards[path] = true
		}
	}

	code, env, errOut := runApplyPlan(t, dir, plan, "--"+UserRequestFlag)
	if code != ExitOK {
		t.Fatalf("正式执行退出码 = %d：%s", code, errOut)
	}
	rep := applyReport(t, env)
	formalCards := map[string]bool{}
	for _, id := range append(append([]string{}, rep.Cards.Created...), rep.Cards.Updated...) {
		formalCards[store.CardRel("ai-infra", id)] = true
	}

	if len(dryCards) != len(formalCards) {
		t.Fatalf("同一 plan：dry-run 数出 %d 张卡（%v），正式执行数出 %d 张卡（%v）："+
			"两条源必须数出同样多（I-…-002 的缺陷形态不得复制）",
			len(dryCards), keysOfSet(dryCards), len(formalCards), keysOfSet(formalCards))
	}
	for path := range formalCards {
		if !dryCards[path] {
			t.Fatalf("正式执行写了 %s，dry-run 的 planned[] 里却没有它：%v", path, keysOfSet(dryCards))
		}
	}
	// links[] 是同一个事实的第二种表达：dry-run 的「将写入」与正式执行的「已写入」必须同集合。
	assertSameList(t, "links", sortedCopy(dryRep.Links), sortedCopy(rep.Links))
}

// —— 断言助手 ——

// assertPartition 断言「两个清单的并集恰等于全集 ∧ 交集为空」（§4.3 的两侧一致性）。
func assertPartition(t *testing.T, written, unwritten, all []string) {
	t.Helper()
	seen := map[string]int{}
	for _, rel := range written {
		seen[rel]++
	}
	for _, rel := range unwritten {
		seen[rel]++
	}
	for rel, n := range seen {
		if n != 1 {
			t.Fatalf("路径 %s 出现 %d 次：written_paths ∩ unwritten_paths 必须为空", rel, n)
		}
	}
	if len(seen) != len(all) {
		t.Fatalf("并集 %d 条（%v + %v），影响文件全集 %d 条（%v）：必须恰相等",
			len(seen), written, unwritten, len(all), all)
	}
	for _, rel := range all {
		if seen[rel] != 1 {
			t.Fatalf("影响文件 %s 既不在 written_paths 也不在 unwritten_paths 里", rel)
		}
	}
}

func assertSameList(t *testing.T, what string, want, got []string) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("%s：两侧条数不同（%d vs %d）：%v / %v", what, len(want), len(got), want, got)
	}
	for i := range want {
		if want[i] != got[i] {
			t.Fatalf("%s：第 %d 条不同（%q vs %q）：唯一来源必须让两侧逐字相同",
				what, i, want[i], got[i])
		}
	}
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

func keysOfSet(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
