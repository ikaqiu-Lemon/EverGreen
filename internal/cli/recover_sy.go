package cli

import (
	"fmt"
	"strconv"
	"strings"
)

func recoverCommand() *Command {
	return &Command{
		Name:        "recover",
		Display:     "recover lease|journal",
		Summary:     "显式恢复 .sy writer lease 或未完成 operation journal",
		Owner:       "T-evergreen.siyuan_full_stack_rearchitecture-158614-008",
		Subs:        []string{"lease", "journal"},
		SubRequired: true,
		Usage: `eg recover lease --backend sy --vault <data-dir> --expected-pid <pid> --expected-nonce <nonce> [--json]
eg recover journal --backend sy --vault <data-dir> [--json]

lease 仅用于 kernel 已停止且 flock 空闲的残留 writer.lease。必须逐字匹配记录中的 PID 与
nonce，并确认该 PID 已退出；lease 年龄不能授权恢复。损坏记录保持原字节并 fail closed。
journal 在线时只调用 kernel recovery API；离线时取得 writer lease 后调用同一 shared core。
配置了 EG_KERNEL_URL 后 transport 失败不会回退本地文件。

退出码：0 恢复完成 / 无待恢复 journal | 1 参数非法 |
        2 恢复证据、授权、lease、journal 或 transport 校验失败（零未授权写入）
`,
		Flags: func(fs *flagSet) {
			fs.Int("expected-pid", 0, "writer.lease 中记录的 PID")
			fs.String("expected-nonce", "", "writer.lease 中记录的 nonce")
		},
		Validate: func(inv *Invocation) error {
			if err := noPositionalArgs(inv); err != nil {
				return err
			}
			if inv.String("backend") != "sy" {
				return &UsageError{Msg: "eg recover requires --backend sy"}
			}
			switch inv.Sub {
			case "lease":
				pid, err := strconv.Atoi(inv.String("expected-pid"))
				if err != nil || pid <= 0 || strings.TrimSpace(inv.String("expected-nonce")) == "" {
					return &UsageError{Msg: "eg recover lease requires positive --expected-pid and nonempty --expected-nonce"}
				}
			case "journal":
				if inv.Set("expected-pid") || inv.Set("expected-nonce") {
					return &UsageError{Msg: "eg recover journal does not accept lease recovery evidence"}
				}
			default:
				return &UsageError{Msg: fmt.Sprintf("unknown recover mode %q", inv.Sub)}
			}
			return nil
		},
	}
}
