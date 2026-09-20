package plan

// v2 `write_note` 的**结构资产保真校验**（Schema v2 契约 §4.2.1 第 5 条）：
// 在来源覆盖（source_ref + omissions 已判过形态 / 顺序 / 交叉 / 空洞）之上，进一步核验
// 「图片 URL、图注、代码块内容、表格行、列表项、引用、链接、脚注」这八类结构资产的
// **数量与相对顺序**在 Source 对应来源范围与整理正文之间一致——格式恢复或翻译不免除保真。
//
// # 诚实到机器能力为止
//
// 只锁可稳定标识的身份：图片 / 链接 / 脚注 / 代码锁精确签名（目标值 / label / payload），
// 可标识事件的乱序也判；纯文本型资产（表格行 / 列表项 / 引用）只锁结构形状、数量与跨类型顺序，
// 不声称能识别两条同形但语义互换的翻译行。不做 prose / 翻译语义相等，不要求图注 / 单元格 /
// 列表文本 / 引用文本逐字相等。
//
// # 两侧解析口径
//
//	来源侧：在完整 Source 正文快照上解析一次，得到有序资产事件（各带物理行区间）。
//	        每个资产必须完整落在**恰一个** source_ref 或**恰一个** omission 内；跨边界、
//	        切进多行资产、或只靠未覆盖空白行把同一资产拆开 → E2 钉到造成切分的区间 path。
//	        完整落在 omission 的资产从期望序列移除；部分相交 fail closed。
//	目标侧：把所有 role: source 块正文按 blocks 顺序拼成一个**只读解析视图**（保留每块的
//	        行归属），一次解析，使 reference 定义可跨块解析；agent 块正文不参与来源资产比较。
//
// 每个 source 块只与其 source_ref 对应的期望事件序列比较：事件种类 / 稳定签名 / 顺序逐项相等，
// 少、增、重复、同类可标识事件乱序、跨类乱序均 E2，钉 ops[i].blocks[j].body、Target=source id，
// message 指出首个不一致的 expected/actual。原始 HTML 资产形态 / 无法可靠解析的形态 → E2 fail
// closed。任一失败该 write_note 零 action。

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// noteFidelity 是资产保真闸门。返回 false 表示已登记至少一条 E2、整条 write_note 零写入。
func (v *validator) noteFidelity(op *Op, cov noteCoverage) bool {
	// 来源侧：完整 Source 正文解析一次。无法可靠解析的资产形态（原始 HTML 资产等）fail closed。
	srcEvents, err := store.ScanAssets(cov.snap.body)
	if err != nil {
		d := errorAt(E2, op.Index, opPath(op.Index, "source"),
			"source %s 正文的结构资产无法可靠解析：%v；按 fail closed 拒绝落盘"+
				"（契约 §4.2.1，绝不静默当「无资产」）", op.Source, err)
		d.Target = op.Source
		v.add(d)
		return false
	}

	// 把每个来源资产钉进恰一个 source_ref / omission 区间，得到每段来源范围的期望事件序列。
	expected, ok := v.assignSourceAssets(op, srcEvents, cov)
	if !ok {
		return false
	}

	// 目标侧：拼接所有 role: source 块正文成只读解析视图，一次解析并按行归属回每个块。
	actual, ok := v.scanTargetBlocks(op)
	if !ok {
		return false
	}

	// 逐段来源范围与其对应 source 块比对（srcSpans 与 expected 同序）。
	for k, sp := range cov.srcSpans {
		if diag, equal := compareAssetEvents(expected[k], actual[sp.blockIndex]); !equal {
			d := errorAt(E2, op.Index, blockPath(op.Index, sp.blockIndex, "body"),
				"结构资产保真失败：%s。source %s 的来源范围 L%d-L%d 与本块的图片 / 图注 / 代码 / 表格行 / "+
					"列表项 / 引用 / 链接 / 脚注的数量或顺序不一致（契约 §4.2.1；格式恢复或翻译不免除保真）",
				diag, op.Source, sp.start, sp.end)
			d.Target = op.Source
			v.add(d)
			return false
		}
	}
	return true
}

// assignSourceAssets 把来源资产逐个钉进恰一个 source_ref / omission 区间，产出每段 source_ref
// 对应的期望事件序列（与 cov.srcSpans 同序）。完整落在 omission 的资产被移除；任一资产跨边界 /
// 被空白切断（起点所在区间容不下其终点）→ E2 钉到造成切分的区间 path，返回 false。
func (v *validator) assignSourceAssets(op *Op, events []store.AssetEvent, cov noteCoverage) ([][]store.AssetEvent, bool) {
	// 合并两组区间并按起点排序（T12-2A 已保证组内与跨组均严格递增、不重叠）。
	type tagged struct {
		sp       lineSpan
		omission bool
		srcSlot  int // 在 cov.srcSpans 中的下标（omission 为 -1）
	}
	merged := make([]tagged, 0, len(cov.srcSpans)+len(cov.omSpans))
	for i, sp := range cov.srcSpans {
		merged = append(merged, tagged{sp: sp, srcSlot: i})
	}
	for _, sp := range cov.omSpans {
		merged = append(merged, tagged{sp: sp, omission: true, srcSlot: -1})
	}
	sort.Slice(merged, func(a, b int) bool { return merged[a].sp.start < merged[b].sp.start })

	expected := make([][]store.AssetEvent, len(cov.srcSpans))
	for _, ev := range events {
		// 容纳 / 切断按事件的**完整归属范围**（scope）判定：普通事件 scope 即自身 span；表格行的
		// scope 是整张 Table 容器，故整表被两个 source_ref / omission 拆开也能检出（而事件仍按真实
		// 行排序、不影响乱序检测）。找 scope 起点所在区间。
		idx := -1
		for i := range merged {
			if ev.ScopeStartLine >= merged[i].sp.start && ev.ScopeStartLine <= merged[i].sp.end {
				idx = i
				break
			}
		}
		if idx < 0 {
			// 资产起点落在未被任何区间覆盖的行（理论上覆盖校验已排除非空行空洞）：fail closed。
			d := errorAt(E2, op.Index, opPath(op.Index, "blocks"),
				"结构资产（%s，L%d-L%d）的起点未落在任何 source_ref / omission 区间内：fail closed"+
					"（契约 §4.2.1）", ev.Label(), ev.ScopeStartLine, ev.ScopeEndLine)
			d.Target = op.Source
			v.add(d)
			return nil, false
		}
		if ev.ScopeEndLine > merged[idx].sp.end {
			// 资产（归属范围）跨出起点所在区间——跨边界 / 切进多行资产 / 切开整表 / 被未覆盖空白拆开。
			d := errorAt(E2, op.Index, merged[idx].sp.path,
				"结构资产（%s，L%d-L%d）被该区间（L%d-L%d）切断：多行资产必须完整落在恰一个 "+
					"source_ref 或恰一个 omission 内，不得跨边界、切进代码块 / 表格 / 列表项 / 引用 / "+
					"脚注定义，或只靠未覆盖空白行拆开（契约 §4.2.1 fail closed）",
				ev.Label(), ev.ScopeStartLine, ev.ScopeEndLine, merged[idx].sp.start, merged[idx].sp.end)
			d.Target = op.Source
			v.add(d)
			return nil, false
		}
		if merged[idx].omission {
			continue // 完整落在 omission：从期望序列移除
		}
		slot := merged[idx].srcSlot
		expected[slot] = append(expected[slot], ev)
	}
	return expected, true
}

// scanTargetBlocks 把所有 role: source 块正文按 blocks 顺序拼成只读解析视图并一次解析，
// 再按物理行把资产事件归属回每个 source 块（op.Blocks 下标 → 事件序列）。
//
// 拼接用空行分隔，保证块边界不被 CommonMark 合并；跨块的 reference 定义在同一视图内可解析。
// agent 块正文不进入视图（不参与来源资产比较）。视图解析出结构扫描错误时按行定位到肇事块，
// E2 fail closed 钉到该块 body。
func (v *validator) scanTargetBlocks(op *Op) (map[int][]store.AssetEvent, bool) {
	var view []byte
	// blockAt[行号] = op.Blocks 下标；ranges 记录每块在视图里的行区间用于命中判定。
	type lineRange struct {
		blockIndex int
		first      int
		last       int
	}
	var ranges []lineRange
	curLine := 1
	for j, b := range op.Blocks {
		if b.Role != NoteBlockSource {
			continue
		}
		body := bytes.Trim(b.Body, "\n")
		if len(view) > 0 {
			view = append(view, '\n', '\n')
			curLine += 2
		}
		first := curLine
		view = append(view, body...)
		curLine += bytes.Count(body, []byte("\n"))
		ranges = append(ranges, lineRange{blockIndex: j, first: first, last: curLine})
	}

	events, err := store.ScanAssets(view)
	if err != nil {
		// 结构扫描错误：按错误行定位肇事块（定位不到则钉整块 blocks）。
		blockIdx := -1
		if se, ok := err.(*store.AssetScanError); ok && se.Line > 0 {
			for _, r := range ranges {
				if se.Line >= r.first && se.Line <= r.last {
					blockIdx = r.blockIndex
					break
				}
			}
		}
		path := opPath(op.Index, "blocks")
		if blockIdx >= 0 {
			path = blockPath(op.Index, blockIdx, "body")
		}
		d := errorAt(E2, op.Index, path,
			"整理正文的结构资产无法可靠解析：%v；按 fail closed 拒绝落盘（契约 §4.2.1）", err)
		d.Target = op.Source
		v.add(d)
		return nil, false
	}

	out := map[int][]store.AssetEvent{}
	for _, ev := range events {
		// 每个 target 资产必须**完整落在恰一个** source 块内：按事件的**完整归属范围**（scope）判定
		// ——scope 起点须落在某块行区间，scope 终点不得越出该块。普通事件 scope 即自身 span；表格行
		// scope 是整张 Table，故被两个 source 块拼接复原的整表也会被检出。只按起点归属会让同一个跨两
		// 块的资产（如 fenced code 被两个 source_ref 各取一半后靠拼接视图复原同一 payload）蒙混过关。
		hit := -1
		for _, r := range ranges {
			if ev.ScopeStartLine >= r.first && ev.ScopeStartLine <= r.last {
				hit = r.blockIndex
				if ev.ScopeEndLine > r.last {
					// 归属范围终点越出起点所在块：靠相邻块拼接才能复原该资产 → fail closed，
					// 钉造成跨块的**起点所在块** body。
					d := errorAt(E2, op.Index, blockPath(op.Index, r.blockIndex, "body"),
						"整理正文的结构资产（%s，视图 L%d-L%d）跨出其所在 source 块（视图 L%d-L%d）："+
							"每个来源资产必须完整落在恰一个 source 块内，不得靠相邻块拼接复原"+
							"（契约 §4.2.1 fail closed）",
						ev.Label(), ev.ScopeStartLine, ev.ScopeEndLine, r.first, r.last)
					d.Target = op.Source
					v.add(d)
					return nil, false
				}
				break
			}
		}
		if hit < 0 {
			// 起点落在块间分隔区（未覆盖），不属于任何 source 块 → fail closed。
			d := errorAt(E2, op.Index, opPath(op.Index, "blocks"),
				"整理正文的结构资产（%s，视图 L%d-L%d）起点落在 source 块之间的分隔区、"+
					"不属于任何 source 块：fail closed（契约 §4.2.1）",
				ev.Label(), ev.ScopeStartLine, ev.ScopeEndLine)
			d.Target = op.Source
			v.add(d)
			return nil, false
		}
		out[hit] = append(out[hit], ev)
	}
	return out, true
}

// compareAssetEvents 逐项比较期望与实际事件序列：种类 + 稳定签名 + 顺序全等则相等。
// 不等时返回首个不一致的人读诊断（指出 expected / actual 的 kind + 稳定标识），equal=false。
func compareAssetEvents(expected, actual []store.AssetEvent) (string, bool) {
	n := len(expected)
	if len(actual) > n {
		n = len(actual)
	}
	for i := 0; i < n; i++ {
		var exp, act *store.AssetEvent
		if i < len(expected) {
			exp = &expected[i]
		}
		if i < len(actual) {
			act = &actual[i]
		}
		switch {
		case exp == nil:
			return fmt.Sprintf("首个不一致位于第 %d 项：期望无更多资产，实际多出 %s", i+1, act.Label()), false
		case act == nil:
			return fmt.Sprintf("首个不一致位于第 %d 项：期望 %s，实际缺失", i+1, exp.Label()), false
		case exp.Kind != act.Kind || exp.Sig != act.Sig:
			return fmt.Sprintf("首个不一致位于第 %d 项：期望 %s，实际 %s", i+1, exp.Label(), act.Label()), false
		}
	}
	return "", true
}
