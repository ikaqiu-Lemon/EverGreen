package store

// 新建产物的**字节拼装**（供 note.go / card.go 使用）。
//
// 写路径硬约束（§16.3）：这里是「按模板拼字节」，**不是**「结构体 → 序列化」。
// 全程只做 []byte 追加，永不把已存在的文档解析后重新渲染；YAML 库在本包只用于**只读**解析。
// frontmatter 键序 = 下面 append 的顺序，与 §4.1 / §4.3 逐字一致；
// 标量一律单引号包裹（'' 转义），日期 / 时刻因此不会被 YAML 当成 timestamp 类型。

import (
	"errors"
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
)

// ErrUnquotableScalar frontmatter 标量含换行：S1 不拼多行标量（避免猜缩进）。
var ErrUnquotableScalar = errors.New("frontmatter 标量不得含换行（S1 不拼装多行标量）")

// ErrMissingSection 新建产物缺必写分区。
var ErrMissingSection = errors.New("缺必写分区")

// seqIndent 是本工具**新建**块状序列时使用的缩进；对**既有**文件一律探测既有风格
// （mdfile.FMSeq），此处只用于自己刚拼出来的新文件。
const seqIndent = "  "

// quoted 把标量包成单引号 YAML 标量：内部单引号按 YAML 规则写成两个。
func quoted(s string) ([]byte, error) {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '\'')
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\n', '\r':
			return nil, fmt.Errorf("%w：%q", ErrUnquotableScalar, s)
		case '\'':
			out = append(out, '\'', '\'')
		default:
			out = append(out, s[i])
		}
	}
	return append(out, '\''), nil
}

// fmLine 拼一行 `key: '值'`。
func fmLine(key, value string) ([]byte, error) {
	q, err := quoted(value)
	if err != nil {
		return nil, fmt.Errorf("%s：%w", key, err)
	}
	line := make([]byte, 0, len(key)+len(q)+3)
	line = append(line, key...)
	line = append(line, ':', ' ')
	line = append(line, q...)
	return append(line, '\n'), nil
}

// fmSeq 拼一个块状序列（每项一行 `  - '值'`）。空序列不写键（S1 不产出空序列，
// 因为空序列没有缩进风格可供后续追加沿用）。
func fmSeq(key string, items []string) ([]byte, error) {
	if len(items) == 0 {
		return nil, nil
	}
	out := make([]byte, 0, 16*len(items))
	out = append(out, key...)
	out = append(out, ':', '\n')
	for _, it := range items {
		q, err := quoted(it)
		if err != nil {
			return nil, fmt.Errorf("%s：%w", key, err)
		}
		out = append(out, seqIndent...)
		out = append(out, '-', ' ')
		out = append(out, q...)
		out = append(out, '\n')
	}
	return out, nil
}

// document 把 frontmatter 字节与固定分区顺序拼成完整文档字节。
// sections 是「分区名 → 载荷」；缺失的固定分区留空（分区头照写，正文为空）。
// 载荷逐字插入，必须以 \n 结束。
func document(kind mdfile.Kind, fm []byte, sections map[string][]byte) ([]byte, error) {
	out := make([]byte, 0, len(fm)+256)
	out = append(out, "---\n"...)
	out = append(out, fm...)
	out = append(out, "---\n\n"...)
	for _, name := range mdfile.KnownSections(kind) {
		out = append(out, "## "...)
		out = append(out, name...)
		out = append(out, '\n', '\n')
		payload, ok := sections[name]
		if !ok || len(payload) == 0 {
			continue
		}
		if payload[len(payload)-1] != '\n' {
			return nil, mdfile.ErrPayloadNotLineTerminated
		}
		out = append(out, payload...)
		out = append(out, '\n')
	}
	if req := mdfile.RequiredSection(kind); req != "" {
		if len(sections[req]) == 0 {
			return nil, fmt.Errorf("%w：「%s」（%s）", ErrMissingSection, req, kind)
		}
	}
	return out, nil
}

// sectionMap 把有序的分区追加动作折叠成「分区名 → 载荷」，并拒绝写「用户补充」（B2）。
func sectionMap(kind mdfile.Kind, appends []SectionAppend) (map[string][]byte, error) {
	out := make(map[string][]byte, len(appends))
	known := map[string]bool{}
	for _, name := range mdfile.KnownSections(kind) {
		known[name] = true
	}
	for _, sa := range appends {
		if neverWrite(sa.Section) {
			return nil, fmt.Errorf("%w：%s", ErrUserSectionWrite, sa.Section)
		}
		if !known[sa.Section] {
			return nil, fmt.Errorf("%w：%s（%s）", ErrSectionNotWritable, sa.Section, kind)
		}
		out[sa.Section] = append(out[sa.Section], sa.Payload...)
	}
	return out, nil
}
