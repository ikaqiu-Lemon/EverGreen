package cli

// 命令注册树与全局 flag（合同 §1 / §1.0）。
//
// 本文件只做**骨架**：注册九命令、声明参数、解析、统一守卫、把结果交给 exit.go 渲染。
// 各命令的业务实现由后续 task 通过 Wire 挂载（init/config → T-…-008，capture → 009，
// context → 010，apply → 015，report → 016）。骨架阶段调用未挂载命令返回
// NotWiredError（退 1、零写入），不 panic、不静默成功。

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"evergreen/internal/model"
	"evergreen/internal/version"
)

// ConfigFileName 是 vault 根的标识文件：工程配置而非知识产物，纳入 Git。
const ConfigFileName = "evergreen.yml"

// PlaceholderNotice 是 M1 占位命令的**固定文案**（合同 §1 M1 占位口径）。
// 措辞固定：它们都是 §7.1 的 S1 命令，只是按 §16.1 划归 M2 实现，
// 不得改写成任何暗示它们不属于 S1 的口径（门禁对该措辞做 grep 反证）。
const PlaceholderNotice = "M1 未实现（S1 命令，M2 落地）"

// MilestoneTag 是 --help 中占位命令的里程碑标注。
const MilestoneTag = "M2 落地"

// flagSet 是 flag.FlagSet 的别名：命令声明参数只需要标准库 flag，不引入任何第三方 CLI 框架。
type flagSet = flag.FlagSet

// Handler 是命令业务实现的挂载点。
type Handler func(*Invocation) (*Result, error)

// Invocation 是一次命令调用的上下文。
type Invocation struct {
	Cmd   *Command
	Sub   string   // 子动词：config get|set、card show、rel add|remove；无则空
	Args  []string // 去掉子动词后的位置参数
	Flags *flag.FlagSet
	JSON  bool

	VaultFlag string // --vault 原样值
	VaultRoot string // 解析后的 vault 根（未找到则空）
	Config    model.Config

	Out io.Writer
	Err io.Writer
}

// Set 报告某个 flag 是否被显式给出（区别于「取了默认值」）。
func (inv *Invocation) Set(name string) bool {
	found := false
	inv.Flags.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

// String 返回 flag 的当前字符串值。
func (inv *Invocation) String(name string) string {
	if f := inv.Flags.Lookup(name); f != nil {
		return f.Value.String()
	}
	return ""
}

// Command 是一个已注册命令。
type Command struct {
	Name    string // 命令树上的一级名字
	Display string // 帮助与文案里的展示名，如 "config get|set"
	Summary string // 一行说明
	Subs    []string
	// SubRequired 为真时首个位置参数必须是 Subs 之一（config get|set、card show）；
	// 为假时首个位置参数也可以是普通参数（rel <k-id> 的读路径）。
	SubRequired bool
	Usage       string // 完整用法块（参数表逐项对齐合同 §1.x）

	ReadOnly       bool // 只读命令：零文件变化、零 commit
	SkipVaultGuard bool // init / config 不受 default_domain 守卫约束
	Placeholder    bool // M1 占位（search / card show / rel）
	Owner          string

	Flags    func(*flagSet)
	Validate func(*Invocation) error
	Handler  Handler
}

// Root 是命令树。
type Root struct {
	cmds []*Command

	// FindVault 解析 vault 根；LoadConfig 只读解析 evergreen.yml。
	// 两者可被下游 task 替换（T-…-008 接入真实实现后注入）。
	FindVault  func(start string) (string, error)
	LoadConfig func(root string) (model.Config, error)
	Getwd      func() (string, error)
}

// New 构造带九命令的命令树，并挂载**已实现**的命令。
//
// 已挂载：init / config get|set（T-…-008）。其余命令仍返回 NotWiredError（退 1、零写入），
// 由各自的 task 挂载；占位命令永不挂载。
func New() *Root {
	r := &Root{
		cmds:       commands(),
		FindVault:  findVaultUpwards,
		LoadConfig: loadConfigReadOnly,
		Getwd:      os.Getwd,
	}
	r.wireImplemented()
	return r
}

// wireImplemented 挂载本阶段已落地的业务实现。Wire 失败只可能是注册表与实现不一致
// （编译期就该发现的 bug），因此直接 panic 会掩盖问题——这里选择保留 NotWired 行为。
func (r *Root) wireImplemented() {
	_ = r.Wire("init", r.runInit)
	_ = r.Wire("config", r.runConfig)
}

// Commands 返回注册的命令（顺序即 --help 顺序）。
func (r *Root) Commands() []*Command { return r.cmds }

// Lookup 按名字取命令。
func (r *Root) Lookup(name string) *Command {
	for _, c := range r.cmds {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// Wire 给命令挂载业务实现（下游 task 用）。占位命令不允许挂载实现。
func (r *Root) Wire(name string, h Handler) error {
	c := r.Lookup(name)
	if c == nil {
		return fmt.Errorf("未注册的命令 %q", name)
	}
	if c.Placeholder {
		return fmt.Errorf("eg %s 是 M1 占位命令（%s），不得挂载实现", c.Display, PlaceholderNotice)
	}
	c.Handler = h
	return nil
}

// Usage 渲染顶层帮助：命令区**只列九个 S1 命令**。
func (r *Root) Usage() string {
	var b strings.Builder
	b.WriteString("eg — Evergreen 确定性本地 CLI（单二进制；不调用模型、不做网络请求）\n\n")
	b.WriteString("用法：\n  eg <command> [flags]\n\n命令（S1 九命令）：\n")
	width := 0
	for _, c := range r.cmds {
		if len(c.Display) > width {
			width = len(c.Display)
		}
	}
	for _, c := range r.cmds {
		line := fmt.Sprintf("  %-*s  %s", width, c.Display, c.Summary)
		if c.Placeholder {
			line += "（" + MilestoneTag + "）"
		}
		b.WriteString(line + "\n")
	}
	b.WriteString(`
全局 flag：
  --json           输出结构化信封（ok / data / warnings / exit_code / status）
  --vault <path>   vault 根；默认从 cwd 向上查找含 evergreen.yml 的目录
  --help, -h       打印用法后退 0
  --version        打印版本后退 0

退出码：0 成功 | 1 用法 / 参数非法（零写入） | 2 校验失败（零写入） |
       3 部分写入被跳过 | 4 Git 提交失败（磁盘保留现状，不做破坏性还原）
`)
	return b.String()
}

// Run 执行一次命令，返回进程退出码。**本函数不调用 os.Exit**。
func (r *Root) Run(args []string, out, errw io.Writer) int {
	g, rest, err := parseGlobal(args)
	if err != nil {
		fmt.Fprint(errw, r.Usage())
		fmt.Fprintf(errw, "错误：%s\n", err)
		return ExitUsage
	}
	if g.version {
		fmt.Fprintln(out, version.String())
		return ExitOK
	}
	if len(rest) == 0 {
		if g.help {
			fmt.Fprint(out, r.Usage())
			return ExitOK
		}
		fmt.Fprint(errw, r.Usage())
		fmt.Fprintln(errw, "错误：缺少子命令")
		return ExitUsage
	}

	cmd := r.Lookup(rest[0])
	if cmd == nil {
		fmt.Fprint(errw, r.Usage())
		fmt.Fprintf(errw, "错误：未知命令 %q（S1 只有九个命令）\n", rest[0])
		return ExitUsage
	}

	jsonMode, res, err := r.dispatch(cmd, g, rest[1:], out, errw)
	return r.render(cmd, jsonMode, res, err, out, errw)
}

type globalFlags struct {
	json    bool
	vault   string
	help    bool
	version bool
}

// parseGlobal 抽出子命令**之前**的全局 flag；子命令之后的全局 flag 由 FlagSet 再解析一次。
func parseGlobal(args []string) (globalFlags, []string, error) {
	var g globalFlags
	i := 0
	for ; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return g, args[i+1:], nil
		case a == "--json":
			g.json = true
		case a == "--help" || a == "-h" || a == "help":
			g.help = true
		case a == "--version":
			g.version = true
		case a == "--vault":
			if i+1 >= len(args) {
				return g, nil, errors.New("--vault 缺少参数值")
			}
			i++
			g.vault = args[i]
		case strings.HasPrefix(a, "--vault="):
			g.vault = strings.TrimPrefix(a, "--vault=")
		case strings.HasPrefix(a, "-"):
			return g, nil, fmt.Errorf("未知全局 flag %q", a)
		default:
			return g, args[i:], nil
		}
	}
	return g, nil, nil
}

// dispatch 解析参数并执行命令，返回 (是否 JSON 输出, 结果, 错误)。
func (r *Root) dispatch(cmd *Command, g globalFlags, args []string, out, errw io.Writer) (bool, *Result, error) {
	sub := ""
	if len(cmd.Subs) > 0 && len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		for _, s := range cmd.Subs {
			if args[0] == s {
				sub, args = s, args[1:]
				break
			}
		}
		if sub == "" && cmd.SubRequired {
			return g.json, nil, &UsageError{Msg: fmt.Sprintf(
				"eg %s 的子命令必须是 %s，实际 %q", cmd.Name, strings.Join(cmd.Subs, " | "), args[0])}
		}
	}
	if cmd.SubRequired && sub == "" && len(args) == 0 {
		return g.json, nil, &UsageError{Msg: fmt.Sprintf(
			"eg %s 必须带子命令：%s", cmd.Name, strings.Join(cmd.Subs, " | "))}
	}

	fs := flag.NewFlagSet("eg "+cmd.Name, flag.ContinueOnError)
	var buf bytes.Buffer
	fs.SetOutput(&buf)
	jsonFlag := fs.Bool("json", g.json, "输出结构化信封")
	vaultFlag := fs.String("vault", g.vault, "vault 根")
	helpFlag := fs.Bool("help", false, "打印用法")
	fs.BoolVar(helpFlag, "h", false, "打印用法")
	if cmd.Flags != nil {
		cmd.Flags(fs)
	}
	if err := fs.Parse(args); err != nil {
		msg := strings.TrimSpace(buf.String())
		if errors.Is(err, flag.ErrHelp) {
			*helpFlag = true
		} else {
			return g.json, nil, &UsageError{
				Msg: fmt.Sprintf("eg %s 参数非法：%s", cmd.Display, firstLine(msg))}
		}
	}
	if g.help || *helpFlag {
		fmt.Fprint(out, cmd.Usage)
		return *jsonFlag, nil, errHelpRequested
	}

	inv := &Invocation{
		Cmd:       cmd,
		Sub:       sub,
		Args:      fs.Args(),
		Flags:     fs,
		JSON:      *jsonFlag,
		VaultFlag: *vaultFlag,
		Out:       out,
		Err:       errw,
	}

	// 占位命令**先短路**：只写 stderr，不解析 vault、不读配置、不碰任何文件（零副作用）。
	if cmd.Placeholder {
		return inv.JSON, nil, placeholderError(cmd, sub)
	}
	if cmd.Validate != nil {
		if err := cmd.Validate(inv); err != nil {
			return inv.JSON, nil, err
		}
	}
	if !cmd.SkipVaultGuard {
		if err := r.guard(inv); err != nil {
			return inv.JSON, nil, err
		}
	}
	if cmd.Handler == nil {
		return inv.JSON, nil, &NotWiredError{Command: cmd.Display, Owner: cmd.Owner}
	}
	res, err := cmd.Handler(inv)
	return inv.JSON, res, err
}

// errHelpRequested 表示「已打印用法，退 0」，不是错误对外语义。
var errHelpRequested = errors.New("help requested")

// guard 是框架层统一守卫：
//   - 找不到 vault（缺 evergreen.yml）→ 退 1；
//   - 未配置 default_domain → 退 1 并提示配置（EG-DOM-03：CLI 绝不自选默认领域）。
//
// init / config 由 SkipVaultGuard 豁免。
func (r *Root) guard(inv *Invocation) error {
	start := inv.VaultFlag
	if start == "" {
		wd, err := r.Getwd()
		if err != nil {
			return &UsageError{Msg: "无法确定当前目录：" + err.Error()}
		}
		start = wd
	}
	root, err := r.FindVault(start)
	if err != nil {
		return &UsageError{Msg: fmt.Sprintf(
			"未找到 vault（缺 %s）：%v；先执行 eg init 或用 --vault <path> 指定", ConfigFileName, err)}
	}
	inv.VaultRoot = root
	cfg, err := r.LoadConfig(root)
	if err != nil {
		return &UsageError{Msg: fmt.Sprintf("%s 不可解析：%v", ConfigFileName, err)}
	}
	inv.Config = cfg
	if !cfg.DefaultDomainConfigured() {
		return &UsageError{Msg: fmt.Sprintf(
			"未配置 default_domain：请先执行 eg config set default_domain <domain>（%s 未设置该键）；"+
				"CLI 绝不自选默认领域", ConfigFileName)}
	}
	return nil
}

func (r *Root) render(cmd *Command, jsonMode bool, res *Result, err error, out, errw io.Writer) int {
	if errors.Is(err, errHelpRequested) {
		return ExitOK
	}
	code := ExitCodeFor(err)
	if err != nil {
		res = mergeErrorIntoResult(res, err, cmd)
	}
	env := NewEnvelope(res, code)
	if jsonMode {
		_ = RenderJSON(out, env)
		return code
	}
	sink := out
	if code != ExitOK {
		sink = errw
		fmt.Fprintf(sink, "错误：%s\n", err)
	}
	var summary []string
	if res != nil {
		summary = res.Summary
	}
	_ = RenderHuman(sink, env, summary)
	return code
}

// mergeErrorIntoResult 把错误落进 Result.Errors，使 --json 与人类可读两套渲染事实一致。
func mergeErrorIntoResult(res *Result, err error, cmd *Command) *Result {
	if res == nil {
		res = &Result{}
	}
	var d diagnoser
	if errors.As(err, &d) {
		res.Errors = append(res.Errors, d.Diagnostics()...)
	}
	// 已由带类型错误给出同message诊断时不再重复追加顶层条目。
	for _, existing := range res.Errors {
		if strings.Contains(err.Error(), existing.Message) {
			return res
		}
	}
	res.Errors = append(res.Errors, Diagnostic{
		Code:    "",
		Level:   LevelError,
		Path:    "eg " + cmd.Display,
		OpIndex: NonOpDiagnostic,
		Message: err.Error(),
	})
	return res
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// findVaultUpwards 从 start 起逐级向上查找含 evergreen.yml 的目录。
func findVaultUpwards(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if st, err := os.Stat(filepath.Join(dir, ConfigFileName)); err == nil && !st.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("自 %s 向上未找到 %s", start, ConfigFileName)
		}
		dir = parent
	}
}

// stringList 承接可重复 flag（--domain / --tag）。
type stringList []string

func (l *stringList) String() string { return strings.Join(*l, ",") }

func (l *stringList) Set(v string) error {
	*l = append(*l, v)
	return nil
}
