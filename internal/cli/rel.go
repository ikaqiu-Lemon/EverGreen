package cli

// M1 占位命令：`rel`（**读路径与 `rel add|remove` 写路径一并占位**）。
//
// 口径与 `milestones/M-001-m1.md` 差异表、`EPIC.md` M2 行、合同 §1.8 逐字一致：
// `rel` 按 §7.1 注册进命令树并出现在 `eg --help`，但正向读 `relations[]` + 反向全库扫描
// 的读路径、以及 `rel add|remove`（verb = relate）的写路径，**M1 均不实现**，
// 被调用统一输出「M1 未实现（S1 命令，M2 落地）」并退 1。
//
// 理由：反向关系视图需要 query 包的全库反查实现，M1 的 18 个 task 中无任何 task 承担该实现；
// M1 若把读路径当已实现注册，就会交付一个「既不报未实现、也没有实现」的命令。
// 反向查询的实现随 `rel` 整体在 M2 立项。
//
// 本文件因此**不得**引用查询包：门禁对 `internal/cli/rel*` 做 import 反证（P1-2），
// 命中查询包的 import 路径即判失败。

func relCommand() *Command {
	return &Command{
		Name:        "rel",
		Display:     "rel",
		Summary:     "论证关系：读 relations[] 与反向视图 / add|remove 写入",
		ReadOnly:    true,
		Placeholder: true,
		Subs:        []string{"add", "remove"},
		Usage: `eg rel <k-id>                                   # 读：正向 relations[] + 反向全库扫描（` + MilestoneTag + `）
eg rel add <from> <type> <to> --reason <text>    # 写（` + MilestoneTag + `，verb = relate）
eg rel remove <from> <type> <to>                 # 写（` + MilestoneTag + `）

参数（合同 §1.8）：
  <k-id>            读路径：知识卡 ID
  <from> <to>       写路径：知识卡 ID
  <type>            derives | supports | limits | opposing（冻结合同 F4）
  --reason <text>   rel add 必填

M1 状态：` + PlaceholderNotice + `。读路径与写路径**均不实现**，
五种调用形态（search / card show / rel / rel add / rel remove）一律退 1，零副作用。
`,
		Flags: func(fs *flagSet) {
			fs.String("reason", "", "关系理由（rel add 必填）")
		},
	}
}
