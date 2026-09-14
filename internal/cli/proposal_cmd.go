package cli

// `eg proposal` 的命令外壳与四条子命令实现（提案合同 §7.1 命令表 S2 行；T-…-040）。
//
// # A-23 直写例外（本文件存在的理由）
//
// `eg proposal new` / `approve` **直写 `proposals/`**（§10.2 时序第 4 步），
// 不合成 ChangePlan、不走 plan.Execute：提案是**控制面**产物，不是知识数据。
// 因此这里直接调 internal/proposal 的 store API（它内部仍走 guarded 写口，不裸写文件）。
// 批准之后对知识数据的真实改动仍走 ChangePlan 的 delete op（A-23 第 4 条）。
//
// # 阶段边界（本 task 不做，下一层做）
//
//   - `approve` 的确认门（`--confirm` / 退出码 6）、U-12 守卫与执行前重算编排已接线，
//     实现单列在 proposal_approve.go；**删除类的逐文件写入仍不做** —— 属 T-…-041。
//     `eg proposal approve --help` 必须退 0（框架层在 Handler 之前就打印用法）。
//   - 不实现 `eg delete` / `undelete`（T-…-041）、不实现四态状态机本体与 E8 / E9 判定
//     （T-…-034 已有，这里只调用）、不实现 `superseded` 触发算法（T-…-035 已有）。
//   - `stale_reviews` 的自动判定归 S3：影响面四项一律取 proposal.RecomputeImpact 的结论，
//     本文件不自己算、也不改它的口径。
//
// # 只读与写的分界
//
// `list` / `show` 是**纯只读**：不 commit、不落 last-report、不回写任何时间戳
// （提案根本没有 reviewed_at / updated_at 两键，M-6），因此在任何仓库状态下工作区都不变。
// `new` / `reject` 各产生**恰一次** commit（verb = proposal，§10.2 时序第 5 步）。
//
// # [M6 · T-…-072 批次 C4a] 事务化：`new` / `reject` 均已接入 A 类强事务
//
// M6 原子性合同 §16.1 把「产生权威写入的写命令」归入 A 类，必须全程只持**同一把**
// `.index/run.lock` 走完固定时序。本批次迁移 `new`（见 runProposalNewCritical）与
// `reject`（见 runProposalRejectCritical），两者共用下述同一套 S0~S9 时序（以 `new` 为例）：
//
//	S0 锁外只解析参数（--type / --target / --reason 的形态，已由 Validate 判过）
//	S1 取 run.lock
//	S2 崩溃恢复屏障（临界区内第一件事，早于任何一次业务读）
//	S3 锁内重建 Store，重算影响面四项、重扫在库提案 ID 并现取当日序号
//	S4 Store atomic overlay 预演：proposal.Create 只落 overlay，
//	   accepted write-set 必须**恰含新提案一份**（实盘与 Git 全程零变化）
//	S5 AllocateTxnID → RecordTxnID → WriteIntent（发布屏障）
//	S6 txn.Commit：commit marker 落盘之前，提案一律不算创建成功
//	S7 Git commit（仍在锁内、严格晚于 commit marker）
//	S8 唯一 syncIndexAfterWrite（Git 成败都走，仍在锁内、早于 Release）
//	S9 显式释放锁；报告渲染、last-report 落盘与退出码裁决全部回到锁外
//
// 为什么 S3 的三件事一件都不能留在锁外：影响面四项要当**创建前**的事实（EG-CFM-05），
// 而 S2 刚可能把某份笔记 / 卡回滚到前像；当日序号要看**当下**在库提案 —— 取锁之前
// 另一个写者可能刚创建过同一天的提案，锁外算出来的 seq 会撞号。
//
// `txn_id` 的审计边界是「AllocateTxnID 成功」而非「提交成功」（A-59）：号码一旦到手，
// intent 失败 / S6 回滚 / Git 失败 / 全链路成功四种结局都必须把它交进 `data.report`。
// **Git 之后绝不再写一个字节权威内容**：commit marker 已经定盘，Git 失败只是「这份提案
// 没进版本历史」，退 4、保留目标态、不回滚、不二次写（B4）。
//
// `reject` 已同样接入上述强事务（见 runProposalRejectCritical）：与 `new` 的差异仅在
// S3 锁内重载被拒提案、S4 用 proposal.MarkRejected 预演、S6 后锁内重读 rejected 现态、
// 以及 Git 失败保留 rejected 目标态。`approve` 亦已接入（C4b，见 proposal_approve.go 的
// runProposalApproveCritical），至此四条提案写命令全部走同一套 A 类强事务，旧的
// finishProposalWrite 收尾函数已随之删除。

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/git"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/plan"
	"github.com/ikaqiu-Lemon/EverGreen/internal/proposal"
	"github.com/ikaqiu-Lemon/EverGreen/internal/report"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// 五个子命令名（**恰五个**：合同 §7.1 的 S2 行逐字，show 不得漏交付）。
const (
	SubProposalNew     = "new"
	SubProposalList    = "list"
	SubProposalShow    = "show"
	SubProposalApprove = "approve"
	SubProposalReject  = "reject"
)

// ProposalSubcommands 返回 `eg proposal` 的子命令集合（顺序即 --help 顺序）。
func ProposalSubcommands() []string {
	return []string{SubProposalNew, SubProposalList, SubProposalShow,
		SubProposalApprove, SubProposalReject}
}

func proposalCommand() *Command {
	return &Command{
		Name:        "proposal",
		Display:     "proposal new|list|show|approve|reject",
		Summary:     "提案子系统：新建 / 列表 / 查看 / 批准 / 拒绝（new 与 reject 产生 commit verb=proposal）",
		Owner:       "T-evergreen.s1_main_flow-158614-040",
		Subs:        ProposalSubcommands(),
		SubRequired: true,
		Usage: `eg proposal new --type <t> --target <id> [--reason <text>] [--json]
eg proposal list [--status <s>] [--execution <e>] [--json]
eg proposal show <p-id> [--json]
eg proposal approve <p-id> --confirm --user-request [--json]
eg proposal reject <p-id> --reason <text> [--json]

参数（提案合同 §7.1 命令表 S2 行）：
  --type <t>        new 必填；v1 唯一类型 logical_delete
  --target <id>     new 必填；被逻辑删除的对象 ID
  --reason <text>   reject 必填（十项必备 ⑨「拒绝必带 reason」）；new 可选
  --status <s>      list 可选；pending | approved | rejected | superseded
  --execution <e>   list 可选；not_started | succeeded | failed（与 status 正交，取交集）
  --confirm         approve 必填；执行前的**唯一一次**确认（缺它 → 退 6、零写入）
  --user-request    approve 必填；U-12 要求批准由用户显式发起（Agent 路径 → 退 2、零写入）

new 允许 Agent 调用（§10.3「可提不可执」）：不要求 --user-request。
创建前直接扫描 Markdown 与 frontmatter 算影响面四项（S2 不依赖任何派生缓存）；
十项必备缺项按 W9 处理：照常创建 + 进 warnings[]，绝不拒绝创建。
list / show 只读：零文件变化、零 commit，可任意次调用。
approve 只允许用户显式路径（U-12）：缺 --user-request 退 2、零写入；
approve 缺 --confirm 退 6：零写入、零 commit，权威 Markdown 完全不变；
approve 执行前必须重算影响面并与 impact 比对，前提已变则改判 superseded 且不执行。
退出码：0 | 1 参数非法（零写入） | 2 校验失败（零写入） | 4 Git 提交失败 | 6 仅缺 --confirm
`,
		Flags: func(fs *flagSet) {
			fs.String("type", "", "提案类型（v1 唯一：logical_delete）")
			fs.String("target", "", "被逻辑删除的对象 ID")
			fs.String("reason", "", "理由")
			fs.String("status", "", "按 status 过滤")
			fs.String("execution", "", "按 execution.status 过滤")
			fs.Bool(ApproveConfirmFlag, false, "approve 执行前的唯一一次确认（缺它 → 退 6、零写入）")
		},
		Validate: validateProposalArgs,
	}
}

// validateProposalArgs 只做**参数形态**校验（缺必填 / 多余位置参数 → 退 1、零写入）。
//
// 取值是否落在封闭集合内（status / execution）也在这里判：拼错的状态名不能被静默
// 当成「查不到」，那会让调用方误判库里没有这类提案。
func validateProposalArgs(inv *Invocation) error {
	switch inv.Sub {
	case SubProposalNew:
		if err := noPositionalArgs(inv); err != nil {
			return err
		}
		t := strings.TrimSpace(inv.String("type"))
		if t == "" {
			return &UsageError{Msg: "eg proposal new 缺必填参数 --type <t>（v1 唯一类型 logical_delete）"}
		}
		if !proposal.Type(t).Valid() {
			return &UsageError{Msg: fmt.Sprintf(
				"eg proposal new 的 --type = %q 不在封闭集合内：v1 唯一类型是 %s",
				t, proposal.TypeLogicalDelete)}
		}
		if strings.TrimSpace(inv.String("target")) == "" {
			return &UsageError{Msg: "eg proposal new 缺必填参数 --target <id>：提案必须点名删除对象"}
		}
		return nil
	case SubProposalList:
		if err := noPositionalArgs(inv); err != nil {
			return err
		}
		if err := proposalFilter(inv).Validate(); err != nil {
			return &UsageError{Msg: "eg proposal list 的过滤条件非法：" + err.Error()}
		}
		return nil
	case SubProposalShow, SubProposalApprove, SubProposalReject:
		if len(inv.Args) != 1 {
			return &UsageError{Msg: fmt.Sprintf(
				"eg proposal %s 需要恰 1 个位置参数 <p-id>，实际 %d 个", inv.Sub, len(inv.Args))}
		}
		if !proposal.ID(inv.Args[0]).Valid() {
			return &UsageError{Msg: fmt.Sprintf(
				"提案 ID %q 形态非法：必须是 p-<yyyymmdd>-<3d>", inv.Args[0])}
		}
		if inv.Sub == SubProposalReject &&
			(!inv.Set("reason") || strings.TrimSpace(inv.String("reason")) == "") {
			return &UsageError{Msg: "eg proposal reject 缺必填参数 --reason <text>（十项必备 ⑨：拒绝必带 reason）"}
		}
		return nil
	}
	return &UsageError{Msg: fmt.Sprintf("eg proposal 必须带子命令：%s",
		strings.Join(ProposalSubcommands(), " | "))}
}

// proposalFilter 把 --status / --execution 组装成检索条件（空串 = 该维不过滤）。
func proposalFilter(inv *Invocation) proposal.Filter {
	return proposal.Filter{
		Status: proposal.Status(strings.TrimSpace(inv.String("status"))),
		Exec:   proposal.ExecStatus(strings.TrimSpace(inv.String("execution"))),
	}
}

// runProposal 按子命令分派（子命令集合恰五个，由注册表保证）。
func (r *Root) runProposal(inv *Invocation) (*Result, error) {
	switch inv.Sub {
	case SubProposalNew:
		return r.runProposalNew(inv)
	case SubProposalList:
		return r.runProposalList(inv)
	case SubProposalShow:
		return r.runProposalShow(inv)
	case SubProposalReject:
		return r.runProposalReject(inv)
	case SubProposalApprove:
		return r.runProposalApprove(inv)
	}
	return nil, &UsageError{Msg: fmt.Sprintf("eg proposal 未知子命令 %q", inv.Sub)}
}

// —— ① new：A 类强事务直写 proposals/，一次 commit（M6 · T-…-072 批次 C4a）——

// runProposalNew 的 S0（锁外）只有一件事：把参数原样带进临界区（形态已由 Validate 判过）。
//
// 报告体在**进临界区之前**建好并全程传引用：S1 的 W28、S2 的 W26 讲的是「进临界区时
// 库里发生过什么」，与本次创建了什么无关，必须落进最终产物。
func (r *Root) runProposalNew(inv *Invocation) (*Result, error) {
	rep := report.New()
	return r.runProposalNewCritical(inv, &rep,
		strings.TrimSpace(inv.String("target")), inv.String("reason"))
}

// runProposalNewCritical 是 `eg proposal new` 的整个临界区：进函数即取锁，出函数即释放锁。
//
// 时序见文件头 S1~S9。原子域**恰是新提案一份文件**：要么它连同 commit marker 一起生效，
// 要么一个字节都不落盘。锁内做完 S8 与全部报告事实采集后显式 release，报告渲染与
// last-report 落盘一律回到锁外。
func (r *Root) runProposalNewCritical(inv *Invocation, rep *report.Report,
	target, reason string) (*Result, error) {
	targets := []string{target}

	// —— S1 + S2：取锁、崩溃恢复屏障（与 plan 写链 / delete / mark-reviewed 共用同一把锁）。——
	sess, serr := r.enterTxnCritical(inv, reportWarnSink{rep}, txnCriticalOpts{
		ZeroWrite: "本次零写入", ReleaseNotice: "本次写入与提交不受影响",
	})
	if serr != nil {
		return nil, serr
	}
	defer sess.release()

	// —— S3：锁内重建 Store，重算影响面四项、重扫在库提案并现取当日序号。——
	//
	// 严禁把这三步挪到锁外：影响面四项要当**创建前**的事实（EG-CFM-05），而 S2 刚可能把
	// 某份笔记 / 卡回滚到前像；当日序号要看**当下**在库提案 —— 取锁之前别的写者可能刚建过
	// 同一天的提案，锁外算出来的 seq 会撞号。
	st := store.New(inv.VaultRoot)
	impact, err := proposal.RecomputeImpact(st, targets)
	if err != nil {
		return nil, err
	}
	existing, err := proposal.ListIDs(st)
	if err != nil {
		return nil, err
	}
	date := model.NewDate(r.now())
	seq, err := proposal.NextSeq(date.Compact(), existing)
	if err != nil {
		return nil, err
	}
	id, err := proposal.NewID(date, seq)
	if err != nil {
		return nil, err
	}
	newRel := proposal.Rel(id)
	fireTxnStep(TxnStepReread, "")

	// —— S4：原子预演。proposal.Create 复用 guarded 写口，实盘与 Git 全程零变化。——
	if err := st.BeginAtomic(); err != nil {
		return nil, blockedError("原子预演无法开始，本次零写入", err)
	}
	// 兜底丢弃：到导出 write-set 之前的每一条 return 都必须是「零生效」。
	defer st.EndAtomic()

	if _, err := proposal.Create(st, proposal.Template{
		ID:        id,
		Title:     fmt.Sprintf("逻辑删除 %s", target),
		CreatedAt: date.String(),
		Targets:   targets,
		Impact:    impact,
	}); err != nil {
		// 预演期写失败 ⇒ overlay 整体丢弃即零写入：不开事务、不发 intent、不跑 Git。
		return nil, err
	}

	ws := st.AtomicWriteSet()
	if aerr := checkSingleProposalWriteSet(ws, newRel); aerr != nil {
		return nil, blockedError("原子域与本次新建范围不一致，本次零写入", aerr)
	}
	fireTxnStep(TxnStepExecuted, "")

	summary := []string{fmt.Sprintf(
		"提案 %s 已创建：%s（status=%s，execution=%s；批准后的删除仍走 ChangePlan 的 delete op）",
		id, newRel, proposal.StatusPending, proposal.ExecNotStarted)}
	summary = append(summary, impactLines(impact)...)

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
			"新建提案未提交：事务 %s 已分配号码但未闭合，本次零权威写入（目标 %s）",
			txnID, target)})
		r.saveReport(inv.VaultRoot, res, ExitCodeFor(classifyExit5(oerr)))
		return res, oerr
	}

	// —— S6：原子提交。commit marker 在盘之前，新提案一律不算创建成功。——
	//
	// 两种失败必须分开翻译（合同 §5.2 vs §16），与 runPlan / delete / mark-reviewed 同源。
	cres, cerr := sess.commitWriteSet(txnID, ws)
	switch {
	case cerr != nil && cres != nil && cres.RolledBack:
		rep.AddWarning(unnumberedWarning("ops",
			"原子提交失败，事务 %s 已按合同 §5.2 主动放弃：%v", txnID, cerr))
		rep.AddWarning(unnumberedWarning(newRel,
			"目标未写入：本次事务已整体回滚，该提案未留在库里（磁盘回到事务开始前）"))
		rep.SetCommit("")
		sess.release()
		res := proposalResult(*rep, []string{fmt.Sprintf(
			"新建提案未生效：事务 %s 已整体回滚，提案 %s 未留在库里（目标 %s）",
			txnID, id, target)})
		r.saveReport(inv.VaultRoot, res, ExitPartialWrite)
		return res, &PartialWriteError{Msg: fmt.Sprintf(
			"原子提交失败，事务 %s 已整体回滚：新提案一个字节都没有写入、"+
				"磁盘保持事务开始前的状态、未产生 commit（原因：%v）", txnID, cerr)}
	case cerr != nil:
		blocked := blockedError(fmt.Sprintf(
			"事务 %s 的原子提交失败且未能收敛为已回滚状态，需人工处置", txnID), cerr)
		rep.SetCommit("")
		sess.release()
		res := proposalResult(*rep, []string{fmt.Sprintf(
			"新建提案未闭合：事务 %s 的提交既未完成也未回滚，请人工核对（目标 %s）",
			txnID, target)})
		r.saveReport(inv.VaultRoot, res, ExitCodeFor(classifyExit5(blocked)))
		return res, blocked
	}
	fireTxnStep(TxnStepCommitted, txnID)

	// —— S7：Git。严格晚于 commit marker、仍持同一把锁（verb = proposal，§10.2 时序第 5 步）。——
	repo := r.repo(inv.VaultRoot)
	// 既有改动同样会被 add -A 带进本次 commit：采样早于 Add，如实披露（I-…-023）。
	noteExistingChangesInReport(rep, repo, writeSetPaths(ws))
	info, gerr := repo.Commit(git.Message{
		Verb:           string(model.VerbProposal),
		Domain:         commitDomain(proposal.DirProposals),
		Subject:        fmt.Sprintf("新建提案 %s（%s）", id, target),
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
	// 提案不是知识卡，索引侧多半无事可做；照走不误是为了不给「从 S7 提前 return」开先例。
	r.syncIndexAfterWrite(rep, inv.VaultRoot, indexWriteLabel(inv), writeSetPaths(ws))
	fireTxnStep(TxnStepIndexSync, txnID)

	// 报告所需的磁盘事实（新提案现态 + W9 缺项）此刻才采集：commit marker 已定盘，
	// 重读到的就是本次写入后的真实字节（原输出事实：links[] / W9 / proposals[] 保持不变）。
	rep.Links = append(rep.Links, newRel)
	noteProposalGaps(rep, st, newRel)
	rep.AddProposal(report.ProposalEntry{
		ID: string(id), Path: newRel, Status: string(proposal.StatusPending),
		Targets: targets,
		Execution: report.ProposalExecution{
			Status: string(proposal.ExecNotStarted), GitCommit: nil,
			WrittenPaths: []string{}, UnwrittenPaths: []string{},
		},
	})

	// —— S9：报告所需的锁内事实已采集完，这里显式还锁；之后只剩纯渲染与退出码裁决。——
	sess.release()

	if gerr != nil {
		// B4：Git 失败退 4。新提案已由 commit marker 定盘并保持目标态，
		// **不回滚、不做第二次权威写**。
		rep.SetCommit("")
		res := proposalResult(*rep, append(summary,
			"  Git 提交失败："+oneLineReason(gerr.Error())))
		r.saveReport(inv.VaultRoot, res, ExitCommitFailed)
		return res, &CommitFailedError{
			Msg: "Git 提交失败：新提案已原子写入磁盘并保持现状，未做任何还原（B4）",
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

// checkSingleProposalWriteSet 钉死「本次恰写一份提案」：accepted write-set 长度必须为 1，
// 且那一份的路径恰等于 wantRel。任何偏离都带上**实际路径**返回错误，便于定位漏出的原子域。
func checkSingleProposalWriteSet(ws []store.AtomicFileSpec, wantRel string) error {
	if len(ws) == 1 && ws[0].Path == wantRel {
		return nil
	}
	got := make([]string, 0, len(ws))
	for _, f := range ws {
		got = append(got, f.Path)
	}
	return fmt.Errorf("accepted write-set 应恰含提案 %s 一份文件，实得 %v", wantRel, got)
}

// impactLines 把影响面四项渲染成报告行（数量守恒，不折叠）。
func impactLines(im proposal.Impact) []string {
	return []string{
		fmt.Sprintf("  影响面 %s：%d 项", proposal.KeyExitsDefaultView, len(im.ExitsDefaultView)),
		fmt.Sprintf("  影响面 %s：%d 项", proposal.KeyCardsLosingSupport, len(im.CardsLosingSupport)),
		fmt.Sprintf("  影响面 %s：%d 条", proposal.KeyAffectedMaterialRels, im.AffectedMaterialRels),
		fmt.Sprintf("  影响面 %s：%d 条", proposal.KeyAffectedRelations, im.AffectedRelations),
		fmt.Sprintf("  影响面 %s：%d 项（只提示，自动判定属 S3）", proposal.KeyStaleReviews, len(im.StaleReviews)),
	}
}

// noteProposalGaps 判 W9（十项必备缺项）：**照常创建** + 进 warnings[]，绝不拒绝创建
// （提案合同 §10 章首「缺项按 warning 处理而不是拒绝创建」）。
//
// 判据只看**刚落盘的字节**：模板把七个分区先留着、decision / execution 留空骨架，
// 因此新建的提案必然带若干缺项 —— 这正是合同预期的形态，缺项由使用反馈逐步补齐。
func noteProposalGaps(rep *report.Report, st *store.Store, rel string) {
	rec, err := proposal.LoadRel(st, rel)
	if err != nil {
		rep.AddWarning(unnumberedWarning(rel, "提案已落盘但复读失败，缺项未能登记：%v", err))
		return
	}
	gaps := proposalGaps(rec)
	if len(gaps) == 0 {
		return
	}
	rep.AddWarning(report.Diagnostic{
		Code: plan.W9, Level: report.LevelWarning, Path: rel, OpIndex: report.NonOp,
		Message: fmt.Sprintf("提案十项必备缺项（照常创建，不拒绝）：%s", strings.Join(gaps, "、")),
	})
}

// proposalGaps 列出十项必备里**当前为空**的项（顺序固定，可复算）。
func proposalGaps(rec proposal.Record) []string {
	p := rec.Proposal()
	var gaps []string
	for _, sec := range proposal.BodySections() {
		if strings.TrimSpace(string(rec.File.SectionBody(sec))) == "" {
			gaps = append(gaps, "正文分区「"+sec+"」为空")
		}
	}
	if p.Decision.Result == "" && p.Decision.Reason == "" && p.Decision.SupersededBy == "" {
		gaps = append(gaps, proposal.KeyDecision+" 子字段未填（尚无用户决定）")
	}
	if p.Execution.AttemptedAt == "" {
		gaps = append(gaps, proposal.KeyExecBlock+" 子字段未填（尚未执行）")
	}
	return gaps
}

// —— ② list：只读，零副作用 ——

func (r *Root) runProposalList(inv *Invocation) (*Result, error) {
	st := store.New(inv.VaultRoot)
	f := proposalFilter(inv)
	recs, err := proposal.Search(st, f)
	if err != nil {
		return nil, &ValidationError{Msg: "提案检索失败：" + err.Error()}
	}
	items := make([]map[string]interface{}, 0, len(recs))
	summary := make([]string, 0, len(recs)+1)
	summary = append(summary, fmt.Sprintf("提案 %d 份%s（只读：零文件变化、零 commit）",
		len(recs), filterLabel(f)))
	for _, rec := range recs {
		p := rec.Proposal()
		items = append(items, map[string]interface{}{
			"id": string(rec.ID), "path": rec.Rel, "status": string(p.Status),
			"execution": string(p.Execution.Status), "created_at": p.CreatedAt,
			"targets": nonNilStrings(p.Targets), "title": rec.File.Title(),
		})
		summary = append(summary, fmt.Sprintf("  %s  %-10s  %-11s  %s",
			rec.ID, p.Status, p.Execution.Status, rec.File.Title()))
	}
	return &Result{
		Data:      map[string]interface{}{"proposals": items, "count": len(items)},
		DataOrder: []string{"proposals", "count"},
		Summary:   summary,
	}, nil
}

func filterLabel(f proposal.Filter) string {
	var parts []string
	if f.Status != "" {
		parts = append(parts, proposal.KeyStatus+"="+string(f.Status))
	}
	if f.Exec != "" {
		parts = append(parts, proposal.KeyExecBlock+"."+proposal.KeyExecStatus+"="+string(f.Exec))
	}
	if len(parts) == 0 {
		return ""
	}
	return "（过滤：" + strings.Join(parts, "，") + "）"
}

// —— ③ show：只读，输出 status / execution / decision / impact 与七个 H2 正文 ——

func (r *Root) runProposalShow(inv *Invocation) (*Result, error) {
	st := store.New(inv.VaultRoot)
	rec, err := proposal.Load(st, proposal.ID(inv.Args[0]))
	if err != nil {
		return nil, proposalLoadError(inv.Args[0], err)
	}
	p := rec.Proposal()
	sections := make([]map[string]interface{}, 0, len(proposal.BodySections()))
	for _, name := range proposal.BodySections() {
		sections = append(sections, map[string]interface{}{
			"name": name, "body": string(rec.File.SectionBody(name)),
		})
	}
	data := map[string]interface{}{
		"id": string(rec.ID), "path": rec.Rel, "title": rec.File.Title(),
		"type": string(p.Type), "created_at": p.CreatedAt,
		"status": string(p.Status), "targets": nonNilStrings(p.Targets),
		"impact": map[string]interface{}{
			proposal.KeyExitsDefaultView:     nonNilStrings(p.Impact.ExitsDefaultView),
			proposal.KeyCardsLosingSupport:   nonNilStrings(p.Impact.CardsLosingSupport),
			proposal.KeyAffectedMaterialRels: p.Impact.AffectedMaterialRels,
			proposal.KeyAffectedRelations:    p.Impact.AffectedRelations,
			proposal.KeyStaleReviews:         nonNilStrings(p.Impact.StaleReviews),
		},
		"decision": map[string]interface{}{
			proposal.KeyResult:       string(p.Decision.Result),
			proposal.KeyReason:       p.Decision.Reason,
			proposal.KeySupersededBy: p.Decision.SupersededBy,
		},
		"execution": map[string]interface{}{
			proposal.KeyExecStatus:     string(p.Execution.Status),
			proposal.KeyAttemptedAt:    p.Execution.AttemptedAt,
			proposal.KeyExecReason:     p.Execution.Reason,
			proposal.KeyGitCommit:      p.Execution.GitCommit,
			proposal.KeyWrittenPaths:   nonNilStrings(p.Execution.WrittenPaths),
			proposal.KeyUnwrittenPaths: nonNilStrings(p.Execution.UnwrittenPaths),
		},
		"sections": sections,
	}
	summary := []string{
		fmt.Sprintf("提案 %s（%s）：status=%s，execution=%s（只读：零文件变化、零 commit）",
			rec.ID, rec.Rel, p.Status, p.Execution.Status),
		fmt.Sprintf("  decision：result=%q reason=%q superseded_by=%q",
			p.Decision.Result, p.Decision.Reason, p.Decision.SupersededBy),
	}
	summary = append(summary, impactLines(p.Impact)...)
	for _, sec := range sections {
		summary = append(summary, fmt.Sprintf("  ## %s", sec["name"]))
		if body := strings.TrimRight(sec["body"].(string), "\n"); body != "" {
			summary = append(summary, body)
		}
	}
	return &Result{
		Data: data,
		DataOrder: []string{"id", "path", "title", "type", "created_at", "status",
			"targets", "impact", "decision", "execution", "sections"},
		Summary: summary,
	}, nil
}

// —— ④ reject：T2 落盘 + 一次 commit ——

func (r *Root) runProposalReject(inv *Invocation) (*Result, error) {
	rep := report.New()
	return r.runProposalRejectCritical(inv, &rep,
		proposal.ID(inv.Args[0]), strings.TrimSpace(inv.String("reason")))
}

// runProposalRejectCritical 是 `eg proposal reject` 的整个临界区：进函数即取锁，出函数即释放锁。
//
// 时序见文件头 S1~S9。原子域**恰是被拒提案那一份文件**（T2：pending → rejected 只改
// status 与 decision 两处）：要么它连同 commit marker 一起生效，要么一个字节都不落盘。
// 锁内做完 S8 与全部报告事实采集后显式 release，报告渲染与 last-report 落盘一律回到锁外。
func (r *Root) runProposalRejectCritical(inv *Invocation, rep *report.Report,
	id proposal.ID, reason string) (*Result, error) {
	// —— S1 + S2：取锁、崩溃恢复屏障（与 new / plan 写链 / delete / mark-reviewed 共用同一把锁）。——
	sess, serr := r.enterTxnCritical(inv, reportWarnSink{rep}, txnCriticalOpts{
		ZeroWrite: "本次零写入", ReleaseNotice: "本次写入与提交不受影响",
	})
	if serr != nil {
		return nil, serr
	}
	defer sess.release()

	// —— S3：锁内重建 Store、锁内加载被拒提案。——
	//
	// 严禁复用锁外快照：S2 刚可能把某份提案回滚到前像，取锁之前别的写者也可能刚改过它的
	// 状态；E8 / E9 的判定必须落在**当下**盘上的字节。
	st := store.New(inv.VaultRoot)
	rec, err := proposal.Load(st, id)
	if err != nil {
		return nil, proposalLoadError(inv.Args[0], err)
	}
	fireTxnStep(TxnStepReread, "")

	// —— S4：原子预演。proposal.MarkRejected 复用 guarded 写口，实盘与 Git 全程零变化。——
	if err := st.BeginAtomic(); err != nil {
		return nil, blockedError("原子预演无法开始，本次零写入", err)
	}
	// 兜底丢弃：到导出 write-set 之前的每一条 return 都必须是「零生效」。
	defer st.EndAtomic()

	// E8 / E9 的判定全在 internal/proposal（终态出边 → E9、status 与 decision.result
	// 不一致 → E8）；这里只接线：失败即零写入（overlay 整体丢弃），退 2。
	if _, err := proposal.MarkRejected(proposal.RejectSpec{
		Store: st, Original: rec.ID, OriginalRel: rec.Rel, Reason: reason,
	}); err != nil {
		return nil, proposalStateError(rec.Rel, err)
	}

	ws := st.AtomicWriteSet()
	if aerr := checkSingleProposalWriteSet(ws, rec.Rel); aerr != nil {
		return nil, blockedError("原子域与本次拒绝范围不一致，本次零写入", aerr)
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
			"拒绝提案未提交：事务 %s 已分配号码但未闭合，本次零权威写入（提案 %s）",
			txnID, rec.ID)})
		r.saveReport(inv.VaultRoot, res, ExitCodeFor(classifyExit5(oerr)))
		return res, oerr
	}

	// —— S6：原子提交。commit marker 在盘之前，拒绝一律不算生效。——
	//
	// 两种失败必须分开翻译（合同 §5.2 vs §16），与 runPlan / delete / mark-reviewed / new 同源。
	cres, cerr := sess.commitWriteSet(txnID, ws)
	switch {
	case cerr != nil && cres != nil && cres.RolledBack:
		rep.AddWarning(unnumberedWarning("ops",
			"原子提交失败，事务 %s 已按合同 §5.2 主动放弃：%v", txnID, cerr))
		rep.AddWarning(unnumberedWarning(rec.Rel,
			"目标未写入：本次事务已整体回滚，该提案保持原状态（磁盘回到事务开始前）"))
		rep.SetCommit("")
		sess.release()
		res := proposalResult(*rep, []string{fmt.Sprintf(
			"拒绝未生效：事务 %s 已整体回滚，提案 %s 未落 rejected（磁盘保持事务开始前的状态）",
			txnID, rec.ID)})
		r.saveReport(inv.VaultRoot, res, ExitPartialWrite)
		return res, &PartialWriteError{Msg: fmt.Sprintf(
			"原子提交失败，事务 %s 已整体回滚：被拒提案一个字节都没有写入、"+
				"磁盘保持事务开始前的状态、未产生 commit（原因：%v）", txnID, cerr)}
	case cerr != nil:
		blocked := blockedError(fmt.Sprintf(
			"事务 %s 的原子提交失败且未能收敛为已回滚状态，需人工处置", txnID), cerr)
		rep.SetCommit("")
		sess.release()
		res := proposalResult(*rep, []string{fmt.Sprintf(
			"拒绝未闭合：事务 %s 的提交既未完成也未回滚，请人工核对（提案 %s）",
			txnID, rec.ID)})
		r.saveReport(inv.VaultRoot, res, ExitCodeFor(classifyExit5(blocked)))
		return res, blocked
	}
	fireTxnStep(TxnStepCommitted, txnID)

	// —— S7：Git。严格晚于 commit marker、仍持同一把锁（verb = proposal，§10.2 时序第 5 步）。——
	repo := r.repo(inv.VaultRoot)
	// 既有改动同样会被 add -A 带进本次 commit：采样早于 Add，如实披露（I-…-023）。
	noteExistingChangesInReport(rep, repo, writeSetPaths(ws))
	info, gerr := repo.Commit(git.Message{
		Verb:           string(model.VerbProposal),
		Domain:         commitDomain(proposal.DirProposals),
		Subject:        fmt.Sprintf("拒绝提案 %s", rec.ID),
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
	r.syncIndexAfterWrite(rep, inv.VaultRoot, indexWriteLabel(inv), writeSetPaths(ws))
	fireTxnStep(TxnStepIndexSync, txnID)

	// 报告所需的磁盘事实（被拒提案现态）此刻才采集：commit marker 已定盘，
	// 重读到的就是本次写入后的 rejected 字节（原输出事实：links[] / proposals[] / summary 保持不变）。
	rep.Links = append(rep.Links, rec.Rel)
	after, lerr := proposal.LoadRel(st, rec.Rel)
	if lerr != nil {
		sess.release()
		return nil, lerr
	}
	p := after.Proposal()
	rep.AddProposal(report.ProposalEntry{
		ID: string(after.ID), Path: after.Rel, Status: string(p.Status),
		Targets: nonNilStrings(p.Targets),
		Execution: report.ProposalExecution{
			Status: string(p.Execution.Status), GitCommit: nil,
			WrittenPaths: []string{}, UnwrittenPaths: []string{},
		},
	})
	summary := []string{fmt.Sprintf(
		"提案 %s 已拒绝：%s（status=%s，decision.result=%s；rejected 是终态，无任何合法出边）",
		after.ID, after.Rel, p.Status, p.Decision.Result)}

	// —— S9：报告所需的锁内事实已采集完，这里显式还锁；之后只剩纯渲染与退出码裁决。——
	sess.release()

	if gerr != nil {
		// B4：Git 失败退 4。被拒提案已由 commit marker 定盘并保持 rejected 目标态，
		// **不回滚、不做第二次权威写**。
		rep.SetCommit("")
		res := proposalResult(*rep, append(summary,
			"  Git 提交失败："+oneLineReason(gerr.Error())))
		r.saveReport(inv.VaultRoot, res, ExitCommitFailed)
		return res, &CommitFailedError{
			Msg: "Git 提交失败：被拒提案已原子写入磁盘并保持 rejected 现状，未做任何还原（B4）",
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

// —— ⑤ approve：确认门 + U-12 守卫 + 执行前重算，实现在 proposal_approve.go ——
//
// 编排（含退出码 6 与 --confirm 语义）单列一层，理由见 proposal_approve.go 文件头。

func proposalResult(rep report.Report, summary []string) *Result {
	res := &Result{
		Data:      map[string]interface{}{"report": rep},
		DataOrder: []string{"report"},
		Warnings:  toCLIReportDiags(rep.Warnings),
	}
	res.Summary = append(summary, rep.Lines()...)
	return res
}

// proposalLoadError 把「找不到提案」判成**校验失败**（退 2、零写入）：
// ID 形态在 Validate 阶段已经判过，走到这里说明形态合法但库里没有，属事实问题而非用法问题。
func proposalLoadError(id string, err error) error {
	if errors.Is(err, proposal.ErrProposalNotFound) {
		return &ValidationError{
			Msg: fmt.Sprintf("提案 %s 不存在（已扫遍 %s/，ID 才是主键）", id, proposal.DirProposals),
			Diags: []Diagnostic{{
				Code: E18, Level: LevelError, Path: proposal.DirProposals, OpIndex: NonOpDiagnostic,
				Message: err.Error(), Target: id,
			}},
		}
	}
	return &ValidationError{Msg: fmt.Sprintf("提案 %s 不可读：%v", id, err)}
}

// proposalStateError 把 internal/proposal 的结构化违规转成带编号的校验失败（退 2、零写入）。
//
// 编号只由 internal/plan 发（E8 枚举 / 一致性、E9 非法迁移含终态出边）：
// 本文件不写编号字面量，也不自己判「哪条规则不成立」。
func proposalStateError(rel string, err error) error {
	code := plan.CodeForProposalViolation(err)
	return &ValidationError{
		Msg: "提案状态校验失败（零写入）：" + err.Error(),
		Diags: []Diagnostic{{
			Code: code, Level: LevelError, Path: rel, OpIndex: NonOpDiagnostic,
			Message: err.Error(),
		}},
	}
}

// nonNilStrings 让空清单序列化成 `[]` 而不是 `null`（报告与 --json 的空值口径一致）。
func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	out := append([]string{}, in...)
	return out
}
