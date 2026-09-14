package cli

// `eg proposal approve <id> --confirm` 的**命令侧编排**：确认门 + U-12 守卫 + 执行前重算接线
// （提案合同 §10.2 时序第 7 / 8 步、退出码 §4 的 `6`、授权合同 U-12 与 N-1 反伪造条款；
// T-evergreen.s1_main_flow-158614-040 第三层）。
//
// # 为什么单独一个文件
//
// 能力层（四步链路 approveProposal）在 proposal.go，子命令壳在 proposal_cmd.go；
// 二者之间还差一层**门禁**：谁有资格发起（U-12）、什么条件下才允许落盘（--confirm）。
// 门禁的判定顺序一旦掺进能力层，就会出现「先写盘后发现没确认」这种不可回滚的时序，
// 因此它必须是独立的一层、且**全部前置**于任何写盘动作。
//
// # 三个不可放宽的约束
//
//  1. 无 `--confirm` → 退出码 `6`，且**权威 Markdown 完全不变**（§10.2 第 7 步逐字）：
//     零写入、零 commit、不落 last-report —— `git status --porcelain` 与 `git log -1`
//     与执行前逐字相同。退出码由 ExitCodeForConfirm / NeedConfirmError 给出，
//     本文件不自己拼数字，也不调 os.Exit（唯一允许 os.Exit 的地方是 cmd/eg/main.go）。
//  2. 确认点**恰 1 处**（EG-CFM-05）：`--confirm` 是执行前唯一一次确认。
//     确认门之后的代码路径**绝不读取 stdin**（r.In 在本文件出现零次），
//     因此 stdin 被关闭时仍必须能跑完并退 0 —— 由 TestApprove_NoSecondConfirmation 反证。
//  3. 执行前**必须重算影响面并与提案比对**（§10.2 第 8 步）：走 approveProposal 的四步链路，
//     重算恒在写盘之前。前提已变 → S-② 改判（新提案 + 原提案 superseded）且**不执行**。
//
// # 阶段边界（本 task 不做）
//
//   - 删除类的**逐文件写入**归 T-…-041：本文件只把提案标到 `approved` 真实落盘，
//     知识数据的改动仍走 ChangePlan 的 delete op（A-23 第 4 条）。报告里如实上报
//     「执行侧待 T-…-041 接线」，不假装已删除、不写 execution 三态（属 T-…-036）。
//   - `superseded` 触发算法本体属 T-…-035（internal/proposal 既有能力），这里只接线与上报。

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/git"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/proposal"
	"github.com/ikaqiu-Lemon/EverGreen/internal/report"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// ApproveConfirmFlag 是确认参数名（与 proposal_cmd.go 的 flag 注册同一份字面量）。
const ApproveConfirmFlag = "confirm"

// ApproveNeedConfirmMsg 是缺确认时的固定文案：措辞固定便于 e2e 与用例逐字断言，
// 也让「6 不是失败而是等待用户点头」这件事在输出里说得清楚。
const ApproveNeedConfirmMsg = "eg proposal approve 需要用户二次确认：请重跑并加 --confirm（本次零写入、零 commit，权威 Markdown 完全不变）"

// ApproveAgentDeniedMsg 是 U-12 守卫的固定文案（退 2、零写入）。
const ApproveAgentDeniedMsg = "U-12：Agent 不得把提案 status 写成 approved —— 批准必须由用户显式发起"

// ApproveExecutionPendingNotice 是本层的**阶段边界**如实上报：提案已批准落盘，
// 但删除类的逐文件写入尚未接线（T-…-041）。`approved` ≠ 已执行（execution 仍 not_started）。
const ApproveExecutionPendingNotice = "提案已批准（status: approved）；知识数据的逐文件删除写入待 T-…-041 接线，本次未改动任何知识产物"

// approveConfirmed 读 `--confirm` 的取值。
//
// 只认 flag 的**当前值**而不是「是否被显式给出」：`--confirm=false` 必须被当成
// 没有确认，否则确认门就能被一个否定式写法绕过。
func approveConfirmed(inv *Invocation) bool {
	ok, err := strconv.ParseBool(inv.String(ApproveConfirmFlag))
	if err != nil {
		// bool flag 的取值只可能是 true / false；解析不了说明 flag 声明被改坏了，fail fast。
		return false
	}
	return ok
}

// runProposalApprove 是 `eg proposal approve <id> --confirm` 的唯一入口。
//
// 固定次序（不可重排，前三步全部在写盘之前）：
//
//	① U-12 守卫：非用户显式路径 → 退 2、零写入
//	② 提案可读性复核：库里没有这份提案 → 退 2、零写入（此时谈不上「只差确认」）
//	③ 确认门：缺 --confirm → 退 6、零写入零 commit
//	④ 执行前重算 + 裁决 + 落盘（approveProposal 四步链路）
//	⑤ 一次 commit（verb = proposal）+ 报告
func (r *Root) runProposalApprove(inv *Invocation) (*Result, error) {
	id := proposal.ID(inv.Args[0])

	// ① U-12：`approve` 是高风险操作，要求**真实的**命令行佐证 `--user-request`。
	//
	// 为什么这里不像 eg deprecate 那样在进程边界自动置真：deprecate 的 CLI 入口本身
	// 就是「用户敲了这条命令」的等价佐证，且其后果可由用户再次 deprecate 回退；
	// 而 approve 的后果是放行一次删除（U-01 不可逆的替代路径只有 undelete），
	// 授权合同 §10.3 的 V9 明确要求 initiator=user 且**不随 §4.5.1 宽松口径放宽**。
	// N-1 反伪造条款进一步说明：提案文件里写 `initiator: user` 不能自证 ——
	// 唯一可信的信号是命令行上的 --user-request（Invocation 字段，来自进程边界）。
	if !inv.UserRequest {
		item, ok := UnauthorizableNum("U-12")
		if !ok {
			panic("不可授权项清单缺 U-12：授权合同 §6 的十三条不得被删")
		}
		return nil, &ValidationError{
			Msg: fmt.Sprintf("%s（%s）；替代路径：%s。请加 --%s 表明本次调用由用户显式发起",
				ApproveAgentDeniedMsg, item.Basis, item.Alternative, UserRequestFlag),
			Diags: []Diagnostic{{
				Code: E19, Level: LevelError, Path: proposal.Rel(id), OpIndex: NonOpDiagnostic,
				Message: ApproveAgentDeniedMsg, Target: string(id),
			}},
		}
	}

	// U-12 通过：其余全部进临界区。创建报告，交给 A 类强事务编排（C4b）。
	rep := report.New()
	return r.runProposalApproveCritical(inv, &rep, id)
}

// runProposalApproveCritical 是 `approve` 的 A 类强事务编排（提案合同 §16.1；与 new /
// reject / delete / mark-reviewed 共用**同一把** `.index/run.lock`）。
//
// 本小步落 S1~S4 与锁内确认门；S5~S9（intent / 提交 / Git / 索引同步 / 释放）尚未接线：
//
//	S1 取 run.lock
//	S2 崩溃恢复屏障（临界区内第一件事，早于任何一次业务读）
//	S3 锁内重建 Store、锁内加载提案；随后仍按现有顺序在锁内裁决确认门
//	   —— 提案不存在优先退 2、缺 --confirm 退 6，两者都**不开 txn**
//	S4 原子预演：approveProposal 的重算 / 裁决 / 落盘全走 atomic overlay，
//	   accepted write-set 必须与本次结局（批准 / 改判）严格对齐（实盘与 Git 全程零变化）
//
// U-12 守卫仍留在锁外的 S0（见 runProposalApprove）：它只看命令行佐证，不读库、不写盘，
// 没有必要为一次注定被拒的调用去抢锁。
func (r *Root) runProposalApproveCritical(inv *Invocation, rep *report.Report,
	id proposal.ID) (*Result, error) {
	// —— S1 + S2：取锁、崩溃恢复屏障。——
	sess, serr := r.enterTxnCritical(inv, reportWarnSink{rep}, txnCriticalOpts{
		ZeroWrite: "本次零写入", ReleaseNotice: "本次写入与提交不受影响",
	})
	if serr != nil {
		return nil, serr
	}
	defer sess.release()

	// —— S3：锁内重建 Store、锁内加载提案。——
	//
	// 严禁复用锁外快照：S2 刚可能把某份提案回滚到前像，取锁之前别的写者也可能刚改过它；
	// 确认门的前提与其后 E8 / E9 的判定，都必须落在**当下**盘上的字节。
	st := store.New(inv.VaultRoot)
	// 提案不存在是事实问题（退 2、零写入）：此时谈不上「只差确认」，更不该开事务。
	rec, err := proposal.Load(st, id)
	if err != nil {
		return nil, proposalLoadError(inv.Args[0], err)
	}

	// —— 确认门：仍按现有顺序在锁内裁决（1 → 2 → 6 → 0）。走到这里 ArgsValid /
	// ValidationPassed 均已由前面阶段证成，因此恒为 true，唯一可能的非 0 结局是 6。——
	decision := ConfirmDecision{
		ArgsValid:        true,
		ValidationPassed: true,
		ConfirmMissing:   !approveConfirmed(inv),
	}
	if code := ExitCodeForConfirm(decision); code != ExitOK {
		if code != ExitNeedConfirm {
			panic(fmt.Sprintf("确认门只应产出 %d，实得 %d：判定顺序被改动", ExitNeedConfirm, code))
		}
		// 缺 --confirm：退 6、零写入、零 commit、不落 last-report，且**绝不开 txn**
		// （AllocateTxnID 尚未发生，盘上不会留下任何事务目录）。
		return nil, &NeedConfirmError{
			Command: CmdProposalApprove,
			Msg: fmt.Sprintf("%s（提案 %s，路径 %s）",
				ApproveNeedConfirmMsg, rec.ID, rec.Rel),
			Diags: []Diagnostic{{
				Code: E19, Level: LevelError, Path: rec.Rel, OpIndex: NonOpDiagnostic,
				Message: ApproveNeedConfirmMsg, Target: string(rec.ID),
			}},
		}
	}

	fireTxnStep(TxnStepReread, "")

	// —— S4：原子预演。approveProposal 的重算 / 裁决 / 落盘全走 store 的 atomic overlay，
	// 权威写只进 overlay，实盘与 Git 全程零变化；任一步失败即零写入（overlay 整体丢弃）。——
	if err := st.BeginAtomic(); err != nil {
		return nil, blockedError("原子预演无法开始，本次零写入", err)
	}
	// 兜底丢弃：到导出 write-set 之前的每一条 return 都必须是「零生效」。
	defer st.EndAtomic()

	out, aerr := approveProposal(st, approveRequest{
		ID:     rec.ID,
		Rel:    rec.Rel,
		Reason: strings.TrimSpace(inv.String("reason")),
		Today:  model.NewDate(r.now()),
	}, defaultApproveHooks())
	if aerr != nil {
		// 预演期失败 ⇒ overlay 整体丢弃即零写入：实盘与 Git 全程未变，**绝不声称磁盘已部分写**。
		return nil, approvePreviewFailure(rec.Rel, out, aerr)
	}

	// 依 outcome 校验原子域：改判恰含「原提案 + 新提案」，批准恰含那一份提案；
	// 既没改判也没批准属实现 bug（链路第三种结局），拒绝静默成功。
	ws := st.AtomicWriteSet()
	switch {
	case out.Superseded != nil:
		sup := *out.Superseded
		if aerr := checkSupersedeWriteSet(ws, sup.OriginalRel, sup.NewRel); aerr != nil {
			return nil, blockedError("原子域与本次改判范围不一致，本次零写入", aerr)
		}
	case out.Executed:
		if aerr := checkSingleProposalWriteSet(ws, rec.Rel); aerr != nil {
			return nil, blockedError("原子域与本次批准范围不一致，本次零写入", aerr)
		}
	default:
		return nil, &ValidationError{Msg: fmt.Sprintf(
			"批准提案 %s 既未落地 approved 也未改判 superseded：链路结论缺失，拒绝静默成功", rec.ID)}
	}
	fireTxnStep(TxnStepExecuted, "")

	// —— S5：分配 txn_id → 记进锁正文 → 发布 intent（发布屏障）。——
	txnID, oerr := sess.openTxn(inv, intentFilesOf(ws), nil, r.Now, func(tid string) {
		rep.SetTxnID(tid) // 审计边界是「分配成功」而非「提交成功」（A-59）
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
			"批准提案未提交：事务 %s 已分配号码但未闭合，本次零权威写入（提案 %s）",
			txnID, rec.ID)})
		r.saveReport(inv.VaultRoot, res, ExitCodeFor(classifyExit5(oerr)))
		return res, oerr
	}

	// —— S6：原子提交。commit marker 在盘之前，批准 / 改判一律不算生效。——
	//
	// 两种失败分开翻译（合同 §5.2 vs §16），与 new / reject / delete / mark-reviewed 同源。
	cres, cerr := sess.commitWriteSet(txnID, ws)
	switch {
	case cerr != nil && cres != nil && cres.RolledBack:
		unwritten := writeSetPaths(ws)
		rep.AddWarning(unnumberedWarning("ops",
			"原子提交失败，事务 %s 已按合同 §5.2 主动放弃：%v", txnID, cerr))
		// 逐路径交代「目标未写入」：批准恰一份提案、改判两份（原提案 + 新提案）。
		for _, p := range unwritten {
			rep.AddWarning(unnumberedWarning(p,
				"目标未写入：本次事务已整体回滚，该文件保持事务开始前的字节（前像）"))
		}
		rep.SetCommit("")
		sess.release()
		res := proposalResult(*rep, []string{fmt.Sprintf(
			"批准未生效：事务 %s 已整体回滚，%d 份提案文件全部保持事务开始前的字节（提案 %s）",
			txnID, len(unwritten), rec.ID)})
		r.saveReport(inv.VaultRoot, res, ExitPartialWrite)
		return res, &PartialWriteError{Msg: fmt.Sprintf(
			"原子提交失败，事务 %s 已整体回滚：%d 份提案一个字节都没有写入、"+
				"磁盘保持事务开始前的字节、未产生 commit（原因：%v）",
			txnID, len(unwritten), cerr)}
	case cerr != nil:
		blocked := blockedError(fmt.Sprintf(
			"事务 %s 的原子提交失败且未能收敛为已回滚状态，需人工处置", txnID), cerr)
		rep.SetCommit("")
		sess.release()
		res := proposalResult(*rep, []string{fmt.Sprintf(
			"批准未闭合：事务 %s 的提交既未完成也未回滚，请人工核对（提案 %s）",
			txnID, rec.ID)})
		r.saveReport(inv.VaultRoot, res, ExitCodeFor(classifyExit5(blocked)))
		return res, blocked
	}
	fireTxnStep(TxnStepCommitted, txnID)

	// —— S7：Git。verb = proposal、严格晚于 commit marker、仍持同一把锁（§10.2 时序第 5 步）。
	//    subject 依本次结局而定：批准落地 vs 影响面已变化改判 superseded。——
	subject := fmt.Sprintf("批准提案 %s", rec.ID)
	if out.Superseded != nil {
		subject = fmt.Sprintf("批准提案 %s 时影响面已变化，改判 superseded 并生成 %s",
			rec.ID, out.Superseded.NewID)
	}
	repo := r.repo(inv.VaultRoot)
	// 既有改动同样会被 add -A 带进本次 commit：采样早于 Add，如实披露（I-…-023）。
	noteExistingChangesInReport(rep, repo, writeSetPaths(ws))
	info, gerr := repo.Commit(git.Message{
		Verb:           string(model.VerbProposal),
		Domain:         commitDomain(proposal.DirProposals),
		Subject:        subject,
		Reason:         inv.String("reason"),
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
	r.syncIndexAfterWrite(rep, inv.VaultRoot, indexWriteLabel(inv), writeSetPaths(ws))
	fireTxnStep(TxnStepIndexSync, txnID)

	// —— S6 后锁内重读：commit marker 已定盘，此刻采集的就是本次写入后的现态；
	//    恢复旧路径的报告语义（links / proposals[] / summary）。——
	summary := append([]string{}, out.Lines...)
	if out.Superseded != nil {
		sup := *out.Superseded
		rep.Links = append(rep.Links, sup.NewRel, sup.OriginalRel)
		if aerr := addProposalEntry(rep, st, sup.NewRel); aerr != nil {
			return nil, aerr
		}
		if aerr := addProposalEntry(rep, st, sup.OriginalRel); aerr != nil {
			return nil, aerr
		}
		summary = append(summary, fmt.Sprintf(
			"影响面已变化：原提案 %s 标记 %s，新提案 %s 待用户重新决定；本次**不执行**删除",
			sup.From, proposal.StatusSuperseded, sup.NewID))
	} else {
		rep.Links = append(rep.Links, rec.Rel)
		if aerr := addProposalEntry(rep, st, rec.Rel); aerr != nil {
			return nil, aerr
		}
		summary = append(summary, "  "+ApproveExecutionPendingNotice)
	}

	// —— S9：报告所需的锁内事实已采集完，这里显式还锁；之后只剩纯渲染与退出码裁决。——
	sess.release()

	if gerr != nil {
		// B4：Git 失败退 4。已由 commit marker 定盘的批准 / 改判结果保持目标态，
		// **不回滚、不做第二次权威写**。
		rep.SetCommit("")
		res := proposalResult(*rep, append(summary,
			"  Git 提交失败："+oneLineReason(gerr.Error())))
		r.saveReport(inv.VaultRoot, res, ExitCommitFailed)
		return res, &CommitFailedError{
			Msg: "Git 提交失败：已原子生效的批准 / 改判结果保留在磁盘并保持现状，未做任何还原（B4）",
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

// addProposalEntry 从**刚落盘的字节**读回一条提案事实进 `proposals[]`（不由内存推断，
// 报告里的值因此与磁盘逐字一致；数量守恒，不折叠、不去重）。
func addProposalEntry(rep *report.Report, st *store.Store, rel string) error {
	rec, err := proposal.LoadRel(st, rel)
	if err != nil {
		return err
	}
	p := rec.Proposal()
	rep.AddProposal(report.ProposalEntry{
		ID: string(rec.ID), Path: rec.Rel, Status: string(p.Status),
		Targets: nonNilStrings(p.Targets),
		Execution: report.ProposalExecution{
			// git_commit 恒 nil：本层不回写 execution 三态（属 T-…-036），
			// 报告里也不得凭空填一个 SHA 假装执行已发生。
			Status: string(p.Execution.Status), GitCommit: nil,
			WrittenPaths:   nonNilStrings(p.Execution.WrittenPaths),
			UnwrittenPaths: nonNilStrings(p.Execution.UnwrittenPaths),
		},
	})
	return nil
}

// approvePreviewFailure 把 S4 原子预演里四步链路的失败转成校验错误（退 2）。
//
// 预演全程在 store 的 atomic overlay 上：任一步失败 ⇒ overlay 整体丢弃 ⇒ **本次零写入**，
// 实盘与 Git 一个字节都没动。因此这里的措辞是「零写入」而非「磁盘保留现状 / 未做还原」——
// 后者会误导读者以为盘上曾发生过部分写。out.Lines 里已走到的步骤仅作诊断线索如实带出。
func approvePreviewFailure(rel string, out approveOutcome, err error) error {
	diags := []Diagnostic{{
		Code: E21, Level: LevelError, Path: rel, OpIndex: NonOpDiagnostic,
		Message: err.Error(),
	}}
	for _, line := range out.Lines {
		diags = append(diags, Diagnostic{
			Code: "", Level: LevelInfo, Path: rel, OpIndex: NonOpDiagnostic,
			Message: strings.TrimSpace(line),
		})
	}
	return &ValidationError{
		Msg:   "批准提案的原子预演失败，本次零写入（overlay 整体丢弃，实盘与 Git 全程未变）：" + err.Error(),
		Diags: diags,
	}
}
