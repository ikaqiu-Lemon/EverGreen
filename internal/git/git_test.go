package git

import (
	"bytes"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func newRepo(t *testing.T) (*Repo, string) {
	t.Helper()
	root := t.TempDir()
	r, err := Init(root)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	for _, cfg := range [][]string{
		{"config", "user.email", "eg@example.com"},
		{"config", "user.name", "eg"},
		{"config", "commit.gpgsign", "false"},
	} {
		if _, stderr, err := r.run(root, cfg...); err != nil {
			t.Fatalf("config %v: %v %s", cfg, err, stderr)
		}
	}
	return r, root
}

func seed(t *testing.T, root, rel, content string) string {
	t.Helper()
	abs := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return abs
}

func demoMessage() Message {
	return Message{
		Verb:           "process",
		Domain:         "ai",
		Subject:        "加工材料笔记 n-20260901-x",
		Reason:         "用户要求把这份材料转成知识卡",
		RequirementIDs: []string{"EG-CVG-04", "EG-AGT-03"},
	}
}

// ---------- 提交信息规范 ----------

func TestMessageSubjectAndBody(t *testing.T) {
	subject, body, warnings, err := demoMessage().Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if subject != "process(ai): 加工材料笔记 n-20260901-x" {
		t.Fatalf("主题必须严格是 <verb>(<domain>): <subject>，得到 %q", subject)
	}
	lines := strings.Split(body, "\n")
	if len(lines) != 2 {
		t.Fatalf("正文应恰两行，得到 %q", body)
	}
	if lines[0] != "Reason: 用户要求把这份材料转成知识卡" {
		t.Fatalf("Reason 行取自 plan.reason，得到 %q", lines[0])
	}
	if lines[1] != "Requirement: EG-CVG-04, EG-AGT-03" {
		t.Fatalf("Requirement 行取自 plan.requirement_ids，得到 %q", lines[1])
	}
	if len(warnings) != 0 {
		t.Fatalf("齐全时不应有 warning：%v", warnings)
	}
}

func TestMessageMissingRequirementIDs(t *testing.T) {
	m := demoMessage()
	m.RequirementIDs = nil
	_, body, warnings, err := m.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if strings.Contains(body, RequirementPrefix) {
		t.Fatalf("缺失时正文不得有 Requirement 行：%q", body)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "requirement_ids") {
		t.Fatalf("应返回一条 requirement_ids 缺失 warning：%v", warnings)
	}
}

func TestMessageMissingReason(t *testing.T) {
	m := demoMessage()
	m.Reason = ""
	_, body, warnings, err := m.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if strings.Contains(body, ReasonPrefix) {
		t.Fatalf("缺失时正文不得有 Reason 行：%q", body)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "reason") {
		t.Fatalf("应返回一条 reason 缺失 warning：%v", warnings)
	}
}

func TestMessageDomainRequired(t *testing.T) {
	m := demoMessage()
	m.Domain = "  "
	if _, _, _, err := m.Build(); !errors.Is(err, ErrDomainRequired) {
		t.Fatalf("domain 缺失应报错，得到 %v", err)
	}
}

func TestUnknownVerbFallsBackToProcess(t *testing.T) {
	r, root := newRepo(t)
	seed(t, root, "cards/k.md", "# 卡\n")
	m := demoMessage()
	m.Verb = "frobnicate"
	info, err := r.Commit(m)
	if err != nil {
		t.Fatalf("未知 verb 不得拒绝提交：%v", err)
	}
	if !info.Created {
		t.Fatal("应产生 commit")
	}
	if !strings.HasPrefix(info.Subject, "process(ai): ") {
		t.Fatalf("未知 verb 应退化为 process，得到 %q", info.Subject)
	}
	var found bool
	for _, w := range info.Warnings {
		if strings.Contains(w, "frobnicate") {
			found = true
		}
	}
	if !found {
		t.Fatalf("应返回未知 verb 的 warning：%v", info.Warnings)
	}
	subjects, err := r.LogSubjects()
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if len(subjects) != 1 || !strings.HasPrefix(subjects[0], "process(ai): ") {
		t.Fatalf("实际 commit 主题应用 process：%v", subjects)
	}
}

// ---------- 幂等：无改动不产生空 commit ----------

func TestCommitNoChangesProducesNoCommit(t *testing.T) {
	r, root := newRepo(t)
	seed(t, root, "cards/k.md", "# 卡\n")
	if _, err := r.Commit(demoMessage()); err != nil {
		t.Fatalf("首个 commit: %v", err)
	}
	before, err := r.LogSubjects()
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	info, err := r.Commit(demoMessage())
	if err != nil {
		t.Fatalf("无改动时不应报错：%v", err)
	}
	if info.Created {
		t.Fatal("无改动时不得产生空 commit")
	}
	after, err := r.LogSubjects()
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if len(before) != len(after) {
		t.Fatalf("git log 条数应不变：%d → %d", len(before), len(after))
	}
}

// ---------- 脏工作区一并进本次 commit ----------

func TestDirtyWorkspaceGoesIntoSameCommit(t *testing.T) {
	r, root := newRepo(t)
	seed(t, root, "cards/k.md", "# 卡\n")
	if _, err := r.Commit(demoMessage()); err != nil {
		t.Fatalf("首个 commit: %v", err)
	}
	// 本次写入
	seed(t, root, "cards/k2.md", "# 新卡\n")
	// 与本次写入无关的既有脏改动
	seed(t, root, "手写笔记.md", "用户自己写的内容\n")

	sample, err := r.Sample([]string{"cards/k2.md"})
	if err != nil {
		t.Fatalf("sample: %v", err)
	}
	if len(sample.Ours) != 1 || sample.Ours[0] != "cards/k2.md" {
		t.Fatalf("本次改动清单应含 cards/k2.md：%v", sample.Ours)
	}
	if len(sample.Existing) != 1 || sample.Existing[0] != "手写笔记.md" {
		t.Fatalf("既有改动清单应含手写笔记.md：%v", sample.Existing)
	}

	info, err := r.Commit(demoMessage())
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	files := strings.Join(info.Files, "\n")
	if !strings.Contains(files, "cards/k2.md") || !strings.Contains(files, "手写笔记.md") {
		t.Fatalf("git add -A 语义：两者都应在本次 commit 里，得到 %v", info.Files)
	}
	st, err := r.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !st.IsClean() {
		t.Fatalf("提交后 git status --porcelain 应为空，得到 %v", st.Changes)
	}
}

// ---------- B4：提交失败不做破坏性回滚 ----------

func TestB4CommitFailureKeepsDiskState(t *testing.T) {
	r, root := newRepo(t)
	seed(t, root, "cards/k.md", "# 卡\n")
	if _, err := r.Commit(demoMessage()); err != nil {
		t.Fatalf("首个 commit: %v", err)
	}
	written := "# 新卡\n\n本次写入的字节\n"
	abs := seed(t, root, "cards/k2.md", written)
	// 注入提交失败：索引锁已存在
	lock := filepath.Join(root, ".git", "index.lock")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatalf("lock: %v", err)
	}
	entriesBefore := listFiles(t, root)

	_, err := r.Commit(demoMessage())
	var failed *CommitFailed
	if !errors.As(err, &failed) {
		t.Fatalf("应返回 *CommitFailed，得到 %v", err)
	}
	if len(failed.Uncommitted) == 0 {
		t.Fatal("CommitFailed 必须携带未提交变更清单")
	}
	if !strings.Contains(strings.Join(failed.Uncommitted, ","), "cards/k2.md") {
		t.Fatalf("未提交清单应含本次写入：%v", failed.Uncommitted)
	}
	got, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, []byte(written)) {
		t.Fatal("磁盘文件必须保持写入后状态（字节比对）")
	}
	if diff := diffFileSets(entriesBefore, listFiles(t, root)); diff != "" {
		t.Fatalf("失败过程中不得删除或新增文件：%s", diff)
	}
}

func TestB4RunnerFailureIsReportedNotRolledBack(t *testing.T) {
	root := t.TempDir()
	var calls []string
	stub := func(dir string, args ...string) ([]byte, []byte, error) {
		calls = append(calls, strings.Join(args, " "))
		switch args[0] {
		case "status":
			return []byte("?? cards/k.md\x00"), nil, nil
		case "add":
			return nil, nil, nil
		case "commit":
			return nil, []byte("fatal: 注入的提交失败"), errors.New("exit status 128")
		}
		return nil, nil, nil
	}
	r := NewWithRunner(root, stub)
	_, err := r.Commit(demoMessage())
	var failed *CommitFailed
	if !errors.As(err, &failed) {
		t.Fatalf("应返回 *CommitFailed，得到 %v", err)
	}
	if !strings.Contains(failed.Reason, "注入的提交失败") {
		t.Fatalf("失败原因应来自 git stderr：%q", failed.Reason)
	}
	if !strings.Contains(failed.Error(), "不做破坏性回滚") {
		t.Fatalf("错误文案应写明不做破坏性回滚：%q", failed.Error())
	}
	for _, c := range calls {
		for _, forbidden := range forbiddenSubcommands() {
			if strings.HasPrefix(c, forbidden) {
				t.Fatalf("失败后不得执行撤销类子命令：%q", c)
			}
		}
	}
}

func forbiddenSubcommands() []string {
	return []string{"check" + "out", "res" + "et", "cle" + "an", "rest" + "ore", "rev" + "ert", "rm", "stash"}
}

func TestNoRollbackEntrypoints(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	forbiddenText := []string{"check" + "out", "res" + "et --hard", "cle" + "an -", "git " + "rm", "sta" + "sh"}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		for _, bad := range forbiddenText {
			if strings.Contains(string(src), bad) {
				t.Fatalf("%s 出现撤销类 Git 能力 %q：B4 禁止实现", f, bad)
			}
		}
	}
	rollbackish := regexp.MustCompile(`^(Checkout|Reset|Restore|Revert|Rollback|Undo|Discard|Clean|Remove|Delete|Prune)`)
	for _, name := range exportedNames(t) {
		if rollbackish.MatchString(name) {
			t.Fatalf("导出符号 %q 像回滚 / 还原 API：B4 禁止提供", name)
		}
	}
}

func exportedNames(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse dir: %v", err)
	}
	var names []string
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				switch d := decl.(type) {
				case *ast.FuncDecl:
					if d.Name.IsExported() {
						names = append(names, d.Name.Name)
					}
				case *ast.GenDecl:
					for _, spec := range d.Specs {
						if ts, ok := spec.(*ast.TypeSpec); ok && ts.Name.IsExported() {
							names = append(names, ts.Name.Name)
						}
					}
				}
			}
		}
	}
	return names
}

// ---------- init 产物 ----------

func TestInitWritesGitignoreAndIsIdempotent(t *testing.T) {
	_, root := newRepo(t)
	got, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if string(got) != GitignoreContent {
		t.Fatalf(".gitignore 内容应为 %q，得到 %q", GitignoreContent, got)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".index/\n# 用户自己加的\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Init(root); err != nil {
		t.Fatalf("再次 init 应幂等：%v", err)
	}
	again, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(again), "# 用户自己加的") {
		t.Fatal("已存在的 .gitignore 不得被改写")
	}
}

func TestCommitBodyLandsInGitLog(t *testing.T) {
	r, root := newRepo(t)
	seed(t, root, "cards/k.md", "# 卡\n")
	info, err := r.Commit(demoMessage())
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	body, err := r.BodyOf(info.SHA)
	if err != nil {
		t.Fatalf("body: %v", err)
	}
	if !bytes.Contains(body, []byte("Reason: 用户要求把这份材料转成知识卡")) {
		t.Fatalf("正文应含 Reason 行：%q", body)
	}
	if !bytes.Contains(body, []byte("Requirement: EG-CVG-04, EG-AGT-03")) {
		t.Fatalf("正文应含 Requirement 行：%q", body)
	}
}

// ---------- 工具 ----------

func listFiles(t *testing.T, root string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if strings.HasPrefix(rel, ".git"+string(filepath.Separator)) {
			return nil
		}
		out[filepath.ToSlash(rel)] = true
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	return out
}

func diffFileSets(before, after map[string]bool) string {
	var msgs []string
	for p := range before {
		if !after[p] {
			msgs = append(msgs, "被删除："+p)
		}
	}
	for p := range after {
		if !before[p] {
			msgs = append(msgs, "被新增："+p)
		}
	}
	return strings.Join(msgs, "；")
}
