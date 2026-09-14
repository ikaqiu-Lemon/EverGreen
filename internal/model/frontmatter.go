package model

// [M3] internal/model/frontmatter.go：`reviewed_at` 这一**可选** frontmatter 键的读写往返口径
// （提案与状态合同 §6.1 全表；T-evergreen.s1_main_flow-158614-042）。
//
// [M4] 同一条纪律下追加**综述专属**的 `stale` / `stale_reason` 两个键名常量与 `stale_reason`
// 的封闭三值（对账合同 §9；T-evergreen.s1_main_flow-158614-055 阶段 1）——
// 键名字面量与取值枚举各只有一处，写入侧（internal/store）、op 侧（internal/plan）
// 与后续的 R6 检查器都引用本文件，不各写一份字符串。
//
// # 为什么单独一个文件、且只给「取值」不给「判定」
//
// ADR-20 把「`updated_at > reviewed_at`」定为**只读信号**且**只允许出现在筛选条件里**，
// 隔离在 `internal/query/filter/unreviewed.go` 一个文件内。因此本文件负责的恰是三件事：
//
//   - 键名常量（写入侧 internal/store 用它定位那一行，不各处重写字面量）；
//   - 可选键的**读出往返**：`ok=false` 表示「键缺省」，而不是「时刻为零」——
//     两者必须可区分，否则「从未过目」会被写成一个具体时刻（回填默认值）；
//   - 「未过目」判定值在输出里的**键名**（供 T-…-043 渲染标记消费）。
//
// 判定本身（谁算未过目）**不在**这里：那是筛选器的唯一落点。
//
// # 三条不变式（合同 §6.1 逐行）
//
//   - **缺省即缺省**：本层从不为缺 `reviewed_at` 的产物补一个默认值，也不批量迁移历史文件；
//     缺省的语义等价于「从未过目」，由筛选器按 `reviewed_at = -∞` 处理。
//   - **第三个正交维度**：`reviewed_at` 与 `status`、与删除维度（`deleted_at` /
//     `deleted_reason`）互不相干，读写任一维度绝不连带另一维度。
//   - **不是写入触发**：M3 只承接「用户明确标记已过目」（`eg mark-reviewed`）这一条触发；
//     被动查看、结果报告、索引读取、Agent 写入一律不更新它（A-16 已裁决触发①属 S3/M4）。

import "fmt"

// FMKeyReviewedAt 是 `reviewed_at` 的 frontmatter 顶层键名。
//
// 写入侧（internal/store 的受守卫写口）与命令侧共用本常量：键名只有一处字面量，
// 「唯一写入路径」这条判据才不会因为某处手写字符串而失真。
const FMKeyReviewedAt = "reviewed_at"

// FieldUnreviewed 是「未过目」判定值在输出里的键名（合同 §6.1 标记表：`unreviewed: true`）。
//
// 本 task 只提供**判定值**，标记文本的输出与顺序归 T-…-043：那边渲染时读的就是这个键。
const FieldUnreviewed = "unreviewed"

// —— [M4] 综述失准标记的两个 frontmatter 键（对账合同 §9 / R6；A-33 / A-34）——
//
// 与 `reviewed_at` 同一条纪律：本文件只给**键名与取值**，不给判定。
// 「哪张综述算失准」是 R6 只读检查器的唯一职责（`internal/reconcile`，属 T-…-055 阶段 2），
// 落盘只在 `internal/store` 的受守卫状态写口（`SetStale`，第五种状态写形态）。
//
// 两键是**综述专属**（合同 §9 逐字）：不得出现在知识卡 / 材料笔记 / 原文 / 提案上，
// 该约束由写权限矩阵 #33（对象类恒为「主题综述 r-」）与 CLI / e2e 侧的键集合反证共同兜住。
const (
	// FMKeyStale 是 `stale` 的 frontmatter 顶层键名（取值恒 `true`，本层不提供清除口径：
	// 合同 §9 明文「不自动清除 stale」）。
	FMKeyStale = "stale"
	// FMKeyStaleReason 是 `stale_reason` 的 frontmatter 顶层键名（取值封闭三值，见 StaleReason）。
	FMKeyStaleReason = "stale_reason"
)

// StaleReason 是 `stale_reason` 的**封闭三值**（对账合同 §9 逐字）。
//
// 为什么做成封闭枚举而不是自由文本：合同要求「多因并存按固定顺序取**第一个**命中值」，
// 这条可复算性判据的前提就是取值集合封闭——自由文本一进来，同一份库跑两次可以给出
// 两个不同理由，R6 的结论就不可复算了。第四种取值一律拒写（store 侧 ErrStaleReasonClosed）。
type StaleReason string

const (
	// StaleReasonUpdated 引用卡 `updated_at` 晚于综述 `updated_at`。
	StaleReasonUpdated StaleReason = "引用卡已更新"
	// StaleReasonDeleted 引用卡被逻辑删除（`deleted_at` 非空）。
	StaleReasonDeleted StaleReason = "引用卡已逻辑删除"
	// StaleReasonDeprecated 引用卡失效（`status: deprecated`）。
	StaleReasonDeprecated StaleReason = "引用卡已失效"
)

// ValidStaleReasons 返回封闭三值。**声明顺序即合同 §9 的取值顺序**：
// 多因并存时取本切片里第一个命中的值（顺序即判定，调用方不得重排）。
func ValidStaleReasons() []StaleReason {
	return []StaleReason{StaleReasonUpdated, StaleReasonDeleted, StaleReasonDeprecated}
}

// Valid 报告 r 是否为封闭三值之一。
func (r StaleReason) Valid() bool {
	for _, want := range ValidStaleReasons() {
		if r == want {
			return true
		}
	}
	return false
}

func (r StaleReason) String() string { return string(r) }

// ParseStaleReason 严格解析 `stale_reason`：三值之外一律报错（本层不猜、不归一化）。
func ParseStaleReason(raw string) (StaleReason, error) {
	r := StaleReason(raw)
	if !r.Valid() {
		return "", fmt.Errorf("非法 %s %q：合法取值恰三值 %v（合同 §9 封闭枚举，顺序即首个命中顺序）",
			FMKeyStaleReason, raw, ValidStaleReasons())
	}
	return r, nil
}

// ReviewedStamp 读出可选键 `reviewed_at`：ok=false 表示**该键缺省**（从未过目）。
//
// 为什么返回两个值而不是零值 Stamp：零值时刻与「键不存在」在语义上是两件事，
// 合成一个值就必然要在某处代入默认时刻，那正是「不回填默认值」明令禁止的。
func ReviewedStamp(reviewed *Stamp) (Stamp, bool) {
	if reviewed == nil {
		return Stamp{}, false
	}
	if reviewed.IsZero() {
		// 键在但取值不可用（零值时刻）同样按「缺省」处理：本层不猜用户意图、不补值。
		return Stamp{}, false
	}
	return *reviewed, true
}

// ReviewedAtText 渲染 `reviewed_at` 的输出文本：**键缺省即空串**（不代入任何时刻）。
func ReviewedAtText(reviewed *Stamp) string {
	at, ok := ReviewedStamp(reviewed)
	if !ok {
		return ""
	}
	return at.String()
}

// CardReviewedAt / NoteReviewedAt 是四类产物**同构**的读出口：字段名与语义完全一致，
// 调用方不按产物类型分叉（原文与综述在 S1/S2 无该键，读出即缺省）。
func CardReviewedAt(c Card) (Stamp, bool) { return ReviewedStamp(c.ReviewedAt) }

// NoteReviewedAt 见 CardReviewedAt。
func NoteReviewedAt(n Note) (Stamp, bool) { return ReviewedStamp(n.ReviewedAt) }
