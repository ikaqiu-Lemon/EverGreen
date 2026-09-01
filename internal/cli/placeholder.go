package cli

// 尚未落地命令的占位：`card show`（`rel` 的子命令占位见 rel.go）。
//
// 它们都是技术方案 §7.1 的 **S1 命令**，只是按 §16.1 划归 **M2 实现**，
// 因此文案一律沿用 root.go 的 PlaceholderNotice，
// **禁止改写成任何暗示它们不属于 S1 的口径**（门禁对该措辞做 grep 反证）。
// 占位分支只写 stderr：不碰文件、不产生 commit（只读命令零副作用保证同样覆盖它们）。
//
// M2 进度（T-…-021）：`eg search` 已换成真实实现，命令定义与业务实现搬到 search.go，
// 本文件不再承载它——占位清单因此从三条收缩为「card show + rel 的写子命令」。

import "fmt"

func cardCommand() *Command {
	return &Command{
		Name:        "card",
		Display:     "card show",
		Summary:     "查看单张知识卡",
		ReadOnly:    true,
		Placeholder: true,
		Subs:        []string{"show"},
		SubRequired: true,
		Usage: `eg card show <k-id>

参数（合同 §1.7）：
  <k-id>   是；知识卡 ID

M1 状态：` + PlaceholderNotice + `。M2 落地时输出单卡视图（sources[] / relations[] 与显著标记）。
`,
	}
}

// placeholderError 生成占位命令的统一错误：退出码 1（用法错误），措辞固定。
func placeholderError(cmd *Command, sub string) error {
	invoked := "eg " + cmd.Name
	if sub != "" {
		invoked += " " + sub
	}
	return &UsageError{
		Msg: fmt.Sprintf("%s：%s", invoked, PlaceholderNotice),
		Diags: []Diagnostic{{
			Code:    "",
			Level:   LevelError,
			Path:    invoked,
			OpIndex: NonOpDiagnostic,
			Message: PlaceholderNotice,
		}},
	}
}
