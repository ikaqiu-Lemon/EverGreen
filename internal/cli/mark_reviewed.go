package cli

// `eg mark-reviewed --target <id>` 的**命令侧编排**：`reviewed_at` 的**唯一**写入路径
// （提案与状态合同 §6.1、授权合同 §2 矩阵 #7 / #22「P-A 🔴 / P-U ✅ 仅 eg mark-reviewed」、
// §9 A-15 / A-16）；T-evergreen.s1_main_flow-158614-042。
//
// # 为什么是 CLI 直写而不是走 ChangePlan（A-23 同一档）
//
// 矩阵 #7 / #22 的 P-A 格是 🔴：Agent 自动路径**一律不更新** reviewed_at，因此
// plan 层的 `mark_reviewed` op 在 P-A 下被矩阵拦住、在 P-U 下也只走校验链不产出 action
// （见 internal/plan/validate_m3.go 的 markReviewed）。落盘因此只能发生在**用户显式敲命令**
// 这条路径上，与 delete / undelete 同为命令侧编排；写口仍然唯一：
// 只经 store.ApplyStateWrite（StateWriteReviewedAt 形态），本文件不呼任何 setter、
// 也不复制一份 frontmatter 改写逻辑，注入点复用同一个 Root.StateWrite。
//
// # 为什么可以在进程边界置 UserRequest（授权合同 N-1 反伪造条款 + 风险分级）
//
// N-1 约束的是「Agent 在**文件内容**里写 initiator: user 自证」；用户在终端敲
// `eg mark-reviewed` 本身发生在进程边界上，即命令行佐证。风险等级归入**低风险、
// 用户显式**那一档（与 deprecate / restore / replaced-by / undelete 同级）：
//   - 授权合同 §3 X1 把本命令的「需确认」列写作**否**；
//   - 它只写一个**只读信号**（ADR-20），既不改状态、也不改内容、更不销毁任何东西；
//   - 因此不要求再手敲 --user-request，也**不在退出码 6 的白名单**里（白名单恰
//     proposal approve 与 delete 两条）。
//
// # 三条不可放宽的不变式
//
//   - **只写 reviewed_at 一个键**：status 与删除维度（deleted_at / deleted_reason）
//     一格不碰——三者是彼此正交的维度。
//   - **不更新 updated_at**：过目不是对内容的修改；更新它会让刚标记完的产物立刻又变未过目。
//   - **四类产物同构**：知识卡 / 材料笔记 / 原文 / 综述走同一条路径，读 frontmatter 用
//     store.FrontmatterInto（不用 CardOf：后者连正文五分区一起校验，对笔记与原文必然报错）。
//
// # 阶段边界（本命令一律不做）
//
//   - 不实现「用户直接编辑 Markdown 后在对账时补齐」那条触发（A-16 已裁决属 S3/M4）；
//   - 不输出 `[未过目]` 文本标记（T-…-043）；
//   - 不做任何催促类文案、不做过期自动判定（S3）。
//
// # [M6 · T-…-072 批次 C2b] 事务化：与 undelete（C2a）同类的 A 类写命令
//
// 旧形态是「锁外扫库 + 直落实盘 + 事后 Git」。M6 原子性合同 §16.1 把本命令归入 A 类
// （产生权威写入的写命令），必须走完整时序，全程只持**同一把** `.index/run.lock`：
//
//	S0 锁外解析 --target 与授权佐证
//	S1 取 run.lock
//	S2 崩溃恢复屏障（临界区内第一件事，早于任何一次业务读）
//	S3 锁内重建 Store，重做 ScanIDs → Resolve → Read → frontmatter
//	S4 Store atomic overlay 预演：唯一写口 applyStateWrite(StateWriteReviewedAt)，
//	   导出恰含目标一个文件的 accepted write-set（实盘与 Git 全程零变化）
//	S5 AllocateTxnID → RecordTxnID → WriteIntent（发布屏障）
//	S6 txn.Commit：commit marker 落盘之前，reviewed_at 一律不算生效
//	S7 Git commit（仍在锁内、严格晚于 commit marker）
//	S8 唯一 syncIndexAfterWrite（Git 成败都走，仍在锁内、早于 Release）
//	S9 释放锁；报告渲染与退出码裁决全部回到锁外
//
// `txn_id` 的审计边界是「AllocateTxnID 成功」而非「提交成功」（A-59）：号码一旦到手，
// intent 失败 / S6 回滚 / Git 失败 / 全链路成功四种结局都必须把它交进 `data.report`。
//
// **Git 之后绝不再写一个字节权威内容**：commit marker 已经定盘，Git 失败只是「这批改动
// 没进版本历史」，退 4、保留目标态、不回滚、不二次写。
//
// # 为什么不与 undelete 抽一个共享的「单文件状态事务」helper
//
// 两条命令的临界区**只有骨架相同**，中间四处各不相同：undelete 有 W11 幂等提前返回、
// 写的是 Clear 形态、成功文案与「不改 status」的说明绑定；本命令要在写前读出「上一次
// 过目时刻」这一事实、写的是 StateWriteReviewedAt 形态、时刻取自 Root.Now。把这些差异
// 塞进回调，等于用四个 func 参数换回三十行样板，可读性与改动面都更差。真正该共享的
// 那一层（取锁 / 恢复 / 分配 / intent / 提交 / 释放）已经在 recover_hook.go 的
// txnSession 里，本文件与 undelete.go 都只是它的调用方。

import (
	"fmt"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/git"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/report"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// MarkReviewedNoContentChangeNotice 陈述「系统没做什么」：标记已过目不改内容也不改状态。
// 措辞固定，便于用例与 e2e 逐字断言。
const MarkReviewedNoContentChangeNotice = "本次标记已过目只写 reviewed_at 一个键：内容、status 与删除标记一律未变"

// markReviewedCommand 注册 `eg mark-reviewed`（矩阵 #7 / #22：P-A 🔴、P-U ✅，**无需二次确认**）。
func markReviewedCommand() *Command {
	return &Command{
		Name:    "mark-reviewed",
		Display: "mark-reviewed",
		Summary: "标记产物已过目（只写 reviewed_at 单键；不改内容与状态；commit verb=process）",
		Owner:   "T-evergreen.s1_main_flow-158614-042",
		Usage: `eg mark-reviewed --target <id> [--json]

参数：
  --target <id>   是；要标记的产物（四类同构：知识卡 / 材料笔记 / 原文 / 综述）

只写 frontmatter 的 reviewed_at 单键：status、deleted_at / deleted_reason 与 updated_at
一格不碰（三个维度彼此正交）。这是 reviewed_at 的唯一写入路径：被动查看、结果报告、
索引读取、Agent 写入一律不更新它。
不需要二次确认，也不产生退出码 6。
退出码：0 | 1 参数非法（零写入） | 2 校验失败（零写入） | 3 写入被跳过 | 4 Git 提交失败
`,
		Flags: func(fs *flagSet) {
			fs.String("target", "", "要标记已过目的产物 ID")
		},
		Validate: func(inv *Invocation) error {
			if err := noPositionalArgs(inv); err != nil {
				return err
			}
			if strings.TrimSpace(inv.String("target")) == "" {
				return &UsageError{Msg: "eg mark-reviewed 缺必填参数 --target <id>：标记已过目必须点名目标产物"}
			}
			return nil
		},
	}
}

// markReviewedOutcome 是**临界区内**产出的全部事实，供锁外渲染报告与裁决退出码。
//
// 之所以要专门带出来：报告体（含 S9 释放锁失败那条纯诊断）只有等临界区函数返回、
// 锁真正还回去之后才算齐；在锁内拼产物必然漏掉最后那一格。
type markReviewedOutcome struct {
	// Rel 是锁内解析定盘的目标文件。
	Rel string
	// At 是本次写进 reviewed_at 的时刻（锁内取自 Root.Now，用例因此确定性）。
	At model.Stamp
	// Before / HadBefore 是**写之前**读到的上一次过目时刻（缺省就说缺省，不编造）。
	Before    model.Stamp
	HadBefore bool
	// TxnID 在 AllocateTxnID 成功之后即非空（= `.index/txn/<txn_id>` 目录名）；
	// 分配之前失败的路径恒为空串（此时报告省略该键）。
	TxnID string
	// Commit 是 Git 回执；未跑 Git（提交前阻断 / Git 失败）时为零值。
	Commit git.CommitInfo
	// RolledBack 表示 S6 提交期出现普通 I/O 失败、txn 层已按合同 §5.2 主动放弃：
	// 目标一条未写、磁盘已回前像、abort 最后落盘、Git 未跑（退 3，**不是**退 1）。
	RolledBack bool
	// RollbackErr 是触发上述回滚的原始 I/O 失败（只进文案，不参与退出码分类）。
	RollbackErr error
	// Unwritten 是本次 accepted write-set 的路径清单（**不是**已写入清单）。
	Unwritten []string
	// Blocked 是「号码已分配、但事务在提交前被阻断」（intent 发布失败 / 提交未收敛为
	// 已回滚状态）。与 RolledBack 互斥：那一格是干净放弃，这一格需要人工处置。
	Blocked error
	// GitErr 是 S7 的 Git 失败：Markdown 已原子生效并保持目标态，不回滚、不二次写（退 4）。
	GitErr error
}

// runMarkReviewed 是 `eg mark-reviewed` 的唯一入口。
//
//	S0（锁外）解析 --target 与授权佐证 → S1~S9（临界区，见文件头时序）
//	→ 锁外渲染报告与裁决退出码
//
// 报告体在**进临界区之前**建好并全程传引用：S1 的 W28、S2 的 W26 都必须落进最终产物
// （那两条讲的是「进临界区时库里发生过什么」，与本次写了什么无关）。
func (r *Root) runMarkReviewed(inv *Invocation) (*Result, error) {
	target := strings.TrimSpace(inv.String("target"))

	// 命令行佐证置真：用户敲这条命令本身即进程边界上的显式发起（见文件头 N-1 与分级说明）。
	inv.UserRequest = true

	rep := report.New()
	oc, cerr := r.markReviewedCritical(inv, &rep, target)
	if oc == nil {
		// 连目标都没定盘（锁失败 / 恢复阻断 / 解析不到 / 读失败 / 预演写失败）：
		// 本次零权威写、无 commit，报告无从谈起。
		return nil, cerr
	}
	return r.markReviewedFinish(inv, &rep, oc, target)
}

// markReviewedCritical 是 `eg mark-reviewed` 的整个临界区：进函数即取锁，出函数即释放锁。
//
// 返回 (nil, err) 表示「连报告都产不出来」的阻断；返回 (oc, nil) 时失败事实挂在 oc 上，
// 由调用方在锁外连同报告一起交付。
func (r *Root) markReviewedCritical(inv *Invocation, rep *report.Report,
	target string) (*markReviewedOutcome, error) {
	root := inv.VaultRoot

	// —— S1 + S2：取锁、崩溃恢复屏障（与 plan 写链 / capture / undelete 共用同一把锁）。——
	sess, serr := r.enterTxnCritical(inv, reportWarnSink{rep}, txnCriticalOpts{
		ZeroWrite: "本次零写入", ReleaseNotice: "本次写入与提交不受影响",
	})
	if serr != nil {
		return nil, serr
	}
	defer sess.release()

	// —— S3：锁内重新建 Store、重新全库发现、重新读。——
	//
	// 严禁把这几步挪到锁外：S2 刚可能把目标回滚到前像（连 reviewed_at 在不在都会变），
	// 且在取锁之前别的写者可能刚改完同一个产物 —— 拿锁外快照当 B3 守卫基准、
	// 当「上一次过目时刻」的事实来源，等于把「写前复核」变成一句空话。
	st := store.New(root)
	idx, err := st.ScanIDs()
	if err != nil {
		return nil, &ValidationError{Msg: "扫描 vault 失败（零写入）：" + err.Error()}
	}
	rel, rerr := idx.Resolve(target)
	if rerr != nil {
		return nil, &ValidationError{
			Msg: fmt.Sprintf("eg mark-reviewed 的 --target %s 在库里解析不到文件（零写入）：%v",
				target, rerr),
			Diags: []Diagnostic{{
				Code: E18, Level: LevelError, Path: "--target", OpIndex: NonOpDiagnostic,
				Message: rerr.Error(), Target: target,
			}},
		}
	}
	f, ferr := st.Read(rel)
	if ferr != nil {
		return nil, &ValidationError{Msg: fmt.Sprintf("读取 %s 失败（零写入）：%v", rel, ferr)}
	}
	// 只读 frontmatter：CardOf 会连正文五分区一起校验，拿卡的结构去要求笔记 / 原文
	// 等于把「同构的过目维度」变成只对卡可用。
	var card model.Card
	if cerr := store.FrontmatterInto(f.Bytes, &card); cerr != nil {
		return nil, &ValidationError{
			Msg: fmt.Sprintf("解析 %s 的 frontmatter 失败（零写入）：%v", rel, cerr),
			Diags: []Diagnostic{{
				Code: E20, Level: LevelError, Path: rel, OpIndex: NonOpDiagnostic,
				Message: cerr.Error(), Target: target,
			}},
		}
	}
	fireTxnStep(TxnStepReread, "")

	before, hadBefore := model.ReviewedStamp(card.ReviewedAt)
	// 时刻同样在**锁内**取：它要进权威字节与 intent，必须与本次守卫写的基准同处一个临界区。
	oc := &markReviewedOutcome{Rel: rel, At: model.NewStamp(r.now()),
		Before: before, HadBefore: hadBefore}

	// —— S4：原子预演。复用**唯一**写口，实盘与 Git 全程零变化，只攒 accepted write-set。——
	if err := st.BeginAtomic(); err != nil {
		return nil, blockedError("原子预演无法开始，本次零写入", err)
	}
	defer st.EndAtomic()

	// 唯一写口：只 reviewed_at 一个键。status / updated_at / deleted_* 不在这条路径上，
	// 结构上不可能被改（StateWriteReviewedAt 形态只定位 reviewed_at 那一行）。
	if _, werr := r.applyStateWrite(st, store.StateWriteSpec{
		Op: store.StateWriteReviewedAt, Rel: rel, ExpectedHash: f.Hash, At: oc.At,
	}); werr != nil {
		// 预演期写失败 ⇒ overlay 丢弃即零写入：不开事务、不发 intent、不跑 Git。
		return nil, &PartialWriteError{
			Msg: fmt.Sprintf("写入 %s 的 %s 失败（磁盘保留现状，未做任何还原）：%v",
				rel, model.FMKeyReviewedAt, werr),
			Diags: []Diagnostic{{
				Code: E21, Level: LevelError, Path: rel, OpIndex: NonOpDiagnostic,
				Message: oneLineReason(werr.Error()), Target: target,
			}},
		}
	}

	ws := st.AtomicWriteSet()
	fireTxnStep(TxnStepExecuted, "")
	if len(ws) == 0 {
		// 结构上到不了这里（守卫写成功一定 stage 目标文件；overlay 按「发生过权威写」
		// 而非「字节是否变化」登记 write-set）。保留这一格只为把语义写死：
		// 零 accepted write-set ⇒ 不分配 txn_id、不发 intent、不提交、不跑 Git、不伪造 S8。
		return oc, nil
	}

	// —— S5：分配 txn_id → 记进锁正文 → 发布 intent（发布屏障）。——
	//
	// onAllocated 在号码到手的第一时间就写进报告：审计边界是「分配成功」而非「提交成功」
	// （A-59）。此后 intent 失败 / S6 回滚 / Git 失败 / 成功四种结局都不撤销这个键。
	txnID, oerr := sess.openTxn(inv, intentFilesOf(ws), nil, r.Now, func(id string) {
		oc.TxnID = id
		rep.SetTxnID(id)
	})
	if oerr != nil {
		if txnID == "" {
			// 号码根本没分配出来：盘上不存在对应事务目录，本次零写入、零 intent。
			return nil, oerr
		}
		// 号码已在盘 ⇒ 必须带着 txn_id 交付（用户要靠它定位那笔未闭合事务）。
		oc.Blocked = oerr
		return oc, nil
	}

	// —— S6：原子提交。commit marker 在盘之前，reviewed_at 一律不算生效。——
	//
	// 两种失败必须分开翻译（合同 §5.2 vs §16），与 runPlan / capture / undelete 逐字同源。
	cres, cerr := sess.commitWriteSet(txnID, ws)
	switch {
	case cerr != nil && cres != nil && cres.RolledBack:
		oc.RolledBack, oc.RollbackErr = true, cerr
		oc.Unwritten = writeSetPaths(ws)
		rep.AddWarning(unnumberedWarning("ops",
			"原子提交失败，事务 %s 已按合同 §5.2 主动放弃：%v", txnID, cerr))
		for _, p := range oc.Unwritten {
			rep.AddWarning(unnumberedWarning(p,
				"目标未写入：本次事务已整体回滚，该文件保持事务开始前的字节（前像）"))
		}
		rep.AddInfo("ops", report.NonOp,
			"事务 %s 的 %d 个目标文件**一个都没有写入**：txn 层已逐个还原前像、"+
				"全部还原完成后才写下 abort 标记，随后未执行 Git", txnID, len(ws))
		return oc, nil
	case cerr != nil:
		oc.Blocked = blockedError(fmt.Sprintf(
			"事务 %s 的原子提交失败且未能收敛为已回滚状态，需人工处置", txnID), cerr)
		return oc, nil
	}
	fireTxnStep(TxnStepCommitted, txnID)

	// —— S7：Git。严格晚于 commit marker，仍持同一把锁。——
	//
	// 走 r.repo(root) 而不是 git.New(root)：`Root.NewRepo` 是全仓统一的 Git 注入面，
	// 「Git 失败不回滚、不二次写」这类反证全靠它把提交打成失败。
	// verb 取 process：S1 冻结的提交动词里没有「标记已过目」这一个，与状态类命令同口径
	// （借既有已知动词躲开 W5 一律不成立；KnownVerbs 不因本命令增减）。
	repo := r.repo(root)
	// 既有改动同样会被 add -A 带进本次 commit：采样早于 Add，如实披露（I-…-023）。
	noteExistingChangesInReport(rep, repo, writeSetPaths(ws))
	info, gerr := repo.Commit(git.Message{
		Verb:           string(model.VerbProcess),
		Domain:         commitDomain(domainOfPath(rel)),
		Subject:        fmt.Sprintf("标记 %s 已过目（mark-reviewed）", target),
		Reason:         "用户明确标记已过目：M3 承接的唯一写入触发",
		RequirementIDs: []string{},
	})
	for _, w := range info.Warnings {
		rep.AddWarning(report.Diagnostic{
			Code: "", Level: report.LevelWarning, Path: "git.commit", OpIndex: report.NonOp,
			Message: w,
		})
	}
	// **无论成败都在尝试结束后触发**：这个节点的语义是「Git 已经跑过了」，
	// 「Git 之后没有第二次权威写」这条反证正是靠它在失败支上取证的。
	fireTxnStep(TxnStepGit, txnID)
	if gerr != nil {
		// **不回滚、不做第二次权威写**：reviewed_at 已由 commit marker 定盘并保持目标态。
		oc.GitErr = gerr
	} else {
		oc.Commit = info
	}

	// —— S8：写后索引同步。仍在**同一把锁内**、Git 之后、Release 之前（合同 §16.1 / §16.3）。——
	//
	// Git 成败都要走：索引里没有 reviewed_at 这一列，但文件指纹变了必须收敛；从 S7 提前
	// return 绕过 S8，等于把陈旧窗口留到锁外。索引问题只产 W22 / W24，不改退出码、
	// 不回滚、不二次写权威。
	r.syncIndexAfterWrite(rep, root, indexWriteLabel(inv), writeSetPaths(ws))
	fireTxnStep(TxnStepIndexSync, txnID)
	return oc, nil
}

// markReviewedFinish 在**锁外**把临界区的事实渲染成产物并裁决退出码。
//
// 四条出口：主动回滚退 3、提交前阻断按 ExitCodeFor 兜底、Git 失败退 4、其余退 0。
// 每一条都产报告 —— 号码已在盘的事务与 S2 的 W26 必须随产物交付，不能被一句错误文案吞掉。
func (r *Root) markReviewedFinish(inv *Invocation, rep *report.Report,
	oc *markReviewedOutcome, target string) (*Result, error) {
	rel := oc.Rel
	switch {
	case oc.RolledBack:
		// 一条都没写成：不能说「已执行」，也不能留 links[]（那是预演的账）。
		rep.SetCommit("")
		res := proposalResult(*rep, []string{fmt.Sprintf(
			"mark-reviewed 未生效：事务 %s 已整体回滚，%s 的 %s 保持事务开始前的字节"+
				"（目标 %s，%s）",
			oc.TxnID, rel, model.FMKeyReviewedAt, target,
			markReviewedPrevious(oc.Before, oc.HadBefore))})
		r.saveReport(inv.VaultRoot, res, ExitPartialWrite)
		return res, &PartialWriteError{
			Msg: fmt.Sprintf("原子提交失败，事务 %s 已整体回滚：%d 个目标文件一个都没有写入、"+
				"磁盘保持事务开始前的字节、未产生 commit（原因：%v）",
				oc.TxnID, len(oc.Unwritten), oc.RollbackErr),
		}
	case oc.Blocked != nil:
		// 号码已分配但事务未闭合：零权威写，事务目录留待人工核对。
		rep.SetCommit("")
		res := proposalResult(*rep, []string{fmt.Sprintf(
			"mark-reviewed 未提交：事务 %s 已分配号码但未闭合，本次零权威写入（目标 %s，%s）",
			oc.TxnID, target, rel)})
		r.saveReport(inv.VaultRoot, res, ExitCodeFor(classifyExit5(oc.Blocked)))
		return res, oc.Blocked
	}

	summary := []string{fmt.Sprintf(
		"mark-reviewed 已执行：%s 的 %s = %s（目标 %s，%s）",
		rel, model.FMKeyReviewedAt, oc.At.String(), target,
		markReviewedPrevious(oc.Before, oc.HadBefore))}
	if oc.GitErr != nil {
		// B4：提交失败退 4，已写的字节留在工作区并保持现状，不做任何还原。
		rep.SetCommit("")
		res := proposalResult(*rep, summary)
		r.saveReport(inv.VaultRoot, res, ExitCommitFailed)
		return res, &CommitFailedError{
			Msg: "Git 提交失败：已写入的 reviewed_at 保留在磁盘并保持现状，未做任何还原（B4）",
			Err: oc.GitErr,
			Diags: []Diagnostic{{
				Code: E22, Level: LevelError, Path: "git.commit", OpIndex: NonOpDiagnostic,
				Message: commitFailureMessage(oc.GitErr),
			}},
		}
	}
	rep.SetCommit(oc.Commit.SHA)
	rep.Links = append(rep.Links, rel)
	rep.AddInfo(rel, report.NonOp, "%s", MarkReviewedNoContentChangeNotice)
	res := proposalResult(*rep, summary)
	r.saveReport(inv.VaultRoot, res, ExitOK)
	return res, nil
}

// markReviewedPrevious 如实陈述「上一次过目时刻」这一事实：缺省就说缺省（不编造一个时刻）。
func markReviewedPrevious(before model.Stamp, had bool) string {
	if !had {
		return "此前无 " + model.FMKeyReviewedAt + "（从未过目）"
	}
	return "此前 " + model.FMKeyReviewedAt + " = " + before.String()
}
