package git

// `git log` 只读参数面（`--follow` / `--name-status`）的用例（M4 · T-…-054）。
//
// 三组：真实仓库上的跨领域 rename 采样、纯字符串解析的真值表、主题行动词位提取。
// 与本包既有用例同源复用 `newRepo` / `seed` 两个 helper（不另造一套建仓工具）。

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// commitAll 用给定主题把工作区全部改动提交（**仅用例内**的建数手段：
// 产品侧的提交口径仍是 Message.Build + Commit，本文件不新增任何写方法）。
func commitAll(t *testing.T, r *Repo, root, subject string) {
	t.Helper()
	if _, stderr, err := r.run(root, "add", "-A"); err != nil {
		t.Fatalf("add: %v %s", err, stderr)
	}
	if _, stderr, err := r.run(root, "commit", "-q", "-m", subject); err != nil {
		t.Fatalf("commit: %v %s", err, stderr)
	}
}

// TestFollowRenamesCrossDomain 在真实仓库上采样一次跨领域目录 rename。
func TestFollowRenamesCrossDomain(t *testing.T) {
	r, root := newRepo(t)
	oldRel := "domains/ai/knowledge/k-20261126-move.md"
	newRel := "domains/ops/knowledge/k-20261126-move.md"
	seed(t, root, oldRel, "---\nid: k-20261126-move\n---\n\n## 知识内容\n\n占位。\n")
	commitAll(t, r, root, "capture(ai): 收录一张卡")

	// 手工跨领域移动（用例内直接搬文件 + 提交，模拟用户在文件管理器里的操作）。
	abs := filepath.Join(root, newRel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, stderr, err := r.run(root, "mv", oldRel, newRel); err != nil {
		t.Fatalf("git mv: %v %s", err, stderr)
	}
	commitAll(t, r, root, "manual(ops): 手工把卡挪到另一个领域")

	recs, err := r.FollowRenames(newRel)
	if err != nil {
		t.Fatalf("FollowRenames: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("rename 记录 = %d 条，期望恰 1 条：%+v", len(recs), recs)
	}
	got := recs[0]
	if got.OldPath != oldRel || got.NewPath != newRel {
		t.Fatalf("rename 路径 = %q → %q，期望 %q → %q", got.OldPath, got.NewPath, oldRel, newRel)
	}
	if got.Verb != "manual" {
		t.Fatalf("rename 提交的 verb = %q，期望 %q（主题行动词位）", got.Verb, "manual")
	}
	if !strings.Contains(got.Subject, "手工把卡挪到另一个领域") {
		t.Fatalf("主题行未逐字带出：%q", got.Subject)
	}
	// 只读：采样不产生任何工作区改动。
	st, err := r.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.IsClean() {
		t.Fatalf("采样后工作区不干净：%+v", st.Changes)
	}
	// 没有 rename 历史的文件 → 空集合；空路径 → 空集合（都不报错）。
	seed(t, root, "domains/ai/knowledge/k-20261126-stay.md", "---\nid: k-20261126-stay\n---\n")
	commitAll(t, r, root, "capture(ai): 收录另一张卡")
	if recs, err := r.FollowRenames("domains/ai/knowledge/k-20261126-stay.md"); err != nil ||
		len(recs) != 0 {
		t.Fatalf("未移动过的文件应零 rename 记录，实得 %+v（err=%v）", recs, err)
	}
	if recs, err := r.FollowRenames("  "); err != nil || len(recs) != 0 {
		t.Fatalf("空路径应返回空集合，实得 %+v（err=%v）", recs, err)
	}
}

// TestParseFollowNameStatus 是解析口径的真值表（纯字符串运算，零 IO）。
func TestParseFollowNameStatus(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want []RenameRecord
	}{
		{"空输出", "", nil},
		{
			name: "一次 rename + 一次新增",
			out: "\x1fmanual(ops): 手工移动\nR100\tdomains/ai/knowledge/k-1.md\t" +
				"domains/ops/knowledge/k-1.md\n\n\x1fcapture(ai): 收录\nA\tdomains/ai/knowledge/k-1.md\n",
			want: []RenameRecord{{
				OldPath: "domains/ai/knowledge/k-1.md",
				NewPath: "domains/ops/knowledge/k-1.md",
				Subject: "manual(ops): 手工移动", Verb: "manual",
			}},
		},
		{
			name: "两跳 rename（新 → 旧的日志序逐条保留）",
			out: "\x1fmanual(ops): 第二跳\nR090\tdomains/ml/knowledge/k-1.md\t" +
				"domains/ops/knowledge/k-1.md\n\x1fprocess(ml): 第一跳\nR100\t" +
				"domains/ai/knowledge/k-1.md\tdomains/ml/knowledge/k-1.md\n",
			want: []RenameRecord{
				{OldPath: "domains/ml/knowledge/k-1.md", NewPath: "domains/ops/knowledge/k-1.md",
					Subject: "manual(ops): 第二跳", Verb: "manual"},
				{OldPath: "domains/ai/knowledge/k-1.md", NewPath: "domains/ml/knowledge/k-1.md",
					Subject: "process(ml): 第一跳", Verb: "process"},
			},
		},
		{"字段不足三段的 R 行丢弃", "\x1fmanual: x\nR100\tonly-one-path.md\n", nil},
		{"非 R 状态行不收", "\x1fmanual: x\nM\tdomains/ai/knowledge/k-1.md\nD\tx.md\nA\ty.md\n", nil},
		{
			name: "无主题哨兵时 rename 仍收（verb 为空 = 读不出动词位）",
			out:  "R100\ta/k-1.md\tb/k-1.md\n",
			want: []RenameRecord{{OldPath: "a/k-1.md", NewPath: "b/k-1.md"}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseFollowNameStatus([]byte(c.out))
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("解析结果 = %+v，期望 %+v", got, c.want)
			}
		})
	}
}

// TestSubjectVerb 是主题行动词位提取的真值表（不做归属判定、不做归一化）。
func TestSubjectVerb(t *testing.T) {
	cases := []struct {
		subject string
		want    string
	}{
		{"process(ai): 加工材料", "process"},
		{"capture(ai-infra): 收录材料", "capture"},
		{"reconcile(ops): 对账纳管", "reconcile"},
		{"manual(ops): 手工移动", "manual"},
		{"chore: 无领域段的主题", "chore"},
		{"", ""},
		{"   ", ""},
		{"没有分隔符的主题行", ""},
		{": 空动词位", ""},
		{"(ai): 空动词位", ""},
		{"两个 词(ai): 动词位含空白", ""},
	}
	for _, c := range cases {
		t.Run(c.subject, func(t *testing.T) {
			if got := SubjectVerb(c.subject); got != c.want {
				t.Fatalf("SubjectVerb(%q) = %q，期望 %q", c.subject, got, c.want)
			}
		})
	}
}

// TestLogFileHasNoWriteMethod 结构反证：本文件只读 —— 没有任何 Git 写方法，
// 且只出现 `log` 一个子命令字面量（task Acceptance 的两条 grep 的用例侧同源反证）。
func TestLogFileHasNoWriteMethod(t *testing.T) {
	raw, err := os.ReadFile("log.go")
	if err != nil {
		t.Fatalf("读 log.go：%v", err)
	}
	body := string(raw)
	for _, bad := range []string{"func (r *Repo) Commit(", "func (r *Repo) Add(",
		"func (r *Repo) Push(", "func (r *Repo) Reset(", "func (r *Repo) Checkout("} {
		if strings.Contains(body, bad) {
			t.Fatalf("log.go 出现写方法 %q：本文件必须是纯只读面", bad)
		}
	}
	for _, want := range []string{logFollowFlag, logNameStatusFlag} {
		if !strings.Contains(body, want) {
			t.Fatalf("log.go 缺只读参数 %q", want)
		}
	}
	if strings.Contains(body, `"commit"`) || strings.Contains(body, `"add"`) {
		t.Fatal("log.go 出现写子命令字面量：本文件只许跑 git log")
	}
}
