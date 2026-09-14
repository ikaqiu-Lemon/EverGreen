package cli

// 尚未落地命令的占位：`rel`（读路径与 add / remove 子命令，见 rel.go）。
//
// 它们都是技术方案 §7.1 的 **S1 命令**，只是按 §16.1 划归 **M2 实现**，
// 因此文案一律沿用 root.go 的 PlaceholderNotice，
// **禁止改写成任何暗示它们不属于 S1 的口径**（门禁对该措辞做 grep 反证）。
// 占位分支只写 stderr：不碰文件、不产生 commit（只读命令零副作用保证同样覆盖它们）。
//
// M2 进度：`eg search`（T-…-021）与 `eg card show`（T-…-022）已换成真实实现，
// 命令定义与业务实现分别搬到 search.go / card.go，本文件不再承载它们——
// 占位清单因此从三条收缩为「rel 读路径 + rel 的写子命令」，本文件只留占位错误的统一构造。

import "fmt"

// placeholderError 生成占位命令的统一错误：退出码 1（用法错误），措辞固定。
func placeholderError(cmd *Command, sub string) error {
	invoked := "eg " + cmd.Name
	if sub != "" {
		invoked += " " + sub
	}
	return &UsageError{
		Msg: fmt.Sprintf("%s：%s", invoked, PlaceholderNotice),
		Diags: []Diagnostic{{
			Code:    E17,
			Level:   LevelError,
			Path:    invoked,
			OpIndex: NonOpDiagnostic,
			Message: PlaceholderNotice,
		}},
	}
}
