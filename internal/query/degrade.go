package query

// [S4] 读路径的**降级取数**（M5 索引架构合同 §5.2 / §6.1 / §6.3；T-…-067）。
//
// 一句话合同：**索引可以坏、可以旧、可以根本没有，读命令都必须照常出结果。**
// 三形态一律回落全量 Markdown 扫描，退出码一格不动（M5 不启用退出码 5）：
//
//	index_missing（W23）→ 全量扫描 + 恰一条 W23 + 恰一条 Q5
//	index_corrupt（W24）→ 全量扫描 + 恰一条 W24 + 恰一条 Q5
//	index_stale  （W22）→ 全量扫描 + 恰一条 W22 + 恰一条 Q5
//
// 第四种情形也收在这里：索引**探测时健康**、但按需解析中途发现它已经落后于权威
// （errIndexBehindAuthority，或任何索引读取失败）—— 同样整体退回扫描并按 W22 留痕。
// 半索引半扫描的混合态一律不做：说不清、也没法复算。
//
// 降级的结果**不是打了折的结果**：扫描后端就是 M2 冻结的权威取数路径，
// 因此 `.data` 与索引在位时逐字相等（e2e 的 diff 空差异断言即此事的机器形态）。
// Q5 存在的意义只有一个：让「这次读走的是降级路径」这件事**说得出口**，
// 不至于让用户与 agent 把降级结果误当成索引路径的结果（合同 §6.1 末段）。
//
// 明确不做：不因索引问题改任何退出码、不在降级时静默丢结果、不把 W22/W23/W24
// 的字面量写进查询层（码一律引用 internal/index 的常量）。

import (
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/index"
)

// loadVault 按已选后端取数，**并在索引路不通时如实降级**：全库唯一的降级落点。
//
// 返回值第二项是**修正后**的后端结论：走索引失败时它已被改写成「扫描 + W22」，
// 调用方据此产出诊断即可，不需要（也不允许）各自再判一次「这次算不算降级」。
//
// 参数 need 只用于派生解析计划（planFor），不在这里重做后端选择 ——
// 选择只发生在 SelectBackend 一处。
func loadVault(root string, opt ScanOptions, need Need, b Backend) (*ScanResult, Backend, error) {
	if b.UseIndex() {
		res, err := indexVault(root, b.probe, planFor(need, opt))
		if err == nil {
			return res, b, nil
		}
		// 索引这条路走不通：退回扫描后端并按**陈旧**留痕（索引与权威已经不一致，
		// 处置方式与 stale 相同 —— `eg index sync`）。绝不把索引的问题上抛成命令失败。
		b = b.degradedToScan(fmt.Sprintf("索引在取数途中被判为落后于权威：%v", err))
	}
	res, err := VaultScan(root, opt)
	if err != nil {
		// 这是**权威 Markdown** 读不动（磁盘 / 权限 / 目录形态问题），与索引无关：
		// 照 M2 既有语义原样上抛，索引降级不得吞掉权威侧的真错误。
		return nil, b, err
	}
	return res, b, nil
}

// degradedToScan 把一个「本打算走索引」的结论改写成「扫描 + 陈旧（W22）」。
//
// 只在 loadVault 里调用：它是「探测健康但取数途中发现不一致」这一种情形的唯一出口，
// 因此 Reason / Code / Message 的口径与 SelectBackend 的 stale 分支同源。
func (b Backend) degradedToScan(detail string) Backend {
	b.Kind, b.Reason, b.Code = BackendScan, ReasonIndexStale, index.CodeIndexStale
	b.Freshness = index.FreshnessStale
	b.Message = fmt.Sprintf("索引已陈旧：本次 %s 已降级为全量 Markdown 扫描，"+
		"结果以权威 Markdown 为准；%s；请跑 eg index sync", b.Path, detail)
	return b
}

// degradeDiagnostics 产出本次降级的**恰两条**诊断：一条原因码（W22|W23|W24）+ 一条 Q5。
//
// 未降级（含「索引健康但查询不可由索引表达」）时返回 nil —— 健康索引下**不产** Q5
// 是合同 §6.3 的逐字要求，也是 TestQ5NotEmittedOnHealthyIndex 的判据。
//
// 成对出现是刻意的：只有 Q5 说不清「为什么降级、怎么修」，只有 W2x 说不清
// 「本次结果是降级路径给的」。两条同源同事实（都取自同一个 Backend），不引入第三套说法。
func degradeDiagnostics(b Backend) []Diagnostic {
	if !b.Degraded() {
		return nil
	}
	return []Diagnostic{
		{Code: b.Code, Level: DiagLevel, Path: diagSummaryPath, Message: b.Message},
		newQ5(b.Path, b.Reason),
	}
}
