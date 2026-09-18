package cli

// eg context 的业务实现（合同 §1.4；技术方案 §8 第 2 步、§4.5 的 base）。
//
// **只读**：零文件变化、零 commit（EG-VIEW-01）——本文件没有任何 store 写口调用、
// 没有 git 调用，也不做模型调用与网络请求（候选打分是 internal/query 里的确定性算法）。
//
// 输出即 Agent 做收敛判断的**唯一**上下文来源，同时把每个「可能被本次加工修改」的文件的
// content_hash 一并交出，Agent 原样填进 plan.base —— 这是 B3（文件变过就跳过）唯一的版本依据。
// hash 口径**直接注入 store.ContentHash**，不另起一套，保证与 apply 写前重算逐字一致。

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// runContext 实现 eg context。
func (r *Root) runContext(inv *Invocation) (*Result, error) {
	domain, fallback := captureDomain(inv)
	// EG-DOM-03：领域不在 evergreen.yml 的 domains 里 → 退 1、零输出内容、零写入。
	// 与 capture 的口径差异是有意的：capture 是收录（照常登记 + warning），
	// context 要交给 Agent 做收敛判断，领域错了整份上下文就是错的，必须拒绝。
	if !inv.Config.HasDomain(domain) {
		return nil, &UsageError{Msg: fmt.Sprintf(
			"领域 %s 未登记在 %s 的 domains 里：请先 eg config set domains <d1,d2>（eg 绝不自选领域）",
			domain, ConfigFileName)}
	}

	ctx, err := query.Build(query.Request{
		Root:   inv.VaultRoot,
		Domain: domain,
		Source: inv.String("source"),
		Note:   inv.String("note"),
	}, store.ContentHash)
	if err != nil {
		if errors.Is(err, query.ErrTargetNotFound) {
			return nil, &ValidationError{
				Msg: err.Error(),
				Diags: []Diagnostic{{
					Level: LevelError, Path: "--source|--note", OpIndex: NonOpDiagnostic,
					Code:    E18,
					Message: err.Error(),
				}},
			}
		}
		return nil, err
	}

	res := &Result{Data: map[string]interface{}{
		"domain":                  ctx.Domain,
		"default_domain":          inv.Config.DefaultDomain,
		"default_domain_fallback": fallback,
		"source":                  ctx.Source,
		"notes":                   ctx.Notes,
		"cards":                   ctx.Cards,
		// D-3：candidates 兼容字段（≡ knowledge_candidates）与拆分后的双候选字段同时输出。
		// 三者都恒是数组（query 侧已初始化空切片），调用方读哪个都拿到数组而非 null。
		"candidates":           ctx.Candidates,
		"knowledge_candidates": ctx.KnowledgeCandidates,
		"opinion_candidates":   ctx.OpinionCandidates,
		"base":                 ctx.Base,
		// M3（T-…-033）：提案控制面**只给摘要**——每项仅 id / path / title / targets，
		// 供 Agent 判断「同一件事是否已有在办提案」以免重复提案；提案正文七分区一律不出，
		// 提案也不进 cards / candidates / base（提案不是知识数据，见提案合同 §10.3）。
		"proposals": ctx.Proposals,
	}}
	if fallback {
		res.Warnings = append(res.Warnings, Diagnostic{
			Level: LevelWarning, Path: ConfigFileName, OpIndex: NonOpDiagnostic,
			Message: fmt.Sprintf("未给 --domain：按 %s 的 default_domain 取 %s（default_domain_fallback）",
				ConfigFileName, domain),
		})
	}
	if len(ctx.Notes) > 0 {
		res.Warnings = append(res.Warnings, Diagnostic{
			Level: LevelInfo, Path: "notes", OpIndex: NonOpDiagnostic,
			Message: fmt.Sprintf("该原文在领域 %s 已有 %d 篇材料笔记：笔记是材料层产物，"+
				"只供回读原始提炼，不参与知识收敛、不进候选相似卡（EG-NOTE-04）", domain, len(ctx.Notes)),
		})
	}
	// query 只读诊断（M2 合同 §5）原样透出：Q 系列的「扫不动 / 结果不完整」（warning）
	// 与 D-3 的 candidates 弃用提示 I1（info）同源于 ctx.Diagnostics。**逐条保留 d.Level**——
	// 绝不把 info 硬编码成 warning：--json 的 warnings[] 与纯文本输出同源同事实，
	// 缺失结果绝不能看起来像完整结果，弃用提示也不能被误读成告警。Q 类一律不影响退出码
	// （context 仍退 0）。I1 由 query 侧无条件产出恰一条，CLI 不再各造一份。
	for _, d := range ctx.Diagnostics {
		res.Warnings = append(res.Warnings, Diagnostic{
			Code: d.Code, Level: d.Level, Path: d.Path, OpIndex: NonOpDiagnostic,
			Message: d.Message,
		})
	}
	res.Summary = append(res.Summary, fmt.Sprintf(
		"context：领域 %s，材料笔记 %d 篇，同领域 active 卡 %d 张，知识候选 %d 张，观点候选 %d 条，base %d 项（只读，零写入零 commit）",
		ctx.Domain, len(ctx.Notes), len(ctx.Cards),
		len(ctx.KnowledgeCandidates), len(ctx.OpinionCandidates), len(ctx.Base)))
	// 知识候选与观点候选两块**分别**逐项渲染，与 --json 的 knowledge_candidates /
	// opinion_candidates 同源同事实；兼容字段 candidates 不再单独渲染一遍（它 ≡ 知识候选）。
	res.Summary = append(res.Summary, candidateLines(knowledgeCandidatePrefix, ctx.KnowledgeCandidates)...)
	res.Summary = append(res.Summary, candidateLines(opinionCandidatePrefix, ctx.OpinionCandidates)...)
	res.Summary = append(res.Summary, proposalLines(ctx.Proposals)...)
	return res, nil
}

// candidateLines 把候选相似项逐条渲染成人类可读行（T-…-025 / T-…-006 阶段 6E）。
//
// 与 `--json` 的 `data.knowledge_candidates` / `data.opinion_candidates` **同源同事实**：
// 顺序逐字一致、得分与命中理由都取自同一份 query.Candidate，人读模式不再只打一个总数。
// 每项一行「<prefix><id>　得分 <n>　<标题>」，其后每条命中理由缩进一行；
// 理由为空的项不会进候选（query 侧已保证推荐必可解释），这里不做任何补写、不引入
// JSON 里没有的事实。prefix 由调用方按类别（知识 / 观点）传入，两类各渲一块、不混排。
func candidateLines(prefix string, cands []query.Candidate) []string {
	lines := make([]string, 0, len(cands)*2)
	for _, c := range cands {
		lines = append(lines, fmt.Sprintf("%s%s　得分 %d　%s",
			prefix, c.ID, c.Score, c.Title))
		for _, why := range c.Reasons {
			lines = append(lines, candidateReasonPrefix+why)
		}
	}
	return lines
}

// 候选项行与理由行的固定前缀（供渲染与测试共用，不散落字面量）。
// 知识候选与观点候选各用独立前缀，人读模式据此区分两类；理由行前缀两类共用。
const (
	knowledgeCandidatePrefix = "  知识候选 "
	opinionCandidatePrefix   = "  观点候选 "
	candidateReasonPrefix    = "      · "
)

// proposalLines 把在库提案渲染成人类可读的**摘要行**（T-…-033）。
//
// 与 `--json` 的 `data.proposals` 同源同事实：逐项「<p-id>　<标题>　targets: a,b」，
// 只出 title 与 targets 两项事实——**绝不**渲染提案正文任一 H2 的内容，
// 也不渲染 status / decision / execution（那是 eg proposal show 的事，属 T-…-040）。
// 提案数为 0 时不输出任何行，人读模式与 M2 完全一致。
func proposalLines(ps []query.ProposalSummary) []string {
	if len(ps) == 0 {
		return nil
	}
	lines := make([]string, 0, len(ps)+1)
	lines = append(lines, fmt.Sprintf("%s在库提案 %d 项（只给标题与 targets 摘要，正文不出）",
		proposalHeaderPrefix, len(ps)))
	for _, p := range ps {
		lines = append(lines, fmt.Sprintf("%s%s　%s　targets: %s",
			proposalLinePrefix, p.ID, p.Title, strings.Join(p.Targets, ",")))
	}
	return lines
}

// 提案摘要行的固定前缀（供渲染与测试共用，不散落字面量）。
const (
	proposalHeaderPrefix = "  "
	proposalLinePrefix   = "  提案 "
)
