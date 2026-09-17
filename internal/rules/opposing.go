package rules

// `opposing` 方向规范化与同对去重的**唯一实现**（技术方案 §6.2；需求 EG-CVG-05）。
//
// 无向语义单向存储：按两端稳定 ID **字典序**取小者为写入端（唯一规范方向），
// 写入前对同一对去重——已存在则只更新 `reason`，**不产生第二条**。
// 端点是**跨类型**的（RelationEndpoint，k- / o- 皆可）：字典序只比 ID 全串本身，
// 与端点是知识卡还是观点无关，因此 k↔k / k↔o / o↔k / o↔o 四种组合走同一套规范化。
// 一对一条记录：不引入议题组、不聚合成任何第三方对象、不自动补全关系图。
// 方向不规范或同对重复由上层登记 W8（照写 + 进报告，不影响退出码）。

import "github.com/ikaqiu-Lemon/EverGreen/internal/model"

// OpposingPair 是规范化后的一条 opposing：From 永远是字典序较小的一端。
type OpposingPair struct {
	From   model.RelationEndpoint
	Target model.RelationEndpoint
	// Normalized 为真表示输入方向不是规范方向，已被调换（上层据此登记 W8）。
	Normalized bool
}

// Opposing 把输入的两端规范化成唯一方向。两端相同时原样返回（自反关系由上层拒绝）。
func Opposing(from, target model.RelationEndpoint) OpposingPair {
	if string(target) < string(from) {
		return OpposingPair{From: target, Target: from, Normalized: true}
	}
	return OpposingPair{From: from, Target: target}
}

// OpposingDuplicate 报告规范化后的这一对是否已存在于该卡的 relations[] 中。
// 命中即为「同对重复」：上层登记 W8，只更新 reason，**不追加第二条**。
func OpposingDuplicate(existing []model.Relation, pair OpposingPair) bool {
	for _, r := range existing {
		if r.Type != model.RelationOpposing {
			continue
		}
		// 同对判定与方向无关：命中任一端即视为已存在（历史非规范方向也算同对）。
		if r.Target == pair.Target || r.Target == pair.From {
			return true
		}
	}
	return false
}
