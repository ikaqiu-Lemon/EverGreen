package cli

// T-…-008 的验收用例：eg init 的 vault 骨架与 eg config get|set 的唯一读写口。
// 用例名以 Init / Config 开头，可用 `go test ./internal/cli/... -run 'Init|Config'` 单独跑。

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/skill"
)

// setGitIdentity 给子进程 git 提供确定性提交身份（不改用户全局配置）。
func setGitIdentity(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_AUTHOR_NAME", "eg-test")
	t.Setenv("GIT_AUTHOR_EMAIL", "eg-test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "eg-test")
	t.Setenv("GIT_COMMITTER_EMAIL", "eg-test@example.com")
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func initVault(t *testing.T, args ...string) (string, int, string) {
	t.Helper()
	setGitIdentity(t)
	dir := t.TempDir()
	r := newTestRoot(t, dir)
	code, out, errOut := runCLI(t, r, append([]string{"init", "--vault", dir}, args...)...)
	if code != ExitOK {
		t.Fatalf("eg init 退出码 = %d，stderr=%q", code, errOut)
	}
	return dir, code, out
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

// —— eg init：骨架七项与三个「S1 不需要存在」——

func TestInitCreatesS1SkeletonOnly(t *testing.T) {
	dir, _, _ := initVault(t, "--domain", "ai-infra")

	mustExist := []string{
		".git", ".gitignore", ConfigFileName, skill.FileName, UnprocessedFileName,
		SourcesDirName, DomainsDirName,
		filepath.Join(DomainsDirName, "ai-infra", NotesDirName),
		filepath.Join(DomainsDirName, "ai-infra", KnowledgeDirName),
	}
	for _, rel := range mustExist {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("骨架应包含 %s：%v", rel, err)
		}
	}
	for _, rel := range NotCreatedInS1() {
		if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			t.Fatalf("S1 不得创建 %s", rel)
		}
		if _, err := os.Stat(filepath.Join(dir, DomainsDirName, "ai-infra", rel)); err == nil {
			t.Fatalf("S1 不得创建 domains/<d>/%s", rel)
		}
	}
	if got := string(mustRead(t, filepath.Join(dir, ".gitignore"))); got != ".index/\n" {
		t.Fatalf(".gitignore 内容应为 %q，得到 %q", ".index/\n", got)
	}
	if got := mustRead(t, filepath.Join(dir, skill.FileName)); string(got) != string(skill.Content()) {
		t.Fatal("vault 里的 SKILL.md 必须与内嵌副本逐字相同")
	}
}

func TestInitProducesExactlyOneInitCommit(t *testing.T) {
	dir, _, _ := initVault(t, "--domain", "ai-infra")
	log := strings.TrimSpace(gitOut(t, dir, "log", "--oneline"))
	lines := strings.Split(log, "\n")
	if len(lines) != 1 {
		t.Fatalf("git log --oneline 应恰一条，得到 %d 条：\n%s", len(lines), log)
	}
	if !strings.Contains(lines[0], "init(") {
		t.Fatalf("唯一 commit 主题应是 init(...)：%q", lines[0])
	}
	tracked := gitOut(t, dir, "ls-files")
	for _, want := range []string{ConfigFileName, skill.FileName, UnprocessedFileName, ".gitignore"} {
		if !strings.Contains(tracked, want) {
			t.Fatalf("%s 应被 Git 跟踪：\n%s", want, tracked)
		}
	}
}

func TestInitIsIdempotent(t *testing.T) {
	dir, _, _ := initVault(t, "--domain", "ai-infra")
	before := map[string][]byte{}
	for _, rel := range []string{ConfigFileName, skill.FileName, UnprocessedFileName, ".gitignore"} {
		before[rel] = mustRead(t, filepath.Join(dir, rel))
	}
	logBefore := gitOut(t, dir, "log", "--oneline")
	if got := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); got != "" {
		t.Fatalf("前置条件：工作区必须干净，得到 %q", got)
	}

	setGitIdentity(t)
	code, _, errOut := runCLI(t, newTestRoot(t, dir), "init", "--vault", dir, "--domain", "ai-infra")
	if code != ExitOK {
		t.Fatalf("重复 init 应退 0，得到 %d（%s）", code, errOut)
	}
	for rel, want := range before {
		if got := mustRead(t, filepath.Join(dir, rel)); string(got) != string(want) {
			t.Fatalf("重复 init 改写了 %s", rel)
		}
	}
	if got := gitOut(t, dir, "log", "--oneline"); got != logBefore {
		t.Fatalf("干净工作区下重复 init 不得产生新 commit：\n%s→\n%s", logBefore, got)
	}
}

func TestInitJSONEnvelope(t *testing.T) {
	setGitIdentity(t)
	dir := t.TempDir()
	code, out, errOut := runCLI(t, newTestRoot(t, dir), "init", "--vault", dir, "--json")
	if code != ExitOK {
		t.Fatalf("退出码 = %d（%s）", code, errOut)
	}
	var env map[string]interface{}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("--json 输出不是合法 JSON：%v（%q）", err, out)
	}
	assertEnvelopeKeys(t, env)
	data := env["data"].(map[string]interface{})
	if data["default_domain"] != "" {
		t.Fatalf("未给 --domain 时 default_domain 应为空：%v", data["default_domain"])
	}
}

func TestInitGeneratedFilesHaveNoDomainOrTypeKey(t *testing.T) {
	dir, _, _ := initVault(t, "--domain", "ai-infra")
	for _, rel := range []string{UnprocessedFileName, skill.FileName} {
		for _, line := range strings.Split(string(mustRead(t, filepath.Join(dir, rel))), "\n") {
			if strings.HasPrefix(line, "domain:") || strings.HasPrefix(line, "type:") {
				t.Fatalf("%s 出现被黑名单的顶层键：%q（领域由目录唯一决定）", rel, line)
			}
		}
	}
}

// —— eg config get|set ——

func TestConfigGetUnsetDefaultDomainExitsZero(t *testing.T) {
	dir, _, _ := initVault(t)
	code, out, errOut := runCLI(t, newTestRoot(t, dir), "--vault", dir, "config", "get", KeyDefaultDomain)
	if code != ExitOK {
		t.Fatalf("未设置时应退 0，得到 %d（%s）", code, errOut)
	}
	if strings.Contains(out, "value: ai") {
		t.Fatalf("未设置时不得输出任何领域值：%q", out)
	}
	// --json 下 data.value 必须是空字符串（空值而非错误）
	code, out, errOut = runCLI(t, newTestRoot(t, dir), "--vault", dir, "--json", "config", "get", KeyDefaultDomain)
	if code != ExitOK {
		t.Fatalf("--json 未设置时应退 0，得到 %d（%s）", code, errOut)
	}
	var env map[string]interface{}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("非法 JSON：%v（%q）", err, out)
	}
	data := env["data"].(map[string]interface{})
	if data["value"] != "" || data["configured"] != false {
		t.Fatalf("未设置时 value 应为空值：%v", data)
	}

	// 此时其它命令必须提示配置并退 1，且零文件变化、无 commit。
	filesBefore := gitOut(t, dir, "ls-files")
	code, out, errOut = runCLI(t, newTestRoot(t, dir), "--vault", dir, "context",
		"--domain", "x", "--source", "s-20260901-a")
	if code != ExitUsage {
		t.Fatalf("未配置 default_domain 时 eg context 应退 1，得到 %d", code)
	}
	if !strings.Contains(errOut, KeyDefaultDomain) || !strings.Contains(errOut, "eg config set") {
		t.Fatalf("stderr 缺配置提示：%q", errOut)
	}
	if out != "" {
		t.Fatalf("stdout 应为空，得到 %q", out)
	}
	if got := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); got != "" {
		t.Fatalf("git status --porcelain 应为空，得到 %q", got)
	}
	if gitOut(t, dir, "ls-files") != filesBefore {
		t.Fatal("不得有文件新增")
	}
}

func TestConfigSetDefaultDomainCommitsReconcileVerb(t *testing.T) {
	dir, _, _ := initVault(t)
	setGitIdentity(t)
	code, _, errOut := runCLI(t, newTestRoot(t, dir), "--vault", dir, "config", "set", KeyDefaultDomain, "ai-infra")
	if code != ExitOK {
		t.Fatalf("退出码 = %d（%s）", code, errOut)
	}
	raw := string(mustRead(t, filepath.Join(dir, ConfigFileName)))
	var topKeys []string
	for _, line := range strings.Split(raw, "\n") {
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, " ") {
			continue
		}
		topKeys = append(topKeys, strings.SplitN(line, ":", 2)[0])
	}
	if strings.Join(topKeys, ",") != "version,domains,default_domain" {
		t.Fatalf("evergreen.yml 应恰含三个顶层键，得到 %v：\n%s", topKeys, raw)
	}
	if !strings.Contains(raw, "default_domain: ai-infra") {
		t.Fatalf("default_domain 未写入：\n%s", raw)
	}

	log := strings.TrimSpace(gitOut(t, dir, "log", "--oneline"))
	lines := strings.Split(log, "\n")
	if len(lines) != 2 {
		t.Fatalf("应是 init + 一次配置提交共两条，得到：\n%s", log)
	}
	if !strings.Contains(lines[0], "reconcile(ai-infra): ") {
		t.Fatalf("配置提交的 verb 应取 §7.1 命令表口径，得到 %q", lines[0])
	}
	// 领域目录按需创建，且不得出现 reviews/
	for _, rel := range []string{NotesDirName, KnowledgeDirName} {
		if _, err := os.Stat(filepath.Join(dir, DomainsDirName, "ai-infra", rel)); err != nil {
			t.Fatalf("应创建 domains/ai-infra/%s：%v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, DomainsDirName, "ai-infra", "reviews")); err == nil {
		t.Fatal("不得创建 domains/<d>/reviews（S2）")
	}

	// 设置之后守卫放行：eg context 不再因 default_domain 退 1。
	_, _, errOut = runCLI(t, newTestRoot(t, dir), "--vault", dir, "context", "--source", "s-20260901-a")
	if strings.Contains(errOut, "未配置 "+KeyDefaultDomain) {
		t.Fatalf("守卫应已放行：%q", errOut)
	}
}

func TestConfigSetDomainsIsUnionAppend(t *testing.T) {
	dir, _, _ := initVault(t, "--domain", "ai-infra")
	setGitIdentity(t)
	before := string(mustRead(t, filepath.Join(dir, ConfigFileName)))
	code, _, errOut := runCLI(t, newTestRoot(t, dir), "--vault", dir, "config", "set", KeyDomains, "ai-infra,ml-sys")
	if code != ExitOK {
		t.Fatalf("退出码 = %d（%s）", code, errOut)
	}
	after := string(mustRead(t, filepath.Join(dir, ConfigFileName)))
	if !strings.Contains(after, "  - ai-infra\n") || !strings.Contains(after, "  - ml-sys\n") {
		t.Fatalf("并集追加失败：\n%s", after)
	}
	if strings.Count(after, "- ai-infra") != 1 {
		t.Fatalf("既有领域不得重复写入：\n%s", after)
	}
	if !strings.Contains(after, "# Evergreen 工程配置") {
		t.Fatal("注释必须逐字保留")
	}
	if !strings.Contains(before, "default_domain: ai-infra") || !strings.Contains(after, "default_domain: ai-infra") {
		t.Fatal("default_domain 不应被 set domains 改动")
	}
	for _, rel := range domainDirs("ml-sys") {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("新增领域应建 %s：%v", rel, err)
		}
	}
}

func TestConfigSetPreservesUnknownKeysAndComments(t *testing.T) {
	dir, _, _ := initVault(t)
	setGitIdentity(t)
	path := filepath.Join(dir, ConfigFileName)
	raw := string(mustRead(t, path))
	custom := raw + "# 用户自己的注释\ncustom_key: 保留我\n"
	if err := os.WriteFile(path, []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := runCLI(t, newTestRoot(t, dir), "--vault", dir, "config", "set", KeyDefaultDomain, "ai-infra")
	if code != ExitOK {
		t.Fatalf("退出码 = %d（%s）", code, errOut)
	}
	after := string(mustRead(t, path))
	for _, keep := range []string{"# 用户自己的注释", "custom_key: 保留我", "# Evergreen 工程配置"} {
		if !strings.Contains(after, keep) {
			t.Fatalf("未知键 / 注释必须逐字保留，缺 %q：\n%s", keep, after)
		}
	}
}

func TestConfigRejectsUnknownKeyAndEmptyValue(t *testing.T) {
	dir, _, _ := initVault(t)
	for _, args := range [][]string{
		{"config", "get", "frobnicate"},
		{"config", "set", KeyDefaultDomain, " "},
		{"config", "set", KeyDefaultDomain, "Ai_Infra!"},
	} {
		before := mustRead(t, filepath.Join(dir, ConfigFileName))
		code, out, _ := runCLI(t, newTestRoot(t, dir), append([]string{"--vault", dir}, args...)...)
		if code != ExitUsage {
			t.Fatalf("eg %v 应退 1，得到 %d", args, code)
		}
		if out != "" {
			t.Fatalf("eg %v 的 stdout 应为空，得到 %q", args, out)
		}
		if string(mustRead(t, filepath.Join(dir, ConfigFileName))) != string(before) {
			t.Fatalf("eg %v 被拒后不得改写配置", args)
		}
	}
}

// TestConfigReconcileVerbDisambiguation 是消歧判据：commit 动词 `reconcile` 与命令
// `eg reconcile` 是两码事 —— 前者是 M1 起就在的提交动词，后者自 M4（T-…-058）起才是命令。
//
// M4 重钉说明：本用例原先还反证「S1 不注册该命令」。T-…-058 把 `eg reconcile` 真正
// 注册进来后，那一格的事实变了，因此改成**正面**断言「已注册且实现已挂载」——
// 消歧这件事本身一字未放宽：config 的帮助文本仍必须保留 `≠` 那句说明，
// 且仍不许把它当成 config 的子命令来提供。
func TestConfigReconcileVerbDisambiguation(t *testing.T) {
	r := New()
	help := r.Usage()
	if strings.Contains(help, "eg reconcile") {
		t.Fatalf("eg --help 的命令区只列命令名，不得出现 `eg reconcile` 用法行：\n%s", help)
	}
	cmd := r.Lookup("reconcile")
	if cmd == nil {
		t.Fatal("M4 起对账命令必须已注册（T-…-058）")
	}
	if cmd.Handler == nil {
		t.Fatal("对账命令已注册但实现未挂载（wireImplemented 漏了一行）")
	}
	// 帮助文本里只允许以「≠」形式做消歧说明，不得把它当成一个可用子命令来提供
	// （即不出现以 `eg reconcile` 起头的用法行）。
	cfgHelp := r.Lookup("config").Usage
	for _, line := range strings.Split(cfgHelp, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "eg reconcile") {
			t.Fatalf("eg config --help 不得把它当子命令提供：%q", line)
		}
	}
	if !strings.Contains(cfgHelp, "≠ S3 的 eg reconcile 命令") {
		t.Fatal("eg config --help 应保留消歧说明")
	}
	src := string(mustRead(t, "config.go"))
	for i, line := range strings.Split(src, "\n") {
		if !strings.Contains(line, "reconcile") {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		t.Fatalf("config.go 第 %d 行的该词既不是 commit 动词常量也不是消歧注释：%q", i+1, line)
	}
	if !strings.Contains(src, "model.VerbReconcile") {
		t.Fatal("提交动词应取 model 的常量（出处：技术方案 §7.1 命令表）")
	}
}
