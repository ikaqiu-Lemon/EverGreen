package cli

// T-evergreen.s1_main_flow-158614-050 的写口侧机器判据（R1 纳管 commit）。
//
// 三组必备用例（task Acceptance「单元用例齐全」逐字点名）：
//   - TestR1TakeoverExactlyOneCommit —— 有改动 → 恰一次提交（第二次调用被拒，历史不再增长）；
//   - TestR1CleanTreeProducesNoCommit —— 零改动 / dry-run / 调用序不足 → 零提交零写入；
//   - TestR1AddAllSemanticsUnchanged —— 提交范围口径恒 `git add -A`（源码 + 行为双反证）。
//
// 用例一律用**真实 git 子进程**取证（与 M1 起的 e2e 同一口径）：提交条数、工作区状态、
// 提交主题的 verb 都从 git 自己读，不看实现自报。

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// —— 测试辅助：全部只读地问 git，事实不由被测代码自报 ——

// commitCountR1 返回 HEAD 的提交条数（`git rev-list --count HEAD`）。
func commitCountR1(t *testing.T, dir string) string {
	t.Helper()
	return strings.TrimSpace(gitOut(t, dir, "rev-list", "--count", "HEAD"))
}

// porcelainR1 返回 `git status --porcelain` 原文（空串 = 工作区干净）。
func porcelainR1(t *testing.T, dir string) string {
	t.Helper()
	return strings.TrimSpace(gitOut(t, dir, "status", "--porcelain"))
}

// headSubjectR1 返回 HEAD 提交的主题行。
func headSubjectR1(t *testing.T, dir string) string {
	t.Helper()
	return strings.TrimSpace(gitOut(t, dir, "log", "-1", "--pretty=%s"))
}

// headBodyR1 返回 HEAD 提交的正文。
func headBodyR1(t *testing.T, dir string) string {
	t.Helper()
	return gitOut(t, dir, "log", "-1", "--pretty=%b")
}

// externalEditR1 模拟「用户在编辑器里直接改文件」：写盘但**不**提交。
func externalEditR1(t *testing.T, dir, rel, content string) {
	t.Helper()
	abs := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, abs, content)
}

// takeoverInputR1 是一份「检查与修复都已跑完」的标准输入（只改需要变的那一格）。
func takeoverInputR1() ReconcileTakeoverInput {
	return ReconcileTakeoverInput{
		Domain:         "ai-infra",
		Findings:       1,
		ChecksDone:     true,
		RepairsDone:    true,
		RequirementIDs: []string{"EG-EDIT-05"},
	}
}

// —— ① 恰一次 commit：有改动提交一次，第二次调用被拒 ——

func TestR1TakeoverExactlyOneCommit(t *testing.T) {
	cases := []struct {
		name  string
		edits map[string]string
	}{
		{"改一个已跟踪文件", map[string]string{
			ConfigFileName: "version: 1\ndomains:\n  - ai-infra\ndefault_domain: ai-infra\n# 用户手改\n",
		}},
		{"改两个已跟踪文件", map[string]string{
			ConfigFileName:      "version: 1\ndomains:\n  - ai-infra\ndefault_domain: ai-infra\n# 手改 1\n",
			UnprocessedFileName: "# 未处理清单\n\n- 用户手写的一行\n",
		}},
		{"新增未跟踪文件 + 改已跟踪文件", map[string]string{
			"sources/s-20261120-a.md": "---\nid: s-20261120-a\n---\n\n用户手放的原文\n",
			ConfigFileName:            "version: 1\ndomains:\n  - ai-infra\ndefault_domain: ai-infra\n# 手改 2\n",
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, _, _ := initVault(t, "--domain", "ai-infra")
			before := commitCountR1(t, dir)
			for rel, body := range c.edits {
				externalEditR1(t, dir, rel, body)
			}
			if porcelainR1(t, dir) == "" {
				t.Fatal("前置条件失败：外部编辑后工作区应为脏")
			}

			tk := newTestRoot(t, dir).newReconcileTakeover(dir)
			res, err := tk.Takeover(takeoverInputR1())
			if err != nil {
				t.Fatalf("纳管失败：%v", err)
			}
			// ① 提交恰一次，且回执是单值。
			if res.Commit == nil || *res.Commit == "" {
				t.Fatalf("有改动时必须产生恰一条提交，实际 Commit=%v", res.Commit)
			}
			if got := tk.Commits(); got != 1 {
				t.Fatalf("写口提交条数 = %d，应恰 1", got)
			}
			// ② git 历史恰 +1（事实由 git 自己给）。
			after := commitCountR1(t, dir)
			if !plusOneR1(before, after) {
				t.Fatalf("git 历史条数 %s → %s，应恰 +1", before, after)
			}
			// ③ 纳管后工作区干净（改动确实进了历史，不是空转）。
			if s := porcelainR1(t, dir); s != "" {
				t.Fatalf("纳管后 git status 应为空，实际 %q", s)
			}
			// ④ commit verb 取 A-30 裁决值 reconcile，且不得退化成 process。
			subject := headSubjectR1(t, dir)
			if !strings.HasPrefix(subject, "reconcile(ai-infra): ") {
				t.Fatalf("提交主题 = %q，应以 reconcile(<domain>): 开头（A-30）", subject)
			}
			for _, w := range res.Warnings {
				if strings.Contains(w, "未知，已退化为") {
					t.Fatalf("verb 退化警告不该出现（reconcile 属已知 verb）：%q", w)
				}
			}
			if body := headBodyR1(t, dir); !strings.Contains(body, "Reason:") ||
				!strings.Contains(body, "EG-EDIT-05") {
				t.Fatalf("提交正文缺 Reason: / Requirement: 行：%q", body)
			}
			// ⑤ 反证「恰一次」：同一实例第二次调用直接被拒，历史不再增长。
			res2, err2 := tk.Takeover(takeoverInputR1())
			if !errors.Is(err2, ErrTakeoverAlreadyRan) {
				t.Fatalf("第二次纳管应返回 ErrTakeoverAlreadyRan，实际 %v", err2)
			}
			if res2.Commit != nil {
				t.Fatalf("被拒的第二次纳管不得带提交：%v", *res2.Commit)
			}
			if got := tk.Commits(); got != 1 {
				t.Fatalf("被拒后提交条数 = %d，仍应恰 1", got)
			}
			if now := commitCountR1(t, dir); now != after {
				t.Fatalf("被拒的第二次纳管改变了历史条数：%s → %s", after, now)
			}
			if !tk.Ran() {
				t.Fatal("写口跑过之后 Ran() 必须为 true")
			}
		})
	}
}

// plusOneR1 判定十进制计数字符串是否恰 +1。
func plusOneR1(before, after string) bool {
	return atoiR1(after) == atoiR1(before)+1
}

func atoiR1(s string) int {
	n := 0
	for _, c := range strings.TrimSpace(s) {
		if c < '0' || c > '9' {
			return -1
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// —— ② 零改动即零 commit（外加 dry-run 与调用序两条零写入分支）——

func TestR1CleanTreeProducesNoCommit(t *testing.T) {
	// (a) 干净工作区：Ran=true / Commit=nil / 历史条数不变。
	dir, _, _ := initVault(t, "--domain", "ai-infra")
	before := commitCountR1(t, dir)
	if s := porcelainR1(t, dir); s != "" {
		t.Fatalf("前置条件失败：eg init 后工作区应干净，实际 %q", s)
	}
	snapBefore := snapshot(t, dir)

	tk := newTestRoot(t, dir).newReconcileTakeover(dir)
	res, err := tk.Takeover(takeoverInputR1())
	if err != nil {
		t.Fatalf("干净工作区纳管不应报错：%v", err)
	}
	if !res.Ran {
		t.Fatal("零改动分支同样属于「跑过」：Ran 必须为 true（reconcile.ran=true）")
	}
	if res.Commit != nil {
		t.Fatalf("零改动必须零提交（不产生空提交），实际 %v", *res.Commit)
	}
	if got := tk.Commits(); got != 0 {
		t.Fatalf("零改动时提交条数 = %d，应恰 0", got)
	}
	if now := commitCountR1(t, dir); now != before {
		t.Fatalf("零改动却改变了历史条数：%s → %s", before, now)
	}
	if s := porcelainR1(t, dir); s != "" {
		t.Fatalf("零改动纳管后工作区应仍干净，实际 %q", s)
	}
	if snapshot(t, dir) != snapBefore {
		t.Fatal("零改动纳管不得写盘一个字节")
	}
	if !containsR1(res.Warnings, ReconcileZeroChangeNotice) {
		t.Fatalf("零改动应给出固定交代文案，实际 %v", res.Warnings)
	}

	// (b) dry-run：工作区脏也零写入零提交，清单照报。
	dir2, _, _ := initVault(t, "--domain", "ai-infra")
	externalEditR1(t, dir2, ConfigFileName,
		"version: 1\ndomains:\n  - ai-infra\ndefault_domain: ai-infra\n# 手改\n")
	before2 := commitCountR1(t, dir2)
	dirty2 := porcelainR1(t, dir2)
	in := takeoverInputR1()
	in.DryRun = true
	res2, err := newTestRoot(t, dir2).newReconcileTakeover(dir2).Takeover(in)
	if err != nil {
		t.Fatalf("dry-run 纳管不应报错：%v", err)
	}
	if res2.Commit != nil {
		t.Fatalf("dry-run 必须零提交，实际 %v", *res2.Commit)
	}
	if len(res2.Pending) == 0 {
		t.Fatal("dry-run 必须如实回报待纳管清单")
	}
	if now := commitCountR1(t, dir2); now != before2 {
		t.Fatalf("dry-run 改变了历史条数：%s → %s", before2, now)
	}
	if now := porcelainR1(t, dir2); now != dirty2 {
		t.Fatalf("dry-run 改变了工作区状态：%q → %q", dirty2, now)
	}
	if !containsR1(res2.Warnings, ReconcileDryRunNotice) {
		t.Fatalf("dry-run 应给出固定交代文案，实际 %v", res2.Warnings)
	}

	// (c) 调用序不足（检查 / 修复未跑完）：一律拒绝，零提交零写入。
	for _, c := range []struct {
		name   string
		mutate func(*ReconcileTakeoverInput)
	}{
		{"检查未跑完", func(i *ReconcileTakeoverInput) { i.ChecksDone = false }},
		{"修复未跑完", func(i *ReconcileTakeoverInput) { i.RepairsDone = false }},
	} {
		dir3, _, _ := initVault(t, "--domain", "ai-infra")
		externalEditR1(t, dir3, ConfigFileName, "version: 1\ndomains:\n  - ai-infra\n# 手改\n")
		before3 := commitCountR1(t, dir3)
		bad := takeoverInputR1()
		c.mutate(&bad)
		tk3 := newTestRoot(t, dir3).newReconcileTakeover(dir3)
		res3, err := tk3.Takeover(bad)
		if !errors.Is(err, ErrTakeoverTooEarly) {
			t.Fatalf("%s：应返回 ErrTakeoverTooEarly，实际 %v", c.name, err)
		}
		if res3.Ran || res3.Commit != nil || tk3.Commits() != 0 {
			t.Fatalf("%s：被拒的调用不得留下任何提交痕迹：%+v", c.name, res3)
		}
		if now := commitCountR1(t, dir3); now != before3 {
			t.Fatalf("%s：被拒的调用改变了历史条数：%s → %s", c.name, before3, now)
		}
	}
}

func containsR1(list []string, want string) bool {
	for _, s := range list {
		if strings.Contains(s, want) {
			return true
		}
	}
	return false
}

// —— ③ `git add -A` 口径一字未改（源码反证 + 行为反证）——

func TestR1AddAllSemanticsUnchanged(t *testing.T) {
	raw, err := os.ReadFile("reconcile_commit.go")
	if err != nil {
		t.Fatalf("写口实现文件缺失：%v", err)
	}
	text := string(raw)
	// (a) 口径字面量在册（Acceptance：`add -A|AddAll` ≥ 1）。
	if !strings.Contains(text, "add -A") {
		t.Fatal("写口必须逐字登记 `git add -A` 口径（R-15：不得偷改提交范围）")
	}
	// (b) 不得改成选择性暂存 / 破坏性 Git 操作。
	for _, banned := range []string{"add --", "add -u", "add -p", `"add",`, "checkout --",
		"reset --hard", "RemoveAll"} {
		if strings.Contains(text, banned) {
			t.Fatalf("写口出现被禁的 Git 形态 %q（口径只许 add -A）", banned)
		}
	}
	// (c) 写口边界：直连 internal/git，不得触碰知识数据写盘层（合同 §1.3）。
	if !strings.Contains(text, `github.com/ikaqiu-Lemon/EverGreen/internal/git"`) {
		t.Fatal("写口必须直连 internal/git")
	}
	for _, banned := range []string{`github.com/ikaqiu-Lemon/EverGreen/internal/` + `store"`, `github.com/ikaqiu-Lemon/EverGreen/internal/plan"`} {
		if strings.Contains(text, banned) {
			t.Fatalf("写口不得 import %q（知识数据修改走 ChangePlan，不在本文件）", banned)
		}
	}
	// (d) 写口不写文件、不注册命令。
	for _, banned := range []string{"os.WriteFile", "os.Create", "r.register(", "Command{"} {
		if strings.Contains(text, banned) {
			t.Fatalf("写口不得出现 %q（只提交既有事实，且不注册命令）", banned)
		}
	}

	// (e) 行为反证：本次写入 + 工作区既有改动**一并**进同一条提交（add -A 的语义）。
	dir, _, _ := initVault(t, "--domain", "ai-infra")
	before := commitCountR1(t, dir)
	const ours = "sources/s-20261120-b.md"
	externalEditR1(t, dir, ours, "---\nid: s-20261120-b\n---\n\neg 自己写入的原文\n")
	externalEditR1(t, dir, ConfigFileName,
		"version: 1\ndomains:\n  - ai-infra\ndefault_domain: ai-infra\n# 用户手改（既有改动）\n")

	in := takeoverInputR1()
	in.OurWrites = []string{ours}
	tk := newTestRoot(t, dir).newReconcileTakeover(dir)
	res, err := tk.Takeover(in)
	if err != nil {
		t.Fatalf("纳管失败：%v", err)
	}
	if got := tk.Commits(); got != 1 {
		t.Fatalf("提交条数 = %d，应恰 1（既有改动不另开一条提交）", got)
	}
	if !plusOneR1(before, commitCountR1(t, dir)) {
		t.Fatal("git 历史应恰 +1")
	}
	if !containsR1(res.Existing, ConfigFileName) {
		t.Fatalf("既有改动应被如实区分出来，实际 %v", res.Existing)
	}
	files := strings.Join(res.Files, "\n")
	if !strings.Contains(files, ours) || !strings.Contains(files, ConfigFileName) {
		t.Fatalf("同一条提交必须同时含本次写入与既有改动（add -A 口径），实际 %v", res.Files)
	}
	if s := porcelainR1(t, dir); s != "" {
		t.Fatalf("add -A 之后工作区应干净，实际 %q", s)
	}
	if !containsR1(res.Warnings, ReconcileTakeoverNotice) {
		t.Fatalf("纳管应交代「只记事实、不改写内容」，实际 %v", res.Warnings)
	}
}

// —— ④ 提交失败：退出码 4 + 提交条数仍 0（B4：磁盘保留现状，不做还原）——

func TestR1TakeoverCommitFailureKeepsZeroCommit(t *testing.T) {
	dir, _, _ := initVault(t, "--domain", "ai-infra")
	externalEditR1(t, dir, ConfigFileName, "version: 1\ndomains:\n  - ai-infra\n# 手改\n")
	before := commitCountR1(t, dir)

	r := newTestRoot(t, dir)
	r.NewRepo = failingCommitRepo()
	tk := r.newReconcileTakeover(dir)
	res, err := tk.Takeover(takeoverInputR1())
	var failed *CommitFailedError
	if !errors.As(err, &failed) {
		t.Fatalf("提交失败应返回 *CommitFailedError，实际 %v", err)
	}
	if got := ExitCodeFor(err); got != ExitCommitFailed {
		t.Fatalf("提交失败的退出码 = %d，应为 %d", got, ExitCommitFailed)
	}
	if res.Commit != nil || tk.Commits() != 0 {
		t.Fatalf("提交失败时提交条数必须为 0：%+v", res)
	}
	if now := commitCountR1(t, dir); now != before {
		t.Fatalf("提交失败却改变了历史条数：%s → %s", before, now)
	}
	if s := porcelainR1(t, dir); s == "" {
		t.Fatal("B4：提交失败后磁盘保留现状，改动不得被还原（工作区应仍非空）")
	}
}
