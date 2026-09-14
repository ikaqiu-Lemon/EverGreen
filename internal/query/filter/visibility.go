package filter

// [M3] internal/query/filter/visibility.go：§5.1 四象限可见性真值表的**唯一声明落点**
// （提案与状态合同 §5.1 全表；T-evergreen.s1_main_flow-158614-043）。
//
// # 为什么真值表要单独成文
//
// 「`status`（active / deprecated）」与「删除维度（`deleted_at` 有值 / 无值）」是两个**正交**
// 维度，四个象限在 M3 全部可达。把 4 行 × 5 列的取值写成一份数据，可见性就从「散落在若干
// if 里的行为」变成可逐格比对的事实（表驱动用例 TestDeletedExcludedFromDefaultView）。
//
// # 本文件与 ADR-20 的关系
//
// 本文件**不碰**过目维度：它只吃 `status` 与删除维度两个布尔，与 ADR-20 那个受限只读信号
// （两个时刻的比较）无关，因此同包内的隔离事实不变——那个比较仍然只出现在 unreviewed.go 一处。
//
// # 只声明、零副作用
//
// 纯函数、无 I/O、无状态、不导入本仓任何其它包（叶子包口径与 unreviewed.go 一致）。
// **过滤在查询层做，记录不动**：可见性为假只表示「默认视图不展示」，绝不意味着删文件、
// 删关系条目或改任何字节（U-01 无物理删除的正面表述）。

// Quadrant 是四象限的坐标：两个正交维度各取一个布尔。
type Quadrant struct {
	// Deprecated ← `status == deprecated`。
	Deprecated bool
	// Deleted ← `deleted_at != null`。
	Deleted bool
}

// Visibility 是 §5.1 真值表的**五列**（列名与合同表头逐项对应）。
//
// 字段顺序即合同列序：默认检索 / 参与收敛与冲突判断 / 作为关系端点默认展示 / 综述取材 /
// 可显式查看。`ReplacedByTarget`（合同第六列）不在本任务范围内：那一列的拦截归提案与
// 生命周期指针命令（E10 / W12），本文件不做门闸判定。
type Visibility struct {
	DefaultSearch    bool
	Converge         bool
	RelationEndpoint bool
	Digest           bool
	ExplicitView     bool
}

// VisibilityOf 返回该象限的五列可见性（合同 §5.1 逐行抄录）：
//
//	active     + 未删除 → ✅ ✅ ✅ ✅ ✅
//	deprecated + 未删除 → ✅（标 [失效]） 🔴 🔴 🔴 ✅
//	active     + 已删除 → 🔴 🔴 🔴 🔴 ✅（标 [已删除]）
//	deprecated + 已删除 → 🔴 🔴 🔴 🔴 ✅（双标记）
//
// 「已删除」是**一刀切**的四列否：只要 `deleted_at` 有值，四个视图口一律不展示，
// 只剩显式查看这一条路径（合同 §5.1 后两行）。
func VisibilityOf(q Quadrant) Visibility {
	if q.Deleted {
		// 已删除：退出默认视图、退出收敛、退出关系端点展示、退出综述取材，仅可显式查看。
		return Visibility{ExplicitView: true}
	}
	if q.Deprecated {
		// 失效但未删除：**默认检索里同等可见并标 [失效]**（S1/M2 冻结口径），
		// 但不参与收敛、不作综述取材来源。
		return Visibility{DefaultSearch: true, ExplicitView: true}
	}
	return Visibility{
		DefaultSearch: true, Converge: true, RelationEndpoint: true,
		Digest: true, ExplicitView: true,
	}
}

// ExcludedFromDefaultView 报告该象限是否**退出默认视图**（默认检索列取反）。
//
// 供默认清单类视图直接消费：判据「默认视图不返回已删除项」的口径只有这一处。
func ExcludedFromDefaultView(q Quadrant) bool { return !VisibilityOf(q).DefaultSearch }

// ValidEndpoint 报告该象限能否**作为关系端点默认展示**。
//
// 关系过滤靠端点有效性：过滤发生在读路径，源文件 `relations[]` 条目**一条不少、一字不改**。
func ValidEndpoint(q Quadrant) bool { return VisibilityOf(q).RelationEndpoint }
