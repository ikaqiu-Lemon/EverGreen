package cli

// 退出码 6 的判定顺序与启用边界的机器判据（exitcode.go）。
//
// 只测两件事：三个码的**优先级**、6 的**命令白名单**（含 5 全程不启用）。

import (
	"strings"
	"testing"
)

// TestExitCode6_OnlyAfterValidationPasses 判定顺序锁死：参数错 1 → 校验失败 2 → 仅缺确认 6。
//
// 表恰 3 行：6 只在「参数合法且校验全过」时出现，优先级低于 1 与 2。
func TestExitCode6_OnlyAfterValidationPasses(t *testing.T) {
	cases := []struct {
		name string
		in   ConfirmDecision
		want int
	}{
		{"未批准/状态非法：校验失败", ConfirmDecision{ArgsValid: true, ValidationPassed: false, ConfirmMissing: true}, ExitValidation},
		{"参数缺失：用法错优先", ConfirmDecision{ArgsValid: false, ValidationPassed: false, ConfirmMissing: true}, ExitUsage},
		{"仅缺 --confirm：校验已通过", ConfirmDecision{ArgsValid: true, ValidationPassed: true, ConfirmMissing: true}, ExitNeedConfirm},
	}
	for _, c := range cases {
		if got := ExitCodeForConfirm(c.in); got != c.want {
			t.Errorf("%s：ExitCodeForConfirm(%+v) = %d，期望 %d", c.name, c.in, got, c.want)
		}
	}
	if ExitNeedConfirm != 6 {
		t.Fatalf("ExitNeedConfirm = %d，必须是 6", ExitNeedConfirm)
	}
	// 三事实全成立 = 可以继续执行（不是「已执行完」）。
	if got := ExitCodeForConfirm(ConfirmDecision{ArgsValid: true, ValidationPassed: true}); got != ExitOK {
		t.Fatalf("确认齐备时应为 %d，得 %d", ExitOK, got)
	}
}

// TestExitCode6EnabledCommandsAreClosed 启用边界：6 的白名单恰 {proposal approve, delete}，5 不启用。
func TestExitCode6EnabledCommandsAreClosed(t *testing.T) {
	got := NeedConfirmCommands()
	want := []string{"delete", "proposal approve"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("6 的启用白名单 = %v，必须恰为 %v", got, want)
	}
	for _, c := range want {
		if !NeedConfirmEnabled(c) {
			t.Fatalf("%s 应在白名单内", c)
		}
	}
	for _, c := range []string{"card show", "apply", "proposal list", "proposal reject"} {
		if NeedConfirmEnabled(c) {
			t.Fatalf("%s 不得启用退出码 6", c)
		}
	}
	if (&NeedConfirmError{Command: "proposal approve", Msg: "缺 --confirm"}).ExitCode() != ExitNeedConfirm {
		t.Fatalf("白名单内命令的 NeedConfirmError 必须映射到 6")
	}
	if (&NeedConfirmError{Command: "apply", Msg: "缺 --confirm"}).ExitCode() != ExitUsage {
		t.Fatalf("白名单外命令不得拿到 6，必须退化为 1")
	}
	if !ExitCode5Enabled() {
		t.Fatalf("退出码 5 自 M6（T-074）起已启用（ExitPrecheckOrLock=5）")
	}
}
