package cli

// R6 修复桥：把对账产出的**失准标记意向**翻成一份内存 ChangePlan，交计划层的完整校验链落盘
// （对账合同 §9 / §1.3 写口归属表第 3 行；A-33 / A-34 / A-35；M4 · T-…-055 阶段 3）。
//
// 本文件是「R6 只读判定」与「综述失准标记落盘」之间的**唯一**通路，五条纪律逐条对应硬约束：
//
//  1. **两键封闭**：待写键集合恒 `{stale, stale_reason}` —— 键名来自检查侧的封闭表
//     （真源在 internal/model，本文件零键名字面量），且组装前**逐条比对**每份意向的 keys，
//     不是「恰那两个键」就整批拒绝（ErrStaleKeys），绝不「挑能写的先写一半」。
//  2. **不绕开 ChangePlan**：本文件**不 import 落盘层**（判据：那个包路径字面量在本文件
//     零命中），也不直呼任何 setter；写盘只可能发生在 `plan.Execute` 之后。base 由
//     apply.go 的 planBase 按 `eg context` 的同一口径算（唯一的 content_hash 实现）。
//  3. **不需要 `--user-request`，但仍走 ChangePlan**（A-34）：合成 plan 逐字**不带**
//     `initiator`（`set_stale` 的字段表恰 `target` / `reason` 两格，形态上写不进去），
//     执行时也不给命令行佐证 —— 因此这条 op 恒走 **P-A**，由写权限矩阵 #33 的 P-A 格放行。
//     矩阵**不新增行**：能不能写这一格仍由矩阵说话，本文件不设任何豁免。
//  4. **恰一次 commit**（A-35）：本文件**自己不提交**（Commits 恒 0）。一次对账里 R1 纳管与
//     R2 / R6 的修复写入合并为**恰一次** commit，那一次由纳管写口在编排末尾产生 ——
//     所以这里刻意不复用会自带一次提交的 runPlan，而走 apply.go 的无提交入口
//     executePlanNoCommit（与 runPlan 逐字同一条校验 + 执行链，一格不放宽）。
//  5. **幂等**：落盘上已是同一标记且理由逐字相同（检查侧的 AlreadyMarked）→ 该篇综述
//     **不进** op 集合：零写入、零 commit，**finding 仍在**（回执里如实登记进 Idempotent）。
//     全部命中都幂等时**连 plan 都不组装**（干净库上重跑无任何痕迹）。
//
// B3 不豁免：ExpectedHash 经 base 进 action，写前重算不一致 → 该文件进 `skipped[]`
// （封闭两值之一，`kind=file_changed`），本文件**不写它**、也不重试、不自造第三种 kind；
// 对应的 finding 仍产出，并由检查侧定稿的 WithSkipNotice 在同一条 detail 里如实注明已跳过。
//
// 本文件**不做**的事（合同 §9 两条「不自动」）：不重算综述（op 载荷里没有正文那一格，
// 综述的字节除那两个键之外一律不动）、不清除失准标记（`set_stale` 没有反向形态）；
// 状态维度、删除维度、替代指针维度与过目维度一格不写（那些字段在本 op 的字段表里不存在）。

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/plan"
	"github.com/ikaqiu-Lemon/EverGreen/internal/reconcile"
)

// StaleRepairOp 是本桥使用的 op 名：M4 新增的 `set_stale`（A-33，真源在 internal/plan）。
const StaleRepairOp = plan.OpSetStale

// StaleRepairVerb 是本桥合成 plan 的 verb：`reconcile`（A-30 的同一 verb，
// `KnownVerbs` 恒 8）。它只用于校验链的 verb 归属，本桥不发提交。
const StaleRepairVerb = string(model.VerbReconcile)

// 三个结构性错误（不是数据问题，而是「调用方给的意向不合封闭形态」）：
// 一律**整批拒绝、零写入**。
var (
	// ErrStaleKeys 表示某份修复意向的待写键不是**恰**那两个封闭键。
	ErrStaleKeys = errors.New("R6 修复意向的待写键集合不封闭")
	// ErrStaleMismatch 表示修复意向与命中对象两侧对不上（同一次检查的两个产物必须同源）。
	ErrStaleMismatch = errors.New("R6 修复意向与命中对象不同源")
	// ErrStaleReasonNotClosed 表示失准理由不在封闭三值内（本层绝不代入默认理由）。
	ErrStaleReasonNotClosed = errors.New("R6 失准理由不在封闭三值内")
)

// StaleRepairInput 是一次 R6 修复的全部输入（**只读**，本函数不改入参）。
type StaleRepairInput struct {
	// VaultRoot 是 vault 根（绝对路径）。
	VaultRoot string
	// Targets 是检查侧判定命中的综述（含综述 ID、落盘路径与那个封闭三值），
	// 顺序即检查侧的可复算序。
	Targets []reconcile.RecapTarget
	// Repairs 是同一次检查产出的修复意向（本桥只消费 `recap_stale` 那些条目；
	// 其余 check 的意向原样忽略，属别的 R 项，越界处理才是错）。
	Repairs []reconcile.RepairSpec
	// Findings 是同一次检查产出的 findings（可空）：跳过发生时按 check + 路径给对应条目
	// 追加「已跳过」注记，**不删条目、不降级**。
	Findings []reconcile.Finding
	// Base 是**对账观测时刻**的 id / 路径 → content_hash 映射（可空）。
	//
	// 为什么允许调用方带进来：B3 问的是「文件自上次读取以来变过吗」，而对账的「上次读取」
	// 就是检查阶段那一次扫描。检查与修复之间用户又改了同一个文件 → 观测到的 hash 与写前
	// 重算不一致 → 该文件进 `skipped[]`、本次不写（标记留给下次对账）。**空 = 按
	// `eg context` 的同一口径现算**（apply.go 的 planBase，唯一的 content_hash 实现）。
	Base map[string]string
	// Reason / RequirementIDs 进 plan 顶层（本桥不提交，故它们只影响诊断与回执）。
	Reason         string
	RequirementIDs []string
}

// StaleRepairSkip 是一条「本文件本次未写入」的如实交代（形状照计划层的 skipped[]）。
type StaleRepairSkip struct {
	OpIndex int    `json:"op_index"`
	Kind    string `json:"kind"`
	Target  string `json:"target"`
	Path    string `json:"path"`
	Cause   string `json:"cause"`
	Detail  string `json:"detail"`
}

// StaleRepairResult 是一次 R6 修复的全部事实（写了什么、跳过了什么、为什么）。
type StaleRepairResult struct {
	// Ran 恒为 true（本桥被调用过就算跑过，哪怕零命中）。
	Ran bool `json:"ran"`
	// Ops 是本次合成的 op 条数（= 需要写入的综述数；幂等与零命中时为 0 且不组装 plan）。
	Ops int `json:"ops"`
	// Keys 是本次**意图**写入的 frontmatter 键集合：恒那两个综述专属键。
	Keys []string `json:"keys"`
	// Written 是实际被写盘的 vault 相对路径（升序去重），供纳管写口区分「本次由 eg 写入」
	// 与「用户既有改动」（A-35 的 OurWrites 口径）。
	Written []string `json:"written"`
	// Staled 是失准标记落盘成功的综述 ID（升序去重）。
	Staled []string `json:"staled"`
	// Idempotent 是落盘上**已是**同一标记与同一理由、故本次零写入的综述 ID（升序去重）。
	// 它们的 finding **仍在**（合同 §9 幂等条）。
	Idempotent []string `json:"idempotent"`
	// Reasons 是逐篇综述的失准理由（综述 ID → 封闭三值之一），供报告侧机读，
	// 不必解析任何 detail 文本。
	Reasons map[string]string `json:"reasons"`
	// Skipped 是 B3 前置比对未通过等原因下**未写入**的条目（一条都不折叠）。
	Skipped []StaleRepairSkip `json:"skipped"`
	// Findings 是（必要时）追加过「已跳过」注记的 findings 副本；入参为空时此项为空。
	Findings []reconcile.Finding `json:"findings"`
	// Warnings / Failures 是计划层与落盘层如实上报的诊断文本（不吞、不折叠）。
	Warnings []string `json:"warnings"`
	Failures []string `json:"failures"`
	// Commits 恒 0：提交归纳管写口的恰一次 commit（A-35）。
	Commits int `json:"commits"`
}

// RepairRecapStale 执行一次 R6 修复：把命中综述翻成 `set_stale` op 并落盘（不提交）。
//
// 返回的 error 只表示**结构性拒绝**（意向不封闭 / 两侧不同源 / 理由越界 / 校验链判 error）：
// 这些情形一律零写入。数据面的「未写入」不用 error 表达，而是如实落在 Skipped / Idempotent 里。
//
// 本函数只是薄壳：注入默认执行器 executePlanNoCommit（各自 store.New(root) 的实盘写口，
// 行为逐字不变）。事务底座（C5）会另有入口注入绑定同一把 atomic Store 的执行器。
func (r *Root) RepairRecapStale(in StaleRepairInput) (StaleRepairResult, error) {
	return r.repairRecapStaleWithExecutor(in, executePlanNoCommit)
}

// repairRecapStaleWithExecutor 是 RepairRecapStale 的实体：把「无提交校验 + 执行」入口作为
// 参数 exec 注入，其余逻辑与原实现逐字相同。默认注入 executePlanNoCommit；对账事务底座可注入
// 一个绑定同一把 atomic Store 的执行器，让本桥的写只 stage 到编排 overlay。
func (r *Root) repairRecapStaleWithExecutor(in StaleRepairInput,
	exec planNoCommitExecutor) (StaleRepairResult, error) {
	out := StaleRepairResult{Ran: true, Keys: reconcile.RecapStaleKeys(),
		Findings: annotateNone(in.Findings), Reasons: map[string]string{}}
	root := strings.TrimSpace(in.VaultRoot)
	if root == "" {
		return out, &UsageError{Msg: "R6 修复缺 vault 根路径"}
	}

	// ① 意向侧：只取本 check 的意向，并逐条比对待写键是否**恰**那两个封闭键。
	notes, err := recapStaleNotes(in.Repairs)
	if err != nil {
		return out, err
	}
	// ② 对象侧：综述专属再挡一次（`r-` 之外的对象类恒不进本写入面）、理由取值封闭、
	//    与意向侧比对同源；同时把「已是同一标记与同一理由」的幂等对象分流出去。
	pending, idem, err := recapRepairTargets(in.Targets, notes)
	if err != nil {
		return out, err
	}
	for _, t := range append(append([]reconcile.RecapTarget{}, pending...), idem...) {
		out.Reasons[t.ID] = string(t.Reason)
	}
	out.Idempotent = sortUniq(recapIDs(idem))
	out.Ops = len(pending)
	// 幂等 / 零命中：连 plan 都不组装，零写入零提交（finding 仍由入参原样带回）。
	if out.Ops == 0 {
		return out, nil
	}

	// ③ 组装内存 ChangePlan：ops 逐个 `set_stale`，base 按唯一的 content_hash 口径算。
	base, err := recapRepairBase(root, recapIDs(pending), in.Base)
	if err != nil {
		return out, err
	}
	p := recapStalePlan(pending, base, in)

	// ④ 走与 runPlan 逐字相同的校验 + 执行链，但**不提交**（A-35）；
	//    命令行佐证逐字给 false —— A-34：R6 写这两键不需要 `--user-request`，走 P-A。
	pres, ex, err := exec(root, false, model.NewStamp(r.now()), p)
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
		return out, fmt.Errorf("R6 修复的 ChangePlan 校验失败（%d 条 error）：零写入、无 commit",
			len(pres.Errors))
	}

	// ⑤ 如实汇总：写了什么、跳过了什么、为什么跳过。
	out.Written = sortUniq(ex.Written)
	out.Staled = sortUniq(ex.RecapsStaled)
	for _, s := range ex.Skipped {
		out.Skipped = append(out.Skipped, StaleRepairSkip{
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
	// 跳过的综述：同一条 finding **仍在**，只追加注记（B3 不豁免的可见形态）。
	out.Findings = annotateRecapSkipped(in.Findings, pending, out.Skipped)
	return out, nil
}

// recapRepairBase 决定进 plan 的 base：调用方给了对账观测值就**照用**，
// 否则按 `eg context` 的同一口径现算（apply.go 的 planBase，唯一的 content_hash 实现）。
//
// 无论走哪一支，B3 都不豁免：写前重算与 base 不一致 → 该文件进 `skipped[]`、本次不写。
func recapRepairBase(root string, ids []string, observed map[string]string) (
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

// recapStaleNotes 取出本 check 的修复意向，返回 path → 人类可读原因映射。
//
// 逐条封闭比对：keys 不是**恰**那两个键即整批拒绝（宁可一篇不标，也不写第三个键）。
// 注意这里取的是意向的**人类可读原因**（进 plan 顶层与诊断）；落盘用的那个封闭三值
// 恒取命中对象的机读字段，绝不从这段文本里解析（可复算性的落点）。
func recapStaleNotes(repairs []reconcile.RepairSpec) (map[string]string, error) {
	want := reconcile.RecapStaleKeys()
	out := make(map[string]string, len(repairs))
	for _, sp := range repairs {
		if sp.Check != reconcile.CheckRecapStale {
			continue // 别的 R 项的意向：不是本桥的职责，原样不碰。
		}
		if len(sp.Keys) != len(want) || len(want) != reconcile.RecapStaleKeyCount {
			return nil, fmt.Errorf("%w：%s 的待写键 = %v，应恰 %v",
				ErrStaleKeys, sp.Path, sp.Keys, want)
		}
		for i := range want {
			if sp.Keys[i] != want[i] {
				return nil, fmt.Errorf("%w：%s 的待写键 = %v，应恰 %v",
					ErrStaleKeys, sp.Path, sp.Keys, want)
			}
		}
		out[sp.Path] = sp.Reason
	}
	return out, nil
}

// recapRepairTargets 过滤 + 校验命中综述，并把幂等对象分流。
//
// 四条准入（任一不成立即**整批拒绝、零写入**）：
//  1. ID 与路径非空（无法定位就不进写入面）；
//  2. ID 是**主题综述**（`r-`）：两键是综述专属，卡 / 笔记 / 原文 / 提案一律拒绝 ——
//     计划层的矩阵 #33 还会再挡一次，这里是**第一道**、也是最早的那一道；
//  3. 失准理由落在封闭三值内（本层绝不代入默认理由）；
//  4. 两侧同源：每个命中对象都要有对应的修复意向，且两侧条数相等。
//
// 返回值：pending = 需要写入的综述；idem = 落盘上已是同一标记与同一理由的综述（零写入）。
func recapRepairTargets(targets []reconcile.RecapTarget, notes map[string]string) (
	pending, idem []reconcile.RecapTarget, err error) {
	seen := make(map[string]bool, len(targets))
	for _, t := range targets {
		id, path := strings.TrimSpace(t.ID), strings.TrimSpace(t.Path)
		if id == "" || path == "" {
			return nil, nil, fmt.Errorf("%w：命中综述缺 ID 或路径（%+v）", ErrStaleMismatch, t)
		}
		parsed, perr := model.ParseID(id)
		if perr != nil || parsed.Prefix != model.PrefixReview {
			return nil, nil, fmt.Errorf(
				"%w：%s 不是主题综述 ID（%s…），失准标记两键是综述专属，"+
					"不得落在知识卡 / 材料笔记 / 原文 / 提案上",
				ErrStaleMismatch, id, model.PrefixReview)
		}
		if _, ok := model.ParseStaleReason(string(t.Reason)); ok != nil {
			return nil, nil, fmt.Errorf("%w：%s 的理由 = %q，合法取值恰 %v",
				ErrStaleReasonNotClosed, id, string(t.Reason), model.ValidStaleReasons())
		}
		if _, ok := notes[path]; !ok {
			return nil, nil, fmt.Errorf("%w：%s 有命中综述但没有对应的修复意向",
				ErrStaleMismatch, path)
		}
		if seen[id] {
			continue // 同一篇综述只标一次（同一次对账里不重复写同一份文件）
		}
		seen[id] = true
		t.ID, t.Path = id, path
		if t.AlreadyMarked {
			// 幂等（合同 §9）：零写入、零 commit —— 但 finding 仍产出，故如实登记。
			idem = append(idem, t)
			continue
		}
		pending = append(pending, t)
	}
	if len(seen) != len(notes) {
		return nil, nil, fmt.Errorf(
			"%w：命中综述 %d 篇、修复意向 %d 条（同一次检查的两个产物必须同源）",
			ErrStaleMismatch, len(seen), len(notes))
	}
	return pending, idem, nil
}

// recapIDs 取一批命中综述的 ID（顺序即入参序）。
func recapIDs(targets []reconcile.RecapTarget) []string {
	out := make([]string, 0, len(targets))
	for _, t := range targets {
		out = append(out, t.ID)
	}
	return out
}

// recapStalePlan 组装那份内存 ChangePlan（顶层键齐备；domain 取 null = 全局操作）。
//
// 为什么 domain 是 null：一次对账跨领域，写入面由每篇综述自己的目录决定（EG-DOM-01）；
// 给 plan 硬塞一个领域会让**别的**领域的对象被判成「落在 plan.domain 之外」而产生噪声
// 警告 —— 合同允许 `domain: null` 表达全局操作，这里正是那个形态。
//
// 每条 op 恰两格：`target`（综述 ID）+ `reason`（封闭三值之一，机读取值直接来自检查侧的
// RecapTarget.Reason，**不解析**任何 detail 文本）。`set_stale` 的字段表里没有承载正文、
// 状态维度、删除维度、替代指针维度或过目维度的字段，所以「顺手改别的键」在**形态上**
// 就不可表达；也**没有**反向形态可以清除标记（合同 §9 两条「不自动」的结构性落点）。
//
// 逐字**不带** `initiator`：A-34 明确 R6 写这两键不需要 `--user-request`，
// 于是这条 op 恒走 P-A，由矩阵 #33 的 P-A 格放行（矩阵不新增行、也不设豁免）。
func recapStalePlan(targets []reconcile.RecapTarget, base map[string]string,
	in StaleRepairInput) *plan.ChangePlan {
	ops := make([]*plan.Op, 0, len(targets))
	for i, t := range targets {
		ops = append(ops, &plan.Op{
			Index: i, Name: StaleRepairOp, Target: t.ID,
			Reason: string(t.Reason), ReasonGiven: true,
		})
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		reason = fmt.Sprintf("对账 R6：为 %d 篇引用卡已变化的主题综述写失准标记两键", len(targets))
	}
	return &plan.ChangePlan{
		Version: plan.PlanVersion, VersionRaw: plan.PlanVersion,
		Verb: StaleRepairVerb, VerbGiven: true,
		DomainGiven: true, DomainNull: true,
		Reason: reason, RequirementIDs: in.RequirementIDs,
		Base: base, Ops: ops, Extra: map[string]interface{}{},
	}
}

// annotateRecapSkipped 给「本次被跳过」的综述所对应的 finding 追加注记
// （条目不删、分级不降；文案复用检查侧定稿的那一份，不另造第二套）。
//
// 匹配口径：跳过项里的路径 / 对象 ID 命中该条 finding 的 targets 即算同一条事实。
func annotateRecapSkipped(fs []reconcile.Finding, targets []reconcile.RecapTarget,
	skipped []StaleRepairSkip) []reconcile.Finding {
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
	// 命中综述的 ID 与路径互为别名：跳过项只带其中一个也要认出同一条 finding。
	for _, t := range targets {
		if c, ok := hit[t.Path]; ok {
			hit[t.ID] = c
		}
		if c, ok := hit[t.ID]; ok {
			hit[t.Path] = c
		}
	}
	for i, f := range out {
		if f.Check != reconcile.CheckRecapStale {
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
