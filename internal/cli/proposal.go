package cli

// `eg proposal approve` 的**执行前重算与改判**（提案合同 §3 S-② 硬要求、§5.3 第 8 / 9a / 9b 步；
// T-evergreen.s1_main_flow-158614-035）。
//
// §3 逐字：「`eg proposal approve` **必须**在执行前重算影响面并与 `impact` 比对。
// 「先批准后重算」是本触发的**唯一**实现顺序，**不得省略重算直接执行**。」
//
// 本文件是该顺序在 CLI 侧的唯一落点，固定四步、不可换序：
//
//	① 读提案（只读解析 + 形态复核）
//	② **重算影响面**（recomputeImpact → internal/proposal 的直接扫描实现）
//	③ 裁决（proposal.DecideApprove：入参就是重算结果，绕过重算拿不到裁决）
//	④ 落盘：一致 → T1（status → approved，`execution` 一字不动）；
//	        不一致 → **不执行删除**，走触发② 生成新提案 + 原提案标 superseded
//
// 反证（本 task Acceptance）：`TestApprove_RecomputesImpactBeforeExecute` 用注入计数器断言
// 第 ② 步在任何写盘函数**之前**被调用；顺序颠倒或省略重算，用例立刻红。
//
// # 边界（本 task 不做）
//
//   - **不注册** `eg proposal` 子命令外壳、参数解析、`--confirm` 与退出码 6 —— 属 T-…-040；
//     本文件只交付被它调用的**能力**（approveProposal）。
//   - **不回写 `execution` 三态**、不出逐路径失败报告 —— 属 T-…-036。
//   - **不写任何知识数据**：批准这一动作本身零知识写入，真实删除仍是 ChangePlan 的
//     `delete` op（A-23 第 4 条：批准后的知识数据修改仍走 ChangePlan）。
//   - Git 与报告由 CLI 负责：本文件产出报告行，commit 由调用方（T-…-040）发起。

import (
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/proposal"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// approveRequest 是一次执行前重算改判的输入（子命令外壳属 T-…-040，这里只收已解析好的事实）。
type approveRequest struct {
	// ID 是待批准的提案 ID。
	ID proposal.ID
	// Rel 是提案在库的实际路径；留空按默认落位（F2：文件允许被改名 / 移动）。
	Rel string
	// Reason 是用户决定的理由（进 decision.reason）。
	Reason string
	// Today 是改判时生成新提案所需的日期段：**CLI 传入**，internal/proposal 不读时钟。
	Today model.Date
	// NewTitle 是触发② 新提案的标题；留空由 internal/proposal 拼默认标题。
	NewTitle string
}

// approveHooks 是四步链路里两类外部动作的注入点：**重算**与**写盘**。
//
// 生产路径一律用 defaultApproveHooks（重算 = internal/proposal 的直接扫描实现，
// 写盘 = A-23 的 guarded 提案写口）；测试用同样的签名注入计数器，从而把
// 「重算必须发生在写盘之前」判成可执行断言，而不是靠代码评审。
type approveHooks struct {
	recomputeImpact func(s *store.Store, targets []string) (proposal.Impact, error)
	markApproved    func(spec proposal.ApproveSpec) (store.Result, error)
	supersede       func(spec proposal.SupersedeSpec) (proposal.SupersedeResult, error)
}

// defaultApproveHooks 返回生产路径的三个实现。
func defaultApproveHooks() approveHooks {
	return approveHooks{
		recomputeImpact: proposal.RecomputeImpact,
		markApproved:    proposal.MarkApproved,
		supersede:       proposal.Supersede,
	}
}

// approveOutcome 是一次执行前重算改判的结论（供 T-…-040 组报告与决定退出码）。
type approveOutcome struct {
	// Decision 是裁决结果（含重算出的影响面与逐项差异）。
	Decision proposal.ApproveDecision
	// Executed 报告是否走了继续执行分支（T1）。**注意**：为真只表示提案已批准，
	// 不表示删除已完成 —— 知识数据的改动仍走 ChangePlan（`approved` ≠ 已执行）。
	Executed bool
	// Superseded 在触发② 改判时非 nil，承载三步动作的回执。
	Superseded *proposal.SupersedeResult
	// Lines 是人类可读报告行（不一致时逐字含「不执行删除」）。
	Lines []string
}

// approveProposal 执行「批准前重算 → 比对 → 改判 / 继续」的四步链路（顺序不可换）。
//
// 返回错误时**零写入**：读盘、形态复核、重算、裁决四步都在写盘之前，
// 任一步不过就直接返回（B4：保留现状，不做破坏性动作）。
func approveProposal(st *store.Store, req approveRequest, hooks approveHooks) (approveOutcome, error) {
	var out approveOutcome
	if st == nil {
		return out, proposal.ErrNoStore
	}
	rel := req.Rel
	if rel == "" {
		rel = proposal.Rel(req.ID)
	}
	// ① 读提案：只读解析 + 落盘形态复核（形态不成立不动它）。
	f, err := st.Read(rel)
	if err != nil {
		return out, err
	}
	p, err := proposal.Parse(f.Bytes)
	if err != nil {
		return out, err
	}
	if err := proposal.ValidateLayout(p); err != nil {
		return out, err
	}
	// ② 执行前**重算影响面**（不得省略：第 ③ 步的入参就是它的结果）。
	recomputed, err := hooks.recomputeImpact(st, p.P.Targets)
	if err != nil {
		return out, err
	}
	// ③ 裁决：一致走 T1，不一致走触发②。
	decision, err := proposal.DecideApprove(p.P, recomputed)
	if err != nil {
		return out, err
	}
	out.Decision = decision
	out.Lines = append(out.Lines, fmt.Sprintf("提案 %s：%s", p.P.ID, decision.Message))
	for _, d := range decision.Diff {
		out.Lines = append(out.Lines, "  影响面差异："+d)
	}
	// ④ 落盘。
	if decision.Proceed {
		res, err := hooks.markApproved(proposal.ApproveSpec{
			Store: st, Original: p.P.ID, OriginalRel: rel, Reason: req.Reason,
		})
		if err != nil {
			return out, err
		}
		out.Executed = true
		out.Lines = append(out.Lines, fmt.Sprintf(
			"  已批准（%s）：%s；知识数据的删除仍走 ChangePlan 的 delete op", decision.Edge, res.Path))
		return out, nil
	}
	sup, err := hooks.supersede(proposal.SupersedeSpec{
		Store:       st,
		Original:    p.P.ID,
		OriginalRel: rel,
		Trigger:     proposal.TriggerImpactChanged,
		Reason:      req.Reason,
		Today:       req.Today,
		NewTitle:    req.NewTitle,
		NewTargets:  p.P.Targets,
		NewImpact:   decision.Recomputed,
	})
	if err != nil {
		out.Superseded = &sup
		out.Lines = append(out.Lines, supersedeLines(sup)...)
		return out, err
	}
	out.Superseded = &sup
	out.Lines = append(out.Lines, supersedeLines(sup)...)
	return out, nil
}

// supersedeLines 把三步动作的回执渲染成报告行（已完成的步骤逐条在册；B4 下失败也如实列出）。
func supersedeLines(sup proposal.SupersedeResult) []string {
	out := make([]string, 0, len(sup.Steps)+1)
	for _, s := range sup.Steps {
		out = append(out, "  "+s)
	}
	if sup.Message != "" {
		out = append(out, "  "+sup.Message)
	}
	return out
}
