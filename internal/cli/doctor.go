package cli

func doctorCommand() *Command {
	return &Command{
		Name:    "doctor",
		Display: "doctor",
		Summary: "从 .sy 全量扫描核对实体、关系、Candidate、coverage 与查询投影",
		Owner:   "T-evergreen.siyuan_full_stack_rearchitecture-158614-008",
		Usage: `eg doctor --backend sy --vault <data-dir> [--rebuild-index] [--json]

默认只读扫描 .sy authority 并输出完整语义与查询集合 digest。
--rebuild-index 必须连接在线 SiYuan kernel，由宿主在 writer gate 内先检查 journal，
再重建实际派生投影并复扫；before/after 不一致即失败。离线模式不伪造宿主 SQLite/FTS 重建。
`,
		Flags: func(fs *flagSet) {
			fs.Bool("rebuild-index", false, "由在线 SiYuan kernel 重建实际派生投影")
		},
		Validate: func(inv *Invocation) error {
			if err := noPositionalArgs(inv); err != nil {
				return err
			}
			if inv.String("backend") != "sy" {
				return &UsageError{Msg: "eg doctor requires --backend sy"}
			}
			return nil
		},
	}
}

func (r *Root) runSYMaintenanceOnly(*Invocation) (*Result, error) {
	return nil, &UsageError{Msg: "this maintenance command requires --backend sy"}
}
