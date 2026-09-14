// Package e2e 是 M1 端到端验收用例（T-evergreen.s1_main_flow-158614-018）。
//
// 判据只有一条：**一篇真实文章从收录到知识卡与关系落盘，全程零介入，报告如实**。
// 本文件把这句话拆成可复跑的断言，全部在临时 vault 内跑真实 `eg` 二进制：
// 无交互、无网络、无手工改文件（除刻意模拟「用户手写」与「外部改动」的两处，
// 它们本身就是 B2 / B3 的被测场景）。
//
// 语料是两篇**真实文章**（`testdata/`，Agent 侧已清洗的正文字节）：
//   - The Bitter Lesson（Rich Sutton, 2019）—— 主链路第一篇；明显不含反例，
//     用于 EG-EXT-02 的「覆盖项缺失 → 报告标注」实测；
//   - Verification, The Key to AI（Rich Sutton, 2001）—— 与已有卡高度相似的第二篇，
//     走 `append_card` 三分区复用路径。
//
// ChangePlan 不另写一份：直接取 `SKILL.md` 的两份样例（`skill.Samples`），
// 只把 `base` 的占位 hash 换成本次 `eg context` 的真值——「文档样例即用例」，
// 样例漂移会立刻在这里失败。
package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/cli"
	"github.com/ikaqiu-Lemon/EverGreen/internal/report"
	"github.com/ikaqiu-Lemon/EverGreen/skill"
)

const (
	domain      = "ai-infra"
	altDomain   = "ops"
	srcArticle1 = "s-20260917-the-bitter-lesson"
	srcArticle2 = "s-20260917-verification-the-key-to-ai"
	noteA1      = "n-20260917-the-bitter-lesson"
	noteA2      = "n-20260917-verification-the-key-to-ai"
	cardA1      = "k-20260917-bitter-lesson"
	capturedAt1 = "2026-09-17T09:00:00+08:00"
	capturedAt2 = "2026-09-17T09:30:00+08:00"
	url1        = "http://www.incompleteideas.net/IncIdeas/BitterLesson.html"
	url2        = "http://www.incompleteideas.net/IncIdeas/KeytoAI.html"
	title1      = "The Bitter Lesson"
	title2      = "Verification, The Key to AI"
)

// buildOnce 保证整轮 e2e 只编一次二进制（真二进制、真进程，不是进程内调用）。
var (
	buildOnce sync.Once
	egBin     string
	buildErr  error
)

// egPath 返回本轮 e2e 使用的 `eg` 二进制路径（CGO_ENABLED=0，与发布口径一致）。
func egPath(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "eg-e2e-bin-")
		if err != nil {
			buildErr = err
			return
		}
		egBin = filepath.Join(dir, "eg")
		build := exec.Command("go", "build", "-o", egBin, "github.com/ikaqiu-Lemon/EverGreen/cmd/eg")
		build.Dir = ".."
		build.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := build.CombinedOutput(); err != nil {
			buildErr = fmt.Errorf("编译 eg 失败：%v\n%s", err, out)
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return egBin
}

// ---------- 运行与解析 ----------

type diag struct {
	Code    string `json:"code"`
	Level   string `json:"level"`
	Path    string `json:"path"`
	OpIndex int    `json:"op_index"`
	Message string `json:"message"`
	Target  string `json:"target"`
}

type envelope struct {
	OK       bool                       `json:"ok"`
	Data     map[string]json.RawMessage `json:"data"`
	Warnings []diag                     `json:"warnings"`
	ExitCode int                        `json:"exit_code"`
	Status   string                     `json:"status"`
	raw      string
}

// runEG 跑一次真实 CLI；返回退出码与 stdout。零交互：stdin 恒为空。
func runEG(t *testing.T, vault string, args ...string) (int, string, string) {
	t.Helper()
	full := append([]string{"--vault", vault}, args...)
	cmd := exec.Command(egPath(t), full...)
	cmd.Stdin = strings.NewReader("")
	var out, errOut strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("执行 eg %v 失败：%v", args, err)
	}
	return code, out.String(), errOut.String()
}

// runJSON 跑一次 CLI 并解析 --json 信封。
func runJSON(t *testing.T, vault string, args ...string) (envelope, int) {
	t.Helper()
	code, stdout, stderr := runEG(t, vault, append(args, "--json")...)
	var env envelope
	body := stdout
	if strings.TrimSpace(body) == "" {
		body = stderr
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(body)), &env); err != nil {
		t.Fatalf("eg %v 的 --json 输出不可解析：%v\n%s", args, err, body)
	}
	env.raw = body
	if env.ExitCode != code {
		t.Fatalf("eg %v：信封 exit_code=%d 与进程退出码 %d 不一致", args, env.ExitCode, code)
	}
	if want := statusFor(code); env.Status != want {
		t.Fatalf("eg %v：status=%q，按退出码 %d 应为 %q", args, env.Status, code, want)
	}
	return env, code
}

func statusFor(code int) string {
	switch code {
	case 0:
		return "completed"
	case 3, 4:
		return "partial"
	default:
		return "failed"
	}
}

func (e envelope) report(t *testing.T) report.Report {
	t.Helper()
	raw, ok := e.Data["report"]
	if !ok {
		t.Fatal("data.report 缺失")
	}
	var rep report.Report
	if err := json.Unmarshal(raw, &rep); err != nil {
		t.Fatalf("报告体不可解析：%v", err)
	}
	return rep
}

func (e envelope) reportKeys(t *testing.T) []string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(e.Data["report"], &m); err != nil {
		t.Fatalf("报告体不可解析：%v", err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (e envelope) str(t *testing.T, key string) string {
	t.Helper()
	var s string
	if err := json.Unmarshal(e.Data[key], &s); err != nil {
		t.Fatalf("data.%s 不是字符串：%v", key, err)
	}
	return s
}

// ---------- vault 与 git 辅助 ----------

func gitOut(t *testing.T, vault string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = vault
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v 失败：%v\n%s", args, err, out)
	}
	return string(out)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读不到 %s：%v", path, err)
	}
	return string(raw)
}

func writePlan(t *testing.T, dir, name string, body []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, body, 0o644); err != nil {
		t.Fatalf("写 plan 失败：%v", err)
	}
	return p
}

func testdata(t *testing.T, name string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("语料路径解析失败：%v", err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("语料缺失 %s：%v", abs, err)
	}
	return abs
}

// newVault 建一个干净 vault 并配好领域（零交互）。
func newVault(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	vault := filepath.Join(root, "vault")
	if code, out, errOut := runEG(t, vault, "init", "--domain", domain); code != 0 {
		t.Fatalf("eg init 退出码 %d\n%s%s", code, out, errOut)
	}
	// default_domain 已由 init 落定，这里再显式设一次（命令覆盖；无实际变化不产生空 commit）。
	if code, out, errOut := runEG(t, vault, "config", "set", "default_domain", domain); code != 0 {
		t.Fatalf("eg config set default_domain 退出码 %d\n%s%s", code, out, errOut)
	}
	// 注册第二个领域（W1 跨领域反例要用）：这次是真改动，产生链路里的 reconcile commit。
	if code, out, errOut := runEG(t, vault, "config", "set", "domains", domain+","+altDomain); code != 0 {
		t.Fatalf("eg config set domains 退出码 %d\n%s%s", code, out, errOut)
	}
	return vault
}

// captureArticle 收录一篇真实文章，返回 source_id。
func captureArticle(t *testing.T, vault, url, title, file, at string) envelope {
	t.Helper()
	env, code := runJSON(t, vault, "capture", "--url", url, "--title", title,
		"--body-file", testdata(t, file), "--reason", "M1 端到端验收语料（真实文章）",
		"--domain", domain, "--captured-at", at)
	if code != 0 {
		t.Fatalf("eg capture 退出码 %d：%s", code, env.raw)
	}
	return env
}

// contextBase 取 eg context 的 base（id/路径 → content_hash）。
func contextBase(t *testing.T, vault, kind, id string) map[string]string {
	t.Helper()
	env, code := runJSON(t, vault, "context", "--"+kind, id)
	if code != 0 {
		t.Fatalf("eg context 退出码 %d：%s", code, env.raw)
	}
	var base map[string]string
	if err := json.Unmarshal(env.Data["base"], &base); err != nil {
		t.Fatalf("context 的 base 不可解析：%v", err)
	}
	if len(base) == 0 {
		t.Fatal("context 未返回任何 content_hash：B3 无版本依据")
	}
	for k, v := range base {
		if !strings.HasPrefix(v, "sha256:") {
			t.Fatalf("base[%s] = %q 不是 sha256: 形态", k, v)
		}
	}
	return base
}

// ---------- SKILL.md 样例 → 可执行 plan ----------

func skillSamples(t *testing.T) []string {
	t.Helper()
	s := skill.Samples()
	if len(s) != 2 {
		t.Fatalf("SKILL.md 应含 2 份 ChangePlan 样例，实际 %d", len(s))
	}
	return s
}

// planFromSample 把样例的 base 占位 hash 换成本次 eg context 的真值，其余字节不动。
func planFromSample(t *testing.T, sample string, base map[string]string) []byte {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(sample), &m); err != nil {
		t.Fatalf("样例不是合法 JSON：%v", err)
	}
	raw, _ := m["base"].(map[string]interface{})
	filled := map[string]interface{}{}
	for k := range raw {
		v, ok := base[k]
		if !ok {
			t.Fatalf("eg context 的 base 未覆盖样例声明的 %s：无法逐字填 content_hash", k)
		}
		filled[k] = v
	}
	m["base"] = filled
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("plan 序列化失败：%v", err)
	}
	return out
}

// ---------- 主用例 ----------

func TestM1RealArticleZeroIntervention(t *testing.T) {
	vault := newVault(t)
	work := t.TempDir()
	samples := skillSamples(t)

	// —— ① 收录第一篇真实文章 ——
	cap1 := captureArticle(t, vault, url1, title1, "bitter-lesson.txt", capturedAt1)
	if got := cap1.str(t, "source_id"); got != srcArticle1 {
		t.Fatalf("source_id = %q，期望 %q", got, srcArticle1)
	}
	if !strings.Contains(readFile(t, filepath.Join(vault, "unprocessed.md")), srcArticle1) {
		t.Fatal("收件区未登记该原文")
	}

	// —— ② eg context 取 base ——
	base1 := contextBase(t, vault, "source", srcArticle1)
	if _, ok := base1["unprocessed.md"]; !ok {
		t.Fatal("context 的 base 未覆盖 unprocessed.md")
	}

	// —— ③ SKILL.md 样例 ① 先 --dry-run：退 0、零写入零 commit ——
	t.Run("skill_sample1_dry_run", func(t *testing.T) {
		headBefore := strings.TrimSpace(gitOut(t, vault, "rev-parse", "HEAD"))
		p := writePlan(t, work, "sample1-dry.json", []byte(samples[0]))
		env, code := runJSON(t, vault, "apply", "--plan", p, "--dry-run")
		if code != 0 {
			t.Fatalf("样例 ① 的 --dry-run 退出码 %d，期望 0：%s", code, env.raw)
		}
		if _, ok := env.Data["planned"]; !ok {
			t.Fatal("--dry-run 未输出 planned[]")
		}
		if s := strings.TrimSpace(gitOut(t, vault, "status", "--porcelain")); s != "" {
			t.Fatalf("--dry-run 必须零写入，实际工作区脏：%s", s)
		}
		if head := strings.TrimSpace(gitOut(t, vault, "rev-parse", "HEAD")); head != headBefore {
			t.Fatal("--dry-run 必须零 commit")
		}
		if rep := env.report(t); rep.Git.Commit != nil && *rep.Git.Commit != "" {
			t.Fatalf("--dry-run 的 git.commit 应为空，实际 %v", *rep.Git.Commit)
		}
	})

	// —— ④ 正式 apply 第一篇（样例 ① + 真 content_hash）——
	plan1 := planFromSample(t, samples[0], base1)
	env1, code1 := runJSON(t, vault, "apply", "--plan", writePlan(t, work, "plan1.json", plan1))
	if code1 != 0 {
		t.Fatalf("第一篇 apply 退出码 %d，期望 0：%s", code1, env1.raw)
	}
	rep1 := env1.report(t)

	t.Run("artifacts_complete", func(t *testing.T) {
		for _, rel := range []string{
			filepath.Join("sources", srcArticle1+".md"),
			filepath.Join("domains", domain, "notes", noteA1+".md"),
			filepath.Join("domains", domain, "knowledge", cardA1+".md"),
		} {
			if _, err := os.Stat(filepath.Join(vault, rel)); err != nil {
				t.Fatalf("产物缺失 %s：%v", rel, err)
			}
		}
		note := readFile(t, filepath.Join(vault, "domains", domain, "notes", noteA1+".md"))
		if !strings.Contains(note, "## 产出知识卡") || !strings.Contains(note, cardA1) {
			t.Fatal("笔记「产出知识卡」未列出该卡 ID")
		}
		card := readFile(t, filepath.Join(vault, "domains", domain, "knowledge", cardA1+".md"))
		for _, four := range []string{"source: '" + srcArticle1 + "'", "note: '" + noteA1 + "'", "rel: 'support'", "reason: '"} {
			if !strings.Contains(card, four) {
				t.Fatalf("卡 sources[] 材料关系四要素不全，缺 %s", four)
			}
		}
		if !strings.Contains(card, "status: 'active'") {
			t.Fatal("新建卡应直接 active")
		}
		// 收件区条目在同一次写入里移出（EG-SRC-02 的 M1 口径：不保证强原子，但正常路径应移出）。
		if strings.Contains(readFile(t, filepath.Join(vault, "unprocessed.md")), srcArticle1) {
			t.Fatal("write_note 成功后收件区条目未移出，且报告未如实登记")
		}
	})

	t.Run("report_matches_disk", func(t *testing.T) {
		if rep1.Note.ID != noteA1 {
			t.Fatalf("报告 note.id = %q", rep1.Note.ID)
		}
		if len(rep1.Cards.Created) != 1 || rep1.Cards.Created[0] != cardA1 {
			t.Fatalf("报告 cards.created = %v", rep1.Cards.Created)
		}
		if len(rep1.Relations.Material) != 1 {
			t.Fatalf("报告材料关系 = %d 条，期望 1", len(rep1.Relations.Material))
		}
		if len(rep1.Skipped) != 0 {
			t.Fatalf("正常路径不应有 skipped：%+v", rep1.Skipped)
		}
		if rep1.Reconcile.Ran {
			t.Fatal("S1 从不跑对账：报告 reconcile.ran 必须为 false")
		}
		// 写入清单逐项在磁盘上存在。
		for _, l := range rep1.Links {
			if _, err := os.Stat(filepath.Join(vault, l)); err != nil {
				t.Fatalf("报告声称写入 %s，磁盘上不存在", l)
			}
		}
		// commit hash 与 git 交叉断言。
		if rep1.Git.Commit == nil || *rep1.Git.Commit == "" {
			t.Fatal("报告未给出 commit")
		}
		if !strings.Contains(gitOut(t, vault, "rev-parse", "HEAD"), (*rep1.Git.Commit)[:7]) {
			t.Fatalf("报告 commit %s 不是 HEAD", *rep1.Git.Commit)
		}
		// eg report --last 与该次 apply 的报告体逐字一致。
		last, code := runJSON(t, vault, "report", "--last")
		if code != 0 {
			t.Fatalf("eg report --last 退出码 %d", code)
		}
		if string(last.Data["report"]) != string(env1.Data["report"]) {
			t.Fatal("eg report --last 的报告体与该次 apply 不一致")
		}
		if s := strings.TrimSpace(gitOut(t, vault, "status", "--porcelain")); s != "" {
			t.Fatalf("report 是只读命令，工作区不应变化：%s", s)
		}
	})

	// —— EG-EXT-02：覆盖项缺失在两种形态都可见 ——
	t.Run("coverage_gap_visible_in_both_forms", func(t *testing.T) {
		if !strings.Contains(env1.raw, "counterexample") {
			t.Fatal("--json 形态未标注 coverage_gaps 缺失项 counterexample")
		}
		var hit bool
		for _, w := range rep1.Warnings {
			if w.Code == "I1" && strings.Contains(w.Message, "counterexample") {
				if w.OpIndex < 0 || !strings.Contains(w.Path, "coverage_gaps") {
					t.Fatalf("覆盖缺失条目缺 op 下标或字段路径：%+v", w)
				}
				hit = true
			}
		}
		if !hit {
			t.Fatal("报告 warnings[] 里没有 coverage_gaps 缺失条目")
		}
		_, text, _ := runEG(t, vault, "report", "--last")
		if !strings.Contains(text, "counterexample") {
			t.Fatal("人类可读报告未标注 counterexample 缺失")
		}
	})

	t.Run("git_chain_and_subject_format", func(t *testing.T) {
		log := gitOut(t, vault, "log", "--oneline", "--reverse")
		lines := strings.Split(strings.TrimSpace(log), "\n")
		wantVerbs := []string{"init", "reconcile", "capture", "process"}
		if len(lines) < len(wantVerbs) {
			t.Fatalf("commit 链不完整：%s", log)
		}
		for i, verb := range wantVerbs {
			subject := strings.SplitN(lines[i], " ", 2)[1]
			if !strings.HasPrefix(subject, verb+"("+domain+"): ") {
				t.Fatalf("第 %d 条 commit 主题 %q 不符合 <verb>(<domain>): <subject>（期望 verb=%s）", i+1, subject, verb)
			}
		}
		// 链路里的 reconcile 是 eg config set 的 commit verb，不是 S3 对账命令：
		// 报告 reconcile.ran == false 已在 report_matches_disk 断言。
		//
		// —— M4 · T-evergreen.s1_main_flow-158614-058 按实测重钉（只改形态，不放宽本体）——
		// M1 期该命令未注册是 M1 收口当日的真事实；M4 · T-…-058 按技术方案 §7.1 把它作为 S3 命令
		// 落地，判据由「未知命令退 1」按实测重钉为「不属 S1 九命令集合 + 阶段归属 S3」两格，
		// M1 侧的 S1 九命令事实逐字未动。
		//
		// 第一格（本体，加严成集合等式）：S1 命令清单逐字仍**恰 9 条**且**不含 reconcile**。
		// 真源取注册表的前九项（注册序即 S1 九命令的声明序，见 internal/cli/commands.go），
		// 与这里冻结的九个名字做集合等式 —— 谁把 S1 集合悄悄扩到 10 条，这里当场红。
		s1Nine := []string{"init", "config", "capture", "context", "apply",
			"search", "card", "rel", "report"}
		all := cli.New().Commands()
		if len(all) < len(s1Nine) {
			t.Fatalf("命令注册表只有 %d 条，装不下 S1 九命令", len(all))
		}
		var got []string
		for _, c := range all[:len(s1Nine)] {
			got = append(got, c.Name)
		}
		if strings.Join(got, ",") != strings.Join(s1Nine, ",") {
			t.Fatalf("S1 九命令清单 = %v，期望逐字 %v（集合与次序都是 M1 的历史事实）", got, s1Nine)
		}
		for _, name := range s1Nine {
			if name == "reconcile" {
				t.Fatal("reconcile 不属 S1 九命令集合：它出现在 S1 清单里即判红")
			}
		}
		// 第二格（新增加严）：reconcile 现由 M4（S3 阶段）注册，故**不再**退 1；
		// 它在注册表里的位次必须落在前九项**之后**（阶段归属 S3，不是第十条 S1 命令）。
		var reconcileIdx = -1
		for i, c := range all {
			if c.Name == "reconcile" {
				reconcileIdx = i
			}
		}
		if reconcileIdx < len(s1Nine) {
			t.Fatalf("reconcile 在注册表的位次 = %d，必须排在 S1 九命令之后（S3 阶段命令）", reconcileIdx)
		}
		// 用 --dry-run 实测：零写入零提交，因此本子测试之后的 M1 断言状态一格不动。
		beforeLog := gitOut(t, vault, "log", "--oneline")
		beforeStatus := gitOut(t, vault, "status", "--porcelain")
		code, _, egErr := runEG(t, vault, "reconcile", "--dry-run")
		if code == 1 {
			t.Fatalf("eg reconcile 自 M4 起已是注册命令，不得再按未知命令退 1：%s", egErr)
		}
		if got := gitOut(t, vault, "log", "--oneline"); got != beforeLog {
			t.Fatal("eg reconcile --dry-run 不得产生提交")
		}
		if got := gitOut(t, vault, "status", "--porcelain"); got != beforeStatus {
			t.Fatal("eg reconcile --dry-run 不得改动工作区")
		}
	})

	// —— 幂等：同一篇再收录一次 ——
	t.Run("idempotent_capture", func(t *testing.T) {
		before := gitOut(t, vault, "log", "--oneline")
		env, code := runJSON(t, vault, "capture", "--url", url1, "--title", title1,
			"--body-file", testdata(t, "bitter-lesson.txt"), "--reason", "重复收录（幂等回归）",
			"--domain", domain, "--captured-at", capturedAt1)
		if code != 0 {
			t.Fatalf("重复收录退出码 %d：%s", code, env.raw)
		}
		var deduped, hasNote bool
		json.Unmarshal(env.Data["deduped"], &deduped)
		json.Unmarshal(env.Data["has_note"], &hasNote)
		if !deduped || !hasNote {
			t.Fatalf("重复收录应 deduped=true / has_note=true，实际 %v / %v", deduped, hasNote)
		}
		entries, _ := filepath.Glob(filepath.Join(vault, "sources", "*.md"))
		if len(entries) != 1 {
			t.Fatalf("sources/ 应只有一份原文，实际 %d 份", len(entries))
		}
		if n := strings.Count(readFile(t, filepath.Join(vault, "unprocessed.md")), "source_id:"); n != 0 {
			t.Fatalf("收件区不应因重复收录再登记条目，实际 %d 条", n)
		}
		card := readFile(t, filepath.Join(vault, "domains", domain, "knowledge", cardA1+".md"))
		if n := strings.Count(card, "  - source:"); n != 1 {
			t.Fatalf("卡 sources[] 条目数 = %d，期望 1（四要素逐字相同的关系被幂等去重）", n)
		}
		if after := gitOut(t, vault, "log", "--oneline"); len(after) < len(before) {
			t.Fatal("重复收录不得丢失历史 commit")
		}
	})

	// —— ⑤ 第二篇（高度相似）走 append_card 三分区 ——
	t.Run("second_article_append_card", func(t *testing.T) {
		cardPath := filepath.Join(vault, "domains", domain, "knowledge", cardA1+".md")
		beforeCore := sectionBytes(t, readFile(t, cardPath), "知识内容")

		cap2 := captureArticle(t, vault, url2, title2, "verification-key-to-ai.txt", capturedAt2)
		if got := cap2.str(t, "source_id"); got != srcArticle2 {
			t.Fatalf("第二篇 source_id = %q", got)
		}
		base2 := contextBase(t, vault, "source", srcArticle2)

		// 样例 ② 先 dry-run（退 0、零写入）。
		p := writePlan(t, work, "sample2-dry.json", []byte(samples[1]))
		if env, code := runJSON(t, vault, "apply", "--plan", p, "--dry-run"); code != 0 {
			t.Fatalf("样例 ② 的 --dry-run 退出码 %d：%s", code, env.raw)
		}
		if s := strings.TrimSpace(gitOut(t, vault, "status", "--porcelain")); s != "" {
			t.Fatalf("--dry-run 必须零写入：%s", s)
		}

		plan2 := planFromSample(t, samples[1], base2)
		env2, code2 := runJSON(t, vault, "apply", "--plan", writePlan(t, work, "plan2.json", plan2))
		if code2 != 0 {
			t.Fatalf("第二篇 apply 退出码 %d：%s", code2, env2.raw)
		}
		rep2 := env2.report(t)
		if len(rep2.Cards.Created) != 0 {
			t.Fatalf("复用路径不应新建卡：%v", rep2.Cards.Created)
		}
		if len(rep2.Cards.Updated) != 1 || rep2.Cards.Updated[0] != cardA1 {
			t.Fatalf("报告 cards.updated = %v，期望 [%s]", rep2.Cards.Updated, cardA1)
		}
		after := readFile(t, cardPath)
		if got := sectionBytes(t, after, "知识内容"); got != beforeCore {
			t.Fatalf("append_card 改动了「知识内容」分区：\n前：%q\n后：%q", beforeCore, got)
		}
		for _, sec := range []string{"解释与依据", "条件与边界", "理解自检"} {
			if !strings.Contains(sectionBytes(t, after, sec), "补充") &&
				!strings.Contains(sectionBytes(t, after, sec), "可扩展") {
				t.Fatalf("分区「%s」未收到追加内容", sec)
			}
		}
		if strings.Contains(sectionBytes(t, after, "用户补充"), "补充依据") {
			t.Fatal("「用户补充」被写入：B2 / E6 被破坏")
		}
		// 无缺失时不写 coverage_gaps：报告里不应出现任何覆盖要点枚举名。
		if hit := firstGapName(env2.raw); hit != "" {
			t.Fatalf("样例 ② 未声明 coverage_gaps，报告却出现枚举名 %s", hit)
		}
	})

	// —— B3：content_hash 不匹配 → 跳过并上报（退 3）——
	t.Run("B3_content_hash_mismatch_skip", func(t *testing.T) {
		cardPath := filepath.Join(vault, "domains", domain, "knowledge", cardA1+".md")
		// context --source 会连同候选卡一起给出 content_hash（收敛判定要用）。
		base := contextBase(t, vault, "source", srcArticle2)
		before := base["domains/"+domain+"/knowledge/"+cardA1+".md"]
		if before == "" {
			t.Fatalf("context 未给出该卡的 content_hash：%v", base)
		}
		// 模拟「外部编辑」：eg context 之后文件被改（这正是 B3 要防的场景）。
		outside := readFile(t, cardPath) + "\n<!-- 外部编辑：模拟用户在 Obsidian 里改了这张卡 -->\n"
		if err := os.WriteFile(cardPath, []byte(outside), 0o644); err != nil {
			t.Fatal(err)
		}
		body := fmt.Sprintf(`{"plan_version":1,"verb":"process","domain":%q,
 "reason":"B3 回归：base 用改动前的 content_hash",
 "base":{"domains/%s/knowledge/%s.md":%q},
 "ops":[{"op":"append_card","card":%q,"sections":{"解释与依据":"- 这条不应落盘\n"}}]}`,
			domain, domain, cardA1, before, cardA1)
		env, code := runJSON(t, vault, "apply", "--plan", writePlan(t, work, "b3.json", []byte(body)))
		if code != 3 {
			t.Fatalf("B3 应退 3，实际 %d：%s", code, env.raw)
		}
		rep := env.report(t)
		if len(rep.Skipped) != 1 {
			t.Fatalf("skipped[] 应恰 1 条，实际 %d", len(rep.Skipped))
		}
		s := rep.Skipped[0]
		if s.Kind != "file_changed" || s.Cause != "content_hash_mismatch" {
			t.Fatalf("skipped 条目命名越界：kind=%s cause=%s", s.Kind, s.Cause)
		}
		if s.Locator == "" || s.Detail == "" {
			t.Fatal("skipped 条目缺 locator / detail")
		}
		if strings.Contains(readFile(t, cardPath), "这条不应落盘") {
			t.Fatal("B3 失效：被跳过的内容仍然落盘")
		}
		// 报告如实：eg report --last 同样能看到该跳过。
		_, text, _ := runEG(t, vault, "report", "--last")
		if !strings.Contains(text, "content_hash_mismatch") {
			t.Fatal("人类可读报告未如实转述跳过原因")
		}
		// 把外部编辑还原成已提交状态，避免污染后续用例。
		gitOut(t, vault, "checkout", "--", ".")
	})

	// —— W1：跨领域写入是 warning，不是 error ——
	t.Run("W1_cross_domain_is_warning", func(t *testing.T) {
		body := fmt.Sprintf(`{"plan_version":1,"verb":"process","domain":%q,
 "reason":"W1 回归：写入目标落在 plan.domain 之外",
 "requirement_ids":["EG-DOM-02"],"base":{},
 "ops":[{"op":"create_card","card_id":"k-20260917-cross-domain","title":"跨领域回归卡","domain":%q,
  "sources":[{"source":%q,"note":%q,"rel":"context","reason":"跨领域回归用例的材料出处"}],
  "sections":{"知识内容":"跨领域写入在 S1 是 W1 warning，照常落盘。\n"}}]}`,
			domain, altDomain, srcArticle1, noteA1)
		env, code := runJSON(t, vault, "apply", "--plan", writePlan(t, work, "w1.json", []byte(body)))
		if code == 2 {
			t.Fatalf("W1 不得当成 error：退出码 %d\n%s", code, env.raw)
		}
		if code != 0 && code != 3 {
			t.Fatalf("W1 用例退出码应为 0 或 3，实际 %d", code)
		}
		rep := env.report(t)
		var hit bool
		for _, w := range rep.Warnings {
			if w.Code == "W1" {
				if w.OpIndex < 0 || w.Path == "" {
					t.Fatalf("W1 条目缺 op 下标或字段路径：%+v", w)
				}
				hit = true
			}
		}
		if !hit {
			t.Fatalf("报告 warnings[] 未见 W1：%s", env.raw)
		}
		if len(rep.Cards.Created) != 1 {
			t.Fatal("W1 用例必须照常写入（非零写入）")
		}
		if _, err := os.Stat(filepath.Join(vault, "domains", altDomain, "knowledge", "k-20260917-cross-domain.md")); err != nil {
			t.Fatalf("跨领域 op 未落盘：%v", err)
		}
	})

	// —— 产物可被 Obsidian 打开（机器替代判据）——
	t.Run("obsidian_parsable_artifacts", func(t *testing.T) {
		assertVaultParsable(t, vault)
	})

	// —— skipped[] 封闭命名：报告里不得出现封闭命名表之外的同义词 ——
	t.Run("skipped_kind_closed_set", func(t *testing.T) {
		last, _ := runJSON(t, vault, "report", "--last")
		for _, blob := range []string{env1.raw, last.raw} {
			// 被禁措辞按运行期拼接，保证本目录对该词的 grep 零匹配。
			for _, forbidden := range []string{"sta" + "le", "block_conflict", "block_hash_changed"} {
				if strings.Contains(blob, forbidden) {
					t.Fatalf("报告出现封闭命名表之外的措辞 %q", forbidden)
				}
			}
		}
	})
}

// TestM1ErrorCasesRejectedWithZeroWrite：四条 error 反例各退 2 且零写入。
func TestM1ErrorCasesRejectedWithZeroWrite(t *testing.T) {
	vault := newVault(t)
	work := t.TempDir()
	samples := skillSamples(t)

	captureArticle(t, vault, url1, title1, "bitter-lesson.txt", capturedAt1)
	base := contextBase(t, vault, "source", srcArticle1)
	if _, code := runJSON(t, vault, "apply", "--plan",
		writePlan(t, work, "seed.json", planFromSample(t, samples[0], base))); code != 0 {
		t.Fatalf("前置 apply 退出码 %d", code)
	}
	cardPath := filepath.Join(vault, "domains", domain, "knowledge", cardA1+".md")
	notePath := filepath.Join(vault, "domains", domain, "notes", noteA1+".md")

	cases := []struct {
		name, code, body string
	}{
		{"E3_source_id_into_relations", "E3", fmt.Sprintf(
			`{"plan_version":1,"verb":"process","domain":%q,"reason":"E3 反例：s- 写进 relations","base":{},
 "ops":[{"op":"add_relation","from":%q,"type":"limits","target":%q,"reason":"论证关系只连知识卡"}]}`,
			domain, cardA1, srcArticle1)},
		{"E3_card_id_into_sources", "E3", fmt.Sprintf(
			`{"plan_version":1,"verb":"process","domain":%q,"reason":"E3 反例：k- 写进 sources","base":{},
 "ops":[{"op":"add_material_rel","card":%q,"source":%q,"note":%q,"rel":"support","reason":"材料关系只接受原文 ID"}]}`,
			domain, cardA1, cardA1, noteA1)},
		{"E6_user_section", "E6", fmt.Sprintf(
			`{"plan_version":1,"verb":"process","domain":%q,"reason":"E6 反例：写「用户补充」","base":{},
 "ops":[{"op":"append_card","card":%q,"sections":{"用户补充":"- 越界写入\n"}}]}`, domain, cardA1)},
		{"E6_core_section", "E6", fmt.Sprintf(
			`{"plan_version":1,"verb":"process","domain":%q,"reason":"E6 反例：自动路径改「知识内容」","base":{},
 "ops":[{"op":"append_card","card":%q,"sections":{"知识内容":"- 越界改写\n"}}]}`, domain, cardA1)},
		{"E1_duplicate_id", "E1", fmt.Sprintf(
			`{"plan_version":1,"verb":"process","domain":%q,"reason":"E1 反例：全库重复 id","base":{},
 "ops":[{"op":"create_card","card_id":%q,"title":"重复 ID 卡",
  "sources":[{"source":%q,"note":%q,"rel":"support","reason":"重复 ID 回归"}],
  "sections":{"知识内容":"重复\n"}}]}`, domain, cardA1, srcArticle1, noteA1)},
		// M3 重钉（T-…-037）：`replace_block` 已实装，未知 op 的反例改用**归属仍未定**的
		// `set_tags`（授权合同 §9 A-18：本仓不定义其字段，仍按未知 op 拒绝）。判据不变：E5 + 零写入。
		{"E5_unknown_op", "E5", fmt.Sprintf(
			`{"plan_version":1,"verb":"process","domain":%q,"reason":"E5 反例：未知 op（set_tags 归属未定）","base":{},
 "ops":[{"op":"set_tags","card":%q,"tags":["x"]}]}`, domain, cardA1)},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cardBefore, noteBefore := readFile(t, cardPath), readFile(t, notePath)
			headBefore := strings.TrimSpace(gitOut(t, vault, "rev-parse", "HEAD"))
			env, code := runJSON(t, vault, "apply", "--plan",
				writePlan(t, work, c.name+".json", []byte(c.body)))
			if code != 2 {
				t.Fatalf("%s 应退 2，实际 %d：%s", c.name, code, env.raw)
			}
			if !strings.Contains(env.raw, `"code":"`+c.code+`"`) {
				t.Fatalf("%s 未给出 %s 诊断：%s", c.name, c.code, env.raw)
			}
			if s := strings.TrimSpace(gitOut(t, vault, "status", "--porcelain")); s != "" {
				t.Fatalf("%s 必须零写入，工作区却脏了：%s", c.name, s)
			}
			if strings.TrimSpace(gitOut(t, vault, "rev-parse", "HEAD")) != headBefore {
				t.Fatalf("%s 不应产生 commit", c.name)
			}
			if readFile(t, cardPath) != cardBefore || readFile(t, notePath) != noteBefore {
				t.Fatalf("%s 改动了目标文件字节", c.name)
			}
		})
	}
}

// TestM1SafetyBaselines：B1 / B2 / B4 在真实链路上各一条（B3 在主用例内）。
func TestM1SafetyBaselines(t *testing.T) {
	vault := newVault(t)
	work := t.TempDir()
	samples := skillSamples(t)
	captureArticle(t, vault, url1, title1, "bitter-lesson.txt", capturedAt1)
	base := contextBase(t, vault, "source", srcArticle1)
	if _, code := runJSON(t, vault, "apply", "--plan",
		writePlan(t, work, "seed.json", planFromSample(t, samples[0], base))); code != 0 {
		t.Fatal("前置 apply 失败")
	}
	cardPath := filepath.Join(vault, "domains", domain, "knowledge", cardA1+".md")
	notePath := filepath.Join(vault, "domains", domain, "notes", noteA1+".md")
	cardRel := "domains/" + domain + "/knowledge/" + cardA1 + ".md"
	// 已有卡的 content_hash 由「下一篇材料」的 eg context 给出（候选卡随 base 一并返回），
	// 这正是 Agent 追加已有卡时的真实取数路径。
	captureArticle(t, vault, url2, title2, "verification-key-to-ai.txt", capturedAt2)
	cardHash := func(t *testing.T) string {
		t.Helper()
		h := contextBase(t, vault, "source", srcArticle2)[cardRel]
		if h == "" {
			t.Fatal("eg context 未给出已有卡的 content_hash")
		}
		return h
	}

	t.Run("B1_append_only", func(t *testing.T) {
		before := readFile(t, cardPath)
		beforeStamp := frontmatterField(t, before, "updated_at")
		waitPastStamp(t, beforeStamp)
		body := fmt.Sprintf(`{"plan_version":1,"verb":"process","domain":%q,"reason":"B1 回归：只追加",
 "base":{%q:%q},
 "ops":[{"op":"append_card","card":%q,"sections":{"条件与边界":"- B1 追加的一行\n"}}]}`,
			domain, cardRel, cardHash(t), cardA1)
		if _, code := runJSON(t, vault, "apply", "--plan", writePlan(t, work, "b1.json", []byte(body))); code != 0 {
			t.Fatalf("B1 用例退出码 %d", code)
		}
		after := readFile(t, cardPath)
		if !strings.Contains(after, "B1 追加的一行") {
			t.Fatal("追加内容未落盘")
		}
		// B1 底线（技术方案 §9 表 B1）管的是**普通块**：「默认只追加，不自动替换普通块」。
		// frontmatter 的 `updated_at` 是唯一例外，且是**反向要求**：M3 用户授权合同
		// 「写入字段表」第 8 条规定它由 CLI 在实际写入时更新。因此判据分两半——
		//   ① 时间戳必须前进（本次已跨秒，它不许原地不动）；
		//   ② 除这一行之外，既有每一行都必须仍在、且顺序不变。
		afterStamp := frontmatterField(t, after, "updated_at")
		tb, err := time.Parse(time.RFC3339, beforeStamp)
		if err != nil {
			t.Fatalf("前像 updated_at %q 非法：%v", beforeStamp, err)
		}
		ta, err := time.Parse(time.RFC3339, afterStamp)
		if err != nil {
			t.Fatalf("后像 updated_at %q 不是带时区的 RFC3339：%v", afterStamp, err)
		}
		if !ta.After(tb) {
			t.Fatalf("updated_at 未前进：%q → %q（合同要求 CLI 在实际写入时更新该字段）",
				beforeStamp, afterStamp)
		}
		// 时间戳只准出现在 frontmatter 里，且恰一处：防止「豁免一行」被用来夹带正文改写。
		if n := strings.Count(after, "\nupdated_at:"); n != 1 {
			t.Fatalf("后像 updated_at 行数 = %d，期望恰 1", n)
		}
		if fm, ok := frontmatter(after); !ok || !strings.Contains(fm, "updated_at:") {
			t.Fatal("后像 updated_at 不在 frontmatter 区内")
		}
		exempt := "updated_at: '" + beforeStamp + "'"
		fmBefore, ok := frontmatter(before)
		if !ok || !strings.Contains(fmBefore, exempt) {
			// 引号形态由 YAML 序列化决定；形态变了就得同步这里，不能让豁免变成宽泛匹配。
			t.Fatalf("前像 frontmatter 里找不到待豁免行 %q，判据需同步", exempt)
		}
		exempted := 0
		// 逐行**整行**比对（不是子串包含）：子串口径下「把既有标题改成 `## 条件与边界X`」
		// 这类改写会因为原行仍是新行的前缀而蒙混过关，B1「不替换普通块」形同虚设。
		// 整行 + 保序双指针：既有行必须原封不动地仍在，且相对顺序不变。
		afterLines := strings.Split(after, "\n")
		pos := 0
		for _, line := range strings.Split(strings.TrimRight(before, "\n"), "\n") {
			found := -1
			for i := pos; i < len(afterLines); i++ {
				if afterLines[i] == line {
					found = i
					break
				}
			}
			if found < 0 {
				if line == exempt && exempted == 0 {
					exempted++
					continue
				}
				t.Fatalf("既有行被改写或删除：%q", line)
			}
			pos = found + 1
		}
		if exempted != 1 {
			t.Fatalf("updated_at 行逐字未变（豁免次数 %d）：跨秒写入后该字段必须被 CLI 更新", exempted)
		}
	})

	t.Run("B2_user_block_preserved_verbatim", func(t *testing.T) {
		// 模拟用户手写「用户补充」（CLI 永不写这个分区）。
		userBlock := "- 用户手写：这里有缩进与空行\n\n  - 子项，必须逐字保留\n"
		src := readFile(t, notePath)
		marked := strings.Replace(src, "## 用户补充\n", "## 用户补充\n\n"+userBlock, 1)
		if marked == src {
			t.Fatal("笔记里没有「用户补充」分区")
		}
		if err := os.WriteFile(notePath, []byte(marked), 0o644); err != nil {
			t.Fatal(err)
		}
		gitOut(t, vault, "add", "-A")
		gitOut(t, vault, "-c", "user.name=u", "-c", "user.email=u@x", "commit", "-m", "user: 手写补充")

		b := contextBase(t, vault, "note", noteA1)
		body := fmt.Sprintf(`{"plan_version":1,"verb":"reprocess","domain":%q,"reason":"B2 回归：重新加工逐字保留用户块",
 "base":{"domains/%s/notes/%s.md":%q},
 "ops":[{"op":"write_note","source":%q,"note_id":%q,"reprocess":true,
  "sections":{"材料提炼":"- 重新加工追加的一条\n"}}]}`,
			domain, domain, noteA1, b["domains/"+domain+"/notes/"+noteA1+".md"], srcArticle1, noteA1)
		env, code := runJSON(t, vault, "apply", "--plan", writePlan(t, work, "b2.json", []byte(body)))
		if code != 0 && code != 3 {
			t.Fatalf("B2 用例退出码 %d：%s", code, env.raw)
		}
		if got := readFile(t, notePath); !strings.Contains(got, userBlock) {
			t.Fatalf("用户块未逐字保留：\n%s", got)
		}
	})

	t.Run("B4_commit_failure_keeps_disk", func(t *testing.T) {
		lock := filepath.Join(vault, ".git", "index.lock")
		if err := os.WriteFile(lock, []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(lock)
		body := fmt.Sprintf(`{"plan_version":1,"verb":"process","domain":%q,"reason":"B4 回归：提交失败不回滚",
 "base":{%q:%q},
 "ops":[{"op":"append_card","card":%q,"sections":{"条件与边界":"- B4 用例写入的一行\n"}}]}`,
			domain, cardRel, cardHash(t), cardA1)
		env, code := runJSON(t, vault, "apply", "--plan", writePlan(t, work, "b4.json", []byte(body)))
		if code != 4 {
			t.Fatalf("Git 提交失败应退 4，实际 %d：%s", code, env.raw)
		}
		if !strings.Contains(readFile(t, cardPath), "B4 用例写入的一行") {
			t.Fatal("B4 被破坏：提交失败后磁盘内容被还原")
		}
		rep := env.report(t)
		if rep.Git.Commit != nil && *rep.Git.Commit != "" {
			t.Fatal("提交失败时不应报告 commit")
		}
		if len(rep.Links) == 0 {
			t.Fatal("提交失败时报告应仍列出已写入清单")
		}
	})
}

// m6TxnIDKey / m6TxnIDRe 是 M6（A-59）在报告键面上**唯一**一格的条件增量：成功分配过事务号的
// 写路径必须交代 `txn_id`，取值恒等于 `.index/txn/<txn_id>` 目录名。正则在 e2e 侧**独立重写**
// 一份（而不是复用 internal/txn 的私有 pattern）：外部合同要能在实现改形态时立刻判红。
const m6TxnIDKey = "txn_id"

var m6TxnIDRe = regexp.MustCompile(`^t[0-9a-f]{16}$`)

// TestM1CoverageGapAbsentRerun：同一篇文章去掉 coverage_gaps 重跑 → 报告不出现任何覆盖要点枚举名。
func TestM1CoverageGapAbsentRerun(t *testing.T) {
	samples := skillSamples(t)
	run := func(t *testing.T, dropGaps bool) envelope {
		t.Helper()
		vault := newVault(t)
		work := t.TempDir()
		captureArticle(t, vault, url1, title1, "bitter-lesson.txt", capturedAt1)
		base := contextBase(t, vault, "source", srcArticle1)
		body := planFromSample(t, samples[0], base)
		if dropGaps {
			var m map[string]interface{}
			if err := json.Unmarshal(body, &m); err != nil {
				t.Fatal(err)
			}
			for _, raw := range m["ops"].([]interface{}) {
				op := raw.(map[string]interface{})
				delete(op, "coverage_gaps")
			}
			body, _ = json.Marshal(m)
		}
		env, code := runJSON(t, vault, "apply", "--plan", writePlan(t, work, "p.json", body))
		if code != 0 {
			t.Fatalf("apply 退出码 %d：%s", code, env.raw)
		}
		return env
	}

	withGaps := run(t, false)
	if !strings.Contains(withGaps.raw, "counterexample") {
		t.Fatal("声明了 coverage_gaps，报告却没有标注")
	}
	without := run(t, true)
	if hit := firstGapName(without.raw); hit != "" {
		t.Fatalf("未声明 coverage_gaps，报告却出现枚举名 %s：CLI 不得自行推断", hit)
	}
	a, b := withGaps.reportKeys(t), without.reportKeys(t)
	if strings.Join(a, ",") != strings.Join(b, ",") {
		t.Fatalf("两次运行的报告体键集合不同：\n%v\n%v", a, b)
	}
	// 报告体键集合 = §4.6 的 S1 必填 11 项 + 阶段占位 5 项 + M6 条件键 `txn_id`，且不含自创键。
	//
	// 【M6 · T-…-072 精确追加】本用例两次跑的都是**成功的事务写**（apply 退 0 ⇒ 事务号
	// 已分配、事务已提交），A-59 因此要求报告必填 `txn_id`。这里追加的是**恰这一个**具名键：
	// 判据仍是逐字等号，未知键、拼错的键、别的 M6 新键一律照旧判红 —— 绝不改成
	// 「忽略不认识的键」那种宽泛放行（那等于把键面合同整个作废）。
	want := append(append([]string{}, report.RequiredKeys()...), report.StageKeys()...)
	want = append(want, m6TxnIDKey)
	sort.Strings(want)
	if strings.Join(a, ",") != strings.Join(want, ",") {
		t.Fatalf("报告体键集合越界：\ngot  %v\nwant %v", a, want)
	}
	// 键在还不够：两次运行都必须给出一个**合 A-59 形态**的真事务号。
	// 只断言「键存在」会让一个填空串 / 填占位文本的实现照样通过。
	for _, c := range []struct {
		name string
		env  envelope
	}{
		{"声明 coverage_gaps 的那次", withGaps},
		{"未声明 coverage_gaps 的那次", without},
	} {
		id := c.env.report(t).TxnID
		if id == "" {
			t.Fatalf("%s：apply 成功提交了事务，报告却没有 %s（A-59：凡成功分配事务号的最终报告必填）",
				c.name, m6TxnIDKey)
		}
		if !m6TxnIDRe.MatchString(id) {
			t.Fatalf("%s：%s = %q 不符合 A-59 形态 %s", c.name, m6TxnIDKey, id, m6TxnIDRe)
		}
	}
	for _, k := range report.ForbiddenKeys() {
		for _, got := range a {
			if got == k {
				t.Fatalf("报告体出现禁止键 %s", k)
			}
		}
	}
}

// TestM1PlaceholderCommandsStayPlaceholders：M1 的「退 1 + 零副作用」底线不因占位清零而失守。
func TestM1PlaceholderCommandsStayPlaceholders(t *testing.T) {
	vault := newVault(t)
	head := strings.TrimSpace(gitOut(t, vault, "rev-parse", "HEAD"))
	// M2 起清单只减不增：search（T-…-021）、card show（T-…-022）、rel 读路径（T-…-023）与
	// rel add（T-…-024）已换成真实实现；最后一条 `rel remove` 由 **T-…-044** 接管成真实写路径
	// （verb=relate、一次 commit、A-24 物理移除），占位清单因此**清零**。
	// 覆盖面不减：这里改用「参数形态错误」继续守住 M1 那条底线——退 1、零写入、零 commit，
	// 且输出里不得再出现阶段「未实现」宣告（针按运行期拼接，避免本文件成为占位 grep 的命中）。
	notice := "M3/S2 " + "未实现"
	for _, args := range [][]string{
		{"rel", "remove", "k-20260901-a", "supports"},
		{"rel", "remove", "k-20260901-a", "supports", "k-20260901-b"}, // 缺 --reason
	} {
		code, out, errOut := runEG(t, vault, args...)
		if code != 1 {
			t.Fatalf("eg %v 应退 1，实际 %d", args, code)
		}
		joined := out + errOut
		if strings.Contains(joined, notice) || strings.Contains(joined, "未实现") {
			t.Fatalf("eg %v 不得再宣告阶段未实现：%s", args, joined)
		}
	}
	if s := strings.TrimSpace(gitOut(t, vault, "status", "--porcelain")); s != "" {
		t.Fatalf("占位命令必须零副作用：%s", s)
	}
	if strings.TrimSpace(gitOut(t, vault, "rev-parse", "HEAD")) != head {
		t.Fatal("占位命令不得产生 commit")
	}
}

// ---------- 断言辅助 ----------

// sectionBytes 取某个 H2 分区的正文字节（不含标题行）。
func sectionBytes(t *testing.T, doc, name string) string {
	t.Helper()
	head := "## " + name + "\n"
	i := strings.Index(doc, head)
	if i < 0 {
		t.Fatalf("找不到分区「%s」", name)
	}
	rest := doc[i+len(head):]
	if j := strings.Index(rest, "\n## "); j >= 0 {
		return rest[:j+1]
	}
	return rest
}

// firstGapName 返回文本中命中的第一个 coverage_gaps 受控枚举名（无命中返回空串）。
func firstGapName(s string) string {
	for _, g := range []string{"core_claim", "key_evidence", "counterexample", "boundary", "method", "conclusion", "limitation"} {
		if strings.Contains(s, g) {
			return g
		}
	}
	return ""
}

// assertVaultParsable 对 vault 内**全部**笔记与知识卡跑 Markdown 解析断言
// （§16.1「产物能被 Obsidian 正常打开」的机器替代判据）：
// 五个 H2 分区名逐字正确、无 H1、frontmatter 可被 YAML 解析、内部 wiki 链接目标存在。
func assertVaultParsable(t *testing.T, vault string) {
	t.Helper()
	noteSections := []string{"材料提炼", "Agent 分析", "用户补充", "存疑与待验证", "产出知识卡"}
	cardSections := []string{"知识内容", "解释与依据", "条件与边界", "用户补充", "理解自检"}
	var checked int
	err := filepath.Walk(filepath.Join(vault, "domains"), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".md") {
			return err
		}
		doc := readFile(t, path)
		want := cardSections
		if strings.Contains(path, string(filepath.Separator)+"notes"+string(filepath.Separator)) {
			want = noteSections
		}
		for _, sec := range want {
			if !strings.Contains(doc, "\n## "+sec+"\n") {
				t.Fatalf("%s 缺分区「%s」或分区名不逐字正确", path, sec)
			}
		}
		if n := strings.Count(doc, "\n## "); n != len(want) {
			t.Fatalf("%s 的 H2 分区数 = %d，期望 %d", path, n, len(want))
		}
		for _, line := range strings.Split(doc, "\n") {
			if strings.HasPrefix(line, "# ") {
				t.Fatalf("%s 出现 H1：%q（分区一律 H2）", path, line)
			}
		}
		fm, ok := frontmatter(doc)
		if !ok {
			t.Fatalf("%s 没有 frontmatter", path)
		}
		if !strings.Contains(fm, "id: '") {
			t.Fatalf("%s 的 frontmatter 缺 id", path)
		}
		for _, line := range strings.Split(fm, "\n") {
			if s := strings.TrimSpace(line); s != "" && !strings.Contains(s, ":") && !strings.HasPrefix(s, "-") {
				t.Fatalf("%s 的 frontmatter 不是合法 YAML 行：%q", path, line)
			}
		}
		for _, link := range wikiLinks(doc) {
			if !fileExistsSomewhere(vault, link) {
				t.Fatalf("%s 的内部链接 [[%s]] 指向不存在的文件", path, link)
			}
		}
		checked++
		return nil
	})
	if err != nil {
		t.Fatalf("遍历产物失败：%v", err)
	}
	if checked == 0 {
		t.Fatal("没有任何笔记 / 知识卡被检查：e2e 未产出产物")
	}
}

func frontmatter(doc string) (string, bool) {
	if !strings.HasPrefix(doc, "---\n") {
		return "", false
	}
	end := strings.Index(doc[4:], "\n---\n")
	if end < 0 {
		return "", false
	}
	return doc[4 : 4+end], true
}

// frontmatterField 取 frontmatter 里某个标量字段的原始行值（不含 key 与冒号，去掉包裹引号）。
// 只在 frontmatter 区内查找：正文里出现同名行不算。
func frontmatterField(t *testing.T, doc, key string) string {
	t.Helper()
	fm, ok := frontmatter(doc)
	if !ok {
		t.Fatalf("文档没有合法 frontmatter：%.80q", doc)
	}
	for _, line := range strings.Split(fm, "\n") {
		if !strings.HasPrefix(line, key+":") {
			continue
		}
		v := strings.TrimSpace(strings.TrimPrefix(line, key+":"))
		return strings.Trim(v, "'\"")
	}
	t.Fatalf("frontmatter 里没有 %s 字段：%q", key, fm)
	return ""
}

// waitPastStamp 阻塞到挂钟秒数确实越过 raw（RFC3339，秒精度）所在的那一秒。
//
// 存在理由：`updated_at` 按 M3 用户授权合同「由 CLI 在实际写入时更新」，但两次写入若落在
// **同一秒**内，该字段的字节恰好不变——于是任何「既有行是否仍在」的逐行判据都会因为时间戳
// 碰巧相等而绿，把「时间戳到底有没有被更新」这条真实行为遮蔽成偶然为真（-race 下进程变慢、
// 跨过秒边界，才把这个遮蔽暴露成红）。主动跨秒之后，该字段**必变**，判据得以从「碰巧相等」
// 升级为「必须前进，且只准它变」。上限 3s：真跨不过去说明时钟异常，应当报错而不是静默放过。
func waitPastStamp(t *testing.T, raw string) {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		t.Fatalf("updated_at %q 不是带时区的 RFC3339：%v", raw, err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for !time.Now().Truncate(time.Second).After(ts.Truncate(time.Second)) {
		if time.Now().After(deadline) {
			t.Fatalf("等待 3s 仍未越过 updated_at=%q 所在的秒：挂钟异常", raw)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func wikiLinks(doc string) []string {
	var out []string
	rest := doc
	for {
		i := strings.Index(rest, "[[")
		if i < 0 {
			return out
		}
		rest = rest[i+2:]
		j := strings.Index(rest, "]]")
		if j < 0 {
			return out
		}
		target := rest[:j]
		if k := strings.Index(target, "|"); k >= 0 {
			target = target[:k]
		}
		out = append(out, strings.TrimSpace(target))
		rest = rest[j+2:]
	}
}

func fileExistsSomewhere(vault, name string) bool {
	if !strings.HasSuffix(name, ".md") {
		name += ".md"
	}
	var found bool
	filepath.Walk(vault, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if filepath.Base(path) == name {
			found = true
		}
		return nil
	})
	return found
}
