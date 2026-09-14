package plan

// internal/plan/precheck.go —— 写前强校验（S3 precheck）的**策略层唯一落点**
// （M6 · T-evergreen.s1_main_flow-158614-074；合同 §2 / §9 / §16 / §18）。
//
// # 唯一职责（一句话）
//
// 回答「一份**已在临界区内最终化**的 ChangePlan 校验结果，在给定 `--strict` 开关下，
// 是否应被写前强校验拦下」这**一个**判定问题：`--strict` 下把 `W1`/`W2`/`W3`/`W4`/`W6`
// 视作 error，命中即 precheck 失败 —— 由调用方（CLI 写路径）落成 `E15` + 退出码 5、
// **零写入、不发布 intent、事务保持未闭合**。
//
// # 插入点：恒为临界区内的 S3（不得挪到锁外）
//
// 本策略消费的 `*Result` 必须来自**锁内**的重新发现 + 重读 + 重算 / 最终化
// （见 recover_hook.go 的 `runPlanCritical`：`S1` 取锁 → `S2` 恢复屏障 →
// `S3` 锁内 `store.New` + `EnvFor` + `Validate` → **precheck** → intent）。
// 锁外算出的校验结果可能是另一写者 rename 序列的混合快照，据此放行等于把「写前复核」
// 变成一句空话。本文件是**纯策略**：不读盘、不解析计划、不判定任何业务事实，只把
// (校验结果, strict) 映射到「放行 / 拦下 + 被升级的诊断清单」。
//
// # 为什么强校验面复用 model 的升级面真源而不在此另立一份
//
// 「哪五个码在 `--strict` 下升 error」这条事实的**唯一真源**是 `internal/model`
// （`model.StrictUpgradeCodes` = `W1`/`W2`/`W3`/`W4`/`W6`）。写前强校验（plan 层）与
// reconcile 层的 severity 策略都引用同一份 —— 之所以落在零依赖的 model 而非 reconcile，
// 是因为 `plan` 与 `reconcile` 按 §13 互不 import，只有共同的最底层 model 能被两者同引，
// 升级面才不会各抄一份而漂移。本文件只调 `model.IsStrictUpgradeCode`，**不抄码、不改码**：
// 升级只改 severity（`W3` 升 error 后仍是 `W3`）。
//
// # 三条不可放宽的边界
//
//   - **默认路径零漂移**：`strict == false` 时 `Precheck` **恒不失败**、升级清单恒空 ——
//     M1 ~ M5 的既有行为（error 级 finding 退 2、warning 照常放行）一字不变。
//   - **只改 severity**：被升级项的 `Code` 逐字保留，仅把 `Level` 记为 error 进升级清单；
//     原 `Result.Warnings` / `Result.Errors` 不被本函数改写（策略无副作用）。
//   - **只读命令不触发**：本策略只被写路径调用；`eg check` / 读查询自有其 finding 段
//     （`E11`–`E14` / `W13`–`W20`），与升级面不相交，`--strict` 对其为恒等变换。

import "github.com/ikaqiu-Lemon/EverGreen/internal/model"

// PrecheckResult 是一次写前强校验的判定回执。
//
// Failed 为真 ⇒ 调用方必须**零写入**地中止并落成 `E15` + 退 5；
// Upgraded 是「在 `--strict` 下由 warning 升为 error」的那批诊断（Code 原样、Level 记 error），
// 供报告如实交代「是哪几条把写拦下的」。
type PrecheckResult struct {
	Failed   bool
	Upgraded []Diagnostic
}

// Precheck 对一份**锁内最终化**的校验结果施加写前强校验策略。
//
// 语义（三条，缺一即违反合同 §9 A-56）：
//   - res == nil 或 strict == false：恒返回零值（不失败、无升级）——默认路径零漂移；
//   - strict == true：遍历 res.Warnings，凡 Code ∈ 升级面（`model.IsStrictUpgradeCode`）
//     者升为 error 计入 Upgraded；Upgraded 非空即 Failed；
//   - 非升级面的 `W*` / `I*` / `Q*` 一律不受影响（仍是 warning，照常放行）。
//
// 本函数**不碰** res 本身：它只回答判定并给出被升级清单，不改写调用方持有的 Result。
func Precheck(res *Result, strict bool) PrecheckResult {
	if res == nil || !strict {
		return PrecheckResult{}
	}
	var up []Diagnostic
	for _, w := range res.Warnings {
		if model.IsStrictUpgradeCode(w.Code) {
			u := w
			u.Level = LevelError // 只改 severity，Code 逐字保留
			up = append(up, u)
		}
	}
	return PrecheckResult{Failed: len(up) > 0, Upgraded: up}
}
