package query

// [S4] 读路径的**分页与截断**（M5 索引架构合同 §8.2 A-47 + `M-005` 判据 12；T-…-068）。
//
// # 一句话合同
//
// **分页只决定「这一页给你看几条」，绝不决定「库里有几条」。**
//
// 且「几条」是**一个全局上限**：`--limit N` 一次读最多返回 N 条条目，与这些条目分散在
// 几个 data 列表里无关（`card show` / `rel` 有正反两个关系列表，合计仍受同一个 N 约束 ——
// 见 ApplyPagePair）。合同 §8.2 的字面是「最多返回的条数」，不是「每个列表的条数」。
// `total` 恒为分页**之前**的总条数，截断这件事经诊断区的 `W25` 说出口，
// `data` 键集合一格不扩张（合同 §8.3 / `M-005` 判据 10）。
//
// # 参数面（封闭，恰两个）
//
//	--limit   非负整数，默认 50，**0 == 不限量**（且永不产 W25）
//	--offset  非负整数，默认 0；**超出总数返回空结果并退 0**（不是错误）
//
// 负数或非整数 = 参数错误（合同 §8.1 / §8.2 的退出码 `4`），由命令层翻译，本包只给
// 带类型的 ErrInvalidPage。
//
// # 边界语义（逐条对齐合同 §8.2 的四行表）
//
//	limit 0                    → 不限量、恒无 W25
//	offset ≥ total             → 空结果 + 退 0（**不报错**）
//	limit / offset 为负        → ErrInvalidPage（命令层 → 退 4）
//	total > offset + limit     → 恰一条 W25 result_truncated
//
// 翻页无重无漏是**构造性**的：分页施加在排序**之后**（rank.go 的四级全序 / §1.4 的
// 四级全序），切片按 [offset, offset+limit) 半开区间取，相邻页并集因此恰是连续区间。
//
// # 明确不做
//
//   - 不新增 `data` 顶层键（页码 / 是否还有下一页一律不进 data）；
//   - 不改后端选择与降级语义（T-…-067 的 backend.go / degrade.go 一格不碰）；
//   - 不引入第三个分页参数（`--page` / `--page-size` 已被 A-47 排除）；
//   - 零值 PageSpec == 不限量：M1–M4 的库内调用方（`Search` / `ShowCard` / `RelView`）
//     因此行为一字不变，默认 50 只在**命令层**生效。

import (
	"errors"
	"fmt"
)

// CodeResultTruncated 是「结果因 --limit 被截断」的诊断码（合同 §6.3 的 `W25`）。
//
// 归位说明：`W22` / `W23` / `W24` 是**索引健康**的事实，字面量归 internal/index；
// `W25` 是**读路径分页**的事实，与索引在不在位无关（合同 §6.4：两者正交），
// 因此它的唯一字面量落在本文件 —— 判据 12 的 `grep -c '"W25"' internal/query/page.go` 即此。
const CodeResultTruncated = "W25"

// ErrInvalidPage 是分页参数非法族（负数 / 非整数）。命令层据此给退出码 `4`（合同 §8.2）。
var ErrInvalidPage = errors.New("分页参数非法")

// PageLimitFlag / PageOffsetFlag 是两个分页 flag 的名字（**唯一字面量**，命令层引用它）。
const (
	PageLimitFlag  = "limit"
	PageOffsetFlag = "offset"
)

// PageDefaultLimit 是命令层的默认页大小（合同 §8.2：`--limit` 默认 50）。
//
// 注意它**不是**库层默认值：PageSpec 的零值表示不限量，见文件头最后一条。
const PageDefaultLimit = 50

// PageParams 返回分页参数的封闭集合（恰两项，次序固定）。
// `TestPageParamsClosedSet` 拿它与命令层实际注册的 flag 逐格比对。
func PageParams() []string { return []string{PageLimitFlag, PageOffsetFlag} }

// PageSpec 是一次读的分页口径。
//
// Limit == 0 表示**不限量**（合同 §8.2）；Offset 是跳过的条数。
// 零值 = 全量、不跳过、恒无 W25，因此可以安全地作为库层默认。
type PageSpec struct {
	Limit  int
	Offset int
}

// Unlimited 报告本次是否不限量（`--limit 0`）。
func (p PageSpec) Unlimited() bool { return p.Limit == 0 }

// Validate 做参数形态校验：负数即非法（非整数在命令层解析时即被拒，同一族错误）。
func (p PageSpec) Validate() error {
	if p.Limit < 0 {
		return fmt.Errorf("%w：--%s=%d 不是非负整数（0 表示不限量）",
			ErrInvalidPage, PageLimitFlag, p.Limit)
	}
	if p.Offset < 0 {
		return fmt.Errorf("%w：--%s=%d 不是非负整数",
			ErrInvalidPage, PageOffsetFlag, p.Offset)
	}
	return nil
}

// Page 是一次分页的结果事实（不进 data，只供渲染层与诊断层判定）。
type Page struct {
	// Total 是分页**之前**的总条数（`data.total` 取它，不取本页条数）。
	Total int
	// Returned 是本页实际返回的条数。
	Returned int
	// Truncated 报告是否发生截断（`total > offset + limit` 且 limit > 0）。
	Truncated bool
	// Spec 是本次生效的分页口径（回显，便于诊断文案与用例逐格比对）。
	Spec PageSpec
}

// ApplyPage 按 [offset, offset+limit) 半开区间取一页，并给出分页事实。
//
// 三条硬语义：① `limit 0` 返回全部；② `offset` 超界返回**空切片**（不是 nil、不是错误）；
// ③ 返回的切片是**新切片**（不共享底层数组的写口），调用方随后追加渲染不会污染入参。
func ApplyPage[T any](items []T, p PageSpec) ([]T, Page) {
	total := len(items)
	pg := Page{Total: total, Spec: p}
	lo := p.Offset
	if lo > total {
		lo = total
	}
	hi := total
	if !p.Unlimited() && lo+p.Limit < total {
		hi = lo + p.Limit
	}
	out := make([]T, 0, hi-lo)
	out = append(out, items[lo:hi]...)
	pg.Returned = len(out)
	// 截断的判定逐字取合同 §8.2：`total > offset + limit`（limit 0 恒不截断）。
	pg.Truncated = !p.Unlimited() && total > p.Offset+p.Limit
	return out, pg
}

// PagePairSequence 把「正向列表 + 反向列表」拼成**一条确定序列**：out 段恒在前、in 段恒在后。
//
// 为什么需要它：`card show` / `rel` 的 data 里有两个关系列表，而合同 §8.2 的 `--limit`
// 说的是「**最多返回的条数**」——**一个**全局上限，不是「每个列表各自的上限」。
// 若两个列表各自 ApplyPage，`--limit 50` 会返回最多 100 条，`total > offset+limit` 的截断
// 判定也会各算一套：那是**两个分页器**，与合同逐字不符。
//
// 段间次序取「正向在前」并且是**固定**的：正向 = 本卡自己写下的记录、反向 = 别人指向本卡，
// 语义上前者更贴近「这张卡说了什么」，且两个后端（索引 / 扫描）都按同一顺序组装；
// 段内次序由 rank.go 的四级全序给出。因此拼接结果与输入顺序、与后端选择无关。
func PagePairSequence(out, in []RelationEdge) []RelationEdge {
	merged := make([]RelationEdge, 0, len(out)+len(in))
	merged = append(merged, out...)
	return append(merged, in...)
}

// ApplyPagePair 对「合并后的确定序列」施加**一个**全局 limit/offset，再切回正 / 反两段。
//
// 与合同的等价性是**构造性**的，可机器证明（`TestPagePairEqualsGlobalApplyPage`）：
// 本函数的返回值满足 `PagePairSequence(outPage, inPage) == ApplyPage(PagePairSequence(out,in), p)`
// 的第一个返回值，且 Page 事实逐格等于后者的第二个返回值。也就是说，它就是
// 「先合并成一条序列、再全局分页」的同一件事，只是把结果按段还原回两个 data 键。
//
// 由此三条合同语义自动成立：
//
//	Returned ≤ limit（limit > 0 时）—— 全局上限，**不会**出现 2*limit；
//	Total = 正向条数 + 反向条数 —— 分页前的合计，不受 limit/offset 影响；
//	Truncated ⇔ Total > offset+limit —— 只判一次，故只产恰一条 W25。
//
// 切分点的算法：窗口是合并序列上的半开区间 [lo, hi)，out 段占据 [0, len(out))，
// 因此本页里属于 out 的条数就是两个区间交集的长度；剩下的一律属于 in 段。
func ApplyPagePair(out, in []RelationEdge, p PageSpec) ([]RelationEdge, []RelationEdge, Page) {
	merged, pg := ApplyPage(PagePairSequence(out, in), p)
	lo := p.Offset
	if lo > pg.Total {
		lo = pg.Total
	}
	hi := lo + len(merged) // ApplyPage 取的就是 [lo, lo+Returned)
	cut := 0
	switch {
	case hi <= len(out): // 整页都落在正向段内
		cut = len(merged)
	case lo >= len(out): // 整页都落在反向段内
		cut = 0
	default: // 跨段：正向段只剩 len(out)-lo 条
		cut = len(out) - lo
	}
	outPage := append([]RelationEdge{}, merged[:cut]...)
	inPage := append([]RelationEdge{}, merged[cut:]...)
	return outPage, inPage, pg
}

// newW25 记一条「结果被截断」的诊断（path 逐字「(汇总)」：截断不属于某一个文件）。
//
// 文案说三件事：库里一共多少条、这一页给了多少条、怎么拿到其余的 ——
// 「少给了结果」这件事必须说得出口，否则截断结果会被当成完整结果（M-002 R-1 的同型风险）。
func newW25(pg Page, unit string) Diagnostic {
	return Diagnostic{Code: CodeResultTruncated, Level: DiagLevel, Path: diagSummaryPath,
		Message: fmt.Sprintf("结果已截断：%s共 %d 条，本次按 --%s=%d --%s=%d 返回 %d 条；"+
			"如需其余条目请调大 --%s / 前移 --%s，或用 --%s 0 取全量（total 恒为截断前的总数）",
			unit, pg.Total, PageLimitFlag, pg.Spec.Limit, PageOffsetFlag, pg.Spec.Offset,
			pg.Returned, PageOffsetFlag, PageLimitFlag, PageLimitFlag)}
}

// withTruncationDiagnostic 在诊断集合里追加**至多一条** W25。
//
// 插入位置与 Q4 / 降级两条同一条纪律：**Q3 恒末位**，故 W25 插到末尾 Q3 之前；
// 无 Q3 时追加到末尾。整体次序因此确定为 Q1 → Q2 → Q4 → W2x → Q5 → W25 → Q3。
//
// 「至多一条」是逐字判据，而且在 ApplyPagePair 之后它是**构造性**的：正反两个列表
// 共用一次全局分页、只有一个 Page 事实、因此截断只判一次，不存在「两边各产一条」的形态
// （合同 §8.2 的「产出恰一条 W25」），文案里给的就是合计口径。
func withTruncationDiagnostic(diags []Diagnostic, truncated bool, pg Page, unit string) []Diagnostic {
	if !truncated {
		return diags
	}
	w25 := newW25(pg, unit)
	if n := len(diags); n > 0 && diags[n-1].Code == CodeQ3 {
		out := append([]Diagnostic{}, diags[:n-1]...)
		out = append(out, w25)
		return append(out, diags[n-1])
	}
	return append(append([]Diagnostic{}, diags...), w25)
}
