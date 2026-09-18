package cli

// `eg opinion` 的命令外壳、search / show 只读检索实现与 validate/reject **骨架合同**
// （读路径检索拆分设计 §5.4；T-…-006 批次 B1a 起，D 批补齐 validate/reject 用法 / 退出码 / 授权合同）。
//
// # 本批做什么（D 批 · 骨架合同）
//
// search / show 已接通只读检索 / 单条视图（前序批次）。本批**只补 validate/reject 的合同**、
// **不接状态机**：给它们落定用法面（必带非空 --reason + --user-request）、退出码面
// （缺/空 reason 或读 flag → 退 1；缺 --user-request → 退 2、E19；授权齐备 → 仍 NotWired 退 1）
// 与授权面（命令行 --user-request 显式佐证，N-1 反伪造）。观点验证生命周期状态机与任何写入归
// T-007，本骨架一格不碰 store / plan / txn（零文件变化、零 commit）。子命令集合与顺序**恰**为
// search|show|validate|reject，不增删。T-007 批次 A2 起：注册 `--reopen` 参数面与分域合同
// （bool、默认 false，**仅 validate 接受**：把验证态复议回 pending），但仍不接状态机 / 写入。
//
// # 参数面（父命令注册 8 个读 flag + 2 个写路径 flag --reason / --reopen，恰不含 --kind）
//
// 读 flag：domain / 可重复 tag / since / until / include-deleted / include-deprecated / limit /
// offset —— 与 `eg search` / `eg card show` 同名同义，供 search / show 复用同一套口径。
// 写路径 flag：--reason —— 观点验证 / 驳回的理由，**只允许** validate/reject 使用；
// --reopen —— 观点复议开关（bool、默认 false），**只允许** validate 使用（rejected/validated →
// pending 复议边；观点 schema v2 设计 §6.1/§6.2）。**刻意不注册 --kind**：opinion 检索面天然只搜
// 观点（runOpinionSearch 把 Kind 固定成 opinion），再给 kind 开关即多余且可诱导误用；
// `eg opinion search --kind …` 因此被参数解析当场判成「未定义 flag」→ 退 1、零写入。
//
// 这些 flag 都注册在父命令上（FlagSet 分不清子命令），每条子命令都会**解析**到它们；各子命令按
// 分域显式拒绝不属于自己的 flag（见 rejectOpinionFlags）：search 拒 include-deprecated + reason +
// reopen、show 拒检索过滤 flag + reason + reopen、reject 拒全部读 flag + reopen 但**必带**非空
// --reason、validate 拒全部读 flag、**接受** --reopen 但仍**必带**非空 --reason，
// 绝不静默接受 —— 「参数写了却不生效」比报错更坏。
//
// # 专用 DTO
//
// opinion search 的命中沿用 `eg search` hit 的既有字段，**追加** validation 与 relation_summary
// （relation_summary 的 JSON 键固定 supports / limits / opposing）。普通 `eg search` 的 JSON
// 合同（SearchHitKeys / SearchDataKeys）一字不改：观点专属事实一个键都不进 eg search 输出。
//
// # 只读零副作用 / 写路径骨架零副作用
//
// search / show 没有任何 store / plan / git 写口调用：零文件变化、零 commit，退出码只可能 0 / 1。
// validate / reject 骨架同样不接任何写口：授权失败退 2、授权齐备退 1（NotWired），两者都零文件
// 变化、零 commit —— 真正的写行为由 T-007 落地。
//
// # 阶段边界（本批不做，后续批次做）
//
//   - validate / reject 的验证生命周期状态机与写参数合同、--reopen 复议**写行为** → T-007 后续批次。
//     本批（A2）只补 --reopen 的参数面与分域合同，不造任何写行为。

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
)

// 四个子命令名（**恰四个**，顺序即 --help 顺序：设计 §5.4 逐字 search|show|validate|reject）。
// 后续批次接管时只允许**填实现**，不得增删或重排这四个子命令。
const (
	SubOpinionSearch   = "search"
	SubOpinionShow     = "show"
	SubOpinionValidate = "validate"
	SubOpinionReject   = "reject"
)

// OpinionReasonFlag 是 validate/reject 的写路径理由 flag 名（注册点与分域拒绝逐字共用同一字面量）。
const OpinionReasonFlag = "reason"

// OpinionReopenFlag 是 validate 的观点复议 flag 名（bool，默认 false；注册点与分域拒绝逐字共用同一字面量）。
// 语义（观点 schema v2 设计 §6.1/§6.2）：把观点验证态**复议回 pending**（rejected/validated → pending，
// 出现新反例时降级），复用 `eg opinion validate --reopen` 而不新增命令。**只作用于 validate**：
// reject / search / show 显式带它一律退 1（逐字点名 --reopen）。本批只补参数面与分域合同、不接状态机。
const OpinionReopenFlag = "reopen"

// opinionReopenValidateOnlyHint 是 reject / search / show 显式带 --reopen 时用法错的逐字理由
// （分域拒绝可判定：措辞点名该 flag 只作用于 validate）。
const opinionReopenValidateOnlyHint = "该参数只作用于 opinion validate（观点复议：把验证态复议回 pending），驳回 / 检索 / 查看路径不解读它"

// OpinionSubcommands 返回 `eg opinion` 的子命令集合（顺序即 --help 顺序）。
func OpinionSubcommands() []string {
	return []string{SubOpinionSearch, SubOpinionShow, SubOpinionValidate, SubOpinionReject}
}

func opinionCommand() *Command {
	return &Command{
		Name:        "opinion",
		Display:     "opinion search|show|validate|reject",
		Summary:     "观点子系统：检索 / 查看 / 采纳 / 驳回（search + show 已接通只读；validate/reject 骨架合同就绪，状态机随 T-007 落地）",
		Owner:       "T-evergreen.knowledge_opinion_split-158614-006",
		Subs:        OpinionSubcommands(),
		SubRequired: true,
		// 刻意**不设** ReadOnly：opinion 含 validate / reject 写子命令（骨架阶段虽不写盘，
		// 但命令语义属写路径），父命令一旦标只读，框架的零副作用断言就会与那两条写子命令冲突。
		// search / show 的只读性由各自 run 函数「不碰任何写口」保证，不靠命令级 ReadOnly 标记。
		Usage: `eg opinion search <q> [--domain <d>] [--tag <t>]... [--since <YYYY-MM-DD>] [--until <YYYY-MM-DD>] [--include-deleted] [--limit <n>] [--offset <n>] [--json]
eg opinion show <o-id> [--include-deprecated] [--limit <n>] [--offset <n>] [--json]
eg opinion validate <o-id> --reason <text> --user-request [--reopen] [--json]
eg opinion reject <o-id> --reason <text> --user-request [--json]

参数（位置参数）：
  <q>       search 恰 1 个；检索词（空串 / 只含空白 → 退 1）
  <o-id>    show / validate / reject 各恰 1 个；观点 ID（前缀 o-，形态经 model.OpinionID 校验）

search 的检索 / 分页参数（与 eg search 同名同义；**刻意不含 --kind**：opinion 检索面只搜观点）：
  --domain <d>           否，默认全库；值不在 evergreen.yml 的 domains 内 → 退 1（eg 绝不自选领域）
  --tag <t>              否，可重复；多次给出为 AND（观点须同时含全部标签），逐字相等比较
  --since <YYYY-MM-DD>   否；updated_at 的日期部分 ≥ 该值（闭区间；须是日历真实存在的日期）
  --until <YYYY-MM-DD>   否；updated_at 的日期部分 ≤ 该值（闭区间；须是日历真实存在的日期）
  --include-deleted      否，默认 false；显式把已删除观点带回结果并标 [已删除]
  --limit <n>            否，默认 50；最多返回条数，0 = 不限量；截断产恰一条 W25（total 仍为截断前总数）
  --offset <n>           否，默认 0；跳过的条数；超出总数返回空结果且仍退 0；两者为负 / 非整 → 退 1

show 的查看参数（只读单条观点视图；只吃 --include-deprecated 与分页，绝不吃检索过滤 flag）：
  --include-deprecated   否，默认 false；展示对端 deprecated 的关系条目（默认隐藏并计 Q4；不影响已删除维度）
  --limit <n> / --offset <n>  否；一个全局 limit/offset 跨 supports/limits/opposing 三组正反共六段，截断产恰一条 W25

validate / reject 的写路径参数（骨架合同：状态机随 T-007 落地，本批不写盘、不接 store/plan/txn）：
  --reason <text>        是；采纳 / 驳回的理由（缺 / 空串 / 纯空白 → 退 1；search / show 显式带它也 → 退 1）
  --user-request         是（全局 flag）；本次调用由用户显式发起的命令行佐证（观点验证属写路径，
                         文件内容不能自证，N-1）。缺它 → 退 2、零写入，data.errors[] 恰一条 E19（path=--user-request）
  --reopen               否，默认 false，**仅 validate**；把观点验证态复议回 pending（rejected/validated
                         → pending，出现新反例时降级）；reject / search / show 显式带它 → 退 1（逐字点名 --reopen）

search 已接通：只读检索观点（domains/<d>/opinions/**.md），validation 三态（pending /
validated / rejected）全部召回、绝不隐式过滤；每条命中带 validation 与 supports/limits/opposing
关系计数；失效观点标 [失效]、已删除观点经 --include-deleted 带回并标 [已删除]。
show 已接通：按 <o-id> 全库定位单条观点，显式给出 validation、五分区正文、sources、supports /
limits / opposing 三组各正反两段（空段写“无”）；悬空对端标“目标不存在”并产 Q2；对端 deprecated
默认隐藏并计 Q4，--include-deprecated 才展示；已删除观点仍可显式查看并标 [已删除]。
search / show 都只读：零文件变化、零 commit。validate / reject **仍是未挂载骨架**：判定顺序为
位置参数 → <o-id> 形态 → flag 分域 → 缺/空 reason（以上均退 1）→ 缺 --user-request（退 2、E19）→
授权齐备（非空 reason + --user-request）仍 NotWired 退 1；全程零写入零 commit。
缺 / 未知子命令、位置参数个数不符、ID 形态非法 → 一律退 1、零写入。
退出码：0（零命中 / 单条视图也退 0） | 1 参数非法 / 领域未登记 / 观点不存在 / 缺空 reason / 实现未挂载（均零写入）
        | 2 授权失败（validate/reject 缺 --user-request；零写入零 commit）
`,
		Flags: func(fs *flagSet) {
			// 与 eg search 同名同义的检索面（search.go 的 runSearch 复用同一套口径）。
			fs.String("domain", "", "限定领域（默认全库）")
			var tags stringList
			fs.Var(&tags, "tag", "标签过滤（可重复，AND）")
			fs.String("since", "", "updated_at 日期下界（YYYY-MM-DD，闭区间）")
			fs.String("until", "", "updated_at 日期上界（YYYY-MM-DD，闭区间）")
			fs.Bool(SearchIncludeDeletedFlag, false, "显式把已删除观点带回结果（默认视图不返回）")
			// show 专属：与 eg card show / eg rel 同名同义的对端可见性开关（读路径 flag）。
			// 注册点**逐字**用 "include-deprecated" 字面量，与 rel.go / card.go 的 fs.Bool 注册点
			// 完全一致（M4 §3.4 静态反证 grep 按注册点逐字搜源码，产品侧恰命中三处）。
			fs.Bool("include-deprecated", false, "展示对端 deprecated 的关系条目（默认隐藏；不影响已删除维度）")
			// validate/reject 专属写路径 flag：观点采纳 / 驳回理由。注册在父命令上（FlagSet 分不清
			// 子命令），因此 search / show 也能解析到它 —— 那两条只读子命令显式带 --reason 一律退 1
			// （见 validateOpinionArgs 的 rejectOpinionFlags），绝不静默接受。
			fs.String("reason", "", "验证 / 驳回理由（仅 validate/reject；非空）")
			// validate 专属写路径 flag：观点复议开关（bool，默认 false）。同样注册在父命令上，
			// 因此 reject / search / show 也能解析到它 —— 但它**只作用于 validate**（把验证态复议回
			// pending；观点 schema v2 设计 §6.1/§6.2），其余三条子命令显式带 --reopen 一律退 1、
			// 逐字点名（见 validateOpinionArgs 的 rejectOpinionFlags）。本批只补参数面与分域合同、
			// 不接状态机：validate --reopen 授权齐备仍 NotWired（零写入零 commit）。
			fs.Bool(OpinionReopenFlag, false, "观点复议：把验证态复议回 pending（仅 validate；rejected/validated → pending）")
			// 分页（S4 · T-…-068）：注册点唯一，见 page.go。search 与 show 共用。
			pageFlags(fs)
			// **不注册 --kind**：opinion 检索面天然只搜观点（见文件头「参数面」）。
		},
		Validate: validateOpinionArgs,
	}
}

// opinionSearchFilterFlags 是「只在 search 子命令成立」的检索过滤 flag（次序固定，供拒绝与用例逐格比对）。
//
// 这些 flag 注册在 opinion 父命令上，show / validate / reject 与 search 共享同一个 FlagSet，
// 因此那三条子命令能**解析**到它们；show 视图按 <o-id> 精确定位、不做检索过滤，validate/reject
// 尚未挂载，都必须在 Validate 阶段显式拒绝——否则 `eg opinion show o-… --domain x` 会被静默
// 忽略，「参数写了却不生效」比报错更坏。分页（limit/offset）不在此列：search 与 show 都吃它。
func opinionSearchFilterFlags() []string {
	return []string{"domain", "tag", "since", "until", SearchIncludeDeletedFlag}
}

// opinionPageFlags 是 search 与 show 共用的分页 flag（validate/reject 尚未挂载，显式带即退 1）。
func opinionPageFlags() []string {
	return []string{query.PageLimitFlag, query.PageOffsetFlag}
}

// opinionReadFlags 汇总全部读路径 flag（检索过滤 + show 可见性 + 分页），供 validate/reject 整片拒绝。
func opinionReadFlags() []string {
	out := append([]string{}, opinionSearchFilterFlags()...)
	out = append(out, "include-deprecated")
	return append(out, opinionPageFlags()...)
}

// rejectOpinionFlags 在子命令上显式拒绝不属于其分域的读 flag：退 1、零写入。措辞逐字点名该 flag，
// 使拒绝可判定（反证拒绝确由 flag 触发，而非缺参数）。
func rejectOpinionFlags(inv *Invocation, names []string, hint string) error {
	for _, name := range names {
		if inv.Set(name) {
			return &UsageError{Msg: fmt.Sprintf(
				"eg opinion %s 不接受 --%s：%s", inv.Sub, name, hint)}
		}
	}
	return nil
}

// validateOpinionArgs 做**参数形态**校验：位置参数个数、<o-id> 形态、按子命令精确分域拒绝 flag、
// validate/reject 必带非空 --reason。
//
// search 恰 1 个 <q>，只做检索：显式带 show 专属的 --include-deprecated 或写路径的 --reason → 退 1。
// show / validate / reject 恰 1 个 <o-id>（须经 model.OpinionID.Valid）：
//   - show 只吃 --include-deprecated 与分页，显式带任一检索过滤 flag（domain/tag/since/until/
//     include-deleted）或写路径 --reason → 退 1；
//   - validate / reject 属写路径骨架：显式带任一读路径 flag（检索过滤 / 可见性 / 分页）→ 退 1，
//     且**必带非空 --reason**（缺 / 空串 / 纯空白 → 退 1）。判定顺序逐字锁：位置参数 → <o-id> 形态 →
//     flag 分域 → 缺/空 reason（授权判定在 runOpinionLifecycleSkeleton，晚于这里，见其文件注释）。
//
// 缺 / 未知子命令由 dispatch 的 SubRequired 分支先行拦下，这里的兜底 default 只覆盖「子命令为空」
// 这一残余路径，措辞与 dispatch 一致。
func validateOpinionArgs(inv *Invocation) error {
	switch inv.Sub {
	case SubOpinionSearch:
		if len(inv.Args) != 1 {
			return &UsageError{Msg: fmt.Sprintf(
				"eg opinion search 需要恰 1 个位置参数 <q>，实际 %d 个", len(inv.Args))}
		}
		if err := rejectOpinionFlags(inv, []string{"include-deprecated"},
			"该参数只作用于 opinion show（观点视图对端可见性开关），检索路径不解读它"); err != nil {
			return err
		}
		if err := rejectOpinionFlags(inv, []string{OpinionReopenFlag}, opinionReopenValidateOnlyHint); err != nil {
			return err
		}
		return rejectOpinionFlags(inv, []string{OpinionReasonFlag},
			"该参数只作用于 opinion validate/reject（观点采纳 / 驳回理由），只读检索路径不解读它")
	case SubOpinionShow:
		if len(inv.Args) != 1 {
			return &UsageError{Msg: fmt.Sprintf(
				"eg opinion %s 需要恰 1 个位置参数 <o-id>，实际 %d 个", inv.Sub, len(inv.Args))}
		}
		if !model.OpinionID(inv.Args[0]).Valid() {
			return &UsageError{Msg: fmt.Sprintf(
				"观点 ID %q 形态非法：必须以 o- 为前缀（避免把 k-/n-/s- 串当成观点 ID）", inv.Args[0])}
		}
		if err := rejectOpinionFlags(inv, opinionSearchFilterFlags(),
			"该参数只作用于 opinion search 检索路径，show 视图按 <o-id> 精确定位、不做检索过滤"); err != nil {
			return err
		}
		if err := rejectOpinionFlags(inv, []string{OpinionReopenFlag}, opinionReopenValidateOnlyHint); err != nil {
			return err
		}
		return rejectOpinionFlags(inv, []string{OpinionReasonFlag},
			"该参数只作用于 opinion validate/reject（观点采纳 / 驳回理由），只读查看路径不解读它")
	case SubOpinionValidate, SubOpinionReject:
		if len(inv.Args) != 1 {
			return &UsageError{Msg: fmt.Sprintf(
				"eg opinion %s 需要恰 1 个位置参数 <o-id>，实际 %d 个", inv.Sub, len(inv.Args))}
		}
		if !model.OpinionID(inv.Args[0]).Valid() {
			return &UsageError{Msg: fmt.Sprintf(
				"观点 ID %q 形态非法：必须以 o- 为前缀（避免把 k-/n-/s- 串当成观点 ID）", inv.Args[0])}
		}
		// flag 分域：validate/reject 是写路径，一律不吃只读检索 / 查看 / 分页 flag（**不含 --reason**：
		// 那是本档必填项，紧随其后单独判空）。
		if err := rejectOpinionFlags(inv, opinionReadFlags(),
			"该参数属只读检索 / 查看路径，验证 / 驳回子命令（写路径骨架）不接受任何读 flag"); err != nil {
			return err
		}
		// --reopen 分域：**只 validate 接受**（把验证态复议回 pending），reject 显式带它 → 退 1、
		// 逐字点名 --reopen。此判定**先于**缺 / 空 reason（reject --reopen 哪怕同时缺 reason，也须
		// 点名 --reopen，绝不冒名成缺 reason 用法错）。validate 分域不在此拒绝之列 —— 它接受 --reopen。
		if inv.Sub == SubOpinionReject {
			if err := rejectOpinionFlags(inv, []string{OpinionReopenFlag}, opinionReopenValidateOnlyHint); err != nil {
				return err
			}
		}
		// 缺 / 空 reason **先于**授权判定（授权在 runOpinionLifecycleSkeleton；这里退 1、那里退 2，
		// 两码绝不互相冒名）：空串 / 纯空白同样按缺失处理，写路径不接受空理由。
		if strings.TrimSpace(inv.String(OpinionReasonFlag)) == "" {
			return &UsageError{Msg: fmt.Sprintf(
				"eg opinion %s 需要 --%s <text>：验证 / 驳回须给出非空理由（缺 / 空串 / 纯空白均退 1）",
				inv.Sub, OpinionReasonFlag)}
		}
		return nil
	}
	return &UsageError{Msg: fmt.Sprintf("eg opinion 必须带子命令：%s",
		strings.Join(OpinionSubcommands(), " | "))}
}

// runOpinion 是 `eg opinion` 的分发壳：search / show 走已接通的只读视图，validate / reject 走
// **骨架合同**（授权判定 + 未挂载状态机），本批一格不碰 store / plan / txn。
//
// validate / reject 的授权齐备路径一律返回 NotWiredError —— 与框架「命令已注册、Handler 未挂载」
// 时 dispatch 自发的错误**逐字同源**（退 1、零文件变化、零 commit）。缺 --user-request 则先在
// 授权判定处退 2（E19）。后续批次接管时只需在 runOpinionLifecycleSkeleton 里填状态机实现，
// 本壳的「未挂载即退 1、授权失败退 2、全程零写入」边界不放宽。
func (r *Root) runOpinion(inv *Invocation) (*Result, error) {
	switch inv.Sub {
	case SubOpinionSearch:
		return r.runOpinionSearch(inv)
	case SubOpinionShow:
		return r.runOpinionShow(inv)
	case SubOpinionValidate, SubOpinionReject:
		return r.runOpinionLifecycleSkeleton(inv)
	}
	return nil, &NotWiredError{Command: inv.Cmd.Display, Owner: inv.Cmd.Owner}
}

// runOpinionLifecycleSkeleton 是 validate / reject 的**骨架合同**：先判授权，再止步于未挂载状态机。
//
// 到这里时形态校验（位置参数 / <o-id> 形态 / flag 分域 / 非空 reason）已在 validateOpinionArgs
// 全部通过。授权面：观点验证 / 驳回属写路径，须由命令行 --user-request 显式佐证本次调用由用户
// 发起（观点文件内容不能自证，N-1）。缺它 → 退 2、E19、零写入零 commit（**晚于** Validate 的
// 缺 / 空 reason 用法错退 1，两码绝不冒名）。授权齐备（非空 reason + --user-request）→ 越过授权，
// 止步于**未挂载的验证生命周期状态机** → NotWired 退 1；真正的写行为（状态机、store/plan/txn）
// 归 T-007，本骨架一格不碰。
func (r *Root) runOpinionLifecycleSkeleton(inv *Invocation) (*Result, error) {
	if !inv.UserRequest {
		return nil, opinionAuthDenied(inv.Args[0])
	}
	return nil, &NotWiredError{Command: inv.Cmd.Display, Owner: inv.Cmd.Owner}
}

// opinionAuthDenied 是 validate / reject 缺 --user-request 时的带类型错误（退 2、零写入零 commit）。
// 与 deleteDenied 的授权口径同源：ValidationError 携**恰一条** E19（path=--user-request、
// target=<o-id>、op_index 非 op 级），机读侧据此逐字段定位到「缺用户显式发起佐证」这一格。
func opinionAuthDenied(target string) error {
	return &ValidationError{
		Msg: fmt.Sprintf(
			"eg opinion 验证 / 驳回属写路径：请加 --%s 表明本次调用由用户显式发起（观点文件内容写什么都不能自证，N-1）",
			UserRequestFlag),
		Diags: []Diagnostic{{
			Code: E19, Level: LevelError, Path: "--" + UserRequestFlag, OpIndex: NonOpDiagnostic,
			Message: "观点验证 / 驳回缺少用户显式发起佐证（--user-request）", Target: target,
		}},
	}
}

// runOpinionShow 实现 `eg opinion show`：按 <o-id> 全库定位单条观点，只读投影成
// query.ShowOpinionPaged 的权威结果，再折成文本 / JSON 双渲染（同源同事实）。
//
// 复用 card show 的每一处口径 —— readIndexDeps 注入的 A-44 水位线、pageSpecFrom 的分页规格、
// VisibilityPolicy 的对端 deprecated 可见性、ErrInvalidOpinionID / ErrOpinionNotFound → 退 1、
// Q / W 系列 diagnostics 透出 —— 只把「单卡」换成「单观点」，DTO 键序换成 query.OpinionDataKeys()。
// 全程零写入、零 commit：退出码只可能 0（单条视图也退 0）/ 1（形态非法 / 观点不存在）。
func (r *Root) runOpinionShow(inv *Invocation) (*Result, error) {
	page, err := pageSpecFrom(inv)
	if err != nil {
		return nil, err
	}
	view, err := query.ShowOpinionPaged(inv.VaultRoot, model.OpinionID(inv.Args[0]),
		readIndexDeps(inv.VaultRoot), page,
		query.VisibilityPolicy{IncludeDeprecated: inv.String("include-deprecated") == "true"})
	if err != nil {
		if errors.Is(err, query.ErrInvalidOpinionID) || errors.Is(err, query.ErrOpinionNotFound) {
			return nil, &UsageError{Msg: err.Error()}
		}
		return nil, err
	}

	o := view.Opinion
	// data 键序**逐字**取 query.OpinionDataKeys()（新投影，与 CardDataKeys / Search*Keys 解耦）；
	// supports / limits / opposing 各自 marshal 成固定的 forward / reverse 两段（RelationGroup）。
	out := &Result{
		Data: map[string]interface{}{
			"id": o.ID, "title": o.Title, "domain": o.Domain, "status": o.Status,
			"deprecated": o.Deprecated, "validation": o.Validation,
			"created_at": o.CreatedAt, "updated_at": o.UpdatedAt, "path": o.Path,
			"tags": o.Tags, "markers": o.Markers, "sections": o.Sections,
			"unknown_sections": o.UnknownSections, "sources": o.Sources,
			"supports": o.Supports, "limits": o.Limits, "opposing": o.Opposing,
			query.FieldDeleted: o.Deleted,
		},
		DataOrder: query.OpinionDataKeys(),
	}
	out.Summary = append(out.Summary, opinionShowLines(view)...)
	out.Summary = append(out.Summary, pageSummaryLines(view.Page, "关系条目")...)
	out.Warnings = append(out.Warnings, queryDiagnostics(view.Diagnostics)...)
	return out, nil
}

// opinionShowLines 渲染单条观点的人类可读视图。与 --json **同源同事实**：吃的是同一份
// view.Opinion（标记、validation、五分区、sources、supports / limits / opposing 三组正反向），
// 每一行都能在 data 里逐字对上。悬空目标标「（目标不存在）」（据 view.MissingTargets），
// 已展示的 deprecated 对端标 [失效]（据 view.DeprecatedPeers，仅 --include-deprecated 下非空）。
func opinionShowLines(view *query.OpinionShowResult) []string {
	o := view.Opinion
	lines := []string{
		fmt.Sprintf("%s%s  %s  [%s] %s", strings.Join(o.Markers, ""), o.ID, o.Title, o.Domain,
			strings.Join(o.Tags, ",")),
		fmt.Sprintf("状态：%s（deprecated=%t）  validation=%s  created_at=%s  updated_at=%s  路径：%s",
			o.Status, o.Deprecated, o.Validation, o.CreatedAt, o.UpdatedAt, o.Path),
	}
	// 五分区（固定序）：缺分区键仍在、显式标注「（本分区缺失）」——分区缺失不属 Q 系列。
	for _, name := range o.Sections.Keys() {
		if o.Sections.Missing(name) {
			lines = append(lines, fmt.Sprintf("分区 %s：（本分区缺失）", name))
			continue
		}
		lines = append(lines, fmt.Sprintf("分区 %s：%s", name, firstLine(o.Sections.Get(name))))
	}
	// 非固定分区（用户自建 H2）：与 --json 的 unknown_sections 同源，共享 helper 渲染完整正文；
	// 空数组不产生任何行（I-…-007，与 card show 同一口径）。
	lines = append(lines, unknownSectionLines(o.UnknownSections)...)
	// 材料出处 sources[]（空写「无」）。
	if len(o.Sources) == 0 {
		lines = append(lines, "材料出处 sources[]：无")
	}
	for _, s := range o.Sources {
		lines = append(lines, fmt.Sprintf("材料出处：%s / %s  rel=%s  理由：%s",
			s.Source, s.Note, s.Rel, s.Reason))
	}
	missing := map[string]bool{}
	for _, id := range view.MissingTargets {
		missing[id] = true
	}
	deprecated := map[string]bool{}
	for _, id := range view.DeprecatedPeers {
		deprecated[id] = true
	}
	// 支持 / 限制 / 反对三组，每组正向 + 反向两段（固定序；derives 已在 query 层排除）。
	lines = append(lines, opinionGroupLines("支持", o.Supports, missing, deprecated)...)
	lines = append(lines, opinionGroupLines("限制", o.Limits, missing, deprecated)...)
	lines = append(lines, opinionGroupLines("反对", o.Opposing, missing, deprecated)...)
	lines = append(lines, fmt.Sprintf(
		"opinion show：扫描 %d 个 .md，跳过 %d 个（只读，零写入零 commit）",
		view.ScannedFiles, view.SkippedFiles))
	return lines
}

// opinionGroupLines 渲染一组论证关系的正向 + 反向两段：空段显式写「无」，绝不省略段落
// （否则读者分不清「该组无正向」与「渲染漏了」）。悬空正向目标标「（目标不存在）」，
// 已展示的 deprecated 对端标 [失效]，与 card show 的对端标注口径逐字一致。
func opinionGroupLines(label string, g query.RelationGroup,
	missing, deprecated map[string]bool) []string {
	lines := []string{}
	if len(g.Forward) == 0 {
		lines = append(lines, fmt.Sprintf("%s·正向：无", label))
	}
	for _, e := range g.Forward {
		note := ""
		if missing[e.Target] {
			note = "（目标不存在）"
		}
		lines = append(lines, fmt.Sprintf("%s·正向：%s --%s--> %s%s%s  理由：%s",
			label, e.From, e.Type, e.Target, peerDeprecatedMark(deprecated, e.Target), note, e.Reason))
	}
	if len(g.Reverse) == 0 {
		lines = append(lines, fmt.Sprintf("%s·反向：无", label))
	}
	for _, e := range g.Reverse {
		lines = append(lines, fmt.Sprintf("%s·反向：%s%s --%s--> %s  理由：%s（来源：%s）",
			label, e.From, peerDeprecatedMark(deprecated, e.From), e.Type, e.Target, e.Reason, e.Path))
	}
	return lines
}

// runOpinionSearch 实现 `eg opinion search`：只读检索观点，严格等于 query.Search(Kind=opinion)。
//
// 复用 eg search 的每一处口径 —— include-deleted 解析、分页规格、领域登记校验、Index 水位线、
// ErrInvalidQuery→退 1、Q 系列 diagnostics 透出 —— 只把 Kind 钉死成 opinion，并把命中折成
// 追加了 validation / relation_summary 的专用 DTO。全程零写入、零 commit。
func (r *Root) runOpinionSearch(inv *Invocation) (*Result, error) {
	includeDeleted, err := strconv.ParseBool(inv.String(SearchIncludeDeletedFlag))
	if err != nil {
		return nil, &UsageError{Msg: fmt.Sprintf("--%s 取值非法：%v",
			SearchIncludeDeletedFlag, err)}
	}
	page, err := pageSpecFrom(inv)
	if err != nil {
		return nil, err
	}
	req := query.SearchRequest{
		Query:  inv.Args[0],
		Domain: inv.String("domain"),
		Tags:   captureTags(inv),
		Since:  inv.String("since"),
		Until:  inv.String("until"),
		// Kind 固定 opinion：opinion 检索面天然只搜观点，不经 --kind 开关（本命令未注册它）。
		Kind:           query.SearchKindOpinion,
		IncludeDeleted: includeDeleted,
		Index:          readIndexDeps(inv.VaultRoot),
		Page:           page,
	}
	if req.Domain != "" && !inv.Config.HasDomain(req.Domain) {
		return nil, &UsageError{Msg: fmt.Sprintf(
			"领域 %s 未登记在 %s 的 domains 里：请先 eg config set domains <d1,d2>（eg 绝不自选领域）",
			req.Domain, ConfigFileName)}
	}

	res, err := query.Search(inv.VaultRoot, req)
	if err != nil {
		if errors.Is(err, query.ErrInvalidQuery) {
			return nil, &UsageError{Msg: err.Error()}
		}
		return nil, err
	}

	hits := make([]opinionSearchHit, 0, len(res.Hits))
	for _, h := range res.Hits {
		hits = append(hits, newOpinionSearchHit(h))
	}
	out := &Result{
		Data: map[string]interface{}{
			"hits":          hits,
			"total":         res.Total,
			"scanned_files": res.ScannedFiles,
			"skipped_files": res.SkippedFiles,
		},
		DataOrder: query.SearchDataKeys(),
	}
	out.Summary = append(out.Summary, fmt.Sprintf(
		"opinion search：query=%q，命中 %d 条观点（扫描 %d 个 .md，跳过 %d 个；只读，零写入零 commit）",
		req.Query, res.Total, res.ScannedFiles, res.SkippedFiles))
	out.Summary = append(out.Summary, pageSummaryLines(res.Page, "观点")...)
	if res.Total == 0 {
		out.Summary = append(out.Summary, "无匹配：hits 为空、total = 0（零观点结果合法，仍退 0）")
	}
	for _, h := range res.Hits {
		out.Summary = append(out.Summary, opinionSearchHitLine(h))
	}
	out.Warnings = append(out.Warnings, queryDiagnostics(res.Diagnostics)...)
	return out, nil
}

// opinionSearchHit 是 `eg opinion search` 的专属命中 DTO：匿名内嵌 eg search 的既有 hit
// （其字段与 JSON 键、次序一字不改），再**追加** validation 与 relation_summary 两键。
//
// 内嵌字段在 JSON 里就地展开（promoted），因此键序恒为「eg search 既有键 → validation →
// relation_summary」；query.SearchHit.Opinion 带 `json:"-"`，不会随内嵌泄漏进输出。
type opinionSearchHit struct {
	query.SearchHit
	Validation      string                    `json:"validation"`
	RelationSummary opinionRelationSummaryDTO `json:"relation_summary"`
}

// opinionRelationSummaryDTO 是 relation_summary 的 JSON 形态：键**固定** supports / limits /
// opposing（即声明序），供机器判据逐字反证。只承载观点自身 relations[] 的三类确定性计数。
type opinionRelationSummaryDTO struct {
	Supports int `json:"supports"`
	Limits   int `json:"limits"`
	Opposing int `json:"opposing"`
}

// newOpinionSearchHit 把一条 query.SearchHit 折成专用 DTO：validation 与三类关系计数取自
// hit 侧的 opinionMeta 投影（query 层对 Kind=opinion 命中必然填充；理论上的 nil 兜成空值）。
func newOpinionSearchHit(h query.SearchHit) opinionSearchHit {
	dto := opinionSearchHit{SearchHit: h}
	if h.Opinion != nil {
		dto.Validation = h.Opinion.Validation
		dto.RelationSummary = opinionRelationSummaryDTO{
			Supports: h.Opinion.Relations.Supports,
			Limits:   h.Opinion.Relations.Limits,
			Opposing: h.Opinion.Relations.Opposing,
		}
	}
	return dto
}

// opinionSearchHitLine 渲染一条观点命中的人类可读行：在 eg search 同形态基础上，**显式**追加
// [<validation>] 与三类关系计数（次序 supports → limits → opposing，由 Ordered 固定、零分支）。
// 与 --json 同源同事实：两种渲染吃的是同一份 res.Hits。
func opinionSearchHitLine(h query.SearchHit) string {
	base := fmt.Sprintf("%s%s  %s  [%s] %s  %s",
		searchHitMarkers(h), h.ID, h.Title, h.Domain, strings.Join(h.Tags, ","), h.UpdatedAt)
	var validation string
	var summary query.OpinionRelationSummary
	if h.Opinion != nil {
		validation = h.Opinion.Validation
		summary = h.Opinion.Relations
	}
	counts := make([]string, 0, 3)
	for _, c := range summary.Ordered() {
		counts = append(counts, fmt.Sprintf("%s=%d", c.Type, c.Count))
	}
	return fmt.Sprintf("%s  [%s]  %s", base, validation, strings.Join(counts, " "))
}
