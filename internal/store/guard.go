package store

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
)

var (
	// ErrUserSectionWrite 是 B2 的硬拒绝：「用户补充」任何时候、任何路径都不得写入。
	ErrUserSectionWrite = errors.New("「用户补充」不得写入（安全底线 B2）")
	// ErrSectionNotWritable 表示目标分区不在自动路径写白名单内（如已有卡的「知识内容」只读）。
	ErrSectionNotWritable = errors.New("该分区不在自动路径写白名单内")
	// ErrSelfCheckFailed 表示 Parse→Render 与原字节不等（写前字节自检失败，拒写）。
	ErrSelfCheckFailed = errors.New("写前字节自检失败：Parse→Render 与原字节不等")
)

// UserOwnedSections 是 B2 要求逐字保留的用户分区：「用户补充」（永不写）与
// 「存疑与待验证」（可追加，但既有字节逐字保留）。
func UserOwnedSections() []string {
	return []string{mdfile.SecUserAppend, mdfile.SecOpenQuest}
}

// UserSectionBytes 从原文取回用户分区的**原始字节**（含空行、缩进、未知子结构），
// 供 B2 逐字回填与逐字比对使用。只读，不做任何规范化。
func UserSectionBytes(raw []byte) (map[string][]byte, error) {
	doc, err := mdfile.Parse(raw)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]byte)
	for _, name := range UserOwnedSections() {
		span, ok := doc.Section(name)
		if !ok {
			continue
		}
		out[name] = raw[span.Body:span.End]
	}
	return out, nil
}

// PreserveUserSections 校验候选输出对用户分区的逐字保留（B2）。
// 无法安全保留时返回 *SkipError{Reason: SkipUserBlockUnsafe}，调用方必须跳过该文件。
//
// **追加型**写入专用：额外要求「候选输出不得比原文短」。物理移除 / 块替换这两条
// M3 写形态天然会变短，走 PreserveUserSectionsAfterCut（同一套用户分区判据，不含长度前置）。
func PreserveUserSections(path string, before, after []byte) error {
	if len(after) < len(before) {
		return &SkipError{Path: path, Reason: SkipUserBlockUnsafe,
			Detail: "候选输出比原文更短：只追加不删减的前提被破坏"}
	}
	return PreserveUserSectionsAfterCut(path, before, after)
}

// PreserveUserSectionsAfterCut 是 M3 非追加写形态（`remove_relation` 物理移除、
// `replace_block` 块替换）的 B2 校验：与 PreserveUserSections **同一套**「用户分区既有
// 字节逐字连续保留」判据，只是**不含**「输出不得变短」这条追加型前置——
// 移除关系条目与替换自检块必然使文件变短，但用户分区自身一个字节都不许动。
func PreserveUserSectionsAfterCut(path string, before, after []byte) error {
	doc, err := mdfile.Parse(before)
	if err != nil {
		return &SkipError{Path: path, Reason: SkipUserBlockUnsafe,
			Detail: "原文无法解析，用户分区归属不可确定：" + err.Error()}
	}
	names := doc.SectionNames()
	for _, name := range UserOwnedSections() {
		if countName(names, name) > 1 {
			return &SkipError{Path: path, Reason: SkipUserBlockUnsafe,
				Detail: fmt.Sprintf("分区「%s」重复出现 %d 次，用户内容归属不可确定", name, countName(names, name))}
		}
		span, ok := doc.Section(name)
		if !ok {
			continue
		}
		body := before[span.Body:span.End]
		if len(bytes.TrimSpace(body)) == 0 {
			continue
		}
		// 「用户补充」永不写：要求整段正文（含尾随空行）逐字连续保留。
		// 「存疑与待验证」允许在尾部追加：要求既有正文主体逐字连续保留，
		// 追加点只能落在原正文最后一个非空行之后。
		if !neverWrite(name) {
			body = bytes.TrimRight(body, " \t\r\n")
		}
		if !bytes.Contains(after, body) {
			return &SkipError{Path: path, Reason: SkipUserBlockUnsafe,
				Detail: fmt.Sprintf("分区「%s」的既有字节未逐字保留", name)}
		}
	}
	return nil
}

func neverWrite(section string) bool {
	for _, name := range mdfile.NeverWriteSections() {
		if section == name {
			return true
		}
	}
	return false
}

func countName(names []string, want string) int {
	n := 0
	for _, name := range names {
		if name == want {
			n++
		}
	}
	return n
}

// writableSection 是分区写白名单：先拒「用户补充」（B2），再按类型白名单放行。
func writableSection(kind mdfile.Kind, section string) error {
	if neverWrite(section) {
		return fmt.Errorf("%w：%s", ErrUserSectionWrite, section)
	}
	for _, ok := range mdfile.AutoWritableSections(kind) {
		if section == ok {
			return nil
		}
	}
	return fmt.Errorf("%w：%s（%s）", ErrSectionNotWritable, section, kind)
}
