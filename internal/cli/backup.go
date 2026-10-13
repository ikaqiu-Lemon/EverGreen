package cli

import (
	"context"
	"os"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/client"
	"github.com/ikaqiu-Lemon/EverGreen/internal/workspacebackup"
	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
)

func backupCommand() *Command {
	return &Command{
		Name:           "backup",
		Display:        "backup create|restore",
		Summary:        "创建或恢复带 checksum、Git、journal、资产、配置和加密材料的冷备份",
		Owner:          "T-evergreen.siyuan_full_stack_rearchitecture-158614-008",
		Subs:           []string{"create", "restore"},
		SubRequired:    true,
		SkipVaultGuard: true,
		Usage: `eg backup create --backend sy --vault <data-dir> --output <new-backup-dir> [--json]
eg backup restore --backend sy --input <backup-dir> --target <new-workspace-dir> [--json]

create 只允许 kernel 和所有其他 writer 已停止的离线 workspace；活跃/残留 lease、未完成
journal、缺 Git/assets/config/crypto recovery material 均 fail closed。输出必须是 workspace
外的新目录。restore 先在独立 staging 校验逐文件 SHA-256、Git HEAD、journal 与 crypto
identity；目标必须不存在或为空。明文 authority 必须通过 shared doctor/canonical parity
才发布目标；加密 authority 明确标记 requires_host_doctor，不会伪造离线语义 PASS。
restore 不切换当前 workspace。

退出码：0 cold backup / restore 完成 | 1 参数非法 |
        2 writer、journal、checksum、Git、crypto、目标或离线前置校验失败
`,
		Flags: func(fs *flagSet) {
			fs.String("output", "", "新的 backup 目录")
			fs.String("input", "", "已有 backup 目录")
			fs.String("target", "", "新的 workspace 根")
		},
		Validate: func(inv *Invocation) error {
			if err := noPositionalArgs(inv); err != nil {
				return err
			}
			if inv.String("backend") != "sy" {
				return &UsageError{Msg: "eg backup requires --backend sy"}
			}
			switch inv.Sub {
			case "create":
				if strings.TrimSpace(inv.VaultFlag) == "" || strings.TrimSpace(inv.String("output")) == "" {
					return &UsageError{Msg: "eg backup create requires --vault <data-dir> and --output <new-backup-dir>"}
				}
				if inv.Set("input") || inv.Set("target") {
					return &UsageError{Msg: "eg backup create does not accept --input or --target"}
				}
			case "restore":
				if strings.TrimSpace(inv.String("input")) == "" || strings.TrimSpace(inv.String("target")) == "" {
					return &UsageError{Msg: "eg backup restore requires --input <backup-dir> and --target <new-workspace-dir>"}
				}
				if inv.VaultFlag != "" || inv.Set("output") {
					return &UsageError{Msg: "eg backup restore does not accept --vault or --output"}
				}
			}
			return nil
		},
	}
}

func (r *Root) runSYBackup(inv *Invocation) (*Result, error) {
	if strings.TrimSpace(inv.String("backend")) != "sy" {
		return nil, &UsageError{Msg: "eg backup requires --backend sy"}
	}
	if strings.TrimSpace(os.Getenv(client.KernelURLEnv)) != "" {
		return nil, syError(&core.DiagnosticError{Diagnostics: []core.Diagnostic{{
			Code: core.CodeBackupOfflineRequired, Level: "error", Path: "workspace",
			Message: "cold backup and restore are offline-only; the configured kernel endpoint disables local fallback",
		}}})
	}
	switch inv.Sub {
	case "create":
		result, err := workspacebackup.Create(context.Background(), workspacebackup.CreateOptions{
			DataDir: inv.VaultFlag, OutputDir: inv.String("output"), CreatedAt: r.now(),
			Registry: core.ApplicationRegistry(),
		})
		if err != nil {
			return nil, syError(err)
		}
		return &Result{Data: map[string]interface{}{"backup": result}}, nil
	case "restore":
		result, err := workspacebackup.Restore(context.Background(), workspacebackup.RestoreOptions{
			InputDir: inv.String("input"), TargetDir: inv.String("target"),
			Registry: core.ApplicationRegistry(),
		})
		if err != nil {
			return nil, syError(err)
		}
		return &Result{Data: map[string]interface{}{"restore": result}}, nil
	default:
		return nil, &UsageError{Msg: "unknown backup command"}
	}
}
