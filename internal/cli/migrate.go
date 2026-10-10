package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/migrate"
	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
)

func migrateCommand() *Command {
	return &Command{
		Name: "migrate", Display: "migrate inventory|import|parity", Summary: "清点、冻结并验证旧 vault 的 .sy 冷迁移",
		Subs: []string{"inventory", "import", "parity"}, SubRequired: true, SkipVaultGuard: true,
		Usage: `eg migrate inventory --vault <source> --output <manifest.json> [--json]
eg migrate import --vault <source> --manifest <manifest.json> --target <new-data-dir> [--json]
eg migrate parity --vault <source> --manifest <manifest.json> --target <data-dir> [--json]

inventory 只读取源 vault。import 拒绝非空或已存在的目标以及任何 quarantine，
先生成完整暂存 workspace，再原子发布。manifest 固定源 revision、hash 与物理映射。
退出码：0 成功 | 1 参数或输出失败 | 2 校验失败，零目标发布
`,
		Flags: func(fs *flagSet) {
			fs.String("output", "", "新 manifest 文件")
			fs.String("manifest", "", "已冻结的 manifest")
			fs.String("target", "", "新的 SiYuan data 根")
		},
		Validate: func(inv *Invocation) error {
			if err := noPositionalArgs(inv); err != nil {
				return err
			}
			if strings.TrimSpace(inv.VaultFlag) == "" {
				return &UsageError{Msg: "migrate requires explicit --vault"}
			}
			if inv.Sub == "inventory" && inv.String("output") == "" {
				return &UsageError{Msg: "inventory requires --output"}
			}
			if inv.Sub != "inventory" && (inv.String("manifest") == "" || inv.String("target") == "") {
				return &UsageError{Msg: "import/parity require --manifest and --target"}
			}
			return nil
		},
	}
}

func (r *Root) runMigrate(inv *Invocation) (*Result, error) {
	switch inv.Sub {
	case "inventory":
		prepared, err := migrate.Inventory(inv.VaultFlag)
		if err != nil {
			return nil, &ValidationError{Msg: err.Error()}
		}
		if err = ensurePlainOutputStillSafe(inv.VaultFlag, inv.String("output")); err != nil {
			return nil, &UsageError{Msg: err.Error()}
		}
		if err = writeNewJSON(inv.String("output"), prepared.Manifest); err != nil {
			return nil, &UsageError{Msg: err.Error()}
		}
		return &Result{Data: map[string]interface{}{"manifest": prepared.Manifest},
			Summary: []string{fmt.Sprintf("inventory: %d entities, %d quarantine; source unchanged",
				len(prepared.Manifest.Entities), len(prepared.Manifest.Quarantine))}}, nil
	case "import", "parity":
		var manifest migrate.Manifest
		if err := readJSON(inv.String("manifest"), &manifest); err != nil {
			return nil, &UsageError{Msg: err.Error()}
		}
		if inv.Sub == "import" {
			if err := migrate.ColdImport(inv.VaultFlag, inv.String("target"), manifest); err != nil {
				return nil, &ValidationError{Msg: err.Error()}
			}
		}
		parity, err := migrate.Parity(inv.VaultFlag, inv.String("target"), manifest)
		if err != nil {
			return &Result{Data: map[string]interface{}{"parity": parity}}, &ValidationError{Msg: err.Error()}
		}
		return &Result{Data: map[string]interface{}{"parity": parity},
			Summary: []string{fmt.Sprintf("parity: %d entities, %d locations, 0 differences", parity.Entities, parity.Locations)}}, nil
	}
	return nil, &UsageError{Msg: "unknown migration command"}
}

func diffCommand() *Command {
	return &Command{
		Name: "diff", Display: "diff", Summary: "比较 canonical archive 的实体、字段、正文、关系与覆盖语义",
		ReadOnly: true, SkipVaultGuard: true,
		Usage: "eg diff --before <archive.json> --after <archive.json> [--json]\n退出码：0 成功 | 1 参数失败 | 2 归档校验失败\n",
		Flags: func(fs *flagSet) {
			fs.String("before", "", "此前 canonical archive")
			fs.String("after", "", "此后 canonical archive")
		},
		Validate: func(inv *Invocation) error {
			if err := noPositionalArgs(inv); err != nil {
				return err
			}
			if inv.String("before") == "" || inv.String("after") == "" {
				return &UsageError{Msg: "diff requires --before and --after"}
			}
			return nil
		},
	}
}

func (r *Root) runSemanticDiff(inv *Invocation) (*Result, error) {
	var before, after core.CanonicalArchive
	if err := readJSON(inv.String("before"), &before); err != nil {
		return nil, &UsageError{Msg: err.Error()}
	}
	if err := readJSON(inv.String("after"), &after); err != nil {
		return nil, &UsageError{Msg: err.Error()}
	}
	changes, err := core.DiffCanonical(before, after)
	if err != nil {
		return nil, &ValidationError{Msg: err.Error()}
	}
	return &Result{Data: map[string]interface{}{"changes": changes, "count": len(changes)},
		Summary: []string{fmt.Sprintf("semantic diff: %d changes", len(changes))}}, nil
}

func readJSON(path string, target any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}

func writeNewJSON(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = file.Write(append(raw, '\n'))
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}
