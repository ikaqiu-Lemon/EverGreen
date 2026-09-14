package cli

// `eg undelete --target <id> --reason <text>` 的**命令侧编排**：清空逻辑删除标记
// （提案与状态合同 §5.2 ADR-11 的反向、授权合同 §2 矩阵 #6「清空 deleted_at /
// deleted_reason」P-A 🔴 / P-U ✅）；T-evergreen.s1_main_flow-158614-041 收尾。
//
// # 为什么与 eg delete 同为 CLI 直写例外（A-23 已裁决）
//
// 删除维度的落盘是「逐文件、非强原子」的形态（R-9：M3 不引入强原子写），
// 写口仍然唯一：状态落盘只经 store.ApplyStateWrite（ClearDeleted 的调用点全部留在
// internal/store/ 内），本文件既不呼 setter、也不复制一份 frontmatter 改写逻辑。
// 注入点复用 delete 那一个 Root.StateWrite（生产恒走 store.ApplyStateWrite），不另造第二个。
//
// # 三条不可放宽的不变式
//
//   - **status 原封不动**：删除维度与状态维度正交（冻结合同 F3）。本命令只删
//     deleted_at + deleted_reason 两行，根本不定位 status 键——结构上不可能改状态，
//     被恢复的卡是 active 还是 deprecated 保持原样。
//   - **不二次确认**：确认门只服务「不可逆 / 高风险」的两条命令（approve 与 delete，
//     见 exitcode.go 的白名单）。清空删除标记是把对象**恢复**可见，不销毁任何东西，
//     因此本命令不在退出码 6 的白名单里，也不注册任何二次确认参数
//     （本文件因此刻意不出现那个参数名的字面量：源码守卫按「确认流程只属 approve / delete」
//     逐字比对文件清单，undelete 一次都不许出现在里面）。
//   - **目标未被删除 → W11 幂等 no-op**：零写入、零 commit、进报告 warning。
//     判定复用 plan 层 T-…-039 的同一个函数 plan.UndeleteNoOp（不另写一套口径）。
//
// # 为什么可以在进程边界置 UserRequest（授权合同 N-1 反伪造条款）
//
// N-1 约束的是「Agent 在**文件内容**里写 initiator: user 自证」；而用户在终端敲
// `eg undelete` 这一动作本身就发生在进程边界上，即命令行佐证本身，与 deprecate /
// restore / replaced-by 三条状态命令同级（矩阵 #6 与 #3 同为 P-A 🔴 / P-U ✅，
// 且授权合同 §3 未把它列进需确认清单）。delete / approve 那种要求用户再手敲
// --user-request 的高风险闸门在这里不适用：它们会让对象消失或让提案生效，本命令相反。
//
// # 阶段边界（本命令一律不做）
//
//   - 不产出 `[已删除]` / `[未过目]` 标记与检索过滤（T-…-043）；
//   - 不判依据是否充分、不做 reviews 的过期自动判定（S3）；
//   - 不做任何文件系统层面的产物抹除，关系记录一条不删（U-01）。

// 事务化（M6 · T-…-072 批次 C2a；合同 §2「事务边界与临界区 S0~S9」、§16.1 A 类）：
//
//	S0  --target / --reason 解析 + 授权佐证       —— **锁外**（不触碰库内任何权威文件）
//	S1  txn.Acquire(vault/.index/run.lock)        —— 临界区开始；W28 进 data.report.warnings
//	S2  txn.Recover                                —— 临界区内第一件事；真实回滚产 W26
//	S3  ScanIDs / Resolve / Read / frontmatter /   —— **全部锁内重做**：S2 可能刚把目标回滚到
//	    W11 幂等判定                                   前像，在我们取到锁之前也可能有别的写者刚
//	                                                   改完同一张卡；锁外算出的 id 索引、B3 基准
//	                                                   与「删没删过」的结论全都可能过期
//	S4  BeginAtomic + 唯一写口 applyStateWrite     —— 内存预演，实盘零变化，得 accepted write-set
//	                                                   （恰一个文件：本命令只动目标一张卡）
//	S5  AllocateTxnID → RecordTxnID → WriteIntent（仅当 write-set 非空）
//	S6  txn.Commit                                 —— commit marker 在盘，两键的整行删除才算生效
//	S7  git commit                                 —— 严格晚于 commit marker、仍持同一把锁；
//	    失败退 4、Markdown 保持目标态、**不回滚、不做第二次权威写**
//	S8  syncIndexAfterWrite                        —— 仍在同一把锁内、S7 之后、Release 之前；
//	    Git 成败都要走，索引问题只产 W22 / W24，不改退出码
//	S9  Release                                    —— 报告渲染与 last-report 落盘在**锁外**
//
// 三条边界与 plan 写链 / `eg capture` 逐字同源，且**不复制第二套 S1~S8 编排** ——
// 取锁、恢复、发布屏障、原子提交全部经 recover_hook.go 的共享底座
// （enterTxnCritical / txnSession.openTxn / txnSession.commitWriteSet）：
//   - **W11 幂等 no-op 仍是零 write-set**：它照样先过 S1 / S2（恢复屏障之后重读才作数），
//     但**不分配 txn_id、不发 intent、不跑 Git、不伪造 S8**，退出码仍是 0；
//   - **txn_id 的审计边界是「分配成功」**（A-59）：号码一旦到手就进 `data.report.txn_id`，
//     此后 intent 失败 / S6 主动回滚 / Git 失败 / 成功四种结局都不撤销；
//   - **Git 之后绝不再写权威 Markdown**：退 4 只进报告，不回滚、不重试、不补写。

import (
	"fmt"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/git"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/plan"
	"github.com/ikaqiu-Lemon/EverGreen/internal/report"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// 固定文案（措辞固定便于用例与 e2e 逐字断言）。
const (
	// UndeleteNoStatusChangeNotice 陈述「系统没做什么」：恢复删除不改任何卡的 status。
	UndeleteNoStatusChangeNotice = "本次清空删除标记没有自动改变任何知识卡的状态"
	// UndeleteIdempotentMsg 是 W11 幂等 no-op 的说明（退 0、零写入、不产生空 commit）。
	UndeleteIdempotentMsg = "目标未被逻辑删除：幂等 no-op，本次零写入、零 commit"
)

// undeleteCommand 注册 `eg undelete`（矩阵 #6：P-A 🔴、P-U ✅，**无需二次确认**）。
//
// 不注册二次确认参数、也不产生退出码 6：确认门的白名单恰含 approve 与 delete 两条，
// 在这里加一个确认参数等于把白名单悄悄扩大（见 exitcode.go 的 needConfirmCommands）。
func undeleteCommand() *Command {
	return &Command{
		Name:    "undelete",
		Display: "undelete",
		Summary: "清空逻辑删除标记（删 deleted_at / deleted_reason 两键；status 原封不动；commit verb=process）",
		Owner:   "T-evergreen.s1_main_flow-158614-041",
		Usage: `eg undelete --target <id> --reason <text> [--json]

参数：
  --target <id>     是；要恢复的对象（四类产物同构：知识卡 / 材料笔记 / 原文 / 综述）
  --reason <text>   是；恢复理由（缺它退 1、零写入、零 commit）

整行删掉 deleted_at 与 deleted_reason（不留墓碑、不写空值）：status 绝不被自动改变，
关系与 sources[] 的记录一条不动。不需要二次确认，也不产生退出码 6。
目标未被逻辑删除 → W11 幂等 no-op：零写入、不产生空 commit（退 0）。
退出码：0 | 1 参数非法（零写入） | 2 校验失败（零写入） | 3 写入被跳过 | 4 Git 提交失败 |
        5 写前强校验失败（E15）/ run.lock 不可用（E16），两者均零写入
`,
		Flags: func(fs *flagSet) {
			fs.String("target", "", "要恢复的对象 ID")
			fs.String("reason", "", "恢复理由")
		},
		Validate: func(inv *Invocation) error {
			// 与三条状态命令同一份形态校验：缺 --target / --reason 一律退 1、零写入。
			return requireStateTargetAndReason(inv, "undelete")
		},
	}
}

// undeleteOutcome 是**临界区内**产出的全部事实，供锁外渲染报告与裁决退出码。
//
// 之所以要专门带出来：报告体（含 S9 释放锁失败那条纯诊断）只有等临界区函数返回、
// 锁真正还回去之后才算齐；在锁内拼产物必然漏掉最后那一格。
type undeleteOutcome struct {
	// Rel 是锁内解析定盘的目标文件（W11 分支同样有值）。
	Rel string
	// NoOp 为真表示 W11 幂等：目标未被逻辑删除，本次零写入、零事务、零 Git（退 0）。
	NoOp bool
	// TxnID 在 AllocateTxnID 成功之后即非空（= `.index/txn/<txn_id>` 目录名）；
	// W11 与分配之前失败的路径恒为空串（此时报告省略该键）。
	TxnID string
	// Commit 是 Git 回执；未跑 Git（W11 / 提交前阻断 / Git 失败）时为零值。
	Commit git.CommitInfo
	// RolledBack 表示 S6 提交期出现普通 I/O 失败、txn 层已按合同 §5.2 主动放弃：
	// 目标一条未写、磁盘已回前像、abort 最后落盘、Git 未跑（退 3，**不是**退 1）。
	RolledBack bool
	// RollbackErr 是触发上述回滚的原始 I/O 失败（只进文案，不参与退出码分类）。
	RollbackErr error
	// Unwritten 是本次 accepted write-set 的路径清单（**不是**已写入清单）：
	// 回滚路径要逐条交代「这些目标一个都没写成」。
	Unwritten []string
	// Blocked 是「号码已分配、但事务在提交前被阻断」（intent 发布失败 / 提交未收敛为
	// 已回滚状态）。与 RolledBack 互斥：那一格是干净放弃，这一格需要人工处置。
	Blocked error
	// GitErr 是 S7 的 Git 失败：Markdown 已原子生效并保持目标态，不回滚、不二次写（退 4）。
	GitErr error
}

// runUndelete 是 `eg undelete` 的唯一入口。
//
//	S0（锁外）解析 --target / --reason 与授权佐证 → S1~S9（临界区，见文件头时序）
//	→ 锁外渲染报告与裁决退出码
//
// 报告体在**进临界区之前**建好并全程传引用：S1 的 W28、S2 的 W26 都必须落进最终产物，
// 连 W11 幂等这条零写入分支也不例外（那两条讲的是「进临界区时库里发生过什么」，
// 与本次写不写一个字节无关）。
func (r *Root) runUndelete(inv *Invocation) (*Result, error) {
	target := strings.TrimSpace(inv.String("target"))
	reason := strings.TrimSpace(inv.String("reason"))

	// 命令行佐证置真：用户敲这条命令本身即进程边界上的显式发起（见文件头 N-1 说明）。
	// 与 delete 的区别在于本命令不销毁任何东西，故不要求再手敲 --user-request。
	inv.UserRequest = true

	rep := report.New()
	oc, cerr := r.undeleteCritical(inv, &rep, target, reason)
	if oc == nil {
		// 连目标都没定盘（锁失败 / 恢复阻断 / 解析不到 / 读失败 / 预演写失败）：
		// 本次零权威写、无 commit，报告无从谈起。
		return nil, cerr
	}
	if oc.NoOp {
		return r.undeleteNoOp(inv, &rep, oc.Rel, target)
	}
	return r.undeleteFinish(inv, &rep, oc, target)
}

// undeleteCritical 是 `eg undelete` 的整个临界区：进函数即取锁，出函数即释放锁。
//
// 返回 (nil, err) 表示「连报告都产不出来」的阻断；返回 (oc, nil) 时失败事实挂在 oc 上，
// 由调用方在锁外连同报告一起交付。
//
// 顺序与判据全部取自 recover_hook.go 的共享底座，本函数**只**负责 undelete 的业务面：
// 解析目标、W11 判定、一次 Clear 形态的守卫写、一条 verb=process 的 commit。
func (r *Root) undeleteCritical(inv *Invocation, rep *report.Report,
	target, reason string) (*undeleteOutcome, error) {
	root := inv.VaultRoot

	// —— S1 + S2：取锁、崩溃恢复屏障（与 plan 写链 / capture 共用同一底座与同一把锁）。——
	sess, serr := r.enterTxnCritical(inv, reportWarnSink{rep}, txnCriticalOpts{
		ZeroWrite: "本次零写入", ReleaseNotice: "本次写入与提交不受影响",
	})
	if serr != nil {
		return nil, serr
	}
	defer sess.release()

	// —— S3：锁内重新建 Store、重新全库发现、重新读、重新判 W11。——
	//
	// 严禁把这几步挪到锁外：S2 刚可能把目标回滚到前像（连 deleted_at 在不在都会变），
	// 且在取锁之前别的写者可能刚改完同一张卡 —— 拿锁外快照去判「删没删过」、去当 B3 基准，
	// 等于把「写前复核」变成一句空话。
	st := store.New(root)
	idx, err := st.ScanIDs()
	if err != nil {
		return nil, &ValidationError{Msg: "扫描 vault 失败（零写入）：" + err.Error()}
	}
	rel, rerr := idx.Resolve(target)
	if rerr != nil {
		return nil, &ValidationError{
			Msg: fmt.Sprintf("eg undelete 的 --target %s 在库里解析不到文件（零写入）：%v",
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
	// 只读**frontmatter**（FrontmatterInto），不走 CardOf：四类产物同构，
	// 而 CardOf 会连正文五分区一起校验——材料笔记 / 原文的分区名与卡不同，
	// 拿卡的结构去要求它们等于把「同构的删除维度」变成只对卡可用。
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

	// W11：判定口径只有 plan.UndeleteNoOp 一处（与 plan 层 undelete op 逐字同源）。
	// 它读的是**恢复屏障之后**的 frontmatter —— 这正是把它放进锁内的理由。
	if plan.UndeleteNoOp(card) {
		return &undeleteOutcome{Rel: rel, NoOp: true}, nil
	}

	// —— S4：原子预演。复用**唯一**写口，实盘与 Git 全程零变化，只攒 accepted write-set。——
	if err := st.BeginAtomic(); err != nil {
		return nil, blockedError("原子预演无法开始，本次零写入", err)
	}
	defer st.EndAtomic()

	// 唯一写口：Clear 形态 = ClearDeleted。status 不在这条路径上，结构上不可能被改。
	// Stamp：清空删除标记是一次实际写入 → 刷新内容时间戳（矩阵第 8 行 / I-…-009）。
	// 时刻取 r.now()（与本命令 commit 同一口径），本文件不另造第二个时钟。
	if _, werr := r.applyStateWrite(st, store.StateWriteSpec{
		Op: store.StateWriteDeleted, Rel: rel, ExpectedHash: f.Hash, Clear: true,
		Stamp: model.NewStamp(r.now()),
	}); werr != nil {
		// 预演期写失败 ⇒ overlay 丢弃即零写入：不开事务、不发 intent、不跑 Git。
		return nil, &PartialWriteError{
			Msg: fmt.Sprintf("清空 %s 的删除标记失败（磁盘保留现状，未做任何还原）：%v", rel, werr),
			Diags: []Diagnostic{{
				Code: E21, Level: LevelError, Path: rel, OpIndex: NonOpDiagnostic,
				Message: oneLineReason(werr.Error()), Target: target,
			}},
		}
	}

	oc := &undeleteOutcome{Rel: rel}
	ws := st.AtomicWriteSet()
	fireTxnStep(TxnStepExecuted, "")
	if len(ws) == 0 {
		// 结构上到不了这里（Clear 形态一定 stage 目标文件），但零 write-set 的语义只有一条：
		// 不分配 txn_id、不发 intent、不提交、不跑 Git、不伪造 S8。
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

	// —— S6：原子提交。commit marker 在盘之前，两键的整行删除一律不算生效。——
	//
	// 两种失败必须分开翻译（合同 §5.2 vs §16），与 runPlan / capture 逐字同源：
	//   ① RolledBack=true：提交期普通 I/O 失败，txn 层已把目标还原成前像、最后写下 abort。
	//      磁盘停在事务开始前，Git 不该跑。这是「主动放弃」，退 3。
	//   ② 其余：未能收敛为已回滚状态，库可能需要人工处置 ⇒ 阻断（带 txn_id 交付）。
	cres, cerr := sess.commitWriteSet(txnID, ws)
	switch {
	case cerr != nil && cres != nil && cres.RolledBack:
		oc.RolledBack, oc.RollbackErr = true, cerr
		oc.Unwritten = writeSetPaths(ws)
		rep.AddWarning(unnumberedWarning("ops",
			"原子提交失败，事务 %s 已按合同 §5.2 主动放弃：%v", txnID, cerr))
		// 逐路径交代「这一条没写成」：只留一句总述，用户无从知道到底哪个目标受影响。
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
	// verb 取 process：S1 冻结的提交动词里没有「恢复删除」这一个，与三条状态命令同口径
	// （借既有已知动词躲开 W5 一律不成立；KnownVerbs 不因本命令增减）。
	repo := r.repo(root)
	// 既有改动同样会被 add -A 带进本次 commit：采样早于 Add，如实披露（I-…-023）。
	noteExistingChangesInReport(rep, repo, writeSetPaths(ws))
	info, gerr := repo.Commit(git.Message{
		Verb:           string(model.VerbProcess),
		Domain:         commitDomain(domainOfPath(rel)),
		Subject:        fmt.Sprintf("清空 %s 的删除标记（undelete）", target),
		Reason:         reason,
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
		// **不回滚、不做第二次权威写**：两键的整行删除已由 commit marker 定盘并保持目标态。
		oc.GitErr = gerr
	} else {
		oc.Commit = info
	}

	// —— S8：写后索引同步。仍在**同一把锁内**、Git 之后、Release 之前（合同 §16.1 / §16.3）。——
	//
	// Git 成败都要走：成功 ⇒ HEAD 前进了，水位线不跟上索引立刻陈旧；失败 ⇒ Markdown 已由
	// commit marker 原子生效，索引的对象面同样已经变了。从 S7 提前 return 绕过 S8，
	// 等于把陈旧窗口留到锁外。索引问题只产 W22 / W24，不改退出码、不回滚、不二次写权威。
	r.syncIndexAfterWrite(rep, root, indexWriteLabel(inv), writeSetPaths(ws))
	fireTxnStep(TxnStepIndexSync, txnID)
	return oc, nil
}

// undeleteNoOp 是 W11 幂等分支：零写入、零 commit、进报告 warning，退 0。
//
// 为什么不报错：幂等 no-op 陈述的是「目标已经处于期望状态」，这不是失败；
// 也**不**产生空 commit——没有字节变化的提交会污染历史（与 deprecate / restore 同口径）。
//
// 报告体由调用方传入而不是在这里新建：S1 / S2 攒下的 W28 / W26 是「本次进临界区时库里
// 发生过什么」的事实，与本次写不写字节无关，重开一个空报告等于把它们丢掉。
func (r *Root) undeleteNoOp(inv *Invocation, rep *report.Report, rel, target string) (*Result, error) {
	rep.AddWarning(report.Diagnostic{
		Code: plan.W11, Level: report.LevelWarning, Path: rel, OpIndex: report.NonOp,
		Message: fmt.Sprintf("目标 %s 未被逻辑删除（无 deleted_at）：%s", target, UndeleteIdempotentMsg),
		Target:  target,
	})
	rep.SetCommit("")
	summary := []string{fmt.Sprintf("undelete：%s（目标 %s，%s；%s）",
		UndeleteIdempotentMsg, target, rel, UndeleteNoStatusChangeNotice)}
	res := proposalResult(*rep, summary)
	r.saveReport(inv.VaultRoot, res, ExitOK)
	return res, nil
}

// undeleteFinish 在**锁外**把临界区的事实渲染成产物并裁决退出码。
//
// 四条出口：主动回滚退 3、提交前阻断按 ExitCodeFor 兜底、Git 失败退 4、其余退 0。
// 每一条都产报告 —— 号码已在盘的事务与 S2 的 W26 必须随产物交付，不能被一句错误文案吞掉。
func (r *Root) undeleteFinish(inv *Invocation, rep *report.Report,
	oc *undeleteOutcome, target string) (*Result, error) {
	rel := oc.Rel
	switch {
	case oc.RolledBack:
		// 一条都没写成：不能说「已执行」，也不能留 links[]（那是预演的账）。
		rep.SetCommit("")
		res := proposalResult(*rep, []string{fmt.Sprintf(
			"undelete 未生效：事务 %s 已整体回滚，%s 的 deleted_at / deleted_reason "+
				"保持事务开始前的字节（目标 %s）；%s",
			oc.TxnID, rel, target, UndeleteNoStatusChangeNotice)})
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
			"undelete 未提交：事务 %s 已分配号码但未闭合，本次零权威写入（目标 %s，%s）",
			oc.TxnID, target, rel)})
		r.saveReport(inv.VaultRoot, res, ExitCodeFor(classifyExit5(oc.Blocked)))
		return res, oc.Blocked
	}

	summary := []string{fmt.Sprintf(
		"undelete 已执行：%s 的 deleted_at / deleted_reason 两键整行删除（目标 %s）；"+
			"%s；关系与 sources[] 的记录一条未删", rel, target, UndeleteNoStatusChangeNotice)}
	if oc.GitErr != nil {
		// B4：提交失败退 4，已清空的字节留在工作区并保持现状，不做任何还原。
		rep.SetCommit("")
		res := proposalResult(*rep, summary)
		r.saveReport(inv.VaultRoot, res, ExitCommitFailed)
		return res, &CommitFailedError{
			Msg: "Git 提交失败：已清空的删除标记保留在磁盘并保持现状，未做任何还原（B4）",
			Err: oc.GitErr,
			Diags: []Diagnostic{{
				Code: E22, Level: LevelError, Path: "git.commit", OpIndex: NonOpDiagnostic,
				Message: commitFailureMessage(oc.GitErr),
			}},
		}
	}
	rep.SetCommit(oc.Commit.SHA)
	rep.Links = append(rep.Links, rel)
	rep.AddInfo(rel, report.NonOp, "%s", UndeleteNoStatusChangeNotice)
	res := proposalResult(*rep, summary)
	r.saveReport(inv.VaultRoot, res, ExitOK)
	return res, nil
}
