package cli

// M1 占位命令：`search` / `card show`（`rel` 见 rel.go）。
//
// 三者都是技术方案 §7.1 的 **S1 命令**，只是按 §16.1 划归 **M2 实现**，
// 因此文案一律写「M1 未实现（S1 命令，M2 落地）」，
// **禁止改写成任何暗示它们不属于 S1 的口径**（门禁对该措辞做 grep 反证）。
// 占位分支只写 stderr：不碰文件、不产生 commit（只读命令零副作用保证同样覆盖它们）。

import "fmt"

func searchCommand() *Command {
	return &Command{
		Name:        "search",
		Display:     "search",
		Summary:     "按关键词检索知识卡",
		ReadOnly:    true,
		Placeholder: true,
		Usage: `eg search <query> [--domain <d>]

参数（合同 §1.6，按 §7.1 登记，供 M2 直接落地）：
  <query>        是；检索词
  --domain <d>   否；限定领域

M1 状态：` + PlaceholderNotice + `。被调用即输出该文案并退 1，零副作用。
`,
		Flags: func(fs *flagSet) {
			fs.String("domain", "", "限定领域")
		},
	}
}

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
