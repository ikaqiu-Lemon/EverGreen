package reconcile

// internal/reconcile/strict.go —— 强校验（`--strict`）severity 升级策略的**唯一真源**
// （M6 · T-evergreen.s1_main_flow-158614-074；合同 §9 A-56「默认宽松 + --strict 显式开启」）。
//
// # 唯一职责（一句话）
//
// 定义并回答「哪些诊断码在 `--strict` 下升为 error，其余一律不变」这**一个**策略问题，
// 且**只改 severity、诊断码一字不变**。它是一段**纯策略**：不读盘、不解析计划、不判定
// 任何业务事实，只把 (code, base_severity, strict) 映射到 effective_severity。
//
// # 升级面恰五条（合同 §9 第二 bullet，逐字）
//
// `W1` / `W2` / `W3` / `W4` / `W6` 在 `--strict` 下升 severity 为 **error，码不变**；
// 其余 `W*` / `I*` / `Q*` 一律不受影响。这五个码属**写前校验（plan 层）**诊断，
// 因此本策略的真正消费方是**写命令的写前强校验（plan 层的 precheck，S3）**——
// 它在 `--strict` 下把这五类 warning 视作 error，从而让写被拦下、退 `5` + `E15`（T-…-074 批次 B）。
// `eg check`（只读 R3 / R4 结构体检）的 finding 用的是 `E11`–`E14` / `W13`–`W20` 段，
// **与升级面不相交**，因此 `eg check --strict` 对其自身 finding 是恒等变换（默认路径零漂移）——
// 这不是本策略的缺陷，而是「升级面恰五条」的必然：`eg check` 本就不产这五个码。
//
// # 为什么升级面代码集不落在本文件，而在 internal/model
//
// 「哪五个码升级」这条事实同时被写前强校验（**plan** 层的 precheck）与本文件（**reconcile**
// 层的 severity 策略）消费，而按 §13 / 对账合同 §1.2 `plan` 与 `reconcile` **互不 import**。
// 若把表放在其中任一层，另一层就得各抄一份、必然漂移。因此升级面代码集的**单一真源**在
// 零依赖的 `internal/model`（model.StrictUpgradeCodes / model.IsStrictUpgradeCode），两层同引
// 一份。本文件只在其上叠加 reconcile 侧的 severity 映射（StrictLevelFor），不复制那张表。
//
// # 三条不可放宽的边界
//
//   - **只改 severity**：升级只改分级，`code` 逐字不变（`W3` 升 error 后仍是 `W3`，不是新码）。
//   - **默认路径零漂移**：`strict == false` 时**恒返回 base**——M1 ~ M5 的既有行为一字不变。
//   - **升级面封闭**：恰这五个码；由 model 的定长数组在编译期钉死，本层不得另开一套。

import "github.com/ikaqiu-Lemon/EverGreen/internal/model"

// StrictUpgradeCodeCount 是 `--strict` 下升为 error 的诊断码数：**恰 5**（`W1`/`W2`/`W3`/`W4`/`W6`）。
// 值取自单一真源 model，本层不另定义（避免第二份计数各自漂移）。
const StrictUpgradeCodeCount = model.StrictUpgradeCodeCount

// StrictUpgradeCodes 返回升级面的封闭集合副本（转发单一真源 model；供 reconcile 与用例逐字断言）。
func StrictUpgradeCodes() []string { return model.StrictUpgradeCodes() }

// IsStrictUpgradeCode 报告某诊断码是否落在 `--strict` 升级面内（转发单一真源 model，恰五条之一）。
func IsStrictUpgradeCode(code string) bool { return model.IsStrictUpgradeCode(code) }

// StrictLevelFor 返回某诊断码在给定强校验开关下的**有效 severity**。
//
// 语义硬约束（三条，缺一即违反合同 §9 A-56）：
//   - strict == false：**恒返回 base**（默认路径与升级前逐字一致，零漂移）；
//   - strict == true 且 code ∈ 升级面：返回 error（**只改 severity，code 由调用方原样保留**）；
//   - strict == true 且 code ∉ 升级面：仍返回 base（其余 W*/I*/Q* 不受影响）。
//
// 本函数**不碰 code**：它只回答「这一条在当前模式下算 error 还是保持原级」，
// 升级后的 code 仍是调用方手里那一个，绝不替换成别的码。
func StrictLevelFor(code, base string, strict bool) string {
	if strict && IsStrictUpgradeCode(code) {
		return SeverityError
	}
	return base
}
