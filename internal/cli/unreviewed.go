package cli

// `eg unreviewed` 的命令定义与业务实现（提案与状态合同 §6.1「`eg unreviewed`」两行 +
// §14.4 EG-VIEW-08 验收测试；T-evergreen.s1_main_flow-158614-042）。
//
// **只读零副作用**（§6.1「不改状态、无提醒督促」那一行）：本文件没有任何 store / plan / git
// 写口调用，不产生 commit、不改任何字节，尤其**不写 reviewed_at**——「查看未过目清单」
// 本身不是过目（§5.5 的「被动查看一律不更新」）。退出码只可能 0 / 1 / 2。
//
// 取数复用 M2 的扫描底座 internal/query 的 VaultScan（不另起遍历、不建索引）；
// **判定不在本文件**：`updated_at > reviewed_at` 全部关在 internal/query/filter 单文件里
// （ADR-20 文件级隔离），本文件只负责「组装快照 → 交给筛选器 → 渲染事实」。
//
// 排序只用既有事实（updated_at 倒序 → id 升序），**不把未过目信号当权重**：
// ADR-20 明令这个信号不得进入排序权重。M2 三个查询命令的排序与输出格式一字未动。
//
// 阶段边界：不输出 `[未过目]` 文本标记（T-…-043 负责标记的输出与顺序，本命令只给判定值）；
// 不做过期自动判定（S3）；不做对账补齐（S3/M4）。

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
	"github.com/ikaqiu-Lemon/EverGreen/internal/query/filter"
)

// UnreviewedRow 是 `eg unreviewed` 的一行（结构体字段序即 JSON 键序）。
//
// ReviewedAt 为空串即「该键缺省」（从未过目）：这里**不**回填任何默认时刻，
// 输出如实呈现「缺省」这一事实。Unreviewed 恒为 true（进清单的就是命中的），
// 它是 T-…-043 渲染 `[未过目]` 标记时读的那个判定值。
type UnreviewedRow struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind"`
	Domain     string   `json:"domain"`
	Path       string   `json:"path"`
	Tags       []string `json:"tags"`
	UpdatedAt  string   `json:"updated_at"`
	ReviewedAt string   `json:"reviewed_at"`
	Unreviewed bool     `json:"unreviewed"`
}

// 产物类别取值（四类产物同构，本命令的筛选面是知识卡与材料笔记：
// 原文正文收录后不因加工改写、综述属 S3，二者在 M3 语料里没有 reviewed_at 的写入路径）。
const (
	unreviewedKindCard = "card"
	unreviewedKindNote = "note"
)

// unreviewedCommand 注册 `eg unreviewed`（合同 §7.1 命令表 S2 行；只读、无副作用）。
func unreviewedCommand() *Command {
	return &Command{
		Name:     "unreviewed",
		Display:  "unreviewed",
		Summary:  "列出未过目的产物（updated_at > reviewed_at；只读、不改状态）",
		Owner:    "T-evergreen.s1_main_flow-158614-042",
		ReadOnly: true,
		Usage: `eg unreviewed [--domain <d>] [--tag <t>]... [--since <YYYY-MM-DD>] [--until <YYYY-MM-DD>] [--json]

参数（可叠加领域 / 标签 / 时间三类条件，与未过目判定取交集）：
  --domain <d>           否，默认全库；值不在 evergreen.yml 的 domains 内 → 退 1
  --tag <t>              否，可重复；多次给出为 AND（须同时含全部标签），逐字相等比较
  --since <YYYY-MM-DD>   否；updated_at 的日期部分 ≥ 该值（闭区间）
  --until <YYYY-MM-DD>   否；updated_at 的日期部分 ≤ 该值（闭区间）
                         两者须是**日历上真实存在**的日期（与 eg search 同一实现）：形态恰
                         YYYY-MM-DD，且月 01–12、日在该年该月实际天数内 → 否则退 1

判定：updated_at > reviewed_at。**无 reviewed_at 的产物计入**（缺省视为从未过目）。
只读：零文件变化、零 commit，不写 reviewed_at（查看不等于过目），不改状态，
也不产生任何提示用户尽快处理的文案——清单只陈述事实。
退出码：0（零命中也退 0） | 1 参数非法 / 领域未登记 | 2 库内时刻不可比较
`,
		Flags: func(fs *flagSet) {
			fs.String("domain", "", "限定领域（默认全库）")
			var tags stringList
			fs.Var(&tags, "tag", "标签过滤（可重复，AND）")
			fs.String("since", "", "updated_at 日期下界（YYYY-MM-DD，闭区间）")
			fs.String("until", "", "updated_at 日期上界（YYYY-MM-DD，闭区间）")
		},
		Validate: func(inv *Invocation) error {
			if err := noPositionalArgs(inv); err != nil {
				return err
			}
			// 次序固定（先 --since 再 --until）：两个都非法时报的也必须是同一条，
			// 否则同一输入两次执行的错误文案会不同。
			for _, f := range []struct{ name, value string }{
				{"--since", inv.String("since")}, {"--until", inv.String("until")},
			} {
				if f.value != "" && !query.ValidDay(f.value) {
					return &UsageError{Msg: fmt.Sprintf(
						"eg unreviewed 的 %s=%q 不是 YYYY-MM-DD 形态的日期，"+
							"或是日历上不存在的日期（月份须 01–12、日须在该年该月的实际天数内，"+
							"闰年按公历真实规则判定）", f.name, f.value)}
				}
			}
			since, until := inv.String("since"), inv.String("until")
			if since != "" && until != "" && since > until {
				return &UsageError{Msg: fmt.Sprintf(
					"eg unreviewed 的 --since=%s 晚于 --until=%s（闭区间为空）", since, until)}
			}
			return nil
		},
	}
}

// runUnreviewed 实现 eg unreviewed：扫描 → 组装只读快照 → 筛选器判定 → 渲染。
func (r *Root) runUnreviewed(inv *Invocation) (*Result, error) {
	domain := inv.String("domain")
	// EG-DOM-03：领域必须已登记（与 eg search 同一条口径，eg 绝不自选领域，
	// 也绝不把「领域拼错」悄悄降级成全库）。
	if domain != "" && !inv.Config.HasDomain(domain) {
		return nil, &UsageError{Msg: fmt.Sprintf(
			"领域 %s 未登记在 %s 的 domains 里：请先 eg config set domains <d1,d2>（eg 绝不自选领域）",
			domain, ConfigFileName)}
	}

	opt := query.ScanOptions{IncludeNotes: true}
	if domain != "" {
		opt.Domains = []string{domain}
	}
	scan, err := query.VaultScan(inv.VaultRoot, opt)
	if err != nil {
		return nil, err
	}

	spec := filter.Spec{
		Domain: domain, Tags: captureTags(inv),
		Since: inv.String("since"), Until: inv.String("until"),
	}
	hits, ferr := filter.Select(unreviewedCandidates(scan), spec)
	if ferr != nil {
		// 时刻不可比较是**事实问题**（frontmatter 内容），不是用法问题：判校验失败退 2，
		// 且不静默跳过——宁可如实报错，也不产出一份口径不明的清单。
		return nil, &ValidationError{
			Msg: "未过目清单无法判定（零写入）：" + ferr.Error(),
			Diags: []Diagnostic{{
				Code: E20, Level: LevelError, Path: "eg unreviewed", OpIndex: NonOpDiagnostic,
				Message: oneLineReason(ferr.Error()),
			}},
		}
	}

	rows := make([]UnreviewedRow, 0, len(hits))
	for _, c := range hits {
		rows = append(rows, UnreviewedRow{
			ID: c.ID, Kind: c.Kind, Domain: c.Domain, Path: c.Path,
			Tags: tagsOrEmpty(c.Tags), UpdatedAt: c.UpdatedAt, ReviewedAt: c.ReviewedAt,
			Unreviewed: true,
		})
	}
	sortUnreviewedRows(rows)

	out := &Result{
		Data: map[string]interface{}{
			model.FieldUnreviewed: rows,
			"total":               len(rows),
			"scanned_files":       scan.ScannedFiles,
			"skipped_files":       scan.SkippedFiles,
		},
		DataOrder: UnreviewedDataKeys(),
	}
	out.Summary = append(out.Summary, fmt.Sprintf(
		"unreviewed：%d 个产物未过目（扫描 %d 个 .md，跳过 %d 个；只读，零写入零 commit，未写 %s）",
		len(rows), scan.ScannedFiles, scan.SkippedFiles, model.FMKeyReviewedAt))
	if len(rows) == 0 {
		out.Summary = append(out.Summary, "无未过目产物：清单为空、total = 0（仍退 0）")
	}
	for _, row := range rows {
		out.Summary = append(out.Summary, unreviewedRowLine(row))
	}
	out.Warnings = append(out.Warnings, queryDiagnostics(scan.Diagnostics)...)
	return out, nil
}

// UnreviewedDataKeys 是 `unreviewed` 的 data 键次序（键序即本命令的表次序，供逐字反证）。
func UnreviewedDataKeys() []string {
	return []string{model.FieldUnreviewed, "total", "scanned_files", "skipped_files"}
}

// unreviewedCandidates 把一次扫描的卡与笔记组装成筛选器的只读快照。
//
// 组装即**纯搬运**：不做任何判定、不补任何缺省值（缺 reviewed_at 就是空串）。
func unreviewedCandidates(scan *query.ScanResult) []filter.Candidate {
	out := make([]filter.Candidate, 0, len(scan.Cards)+len(scan.Notes))
	for _, c := range scan.Cards {
		out = append(out, filter.Candidate{
			ID: c.ID, Kind: unreviewedKindCard, Path: c.Path, Domain: c.Domain,
			Tags: c.Tags, UpdatedAt: c.UpdatedAt, ReviewedAt: c.ReviewedAt,
		})
	}
	for _, n := range scan.Notes {
		out = append(out, filter.Candidate{
			ID: n.ID, Kind: unreviewedKindNote, Path: n.Path, Domain: n.Domain,
			Tags: n.Tags, UpdatedAt: n.UpdatedAt, ReviewedAt: n.ReviewedAt,
		})
	}
	return out
}

// sortUnreviewedRows 排两级全序：updated_at 倒序 → id 升序。
//
// 只用既有事实排序，**不引入未过目信号作为权重**（ADR-20），也不改动 M2 已冻结的
// 三个查询命令的排序口径。
func sortUnreviewedRows(rows []UnreviewedRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].UpdatedAt != rows[j].UpdatedAt {
			return rows[i].UpdatedAt > rows[j].UpdatedAt
		}
		return rows[i].ID < rows[j].ID
	})
}

// unreviewedRowLine 渲染一行：`<id>  <kind>  [<domain>] <tags>  updated_at=…  reviewed_at=<值|(无)>`。
// 与 --json 同源同事实：两种渲染吃的是同一份 rows，只是格式不同。
func unreviewedRowLine(row UnreviewedRow) string {
	reviewed := row.ReviewedAt
	if reviewed == "" {
		reviewed = "(无)"
	}
	return fmt.Sprintf("%s  %s  [%s] %s  updated_at=%s  %s=%s",
		row.ID, row.Kind, row.Domain, strings.Join(row.Tags, ","),
		row.UpdatedAt, model.FMKeyReviewedAt, reviewed)
}

// tagsOrEmpty 把 nil 归一成空数组：--json 里 tags 无值时是 `[]` 而不是 `null`。
func tagsOrEmpty(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}
