package query

// `eg search` 的 **kind 收窄口径**（设计 §5.2 检索拆分；T-…-006-A）。
//
// 这是**唯一**的封闭枚举与校验落点：query 层、CLI 层、以及任何未来消费方都只认这里
// 定义的三值与这里的 normalize / list —— 不在多处硬编码字符串比较，避免「命令层判一套、
// 取数层判另一套」的口径分叉。
//
// 封闭口径（逐字，大小写 / 前后空白敏感）：
//
//	knowledge  只搜知识卡（k-*）——**默认**，也是 SearchRequest 零值的兼容口径；
//	opinion    只搜观点（o-*）；
//	all        两类都搜，在**合并后的统一候选集**上走同一套 Filter / 四级 SortEntries /
//	           删除过滤 / ApplyPage（不分组排序分页再拼接）。
//
// 只读、确定性：本文件没有任何 I/O，输入相同两次判定逐字相同。

import (
	"fmt"
	"strings"
)

// SearchKind 是 `eg search` 的检索面收窄口径（封闭三值，见本文件头）。
type SearchKind string

const (
	// SearchKindKnowledge 只搜知识卡（k-*）。它是**默认**口径，也是零值兼容目标。
	SearchKindKnowledge SearchKind = "knowledge"
	// SearchKindOpinion 只搜观点（o-*）。
	SearchKindOpinion SearchKind = "opinion"
	// SearchKindAll 两类都搜（统一候选集上全序 + 分页）。
	SearchKindAll SearchKind = "all"
)

// searchKinds 是封闭集合的**权威次序**（knowledge → opinion → all）。
// SearchKindList / 校验都从这一处派生，改一处即处处一致。
var searchKinds = []SearchKind{SearchKindKnowledge, SearchKindOpinion, SearchKindAll}

// SearchKindList 返回封闭值的固定文案 `knowledge|opinion|all`，供参数错误提示与用例反证。
// 次序即 searchKinds 的登记序，不随 map 遍历抖动。
func SearchKindList() string {
	parts := make([]string, 0, len(searchKinds))
	for _, k := range searchKinds {
		parts = append(parts, string(k))
	}
	return strings.Join(parts, "|")
}

// normalizeSearchKind 把请求里的 kind 归一到封闭三值之一。
//
// 口径（逐字，非等价类归并）：
//   - **零值兼容**：空串 == knowledge（SearchRequest 零值即默认只搜知识卡，M1–M5 的库内
//     调用方因此一字不受影响）；
//   - 恰等于 knowledge / opinion / all 之一 → 原样返回；
//   - 其余一切（大小写变体 `Knowledge`、带空白 ` all` / `knowledge `、复数 `opinions`、
//     缩写 `k`、单个空格 ` ` 等）→ 非法，wrap ErrInvalidQuery，文案列出封闭值。
//
// 空白**不** trim、大小写**不**折叠：检索口径要可逐字复算，容错归并会让「用户到底给了
// 什么」变得不可判定（与 --since/--until 的严格逐字口径同一原则）。
func normalizeSearchKind(k SearchKind) (SearchKind, error) {
	if k == "" {
		return SearchKindKnowledge, nil // 零值兼容
	}
	for _, ok := range searchKinds {
		if k == ok {
			return k, nil
		}
	}
	return "", fmt.Errorf("%w：--kind=%q 不是合法检索面，封闭值为 %s（大小写 / 前后空白敏感）",
		ErrInvalidQuery, string(k), SearchKindList())
}
