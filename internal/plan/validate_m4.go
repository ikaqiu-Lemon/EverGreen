package plan

// M4 期新增 op `set_stale` 的校验链与写入意图（对账合同 **§9**（R6 全文）/ §1.3 第 3 行
// 「写口归属」；裁决 **A-33**（新增 op，`AllOpNames` 16 → 17）/ **A-34**（不需要
// `--user-request`，写权限矩阵**不新增行**）；T-evergreen.s1_main_flow-158614-055 阶段 1）。
//
// # 为什么单独一个文件
//
// validate_m3.go 是提案与状态合同 §8.1 八个 op 的校验链（M3 期事实），本文件是 M4 期
// 唯一新增 op 的校验链。两者纪律完全一致：只做「校验 + 展开」，**不写盘、不 commit**，
// 落盘形态在 internal/store（`set_stale` → `ApplyStateWrite` 的**第五种**形态 stale）。
//
// # 三条边界（合同 §9 逐字，不得自行扩张）
//
//   - **两键封闭**：写入面恰 `{stale, stale_reason}`。正文 / `status` / 删除维度 /
//     替代指针 / `reviewed_at` / `updated_at` 一格不碰——本 op 的载荷里根本没有那些格。
//   - **取值封闭**：`stale_reason` 必须是 model.ValidStaleReasons() 的三值之一
//     （非三值 → E5、整条 op 不执行、零写入）；「多因并存取第一个命中值」的**顺序判定**
//     属 R6 检查器（`internal/reconcile`，阶段 2），本文件只做取值封闭校验，不自造第二套顺序。
//   - **综述专属**：对象类恒为「主题综述 r-」（矩阵 #33），`target` 是 `k-` / `n-` / `s-` /
//     `p-` 一律 E2 —— 两键不得出现在知识卡 / 材料笔记 / 原文 / 提案上。
//
// # 授权口径（A-34，必须分清两件事）
//
// A-34 只说「R6 写 `stale` **不需要** `--user-request`」，它**不是**豁免矩阵：
//   - `set_stale` 不进 StateOpNames（恒 5），因此缺 `initiator` 不会把 W7 升成 error；
//     本文件也不调 requireInitiator —— 它连 `initiator` 字段都不收（M4OpFields 恰两格）。
//   - 能不能写这一格仍由矩阵说话：#33 的 **P-A 格由 T-…-055 依 A-34 放开为 ✅**
//     （P-U 侧一格未动，仍是 🔴 —— 只加严不放宽：本 task 只净放开 A-34 授权的那一格）。

import (
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// ActSetStale 是**综述失准标记**这一写入形态（M4 · A-33：落盘走 store.ApplyStateWrite 的
// 第五种形态 stale，两键一次守卫写）。
//
// 载荷**逐键封闭**为那两个键：Reason 是封闭三值之一，`stale` 恒为 true（没有可供调用方
// 塞第三个键的地方，也没有「取消失准」的反向形态 —— 合同 §9 明文不自动清除 stale）。
const ActSetStale ActionKind = "set_stale_write" // M4 / S3

// StaleWrite 是一次综述失准标记的写入意图（校验后确定要落盘的值）。
type StaleWrite struct {
	// Reason 是封闭三值之一（model.StaleReason），落盘为 `stale_reason`。
	Reason model.StaleReason
}

// setStale 校验并展开 set_stale。
//
// 判据次序固定（先定位对象、再问能不能写、最后校验取值）：
//  1. `target` 必须是可解析、存在于全库的**主题综述**（`r-`）：其余 ID 类型一律 E2；
//  2. 矩阵 #33（`stale / stale_reason`）：P-A ✅（A-34 由对账自动写入）/ P-U 🔴；
//     🔴 → E6、整条 op 不执行、目标文件字节不变；
//  3. `reason` 必须给出且落在封闭三值内（缺失或第四种取值 → E5、零写入）；
//  4. B3：`baseCheck` 写入 ExpectedHash，base 未覆盖该文件 → W6 + 标记跳过（退 3）——
//     **B3 不豁免**，hash 过期由 store 返回 *SkipError{file_changed} 落进 skipped[]。
func (v *validator) setStale(op *Op) {
	rel, ok := v.recapTarget(op)
	if !ok {
		return
	}
	// 矩阵 #33：A-34 放开的是 **P-A 一格**；P-U 侧仍 🔴，由 matrixGate 统一登记 E6。
	if !v.matrixGate(op, ObjectReview, FieldReviewStale) {
		return
	}
	reason, ok := v.staleReason(op)
	if !ok {
		return
	}
	act := Action{Kind: ActSetStale, OpIndex: op.Index, Op: op, ID: op.Target, Path: rel,
		Domain: store.DomainOf(rel), Stale: &StaleWrite{Reason: reason}}
	v.baseCheck(op, op.Target, rel, &act)
	v.res.Actions = append(v.res.Actions, act)
}

// recapTarget 解析 set_stale 的 target：**只认主题综述**（`r-`，矩阵 #33 的对象类）。
//
// 为什么不复用 cardTarget / reviewedTarget：那两个入口分别只认 `k-` 与 `k-` / `n-`；
// `stale` / `stale_reason` 是综述专属两键（合同 §9），把卡或笔记放进来就等于让这两个键
// 长到别的对象上 —— 因此这里逐字只放 `r-` 前缀，其余 ID 类型（含写混的 `k-` / `n-` /
// `s-` / `p-`）一律 E2、零展开。
func (v *validator) recapTarget(op *Op) (string, bool) {
	id := op.Target
	if id == "" {
		v.add(errorAt(E2, op.Index, opPath(op.Index, "target"),
			"%s 缺 target：失准标记的落点无法定位", op.Name))
		return "", false
	}
	parsed, err := model.ParseID(id)
	if err != nil || parsed.Prefix != model.PrefixReview {
		v.add(errorAt(E2, op.Index, opPath(op.Index, "target"),
			"%s 的 target %s 不是主题综述 ID（%s…）：%s / %s 两键是综述专属，"+
				"不得落在知识卡 / 材料笔记 / 原文 / 提案上",
			op.Name, id, model.PrefixReview, model.FMKeyStale, model.FMKeyStaleReason))
		return "", false
	}
	rel, ok := v.resolve(id)
	if !ok {
		v.add(errorAt(E2, op.Index, opPath(op.Index, "target"),
			"target %s 不存在于全库：失准标记的落点无法定位", id))
		return "", false
	}
	if !v.frontmatterCheck(op, "target", rel) {
		return "", false
	}
	v.domainCheck(op, "target", rel)
	return rel, true
}

// staleReason 校验 `reason` 落在封闭三值内（合同 §9）。
//
// 缺失与第四种取值同判 **E5**（取值集合越界，与「type 不在四值内」同源体例）：
// 本层**绝不**代入默认理由 —— 补一个默认值等于凭空发明一条对账结论，R6 的可复算性当场失效。
func (v *validator) staleReason(op *Op) (model.StaleReason, bool) {
	reason, err := model.ParseStaleReason(op.Reason)
	if err != nil {
		got := op.Reason
		if !op.ReasonGiven {
			got = "（缺该字段）"
		}
		v.add(errorAt(E5, op.Index, opPath(op.Index, "reason"),
			"%s 的 reason=%s 不在 %s 封闭三值内：合法取值恰为 %v（本层不代入默认理由）",
			op.Name, got, model.FMKeyStaleReason, model.ValidStaleReasons()))
		return "", false
	}
	return reason, true
}
