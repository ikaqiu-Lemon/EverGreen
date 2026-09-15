package mdfile

// 原文正文的**结构锚点计数**（Schema v2 契约 §4.3 的 `src_anchors`）。
//
// # 为什么落在 mdfile 而不是 plan
//
// 「一份 Markdown 里有几个 H2/H3 标题」是文档结构事实，与它被谁消费无关。
// 本包已经拥有围栏状态机（indexSections 的 fenceMark / isFenceClose）与
// frontmatter 边界（BodyFrom）这两件必需品；把计数放在 plan 里就得在那边重写一遍
// 围栏屏蔽，而重写的第一个后果是「代码块里的 `## 注释` 被数成章节」——
// 那会让 `W21` 对任何带 Markdown 示例的技术文章误报。
//
// 计数口径（逐字对齐契约 §4.3）：
//   - 只数 **H2 与 H3**：H1 是文档标题（原文通常恰一个），H4 以下是段内细分，
//     都不是「章节」这一层的结构信号；
//   - **去空标题**：`##` 后面没有实际文字的行不计入——它不承载结构；
//   - 围栏代码块内的 `##` / `###` 一律不计入（与 indexSections 同一套状态机）；
//   - 只数 frontmatter **之后**的正文：frontmatter 里不会有标题行，但把边界写明确
//     可以让「原文正好以 --- 开头」这种输入有确定行为。

import "bytes"

// CountBodyAnchors 返回一份文档正文里的 H2 + H3 标题数（去空标题，屏蔽围栏）。
//
// 解析失败（frontmatter 未闭合）时返回 0：调用方据此**不判**诊断——
// 拿不到原文结构时猜一个数字比不报更糟（契约 §4.3 对读不到 Source 的处置同源）。
func CountBodyAnchors(raw []byte) int {
	d, err := Parse(raw)
	if err != nil {
		return 0
	}
	return d.CountBodyAnchors()
}

// CountBodyAnchors 是 Doc 上的同名方法（已建索引时不必重复解析）。
func (d *Doc) CountBodyAnchors() int {
	raw := d.Raw
	n := 0
	var fence []byte
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
		if anchorTitle(line) {
			n++
		}
		at = end
	}
	return n
}

// anchorTitle 判定一行是否为**非空**的 H2 或 H3 标题。
//
// 判定顺序：先排除 H4+（`#### `），再认 `### ` 与 `## `。
// 不复用 h2Name 是因为那个函数的职责是「切分 H2 分区」——它必须把 H3 排除掉，
// 而这里恰恰要把 H3 数进来。两个相反的需求共用一个函数，就得给它加一个布尔参数，
// 而带布尔开关的判定函数是「调用点写错一个 true 就静默改变语义」的经典形态。
func anchorTitle(line []byte) bool {
	if bytes.HasPrefix(line, []byte("#### ")) {
		return false
	}
	for _, mark := range [][]byte{[]byte("### "), []byte("## ")} {
		if !bytes.HasPrefix(line, mark) {
			continue
		}
		return len(trimLineEnd(line[len(mark):])) > 0
	}
	return false
}
