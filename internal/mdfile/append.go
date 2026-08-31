package mdfile

// 字节区间写入（施工索引 §16.2 的「写」侧）。
//
// **本文件是本包唯一的写路径，机制只有一条：`d.Raw[:at]` + 插入字节 + `d.Raw[at:]`。**
// 没有任何「结构体 → 文本」的回写函数；未知字段 / 未知分区 / 未知块永不进入重写路径。
// 空行：从不重写包含它的区间，追加时回退到「最后一个非空行之后」插入，保留原有尾随空行。
// 缩进：探测既有风格并沿用，绝不规范化；空序列不猜缩进 → 返回 ErrNoIndentStyle 供上层出 warning。

import (
	"bytes"
	"errors"
	"fmt"
)

var (
	// ErrPayloadNotLineTerminated 追加载荷必须以换行结束：本包绝不替调用方补字节。
	ErrPayloadNotLineTerminated = errors.New("追加载荷必须以 \\n 结束（本包不改写载荷一个字节）")
	// ErrSectionNeverWrite 目标分区属「任何时候都不得写入」（安全底线 B2）。
	ErrSectionNeverWrite = errors.New("该分区任何时候都不得写入（安全底线 B2）")
	// ErrNoFrontmatter 文档没有 frontmatter。
	ErrNoFrontmatter = errors.New("文档没有 frontmatter")
	// ErrFMKeyExists frontmatter 已有同名键：只追加、不改写（安全底线 B1）。
	ErrFMKeyExists = errors.New("frontmatter 已存在同名键（默认只追加，不改写既有键）")
	// ErrNoIndentStyle 序列为空，无既有缩进风格可沿用；S1 不猜，交上层报 warning。
	ErrNoIndentStyle = errors.New("序列为空，无既有缩进风格可沿用（S1 不猜缩进）")
	// ErrSeqKeyNotFound frontmatter 里没有该序列键。
	ErrSeqKeyNotFound = errors.New("frontmatter 无该序列键")
)

// insert 是唯一的落盘字节构造方式：区间拼接 + 在 at 处插入 payload。
func insert(raw []byte, at int, payload []byte) []byte {
	out := make([]byte, 0, len(raw)+len(payload))
	out = append(out, raw[:at]...)
	out = append(out, payload...)
	out = append(out, raw[at:]...)
	return out
}

// AppendPoint 返回向分区追加内容的插入偏移：分区内**最后一个非空行之后**。
// 分区为空（或全是空行）时插入点是分区正文起始，原有尾随空行因此完整保留在插入点之后。
func (d *Doc) AppendPoint(section string) (int, error) {
	s, ok := d.Section(section)
	if !ok {
		return 0, fmt.Errorf("%w：%s", ErrSectionNotFound, section)
	}
	at := s.Body
	for cur := s.Body; cur < s.End; {
		end := clampLine(d.Raw, cur, s.End)
		if !isBlank(d.Raw[cur:end]) {
			at = end
		}
		cur = end
	}
	return at, nil
}

// AppendToSection 在目标分区尾部追加 payload，返回**新的**字节切片。
//
// 安全底线 B1：只追加，绝不替换 / 删除既有块；
// 安全底线 B2：「用户补充」任何时候都不得写入，直接拒绝。
// payload 逐字插入（含其中的空行与缩进），本包不做任何规范化。
func (d *Doc) AppendToSection(section string, payload []byte) ([]byte, error) {
	for _, never := range NeverWriteSections() {
		if section == never {
			return nil, fmt.Errorf("%w：%s", ErrSectionNeverWrite, section)
		}
	}
	if len(payload) == 0 {
		return nil, ErrPayloadNotLineTerminated
	}
	if payload[len(payload)-1] != '\n' {
		return nil, ErrPayloadNotLineTerminated
	}
	at, err := d.AppendPoint(section)
	if err != nil {
		return nil, err
	}
	return insert(d.Raw, at, payload), nil
}

// AppendFMKey 把新键追加到 frontmatter **末尾**（键顺序不重排）。
// 已存在同名键时返回 ErrFMKeyExists——默认只追加，不改写既有键。
func (d *Doc) AppendFMKey(key string, value []byte) ([]byte, error) {
	if !d.HasFM {
		return nil, ErrNoFrontmatter
	}
	exists, err := d.HasFMKey(key)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, fmt.Errorf("%w：%s", ErrFMKeyExists, key)
	}
	line := make([]byte, 0, len(key)+len(value)+3)
	line = append(line, key...)
	line = append(line, ':', ' ')
	line = append(line, value...)
	line = append(line, '\n')
	return insert(d.Raw, d.FMEnd, line), nil
}

// SeqSpan 是 frontmatter 里一个块状序列（`key:` 之下的 `- ` 行集合）的索引。
type SeqSpan struct {
	Key    string
	Indent []byte // 首个 `- ` 行的前导空白（既有风格，沿用不规范化）
	Start  int    // 首个 `- ` 行起始（空序列时等于 InsertAt）
	End    int    // 末个序列行之后（插入点）
	Items  int
}

// FMSeq 定位 frontmatter 顶层键 key 之下的块状序列。
//
// 缩进风格取该序列**首个 `- ` 的前导空白**（§16.2）；序列为空则返回 ErrNoIndentStyle，
// 由上层出 warning 并跳过——S1 不猜缩进。
func (d *Doc) FMSeq(key string) (SeqSpan, error) {
	if !d.HasFM {
		return SeqSpan{}, ErrNoFrontmatter
	}
	fmStart, fmEnd := d.FMStart, d.FMEnd
	prefix := append([]byte(key), ':')
	for cur := fmStart; cur < fmEnd; {
		end := clampLine(d.Raw, cur, fmEnd)
		line := d.Raw[cur:end]
		if !bytes.HasPrefix(line, prefix) {
			cur = end
			continue
		}
		// 命中顶层键行；序列行是其后连续的缩进 `- ` 行（允许中间夹缩进的续行）。
		seq := SeqSpan{Key: key, Start: end, End: end}
		at := end
		for at < fmEnd {
			lend := clampLine(d.Raw, at, fmEnd)
			l := d.Raw[at:lend]
			if isBlank(l) {
				break
			}
			if !isIndented(l) {
				break
			}
			if item, indent := seqItemIndent(l); item {
				if seq.Items == 0 {
					seq.Indent = indent
					seq.Start = at
				}
				seq.Items++
			}
			seq.End = lend
			at = lend
		}
		if seq.Items == 0 {
			return seq, ErrNoIndentStyle
		}
		return seq, nil
	}
	return SeqSpan{}, fmt.Errorf("%w：%s", ErrSeqKeyNotFound, key)
}

// seqItemIndent 判定缩进的 `- ` 行并返回其前导空白。
func seqItemIndent(line []byte) (bool, []byte) {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	if i == 0 || i+1 >= len(line) || line[i] != '-' {
		return false, nil
	}
	if line[i+1] != ' ' && line[i+1] != '\t' {
		return false, nil
	}
	return true, line[:i]
}

// AppendFMSeqItem 向 frontmatter 的块状序列尾部追加一条，沿用既有缩进风格。
//
// item 是**不含**前导缩进与 `- ` 的条目字节（可含换行以表达多行映射，
// 多行时调用方需自行为续行准备缩进——本包不改写 item 一个字节）。
func (d *Doc) AppendFMSeqItem(key string, item []byte) ([]byte, error) {
	seq, err := d.FMSeq(key)
	if err != nil {
		return nil, err
	}
	if len(item) == 0 {
		return nil, ErrPayloadNotLineTerminated
	}
	if item[len(item)-1] != '\n' {
		return nil, ErrPayloadNotLineTerminated
	}
	payload := make([]byte, 0, len(seq.Indent)+2+len(item))
	payload = append(payload, seq.Indent...)
	payload = append(payload, '-', ' ')
	payload = append(payload, item...)
	return insert(d.Raw, seq.End, payload), nil
}
