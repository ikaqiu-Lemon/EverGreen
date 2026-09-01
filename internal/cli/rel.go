package cli

// `eg rel` 的命令定义与业务实现：读路径（M2 查询合同 §3；T-…-023）+ `rel add` 写路径
// （同合同 §4；T-…-024，实现在 rel_add.go）+ `rel remove` 的 M3/S2 占位（同合同 §7）。
//
// 正向读本卡 frontmatter 的 `relations[]`，反向靠 **Markdown 全库扫描**反查「谁指向了我」，
// 悬空引用如实报 Q2；`opposing` 单向存储，读路径不补对称条目。
//
// **M1 期口径的变更说明（同 commit 生效，不留自相矛盾的禁令）**：M1 的文件头曾写死
// 「本文件不得引用查询包：门禁对 `internal/cli/rel*` 做 import 反证（P1-2）」——
// 那是「rel 读路径 M1 不实现」的占位守卫。T-…-023 落地读路径后该禁令必然失效，
// 因此改写为 M2 口径：本文件（`rel` 读路径）**不得引用写入 API**
// （`internal/store` / `internal/plan` / `internal/git` 与 `Apply*` / `Write*` / `Commit`）。
// T-…-024 落地 `rel add` 后禁令再收紧一次并**按文件分工**：写路径文件 `rel_add.go`
// 只允许经 `internal/plan` 走 ChangePlan 链路，`internal/store` 的写 API 一律不得直呼——
// 对应反证由 cli_test.go 的 P1-2 用例按新语义承接。
//
// 子命令边界：
//   - `rel add`：本文件只做参数形态校验 + 分发，业务在 rel_add.go（verb = relate，一次 commit）；
//   - `rel remove`：按 `M-002-m2.md` 与合同 §7 的阶段裁决归 **M3/S2**，M2 不做——
//     调用即返回固定文案 RemovePlaceholderNotice、退 1、**零写入零 commit**，
//     且不引入 `remove_relation` op（该 op 同属 M3/S2）。

import (
	"errors"
	"fmt"

	"evergreen/internal/model"
	"evergreen/internal/query"
)

// relSubcommands 是 rel 的写子命令（add 已落地；remove 是 M3/S2 占位）。
var relSubcommands = []string{"add", "remove"}

// RemovePlaceholderNotice 是 `eg rel remove` 的**固定占位文案**（M2 查询合同 §7）。
//
// 与 M1 的 PlaceholderNotice 刻意区分：那条说的是「S1 命令，M2 落地」，而关系删除
// 只能表达为一个 `remove_relation` op，该 op 在 ChangePlan 合同 §4.5 属 S2，
// 故命令与 op 一并归 M3/S2——措辞不得回写成 M2 口径（门禁对该表述做 grep 反证）。
const RemovePlaceholderNotice = "M3/S2 未实现（关系删除属 remove_relation op，阶段裁决见 M-002-m2.md：" +
	"该 op 属 S2，命令与 op 一并归 M3/S2；M2 不做，本次调用零写入、零 commit）"

func relCommand() *Command {
	return &Command{
		Name:    "rel",
		Display: "rel",
		Summary: "论证关系：读 relations[] 与反向视图 / add 写入（verb=relate）",
		Owner:   "T-evergreen.s1_main_flow-158614-024",
		Subs:    relSubcommands,
		Usage: `eg rel <k-id> [--to <id>] [--json]           # 读：正向 relations[] + 反向全库扫描
eg rel add <from> <type> <to> --reason <text> [--domain <d>] [--json]   # 写（已实现，verb = relate）
eg rel remove <from> <type> <to>                 # ` + RemovePlaceholderNotice + `

参数（M2 查询合同 §3.1 读 / §4.1 写）：
  <k-id>            读路径：起点卡 ID；卡不存在 → 退 1
  --to <id>         否；只保留对端 == 该 ID 的条目（正反向同时过滤）；ID 不存在 → 结果为空 + 一条 Q2
  <from> <to>       写路径：知识卡 ID；不可解析 / 不存在 → E2，target 写成 s-… → E3（退 2，零写入）
  <type>            derives | supports | limits | opposing（冻结合同 F4）；集合外退 1
  --reason <text>   rel add 必填（缺该 flag 退 1；给了空串或等于关系名本身 → W2 照写不拦截）
  --domain <d>      否；只用于 plan 的 domain；未给则取 from 卡所在领域，再回落 default_domain

读（§3.1）data 键序固定：id / relations_out / relations_in / scanned_files / skipped_files；
条目键恰 from / type / target / reason / path 五项，正反向同构。
正向 = 本卡 frontmatter relations[]（from 恒为本卡）；反向 = 全库 Markdown 扫描（不走索引）。
排序（§3.3 两级）：type 固定次序 opposing → limits → supports → derives，再按对端 ID 升序。
opposing 单向存储：库中只有一条记录，读路径不补对称条目、不去重合并。
悬空引用仍照实展示并标注「目标不存在」，同时记一条 Q2 warning，退出码仍 0。
读路径只读：零文件变化、零 commit。

写（§4）：参数在内存里合成一份 ChangePlan（verb=relate + 单条 add_relation op），
经 plan 校验与 executor 展开落到 store 写口，一次 rel add = 一次 relate commit；
opposing 按两端 ID 字典序单向存储（方向被规范化 / 同对已存在均记 W8，不产生第二条）。
退出码：0（含「无实际改动、未提交」）| 1 参数非法 / 未配置领域 / rel remove 占位 |
        2 校验失败（零写入）| 3 B3 跳过（已写部分保留并提交）| 4 commit 失败（不回滚，B4）
`,
		Flags: func(fs *flagSet) {
			fs.String("reason", "", "关系理由（rel add 必填）")
			fs.String("to", "", "只保留对端 == 该 ID 的条目（读路径）")
			fs.String("domain", "", "plan.domain（rel add 可选）")
		},
		Validate: func(inv *Invocation) error {
			switch inv.Sub {
			case "add":
				return validateRelAddArgs(inv)
			case "remove":
				// M3/S2 占位在**守卫之前**短路：不解析 vault、不碰任何文件。
				return removePlaceholderError(inv.Cmd)
			}
			if len(inv.Args) != 1 {
				return &UsageError{Msg: fmt.Sprintf(
					"eg rel 需要恰一个位置参数 <k-id>，实际 %d 个：%v", len(inv.Args), inv.Args)}
			}
			return nil
		},
	}
}

// removePlaceholderError 生成 `eg rel remove` 的 M3/S2 占位错误：退 1、零写入、零 commit。
func removePlaceholderError(cmd *Command) error {
	invoked := "eg " + cmd.Name + " remove"
	return &UsageError{
		Msg: fmt.Sprintf("%s：%s", invoked, RemovePlaceholderNotice),
		Diags: []Diagnostic{{
			Code:    "",
			Level:   LevelError,
			Path:    invoked,
			OpIndex: NonOpDiagnostic,
			Message: RemovePlaceholderNotice,
		}},
	}
}

// runRel 分发 rel 的三种形态：读路径 / add 写入 / remove 占位。
func (r *Root) runRel(inv *Invocation) (*Result, error) {
	switch inv.Sub {
	case "add":
		return r.runRelAdd(inv)
	case "remove":
		// 纵深防御：Validate 已在守卫前短路，占位形态绝不落进任何执行路径。
		return nil, removePlaceholderError(inv.Cmd)
	}
	return r.runRelQuery(inv)
}

// runRelQuery 实现 eg rel <k-id> 的读路径（只读，零副作用）。
func (r *Root) runRelQuery(inv *Invocation) (*Result, error) {
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
