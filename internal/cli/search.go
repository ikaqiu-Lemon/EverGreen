package cli

// `eg search` 的命令定义与业务实现（M2 查询合同
// `2026-09-19-m2-query-contract.md` §1；CLI 合同 §1.6 / §1.10 / §3 / §4；T-…-021）。
//
// M1 期本命令仅登记参数表（占位分支已由本 task 摘除），M2 换成真实实现：
// 关键词加过滤、四级全序排序、失效卡同等可见并标 `[失效]`、Q 类诊断如实透出。
//
// **只读零副作用**（合同 §6）：本文件没有任何 store / plan / git 写口调用，
// 不产生 commit、不改任何字节；退出码只可能 0 / 1（2 / 3 / 4 的语义都涉及写入或提交）。
// 取数一律走 internal/query 的 VaultScan 扫描底座（T-…-020），不另起一套遍历、不建索引。

import (
	"errors"
	"fmt"
	"strings"

	"evergreen/internal/query"
)

// searchCommand 注册 `eg search`（参数表逐项对齐 M2 合同 §1.1）。
func searchCommand() *Command {
	return &Command{
		Name:     "search",
		Display:  "search",
		Summary:  "按关键词检索知识卡（只读；失效卡同等可见并标 [失效]）",
		Owner:    "T-evergreen.s1_main_flow-158614-021",
		ReadOnly: true,
		Usage: `eg search <query> [--domain <d>] [--tag <t>]... [--since <YYYY-MM-DD>] [--until <YYYY-MM-DD>] [--json]

参数（M2 查询合同 §1.1）：
  <query>                是；关键词串，空串 / 只含空白 → 退 1、零输出内容
  --domain <d>           否，默认全库；值不在 evergreen.yml 的 domains 内 → 退 1（eg 绝不自选领域）
  --tag <t>              否，可重复；多次给出为 AND（卡须同时含全部标签），逐字相等比较
  --since <YYYY-MM-DD>   否；updated_at 的日期部分 ≥ 该值（闭区间）
  --until <YYYY-MM-DD>   否；updated_at 的日期部分 ≤ 该值（闭区间）

搜索面 = 知识卡（domains/<d>/knowledge/**.md）；材料笔记与原文不进 hits[]。
匹配分（§1.3）：命中 title +3 / tags +2 / 正文 +1，每词每字段至多一次。
排序（§1.4 四级全序）：匹配分降序 → updated_at 倒序 → created_at 倒序 → id 升序；不截断不分页。
失效卡（§1.5）同等可见：文本模式前缀 [失效]、--json 置 deprecated: true；无隐藏开关。
只读：零文件变化、零 commit。不可解析文件一律记 Q1 并计入 skipped_files，不静默跳过。
退出码：0（零命中也退 0） | 1 参数非法 / 领域未登记 / 未配置 default_domain
`,
		Flags: func(fs *flagSet) {
			fs.String("domain", "", "限定领域（默认全库）")
			var tags stringList
			fs.Var(&tags, "tag", "标签过滤（可重复，AND）")
			fs.String("since", "", "updated_at 日期下界（YYYY-MM-DD，闭区间）")
			fs.String("until", "", "updated_at 日期上界（YYYY-MM-DD，闭区间）")
		},
		Validate: func(inv *Invocation) error {
			if len(inv.Args) != 1 {
				return &UsageError{Msg: fmt.Sprintf(
					"eg search 需要恰一个位置参数 <query>，实际 %d 个：%v", len(inv.Args), inv.Args)}
			}
			return nil
		},
	}
}

// runSearch 实现 eg search。
func (r *Root) runSearch(inv *Invocation) (*Result, error) {
	req := query.SearchRequest{
		Query:  inv.Args[0],
		Domain: inv.String("domain"),
		Tags:   captureTags(inv),
		Since:  inv.String("since"),
		Until:  inv.String("until"),
	}
	// EG-DOM-03：领域必须已登记在 evergreen.yml 的 domains 里（合同 §1.1）——
	// eg 绝不自选领域，也绝不把「领域拼错」悄悄降级成全库搜索。
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

	out := &Result{
		Data: map[string]interface{}{
			"hits":          res.Hits,
			"total":         res.Total,
			"scanned_files": res.ScannedFiles,
			"skipped_files": res.SkippedFiles,
		},
		DataOrder: query.SearchDataKeys(),
	}
	out.Summary = append(out.Summary, fmt.Sprintf(
		"search：query=%q，命中 %d 张卡（扫描 %d 个 .md，跳过 %d 个；只读，零写入零 commit）",
		req.Query, res.Total, res.ScannedFiles, res.SkippedFiles))
	if res.Total == 0 {
		out.Summary = append(out.Summary, "无匹配：hits 为空、total = 0（零知识结果合法，仍退 0）")
	}
	// 人类可读的每行一条命中：`<标记><id>  <title>  [<domain>] <tags>  <updated_at>`（合同 §1.2）。
	// 与 --json 同源同事实：两种渲染吃的是同一份 res.Hits，只是格式不同。
	for _, h := range res.Hits {
		out.Summary = append(out.Summary, searchHitLine(h))
	}
	out.Warnings = append(out.Warnings, queryDiagnostics(res.Diagnostics)...)
	return out, nil
}

// searchHitLine 渲染一条命中（合同 §1.2 末段的形态）。
// `<标记>` 仅在失效卡时为 `[失效]`；`[已删除]` / `[未过目]` / `[材料支持不足]` 属 S2 / S3，M2 不输出。
func searchHitLine(h query.SearchHit) string {
	marker := ""
	if h.Deprecated {
		marker = MarkerDeprecated
	}
	return fmt.Sprintf("%s%s  %s  [%s] %s  %s",
		marker, h.ID, h.Title, h.Domain, strings.Join(h.Tags, ","), h.UpdatedAt)
}

// MarkerDeprecated 是 M2 唯一的显著标记（合同 §1.5 / §2.2）：失效卡前缀。
// 取值与 internal/query 同源，避免两处各写一份字面量。
// `[已删除]` / `[未过目]` / `[材料支持不足]` 属 S2 / S3，M2 **不输出**。
const MarkerDeprecated = query.MarkerDeprecated

// queryDiagnostics 把 internal/query 的 Q 系列诊断原样翻译成 CLI 诊断条目
// （合同 §5：code 填 Q1 / Q2 / Q3，level 一律 warning，op_index 填 -1）。
//
// 三个只读查询命令（search / card show / rel）共用本函数：诊断在 --json 的 warnings[]
// 与人类可读输出里**同源同事实**，且 Q 类**不影响退出码**（仍退 0）。
func queryDiagnostics(diags []query.Diagnostic) []Diagnostic {
	out := make([]Diagnostic, 0, len(diags))
	for _, d := range diags {
		out = append(out, Diagnostic{
			Code: d.Code, Level: LevelWarning, Path: d.Path,
			OpIndex: NonOpDiagnostic, Message: d.Message,
		})
	}
	return out
}
