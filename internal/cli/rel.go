package cli

// `eg rel` 读路径的命令定义与业务实现（M2 查询合同
// `2026-09-19-m2-query-contract.md` §3；CLI 合同 §1.8 / §1.10 / §3 / §4；T-…-023）。
//
// 正向读本卡 frontmatter 的 `relations[]`，反向靠 **Markdown 全库扫描**反查「谁指向了我」，
// 悬空引用如实报 Q2；`opposing` 单向存储，读路径不补对称条目。
//
// **M1 期口径的变更说明（同 commit 生效，不留自相矛盾的禁令）**：M1 的文件头曾写死
// 「本文件不得引用查询包：门禁对 `internal/cli/rel*` 做 import 反证（P1-2）」——
// 那是「rel 读路径 M1 不实现」的占位守卫。本 task 落地读路径后该禁令必然失效，
// 因此改写为 M2 口径：`internal/cli/rel*` **不得引用写入 API**
// （`internal/store` / `internal/plan` / `internal/git` 与 `Apply*` / `Write*` / `Commit`），
// 引用查询包则是本命令的正常取数方式。对应反证由 cli_test.go 的 P1-2 用例按新语义承接。
//
// 子命令边界：`rel add` 由 T-…-024 落地、`rel remove` 按 `M-002-m2.md` 的阶段裁决归 M3/S2，
// 二者在本 task 内**仍返回未实现**（措辞沿用 PlaceholderNotice，退 1、零写入零 commit），
// 且**绝不落进读路径**。

import (
	"errors"
	"fmt"

	"evergreen/internal/model"
	"evergreen/internal/query"
)

// relSubcommands 是 rel 的写子命令（本 task 不实现，只保留入口）。
var relSubcommands = []string{"add", "remove"}

func relCommand() *Command {
	return &Command{
		Name:     "rel",
		Display:  "rel",
		Summary:  "论证关系：读 relations[] 与反向视图 / add|remove 写入",
		Owner:    "T-evergreen.s1_main_flow-158614-023",
		ReadOnly: true,
		Subs:     relSubcommands,
		Usage: `eg rel <k-id> [--to <id>] [--json]           # 读：正向 relations[] + 反向全库扫描
eg rel add <from> <type> <to> --reason <text>    # 写（` + MilestoneTag + `，verb = relate）
eg rel remove <from> <type> <to>                 # 写（` + MilestoneTag + `）

参数（M2 查询合同 §3.1）：
  <k-id>            读路径：起点卡 ID；卡不存在 → 退 1
  --to <id>         否；只保留对端 == 该 ID 的条目（正反向同时过滤）；ID 不存在 → 结果为空 + 一条 Q2
  <from> <to>       写路径：知识卡 ID
  <type>            derives | supports | limits | opposing（冻结合同 F4）
  --reason <text>   rel add 必填

data 键序固定（§3.1）：id / relations_out / relations_in / scanned_files / skipped_files；
条目键恰 from / type / target / reason / path 五项，正反向同构。
正向 = 本卡 frontmatter relations[]（from 恒为本卡）；反向 = 全库 Markdown 扫描（不走索引）。
排序（§3.3 两级）：type 固定次序 opposing → limits → supports → derives，再按对端 ID 升序。
opposing 单向存储：库中只有一条记录，读路径不补对称条目、不去重合并。
悬空引用仍照实展示并标注「目标不存在」，同时记一条 Q2 warning，退出码仍 0。
只读：零文件变化、零 commit。退出码：0 | 1（参数非法 / 卡不存在 / 子命令未实现）
`,
		Flags: func(fs *flagSet) {
			fs.String("reason", "", "关系理由（rel add 必填）")
			fs.String("to", "", "只保留对端 == 该 ID 的条目（读路径）")
		},
		Validate: func(inv *Invocation) error {
			// 写子命令在**守卫之前**短路：措辞与 M1 逐字一致，且不解析 vault、不碰任何文件。
			if inv.Sub != "" {
				return placeholderError(inv.Cmd, inv.Sub)
			}
			if len(inv.Args) != 1 {
				return &UsageError{Msg: fmt.Sprintf(
					"eg rel 需要恰一个位置参数 <k-id>，实际 %d 个：%v", len(inv.Args), inv.Args)}
			}
			return nil
		},
	}
}

// runRel 实现 eg rel <k-id> 的读路径。
func (r *Root) runRel(inv *Invocation) (*Result, error) {
	if inv.Sub != "" {
		// 纵深防御：Validate 已在守卫前短路，写子命令绝不落进读路径。
		return nil, placeholderError(inv.Cmd, inv.Sub)
	}
	res, err := query.RelView(inv.VaultRoot, query.RelRequest{
		ID: model.CardID(inv.Args[0]), To: inv.String("to"),
	})
	if err != nil {
		if errors.Is(err, query.ErrInvalidCardID) || errors.Is(err, query.ErrCardNotFound) {
			return nil, &UsageError{Msg: err.Error()}
		}
		return nil, err
	}

	d := res.Data
	out := &Result{
		Data: map[string]interface{}{
			"id": d.ID, "relations_out": d.RelationsOut, "relations_in": d.RelationsIn,
			"scanned_files": d.ScannedFiles, "skipped_files": d.SkippedFiles,
		},
		DataOrder: query.RelDataKeys(),
	}
	out.Summary = append(out.Summary, relSummaryLines(res)...)
	out.Warnings = append(out.Warnings, queryDiagnostics(res.Diagnostics)...)
	return out, nil
}

// relSummaryLines 渲染人类可读关系视图。与 --json **同源同事实**：吃的是同一份 res.Data，
// 每一行都能在 data 里逐字对上（条目数、类型次序、对端 ID、理由、来源路径）。
func relSummaryLines(res *query.RelResult) []string {
	d := res.Data
	lines := []string{fmt.Sprintf(
		"rel：%s 的正向 %d 条 / 反向 %d 条（扫描 %d 个 .md，跳过 %d 个；只读，零写入零 commit）",
		d.ID, len(d.RelationsOut), len(d.RelationsIn), d.ScannedFiles, d.SkippedFiles)}
	missing := map[string]bool{}
	for _, id := range res.MissingTargets {
		missing[id] = true
	}
	if len(d.RelationsOut) == 0 {
		lines = append(lines, "正向关系 relations_out[]：无")
	}
	for _, e := range d.RelationsOut {
		note := ""
		if missing[e.Target] {
			note = "（目标不存在）"
		}
		lines = append(lines, fmt.Sprintf("正向关系：%s --%s--> %s%s  理由：%s",
			e.From, e.Type, e.Target, note, e.Reason))
	}
	if len(d.RelationsIn) == 0 {
		lines = append(lines, "反向关系 relations_in[]：无")
	}
	for _, e := range d.RelationsIn {
		lines = append(lines, fmt.Sprintf("反向关系：%s --%s--> %s  理由：%s（来源卡：%s）",
			e.From, e.Type, e.Target, e.Reason, e.Path))
	}
	return lines
}
