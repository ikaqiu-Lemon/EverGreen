package cli

// `eg opinion` 的命令外壳、search 只读检索实现与另三条子命令**骨架**
// （读路径检索拆分设计 §5.4；T-…-006 批次 B1a 起，B1b-cli 接通 search）。
//
// # 本批做什么（B1b-cli）
//
// 在既有 opinion 命令壳内**接通 search 一条子命令**：runOpinionSearch 只读检索，严格等于
// query.Search(Kind=opinion)，复用 `eg search` 的领域校验、Index deps、分页、错误与
// diagnostics 口径。show / validate / reject **仍是未实现骨架**（合法形态走 NotWiredError，
// 退 1、零文件变化、零 commit）。子命令集合与顺序**恰**为 search|show|validate|reject，不增删。
//
// # 参数面（父命令注册 7 个 search-only flag，恰不含 --kind）
//
// domain / 可重复 tag / since / until / include-deleted / limit / offset —— 与 `eg search`
// 同名同义，供 search 复用同一套口径。**刻意不注册 --kind**：opinion 检索面天然只搜观点
// （runOpinionSearch 把 Kind 固定成 opinion），再给 kind 开关即多余且可诱导误用；
// `eg opinion search --kind …` 因此被参数解析当场判成「未定义 flag」→ 退 1、零写入。
//
// 这 7 个 flag 注册在父命令上（FlagSet 分不清子命令），show / validate / reject 也会**解析**
// 到它们；那三条子命令显式带任一 search-only flag 一律退 1（见 rejectOpinionSearchOnlyFlags），
// 绝不静默接受 —— 「参数写了却不生效」比报错更坏。
//
// # 专用 DTO
//
// opinion search 的命中沿用 `eg search` hit 的既有字段，**追加** validation 与 relation_summary
// （relation_summary 的 JSON 键固定 supports / limits / opposing）。普通 `eg search` 的 JSON
// 合同（SearchHitKeys / SearchDataKeys）一字不改：观点专属事实一个键都不进 eg search 输出。
//
// # 只读零副作用
//
// search 路径没有任何 store / plan / git 写口调用：零文件变化、零 commit，退出码只可能 0 / 1。
//
// # 阶段边界（本批不做，后续批次做）
//
//   - opinion show 的五分区 + 支持/限制/反对三组正反向关系视图 → 后续批次 / T-006-D。
//   - validate / reject 的验证生命周期状态机与写参数合同 → T-006-D / T-007。本批不造任何写行为。

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

// OpinionSubcommands 返回 `eg opinion` 的子命令集合（顺序即 --help 顺序）。
func OpinionSubcommands() []string {
	return []string{SubOpinionSearch, SubOpinionShow, SubOpinionValidate, SubOpinionReject}
}

func opinionCommand() *Command {
	return &Command{
		Name:        "opinion",
		Display:     "opinion search|show|validate|reject",
		Summary:     "观点子系统：检索 / 查看 / 采纳 / 驳回（search 已接通只读检索；另三条随后续批次落地）",
		Owner:       "T-evergreen.knowledge_opinion_split-158614-006",
		Subs:        OpinionSubcommands(),
		SubRequired: true,
		// 刻意**不设** ReadOnly：opinion 未来含 validate / reject 写子命令，父命令一旦标只读，
		// 框架的零副作用断言就会与那两条写子命令冲突。search 路径的只读性由 runOpinionSearch
		// 自身「不碰任何写口」保证，不靠命令级 ReadOnly 标记。
		Usage: `eg opinion search <q> [--domain <d>] [--tag <t>]... [--since <YYYY-MM-DD>] [--until <YYYY-MM-DD>] [--include-deleted] [--limit <n>] [--offset <n>] [--json]
eg opinion show <o-id> [--include-deprecated] [--limit <n>] [--offset <n>] [--json]
eg opinion validate <o-id> [--json]
eg opinion reject <o-id> [--json]

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

search 已接通：只读检索观点（domains/<d>/opinions/**.md），validation 三态（pending /
validated / rejected）全部召回、绝不隐式过滤；每条命中带 validation 与 supports/limits/opposing
关系计数；失效观点标 [失效]、已删除观点经 --include-deleted 带回并标 [已删除]。
show 已接通：按 <o-id> 全库定位单条观点，显式给出 validation、五分区正文、sources、supports /
limits / opposing 三组各正反两段（空段写“无”）；悬空对端标“目标不存在”并产 Q2；对端 deprecated
默认隐藏并计 Q4，--include-deprecated 才展示；已删除观点仍可显式查看并标 [已删除]。
两条都只读：零文件变化、零 commit。validate / reject **仍是未实现骨架**：合法形态退 1（业务实现
尚未挂载）、零写入零 commit。子命令显式带不属于自己分域的读 flag → 退 1（不静默接受）。
缺 / 未知子命令、位置参数个数不符、ID 形态非法 → 一律退 1、零写入。
退出码：0（零命中 / 单条视图也退 0） | 1 参数非法 / 领域未登记 / 观点不存在 / 实现未挂载（均零写入）
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

// validateOpinionArgs 做**参数形态**校验：位置参数个数、<o-id> 形态、按子命令精确分域拒绝读 flag。
//
// search 恰 1 个 <q>，只做检索：显式带 show 专属的 --include-deprecated 一律退 1。
// show / validate / reject 恰 1 个 <o-id>（须经 model.OpinionID.Valid）：
//   - show 只吃 --include-deprecated 与分页，显式带任一检索过滤 flag（domain/tag/since/until/
//     include-deleted）退 1；
//   - validate / reject 尚未挂载业务，显式带任一读路径 flag（检索过滤 / 可见性 / 分页）退 1。
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
		return rejectOpinionFlags(inv, []string{"include-deprecated"},
			"该参数只作用于 opinion show（观点视图对端可见性开关），检索路径不解读它")
	case SubOpinionShow:
		if len(inv.Args) != 1 {
			return &UsageError{Msg: fmt.Sprintf(
				"eg opinion %s 需要恰 1 个位置参数 <o-id>，实际 %d 个", inv.Sub, len(inv.Args))}
		}
		if !model.OpinionID(inv.Args[0]).Valid() {
			return &UsageError{Msg: fmt.Sprintf(
				"观点 ID %q 形态非法：必须以 o- 为前缀（避免把 k-/n-/s- 串当成观点 ID）", inv.Args[0])}
		}
		return rejectOpinionFlags(inv, opinionSearchFilterFlags(),
			"该参数只作用于 opinion search 检索路径，show 视图按 <o-id> 精确定位、不做检索过滤")
	case SubOpinionValidate, SubOpinionReject:
		if len(inv.Args) != 1 {
			return &UsageError{Msg: fmt.Sprintf(
				"eg opinion %s 需要恰 1 个位置参数 <o-id>，实际 %d 个", inv.Sub, len(inv.Args))}
		}
		if !model.OpinionID(inv.Args[0]).Valid() {
			return &UsageError{Msg: fmt.Sprintf(
				"观点 ID %q 形态非法：必须以 o- 为前缀（避免把 k-/n-/s- 串当成观点 ID）", inv.Args[0])}
		}
		return rejectOpinionFlags(inv, opinionReadFlags(),
			"该参数属只读检索 / 查看路径，验证 / 驳回子命令（尚未挂载）不接受任何读 flag")
	}
	return &UsageError{Msg: fmt.Sprintf("eg opinion 必须带子命令：%s",
		strings.Join(OpinionSubcommands(), " | "))}
}

// runOpinion 是 `eg opinion` 的分发壳：search / show 走已接通的只读视图，validate / reject 仍为未实现骨架。
//
// validate / reject 一律返回 NotWiredError —— 与框架「命令已注册、Handler 未挂载」时 dispatch
// 自发的错误**逐字同源**（退 1、零文件变化、零 commit）。后续批次接管时只需在此增分派、填实现，
// 本壳的「未实现即退 1、零写入」边界不放宽。
func (r *Root) runOpinion(inv *Invocation) (*Result, error) {
	switch inv.Sub {
	case SubOpinionSearch:
		return r.runOpinionSearch(inv)
	case SubOpinionShow:
		return r.runOpinionShow(inv)
	}
	return nil, &NotWiredError{Command: inv.Cmd.Display, Owner: inv.Cmd.Owner}
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
			"tags": o.Tags, "markers": o.Markers, "sections": o.Sections, "sources": o.Sources,
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
