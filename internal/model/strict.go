package model

// internal/model/strict.go —— `--strict` 强校验**升级面代码集**的单一真源
// （M6 · T-evergreen.s1_main_flow-158614-074；合同 §9 A-56「默认宽松 + --strict 显式开启」）。
//
// # 为什么落在 model 而不是 reconcile / plan
//
// 「哪五个诊断码在 `--strict` 下升为 error」这条事实同时被两个互不依赖的层消费：
// 写前强校验在 **plan** 层（plan/precheck.go 的 S3 precheck），强校验 severity 策略在
// **reconcile** 层（reconcile/strict.go）。按 §13 / 对账合同 §1.2，`plan` 与 `reconcile`
// **互不 import**（两向都禁）。若把这张表放在其中任一层，另一层就取不到，只能各抄一份 ——
// 而「升级面恰五条」一旦抄成两份就会各自漂移。`model` 是零依赖的最底层，两层都允许 import，
// 因此这张表只能落在这里，才能让 plan 与 reconcile 引用**同一份**、不可能出现第二种口径。
//
// # 升级面恰五条（合同 §9 第二 bullet，逐字）
//
// `W1` / `W2` / `W3` / `W4` / `W6` 在 `--strict` 下升 severity 为 **error，码不变**；
// 其余 `W*` / `I*` / `Q*` 一律不受影响。**注意 `W5` 不在升级面**（合同逐字：跳过 W5）。
// 这五个码都属**写前校验（plan 层）**诊断（见 plan 层 diagnostic 定义）：
//   - W1 写入目标落在 plan.domain 之外；
//   - W2 关系四要素不全，或 reason 为空 / 等于关系名本身；
//   - W3 论证关系某端不是 status: active；
//   - W4 顶层命中废弃字段黑名单；
//   - W6 base 未覆盖某个被改文件。
//
// 本文件只承载**代码集这一条事实**；「升级只改 severity、code 逐字不变」的映射语义由消费方
// （reconcile.StrictLevelFor / plan.Precheck）实现，各层不在此另立第二套 severity 词汇。

// StrictUpgradeCodeCount 是 `--strict` 下升为 error 的诊断码数：**恰 5**（`W1`/`W2`/`W3`/`W4`/`W6`）。
//
// 写成常量并用它约束数组长度，是为了让「升级面恰五条」在**编译期**成立：
// 往 strictUpgradeCodes 里多写一行会得到「index out of range」编译错误，少写一行则
// 留下零值空串并被 model_test.go / 各消费层测试当场判红。
const StrictUpgradeCodeCount = 5

// strictUpgradeCodes 是升级面的**封闭全集**（长度固定数组：第 6 个码加不进来）。
var strictUpgradeCodes = [StrictUpgradeCodeCount]string{"W1", "W2", "W3", "W4", "W6"}

// StrictUpgradeCodes 返回升级面的封闭集合副本（供 plan 写前校验、reconcile 与用例逐字断言）。
func StrictUpgradeCodes() []string {
	out := make([]string, 0, StrictUpgradeCodeCount)
	out = append(out, strictUpgradeCodes[:]...)
	return out
}

// IsStrictUpgradeCode 报告某诊断码是否落在 `--strict` 升级面内（恰五条之一）。
func IsStrictUpgradeCode(code string) bool {
	for _, c := range strictUpgradeCodes {
		if c == code {
			return true
		}
	}
	return false
}
