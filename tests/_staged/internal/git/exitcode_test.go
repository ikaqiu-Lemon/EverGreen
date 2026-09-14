package git_test

import (
	"errors"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/cli"
	"github.com/ikaqiu-Lemon/EverGreen/internal/git"
)

// TestCommitFailedMapsToExitFour 断言 B4 的对外语义：提交失败经上层包装后退 4，
// 且底层 *git.CommitFailed 仍可被取出用于报告（不做任何撤销动作）。
func TestCommitFailedMapsToExitFour(t *testing.T) {
	inner := &git.CommitFailed{
		Reason:      "注入的提交失败",
		Uncommitted: []string{"cards/k2.md"},
		Err:         errors.New("exit status 128"),
	}
	wrapped := &cli.CommitFailedError{Msg: "git 提交失败", Err: inner}
	if got := cli.ExitCodeFor(wrapped); got != cli.ExitCommitFailed {
		t.Fatalf("提交失败应退 %d，得到 %d", cli.ExitCommitFailed, got)
	}
	var failed *git.CommitFailed
	if !errors.As(wrapped, &failed) {
		t.Fatal("上层错误应可解包出 *git.CommitFailed（供报告如实上报未提交清单）")
	}
	if len(failed.Uncommitted) != 1 {
		t.Fatalf("未提交清单应被保留：%v", failed.Uncommitted)
	}
}
