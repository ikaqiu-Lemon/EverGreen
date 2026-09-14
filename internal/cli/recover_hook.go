package cli

// [S5] internal/cli/recover_hook.go —— plan-based 写链的**事务编排单点**
// （M6 · T-evergreen.s1_main_flow-158614-072 批次 B2a；合同 §2「事务边界与临界区 S0~S9」）。
//
// # 唯一职责（一句话）
//
// 把 `runPlan` 这**一个**共同写口的写入段，从「直落实盘 + 事后 Git」改成
// 「持锁 → 恢复 → 锁内重读重校验 → 原子预演 → 发布 intent → 原子提交 Markdown →
// 提交后 Git → 锁内索引同步 → 释放锁」的固定时序，且只此一处。
//
// 覆盖面恰是 runPlan 的七条命令：`apply` / `edit` / `deprecate` / `restore` /
// `replaced-by` / `rel add` / `rel remove` —— 它们本来就共用 runPlan，因此接一次就是七条。
// **不接** capture / delete / undelete / mark-reviewed / reconcile / proposal 直写
// （它们各有自己的写口，属后续批次），也不动 internal/index 与退出码 5。
//
// # 时序（逐字定死，任何一格前后颠倒都是 bug）
//
//	S0  参数解析 + plan 反序列化          —— **锁外**（在 runApply / rel add 等入口完成）
//	S1  txn.Acquire(vault/.index/run.lock) —— 临界区开始；等待期 W28 如实进报告。
//	    锁等待走**真实墙钟**（不传 r.Now），业务时钟只管 stamp / intent.started_at
//	S2  txn.Recover                        —— **临界区内第一件事**；真实回滚产 W26 并入报告
//	S3  store.New + plan.EnvFor + 注入 UserRequest + plan.Validate —— **全部在锁内重做**，
//	    严禁复用任何锁外的 env / pres / 读缓存（S2 可能刚把文件回滚到前像，锁外快照必然过期）
//	S4  plan.ExecuteAtomicFinal            —— 内存预演，实盘零变化；收尾钩子把提案 execution
//	    回写也落进**同一个** overlay，得最终 accepted write-set
//	S5  AllocateTxnID → lock.RecordTxnID → WriteIntent —— 发布屏障
//	S6  txn.Commit                          —— 多文件原子提交；commit marker 在盘才算数
//	S7  git add -A + commit                 —— **严格晚于 commit marker、仍持同一把锁**；
//	    失败退 4、Markdown 保持目标态、不回滚、**不做第二次权威写**
//	S8  syncIndexAfterWrite                 —— 仍在锁内、Git 之后、Release 之前
//	S9  Release                             —— 报告渲染与 last-report 落盘在**锁外**
//
// # 三条不可放宽的边界
//
//   - **零 accepted write-set 不开事务**：普通预演失败（Complete=false）一律零权威写、
//     零 intent、零 Git；只跳过 / 无写入同样不分配 txn_id、不发 intent、不造空 Git commit。
//   - **txn_id 的审计边界是「分配成功」而不是「提交成功」**：`AllocateTxnID` 一旦返回，
//     `.index/txn/<txn_id>` 就已在盘上占位，此后 intent 失败 / 主动 rollback+abort /
//     Git 失败 / 提交成功**四种结局**都必须把它写进最终报告（A-59）；只有分配之前失败、
//     校验失败、dry-run、零 accepted write-set 这四类才省略该键。相应地，分配之后的阻断
//     **不得** `return nil, error` —— 那会连同 S2 的 W26 一起把审计事实吞掉。
//   - **Git 之后绝不再写权威 Markdown**：Git 失败只进报告（一条 warning + 退 4 的诊断），
//     不重试、不回滚、不补一次提案回写 —— 那会是事务外的第二次权威写。

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/git"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/plan"
	"github.com/ikaqiu-Lemon/EverGreen/internal/report"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
	"github.com/ikaqiu-Lemon/EverGreen/internal/txn"
)

// —— 事务顺序钩子（仅供包内测试注入；生产恒 nil）——

// txnOrderHook 在编排的每个关键节点按 (step, txnID) 回调，供用例逐点反证时序
// （S2 先于 S3、intent 先于首个权威 rename、Git 晚于 commit marker、索引同步早于 Release…）。
// 生产恒 nil：这行是**观测**接缝，不是行为开关，钩子不参与任何裁决。
var txnOrderHook func(step, txnID string)

// 编排节点名（测试逐字断言，改名即改合同）。
const (
	TxnStepAcquired  = "acquired"   // S1 完成：已持锁
	TxnStepRecovered = "recovered"  // S2 完成：崩溃恢复屏障已过
	TxnStepReread    = "reread"     // S3 完成：锁内 EnvFor + Validate 已重做
	TxnStepExecuted  = "executed"   // S4 完成：预演结束，accepted write-set 已定盘
	TxnStepAllocated = "allocated"  // S5 中段：txn_id 已分配并记进锁正文
	TxnStepIntent    = "intent"     // S5 完成：intent 已发布（任何权威 rename 之前）
	TxnStepCommitted = "committed"  // S6 完成：commit marker 在盘，Markdown 已生效
	TxnStepGit       = "git"        // S7 完成：Git 提交已尝试（成功或失败）
	TxnStepIndexSync = "index_sync" // S8 完成：写后索引同步已尝试（仍持锁）
	TxnStepReleasing = "releasing"  // S9 之前：即将释放锁（此刻仍持锁）
)

func fireTxnStep(step, txnID string) {
	if txnOrderHook != nil {
		txnOrderHook(step, txnID)
	}
}

// TxnBlockedError 是事务链路的**写前阻断 / 基础设施失败**（锁不可用 E16、恢复 fail closed
// E15、intent 发布失败、原子提交失败…）。共同事实：权威 Markdown **零写入**、无 commit。
//
// 本类型刻意**不实现** ExitCode()：退出码 5 的启用与 E15 / E16 的映射属 T-…-074，
// 本批次不动那一格。因此它经 ExitCodeFor 的「未分类」兜底走 1，诊断照常如实渲染。
type TxnBlockedError struct {
	Msg   string
	Err   error
	Diags []Diagnostic
}

func (e *TxnBlockedError) Error() string {
	if e.Err == nil {
		return e.Msg
	}
	return e.Msg + "：" + e.Err.Error()
}

// Unwrap 暴露底层 txn 错误（供 errors.Is/As 穿透 ErrLockUnavailable 等 sentinel）。
func (e *TxnBlockedError) Unwrap() error { return e.Err }

// Diagnostics 实现 diagnoser。
func (e *TxnBlockedError) Diagnostics() []Diagnostic { return e.Diags }

// planTxnOutcome 是临界区内产出的全部事实，供**锁外**的报告渲染与退出码裁决消费。
type planTxnOutcome struct {
	// Pres 是**锁内**校验结果（唯一真源；锁外不得另算一份）。
	Pres *plan.Result
	// Exec 是执行账本；Aborted 时它只承载跳过 / 失败，不承载任何写入事实。
	Exec *plan.ExecResult
	// ValidationFailed 表示锁内校验失败：零写入、无 commit、未开事务（退 2）。
	ValidationFailed bool
	// Aborted 表示原子预演出现普通写失败：整事务放弃，零权威写、零 intent、零 Git。
	Aborted bool
	// RolledBack 表示 S6 提交期出现普通 I/O 失败、txn 层已按合同 §5.2 主动放弃：
	// 全部 accepted 目标一条未写、磁盘已回前像、abort 最后落盘、Git 未跑（退 3）。
	RolledBack bool
	// RollbackErr 是触发上述回滚的原始 I/O 失败（只进报告与错误文案，不参与退出码分类）。
	RollbackErr error
	// TargetCount 是本次 accepted write-set 的目标文件数（**不是**已写入数）。
	// 回滚路径要说清「这 N 个目标一个都没写成」，而这个 N 只能取自 write-set：
	// ex.Written 是 overlay 的预演账，拿它当实盘写入事实正是本批次要杜绝的那类误报。
	TargetCount int
	// TxnID 在 **AllocateTxnID 成功之后**即非空（= `.index/txn/<txn_id>` 目录名）。
	// 分配成功等于这个事务号已在盘上占位，此后无论提交、回滚还是阻断，它都必须可追溯。
	TxnID string
	// Blocked 是 **Allocate 之后**发生的写前阻断（intent 发布失败、原子提交未能收敛为
	// 已回滚状态）。这类失败依然「连报告都要产」：事务号已在盘、S2 的 W26 已经攒进报告，
	// 直接 return nil,error 会把这两样审计事实一起丢掉，正是 A-59 要防的漏报。
	Blocked error
	// CommitErr 是 Git 提交失败（退 4）；Markdown 已生效且保持目标态，不回滚。
	CommitErr error
	// PrecheckFailed 表示 S3 写前强校验（`--strict` 升级面命中）在**开事务之前**把写拦下：
	// 零权威写入、未分配 txn_id、未发 intent、无 commit、不发 W26。此时 Blocked 携 E15，
	// 由调用方落成退出码 5（合同 §9 / §17.1，M6 · T-…-074）。
	PrecheckFailed bool
}

// —— 低层事务 helper：锁 / 恢复 / 发布屏障 / 原子提交的顺序，全仓只此一份 ——
//
// 下面这一组（txnWarnSink / txnSession / enterTxnCritical / openTxn / commitWriteSet）
// 是**共享底座**：plan 写链（runPlanCritical）与直写命令（M6 · T-…-072 批次 C1 起，
// 首个接入的是 `eg capture`）都从这里取同一条时序。抽出来的唯一目的就是**禁止第二套顺序**——
// 「先恢复再读」「intent 先于任何权威 rename」「commit marker 先于 Git」这三条一旦被抄成
// 两份，两份就会各自漂移，而漂移出来的那份恰好是崩溃后无法判定的那份。
//
// 本组 helper 只负责**顺序与诊断搬运**，不做任何业务裁决：写什么、写不写、失败怎么翻译成
// 退出码，全由调用方决定。

// txnWarnSink 是事务编排的诊断出口。
//
// 两类调用方的产物形态不同 —— plan 写链有 `data.report` 容器（report.Report），
// `eg capture` 这类直写命令只有 `Result.Warnings` —— 但它们要交付的事务事实完全一样：
// W28（锁等待）、W26（崩溃恢复真实回滚）、锁正文写失败 / 释放失败这两类纯诊断。
// 把出口抽成接口，编排层就不必知道自己在往哪种产物里写，顺序也就不必抄第二遍。
type txnWarnSink interface {
	// addTxnDiag 原样搬运 internal/txn 的中性诊断（条目数守恒，不折叠、不去重）。
	addTxnDiag(d txn.Diag)
	// addPlainWarning 追加一条无码 warning（纯诊断：不产码、不改互斥、不改退出码）。
	addPlainWarning(path, format string, args ...interface{})
}

// reportWarnSink 把事务诊断写进 plan 写链的报告体。
type reportWarnSink struct{ rep *report.Report }

func (s reportWarnSink) addTxnDiag(d txn.Diag) { addTxnDiags(s.rep, []txn.Diag{d}) }

func (s reportWarnSink) addPlainWarning(path, format string, args ...interface{}) {
	s.rep.AddWarning(unnumberedWarning(path, format, args...))
}

// resultWarnSink 把事务诊断写进直写命令的 `Result.Warnings`（`eg capture` 走这条）。
//
// 这些命令的 `data.warnings[]` 是从 `Result.Warnings` 派生的，因此 W26 / W28 会**自动**
// 出现在它们既有的 warnings 面上：不新增 data 键、不给直写命令硬塞一个 report 容器。
type resultWarnSink struct{ res *Result }

func (s resultWarnSink) addTxnDiag(d txn.Diag) {
	s.res.Warnings = append(s.res.Warnings, Diagnostic{
		Code: d.Code, Level: d.Level, Path: d.Path,
		OpIndex: NonOpDiagnostic, Message: d.Message, Target: d.Target,
	})
}

func (s resultWarnSink) addPlainWarning(path, format string, args ...interface{}) {
	s.res.Warnings = append(s.res.Warnings, Diagnostic{
		Code: "", Level: LevelWarning, Path: path,
		OpIndex: NonOpDiagnostic, Message: fmt.Sprintf(format, args...),
	})
}

// txnCriticalOpts 是临界区的文案参数：不同命令对「本次什么都没写」的说法不同，
// 但顺序与判据完全一致，因此差异只留在措辞上，绝不留在控制流里。
type txnCriticalOpts struct {
	// ZeroWrite 进阻断错误消息（例如「本次零写入」）。
	ZeroWrite string
	// ReleaseNotice 进「释放锁失败」这条纯诊断的括注（例如「本次写入与提交不受影响」）。
	ReleaseNotice string
}

// txnSession 是一次**已进入临界区**的事务：锁已持有、崩溃恢复屏障已过。
//
// 零值不可用，只能由 enterTxnCritical 产出；持有者必须 `defer sess.release()`。
type txnSession struct {
	root string
	lock *txn.Lock
	sink txnWarnSink
	opts txnCriticalOpts
	// restored 是 S2 恢复**真的回滚过字节**的权威文件（vault 内相对路径，来自
	// txn.RecoverResult.Restored）。noop 恢复时为空。
	//
	// 只有它能界定「锁外采样因恢复而过期」的**确切范围**（I-…-022）：超出这份清单的
	// 前像变化不是恢复造成的（是并发写者或外部编辑），必须继续按 B3 判冲突。
	restored []string
	// released 记「锁已经还过了」。调用方**应当**在锁内做完最后一件事（S8）之后显式
	// release，好让报告渲染回到锁外；`defer sess.release()` 因此退化成兜底。两处都写
	// 是有意的：漏掉 defer 会在提前 return 的分支上把锁带出函数，而没有这一格幂等，
	// 显式 + 兜底就会 Release 两次（第二次必然失败）并多打一个 S9 节点，把
	// 「index_sync 早于 releasing」这类顺序反证搅成噪声。
	released bool
}

// enterTxnCritical 执行 S1（取锁）+ S2（崩溃恢复屏障），成功即表示「已进入临界区」。
//
// 返回 error 时锁已释放（或从未取得），调用方不必也不应再 release。
//
// **刻意不传 Now**（P0）：锁等待的 `elapsed` 必须走真实墙钟，不能用 r.Now。
// r.Now 是**业务时钟**，测试与可复现构建常把它钉成一个常量（stamp / intent.started_at
// 需要可复算的确定值）。而 Acquire 的超时判据是 `now().Sub(start) >= timeout` ——
// 喂进常量时钟，elapsed 恒为 0，`waited >= timeout` 永不成立，锁一旦被别的进程占住，
// 退避重试就会**无限循环**：不是退 E16，而是整条命令挂死。
// LockOptions.Now 为 nil 时 txn 侧回落到 time.Now，这正是我们要的真实时钟；
// Timeout 留零同样是有意的，交给现有的 EG_LOCK_TIMEOUT_MS / DefaultLockTimeout 决定。
//
// 代价只有一处且可接受：锁正文的 `acquired_at` 随之取真实时间。它是**纯诊断**字段
// （合同 §3：不参与互斥判定），而 stamp、intent.started_at 这些进入权威内容与事务日志、
// 需要确定性的时刻，依旧全部由 r.Now 供给（见 openTxn 的 now 形参）。
func (r *Root) enterTxnCritical(inv *Invocation, sink txnWarnSink,
	opts txnCriticalOpts) (*txnSession, error) {
	return r.enterTxnCriticalAt(inv, inv.VaultRoot, sink, opts)
}

// enterTxnCriticalAt 是**显式给出 vault 根**的进入口，语义与 enterTxnCritical 逐字相同。
//
// 为什么需要它（I-…-021）：`eg config set` 与 `eg init` 走 SkipVaultGuard —— 前者自己
// 向上找 `evergreen.yml`，后者可能正在**创建** vault，因此 `inv.VaultRoot` 恒为空。
// 这两条命令同样会改权威文件与 Git 历史（合同 §16.1 的 A 类），必须走同一条 S1/S2；
// 若为它们另抄一份取锁 + 恢复的代码，「A 类一律持锁 + 过恢复屏障」就会有第二份实现，
// 而 I-…-021 的根因恰恰是「这两条命令没有走这条底座」。因此接口只加一层形参，不复制逻辑。
func (r *Root) enterTxnCriticalAt(inv *Invocation, root string, sink txnWarnSink,
	opts txnCriticalOpts) (*txnSession, error) {
	// —— S1：取锁。等待期的 W28 与正文写失败都如实进产物，不改互斥、不改退出码。——
	lock, aerr := txn.Acquire(root, txn.LockOptions{Argv: txnArgv(inv)})
	if aerr != nil {
		return nil, blockedError("获取 "+txn.LockPath(root)+" 失败，"+opts.ZeroWrite, aerr)
	}
	sess := &txnSession{root: root, lock: lock, sink: sink, opts: opts}
	for _, d := range lock.Diagnostics() {
		sink.addTxnDiag(d)
	}
	if berr := lock.BodyWriteError(); berr != nil {
		sink.addPlainWarning(txn.LockPath(root),
			"run.lock 正文写入失败（纯诊断，不影响互斥与退出码）：%v", berr)
	}
	fireTxnStep(TxnStepAcquired, "")

	// —— S2：崩溃恢复屏障。**必须是临界区内第一件事**，且必须早于任何一次业务读。——
	rres, rerr := txn.Recover(root)
	if rerr != nil {
		// 恢复被阻断 ⇒ 本次零写入。锁在这里就还回去：调用方拿到 error 后不再持有会话。
		sess.release()
		return nil, blockedError("未闭合事务的崩溃恢复被阻断，"+opts.ZeroWrite, rerr)
	}
	for _, d := range rres.Diagnostics { // 真实回滚才有 W26，noop 时为空
		sink.addTxnDiag(d)
	}
	// 记下恢复真的回滚了哪些权威文件：调用方据此**只**重采这些路径的写前凭据（I-…-022）。
	sess.restored = rres.Restored
	fireTxnStep(TxnStepRecovered, rres.TxnID)
	return sess, nil
}

// release 是 S9：释放锁。失败只进诊断 —— 本次已提交的事实不因释放失败而改变。
//
// **幂等**：第二次及以后一律无动作（不再 Release、不再触发 S9 节点、不再产诊断）。
// 这让「锁内做完 S8 就显式还锁、锁外再渲染报告」成为可写的形态，而不必把 defer 去掉。
func (s *txnSession) release() {
	if s.released {
		return
	}
	s.released = true
	fireTxnStep(TxnStepReleasing, "")
	if rerr := s.lock.Release(); rerr != nil {
		s.sink.addPlainWarning(txn.LockPath(s.root),
			"释放 run.lock 失败（%s）：%v", s.opts.ReleaseNotice, rerr)
	}
}

// openTxn 是 S5：AllocateTxnID → RecordTxnID → WriteIntent（发布屏障）。
//
// **返回值的两段式是合同的一部分**（A-59）：`AllocateTxnID` 一旦成功，
// `.index/txn/<txn_id>` 就已在盘上占位，此后 intent 失败也**不撤销**这个号码 ——
// 因此本函数在 intent 失败时仍返回非空 txnID 与 error，调用方必须把号码写进产物，
// 而不是当作「什么都没发生」。号码分配之前失败则返回空串（那时确实什么都没发生）。
//
// onAllocated 在号码到手的**第一时间**回调（plan 写链在这里 rep.SetTxnID，capture 在
// 这里把它记进 outcome）：审计边界是「分配成功」，不是「提交成功」。
func (s *txnSession) openTxn(inv *Invocation, files []txn.FileSpec, skipped []txn.SkippedFile,
	now func() time.Time, onAllocated func(txnID string)) (string, error) {
	txnID, ierr := txn.AllocateTxnID(s.root)
	if ierr != nil {
		return "", blockedError("分配 txn_id 失败，"+s.opts.ZeroWrite, ierr)
	}
	if onAllocated != nil {
		onAllocated(txnID)
	}
	if err := s.lock.RecordTxnID(txnID); err != nil {
		// 纯诊断：不产码、不改退出码、不影响互斥（合同 §3）。
		s.sink.addPlainWarning(txn.LockPath(s.root),
			"把 txn_id=%s 补写进 run.lock 正文失败（纯诊断，不影响本次事务）：%v", txnID, err)
	}
	fireTxnStep(TxnStepAllocated, txnID)

	if _, err := txn.WriteIntent(s.root, txnID, txn.IntentInput{
		Argv:         txnArgv(inv),
		ExpectCommit: true,
		Files:        files,
		Skipped:      skipped,
		Now:          now,
	}); err != nil {
		return txnID, blockedError(fmt.Sprintf(
			"事务 %s 的 intent 发布失败，本次零写入", txnID), err)
	}
	fireTxnStep(TxnStepIntent, txnID)
	return txnID, nil
}

// commitWriteSet 是 S6：多文件原子提交。commit marker 在盘之前，Markdown 一律不算生效。
//
// 两种失败必须由调用方分开翻译（合同 §5.2 vs §16）：`res.RolledBack` 为真表示 txn 层
// 已把全部目标还原成前像并写下 abort（「一条都没写成」，退 3）；其余情形表示未能收敛，
// 需要人工处置（走 TxnBlockedError，且报告必须带上事务号）。
func (s *txnSession) commitWriteSet(txnID string, ws []store.AtomicFileSpec) (*txn.CommitResult, error) {
	return txn.Commit(s.root, txnID, txn.CommitInput{Files: commitFilesOf(ws)})
}

// runPlanCritical 是整个临界区：进函数即取锁，出函数即释放锁。
//
// 报告体由调用方持有并在**锁外**渲染；本函数只往里追加事实。
// 返回 error 表示「本次连报告都产不出来」的阻断（退 1 / 未分类）；其余情形一律
// 通过 planTxnOutcome 如实回报，由调用方翻译成 0 / 2 / 3 / 4。
func (r *Root) runPlanCritical(inv *Invocation, p *plan.ChangePlan,
	rep *report.Report) (*planTxnOutcome, error) {
	root := inv.VaultRoot

	// —— S1 + S2：取锁、崩溃恢复屏障（共享底座，见上面的 enterTxnCritical）。——
	sess, serr := r.enterTxnCritical(inv, reportWarnSink{rep}, txnCriticalOpts{
		ZeroWrite: "本次零写入", ReleaseNotice: "本次写入与提交不受影响",
	})
	if serr != nil {
		return nil, serr
	}
	defer sess.release()

	// —— S2′：被 S2 回滚过的那几个文件，其 CLI 自算写前凭据在此重采（I-…-022）。——
	//
	// 必须夹在 S2 与 S3 之间：S2 刚把某些权威文件回滚到前像，而 S3 的 plan.Validate 就是
	// B3 的判定点 —— 若还拿 S0（锁外）那份采样去比，崩溃恢复后**第一条**正常写命令会被
	// 自己的恢复动作判成 content_hash_mismatch，退 3、零写入。
	//
	// 范围双重收窄，B3 一寸不让：只动 CLI 自算的 base（`eg apply --plan` 的显式 base 不碰），
	// 且只动 sess.restored 点名的路径（并发写者 / 外部编辑造成的不一致继续判冲突）。
	if err := r.rebaseSelfComputedBase(inv, p, sess.restored, rep); err != nil {
		return nil, err
	}

	// —— S3：锁内重新建 Store、重新全库发现、重新读、重新校验。——
	//
	// 为什么不能复用锁外的 env / pres：S2 刚可能把若干权威文件回滚到前像，
	// 而且在我们拿到锁之前另一个写者可能刚提交完一整批改动。锁外算出来的
	// id 索引、base 现态、B3 的 ExpectedHash 全部可能已经过期 —— 拿过期快照
	// 去写盘，就是把「写前复核」变成一句空话。
	st := store.New(root)
	env, eerr := plan.EnvFor(st, inv.Config.DefaultDomain)
	if eerr != nil {
		return nil, eerr
	}
	// 授权佐证只能从**进程边界**注入（授权合同 N-1）。
	env.UserRequest = AuthorizationOf(inv).UserRequest
	pres := plan.Validate(p, env)
	fireTxnStep(TxnStepReread, "")

	// EG-DOM-03：既无 plan.domain 也无 default_domain → 退 1，CLI 绝不自选领域。
	if pres.DomainUnavailable {
		return nil, &UsageError{Msg: fmt.Sprintf(
			"无法确定落位领域：plan.domain 缺失且 %s 未配置 default_domain（CLI 绝不自选领域）",
			ConfigFileName)}
	}
	notePlanValidation(rep, pres)

	// —— S3 写前强校验（precheck，`--strict`）：命中升级面即在开事务前零写入中止。——
	//
	// 插入点恒在锁内、在 Validate 之后、在 openTxn（发布屏障）之前：`--strict` 下把
	// W1/W2/W3/W4/W6 视作 error（升级面唯一真源在 internal/reconcile/strict.go），命中即
	// 产 E15、退出码 5、零权威写入、未分配 txn_id、未发 intent、不发 W26、事务保持未开。
	// 默认（非 strict）路径零漂移：Precheck 恒不失败，与 M1~M5 行为逐字一致。
	if pc := plan.Precheck(pres, inv.Set(StrictFlag)); pc.Failed {
		notePrecheckUpgraded(rep, pc.Upgraded)
		rep.NoteZeroCards("cards")
		return &planTxnOutcome{Pres: pres, PrecheckFailed: true,
			Blocked: precheckStrictBlocked(pc.Upgraded)}, nil
	}

	// 校验失败：零写入、无 commit、**不开事务**（因此报告里没有 txn_id）。
	if pres.Failed() {
		rep.NoteZeroCards("cards")
		return &planTxnOutcome{Pres: pres, ValidationFailed: true}, nil
	}

	// —— S4：原子预演。实盘与 Git 全程零变化，只攒 accepted write-set。——
	stamp := model.NewStamp(r.now())
	// 提案 execution 的逐路径回写与本次写入**同生共死**：它必须落进同一个 overlay，
	// 因此走 ExecuteAtomicFinal 的收尾钩子，而不是预演之后再补一刀实盘写。
	// 钩子产出的报告事实先攒在 sub 里，等 fillReport / noteDeprecatedNewSupport 之后
	// 再合并 —— 这样 `links[]` / `proposals[]` / `warnings[]` 的次序与 M3~M5 逐字一致。
	sub := report.New()
	recorded := map[string]bool{}
	ar, xerr := plan.ExecuteAtomicFinal(st, pres, plan.ExecOptions{
		Stamp: stamp, Date: model.NewDate(r.now()), Base: p.Base, Index: &env.Index,
	}, func(s *store.Store, ex *plan.ExecResult) {
		recordProposalExecutions(proposalExecInput{
			Store: s, Index: env.Index, Plan: p, Exec: ex, Stamp: stamp,
		}, &sub, recorded)
	})
	if xerr != nil {
		return nil, blockedError("原子预演无法开始，本次零写入", xerr)
	}
	ex := ar.Exec
	fireTxnStep(TxnStepExecuted, "")

	// 普通写失败 ⇒ 整事务放弃：零权威写、零 intent、零 Git（要么全生效、要么全不生效）。
	if !ar.Complete {
		fillReportAborted(rep, ex)
		rep.SetCommit("")
		rep.NoteZeroCards("cards")
		return &planTxnOutcome{Pres: pres, Exec: ex, Aborted: true}, nil
	}

	// 预演成功，但**写入事实先不搬进报告**：在 S6 的 commit marker 落盘之前，`ex.Written`
	// 只是 overlay 里的目标态，实盘一个字节都还没动。剩下两条路径（零 accepted、提交回滚）
	// 要报的恰恰是「没写」——提前 fillReport 就等于把预演当既成事实。
	// 另一半理由同样硬：noteDeprecatedNewSupport 会**重新读盘**，只有在 commit 之后读到的
	// 才是本次写入后的真实内容（M1~M5 一直如此），提前读到的是前像。

	// 零 accepted write-set：不分配 txn_id、不发 intent、不提交、**不造空 Git commit**。
	if len(ar.WriteSet) == 0 {
		fillReport(rep, ex) // 此时 ex.Written 必为空：没有任何权威写被接受
		noteDeprecatedNewSupport(st, env.Index, ex, rep)
		mergeSubReport(rep, sub)
		rep.SetCommit("")
		rep.AddInfo("git.commit", report.NonOp,
			"本次没有任何被接受的权威写入（accepted write-set 为空）："+
				"未开事务、未发布 intent、未产生 commit（不制造空提交）")
		rep.NoteZeroCards("cards")
		r.syncIndexAfterWrite(rep, root, indexWriteLabel(inv), ex.Written)
		fireTxnStep(TxnStepIndexSync, "")
		return &planTxnOutcome{Pres: pres, Exec: ex}, nil
	}

	// —— S5：分配 txn_id → 记进锁正文 → 发布 intent（发布屏障）。——
	//
	// **审计边界就在号码到手那一刻**（A-59）：AllocateTxnID 成功 ⇒ `.index/txn/<txn_id>`
	// 已在盘上占位，这个号码从此对外可见、可被 Scan / Recover 读到。因此它必须立刻进最终
	// 报告（下面的 onAllocated 回调），且此后**任何**结局都不再撤销：
	//
	//   - intent 发布失败       → 报告带 txn_id（用户才知道去哪个目录看这笔未闭合事务）；
	//   - S6 主动 rollback+abort → 报告带 txn_id，且它恰是 abort 标记所在的目录名；
	//   - Git 失败              → 报告带 txn_id，Markdown 已生效；
	//   - 提交成功              → 报告带 txn_id。
	//
	// 反过来，**Allocate 之前**失败的路径（锁失败 / 恢复阻断 / 校验失败 / 预演放弃 /
	// 零 accepted write-set）连号码都没分配过，dry-run 更是不进临界区 —— 它们一律省略该键。
	// 早先那版把 SetTxnID 放在 commit marker 之后，把「已提交」当成了审计边界，
	// 于是分配之后、提交之前的三类结局全部漏报事务号，与 A-59 原文不符。
	txnID, oerr := sess.openTxn(inv, intentFilesOf(ar.WriteSet), intentSkippedOf(ex), r.Now,
		func(id string) { rep.SetTxnID(id) })
	if oerr != nil {
		if txnID == "" {
			// 号码根本没分配出来：盘上不存在对应事务目录，本次零写入、零 intent。
			return nil, oerr
		}
		// 号码已在盘 ⇒ 走**带报告**的阻断：零权威写的事实不变，但 txn_id 与 S2 的 W26
		// 必须随报告一起交付给用户（return nil,error 会把它们一并吞掉）。
		fillReportBlockedAfterAllocate(rep, ex, txnID,
			"intent 发布失败：事务已分配号码但未发布发布屏障，本次零权威写入")
		return &planTxnOutcome{Pres: pres, Exec: ex, TxnID: txnID, Blocked: oerr}, nil
	}

	// —— S6：多文件原子提交。commit marker 在盘之前，Markdown 一律不算生效。——
	//
	// 这里有**两种**失败，必须分开翻译（合同 §5.2 vs §16）：
	//
	//   ① `RolledBack=true`：提交期出现普通 I/O 失败（备料 / rename / 标记写失败），txn 层已经
	//      按两遍恢复把**全部**权威文件还原到前像、最后写下 abort。事务是「主动放弃」的，
	//      磁盘处在事务开始前的状态，Git 不该跑。它不是基础设施崩坏，因此**不走 E15/退 1**，
	//      而是如实报「这批目标一条都没写成」并退 3。
	//   ② 其余（result 为 nil，或 RolledBack=false 的 fail closed / 回滚自身也失败）：
	//      库可能停在需要人工处置的状态，走 TxnBlockedError 如实阻断 —— 但同样**带报告**，
	//      因为号码已分配，用户必须拿到它才能定位那笔待处置的事务目录。
	cres, cerr := sess.commitWriteSet(txnID, ar.WriteSet)
	switch {
	case cerr != nil && cres != nil && cres.RolledBack:
		// txn_id 已在 Allocate 那一刻进报告，此处**不撤销**：它恰是 abort 标记所在的
		// `.index/txn/<txn_id>` 目录名，是用户核对「这笔事务确实主动放弃了」的唯一入口。
		fillReportRolledBack(rep, ex, ar.WriteSet, txnID, cerr)
		return &planTxnOutcome{Pres: pres, Exec: ex, RolledBack: true, TxnID: txnID,
			RollbackErr: cerr, TargetCount: len(ar.WriteSet)}, nil
	case cerr != nil:
		fillReportBlockedAfterAllocate(rep, ex, txnID,
			"原子提交失败且未能收敛为已回滚状态：磁盘可能停在需要人工处置的状态，"+
				"请按事务目录内的标记文件核对后再写入")
		return &planTxnOutcome{Pres: pres, Exec: ex, TxnID: txnID,
			Blocked: blockedError(fmt.Sprintf(
				"事务 %s 的原子提交失败且未能收敛为已回滚状态，需人工处置", txnID), cerr)}, nil
	}
	fireTxnStep(TxnStepCommitted, txnID)

	// 写入事实此刻才进报告：盘上已是目标态，重新读盘也读得到本次内容。
	fillReport(rep, ex)
	// 失效卡出现新支持材料：只进报告，不改 status、不给恢复建议（U-06 / EG-KNW-06）。
	noteDeprecatedNewSupport(st, env.Index, ex, rep)
	mergeSubReport(rep, sub)

	// —— S7：Git。严格晚于 commit marker，仍持同一把锁。——
	written := writeSetPaths(ar.WriteSet)
	repo := r.repo(root)
	// 既有改动的披露走全 CLI 唯一口径（git_disclosure.go）：采样必须早于 Add（I-…-023）。
	noteExistingChangesInReport(rep, repo, written)
	info, cerr := repo.Commit(git.Message{
		Verb:           pres.Verb,
		Domain:         commitDomain(pres.Domain),
		Subject:        applySubject(ex),
		Reason:         p.Reason,
		RequirementIDs: p.RequirementIDs,
	})
	for _, w := range info.Warnings {
		rep.AddWarning(report.Diagnostic{
			Code: "", Level: report.LevelWarning, Path: "git.commit",
			OpIndex: report.NonOp, Message: w,
		})
	}
	rep.NoteZeroCards("cards")
	switch {
	case cerr != nil:
		rep.SetCommit("")
		rep.AddWarning(report.Diagnostic{
			Code: "", Level: report.LevelWarning, Path: "git.commit", OpIndex: report.NonOp,
			Message: commitFailureMessage(cerr),
		})
		// **不做第二次权威写**：Markdown 已由 commit marker 定盘并保持目标态，
		// 提案 execution 也已随同一份 write-set 落盘。Git 失败只是「这批已生效的
		// 改动没进版本历史」，它不构成再改一次 Markdown 的理由（那会漏出原子域）。
		rep.AddInfo("git.commit", report.NonOp,
			"事务 %s 的 Markdown 已原子生效并保持目标态：Git 失败不回滚、不重写任何权威文件"+
				"（未提交的改动可用 git status / git diff 自行核对）", txnID)
	case info.Created:
		rep.SetCommit(info.SHA)
	default:
		rep.SetCommit("")
	}
	fireTxnStep(TxnStepGit, txnID)

	// —— S8：写后索引同步。仍在锁内、Git 之后、Release 之前。——
	r.syncIndexAfterWrite(rep, root, indexWriteLabel(inv), ex.Written)
	fireTxnStep(TxnStepIndexSync, txnID)

	return &planTxnOutcome{Pres: pres, Exec: ex, TxnID: txnID, CommitErr: cerr}, nil
}

// —— 辅助：报告事实 ——

// notePlanValidation 把校验期的领域落位、诊断与覆盖项缺失如实登记进报告。
func notePlanValidation(rep *report.Report, pres *plan.Result) {
	if pres.DomainFallback && pres.Domain != "" {
		rep.DefaultDomainFallback = report.Fallback{Used: true, Reason: fmt.Sprintf(
			"plan.domain 缺失，已按 %s 的 default_domain=%q 落位", ConfigFileName, pres.Domain)}
	}
	for _, d := range pres.Warnings {
		rep.AddWarning(toReportDiag(d))
	}
	addCoverageGapInfos(rep, pres)
}

// fillReportAborted 是「原子预演失败 ⇒ 整事务放弃」时的报告填充。
//
// 与 fillReport 的差别只有一条、但至关重要：**一条写入事实都不搬运**。
// 预演里的 written / cards / relations 都只发生在内存 overlay 上，实盘一个字节没动；
// 把它们照搬进报告就是编造事实。这里只留跳过、警告、失败，外加一条说明原子语义的 info。
func fillReportAborted(rep *report.Report, ex *plan.ExecResult) {
	for _, s := range ex.Skipped {
		rep.Skipped = append(rep.Skipped, report.Skipped{
			Kind: string(s.Kind), Target: s.Target, Locator: s.Locator,
			Cause: s.Cause, Detail: s.Detail})
	}
	for _, d := range ex.Warnings {
		rep.AddWarning(toReportDiag(d))
	}
	for _, d := range ex.Failures {
		rep.AddWarning(toReportDiag(d))
	}
	rep.Links = []string{}
	rep.AddInfo("ops", report.NonOp,
		"原子事务已放弃：预演期出现 %d 处写入失败，本次**零权威写入**、未发布 intent、"+
			"未产生 commit（要么全部生效、要么全部不生效）；磁盘保持事务开始前的状态",
		len(ex.Failures))
}

// fillReportRolledBack 是「S6 提交期普通 I/O 失败 ⇒ 合同 §5.2 主动放弃」时的报告填充。
//
// 与 fillReportAborted 同一条原则（一条写入事实都不搬运），但多两件必须交代的事：
//
//   - **逐条**列出本次 accepted write-set 里的每一个目标路径并标明「未写入」。预演阶段
//     `ex.Written` 里它们是「已写」，那是 overlay 的账；实盘上 txn 层已经把它们全部还原
//     成前像了。不逐条说清楚，用户就会拿着一份写着 links[] 的报告去找根本不存在的改动。
//   - 说明磁盘状态与放弃序：全部回前像 → 最后写 abort → Git 未跑。回滚是**先回滚后 abort**，
//     这个次序本身就是「下次启动不会把半应用态当已闭合」的保证，值得写在报告里。
func fillReportRolledBack(rep *report.Report, ex *plan.ExecResult,
	ws []store.AtomicFileSpec, txnID string, cause error) {
	for _, s := range ex.Skipped {
		rep.Skipped = append(rep.Skipped, report.Skipped{
			Kind: string(s.Kind), Target: s.Target, Locator: s.Locator,
			Cause: s.Cause, Detail: s.Detail})
	}
	for _, d := range ex.Warnings {
		rep.AddWarning(toReportDiag(d))
	}
	// 一条都没写成：links[] 必须是空数组（而不是预演里的那份清单）。
	rep.Links = []string{}
	rep.SetCommit("")
	rep.NoteZeroCards("cards")

	rep.AddWarning(unnumberedWarning("ops",
		"原子提交失败，事务 %s 已按合同 §5.2 主动放弃：%v", txnID, cause))
	for _, spec := range ws {
		rep.AddWarning(unnumberedWarning(spec.Path,
			"目标未写入：本次事务已整体回滚，该文件保持事务开始前的字节（前像）"))
	}
	rep.AddInfo("ops", report.NonOp,
		"事务 %s 的 %d 个目标文件**一个都没有写入**：txn 层已逐个还原前像、"+
			"全部还原完成后才写下 abort 标记，随后未执行 Git（磁盘与版本历史都停在事务开始前）",
		txnID, len(ws))
}

// fillReportBlockedAfterAllocate 是「txn_id 已分配、但事务在提交前被阻断」时的报告填充。
//
// 覆盖两处：intent 发布失败、原子提交未能收敛为已回滚状态。共同事实与 fillReportAborted
// 一致 —— **一条写入事实都不搬运**（`ex.Written` 是 overlay 的账，实盘要么没动、要么处在
// 需要人工核对的状态，两种都不该渲染成 links[]）。
//
// 与 fillReportAborted 的差别只有一条：本路径的号码已经在盘上占位，因此报告里必须留下
// `.index/txn/<txn_id>` 这个可追溯入口（字段侧由 rep.SetTxnID 承担，人类可读侧由这条 info
// 承担）。什么都不写就等于让用户面对一个「库里多了个事务目录、报告只字未提」的黑箱。
func fillReportBlockedAfterAllocate(rep *report.Report, ex *plan.ExecResult,
	txnID, detail string) {
	for _, s := range ex.Skipped {
		rep.Skipped = append(rep.Skipped, report.Skipped{
			Kind: string(s.Kind), Target: s.Target, Locator: s.Locator,
			Cause: s.Cause, Detail: s.Detail})
	}
	for _, d := range ex.Warnings {
		rep.AddWarning(toReportDiag(d))
	}
	rep.Links = []string{}
	rep.SetCommit("")
	rep.NoteZeroCards("cards")
	rep.AddInfo("ops", report.NonOp,
		"事务 %s 已分配号码但未提交：%s（事务目录见 %s/%s/%s）",
		txnID, detail, txn.IndexDirName, txn.TxnDirName, txnID)
}

// mergeSubReport 把收尾钩子攒下的报告事实按原次序并进主报告。
func mergeSubReport(rep *report.Report, sub report.Report) {
	rep.Links = append(rep.Links, sub.Links...)
	rep.Proposals = append(rep.Proposals, sub.Proposals...)
	rep.Warnings = append(rep.Warnings, sub.Warnings...)
}

// addTxnDiags 把 internal/txn 的中性诊断搬进报告（条目数守恒，不折叠不去重）。
func addTxnDiags(rep *report.Report, list []txn.Diag) {
	for _, d := range list {
		rep.AddWarning(report.Diagnostic{
			Code: d.Code, Level: d.Level, Path: d.Path,
			OpIndex: report.NonOp, Message: d.Message, Target: d.Target,
		})
	}
}

// txnDiagnoser 是 internal/txn 阻断错误的最小契约（与该包内部的 `coded` 同形）：
// 携带一组中性诊断（E15 / E16 / W2x…）。CLI 只认这个接口，不认具体错误类型 ——
// txn 侧新增一种阻断错误时，这里一行都不用改。
type txnDiagnoser interface{ Diagnostics() []txn.Diag }

// blockedError 把 txn 的类型化错误包成 CLI 的写前阻断错误，并把它自带的诊断搬进载荷。
//
// **必须用 errors.As，不能用类型断言**：`err.(txnDiagnoser)` 只看最外层那一个值。
// 一旦底层错误被包装过 —— `fmt.Errorf("…：%w", e15)`、`errors.Join(a, b)`、或者未来
// 某一层顺手加了上下文 —— 断言当场落空，E15/E16 这类**必须如实上报**的诊断就会被
// 悄悄吞掉，只剩下兜底那条无码的 error。诊断码是 T-…-074 做退出码映射的唯一依据，
// 丢码等于把「fail closed」降级成「不明原因失败」。errors.As 会沿 Unwrap() error 与
// Unwrap() []error（errors.Join 树）两条链一路找下去，包装多少层都能取到。
func blockedError(msg string, err error) *TxnBlockedError {
	out := &TxnBlockedError{Msg: msg, Err: err}
	var d txnDiagnoser
	if errors.As(err, &d) {
		for _, one := range d.Diagnostics() {
			// Target 必须一起搬：`txn_id` 就住在这一格（CLI 合同 §5 给 `path` 的语义是文件路径）。
			// 漏掉它，「全部问题 txn_id 列表」在信封上就只剩人类可读文本，脚本无从逐条取用。
			out.Diags = append(out.Diags, Diagnostic{
				Code: one.Code, Level: one.Level, Path: one.Path,
				OpIndex: NonOpDiagnostic, Message: one.Message, Target: one.Target,
			})
		}
	}
	if len(out.Diags) == 0 {
		out.Diags = []Diagnostic{{
			Code: E21, Level: LevelError, Path: txn.IndexDirName + "/",
			OpIndex: NonOpDiagnostic, Message: out.Error(),
		}}
	}
	return out
}

// —— 辅助：write-set → 事务日志输入 ——

// txnArgv 是进锁正文与 intent 的命令标识（只做诊断 / 审计，不参与任何裁决）。
//
// 取值只来自**本次 Invocation**：命令名 + 子动词 + 去掉子动词后的位置参数。
// 刻意**不读 os.Args**：一来 CLI 全链路（含测试）都以 Invocation 为唯一输入面，读全局
// 进程参数会让「锁正文 / intent 里记的是什么」依赖调用方式而非本次解析结果；二来 os.Args
// 里可能混入 `--vault <绝对路径>` 之类的环境量，写进事务日志既无用又是噪声。
//
// 位置参数如实并入（rel add k-a supports k-b 这种，只看 `eg rel add` 是分不清对象的），
// flag 不并入（值可能很长且含路径 / 长理由，审计价值低于噪声成本）。
func txnArgv(inv *Invocation) []string {
	if inv == nil || inv.Cmd == nil {
		return []string{"eg"}
	}
	out := []string{"eg", inv.Cmd.Name}
	if inv.Sub != "" {
		out = append(out, inv.Sub)
	}
	return append(out, inv.Args...)
}

// 目标写形态（intent.files[].target_op，非空即可；只用于审计可读性）。
const (
	txnTargetOpCreate = "create"
	txnTargetOpUpdate = "update"
)

// intentFilesOf 把 accepted write-set 逐项映射成 intent 的 files[]（一一对应，不合并不丢弃）。
func intentFilesOf(ws []store.AtomicFileSpec) []txn.FileSpec {
	out := make([]txn.FileSpec, 0, len(ws))
	for _, s := range ws {
		op := txnTargetOpUpdate
		if s.IsNew {
			op = txnTargetOpCreate
		}
		out = append(out, txn.FileSpec{
			Path: s.Path, Create: s.IsNew,
			PreBytes: s.PreBytes, TargetBytes: s.TargetBytes, TargetOp: op,
		})
	}
	return out
}

// commitFilesOf 把 accepted write-set 逐项映射成原子提交的写集合（与 intent 逐项对应）。
func commitFilesOf(ws []store.AtomicFileSpec) []txn.CommitFile {
	out := make([]txn.CommitFile, 0, len(ws))
	for _, s := range ws {
		out = append(out, txn.CommitFile{Path: s.Path, TargetBytes: s.TargetBytes})
	}
	return out
}

// writeSetPaths 返回 accepted write-set 的路径清单（本次命令真正写过的全部权威文件）。
func writeSetPaths(ws []store.AtomicFileSpec) []string {
	out := make([]string, 0, len(ws))
	for _, s := range ws {
		out = append(out, s.Path)
	}
	return out
}

// intentSkippedOf 把执行账本里的跳过映射进 intent 的 skipped[]（审计面，不属原子域）。
//
// intent 的 `skipped[].kind` 是**封闭两值**（file_changed / user_block_unsafe，合同 §4），
// 且 path 必须是规范化的 vault 相对路径。名册外的形态（例如写失败留下的空 kind）**不进日志**：
// 硬塞进去会让下一次 Scan 把整个事务判成 corrupt，从而把「一条审计记录写歪」升级成
// 「整个库再也写不进去」。它们在报告 `skipped[]` / `warnings[]` 里一条不少，用户看得到。
func intentSkippedOf(ex *plan.ExecResult) []txn.SkippedFile {
	out := make([]txn.SkippedFile, 0, len(ex.Skipped))
	for _, s := range ex.Skipped {
		kind := string(s.Kind)
		if kind != txn.SkipKindFileChanged && kind != txn.SkipKindUserBlockUnsafe {
			continue
		}
		if strings.TrimSpace(s.Locator) == "" {
			continue
		}
		out = append(out, txn.SkippedFile{Path: s.Locator, Kind: kind})
	}
	return out
}
