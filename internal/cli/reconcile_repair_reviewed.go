package cli

// R2 修复桥：把对账产出的**修复意向**翻成一份内存 ChangePlan，交计划层的完整校验链落盘
// （对账合同 §5 / §1.3 写口归属表第 2 行；A-33 / A-34 / A-35；M4 · T-…-051）。
//
// 本文件是「只读检查」与「知识数据写入」之间的**唯一**通路，四条纪律逐条对应硬约束：
//
//  1. **不新增 op**：op 名恒取既有 `mark_reviewed`（A-33），`plan.AllOpNames()` 恒 16、
//     写权限矩阵不新增行（命中既有 #7 知识卡 / #22 材料笔记两行的 P-U 格）。
//  2. **不绕开 ChangePlan**：本文件**不 import 落盘层**（判据：本文件里那个包路径字面量
//     零命中），也不直呼任何 setter；写盘只可能发生在 `plan.Execute` 之后。base 由
//     apply.go 的 planBase 按 `eg context` 的同一口径算（唯一的 content_hash 实现）。
//  3. **逐键封闭**：待写键集合恒 `{reviewed_at}` —— 键名来自检查侧的封闭表，且本文件在
//     组装前**逐条比对**每份修复意向的 keys，不是恰那一个键就整批拒绝（ErrReviewedKeys）。
//     状态维度、删除维度、替代指针维度与正文一格不写：`mark_reviewed` 这一 op 的字段表
//     里根本没有承载它们的字段（形态上写不进去），diff 级反证见本文件的用例。
//  4. **恰一次 commit**：本文件**自己不提交**（Commits 恒 0）。一次对账里 R1 纳管与 R2
//     修复的写入合并为**恰一次** commit，那一次由纳管写口在编排末尾产生（A-35）——
//     所以这里刻意不复用会自带一次提交的 runPlan，而走 apply.go 的无提交入口
//     executePlanNoCommit（与 runPlan 逐字同一条校验 + 执行链，一格不放宽）。
//
// B3 不豁免：ExpectedHash 经 base 进 action，写前重算不一致 → 该文件进 `skipped[]`
// （封闭两值之一），本文件**不写它**、也不重试；对应的 finding 仍产出，并由检查侧的
// WithSkipNotice 在同一条 detail 里如实注明已跳过。
//
// 幂等：零命中 → 零 op、**根本不组装 plan**、零写入零提交（干净库上重跑无任何痕迹）。

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/plan"
	"github.com/ikaqiu-Lemon/EverGreen/internal/reconcile"
)

// ReviewedRepairOp 是本桥使用的 op 名：**既有** `mark_reviewed`（A-33，不新增 op）。
const ReviewedRepairOp = plan.OpMarkReviewed

// ReviewedRepairVerb 是本桥合成 plan 的 verb：`reconcile`（A-30 的同一 verb，
// `KnownVerbs` 恒 8）。它只用于校验链的 verb 归属，本桥不发提交。
const ReviewedRepairVerb = string(model.VerbReconcile)

// 两个结构性错误（不是数据问题，而是「调用方给的意向不合封闭形态」）：
// 一律**整批拒绝、零写入**，绝不「挑能写的先写一半」。
var (
	// ErrReviewedKeys 表示某份修复意向的待写键不是**恰**那一个封闭键。
	ErrReviewedKeys = errors.New("R2 修复意向的待写键集合不封闭")
	// ErrReviewedMismatch 表示修复意向与命中对象两侧对不上（同一次检查的两个产物必须同源）。
	ErrReviewedMismatch = errors.New("R2 修复意向与命中对象不同源")
)

// ReviewedRepairInput 是一次 R2 修复的全部输入（**只读**，本函数不改入参）。
type ReviewedRepairInput struct {
	// VaultRoot 是 vault 根（绝对路径）。
	VaultRoot string
	// Targets 是检查侧判定命中的对象（含对象 ID 与落盘路径），顺序即检查侧的可复算序。
	Targets []reconcile.ReviewedTarget
	// Repairs 是同一次检查产出的修复意向（本桥只消费 `reviewed_at_missing` 那些条目；
	// 其余 check 的意向原样忽略，属别的 R 项，越界处理才是错）。
	Repairs []reconcile.RepairSpec
	// Findings 是同一次检查产出的 findings（可空）：跳过发生时按 check + 路径给对应条目
	// 追加「已跳过」注记，**不删条目、不降级**。
	Findings []reconcile.Finding
	// UserRequest 是**进程边界**注入的授权佐证（A-34：R2 走 P-U 列）。
	// 它不来自 plan 内容自证，也不由本文件默认置真。
	UserRequest bool
	// Base 是**对账观测时刻**的 id / 路径 → content_hash 映射（可空）。
	//
	// 为什么允许调用方带进来：B3 问的是「文件自上次读取以来变过吗」，而对账的「上次读取」
	// 就是检查阶段那一次扫描。检查与修复之间用户又改了同一个文件 → 观测到的 hash 与写前
	// 重算不一致 → 该文件进 `skipped[]`、本次不写（补齐留给下次对账）。**空 = 按
	// `eg context` 的同一口径现算**（apply.go 的 planBase，唯一的 content_hash 实现）。
	Base map[string]string
	// Reason / RequirementIDs 进 plan 顶层（本桥不提交，故它们只影响诊断与回执）。
	Reason         string
	RequirementIDs []string
}

// ReviewedRepairSkip 是一条「本文件本次未写入」的如实交代（形状照计划层的 skipped[]）。
type ReviewedRepairSkip struct {
	OpIndex int    `json:"op_index"`
	Kind    string `json:"kind"`
	Target  string `json:"target"`
	Path    string `json:"path"`
	Cause   string `json:"cause"`
	Detail  string `json:"detail"`
}

// ReviewedRepairResult 是一次 R2 修复的全部事实（写了什么、跳过了什么、为什么）。
type ReviewedRepairResult struct {
	// Ran 恒为 true（本桥被调用过就算跑过，哪怕零命中）。
	Ran bool `json:"ran"`
	// Ops 是本次合成的 op 条数（= 命中对象数；零命中时为 0 且不组装 plan）。
	Ops int `json:"ops"`
	// Keys 是本次**意图**写入的 frontmatter 键集合：恒 `{reviewed_at}` 一个键。
	Keys []string `json:"keys"`
	// Stamp 是补写进 frontmatter 的时刻（RFC3339，与 `eg mark-reviewed` 同一口径）。
	Stamp string `json:"stamp"`
	// Written 是实际被写盘的 vault 相对路径（升序去重），供纳管写口区分「本次由 eg 写入」
	// 与「用户既有改动」（A-35 的 OurWrites 口径）。
	Written []string `json:"written"`
	// Reviewed 是过目信号被补齐的对象 ID（升序去重）。
	Reviewed []string `json:"reviewed"`
	// Skipped 是 B3 前置比对未通过等原因下**未写入**的条目（一条都不折叠）。
	Skipped []ReviewedRepairSkip `json:"skipped"`
	// Findings 是（必要时）追加过「已跳过」注记的 findings 副本；入参为空时此项为空。
	Findings []reconcile.Finding `json:"findings"`
	// Warnings / Failures 是计划层与落盘层如实上报的诊断文本（不吞、不折叠）。
	Warnings []string `json:"warnings"`
	Failures []string `json:"failures"`
	// Commits 恒 0：提交归纳管写口的恰一次 commit（A-35）。
	Commits int `json:"commits"`
}

// RepairReviewed 执行一次 R2 修复：把命中对象翻成 `mark_reviewed` op 并落盘（不提交）。
//
// 返回的 error 只表示**结构性拒绝**（意向不封闭 / 两侧不同源 / 校验链判 error）：
// 这些情形一律零写入。数据面的「未写入」不用 error 表达，而是如实落在 Skipped 里。
//
// 本函数只是薄壳：注入默认执行器 executePlanNoCommit（各自 store.New(root) 的实盘写口，
// 行为逐字不变）。事务底座（C5）会另有入口注入绑定同一把 atomic Store 的执行器。
func (r *Root) RepairReviewed(in ReviewedRepairInput) (ReviewedRepairResult, error) {
	return r.repairReviewedWithExecutor(in, executePlanNoCommit)
}

// repairReviewedWithExecutor 是 RepairReviewed 的实体：把「无提交校验 + 执行」入口作为参数
// exec 注入，其余逻辑与原实现逐字相同。默认注入 executePlanNoCommit；对账事务底座可注入一个
// 绑定同一把 atomic Store 的执行器，让本桥的写只 stage 到编排 overlay。
func (r *Root) repairReviewedWithExecutor(in ReviewedRepairInput,
	exec planNoCommitExecutor) (ReviewedRepairResult, error) {
	out := ReviewedRepairResult{Ran: true, Keys: reconcile.ReviewedKeys(),
		Findings: annotateNone(in.Findings)}
	root := strings.TrimSpace(in.VaultRoot)
	if root == "" {
		return out, &UsageError{Msg: "R2 修复缺 vault 根路径"}
	}

	// ① 意向侧：只取本 check 的意向，并逐条比对待写键是否**恰**那一个封闭键。
	reasons, err := reviewedReasons(in.Repairs)
	if err != nil {
		return out, err
	}
	// ② 对象侧：范围面再挡一次（提案面与收件区恒不进写入面），并与意向侧比对同源。
	targets, err := reviewedRepairTargets(in.Targets, reasons)
	if err != nil {
		return out, err
	}
	out.Ops = len(targets)
	// 幂等：零命中 → 连 plan 都不组装，零写入零提交（干净库上重跑无痕迹）。
	if out.Ops == 0 {
		return out, nil
	}

	// ③ 组装内存 ChangePlan：ops 逐个 `mark_reviewed`，base 按唯一的 content_hash 口径算。
	stamp := model.NewStamp(r.now())
	out.Stamp = stamp.String()
	ids := make([]string, 0, len(targets))
	for _, t := range targets {
		ids = append(ids, t.ID)
	}
	base, err := reviewedRepairBase(root, ids, in.Base)
	if err != nil {
		return out, err
	}
	p := reviewedRepairPlan(targets, reasons, base, in)

	// ④ 走与 runPlan 逐字相同的校验 + 执行链，但**不提交**（A-35）。
	pres, ex, err := exec(root, in.UserRequest, stamp, p)
	if err != nil {
		return out, err
	}
	for _, d := range pres.Warnings {
		out.Warnings = append(out.Warnings, d.Message)
	}
	if pres.Failed() {
		for _, d := range pres.Errors {
			out.Failures = append(out.Failures, d.Message)
		}
		// 校验失败 = 零写入、无 commit（与 `eg apply` 的 ① 分支同一口径）。
		return out, fmt.Errorf("R2 修复的 ChangePlan 校验失败（%d 条 error）：零写入、无 commit",
			len(pres.Errors))
	}

	// ⑤ 如实汇总：写了什么、跳过了什么、为什么跳过。
	out.Written = sortUniq(ex.Written)
	out.Reviewed = sortUniq(ex.Reviewed)
	for _, s := range ex.Skipped {
		out.Skipped = append(out.Skipped, ReviewedRepairSkip{
			OpIndex: s.OpIndex, Kind: string(s.Kind), Target: s.Target,
			Path: s.Locator, Cause: s.Cause, Detail: s.Detail,
		})
	}
	for _, d := range ex.Warnings {
		out.Warnings = append(out.Warnings, d.Message)
	}
	for _, d := range ex.Failures {
		out.Failures = append(out.Failures, d.Message)
	}
	// 跳过的对象：同一条 finding **仍在**，只追加注记（B3 不豁免的可见形态）。
	out.Findings = annotateSkipped(in.Findings, targets, out.Skipped)
	return out, nil
}

// reviewedRepairBase 决定进 plan 的 base：调用方给了对账观测值就**照用**，
// 否则按 `eg context` 的同一口径现算（apply.go 的 planBase，唯一的 content_hash 实现）。
//
// 无论走哪一支，B3 都不豁免：写前重算与 base 不一致 → 该文件进 `skipped[]`、本次不写。
func reviewedRepairBase(root string, ids []string, observed map[string]string) (
	map[string]string, error) {
	if len(observed) > 0 {
		out := make(map[string]string, len(observed))
		for k, v := range observed {
			out[k] = v
		}
		return out, nil
	}
	base, _, err := planBase(root, ids)
	return base, err
}

// reviewedReasons 取出本 check 的修复意向，返回 path → reason 映射。
//
// 逐条封闭比对：keys 不是**恰**那一个键即整批拒绝（宁可一个不写，也不写第二个键）。
func reviewedReasons(repairs []reconcile.RepairSpec) (map[string]string, error) {
	want := reconcile.ReviewedKeys()
	out := make(map[string]string, len(repairs))
	for _, sp := range repairs {
		if sp.Check != reconcile.CheckReviewedAtMissing {
			continue // 别的 R 项的意向：不是本桥的职责，原样不碰。
		}
		if len(sp.Keys) != len(want) {
			return nil, fmt.Errorf("%w：%s 的待写键 = %v，应恰 %v",
				ErrReviewedKeys, sp.Path, sp.Keys, want)
		}
		for i := range want {
			if sp.Keys[i] != want[i] {
				return nil, fmt.Errorf("%w：%s 的待写键 = %v，应恰 %v",
					ErrReviewedKeys, sp.Path, sp.Keys, want)
			}
		}
		out[sp.Path] = sp.Reason
	}
	return out, nil
}

// reviewedRepairTargets 过滤 + 校验命中对象：范围面再挡一次、两侧同源、路径与 ID 非空。
func reviewedRepairTargets(targets []reconcile.ReviewedTarget,
	reasons map[string]string) ([]reconcile.ReviewedTarget, error) {
	out := make([]reconcile.ReviewedTarget, 0, len(targets))
	seen := make(map[string]bool, len(targets))
	for _, t := range targets {
		id, path := strings.TrimSpace(t.ID), strings.TrimSpace(t.Path)
		if id == "" || path == "" {
			return nil, fmt.Errorf("%w：命中对象缺 ID 或路径（%+v）", ErrReviewedMismatch, t)
		}
		if !reconcile.InReviewedScope(path) {
			return nil, fmt.Errorf("%w：%s 不在对账域内，不得进入写入面", ErrReviewedMismatch, path)
		}
		if _, ok := reasons[path]; !ok {
			return nil, fmt.Errorf("%w：%s 有命中对象但没有对应的修复意向", ErrReviewedMismatch, path)
		}
		if seen[id] {
			continue // 同一对象只补一次（同一次对账里不重复写同一个键）。
		}
		seen[id] = true
		t.ID, t.Path = id, path
		out = append(out, t)
	}
	if len(out) != len(reasons) {
		return nil, fmt.Errorf("%w：命中对象 %d 个、修复意向 %d 条（同一次检查的两个产物必须同源）",
			ErrReviewedMismatch, len(out), len(reasons))
	}
	return out, nil
}

// reviewedRepairPlan 组装那份内存 ChangePlan（顶层键齐备；domain 取 null = 全局操作）。
//
// 为什么 domain 是 null：一次对账跨领域，写入面由每个对象自己的目录决定（EG-DOM-01）；
// 给 plan 硬塞一个领域会让**别的**领域的对象被判成「落在 plan.domain 之外」而产生噪声
// 警告 —— 合同允许 `domain: null` 表达全局操作，这里正是那个形态。
//
// 每条 op 恰四格：`op` / `target` / `initiator` / （reason 只进 plan 顶层与诊断）——
// `mark_reviewed` 的字段表里没有承载状态维度、删除维度、替代指针维度或正文的字段，
// 所以「顺手改别的键」在**形态上**就不可表达。
func reviewedRepairPlan(targets []reconcile.ReviewedTarget, reasons map[string]string,
	base map[string]string, in ReviewedRepairInput) *plan.ChangePlan {
	ops := make([]*plan.Op, 0, len(targets))
	for i, t := range targets {
		ops = append(ops, &plan.Op{
			Index: i, Name: ReviewedRepairOp, Target: t.ID,
			// A-34：合成 plan 逐字带 initiator=user（另一半授权佐证由进程边界注入）。
			Initiator: plan.InitiatorUser, InitiatorGiven: true,
			Reason: reasons[t.Path], ReasonGiven: reasons[t.Path] != "",
		})
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		reason = fmt.Sprintf("对账 R2：为 %d 个经用户直接编辑的对象补齐过目信号单键", len(targets))
	}
	return &plan.ChangePlan{
		Version: plan.PlanVersion, VersionRaw: plan.PlanVersion,
		Verb: ReviewedRepairVerb, VerbGiven: true,
		DomainGiven: true, DomainNull: true,
		Reason: reason, RequirementIDs: in.RequirementIDs,
		Base: base, Ops: ops, Extra: map[string]interface{}{},
	}
}

// annotateNone 返回 findings 的浅副本（不改入参；nil → nil）。
func annotateNone(fs []reconcile.Finding) []reconcile.Finding {
	if len(fs) == 0 {
		return nil
	}
	out := make([]reconcile.Finding, len(fs))
	copy(out, fs)
	return out
}

// annotateSkipped 给「本次被跳过」的对象所对应的 finding 追加注记（条目不删、分级不降）。
//
// 匹配口径：跳过项里的路径 / 对象 ID 命中该条 finding 的 targets 即算同一条事实。
func annotateSkipped(fs []reconcile.Finding, targets []reconcile.ReviewedTarget,
	skipped []ReviewedRepairSkip) []reconcile.Finding {
	out := annotateNone(fs)
	if len(out) == 0 || len(skipped) == 0 {
		return out
	}
	hit := make(map[string]string, len(skipped)*2)
	for _, s := range skipped {
		for _, key := range []string{strings.TrimSpace(s.Path), strings.TrimSpace(s.Target)} {
			if key != "" {
				hit[key] = s.Cause
			}
		}
	}
	// 命中对象的 ID 与路径互为别名：跳过项只带其中一个也要认出同一条 finding。
	for _, t := range targets {
		if c, ok := hit[t.Path]; ok {
			hit[t.ID] = c
		}
		if c, ok := hit[t.ID]; ok {
			hit[t.Path] = c
		}
	}
	for i, f := range out {
		if f.Check != reconcile.CheckReviewedAtMissing {
			continue
		}
		for _, tg := range f.Targets {
			cause, ok := hit[tg]
			if !ok {
				continue
			}
			got, err := reconcile.WithSkipNotice(f, cause)
			if err != nil {
				break // 构造失败就保留原条目：宁可少一行注记，也不丢一条 finding。
			}
			out[i] = got
			break
		}
	}
	return out
}

// sortUniq 去重 + 升序（回执可逐字复算）。
func sortUniq(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		v := strings.TrimSpace(s)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
