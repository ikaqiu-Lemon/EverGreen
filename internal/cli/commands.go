package cli

// 九命令注册表：参数逐项对齐
// `teamwork/projects/evergreen/s1_main_flow/docs/specs/2026-09-01-eg-cli-contract.md` §1.1–§1.9。
//
// 本文件只声明参数与**参数形态校验**（缺必填 / 互斥 / 多余位置参数），
// 不含任何业务逻辑；业务由后续 task Wire 进来。

import (
	"fmt"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// commands 返回九命令，顺序即 --help 顺序（合同 §1 表格行序）。
func commands() []*Command {
	return []*Command{
		initCommand(),
		configCommand(),
		captureCommand(),
		contextCommand(),
		applyCommand(),
		searchCommand(),
		cardCommand(),
		relCommand(),
		reportCommand(),
		deprecateCommand(),
		restoreCommand(),
		replacedByCommand(),
		// S2 提案子系统（T-…-040）：五个子命令一次注册，approve 本层只注册壳。
		proposalCommand(),
		// M3 逻辑删除（T-…-041）：注册表总量随之 13 → 14。
		deleteCommand(),
		// M3 清空删除标记（T-…-041 收尾）：注册表总量随之 14 → 15。
		undeleteCommand(),
		// M3 reviewed_at（T-…-042）：mark-reviewed 是 reviewed_at 的唯一写入路径，
		// unreviewed 是只读筛选。注册表总量随之 15 → 17。
		markReviewedCommand(),
		unreviewedCommand(),
		// M3 用户显式改核心内容（A-13，T-…-045）：eg edit 是矩阵 #12 P-U ✅ 的唯一命令载体。
		// 注册表总量随之 17 → 18。
		editCommand(),
		// M4 全库对账（T-…-058）：注册表总量随之 18 → 19。
		reconcileCommand(),
		// M4 只读结构体检（T-…-059）：`eg reconcile --dry-run` 的只读真子集
		// （恰 R3 / R4 七个 check）。注册表总量随之 19 → 20（M4 收口值）。
		checkCommand(),
		// M5 派生索引（T-…-065 三子命令 + T-…-066 阶段 B 补 `sync`）：
		// `eg index build|rebuild|status|sync` 一次注册。注册表总量随之 20 → 21 ——
		// 这是 M5 的**过程值**，终值 22 由 T-…-068 的 `eg bench` 补齐（合同 §8.1）。
		// 子命令按 `eg config get|set` 的既有惯例**不单独计数**。
		indexCommand(),
		// M5 性能采样（T-…-068 阶段 B）：`eg bench` 按合同 §7.3 冻结口径采五个数。
		// 注册表总量随之 21 → 22 —— 这是 M5 的**终值**（合同 §8.1 命令数复算：
		// 20 + 1（index）+ 1（bench）= 22）。判据一字未变，仍是「注册表与 --help
		// 命令区逐行相等」，只是行数按实测从 21 变成 22。
		benchCommand(),
	}
}

// deprecateCommand / restoreCommand / replacedByCommand 是 M3 的三条**用户显式**状态命令
// （授权合同 §2 矩阵 #3 / #4：P-A 🔴、P-U ✅）。三者都**不需二次确认**、也不收确认参数：
// 授权合同 §3 X1「需确认 = 否」——只有逻辑删除那一支才要二次确认（T-…-041）。
func deprecateCommand() *Command {
	return &Command{
		Name:    "deprecate",
		Display: "deprecate",
		Summary: "用户显式失效一张卡（status → deprecated；commit verb=process）",
		Owner:   "T-evergreen.s1_main_flow-158614-039",
		Usage: `eg deprecate --target <k-id> --reason <text> [--strict] [--json]

参数：
  --target <k-id>   是；要失效的卡 ID
  --reason <text>   是；失效理由（状态类 op 缺 reason 在 plan 层是 error）
  --strict          否；M6 写前强校验：升级面命中即锁内零写入中止退 5（携 E15）

只覆盖 frontmatter 的 status 单键：不写 deleted_at、不动关系与 sources[]、
不新增任何历史数组（反复失效/恢复只反复覆盖同一个键）。
目标已是 deprecated → W11 幂等 no-op：零写入、不产生空 commit。
退出码：0 | 1 参数非法（零写入） | 2 校验失败（零写入） | 3 部分写入被跳过 | 4 Git 提交失败 |
        5 写前强校验失败（E15）/ run.lock 不可用（E16），两者均零写入
`,
		Flags: func(fs *flagSet) {
			fs.String("target", "", "要失效的卡 ID")
			fs.String("reason", "", "失效理由")
			registerStrictFlag(fs)
		},
		Validate: func(inv *Invocation) error {
			return requireStateTargetAndReason(inv, "deprecate")
		},
	}
}

func restoreCommand() *Command {
	return &Command{
		Name:    "restore",
		Display: "restore",
		Summary: "用户显式恢复一张失效卡（status → active；commit verb=process）",
		Owner:   "T-evergreen.s1_main_flow-158614-039",
		Usage: `eg restore --target <k-id> --reason <text> [--strict] [--json]

参数：
  --target <k-id>   是；要恢复的卡 ID
  --reason <text>   是；恢复理由
  --strict          否；M6 写前强校验：升级面命中即锁内零写入中止退 5（携 E15）

不以「存在有效 support」为前提：系统不拦截、不判定材料是否充分（该提示归 S3）。
目标已是 active → W11 幂等 no-op：零写入、不产生空 commit。
退出码：0 | 1 参数非法（零写入） | 2 校验失败（零写入） | 3 部分写入被跳过 | 4 Git 提交失败 |
        5 写前强校验失败（E15）/ run.lock 不可用（E16），两者均零写入
`,
		Flags: func(fs *flagSet) {
			fs.String("target", "", "要恢复的卡 ID")
			fs.String("reason", "", "恢复理由")
			registerStrictFlag(fs)
		},
		Validate: func(inv *Invocation) error {
			return requireStateTargetAndReason(inv, "restore")
		},
	}
}

func replacedByCommand() *Command {
	return &Command{
		Name:    "replaced-by",
		Display: "replaced-by",
		Summary: "在失效卡上写替代指针 replaced_by（单向；commit verb=process）",
		Owner:   "T-evergreen.s1_main_flow-158614-039",
		Usage: `eg replaced-by --target <k-id> --to <k-id> --reason <text> [--strict] [--json]

参数：
  --target <k-id>   是；被替代的**失效卡**（replaced_by 写在它身上）
  --to <k-id>       是；替代它的新卡
  --reason <text>   是；替代理由（落到 replaced_by.reason）
  --strict          否；M6 写前强校验：升级面命中即锁内零写入中止退 5（携 E15）

单向写入：被指向的新卡文件字节不变（反向查询归 S4）。
E10：target 或 --to 任一已被逻辑删除 → 退 2、零写入。
W12：--to 是 deprecated 且未被删除 → 允许写入 + 进报告提示（退 0）。
退出码：0 | 1 参数非法（零写入） | 2 校验失败（零写入） | 3 部分写入被跳过 | 4 Git 提交失败 |
        5 写前强校验失败（E15）/ run.lock 不可用（E16），两者均零写入
`,
		Flags: func(fs *flagSet) {
			fs.String("target", "", "被替代的失效卡 ID")
			fs.String("to", "", "替代它的新卡 ID")
			fs.String("reason", "", "替代理由")
			registerStrictFlag(fs)
		},
		Validate: func(inv *Invocation) error {
			if err := requireStateTargetAndReason(inv, "replaced-by"); err != nil {
				return err
			}
			if strings.TrimSpace(inv.String("to")) == "" {
				return &UsageError{Msg: "eg replaced-by 缺必填参数 --to <k-id>：替代指针必须点名新卡"}
			}
			return nil
		},
	}
}

func initCommand() *Command {
	return &Command{
		Name:           "init",
		Display:        "init",
		Summary:        "初始化 vault（写 evergreen.yml / SKILL.md / unprocessed.md 等；commit verb=init）",
		Owner:          "T-evergreen.s1_main_flow-158614-008",
		SkipVaultGuard: true,
		Usage: `eg init [--vault <path>] [--domain <domain>]...

参数（合同 §1.1）：
  --vault <path>   否，默认 cwd；要初始化的 vault 根，目录不存在则创建
  --domain <d>     否，可重复；初始领域，首个作为 default_domain

写入 F1 的 [S1] 列；不创建 reviews/（S2）、proposals/（S2）、.index/（S4）。
commit：verb = init。幂等：重复执行不改已有字节，干净工作区下不产生空 commit。
退出码：0 | 1 参数非法 | 4 Git 提交失败 |
        5 写前强校验失败（E15）/ run.lock 不可用（E16），两者均零写入
`,
		Flags: func(fs *flagSet) {
			var domains stringList
			fs.Var(&domains, "domain", "初始领域（可重复）")
		},
		Validate: noPositionalArgs,
	}
}

func configCommand() *Command {
	return &Command{
		Name:           "config",
		Display:        "config get|set",
		Summary:        "读写 evergreen.yml 的 default_domain / domains（set 产生 commit verb=reconcile）",
		Owner:          "T-evergreen.s1_main_flow-158614-008",
		Subs:           []string{"get", "set"},
		SubRequired:    true,
		SkipVaultGuard: true,
		Usage: `eg config get <key>
eg config set <key> <value>

参数（合同 §1.2）：
  <key>     是，取值 default_domain | domains（S1 只此两键，其他键退 1）
  <value>   set 必填；领域名 / 逗号分隔领域列表（set domains a,b 为并集追加，不删除既有）

commit：get 无；set 产生 verb = reconcile（≠ S3 的 eg reconcile 命令，≠ §4.6 报告字段 reconcile）。
get default_domain 未设置时输出空值并退 0（不是错误）。
退出码：0 | 1 | 4 | 5 写前强校验失败（E15）/ run.lock 不可用（E16），两者均零写入（仅 set 会取锁）
`,
		Validate: func(inv *Invocation) error {
			if inv.Sub == "" {
				return &UsageError{Msg: "eg config 必须带子命令：get | set"}
			}
			want := 1
			if inv.Sub == "set" {
				want = 2
			}
			if len(inv.Args) != want {
				return &UsageError{Msg: fmt.Sprintf(
					"eg config %s 需要 %d 个位置参数，实际 %d 个", inv.Sub, want, len(inv.Args))}
			}
			key := inv.Args[0]
			for _, k := range model.ConfigKeys() {
				if key == k {
					return nil
				}
			}
			return &UsageError{Msg: fmt.Sprintf(
				"未知配置键 %q：S1 只支持 %s", key, strings.Join(model.ConfigKeys(), " | "))}
		},
	}
}

func captureCommand() *Command {
	return &Command{
		Name:    "capture",
		Display: "capture",
		Summary: "收录原文并登记收件区（commit verb=capture）",
		Owner:   "T-evergreen.s1_main_flow-158614-009",
		Usage: `eg capture --url <url> --title <title> (--body-stdin | --body-file <path>) --reason <text>
           [--domain <d>] [--tag <t>]... [--captured-at <rfc3339>] [--reprocess]

参数（合同 §1.3）：
  --url <url>            与 --title 至少一个；判重第一键（URL 规范化后精确匹配）
  --title <t>            与 --url 至少一个；判重第二键（标题精确匹配）
  --body-stdin           与 --body-file 二选一必填；正文自 stdin 读入
  --body-file <path>     与 --body-stdin 二选一必填
  --reason <text>        是；收录理由（命中已有原文时追加到理由列表）
  --domain <d>           否；目标领域（白名单用法，不写进产物 frontmatter）
  --tag <t>              否，可重复
  --captured-at <t>      否；带时区 RFC3339，默认取本机时间
  --reprocess            否；已有材料笔记时，仅此 flag 允许重新加工

eg 不抓取网页、不解析 HTML、不调用模型：正文字节由 Agent 清洗后传入。
退出码：0 | 1 参数非法 | 2 正文为空/过短或 --url 与 --title 同时缺失（零写入） |
        3 部分写入被跳过 | 4 Git 提交失败 |
        5 写前强校验失败（E15）/ run.lock 不可用（E16），两者均零写入
`,
		Flags: func(fs *flagSet) {
			fs.String("url", "", "原文 URL（判重第一键）")
			fs.String("title", "", "原文标题（判重第二键）")
			fs.Bool("body-stdin", false, "正文自 stdin 读入")
			fs.String("body-file", "", "正文文件路径")
			fs.String("reason", "", "收录理由")
			fs.String("domain", "", "目标领域")
			var tags stringList
			fs.Var(&tags, "tag", "标签（可重复）")
			fs.String("captured-at", "", "收录时刻（带时区 RFC3339）")
			fs.Bool("reprocess", false, "允许重新加工")
		},
		Validate: func(inv *Invocation) error {
			if err := noPositionalArgs(inv); err != nil {
				return err
			}
			if inv.String("reason") == "" {
				return &UsageError{Msg: "eg capture 缺必填参数 --reason"}
			}
			stdin, file := inv.Set("body-stdin"), inv.String("body-file") != ""
			switch {
			case !stdin && !file:
				return &UsageError{Msg: "eg capture 缺必填参数：--body-stdin 与 --body-file 二选一"}
			case stdin && file:
				return &UsageError{Msg: "eg capture 的 --body-stdin 与 --body-file 互斥，只能给一个"}
			}
			// 合同 §1.3：--url 与 --title 同时缺失属**校验失败**（退 2，零写入），不是参数形态错误。
			if inv.String("url") == "" && inv.String("title") == "" {
				return &ValidationError{
					Msg: "eg capture 的 --url 与 --title 同时缺失：判重无键可用（零写入）",
					Diags: []Diagnostic{{
						Code:    E17,
						Level:   LevelError,
						Path:    "--url|--title",
						OpIndex: NonOpDiagnostic,
						Message: "--url 与 --title 至少给一个（判重第一键 / 第二键）",
					}},
				}
			}
			return nil
		},
	}
}

func contextCommand() *Command {
	return &Command{
		Name:     "context",
		Display:  "context",
		Summary:  "只读输出加工上下文（候选卡 + base 的 id→content_hash）",
		Owner:    "T-evergreen.s1_main_flow-158614-010",
		ReadOnly: true,
		Usage: `eg context (--source <s-id> | --note <n-id>) [--domain <d>] [--json]

参数（合同 §1.4）：
  --source <s-id>   与 --note 二选一必填；加工对象为原文
  --note <n-id>     与 --source 二选一必填；加工对象为材料笔记
  --domain <d>      否；限定领域（白名单用法）

只读：零文件变化、零 commit（EG-VIEW-01）。
退出码：0 | 1 参数非法或未配置 default_domain | 2 对象不存在或 frontmatter 不可解析
`,
		Flags: func(fs *flagSet) {
			fs.String("source", "", "原文 ID")
			fs.String("note", "", "材料笔记 ID")
			fs.String("domain", "", "限定领域")
		},
		Validate: func(inv *Invocation) error {
			if err := noPositionalArgs(inv); err != nil {
				return err
			}
			src, note := inv.String("source") != "", inv.String("note") != ""
			switch {
			case !src && !note:
				return &UsageError{Msg: "eg context 缺必填参数：--source 与 --note 二选一"}
			case src && note:
				return &UsageError{Msg: "eg context 的 --source 与 --note 互斥，只能给一个"}
			}
			return nil
		},
	}
}

func applyCommand() *Command {
	return &Command{
		Name:    "apply",
		Display: "apply",
		Summary: "按 ChangePlan 写入并提交（commit verb 取自 plan.verb）",
		Owner:   "T-evergreen.s1_main_flow-158614-015",
		Usage: `eg apply --plan <file|-> [--dry-run] [--strict] [--json]

参数（合同 §1.5 / §2）：
  --plan <file|->   是；ChangePlan 文件，- 表示 stdin
  --dry-run         否；M1 工程 flag：完整走校验与 op 展开，零写入零 commit，
                    退出码语义不变（校验失败仍退 2，展开成功退 0）
  --strict          否；M6 写前强校验：W1/W2/W3/W4/W6 视作 error，命中即锁内零写入中止退 5（携 E15）

写入遵守 B1 只追加 / B2 保留用户块 / B3 content_hash 校验；
verb 未知值 → warning 并退化为 process（§4.5），不拒绝提交。
退出码：0 | 1 参数非法 | 2 校验失败（零写入） | 3 部分写入被跳过 | 4 Git 提交失败 |
        5 写前强校验失败（E15）/ run.lock 不可用（E16），两者均零写入（--dry-run 只读、不取锁）
`,
		Flags: func(fs *flagSet) {
			fs.String("plan", "", "ChangePlan 文件（- 为 stdin）")
			fs.Bool("dry-run", false, "零写入零 commit 的校验 + 展开预演")
			registerStrictFlag(fs)
		},
		Validate: func(inv *Invocation) error {
			if err := noPositionalArgs(inv); err != nil {
				return err
			}
			if inv.String("plan") == "" {
				return &UsageError{Msg: "eg apply 缺必填参数 --plan <file|->"}
			}
			return nil
		},
	}
}

func reportCommand() *Command {
	return &Command{
		Name:     "report",
		Display:  "report --last",
		Summary:  "只读输出最近一次 apply / capture 的最终报告",
		Owner:    "T-evergreen.s1_main_flow-158614-016",
		ReadOnly: true,
		Usage: `eg report --last [--json]

参数（合同 §1.9）：
  --last   是；输出最近一次 apply / capture 的最终报告（§4.6 的 S1 必填子集）

覆盖范围（I-…-018）：产出 §4.6 报告体的写命令都会刷新这份记录 ——
  apply / capture / delete / undelete / mark-reviewed /
  proposal new|approve|reject / reconcile；
  回放会交代记录来自哪条命令。rel add / edit / deprecate / restore /
  replaced-by / index build|rebuild|sync 不产出报告体，不刷新记录。

只读：零文件变化、零 commit。
退出码：0 | 1 无历史报告或参数非法
`,
		Flags: func(fs *flagSet) {
			fs.Bool("last", false, "最近一次报告")
		},
		Validate: func(inv *Invocation) error {
			if err := noPositionalArgs(inv); err != nil {
				return err
			}
			if !inv.Set("last") {
				return &UsageError{Msg: "eg report 缺必填参数 --last（S1 只支持 --last）"}
			}
			return nil
		},
	}
}

// noPositionalArgs 拒绝多余位置参数（参数非法 → 退 1，零写入）。
func noPositionalArgs(inv *Invocation) error {
	if len(inv.Args) > 0 {
		return &UsageError{Msg: fmt.Sprintf(
			"eg %s 不接受位置参数，实际收到 %v", inv.Cmd.Display, inv.Args)}
	}
	return nil
}
