package cli

// `eg index build / rebuild / sync` 的**临界区**用例（M6 · T-…-072 批次 B2c1）。
//
// 本文件只测一件事：三条索引维护命令确实被关进了与 plan 写链**同一把**
// `vault/.index/run.lock`，并且持锁之后的次序恰是「恢复 → 保留条目体检 → 维护」。
// 索引自身的三支语义 / 等价性 / 陈旧判定在 index_test.go，一条不重复。
//
// 三条纪律：
//   - **零 mock**：外部持锁者是**真的** flock（另开一个 fd），恢复冲突是**真的**
//     未闭合事务 + 真的外部编辑，坏字节是真字节。没有一处打桩让被测代码改道。
//   - **有限时间**：锁忙分支全部套在 select + 计时器里 —— 若实现把业务时钟喂给
//     `Acquire`，表现是**挂死**而不是失败；用超时兜住才能把它判成红，而不是卡住整包。
//   - **零变化按字节**：`.index/` 的「零变化」是全目录逐文件哈希前后比对（含
//     `txn/` 全树与 `seq`），不是「看了几个文件」。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/index"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
	"github.com/ikaqiu-Lemon/EverGreen/internal/txn"
)

// —— 夹具 ——

// idxRuntimeSnapshot 抓 `.index/` 全树的「相对路径 → 内容哈希」快照。
//
// 目录不存在时返回空表（这本身也是一种可比对的状态）。`run.lock` **包含在内**：
// 是否允许它变化由断言方按场景显式声明，而不是在采集口径里悄悄豁免。
func idxRuntimeSnapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	base := index.DirPath(dir)
	out := map[string]string{}
	err := filepath.Walk(base, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.IsDir() {
			return nil
		}
		raw, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		rel, _ := filepath.Rel(base, p)
		out[filepath.ToSlash(rel)] = store.ContentHash(raw)
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("抓 %s 快照失败：%v", index.DirName, err)
	}
	return out
}

// idxAssertRuntimeUnchanged 逐字比对两份 `.index/` 快照。
//
// allowLockBody=true 时**只**豁免 `run.lock` 的正文：命令成功取到锁就会把
// pid / argv / acquired_at 写进锁正文，那是互斥设施的运行痕迹、不是索引写入。
// 豁免面恰这一个名字，`txn/` 全树与 DB 文件一律逐字比。
func idxAssertRuntimeUnchanged(t *testing.T, before, after map[string]string,
	allowLockBody bool, what string) {
	t.Helper()
	var diffs []string
	for rel, h := range after {
		if allowLockBody && rel == txn.LockFileName {
			continue
		}
		if old, ok := before[rel]; !ok {
			diffs = append(diffs, "新增 "+rel)
		} else if old != h {
			diffs = append(diffs, "改写 "+rel)
		}
	}
	for rel := range before {
		if allowLockBody && rel == txn.LockFileName {
			continue
		}
		if _, ok := after[rel]; !ok {
			diffs = append(diffs, "删除 "+rel)
		}
	}
	if len(diffs) > 0 {
		sort.Strings(diffs)
		t.Fatalf("%s：%s/ 必须逐字节不变，实际变化 %s", what, index.DirName, strings.Join(diffs, "；"))
	}
}

// idxRunAsync 在后台跑一次 `eg index <sub>`，并用计时器兜住「有没有挂死」。
//
// 这层 select 不是保险丝而是**判据**：锁被别人占住时，若实现把 `Root.Now`
// 这种被钉死的业务时钟喂给了 `txn.Acquire`，`elapsed` 恒为 0 ⇒ 超时永不成立 ⇒
// 退避无限循环。表现不是「退 E16」，而是命令连同 CI 一起挂住。
func idxRunAsync(t *testing.T, dir, sub string, limit time.Duration) (int, string) {
	t.Helper()
	type out struct {
		code   int
		stdout string
	}
	done := make(chan out, 1)
	go func() {
		code, stdout, _ := runIndexCLI(t, dir, sub)
		done <- out{code: code, stdout: stdout}
	}()
	select {
	case got := <-done:
		return got.code, got.stdout
	case <-time.After(limit):
		t.Fatalf("锁被占用时 eg index %s 在 %s 内没有返回：等待上限只有 %s=80ms，"+
			"说明 Acquire 拿到的是被钉死的业务时钟（elapsed 恒为 0 ⇒ 超时永不成立 ⇒ 无限退避）",
			sub, limit, txn.LockTimeoutEnv)
		return 0, ""
	}
}

// idxEnvelope 把 `--json` 输出解析成信封（空输出返回零值信封）。
func idxEnvelope(t *testing.T, out string) Envelope {
	t.Helper()
	var env Envelope
	if strings.TrimSpace(out) == "" {
		return env
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("--json 输出不可解析：%v\n%s", err, out)
	}
	return env
}

// idxMaintenanceSubs 是三条 **B 类**维护命令（= 全部子命令去掉只读的 status）。
//
// 从 IndexSubcommands() 派生而不是另抄一份名单：将来若新增第五个子命令，
// 它要么进 status 那一类、要么落进本切片并立刻被本用例覆盖，不存在「漏钉」的第三种可能。
func idxMaintenanceSubs() []string {
	var out []string
	for _, s := range IndexSubcommands() {
		if s != IndexSubStatus {
			out = append(out, s)
		}
	}
	return out
}

// idxStaleTxn 手工造一笔「intent 已发布、commit / abort 皆缺席」的未闭合事务（= 崩溃现场）。
//
// target 是崩溃前打算写进 cardRel 的目标态字节；返回事务号。
func idxStaleTxn(t *testing.T, dir, cardRel string, pre, target []byte) string {
	t.Helper()
	id, err := txn.AllocateTxnID(dir)
	if err != nil {
		t.Fatalf("分配 txn_id 失败：%v", err)
	}
	if _, err := txn.WriteIntent(dir, id, txn.IntentInput{
		Argv: []string{"eg", "apply"}, ExpectCommit: true,
		Now:   func() time.Time { return stampAt(t, idxFixedNow) },
		Files: []txn.FileSpec{{Path: cardRel, PreBytes: pre, TargetBytes: target, TargetOp: "update"}},
	}); err != nil {
		t.Fatalf("发布 intent 失败：%v", err)
	}
	return id
}

// —— 用例本体 ——

// TestIndexMaintenanceUsesRunLock：三条维护命令与 plan 写链共用同一把 run.lock，
// 且持锁后先过崩溃恢复、再过运行时保留条目体检，最后才动索引。
//
// 四个子场景，各自钉住一格不能松的事实：
//
//	① 锁忙        → build / rebuild / sync 全部在有限时间内非零返回且携 E16，
//	                 `.index/` 逐字节不变（连锁正文都没被碰）；status 同场景照常退 0。
//	② 恢复冲突    → 三条命令全部非零返回且携 E15，索引零写、未闭合事务保持未闭合。
//	③ 正常成功    → 三条命令都不开事务（`txn/` 全树含 seq 逐字不变）、不产 Git commit。
//	④ 真实回滚    → 恢复确实回滚了别人留下的未闭合事务时，W26 进**本次命令**的报告。
//	⑤ 保留条目违规 → 写前 fail closed（携 E15），维护那一格一次都没跑。
func TestIndexMaintenanceUsesRunLock(t *testing.T) {
	t.Run("锁忙：三条维护命令快速 E16 且索引零变化", testIndexMaintenanceLockBusy)
	t.Run("恢复冲突：三条维护命令 E15 且索引零写", testIndexMaintenanceRecoverConflict)
	t.Run("正常成功：不开事务、不产 commit、时序固定", testIndexMaintenanceOpensNoTxn)
	t.Run("真实回滚：W26 进本次维护命令的报告", testIndexMaintenanceReportsW26)
	t.Run("保留条目类型违规：写前 E15 且维护一格未跑", testIndexMaintenanceReservedViolation)
}

// ① 锁忙。
func testIndexMaintenanceLockBusy(t *testing.T) {
	dir := idxVault(t)
	authority := idxAuthoritySnapshot(t, dir)
	// 先把索引建起来：这样「零变化」比对的是一个**有内容**的库，而不是空目录。
	if code, _, errOut := runIndexCLI(t, dir, IndexSubBuild); code != ExitOK {
		t.Fatalf("前置 build 退出码 = %d：%s", code, errOut)
	}
	commitsBefore := gitLogCount(t, dir)
	base := txnIDsOn(t, dir)

	// 很小的等待上限：修好之后应在百毫秒量级返回；没修好则无论多小都不会返回。
	t.Setenv(txn.LockTimeoutEnv, "80")

	// 外部持锁者：另开一个 fd 真实握住 flock（OFD 语义，同进程另开一次 open 照样冲突）。
	held, err := txn.Acquire(dir, txn.LockOptions{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("外部持锁失败，用例前提不成立：%v", err)
	}
	defer func() { _ = held.Release() }()

	before := idxRuntimeSnapshot(t, dir)
	for _, sub := range idxMaintenanceSubs() {
		started := time.Now()
		code, out := idxRunAsync(t, dir, sub, 20*time.Second)
		if elapsed := time.Since(started); elapsed > 10*time.Second {
			t.Fatalf("eg index %s：等待上限 80ms，实际耗时 %s —— 锁等待没有走真实墙钟",
				sub, elapsed)
		}
		if code != ExitPrecheckOrLock {
			t.Fatalf("eg index %s：锁被占用（E16）必须按 M6 退 %d，实得退出码 %d：%s",
				sub, ExitPrecheckOrLock, code, out)
		}
		diags := errorDiagsOf(t, idxEnvelope(t, out))
		if !diagsHaveCode(diags, txn.CodeLockTimeout) {
			t.Fatalf("eg index %s：锁等待超时必须携 %s，实得诊断 %+v",
				sub, txn.CodeLockTimeout, diags)
		}
		// 连锁都没拿到 ⇒ **连锁正文都不许写**：这一支不豁免 run.lock。
		idxAssertRuntimeUnchanged(t, before, idxRuntimeSnapshot(t, dir), false,
			"eg index "+sub+" 撞上锁忙")
	}

	// status 是 C 类：不取锁，因此库忙的时候照样能回答（恒退 0）。
	code, out := idxRunAsync(t, dir, IndexSubStatus, 20*time.Second)
	if code != ExitOK {
		t.Fatalf("锁被占用时 eg index status 退出码 = %d，期望 0（只读命令不取锁）：%s", code, out)
	}
	if h := idxHealth(t, out); h != string(index.HealthHealthy) {
		t.Fatalf("锁被占用时 status 的体检结论 = %q，期望 %q（只读不受互斥影响）",
			h, index.HealthHealthy)
	}
	idxAssertRuntimeUnchanged(t, before, idxRuntimeSnapshot(t, dir), false,
		"eg index status 在锁忙时")

	// 全程零权威写、零事务、零 commit。
	idxAssertAuthorityUnchanged(t, dir, authority)
	assertNoNewTxn(t, dir, base, "索引维护撞上锁忙")
	if n := gitLogCount(t, dir); n != commitsBefore {
		t.Fatalf("commit 数 %d → %d：索引维护恒 0 次提交", commitsBefore, n)
	}
}

// ② 恢复冲突（B-R3）：Pass A 判出 post-crash 外部编辑 ⇒ fail closed 携 E15。
//
// 这一支同时反证「M2 早于 M4」：若维护先于恢复发生，索引会被写出来，
// 而下面的 `.index/` 零变化比对当场红。
func testIndexMaintenanceRecoverConflict(t *testing.T) {
	dir := idxVault(t)
	// idxVault 已经用 `eg apply` 落过这张卡：直接引用它的落位路径，不重复建一遍
	// （applyNoteAndCard 不是幂等夹具，二次调用会撞上已存在的 id 而退 2）。
	cardRel := store.CardRel("ai-infra", applyCardID)
	cardAbs := filepath.Join(dir, filepath.FromSlash(cardRel))
	pre := mustRead(t, cardAbs)

	if code, _, errOut := runIndexCLI(t, dir, IndexSubBuild); code != ExitOK {
		t.Fatalf("前置 build 退出码 = %d：%s", code, errOut)
	}
	dbBefore, err := index.Digest(index.DirPath(dir))
	if err != nil {
		t.Fatalf("前置索引应可读：%v", err)
	}

	// 崩溃现场：intent 已发布、commit / abort 皆缺席。
	stale := idxStaleTxn(t, dir, cardRel, pre,
		append(append([]byte(nil), pre...), []byte("\n<!-- 崩溃前的目标态 -->\n")...))
	// post-crash 外部编辑：current 既非前像也非目标态 ⇒ Pass A 判冲突。
	if err := os.WriteFile(cardAbs,
		append(append([]byte(nil), pre...), []byte("\n<!-- 崩溃后有人又手改了一次 -->\n")...),
		0o644); err != nil {
		t.Fatalf("制造 post-crash 外部编辑失败：%v", err)
	}

	before := idxRuntimeSnapshot(t, dir)
	commitsBefore := gitLogCount(t, dir)
	for _, sub := range idxMaintenanceSubs() {
		code, out, _ := runIndexCLI(t, dir, sub)
		if code != ExitPrecheckOrLock {
			t.Fatalf("eg index %s：恢复被阻断（E15）必须 fail closed 退 %d，实得退出码 %d：%s",
				sub, ExitPrecheckOrLock, code, out)
		}
		diags := errorDiagsOf(t, idxEnvelope(t, out))
		if !diagsHaveCode(diags, txn.CodePrecheckFailed) {
			t.Fatalf("eg index %s：恢复 fail closed 必须携 %s，实得诊断 %+v",
				sub, txn.CodePrecheckFailed, diags)
		}
		// 拿到了锁（因此锁正文被写过），但**维护一格没跑**：索引与事务日志逐字不变。
		idxAssertRuntimeUnchanged(t, before, idxRuntimeSnapshot(t, dir), true,
			"eg index "+sub+" 撞上恢复冲突")
	}

	// 索引内容逐字未变；未闭合事务仍未闭合（不写 abort，等人工处置）。
	dbAfter, err := index.Digest(index.DirPath(dir))
	if err != nil {
		t.Fatalf("恢复被阻断后索引应仍可读：%v", err)
	}
	if dbAfter != dbBefore {
		t.Fatal("恢复被阻断时索引被改写了：屏障未过就不该动 .index/")
	}
	if markerExists(dir, stale, txn.AbortMarker) || markerExists(dir, stale, txn.CommitMarker) {
		t.Fatalf("事务 %s 应保持未闭合（fail closed 不写 abort / commit）", stale)
	}
	if n := gitLogCount(t, dir); n != commitsBefore {
		t.Fatalf("commit 数 %d → %d：索引维护恒 0 次提交", commitsBefore, n)
	}
}

// ③ 正常成功：三条命令都不开事务、不产 commit，且节点时序逐字固定。
func testIndexMaintenanceOpensNoTxn(t *testing.T) {
	dir := idxVault(t)
	authority := idxAuthoritySnapshot(t, dir)
	commitsBefore := gitLogCount(t, dir)
	base := txnIDsOn(t, dir)
	// `txn/` 全树（含全局计数器 seq）：AllocateTxnID 会 bump seq，因此 seq 逐字不变
	// 就是「一次事务号都没分配过」的机器形态。
	txnBefore := idxTxnTreeSnapshot(t, dir)

	rec := watchTxn(t, nil)
	for _, sub := range idxMaintenanceSubs() {
		rec.names = nil
		rec.ids = nil
		code, out, errOut := runIndexCLI(t, dir, sub)
		if code != ExitOK {
			t.Fatalf("eg index %s 退出码 = %d，期望 0：%s", sub, code, errOut)
		}
		want := []string{IndexStepAcquired, IndexStepRecovered, IndexStepReserved,
			IndexStepMaintained, IndexStepReleasing}
		if strings.Join(rec.names, ",") != strings.Join(want, ",") {
			t.Fatalf("eg index %s 的临界区节点序列 =\n  %v\n期望逐字\n  %v\n"+
				"（取锁 → 恢复 → 保留条目体检 → 维护 → 释放；B 类没有 allocate / intent / "+
				"commit / git 这几格）", sub, rec.names, want)
		}
		if raw := strings.TrimSpace(string(
			rcRawAt(t, []byte(out), "data", "report", "git", "commit"))); raw != "null" {
			t.Fatalf("eg index %s 的报告里 git.commit = %s，期望 null", sub, raw)
		}
		assertNoNewTxn(t, dir, base, "eg index "+sub)
		idxAssertTxnTreeUnchanged(t, txnBefore, idxTxnTreeSnapshot(t, dir),
			"eg index "+sub+" 成功之后")
	}
	if n := gitLogCount(t, dir); n != commitsBefore {
		t.Fatalf("commit 数 %d → %d：eg index 恒 0 次提交", commitsBefore, n)
	}
	idxAssertAuthorityUnchanged(t, dir, authority)
}

// ④ 真实回滚：B 类自己不开事务，但恢复**别人**留下的未闭合事务是一次权威写入，
// 必须以 W26 出现在本次维护命令的报告里（否则用户不知道自己的卡被改回去了）。
func testIndexMaintenanceReportsW26(t *testing.T) {
	dir := idxVault(t)
	cardRel := store.CardRel("ai-infra", applyCardID)
	cardAbs := filepath.Join(dir, filepath.FromSlash(cardRel))
	pre := mustRead(t, cardAbs)

	// 崩溃现场 + 盘上停在**目标态**（= 崩溃发生在提交中途）⇒ Recover 回滚到前像。
	target := append(append([]byte(nil), pre...), []byte("\n<!-- 崩溃前的目标态 -->\n")...)
	stale := idxStaleTxn(t, dir, cardRel, pre, target)
	if err := os.WriteFile(cardAbs, target, 0o644); err != nil {
		t.Fatalf("把盘面摆到目标态失败：%v", err)
	}

	code, out, errOut := runIndexCLI(t, dir, IndexSubBuild)
	if code != ExitOK {
		t.Fatalf("恢复成功后 build 退出码 = %d，期望 0：%s", code, errOut)
	}
	if got := idxWarnCodes(t, out); !contains(got, txn.CodeTxnRecovered) {
		t.Fatalf("恢复真实回滚了一笔未闭合事务，本次 eg index build 的报告必须留一条 %s，实得 %v",
			txn.CodeTxnRecovered, got)
	}
	if !markerExists(dir, stale, txn.AbortMarker) {
		t.Fatalf("事务 %s 被回滚后必须写下 abort 标记", stale)
	}
	if strings.Contains(string(mustRead(t, cardAbs)), "崩溃前的目标态") {
		t.Fatal("未闭合事务的目标态被当成已提交内容留在了盘上：恢复没有真正回滚")
	}
	// 回滚之后建出来的索引，内容必须对应**回滚后**的权威 —— 这正是 M2 早于 M4 的意义：
	// 若快照取自恢复之前，库里存的就是那条被回滚掉的目标态，紧接着的 status 会判 stale。
	// （build 分支的 data.index 没有 freshness 这一格，因此这条只能由 status 回答。）
	code, out, errOut = runIndexCLI(t, dir, IndexSubStatus)
	if code != ExitOK {
		t.Fatalf("回滚后 status 退出码 = %d：%s", code, errOut)
	}
	if f := idxFreshness(t, out); f != string(index.FreshnessFresh) {
		t.Fatalf("回滚后立刻 build 出来的索引 freshness = %q，期望 %q（快照必须取自恢复之后）",
			f, index.FreshnessFresh)
	}
}

// ⑤ 运行时保留条目类型违规：写前 fail closed（携 E15），维护那一格**一次都没跑**。
//
// 语料是一条**指向合法目录**的 `.index/txn` symlink：目标是真目录，因此
// `stat` 看它一切正常，只有 `lstat` 口径的判定才认得出违规。这正是
// index.InspectRuntimeReserved 的安全前提 —— 一条指向别处的 `txn` 链接，
// 就是一条把事务日志写出目录外的通道。
//
// 断言刻意**不声称是哪一道屏障拦下的**：现状下 M2 里 Recover 的 parent-symlink
// fail closed 会先一步顶回来，M3 的保留条目体检是同口径的冗余（见 index_lock.go
// 里 M3 那段注释）。可断言的、也是真正要保的不变量只有两条：
// ① 携 E15 且退出码非零；② `IndexStepMaintained` 从未触发、索引逐字未变 ——
// 即「屏障没过就绝不动 .index/」。哪一层先拦下属实现细节，钉死它只会让日后
// 收紧任一层时凭空变红。
func testIndexMaintenanceReservedViolation(t *testing.T) {
	dir := idxVault(t)
	if code, _, errOut := runIndexCLI(t, dir, IndexSubBuild); code != ExitOK {
		t.Fatalf("前置 build 退出码 = %d：%s", code, errOut)
	}
	dbBefore, err := index.Digest(index.DirPath(dir))
	if err != nil {
		t.Fatalf("前置索引应可读：%v", err)
	}

	// 把真的事务日志目录挪到 vault 之外，再用一条 symlink 顶替它的位置。
	outside := filepath.Join(t.TempDir(), "txn-real")
	if err := os.Rename(txn.TxnRootPath(dir), outside); err != nil {
		t.Fatalf("挪走事务日志目录失败：%v", err)
	}
	if err := os.Symlink(outside, txn.TxnRootPath(dir)); err != nil {
		t.Fatalf("造 symlink 语料失败：%v", err)
	}
	// 前提自检：这确实是一条**指向合法目录**的链接（否则语料退化成「目标也坏了」，
	// 就不再是「只有 lstat 认得出」的那一类违规）。
	if fi, serr := os.Stat(txn.TxnRootPath(dir)); serr != nil || !fi.IsDir() {
		t.Fatalf("用例前提不成立：%s 应能 stat 成一个目录，实得 %v / %v",
			txn.TxnDirName, fi, serr)
	}
	if _, bad := index.InspectRuntimeReserved(index.DirPath(dir)); !bad {
		t.Fatalf("用例前提不成立：%s 的 symlink 必须被判为保留条目类型违规", txn.TxnDirName)
	}

	commitsBefore := gitLogCount(t, dir)
	rec := watchTxn(t, nil)
	for _, sub := range idxMaintenanceSubs() {
		rec.names = nil
		rec.ids = nil
		code, out, _ := runIndexCLI(t, dir, sub)
		if code != ExitPrecheckOrLock {
			t.Fatalf("eg index %s：保留条目类型违规（E15）必须 fail closed 退 %d，实得退出码 %d：%s",
				sub, ExitPrecheckOrLock, code, out)
		}
		diags := errorDiagsOf(t, idxEnvelope(t, out))
		if !diagsHaveCode(diags, txn.CodePrecheckFailed) {
			t.Fatalf("eg index %s：保留条目类型违规必须携 %s，实得诊断 %+v",
				sub, txn.CodePrecheckFailed, diags)
		}
		if contains(rec.names, IndexStepMaintained) {
			t.Fatalf("eg index %s：屏障未过却跑到了维护那一格，实际节点序列 %v", sub, rec.names)
		}
		dbAfter, derr := index.Digest(index.DirPath(dir))
		if derr != nil {
			t.Fatalf("eg index %s 被拦下后索引应仍可读：%v", sub, derr)
		}
		if dbAfter != dbBefore {
			t.Fatalf("eg index %s：屏障未过就动了索引 —— 违规时必须零索引写", sub)
		}
	}
	if n := gitLogCount(t, dir); n != commitsBefore {
		t.Fatalf("commit 数 %d → %d：索引维护恒 0 次提交", commitsBefore, n)
	}
}

// —— `txn/` 全树快照（含 seq）——

// idxTxnTreeSnapshot 抓 `.index/txn/` 全树的「相对路径 → 内容哈希」。
//
// 与 idxRuntimeSnapshot 分开是因为断言意图不同：这一份专门用来反证
// 「B 类维护命令一次事务号都没分配、一个标记都没写」，因此**必须**含 `seq`。
func idxTxnTreeSnapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	base := txn.TxnRootPath(dir)
	out := map[string]string{}
	err := filepath.Walk(base, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.IsDir() {
			return nil
		}
		raw, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		rel, _ := filepath.Rel(base, p)
		out[filepath.ToSlash(rel)] = store.ContentHash(raw)
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("抓事务日志快照失败：%v", err)
	}
	return out
}

func idxAssertTxnTreeUnchanged(t *testing.T, before, after map[string]string, what string) {
	t.Helper()
	var diffs []string
	for rel, h := range after {
		if old, ok := before[rel]; !ok {
			diffs = append(diffs, "新增 "+rel)
		} else if old != h {
			diffs = append(diffs, "改写 "+rel)
		}
	}
	for rel := range before {
		if _, ok := after[rel]; !ok {
			diffs = append(diffs, "删除 "+rel)
		}
	}
	if len(diffs) > 0 {
		sort.Strings(diffs)
		t.Fatalf("%s：索引维护不开事务，%s/%s/ 必须逐字节不变（含 seq），实际变化 %s",
			what, index.DirName, txn.TxnDirName, strings.Join(diffs, "；"))
	}
}
