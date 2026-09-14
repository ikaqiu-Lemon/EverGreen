package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
)

// ErrIDNotFound 表示全库扫描后没有任何文件的 frontmatter id 等于该 ID。
var ErrIDNotFound = errors.New("未找到该 ID 对应的文件")

// skipDirs 是扫描时跳过的目录（`.git` 是仓库内部；`.index` 属 S4 目标态，S1 不建）。
var skipDirs = map[string]bool{".git": true, ".index": true}

// Duplicate 是重复 ID 的可定位诊断（供上层出 E1 error）。
type Duplicate struct {
	ID    string
	Path  string // 后遇到的文件
	First string // 先遇到的文件
}

func (d Duplicate) String() string {
	return fmt.Sprintf("重复 ID %s：%s 与 %s", d.ID, d.First, d.Path)
}

// Index 是一次全库扫描得到的 id → 相对路径映射。
//
// 文件**允许被重命名或移动**：关系只引用 ID（冻结合同 F2），所以定位一律走扫描解析，
// 不依赖文件名。S1 接受 O(N) 扫描代价（无 .index 增量索引，那是 S4）。
type Index struct {
	ByID       map[string]string
	Duplicates []Duplicate
}

// Resolve 按 frontmatter id 解析出 vault 内相对路径。
func (i Index) Resolve(id string) (string, error) {
	for _, dup := range i.Duplicates {
		if dup.ID == id {
			return "", fmt.Errorf("%s", dup.String())
		}
	}
	p, ok := i.ByID[id]
	if !ok {
		return "", fmt.Errorf("%w：%s", ErrIDNotFound, id)
	}
	return p, nil
}

type idOnly struct {
	ID string `yaml:"id"`
}

// ScanIDs 遍历 vault 下的 Markdown，按 frontmatter id 建映射。
// 只读：不修复、不重命名、不改写任何文件；无法解析的文件被忽略（由上层校验命令负责报告）。
func (s *Store) ScanIDs() (Index, error) {
	idx := Index{ByID: make(map[string]string)}
	err := filepath.WalkDir(s.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		doc, err := mdfile.Parse(raw)
		if err != nil || !doc.HasFM {
			return nil
		}
		var got idOnly
		if err := doc.DecodeFM(&got); err != nil || got.ID == "" {
			return nil
		}
		rel, err := filepath.Rel(s.root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if first, ok := idx.ByID[got.ID]; ok {
			idx.Duplicates = append(idx.Duplicates, Duplicate{ID: got.ID, First: first, Path: rel})
			return nil
		}
		idx.ByID[got.ID] = rel
		return nil
	})
	if err != nil {
		return Index{}, err
	}
	return idx, nil
}
