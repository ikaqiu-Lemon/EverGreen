package cli

// [S4] internal/cli/index_lock.go —— `eg index build / rebuild / sync` 的**临界区编排单点**
// （M6 · T-evergreen.s1_main_flow-158614-072 批次 B2c1；合同 §2「事务边界与临界区」+ §16.5）。
//
// # 唯一职责（一句话）
//
// 把三条**索引维护**命令的「体检 + 快照 + 索引写」整段放进与 plan 写链**同一把**
// `vault/.index/run.lock` 里，并在持锁后依次过两道屏障（崩溃恢复 → 运行时保留条目体检），
// 全部通过之后才允许动 `.index/`。只此一处，三条命令共用。
//
// # 三类命令的分层（本文件只服务 B 类）
//
//	A 类  plan 写链（apply / edit / deprecate / …）：完整事务 S0~S9，见 recover_hook.go
//	B 类  索引维护（build / rebuild / sync）：**取锁 + 恢复 + 保留体检 + 维护**，本文件
//	C 类  只读（index status / bench / search / card / rel）：不取锁、不恢复、不建 .index/
//
// B 类与 A 类共用同一把锁，是因为它们的写入面在**同一个目录**里相互可见：plan 写链的
// S8 会在锁内 `index.Apply`，而 `eg index rebuild` 会把 `.index/` 里除运行时保留条目之外
// 的一切清掉。两者若各持各的锁（或维护命令干脆不取锁），一次 rebuild 就可能在另一个进程
// 的事务提交中途把它正要同步的库抽走 —— 这不是「索引旧一点」，而是并发下的不可判定态。
//
// # B 类与 A 类的四条硬差别（不得越线）
//
//   - **不开事务**：B 类自身不 `AllocateTxnID`、不 `RecordTxnID`、不 `WriteIntent`、
//     不写 commit / abort 标记。理由是 `.index/` 是**可重建派生**：它没有「前像」值得回滚，
//     崩在半路的补救手段是重跑一次 `eg index rebuild`，而不是回放事务日志。给派生物开事务
//     只会凭空造出一批需要恢复的未闭合事务，且每一笔的正确恢复动作都恰好是「什么都不做」。
//   - **不碰 Git**：`.index/` 被 `eg init` 写进 `.gitignore`，维护命令恒 0 次 commit。
//   - **W26 照进报告**：B 类自己不开事务，但 S2 的 `txn.Recover` 若**真的**回滚了别人留下的
//     未闭合事务，那是一次**权威 Markdown 的写入**，必须如实进本次命令的报告 ——
//     「我只是建了个索引」和「我顺手把上一次崩溃回滚了」对用户是两件事。
//   - **退出码 5 仍不启用**：锁忙（E16）/ 恢复 fail closed（E15）/ 保留条目类型违规（E15）
//     一律经 `TxnBlockedError` 上报，诊断码如实携带，退出码走既有「未分类 → 1」兜底。
//     E15 / E16 → 5 的映射属 T-…-074，本批次一格都不提前实现。
//
// # 时序（逐格定死，颠倒任何一格都是 bug）
//
//	M0  参数校验                 —— **锁外**（dispatch 的 Command.Validate 已完成）
//	M1  txn.Acquire              —— 临界区开始；锁忙 ⇒ E16 且**此刻 .index/ 还没被动过**
//	M2  txn.Recover              —— **持锁后第一件事**；真实回滚产 W26 并入本次报告
//	M3  index.InspectRuntimeReserved —— 运行时保留条目类型体检；违规 ⇒ E15，零索引写
//	M4  maintain()               —— 体检 + 快照 + Build / Rebuild / Sync，**全程仍持锁**
//	M5  Release                  —— defer；报告渲染在锁外
//
// M1 在 M4 之前这件事必须是**结构性**的，而不是靠调用方自觉：`maintain` 是闭包参数，
// 它在语法上就只可能在 `runIndexCritical` 的锁内被调用一次。
//
// # 为什么 M2 必须早于 M4 的快照
//
// `txn.Recover` 会把未闭合事务涉及的权威 Markdown 回滚到前像。若先扫快照再恢复，
// 索引就会被写进一批**恢复前**的内容，随后落盘的水位线又指向恢复后的 HEAD ——
// 一个自洽性已经破掉、却自称 fresh 的索引，比没有索引更坏。

import (
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/index"
	"github.com/ikaqiu-Lemon/EverGreen/internal/report"
	"github.com/ikaqiu-Lemon/EverGreen/internal/txn"
)

// 索引维护临界区的节点名（测试逐字断言时序；生产不消费）。
//
// 与 plan 写链的 `TxnStep*` 刻意**不共用命名**：两条链的节点集合不同（B 类没有
// allocated / intent / committed / git），共用会让「某条链少跑了一格」在断言里看不出来。
const (
	IndexStepAcquired   = "index_acquired"   // M1 完成：已持锁
	IndexStepRecovered  = "index_recovered"  // M2 完成：崩溃恢复屏障已过
	IndexStepReserved   = "index_reserved"   // M3 完成：运行时保留条目体检已过
	IndexStepMaintained = "index_maintained" // M4 完成：索引维护已执行（成功或失败）
	IndexStepReleasing  = "index_releasing"  // M5 之前：即将释放锁（此刻仍持锁）
)

// IndexReservedBlockedNotice 是保留条目类型违规时的固定前缀（供用例逐字断言）。
const IndexReservedBlockedNotice = "运行时保留条目类型违规，索引维护 fail closed：本次零索引写入"

// runIndexCritical 是 `eg index build / rebuild / sync` 三条维护命令的**唯一**临界区入口。
//
// 契约：
//
//   - 进函数即取锁、出函数即释放锁；`maintain` 只在锁内被调用，且至多一次。
//   - 返回 nil ⇒ 三道屏障全过且 `maintain` 成功；返回非 nil ⇒ 由调用方原样上抛。
//     M1 / M2 / M3 的阻断统一是 `*TxnBlockedError`（携 E16 / E15），此时 `.index/` 里
//     的**索引产物**一个字节都没被动过；`maintain` 自己的错误（`*UsageError` /
//     `*CommitFailedError`）原样透出，不被二次包装 —— 包装会把退出码 1 / 4 的分类抹平。
//   - 报告体由调用方持有，本函数只往里追加事实（W28 锁等待、W26 恢复、锁正文写失败）。
//
// **不对 run.lock 再嵌套取锁**：`maintain` 内部调用的 `index.*` 全是纯目录操作，
// plan 写链的 `syncIndexAfterWrite` 也是直接调 `index.Apply`（它本来就跑在 A 类的锁内）。
// 整条链上取锁点恰一个，因此不存在自死锁的可能。
func (r *Root) runIndexCritical(inv *Invocation, rep *report.Report, maintain func() error) error {
	root := inv.VaultRoot

	// —— M1：取锁。——
	//
	// **刻意不传 Now**（P0，与 recover_hook.go 的 S1 同一条理由）：锁等待的判据是
	// `now().Sub(start) >= timeout`，而 `r.Now` 是**业务时钟**、在测试与可复现构建里
	// 常被钉成常量。喂进常量时钟 ⇒ elapsed 恒为 0 ⇒ 超时永不成立 ⇒ 锁被别人占住时
	// 不是退 E16 而是无限退避挂死。LockOptions.Now 为 nil 时 txn 侧回落 time.Now，
	// 这正是我们要的真实墙钟；Timeout 留零，交给 EG_LOCK_TIMEOUT_MS / DefaultLockTimeout。
	lock, aerr := txn.Acquire(root, txn.LockOptions{Argv: txnArgv(inv)})
	if aerr != nil {
		// 此刻连锁都没拿到：索引产物零变化（`.index/` 目录本身可能已由 Acquire 创建，
		// 那是互斥设施而不是索引 —— 两者的区分由 RuntimeReservedEntries 单点表达）。
		return blockedError("获取 "+txn.LockPath(root)+" 失败，本次零索引写入", aerr)
	}
	defer func() {
		fireTxnStep(IndexStepReleasing, "")
		if rerr := lock.Release(); rerr != nil {
			rep.AddWarning(unnumberedWarning(txn.LockPath(root),
				"释放 run.lock 失败（本次索引维护的结果不受影响）：%v", rerr))
		}
	}()
	addTxnDiags(rep, lock.Diagnostics())
	if berr := lock.BodyWriteError(); berr != nil {
		rep.AddWarning(unnumberedWarning(txn.LockPath(root),
			"run.lock 正文写入失败（纯诊断，不影响互斥与退出码）：%v", berr))
	}
	fireTxnStep(IndexStepAcquired, "")

	// —— M2：崩溃恢复屏障。持锁后第一件事，且必须早于 M4 的任何一次权威读。——
	rres, rerr := txn.Recover(root)
	if rerr != nil {
		return blockedError("未闭合事务的崩溃恢复被阻断，本次零索引写入", rerr)
	}
	// 真实回滚才有 W26（noop 时为空）：B 类自己不开事务，但**别人**留下的未闭合事务
	// 被本次命令回滚掉了，这条权威写入必须出现在本次命令的报告里。
	addTxnDiags(rep, rres.Diagnostics)
	fireTxnStep(IndexStepRecovered, rres.TxnID)

	// —— M3：运行时保留条目类型体检（`run.lock` 必须是普通文件、`txn` 必须是目录）。——
	//
	// **今天它是一道冗余屏障，这一点必须写在明面上**：`run.lock` 的类型违规会更早地被
	// M1 的 `openat + O_NOFOLLOW` 顶回来（ELOOP / 非普通文件），`txn` 的类型违规会更早地
	// 被 M2 里 Recover 的 parent-symlink fail closed 顶回来。两者都携 E15，因此现状下
	// 走到这一行时通常已经没有违规可报。
	//
	// 仍然保留它，理由是**判据的归属**：M1 / M2 拒绝的前提是「它们恰好要去碰那个条目」，
	// 那是各自实现路径的副产物，不是索引维护对自身前置条件的表态。而下一格就要在同一个
	// 目录里做删除与创建（`purgeNonReserved` 凭名册删、`cleanupBuildAttempt` 凭因果删），
	// 「保留条目形态必须正确」是它**自己**的前置条件，不该寄存在别人的实现细节上：
	// Recover 在 `txn/` 缺席时会直接 noop 返回，后续批次一旦让维护路径自己创建该目录，
	// 这道判据就是唯一那道。fail closed 的正确形状是「多问一次」，绝不是「先删了再说」。
	if v, bad := index.InspectRuntimeReserved(index.DirPath(root)); bad {
		return indexReservedBlocked(v)
	}
	fireTxnStep(IndexStepReserved, "")

	// —— M4：维护本体。体检 / 快照 / 索引写全在锁内，Release 由上面的 defer 兜底。——
	err := maintain()
	fireTxnStep(IndexStepMaintained, "")
	return err
}

// indexReservedBlocked 把一条保留条目类型违规折成携 E15 的写前阻断错误。
//
// 用 `txn.CodePrecheckFailed`（E15）而不是 `index.CodeIndexCorrupt`（W24）：
// 两者讲的是同一个事实的**两个面**，且必须分开——
//
//	W24  是**只读体检**的结论（`eg index status` / 读路径降级用它，不改退出码）；
//	E15  是**写前校验**的裁决（维护命令据此 fail closed，零写入）。
//
// 同一处磁盘现象在只读侧报 W24、在写入侧报 E15，正是「只读恒不阻断、写入恒 fail closed」
// 这条分层的机器形态。退出码 5 的启用属 T-…-074，本批次仍走未分类兜底。
func indexReservedBlocked(v index.ReservedViolation) error {
	path := txn.IndexDirName + "/" + v.Name
	msg := fmt.Sprintf("%s：%s。%s/ 下的运行时保留条目由事务层独占，索引维护既不删它也不改它；"+
		"请人工核对该条目（它多半是被外部替换成了符号链接或错误类型）后重跑",
		IndexReservedBlockedNotice, v, txn.IndexDirName)
	return &TxnBlockedError{
		Msg: msg,
		Diags: []Diagnostic{{
			Code: txn.CodePrecheckFailed, Level: LevelError, Path: path,
			OpIndex: NonOpDiagnostic, Message: msg,
		}},
	}
}
