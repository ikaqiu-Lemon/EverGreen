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
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/git"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
	"github.com/ikaqiu-Lemon/EverGreen/internal/version"
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

	// UserRequest 是 `--user-request` 的取值：本次调用**是否由用户显式发起**。
	//
	// 它是 M3 用户显式路径（P-U）唯一的机器信号来源之一，且必须来自命令行：
	// plan 文件里的 `initiator: user` **不能**自证（授权合同 N-1 反伪造条款）。
	// 因此它落在 Invocation 上（进程边界），而不是从 plan 内容推导。
	UserRequest bool

	// selfBaseIDs 记「本次 plan.base 是 **CLI 自算**的」以及自算时用的 ID 集合。
	//
	// 它存在只为一件事（I-…-022）：`eg deprecate` / `eg edit` / `eg rel add|remove` /
	// `eg replaced-by` 这些单命令闭环在**锁外**（S0）就 planBase 采过一次前像，而 S1 之后的
	// S2 崩溃恢复屏障可能把权威文件回滚到前像、或另一个写者刚提交完一批改动 —— 于是锁外
	// 那份 base 在锁内已经过期，B3 会把「恢复本身」误判成 content_hash_mismatch，让崩溃后
	// **第一条**正常写命令必然退 3。修法是在 S2 之后按同一口径重采一次，而不是豁免 B3。
	//
	// 为什么必须区分来源、不能对所有 plan 一律重采：`eg apply --plan` 的 base 是**用户/Agent
	// 显式声明的期望版本**，重采就等于把 B3 改成永真，stale base 再也拦不住。因此只有 CLI
	// 自己刚算出来的那份才可重采，显式声明的那份一个字节都不许动。字段不可导出、不进任何
	// JSON 产物：它是进程内的编排事实，不是合同面。
	selfBaseIDs []string

	Out io.Writer
	Err io.Writer
}

// selfComputedBase 声明「本次 plan.base 由 CLI 按 ids 自算」，供临界区在 S2 之后重采。
//
// 调用点恒为各命令的 plan 组装函数（S0，锁外），与那里 planBase 用的 ids **同一份**：
// 重采若换一套 ID 集合，锁内锁外就成了两套 base 口径。
func (inv *Invocation) selfComputedBase(ids []string) { inv.selfBaseIDs = ids }

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
	Placeholder    bool // 占位命令（M1 期为 search / card show / rel；M2 起逐个摘除）
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

	// In 是 --body-stdin 与 --plan - 的输入来源（默认 os.Stdin）；Now 是本机时间来源
	// （默认 time.Now）。两者可注入，使收录 / 写入用例完全确定性（T-…-009 / 015）。
	In  io.Reader
	Now func() time.Time

	// NewRepo 构造 vault 的 Git 仓（默认 git.New）。可注入以覆盖「提交失败」分支（B4）。
	NewRepo func(root string) *git.Repo

	// StateWrite 是**逻辑删除逐文件落盘**的注入点（默认 nil → 直接走 store 的唯一写口
	// ApplyStateWrite，见 delete.go 的 applyStateWrite）。
	//
	// 为什么需要它：`eg delete` 的「第 N 个文件写失败」分支必须保留现状（U-02：已写的留着、
	// 未写的字节不变、绝不回滚），而该分支在真实文件系统上无法稳定复现（测试常以 root 身份运行，
	// 权限位挡不住写入）。注入点让这条不变式成为可执行断言，而不是靠代码评审。
	StateWrite func(st *store.Store, spec store.StateWriteSpec) (store.Result, error)

	// benchExec / benchSpecOverride 是 `eg bench` 的两个**包内**注入点（M5 · T-…-068）。
	//
	// 刻意**不导出**：采样口径不是参数面，能被外部调小的门槛不是门槛。
	//   - benchExec：采样要 fork 真实 eg 进程（合同 §7.3 计时边界 = 进程墙钟），
	//     而 `go test` 里的 os.Executable() 是测试二进制 —— 用例必须能换成真 eg 的路径。
	//   - benchSpecOverride：让单测用 1~2 轮跑完全流程，而不是每次 159 次 fork。
	//     生产路径恒取 BenchFrozenSpec()，由 TestBenchProductionUsesFrozenSpec 反证。
	benchExec         func() (string, error)
	benchSpecOverride *BenchSpec

	// cmdSurface 是本次调用的命令表面（`eg capture` / `eg apply` / `eg proposal approve`…），
	// 由 dispatch 在 Handler 之前**单点**写入（I-…-018）。
	//
	// 它只服务于「最近一次报告是谁产出的」这一条事实：`.eg/last-report.json` 里存了
	// `command`，`eg report --last` 的回放文案据此如实交代来源。空串表示不经 dispatch
	// 的直调（白盒单测），此时记录里的 `command` 也如实留空 —— 宁可不说，不许猜。
	cmdSurface string
}

// now 返回本机时间（可注入）。
func (r *Root) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// New 构造带九命令的命令树，并挂载**已实现**的命令。
//
// 已挂载：init / config get|set（T-…-008）、capture（T-…-009）、context（T-…-010）、
// apply（T-…-015）、report（T-…-016）、search（T-…-021）、card show（T-…-022）、
// rel 读路径（T-…-023）与 rel add 写路径（T-…-024；rel remove 归 M3/S2，见 rel.go）。
// 其余命令仍返回 NotWiredError（退 1、零写入），
// 由各自的 task 挂载；占位命令永不挂载。
func New() *Root {
	r := &Root{
		cmds:       commands(),
		FindVault:  findVaultUpwards,
		LoadConfig: loadConfigReadOnly,
		Getwd:      os.Getwd,
		In:         os.Stdin,
		Now:        time.Now,
	}
	r.wireImplemented()
	return r
}

// wireImplemented 挂载本阶段已落地的业务实现。Wire 失败只可能是注册表与实现不一致
// （编译期就该发现的 bug），因此直接 panic 会掩盖问题——这里选择保留 NotWired 行为。
func (r *Root) wireImplemented() {
	_ = r.Wire("init", r.runInit)
	_ = r.Wire("config", r.runConfig)
	_ = r.Wire("capture", r.runCapture)
	_ = r.Wire("context", r.runContext)
	_ = r.Wire("apply", r.runApply)
	_ = r.Wire("report", r.runReport)
	_ = r.Wire("search", r.runSearch)
	_ = r.Wire("card", r.runCardShow)
	_ = r.Wire("rel", r.runRel)
	// M3 三条用户显式状态命令（T-…-039）：它们不调 store，只合成单条 op 的 plan 走 runPlan。
	_ = r.Wire("deprecate", r.runDeprecate)
	_ = r.Wire("restore", r.runRestore)
	_ = r.Wire("replaced-by", r.runSetReplacedBy)
	// S2 提案子系统（T-…-040）：new / list / show / reject 已实现，approve 只注册壳。
	_ = r.Wire("proposal", r.runProposal)
	// M3 逻辑删除（T-…-041）：CLI 直写例外（A-23），逐文件走 store.ApplyStateWrite。
	_ = r.Wire("delete", r.runDelete)
	// M3 清空删除标记（T-…-041 收尾）：同为 CLI 直写例外（A-23），不需二次确认。
	_ = r.Wire("undelete", r.runUndelete)
	// M3 reviewed_at（T-…-042）：mark-reviewed 走命令侧直写（矩阵 #7 / #22 的 P-A 是 🔴，
	// 落盘只能发生在用户显式路径上）；unreviewed 只读、零副作用。
	_ = r.Wire("mark-reviewed", r.runMarkReviewed)
	_ = r.Wire("unreviewed", r.runUnreviewed)
	// M3 用户显式改核心内容（A-13，T-…-045）：edit 不 import store，
	// 只合成单条 edit_section op 的内存 plan 走 runPlan（矩阵 #12 的 P-U ✅ 载体）。
	_ = r.Wire("edit", r.runEdit)
	// M4 全库对账（T-…-058）：下面这一行是对账实现在本仓的**唯一**挂载点 ——
	// 对账不作为任何写命令的前置（合同 §0.1 第 3 条），因此除本行与 reconcile*.go
	// 之外，internal/cli 里不得再出现对该处理函数的引用。
	_ = r.Wire("reconcile", r.runReconcile)
	// M4 只读结构体检（T-…-059）：挂载动作本体在 check.go 的 wireCheck（那里是
	// `eg check` 处理函数在本仓的唯一落点）。强校验（校验不通过就拦住写）属 S5 / M6，
	// 因此除 check*.go 之外，internal/cli 里不得再出现对该处理函数的引用
	// —— 写命令一律不以它为前置，这条判据由文件边界结构性保证。
	r.wireCheck()
	// M5 派生索引（T-…-065）：挂载动作本体在 index.go 的 wireIndex（那里是 `eg index`
	// 处理函数在本仓的唯一落点）。索引**不作为任何命令的前置**：读路径接入与降级属
	// T-…-067，因此除 index*.go 之外，internal/cli 里不得再出现对该处理函数的引用。
	r.wireIndex()
	// M5 性能采样（T-…-068）：挂载动作本体在 bench.go 的 wireBench（那里是 `eg bench`
	// 处理函数在本仓的唯一落点）。性能采样**不作为任何命令的前置**，因此除 bench*.go 之外，
	// internal/cli 里不得再出现对该处理函数的引用。
	r.wireBench()
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
  --json           输出结构化信封（ok / data / warnings / exit_code / status）；
                   写在全局位（eg --json <cmd> …）与子命令位（eg <cmd> … --json）等价，
                   参数解析失败（退 1）时同样输出信封
  --vault <path>   vault 根；默认从 cwd 向上查找含 evergreen.yml 的目录
  --user-request   声明本次调用由用户显式发起；与 plan 内 initiator: user 同时成立
                   才进入用户显式路径 P-U（plan 内容不能自证授权）
  --help, -h       打印用法后退 0
  --version        打印版本后退 0

退出码：0 成功 | 1 用法 / 参数非法（零写入） | 2 校验失败（零写入） |
       3 部分写入被跳过 | 4 Git 提交失败（磁盘保留现状，不做破坏性还原）
`)
	return b.String()
}

// Run 执行一次命令，返回进程退出码。**本函数不调用 os.Exit**。
func (r *Root) Run(args []string, out, errw io.Writer) int {
	// I-…-017：输出格式的意图必须在**任何 flag 解析之前**定下来。
	//
	// 合同 §3 的五键是「必有」，且把退 `1`（用法 / 参数非法）明确列进有信封的一档；
	// 而 flag 解析失败恰恰发生在「还没解析出 --json」的时刻 —— 若此时用解析结果决定输出格式，
	// 就会出现「最需要机读的错误路径反而没有信封」。因此这里对 argv 做一次**只读扫描**
	// （argvWantsJSON，不消费参数、不改变解析语义），把它作为解析层失败的渲染依据。
	wantJSON := argvWantsJSON(args)
	g, rest, err := parseGlobal(args)
	if err != nil {
		return r.usageFailure(wantJSON, &UsageError{Msg: err.Error()}, out, errw)
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
		return r.usageFailure(wantJSON, &UsageError{Msg: "缺少子命令"}, out, errw)
	}

	cmd := r.Lookup(rest[0])
	if cmd == nil {
		return r.usageFailure(wantJSON, &UsageError{
			Msg: fmt.Sprintf("未知命令 %q（S1 只有九个命令）", rest[0])}, out, errw)
	}

	jsonMode, res, err := r.dispatch(cmd, g, rest[1:], out, errw)
	return r.render(cmd, jsonMode, res, err, out, errw)
}

// usageFailure 渲染「命令还没被认出来」这一层的用法失败（未知全局 flag / 缺子命令 / 未知命令）。
//
// `--json` 在场 → 走同一个信封渲染器（合同 §3 五键必有，不因失败发生在解析层而缺席）；
// 不在场 → 人类可读面**逐字保持原样**（打印用法 + `错误：…` 行，写 stderr）。
func (r *Root) usageFailure(jsonMode bool, err error, out, errw io.Writer) int {
	if jsonMode {
		return r.render(nil, true, nil, err, out, errw)
	}
	fmt.Fprint(errw, r.Usage())
	fmt.Fprintf(errw, "错误：%s\n", err)
	return ExitUsage
}

// argvWantsJSON 只读扫描 argv，判定本次是否要输出 JSON 信封。
//
// 为什么需要它（I-…-017）：`--json` 允许写在**全局位**（`eg --json <cmd> …`）也允许写在
// **子命令位**（`eg <cmd> … --json` —— 这是 README 与全部 `<cmd> --help` 用法行的写法）。
// 后者由子命令 FlagSet 解析，一旦 argv 里还有别的非法 flag，`fs.Parse` 会在解析出 `--json`
// **之前**失败，输出格式的意图就丢了。这个扫描把「意图」与「解析」解耦。
//
// 纪律：
//   - **只读**：不消费参数、不重排、不影响后续任何解析（解析仍由 parseGlobal + FlagSet 负责）；
//   - **末次生效**：`--json --json=false` 以最后一次取值为准，与标准库 flag 的覆盖语义一致，
//     因此「显式关掉」不会被误判成开启；
//   - `--` 之后一律是位置参数，不再扫描（否则 `eg apply -- --json` 会被误判）；
//   - 同时认 `--json` 与 `-json` 两种前缀（标准库 flag 两者等价）。
func argvWantsJSON(args []string) bool {
	want := false
	for _, a := range args {
		if a == "--" {
			break
		}
		name, val, hasVal := strings.Cut(a, "=")
		if name != "--json" && name != "-json" {
			continue
		}
		if !hasVal {
			want = true
			continue
		}
		// 取值形态与标准库 flag 的 bool 解析口径一致（大小写不敏感）；
		// 不可解析时保守视为「未表达开启」，把该 argv 的裁决留给真正的 FlagSet。
		switch strings.ToLower(strings.TrimSpace(val)) {
		case "1", "t", "true":
			want = true
		case "0", "f", "false":
			want = false
		}
	}
	return want
}

type globalFlags struct {
	json    bool
	vault   string
	help    bool
	version bool
	// userRequest 对应 `--user-request`：M3 用户显式路径（P-U）的命令行佐证。
	// 做成**全局** flag 而不是 `eg apply` 私有 flag，是因为 P-U 的判定属全局口径，
	// 后续 T-…-039/040/041/045 的命令都要读同一个信号，不能各命令各定义一个。
	userRequest bool
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
		case a == "--user-request":
			g.userRequest = true
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
	// I-…-017：子命令位的 `--json`（README 与全部 <cmd> --help 用法行的写法）必须与全局位等价，
	// 包括**解析失败**的那些提前返回 —— 那时 FlagSet 还没跑出 *jsonFlag，只能靠 argv 扫描。
	jsonWanted := g.json || argvWantsJSON(args)
	sub := ""
	if len(cmd.Subs) > 0 && len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		for _, s := range cmd.Subs {
			if args[0] == s {
				sub, args = s, args[1:]
				break
			}
		}
		if sub == "" && cmd.SubRequired {
			return jsonWanted, nil, &UsageError{Msg: fmt.Sprintf(
				"eg %s 的子命令必须是 %s，实际 %q", cmd.Name, strings.Join(cmd.Subs, " | "), args[0])}
		}
	}
	if cmd.SubRequired && sub == "" && len(args) == 0 {
		return jsonWanted, nil, &UsageError{Msg: fmt.Sprintf(
			"eg %s 必须带子命令：%s", cmd.Name, strings.Join(cmd.Subs, " | "))}
	}

	fs := flag.NewFlagSet("eg "+cmd.Name, flag.ContinueOnError)
	var buf bytes.Buffer
	fs.SetOutput(&buf)
	jsonFlag := fs.Bool("json", g.json, "输出结构化信封")
	vaultFlag := fs.String("vault", g.vault, "vault 根")
	helpFlag := fs.Bool("help", false, "打印用法")
	fs.BoolVar(helpFlag, "h", false, "打印用法")
	// 默认值取子命令**之前**已解析到的全局值，使 `eg --user-request apply …` 与
	// `eg apply … --user-request` 两种写法等价（与 --json / --vault 同一套口径）。
	userRequestFlag := fs.Bool("user-request", g.userRequest, "本次调用由用户显式发起（M3 用户显式路径 P-U 的命令行佐证）")
	if cmd.Flags != nil {
		cmd.Flags(fs)
	}
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		msg := strings.TrimSpace(buf.String())
		if errors.Is(err, flag.ErrHelp) {
			*helpFlag = true
		} else {
			return jsonWanted, nil, &UsageError{
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

		UserRequest: *userRequestFlag,
		Out:         out,
		Err:         errw,
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
	// I-…-018：登记「本次是哪条命令表面」，供 writeLastReport 如实写进最近一次报告记录。
	// 单点赋值（只在这里、只在 Handler 之前），因此 `eg report --last` 的回放文案永远
	// 说得出记录来自 apply 还是 capture —— 报告只陈述既成事实，不许张冠李戴。
	r.cmdSurface = commandSurface(cmd, sub)
	res, err := cmd.Handler(inv)
	return inv.JSON, res, err
}

// commandSurface 把「命令 + 子动词」渲染成用户敲的那个表面（`eg capture` / `eg proposal approve`）。
func commandSurface(cmd *Command, sub string) string {
	if cmd == nil {
		return "eg"
	}
	if sub != "" {
		return "eg " + cmd.Name + " " + sub
	}
	return "eg " + cmd.Name
}

// reorderArgs 把位置参数挪到 flag 之后再交给 FlagSet。
//
// 标准库 flag 遇到**首个非 flag 参数即停止解析**，因此 `eg config set k v --vault X`
// 里的 --vault 会被当成位置参数。合同 §1 的用法示例（如 §1.2）把位置参数写在前、
// 全局 flag 写在后，故这里做一次**只重排、不增删**的参数置换：
// 参数个数与取值逐字不变，`--` 之后的一切原样视为位置参数。
func reorderArgs(fs *flag.FlagSet, args []string) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if len(a) > 1 && a[0] == '-' {
			flags = append(flags, a)
			name := strings.TrimLeft(a, "-")
			if strings.Contains(name, "=") {
				continue
			}
			f := fs.Lookup(name)
			if f == nil {
				continue
			}
			if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
				continue
			}
			if i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		positional = append(positional, a)
	}
	return append(flags, positional...)
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
	// 退出码 5 归类（写前强校验失败 E15 / 锁不可用 E16）在进入唯一翻译点前完成一次：
	// classifyExit5 幂等，只提升错误类型、不改诊断载荷，因此人类可读与 --json 两套渲染同源。
	err = classifyExit5(err)
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
//
// # 登记面唯一（I-…-015 判据 E）
//
// 原实现只在「顶层错误文案包含某条既有诊断的 message」时才跳过追加，于是同一次失败常常
// 登记**两条**：一条带编号（如 `rel add` 悬空对端的 `E2`），一条空码孪生 —— 前者可路由、
// 后者不可，调用方还得自己去重。现在的判据更直接：**错误只要已经自带任何 error 级诊断，
// 就不再补顶层条目**。带类型错误自己给出的诊断更精确（带 `op_index` / `path` / `target`），
// 顶层那条只是把 `err.Error()` 复述一遍，没有新事实。
//
// 只有「一条 error 级诊断都没带」的错误才补一条，且 `code` 由 CodeForError 按**已经决定
// 退出码的那套类型化分类**给出（见 codes.go）——不再留空。空码只对未编号 warning 开放
// （CLI 合同 §5），error 级空码会让接入方无法按码分支，只能对随时可改的中文 message 做子串匹配。
func mergeErrorIntoResult(res *Result, err error, cmd *Command) *Result {
	if res == nil {
		res = &Result{}
	}
	var d diagnoser
	if errors.As(err, &d) {
		res.Errors = append(res.Errors, d.Diagnostics()...)
	}
	for _, existing := range res.Errors {
		if existing.Level == LevelError {
			return res
		}
	}
	// path 取「命令面」；解析层失败（未知命令 / 未知全局 flag / 缺子命令，见 usageFailure）
	// 时命令还没被认出来，cmd 为 nil，此时退回顶层 `eg` —— 不允许因此 panic 掉整个信封。
	path := "eg"
	if cmd != nil {
		path = "eg " + cmd.Display
	}
	res.Errors = append(res.Errors, Diagnostic{
		Code:    CodeForError(err),
		Level:   LevelError,
		Path:    path,
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
