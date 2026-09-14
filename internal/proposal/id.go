package proposal

// 提案 ID 与落位路径（提案合同 §7.1 表逐行）。
//
//	ID   `p-<yyyymmdd>-<seq>`，`<seq>` 是**当日三位序号**（`001`–`999`）
//	目录 `proposals/`，一项一文件，**不属于任何领域**
//	文件 默认 `<id>.md`，例：`p-20260701-001.md`
//
// 文件**允许被重命名或移动**：ID 写在 frontmatter，`id → path` 由扫描完成（F2），
// 因此本文件的 Rel 只是**默认落位**，任何判定都不得把路径当主键。
// 处理过的提案**留在 `proposals/` 原地，不搬目录**（§10.1 第 3 条）。

import (
	"errors"
	"fmt"
	"path"
	"strconv"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// DirProposals 是提案目录名（vault 根，不属于任何领域）。
// 口径与 store / query 两侧的布局常量同源；F1 已把该目录名写进冻结合同。
const DirProposals = "proposals"

// SeqWidth 是当日序号的固定宽度（三位，零填充）。
const SeqWidth = 3

// SeqMin / SeqMax 是当日序号的闭区间边界：三位十进制的可用范围。
const (
	SeqMin = 1
	SeqMax = 999
)

var (
	// ErrSeqOutOfRange 当日序号越界（三位十进制放不下）。
	ErrSeqOutOfRange = errors.New("提案当日序号越界：<seq> 必须是三位十进制（001–999）")
	// ErrIDPrefix ID 前缀不是提案前缀。
	ErrIDPrefix = errors.New("不是提案 ID：前缀必须是 p-")
	// ErrIDSeqShape ID 的序号段不是三位数字。
	ErrIDSeqShape = errors.New("非法提案 ID：<seq> 段必须恰三位数字（001–999）")
)

// ID 是提案 ID（前缀 p-）。与 model 的三个 S1 ID 类型一样是**独立命名类型**，
// 跨类型赋值是编译期错误，不会被当成卡 / 笔记 / 原文 ID 使用。
type ID string

// String 返回 ID 字面量。
func (id ID) String() string { return string(id) }

// Valid 报告 ID 形态是否合法（前缀 + 8 位日期 + 三位序号）。
func (id ID) Valid() bool { _, _, err := ParseID(string(id)); return err == nil }

// NewID 拼一个提案 ID：`p-<yyyymmdd>-<seq>`，seq 零填充到三位。
//
// 确定性：同一 (日期, 序号) 恒得同一 ID，无随机、无时间读取——「今天」由调用方传入，
// 本包不读系统时钟（这样测试与报告可复算）。
func NewID(d model.Date, seq int) (ID, error) {
	if seq < SeqMin || seq > SeqMax {
		return "", fmt.Errorf("%w：得到 %d", ErrSeqOutOfRange, seq)
	}
	return ID(model.PrefixProposal + d.Compact() + "-" + FormatSeq(seq)), nil
}

// FormatSeq 把序号格式化成固定三位（零填充）。
func FormatSeq(seq int) string {
	s := strconv.Itoa(seq)
	for len(s) < SeqWidth {
		s = "0" + s
	}
	return s
}

// ParseID 解析提案 ID，返回日期段（yyyymmdd）与当日序号。
//
// 比 model.ParseID 更严的一层：提案的第三段**不是**自由 slug，而是**恰三位数字**，
// 因此 `p-20260701-vendor` 这类串在这里被拒（同日多提案的序号不冲突要靠它保证）。
func ParseID(raw string) (date string, seq int, err error) {
	p, err := model.ParseID(raw)
	if err != nil {
		return "", 0, err
	}
	if p.Prefix != model.PrefixProposal {
		return "", 0, fmt.Errorf("%w：%q 的前缀是 %q", ErrIDPrefix, raw, p.Prefix)
	}
	if len(p.Slug) != SeqWidth {
		return "", 0, fmt.Errorf("%w：%q 的 <seq> 段是 %q", ErrIDSeqShape, raw, p.Slug)
	}
	n, convErr := strconv.Atoi(p.Slug)
	if convErr != nil || n < SeqMin || n > SeqMax {
		return "", 0, fmt.Errorf("%w：%q 的 <seq> 段是 %q", ErrIDSeqShape, raw, p.Slug)
	}
	return p.Date, n, nil
}

// Rel 是提案的**默认**落位路径：`proposals/<p-id>.md`。
//
// 只是默认值：既有提案允许改名 / 移动，定位一律靠扫描 frontmatter 的 id。
func Rel(id ID) string { return path.Join(DirProposals, string(id)+".md") }

// IsProposalRel 报告 vault 内相对路径是否落在提案目录下。
// 知识扫描面靠**目录**把提案排除出去（§10.3），这里给出同一份判据的只读实现。
func IsProposalRel(rel string) bool {
	parts := splitSlash(path.Clean(rel))
	return len(parts) >= 2 && parts[0] == DirProposals
}

func splitSlash(p string) []string {
	var out []string
	start := 0
	for i := 0; i < len(p); i++ {
		if p[i] == '/' {
			out = append(out, p[start:i])
			start = i + 1
		}
	}
	return append(out, p[start:])
}

// NextSeq 给出「同日下一个可用序号」：已用序号集合里当日最大值 +1。
//
// 入参是**已扫描到的全部提案 ID**（顺序无关，重复无害）：同日多提案的序号不冲突
// 由此保证，且不读时钟、不读文件系统，结果只取决于入参集合（可复算）。
// 无同日提案时返回 SeqMin；当日已用满 999 时返回 ErrSeqOutOfRange。
func NextSeq(date string, existing []ID) (int, error) {
	max := 0
	for _, id := range existing {
		d, seq, err := ParseID(string(id))
		if err != nil || d != date {
			// 形态非法或不同日的 ID 不参与当日序号计算；形态校验由 ParseID 单独承担，
			// 这里不吞掉事实——调用方拿到的是「当日已用序号」这一件事。
			continue
		}
		if seq > max {
			max = seq
		}
	}
	if max+1 > SeqMax {
		return 0, fmt.Errorf("%w：%s 当日序号已用满 %d", ErrSeqOutOfRange, date, SeqMax)
	}
	return max + 1, nil
}
