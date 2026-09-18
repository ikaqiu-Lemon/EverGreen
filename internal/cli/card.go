package cli

// `eg card show` 的命令定义与业务实现（M2 查询合同
// `2026-09-19-m2-query-contract.md` §2；CLI 合同 §1.7 / §1.10 / §3 / §4；T-…-022）。
//
// 一屏看完这张卡：五分区正文、它引用了谁（relations_out[]）、谁引用了它（relations_in[]）、
// 材料出处（sources[]）、是不是已经失效（markers / deprecated）。
//
// **只读零副作用**（合同 §6）：本文件没有任何 store / plan / git 写口调用，
// 不产生 commit、不改任何字节；退出码只可能 0 / 1。取数一律走 internal/query 的
// CardView（内部复用 T-…-020 的全库 VaultScan），不另起一套遍历、不建索引。

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
)

// cardCommand 注册 `eg card show`（参数表逐项对齐 M2 合同 §2.1）。
func cardCommand() *Command {
	return &Command{
		Name:        "card",
		Display:     "card show",
		Summary:     "查看单张知识卡（只读；五分区 + 正反向关系 + 显著标记）",
		Owner:       "T-evergreen.s1_main_flow-158614-022",
		ReadOnly:    true,
		Subs:        []string{"show"},
		SubRequired: true,
		Usage: `eg card show <k-id> [--include-deprecated] [--limit <n>] [--offset <n>] [--json]

参数（M2 查询合同 §2.1；--include-deprecated 见 owner 裁决 A-38/A-39，归 M4 规划）：
  <k-id>                是；目标卡稳定 ID；格式非法或卡不存在 → 退 1（零副作用）
  --include-deprecated  否；默认隐藏对端 deprecated 的关系条目，加此 flag 才展示（仍带 [失效] 标记）；
                        只放开 deprecated 维度，不影响已删除维度（正交）；无短选项无别名
  --limit <n>           否，默认 50；**本次最多返回的关系条数**（0 = 不限量）
  --offset <n>          否，默认 0；跳过的关系条数；超出总数返回空列表且仍退 0
                        两者为负数 / 非整数 → 参数错，退 1、零写入（M5 合同 §8.2 经 I-…-008 改判，原写 4）

定位走全库扫描（卡 ID 全库唯一，不限定领域）。同一 ID 出现在两处 → 取路径字典序最小者
并记一条 Q1 如实说明重复，不静默择一。
data 键序固定（§2.2 + I-…-007）：id / title / domain / status / deprecated / created_at / updated_at /
path / tags / markers / sections / unknown_sections / sources / relations_out / relations_in。
sections 键序固定为知识卡三分区声明序（F5 + 契约 D-7）：知识内容 → 条件与边界 → 用户补充；
分区缺失时键仍在、值为空串，文本模式标注「（本分区缺失）」——分区缺失不属于 Q 系列。
unknown_sections 是**非固定分区**（v1 存量被移除的 解释与依据 / 理解自检、以及用户自建 H2）的有序投影，
与固定 sections 正交、不扩张 sections；无非固定分区时是空数组 []。
relations_out[] = 本卡 frontmatter relations[]；relations_in[] = 全库反向扫描（不走索引）。
排序（§3.3 + M5 §7.4 四级全序）：type 固定次序 opposing → limits → supports → derives →
对端 ID 升序 → path 升序 → 条目输出全等标识（from|type|target|reason）升序，与后端无关可复算。
分页（M5 合同 §8.2，S4 起）：--limit 是**一个全局上限**——正反向两个列表先合并成一条确定序列
（relations_out 段在前、relations_in 段在后）再全局取 [offset, offset+limit)，因此一次读最多返回
limit 条条目，**不会**因为有两个列表而给到 2×limit 条；截断只判一次并产恰一条 W25。
失效卡（§2.2）：markers = ["` + query.MarkerDeprecated + `"]、--json 置 deprecated: true。
已删除 / 未过目（提案与状态合同 §6.2）：markers 按固定顺序叠加，--json 同步 deleted / unreviewed
两个布尔字段；三个维度正交，显示不由一个推断另一个。已删除卡在此照常展示（可显式查看）。
悬空引用仍照实展示并标注「目标不存在」，同时记一条 Q2 warning，退出码仍 0。
只读：零文件变化、零 commit。退出码：0 | 1（参数非法 / 卡不存在 / 未配置 default_domain）
`,
		Validate: func(inv *Invocation) error {
			if len(inv.Args) != 1 {
				return &UsageError{Msg: fmt.Sprintf(
					"eg card show 需要恰一个位置参数 <k-id>，实际 %d 个：%v", len(inv.Args), inv.Args)}
			}
			// 合法的观点 ID（o-*）走**观点**读面：card show 只看知识卡，绝不读 vault 去
			// 找一个注定不是知识卡的 o-*。在 Validate 阶段就退 1 并显式改派到 `eg opinion show`，
			// 零文件变化、零 commit（其它 k-id / 形态非法串行为不变：仍落到 runCardShow 的
			// query 层，由 ErrInvalidCardID / ErrCardNotFound 判定）。
			if model.OpinionID(inv.Args[0]).Valid() {
				return &UsageError{Msg: fmt.Sprintf(
					"%q 是观点 ID（o-*），不是知识卡：请改用 eg opinion show %s（card show 只查知识卡）",
					inv.Args[0], inv.Args[0])}
			}
			return nil
		},
		Flags: func(fs *flagSet) {
			// --include-deprecated（T-…-061，owner 裁决②）：读路径 flag，布尔，默认 false，
			// 无短选项无别名。作用面恰 eg rel / eg card show 两命令（G7）。与其它命令正交。
			fs.Bool("include-deprecated", false, "展示对端 deprecated 的关系条目（默认隐藏；不影响已删除维度）")
			// 分页（S4 · T-…-068，合同 §8.2）：注册点唯一，见 page.go。
			pageFlags(fs)
		},
	}
}

// runCardShow 实现 eg card show。
func (r *Root) runCardShow(inv *Invocation) (*Result, error) {
	if inv.Sub != "show" {
		// SubRequired 已在框架层拦住其它子动词；这里是纵深防御，绝不静默降级。
		return nil, &UsageError{Msg: fmt.Sprintf("eg card 只支持子命令 show，实际 %q", inv.Sub)}
	}
	// ShowCardWith = 注入 A-44 水位线口径（B3 content_hash + Git HEAD）的单卡视图：
	// 生产读路径恒注入，否则证不出索引新鲜度而恒走全量扫描（见 query.ShowCard 注释）。
	page, err := pageSpecFrom(inv)
	if err != nil {
		return nil, err
	}
	view, err := query.ShowCardPaged(inv.VaultRoot, model.CardID(inv.Args[0]),
		readIndexDeps(inv.VaultRoot), page,
		query.VisibilityPolicy{IncludeDeprecated: inv.String("include-deprecated") == "true"})
	if err != nil {
		if errors.Is(err, query.ErrInvalidCardID) || errors.Is(err, query.ErrCardNotFound) {
			return nil, &UsageError{Msg: err.Error()}
		}
		return nil, err
	}

	// 显式查看是「可显式查看」这一列的入口（提案与状态合同 §5.1 第 3 / 4 行）：
	// 已删除卡照常展示，并按 §6.2 输出双标记；过目维度在此注入（判定在 filter 单文件）。
	st, err := cardMarkerState(view)
	if err != nil {
		return nil, err
	}
	c := query.WithUnreviewed(view.Card, st.Unreviewed)
	out := &Result{
		Data: map[string]interface{}{
			"id": c.ID, "title": c.Title, "domain": c.Domain, "status": c.Status,
			"deprecated": c.Deprecated, "created_at": c.CreatedAt, "updated_at": c.UpdatedAt,
			"path": c.Path, "tags": c.Tags, "markers": c.Markers,
			"sections": c.Sections, "unknown_sections": c.UnknownSections, "sources": c.Sources,
			"relations_out": c.RelationsOut, "relations_in": c.RelationsIn,
			query.FieldDeleted: c.Deleted, query.FieldUnreviewed: c.Unreviewed,
		},
		DataOrder: query.CardDataKeys(),
	}
	out.Summary = append(out.Summary, cardSummaryLines(view, c)...)
	out.Summary = append(out.Summary, pageSummaryLines(view.Page, "关系条目")...)
	out.Warnings = append(out.Warnings, queryDiagnostics(view.Diagnostics)...)
	return out, nil
}

// cardSummaryLines 渲染人类可读视图。与 --json **同源同事实**：吃的是同一份 view.Card，
// 每一行都能在 data 里逐字对上（标记、分区、正反向关系、材料出处）。
// 第二个入参是**已注入过目维度**的卡视图：标记前缀必须与 --json 的三个布尔字段同源同事实。
func cardSummaryLines(view *query.CardShowResult, c query.CardDetail) []string {
	lines := []string{
		fmt.Sprintf("%s%s  %s  [%s] %s", strings.Join(c.Markers, ""), c.ID, c.Title, c.Domain,
			strings.Join(c.Tags, ",")),
		fmt.Sprintf("状态：%s（deprecated=%t）  created_at=%s  updated_at=%s  路径：%s",
			c.Status, c.Deprecated, c.CreatedAt, c.UpdatedAt, c.Path),
	}
	for _, name := range c.Sections.Keys() {
		if c.Sections.Missing(name) {
			lines = append(lines, fmt.Sprintf("分区 %s：（本分区缺失）", name))
			continue
		}
		lines = append(lines, fmt.Sprintf("分区 %s：%s", name, firstLine(c.Sections.Get(name))))
	}
	// 非固定分区（v1 存量被移除的分区 + 用户自建 H2）：与 --json 的 unknown_sections 同源，
	// 用共享 helper 渲染**完整正文**（不截首行、不只报数量）；空数组不产生任何行（I-…-007）。
	lines = append(lines, unknownSectionLines(c.UnknownSections)...)
	if len(c.Sources) == 0 {
		lines = append(lines, "材料出处 sources[]：无")
	}
	for _, s := range c.Sources {
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
	if len(c.RelationsOut) == 0 {
		lines = append(lines, "正向关系 relations_out[]：无")
	}
	for _, e := range c.RelationsOut {
		note := ""
		if missing[e.Target] {
			note = "（目标不存在）"
		}
		lines = append(lines, fmt.Sprintf("正向关系：%s --%s--> %s%s%s  理由：%s",
			e.From, e.Type, e.Target, peerDeprecatedMark(deprecated, e.Target), note, e.Reason))
	}
	if len(c.RelationsIn) == 0 {
		lines = append(lines, "反向关系 relations_in[]：无")
	}
	for _, e := range c.RelationsIn {
		lines = append(lines, fmt.Sprintf("反向关系：%s%s --%s--> %s  理由：%s（来源卡：%s）",
			e.From, peerDeprecatedMark(deprecated, e.From), e.Type, e.Target, e.Reason, e.Path))
	}
	lines = append(lines, fmt.Sprintf(
		"card show：扫描 %d 个 .md，跳过 %d 个（只读，零写入零 commit）",
		view.ScannedFiles, view.SkippedFiles))
	return lines
}
