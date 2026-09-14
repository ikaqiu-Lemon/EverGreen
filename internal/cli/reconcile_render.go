package cli

// [S3] internal/cli/reconcile_render.go：对账 finding 的**人类可读渲染**唯一落点
// （M4 · T-…-056；对账合同 §10 第 2 条「`[材料支持不足]` 是**视图提示**」）。
//
// # 为什么渲染要单独成文
//
// R7（`support_insufficient` / W20）是**实时判定 + 零落盘**：判定本体在只读检查包内
// （`reconcile` 的 r7_support.go），而「这张卡目前没有材料支撑」这句人话必须有一个
// **唯一**的产出点，否则同一句提示会在两条渲染路径里各写一份、迟早出现措辞漂移，
// 甚至被谁顺手写进 Markdown。本文件因此是该提示在**整个仓库**里唯一的字面量落点：
//
//   - 只读检查包内该字符串**一次都不出现**（合同 §10 的反证 grep：检查包 + 落盘层恒 0）；
//   - 落盘层（`store`）内同样恒 0 —— 提示只进 stdout，永不进任何 frontmatter 或正文；
//   - `eg reconcile`（T-…-058）与 `eg check`（T-…-059）的人类可读输出**复用**本文件，
//     两条命令都不再自己拼一遍。
//
// # 边界（不得越线）
//
//   - **零写入、零提交、零状态**：本文件是纯函数集合（入参 finding、出参字符串行），
//     不引落盘层、不引提案层、不持有任何句柄，也不改任何产物的状态维度 —— R7 永不改
//     `status`，渲染层更不可能。
//   - **零命令注册**：本文件不注册任何命令、不写任何 `--help` 用法行；`eg reconcile` /
//     `eg check` 的命令本体分属 T-…-058 / T-…-059，本 task 只交付渲染函数。
//   - **不是落盘标记**：提示与 M3 的三个显著标记（`[失效]` / `[已删除]` / `[未过目]`，
//     顺序与字面量唯一来源是 `internal/query` 的标记文件）**不同族**：那三个是按对象维度
//     渲染的前缀，本提示是按 finding 渲染的行首提示，既不进那条前缀、也不参与其顺序，
//     M3 的双标记口径一格未动。
//   - **不与 M3 删除路径混淆**：删除报告里那张材料建议清单（键名与建议文案都是 M3 对外合同，
//     A-28 路径分治）是另一套东西，本文件不读它、不改它、不复用它的文案 —— 那张清单的
//     字段名因此在本文件内一次都不出现（M3 那面的非测试命中数一格未增）。

import (
	"fmt"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/reconcile"
)

// SupportInsufficientHint 是 R7 的**视图提示**逐字字面量（合同 §10 第 2 条）。
//
// 它只出现在命令的人类可读输出里：既不写进任何 Markdown，也不进 JSON 信封的 finding
// 四键（`check` / `severity` / `targets` / `detail` 一格不扩），因此不影响机器口径。
const SupportInsufficientHint = "[材料支持不足]"

// ReconcileHintCount 是「带视图提示的 check」数：**恰 1**（M4 只有 R7 有视图提示）。
//
// 表长在编译期由 map 字面量与本常量双侧锁死（reconcileHintChecks 的长度等号由单测与
// e2e 源码级 grep 反证）：想给第二个 check 也塞一句提示，就必须显式改这个数字。
const ReconcileHintCount = 1

// reconcileHintChecks 是 check → 视图提示的封闭表（恰 ReconcileHintCount 项）。
var reconcileHintChecks = map[string]string{
	reconcile.CheckSupportInsufficient: SupportInsufficientHint,
}

// ReconcileHint 返回该 check 的视图提示；没有提示的 check 返回空串。
//
// 未知 check 同样返回空串（渲染层不做枚举校验：那是 finding 构造侧的职责，
// 在此重复校验只会让同一件事有两处判定）。
func ReconcileHint(check string) string { return reconcileHintChecks[check] }

// ReconcileFindingLine 渲染单条 finding 的人类可读行。
//
// 形态（事实与 JSON 同源，不引入 JSON 里没有的事实）：
//
//	[材料支持不足] W20 warning k-20261127-alpha —— <detail>
//	E11 error domains/ai/knowledge/k-x.md、domains/ml/knowledge/k-x.md —— <detail>
//
// 诊断码取自只读检查包的单射表（渲染层不另写一份码表）；取不到码时省略该段，
// 绝不编一个码出来。
func ReconcileFindingLine(f reconcile.Finding) string {
	var b strings.Builder
	if hint := ReconcileHint(f.Check); hint != "" {
		b.WriteString(hint)
		b.WriteString(" ")
	}
	if code, ok := reconcile.CodeOf(f.Check); ok {
		b.WriteString(code)
		b.WriteString(" ")
	}
	b.WriteString(f.Severity)
	if len(f.Targets) > 0 {
		b.WriteString(" ")
		b.WriteString(strings.Join(f.Targets, "、"))
	}
	if strings.TrimSpace(f.Detail) != "" {
		b.WriteString(" —— ")
		b.WriteString(f.Detail)
	}
	return b.String()
}

// ReconcileFindingLines 渲染整批 finding（顺序 = 检查包已排好的可复算顺序，不重排）。
//
// 零 finding 返回 nil（由调用方决定要不要打「本次对账零 finding」那条信息行，
// 那属报告体与命令层的职责，归 T-…-057 / T-…-058 / T-…-059）。
func ReconcileFindingLines(fs []reconcile.Finding) []string {
	if len(fs) == 0 {
		return nil
	}
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, ReconcileFindingLine(f))
	}
	return out
}

// SupportInsufficientLines 只渲染 R7 那一类 finding（视图提示场景的便利入口）。
//
// 存在的理由：`eg card show` 一类**单卡视图**只关心「这张卡有没有材料支撑」，
// 不需要把整批对账结果都打出来。筛选按 `check` 等值，不看提示字符串本身
// （提示是渲染产物，不能反过来当判据）。
func SupportInsufficientLines(fs []reconcile.Finding) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		if f.Check != reconcile.CheckSupportInsufficient {
			continue
		}
		out = append(out, ReconcileFindingLine(f))
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ReconcileHintSummary 汇总本次结果里带视图提示的条数（供命令层打一行摘要）。
//
// 只统计、不改任何事实：返回值是计数与提示文案，调用方自行决定要不要打。
func ReconcileHintSummary(fs []reconcile.Finding) string {
	n := 0
	for _, f := range fs {
		if ReconcileHint(f.Check) != "" {
			n++
		}
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%s 命中 %d 张知识卡：实时判定，仅提示，未改任何产物、未写任何标记",
		SupportInsufficientHint, n)
}
