package cli

// `eg delete --target <id> --reason <text> --proposal <pid> --confirm --user-request` 的
// **命令侧编排**：提案与状态合同 §5.3 时序第 7 / 8 / 9a / 9b / 10 / 11 步、§5.5 历史保留规则、
// 授权合同 §2 矩阵「逻辑删除」行与 §10.3 的 V9 / V10；T-evergreen.s1_main_flow-158614-041。
//
// # 为什么本命令是 CLI 直写例外（A-23 已裁决）
//
// 逻辑删除要按同一本账（PathLedger）如实回写提案的 `execution`；ChangePlan 的
// plan.Execute 是「一次计划一次执行」的整体语义，套进来会把「哪些路径在这次删除范围里」
// 这件事从账本里挤掉。因此本命令**直读 proposals/ 再直写**，但写口仍然唯一：
// 状态落盘只经 `store.ApplyStateWrite`（写口唯一护栏 TestStateWritePortIsUniqueToStore
// 要求 SetDeleted 等 setter 的非测试命中全部落在 internal/store/）。
//
// # 三个不可放宽的前置（缺一即拒，且目标文件字节不变）
//
//  1. **用户发起**：`initiator: user` 且有**真实的命令行佐证** `--user-request`
//     （授权合同 N-1 反伪造条款：文件内容不能自证）。删除与 approve 同级别高风险，
//     因此**不做**进程边界自动置真（deprecate / restore 那套宽松口径在这里不适用）。
//  2. **必须引用 `status=approved` 的提案**：无 `--proposal` / `pending` / `rejected` /
//     `superseded` 一律是 **W7 后半（≡ V10）升 error** → 退 2、零写入。
//  3. **`--confirm`**：校验全过、仅缺确认 → 退 **6**，权威 Markdown 完全不变
//     （零写入、零 commit、不落 last-report）。判定顺序 1 → 2 → 6 由 ExitCodeForConfirm 锁死。
//
// # 两条最容易做错的不变式
//
//   - **status 绝不被自动改变**（§9 / §5.5 逐字判定行）：本命令只写 `deleted_at` +
//     `deleted_reason`，报告里逐字输出 report.NoAutoStatusChangeNotice。失去有效 support
//     的卡只拿到「建议标记 deprecated」这条**建议**，用户不处理时它们仍是 active。
//   - **U-01 无物理删除 / 不级联删关系**：不做任何文件系统层面的产物抹除，
//     `relations[]` / `sources[]` / `opposing` / `replaced_by` 的记录一条不删
//     （关系过滤靠端点有效性，记录不动）。
//
// # 阶段边界（本 task 不做）
//
//   - 不实现 `eg undelete`（下一个 agent）；不产出 `[已删除]` 标记与检索过滤（T-…-043）；
//   - 不做综述失准的自动判定，也不判定 S3 那个「综述材料是否足够」的标记。
//
// # [M6 · T-…-072 批次 C3a] 事务化：删除分支从「逐文件」升为**全有或全无**
//
// M3 的形态是 §5.3 第 9b 步「逐文件 = 非强原子」（R-9：M3 不引入强原子写）：中途失败
// 停在当前位置，已写的留着、`execution = failed` 逐路径在册。M6 原子性合同 §16.1 把本
// 命令归入 A 类（产生权威写入的写命令），该让步随之作废 —— 一次逻辑删除的原子域恰是
// 「**全部** target 文件 + 那份提案的 execution 块」，全程只持**同一把**
// `.index/run.lock`：
//
//	S0 锁外只做 V9 授权佐证（不碰库内事实）
//	S1 取 run.lock
//	S2 崩溃恢复屏障（临界区内第一件事，早于任何一次业务读）
//	S3 锁内重读库内事实：Store → ScanIDs → 提案 → targets → 确认门 → 重算影响面
//	S4 Store atomic overlay 预演：全部 target 走唯一写口 applyStateWrite，
//	   同一个 overlay 内接着回写提案 execution=succeeded；任一步失败即丢弃 overlay
//	S5 AllocateTxnID → RecordTxnID → WriteIntent（发布屏障）
//	S6 txn.Commit：commit marker 落盘之前，任何一处删除标记都不算生效
//	S7 Git commit（仍在锁内、严格晚于 commit marker）
//	S8 唯一 syncIndexAfterWrite（Git 成败都走，仍在锁内、早于 Release）
//	S9 释放锁
//
// 由此，M3 那条「已写 N 个、未写 M 个」的中间态在删除分支上**不再可能出现**：预演期
// 任一写口失败 ⇒ overlay 整体丢弃、不开事务、不发 intent、不跑 Git，全部 target 与提案
// 文件字节一格不动，退 3。**不再有** `execution = failed` 这次二次写：既然一个字节都没
// 生效，把提案改判成「尝试过并失败」反而是伪造事实（写它本身也是一次权威写）。
// Git 失败同理不回滚、不二次写：commit marker 已定盘，退 4、保留删除态与 succeeded。
//
// 本批次**只**原子化「影响面未变化」的真实删除分支：第 9a 步 superseded 改判仍是旧形态
// （下一批处理），它一个知识产物都不写。

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/git"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/proposal"
	"github.com/ikaqiu-Lemon/EverGreen/internal/report"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// 参数名（与 deleteCommand 的 flag 注册同一份字面量）。
const (
	// DeleteConfirmFlag 是执行前**唯一一次**确认（EG-CFM-05）。
	DeleteConfirmFlag = "confirm"
	// DeleteProposalFlag 点名被引用的提案：删除必须有一份 status=approved 的提案背书。
	DeleteProposalFlag = "proposal"
)

// 固定文案（措辞固定便于用例与后续 e2e 逐字断言）。
const (
	// DeleteNeedConfirmMsg 是缺确认时的说明：6 不是失败，是等待用户点头。
	DeleteNeedConfirmMsg = "eg delete 需要用户二次确认：请重跑并加 --confirm（本次零写入、零 commit，权威 Markdown 完全不变）"
	// DeleteAgentDeniedMsg 是 V9 守卫文案（退 2、零写入）。
	DeleteAgentDeniedMsg = "U-01：Agent 不得发起逻辑删除 —— 删除必须由用户显式发起并二次确认"
	// DeleteNeedApprovedProposalMsg 是 W7 后半（≡ V10）升 error 的文案（退 2、零写入）。
	DeleteNeedApprovedProposalMsg = "eg delete 必须引用一份 status=approved 的提案（W7 后半 ≡ V10：无提案 / pending / rejected / superseded 一律拒绝执行）"
)

// deleteCommand 注册 `eg delete`（授权合同 §2 矩阵「逻辑删除」行：P-A 🔴、P-U ✅ + 需确认）。
//
// `--proposal` 缺失**不在** Validate 里判：那是事实问题（校验失败退 2），
// 不是参数形态问题（退 1）。两类失败不得互相冒名，否则退出码 6 的「校验已通过」前提会被稀释。
func deleteCommand() *Command {
	return &Command{
		Name:    "delete",
		Display: "delete",
		Summary: "逻辑删除（写 deleted_at / deleted_reason；须引用 approved 提案 + --confirm；commit verb=delete）",
		Owner:   "T-evergreen.s1_main_flow-158614-041",
		Usage: `eg delete --target <id> --reason <text> --proposal <p-id> --confirm --user-request [--json]

参数：
  --target <id>      是；被删对象（四类产物同构：知识卡 / 材料笔记 / 原文 / 综述）
  --reason <text>    是；删除理由（落到 deleted_reason）
  --proposal <p-id>  是；必须引用一份 status=approved 的提案（缺失 / 非 approved → 退 2、零写入）
  --confirm          是；执行前唯一一次确认；缺它且校验已通过 → 退 6，权威 Markdown 完全不变
  --user-request     是（全局 flag）；本次调用由用户显式发起（文件内容不能自证，N-1）

只写 deleted_at + deleted_reason 两个键：status 绝不被自动改变；关系记录一条不删
（无物理删除，U-01；不级联删除关系）。全部 target 与提案 execution 同进一次原子事务：
中途任一处写不成 ⇒ 一个字节都不生效（退 3），不会留下「删了一半」的库。
退出码：0 | 1 参数非法（零写入） | 2 校验失败（零写入） | 3 未生效（全部目标字节不变） |
        4 Git 提交失败（删除已原子生效、保留现状） | 6 仅缺 --confirm（权威 Markdown 完全不变）
`,
		Flags: func(fs *flagSet) {
			fs.String("target", "", "被删对象 ID")
			fs.String("reason", "", "删除理由")
			fs.String(DeleteProposalFlag, "", "被引用的 approved 提案 ID")
			fs.Bool(DeleteConfirmFlag, false, "执行前唯一一次确认")
		},
		Validate: func(inv *Invocation) error {
			return requireStateTargetAndReason(inv, "delete")
		},
	}
}

// deleteConfirmed 读 `--confirm` 的**当前取值**：`--confirm=false` 必须被当成没有确认，
// 否则确认门会被一个否定式写法绕过（与 approveConfirmed 同一口径）。
func deleteConfirmed(inv *Invocation) bool {
	ok, err := strconv.ParseBool(inv.String(DeleteConfirmFlag))
	if err != nil {
		return false
	}
	return ok
}

// runDelete 是 `eg delete` 的唯一入口。S0（锁外）只做一件事：授权佐证。
//
// 其余每一步都在临界区里（见文件头 S1~S9）：库内事实必须在**取锁并跑完崩溃恢复之后**
// 才第一次读 —— S2 可能刚把某个 target 回滚到前像（连它还在不在删除范围里都会变）。
func (r *Root) runDelete(inv *Invocation) (*Result, error) {
	target := strings.TrimSpace(inv.String("target"))
	reason := strings.TrimSpace(inv.String("reason"))

	// S0 · V9：删除是本仓最高风险的写动作，要求**命令行**佐证。
	// 为什么这里不像 eg deprecate 那样在进程边界自动置真：deprecate 可由用户再次 restore 回退，
	// 而删除的替代路径只有 undelete，授权合同 §10.3 V9 明确要求 initiator=user 且不随宽松口径放宽。
	// 这一格只看本次进程的调用形态，不读库，因此留在锁外（不为一次必然拒绝去抢锁）。
	if !inv.UserRequest {
		return nil, deleteDenied(target)
	}

	// 报告体在进临界区之前建好并全程传引用：S1 的 W28、S2 的 W26 讲的是「进临界区时
	// 库里发生过什么」，与本次写了什么无关，必须落进最终产物。
	rep := report.New()
	return r.deleteCritical(inv, &rep, target, reason)
}

// deleteCritical 是 `eg delete` 的整个临界区：进函数即取锁，出函数即释放锁。
//
// 固定次序（§5.3 时序在锁内重放一遍）：
//
//	① 目标与提案的事实复核（提案必须 approved；否则退 2、零写入）
//	② 确认门：缺 --confirm → 退 6、零写入零 commit、不落 last-report
//	③ 第 8 步执行前重算影响面；已变 → 第 9a 步：**不执行删除** + 原提案 superseded + 新提案
//	④ 第 9b 步：全部 target + 提案 execution 一次原子提交（S4~S8，见 deleteExecute）
//
// 报告渲染与退出码裁决暂时留在锁内（C3a 只做写链；锁外渲染是下一批的事）。
func (r *Root) deleteCritical(inv *Invocation, rep *report.Report,
	target, reason string) (*Result, error) {
	// —— S1 + S2：取锁、崩溃恢复屏障（与 plan 写链 / capture / undelete 共用同一把锁）。——
	sess, serr := r.enterTxnCritical(inv, reportWarnSink{rep}, txnCriticalOpts{
		ZeroWrite: "本次零写入", ReleaseNotice: "本次写入与提交不受影响",
	})
	if serr != nil {
		return nil, serr
	}
	defer sess.release()

	// —— S3：锁内重建 Store、重新全库发现、重读提案与目标。——
	//
	// 严禁把这几步挪到锁外：拿锁外快照当 approved 判定、当影响面比对的基准、
	// 当 B3 守卫的期望哈希，等于把「执行前复核」变成一句空话。
	st := store.New(inv.VaultRoot)
	idx, err := st.ScanIDs()
	if err != nil {
		return nil, &ValidationError{Msg: "扫描 vault 失败（零写入）：" + err.Error()}
	}

	// ① 提案守卫 + 目标解析：两者都是**事实**问题，不成立即退 2、零写入。
	rec, perr := r.deleteProposal(st, inv)
	if perr != nil {
		return nil, perr
	}
	p := rec.Proposal()
	rels, rerr := deleteTargets(idx, rec, target)
	if rerr != nil {
		return nil, rerr
	}

	// ② 确认门：三个事实如实填表，顺序裁决交给 ExitCodeForConfirm（1 → 2 → 6 → 0）。
	// 走到这里前两项已由前面的阶段证成，因此恒为 true。
	decision := ConfirmDecision{
		ArgsValid:        true,
		ValidationPassed: true,
		ConfirmMissing:   !deleteConfirmed(inv),
	}
	if code := ExitCodeForConfirm(decision); code != ExitOK {
		if code != ExitNeedConfirm {
			panic(fmt.Sprintf("确认门只应产出 %d，实得 %d：判定顺序被改动", ExitNeedConfirm, code))
		}
		// 零写入、零 commit、**不落 last-report**：last-report 也是文件，
		// 「权威 Markdown 完全不变」不允许本次调用留下任何写痕。
		return nil, &NeedConfirmError{
			Command: CmdDelete,
			Msg: fmt.Sprintf("%s（目标 %s，提案 %s）",
				DeleteNeedConfirmMsg, target, rec.ID),
			Diags: []Diagnostic{{
				Code: E19, Level: LevelError, Path: rec.Rel, OpIndex: NonOpDiagnostic,
				Message: DeleteNeedConfirmMsg, Target: target,
			}},
		}
	}

	// ③ 第 8 步：执行前**重算影响面**并与提案比对（不得省略）。
	recomputed, ierr := proposal.RecomputeImpact(st, p.Targets)
	if ierr != nil {
		return nil, &ValidationError{Msg: fmt.Sprintf(
			"重算提案 %s 的影响面失败（零写入）：%v", rec.ID, ierr)}
	}
	fireTxnStep(TxnStepReread, "")
	if diff := proposal.ImpactDiff(p.Impact, recomputed); len(diff) > 0 {
		// 第 9a 步：前提已变 → **不执行删除**，原提案 superseded + 生成新提案（触发②）。
		// 这一支一个知识产物都不写，C3a 暂不改造（仍是旧形态，只是搬进了锁内）。
		return r.deleteSuperseded(inv, sess, rep, st, rec, recomputed, diff, reason)
	}

	// ④ 第 9b 步：全部 target + 提案 execution 一次原子提交。
	return r.deleteExecute(inv, sess, rep, st, idx, rec, rels, target, reason)
}

// deleteDenied 是 V9 不成立时的带类型错误（退 2、零写入；U-01 的替代路径一并给出）。
func deleteDenied(target string) error {
	msg := DeleteAgentDeniedMsg
	if item, ok := UnauthorizableNum("U-01"); ok {
		msg = fmt.Sprintf("%s（%s）；替代路径：%s", DeleteAgentDeniedMsg, item.Basis, item.Alternative)
	}
	return &ValidationError{
		Msg: fmt.Sprintf("%s。请加 --%s 表明本次调用由用户显式发起（提案文件里写 initiator: user 不能自证，N-1）",
			msg, UserRequestFlag),
		Diags: []Diagnostic{{
			Code: E19, Level: LevelError, Path: "--" + UserRequestFlag, OpIndex: NonOpDiagnostic,
			Message: DeleteAgentDeniedMsg, Target: target,
		}},
	}
}

// deleteProposal 取出被引用的提案并复核它是 `approved`。
//
// 三种不成立情形（无 `--proposal` / 库里没有 / status ≠ approved）**同一后果**：
// 退 2 且目标文件字节不变 —— 删除只在有 approved 提案背书时才可能发生。
func (r *Root) deleteProposal(st *store.Store, inv *Invocation) (proposal.Record, error) {
	var zero proposal.Record
	pid := strings.TrimSpace(inv.String(DeleteProposalFlag))
	if pid == "" {
		return zero, &ValidationError{
			Msg: DeleteNeedApprovedProposalMsg + fmt.Sprintf("：本次未给 --%s", DeleteProposalFlag),
			Diags: []Diagnostic{{
				Code: E19, Level: LevelError, Path: "--" + DeleteProposalFlag,
				OpIndex: NonOpDiagnostic, Message: DeleteNeedApprovedProposalMsg,
			}},
		}
	}
	rec, err := proposal.Load(st, proposal.ID(pid))
	if err != nil {
		return zero, proposalLoadError(pid, err)
	}
	if got := rec.Proposal().Status; got != proposal.StatusApproved {
		return zero, &ValidationError{
			Msg: fmt.Sprintf("%s：提案 %s 当前 status=%s", DeleteNeedApprovedProposalMsg, rec.ID, got),
			Diags: []Diagnostic{{
				Code: E19, Level: LevelError, Path: rec.Rel, OpIndex: NonOpDiagnostic,
				Message: DeleteNeedApprovedProposalMsg, Target: string(rec.ID),
			}},
		}
	}
	return rec, nil
}

// deleteTargets 把提案 targets 解析成**影响文件全集**（逐文件写入的封闭集合）。
//
// 为什么全集取自提案而不是 `--target`：批准的是那份提案描述的影响面，执行必须与被批准的
// 范围一致；`--target` 只用来复核「用户点名的对象确实在这份提案里」，防止拿 A 的批准去删 B。
// 任一 target 解析不到文件 → 退 2、零写入（宁可不执行，也不执行一半范围）。
func deleteTargets(idx store.Index, rec proposal.Record, target string) ([]string, error) {
	p := rec.Proposal()
	inProposal := false
	rels := make([]string, 0, len(p.Targets))
	for _, t := range p.Targets {
		if t == target {
			inProposal = true
		}
		rel, err := idx.Resolve(t)
		if err != nil {
			return nil, &ValidationError{
				Msg: fmt.Sprintf("提案 %s 的 target %s 在库里解析不到文件（零写入，不执行部分范围）：%v",
					rec.ID, t, err),
				Diags: []Diagnostic{{
					Code: E18, Level: LevelError, Path: rec.Rel, OpIndex: NonOpDiagnostic,
					Message: err.Error(), Target: t,
				}},
			}
		}
		rels = append(rels, rel)
	}
	if !inProposal {
		return nil, &ValidationError{
			Msg: fmt.Sprintf("%s：--target %s 不在提案 %s 的 targets %v 内（不得拿一份提案的批准去删别的对象）",
				DeleteNeedApprovedProposalMsg, target, rec.ID, p.Targets),
			Diags: []Diagnostic{{
				Code: E19, Level: LevelError, Path: rec.Rel, OpIndex: NonOpDiagnostic,
				Message: DeleteNeedApprovedProposalMsg, Target: target,
			}},
		}
	}
	return rels, nil
}

// deleteSuperseded 是第 9a 步：前提已变 → **不执行删除**，原提案标 superseded（T4）+
// 生成承载重算影响面的新提案，全部进报告。
//
// 触发算法本体在 internal/proposal（T-…-035），本函数只接线与上报；
// 这一侧**不写任何知识产物**：目标文件字节不变，动的只有提案控制面。
//
// # [M6 · T-…-072 批次 C3b] 这一支同样是全有或全无
//
// Supersede 内部是**两次**权威写（① 生成新提案、②③ 改写原提案）。旧形态里它们各自直落
// 实盘：第 ② 步失败会留下一份「凭空多出来、没人引用」的 pending 新提案，而原提案还是
// approved —— 库里从此有两份都自称当前有效的提案。M6 把这两份文件放进**同一个 overlay**、
// 同一笔事务：要么两份一起生效，要么一份都不生效。
//
// 时序与真实删除分支同源（S4~S8，见文件头），差别只有三处：写口是提案控制面（不碰知识
// 产物）、write-set 恰两份提案、Git 动词是 proposal（§5.3 第 5 步同源：本次没有 delete 发生）。
// 锁与报告体都沿用 deleteCritical 已持有的那一份：这里**不得**新建 report，否则 S1 的 W28
// 与 S2 的 W26 会被悄悄丢掉。
func (r *Root) deleteSuperseded(inv *Invocation, sess *txnSession, rep *report.Report,
	st *store.Store, rec proposal.Record, recomputed proposal.Impact,
	diff []string, reason string) (*Result, error) {
	// —— S4：原子预演。两次提案写都只进 overlay，实盘与 Git 全程零变化。——
	if err := st.BeginAtomic(); err != nil {
		return nil, blockedError("原子预演无法开始，本次零写入", err)
	}
	// 兜底丢弃：到导出 write-set 之前的每一条 return 都必须是「零生效」。
	defer st.EndAtomic()

	sup, err := proposal.Supersede(proposal.SupersedeSpec{
		Store:       st,
		Original:    rec.ID,
		OriginalRel: rec.Rel,
		Trigger:     proposal.TriggerImpactChanged,
		Reason:      reason,
		Today:       model.NewDate(r.now()),
		NewTargets:  rec.Proposal().Targets,
		NewImpact:   recomputed,
	})
	if err != nil {
		// overlay 整体丢弃 ⇒ 零权威写：不开事务、不发 intent、不跑 Git，也没有任何东西
		// 需要还原（半成品从未离开内存）。这与 M3 那句「已完成的步骤如实在册」不同：
		// 现在**没有**已完成的步骤可言。
		return nil, &ValidationError{
			Msg: fmt.Sprintf("提案 %s 改判 %s 失败（本次零写入：原提案与新提案均未落盘）：%v",
				rec.ID, proposal.StatusSuperseded, err),
			Diags: []Diagnostic{{
				Code: E21, Level: LevelError, Path: rec.Rel, OpIndex: NonOpDiagnostic,
				Message: err.Error(), Target: string(rec.ID),
			}},
		}
	}

	ws := st.AtomicWriteSet()
	if aerr := checkSupersedeWriteSet(ws, rec.Rel, sup.NewRel); aerr != nil {
		return nil, blockedError("原子域与本次改判范围不一致，本次零写入", aerr)
	}
	fireTxnStep(TxnStepExecuted, "")
	// write-set 已导出（字节是副本），overlay 使命结束：此后 st 直读实盘。
	st.EndAtomic()

	summary := []string{fmt.Sprintf(
		"%s：提案 %s 的执行前提已变化（%d 项不一致），原提案标 %s 并生成新提案重新描述当前影响面；"+
			"本次未改动任何知识产物",
		proposal.NotExecutedNotice, rec.ID, len(diff), proposal.StatusSuperseded)}
	for _, d := range diff {
		summary = append(summary, "  影响面差异："+d)
	}
	summary = append(summary, supersedeLines(sup)...)

	// —— S5：分配 txn_id → 记进锁正文 → 发布 intent（发布屏障）。——
	txnID, oerr := sess.openTxn(inv, intentFilesOf(ws), nil, r.Now, func(id string) {
		rep.SetTxnID(id) // 审计边界是「分配成功」而非「提交成功」（A-59）
	})
	if oerr != nil {
		if txnID == "" {
			return nil, oerr
		}
		rep.SetCommit("")
		sess.release() // S9 先于渲染：报告要带上「释放锁失败」这条诊断
		res := proposalResult(*rep, []string{fmt.Sprintf(
			"改判未提交：事务 %s 已分配号码但未闭合，本次零权威写入（提案 %s 仍是 %s，"+
				"未生成新提案）", txnID, rec.ID, rec.Proposal().Status)})
		r.saveReport(inv.VaultRoot, res, ExitCodeFor(classifyExit5(oerr)))
		return res, oerr
	}

	// —— S6：原子提交。commit marker 在盘之前，两份提案都不算生效。——
	cres, cerr := sess.commitWriteSet(txnID, ws)
	switch {
	case cerr != nil && cres != nil && cres.RolledBack:
		unwritten := writeSetPaths(ws)
		rep.AddWarning(unnumberedWarning("ops",
			"原子提交失败，事务 %s 已按合同 §5.2 主动放弃：%v", txnID, cerr))
		for _, p := range unwritten {
			rep.AddWarning(unnumberedWarning(p,
				"目标未写入：本次事务已整体回滚，该文件保持事务开始前的字节（前像）"))
		}
		rep.SetCommit("")
		sess.release()
		res := proposalResult(*rep, []string{fmt.Sprintf(
			"改判未生效：事务 %s 已整体回滚，提案 %s 保持 %s、新提案 %s 未留在库里",
			txnID, rec.ID, rec.Proposal().Status, sup.NewID)})
		r.saveReport(inv.VaultRoot, res, ExitPartialWrite)
		return res, &PartialWriteError{Msg: fmt.Sprintf(
			"原子提交失败，事务 %s 已整体回滚：%d 份提案一个都没有写入、"+
				"磁盘保持事务开始前的字节、未产生 commit（原因：%v）",
			txnID, len(unwritten), cerr)}
	case cerr != nil:
		blocked := blockedError(fmt.Sprintf(
			"事务 %s 的原子提交失败且未能收敛为已回滚状态，需人工处置", txnID), cerr)
		rep.SetCommit("")
		sess.release()
		res := proposalResult(*rep, []string{fmt.Sprintf(
			"改判未闭合：事务 %s 的提交既未完成也未回滚，请人工核对（原提案 %s / 新提案 %s）",
			txnID, rec.ID, sup.NewID)})
		r.saveReport(inv.VaultRoot, res, ExitCodeFor(classifyExit5(blocked)))
		return res, blocked
	}
	fireTxnStep(TxnStepCommitted, txnID)

	// —— S7：Git。提案控制面的改动走 verb = proposal（§5.3 第 5 步同源）：本次没有 delete 发生。——
	repo := r.repo(inv.VaultRoot)
	// 既有改动同样会被 add -A 带进本次 commit：采样早于 Add，如实披露（I-…-023）。
	noteExistingChangesInReport(rep, repo, writeSetPaths(ws))
	info, gerr := repo.Commit(git.Message{
		Verb:   string(model.VerbProposal),
		Domain: commitDomain(proposal.DirProposals),
		Subject: fmt.Sprintf("执行提案 %s 前影响面已变化，改判 %s 并生成 %s",
			rec.ID, proposal.StatusSuperseded, sup.NewID),
		Reason:         reason,
		RequirementIDs: []string{},
	})
	for _, w := range info.Warnings {
		rep.AddWarning(report.Diagnostic{
			Code: "", Level: report.LevelWarning, Path: "git.commit", OpIndex: report.NonOp,
			Message: w,
		})
	}
	fireTxnStep(TxnStepGit, txnID)

	// —— S8：写后索引同步。Git 成败都走，仍在锁内、早于 Release。——
	//
	// 提案不是知识卡，索引侧多半无事可做；照走不误是为了不给「从 S7 提前 return」开先例。
	r.syncIndexAfterWrite(rep, inv.VaultRoot, indexWriteLabel(inv), writeSetPaths(ws))
	fireTxnStep(TxnStepIndexSync, txnID)

	rep.Links = append(rep.Links, sup.NewRel, sup.OriginalRel)
	if aerr := addProposalEntry(rep, st, sup.NewRel); aerr != nil {
		return nil, aerr
	}
	if aerr := addProposalEntry(rep, st, sup.OriginalRel); aerr != nil {
		return nil, aerr
	}

	// —— S9：报告所需的磁盘事实（两份提案的现态）已在锁内读完，这里显式还锁。——
	// 之后只剩纯渲染与退出码裁决，一个字节都不再读写库 —— 那些活不该占着 run.lock。
	sess.release()

	if gerr != nil {
		// B4：Git 失败退 4。两份提案已由 commit marker 定盘并保持目标态，
		// **不回滚、不做第二次权威写**。
		rep.SetCommit("")
		res := proposalResult(*rep, append(summary,
			"  Git 提交失败："+oneLineReason(gerr.Error())))
		r.saveReport(inv.VaultRoot, res, ExitCommitFailed)
		return res, &CommitFailedError{
			Msg: "Git 提交失败：已原子生效的原提案改判与新提案保留在磁盘并保持现状，" +
				"未做任何还原（B4）",
			Err: gerr,
			Diags: []Diagnostic{{
				Code: E22, Level: LevelError, Path: "git.commit", OpIndex: NonOpDiagnostic,
				Message: commitFailureMessage(gerr),
			}},
		}
	}
	rep.SetCommit(info.SHA)
	res := proposalResult(*rep, summary)
	r.saveReport(inv.VaultRoot, res, ExitOK)
	return res, nil
}

// checkSupersedeWriteSet 钉死改判的原子域：accepted write-set 恰是「原提案 + 新提案」。
//
// 少一份意味着某一步没进事务（库里会多出孤儿提案或留下两份自称有效的提案），
// 多一份意味着这条命令顺手改了别的东西 —— 两者都必须在**提交之前**变成零写入的阻断。
func checkSupersedeWriteSet(ws []store.AtomicFileSpec, originalRel, newRel string) error {
	want := map[string]bool{originalRel: true, newRel: true}
	got := make(map[string]bool, len(ws))
	for _, f := range ws {
		got[f.Path] = true
	}
	var missing, extra []string
	for p := range want {
		if !got[p] {
			missing = append(missing, p)
		}
	}
	for p := range got {
		if !want[p] {
			extra = append(extra, p)
		}
	}
	if len(missing) == 0 && len(extra) == 0 {
		return nil
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return fmt.Errorf("accepted write-set 应恰含原提案 %s 与新提案 %s：缺 %v、多 %v",
		originalRel, newRel, missing, extra)
}

// deleteExecute 是第 9b → 10 → 11 步的**原子**实现：S4 预演 → S5 intent → S6 提交 →
// S7 Git → S8 索引同步 → 报告。
//
// 原子域恰是「**全部** target 文件 + 那份提案的 execution 块」：要么一起生效，要么一个
// 字节都不生效。sess 是 deleteCritical 已经持有的临界区（锁不在本函数取、也不在本函数还），
// rep 里已攒着 S1 / S2 的诊断，本函数只往里追加。
func (r *Root) deleteExecute(inv *Invocation, sess *txnSession, rep *report.Report,
	st *store.Store, idx store.Index, rec proposal.Record, rels []string,
	target, reason string) (*Result, error) {
	stamp := model.NewStamp(r.now())
	led, lerr := proposal.NewPathLedger(rels)
	if lerr != nil {
		return nil, &ValidationError{Msg: "影响文件账本建不起来（零写入）：" + lerr.Error()}
	}

	// —— S4：原子预演。权威写只进 overlay，实盘与 Git 全程零变化。——
	if err := st.BeginAtomic(); err != nil {
		return nil, blockedError("原子预演无法开始，本次零写入", err)
	}
	// 兜底丢弃：从这里到导出 write-set 之间的任何一条 return 都必须是「零生效」。
	defer st.EndAtomic()

	for _, rel := range rels {
		f, ferr := st.Read(rel)
		if ferr != nil {
			return nil, deleteNotApplied(rec, rels, rel, target, ferr)
		}
		// 唯一写口：状态落盘只经 ApplyStateWrite（SetDeleted 的调用点全部留在 internal/store/）。
		// 只写 deleted_at + deleted_reason：status 不在这条路径上，结构上不可能被改。
		if _, werr := r.applyStateWrite(st, store.StateWriteSpec{
			Op: store.StateWriteDeleted, Rel: rel, ExpectedHash: f.Hash,
			At: stamp, Reason: reason,
			// Stamp：逻辑删除是一次实际写入 → 刷新内容时间戳（矩阵第 8 行 / I-…-009）。
			// 与 deleted_at 同取本次 stamp：同一次写入只有一个时刻，不产生两个真相。
			Stamp: stamp,
		}); werr != nil {
			// 预演期写失败 ⇒ overlay 整体丢弃即零写入：不开事务、不发 intent、不跑 Git。
			// 也**不**把提案改判成 failed —— 那是又一次权威写，而本次一个字节都没生效。
			return nil, deleteNotApplied(rec, rels, rel, target, werr)
		}
		if merr := led.MarkWritten(rel); merr != nil {
			return nil, &ValidationError{Msg: "账本登记失败：" + merr.Error()}
		}
	}

	// A-32「整键删除」：succeeded 不再回写 git_commit，execution 回写不必等 SHA。
	// 它与各文件的 deleted_at 同处**一个 overlay**，因此进同一笔事务、同一次 commit：
	// 删除后工作区不留脏提案（K-041-01），也不存在「文件删了、提案没记上」的中间态。
	recRes, rerr := proposal.RecordExecution(proposal.RecordSpec{
		Store: st, ID: rec.ID, Rel: rec.Rel,
		Status: proposal.ExecSucceeded, AttemptedAt: stamp,
		Ledger: led,
	})
	if rerr != nil {
		return nil, deleteNotApplied(rec, rels, rec.Rel, target,
			fmt.Errorf("提案 %s 的 execution 回写被拒：%w", rec.ID, rerr))
	}

	ws := st.AtomicWriteSet()
	if aerr := checkDeleteWriteSet(ws, rels, rec.Rel); aerr != nil {
		return nil, blockedError("原子域与本次删除范围不一致，本次零写入", aerr)
	}
	fireTxnStep(TxnStepExecuted, "")
	// write-set 已导出（字节是副本），overlay 使命结束：此后 st 直读实盘 ——
	// 报告里的「已生效」只能来自 commit marker 之后的真实字节。
	st.EndAtomic()

	// —— S5：分配 txn_id → 记进锁正文 → 发布 intent（发布屏障）。——
	txnID, oerr := sess.openTxn(inv, intentFilesOf(ws), nil, r.Now, func(id string) {
		rep.SetTxnID(id) // 审计边界是「分配成功」而非「提交成功」（A-59）
	})
	if oerr != nil {
		if txnID == "" {
			// 号码根本没分配出来：盘上不存在对应事务目录，本次零写入、零 intent。
			return nil, oerr
		}
		// 号码已在盘 ⇒ 必须带着 txn_id 交付（用户要靠它定位那笔未闭合事务）。
		rep.SetCommit("")
		sess.release() // S9 先于渲染：报告要带上「释放锁失败」这条诊断
		res := proposalResult(*rep, []string{fmt.Sprintf(
			"逻辑删除未提交：事务 %s 已分配号码但未闭合，本次零权威写入（目标 %s，提案 %s）",
			txnID, target, rec.ID)})
		r.saveReport(inv.VaultRoot, res, ExitCodeFor(classifyExit5(oerr)))
		return res, oerr
	}

	// —— S6：原子提交。commit marker 在盘之前，任何一处删除标记都不算生效。——
	//
	// 两种失败必须分开翻译（合同 §5.2 vs §16），与 runPlan / capture / mark-reviewed 同源。
	cres, cerr := sess.commitWriteSet(txnID, ws)
	switch {
	case cerr != nil && cres != nil && cres.RolledBack:
		unwritten := writeSetPaths(ws)
		rep.AddWarning(unnumberedWarning("ops",
			"原子提交失败，事务 %s 已按合同 §5.2 主动放弃：%v", txnID, cerr))
		for _, p := range unwritten {
			rep.AddWarning(unnumberedWarning(p,
				"目标未写入：本次事务已整体回滚，该文件保持事务开始前的字节（前像）"))
		}
		rep.SetCommit("")
		sess.release()
		res := proposalResult(*rep, []string{fmt.Sprintf(
			"逻辑删除未生效：事务 %s 已整体回滚，%d 个文件（%d 个影响目标 + 提案 %s）"+
				"全部保持事务开始前的字节（目标 %s）",
			txnID, len(unwritten), len(rels), rec.ID, target)})
		r.saveReport(inv.VaultRoot, res, ExitPartialWrite)
		return res, &PartialWriteError{Msg: fmt.Sprintf(
			"原子提交失败，事务 %s 已整体回滚：%d 个目标文件一个都没有写入、"+
				"磁盘保持事务开始前的字节、未产生 commit（原因：%v）",
			txnID, len(unwritten), cerr)}
	case cerr != nil:
		blocked := blockedError(fmt.Sprintf(
			"事务 %s 的原子提交失败且未能收敛为已回滚状态，需人工处置", txnID), cerr)
		rep.SetCommit("")
		sess.release()
		res := proposalResult(*rep, []string{fmt.Sprintf(
			"逻辑删除未闭合：事务 %s 的提交既未完成也未回滚，请人工核对（目标 %s，提案 %s）",
			txnID, target, rec.ID)})
		r.saveReport(inv.VaultRoot, res, ExitCodeFor(classifyExit5(blocked)))
		return res, blocked
	}
	fireTxnStep(TxnStepCommitted, txnID)

	// —— S7：Git。严格晚于 commit marker、仍持同一把锁（verb = delete，一次 git log 恰 +1，
	// 含各文件 deleted_at 与提案 execution 回写——两者同进一次历史）。——
	repo := r.repo(inv.VaultRoot)
	// 既有改动同样会被 add -A 带进本次 commit：采样早于 Add，如实披露（I-…-023）。
	noteExistingChangesInReport(rep, repo, writeSetPaths(ws))
	info, gerr := repo.Commit(git.Message{
		Verb:           string(model.VerbDelete),
		Domain:         commitDomain(domainOfPath(rels[0])),
		Subject:        fmt.Sprintf("逻辑删除 %s（提案 %s）", target, rec.ID),
		Reason:         reason,
		RequirementIDs: []string{},
	})
	for _, w := range info.Warnings {
		rep.AddWarning(report.Diagnostic{
			Code: "", Level: report.LevelWarning, Path: "git.commit", OpIndex: report.NonOp,
			Message: w,
		})
	}
	fireTxnStep(TxnStepGit, txnID)

	// —— S8：写后索引同步。Git 成败都走，仍在**同一把锁内**、早于 Release（合同 §16.1 / §16.3）。——
	//
	// 逻辑删除**不删文件**，给的是「这些卡的现态行」，索引里对应行的 deleted 位随之更新；
	// 索引出问题只降级成一条 W22，绝不回滚已由 commit marker 定盘的删除标记。
	r.syncIndexAfterWrite(rep, inv.VaultRoot, indexWriteLabel(inv), writeSetPaths(ws))
	fireTxnStep(TxnStepIndexSync, txnID)

	c := led.Counts()
	summary := []string{fmt.Sprintf(
		"逻辑删除已执行：提案 %s，影响文件 %d 个与提案 execution 由事务 %s 一次性原子生效"+
			"（deleted_at / deleted_reason）；关系记录一条未删、无物理删除（U-01）",
		rec.ID, c.Written, txnID)}
	for _, w := range led.Written() {
		summary = append(summary, "  已写入："+w)
	}
	summary = append(summary, "  "+recRes.Message,
		fmt.Sprintf("  提案 %s 的 execution 回写与各文件同处一笔事务、同一次 commit"+
			"（succeeded；A-32 后不再单存 git_commit）", rec.ID))
	rep.Links = append(rep.Links, rels...)
	if aerr := addProposalEntry(rep, st, rec.Rel); aerr != nil {
		return nil, aerr
	}
	// 第 11 步：报告逐字含「本次删除没有自动改变任何知识卡的状态」+ support_check[]。
	sumWithCheck, serr := r.attachSupportCheck(st, idx, rep, rec.Proposal().Targets, summary)
	if serr != nil {
		return nil, serr
	}

	// —— S9：报告所需的磁盘事实（提案现态 + support_check 的全库投影）已在锁内读完，
	// 这里显式还锁。support_check 是**权威重读**，绝不能挪到释放之后：那会让别的写者
	// 在读与判之间插进来，把一份自相矛盾的支持面清单交给用户。——
	sess.release()

	if gerr != nil {
		// B4：Git 失败退 4。删除标记与 execution=succeeded 已由 commit marker 定盘并保持
		// 现状，**不回滚、不做第二次权威写**（旧形态那次 execution=failed 回写就此作废）。
		rep.SetCommit("")
		res := proposalResult(*rep, append(sumWithCheck,
			"  Git 提交失败："+oneLineReason(gerr.Error())))
		r.saveReport(inv.VaultRoot, res, ExitCommitFailed)
		return res, &CommitFailedError{
			Msg: "Git 提交失败：已原子生效的删除标记与提案 execution 保留在磁盘并保持现状，" +
				"未做任何还原（B4）",
			Err: gerr,
			Diags: []Diagnostic{{
				Code: E22, Level: LevelError, Path: "git.commit", OpIndex: NonOpDiagnostic,
				Message: commitFailureMessage(gerr),
			}},
		}
	}
	rep.SetCommit(info.SHA)
	res := proposalResult(*rep, sumWithCheck)
	r.saveReport(inv.VaultRoot, res, ExitOK)
	return res, nil
}

// deleteNotApplied 是**预演期失败**的唯一出口（退 3）：overlay 已丢弃 ⇒ 一个字节都没生效。
//
// 与 M3 的 deleteFailure 相比少了两样东西，且都是有意少的：没有「已写 N 个」的账
// （不存在这种中间态了），也没有 `execution = failed` 的回写（那是一次权威写，
// 用来记录一件没有发生过的部分执行）。本次连事务都没开，因此也不落 last-report。
func deleteNotApplied(rec proposal.Record, rels []string, failedRel, target string,
	cause error) error {
	diags := make([]Diagnostic, 0, len(rels)+2)
	diags = append(diags, Diagnostic{
		Code: E21, Level: LevelError, Path: failedRel, OpIndex: NonOpDiagnostic,
		Message: oneLineReason(cause.Error()), Target: target,
	})
	for _, rel := range rels {
		diags = append(diags, Diagnostic{
			Code: "", Level: LevelInfo, Path: rel, OpIndex: NonOpDiagnostic,
			Message: "未写入，字节不变（预演整体丢弃，本次未开启事务）",
		})
	}
	diags = append(diags, Diagnostic{
		Code: "", Level: LevelInfo, Path: rec.Rel, OpIndex: NonOpDiagnostic,
		Message: fmt.Sprintf("提案 %s 字节不变：execution 保持原值（不记 %s —— 本次一个字节都没生效）",
			rec.ID, proposal.ExecFailed),
		Target: string(rec.ID),
	})
	return &PartialWriteError{
		Msg: fmt.Sprintf("逻辑删除未生效：%d 个影响文件与提案 %s 全部保持原字节，"+
			"本次未开启事务、未产生 commit（失败于 %s：%s）",
			len(rels), rec.ID, failedRel, oneLineReason(cause.Error())),
		Diags: diags,
	}
}

// checkDeleteWriteSet 钉死原子域：accepted write-set 恰是「全部 target + 提案文件」。
//
// 多一个路径意味着这条命令顺手改了别的东西，少一个意味着某个目标悄悄没进事务；
// 两者都必须在**提交之前**变成零写入的阻断，而不是提交之后由用户去 git diff 发现。
func checkDeleteWriteSet(ws []store.AtomicFileSpec, rels []string, proposalRel string) error {
	want := make(map[string]bool, len(rels)+1)
	for _, rel := range rels {
		want[rel] = true
	}
	want[proposalRel] = true
	got := make(map[string]bool, len(ws))
	for _, f := range ws {
		got[f.Path] = true
	}
	var missing, extra []string
	for p := range want {
		if !got[p] {
			missing = append(missing, p)
		}
	}
	for p := range got {
		if !want[p] {
			extra = append(extra, p)
		}
	}
	if len(missing) == 0 && len(extra) == 0 {
		return nil
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return fmt.Errorf("accepted write-set 应恰含 %d 个影响文件与提案 %s：缺 %v、多 %v",
		len(rels), proposalRel, missing, extra)
}

// attachSupportCheck 挂上 support_check[] 与那条逐字说明。
//
// 判定与文案都只有 internal/report 一处（BuildSupportCheck / SupportCheckLines），
// 这里不重新判定、不改写文案：`recommendation` 是对外合同键，jq 直读。
// 逐字串**无条件**输出（哪怕清单为空）：它陈述的是「系统没做什么」，与受影响卡多少无关。
func (r *Root) attachSupportCheck(st *store.Store, idx store.Index, rep *report.Report,
	deleted []string, summary []string) ([]string, error) {
	entries, err := report.BuildSupportCheck(deleted, deleteSupportVault{st: st, idx: idx})
	if err != nil {
		return nil, &ValidationError{Msg: "支持面检查失败（删除已落盘，磁盘保留现状）：" + err.Error()}
	}
	rep.SetSupportCheck(entries)
	out := append(summary, report.NoAutoStatusChangeNotice+
		"（失去有效 support 的卡只得到建议，不处理时它们仍是 active）")
	out = append(out, report.SupportCheckLines(entries)...)
	rep.AddInfo("support_check", report.NonOp, "%s", report.NoAutoStatusChangeNotice)
	return out, nil
}

// applyStateWrite 是本命令唯一的状态落盘调用点：生产恒走 store.ApplyStateWrite
// （写口唯一），Root.StateWrite 只在用例里注入，用于覆盖「第 N 个文件写失败」分支。
func (r *Root) applyStateWrite(st *store.Store, spec store.StateWriteSpec) (store.Result, error) {
	if r.StateWrite != nil {
		return r.StateWrite(st, spec)
	}
	return st.ApplyStateWrite(spec)
}

// deleteSupportVault 是 report.SupportVault 的**只读**实现：从磁盘投影知识卡的 support 端点。
//
// 只投影 `sources[].rel == support` 的条目：against / context 不是「支持」，不参与本判定。
// 全程只读（Read + CardOf），没有任何写能力 —— 支持面检查绝不可能改状态。
type deleteSupportVault struct {
	st  *store.Store
	idx store.Index
}

// SupportCards 实现 report.SupportVault。
//
// 只看落在 `domains/<d>/knowledge/` 下的产物（知识卡是唯一有 support 支持面的产物）；
// 读不到或形态不成立一律 fail fast，不返回半份清单假装完整。
func (v deleteSupportVault) SupportCards() ([]report.SupportCard, error) {
	ids := make([]string, 0, len(v.idx.ByID))
	for id := range v.idx.ByID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]report.SupportCard, 0, len(ids))
	for _, id := range ids {
		rel := v.idx.ByID[id]
		if !strings.Contains(rel, "/"+store.DirKnowledge+"/") {
			continue
		}
		f, err := v.st.Read(rel)
		if err != nil {
			return nil, fmt.Errorf("读取 %s 失败：%w", rel, err)
		}
		card, err := store.CardOf(f.Bytes)
		if err != nil {
			return nil, fmt.Errorf("解析 %s 的 frontmatter 失败：%w", rel, err)
		}
		supports := make([]report.SupportRef, 0, len(card.Sources))
		for _, s := range card.Sources {
			if s.Rel != model.MaterialSupport {
				continue
			}
			supports = append(supports, report.SupportRef{
				Source: string(s.Source), Note: string(s.Note),
			})
		}
		out = append(out, report.SupportCard{
			ID: id, Status: string(card.Status), Supports: supports,
		})
	}
	return out, nil
}
