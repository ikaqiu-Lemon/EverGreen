package cli

import "strings"

const subNoteCreate = "create"

func noteCommand() *Command {
	return &Command{
		Name:        "note",
		Display:     "note create",
		Summary:     "从原生 segment、Candidate 与 coverage 创建受管 Note",
		Owner:       "T-evergreen.siyuan_full_stack_rearchitecture-158614-008",
		Subs:        []string{subNoteCreate},
		SubRequired: true,
		Usage: `eg note create --request <note-create.json|-> --backend sy [--dry-run] [--json]

参数：
  --request <path|->   是；shared-core Note create 请求，- 从标准输入读取
  --backend sy         是；Note 的权威存储只允许 .sy backend
  --dry-run            否；返回确定性 ChangePlan，零权威写入、零 Git commit

请求只提供原生 segment/payload、Candidate 意图和 coverage。segment hash、Candidate
payload/ref hash、draft state、Source binding 与物理容器身份由 shared core 生成并在
plan/apply 两次复核；请求不能指定 materialized output。
`,
		Validate: func(inv *Invocation) error {
			if err := noPositionalArgs(inv); err != nil {
				return err
			}
			if inv.Sub != subNoteCreate {
				return &UsageError{Msg: "eg note 只支持 create"}
			}
			if strings.TrimSpace(inv.String("request")) == "" {
				return &UsageError{Msg: "eg note create 缺必填参数 --request <note-create.json|->"}
			}
			if inv.String("backend") != "sy" {
				return &UsageError{Msg: "eg note create requires --backend sy"}
			}
			return nil
		},
	}
}

func (r *Root) runNote(*Invocation) (*Result, error) {
	return nil, &UsageError{Msg: "eg note create requires --backend sy"}
}
