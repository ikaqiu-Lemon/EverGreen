package store

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"evergreen/internal/mdfile"
)

// ErrFileExists 表示新建路径上已有文件——新建绝不覆盖既有文件。
var ErrFileExists = errors.New("目标文件已存在（新建路径不覆盖）")

// tmpPrefix / tmpSuffix 是原子替换用的临时文件命名。中断（未 rename）时目标文件
// 仍是完整旧版本，绝不出现半截文件。
const (
	tmpPrefix = ".eg-"
	tmpSuffix = ".tmp"
)

// writeAtomic 是本包**唯一**的落盘动作：tmp + fsync + rename（并 fsync 目录）。
// 入参 data 必须已由 mdfile 的区间拼接产出，本函数不构造、不改写一个字节。
func writeAtomic(abs string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(abs)
	tmp, err := os.CreateTemp(dir, tmpPrefix+"*"+tmpSuffix)
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, abs); err != nil {
		cleanup()
		return err
	}
	return syncDir(dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := d.Sync(); err != nil {
		_ = d.Close()
		return err
	}
	return d.Close()
}

// SectionAppend 是一次分区尾部追加（B1：只追加）。
type SectionAppend struct {
	Section string
	Payload []byte // 逐字插入，必须以 \n 结束
}

// FMKeyAppend 是向 frontmatter 末尾追加一个**新**键（既有键不改写）。
type FMKeyAppend struct {
	Key   string
	Value []byte
}

// FMSeqAppend 是向 frontmatter 里既有块状序列追加一项（缩进沿用既有风格）。
type FMSeqAppend struct {
	Key  string
	Item []byte
}

// FMSeqBlock 是在 frontmatter 末尾**新建**一个块状序列（键缺失时用；键已存在请用 FMSeqAppend）。
type FMSeqBlock struct {
	Key   string
	Items []byte // 调用方自带缩进与 `- `，逐字插入
}

// Edit 是一次守卫写入要施加的全部**追加**动作。没有「替换」「删除」形态——
// 那是 S2 起的用户显式命令，自动路径拿不到（B1）。
type Edit struct {
	Kind       mdfile.Kind
	Sections   []SectionAppend
	FMKeys     []FMKeyAppend
	FMSeqItems []FMSeqAppend
	FMSeqNew   []FMSeqBlock
}

func (e Edit) empty() bool {
	return len(e.Sections) == 0 && len(e.FMKeys) == 0 && len(e.FMSeqItems) == 0 &&
		len(e.FMSeqNew) == 0
}

// WriteGuarded 是 S1 唯一的守卫写入口，固定次序（§16.4）：
//
//	读盘 → content_hash 与 expectedHash 比对（B3，不一致 → SkipFileChanged）
//	→ Parse→Render 与原字节逐字比对，不等即拒写（退化为 SkipFileChanged 语义）
//	→ 用户分区原始字节快照（B2）
//	→ 在目标字节区间插入（只追加）
//	→ 用户分区逐字保留校验（不通过 → SkipUserBlockUnsafe）
//	→ tmp + fsync + rename（并 fsync 目录）
//	→ 写后用 yaml.v3 只读复核「仍是合法 YAML 且新字段语义正确」，失败即记录并进报告
//
// 全程不覆盖、不强写、不重试、不排队。跳过时返回 *SkipError（显式「必须跳过」信号，
// 调用方不得忽略）且磁盘零变化、无 .tmp 残留。
func (s *Store) WriteGuarded(rel, expectedHash string, edit Edit) (Result, error) {
	f, err := s.Read(rel)
	if err != nil {
		return Result{Path: rel}, err
	}
	res := Result{Path: rel, Hash: f.Hash}

	if expectedHash != "" && expectedHash != f.Hash {
		skip := &SkipError{Path: rel, Reason: SkipFileChanged,
			Detail: fmt.Sprintf("自读取以来文件已变化：期望 %s，磁盘 %s", expectedHash, f.Hash)}
		res.Reason, res.Detail = skip.Reason, skip.Detail
		return res, skip
	}
	if err := mdfile.SelfCheck(f.Bytes); err != nil {
		skip := &SkipError{Path: rel, Reason: SkipFileChanged,
			Detail: ErrSelfCheckFailed.Error() + "：" + err.Error()}
		res.Reason, res.Detail = skip.Reason, skip.Detail
		return res, skip
	}
	if edit.empty() {
		return res, nil
	}

	cur, err := applyEdit(f.Bytes, edit)
	if err != nil {
		res.Detail = err.Error()
		return res, err
	}
	if err := PreserveUserSections(rel, f.Bytes, cur); err != nil {
		if skip, ok := AsSkip(err); ok {
			res.Reason, res.Detail = skip.Reason, skip.Detail
		}
		return res, err
	}
	if err := writeAtomic(f.Path, cur, 0o644); err != nil {
		res.Detail = err.Error()
		return res, err
	}
	res.Written = true
	res.Hash = ContentHash(cur)
	res.Warnings = reviewAfterWrite(cur, edit)
	return res, nil
}

// applyEdit 逐个施加追加动作：每步都重新 Parse（偏移会随插入移动），只调用 mdfile
// 的字节插入接口，不做任何序列化。
func applyEdit(raw []byte, edit Edit) ([]byte, error) {
	cur := raw
	for _, sa := range edit.Sections {
		if err := writableSection(edit.Kind, sa.Section); err != nil {
			return nil, err
		}
		doc, err := mdfile.Parse(cur)
		if err != nil {
			return nil, err
		}
		out, err := doc.AppendToSection(sa.Section, sa.Payload)
		if err != nil {
			return nil, err
		}
		cur = out
	}
	for _, fk := range edit.FMKeys {
		doc, err := mdfile.Parse(cur)
		if err != nil {
			return nil, err
		}
		out, err := doc.AppendFMKey(fk.Key, fk.Value)
		if err != nil {
			return nil, err
		}
		cur = out
	}
	for _, fb := range edit.FMSeqNew {
		doc, err := mdfile.Parse(cur)
		if err != nil {
			return nil, err
		}
		out, err := doc.AppendFMSeqBlock(fb.Key, fb.Items)
		if err != nil {
			return nil, err
		}
		cur = out
	}
	for _, fs := range edit.FMSeqItems {
		doc, err := mdfile.Parse(cur)
		if err != nil {
			return nil, err
		}
		out, err := doc.AppendFMSeqItem(fs.Key, fs.Item)
		if err != nil {
			return nil, err
		}
		cur = out
	}
	return cur, nil
}

// reviewAfterWrite 是写后**只读**复核：仍是合法 YAML，且本次追加的 frontmatter 键
// 语义上确实存在。复核失败不回滚（B4 同源口径：不做破坏性动作），只如实记录进报告。
func reviewAfterWrite(out []byte, edit Edit) []string {
	var warnings []string
	doc, err := mdfile.Parse(out)
	if err != nil {
		return append(warnings, "写后复核失败：写入结果无法解析："+err.Error())
	}
	var fm map[string]interface{}
	if err := doc.DecodeFM(&fm); err != nil {
		return append(warnings, "写后复核失败：frontmatter 不是合法 YAML："+err.Error())
	}
	for _, fk := range edit.FMKeys {
		if _, ok := fm[fk.Key]; !ok {
			warnings = append(warnings, "写后复核失败：追加的 frontmatter 键未生效："+fk.Key)
		}
	}
	for _, fs := range edit.FMSeqItems {
		if _, ok := fm[fs.Key]; !ok {
			warnings = append(warnings, "写后复核失败：序列键未生效："+fs.Key)
		}
	}
	for _, fb := range edit.FMSeqNew {
		if _, ok := fm[fb.Key]; !ok {
			warnings = append(warnings, "写后复核失败：新建序列键未生效："+fb.Key)
		}
	}
	return warnings
}

// AppendToSection 是「向已有产物的某个分区尾部追加」的便捷写口（B1 的两种写形态之一）。
// 内部走 WriteGuarded：读盘拿 content_hash → 自检 → 插入 → 原子替换。
// 「用户补充」被直接拒绝；「知识内容」对自动路径只读。
func (s *Store) AppendToSection(rel string, kind mdfile.Kind, section string, payload []byte) (Result, error) {
	f, err := s.Read(rel)
	if err != nil {
		return Result{Path: rel}, err
	}
	return s.WriteGuarded(rel, f.Hash, Edit{
		Kind:     kind,
		Sections: []SectionAppend{{Section: section, Payload: payload}},
	})
}

// CreateFile 是「新建产物文件」的写口（B1 的两种写形态之一）。
// content 由上层按模板拼好（字节），本函数只做：不覆盖既有文件 → 字节自检 →
// 分区结构校验 → 「用户补充」必须为空（B2：新建路径也不写用户内容）→ 原子落盘。
func (s *Store) CreateFile(rel string, kind mdfile.Kind, content []byte) (Result, error) {
	abs, err := s.Abs(rel)
	if err != nil {
		return Result{Path: rel}, err
	}
	res := Result{Path: rel}
	if _, err := os.Stat(abs); err == nil {
		return res, fmt.Errorf("%w：%s", ErrFileExists, rel)
	} else if !errors.Is(err, os.ErrNotExist) {
		return res, err
	}
	if err := mdfile.SelfCheck(content); err != nil {
		return res, fmt.Errorf("%w：%v", ErrSelfCheckFailed, err)
	}
	doc, err := mdfile.Parse(content)
	if err != nil {
		return res, err
	}
	if kind == mdfile.KindCard || kind == mdfile.KindNote {
		if err := doc.ValidateSections(kind); err != nil {
			return res, err
		}
	}
	if err := emptyUserSection(doc, content); err != nil {
		return res, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return res, err
	}
	if err := writeAtomic(abs, content, 0o644); err != nil {
		return res, err
	}
	res.Written = true
	res.Hash = ContentHash(content)
	return res, nil
}

// emptyUserSection 保证新建文档的「用户补充」分区为空——自动路径永不写用户内容。
func emptyUserSection(doc *mdfile.Doc, content []byte) error {
	span, ok := doc.Section(mdfile.SecUserAppend)
	if !ok {
		return nil
	}
	if len(bytes.TrimSpace(content[span.Body:span.End])) != 0 {
		return fmt.Errorf("%w：新建文档的「%s」必须为空", ErrUserSectionWrite, mdfile.SecUserAppend)
	}
	return nil
}
