package plan

// internal/plan/strict_exempt.go —— `--strict` **豁免面**代码集的单一真源
// （Schema v2 · T-evergreen.knowledge_opinion_split-158614-003；契约 D-6 / D-9）。
//
// # 为什么落在 plan 而不是 model
//
// 升级面（`W1`/`W2`/`W3`/`W4`/`W6`，恰五条）落在 `internal/model`，理由写在
// internal/model/strict.go 的文件头：那五条同时被 plan 的写前校验与 reconcile 的
// severity 策略消费，而两层互不 import，只有零依赖的 model 能让两侧引用同一份。
//
// 豁免面的消费面完全不同：`W21` 是 **ChangePlan 域**的诊断码（write_note 结构覆盖，
// 契约 §4.3），只由本包发放，reconcile 侧永远不会看到它，因此没有「两个互不依赖的层
// 都要读同一张表」的约束。而 M3 的诊断编号分域纪律（m3_test.go 的
// TestDiagnosticCodes_Closed）要求 `W21` 这个**字面量**只在其 owner 包
// `internal/plan` 出现：把豁免表放进 model 就会在 model 里留下第二处 `W21` 字面量，
// 令「一码一落点」退化。本包 import model，因此仍能对升级面做交集复算 ——
// 单一真源与分域纪律两者都不必让步。
//
// # 为什么白名单之外还要显式登记豁免
//
// 升级面 model.StrictUpgradeCodes() 已经是封闭白名单，任何新码**天然**不会被升级 ——
// 从「当前行为」看，本文件是冗余的。它存在的理由是**约束未来的改动**：
// D-6 对 `W21` 的结论不是「今天恰好没被升级」，而是「永远不得升级，除非先给出一个
// 不可被凑数规避的判据」。把这条结论写成一份可机器复算的清单 + 一条交集必须为空的
// 不变量，才能让「有人日后顺手把 W21 加进升级面」在测试里当场变红，
// 而不是只在一份文档的段落里留下一句无人复核的叮嘱。
//
// 豁免面**不是**「所有非升级码」的同义词：它只登记那些**被明确论证过、不得升级**的码。
// 一个诊断码不在这两份清单里，含义是「尚未表态」，而不是「已决定豁免」。

import "github.com/ikaqiu-Lemon/EverGreen/internal/model"

// StrictExemptCodeCount 是显式豁免码数：**恰 1**（`W21`）。
//
// 写成常量并用它约束数组长度，是为了让「豁免面恰一条」在**编译期**成立：
// 多写一行得到 index out of range，少写一行留下零值空串并被用例当场判红。
const StrictExemptCodeCount = 1

// strictExemptCodes 是 `--strict` 显式豁免面的封闭全集（长度固定数组）。
//
//   - `W21` `write_note` 结构覆盖诊断（契约 §4.3）：「Note 是否覆盖了原文结构」
//     没有机械正确答案，阈值 `ceil(src_anchors/2)` 只是启发式；升级为 error 会让
//     生成侧的最优策略变成「按标题数凑够 role: source 块」，把语义门禁劣化为计数游戏，
//     与它要解决的「Note 退化为摘要」同源。真正的把关点是用户 `mark-reviewed`。
var strictExemptCodes = [StrictExemptCodeCount]string{W21}

// StrictExemptCodes 返回显式豁免面的封闭集合副本。
func StrictExemptCodes() []string {
	out := make([]string, 0, StrictExemptCodeCount)
	out = append(out, strictExemptCodes[:]...)
	return out
}

// IsStrictExemptCode 报告某诊断码是否被**显式**登记为 `--strict` 豁免。
func IsStrictExemptCode(code string) bool {
	for _, c := range strictExemptCodes {
		if c == code {
			return true
		}
	}
	return false
}

// StrictSetsDisjoint 报告升级面（model 的恰五条）与豁免面是否无交集。
//
// 这是本文件的核心不变量：同一个码不能既「在 --strict 下升为 error」又「豁免于 --strict」。
// 恒为 true 才是正常状态；返回 false 说明有人把一个已论证豁免的码加进了升级面，
// 或反之 —— 两种情形都必须在测试里立刻可见，而不是在某次 `--strict` 运行时才发作。
func StrictSetsDisjoint() bool {
	for _, c := range strictExemptCodes {
		if model.IsStrictUpgradeCode(c) {
			return false
		}
	}
	return true
}
