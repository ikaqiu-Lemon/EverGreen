package cli

// [S4] 三条读命令的**分页参数面**（M5 索引架构合同 §8.2 A-47；T-…-068；
// 退出码口径由 I-…-008 修正为只读闭集 {0,1}）。
//
// 一句话合同：**参数恰两个、语义封闭、错了就明说**。
//
//	--limit   非负整数，默认 50，**0 == 不限量**（且永不产 W25）
//	--offset  非负整数，默认 0；**超出总数返回空结果并退 0**（不是错误）
//
// 作用面恰 `eg search` / `eg card show` / `eg rel` 三条读命令（合同 §8.2 末段）。
// 其余命令**不声明**这两个 flag，传入即由参数解析当场判非法 → 退 1、零写入
// （`eg index status --limit 5` 退 1 的既有反证因此继续成立，一格不放宽）。
//
// # 为什么两个 flag 都注册成字符串
//
// 若用 `fs.Int`，标准库 flag 会在**解析阶段**把 `--limit abc` 判成用法错并直接打印它自己的
// usage 文案；注册成字符串后解析永远成功，两类非法值（非整数 / 负数）由本文件一处翻译，
// 因此走**同一个**出口、给**同一句**逐字理由，也才能挂上 `--json` 信封的 error 级诊断。
//
// # 退出码归属（I-…-008 裁决：只读命令闭集恰 {0,1}）
//
// 分页参数非法退 **1**（`status = "failed"`），**不是** 4。判据是三处独立声明面的多数一致：
//   - M2 查询合同 §1.6：「`1` 参数非法 …。只读命令**不出现** `2` / `3` / `4`」；
//   - 同合同 §6（只读零副作用判定）：「**退出码只可能是 0 / 1**」；
//   - `eg search` / `eg card` / `eg rel` 三条 `--help` 各自逐字声明的闭集 {0,1}。
//
// M5 索引架构合同 §8.2 与 §10 A-47 原写「负数 / 非整退 `4`」，与同 milestone 的
// `2027-01-17-m5-release-and-version.md`「既有命令的退出码未做破坏性变更」自相矛盾；
// I-…-008 取多数声明面，**同步修正了 M5 合同的这两处**（不是把差异登记了事）。
//
// `4` 因此仍**只**表示「Git 提交失败（磁盘保留现状，B4）」——一次纯参数打错的只读查询
// 绝不能让集成方误触发仓库修复 / 回滚 / 告警。`eg bench` 的采样执行失败仍按 M5 §8.1
// 保留 4（它是写侧命令，不在只读闭集内），本次修复一格不动它。

import (
	"fmt"
	"strconv"

	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
)

// ExitPageParam 是分页参数错误的退出码（I-…-008：`1`，与其余「参数非法」同族）。
//
// 刻意写成对 `ExitUsage` 的**引用**而不是新字面量：`--limit -1` 与 `--since abc` 是同一族
// 「参数值非法」，必须同码，否则调用方写不出稳定分支（原实现分别给 4 / 1，正是本 issue）。
const ExitPageParam = ExitUsage

// PageParamError 是分页参数非法（负数 / 非整数）的带类型错误 → 退 `1`、零写入。
//
// 保留独立类型（而非直接复用 *UsageError）的理由：它承载「哪个 flag、什么值」这组事实，
// 供三条读命令共用一句逐字理由；退出码翻译在 exit.go 一处完成，别处不得自行拼装。
type PageParamError struct {
	Flag  string
	Value string
	Msg   string
}

func (e *PageParamError) Error() string {
	if e.Msg != "" {
		return e.Msg
	}
	return fmt.Sprintf("--%s=%q 不是非负整数（%s 表示不限量、%s 表示不跳过）",
		e.Flag, e.Value, "--"+query.PageLimitFlag+" 0", "--"+query.PageOffsetFlag+" 0")
}

// pageFlags 注册两个分页 flag（**唯一注册点**，三条读命令共用它，不各写一份默认值）。
func pageFlags(fs *flagSet) {
	fs.String(query.PageLimitFlag, strconv.Itoa(query.PageDefaultLimit),
		"最多返回条数（0 = 不限量；截断时产一条 W25）")
	fs.String(query.PageOffsetFlag, "0", "跳过的条数（超出总数返回空结果，仍退 0）")
}

// pageSpecFrom 把两个 flag 解析成 query.PageSpec；任何非法值一律 *PageParamError（退 1，
// 见本文件头「退出码归属」：只读闭集恰 {0,1}，I-…-008 改判）。
func pageSpecFrom(inv *Invocation) (query.PageSpec, error) {
	limit, err := pageInt(inv, query.PageLimitFlag)
	if err != nil {
		return query.PageSpec{}, err
	}
	offset, err := pageInt(inv, query.PageOffsetFlag)
	if err != nil {
		return query.PageSpec{}, err
	}
	spec := query.PageSpec{Limit: limit, Offset: offset}
	if err := spec.Validate(); err != nil {
		// 库层已给出逐字理由（含哪个参数、什么值）：原文照搬，不再编第二套说法。
		return query.PageSpec{}, &PageParamError{Msg: err.Error()}
	}
	return spec, nil
}

// pageInt 解析单个分页 flag：非整数 / 负数都归同一族错误（合同 §8.2 同一行）。
func pageInt(inv *Invocation, name string) (int, error) {
	raw := inv.String(name)
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, &PageParamError{Flag: name, Value: raw}
	}
	if n < 0 {
		return 0, &PageParamError{Flag: name, Value: raw}
	}
	return n, nil
}

// pageUsageLines 是三条读命令 `--help` 里逐字共用的分页参数说明（同源同事实）。
const pageUsageLines = `  --` + query.PageLimitFlag + ` <n>            否，默认 ` +
	"50" + `；最多返回条数，**0 = 不限量**；发生截断时产恰一条 W25（total 仍是截断前总数）
  --` + query.PageOffsetFlag + ` <n>           否，默认 0；跳过的条数；超出总数返回空结果且仍退 0
                        两者为负数 / 非整数 → 参数非法，退 1、零写入（M2 合同 §1.6 / §6）`

// pageSummaryLines 渲染文本模式的分页事实（与 --json 的诊断区 W25 **同源同事实**）。
//
// 只在显式给了 --limit / --offset（非默认零值口径）时出现一行，因此 M1–M4 既有的
// 文本模式逐字留痕（不传分页参数的调用）一字不变。
//
// 措辞刻意说清「本页 N 条 / 共 M 条」：截断结果绝不能被读成完整结果（M-002 R-1 同型风险）。
func pageSummaryLines(pg query.Page, unit string) []string {
	if pg.Spec.Limit == 0 && pg.Spec.Offset == 0 {
		return nil
	}
	return []string{fmt.Sprintf(
		"分页：--%s=%d --%s=%d，本页 %s %d 条 / 共 %d 条（截断=%t；limit 是本次返回条数的全局上限）",
		query.PageLimitFlag, pg.Spec.Limit, query.PageOffsetFlag, pg.Spec.Offset,
		unit, pg.Returned, pg.Total, pg.Truncated)}
}

// readPathOnlyFlags 是「只在读路径成立」的 S4 flag 集合（次序固定，供用例逐格比对）。
//
// 合同 §8.2 / §8.4 把分页与替代指针视图的作用面限定在**读**命令：`eg rel` 的两个写子命令
// 与它们同处一个 Command、共享 FlagSet，因此必须在 Validate 阶段显式拒绝，
// 否则 `eg rel add ... --limit 1` 会被静默忽略 —— 「参数写了却不生效」比报错更坏。
func readPathOnlyFlags() []string {
	return []string{query.PageLimitFlag, query.PageOffsetFlag, query.ReplacedByFlag}
}

// rejectReadPathFlags 在写子命令上显式拒绝只读 flag：退 1、零写入。
// 与「参数值非法」（*PageParamError，同样退 1）成因不同、文案不同，但同属用法族。
func rejectReadPathFlags(inv *Invocation, display string) error {
	for _, name := range readPathOnlyFlags() {
		if inv.Set(name) {
			return &UsageError{Msg: fmt.Sprintf(
				"%s 不接受 --%s：该参数只作用于读路径（M5 合同 §8.2 / §8.4）", display, name)}
		}
	}
	return nil
}
