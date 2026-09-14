package cli

// 逐卡收敛结论的**唯一**渲染实现（T-…-026；ChangePlan 合同 §2 / §2.1、
// CLI 合同 §1.5 / §1.9；技术方案 §4.5、§4.6、§16.1）。
//
// 「每张候选卡是怎么被收敛的」有四个出口——`eg apply` 与 `eg report --last`
// 各有文本模式与 `--json` 两种形态。四个出口必须**同源同事实**：
//
//	--json  ：`data.convergence`（键集合与 M1 逐字不变，见 convergenceData）
//	文本模式：convergenceLines（本文件，两条命令共用同一个函数）
//
// 因此本文件是「收敛：」这一行块在整个 internal/ 里的**唯一**产地：
// apply.go 与 report.go 只调用 convergenceLines，绝不各留一份平行渲染
// （反证见 TestConvergenceRenderedOnce：全 internal/ 下「收敛：」零命中于本文件之外）。
//
// 本文件**只做格式化**：
//   - 不重新判断收敛结论（判断在 Agent 侧，规程见 skill/SKILL.md §3.1）；
//   - 不校验七值枚举（W5 校验属 internal/plan）；
//   - 不重排 / 不去重 / 不合并（渲染顺序 == plan 的 convergence[] 原始顺序）；
//   - 字段缺失只打「未给出」，绝不编造，也不做任何语义推断。

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/plan"
)

// ConvergenceMissing 是字段缺失时的固定占位词（EG-CVG-02：缺就是缺，不补算）。
const ConvergenceMissing = "未给出"

// ConvergenceAbsentNotice 是落盘记录里根本没有该字段时的固定说明
// （`eg report --last` 照常退 0：M1 期的旧记录本就没有这段事实，不是错误）。
const ConvergenceAbsentNotice = "该记录未包含逐卡收敛结论"

// convergenceItem 是渲染输入的中间形态：
// `eg apply` 从 plan.Convergence 转入，`eg report --last` 从落盘 JSON 转入，
// 两条路径自此往下**逐字**走同一个 convergenceLines。
//
// json tag 与 `data.convergence` 每条的键集合逐字一致（`card` / `relation` /
// `core_knowledge` / `conditions` / `reuse_purpose` / `note`），因此回放不会错位。
type convergenceItem struct {
	Card         string `json:"card"`
	Relation     string `json:"relation"`
	Core         string `json:"core_knowledge"`
	Conditions   string `json:"conditions"`
	ReusePurpose string `json:"reuse_purpose"`
	Note         string `json:"note"`
}

// convergenceItemsFromPlan 原样搬运 plan 侧条目（顺序守恒、条目数守恒）。
func convergenceItemsFromPlan(items []plan.Convergence) []convergenceItem {
	out := make([]convergenceItem, 0, len(items))
	for _, c := range items {
		out = append(out, convergenceItem{
			Card: c.Card, Relation: c.Relation, Core: c.Core,
			Conditions: c.Conditions, ReusePurpose: c.ReusePurpose, Note: c.Note,
		})
	}
	return out
}

// convergenceData 原样回带 convergence[]（含每条的 relation），不重新判断收敛结论。
// 键集合与 M1 基线逐字不变；字段映射与渲染共用 convergenceItem，杜绝两处漂移。
func convergenceData(items []plan.Convergence) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(items))
	for _, c := range convergenceItemsFromPlan(items) {
		out = append(out, map[string]interface{}{
			"card": c.Card, "relation": c.Relation, "core_knowledge": c.Core,
			"conditions": c.Conditions, "reuse_purpose": c.ReusePurpose, "note": c.Note,
		})
	}
	return out
}

// convergenceLines 把逐卡收敛结论渲染成人类可读行块（本包**唯一**实现）。
//
// 每张卡恰一段、每段恰 5 行：
//
//	收敛：<card> → <relation>
//	  核心知识：<core_knowledge>
//	  成立条件：<conditions>
//	  独立复用用途：<reuse_purpose>
//	  说明：<note>
//
// 无条目 → 返回 nil（不打空标题，不替 Agent 说「没有收敛」）。
func convergenceLines(items []convergenceItem) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items)*5+1)
	out = append(out, fmt.Sprintf(
		"逐卡收敛记录（%d 条，顺序同 ChangePlan 的 convergence[]，未重排未合并）", len(items)))
	for _, c := range items {
		out = append(out,
			"收敛："+convergenceField(c.Card)+" → "+convergenceField(c.Relation),
			"  核心知识："+convergenceField(c.Core),
			"  成立条件："+convergenceField(c.Conditions),
			"  独立复用用途："+convergenceField(c.ReusePurpose),
			"  说明："+convergenceField(c.Note),
		)
	}
	return out
}

// convergenceReplayLines 是 `eg report --last` 的入口：落盘字节 → 同一个渲染函数。
// 缺字段 / 不可解析都只陈述事实（调用方照常退 0），绝不补算出一份收敛结论。
func convergenceReplayLines(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return []string{ConvergenceAbsentNotice}
	}
	var items []convergenceItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return []string{"逐卡收敛结论不可解析：" + err.Error()}
	}
	return convergenceLines(items)
}

// convergenceField 只做「空 → 未给出」与换行折行两件事：
// 换行折成空格是为了保住「一字段一行」的可读结构，不改任何字面内容。
func convergenceField(v string) string {
	s := strings.TrimSpace(v)
	if s == "" {
		return ConvergenceMissing
	}
	s = strings.ReplaceAll(s, "\r\n", " ")
	for _, r := range []string{"\r", "\n", "\t"} {
		s = strings.ReplaceAll(s, r, " ")
	}
	return strings.TrimSpace(s)
}
