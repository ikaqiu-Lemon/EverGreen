package git

import (
	"fmt"
	"strings"
)

// CommitFailed 是提交步骤失败的错误：携带**未提交变更清单**与失败原因，交上层退 4。
//
// B4：拿到这个错误的调用方**只能如实上报**——磁盘保留写入后的状态，
// 不得还原文件、不得删除文件、不得做任何撤销类 Git 操作。
type CommitFailed struct {
	Reason      string   // 失败原因（git 的 stderr 或前置步骤错误）
	Uncommitted []string // 未提交的变更路径清单（失败时刻的采样）
	Err         error
}

func (e *CommitFailed) Error() string {
	var b strings.Builder
	b.WriteString("git 提交失败：")
	b.WriteString(e.Reason)
	if len(e.Uncommitted) > 0 {
		b.WriteString(fmt.Sprintf("；未提交变更 %d 项：%s", len(e.Uncommitted), strings.Join(e.Uncommitted, ", ")))
	}
	b.WriteString("（磁盘保留现状，不做破坏性回滚）")
	return b.String()
}

// Unwrap 暴露底层错误。
func (e *CommitFailed) Unwrap() error { return e.Err }

// wrapGit 把 git 的非零退出包装成带 stderr 的错误（只读诊断，不触发任何撤销动作）。
func wrapGit(op string, stderr []byte, err error) error {
	msg := strings.TrimSpace(string(stderr))
	if msg == "" {
		return fmt.Errorf("git %s 失败：%w", op, err)
	}
	return fmt.Errorf("git %s 失败：%s：%w", op, msg, err)
}
