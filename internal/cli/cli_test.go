package cli

// T-evergreen.s1_main_flow-158614-003 Acceptance 的机器判据。
// 参数表比对以 CLI 合同为准：
// teamwork/projects/evergreen/s1_main_flow/docs/specs/2026-09-01-eg-cli-contract.md

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"evergreen/internal/model"
)

const contractDoc = "../../../teamwork/projects/evergreen/s1_main_flow/docs/specs/" +
	"2026-09-01-eg-cli-contract.md"

// 九命令的展示名（合同 §1 表格行序）。
var wantCommands = []string{
	"init", "config get|set", "capture", "context", "apply",
	"search", "card show", "rel", "report --last",
}

// 每个命令的 flag 集合（逐项对齐合同 §1.1–§1.9；全局 flag 另计）。
var wantFlags = map[string][]string{
	"init":    {"domain"},
	"config":  {},
	"capture": {"url", "title", "body-stdin", "body-file", "reason", "domain", "tag", "captured-at", "reprocess"},
	"context": {"source", "note", "domain"},
	"apply":   {"plan", "dry-run"},
	"search":  {"domain"},
	"card":    {},
	"rel":     {"reason"},
	"report":  {"last"},
}

var globalFlagNames = []string{"json", "vault", "help", "h"}

func newTestRoot(t *testing.T, wd string) *Root {
	t.Helper()
	r := New()
	r.Getwd = func() (string, error) { return wd, nil }
	return r
}

func runCLI(t *testing.T, r *Root, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := r.Run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

// —— ① eg --help 列出且仅列出九个 S1 命令 ——

func TestHelpListsExactlyNineS1Commands(t *testing.T) {
	r := New()
	if len(r.Commands()) != 9 {
		t.Fatalf("注册命令数 = %d，期望 9", len(r.Commands()))
	}
	var got []string
	for _, c := range r.Commands() {
		got = append(got, c.Display)
	}
	if strings.Join(got, ",") != strings.Join(wantCommands, ",") {
		t.Fatalf("命令表 = %v，期望 %v", got, wantCommands)
	}

	help := r.Usage()
	block := helpCommandBlock(t, help)
	if len(block) != 9 {
		t.Fatalf("--help 命令区 %d 行，期望 9 行：%v", len(block), block)
	}
	for i, line := range block {
		if !strings.HasPrefix(line, wantCommands[i]) {
			t.Fatalf("--help 第 %d 行 = %q，期望以 %q 开头", i+1, line, wantCommands[i])
		}
	}
	// S2 / S3 / S4 命令不得注册，也不得出现在 --help。
	for _, forbidden := range []string{"reconcile", "review", "propose", "index", "relate", "graph"} {
		if r.Lookup(forbidden) != nil {
			t.Fatalf("命令 %q 不属 S1 九命令，不得注册", forbidden)
		}
		for _, line := range block {
			if strings.HasPrefix(line, forbidden) {
				t.Fatalf("--help 命令区出现非 S1 命令 %q", forbidden)
			}
		}
	}
}

// helpCommandBlock 抽出 --help 的命令区（到空行为止）。
func helpCommandBlock(t *testing.T, help string) []string {
	t.Helper()
	lines := strings.Split(help, "\n")
	var block []string
	in := false
	for _, l := range lines {
		if strings.HasPrefix(l, "命令（") {
			in = true
			continue
		}
		if !in {
			continue
		}
		if strings.TrimSpace(l) == "" {
			break
		}
		block = append(block, strings.TrimSpace(l))
	}
	if len(block) == 0 {
		t.Fatalf("--help 中未找到命令区：\n%s", help)
	}
	return block
}

// —— ② 每个命令的 --help 参数与合同参数表逐项一致 ——

func TestCommandFlagsMatchContract(t *testing.T) {
	contract, readErr := os.ReadFile(contractDoc)
	if readErr != nil {
		t.Logf("合同文档不可读（要求 teamwork/ 与 evergreen/ 同级）：%v；仅做本地表比对", readErr)
	}
	for _, c := range New().Commands() {
		fs := flag.NewFlagSet("eg "+c.Name, flag.ContinueOnError)
		fs.SetOutput(&bytes.Buffer{})
		fs.Bool("json", false, "")
		fs.String("vault", "", "")
		fs.Bool("help", false, "")
		fs.Bool("h", false, "")
		if c.Flags != nil {
			c.Flags(fs)
		}
		var got []string
		fs.VisitAll(func(f *flag.Flag) {
			for _, g := range globalFlagNames {
				if f.Name == g {
					return
				}
			}
			got = append(got, f.Name)
		})
		want := append([]string{}, wantFlags[c.Name]...)
		sort.Strings(got)
		sort.Strings(want)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("eg %s 的 flag = %v，合同参数表 = %v", c.Display, got, want)
		}
		// --help 文本必须逐项列出这些参数，且合同里也必须有同名参数。
		for _, name := range want {
			if !strings.Contains(c.Usage, "--"+name) {
				t.Errorf("eg %s 的 --help 未列出 --%s", c.Display, name)
			}
			if len(contract) > 0 && !strings.Contains(string(contract), "--"+name) {
				t.Errorf("合同文档缺参数 --%s（实现不得自行发明参数）", name)
			}
		}
		if !strings.Contains(c.Usage, "eg "+c.Name) {
			t.Errorf("eg %s 的 --help 缺用法行", c.Display)
		}
	}
}

func TestEachCommandHelpExitsZeroAndPrintsToStdout(t *testing.T) {
	for _, c := range New().Commands() {
		r := newTestRoot(t, t.TempDir())
		code, out, errOut := runCLI(t, r, c.Name, "--help")
		if code != ExitOK {
			t.Errorf("eg %s --help 退出码 = %d，期望 0", c.Name, code)
		}
		if !strings.Contains(out, "eg "+c.Name) {
			t.Errorf("eg %s --help 未把用法写进 stdout：%q", c.Name, out)
		}
		if errOut != "" {
			t.Errorf("eg %s --help 不应写 stderr：%q", c.Name, errOut)
		}
	}
}

// —— ③ 占位命令五条：退 1、固定措辞、零副作用 ——

var placeholderInvocations = [][]string{
	{"search", "rag"},
	{"card", "show", "k-20260901-x"},
	{"rel", "k-20260901-x"},
	{"rel", "add", "k-20260901-a", "supports", "k-20260901-b", "--reason", "r"},
	{"rel", "remove", "k-20260901-a", "supports", "k-20260901-b"},
}

func TestPlaceholderCommandsExitOneWithFixedWording(t *testing.T) {
	if len(placeholderInvocations) != 5 {
		t.Fatalf("占位用例集合必须是五条，实际 %d", len(placeholderInvocations))
	}
	for _, args := range placeholderInvocations {
		dir := t.TempDir()
		before := snapshot(t, dir)
		r := newTestRoot(t, dir)
		code, out, errOut := runCLI(t, r, args...)
		if code != ExitUsage {
			t.Fatalf("eg %v 退出码 = %d，期望 1", args, code)
		}
		if !strings.Contains(errOut, PlaceholderNotice) {
			t.Fatalf("eg %v 的 stderr 必须含固定文案 %q，实际 %q", args, PlaceholderNotice, errOut)
		}
		if strings.Contains(errOut+out, forbiddenWording) {
			t.Fatalf("eg %v 输出出现被禁措辞 %q", args, forbiddenWording)
		}
		if out != "" {
			t.Fatalf("占位命令不得写 stdout，实际 %q", out)
		}
		if after := snapshot(t, dir); after != before {
			t.Fatalf("eg %v 产生了文件变化：%q → %q", args, before, after)
		}
	}
}

func TestPlaceholderJSONEnvelope(t *testing.T) {
	r := newTestRoot(t, t.TempDir())
	code, out, _ := runCLI(t, r, "--json", "rel", "k-20260901-x")
	if code != ExitUsage {
		t.Fatalf("退出码 = %d，期望 1", code)
	}
	var env map[string]interface{}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("--json 输出不是合法 JSON：%v（%q）", err, out)
	}
	assertEnvelopeKeys(t, env)
	if env["exit_code"].(float64) != 1 || env["status"] != StatusFailed || env["ok"].(bool) {
		t.Fatalf("信封与退出码不一致：%v", env)
	}
	data, _ := env["data"].(map[string]interface{})
	raw, _ := json.Marshal(data["errors"])
	if !strings.Contains(string(raw), PlaceholderNotice) {
		t.Fatalf("data.errors 缺占位文案：%s", raw)
	}
}

func TestRelReadPathIsNotImplemented(t *testing.T) {
	r := newTestRoot(t, t.TempDir())
	_, out, errOut := runCLI(t, r, "rel", "k-20260901-x")
	// 输出只含未实现文案，不含任何关系数据。
	for _, leak := range []string{"derives", "supports", "limits", "opposing", "relations["} {
		if strings.Contains(out+errOut, leak) {
			t.Fatalf("eg rel 的占位输出泄漏了关系数据 %q：%q", leak, out+errOut)
		}
	}
	relCmd := New().Lookup("rel")
	for _, want := range []string{MilestoneTag, "add", "remove"} {
		if !strings.Contains(relCmd.Usage, want) {
			t.Fatalf("rel --help 缺 %q 标注", want)
		}
	}
	// add|remove 行必须各自带 M2 标注。
	for _, line := range strings.Split(relCmd.Usage, "\n") {
		if strings.HasPrefix(line, "eg rel add") || strings.HasPrefix(line, "eg rel remove") {
			if !strings.Contains(line, "M2") {
				t.Fatalf("rel 写路径行缺 M2 标注：%q", line)
			}
		}
	}
	if !strings.Contains(strings.SplitN(relCmd.Usage, "\n", 2)[0], "M2") {
		t.Fatalf("rel 读路径行缺 M2 标注：%q", relCmd.Usage)
	}
}

func TestPlaceholderCannotBeWired(t *testing.T) {
	r := New()
	for _, name := range []string{"search", "card", "rel"} {
		if err := r.Wire(name, func(*Invocation) (*Result, error) { return &Result{}, nil }); err == nil {
			t.Fatalf("占位命令 %q 不得允许挂载实现", name)
		}
	}
	if err := r.Wire("context", func(*Invocation) (*Result, error) { return &Result{}, nil }); err != nil {
		t.Fatalf("context 应允许挂载实现：%v", err)
	}
}

// —— ④ 退出码集中映射：0/1/2/3/4 五条路径 ——

func TestExitCodeForCoversFivePaths(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{nil, ExitOK},
		{&UsageError{Msg: "u"}, ExitUsage},
		{&NotWiredError{Command: "init", Owner: "T-…-008"}, ExitUsage},
		{&ValidationError{Msg: "v"}, ExitValidation},
		{&PartialWriteError{Msg: "p"}, ExitPartialWrite},
		{&CommitFailedError{Msg: "c", Err: errors.New("git")}, ExitCommitFailed},
		{errors.New("未分类"), ExitUsage},
	}
	for _, c := range cases {
		if got := ExitCodeFor(c.err); got != c.want {
			t.Errorf("ExitCodeFor(%v) = %d，期望 %d", c.err, got, c.want)
		}
	}
	for code, want := range map[int]string{
		ExitOK: StatusCompleted, ExitUsage: StatusFailed, ExitValidation: StatusFailed,
		ExitPartialWrite: StatusPartial, ExitCommitFailed: StatusPartial,
	} {
		if got := StatusFor(code); got != want {
			t.Errorf("StatusFor(%d) = %q，期望 %q", code, got, want)
		}
	}
	// 5 / 6 不得出现在 S1 的映射结果里。
	for _, err := range []error{nil, &UsageError{}, &ValidationError{}, &PartialWriteError{},
		&CommitFailedError{}, &NotWiredError{}, errors.New("x")} {
		if got := ExitCodeFor(err); got < 0 || got > 4 {
			t.Fatalf("退出码 %d 越出 S1 的封闭集合 0–4（5 属 S5、6 属 S2）", got)
		}
	}
}

func TestRunMapsTypedErrorsEndToEnd(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"ok", nil, ExitOK},
		{"usage", &UsageError{Msg: "参数非法"}, ExitUsage},
		{"validation", &ValidationError{Msg: "校验失败"}, ExitValidation},
		{"partial", &PartialWriteError{Msg: "部分写入被跳过"}, ExitPartialWrite},
		{"commit", &CommitFailedError{Msg: "Git 提交失败", Err: errors.New("exit 128")}, ExitCommitFailed},
	}
	for _, c := range cases {
		dir := configuredVault(t)
		r := newTestRoot(t, dir)
		err := c.err
		if e := r.Wire("apply", func(*Invocation) (*Result, error) {
			return &Result{Data: map[string]interface{}{"probe": c.name}}, err
		}); e != nil {
			t.Fatal(e)
		}
		code, out, errOut := runCLI(t, r, "--json", "apply", "--plan", "-")
		if code != c.want {
			t.Fatalf("%s：退出码 = %d，期望 %d（stderr=%q）", c.name, code, c.want, errOut)
		}
		var env map[string]interface{}
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("%s：--json 输出非法：%v（%q）", c.name, err, out)
		}
		if int(env["exit_code"].(float64)) != c.want {
			t.Fatalf("%s：exit_code 与进程退出码必须逐字相同", c.name)
		}
		if env["status"] != StatusFor(c.want) {
			t.Fatalf("%s：status 必须由 exit_code 派生", c.name)
		}
	}
}

// —— ⑤ 双渲染事实一致 ——

func TestDualRenderingSameFacts(t *testing.T) {
	res := &Result{
		Data: map[string]interface{}{
			"source_id": "s-20260901-a",
			"deduped":   true,
			"path":      "sources/s-20260901-a.md",
		},
		Warnings: []Diagnostic{{
			Code: "W6", Level: LevelWarning, Path: "ops[2].reason", OpIndex: 2,
			Message: "base 未覆盖 k-20260901-bar，已跳过该文件", Target: "k-20260901-bar",
		}},
		Summary: []string{"已收录 1 篇原文"},
	}
	env := NewEnvelope(res, ExitOK)

	var jsonBuf, humanBuf bytes.Buffer
	if err := RenderJSON(&jsonBuf, env); err != nil {
		t.Fatal(err)
	}
	if err := RenderHuman(&humanBuf, env, res.Summary); err != nil {
		t.Fatal(err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(jsonBuf.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	assertEnvelopeKeys(t, decoded)

	human := humanBuf.String()
	for k, v := range res.Data {
		if !strings.Contains(human, k) {
			t.Fatalf("人类可读渲染缺 data 键 %q：\n%s", k, human)
		}
		if !strings.Contains(human, scalarString(v)) {
			t.Fatalf("人类可读渲染缺 data 值 %v：\n%s", v, human)
		}
	}
	for _, want := range []string{
		StatusCompleted, "exit_code=0", res.Summary[0],
		res.Warnings[0].Code, res.Warnings[0].Path, res.Warnings[0].Message, res.Warnings[0].Target,
	} {
		if !strings.Contains(human, want) {
			t.Fatalf("人类可读渲染缺事实 %q：\n%s", want, human)
		}
	}
	if !strings.Contains(human, "警告（1 条）") {
		t.Fatalf("人类可读渲染缺警告条数：\n%s", human)
	}
}

func assertEnvelopeKeys(t *testing.T, env map[string]interface{}) {
	t.Helper()
	var got []string
	for k := range env {
		got = append(got, k)
	}
	want := append([]string{}, EnvelopeKeys()...)
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("信封键 = %v，合同 §3 规定恰五项 %v", got, want)
	}
}

// —— ⑥ 参数非法退 1 且零文件变化 ——

func TestInvalidArgsExitOneWithZeroFileChange(t *testing.T) {
	cases := [][]string{
		{"apply"},                           // 缺必填 --plan
		{"apply", "--nope"},                 // 未知 flag
		{"config"},                          // 缺子命令
		{"config", "frobnicate", "x"},       // 非法子命令
		{"config", "get", "unknown_key"},    // 非法配置键
		{"config", "set", "default_domain"}, // 缺 value
		{"context"},                         // 缺 --source/--note
		{"context", "--source", "s-1", "--note", "n-1"}, // 互斥
		{"capture", "--reason", "r", "--body-stdin"},    // url/title 同缺 → 2（另测）
		{"report"},                   // 缺 --last
		{"init", "extra-positional"}, // 多余位置参数
		{"nosuchcommand"},            // 未知命令
	}
	for _, args := range cases {
		dir := configuredVault(t)
		before := snapshot(t, dir)
		r := newTestRoot(t, dir)
		code, out, errOut := runCLI(t, r, append([]string{"--vault", dir}, args...)...)
		wantCode := ExitUsage
		if args[0] == "capture" {
			wantCode = ExitValidation // 合同 §1.3：--url 与 --title 同时缺失 → 校验失败
		}
		if code != wantCode {
			t.Fatalf("eg %v 退出码 = %d，期望 %d（stderr=%q）", args, code, wantCode, errOut)
		}
		if out != "" {
			t.Fatalf("eg %v 参数非法时 stdout 必须为空，实际 %q", args, out)
		}
		if after := snapshot(t, dir); after != before {
			t.Fatalf("eg %v 参数非法却改了文件：%q → %q", args, before, after)
		}
	}
}

// —— ⑦ default_domain 守卫 ——

func TestDefaultDomainGuard(t *testing.T) {
	// 未配置 default_domain：context 退 1 且 stderr 含配置提示。
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ConfigFileName), "version: 1\ndomains:\n  - ai-infra\n")
	r := newTestRoot(t, dir)
	code, _, errOut := runCLI(t, r, "context", "--source", "s-20260901-a")
	if code != ExitUsage {
		t.Fatalf("未配置 default_domain 时 eg context 退出码 = %d，期望 1", code)
	}
	if !strings.Contains(errOut, "default_domain") || !strings.Contains(errOut, "eg config set") {
		t.Fatalf("stderr 缺配置提示：%q", errOut)
	}

	// init / config 不受该守卫影响（实现由 T-…-008 挂载后应正常执行，退 0）。
	setGitIdentity(t)
	for _, args := range [][]string{{"init"}, {"config", "get", "default_domain"}} {
		code, _, errOut := runCLI(t, newTestRoot(t, dir), args...)
		if strings.Contains(errOut, "未配置 default_domain") {
			t.Fatalf("eg %v 不应被 default_domain 守卫拦截：%q", args, errOut)
		}
		if code != ExitOK {
			t.Fatalf("eg %v 应正常执行（T-…-008 已挂载实现），实际 code=%d stderr=%q", args, code, errOut)
		}
	}

	// 已配置 default_domain：守卫放行，落到 context 自身的校验
	// （T-…-010 挂载后：库里没有该原文 → 退 2「对象不存在」，而不是被守卫拦在 1）。
	ok := configuredVault(t)
	code, _, errOut = runCLI(t, newTestRoot(t, ok), "context", "--source", "s-20260901-a")
	if code != ExitValidation || strings.Contains(errOut, "default_domain") {
		t.Fatalf("守卫应放行，实际 code=%d stderr=%q", code, errOut)
	}
	// 找不到 vault：退 1 并提示 evergreen.yml。
	empty := t.TempDir()
	code, _, errOut = runCLI(t, newTestRoot(t, empty), "context", "--vault", empty, "--source", "s-1")
	if code != ExitUsage || !strings.Contains(errOut, ConfigFileName) {
		t.Fatalf("缺 %s 时应退 1 并提示，实际 code=%d stderr=%q", ConfigFileName, code, errOut)
	}
}

func TestGuardReadsConfigReadOnly(t *testing.T) {
	dir := configuredVault(t)
	raw, err := os.ReadFile(filepath.Join(dir, ConfigFileName))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfigReadOnly(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Version != model.ConfigVersion || cfg.DefaultDomain != "ai-infra" {
		t.Fatalf("配置解析异常：%+v", cfg)
	}
	if cfg.Extra["custom_key"] != "保留" {
		t.Fatalf("未知键必须落进 Extra：%v", cfg.Extra)
	}
	after, err := os.ReadFile(filepath.Join(dir, ConfigFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, after) {
		t.Fatal("只读解析不得改动 evergreen.yml 一个字节")
	}
}

// —— ⑧ 只读命令零副作用：git status 为空、git log 条数不变 ——

func TestReadOnlyCommandsLeaveGitClean(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("无 git，跳过 git 层零副作用断言")
	}
	dir := configuredVault(t)
	run := func(args ...string) string {
		cmd := exec.Command(git, args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v 失败：%v（%s）", args, err, out)
		}
		return string(out)
	}
	run("init", "-q")
	run("-c", "user.email=eg@example.com", "-c", "user.name=eg", "add", ".")
	run("-c", "user.email=eg@example.com", "-c", "user.name=eg", "commit", "-q", "-m", "init")
	if s := run("status", "--porcelain"); s != "" {
		t.Fatalf("前置条件失败，工作区应干净：%q", s)
	}
	logBefore := run("rev-list", "--count", "HEAD")

	readOnly := [][]string{
		{"context", "--source", "s-20260901-a"},
		{"report", "--last"},
		{"search", "rag"},
		{"card", "show", "k-20260901-x"},
		{"rel", "k-20260901-x"},
	}
	for _, args := range readOnly {
		r := newTestRoot(t, dir)
		runCLI(t, r, append([]string{"--vault", dir}, args...)...)
		if s := run("status", "--porcelain"); s != "" {
			t.Fatalf("eg %v 之后 git status 非空：%q", args, s)
		}
		if got := run("rev-list", "--count", "HEAD"); got != logBefore {
			t.Fatalf("eg %v 改变了 git log 条数：%q → %q", args, logBefore, got)
		}
	}
	for _, c := range New().Commands() {
		if c.Placeholder && !c.ReadOnly {
			t.Fatalf("占位命令 %s 必须标记为只读（零副作用保证覆盖它们）", c.Display)
		}
	}
}

// —— ⑨ 源码级反证：os.Exit / 被禁措辞 / rel 不引用查询包 ——

func TestOnlyMainCallsOsExit(t *testing.T) {
	root := "../.."
	var offenders []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if name := info.Name(); name == "bin" || name == "dist" || name == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// 反证串同样分片拼接，避免本测试文件自己成为命中项。
		if !strings.Contains(string(raw), "os."+"Exit(") {
			return nil
		}
		if filepath.ToSlash(path) == "../../cmd/eg/main.go" {
			return nil
		}
		offenders = append(offenders, path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Fatalf("除 cmd/eg/main.go 外不得调用 os.Exit，命中：%v", offenders)
	}
}

// forbiddenWording 是被禁措辞。**分片拼接**而非字面量：门禁口径是
// `grep -rn "<该措辞>" internal/cli/` 必须零匹配，字面量会让本文件自己成为命中项。
var forbiddenWording = "S1 " + "未" + "覆盖"

// TestForbiddenWordingAbsent 对 internal/cli 全部 .go 文件（含测试）反证被禁措辞。
func TestForbiddenWordingAbsent(t *testing.T) {
	for _, f := range goFilesHere(t) {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), forbiddenWording) {
			t.Fatalf("%s 出现被禁措辞 %q（应为 %q）", f, forbiddenWording, PlaceholderNotice)
		}
	}
}

func TestRelFilesDoNotReferenceQueryPackage(t *testing.T) {
	matches, err := filepath.Glob("rel*")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatal("internal/cli 下应有 rel* 文件承载 rel 占位命令")
	}
	for _, f := range matches {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "internal/query") {
			t.Fatalf("%s 不得引用查询包（P1-2 反证：rel 读路径在 M1 不实现）", f)
		}
	}
}

func goFilesHere(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
			out = append(out, e.Name())
		}
	}
	return out
}

// —— 测试辅助 ——

func configuredVault(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ConfigFileName),
		"version: 1\n# 用户注释：只读解析不得动它\ndomains:\n  - ai-infra\ndefault_domain: ai-infra\ncustom_key: 保留\n")
	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// snapshot 返回目录树的稳定快照（路径 + 内容长度），用于零副作用断言。
func snapshot(t *testing.T, dir string) string {
	t.Helper()
	var lines []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		if info.IsDir() {
			lines = append(lines, "d "+rel)
			return nil
		}
		lines = append(lines, "f "+rel+" "+itoa(info.Size()))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
