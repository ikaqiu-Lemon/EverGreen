package mdfile

// 半开区间索引 + 只读解析（施工索引 §16.2）。
//
// 硬约束：
//   - Raw 只读，**永不原地修改**；任何落盘都只是 d.Raw[a:b] 的区间拼接 + 目标位置插入字节。
//   - 本包**没有**任何「结构体 → 文本」的回写函数：写路径只有 append.go 的字节插入一条。
//   - 用户内容只用 []byte：不做 for range string、不做 rune 迭代重建、不做 strings 规范化
//     （非法 UTF-8 会有损）；只有 block_hash 计算时做**只读**规范化，不影响落盘字节。

import (
	"bytes"
	"errors"
	"fmt"
)

// FMDelim 是 frontmatter 分隔线（行首三连字符）。
var FMDelim = []byte("---")

// Span 是一个 H2 分区的半开区间索引。
type Span struct {
	Name  string // H2 分区名（标题行去掉 "## " 与行尾空白后的原文字节）
	Start int    // 分区标题行起始
	Body  int    // 分区正文起始（标题行之后）
	End   int    // 下一个 H2 起始 / EOF
}

// Doc 是一份 Markdown 文档的只读索引。
type Doc struct {
	Raw      []byte // 原始字节，只读，永不原地修改
	HasFM    bool   // 是否存在 frontmatter
	FMStart  int    // frontmatter 内容起始（起始分隔行之后）
	FMEnd    int    // frontmatter 内容结束（结束分隔行之前）
	BodyFrom int    // 正文起始（frontmatter 结束分隔行之后；无 frontmatter 时为 0）
	Sections []Span
}

// 解析错误。E4（frontmatter YAML 不可解析）由 DecodeFM 返回，见 frontmatter.go。
var (
	// ErrFMUnterminated frontmatter 起始分隔行存在但没有结束分隔行。
	ErrFMUnterminated = errors.New("frontmatter 未闭合：缺少结束的 --- 行")
	// ErrSectionNotFound 目标分区不存在。
	ErrSectionNotFound = errors.New("分区不存在")
)

// Parse 建立索引。**不复制、不改写任何字节**。
//
// 无 frontmatter 的文档同样解析成功（HasFM=false），因为原文正文可以是任意字节。
func Parse(raw []byte) (*Doc, error) {
	d := &Doc{Raw: raw}
	if err := d.indexFrontmatter(); err != nil {
		return nil, err
	}
	d.indexSections()
	return d, nil
}

func (d *Doc) indexFrontmatter() error {
	raw := d.Raw
	if !hasFMOpen(raw) {
		d.HasFM = false
		d.FMStart, d.FMEnd, d.BodyFrom = 0, 0, 0
		return nil
	}
	open := lineEnd(raw, 0)
	for at := open; at < len(raw); {
		end := lineEnd(raw, at)
		if isDelimLine(raw[at:end]) {
			d.HasFM = true
			d.FMStart = open
			d.FMEnd = at
			d.BodyFrom = end
			return nil
		}
		at = end
	}
	return ErrFMUnterminated
}

// hasFMOpen 报告首行是否为 frontmatter 起始分隔行。
func hasFMOpen(raw []byte) bool {
	if len(raw) < len(FMDelim) {
		return false
	}
	return isDelimLine(raw[:lineEnd(raw, 0)])
}

// isDelimLine 报告一整行（含行尾换行）是否恰为 `---`（容忍 CRLF 与行尾空白）。
func isDelimLine(line []byte) bool {
	t := trimLineEnd(line)
	return bytes.Equal(t, FMDelim)
}

// lineEnd 返回自 at 起这一行的结束偏移（**含**行尾 \n；无换行则为 len）。
func lineEnd(raw []byte, at int) int {
	i := bytes.IndexByte(raw[at:], '\n')
	if i < 0 {
		return len(raw)
	}
	return at + i + 1
}

// trimLineEnd 去掉一行末尾的 \n / \r 与尾随空白（只读操作，不影响落盘字节）。
func trimLineEnd(line []byte) []byte {
	for len(line) > 0 {
		switch line[len(line)-1] {
		case '\n', '\r', ' ', '\t':
			line = line[:len(line)-1]
		default:
			return line
		}
	}
	return line
}

// indexSections 切分 H2 分区。围栏代码块内的 ## 由 inFence 状态机屏蔽（§16.2）。
func (d *Doc) indexSections() {
	raw := d.Raw
	var fence []byte // 当前围栏标记（nil 表示不在围栏内）
	for at := d.BodyFrom; at < len(raw); {
		end := lineEnd(raw, at)
		line := raw[at:end]
		if fence != nil {
			if isFenceClose(line, fence) {
				fence = nil
			}
			at = end
			continue
		}
		if mark := fenceMark(line); mark != nil {
			fence = mark
			at = end
			continue
		}
		if name, ok := h2Name(line); ok {
			if n := len(d.Sections); n > 0 {
				d.Sections[n-1].End = at
			}
			d.Sections = append(d.Sections, Span{Name: name, Start: at, Body: end, End: len(raw)})
		}
		at = end
	}
}

// h2Name 判定 H2 标题行并返回分区名（原文字节转 string，仅用于名称比较与诊断）。
func h2Name(line []byte) (string, bool) {
	if !bytes.HasPrefix(line, []byte("## ")) {
		return "", false
	}
	if bytes.HasPrefix(line, []byte("### ")) {
		return "", false
	}
	return string(trimLineEnd(line[len("## "):])), true
}

// fenceMark 返回该行开启的围栏标记（``` 或 ~~~，允许更长），不是围栏则返回 nil。
func fenceMark(line []byte) []byte {
	t := trimLineEnd(line)
	for _, c := range []byte{'`', '~'} {
		n := 0
		for n < len(t) && t[n] == c {
			n++
		}
		if n >= 3 {
			return t[:n]
		}
	}
	return nil
}

// isFenceClose 报告该行是否闭合 open 围栏（同字符且长度不短于开启标记）。
func isFenceClose(line, open []byte) bool {
	mark := fenceMark(line)
	if mark == nil || mark[0] != open[0] || len(mark) < len(open) {
		return false
	}
	// 闭合行除围栏标记外不得有其他内容。
	return len(trimLineEnd(line)) == len(mark)
}

// Section 按名字取分区。
func (d *Doc) Section(name string) (Span, bool) {
	for _, s := range d.Sections {
		if s.Name == name {
			return s, true
		}
	}
	return Span{}, false
}

// SectionNames 返回文档里 H2 分区名的出现顺序。
func (d *Doc) SectionNames() []string {
	out := make([]string, 0, len(d.Sections))
	for _, s := range d.Sections {
		out = append(out, s.Name)
	}
	return out
}

// FMBytes 返回 frontmatter 内容的原始字节切片（不复制）。
func (d *Doc) FMBytes() []byte {
	if !d.HasFM {
		return nil
	}
	return d.Raw[d.FMStart:d.FMEnd]
}

// Render 以**区间拼接**方式重建字节：frontmatter 区间 + 前言区间 + 各分区区间。
//
// 无逻辑变更时逐字返回与 Raw 相同的字节（本包不提供任何原地改写入口，
// 因此「无逻辑变更」是常态）。它同时是 §16.4 写前字节自检的被检对象：
// 索引若不连续 / 不完整，拼接结果就不等于 Raw，store.WriteGuarded 会据此拒写。
func (d *Doc) Render() []byte {
	out := make([]byte, 0, len(d.Raw))
	out = append(out, d.Raw[:d.BodyFrom]...)
	preambleEnd := len(d.Raw)
	if len(d.Sections) > 0 {
		preambleEnd = d.Sections[0].Start
	}
	out = append(out, d.Raw[d.BodyFrom:preambleEnd]...)
	for _, s := range d.Sections {
		out = append(out, d.Raw[s.Start:s.End]...)
	}
	return out
}

// SelfCheck 实现「Parse → Render 字节自检」：不等即返回错误（调用方据此拒写）。
func SelfCheck(raw []byte) error {
	d, err := Parse(raw)
	if err != nil {
		return err
	}
	if !bytes.Equal(d.Render(), raw) {
		return fmt.Errorf("Parse→Render 字节自检失败：索引未覆盖全部字节（len %d → %d）",
			len(raw), len(d.Render()))
	}
	return nil
}
