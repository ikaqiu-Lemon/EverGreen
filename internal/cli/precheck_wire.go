package cli

// internal/cli/precheck_wire.go —— 写前强校验（S3 precheck，`--strict`）的 CLI 出口接线
// （M6 · T-evergreen.s1_main_flow-158614-074 批次 B；合同 §9 / §17.1）。
//
// 职责恰三件，都不含任何裁决逻辑（策略在 internal/plan/precheck.go，升级面真源在
// internal/reconcile/strict.go）：
//   ① 声明写命令的 `--strict` flag 名（`StrictFlag`），供各 plan 写命令统一注册；
//   ② 把 plan 层给出的「升级为 error 的诊断清单」折成一枚携写前强校验诊断码的 *TxnBlockedError
//      （经 classifyExit5 归为 PrecheckFailedError → 退出码 5）；
//   ③ 把被升级项如实登记进报告（Code 逐字保留、error 级），让用户看清「是哪几条把写拦下的」。
//
// 诊断码闭合：本文件**不写** E15 字面量，只引用 `txn.CodePrecheckFailed` 常量
// （E15 / E16 的字面量只许落在 internal/txn）。

import (
	"fmt"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/plan"
	"github.com/ikaqiu-Lemon/EverGreen/internal/report"
	"github.com/ikaqiu-Lemon/EverGreen/internal/txn"
)

// StrictFlag 是写命令的强校验开关 flag 名（`eg <write> --strict`）。
//
// 取值与 `eg index status` 的 IndexStrictFlag 同为 "strict"，但语义不同：那条只对只读
// status 忽略 (size, mtime) 快路径，本条让 plan 写前校验把 W1/W2/W3/W4/W6 视作 error。
// 两者各挂各的命令、互不影响。
const StrictFlag = "strict"

// strictFlagUsage 是各 plan 写命令 `--strict` flag 的统一说明（唯一注册文案）。
const strictFlagUsage = "写前强校验：把 W1/W2/W3/W4/W6 视作 error，命中即零写入中止并退 5（默认宽松）"

// registerStrictFlag 是 plan 写命令注册 `--strict` 的唯一入口（apply / deprecate / restore /
// replaced-by / edit / rel add|remove 共用一份默认值与文案，不各写一遍）。默认 false：
// 不给该 flag 时行为与 M1~M5 逐字一致（非 strict 路径零漂移）。
func registerStrictFlag(fs *flagSet) {
	fs.Bool(StrictFlag, false, strictFlagUsage)
}

// PrecheckStrictNotice 是写前强校验拦下写入时的固定前缀（供用例逐字断言）。
const PrecheckStrictNotice = "写前强校验（--strict）拦截：升级面诊断被视作 error，本次请求事务零权威写入"

// precheckStrictBlocked 把「升级为 error 的诊断清单」折成携写前强校验诊断码的写前阻断错误。
//
// 头条诊断携 `txn.CodePrecheckFailed`（写前强校验失败），classifyExit5 据此把它归为
// *PrecheckFailedError、经 ExitCodeFor 落退出码 5；其后逐条转述被升级项（Code 原样、error 级），
// 好让报告 errors[] 交代清楚具体是哪些 W 码在 `--strict` 下把写拦了下来。
func precheckStrictBlocked(upgraded []plan.Diagnostic) *TxnBlockedError {
	codes := upgradedCodeList(upgraded)
	msg := fmt.Sprintf("%s（升级面命中：%s）", PrecheckStrictNotice, strings.Join(codes, "、"))
	diags := make([]Diagnostic, 0, len(upgraded)+1)
	diags = append(diags, Diagnostic{
		Code: txn.CodePrecheckFailed, Level: LevelError, Path: "ops",
		OpIndex: NonOpDiagnostic, Message: msg,
	})
	for _, u := range upgraded {
		diags = append(diags, Diagnostic{
			Code: u.Code, Level: LevelError, Path: u.Path,
			OpIndex: u.OpIndex, Message: u.Message, Target: u.Target,
		})
	}
	return &TxnBlockedError{Msg: msg, Diags: diags}
}

// notePrecheckUpgraded 把「写前强校验拦截」这一总体事实登记进报告。
//
// 逐条被升级项的 error 级明细由 precheckStrictBlocked 承载，经 render 的 mergeErrorIntoResult
// 落进 data.errors[]（报告体只承载 W / I 级，error 明细不在此重复一份）。这里只留一条
// info 行交代「哪些码在 --strict 下把写拦了下来」，供人类可读与用例定位。
func notePrecheckUpgraded(rep *report.Report, upgraded []plan.Diagnostic) {
	codes := upgradedCodeList(upgraded)
	rep.AddInfo("ops", report.NonOp, "%s（升级面命中：%s）", PrecheckStrictNotice,
		strings.Join(codes, "、"))
}

// upgradedCodeList 取升级项里出现过的 Code（保持出现次序、去重），供文案与诊断复用。
func upgradedCodeList(upgraded []plan.Diagnostic) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(upgraded))
	for _, u := range upgraded {
		if u.Code == "" || seen[u.Code] {
			continue
		}
		seen[u.Code] = true
		out = append(out, u.Code)
	}
	return out
}
