package cli

// `deprecated_new_support[]` 的产出点（报告合同 §4.6；提案与状态合同 §5.3
// 「失效卡关系与依据全保留、可继续追加 support」；T-…-039）。
//
// 为什么这条事实在**执行之后**才登记：它陈述的是「已经写进盘的支持材料落在了一张失效卡上」，
// 而不是「打算写」。写入被 B3 跳过时不该出现这条事实，因此只遍历 ex 的**已写入**记录。
//
// 为什么它**只是报告**、不改任何状态：EG-KNW-06 / U-06 明确禁止自动状态流转——
// 失效卡出现新支持材料既不恢复 status、也不产出「建议恢复」的动作，
// 只把事实交给用户判断。本文件因此没有任何写入调用，只读卡的 frontmatter。

import (
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/plan"
	"github.com/ikaqiu-Lemon/EverGreen/internal/report"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// 「支持」在两套封闭枚举里的逐字取值**不同名**（冻结合同 F4）：
// 材料关系（sources[].rel）是 `support`，论证关系（relations[].type）是 `supports`。
// 两者都得认，否则会漏掉一半的支持材料事实。
const (
	relMaterialSupport  = string(model.MaterialSupport)
	relKnowledgeSupport = string(model.RelationSupports)
)

// noteDeprecatedNewSupport 把本次已写入的支持型关系里「落在失效卡上的那些」逐条登记进报告。
//
// 两种载体都算：材料关系（sources[] 上的 supports）与论证关系（卡对卡的 supports），
// 后者的受支持端是 `target`（`from supports target`）。条数不折叠、不去重，与写入事实一一对应。
func noteDeprecatedNewSupport(st *store.Store, idx store.Index, ex *plan.ExecResult,
	rep *report.Report) {
	for _, m := range ex.Material {
		if m.Rel != relMaterialSupport || !cardIsDeprecated(st, idx, m.Card) {
			continue
		}
		rep.AddDeprecatedNewSupport(report.DeprecatedSupport{
			Card: m.Card, Source: m.Source, Note: m.Note, Rel: m.Rel,
			Detail: deprecatedSupportDetail(m.Card),
		})
	}
	for _, k := range ex.Knowledge {
		if k.Type != relKnowledgeSupport || !cardIsDeprecated(st, idx, k.Target) {
			continue
		}
		rep.AddDeprecatedNewSupport(report.DeprecatedSupport{
			Card: k.Target, Source: "", Note: k.From, Rel: k.Type,
			Detail: deprecatedSupportDetail(k.Target),
		})
	}
}

// deprecatedSupportDetail 是这条事实的逐字说明：只陈述事实与「系统不会做什么」。
func deprecatedSupportDetail(card string) string {
	return fmt.Sprintf("%s 是 deprecated 卡：关系与依据全部保留、支持材料照常追加；"+
		"status 不变、不产生恢复建议（是否恢复由用户用 eg restore 决定）", card)
}

// cardIsDeprecated 只读地判断一张卡当前是否 deprecated；解析不到一律返回 false
// （报告宁可少一条真实事实，也不能凭猜产出假数据 —— §4.6 不得输出假数据）。
func cardIsDeprecated(st *store.Store, idx store.Index, id string) bool {
	rel, err := idx.Resolve(id)
	if err != nil {
		return false
	}
	f, err := st.Read(rel)
	if err != nil {
		return false
	}
	card, err := store.CardOf(f.Bytes)
	if err != nil {
		return false
	}
	return card.Status == model.StatusDeprecated
}
