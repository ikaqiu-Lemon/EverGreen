package cli

// eg apply 的业务实现（合同 §1.5 / §2；技术方案 §7.1、§8、§9、§4.5.1）。
//
// `eg apply` 是 S1 **唯一的写入入口**，本文件只做**编排**，五步固定顺序：
//
//	① 反序列化 + E1–E6 校验（任一 error → 退 2、零写入、无 commit）
//	② 按 ops[] 声明顺序逐个执行（internal/plan 的执行器 → internal/store 的写口）
//	③ 按 §4.6 汇总实际写入事实与 skipped[] / warnings[]
//	④ 一次 git add -A + commit（verb 取 plan.verb）
//	⑤ 产出最终报告（internal/report），失败路径同样出报告
//
// 校验、op 展开、字节写入、提交信息与 B4 全部复用既有实现，本文件**不重新实现**任何一层。
// 退出码只用 exit.go 的四类带类型错误表达，**绝不在这里自行拼装码值**：
// 0 全部成功 / 1 环境或参数错误 / 2 校验失败（零写入）/ 3 部分成功 / 4 提交失败（不回滚，B4）。
//
// 本文件没有事务意图文件、没有两阶段写入、没有文件锁、没有写前复核、没有任何回滚入口
// （S5 / M6 的范围，S1 一律不做）。

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/git"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/plan"
	"github.com/ikaqiu-Lemon/EverGreen/internal/report"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// StateDir 是 vault 内的**工程状态**目录：只放 `eg` 自己的运行痕迹，
// 不是知识产物（EG-AGT-04），也不在 domains/** 或 sources/** 之下。
const StateDir = ".eg"

// LastReportFile 是最近一次 apply 报告的落盘位置（供 `eg report --last` 逐字复现）。
const LastReportFile = StateDir + "/last-report.json"

// coverageGapNames 是 coverage_gaps 受控枚举的中文覆盖要点名（EG-EXT-02：透传不判断）。
var coverageGapNames = map[string]string{
	"core_claim":     "核心主张",
	"key_evidence":   "关键证据",
	"counterexample": "反例",
	"boundary":       "边界",
	"method":         "方法",
	"conclusion":     "结论",
	"limitation":     "局限",
}

// runApply 实现 eg apply：只负责「取字节 → 反序列化」，其余编排交给 runPlan。
func (r *Root) runApply(inv *Invocation) (*Result, error) {
	raw, err := r.planBytes(inv)
	if err != nil {
		return nil, err
	}

	p, perr := plan.Parse(raw)
	if perr != nil {
		rep := report.New()
		rep.NoteZeroCards("plan")
		return applyResult(rep, nil, nil), &ValidationError{
			Msg: perr.Error(),
			Diags: []Diagnostic{{
				Code: "E5", Level: LevelError, Path: "plan", OpIndex: NonOpDiagnostic,
				Message: perr.Error(),
			}},
		}
	}
	return runPlan(r, inv, p)
}

// runPlan 是 ChangePlan 的**唯一**执行编排：`eg apply`（T-…-015）与 `eg rel add`
// （T-…-024）共用这一份实现，绝不各留一份平行副本。
//
// 入参 p 是一份**已解析**的 plan：apply 由 `plan.Parse` 得到，rel add 在内存里组装
// （不落临时文件）。两条入口自此往下逐字走同一条链路：
//
//	plan.Validate（E1–E6 / W1–W8 / I1）→ plan.Execute（executor → internal/store 写口）
//	→ report 汇总 → internal/git 一次 commit（verb 取 plan.verb）
//
// 因此「B1–B4」「退出码 0–4」「一次命令 = 一次 commit」对两条入口是同一套代码事实。
//
// 【M6 · T-…-072 批次 B2a】写入段自此走 recover_hook.go 的**事务编排单点**：
// S0（本函数开头，锁外）只做参数与 plan 解析；S1~S8 全部在 run.lock 的临界区内
// （S2 恢复屏障 → S3 锁内重读重校验 → S4 原子预演 → S5 intent → S6 原子提交 →
// S7 Git → S8 写后索引同步）；报告渲染与 last-report 落盘回到锁外（S9 之后）。
func runPlan(r *Root, inv *Invocation, p *plan.ChangePlan) (*Result, error) {
	root := inv.VaultRoot

	// --dry-run 是**只读预览**：零写入零 commit，因此不取锁、不建 .index/、不开事务。
	// 合同 §2 的临界区护的是写路径；让一次预览去抢锁，只会让它被别人的写入卡成 E16。
	if inv.Set("dry-run") {
		return r.runPlanDryRun(inv, p)
	}

	rep := report.New()
	out, err := r.runPlanCritical(inv, p, &rep)
	if err != nil {
		return nil, err
	}

	// —— 以下全部在**锁外**：报告渲染、last-report 落盘、退出码翻译。——
	res := applyResult(rep, out.Pres, nil)
	switch {
	case out.PrecheckFailed:
		// ⓪ 写前强校验（--strict）失败：W1/W2/W3/W4/W6 升 error，在开事务前把写拦下。
		// 零权威写入、未分配 txn_id、未发 intent、无 commit、不发 W26；退出码 5（携 E15）。
		// last-report 退出码与最终进程退出码同源：只经 ExitCodeFor(classifyExit5(...)) 翻译一次。
		r.saveReport(root, res, ExitCodeFor(classifyExit5(out.Blocked)))
		return res, out.Blocked
	case out.ValidationFailed:
		// ① 校验失败：零写入、无 commit、未开事务，报告照样产出（失败也要有交代）。
		return res, &ValidationError{
			Msg: fmt.Sprintf("ChangePlan 校验失败（%d 条 error）：零写入、无 commit",
				len(out.Pres.Errors)),
			Diags: toCLIDiags(out.Pres.Errors),
		}
	case out.Aborted:
		// ② 原子预演失败：整事务放弃，零权威写、零 intent、零 Git。
		r.saveReport(root, res, ExitPartialWrite)
		return res, &PartialWriteError{
			Msg: fmt.Sprintf("原子事务已放弃：%d 处写入失败、%d 处被跳过；本次零写入、无 commit",
				len(out.Exec.Failures), len(out.Exec.Skipped)),
			Diags: skippedDiags(out.Exec),
		}
	case out.RolledBack:
		// ②' 原子提交期普通 I/O 失败：txn 层已按合同 §5.2 主动放弃 —— 全部目标回前像、
		// abort 最后落盘、Git 未跑。这是「一条都没写成」，不是基础设施崩坏，退 3。
		r.saveReport(root, res, ExitPartialWrite)
		return res, &PartialWriteError{
			Msg: fmt.Sprintf("原子提交失败，事务已整体回滚：%d 个目标文件一个都没有写入、"+
				"磁盘保持事务开始前的字节、未产生 commit（原因：%v）",
				out.TargetCount, out.RollbackErr),
			Diags: skippedDiags(out.Exec),
		}
	case out.Blocked != nil:
		// ②'' txn_id 已分配、但事务在提交前被阻断（intent 发布失败 / 提交未收敛为已回滚）。
		//
		// 这一格**必须落报告**：号码已在 `.index/txn/` 占位，S2 的 W26 也已攒进报告体。
		// 若照旧沿 `return nil, err` 走掉，用户拿到的就只有一句错误文案 —— 事务号与恢复
		// 诊断双双消失，恰是 A-59 的审计边界要防的漏报。退出码仍由 ExitCodeFor 裁决
		// （TxnBlockedError 不实现 ExitCode()，落未分类兜底 1；退出码 5 的启用属 T-…-074）。
		r.saveReport(root, res, ExitCodeFor(classifyExit5(out.Blocked)))
		return res, out.Blocked
	case out.CommitErr != nil:
		// ③ Git 提交失败：Markdown 已原子生效并保持目标态，不回滚、不二次写（B4）。
		r.saveReport(root, res, ExitCommitFailed)
		return res, &CommitFailedError{
			Msg: "Git 提交失败：已写入的文件保持写入后状态，未做任何还原（B4）", Err: out.CommitErr,
			Diags: []Diagnostic{{
				Code: E22, Level: LevelError, Path: "git.commit", OpIndex: NonOpDiagnostic,
				Message: commitFailureMessage(out.CommitErr),
			}},
		}
	case out.Exec.Partial():
		// ④ 部分成功：被跳过的没写，其余已写入并已提交。
		r.saveReport(root, res, ExitPartialWrite)
		return res, &PartialWriteError{
			Msg: fmt.Sprintf("部分成功：%d 处被跳过、%d 处写入失败；已写入内容保留并已提交",
				len(out.Exec.Skipped), len(out.Exec.Failures)),
			Diags: skippedDiags(out.Exec),
		}
	}
	r.saveReport(root, res, ExitOK)
	return res, nil
}

// runPlanDryRun 是 `--dry-run` 的只读预览：完整走校验与展开，只输出将写入的清单。
//
// 它**不进临界区**：零写入零 commit 的预览没有互斥需求，也不该为了预览就在库里
// 造出 `.index/`（索引与事务运行时目录只由真正的写路径 / 显式 index 命令创建）。
func (r *Root) runPlanDryRun(inv *Invocation, p *plan.ChangePlan) (*Result, error) {
	st := store.New(inv.VaultRoot)
	env, err := plan.EnvFor(st, inv.Config.DefaultDomain)
	if err != nil {
		return nil, err
	}
	env.UserRequest = AuthorizationOf(inv).UserRequest
	pres := plan.Validate(p, env)
	if pres.DomainUnavailable {
		return nil, &UsageError{Msg: fmt.Sprintf(
			"无法确定落位领域：plan.domain 缺失且 %s 未配置 default_domain（CLI 绝不自选领域）",
			ConfigFileName)}
	}
	rep := report.New()
	notePlanValidation(&rep, pres)
	if pres.Failed() {
		rep.NoteZeroCards("cards")
		return applyResult(rep, pres, nil), &ValidationError{
			Msg:   fmt.Sprintf("ChangePlan 校验失败（%d 条 error）：零写入、无 commit", len(pres.Errors)),
			Diags: toCLIDiags(pres.Errors),
		}
	}
	return r.applyDryRun(rep, pres), nil
}

// executePlanNoCommit 走 runPlan 的**同一条**前两步（plan.EnvFor → plan.Validate →
// plan.Execute），但**不提交、不落报告**：唯一使用者是对账的 R2 修复桥
// （reconcile_repair_reviewed.go）。
//
// 为什么必须有这条无提交入口（A-35）：一次对账里 R1 纳管与 R2 修复写入**合并为恰一次**
// commit，而那一次 commit 归纳管写口（reconcile_commit.go）在编排末尾产生。修复桥若复用
// 会自带一次 commit 的 runPlan，同一次对账就会出现两条提交 —— 判据是「恰 +1」而不是「+2」。
//
// 三条与 runPlan 逐字相同、**一格不放宽**的事实：校验失败 → 零写入（本函数直接返回
// 校验结果，调用方据此零写入）；B3 的 ExpectedHash 仍由 plan.Validate 从 base 写进 action；
// 跳过与失败仍如实落在 ExecResult 的 skipped[] / failures[] 里，不吞、不折叠。
//
// userRequest 是**进程边界**注入的授权佐证（授权合同 N-1）：与 runPlan 里
// `env.UserRequest = AuthorizationOf(inv).UserRequest` 同一格，绝不由 plan 内容自证。
// stamp 由调用方注入（而不是本函数读时钟）：补写进 frontmatter 的时刻因此可复算，
// 且与调用方回报的事实逐字同值。
func executePlanNoCommit(root string, userRequest bool, stamp model.Stamp,
	p *plan.ChangePlan) (*plan.Result, *plan.ExecResult, error) {
	return executePlanNoCommitOnStore(store.New(root), root, userRequest, stamp, p)
}

// executePlanNoCommitOnStore 是 executePlanNoCommit 的**注入 Store** 版：把要落地的 Store
// 从参数传入，其余行为与 executePlanNoCommit 逐字相同。
//
// 抽出这一层是为对账事务底座（C5）准备：R2 / R6 修复桥要把与编排同一把 **atomic** Store
// 注入进来，让「校验 → 执行」的写只 stage 到该 Store 的 overlay，而不是各自 store.New(root)
// 另起一份实盘写口。root 仍留在签名里，供调用方按需组织注入；取数与写入口径统一走注入的 st。
func executePlanNoCommitOnStore(st *store.Store, root string, userRequest bool, stamp model.Stamp,
	p *plan.ChangePlan) (*plan.Result, *plan.ExecResult, error) {
	_ = root // 保留在签名里供调用方按 root 组织注入；本函数的取数口径统一走注入的 st
	env, err := plan.EnvFor(st, "")
	if err != nil {
		return nil, nil, err
	}
	env.UserRequest = userRequest
	pres := plan.Validate(p, env)
	if pres.Failed() {
		// 零写入：executor 一次都不跑（与 runPlan 的 ① 分支同一口径）。
		return pres, &plan.ExecResult{}, nil
	}
	ex := plan.Execute(st, pres, plan.ExecOptions{
		Stamp: stamp, Date: model.NewDate(stamp.Time()),
		Base: p.Base, Index: &env.Index,
	})
	return pres, ex, nil
}

// planNoCommitExecutor 是「无提交校验 + 执行」入口的函数类型，签名与 executePlanNoCommit
// 逐字相同。抽成命名类型是为对账事务底座（C5）：R2 / R6 修复桥默认注入 executePlanNoCommit
// （行为不变），后续 reconcile 可换注入一个绑定同一把 **atomic** Store 的实现，从而让修复的
// 「校验 → 执行」写只 stage 到编排 overlay，而不是各自另起实盘写口。
type planNoCommitExecutor func(root string, userRequest bool, stamp model.Stamp,
	p *plan.ChangePlan) (*plan.Result, *plan.ExecResult, error)

// planBase 按 `eg context` 的**同一口径**为若干 ID 计算 plan.base（id → content_hash）。
//
// B3 的版本依据只有一份实现——`store.ContentHash`；这里只是把它按 ID 取用，
// 不另起第二套哈希算法（`eg context` 是 Agent 链路的取数口径，`eg rel add` 是单命令闭环，
// 两者算出的 hash 必须逐字相同）。解析不到的 ID 原样跳过：它们由 plan 校验按 E2 处理。
func planBase(root string, ids []string) (map[string]string, map[string]string, error) {
	base, paths := map[string]string{}, map[string]string{}
	st := store.New(root)
	idx, err := st.ScanIDs()
	if err != nil {
		return nil, nil, err
	}
	for _, id := range ids {
		rel, err := idx.Resolve(id)
		if err != nil {
			continue
		}
		paths[id] = rel
		f, err := st.Read(rel)
		if err != nil {
			continue
		}
		base[rel] = store.ContentHash(f.Bytes)
	}
	return base, paths, nil
}

// domainOfPath 复用 store 的唯一实现，从 vault 内相对路径取领域（EG-DOM-01：
// 领域由**目录**唯一决定，绝不从 frontmatter 猜）。
func domainOfPath(rel string) string { return store.DomainOf(rel) }

// rebaseSelfComputedBase 在**临界区内、S2 恢复屏障之后**重采写前凭据，且只重采
// 「CLI 自算 ∩ 本次恢复真的回滚过」的那几个文件（I-…-022）。
//
// 两道收窄各挡一类误伤，缺一不可：
//
//	① 只认 `inv.selfComputedBase(ids)` 声明过来源的 plan（`eg deprecate` / `eg restore` /
//	   `eg replaced-by` / `eg edit` / `eg rel add|remove`）。`eg apply --plan` 的 base 是
//	   用户/Agent **显式声明的期望版本**，重采它等于把 B3 改成永真，陈旧 base 再也拦不住。
//	② 只认 restored 点名的路径（txn.RecoverResult.Restored）。前像变化的来源不止一种：
//	   恢复回滚是「本次请求自己触发的清理」，而并发写者提交、用户在 Obsidian 里手改
//	   都是**真冲突**，必须继续走 B3 跳过 + 退 3。
//
// 于是这不是「豁免 B3」，而是**修正采样时刻**：单命令闭环里用户没有声明期望版本，base 只是
// CLI 为了走 B3 在 S0（锁外）顺手采的一次前像；S2 把文件回滚回前像之后，那次采样对这些
// 路径已然过期，拿它去比就是把「恢复本身」判成 content_hash_mismatch，让崩溃后第一条正常写
// 命令必然退 3、零写入。重采之后 B3 依旧逐字比对，一条校验都没少。
//
// 就地改写 p.Base（不换 map）是有意的：`eg rel add` 这类命令的 `data.plan` 与 ChangePlan
// 共享同一份 base map，换 map 会让产物里回显的 base 与真正执行的 base 分叉 —— 那才是漏报。
func (r *Root) rebaseSelfComputedBase(inv *Invocation, p *plan.ChangePlan,
	restored []string, rep *report.Report) error {
	if p == nil || len(inv.selfBaseIDs) == 0 || len(restored) == 0 {
		return nil // 没有自算 base、或本次恢复一个字节都没回滚 ⇒ 一律不动 base。
	}
	if p.Base == nil {
		p.Base = map[string]string{}
	}
	fresh, _, err := planBase(inv.VaultRoot, inv.selfBaseIDs)
	if err != nil {
		return err
	}
	// 只取「恢复真的动过」且「与本次写目标相关」的交集：多一个路径都不重采。
	//
	// 相关性两侧都算，缺一不可：锁内新解析出来的（fresh）与锁外已采到的（p.Base）。
	// 前者尤其必要 —— 崩溃可能把目标卡整份字节替换成非卡内容，锁外那次 planBase 连 ID 都
	// 解析不到，于是 base **缺键**；恢复把卡还原之后 B3 只会说「base 未覆盖该文件」，
	// 这正是 I-…-022 实测到的形态（缺键，不是 hash 不等）。只补更新已有键补不上这一格。
	scope := make([]string, 0, len(restored))
	for _, rel := range restored {
		_, inFresh := fresh[rel]
		_, inBase := p.Base[rel]
		if inFresh || inBase {
			scope = append(scope, rel)
		}
	}
	if len(scope) == 0 {
		return nil // 恢复回滚的文件与本次写目标无关：base 保持原样，B3 照旧。
	}
	changed := rebaseScoped(p.Base, fresh, scope)
	if len(changed) == 0 {
		return nil // 常态：回滚后的字节与锁外采样逐字相同，不打噪声。
	}
	// 改了就说：重采是事实变更，必须留在报告里可审计（纯诊断，不产码、不改退出码）。
	rep.AddWarning(unnumberedWarning("plan.base",
		"写前凭据已按崩溃恢复后的现态重采（这些文件刚由 S2 回滚到前像，锁外采样随之过期）：%s；"+
			"B3 仍逐字比对，非恢复造成的不一致一律照旧跳过", strings.Join(changed, "、")))
	return nil
}

// rebaseScoped 只把 scope 点名的路径按 fresh 就地覆盖进 dst，返回真的变了的路径（升序）。
//
// scope 之外的键一个不碰；scope 内在锁内已不可解析的键则删除，好让 B3 与 E2 各按自己的
// 职责报，而不是拿一个凭空的旧 hash 去比一个已经不存在的文件。
func rebaseScoped(dst, fresh map[string]string, scope []string) []string {
	var changed []string
	for _, rel := range scope {
		nh, ok := fresh[rel]
		switch {
		case !ok:
			changed = append(changed, rel)
			delete(dst, rel)
		case dst[rel] != nh:
			changed = append(changed, rel)
			dst[rel] = nh
		}
	}
	sort.Strings(changed)
	return changed
}

// applyDryRun 输出「将写入的文件与分区清单」，零写入零 commit（合同 §2）。
func (r *Root) applyDryRun(rep report.Report, pres *plan.Result) *Result {
	var planned []map[string]interface{}
	for _, a := range pres.Actions {
		if a.Skip {
			rep.Skipped = append(rep.Skipped, report.Skipped{
				Kind: string(a.SkipKind), Target: a.ID, Locator: a.Path,
				Cause: a.SkipCause, Detail: a.SkipDetail,
			})
			continue
		}
		// I-…-002：dry-run 的卡计数与 planned[] **同源**（都只读 pres.Actions），
		// 不再让报告体一侧空着计数器却照打零卡说明。这里只镜像执行器真正记进
		// cards.* 的两条形态（executor.cardNew → cards.created、
		// executor.cardAppend → cards.updated）；其余写形态执行器也不记 cards.*，
		// 这里同样不记 —— 两条路径同一把账本，不多数一张也不少数一张。
		switch a.Kind {
		case plan.ActCardNew:
			rep.Cards.Created = append(rep.Cards.Created, a.ID)
		case plan.ActCardAppend:
			rep.Cards.Updated = append(rep.Cards.Updated, a.ID)
		}
		if a.Path == "" {
			continue
		}
		if len(a.Sections) == 0 {
			planned = append(planned, map[string]interface{}{
				"path": a.Path, "section": "", "op_index": a.OpIndex})
			continue
		}
		for _, s := range a.Sections {
			planned = append(planned, map[string]interface{}{
				"path": a.Path, "section": s.Section, "op_index": a.OpIndex})
		}
	}
	rep.Links = pres.Targets()
	if rep.Links == nil {
		rep.Links = []string{}
	}
	rep.SetCommit("")
	rep.NoteZeroCards("cards")
	res := applyResult(rep, pres, planned)
	res.Summary = append([]string{"--dry-run：零写入、零 commit，以下是将写入的清单"}, res.Summary...)
	return res
}

// fillReport 把执行事实逐条填进报告：**不折叠、不去重丢失**（数量守恒）。
func fillReport(rep *report.Report, ex *plan.ExecResult) {
	rep.Source = report.Source{ID: ex.Source.ID, Path: ex.Source.Path}
	rep.Note = report.Note{ID: ex.Note.ID, Domain: ex.Note.Domain, Path: ex.Note.Path,
		Reprocessed: ex.Note.Reprocessed}
	rep.Cards.Created = appendAll(rep.Cards.Created, ex.CardsCreated)
	rep.Cards.Updated = appendAll(rep.Cards.Updated, ex.CardsUpdated)
	if ex.Note.Reused {
		rep.Note.Path = ex.Note.Path
	}
	rep.Cards.Reused = appendAll(rep.Cards.Reused, ex.CardsReused)
	for _, m := range ex.Material {
		rep.Relations.Material = append(rep.Relations.Material, report.Material{
			Card: m.Card, Source: m.Source, Note: m.Note, Rel: m.Rel, Reason: m.Reason})
	}
	for _, k := range ex.Knowledge {
		rep.Relations.Knowledge = append(rep.Relations.Knowledge, report.Knowledge{
			From: k.From, Type: k.Type, Target: k.Target, Reason: k.Reason})
	}
	for _, q := range ex.Questions {
		rep.OpenQuestions = append(rep.OpenQuestions, report.OpenQuestion{
			Note: q.Note, Question: q.Question})
	}
	rep.Links = appendAll(rep.Links, ex.Written)
	for _, s := range ex.Skipped {
		rep.Skipped = append(rep.Skipped, report.Skipped{
			Kind: string(s.Kind), Target: s.Target, Locator: s.Locator,
			Cause: s.Cause, Detail: s.Detail})
	}
	for _, h := range ex.HighImpact {
		rep.HighImpact = append(rep.HighImpact, report.Impact{
			Kind: h.Kind, Target: h.Target, Detail: h.Detail})
	}
	for _, d := range ex.Warnings {
		rep.AddWarning(toReportDiag(d))
	}
	// I-…-010：Failures **不再**同时登记进 report.warnings[]。它们已由 execDiagnostics
	// 以 level=error 进 data.errors[]（唯一权威登记点）；两处都登记会让同一条文本既是 error
	// 又是 warning，诊断区语义自相矛盾，Agent 也无法判断该不该重投。
}

// addCoverageGapInfos 把 write_note 的 coverage_gaps 逐项原样透传成 I1 info
// （EG-EXT-02：只透传，不推断、不补全；缺失或空数组 → 一条都不输出）。
func addCoverageGapInfos(rep *report.Report, pres *plan.Result) {
	for _, a := range pres.Actions {
		for _, gap := range a.Gaps {
			name := coverageGapNames[gap]
			if name == "" {
				name = "受控枚举外的取值，已原样保留"
			}
			rep.AddInfo(fmt.Sprintf("ops[%d].coverage_gaps", a.OpIndex), a.OpIndex,
				"提炼覆盖项缺失：%s（%s），已原样透传进报告", gap, name)
		}
	}
}

// applyResult 组装命令结果：报告体在 data.report，执行元信息与 plan 回带字段平铺在 data。
func applyResult(rep report.Report, pres *plan.Result, planned []map[string]interface{}) *Result {
	data := map[string]interface{}{"report": rep}
	if pres != nil {
		data["domain"] = pres.Domain
		data["convergence"] = convergenceData(pres.Convergence)
	} else {
		data["domain"] = ""
		data["convergence"] = []map[string]interface{}{}
	}
	if planned != nil {
		data["planned"] = planned
	}
	res := &Result{Data: data, Warnings: toCLIReportDiags(rep.Warnings)}
	res.Summary = rep.Lines()
	// 报告体行之后追加逐卡收敛结论：渲染走 convergence.go 的唯一实现
	// （`eg report --last` 回放时调的是同一个 convergenceLines，两条路径逐字相等）。
	if pres != nil {
		res.Summary = append(res.Summary,
			convergenceLines(convergenceItemsFromPlan(pres.Convergence))...)
	}
	return res
}

// skippedDiags 把跳过与失败转成退 3 的诊断载荷（调用方不得吞掉，B3）。
//
// # cause 不占 code 位（I-…-015）
//
// 原实现把 `skipped[].cause`（`content_hash_mismatch` / `user_block_not_preserved`）直接塞进
// `code`，越出 `E*`/`W*`/`I*`/`Q*` 编号域：按码前缀路由的调用方（「`E` 开头 → 修 plan 重投」）
// 会命中 default 分支或直接崩。现在 `code` 恒为 **E24**（本次跳过未写入），而 `kind` 与
// `cause` **两个都**如实进 `message` —— 归因一个字都没丢，只是回到了它该在的位置：
// 机器可读的枚举仍在 `report.skipped[].kind` / `.cause`，`data.errors[]` 侧则同时可读。
func skippedDiags(ex *plan.ExecResult) []Diagnostic {
	var out []Diagnostic
	for _, s := range ex.Skipped {
		out = append(out, Diagnostic{
			Code: E24, Level: LevelError, Path: s.Locator, OpIndex: s.OpIndex,
			Message: fmt.Sprintf("%s（kind=%s，cause=%s）", s.Detail, s.Kind, s.Cause),
			Target:  s.Target,
		})
	}
	for _, f := range ex.Failures {
		out = append(out, Diagnostic{
			Code: f.Code, Level: LevelError, Path: f.Path, OpIndex: f.OpIndex,
			Message: f.Message, Target: f.Target,
		})
	}
	return out
}

func commitFailureMessage(err error) string {
	var failed *git.CommitFailed
	if errors.As(err, &failed) {
		msg := "Git 提交失败：" + failed.Reason + "；磁盘保留当前状态，未做任何还原（B4）"
		if len(failed.Uncommitted) > 0 {
			msg += "；未提交清单：" + strings.Join(failed.Uncommitted, "、")
		}
		return msg
	}
	return "Git 提交失败：" + err.Error() + "；磁盘保留当前状态，未做任何还原（B4）"
}

// applySubject 生成 commit 主题的事实部分（只复述已发生的写入，不加评价）。
func applySubject(ex *plan.ExecResult) string {
	return fmt.Sprintf("写入 %d 个文件（新建卡 %d、补充卡 %d、关系 %d、未决问题 %d）",
		len(ex.Written), len(ex.CardsCreated), len(ex.CardsUpdated),
		len(ex.Material)+len(ex.Knowledge), len(ex.Questions))
}

// commitDomain 返回提交主题的域位；plan.domain 为 null（全局操作）时写 `-`（合同 §6）。
func commitDomain(domain string) string {
	if strings.TrimSpace(domain) == "" {
		return "-"
	}
	return domain
}

// planBytes 读取 plan 字节：`-` 走 stdin（Root.In 可注入），否则读文件。
func (r *Root) planBytes(inv *Invocation) ([]byte, error) {
	src := inv.String("plan")
	if src == "-" {
		in := r.In
		if in == nil {
			in = os.Stdin
		}
		raw, err := io.ReadAll(in)
		if err != nil {
			return nil, &UsageError{Msg: "读取 stdin 的 plan 失败：" + err.Error()}
		}
		return raw, nil
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		return nil, &UsageError{Msg: fmt.Sprintf("--plan 不可读：%v", err)}
	}
	return raw, nil
}

// repo 返回该 vault 的 Git 仓（可注入，供提交失败用例）。
func (r *Root) repo(root string) *git.Repo {
	if r.NewRepo != nil {
		return r.NewRepo(root)
	}
	return git.New(root)
}

// saveReport 把本次报告落到 vault 的工程状态目录，供 `eg report --last` 逐字复现。
//
// 落点不在 domains/** 或 sources/** 之下（报告不是知识产物，EG-AGT-04），
// 且在 commit **之后**写、并登记进仓库本地忽略清单，因此既不进本次 commit，
// 也不会污染下一次 apply 的工作区。写失败只出 warning，不影响本次退出码。
func (r *Root) saveReport(root string, res *Result, code int) {
	r.saveReportBody(root, res, res.Data, code)
}

// saveReportBody 与 saveReport 同一套落盘语义，但允许调用方另给**报告体容器**。
//
// 为什么需要它：`eg capture` 的信封 `data` 是收录事实的封闭键集（§1.3 的 7 + 1 键，
// 不含 `report`），而 `eg report --last` 复现的是 §4.6 报告体。两者不是同一张表，
// 因此 capture 把报告体单独交给这里落盘 —— 信封键面逐字不变，记录里有真报告体
// （I-…-018）。诊断（含落盘失败那条 warning）仍然挂在 res 上，一条都不丢。
func (r *Root) saveReportBody(root string, res *Result, data map[string]interface{}, code int) {
	if err := r.repo(root).Exclude(StateDir + "/"); err != nil {
		res.Warnings = append(res.Warnings, Diagnostic{
			Level: LevelWarning, Path: git.ExcludeRel, OpIndex: NonOpDiagnostic,
			Message: "登记忽略清单失败，报告仍已落盘：" + err.Error()})
	}
	if err := writeLastReport(root, r.cmdSurface, res, data, code); err != nil {
		res.Warnings = append(res.Warnings, Diagnostic{
			Level: LevelWarning, Path: LastReportFile, OpIndex: NonOpDiagnostic,
			Message: "最近一次报告落盘失败（不影响本次写入与提交）：" + err.Error()})
	}
}

func appendAll(dst []string, src []string) []string {
	for _, s := range src {
		dst = append(dst, s)
	}
	return dst
}

// toReportDiag / toCLIDiags / toCLIReportDiags 在三套同构诊断结构间做**无损**搬运：
// internal/plan、internal/report、internal/cli 各自不认识对方的类型（依赖方向 §13），
// 字段一一对应，条目数守恒。
func toReportDiag(d plan.Diagnostic) report.Diagnostic {
	return report.Diagnostic{Code: d.Code, Level: d.Level, Path: d.Path,
		OpIndex: d.OpIndex, Message: d.Message, Target: d.Target}
}

func toCLIDiags(list []plan.Diagnostic) []Diagnostic {
	out := make([]Diagnostic, 0, len(list))
	for _, d := range list {
		out = append(out, Diagnostic{Code: d.Code, Level: d.Level, Path: d.Path,
			OpIndex: d.OpIndex, Message: d.Message, Target: d.Target})
	}
	return out
}

func toCLIReportDiags(list []report.Diagnostic) []Diagnostic {
	out := make([]Diagnostic, 0, len(list))
	for _, d := range list {
		out = append(out, Diagnostic{Code: d.Code, Level: d.Level, Path: d.Path,
			OpIndex: d.OpIndex, Message: d.Message, Target: d.Target})
	}
	return out
}

// stateAbs 返回状态文件的绝对路径。
func stateAbs(root, rel string) string {
	return filepath.Join(root, filepath.FromSlash(rel))
}
