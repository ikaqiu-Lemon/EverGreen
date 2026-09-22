package cli

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
)

func exportCommand() *Command {
	return &Command{
		Name:     "export",
		Display:  "export",
		Summary:  "将权威 Markdown 导出为不含 Evergreen 专有协议的 plain Markdown",
		Owner:    "T-evergreen.block_boundary_materialization-158614-005",
		ReadOnly: true,
		Usage: `eg export --plain --output <dir> [--json]

参数：
  --plain          是；输出 plain Markdown
  --output <dir>   是；vault 外的新目录或空目录

导出只读取 vault，不创建 Git commit。输出目录不得位于 vault 内、不得自身为符号链接，
且经符号链接解析后的真实路径也不得逃逸进 vault；非空目录默认拒绝。
导出保留全部可见 Markdown 与顺序，只删除 eg:nr/eg:cd/eg:cc/eg:nc 机器锚点、
candidate 标题属性和 L2 candidate 围栏行。
退出码：0 成功 | 1 参数、路径、协议或输出写入失败（vault 零改动）
`,
		Flags: func(fs *flagSet) {
			fs.Bool("plain", false, "导出 plain Markdown")
			fs.String("output", "", "vault 外输出目录")
		},
		Validate: validateExportArgs,
	}
}

func validateExportArgs(inv *Invocation) error {
	if err := noPositionalArgs(inv); err != nil {
		return err
	}
	plain, err := strconv.ParseBool(inv.String("plain"))
	if err != nil || !plain {
		return &UsageError{Msg: "eg export 必须显式给出 --plain"}
	}
	if strings.TrimSpace(inv.String("output")) == "" {
		return &UsageError{Msg: "eg export 缺必填参数 --output <dir>"}
	}
	return nil
}

type plainExportFile struct {
	rel  string
	data []byte
}

func (r *Root) runExport(inv *Invocation) (*Result, error) {
	output, err := validatePlainExportOutput(inv.VaultRoot, inv.String("output"))
	if err != nil {
		return nil, &UsageError{Msg: err.Error()}
	}
	files, err := preparePlainExport(inv.VaultRoot)
	if err != nil {
		return nil, &UsageError{Msg: fmt.Sprintf(
			"plain export 预检失败（vault 零改动）：%v", err)}
	}
	if err := os.MkdirAll(output, 0o755); err != nil {
		return nil, &UsageError{Msg: fmt.Sprintf("创建 plain export 输出目录失败：%v", err)}
	}
	if err := ensurePlainOutputStillSafe(inv.VaultRoot, output); err != nil {
		return nil, &UsageError{Msg: err.Error()}
	}
	for _, file := range files {
		target := filepath.Join(output, filepath.FromSlash(file.rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, &UsageError{Msg: fmt.Sprintf("创建导出目录 %s 失败：%v", file.rel, err)}
		}
		if err := os.WriteFile(target, file.data, 0o644); err != nil {
			return nil, &UsageError{Msg: fmt.Sprintf("写入导出文件 %s 失败：%v", file.rel, err)}
		}
	}
	paths := make([]string, len(files))
	for i := range files {
		paths[i] = files[i].rel
	}
	res := &Result{
		Data: map[string]interface{}{
			"output": filepath.Clean(inv.String("output")),
			"files":  paths,
			"count":  len(paths),
		},
		DataOrder: []string{"output", "files", "count"},
		Summary: []string{fmt.Sprintf(
			"plain export：导出 %d 个权威 Markdown 文件；vault 零写入、零 commit",
			len(paths))},
	}
	return res, nil
}

func preparePlainExport(root string) ([]plainExportFile, error) {
	var files []plainExportFile
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			if rel == "." {
				return nil
			}
			switch strings.Split(rel, "/")[0] {
			case ".git", ".index", ".eg":
				return filepath.SkipDir
			}
			return nil
		}
		if !plainExportDataPath(rel) {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("权威 Markdown 不得经符号链接导出：%s", rel)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		plain, err := mdfile.PlainExport(raw)
		if err != nil {
			return fmt.Errorf("%s：%w", rel, err)
		}
		files = append(files, plainExportFile{rel: rel, data: plain})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })
	return files, nil
}

func plainExportDataPath(rel string) bool {
	if filepath.Ext(rel) != ".md" {
		return false
	}
	if rel == "unprocessed.md" {
		return true
	}
	first := strings.Split(rel, "/")[0]
	switch first {
	case "sources", "domains", "reviews", "proposals":
		return true
	}
	return false
}

func validatePlainExportOutput(root, raw string) (string, error) {
	output, err := filepath.Abs(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("解析 --output 失败：%v", err)
	}
	if info, err := os.Lstat(output); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("plain export 输出目录不得是符号链接：%s", raw)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("plain export 输出路径不是目录：%s", raw)
		}
		entries, err := os.ReadDir(output)
		if err != nil {
			return "", fmt.Errorf("读取 plain export 输出目录失败：%v", err)
		}
		if len(entries) != 0 {
			return "", fmt.Errorf("plain export 默认拒绝非空输出目录：%s", raw)
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("检查 plain export 输出目录失败：%v", err)
	}
	if err := ensurePlainOutputStillSafe(root, output); err != nil {
		return "", err
	}
	return output, nil
}

func ensurePlainOutputStillSafe(root, output string) error {
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("解析 vault 真实路径失败：%v", err)
	}
	outputReal, err := canonicalFuturePath(output)
	if err != nil {
		return fmt.Errorf("解析 plain export 输出真实路径失败：%v", err)
	}
	if pathWithin(rootReal, outputReal) {
		return fmt.Errorf("plain export 输出目录不得位于 vault 内：%s", output)
	}
	if info, err := os.Lstat(output); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("plain export 输出目录不得是符号链接：%s", output)
	}
	return nil
}

func canonicalFuturePath(path string) (string, error) {
	current := filepath.Clean(path)
	var suffix []string
	for {
		if _, err := os.Lstat(current); err == nil {
			real, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			for i := len(suffix) - 1; i >= 0; i-- {
				real = filepath.Join(real, suffix[i])
			}
			return filepath.Clean(real), nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("找不到已存在的父目录")
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

func pathWithin(parent, child string) bool {
	rel, err := filepath.Rel(filepath.Clean(parent), filepath.Clean(child))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
