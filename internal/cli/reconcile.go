package cli

// [S3] internal/cli/reconcile.go：`eg reconcile` 命令本体的**唯一**落点
// （对账合同 §12 全文 + §0.1 硬边界 + §11 报告三键；M4 · T-evergreen.s1_main_flow-158614-058）。
//
// # 唯一职责（一句话）
//
// 把**已经在盘**的构件串成一次全库对账：六格只读采样 → `reconcile.Run`（R1–R7 检查）→
// R2 / R6 修复桥落盘 → 纳管写口的**恰一次** commit → 五键 envelope + 报告 `reconcile` 三键 +
// 五档退出码。本文件**不新增任何检查逻辑、不新增任何写口、不新增任何错误类型**：
// 检查在 `internal/reconcile`，R2 / R6 的写入在 `reconcile_repair_reviewed.go` /
// `reconcile_repair_stale.go`，commit 在 `reconcile_commit.go`，人类可读渲染在
// `reconcile_render.go`，报告字段在 `internal/report/reconcile.go`。
//
// # 边界（逐条不得越线）
//
//   - **不实现 `eg check`**（属 T-…-059）：本文件只注册 `reconcile` 一条命令，
//     命令数因此恰 18 → 19（`check` 那一条留给 059，最终 20）。
//   - **不新增 R1–R7 任何检查逻辑**（属 T-…-050 ~ 056）：`checkers` 注册表一格不动，
//     本文件只**取数**并调用 `reconcile.Run`。
//   - **不作为任何写命令的前置**（合同 §0.1 第 3 条）：`runReconcile` 只被 `root.go` 的
//     `wireImplemented` 挂到 `reconcile` 这一条命令上，其它命令的执行路径一字未改。
//   - **不引锁 / 索引 / 性能门槛**：本文件没有 `.index/`、没有 SQLite / FTS5、没有文件锁、
//     没有崩溃恢复、没有强原子事务（均属 S4 / S5 / M6）。对账走**全量 Markdown 扫描**。
//   - **退出码 5 / 6 不启用**：`5` 属 S5 的锁冲突语义、`6` 是「仅缺确认」（白名单恰
//     `proposal approve` 与 `delete` 两条，对账无确认前置）。本文件只用既有四类错误类型，
//     `exitcode.go` 一字不碰。
//   - **不产生第二条 commit、不改 `git add -A` 口径**：commit 的唯一发生地是纳管写口，
//     且写口自身以实例状态锁死「恰一次」。
//   - **不接受任何范围收窄参数**：全库对账，收窄属性能面（S4）。收窄参数**不声明** ——
//     标准库 `flag` 遇到未定义参数当场报错，`dispatch` 包成 `*UsageError` → 退 1、零写入，
//     因此本文件里那两个收窄参数名一次都不出现（Acceptance 的 grep 恒 0）。

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/plan"
	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
	"github.com/ikaqiu-Lemon/EverGreen/internal/reconcile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/report"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// ReconcileScopeNotice 陈述本命令的范围口径（全库、不收窄），措辞固定便于逐字断言。
const ReconcileScopeNotice = "本次对账覆盖整个 vault：不按对象、不按领域收窄（收窄属 S4 性能面，M4 不做）"

// ReconcileEditsUnsampledNotice 是**遗留项 G-058-01** 的逐字交代：
// `reconcile.Input.Edits`（R2 判定条件② 的证据②「已提交的外部编辑」）在本命令路径下
// **未采样**（传 nil）。未采样 ≠ 不存在，所以这句话必须出现在每次对账的输出里。
const ReconcileEditsUnsampledNotice = "遗留项 G-058-01：已提交的外部编辑（最近一次改动某文件的 commit 动词）" +
	"本次**未采样**（传 nil），R2 只用「未提交改动」这一条证据判定；未采样 ≠ 不存在，" +
	"因此本次对账不代表库内没有已提交的外部编辑"

// ReconcileReviewedNeedsUserRequestNotice 是 R2 补齐在**非用户显式路径**下的固定交代。
//
// 为什么会有这一支：`reviewed_at` 的写入由写权限矩阵 #7 / #22 管，两行的 P-A 格是 🔴 ——
// 没有命令行佐证时计划层必然判 error 并零写入（A-34 只解锁了 R6 的两键，没有解锁 R2）。
// 这不是「跳过检查」：对应的 `reviewed_at_missing` finding 照常产出、照常进报告，
// 只是**这一次没有补齐**。
const ReconcileReviewedNeedsUserRequestNotice = "R2 过目信号补齐本次未执行（零写入）：" +
	"写权限矩阵 #7 / #22 的 P-A 为 🔴，补齐只发生在用户显式发起的调用上（--user-request）；" +
	"对应 finding 一条不减、一级不降，补齐留给下次对账"

// reconcileCommand 注册 `eg reconcile`（合同 §12：写入=是、commit 恰 0 或 1 次、五档退出码）。
//
// 参数面**恰一个**命令私有 flag `--dry-run`（bool）：`--json` / `--vault` / `--user-request`
// 是既有全局 flag，由 `dispatch` 统一声明，本命令不重复定义、也不做成全局 flag。
func reconcileCommand() *Command {
	return &Command{
		Name:    "reconcile",
		Display: "reconcile",
		Summary: "全库对账：R1–R7 检查 + R2 / R6 补写 + 恰一次纳管 commit（verb=reconcile）",
		Owner:   "T-evergreen.s1_main_flow-158614-058",
		Usage: `eg reconcile [--dry-run] [--json]

参数：
  --dry-run   否；只跑检查与报告，零写入、零 commit（reconcile.ran=true、reconcile.commit=null）

全库口径：一次对账覆盖整个 vault，**不接受任何对象参数或范围收窄参数**（收窄属 S4 性能面）；
未声明的参数由参数解析当场判非法 → 退 1、零写入。
写入面：R2 的过目信号单键与 R6 的失准标记两键经内存 ChangePlan 落盘（B3 前置比对不豁免），
R1 只把工作区既有改动记进 Git 历史（一个字节都不改写）；全部写入合并为**恰一次** commit，
零改动即零 commit。
本命令**不作为任何写命令的前置**，不做交互式确认（不产生退出码 6），也不引入锁（锁属 M6）。
对账写入落盘后会顺带做一次**写后派生索引同步**（Markdown 恒为唯一权威）：索引未建则不替你建，
索引不可用 / 同步失败只报 W22 / W24 并跳过 —— 既不回滚已写入内容，也不改变本命令的退出码。
退出码：0 成功（含只有 warning 级 finding） | 1 参数非法（零写入） |
       2 存在 error 级 finding（先完成 R1 纳管 commit 再退 2） |
       3 有写入被 B3 跳过 | 4 Git 提交失败（磁盘保留现状，不做任何还原） |
       5 写前强校验失败（E15）/ run.lock 不可用（E16），两者均零写入
`,
		Flags: func(fs *flagSet) {
			fs.Bool("dry-run", false, "只跑检查与报告：零写入、零 commit")
		},
		// 全库对账不吃对象参数：多余位置参数一律退 1（*UsageError），零写入。
		Validate: noPositionalArgs,
	}
}

// reconcileFacts 是退出码判定的**全部**输入：纯计数事实，不含任何策略开关。
//
// 判定被刻意做成「事实结构体 + 纯函数」两段，使次序可以被用例逐字锁死
// （同时有 error finding 与 B3 跳过时恒 3 —— 见 reconcileExitCode 的注释）。
type reconcileFacts struct {
	// CommitFailed：纳管写口未能完成本次提交（Git 层失败）。
	CommitFailed bool
	// SkippedWrites：本次有写入被 B3 前置比对拦下（`skipped[kind=file_changed]`）的条数。
	SkippedWrites int
	// ErrorFindings：error 级 finding 条数（判据来源恒是检查包的 severity，本层不重判）。
	ErrorFindings int
	// RepairRejected：修复桥的**结构性拒绝**条数（意向不封闭 / 两侧不同源 / 校验链判 error）。
	// 它与 error 级 finding 同归退出码 2 的「校验失败」语义，不新增第六个码。
	RepairRejected int
}

// reconcileExitCode 是本命令退出码的**唯一**判定点，次序锁死（合同 §12 + 施工卡 §2.3 第 7 步）：
//
//	4（Git 提交失败） → 3（有写入被 B3 跳过） → 2（有 error 级 finding / 修复被结构性拒绝） → 0
//
// 为什么 3 优先于 2：`3` 表达「本次有事实没落盘」，是更强的行动信号；且合同把 `2` 的前置
// 写成「先完成 R1 纳管 commit 再退 2」，说明 `2` 从不阻断写入路径。
//
// `1`（参数非法）不在本函数内：它发生在更早的 flag 解析 / Validate 阶段（零写入），
// 由 `dispatch` 直接返回 `*UsageError`。`5` / `6` 本命令不使用。
func reconcileExitCode(f reconcileFacts) int {
	switch {
	case f.CommitFailed:
		return ExitCommitFailed
	case f.SkippedWrites > 0:
		return ExitPartialWrite
	case f.ErrorFindings > 0 || f.RepairRejected > 0:
		return ExitValidation
	default:
		return ExitOK
	}
}

// reconcileInputSample 是六格采样的回执：对账输入 + 逐格的诚实性诊断。
//
// **诚实性硬口径**：任何一格采不到就**退化成「未采样」**（nil）并留一条 warning，
// 绝不把「没采到」当成「不存在」——那会让检查器把缺失的事实当成「没有问题」。
type reconcileInputSample struct {
	In       reconcile.Input
	Warnings []report.Diagnostic
}

// sampleReconcileInput 采样对账输入的六格（施工卡 §2.2 的落点表逐格实现）。
//
// 六格与 nil 语义：
//   - `VaultRoot`：来自 `inv.VaultRoot`（框架层守卫已解析）；
//   - `Scan`：`query.VaultScan(root, {IncludeNotes: true})`，nil = 未扫描；
//   - `Status`：`repo.Status()`（`git status --porcelain` 只读采样）。它是**值类型**，
//     无法用 nil 表达「未采样」，因此采样失败时留一条 warning 逐字说明
//     「未采样 ≠ 工作区干净」，而不是让读者以为库是干净的；
//   - `Sources`：`store.ScanSources()` 投影成两字段事实，nil = 未采样（R4 两条源侧判定 + R7 不判）；
//   - `Edits`：**本命令传 nil**（遗留项 G-058-01，见 ReconcileEditsUnsampledNotice）；
//   - `Renames`：逐路径 `repo.FollowRenames(rel)`，nil = 未采样（R5 条件② 不判）；
//   - `Recaps`：`SampleRecaps(root).Recaps`，nil = 未采样（R6 整体不判）。
//
// 零副作用：全程只读（扫描 + `git status` / `git log` 的只读用法 + frontmatter 解析），
// 不写盘、不提交。
func (r *Root) sampleReconcileInput(root string) reconcileInputSample {
	out := reconcileInputSample{In: reconcile.Input{VaultRoot: root}}
	warn := func(path, msg string) {
		out.Warnings = append(out.Warnings, report.Diagnostic{
			Code: "", Level: report.LevelWarning, Path: path, OpIndex: report.NonOp, Message: msg})
	}

	// ① Scan：全量 Markdown 扫描底座（合同 §0.1 第 1 条：不引索引，一律全量扫描）。
	scan, serr := query.VaultScan(root, query.ScanOptions{IncludeNotes: true})
	if serr != nil {
		warn("domains/", "全库扫描未采样（nil）："+oneLineReason(serr.Error())+
			"；依赖扫描面的检查项本次整体不判 —— 未采样 ≠ 库里没有对象")
	} else {
		out.In.Scan = scan
	}

	repo := r.repo(root)
	// ② Status：Git 工作区只读快照（R1 的判定输入 + R2 证据①）。
	if st, err := repo.Status(); err != nil {
		warn("git.status", "Git 工作区状态未采样："+oneLineReason(err.Error())+
			"；R1 与 R2 证据① 本次不判 —— 未采样 ≠ 工作区干净")
	} else {
		out.In.Status = st
	}

	// ③ Sources：`sources/` 分区不在扫描底座的对象面内，由本层投影成两字段事实。
	if infos, err := store.New(root).ScanSources(); err != nil {
		warn(store.DirSources+"/", "原文分区未采样（nil）："+oneLineReason(err.Error())+
			"；R4 的两条源侧判定与 R7 本次整体不判 —— 未采样 ≠ 库里没有原文")
	} else {
		facts := make([]reconcile.SourceFact, 0, len(infos))
		for _, s := range infos {
			facts = append(facts, reconcile.SourceFact{ID: string(s.ID), Path: s.Rel})
		}
		out.In.Sources = facts
	}

	// ④ Edits：**本 task 逐字传 nil** —— 遗留项 G-058-01（施工卡 §5）。
	//
	// 原因（如实登记，不用话术绕过）：`internal/git` 目前只有 `FollowRenames` / `LogSubjects`
	// 两个只读日志面，**没有**「某路径最近一次改动的 commit 主题」这一 API；新增它属
	// `internal/git` 改面，超出本 task 的 code_paths 与唯一职责。
	// 影响面：已提交的外部编辑不会触发 `reviewed_at` 补齐；未提交改动那条证据照常生效。
	// **这不等于「库里没有已提交的外部编辑」**（nil = 未采样，见 Input.Edits 的注释）。
	warn("git.log", ReconcileEditsUnsampledNotice)

	// ⑤ Renames：逐路径采样（R5 条件② 的事实来源）。扫描面缺失时无从逐路径追溯 → 未采样。
	if out.In.Scan == nil {
		warn("git.log", "rename 事实未采样（nil）：扫描面本次未采到，无从逐路径追溯"+
			" —— 未采样 ≠ 没有发生过跨领域移动")
	} else {
		facts := make([]reconcile.RenameFact, 0)
		failed := ""
		for _, rel := range reconcileScanPaths(out.In.Scan) {
			recs, err := repo.FollowRenames(rel)
			if err != nil {
				failed = rel + "：" + oneLineReason(err.Error())
				break
			}
			for _, rec := range recs {
				facts = append(facts, reconcile.RenameFact{
					OldPath: rec.OldPath, NewPath: rec.NewPath, Verb: rec.Verb})
			}
		}
		if failed != "" {
			warn("git.log", "rename 事实未采样（nil）："+failed+
				"；R5 条件② 本次不判 —— 未采样 ≠ 没有发生过跨领域移动")
		} else {
			out.In.Renames = facts
		}
	}

	// ⑥ Recaps：主题综述分区同样不在扫描底座内，采样口径复用 T-…-055 的只读采样层。
	if sample, err := SampleRecaps(root); err != nil {
		warn("reviews/", "主题综述未采样（nil）："+oneLineReason(err.Error())+
			"；R6 本次整体不判 —— 未采样 ≠ 库里没有综述")
	} else {
		out.In.Recaps = sample.Recaps
	}
	return out
}

// reconcileScanPaths 取扫描面的全部对象路径（卡 + 笔记，vault 相对路径，扫描序）。
func reconcileScanPaths(scan *query.ScanResult) []string {
	if scan == nil {
		return nil
	}
	out := make([]string, 0, len(scan.Cards)+len(scan.Notes))
	for _, c := range scan.Cards {
		out = append(out, c.Path)
	}
	for _, n := range scan.Notes {
		out = append(out, n.Path)
	}
	return out
}

// reconcileRepairsResult 承载对账 ④ R2 + ⑤ R6 两步修复的全部产出，供 runReconcile 合并回
// 主流程：更新后的 findings、需计入 facts 的 RepairRejected / SkippedWrites、本次 eg 写入的
// ourWrites，以及 CLI 侧 error 级诊断与 skip 诊断。rep 上的 warning/info/skipped 由 helper
// 直接追加（与原实现同一副作用）。
type reconcileRepairsResult struct {
	Findings       []reconcile.Finding
	OurWrites      []string
	SkippedWrites  int
	RepairRejected int
	ErrDiags       []Diagnostic
	SkipDiags      []Diagnostic
	// Failures 原样承载两桥计划层/落盘层如实上报的失败文本（不吞、不折叠）。当前 runReconcile
	// 不消费它（控制流不变）；事务底座（C5）会据它裁决 overlay 是否整体放弃。
	Failures []string
}

// runReconcileRepairs 执行对账的 ④ R2 + ⑤ R6 两步修复，是从 runReconcile 抽出的**无行为变化**
// 的一段：逻辑、次序、诊断与副作用逐字保持。
//
// exec 是「无提交校验 + 执行」入口：runReconcile 注入默认的 executePlanNoCommit，因此写口与
// 行为逐字不变；对账事务底座（C5）可改注入一个绑定同一把 atomic Store 的执行器，让 R2 / R6 的
// 写只 stage 到编排 overlay。内部固定走 repairReviewedWithExecutor / repairRecapStaleWithExecutor
// 两个注入版实体。dry-run 分支**完全不调用**本函数。
func (r *Root) runReconcileRepairs(inv *Invocation, sample reconcileInputSample,
	checked reconcile.Result, rep *report.Report, exec planNoCommitExecutor) reconcileRepairsResult {
	root := inv.VaultRoot
	out := reconcileRepairsResult{Findings: checked.Findings}

	// addErr 同时登记两处：CLI 侧 error 级诊断（渲染进 data.errors[]）与报告侧 warnings[]。
	// 报告的 warnings[] 只有 warning / info 两级（合同 §5），因此这里**不新造 error 级别**，
	// 而是把同一句事实以 warning 级如实留档 —— 级别归属由 data.errors[] 表达。
	addErr := func(path, msg string) {
		out.ErrDiags = append(out.ErrDiags, Diagnostic{
			Code: E21, Level: LevelError, Path: path, OpIndex: NonOpDiagnostic, Message: msg})
		rep.AddWarning(report.Diagnostic{
			Code: "", Level: report.LevelWarning, Path: path, OpIndex: report.NonOp, Message: msg})
	}

	// ④ R2：过目信号补齐（`mark_reviewed` op，逐键封闭；B3 不豁免）。
	//    命令行佐证逐字取 inv.UserRequest —— 本命令**不**自证授权（授权合同 N-1）。
	r2, r2err := r.repairReviewedWithExecutor(ReviewedRepairInput{
		VaultRoot: root, Targets: reconcile.ReviewedTargets(sample.In),
		Repairs: checked.Repairs, Findings: out.Findings, UserRequest: inv.UserRequest,
		Reason: "对账 R2：为经用户直接编辑的对象补齐过目信号单键",
	}, exec)
	out.Findings = reconcileTakeAnnotated(out.Findings, r2.Findings)
	out.OurWrites = append(out.OurWrites, r2.Written...)
	out.SkippedWrites += len(r2.Skipped)
	for _, s := range r2.Skipped {
		rep.Skipped = append(rep.Skipped, report.Skipped{
			Kind: s.Kind, Target: s.Target, Locator: s.Path, Cause: s.Cause, Detail: s.Detail})
		out.SkipDiags = append(out.SkipDiags, reconcileSkipDiag(s.OpIndex, s.Kind, s.Path, s.Cause, s.Detail))
	}
	for _, w := range r2.Warnings {
		rep.AddWarning(report.Diagnostic{Code: "", Level: report.LevelWarning,
			Path: "reconcile.r2", OpIndex: report.NonOp, Message: oneLineReason(w)})
	}
	out.Failures = append(out.Failures, r2.Failures...)
	if r2err != nil {
		// 两种「未补齐」要分清：
		//   · 没有命令行佐证 → 矩阵必然拦下，这是**预期形态**，只留 warning、不改退出码；
		//   · 有佐证却仍被拒 → 真实的结构性拒绝，按校验失败计入退出码 2。
		if inv.UserRequest {
			out.RepairRejected++
			addErr("reconcile.r2", "R2 修复被拒（零写入）："+oneLineReason(r2err.Error()))
		} else {
			rep.AddWarning(report.Diagnostic{Code: "", Level: report.LevelWarning,
				Path: "reconcile.r2", OpIndex: report.NonOp,
				Message: ReconcileReviewedNeedsUserRequestNotice + "（计划层回执：" +
					oneLineReason(r2err.Error()) + "）"})
		}
	}

	// ⑤ R6：综述失准标记两键（`set_stale` op）。A-34 已裁决**不需要** `--user-request`，
	//    因此这里的结构性拒绝一律是真实异常，计入退出码 2。
	r6, r6err := r.repairRecapStaleWithExecutor(StaleRepairInput{
		VaultRoot: root, Targets: reconcile.RecapTargets(sample.In),
		Repairs: checked.Repairs, Findings: out.Findings,
		Reason: "对账 R6：为引用卡已变化的主题综述写失准标记两键",
	}, exec)
	out.Findings = reconcileTakeAnnotated(out.Findings, r6.Findings)
	out.OurWrites = append(out.OurWrites, r6.Written...)
	out.SkippedWrites += len(r6.Skipped)
	for _, s := range r6.Skipped {
		rep.Skipped = append(rep.Skipped, report.Skipped{
			Kind: s.Kind, Target: s.Target, Locator: s.Path, Cause: s.Cause, Detail: s.Detail})
		out.SkipDiags = append(out.SkipDiags, reconcileSkipDiag(s.OpIndex, s.Kind, s.Path, s.Cause, s.Detail))
	}
	for _, w := range r6.Warnings {
		rep.AddWarning(report.Diagnostic{Code: "", Level: report.LevelWarning,
			Path: "reconcile.r6", OpIndex: report.NonOp, Message: oneLineReason(w)})
	}
	out.Failures = append(out.Failures, r6.Failures...)
	if len(r6.Idempotent) > 0 {
		rep.AddInfo("reconcile.r6", report.NonOp,
			"%d 篇综述的失准标记已是同一取值与同一理由：本次零写入（幂等），finding 仍在",
			len(r6.Idempotent))
	}
	if r6err != nil {
		out.RepairRejected++
		addErr("reconcile.r6", "R6 修复被拒（零写入）："+oneLineReason(r6err.Error()))
	}

	return out
}

// checkReconcileWriteSet 校验一次对账提交的原子 write-set 与本次上报的 ourWrites 是否**逐项相等**：
// 两侧各自排序去重后必须完全一致。事务底座（C5）用它在提交前确认「实际 stage 到 overlay 的权威
// 文件集合」与「回执声称本次由 eg 写入的集合」没有漂移；当前 runReconcile 不调用它（控制流不变）。
//
// want = sortUniq(ourWrites)（回执声称写过的）；got = sortUniq(writeSetPaths(ws))（实际 stage 的）。
func checkReconcileWriteSet(ws []store.AtomicFileSpec, ourWrites []string) error {
	want := sortUniq(ourWrites)
	got := sortUniq(writeSetPaths(ws))
	mismatch := len(want) != len(got)
	for i := 0; !mismatch && i < len(want); i++ {
		if want[i] != got[i] {
			mismatch = true
		}
	}
	if mismatch {
		return fmt.Errorf("对账 write-set 与 ourWrites 不一致：want %v，got %v", want, got)
	}
	return nil
}

// reconcileResult 是对账命令的 Result 组装器：在 proposalResult 之上**始终**补齐
// data.reconcile 三键投影（值取 rep.Reconcile）与 DataOrder=[reconcile, report]。
//
// 单列这一层是为了封死一个契约缺口：退 2 / 3 / 4 乃至事务未闭合等失败路径，先前各自
// 调 proposalResult 后**未必**记得把 data.reconcile 投影上去，导致失败信封里三键时有时无。
// 统一走本 helper 后，只要调用前已 rep.SetReconcile(...)（本命令在 S5 前就已定盘基线），
// 任何返回路径的 data.reconcile 都存在且恰为三键。
func reconcileResult(rep report.Report, summary []string) *Result {
	res := proposalResult(rep, summary)
	res.Data["reconcile"] = rep.Reconcile
	res.DataOrder = []string{"reconcile", "report"}
	return res
}

// runReconcileCritical 是 `eg reconcile` **非 dry-run** 路径的 A 类强事务编排（对账事务底座 ·
// C5 / M6；与 apply / proposal 等写命令共用**同一把** `.index/run.lock`）。
//
// 固定次序（与 proposal / apply 的强事务同源）：
//
//	S1 取 run.lock
//	S2 崩溃恢复屏障（临界区内第一件事，早于任何一次业务读）
//	S3 锁内**重新**六格采样 + reconcile.Run —— 严禁复用锁外快照：S2 刚可能回滚过某未闭合
//	   事务，取锁前别的写者也可能改过盘，findings / repairs 必须落在当下盘上的字节
//	S4 原子预演：共享一把 atomic Store，R2 / R6 修复的「校验 → 执行」只 stage 到 overlay
//	S5 仅当 write-set 非空才分配 txn_id + 发布 intent（ws 为空不开 txn，也不写 txn_id）
//	S6 原子提交（commit marker 定盘）；回滚退 3、未闭合阻断均照 proposal / apply
//	⑥ R1 纳管：S6 成功或 ws 为空后**恰一次** takeover（可能只提交用户既有脏改动或零 commit）
//	S8 写后索引同步：Git 成败都走、仍在同一把锁内、早于 release（txnID 可空）
//	S9 显式 release，之后才 proposalResult / saveReport / reconcileExitCode 与旧四类错误映射
//
// 退出码仍由 reconcileExitCode(facts) 裁决（error finding → 2、B3 跳过 → 3、Git 失败 → 4），
// 与只读原流程逐字同源；rr.Failures 只是如实上报的诊断文本，不改退出码、不触发 overlay 放弃。
func (r *Root) runReconcileCritical(inv *Invocation, rep *report.Report) (*Result, error) {
	root := inv.VaultRoot

	// —— S1 + S2：取锁、崩溃恢复屏障。——
	sess, serr := r.enterTxnCritical(inv, reportWarnSink{rep}, txnCriticalOpts{
		ZeroWrite: "本次零写入", ReleaseNotice: "本次写入与提交不受影响",
	})
	if serr != nil {
		return nil, serr
	}
	defer sess.release()

	// —— S3：锁内重新采样 + 检查（严禁复用锁外快照）。warnings / ScopeNotice 与只读原流程同源。——
	sample := r.sampleReconcileInput(root)
	checked := reconcile.Run(sample.In)
	for _, d := range sample.Warnings {
		rep.AddWarning(d)
	}
	rep.AddInfo("eg reconcile", report.NonOp, "%s", ReconcileScopeNotice)

	// —— S4：原子预演。共享一把 atomic Store，兜底丢弃保证导出 write-set 前的每条 return
	//    都「零生效」（实盘与 Git 全程未变）。——
	st := store.New(root)
	if err := st.BeginAtomic(); err != nil {
		return nil, blockedError("原子预演无法开始，本次零写入", err)
	}
	defer st.EndAtomic()

	// exec 捕获同一把 atomic st：R2 / R6 修复的写只 stage 到该 Store 的 overlay，
	// 而不是各自 store.New(root) 另起实盘写口。
	exec := func(root string, userRequest bool, stamp model.Stamp,
		p *plan.ChangePlan) (*plan.Result, *plan.ExecResult, error) {
		return executePlanNoCommitOnStore(st, root, userRequest, stamp, p)
	}
	rr := r.runReconcileRepairs(inv, sample, checked, rep, exec)

	// —— 导出原子 write-set，校验「实际 stage 的权威文件」与「回执声称本次写过的」逐项相等。——
	//     rr.Failures 只是计划层/落盘层如实上报的诊断文本（例如 R2 无 --user-request 时矩阵
	//     必然拦下的**预期形态**），它不改变退出码、不触发 overlay 放弃：退出码仍由
	//     reconcileExitCode(facts) 按 error finding / B3 跳过 / Git 失败裁决（与只读原流程同源）。
	ws := st.AtomicWriteSet()
	if err := checkReconcileWriteSet(ws, rr.OurWrites); err != nil {
		return nil, blockedError("对账原子域与本次写入回执不一致，本次零写入", err)
	}
	fireTxnStep(TxnStepExecuted, "")

	// 退出码事实与旧只读原流程逐字同源：findings / 跳过数 / 结构性拒绝 / CLI 诊断均取修复回执。
	findings := rr.Findings
	facts := reconcileFacts{
		SkippedWrites:  rr.SkippedWrites,
		RepairRejected: rr.RepairRejected,
	}
	errDiags := append([]Diagnostic(nil), rr.ErrDiags...)
	skipDiags := append([]Diagnostic(nil), rr.SkipDiags...)

	// 契约兜底：先用**当前 findings** 把 data.reconcile 三键（ran / commit / findings）定盘一份
	// 基线投影——commit 暂空、links 取本次写入集合、facts.ErrorFindings 一并定盘。此后无论走
	// 分配后失败、S6 回滚、S6 未闭合，还是最终成功 / 失败，返回信封里的 data.reconcile 都已存在
	// 且恰为三键；S6 成功后再用真实 sha 覆写这份投影。
	facts.ErrorFindings = reconcileErrorCount(findings)
	rep.SetCommit("")
	rep.SetReconcile(true, "", reconcileReportFindings(findings))
	rep.Links = sortUniq(rr.OurWrites)

	// —— S5：仅当有权威写入才开事务（分配 txn_id + 发布 intent，A-59 两段式）。——
	//    ws 为空说明本次没有修复写入：**不开 txn、不写 txn_id**，但 R1 纳管仍在同一把锁内发生
	//    （可能只提交用户在编辑器里既有的脏改动，或零 commit）。
	txnID := ""
	if len(ws) > 0 {
		tid, oerr := sess.openTxn(inv, intentFilesOf(ws), nil, r.Now, func(id string) {
			rep.SetTxnID(id) // txn_id 仅在「有写入且分配成功」时写进报告（审计边界＝分配成功）
		})
		if oerr != nil {
			if tid == "" {
				// 号码根本没分配出来：盘上不存在事务目录，本次零写入、零 intent。
				return nil, oerr
			}
			// 号码已在盘 ⇒ 必须带着 txn_id 交付（用户要靠它定位那笔未闭合事务）。
			rep.SetCommit("")
			sess.release()
			res := reconcileResult(*rep, []string{fmt.Sprintf(
				"对账未提交：事务 %s 已分配号码但未闭合，本次零权威写入", tid)})
			r.saveReport(root, res, ExitCodeFor(classifyExit5(oerr)))
			return res, oerr
		}
		txnID = tid

		// —— S6：原子提交。commit marker 在盘之前，修复写入一律不算生效。——
		cres, cerr := sess.commitWriteSet(txnID, ws)
		switch {
		case cerr != nil && cres != nil && cres.RolledBack:
			// 回滚成功 ⇒ 退 3。逐路径交代「目标未写入」，且**不跑 Git / S8**。
			unwritten := writeSetPaths(ws)
			rep.AddWarning(unnumberedWarning("ops",
				"原子提交失败，事务 %s 已按合同 §5.2 主动放弃：%v", txnID, cerr))
			for _, p := range unwritten {
				rep.AddWarning(unnumberedWarning(p,
					"目标未写入：本次事务已整体回滚，该文件保持事务开始前的字节（前像）"))
			}
			rep.SetCommit("")
			sess.release()
			res := reconcileResult(*rep, []string{fmt.Sprintf(
				"对账未生效：事务 %s 已整体回滚，%d 处修复写入全部保持事务开始前的字节",
				txnID, len(unwritten))})
			r.saveReport(root, res, ExitPartialWrite)
			return res, &PartialWriteError{Msg: fmt.Sprintf(
				"原子提交失败，事务 %s 已整体回滚：%d 处修复一个字节都没有写入、"+
					"磁盘保持事务开始前的字节、未产生 commit（原因：%v）",
				txnID, len(unwritten), cerr)}
		case cerr != nil:
			// 既未完成也未回滚 ⇒ 阻断，交人工处置（**不跑 Git / S8**）。
			blocked := blockedError(fmt.Sprintf(
				"事务 %s 的原子提交失败且未能收敛为已回滚状态，需人工处置", txnID), cerr)
			rep.SetCommit("")
			sess.release()
			res := reconcileResult(*rep, []string{fmt.Sprintf(
				"对账未闭合：事务 %s 的提交既未完成也未回滚，请人工核对", txnID)})
			r.saveReport(root, res, ExitCodeFor(classifyExit5(blocked)))
			return res, blocked
		}
		fireTxnStep(TxnStepCommitted, txnID)
	}

	// —— ⑥ R1 纳管：S6 成功或 ws 为空后，**恰一次** takeover（参数 / 文案保持旧值）。——
	takeover := r.newReconcileTakeover(root)
	tres, terr := takeover.Takeover(ReconcileTakeoverInput{
		Findings: len(findings), Repairs: len(checked.Repairs),
		OurWrites: sortUniq(rr.OurWrites), ChecksDone: true, RepairsDone: true, DryRun: false,
		Reason: "对账纳管：把用户在编辑器里直接改过的文件记进 Git 历史（R1，恰一次提交）",
	})
	for _, w := range tres.Warnings {
		rep.AddWarning(report.Diagnostic{Code: "", Level: report.LevelWarning,
			Path: "git.commit", OpIndex: report.NonOp, Message: oneLineReason(w)})
	}
	sha := ""
	if tres.Commit != nil {
		sha = *tres.Commit
	}
	if terr != nil {
		facts.CommitFailed = true
	}
	fireTxnStep(TxnStepGit, txnID)

	// —— S8：写后索引同步。Git 成败都走，仍在同一把锁内、早于 release（txnID 可空）。——
	r.syncIndexAfterWrite(rep, root, indexWriteLabel(inv), sortUniq(append(append(
		[]string(nil), rr.OurWrites...), tres.Files...)))
	fireTxnStep(TxnStepIndexSync, txnID)

	// —— 锁内定盘 sha 相关投影：用真实 sha 覆写 S5 前定盘的基线三键与 links（三键一字不变）。
	//    facts.ErrorFindings 已在 S5 前定盘，此处不重复计算。——
	rep.SetCommit(sha)
	rep.SetReconcile(true, sha, reconcileReportFindings(findings))
	rep.Links = sortUniq(rr.OurWrites)
	code := reconcileExitCode(facts)

	// —— S9：显式 release，之后只剩纯渲染、落盘与退出码裁决（回到锁外语义）。——
	sess.release()

	res := reconcileResult(*rep, reconcileSummary(findings, facts, sha, false, len(rr.OurWrites)))
	r.saveReport(root, res, code)

	switch code {
	case ExitCommitFailed:
		// 写口在真实提交失败时已返回带诊断的 *CommitFailedError：原样上抛，不改写它的事实。
		var failed *CommitFailedError
		if errors.As(terr, &failed) {
			return res, failed
		}
		return res, &CommitFailedError{
			Msg: "对账纳管未能执行（本次零提交；磁盘保留当前状态，未做任何还原）",
			Err: terr,
			Diags: []Diagnostic{{Code: E22, Level: LevelError, Path: "git.commit",
				OpIndex: NonOpDiagnostic, Message: oneLineReason(terr.Error())}},
		}
	case ExitPartialWrite:
		return res, &PartialWriteError{
			Msg: fmt.Sprintf("对账有 %d 处写入被写前内容比对拦下（B3 不豁免）："+
				"已完成的写入保留并已进本次提交，被拦下的留给下次对账", facts.SkippedWrites),
			Diags: skipDiags,
		}
	case ExitValidation:
		return res, &ValidationError{
			Msg:   reconcileValidationMessage(facts, sha),
			Diags: append(errDiags, reconcileFindingDiags(findings)...),
		}
	default:
		return res, nil
	}
}

// runReconcile 是 `eg reconcile` 的唯一入口。七步固定次序（施工卡 §2.3 = 合同 §12 + A-35）：
//
//	① 六格只读采样 → ② reconcile.Run（R1–R7 检查）→ ③ dry-run 短路（跳过 ④⑤ 的写入）
//	→ ④ R2 修复（矩阵 #7 / #22 的 P-U）→ ⑤ R6 修复（A-34：不需要命令行佐证）
//	→ ⑥ 恰一次纳管 commit → ⑦ 组装报告 / data / 退出码
//
// 「先 commit 再退 2」的实现落点就在这个次序里：退出码在**第 ⑦ 步**才算，
// 而纳管在第 ⑥ 步已经完成；且 `root.go:render` 会把 error 与 Result 合并渲染，
// 因此退 2 / 3 / 4 时 `data`（含 `reconcile` 三键）仍然完整输出。
func (r *Root) runReconcile(inv *Invocation) (*Result, error) {
	root := inv.VaultRoot
	dryRun := inv.Set("dry-run")

	// 非 dry-run 进入 A 类强事务编排（C5 生产迁移）：取锁、崩溃恢复屏障、锁内重采样与
	// 原子预演。dry-run 仍走下方**现有只读原流程**（不拿锁、零写入零 commit），一字未改。
	if !dryRun {
		rep := report.New()
		return r.runReconcileCritical(inv, &rep)
	}

	// ① + ② 取数与检查：本文件不新增任何判定，findings / repairs 全部由检查包产出。
	sample := r.sampleReconcileInput(root)
	checked := reconcile.Run(sample.In)
	findings := checked.Findings

	rep := report.New()
	for _, d := range sample.Warnings {
		rep.AddWarning(d)
	}
	rep.AddInfo("eg reconcile", report.NonOp, "%s", ReconcileScopeNotice)

	var (
		facts     reconcileFacts
		ourWrites []string
		errDiags  []Diagnostic
		skipDiags []Diagnostic
	)

	// ③ dry-run：跳过 ④ ⑤ 两步的**全部**写入（零写入零 commit），检查与报告照常。
	if dryRun {
		rep.AddInfo("eg reconcile", report.NonOp, "%s", ReconcileDryRunNotice)
	} else {
		// ④ R2 + ⑤ R6：整段修复抽到 runReconcileRepairs（无行为变化）。runReconcile 注入
		//    默认执行器 executePlanNoCommit，因此写口与行为逐字不变；dry-run 完全不调用它。
		rr := r.runReconcileRepairs(inv, sample, checked, &rep, executePlanNoCommit)
		findings = rr.Findings
		ourWrites = append(ourWrites, rr.OurWrites...)
		facts.SkippedWrites += rr.SkippedWrites
		facts.RepairRejected += rr.RepairRejected
		errDiags = append(errDiags, rr.ErrDiags...)
		skipDiags = append(skipDiags, rr.SkipDiags...)
	}

	// ⑥ 纳管：**恰一次** commit（写口自身以实例状态锁死次数，本文件只调用一次）。
	takeover := r.newReconcileTakeover(root)
	tres, terr := takeover.Takeover(ReconcileTakeoverInput{
		Findings: len(findings), Repairs: len(checked.Repairs),
		OurWrites: sortUniq(ourWrites), ChecksDone: true, RepairsDone: true, DryRun: dryRun,
		Reason: "对账纳管：把用户在编辑器里直接改过的文件记进 Git 历史（R1，恰一次提交）",
	})
	for _, w := range tres.Warnings {
		rep.AddWarning(report.Diagnostic{Code: "", Level: report.LevelWarning,
			Path: "git.commit", OpIndex: report.NonOp, Message: oneLineReason(w)})
	}
	sha := ""
	if tres.Commit != nil {
		sha = *tres.Commit
	}
	if terr != nil {
		facts.CommitFailed = true
	}

	// ⑦ 报告 + data + 退出码。三个 data 事实同源：报告体、`reconcile` 三键、人类可读摘要
	//    都从同一批 findings / commit 派生，不各算一遍。
	facts.ErrorFindings = reconcileErrorCount(findings)
	rep.SetCommit(sha)
	rep.SetReconcile(true, sha, reconcileReportFindings(findings))
	rep.Links = sortUniq(ourWrites)
	code := reconcileExitCode(facts)

	if !dryRun {
		// 写后索引同步（M5 · T-…-066）：对账的修复写入与**恰一次**纳管 commit 都已完成之后，
		// 把「我们写的文件」+「本次纳管进历史的文件」一并交给索引跟进 ——
		// 后者是用户在编辑器里直接改的卡，正是最需要收敛的一批。
		// dry-run 不走这里：预演一个字节都不写，派生索引也不例外。
		r.syncIndexAfterWrite(&rep, root, indexWriteLabel(inv), sortUniq(append(append(
			[]string(nil), ourWrites...), tres.Files...)))
	}

	res := proposalResult(rep, reconcileSummary(findings, facts, sha, dryRun, len(ourWrites)))
	// `reconcile` 三键（键名一格不改，值直接取报告侧投影）+ 完整报告体：
	// 前者是本命令的合同产物，后者让 `eg report --last` 能逐字复现同一批事实。
	res.Data["reconcile"] = rep.Reconcile
	res.DataOrder = []string{"reconcile", "report"}
	if !dryRun {
		// 落盘最近一次报告（唯一写口是 writeLastReport，saveReport 顺带把工程状态目录
		// 登记进仓库本地忽略清单，因此它既不进本次 commit 也不污染下一次对账的工作区）。
		// dry-run 不落盘：与 `eg apply --dry-run` 逐字同一先例 —— 预演一个字节都不写。
		r.saveReport(root, res, code)
	}

	switch code {
	case ExitCommitFailed:
		// 写口在真实提交失败时已返回带诊断的 *CommitFailedError：原样上抛，不改写它的事实。
		var failed *CommitFailedError
		if errors.As(terr, &failed) {
			return res, failed
		}
		// 其余 Git 层失败（状态读不动 / 调用序守卫）同样是「本次零提交、磁盘保留现状」，
		// 归入既有的 4；**不新增第五种错误类型、不启用退出码 5**。
		return res, &CommitFailedError{
			Msg: "对账纳管未能执行（本次零提交；磁盘保留当前状态，未做任何还原）",
			Err: terr,
			Diags: []Diagnostic{{Code: E22, Level: LevelError, Path: "git.commit",
				OpIndex: NonOpDiagnostic, Message: oneLineReason(terr.Error())}},
		}
	case ExitPartialWrite:
		return res, &PartialWriteError{
			Msg: fmt.Sprintf("对账有 %d 处写入被写前内容比对拦下（B3 不豁免）："+
				"已完成的写入保留并已进本次提交，被拦下的留给下次对账", facts.SkippedWrites),
			Diags: skipDiags,
		}
	case ExitValidation:
		return res, &ValidationError{
			Msg:   reconcileValidationMessage(facts, sha),
			Diags: append(errDiags, reconcileFindingDiags(findings)...),
		}
	default:
		return res, nil
	}
}

// reconcileTakeAnnotated 采纳修复桥回传的**注记副本**：条数守恒才替换。
//
// 修复桥只会给被跳过的条目追加「已跳过」注记（不删条目、不降级），条数因此必须相等；
// 条数不等说明两侧不同源，这时宁可保留原始 findings，也不接受一份可能少了事实的副本。
func reconcileTakeAnnotated(current, annotated []reconcile.Finding) []reconcile.Finding {
	if len(current) == 0 || len(annotated) != len(current) {
		return current
	}
	return annotated
}

// reconcileErrorCount 数 error 级 finding 的条数（severity 的真源恒在检查包，本层不重判）。
func reconcileErrorCount(fs []reconcile.Finding) int {
	n := 0
	for _, f := range fs {
		if f.Severity == reconcile.SeverityError {
			n++
		}
	}
	return n
}

// reconcileReportFindings 把检查包的 finding 搬进报告镜像：**四键一一对应、条数守恒**，
// 不排序、不去重、不合并（排序与归一化是检查包的职责）。
func reconcileReportFindings(fs []reconcile.Finding) []report.ReconcileFinding {
	out := make([]report.ReconcileFinding, 0, len(fs))
	for _, f := range fs {
		out = append(out, report.ReconcileFinding{
			Check: f.Check, Severity: f.Severity, Targets: f.Targets, Detail: f.Detail})
	}
	return out
}

// reconcileFindingDiags 把 error 级 finding 投成可定位的诊断条目（退 2 时进 data.errors[]）。
// 诊断码取自检查包的单射表，取不到就不编一个（渲染与判定都不自造码）。
func reconcileFindingDiags(fs []reconcile.Finding) []Diagnostic {
	var out []Diagnostic
	for _, f := range fs {
		if f.Severity != reconcile.SeverityError {
			continue
		}
		code, _ := reconcile.CodeOf(f.Check)
		out = append(out, Diagnostic{
			Code: code, Level: LevelError, Path: strings.Join(f.Targets, "、"),
			OpIndex: NonOpDiagnostic, Message: f.Detail, Target: f.Check})
	}
	return out
}

// reconcileSkipDiag 把一条「本次未写入」投成诊断（退 3 时进 data.errors[]，逐条不折叠）。
// kind / cause 逐字取自计划层的封闭命名，本层不改写、不归并。
func reconcileSkipDiag(opIndex int, kind, path, cause, detail string) Diagnostic {
	return Diagnostic{
		Code: E24, Level: LevelError, Path: path, OpIndex: opIndex,
		Message: fmt.Sprintf("本次未写入（kind=%s，cause=%s）：%s", kind, cause, detail)}
}

// reconcileValidationMessage 陈述退 2 的两类事实，并把「先 commit 再退 2」写成可复算的一句话。
func reconcileValidationMessage(f reconcileFacts, sha string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "对账发现 %d 条 error 级 finding", f.ErrorFindings)
	if f.RepairRejected > 0 {
		fmt.Fprintf(&b, "、%d 处修复被结构性拒绝（零写入）", f.RepairRejected)
	}
	if sha == "" {
		b.WriteString("；本次纳管零改动故零 commit（检查与纳管都已跑完，退 2 不阻断写入路径）")
		return b.String()
	}
	b.WriteString("；R1 纳管 commit " + sha + " 已完成（先完成纳管再退 2）")
	return b.String()
}

// reconcileSummary 组装人类可读摘要。
//
// finding 行与视图提示**一律复用** reconcile_render.go（`ReconcileHint` / `ReconcileFindingLines`
// / `ReconcileHintSummary`）：那里是这些措辞在整个仓库里的唯一落点，本文件不拼第二份。
func reconcileSummary(fs []reconcile.Finding, f reconcileFacts, sha string,
	dryRun bool, written int) []string {
	errN := reconcileErrorCount(fs)
	head := fmt.Sprintf("对账已执行：finding %d 条（error %d、warning %d）、修复写入 %d 处、"+
		"被跳过 %d 处、commit %s", len(fs), errN, len(fs)-errN, written, f.SkippedWrites,
		reconcileCommitText(sha, dryRun))
	out := []string{head}
	if dryRun {
		out = append(out, ReconcileDryRunNotice)
	}
	out = append(out, ReconcileFindingLines(fs)...)
	if hint := ReconcileHintSummary(fs); hint != "" {
		out = append(out, hint)
	}
	return out
}

// reconcileCommitText 如实陈述 commit 这一格：有就报 sha，没有就说清为什么没有。
func reconcileCommitText(sha string, dryRun bool) string {
	switch {
	case sha != "":
		return sha
	case dryRun:
		return "无（dry-run：零写入零提交，reconcile.commit = null）"
	default:
		return "无（零改动即零 commit，reconcile.commit = null）"
	}
}
