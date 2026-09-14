package cli

// `eg undelete` 的命令侧机器判据（T-evergreen.s1_main_flow-158614-041 收尾）。
//
// 四条断言，逐条对应最容易被做丢的不变式：
//   ① status **逐字未变**（删除维度与状态维度正交，恢复删除同样不碰 status）；
//   ② **不要求 --confirm**：退出码 6 的白名单恰含 approve 与 delete，undelete 不在其中；
//   ③ 缺 --reason → 退 1、零写入、零 commit（参数形态问题，不冒名成校验失败）；
//   ④ 目标未被逻辑删除 → W11 幂等 no-op：退 0、零写入、零 commit（不产生空 commit）。

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/proposal"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// runUndeleteCLI 在 dir 这个 vault 上跑一次 `eg undelete …`（--json 信封）。
//
// stdin 恒为已关闭状态：本命令没有任何确认点，任何读 stdin 的行为都会红。
func runUndeleteCLI(t *testing.T, r *Root, dir string, args ...string) (int, Envelope, string) {
	t.Helper()
	setGitIdentity(t)
	r.Now = func() time.Time { return captureAt(t) }
	r.In = closedStdin{}
	full := append([]string{"undelete"}, args...)
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

// undeletableVault 造一个「**知识卡**已被逻辑删除」的 vault：先走真实 `eg delete` 全时序，
// 因此删除标记的字节形态与生产完全一致（用例不自己拼 frontmatter）。
//
// 为什么删的是卡而不是笔记：本组用例的核心判据是「status 逐字未变」，
// 而只有知识卡的 frontmatter 才有 status 这一格。
func undeletableVault(t *testing.T) (dir string, cardRel string, noteRel string) {
	t.Helper()
	dir, noteRel, cardRel = deleteVault(t)
	rec := approvedProposalFor(t, dir, []string{applyCardID})
	code, _, output := runDeleteCLI(t, newTestRoot(t, dir), dir,
		"--target", applyCardID, "--reason", "该卡已过时", "--proposal", string(rec.ID),
		"--confirm", "--user-request")
	if code != ExitOK {
		t.Fatalf("前置：eg delete 退出码 = %d，期望 0：%s", code, output)
	}
	// delete 之后可能留下未进 commit 的 execution 回写；用例前置自己提交干净，
	// undelete 的「恰 +1 条 commit」才数得准。
	gitOut(t, dir, "add", "-A")
	if strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")) != "" {
		gitOut(t, dir, "commit", "-m", "proposal(-): 用例前置：收拢 delete 之后的工作区")
	}
	return dir, cardRel, noteRel
}

// —— ① status 逐字未变 ——

func TestUndelete_StatusUntouched(t *testing.T) {
	dir, cardRel, noteRel := undeletableVault(t)

	cardBefore := string(mustRead(t, absIn(dir, cardRel)))
	noteBefore := string(mustRead(t, absIn(dir, noteRel)))
	statusBefore := fmStatusLine(t, cardBefore)
	beforeCommits := gitLogCount(t, dir)

	code, _, output := runUndeleteCLI(t, newTestRoot(t, dir), dir,
		"--target", applyCardID, "--reason", "误删，恢复")
	if code != ExitOK {
		t.Fatalf("eg undelete 退出码 = %d，期望 0：%s", code, output)
	}

	after := string(mustRead(t, absIn(dir, cardRel)))
	// 1) 两个删除键整行消失（不留墓碑、不写空值）。
	for _, key := range []string{"deleted_at:", "deleted_reason:"} {
		if strings.Contains(after, key) {
			t.Fatalf("undelete 后仍含 %s：应整行删除\n%s", key, after)
		}
	}
	// 2) status 那一行逐字未变（这是硬约束：删除维度与状态维度正交）。
	if got := fmStatusLine(t, after); got != statusBefore {
		t.Fatalf("status 行 = %q，期望逐字仍是 %q", got, statusBefore)
	}
	if statusBefore == "" {
		t.Fatal("前置不成立：目标 frontmatter 无 status 行，本用例失去判据")
	}
	// 3) 别的产物一个字节都没被碰（不级联）。
	if got := string(mustRead(t, absIn(dir, noteRel))); got != noteBefore {
		t.Fatalf("材料笔记字节发生变化：undelete 只动目标一个文件\n前：%q\n后：%q", noteBefore, got)
	}
	// 4) 恰一条 commit，verb = process（S1 冻结动词内，KnownVerbs 未增减）。
	if got := gitLogCount(t, dir); got != beforeCommits+1 {
		t.Fatalf("commit 数 %d → %d，期望恰 +1", beforeCommits, got)
	}
	if head := gitOut(t, dir, "log", "--oneline", "-1"); !strings.Contains(head, "process(") {
		t.Fatalf("最后一条 commit 主题 = %q，期望 process(<domain>): …", head)
	}
	// 5) 报告逐字含「没有自动改变任何知识卡的状态」那句说明。
	if !strings.Contains(output, UndeleteNoStatusChangeNotice) {
		t.Fatalf("报告缺逐字串 %q：\n%s", UndeleteNoStatusChangeNotice, output)
	}
	// 6) 提案侧一字不动：undelete 不是提案的执行，不回写 execution。
	rec, err := proposal.LoadRel(store.New(dir), latestProposalRel(t, dir))
	if err != nil {
		t.Fatalf("提案读回失败：%v", err)
	}
	if rec.Proposal().Status != proposal.StatusApproved {
		t.Fatalf("提案 status = %q，期望仍是 approved（undelete 不改提案状态）",
			rec.Proposal().Status)
	}
}

// fmStatusLine 取出 frontmatter 里 `status:` 那**一整行**（不存在时返回空串）。
func fmStatusLine(t *testing.T, raw string) string {
	t.Helper()
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "status:") {
			return line
		}
	}
	return ""
}

// latestProposalRel 取库里唯一那份提案的相对路径（用例只造一份）。
func latestProposalRel(t *testing.T, dir string) string {
	t.Helper()
	recs, err := proposal.List(store.New(dir))
	if err != nil {
		t.Fatalf("提案清单读取失败：%v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("提案数 = %d，用例前置只造一份", len(recs))
	}
	return recs[0].Rel
}

// —— ② 不需二次确认：不给 --confirm 也退 0（且不启用退出码 6）——

func TestUndelete_NoSecondConfirm(t *testing.T) {
	dir, cardRel, _ := undeletableVault(t)

	// 注册表侧：本命令根本没有 --confirm 这个参数（有它就等于悄悄扩大确认门白名单）。
	if cmd := newTestRoot(t, dir).Lookup("undelete"); cmd == nil {
		t.Fatal("undelete 未注册")
	}
	for _, name := range wantFlags["undelete"] {
		if name == DeleteConfirmFlag {
			t.Fatal("undelete 不得注册 --confirm：退出码 6 的白名单恰含 approve 与 delete")
		}
	}
	if NeedConfirmEnabled("undelete") {
		t.Fatal("undelete 不在退出码 6 的白名单里，NeedConfirmEnabled 必须为 false")
	}

	code, _, output := runUndeleteCLI(t, newTestRoot(t, dir), dir,
		"--target", applyCardID, "--reason", "误删，恢复")
	if code != ExitOK {
		t.Fatalf("不带 --confirm 的 eg undelete 退出码 = %d，期望 0（本命令不做二次确认）：%s",
			code, output)
	}
	if code == ExitNeedConfirm {
		t.Fatal("undelete 不得产生退出码 6")
	}
	if strings.Contains(string(mustRead(t, absIn(dir, cardRel))), "deleted_at:") {
		t.Fatal("undelete 退 0 却没清掉 deleted_at")
	}
}

// —— ③ 缺 --reason → 退 1、零写入、零 commit ——

func TestUndelete_MissingReasonExits1(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"缺 --reason", []string{"--target", applyCardID}},
		{"缺 --target", []string{"--reason", "误删，恢复"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, cardRel, _ := undeletableVault(t)
			targetBefore := string(mustRead(t, absIn(dir, cardRel)))
			beforeCommits := gitLogCount(t, dir)
			beforePorcelain := gitOut(t, dir, "status", "--porcelain")

			code, _, output := runUndeleteCLI(t, newTestRoot(t, dir), dir, tc.args...)
			if code != ExitUsage {
				t.Fatalf("退出码 = %d，期望 1（参数形态问题不得冒名成校验失败）：%s", code, output)
			}
			if got := string(mustRead(t, absIn(dir, cardRel))); got != targetBefore {
				t.Fatalf("目标字节发生变化：退 1 必须零写入\n前：%q\n后：%q", targetBefore, got)
			}
			if got := gitLogCount(t, dir); got != beforeCommits {
				t.Fatalf("commit 数 %d → %d，期望零 commit", beforeCommits, got)
			}
			if got := gitOut(t, dir, "status", "--porcelain"); got != beforePorcelain {
				t.Fatalf("工作区状态变了：\n前：%q\n后：%q", beforePorcelain, got)
			}
		})
	}
}

// —— ④ 目标未被逻辑删除 → W11 幂等 no-op：零写入、零 commit ——

func TestUndelete_IdempotentNoOpWhenNotDeleted(t *testing.T) {
	dir, _, cardRel := deleteVault(t)
	// 这张卡从未被删除过：W11 判定（plan.UndeleteNoOp）应当命中。
	cardBefore := string(mustRead(t, absIn(dir, cardRel)))
	card, err := store.CardOf([]byte(cardBefore))
	if err != nil {
		t.Fatalf("卡不可解析：%v", err)
	}
	if card.DeletedAt != nil {
		t.Fatal("前置不成立：这张卡不该带 deleted_at")
	}
	beforeCommits := gitLogCount(t, dir)
	beforePorcelain := gitOut(t, dir, "status", "--porcelain")

	code, _, output := runUndeleteCLI(t, newTestRoot(t, dir), dir,
		"--target", applyCardID, "--reason", "重复恢复")
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0（幂等 no-op 不是失败）：%s", code, output)
	}
	if got := string(mustRead(t, absIn(dir, cardRel))); got != cardBefore {
		t.Fatalf("目标字节发生变化：W11 幂等必须零写入\n前：%q\n后：%q", cardBefore, got)
	}
	if got := gitLogCount(t, dir); got != beforeCommits {
		t.Fatalf("commit 数 %d → %d，期望零 commit（不产生空 commit）", beforeCommits, got)
	}
	if got := gitOut(t, dir, "status", "--porcelain"); got != beforePorcelain {
		t.Fatalf("工作区状态变了：\n前：%q\n后：%q", beforePorcelain, got)
	}
	if !strings.Contains(output, UndeleteIdempotentMsg) {
		t.Fatalf("报告缺 W11 幂等说明 %q：\n%s", UndeleteIdempotentMsg, output)
	}
	// status 一字未动（幂等分支同样不碰状态）。
	afterCard, err := store.CardOf(mustRead(t, absIn(dir, cardRel)))
	if err != nil {
		t.Fatalf("卡不可解析：%v", err)
	}
	if afterCard.Status != model.StatusActive {
		t.Fatalf("卡 status = %q，期望仍是 %q", afterCard.Status, model.StatusActive)
	}
}
