package mdfile

// 块切分与 block_hash（技术方案 §4.2）。
//
// S1 的块切分**只用于「追加」的定位**：不做块替换、不做安全合并、不做 base_block_hash 复核
// （replace_block 属 S2，安全合并属 S5）。
//
// 块 = 段落（连续非空行）/ 顶层列表项（连同缩进子项与延续行算一个块）/
//      围栏代码块（整段一个块，内部空行不切分）/ 连续表格行（一个块）/ H3 小标题（自身一个块）。

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// BlockKind 是块型。
type BlockKind string

// 五种块型。
const (
	BlockParagraph BlockKind = "paragraph"
	BlockListItem  BlockKind = "list_item"
	BlockFence     BlockKind = "fence"
	BlockTable     BlockKind = "table"
	BlockHeading3  BlockKind = "heading3"
)

// Block 是一个块的半开区间索引（偏移是**文档级**绝对偏移）。
type Block struct {
	Kind  BlockKind
	Start int
	End   int
}

// Bytes 返回块的原始字节切片（不复制、不改写）。
func (b Block) Bytes(raw []byte) []byte { return raw[b.Start:b.End] }

// Blocks 切分某个分区的正文为块。分区不存在时返回 ErrSectionNotFound。
func (d *Doc) Blocks(name string) ([]Block, error) {
	s, ok := d.Section(name)
	if !ok {
		return nil, fmt.Errorf("%w：%s", ErrSectionNotFound, name)
	}
	return SplitBlocks(d.Raw, s.Body, s.End), nil
}

// SplitBlocks 在 raw[from:to] 上切块。块之间的空行不属于任何块（分隔符），
// 但**顶层列表项内部**用于分隔缩进子项的空行属于该块。
func SplitBlocks(raw []byte, from, to int) []Block {
	var out []Block
	at := from
	for at < to {
		end := lineEnd(raw, at)
		if end > to {
			end = to
		}
		line := raw[at:end]
		if isBlank(line) {
			at = end
			continue
		}
		switch {
		case fenceMark(line) != nil:
			out = append(out, Block{Kind: BlockFence, Start: at, End: fenceEnd(raw, at, to)})
		case isH3(line):
			out = append(out, Block{Kind: BlockHeading3, Start: at, End: end})
		case isTopListItem(line):
			out = append(out, Block{Kind: BlockListItem, Start: at, End: listItemEnd(raw, at, to)})
		case isTableRow(line):
			out = append(out, Block{Kind: BlockTable, Start: at, End: tableEnd(raw, at, to)})
		default:
			out = append(out, Block{Kind: BlockParagraph, Start: at, End: paragraphEnd(raw, at, to)})
		}
		at = out[len(out)-1].End
	}
	return out
}

// fenceEnd 返回围栏代码块的结束偏移：闭合行之后（未闭合则到 to）。内部空行不切分。
func fenceEnd(raw []byte, at, to int) int {
	open := fenceMark(raw[at:clampLine(raw, at, to)])
	cur := clampLine(raw, at, to)
	for cur < to {
		end := clampLine(raw, cur, to)
		if isFenceClose(raw[cur:end], open) {
			return end
		}
		cur = end
	}
	return to
}

// listItemEnd 返回顶层列表项的结束偏移：缩进子项与延续行都算同一个块。
func listItemEnd(raw []byte, at, to int) int {
	cur := clampLine(raw, at, to)
	for cur < to {
		end := clampLine(raw, cur, to)
		line := raw[cur:end]
		if isBlank(line) {
			// 空行之后仍是缩进行 → 同一个列表项（缩进子项之间允许空行）；否则块在空行前结束。
			if next, nextEnd := nextNonBlank(raw, end, to); next >= 0 && isIndented(raw[next:nextEnd]) {
				cur = end
				continue
			}
			return cur
		}
		if isIndented(line) {
			cur = end
			continue
		}
		// 顶层新块起点：新列表项 / 标题 / 围栏 / 表格。
		if isTopListItem(line) || isH3(line) || bytes.HasPrefix(line, []byte("#")) ||
			fenceMark(line) != nil || isTableRow(line) {
			return cur
		}
		cur = end // 顶层延续行（lazy continuation）
	}
	return to
}

// tableEnd 返回连续表格行的结束偏移。
func tableEnd(raw []byte, at, to int) int {
	cur := clampLine(raw, at, to)
	for cur < to {
		end := clampLine(raw, cur, to)
		if !isTableRow(raw[cur:end]) {
			return cur
		}
		cur = end
	}
	return to
}

// paragraphEnd 返回段落（连续非空行）的结束偏移。
func paragraphEnd(raw []byte, at, to int) int {
	cur := clampLine(raw, at, to)
	for cur < to {
		end := clampLine(raw, cur, to)
		line := raw[cur:end]
		if isBlank(line) || isH3(line) || bytes.HasPrefix(line, []byte("#")) ||
			fenceMark(line) != nil || isTopListItem(line) || isTableRow(line) {
			return cur
		}
		cur = end
	}
	return to
}

func clampLine(raw []byte, at, to int) int {
	e := lineEnd(raw, at)
	if e > to {
		return to
	}
	return e
}

func nextNonBlank(raw []byte, at, to int) (int, int) {
	for at < to {
		end := clampLine(raw, at, to)
		if !isBlank(raw[at:end]) {
			return at, end
		}
		at = end
	}
	return -1, -1
}

func isBlank(line []byte) bool { return len(trimLineEnd(line)) == 0 }

func isIndented(line []byte) bool {
	return len(line) > 0 && (line[0] == ' ' || line[0] == '\t')
}

func isH3(line []byte) bool { return bytes.HasPrefix(line, []byte("### ")) }

func isTableRow(line []byte) bool { return bytes.HasPrefix(line, []byte("|")) }

// isTopListItem 判定顶层（零缩进）列表项：`- ` / `* ` / `+ ` / `1. ` / `1) `。
func isTopListItem(line []byte) bool {
	if len(line) < 2 {
		return false
	}
	switch line[0] {
	case '-', '*', '+':
		return line[1] == ' ' || line[1] == '\t'
	}
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i == 0 || i+1 >= len(line) {
		return false
	}
	if line[i] != '.' && line[i] != ')' {
		return false
	}
	return line[i+1] == ' ' || line[i+1] == '\t'
}

// BlockHashLen 是 block_hash 的十六进制长度。
const BlockHashLen = 16

// BlockHash = sha256(规范化块文本)[:16]。
//
// 规范化（**只读**，绝不影响落盘字节）：统一换行（CRLF → LF）+ 去每行尾随空白
// + 去块尾多余空行。因此同一块在 CRLF/LF 与行尾空白差异下 hash 相同。
func BlockHash(block []byte) string {
	sum := sha256.Sum256(NormalizeBlock(block))
	return hex.EncodeToString(sum[:])[:BlockHashLen]
}

// NormalizeBlock 返回规范化后的**新**字节（输入不被修改）。
func NormalizeBlock(block []byte) []byte {
	out := make([]byte, 0, len(block))
	at := 0
	for at < len(block) {
		end := lineEnd(block, at)
		out = append(out, trimLineEnd(block[at:end])...)
		out = append(out, '\n')
		at = end
	}
	for len(out) > 0 && out[len(out)-1] == '\n' {
		out = out[:len(out)-1]
	}
	return out
}

// Locator 是块定位符 `<id>#<分区>#<序号>`。
//
// **只用于报告 / 诊断的可读性：不稳定、不写入权威侧、不出现在任何写回文件的内容中。**
// 写路径（append.go）不引用本函数。
func Locator(id, section string, index int) string {
	return fmt.Sprintf("%s#%s#%d", id, section, index)
}
