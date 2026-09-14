package cli

// [C2 · I-…-025] internal/cli/check_txn.go：`eg check` 的**事务态披露**面。
//
// # 为什么单独一个文件
//
// `eg check` 的本体（check.go）有一组结构性门禁（处理函数符号只许出现在 check.go / check_test.go、
// 文件内不得出现计划层 / 提交面 / 直写 API / 被排除 check 字面量 / 收窄参数名）。事务态披露要读
// `internal/txn`，与那些门禁无关但会把 check.go 越撑越大；更重要的是它是**另一件事**：
// R3 / R4 是「库的结构对不对」，事务态是「这个库现在还能不能写」。两件事各占一个文件，
// 谁改坏了谁一眼可见。
//
// # 唯一职责（一句话）
//
// 在**只读**前提下回答阻断态的三个问题 —— **谁**（全部问题 `txn_id`）、**为什么**
// （逐个的不可解析原因）、**怎么办**（CLI 出路 + 人工处置步骤与风险）。
//
// # 判据来源（声明面）
//
//   - `2027-01-24-m6-atomicity-and-strict-check-contract.md` §18.4 / §4.1.2：
//     「损坏事务给 `txn_id` 与不可解析原因」「扫描全集异常给全部问题 `txn_id` 列表，便于人工处置」；
//   - 同合同 §12：M6 新增诊断码**恰 5 条** ⇒ 本文件**不发新码**（事务态是未编号 warning，
//     合同 §5 只授权未编号 **warning** 填空码，这里正是 warning）；
//   - 同合同 §13.4：命令数恒 22 ⇒ 本文件**不新增命令**、不提供 `--fix` 式清理开关；
//   - 同合同 §18.1：C 类只读路径**不产 E15、不退 5** ⇒ 事务态在本命令里一律 warning、
//     **退出码一字不改**（`eg check` 仍是 `0` / `1` / `2` 三档）；
//   - `2026-09-01-eg-cli-contract.md` §5：`path` 是**文件路径** ⇒ `txn_id` 落 `target`，
//     `path` 放 `.index/txn/<txn_id>` 这一真实目录路径；
//   - `skill/SKILL.md` 的 `E15` 处置指引「先跑 `eg check --strict` 定位」——本文件就是让那句话成真的落点。
//
// # 边界（逐条不得越线）
//
//   - **只读**：只调 `txn.Scan`（该函数自身零写盘），不取锁、不跑恢复、不清理、不改任何字节；
//     因此 `eg check` 在阻断态依然是零文件变化、恒 0 次提交。
//   - **不改退出码**：阻断态是 warning，不进 error 计数，`checkExitCode` 的入参一字不受影响。
//   - **不与 `--strict` 联动**：`--strict` 对本命令 finding 仍是恒等变换（见 CheckStrictNotice）；
//     事务态在**默认面**就披露，因此 `eg check` 与 `eg check --strict` 说的一样多 ——
//     否则「必须加 --strict 才看得见库被堵住」本身就是下一个坑。
//   - **不猜**：`Scan` 失败就如实说没采到样，绝不把「没采样」渲染成「事务态正常」。
//   - **口径单源**：人工出路文案取 `txn.BlockedRemedyHint`（txn 包里那一份），
//     本文件不抄第二份措辞，文档三面也引同一句。

import (
	"errors"
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/report"
	"github.com/ikaqiu-Lemon/EverGreen/internal/txn"
)

// CheckTxnHealthyNotice 是「事务态这一面本次看过了、没问题」的诚实交代（只进人类可读摘要）。
const CheckTxnHealthyNotice = "事务态：未闭合事务 ≤ 1 且无损坏事务，写侧未被阻断"

// CheckTxnBlockedHead 是阻断态摘要的固定抬头（措辞固定便于逐字断言与检索）。
const CheckTxnBlockedHead = "事务态：写侧已被阻断"

// checkTxnRemedyHelpLine 是 `eg check --help` 里那句**人工出路**的唯一来源。
//
// 它**只是引用**：处置对象的目录形态（`.index/txn/<txn_id>/`）与「下一条写命令自动恢复并留痕
// `W26`」这两件事都是 S5 事务包的实现事实，口径单源在 `txn.BlockedRemedyHintOneLine`
// （由该包的 IndexDirName / TxnDirName / CodeTxnRecovered 拼出）。CLI 层**不自带**这些字面量：
// 架构边界要求事务实现细节不外溢到命令层（full 门禁 TestNoOutOfScopeImplementation 的那一格），
// 同时也避免同一份路径口径在 cli / txn 各存一份、改一处漏一处。
//
// 与 §「口径单源」那条边界一致：长版出路仍取 `txn.BlockedRemedyHint`（诊断体里那份），
// 本常量是同一份口径的一行短版，两者都不在 CLI 侧重述措辞。
const checkTxnRemedyHelpLine = txn.BlockedRemedyHintOneLine

// txnStateDisclosure 只读采样事务态，返回 (warning 级诊断, 人类可读摘要行)。
//
// 三种结局：
//
//	① Scan 失败          → 一条 warning 如实说「未采样」（不猜、不报绿）；
//	② 未阻断（≤1 Open、零 Corrupt）→ 零诊断 + 一条「看过了没问题」摘要（健康库零噪音）；
//	③ 阻断               → 逐个问题事务一条 warning（`target` = txn_id、`path` = 事务目录、
//	                       message 含不可解析原因）+ 一条总述（全部 txn_id 列表）+ 一条人工出路。
func txnStateDisclosure(root string) ([]report.Diagnostic, []string) {
	warn := func(path, target, format string, args ...interface{}) report.Diagnostic {
		return report.Diagnostic{
			Code: "", Level: report.LevelWarning, Path: path, Target: target,
			OpIndex: report.NonOp, Message: fmt.Sprintf(format, args...),
		}
	}

	res, serr := txn.Scan(root)
	if serr != nil {
		d := warn(txn.IndexDirName+"/"+txn.TxnDirName, "",
			"事务态未采样（nil）：%s；本次无法判断写侧是否被阻断 —— 未采样 ≠ 事务态正常",
			oneLineReason(serr.Error()))
		return []report.Diagnostic{d}, []string{"事务态：未采样（见 warning）"}
	}

	var blocked *txn.ScanBlockedError
	if !errors.As(res.Blocked(), &blocked) {
		return nil, []string{CheckTxnHealthyNotice}
	}

	// 逐条搬运 txn 层已经算好的披露事实：条目数守恒、原因原样、不折叠不去重。
	// 这里刻意复用 blocked.Diagnostics()——「谁是问题事务、原因是什么」只能有一个真源，
	// 抄第二遍就会出现「写命令说的和体检说的不一样」这种最难查的漂移。
	diags := make([]report.Diagnostic, 0, len(blocked.Corrupt)+len(blocked.OpenTxns)+2)
	for _, d := range blocked.Diagnostics() {
		// 级别降为 warning：C 类只读路径不产 error / 不退 5（合同 §18.1）；
		// 码位一并清空 —— E15 是**写前复核失败**的码，只读体检没有"写前"可言，
		// 借用它会让读者以为这条命令也 fail closed 了。未编号 warning 是合同授权的形态。
		diags = append(diags, warn(d.Path, d.Target, "%s", d.Message))
	}
	diags = append(diags, warn(txn.IndexDirName+"/"+txn.TxnDirName, "", "%s", txn.BlockedRemedyHint))

	lines := []string{
		fmt.Sprintf("%s：损坏事务 %d 个、未闭合事务 %d 个（协议上限 1）；"+
			"全部权威写命令与索引维护命令会退 5 + E15，只读命令不受影响",
			CheckTxnBlockedHead, len(blocked.Corrupt), len(blocked.OpenTxns)),
		txn.BlockedRemedyHint,
	}
	return diags, lines
}
