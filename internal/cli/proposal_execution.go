package cli

// 一次 apply 之后把「本次影响文件的已写 / 未写」逐路径记进被引用提案的 `execution`
// （提案合同 §4.3 / §5.3 第 9b–10 步、§8.3；T-evergreen.s1_main_flow-158614-036）。
//
// # 唯一计数来源（I-…-002 的 DoR 硬约束）
//
// 本文件**不自己数任何路径**：账本只有 internal/proposal 的 PathLedger 一处。
// 已写 / 未写 / 三个计数、提案回写的两个路径数组、报告 `proposals[]` 里的两个数组，
// 全部取自**同一个账本对象**的访问器（Written / Unwritten / Counts）。
// 因此不存在「报告数一遍、回写再数一遍」的第二处计数 —— I-…-002 那种双源形态
// 在这条链路上写不出来。
//
// # 什么时候回写（只在有**真实执行痕迹**时）
//
// 只有当本次执行确实碰过该提案的影响文件（有已写路径，或有跳过 / 写失败的路径）时才回写。
// 「本次根本没执行到该提案」（例如 op 通过校验但其落盘时序尚未交付）一律**不回写**：
// `execution` 保持 not_started，报告也不产出执行事实条目 —— 不假装尝试过。
//
// # 本文件不做
//
//   - 不回写 `succeeded` 的落盘时序（逐文件删除的十一步时序归 T-…-041）：
//     全部写入且提交成功时本文件不表态，`execution` 仍由那条时序负责回写；
//   - 不做任何破坏性动作（B4）：不还原、不删文件、不 git checkout / reset；
//   - 不发诊断编号：这里只用未编号 warning 与既有 I1 info。

import (
	"fmt"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/plan"
	"github.com/ikaqiu-Lemon/EverGreen/internal/proposal"
	"github.com/ikaqiu-Lemon/EverGreen/internal/report"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// proposalExecInput 是一次 execution 回写的输入事实（全部来自本次 apply，不读时钟）。
type proposalExecInput struct {
	Store *store.Store
	Index store.Index
	Plan  *plan.ChangePlan
	Exec  *plan.ExecResult
	Stamp model.Stamp
	// CommitErr 非 nil 表示本次 commit 失败（退 4；磁盘保留现状，B4）。
	CommitErr error
}

// recordProposalExecutions 把本次执行事实按提案逐路径回写进 `execution`，并投影进报告。
//
// done 记住已回写过的提案 ID：提交失败分支会再调一次，同一份提案不重复回写。
func recordProposalExecutions(in proposalExecInput, rep *report.Report, done map[string]bool) {
	if in.Store == nil || in.Exec == nil || in.Plan == nil {
		return
	}
	for _, id := range proposalRefs(in.Plan) {
		if done[id] {
			continue
		}
		rel := proposalRelOf(in.Index, id)
		f, err := in.Store.Read(rel)
		if err != nil {
			rep.AddWarning(unnumberedWarning(rel,
				"提案 %s 读不到（%s），本次执行事实无法回写 execution：%v", id, rel, err))
			continue
		}
		pf, err := proposal.Parse(f.Bytes)
		if err != nil {
			rep.AddWarning(unnumberedWarning(rel,
				"提案 %s 形态不成立，本次执行事实无法回写 execution：%v", id, err))
			continue
		}
		led, err := proposalLedger(in, pf, rep, rel)
		if err != nil {
			rep.AddWarning(unnumberedWarning(rel,
				"提案 %s 的影响文件账本建不起来，本次执行事实无法回写 execution：%v", id, err))
			continue
		}
		// 没有任何执行痕迹 → 本次没执行到这份提案，一个字节都不动（不假装尝试过）。
		if led.Counts().Written == 0 && len(led.Skips()) == 0 {
			continue
		}
		// 全部写入且提交成功 → 属 succeeded 的落盘时序（T-…-041），本文件不表态。
		if led.Complete() && in.CommitErr == nil {
			continue
		}
		spec := proposal.RecordSpec{
			Store: in.Store, ID: pf.P.ID, Rel: rel,
			Status: proposal.ExecFailed, AttemptedAt: in.Stamp,
			Reason: failureReason(led, in.CommitErr), Ledger: led,
		}
		res, err := proposal.RecordExecution(spec)
		if err != nil {
			rep.AddWarning(unnumberedWarning(rel,
				"提案 %s 的 execution 回写被拒（磁盘保留现状，未做任何还原）：%v", id, err))
			continue
		}
		done[id] = true
		rep.Links = append(rep.Links, res.Rel)
		rep.AddProposal(report.ProposalEntry{
			ID: string(res.ID), Path: res.Rel, Status: string(pf.P.Status),
			Targets: pf.P.Targets,
			Execution: report.ProposalExecution{
				Status:      string(res.Status),
				AttemptedAt: in.Stamp.String(),
				Reason:      spec.Reason,
				GitCommit:   nil,
				// 两个数组来自同一个账本对象：报告不重新收集、不重新计数。
				WrittenPaths:   res.Written,
				UnwrittenPaths: res.Unwritten,
			},
		})
		rep.AddInfo(proposalReportPath(rel), report.NonOp, "%s", res.Message)
		if in.CommitErr != nil {
			rep.AddInfo(proposalReportPath(rel), report.NonOp,
				"提案 %s 的 execution 已回写，但本次 commit 失败，该回写未进 commit；"+
					"磁盘保留当前状态，未做任何还原（B4）", res.ID)
		}
	}
}

// proposalReportPath 返回诊断里指向提案 execution 的字段路径。
func proposalReportPath(rel string) string {
	return rel + ":" + proposal.KeyExecBlock
}

// unnumberedWarning 拼一条**未编号** warning（本文件不发诊断编号）。
func unnumberedWarning(path, format string, args ...interface{}) report.Diagnostic {
	return report.Diagnostic{
		Code: "", Level: report.LevelWarning, Path: path, OpIndex: report.NonOp,
		Message: fmt.Sprintf(format, args...),
	}
}

// proposalRefs 收集本次 plan 里被引用的提案 ID（按声明顺序，去重）。
func proposalRefs(p *plan.ChangePlan) []string {
	var out []string
	seen := map[string]bool{}
	for _, op := range p.Ops {
		if op == nil || op.Proposal == "" || seen[op.Proposal] {
			continue
		}
		seen[op.Proposal] = true
		out = append(out, op.Proposal)
	}
	return out
}

// proposalRelOf 解析提案在库的实际路径：优先走 id 索引（F2：文件允许改名 / 移动），
// 索引里没有再退回默认落位。
func proposalRelOf(idx store.Index, id string) string {
	if rel, err := idx.Resolve(id); err == nil && store.IsProposalRel(rel) {
		return rel
	}
	return proposal.Rel(proposal.ID(id))
}

// proposalLedger 开一本账：全集 = 提案 targets 解析出的**影响文件全集**，
// 随后把本次执行的三类事实登记进去（已写 / B2·B3 跳过 / 写失败）。
//
// 三类事实的来源都是 plan 的执行回执（ExecResult），不是二次扫描磁盘：
// 「哪些写成了」只有执行器一处知道。
func proposalLedger(in proposalExecInput, pf *proposal.File, rep *report.Report,
	prel string) (*proposal.PathLedger, error) {
	var all []string
	for _, t := range pf.P.Targets {
		rel, err := in.Index.Resolve(t)
		if err != nil {
			rep.AddWarning(unnumberedWarning(prel,
				"提案 %s 的 target %s 在库里解析不到文件，未计入影响文件全集：%v", pf.P.ID, t, err))
			continue
		}
		all = append(all, rel)
	}
	led, err := proposal.NewPathLedger(all)
	if err != nil {
		return nil, err
	}
	for _, w := range in.Exec.Written {
		if !led.Has(w) {
			continue
		}
		if err := led.MarkWritten(w); err != nil {
			return nil, err
		}
	}
	for _, s := range in.Exec.Skipped {
		if !led.Has(s.Locator) {
			continue
		}
		if err := led.MarkSkipped(s.Locator, s.Kind,
			fmt.Sprintf("%s（cause=%s）", s.Detail, s.Cause)); err != nil {
			return nil, err
		}
	}
	// 写失败不是 B2 / B3 跳过：kind 留空（store.SkipNone），不硬塞成两个既有取值之一。
	for _, fd := range in.Exec.Failures {
		rel, err := in.Index.Resolve(fd.Target)
		if err != nil || !led.Has(rel) {
			continue
		}
		if err := led.MarkSkipped(rel, store.SkipNone, fd.Message); err != nil {
			return nil, err
		}
	}
	return led, nil
}

// failureReason 拼 `execution.reason`：逐条复述未写路径的原因，并写明 B4 口径。
func failureReason(led *proposal.PathLedger, commitErr error) string {
	c := led.Counts()
	var b strings.Builder
	fmt.Fprintf(&b, "本次执行部分完成：影响文件 %d 个，已写 %d 个、未写 %d 个",
		c.Total, c.Written, c.Unwritten)
	for _, s := range led.Skips() {
		fmt.Fprintf(&b, "；%s 未写入：%s", s.Rel, oneLineReason(s.Detail))
	}
	if commitErr != nil {
		fmt.Fprintf(&b, "；Git 提交失败：%s", oneLineReason(commitErr.Error()))
	}
	b.WriteString("；已写入的内容保留在磁盘，未做任何还原（B4）")
	return b.String()
}

// oneLineReason 把原因压成单行（frontmatter 标量不拼多行）。
func oneLineReason(s string) string {
	return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "\r", " "), "\n", " "))
}
