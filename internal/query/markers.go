package query

// [M3] internal/query/markers.go：显著标记的**唯一口径落点**
// （提案与状态合同 §6.2 全表 + §5.1 四象限；T-evergreen.s1_main_flow-158614-043）。
//
// # 为什么把标记集中在一个文件
//
// M3 新增恰 2 个标记（`[已删除]` / `[未过目]`），`[失效]` 沿用 S1 口径不改。三个标记的
// **文本字面量、输出顺序、JSON 字段名**必须只有一份定义：顺序一旦散落到 search / card show
// 两处渲染，就无法保证「同一语料两次执行输出逐字相同」（M2 已冻结口径），逐字前缀比对的
// 测试也就失去意义。
//
// # 顺序（本合同新定，§6.2 末段）
//
//	[失效] → [已删除]，再叠加 [未过目]，完整形态 [失效][已删除][未过目]。
//
// # 本文件**不做**判定「未过目」
//
// ADR-20 把 `updated_at > reviewed_at` 定成只读信号且**只允许出现在筛选条件里**，判定隔离在
// internal/query/filter 单包内；而 internal/query 是排序 / 关系分析 / 综述取材的当前宿主，
// 依赖闭包里**不得**出现那个包（用例 TestADR20_FilterIsolation 用 go list -deps 反证）。
// 因此 MarkerState.Unreviewed 是**入参**：由命令层（已依赖 filter 的 internal/cli）判定后注入，
// 本包只负责「拿到三个布尔事实 → 渲染成确定性文本与字段」。
//
// 删除维度（`deleted_at` 有值 / 无值）不是那个受限信号，故 `[已删除]` 的判定可以留在本包。
//
// # 阶段边界
//
// S3 才引入的第四个标记（综述材料是否足够那一项）在 M3 **不判定、不输出**，其文本与 JSON
// 字段名按完成判据要求在 internal/ 非测试源里零出现，故本文件也不登记它的字面量。

import "github.com/ikaqiu-Lemon/EverGreen/internal/model"

// MarkerDeleted 是删除维度的显著标记（合同 §6.2：判定 `deleted_at != null`）。
const MarkerDeleted = "[已删除]"

// MarkerUnreviewed 是过目维度的显著标记（合同 §6.2：判定 `updated_at > reviewed_at`，
// 判定本身在 internal/query/filter，见文件头）。
const MarkerUnreviewed = "[未过目]"

// FieldDeprecated / FieldDeleted / FieldUnreviewed 是三个标记在 JSON 里的字段名
// （合同 §6.2 的 JSON 字段列）。`unreviewed` 直接复用 internal/model 的键名常量：
// 判定值与渲染字段名共用一处字面量，不各写一份。
const (
	FieldDeprecated = "deprecated"
	FieldDeleted    = "deleted"
	FieldUnreviewed = model.FieldUnreviewed
)

// MarkerState 是一个产物在**三个正交维度**上的只读事实快照。
//
// 三者互不相干（合同 §5.1 / §6.1）：`status` 答「这条判断还算不算数」、删除维度答
// 「这份内容还要不要出现在当前知识库里」、过目维度答「用户有没有看过最新一次改动」。
// 读写任一维度绝不连带另两个，渲染层也不得由一个推断另一个。
type MarkerState struct {
	// Deprecated ← `status == deprecated`（S1 口径，不改）。
	Deprecated bool
	// Deleted ← `deleted_at != null`（S2 新增）。
	Deleted bool
	// Unreviewed ← `updated_at > reviewed_at`（S2 新增）。**由调用方注入**：
	// 判定只能在 internal/query/filter 发生（ADR-20），本包不自行比较两个时刻。
	Unreviewed bool
}

// MarkerOrder 是标记的**输出顺序**（合同 §6.2 新定，逐字）：`[失效]` → `[已删除]` → `[未过目]`。
//
// 返回切片而不是拼好的串：顺序是判据本体，测试可以逐格比对这份次序。
func MarkerOrder() []string {
	return []string{MarkerDeprecated, MarkerDeleted, MarkerUnreviewed}
}

// MarkerFieldKeys 是三个 JSON 布尔字段的名字，次序与 MarkerOrder **一一对应**
// （合同 §6.2 的「文本标记 ↔ JSON 字段」对应关系）。
func MarkerFieldKeys() []string {
	return []string{FieldDeprecated, FieldDeleted, FieldUnreviewed}
}

// MarkerFieldOf 给出某个标记文本对应的 JSON 字段名（一一对应，未登记的标记返回 false）。
func MarkerFieldOf(marker string) (string, bool) {
	order, keys := MarkerOrder(), MarkerFieldKeys()
	for i, m := range order {
		if m == marker {
			return keys[i], true
		}
	}
	return "", false
}

// Markers 按固定顺序渲染标记列表；三个维度都不命中时返回**空数组**（不是 nil：
// JSON 里必须是 `[]`，与 M2 既有口径一致）。
func Markers(st MarkerState) []string {
	hits := []bool{st.Deprecated, st.Deleted, st.Unreviewed}
	out := []string{}
	for i, marker := range MarkerOrder() {
		if hits[i] {
			out = append(out, marker)
		}
	}
	return out
}

// MarkerPrefix 是人类可读输出的标记前缀：把 Markers 逐个相连、**不插分隔符**
// （合同 §6.2 的完整形态 `[失效][已删除][未过目]`，供逐字前缀比对）。
func MarkerPrefix(st MarkerState) string {
	out := ""
	for _, marker := range Markers(st) {
		out += marker
	}
	return out
}

// MarkerFields 给出三个布尔字段的取值（键集合恒为 MarkerFieldKeys，**不因取值 false 而丢键**：
// 缺键与 false 在下游 jq 判据里不是一回事）。
func MarkerFields(st MarkerState) map[string]bool {
	return map[string]bool{
		FieldDeprecated: st.Deprecated,
		FieldDeleted:    st.Deleted,
		FieldUnreviewed: st.Unreviewed,
	}
}

// DeletedFromStamp 判定删除维度：`deleted_at` 有值即已删除（合同 §6.2）。
//
// 入参是 frontmatter 的**逐字原值**，空串即「该键缺省」——本层不回填任何默认时刻，
// 也不因为 `deleted_reason` 有无而改变判定（原因只是留痕，删除事实只看时刻键）。
func DeletedFromStamp(deletedAt string) bool { return deletedAt != "" }

// CardMarkerState 从扫描条目取出**本包能判定的两个维度**；Unreviewed 恒为 false，
// 需要它时由命令层用 internal/query/filter 判定后经 WithUnreviewed 注入（见文件头 ADR-20）。
func CardMarkerState(c CardEntry) MarkerState {
	return MarkerState{Deprecated: c.Deprecated, Deleted: c.Deleted}
}

// stampText 渲染可选时刻键的输出文本：**键缺省即空串**（不代入任何时刻）。
// 与 model.ReviewedAtText 同口径，但服务的是删除维度，故不复用那个按语义命名的函数。
func stampText(at *model.Stamp) string {
	if at == nil || at.IsZero() {
		return ""
	}
	return at.String()
}
