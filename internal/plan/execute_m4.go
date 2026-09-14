package plan

// M4 起新增写入形态的执行体（对账合同 §5 / **§9**；裁决 **A-33**）：
//
//  1. `mark_reviewed` 的**真实落盘**（M4 · T-evergreen.s1_main_flow-158614-051）；
//  2. `set_stale` 的**真实落盘**（M4 · T-evergreen.s1_main_flow-158614-055 阶段 1，R6
//     综述失准标记；A-34：不需要 `--user-request`）。
//
// 为什么单独一个文件而不塞进 execute_m3.go：那个文件是 M3 两条非追加形态 + 状态维度两条
// 形态的执行编排，属 M3 期事实；本文件是 M4 期新增写入形态的**唯一**落点。纪律完全一致：
//
//   - 只调 store 的**导出**状态写入入口 ApplyStateWrite，绝不直呼任何 setter
//     （写口唯一的机械判据要求 setter 的非测试调用点全部落在 internal/store/ 内）；
//   - 自己不拼字节、不写盘、不 commit（提交由命令层在编排末尾产生；对账场景下是
//     R1 纳管写口的**恰一次** commit，A-35）；
//   - B3 **不豁免**：ExpectedHash 一律经 e.expect(a) 带下去，hash 过期由 store 返回
//     *SkipError{file_changed}，经 record 落进 `skipped[]`（cause=content_hash_mismatch），
//     跳过命名仍只用 store.SkipReason 的封闭两值，不自造第三种 kind。
//
// **op 与形态计数**（加法等式，M3 期结论不改写）：`mark_reviewed` 不新增 op 名；
// `set_stale` 按 A-33 新增**一个** op，派发全集因此是「M3 期 16 + M4 新增 1 = **17**」
// （见 ops_m3.go），store 的状态写形态是「M3 期 4 + M4 新增 1 = **5**」；
// 写权限矩阵**仍恒 43 行**（`set_stale` 命中既有 #33，只是那一行的 P-A 格依 A-34 放开）。

import "github.com/ikaqiu-Lemon/EverGreen/internal/store"

// markReviewedWrite 执行 mark_reviewed：覆盖过目信号单键。
//
// 逐键封闭：spec 只带 Op + Rel + ExpectedHash + At 四格 —— **没有**可供调用方塞第二个键
// 的地方，状态维度、删除维度、替代指针维度与正文一格不碰（合同 §5「三维不牵连」）。
// 时刻取本次执行的 Stamp（ExecOptions.Stamp，与 `eg mark-reviewed` 同一 RFC3339 口径），
// 因此「补写成什么时刻」由调用方注入、可复算，本文件不读时钟。
func (e *executor) markReviewedWrite(a Action) {
	res, err := e.s.ApplyStateWrite(store.StateWriteSpec{
		Op:           store.StateWriteReviewedAt,
		Rel:          a.Path,
		ExpectedHash: e.expect(a),
		At:           e.opt.Stamp,
	})
	if !e.record(a, res, err) {
		return
	}
	// 过目信号是**第三个正交维度**：它既不是内容修改也不是状态流转，因此这里
	// 只把文件记进 Written（由 record 完成），**不**登记 CardsUpdated / high_impact ——
	// 「过目」不是对知识内容的改动，报告里不得把它渲染成一次卡片更新。
	e.out.Reviewed = append(e.out.Reviewed, a.ID)
}

// setStaleWrite 执行 set_stale：给综述写失准标记两键（`stale` + `stale_reason`）。
//
// 四条边界（合同 §9 逐字）：
//   - **两键、一次守卫写**：spec 只带 Op + Rel + ExpectedHash + StaleReason 四格；
//     两键在 store 侧落成**同一次** mutateGuarded（setFMScalarKeys），不是两次写。
//   - **取值封闭**：Reason 恒取校验期确定的封闭三值之一（校验在 validate_m4.go，
//     store 侧再校验一次），本文件不做任何补全、不代入默认理由。
//   - **不碰任何别的维度**：正文 / `status` / 删除维度 / 替代指针 / `reviewed_at` 一格不碰；
//     **绝不重算综述、绝不清除 stale**（合同 §9 明文两条「不自动」）。
//   - **B3 不豁免**：与本文件另一形态同一条口径，hash 过期即跳过、零写入、零字节改动。
func (e *executor) setStaleWrite(a Action) {
	st := a.Stale
	if st == nil {
		return
	}
	res, err := e.s.ApplyStateWrite(store.StateWriteSpec{
		Op:           store.StateWriteStale,
		Rel:          a.Path,
		ExpectedHash: e.expect(a),
		StaleReason:  st.Reason,
	})
	if !e.record(a, res, err) {
		return
	}
	// 失准标记是**综述自己的一个信号**，不是一次知识内容更新：这里只登记 RecapsStaled
	// （文件路径由 record 记进 Written），**不**登记 CardsUpdated / high_impact ——
	// 报告若把它折叠进 cards.updated，一次对账会看起来像改过知识内容。
	e.out.RecapsStaled = append(e.out.RecapsStaled, a.ID)
}
