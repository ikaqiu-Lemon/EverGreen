package cli

// 「本次 commit 一并提交了工作区既有改动」的**唯一披露口径**（I-…-023）。
//
// 为什么需要这一个文件：提交范围的既定声明面是 `git add -A`（`internal/git/doc.go:11`：
// 本次写入的改动与工作区已有改动一并进本次 commit），而 M1 第三轮评审选定的口径 A 逐字
// 要求「报告中**如实列出**它属于既有改动」。审计（D3）实测的形态是：行为如实存在，披露
// 却只有 plan 写链与 `eg config set` 两处有 —— `mark-reviewed` / `capture` / `undelete` /
// 提案控制面这些直写命令一律零披露，用户手工编辑中的半成品被**静默**提交进权威 Git 历史，
// 只有事后 `git show --name-only` 才能发现。
//
// 修法不是给每条命令各写一段措辞（那正是两套口径的由来），而是把「采样 + 措辞 + 级别」
// 收敛到这里一处：调用方只决定往哪个诊断容器里放。
//
// 三条不变量：
//   - **采样必须早于 Add**：`git add -A` 一旦跑完，工作区就没有「既有改动」这个概念了，
//     Sample 只能在 Commit 之前调用（`Repo.Sample` 读的是 `git status`）。
//   - **只披露、不改行为**：不做选择性暂存、不做工作区隔离、不改提交范围，也不改退出码。
//     这批修复的范围是「说清楚」，不是「换口径」（换提交范围属另一个声明面变更）。
//   - **干净工作区零输出**：没有既有改动就一条诊断都不产，避免给常态路径打噪声。

import (
	"fmt"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/git"
	"github.com/ikaqiu-Lemon/EverGreen/internal/report"
)

// existingChangesPath 是这条披露的 path 字段（与 plan 写链既有的那条保持同一落点）。
const existingChangesPath = "git.commit"

// existingChangesNotice 采样工作区，返回「既有改动被一并提交」的披露文案。
//
// ours 是**本次写入**涉及的 vault 内相对路径；其余改动即既有改动。返回空串表示无需披露
// （工作区没有既有改动，或采样失败 —— 采样失败绝不阻断提交：它是诊断，不是判据）。
func existingChangesNotice(repo *git.Repo, ours []string) string {
	if repo == nil {
		return ""
	}
	sample, err := repo.Sample(ours)
	if err != nil || len(sample.Existing) == 0 {
		return ""
	}
	return fmt.Sprintf(
		"本次 commit 含 %d 处工作区既有改动（按 git add -A 口径一并提交，不做对账）：%s",
		len(sample.Existing), strings.Join(sample.Existing, "、"))
}

// noteExistingChangesInReport 把披露写进 plan 写链 / 直写命令的报告体（info 级）。
//
// 必须在 repo.Commit 之前调用（见文件头第一条不变量）。
func noteExistingChangesInReport(rep *report.Report, repo *git.Repo, ours []string) {
	if rep == nil {
		return
	}
	if msg := existingChangesNotice(repo, ours); msg != "" {
		rep.AddInfo(existingChangesPath, report.NonOp, "%s", msg)
	}
}

// noteExistingChangesInResult 把披露写进只有 `Result.Warnings` 的命令产物（info 级）。
//
// `eg config set` / `eg init` 这类命令没有 report 容器，但它们同样会 `add -A`，因此同样
// 必须交代 —— 用同一份采样与同一句措辞，`data.warnings[]` 就是它们的披露面。
func noteExistingChangesInResult(res *Result, repo *git.Repo, ours []string) {
	if res == nil {
		return
	}
	if msg := existingChangesNotice(repo, ours); msg != "" {
		// 码取 report.CodeI1：§4.5.1 的 info 编号，其定义逐字包含「工作区既有改动说明」，
		// 因此两个容器用的是**同一个码**，不为直写命令另造第二个编号。
		res.Warnings = append(res.Warnings, Diagnostic{
			Code: report.CodeI1, Level: LevelInfo, Path: existingChangesPath,
			OpIndex: NonOpDiagnostic, Message: msg,
		})
	}
}
