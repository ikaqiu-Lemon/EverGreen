package cli

// `eg edit --target <id> --section <分区> --content <text|file> --user-request` 的写路径
// （授权合同 `2026-10-10-m3-user-authorization-contract.md` §1 两条路径 + N-1 反伪造、
// §2 写权限矩阵 **#12 / #15**、§3 **X1**「需确认 = 否」、§5 授权与 B1–B4、§6 **U-03**、
// §7.2 判定行；§9 临时裁决 **A-13**；T-evergreen.s1_main_flow-158614-045）。
//
// # 本命令的定位
//
// 它是 M3 完成判据「**用户显式命令可改核心内容**」的**唯一命令载体**（A-13），
// 与「Agent 自动路径改不了」（矩阵 #12 的 P-A 🔴 子情形，T-…-038 的
// TestE6_AgentAppendCoreKnowledgeRejected）构成一对可反证的正负用例。
//
// # 编排：合成内存 plan → runPlan（与 rel add / rel remove / deprecate 同一条链路）
//
//	planBase（store.ContentHash 唯一口径）→ 单条 edit_section op（initiator: user）
//	→ runPlan：plan.Validate（W7 分级 + editSectionGate 查矩阵 #12/#13/#14/#15 → E6）
//	→ plan.Execute（executor → store.ApplyReplaceSection 唯一写口）
//	→ report（applied / skipped[]）→ internal/git 一次 `process` commit
//
// 本文件**不 import internal/store**、不拼字节、不自己改 frontmatter：
// 「禁止绕过 ChangePlan 直接调 store」（U-07）在这里是机器可查的事实。
//
// # 为什么这条命令**必须**真的带 `--user-request`（与 deprecate / mark-reviewed 不同档）
//
// deprecate / restore / replaced-by / undelete / mark-reviewed 属「用户显式且低风险」
// 一档（只改一格状态或只读信号，可原样撤回），因此在进程边界置 UserRequest 即可。
// `eg edit` 改的是**核心知识内容本身**——它是 B1「只追加」在 S2 开的唯一替换口子，
// 一次执行会让旧正文只存在于 Git 历史里。故本命令按**高风险执行闸门**归档：
// `initiator: user` 由命令给出，但**命令行佐证必须由用户真的敲出来**（N-1 的精神：
// 授权不能自证）。缺 `--user-request` → 落 P-A → 矩阵 #12 的 🔴 子情形 → **E6 退 2、零写入**。
//
// 它**不需二次确认**（§3 X1「需确认 = 否」），因此**不启用退出码 6**：
// 6 的白名单恰 `{proposal approve, delete}` 两条，本命令不在其中，也不收确认参数。
//
// # 三个正交维度一格不碰
//
// 只替换被点名分区的正文字节：`status`（active/deprecated）、删除维度（deleted_at /
// deleted_reason）、过目维度（reviewed_at）一律不写。`reviewed_at` 只由
// `eg mark-reviewed` 写（A-16 / 矩阵 #7）——编辑正文不等于用户过目了它。
//
// # 阶段边界（本命令一律不做）
//
//   - 不实现 `eg open` / `eg tag` / `eg recap` / `eg review save` / `eg recent`
//     （A-18 归属未知，待 owner 确认，本命令不代为裁决）；
//   - 不实现「用户直接编辑 Markdown 后对账补齐 reviewed_at」（S3/M4，A-16）；
//   - 不做块级三方合并 / 强原子：冲突仍是**整文件跳过**（B3，退 3，R-13 已知风险）；
//   - 不挂起外部编辑器、不做交互式修改；正文字节一律由 --content 逐字给出。

import (
	"fmt"
	"os"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/plan"
)

// EditNoStateChangeNotice 陈述「系统没做什么」：编辑正文不牵连三个正交维度。
// 措辞固定，供用例与 e2e 逐字断言。
const EditNoStateChangeNotice = "本次编辑只替换被点名分区的正文：status、deleted_at / deleted_reason " +
	"与 reviewed_at 一律未变（三个维度彼此正交）"

// editCommand 注册 `eg edit`（矩阵 #12 的 P-U ✅ 载体，A-13；不收确认参数，不产生退出码 6）。
func editCommand() *Command {
	return &Command{
		Name:    "edit",
		Display: "edit",
		Summary: "用户显式修改知识内容等核心分区（整段替换；commit verb=process）",
		Owner:   "T-evergreen.s1_main_flow-158614-045",
		Usage: `eg edit --target <k-id> --section <分区> --content <text|file> --user-request [--strict] [--json]

参数：
  --target <k-id>          是；要修改的知识卡 ID
  --section <分区>          是；被替换的分区，取值恰为可编辑白名单（见下）
  --content <text|file>    是；新的分区正文。值是既存文件路径时按文件字节读入，
                           否则按字面文本处理（两种口径都会在报告里如实说明）
  --user-request           是；用户显式发起的命令行佐证。缺它 → 退 2、零写入
                           （plan 内的 initiator: user 不能自证授权）
  --strict                 否；M6 写前强校验：升级面命中即锁内零写入中止退 5（携 E15）

可编辑分区（写权限矩阵 P-U 为 ✅ 的分区）：知识内容 | 解释与依据 | 条件与边界。
--section 用户补充 → 必退 2（E6）：CLI 任何路径、任何时候都不写它，与是否带
--user-request 无关。--section 理解自检 → 退 2：历史记录块只追加、永不改写。
整段替换：被点名分区的正文按 --content 逐字生效；其它分区、frontmatter 的
status / deleted_at / deleted_reason / reviewed_at 与未知字段逐字不动。
写前逐文件比对 content_hash：不一致即跳过该文件并退 3（授权不放宽 B3）。
不需二次确认，也不产生退出码 6。
退出码：0 | 1 参数非法（零写入） | 2 校验失败（零写入） | 3 写入被跳过 | 4 Git 提交失败
`,
		Flags: func(fs *flagSet) {
			fs.String("target", "", "要修改的知识卡 ID")
			fs.String("section", "", "被替换的分区名")
			fs.String("content", "", "新的分区正文（字面文本或文件路径）")
			registerStrictFlag(fs)
		},
		Validate: func(inv *Invocation) error {
			return requireEditArgs(inv)
		},
	}
}

// requireEditArgs 做**参数形态**校验（缺必填一律退 1、零写入、零 commit）。
//
// 分级刻意与「授权」分开：**缺 `--user-request` 不在这里拦**——它是授权判定的输入，
// 由 plan 层的矩阵 #12 判 E6 退 2（判据 8 的负半边要求的正是「退 2 + 零写入」，
// 而不是「用法错误」）。同理 `--section 用户补充` 也不在这里拦：它必须退 2 + E6。
func requireEditArgs(inv *Invocation) error {
	if err := noPositionalArgs(inv); err != nil {
		return err
	}
	if strings.TrimSpace(inv.String("target")) == "" {
		return &UsageError{Msg: "eg edit 缺必填参数 --target <k-id>：修改内容必须点名目标卡"}
	}
	if strings.TrimSpace(inv.String("section")) == "" {
		return &UsageError{Msg: fmt.Sprintf(
			"eg edit 缺必填参数 --section <分区>：可编辑分区恰为 %v",
			plan.EditableSections())}
	}
	if !inv.Set("content") {
		return &UsageError{Msg: "eg edit 缺必填参数 --content <text|file>：" +
			"新正文必须逐字给出（本工具不生成、不补写用户内容）"}
	}
	return nil
}

// runEdit 实现 eg edit：取正文字节 → 合成单条 op 的内存 plan → runPlan。
func (r *Root) runEdit(inv *Invocation) (*Result, error) {
	target := strings.TrimSpace(inv.String("target"))
	section := strings.TrimSpace(inv.String("section"))

	content, origin, err := editContent(inv.String("content"))
	if err != nil {
		return nil, err
	}

	p, perr := r.buildEditPlan(inv, target, section, content)
	if perr != nil {
		return nil, perr
	}

	res, runErr := runPlan(r, inv, p)
	if res != nil {
		res.Summary = append([]string{
			fmt.Sprintf("edit：目标 %s 的分区「%s」整段替换（%s；单条 %s op，"+
				"initiator=user + 命令行 --user-request=%t，走 ChangePlan → plan → store → 一次 commit）",
				target, section, origin, plan.OpEditSection, inv.UserRequest),
			EditNoStateChangeNotice,
		}, res.Summary...)
	}
	return res, runErr
}

// buildEditPlan 组装那份**内存** ChangePlan：单条 `edit_section` op。
//
// `initiator: user` 由本命令给出，但**不在这里把 UserRequest 置真**：
// 与 deprecate / mark-reviewed 那一档不同，本命令属高风险执行闸门（改核心内容），
// 命令行佐证必须由用户真的敲出来（见文件头分级说明）。因此 `inv.UserRequest`
// 原样来自 `--user-request`，缺它就落 P-A 并被矩阵 #12 拦下退 2。
//
// base 由 CLI 自己算（`store.ContentHash` 唯一口径，见 apply.go 的 planBase）：
// B3 不因此放宽——写前重算不一致仍然跳过该文件并进 `skipped[]`（退 3）。
func (r *Root) buildEditPlan(inv *Invocation, target, section string, content []byte) (
	*plan.ChangePlan, error) {
	base, paths, err := planBase(inv.VaultRoot, []string{target})
	if err != nil {
		return nil, err
	}
	// base 是锁外采样，临界区在 S2 之后按同一 ID 重采（I-…-022）；B3 依旧逐字比对。
	inv.selfComputedBase([]string{target})
	domain, derr := stateOpDomain(inv, paths[target])
	if derr != nil {
		return nil, derr
	}
	op := &plan.Op{
		Index:          0,
		Name:           plan.OpEditSection,
		Target:         target,
		Section:        section,
		Content:        content,
		ContentGiven:   true,
		Initiator:      plan.InitiatorUser,
		InitiatorGiven: true,
	}
	// verb 取 `process`：S1 冻结的提交动词里没有「编辑」这一个，与状态类命令同口径
	// （借既有已知动词躲开 W5 一律不成立；KnownVerbs 不因本命令增减）。
	return &plan.ChangePlan{
		Version:        plan.PlanVersion,
		VersionRaw:     plan.PlanVersion,
		Verb:           string(model.VerbProcess),
		VerbGiven:      true,
		Domain:         domain,
		DomainGiven:    true,
		Reason:         editReason(section),
		RequirementIDs: []string{},
		Base:           base,
		Ops:            []*plan.Op{op},
		Extra:          map[string]interface{}{},
	}, nil
}

// editReason 是 commit 与报告里的理由：只陈述事实（用户显式发起 + 改了哪个分区），
// 不做评价、不编造动机。
func editReason(section string) string {
	return fmt.Sprintf("用户显式修改分区「%s」（eg edit，授权合同 A-13 的唯一命令载体）", section)
}

// editContent 解析 `--content <text|file>` 的两种口径，并把载荷补成行结束形态。
//
// 口径判定**确定且可复述**（不猜、不静默）：给定值是**既存的普通文件**时按文件字节读入，
// 否则按字面文本处理；两种情形各返回一句人类可读的来源说明，进报告与 summary。
//
// 末尾换行：字节级区间替换要求载荷以 `\n` 结束（mdfile 绝不替调用方补字节）。
// 这一个字节由**命令层**补——`--content` 是命令行文本，行结束符属命令层的行文约定，
// 不是用户正文的一部分；用户正文本身一个字节都不改动（含内部空行与缩进）。
func editContent(value string) ([]byte, string, error) {
	raw := []byte(value)
	origin := "content 取字面文本"
	if st, err := os.Stat(value); err == nil && st.Mode().IsRegular() {
		body, rerr := os.ReadFile(value)
		if rerr != nil {
			return nil, "", &UsageError{Msg: fmt.Sprintf("--content 指向的文件不可读：%v", rerr)}
		}
		raw, origin = body, fmt.Sprintf("content 取文件 %s 的字节", value)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, "", &UsageError{Msg: "eg edit 的 --content 为空：新正文必须逐字给出" +
			"（本工具不生成、不补写用户内容）"}
	}
	if raw[len(raw)-1] != '\n' {
		raw = append(raw, '\n')
	}
	return raw, origin, nil
}
