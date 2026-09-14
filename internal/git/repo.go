package git

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// GitignoreContent 是 `eg init` 写入的 .gitignore 内容（`.index/` 属 S4 目标态的产物目录）。
const GitignoreContent = ".index/\n"

// Runner 执行一次 git 子命令并返回 stdout / stderr。抽成函数类型便于注入失败用例。
type Runner func(dir string, args ...string) (stdout, stderr []byte, err error)

// Repo 是 vault 对应的本地仓库（vault/ 即仓库根）。v1 只在本地，不做远端同步 / push。
type Repo struct {
	root string
	run  Runner
}

// New 以 vault 根构造 Repo（走真实 git 可执行文件）。
func New(root string) *Repo { return &Repo{root: filepath.Clean(root), run: execGit} }

// NewWithRunner 注入自定义 Runner（用于测试注入失败分支）。
func NewWithRunner(root string, run Runner) *Repo {
	return &Repo{root: filepath.Clean(root), run: run}
}

// Root 返回仓库根。
func (r *Repo) Root() string { return r.root }

func execGit(dir string, args ...string) ([]byte, []byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// Init 初始化仓库并保证 .gitignore 存在（内容 `.index/`）。已有仓库时幂等。
func Init(root string) (*Repo, error) {
	r := New(root)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	if _, stderr, err := r.run(root, "init", "-q"); err != nil {
		return nil, wrapGit("init", stderr, err)
	}
	ignore := filepath.Join(root, ".gitignore")
	if _, err := os.Stat(ignore); os.IsNotExist(err) {
		if err := os.WriteFile(ignore, []byte(GitignoreContent), 0o644); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	return r, nil
}

// ExcludeRel 是仓库**本地**忽略清单的相对路径（不属工作区、永不进任何 commit）。
const ExcludeRel = ".git/info/exclude"

// Exclude 幂等地把一条 pattern 写进 `.git/info/exclude`。
//
// 用途：把 `eg` 的工程状态目录（如最近一次报告的落盘位置）挡在版本控制之外，
// 同时**不改用户的 .gitignore**（用户文件逐字保留，B2 同源精神）。
// 已存在同一行时零字节改动。
func (r *Repo) Exclude(pattern string) error {
	abs := filepath.Join(r.root, ".git", "info", "exclude")
	raw, err := os.ReadFile(abs)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == pattern {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	out := raw
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	out = append(out, (pattern + "\n")...)
	return os.WriteFile(abs, out, 0o644)
}

// Change 是工作区里的一处改动（porcelain 的 XY 状态码 + 路径）。
type Change struct {
	Code string
	Path string
}

// Status 是 `git status --porcelain` 的采样结果。
type Status struct {
	Changes []Change
}

// IsClean 表示工作区与 HEAD 完全一致（无改动、无未跟踪文件）。
func (s Status) IsClean() bool { return len(s.Changes) == 0 }

// Paths 返回全部改动路径。
func (s Status) Paths() []string {
	out := make([]string, 0, len(s.Changes))
	for _, c := range s.Changes {
		out = append(out, c.Path)
	}
	return out
}

// Status 采样工作区状态（只读）。
func (r *Repo) Status() (Status, error) {
	stdout, stderr, err := r.run(r.root, "status", "--porcelain", "-z", "--untracked-files=all")
	if err != nil {
		return Status{}, wrapGit("status", stderr, err)
	}
	return parsePorcelainZ(stdout), nil
}

func parsePorcelainZ(out []byte) Status {
	var st Status
	records := bytes.Split(out, []byte{0})
	for i := 0; i < len(records); i++ {
		rec := records[i]
		if len(rec) < 4 {
			continue
		}
		code := string(rec[:2])
		path := string(bytes.TrimLeft(rec[2:], " "))
		// 重命名 / 拷贝的记录后面紧跟一条「原路径」，跳过之。
		if code[0] == 'R' || code[0] == 'C' {
			i++
		}
		st.Changes = append(st.Changes, Change{Code: code, Path: path})
	}
	return st
}

// Diff 返回工作区相对索引的 diff 原文（只读，供报告如实展示）。
func (r *Repo) Diff() ([]byte, error) {
	stdout, stderr, err := r.run(r.root, "diff")
	if err != nil {
		return nil, wrapGit("diff", stderr, err)
	}
	return stdout, nil
}

// DiffNames 返回工作区相对索引发生改动的路径（只读）。
func (r *Repo) DiffNames() ([]string, error) {
	stdout, stderr, err := r.run(r.root, "diff", "--name-only", "-z")
	if err != nil {
		return nil, wrapGit("diff", stderr, err)
	}
	return splitZ(stdout), nil
}

// Sample 是 Add 前的两份路径清单：本次写入的改动 / 工作区已有改动。
//
// S1 不做工作区隔离式的选择性提交：两份清单**都会**进本次 commit（`git add -A`），
// 分开只为让上层在报告里如实说明「既有改动被一并提交」。
type Sample struct {
	Ours     []string
	Existing []string
}

// Sample 在 Add 之前采样，ours 是本次写入涉及的仓库相对路径。
func (r *Repo) Sample(ours []string) (Sample, error) {
	st, err := r.Status()
	if err != nil {
		return Sample{}, err
	}
	mine := make(map[string]bool, len(ours))
	for _, p := range ours {
		mine[filepath.ToSlash(p)] = true
	}
	var s Sample
	for _, c := range st.Changes {
		if mine[c.Path] {
			s.Ours = append(s.Ours, c.Path)
			continue
		}
		s.Existing = append(s.Existing, c.Path)
	}
	return s, nil
}

// Add 是唯一的提交范围口径：`git add -A`（本次改动与既有改动一并进本次 commit）。
func (r *Repo) Add() error {
	if _, stderr, err := r.run(r.root, "add", "-A"); err != nil {
		return wrapGit("add -A", stderr, err)
	}
	return nil
}

// CommitInfo 是一次提交的结果回执。Created=false 表示「无改动，未产生空 commit」。
type CommitInfo struct {
	Created  bool
	SHA      string
	Subject  string
	Body     string
	Files    []string
	Warnings []string
}

// Commit 把工作区当前状态提交为一次 commit。
//
// 工作区与 HEAD 完全一致（本次无写入且无既有改动）→ 不产生空 commit；
// 提交失败 → 返回 *CommitFailed（携带未提交变更清单与原因），磁盘保留现状（B4）。
func (r *Repo) Commit(m Message) (CommitInfo, error) {
	subject, body, warnings, err := m.Build()
	if err != nil {
		return CommitInfo{}, err
	}
	st, err := r.Status()
	if err != nil {
		return CommitInfo{Warnings: warnings}, err
	}
	if st.IsClean() {
		head, _ := r.Head()
		return CommitInfo{
			Created:  false,
			SHA:      head,
			Subject:  subject,
			Body:     body,
			Warnings: append(warnings, "工作区与 HEAD 一致，未产生空 commit"),
		}, nil
	}
	pending := st.Paths()
	if err := r.Add(); err != nil {
		return CommitInfo{Warnings: warnings}, &CommitFailed{
			Reason: err.Error(), Uncommitted: pending, Err: err,
		}
	}
	args := []string{"commit", "-m", subject}
	if body != "" {
		args = append(args, "-m", body)
	}
	if _, stderr, err := r.run(r.root, args...); err != nil {
		return CommitInfo{Warnings: warnings}, &CommitFailed{
			Reason:      strings.TrimSpace(string(stderr)),
			Uncommitted: pending,
			Err:         err,
		}
	}
	sha, err := r.Head()
	if err != nil {
		return CommitInfo{Created: true, Subject: subject, Body: body, Warnings: warnings}, err
	}
	files, err := r.Files(sha)
	if err != nil {
		return CommitInfo{Created: true, SHA: sha, Subject: subject, Body: body, Warnings: warnings}, err
	}
	return CommitInfo{
		Created: true, SHA: sha, Subject: subject, Body: body, Files: files, Warnings: warnings,
	}, nil
}

// Head 返回 HEAD 的 commit sha；空仓库返回空串。
func (r *Repo) Head() (string, error) {
	stdout, _, err := r.run(r.root, "rev-parse", "HEAD")
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(string(stdout)), nil
}

// Files 返回某次 commit 涉及的文件清单（只读）。
func (r *Repo) Files(sha string) ([]string, error) {
	stdout, stderr, err := r.run(r.root, "show", "--name-only", "--pretty=format:", "-z", sha)
	if err != nil {
		return nil, wrapGit("show", stderr, err)
	}
	return splitZ(stdout), nil
}

// LogSubjects 返回从新到旧的提交主题（只读，供报告与用例断言）。
func (r *Repo) LogSubjects() ([]string, error) {
	stdout, _, err := r.run(r.root, "log", "--pretty=format:%s", "-z")
	if err != nil {
		return nil, nil
	}
	return splitZ(stdout), nil
}

// BodyOf 返回某次 commit 的正文（只读）。
func (r *Repo) BodyOf(sha string) ([]byte, error) {
	stdout, stderr, err := r.run(r.root, "log", "-1", "--pretty=format:%b", sha)
	if err != nil {
		return nil, wrapGit("log", stderr, err)
	}
	return stdout, nil
}

func splitZ(out []byte) []string {
	var res []string
	for _, part := range bytes.Split(out, []byte{0}) {
		s := strings.TrimSpace(string(part))
		if s != "" {
			res = append(res, s)
		}
	}
	return res
}

// 兜底提交身份：机器上没有可解析的 Git 身份时，为**本仓库**设置的确定性身份。
// 只写 --local（不碰用户全局配置），且由上层出 warning 如实告知。
const (
	FallbackUserName  = "Evergreen"
	FallbackUserEmail = "eg@localhost"
)

// EnsureIdentity 保证本仓库有可用的提交身份。
//
// git 已能解析出身份（全局 / 本地 / 环境变量任一）时什么都不做，返回 false；
// 否则写入本仓库的兜底身份并返回 true，供上层出 warning。这样 `eg init` 在一台
// 全新机器上也能确定性完成首个 commit，而不是以「提交失败」收场。
func (r *Repo) EnsureIdentity() (bool, error) {
	if _, _, err := r.run(r.root, "var", "GIT_COMMITTER_IDENT"); err == nil {
		return false, nil
	}
	if _, stderr, err := r.run(r.root, "config", "--local", "user.name", FallbackUserName); err != nil {
		return false, wrapGit("config user.name", stderr, err)
	}
	if _, stderr, err := r.run(r.root, "config", "--local", "user.email", FallbackUserEmail); err != nil {
		return false, wrapGit("config user.email", stderr, err)
	}
	return true, nil
}
