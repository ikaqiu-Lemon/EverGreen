package cli

// [M3] internal/cli/render.go：显著标记在**命令输出层**的唯一渲染落点
// （提案与状态合同 §6.2「双标记口径」与本合同新定的标记顺序；T-…-043）。
//
// # 为什么渲染要单独成文
//
// 「同一语料两次执行输出逐字相同」是 M2 已冻结的口径，逐字前缀比对的测试要成立，
// 标记前缀就必须只有一条生成路径：顺序常量在 internal/query/markers.go，本文件只做
// 「拿到三个正交事实 → 交出确定性前缀」，search / card show 两条渲染路径都从这里取。
//
// # 判定的分工（ADR-20）
//
//   - `status == deprecated`、`deleted_at != null`：只读 frontmatter 事实，扫描层已带出；
//   - `updated_at > reviewed_at`：受限只读信号，判定**只在** internal/query/filter；
//     本文件是唯一把它接进输出的地方，接法是「调筛选器判一次，再交给渲染」，
//     绝不在这里重写那个比较，也不把它塞进排序或收敛。
//
// # 只读
//
// 本文件零写入、零 commit、零状态：纯函数 + 一次判定调用，失败即上抛带类型错误
// （不猜、不静默降级成「已过目」）。

import (
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
	"github.com/ikaqiu-Lemon/EverGreen/internal/query/filter"
)

// MarkerDeleted / MarkerUnreviewed 是 M3 新增的两个标记（提案与状态合同 §6.2）。
// 取值与 internal/query 同源，命令层不另写字面量——顺序与文本只有一处定义。
const (
	MarkerDeleted    = query.MarkerDeleted
	MarkerUnreviewed = query.MarkerUnreviewed
)

// markerPrefix 渲染标记前缀（顺序与字面量唯一来源是 internal/query）。
//
// 形态：`[失效][已删除][未过目]`，三者按维度命中情况取子集，**不插分隔符**。
func markerPrefix(st query.MarkerState) string { return query.MarkerPrefix(st) }

// unreviewedSignal 判定过目维度：把两个逐字原值交给筛选器（唯一判定落点）。
//
// 失败不兜底：`updated_at` 缺失或不是带时区 RFC3339 时，筛选器既不当已过目也不当未过目，
// 这里如实翻译成参数/数据校验失败（退 1），而不是悄悄按 false 渲染。
func unreviewedSignal(id string, updatedAt string, reviewedAt string) (bool, error) {
	hit, err := filter.Unreviewed(filter.Candidate{
		ID: id, UpdatedAt: updatedAt, ReviewedAt: reviewedAt,
	})
	if err != nil {
		return false, &UsageError{Msg: fmt.Sprintf(
			"无法判定「未过目」：%v（%s / %s 必须是带时区的 RFC3339 时刻）",
			err, "updated_at", "reviewed_at")}
	}
	return hit, nil
}

// cardMarkerState 组装单卡显式查看时的三维事实（合同 §5.1 第 4 行的**双标记**场景）。
//
// 三个维度各读各的来源，互不推断：status 来自卡自身、删除维度来自 `deleted_at`、
// 过目维度来自筛选器判定。
func cardMarkerState(view *query.CardShowResult) (query.MarkerState, error) {
	unreviewed, err := unreviewedSignal(view.Card.ID, view.UpdatedAt, view.ReviewedAt)
	if err != nil {
		return query.MarkerState{}, err
	}
	return query.MarkerState{
		Deprecated: view.Card.Deprecated,
		Deleted:    view.Card.Deleted,
		Unreviewed: unreviewed,
	}, nil
}

// searchHitMarkers 渲染检索命中的标记前缀。
//
// 恰两个维度：`[失效]`（M2 冻结口径）与 `[已删除]`（仅在显式带上已删除项时才可能出现，
// 默认视图里根本没有已删除项）。**刻意不含过目维度**：ADR-20 只允许那个信号出现在筛选
// 条件里，检索是排序路径，入口是 eg unreviewed。
func searchHitMarkers(h query.SearchHit) string {
	return markerPrefix(query.MarkerState{Deprecated: h.Deprecated, Deleted: h.Deleted})
}
