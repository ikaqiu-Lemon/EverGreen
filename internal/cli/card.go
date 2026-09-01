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

	"evergreen/internal/model"
	"evergreen/internal/query"
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
		Usage: `eg card show <k-id> [--json]

参数（M2 查询合同 §2.1）：
  <k-id>   是；目标卡稳定 ID；格式非法或卡不存在 → 退 1（零副作用）

定位走全库扫描（卡 ID 全库唯一，不限定领域）。同一 ID 出现在两处 → 取路径字典序最小者
并记一条 Q1 如实说明重复，不静默择一。
data 键序固定（§2.2）：id / title / domain / status / deprecated / created_at / updated_at /
path / tags / markers / sections / sources / relations_out / relations_in。
sections 键序固定为五分区声明序（F5）：知识内容 → 解释与依据 → 条件与边界 → 用户补充 → 理解自检；
分区缺失时键仍在、值为空串，文本模式标注「（本分区缺失）」——分区缺失不属于 Q 系列。
relations_out[] = 本卡 frontmatter relations[]；relations_in[] = 全库反向扫描（不走索引）。
排序（§3.3）：type 固定次序 opposing → limits → supports → derives，再按对端 ID 升序。
失效卡（§2.2）：markers = ["` + query.MarkerDeprecated + `"]、--json 置 deprecated: true。
悬空引用仍照实展示并标注「目标不存在」，同时记一条 Q2 warning，退出码仍 0。
只读：零文件变化、零 commit。退出码：0 | 1（参数非法 / 卡不存在 / 未配置 default_domain）
`,
		Validate: func(inv *Invocation) error {
			if len(inv.Args) != 1 {
				return &UsageError{Msg: fmt.Sprintf(
					"eg card show 需要恰一个位置参数 <k-id>，实际 %d 个：%v", len(inv.Args), inv.Args)}
			}
			return nil
		},
	}
}

// runCardShow 实现 eg card show。
func (r *Root) runCardShow(inv *Invocation) (*Result, error) {
	if inv.Sub != "show" {
		// SubRequired 已在框架层拦住其它子动词；这里是纵深防御，绝不静默降级。
		return nil, &UsageError{Msg: fmt.Sprintf("eg card 只支持子命令 show，实际 %q", inv.Sub)}
	}
	view, err := query.ShowCard(inv.VaultRoot, model.CardID(inv.Args[0]))
	if err != nil {
		if errors.Is(err, query.ErrInvalidCardID) || errors.Is(err, query.ErrCardNotFound) {
			return nil, &UsageError{Msg: err.Error()}
		}
		return nil, err
	}

	c := view.Card
	out := &Result{
		Data: map[string]interface{}{
			"id": c.ID, "title": c.Title, "domain": c.Domain, "status": c.Status,
			"deprecated": c.Deprecated, "created_at": c.CreatedAt, "updated_at": c.UpdatedAt,
			"path": c.Path, "tags": c.Tags, "markers": c.Markers,
			"sections": c.Sections, "sources": c.Sources,
			"relations_out": c.RelationsOut, "relations_in": c.RelationsIn,
		},
		DataOrder: query.CardDataKeys(),
	}
	out.Summary = append(out.Summary, cardSummaryLines(view)...)
	out.Warnings = append(out.Warnings, queryDiagnostics(view.Diagnostics)...)
	return out, nil
}

// cardSummaryLines 渲染人类可读视图。与 --json **同源同事实**：吃的是同一份 view.Card，
// 每一行都能在 data 里逐字对上（标记、分区、正反向关系、材料出处）。
func cardSummaryLines(view *query.CardShowResult) []string {
	c := view.Card
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
	if len(c.RelationsOut) == 0 {
		lines = append(lines, "正向关系 relations_out[]：无")
	}
	for _, e := range c.RelationsOut {
		note := ""
		if missing[e.Target] {
			note = "（目标不存在）"
		}
		lines = append(lines, fmt.Sprintf("正向关系：%s --%s--> %s%s  理由：%s",
			e.From, e.Type, e.Target, note, e.Reason))
	}
	if len(c.RelationsIn) == 0 {
		lines = append(lines, "反向关系 relations_in[]：无")
	}
	for _, e := range c.RelationsIn {
		lines = append(lines, fmt.Sprintf("反向关系：%s --%s--> %s  理由：%s（来源卡：%s）",
			e.From, e.Type, e.Target, e.Reason, e.Path))
	}
	lines = append(lines, fmt.Sprintf(
		"card show：扫描 %d 个 .md，跳过 %d 个（只读，零写入零 commit）",
		view.ScannedFiles, view.SkippedFiles))
	return lines
}
