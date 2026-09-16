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
eg opinion show <o-id> [--json]
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

search 已接通：只读检索观点（domains/<d>/opinions/**.md），validation 三态（pending /
validated / rejected）全部召回、绝不隐式过滤；每条命中带 validation 与 supports/limits/opposing
关系计数；失效观点标 [失效]、已删除观点经 --include-deleted 带回并标 [已删除]。只读：零文件
变化、零 commit。show / validate / reject **仍是未实现骨架**：合法形态退 1（业务实现尚未挂载）、
零写入零 commit。非 search 子命令显式带任一 search-only flag → 退 1（不静默接受）。
缺 / 未知子命令、位置参数个数不符、ID 形态非法 → 一律退 1、零写入。
退出码：0（零命中也退 0） | 1 参数非法 / 领域未登记 / 实现未挂载（均零写入）
`,
		Flags: func(fs *flagSet) {
			// 与 eg search 同名同义的检索面（search.go 的 runSearch 复用同一套口径）。
			fs.String("domain", "", "限定领域（默认全库）")
			var tags stringList
			fs.Var(&tags, "tag", "标签过滤（可重复，AND）")
			fs.String("since", "", "updated_at 日期下界（YYYY-MM-DD，闭区间）")
			fs.String("until", "", "updated_at 日期上界（YYYY-MM-DD，闭区间）")
			fs.Bool(SearchIncludeDeletedFlag, false, "显式把已删除观点带回结果（默认视图不返回）")
			// 分页（S4 · T-…-068）：注册点唯一，见 page.go。
			pageFlags(fs)
			// **不注册 --kind**：opinion 检索面天然只搜观点（见文件头「参数面」）。
		},
		Validate: validateOpinionArgs,
	}
}

// opinionSearchOnlyFlags 是「只在 search 子命令成立」的 flag 集合（次序固定，供拒绝与用例逐格比对）。
//
// 这些 flag 注册在 opinion 父命令上，show / validate / reject 与 search 共享同一个 FlagSet，
// 因此那三条子命令能**解析**到它们；必须在 Validate 阶段显式拒绝，否则
// `eg opinion show o-… --domain x` 会被静默忽略 —— 「参数写了却不生效」比报错更坏。
func opinionSearchOnlyFlags() []string {
	return []string{"domain", "tag", "since", "until",
		SearchIncludeDeletedFlag, query.PageLimitFlag, query.PageOffsetFlag}
}

// rejectOpinionSearchOnlyFlags 在非 search 子命令上显式拒绝 search-only flag：退 1、零写入。
func rejectOpinionSearchOnlyFlags(inv *Invocation) error {
	for _, name := range opinionSearchOnlyFlags() {
		if inv.Set(name) {
			return &UsageError{Msg: fmt.Sprintf(
				"eg opinion %s 不接受 --%s：该参数只作用于 opinion search 检索路径", inv.Sub, name)}
		}
	}
	return nil
}

// validateOpinionArgs 做**参数形态**校验：位置参数个数、<o-id> 形态、非 search 子命令的 flag 拒绝。
//
// search 恰 1 个 <q>；show / validate / reject 恰 1 个 <o-id>（须经 model.OpinionID.Valid），
// 且不得显式带任一 search-only flag。缺 / 未知子命令由 dispatch 的 SubRequired 分支先行拦下，
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
		return rejectOpinionSearchOnlyFlags(inv)
	}
	return &UsageError{Msg: fmt.Sprintf("eg opinion 必须带子命令：%s",
		strings.Join(OpinionSubcommands(), " | "))}
}

// runOpinion 是 `eg opinion` 的分发壳：search 走已接通的只读检索，其余三条仍为未实现骨架。
//
// search 之外的子命令一律返回 NotWiredError —— 与框架「命令已注册、Handler 未挂载」时
// dispatch 自发的错误**逐字同源**（退 1、零文件变化、零 commit）。后续批次接管时只需在此
// 增分派、填实现，本壳的「未实现即退 1、零写入」边界不放宽。
func (r *Root) runOpinion(inv *Invocation) (*Result, error) {
	if inv.Sub == SubOpinionSearch {
		return r.runOpinionSearch(inv)
	}
	return nil, &NotWiredError{Command: inv.Cmd.Display, Owner: inv.Cmd.Owner}
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
