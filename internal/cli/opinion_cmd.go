package cli

// `eg opinion` 的命令外壳：search / show 只读检索 / 单条视图，validate / reject（含 validate
// --reopen）已接通的**观点验证生命周期直写事务**（读路径检索拆分设计 §5.4；生命周期状态机与写入
// 由 T-007 落地）。
//
// # search / show（只读）
//
// search 把检索面固定为观点（runOpinionSearch 恒置 Kind=opinion），复用 `eg search` 的过滤 / 分页
// 口径并追加 validation 与 relation_summary；show 按 <o-id> 全库定位单条观点、只读投影。两者都不碰
// 任何 store / plan / git 写口：零文件变化、零 commit，退出码只可能 0 / 1。
//
// # validate / reject（写路径：观点验证生命周期）
//
// validate / reject（含 validate --reopen）经 runOpinion → runOpinionLifecycle → opinionLifecycleCritical
// 落地为直写事务：与 mark-reviewed / undelete 同一把 run.lock、同一套 S1~S9 时序。命令名 + --reopen
// 映射成目标验证态（validate → validated；validate --reopen → pending；reject → rejected），action
// 一律由 model.ValidationTransition(from,to) 复算（CLI 从不自报）；合法边恰五条，三个自环与
// rejected→validated 逆向直跳非法（撤销否决须先 reopen 回 pending 再 validate）。观点验证属用户显式
// 写路径（P-U）：缺 --user-request → 退 2、E19；Agent / 自动路径（P-A）不得代替用户写 validation。
// 成功恰改写目标观点单文件、恰一个 verb=process commit。被验证观点的支持面由
// W29（opinion_unsupported_validated）巡检：validated 却零有效 incoming supports 时告警。
//
// # 参数面（父命令注册 8 个读 flag + 2 个写路径 flag --reason / --reopen，恰不含 --kind）
//
// 读 flag：domain / 可重复 tag / since / until / include-deleted / include-deprecated / limit /
// offset —— 与 `eg search` / `eg card show` 同名同义，供 search / show 复用同一套口径。
// 写路径 flag：--reason —— 观点采纳 / 驳回 / 复议的理由，**只允许** validate / reject 使用；
// --reopen —— 观点复议开关（bool、默认 false），**只允许** validate 使用（validated/rejected →
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
// # 副作用与退出码
//
// search / show：零文件变化、零 commit（退出码 0 / 1）。
// validate / reject 按真实阶段裁决退出码（与 mark-reviewed / undelete 同源，见 opinionLifecycleCritical
// 与 opinionLifecycleFinish）：
//   - 参数非法 / 缺空 reason → 退 1；缺 --user-request（E19）/ 观点不存在（E18）/ 非法验证边 → 退 2；
//     以上皆零权威写、零 commit；
//   - 写前预演阻断（S4 / 命令层 B3：S3 锁内重读后、S4 落盘前目标被并发改写，ExpectedHash 守卫命中）→
//     退 3：目标态不生效、命令层零覆盖零还原（并发新字节原样保留）、零事务、零 commit；
//   - 原子提交失败且成功回滚（S6）→ 退 3：目标保持事务前像、零 commit；
//   - Git 提交失败（S7 / B4）→ 退 4：validation 目标态已由 commit marker 定盘并保留、不回滚，但无 Git
//     commit（仍走完 S8 写后索引同步）；
//   - 锁不可用（E16，run.lock 等待超时）/ enterTxnCritical 的事务安全复核·恢复屏障 fail-closed（E15）→
//     退 5，零权威写、零 commit；opinion **不跑** plan 的 --strict 预检，故 E15 只源于 S1/S2 的
//     锁 + 恢复屏障，与 --strict 升级面无关；S4 单文件原子域守卫是防御性兜底（E21 → 退 1），不进 5；
//   - 成功 → 目标观点单文件写入 + 恰一个 verb=process commit。
// 不存在「所有失败都零权威写」这一笼统结论：退 4 的目标态已在磁盘生效并保留。

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/git"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
	"github.com/ikaqiu-Lemon/EverGreen/internal/report"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// 四个子命令名（**恰四个**，顺序即 --help 顺序：设计 §5.4 逐字 search|show|validate|reject）。
// 这四个子命令固定不变，不得增删或重排。
const (
	SubOpinionSearch   = "search"
	SubOpinionShow     = "show"
	SubOpinionValidate = "validate"
	SubOpinionReject   = "reject"
)

// OpinionReasonFlag 是 validate/reject 的写路径理由 flag 名（注册点与分域拒绝逐字共用同一字面量）。
const OpinionReasonFlag = "reason"

// OpinionReopenFlag 是 validate 的观点复议 flag 名（bool，默认 false；注册点与分域拒绝逐字共用同一字面量）。
// 语义（观点 schema v2 设计 §6.1/§6.2）：把观点验证态**复议回 pending**（validated/rejected → pending，
// 出现新反例时降级），复用 `eg opinion validate --reopen` 而不新增命令。**只作用于 validate**：
// reject / search / show 显式带它一律退 1（逐字点名 --reopen）。--reopen 已接通复议边（validate --reopen
// → 目标态 pending），由 opinionTargetValidation 映射、model.ValidationTransition 复算。
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
		Summary:     "观点子系统：检索 / 查看 / 采纳 / 驳回（search / show 只读；validate / reject 已接通观点验证生命周期直写事务，成功单文件、单 commit verb=process）",
		Owner:       "T-evergreen.knowledge_opinion_split-158614-006",
		Subs:        OpinionSubcommands(),
		SubRequired: true,
		// 刻意**不设** ReadOnly：opinion 含 validate / reject 写子命令（已接通观点验证生命周期
		// 直写事务，走 run.lock + S1~S9），父命令一旦标只读，框架的零副作用断言就会与那两条写子命令冲突。
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

validate / reject 的写路径参数（观点验证生命周期已接通：单文件原子事务，成功恰一个 commit，verb=process）：
  --reason <text>        是；采纳 / 驳回 / 复议的理由（缺 / 空串 / 纯空白 → 退 1；search / show 显式带它也 → 退 1）
  --user-request         是（全局 flag）；本次调用由用户显式发起的命令行佐证（观点验证属写路径，
                         文件内容不能自证，N-1）。缺它 → 退 2、零写入，data.errors[] 恰一条 E19（path=--user-request）
  --reopen               否，默认 false，**仅 validate**；把观点验证态复议回 pending（validated/rejected
                         → pending，出现新反例时降级）；reject / search / show 显式带它 → 退 1（逐字点名 --reopen）

search 已接通：只读检索观点（domains/<d>/opinions/**.md），validation 三态（pending /
validated / rejected）全部召回、绝不隐式过滤；每条命中带 validation 与 supports/limits/opposing
关系计数；失效观点标 [失效]、已删除观点经 --include-deleted 带回并标 [已删除]。
show 已接通：按 <o-id> 全库定位单条观点，显式给出 validation、五分区正文、sources、supports /
limits / opposing 三组各正反两段（空段写“无”）；悬空对端标“目标不存在”并产 Q2；对端 deprecated
默认隐藏并计 Q4，--include-deprecated 才展示；已删除观点仍可显式查看并标 [已删除]。
search / show 都只读：零文件变化、零 commit。
validate / reject 已接通观点验证生命周期直写事务：action 由 model.ValidationTransition(from,to) 唯一复算、
CLI 从不自报。合法验证边**恰五条**——pending -> validated（validate）、pending -> rejected（reject）、
validated -> rejected（reject）、validated -> pending（reopen）、rejected -> pending（reopen）；三个自环与
rejected -> validated 逆向直跳一律非法（撤销否决须先 reopen 回 pending，再 validate）。目标态映射：
validate → validated、validate --reopen → pending、reject → rejected。判定顺序逐字锁：位置参数 →
<o-id> 形态 → flag 分域 → 缺/空 reason（以上均退 1）→ 缺 --user-request（退 2、E19）→ 锁内重读定 from →
非法边退 2。观点验证是用户显式写路径（P-U）：Agent / 自动路径（P-A）不得代替用户写 validation。
失败语义按阶段分（**并非所有失败都零权威写**）：
  - 参数 / 授权 / 非法边 / 写前阻断：本命令零权威写、零 commit；
  - S4 预演 / 命令层 B3（S3 锁内重读后、S4 落盘前目标被并发改写，ExpectedHash 守卫命中）：目标态不生效，
    命令层不覆盖 / 不还原并发写入（并发新内容原样保留），零事务、零 commit（退 3）；
  - S6 原子提交失败且成功回滚：退 3，目标保持事务前像、零 commit；
  - S7 Git 提交失败：退 4，validation 目标态已在磁盘生效并保留、不回滚，但无 Git commit；
  - 成功：目标观点**单文件**（write-set 恰 1 条）+ 恰一个 verb=process commit，全程同一把 run.lock。
被验证观点的支持面由 W29（opinion_unsupported_validated）巡检：validation=validated 却零**有效 incoming
supports** 时告警。有效 incoming supports 口径：只计**指向本观点的 incoming supports 边**（本观点自己发出的
outgoing supports 不计）；supporter 必须**存在且未删除**（supporter 已删除 → 该支持无效）；supporter 为
deprecated 但未删除**仍有效**；同一 supporter 的重复 supports 边**去重、只算一次**。W29 只报告，绝不自动
改 validation 或补关系。
缺 / 未知子命令、位置参数个数不符、ID 形态非法 → 一律退 1、零写入。
退出码：0 成功（目标单文件写入 + 恰一个 verb=process commit；零命中 / 单条视图也退 0）
        | 1 参数非法 / 领域未登记 / 缺空 reason（零权威写、零 commit）
        | 2 授权失败（缺 --user-request → E19）/ 观点不存在（E18）/ 非法验证边（零权威写、零 commit）
        | 3 写前预演阻断（S4 / 命令层 B3：目标态不生效、并发写入原样保留、零事务）或原子提交失败已整体回滚（S6：目标保持前像）——两者均零 commit
        | 4 Git 提交失败（S7/B4：validation 目标态已在磁盘生效并保留、不回滚，但无 Git commit）
        | 5 run.lock 不可用（E16）/ enterTxnCritical 事务安全复核·恢复屏障 fail-closed（E15）：零权威写、零 commit（本命令不跑 --strict 预检）
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
			// 逐字点名（见 validateOpinionArgs 的 rejectOpinionFlags）。--reopen 已接通复议边：
			// validate --reopen 走 opinionTargetValidation → 目标态 pending，由状态机复算 action。
			fs.Bool(OpinionReopenFlag, false, "观点复议：把验证态复议回 pending（仅 validate；validated/rejected → pending）")
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
// 因此那三条子命令能**解析**到它们；show 视图按 <o-id> 精确定位、不做检索过滤，validate / reject
// 是写路径、不吃只读检索 flag，都必须在 Validate 阶段显式拒绝——否则 `eg opinion show o-… --domain x`
// 会被静默忽略，「参数写了却不生效」比报错更坏。分页（limit/offset）不在此列：search 与 show 都吃它。
func opinionSearchFilterFlags() []string {
	return []string{"domain", "tag", "since", "until", SearchIncludeDeletedFlag}
}

// opinionPageFlags 是 search 与 show 共用的分页 flag（validate / reject 属写路径、不吃分页，显式带即退 1）。
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
//   - validate / reject 属写路径：显式带任一读路径 flag（检索过滤 / 可见性 / 分页）→ 退 1，
//     且**必带非空 --reason**（缺 / 空串 / 纯空白 → 退 1）。判定顺序逐字锁：位置参数 → <o-id> 形态 →
//     flag 分域 → 缺/空 reason（授权判定在 runOpinionLifecycle，晚于这里，见其函数注释）。
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
			"该参数属只读检索 / 查看路径，验证 / 驳回子命令（写路径）不接受任何读 flag"); err != nil {
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
		// 缺 / 空 reason **先于**授权判定（授权在 runOpinionLifecycle；这里退 1、那里退 2，
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

// runOpinion 是 `eg opinion` 的分发壳：search / show 走已接通的只读视图；validate / reject
// （含 validate --reopen）走已接通的**观点验证生命周期直写事务** runOpinionLifecycle
// （取锁 → S1~S9、状态机复算、单文件事务、verb=process commit）。
//
// 末尾的 default 分支**不可达**：inv.Sub 恒是四个已登记子命令之一（缺 / 未知子命令已由 dispatch 的
// SubRequired 与 validateOpinionArgs 先行拦下）。它只是纯防御性兜底 —— 万一注册表与分发失配才会触到，
// 返回 NotWiredError 让框架把「命令已注册、Handler 未接」这一内部装配错误显式暴露。它**不描述**
// validate / reject 的行为（后两者的真实行为在 runOpinionLifecycle）。
func (r *Root) runOpinion(inv *Invocation) (*Result, error) {
	switch inv.Sub {
	case SubOpinionSearch:
		return r.runOpinionSearch(inv)
	case SubOpinionShow:
		return r.runOpinionShow(inv)
	case SubOpinionValidate, SubOpinionReject:
		return r.runOpinionLifecycle(inv)
	}
	// 防御性 fallback（不可达）：仅在注册表 / 分发装配失配时触发，绝非 validate / reject 的正常出口。
	return nil, &NotWiredError{Command: inv.Cmd.Display, Owner: inv.Cmd.Owner}
}

// runOpinionLifecycle 是 validate / reject（含 validate --reopen）接通的**观点验证生命周期**
// 直写事务：与 mark-reviewed / undelete 同一把 run.lock、同一套 S1~S9 时序，把命令名映射成
// 目标验证态，交由状态机复算 action，再经**唯一**写口 store.ApplyStateWrite(StateWriteValidation)
// 覆盖 validation、刷新 updated_at、追加审计块。
//
// 到这里时形态校验（位置参数 / <o-id> 形态 / flag 分域 / 非空 reason）已在 validateOpinionArgs
// 全部通过。授权面：缺 --user-request → 退 2、E19、零写入零 commit（**先于**取锁）。命令名 → 目标态：
// validate → validated；reject → rejected；validate --reopen → pending。action 一律由
// model.ValidationTransition(from,to) 复算（CLI 不自报），非法边退 2（零写入、不取号、无 commit）。
func (r *Root) runOpinionLifecycle(inv *Invocation) (*Result, error) {
	if !inv.UserRequest {
		return nil, opinionAuthDenied(inv.Args[0])
	}
	target := inv.Args[0]
	to := opinionTargetValidation(inv)

	rep := report.New()
	oc, cerr := r.opinionLifecycleCritical(inv, &rep, target, to)
	if oc == nil {
		// 连目标都没定盘（锁失败 / 恢复阻断 / 解析不到 / 读失败 / 非法边 / 预演写失败）：
		// 本次零权威写、无 commit，报告无从谈起。
		return nil, cerr
	}
	return r.opinionLifecycleFinish(inv, &rep, oc, target, to)
}

// opinionTargetValidation 把子命令 + --reopen 映射成本次要落地的目标验证态（无分支自报 action）。
// validate（默认）→ validated；validate --reopen → pending（复议降级）；reject → rejected。
func opinionTargetValidation(inv *Invocation) model.Validation {
	if inv.Sub == SubOpinionReject {
		return model.ValidationRejected
	}
	if inv.String(OpinionReopenFlag) == "true" {
		return model.ValidationPending
	}
	return model.ValidationValidated
}

// opinionLifecycleOutcome 是临界区内产出的全部事实，供锁外渲染报告与裁决退出码
// （口径与 markReviewedOutcome 同源：字段语义逐格对齐）。
type opinionLifecycleOutcome struct {
	Rel         string
	From        model.Validation
	To          model.Validation
	Action      model.ValidationAction
	At          model.Stamp
	TxnID       string
	Commit      git.CommitInfo
	RolledBack  bool
	RollbackErr error
	Unwritten   []string
	Blocked     error
	GitErr      error
}

// opinionLifecycleCritical 是 validate / reject 的整个临界区：进函数即取锁，出函数即释放锁
// （S1 取锁 → S2 恢复屏障 → S3 锁内重读 → S4 预演 → S5 intent → S6 提交 → S7 Git → S8 写后索引同步）。
//
// 返回 (nil, err) 表示阻断（连报告都产不出来：锁失败 / 恢复阻断 / 解析读失败 / 非法边 / 预演写失败）；
// 返回 (oc, nil) 时失败事实挂在 oc 上，由调用方在锁外连同报告一起交付。
func (r *Root) opinionLifecycleCritical(inv *Invocation, rep *report.Report,
	target string, to model.Validation) (*opinionLifecycleOutcome, error) {
	root := inv.VaultRoot

	// —— S1 + S2：取锁、崩溃恢复屏障（与 plan 写链 / capture / mark-reviewed / undelete 共用同一把锁）。——
	sess, serr := r.enterTxnCritical(inv, reportWarnSink{rep}, txnCriticalOpts{
		ZeroWrite: "本次零写入", ReleaseNotice: "本次写入与提交不受影响",
	})
	if serr != nil {
		return nil, serr
	}
	defer sess.release()

	// —— S3：锁内重建 Store，重新全库发现、重新读 —— 严禁挪到锁外（S2 刚可能把目标回滚到前像）。
	st := store.New(root)
	idx, err := st.ScanIDs()
	if err != nil {
		return nil, &ValidationError{Msg: "扫描 vault 失败（零写入）：" + err.Error()}
	}
	rel, rerr := idx.Resolve(target)
	if rerr != nil {
		return nil, &ValidationError{
			Msg: fmt.Sprintf("eg opinion %s 的 <o-id> %s 在库里解析不到文件（零写入）：%v",
				inv.Sub, target, rerr),
			Diags: []Diagnostic{{
				Code: E18, Level: LevelError, Path: inv.Sub, OpIndex: NonOpDiagnostic,
				Message: rerr.Error(), Target: target,
			}},
		}
	}
	f, ferr := st.Read(rel)
	if ferr != nil {
		return nil, &ValidationError{Msg: fmt.Sprintf("读取 %s 失败（零写入）：%v", rel, ferr)}
	}
	// 读权威 frontmatter 定 from：借 OpinionOf 确认目标确是合法观点（wrong-kind / 坏 FM 在此零写入拒绝）。
	op, operr := store.OpinionOf(f.Bytes)
	if operr != nil {
		return nil, &ValidationError{
			Msg: fmt.Sprintf("解析观点 %s 失败（零写入）：%v", rel, operr),
			Diags: []Diagnostic{{
				Code: E20, Level: LevelError, Path: rel, OpIndex: NonOpDiagnostic,
				Message: operr.Error(), Target: target,
			}},
		}
	}
	fireTxnStep(TxnStepReread, "")

	from := op.Validation
	// action 只能由状态机从 (from,to) 复算：自环 / rejected->validated 逆跳 / 非法端点在此
	// 退 2、零写入、**不取号、无 commit**（先于 S4 预演，绝不落半截产物）。
	action, terr := model.ValidationTransition(from, to)
	if terr != nil {
		return nil, &ValidationError{Msg: fmt.Sprintf(
			"eg opinion %s 拒绝该验证流转（零写入）：%v", inv.Sub, terr)}
	}
	// 时刻在**锁内**取：它要进权威字节（validation 审计块 at / updated_at）与 intent。
	oc := &opinionLifecycleOutcome{
		Rel: rel, From: from, To: to, Action: action, At: model.NewStamp(r.now()),
	}

	// —— S4：原子预演。复用**唯一**写口，实盘与 Git 全程零变化，只攒 accepted write-set。——
	if err := st.BeginAtomic(); err != nil {
		return nil, blockedError("原子预演无法开始，本次零写入", err)
	}
	defer st.EndAtomic()

	reason := inv.String(OpinionReasonFlag)
	if _, werr := r.applyStateWrite(st, store.StateWriteSpec{
		Op: store.StateWriteValidation, Rel: rel, ExpectedHash: f.Hash,
		Reason: reason, Validation: to, Stamp: oc.At,
	}); werr != nil {
		// 预演期写失败 ⇒ overlay 丢弃即零写入：不开事务、不发 intent、不跑 Git。
		return nil, &PartialWriteError{
			Msg: fmt.Sprintf("写入 %s 的 %s 失败（磁盘保留现状，未做任何还原）：%v",
				rel, model.FMKeyValidation, werr),
			Diags: []Diagnostic{{
				Code: E21, Level: LevelError, Path: rel, OpIndex: NonOpDiagnostic,
				Message: oneLineReason(werr.Error()), Target: target,
			}},
		}
	}

	ws := st.AtomicWriteSet()
	fireTxnStep(TxnStepExecuted, "")
	// 单文件原子域**硬约束**（合同：一次合法 validation 流转恰改写目标观点一个文件）：
	// accepted write-set 必须**恰含一条**、且**恰是目标 rel**。任何偏离（零条 / 多条 /
	// 路径不符）都意味着写口越出了本命令声明的原子域 —— 此时 overlay 立即丢弃（defer
	// EndAtomic），**不取号、不发 intent、不提交、不跑 Git**，直接返回阻断错误交人工处置，
	// 绝不把一个写面已经外溢 / 落空的预演继续推进成事务。这一格由源码显式守住，不靠
	// 「成功后 intent 恰含一个文件」间接证明。
	//
	// **这是防御性兜底、只在故障注入下可达**：合同下唯一写口对一次 validation 流转恒产恰一条
	// write-set，正常路径走不到这里。它发生在 **S4 末、S5（openTxn）之前**，因此本命令零权威写、
	// 零事务、零 commit。返回值走 blockedError(…, **nil**)：无底层 txn 诊断 ⇒ 兜底 E21（非 E15），
	// classifyExit5 不会把它提升成 PrecheckFailedError，ExitCodeFor 最终判 **退出码 1**。它既不是
	// E15「写前强校验失败」，也**不经** plan 的 --strict 预检（本命令根本不调用 plan.Precheck）——
	// 故用户文档不暴露这个仅注入可达的内部守卫，只在此开发注释中如实标注其真值。
	if len(ws) != 1 || ws[0].Path != rel {
		return nil, blockedError(fmt.Sprintf(
			"opinion %s 的原子预演写集违反单文件原子域（防御性兜底：E21 → 退 1，S5 前零权威写、零事务、零 commit）："+
				"期望恰 1 个目标文件 %s，实得 %d 个 %v", inv.Sub, rel, len(ws), writeSetPaths(ws)), nil)
	}

	// —— S5：分配 txn_id → 记进锁正文 → 发布 intent（发布屏障）。审计边界是「分配成功」（A-59）。——
	txnID, oerr := sess.openTxn(inv, intentFilesOf(ws), nil, r.Now, func(id string) {
		oc.TxnID = id
		rep.SetTxnID(id)
	})
	if oerr != nil {
		if txnID == "" {
			return nil, oerr
		}
		oc.Blocked = oerr
		return oc, nil
	}

	// —— S6：原子提交。commit marker 在盘之前，validation 一律不算生效。——
	cres, cerr := sess.commitWriteSet(txnID, ws)
	switch {
	case cerr != nil && cres != nil && cres.RolledBack:
		oc.RolledBack, oc.RollbackErr = true, cerr
		oc.Unwritten = writeSetPaths(ws)
		rep.AddWarning(unnumberedWarning("ops",
			"原子提交失败，事务 %s 已按合同 §5.2 主动放弃：%v", txnID, cerr))
		for _, p := range oc.Unwritten {
			rep.AddWarning(unnumberedWarning(p,
				"目标未写入：本次事务已整体回滚，该文件保持事务开始前的字节（前像）"))
		}
		rep.AddInfo("ops", report.NonOp,
			"事务 %s 的 %d 个目标文件**一个都没有写入**：txn 层已逐个还原前像、"+
				"全部还原完成后才写下 abort 标记，随后未执行 Git", txnID, len(ws))
		return oc, nil
	case cerr != nil:
		oc.Blocked = blockedError(fmt.Sprintf(
			"事务 %s 的原子提交失败且未能收敛为已回滚状态，需人工处置", txnID), cerr)
		return oc, nil
	}
	fireTxnStep(TxnStepCommitted, txnID)

	// —— S7：Git。严格晚于 commit marker，仍持同一把锁。verb 取 process（状态类命令同口径）。——
	repo := r.repo(root)
	noteExistingChangesInReport(rep, repo, writeSetPaths(ws))
	info, gerr := repo.Commit(git.Message{
		Verb:           string(model.VerbProcess),
		Domain:         commitDomain(domainOfPath(rel)),
		Subject:        fmt.Sprintf("观点 %s 验证流转：%s -> %s（%s）", target, from, to, action),
		Reason:         "用户显式发起的观点验证生命周期流转：唯一写入触发",
		RequirementIDs: []string{},
	})
	for _, w := range info.Warnings {
		rep.AddWarning(report.Diagnostic{
			Code: "", Level: report.LevelWarning, Path: "git.commit", OpIndex: report.NonOp,
			Message: w,
		})
	}
	fireTxnStep(TxnStepGit, txnID)
	if gerr != nil {
		// 不回滚、不做第二次权威写：validation 已由 commit marker 定盘并保持目标态。
		oc.GitErr = gerr
	} else {
		oc.Commit = info
	}

	// —— S8：写后索引同步。仍在**同一把锁内**、Git 之后、Release 之前。Git 成败都走。——
	r.syncIndexAfterWrite(rep, root, indexWriteLabel(inv), writeSetPaths(ws))
	fireTxnStep(TxnStepIndexSync, txnID)
	return oc, nil
}

// opinionLifecycleFinish 在**锁外**把临界区的事实渲染成产物并裁决退出码
// （四条出口与 mark-reviewed 同源：主动回滚退 3、提交前阻断兜底、Git 失败退 4、其余退 0）。
func (r *Root) opinionLifecycleFinish(inv *Invocation, rep *report.Report,
	oc *opinionLifecycleOutcome, target string, to model.Validation) (*Result, error) {
	rel := oc.Rel
	switch {
	case oc.RolledBack:
		rep.SetCommit("")
		res := proposalResult(*rep, []string{fmt.Sprintf(
			"opinion %s 未生效：事务 %s 已整体回滚，%s 的 %s 保持事务开始前的字节（目标 %s）",
			inv.Sub, oc.TxnID, rel, model.FMKeyValidation, target)})
		r.saveReport(inv.VaultRoot, res, ExitPartialWrite)
		return res, &PartialWriteError{
			Msg: fmt.Sprintf("原子提交失败，事务 %s 已整体回滚：%d 个目标文件一个都没有写入、"+
				"磁盘保持事务开始前的字节、未产生 commit（原因：%v）",
				oc.TxnID, len(oc.Unwritten), oc.RollbackErr),
		}
	case oc.Blocked != nil:
		rep.SetCommit("")
		res := proposalResult(*rep, []string{fmt.Sprintf(
			"opinion %s 未提交：事务 %s 已分配号码但未闭合，本次零权威写入（目标 %s，%s）",
			inv.Sub, oc.TxnID, target, rel)})
		r.saveReport(inv.VaultRoot, res, ExitCodeFor(classifyExit5(oc.Blocked)))
		return res, oc.Blocked
	}

	summary := []string{fmt.Sprintf(
		"opinion %s 已执行：%s 的 %s = %s（%s -> %s，action=%s，目标 %s）",
		inv.Sub, rel, model.FMKeyValidation, oc.To, oc.From, oc.To, oc.Action, target)}
	if oc.GitErr != nil {
		// B4：提交失败退 4，已写的字节留在工作区并保持现状，不做任何还原。
		rep.SetCommit("")
		res := proposalResult(*rep, summary)
		r.saveReport(inv.VaultRoot, res, ExitCommitFailed)
		return res, &CommitFailedError{
			Msg: "Git 提交失败：已写入的 validation 保留在磁盘并保持现状，未做任何还原（B4）",
			Err: oc.GitErr,
			Diags: []Diagnostic{{
				Code: E22, Level: LevelError, Path: "git.commit", OpIndex: NonOpDiagnostic,
				Message: commitFailureMessage(oc.GitErr),
			}},
		}
	}
	rep.SetCommit(oc.Commit.SHA)
	rep.Links = append(rep.Links, rel)
	res := proposalResult(*rep, summary)
	r.saveReport(inv.VaultRoot, res, ExitOK)
	return res, nil
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
