package cli

// `eg opinion` 的命令外壳与四条子命令**骨架**（读路径检索拆分设计 §5.4；T-…-006 批次 B1a）。
//
// # 本批只做什么（B1a）
//
// 一次性注册 opinion 顶层命令，子命令集合与顺序**恰**为 search|show|validate|reject，
// 顶层名册 22 → 23。四条子命令本批**全部是明确的未实现骨架**：合法形态调用一律返回框架的
// NotWiredError（退 1、零文件变化、零 commit）。命令在 root.go 的 wireImplemented 里挂上
// 本文件的**壳处理器 runOpinion**（因此满足「注册面 == 挂载面」的静态追溯判据），而该壳对
// 任一子命令都只返回 NotWiredError —— 用「命令已注册、业务实现尚未挂载」这一**既有**骨架
// 语义表达「本批只落骨架」，不新造错误类型、不提前实现任何子命令行为。
//
// # 阶段边界（本批不做，后续批次做）
//
//   - opinion search 的只读检索实现（严格等于 query.Search(Kind=opinion)、复用唯一
//     Filter→dropDeleted→SortEntries→ApplyPage 链、validation 标记与关系摘要）→ 后续批次。
//   - opinion show 的五分区 + 支持/限制/反对三组正反向关系视图 → 后续批次 / T-006-D。
//   - validate / reject 的验证生命周期状态机、reopen 与 --reason / --user-request 等
//     写参数的完整合同（§6.2/§6.3）→ T-006-D / T-007。**本批不提前造任何写行为。**
//
// # 参数形态（本批只钉位置参数，不注册任何命令私有 flag）
//
//   - search 恰 1 个位置参数 <q>；
//   - show / validate / reject 各恰 1 个位置参数 <o-id>，且必须经 model.OpinionID.Valid
//     （前缀 o-，防止把 k-/n-/s- 串当成观点 ID）。
//
// 缺 / 未知子命令、位置参数个数不符、ID 形态非法 → 一律 UsageError（退 1、零写入）。
// 不暴露 --kind：opinion 检索面天然只搜观点，不需要（也不得）再给 kind 开关。

import (
	"fmt"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// 四个子命令名（**恰四个**，顺序即 --help 顺序：设计 §5.4 逐字 search|show|validate|reject）。
// 后续批次接管时只允许**填实现**，不得增删或重排这四个子命令。
const (
	SubOpinionSearch   = "search"
	SubOpinionShow     = "show"
	SubOpinionValidate = "validate"
	SubOpinionReject   = "reject"
)

// OpinionSubcommands 返回 `eg opinion` 的子命令集合（顺序即 --help 顺序）。
func OpinionSubcommands() []string {
	return []string{SubOpinionSearch, SubOpinionShow, SubOpinionValidate, SubOpinionReject}
}

func opinionCommand() *Command {
	return &Command{
		Name:        "opinion",
		Display:     "opinion search|show|validate|reject",
		Summary:     "观点子系统：检索 / 查看 / 采纳 / 驳回（本批仅命令骨架，实现随后续批次落地）",
		Owner:       "T-evergreen.knowledge_opinion_split-158614-006",
		Subs:        OpinionSubcommands(),
		SubRequired: true,
		Usage: `eg opinion search <q> [--json]
eg opinion show <o-id> [--json]
eg opinion validate <o-id> [--json]
eg opinion reject <o-id> [--json]

参数（位置参数）：
  <q>       search 恰 1 个；检索词
  <o-id>    show / validate / reject 各恰 1 个；观点 ID（前缀 o-，形态经 model.OpinionID 校验）

本批为**命令骨架**：四条子命令均未实现，合法形态调用一律退 1（业务实现尚未挂载）、
零文件变化、零 commit；缺 / 未知子命令、位置参数个数不符、ID 形态非法同样退 1、零写入。
退出码：0 | 1 参数非法或实现未挂载（均零写入）
`,
		Validate: validateOpinionArgs,
	}
}

// validateOpinionArgs 只做**参数形态**校验（本批不接触任何业务）：位置参数个数与 <o-id> 形态。
//
// search 恰 1 个 <q>（任意字符串，不再解析）；show / validate / reject 恰 1 个 <o-id>，
// 且必须经 model.OpinionID.Valid —— 把「拼错 ID / 传了 k-/n-/s- 串」判成用法错（退 1、零写入），
// 而不是留到未挂载的实现里去暴露。缺 / 未知子命令由 dispatch 的 SubRequired 分支先行拦下，
// 这里的兜底 default 只覆盖「子命令为空」这一残余路径，措辞与 dispatch 一致。
func validateOpinionArgs(inv *Invocation) error {
	switch inv.Sub {
	case SubOpinionSearch:
		if len(inv.Args) != 1 {
			return &UsageError{Msg: fmt.Sprintf(
				"eg opinion search 需要恰 1 个位置参数 <q>，实际 %d 个", len(inv.Args))}
		}
		return nil
	case SubOpinionShow, SubOpinionValidate, SubOpinionReject:
		if len(inv.Args) != 1 {
			return &UsageError{Msg: fmt.Sprintf(
				"eg opinion %s 需要恰 1 个位置参数 <o-id>，实际 %d 个", inv.Sub, len(inv.Args))}
		}
		if !model.OpinionID(inv.Args[0]).Valid() {
			return &UsageError{Msg: fmt.Sprintf(
				"观点 ID %q 形态非法：必须以 o- 为前缀（避免把 k-/n-/s- 串当成观点 ID）", inv.Args[0])}
		}
		return nil
	}
	return &UsageError{Msg: fmt.Sprintf("eg opinion 必须带子命令：%s",
		strings.Join(OpinionSubcommands(), " | "))}
}

// runOpinion 是 `eg opinion` 的**壳处理器**（B1a）：四条子命令均为未实现骨架。
//
// 命令已在 wireImplemented 里挂上本壳（使「注册面 == 挂载面」成立），但任一子命令的业务
// 实现都尚未落地，因此对每个合法子命令一律返回 NotWiredError —— 与框架「命令已注册、
// Handler 未挂载」时 dispatch 自发的错误**逐字同源**（退 1、零文件变化、零 commit）。
// 后续批次接管时按 inv.Sub 分派到各自实现，本壳的「未实现即退 1、零写入」边界不放宽。
func (r *Root) runOpinion(inv *Invocation) (*Result, error) {
	return nil, &NotWiredError{Command: inv.Cmd.Display, Owner: inv.Cmd.Owner}
}
