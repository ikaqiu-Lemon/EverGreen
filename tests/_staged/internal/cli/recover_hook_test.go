package cli

// recover_hook_test.go —— M6 · T-…-072 批次 B2a：plan-based 写链**事务编排**的聚焦反证。
//
// 被测对象是 recover_hook.go 里唯一的临界区 runPlanCritical。它把 `apply` / `edit` /
// `deprecate` / `restore` / `replaced-by` / `rel add|remove` 这七条命令共用的写口，
// 从「直落实盘 + 事后 Git」改成固定时序的事务。本文件逐条钉住那个时序与它的边界：
//
//	S1 取锁 → S2 恢复 → S3 锁内重读重校验 → S4 原子预演 → S5 intent 屏障 →
//	S6 原子提交（commit marker）→ S7 Git → S8 写后索引同步 → S9 释放锁
//
// 观测手段是 recover_hook.go 里的 txnOrderHook（生产恒 nil 的观测接缝）：用例在关键节点
// 回调里**检查磁盘当下的样子**，从而把「谁先谁后」变成可反证的事实，而不是读代码得来的印象。
//
// 本文件不测 internal/txn 自身的崩溃安全（那是 txn 包的用例），也不测 internal/index。

// **Schema v2 · T-…-003 夹具重钉（事实变了，判据形态不变）**：本文件里自动路径
// （`append_card` / `append_knowledge`）原先追加的是 Card 的 `解释与依据`。契约 D-7 把
// Knowledge 收敛为 `知识内容 / 条件与边界 / 用户补充` 三分区，`解释与依据` 自 v2 起
// 只作为**存量文件**的分区存在、且不在自动路径写白名单内，因此再拿它当写目标会让
// 整条 op 在校验期就被判「缺可写分区」而退 2 —— 那考的不再是本文件要考的事
// （B3 跳过 / 部分成功 / 事务放弃 / op 顺序 / 报告计数）。改用同为「只追加块」语义的
// v2 分区 `条件与边界`，本文件的判据一格未动。

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/proposal"
	"github.com/ikaqiu-Lemon/EverGreen/internal/report"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
	"github.com/ikaqiu-Lemon/EverGreen/internal/txn"
)

// —— 观测与断言辅助 ——

// txnSteps 装一次命令跑出来的节点序列（以及每个节点当时的 txn_id）。
type txnSteps struct {
	names []string
	ids   []string
}

func (s *txnSteps) at(name string) int {
	for i, n := range s.names {
		if n == name {
			return i
		}
	}
	return -1
}

// idAt 返回某节点回调时的 txn_id（节点缺席则空串）。
func (s *txnSteps) idAt(name string) string {
	if i := s.at(name); i >= 0 {
		return s.ids[i]
	}
	return ""
}

// watchTxn 装上顺序钩子并在用例结束时摘掉。extra 可在指定节点做现场取证 / 制造干扰。
func watchTxn(t *testing.T, extra func(step, txnID string)) *txnSteps {
	t.Helper()
	rec := &txnSteps{}
	txnOrderHook = func(step, txnID string) {
		rec.names = append(rec.names, step)
		rec.ids = append(rec.ids, txnID)
		if extra != nil {
			extra(step, txnID)
		}
	}
	t.Cleanup(func() { txnOrderHook = nil })
	return rec
}

// authoritySnapshot 抓「权威内容」的字节快照：三个知识产物根 + vault 根下的散装
// Markdown / 配置（`unprocessed.md`、`evergreen.yml`、`SKILL.md` 之类）。
//
// 刻意**不含** `.git/`、`.index/` 与 `.eg/`：分别是版本库自身、运行时目录（M5 索引库、
// M6 的 run.lock 与事务日志）与 CLI 的 last-report 落点。区分这两类是本批次的关键：
// `.index/run.lock` 与 `.eg/last-report.json` 的出现**不是**权威写入，而合同 §2 又要求
// 「先取锁、后校验」，所以连校验失败也必然留下它们。「零写入」这条判据钉的是**知识内容**，
// 不是进程运行痕迹 —— 运行时目录里真正要命的「有没有开过事务」由 assertNoNewTxn 与
// assertRuntimeOnlyLock 单独、且更严地钉住。
func authoritySnapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", txn.IndexDirName, StateDir:
				return filepath.SkipDir
			}
			return nil
		}
		raw, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		rel, _ := filepath.Rel(dir, p)
		out[filepath.ToSlash(rel)] = store.ContentHash(raw)
		return nil
	})
	if err != nil {
		t.Fatalf("抓权威快照失败：%v", err)
	}
	return out
}

// assertAuthorityUnchanged 逐字节比对权威内容快照（新增 / 改写 / 删除都当场报出）。
func assertAuthorityUnchanged(t *testing.T, dir string, before map[string]string, what string) {
	t.Helper()
	after := authoritySnapshot(t, dir)
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
		t.Fatalf("%s：权威内容必须逐字节不变，实际变化 %s", what, strings.Join(diffs, "；"))
	}
}

// txnIDsOn 列出 `.index/txn/` 下的事务目录名（不含全局计数器 `seq`）。
func txnIDsOn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(txn.TxnRootPath(dir))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("读事务日志根失败：%v", err)
	}
	var out []string
	for _, e := range entries {
		if e.Name() == txn.SeqFileName || e.Name() == txn.SeqFileName+".tmp" {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

// newTxnsSince 返回相对基线新增的事务目录（夹具自身的写入也会留下事务日志，
// 因此「有没有开事务」只能按**增量**判定，不能按总数）。
func newTxnsSince(t *testing.T, dir string, base []string) []string {
	t.Helper()
	old := map[string]bool{}
	for _, id := range base {
		old[id] = true
	}
	var out []string
	for _, id := range txnIDsOn(t, dir) {
		if !old[id] {
			out = append(out, id)
		}
	}
	return out
}

// assertNoNewTxn 反证「本次根本没开事务」：相对基线零新增事务目录，自然也没有 intent / 标记。
func assertNoNewTxn(t *testing.T, dir string, base []string, what string) {
	t.Helper()
	if got := newTxnsSince(t, dir, base); len(got) != 0 {
		t.Fatalf("%s：不该开事务，却新增了事务目录 %v", what, got)
	}
}

// onlyNewTxn 断言相对基线恰新增一个事务目录，并返回它。
func onlyNewTxn(t *testing.T, dir string, base []string, what string) string {
	t.Helper()
	got := newTxnsSince(t, dir, base)
	if len(got) != 1 {
		t.Fatalf("%s：期望恰新增 1 个事务目录，实得 %v", what, got)
	}
	return got[0]
}

// assertRuntimeOnlyLock 反证 `.index/` 里除了运行时锁**什么都没有**：
// 没有事务目录、没有 intent / commit / abort，也没有索引库（写命令绝不替用户建索引）。
//
// 这是「validation-failure 允许留下 run.lock，但不许留下别的任何东西」的机器形态。
func assertRuntimeOnlyLock(t *testing.T, dir, what string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, txn.IndexDirName))
	if os.IsNotExist(err) {
		return // 连 .index/ 都没有，更严，通过
	}
	if err != nil {
		t.Fatalf("读运行时目录失败：%v", err)
	}
	for _, e := range entries {
		if e.Name() == txn.LockFileName {
			continue
		}
		t.Fatalf("%s：`%s/` 下只允许出现运行时锁 %s，却有 %q"+
			"（事务日志 / 索引库都不该在这条路径上产生）",
			what, txn.IndexDirName, txn.LockFileName, e.Name())
	}
}

// —— 运行时目录（`.index/`）的**增量**反证 ——
//
// `assertRuntimeOnlyLock` 钉的是绝对口径：「`.index/` 下除锁之外空无一物」。它成立的前提
// 是「夹具本身从不开事务」——而 M6 · T-…-072 批次 C1 起 `eg capture` 也走完整事务，凡是用
// 真实 `eg capture` 造出来的夹具（applyVault），`.index/txn/<id>/` 里就**合法地**躺着一笔
// 历史事务。此时绝对口径会把夹具自己的产物误判成被测命令的产物。
//
// 因此这里给出增量口径：执行前抓基线，执行后比对。判据不是「运行时目录里没有东西」，
// 而是**被测命令一个字节都没往里加、也没改既有的那些**——只有 run.lock 正文可以变
// （它是本次取锁的持有者信息），且必须还是同一个 inode（同一个文件被原地更新，
// 而不是被删掉重建：后者意味着有人绕开了 flock 的长驻 fd 语义）。
//
// 这比「忽略整个 .index/」严得多：新增索引库、遗留 commit.tmp、被改写的历史 intent、
// 悄悄前进的 seq 计数器，全都会当场报出来。

// indexTreeSnapshot 抓 `.index/` 全子树的字节快照（相对 vault 根的 slash 路径 → 内容哈希）。
//
// **不含** run.lock：它的正文本就允许随每次取锁更新，身份稳定性由 assertLockInodeStable
// 单独、且以 inode 为判据地钉住。除它以外，`.index/` 下的一切都在这张快照里 ——
// 事务目录、intent.json、commit / abort 标记、seq 计数器、任何索引库产物。
func indexTreeSnapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	root := filepath.Join(dir, txn.IndexDirName)
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) && p == root {
				return nil // 连 .index/ 都还没有：空快照，更严
			}
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		slash := filepath.ToSlash(rel)
		if slash == txn.IndexDirName+"/"+txn.LockFileName {
			return nil
		}
		raw, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		out[slash] = store.ContentHash(raw)
		return nil
	})
	if err != nil {
		t.Fatalf("抓运行时目录快照失败：%v", err)
	}
	return out
}

// txnTreeOf 从运行时快照里筛出事务日志子树（`.index/txn/**`）。
func txnTreeOf(snap map[string]string) map[string]string {
	prefix := txn.IndexDirName + "/" + txn.TxnDirName + "/"
	out := map[string]string{}
	for rel, h := range snap {
		if strings.HasPrefix(rel, prefix) {
			out[rel] = h
		}
	}
	return out
}

// diffSnapshots 返回「新增 / 改写 / 删除」的可读清单（升序，便于失败信息稳定）。
func diffSnapshots(before, after map[string]string) []string {
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
	sort.Strings(diffs)
	return diffs
}

// assertTxnTreeUnchanged 反证「既有事务日志一个字节都没动」。
//
// 它与 assertNoNewTxn 是两个维度：后者只看目录名有没有新增，前者看**内容**——
// 历史 intent 被改写、历史 commit 标记被抹掉、seq 计数器悄悄前进（分配过号又清理了痕迹），
// 都只有内容判据抓得住。
func assertTxnTreeUnchanged(t *testing.T, dir string, before map[string]string, what string) {
	t.Helper()
	if diffs := diffSnapshots(txnTreeOf(before), txnTreeOf(indexTreeSnapshot(t, dir))); len(diffs) > 0 {
		t.Fatalf("%s：既有事务日志必须逐字节不变，实际变化 %s", what, strings.Join(diffs, "；"))
	}
}

// assertIndexTreeUnchanged 反证「运行时目录里除 run.lock 正文外没有任何产物变化」。
//
// 覆盖面比 assertTxnTreeUnchanged 更宽：`.index/` 下**任何**新增文件（索引库、临时目录残留、
// 计数器）都算违约。写命令绝不替用户建索引，校验失败更不该留下半成品。
func assertIndexTreeUnchanged(t *testing.T, dir string, before map[string]string, what string) {
	t.Helper()
	if diffs := diffSnapshots(before, indexTreeSnapshot(t, dir)); len(diffs) > 0 {
		t.Fatalf("%s：`%s/` 下除 %s 正文外必须零变化，实际变化 %s",
			what, txn.IndexDirName, txn.LockFileName, strings.Join(diffs, "；"))
	}
}

// lockIdentity 记录 run.lock 的文件身份（os.FileInfo 内含 dev+ino，交给 os.SameFile 判定）。
//
// 夹具已经真实取过锁，所以调用点上锁文件必须已在盘：拿不到就说明前置条件本身塌了，
// 与其让后面的断言退化成「反正没有就算过」，不如当场失败。
func lockIdentity(t *testing.T, dir string) os.FileInfo {
	t.Helper()
	fi, err := os.Lstat(txn.LockPath(dir))
	if err != nil {
		t.Fatalf("前置条件：夹具已通过真实命令取过锁，%s 必须在盘：%v", txn.LockPath(dir), err)
	}
	if !fi.Mode().IsRegular() {
		t.Fatalf("前置条件：%s 必须是普通文件，实得 %s", txn.LockPath(dir), fi.Mode())
	}
	return fi
}

// assertLockInodeStable 反证「锁文件被原地更新，而不是被删掉重建」。
//
// 允许的只有正文变化（持有者 pid / argv / acquired_at 每次取锁都会重写）；
// inode 一旦变了，就意味着有人 unlink 后重建了锁文件 —— 那会让还持着旧 fd 的进程
// 与新进程各自 flock 到**两个不同的 inode** 上，互斥当场失效。
func assertLockInodeStable(t *testing.T, dir string, before os.FileInfo, what string) {
	t.Helper()
	after := lockIdentity(t, dir)
	if !os.SameFile(before, after) {
		t.Fatalf("%s：%s 必须是同一个 inode（只允许正文更新，不允许删除重建）",
			what, txn.LockPath(dir))
	}
}

// readIntentOf 读回某个事务的 intent.json（解码成通用 map，避免与 txn 的结构体耦合）。
func readIntentOf(t *testing.T, dir, txnID string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(txn.TxnDirPath(dir, txnID), txn.IntentFileName))
	if err != nil {
		t.Fatalf("读事务 %s 的 intent 失败：%v", txnID, err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("intent.json 不可解析：%v", err)
	}
	return m
}

// intentPathsOf 返回 intent.files[].path 的升序集合。
func intentPathsOf(t *testing.T, dir, txnID string) []string {
	t.Helper()
	m := readIntentOf(t, dir, txnID)
	files, _ := m["files"].([]any)
	var out []string
	for _, one := range files {
		f, _ := one.(map[string]any)
		p, _ := f["path"].(string)
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func markerExists(dir, txnID, name string) bool {
	_, err := os.Stat(filepath.Join(txn.TxnDirPath(dir, txnID), name))
	return err == nil
}

// warnCodesOf 收集报告 warnings[] 的诊断码（空码不计）。
func warnCodesOf(rep report.Report) []string {
	var out []string
	for _, w := range rep.Warnings {
		if w.Code != "" {
			out = append(out, w.Code)
		}
	}
	return out
}

func reportHasCode(rep report.Report, code string) bool {
	for _, c := range warnCodesOf(rep) {
		if c == code {
			return true
		}
	}
	return false
}

// —— ① 时序：九个节点的先后次序逐字定死 ——

// TestPlanTxnStepOrderIsFixed 钉住一次成功写入的完整节点序列。
//
// 这一条同时覆盖了四件本批次最关键的事：S2 恢复早于 S3 重读、intent 早于提交、
// Git 晚于 commit marker、写后索引同步早于释放锁。
func TestPlanTxnStepOrderIsFixed(t *testing.T) {
	dir := applyVault(t)
	rec := watchTxn(t, nil)

	code, env, errOut := runApplyPlan(t, dir, notePlan())
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0：%s", code, errOut)
	}
	want := []string{
		TxnStepAcquired, TxnStepRecovered, TxnStepReread, TxnStepExecuted,
		TxnStepAllocated, TxnStepIntent, TxnStepCommitted, TxnStepGit,
		TxnStepIndexSync, TxnStepReleasing,
	}
	if strings.Join(rec.names, ",") != strings.Join(want, ",") {
		t.Fatalf("事务节点序列 =\n  %v\n期望逐字\n  %v", rec.names, want)
	}
	// 逐对复核先后（序列相等已蕴含，但把合同里点名的几对再显式钉一遍，改序时报错更直白）。
	for _, pair := range [][2]string{
		{TxnStepAcquired, TxnStepRecovered},  // S1 → S2
		{TxnStepRecovered, TxnStepReread},    // S2 → S3：恢复必须早于任何一次读
		{TxnStepReread, TxnStepExecuted},     // S3 → S4
		{TxnStepExecuted, TxnStepIntent},     // S4 → S5：写集合定盘后才发布 intent
		{TxnStepIntent, TxnStepCommitted},    // S5 → S6：intent 屏障早于权威落盘
		{TxnStepCommitted, TxnStepGit},       // S6 → S7：Git 严格晚于 commit marker
		{TxnStepGit, TxnStepIndexSync},       // S7 → S8
		{TxnStepIndexSync, TxnStepReleasing}, // S8 → S9：索引同步仍在锁内
	} {
		if rec.at(pair[0]) >= rec.at(pair[1]) {
			t.Fatalf("%s 必须早于 %s，实际序列 %v", pair[0], pair[1], rec.names)
		}
	}
	// txn_id 的出现时机：分配之前一律空，分配之后一路带着同一个 id。
	for _, early := range []string{TxnStepAcquired, TxnStepReread, TxnStepExecuted} {
		if got := rec.idAt(early); got != "" {
			t.Fatalf("%s 节点不该有 txn_id，实得 %q", early, got)
		}
	}
	id := rec.idAt(TxnStepAllocated)
	for _, late := range []string{TxnStepIntent, TxnStepCommitted, TxnStepGit, TxnStepIndexSync} {
		if got := rec.idAt(late); got != id {
			t.Fatalf("%s 节点的 txn_id = %q，期望与分配时一致的 %q", late, got, id)
		}
	}
	if rep := applyReport(t, env); rep.TxnID != id {
		t.Fatalf("报告 txn_id = %q，期望 %q", rep.TxnID, id)
	}
}

// TestPlanTxnIntentPrecedesFirstAuthoritativeRename：intent 落盘时，权威文件**还没被写**。
//
// 反过来说：任何一次权威 rename 都必须能在事务日志里找到它的写意图。这是崩溃恢复
// 能够工作的前提 —— 先写的字节若没有对应的 intent，恢复就不知道该把它还原成什么。
func TestPlanTxnIntentPrecedesFirstAuthoritativeRename(t *testing.T) {
	dir := applyVault(t)
	applyNoteAndCard(t, dir)
	newCard := "k-20260901-intent-barrier"
	cardAbs := filepath.Join(dir, filepath.FromSlash(store.CardRel("ai-infra", newCard)))

	var checked bool
	rec := watchTxn(t, func(step, txnID string) {
		switch step {
		case TxnStepIntent:
			checked = true
			// intent 已在盘……
			if _, err := os.Stat(filepath.Join(txn.TxnDirPath(dir, txnID), txn.IntentFileName)); err != nil {
				t.Fatalf("intent 节点回调时 intent.json 必须已在盘：%v", err)
			}
			// ……而权威文件一个字节都还没有。
			if _, err := os.Stat(cardAbs); !os.IsNotExist(err) {
				t.Fatalf("intent 发布时权威文件已经存在：首个权威 rename 必须晚于 intent 屏障（err=%v）", err)
			}
			if markerExists(dir, txnID, txn.CommitMarker) {
				t.Fatal("intent 节点回调时不该有 commit 标记")
			}
		case TxnStepCommitted:
			if _, err := os.Stat(cardAbs); err != nil {
				t.Fatalf("commit 标记落盘后权威文件必须在盘：%v", err)
			}
			if !markerExists(dir, txnID, txn.CommitMarker) {
				t.Fatal("committed 节点回调时 commit 标记必须已在盘")
			}
		}
	})

	code, _, errOut := runApplyPlan(t, dir, cardPlan(newCard, ""))
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0：%s", code, errOut)
	}
	if !checked {
		t.Fatal("intent 节点没有被触达：本用例什么都没证明")
	}
	if rec.at(TxnStepIntent) >= rec.at(TxnStepCommitted) {
		t.Fatal("intent 必须早于 commit")
	}
}

// TestPlanTxnRereadsInsideLock：S3 的读发生在**取锁之后**，锁外的旧快照一律作废。
//
// 造一次「拿到锁的瞬间库被改掉」：若实现复用了锁外读到的内容，B3 的 content_hash
// 复核就会拿着过期哈希判定通过，从而把别人的改动覆盖掉；只有锁内重读才会发现不一致
// 并按 file_changed 整文件跳过。
func TestPlanTxnRereadsInsideLock(t *testing.T) {
	dir := applyVault(t)
	_, cardRel := applyNoteAndCard(t, dir)
	cardAbs := filepath.Join(dir, filepath.FromSlash(cardRel))
	staleHash := hashOf(t, dir, cardRel)
	base := txnIDsOn(t, dir)

	var mutated bool
	rec := watchTxn(t, func(step, _ string) {
		if step != TxnStepAcquired || mutated {
			return
		}
		mutated = true
		// 取到锁之后、S3 重读之前，模拟「上一个写者刚提交完」。
		raw := mustRead(t, cardAbs)
		if err := os.WriteFile(cardAbs, append(raw, []byte("\n<!-- 并发改动 -->\n")...), 0o644); err != nil {
			t.Fatalf("制造并发改动失败：%v", err)
		}
	})

	plan := `{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"锁内重读反证",
"base":{"` + applyCardID + `":"` + staleHash + `"},
"ops":[{"op":"append_card","card":"` + applyCardID + `","sections":{"条件与边界":"不该被写进去。"}}]}`
	code, env, errOut := runApplyPlan(t, dir, plan)
	if code != ExitPartialWrite {
		t.Fatalf("退出码 = %d，期望 3（锁内重读应发现文件已变化并整文件跳过）：%s", code, errOut)
	}
	if !mutated || rec.at(TxnStepAcquired) < 0 {
		t.Fatal("没有触达取锁节点：本用例什么都没证明")
	}
	rep := applyReport(t, env)
	if len(rep.Skipped) != 1 || rep.Skipped[0].Kind != string(store.SkipFileChanged) {
		t.Fatalf("skipped[] = %+v，期望恰一条 file_changed（证明用的是锁内重读的现态）", rep.Skipped)
	}
	if !strings.Contains(string(mustRead(t, cardAbs)), "并发改动") {
		t.Fatal("并发改动被覆盖了：锁内重读的意义就是不覆盖别人刚落的字节")
	}
	if strings.Contains(string(mustRead(t, cardAbs)), "不该被写进去") {
		t.Fatal("拿过期快照写了盘：这正是锁内重读要防的事")
	}
	// 零 accepted write-set ⇒ 根本不开事务。
	assertNoNewTxn(t, dir, base, "全部 op 被跳过")
	if rep.TxnID != "" {
		t.Fatalf("没开事务却填了 txn_id = %q", rep.TxnID)
	}
}

// —— ② A-59：txn_id 与事务日志目录同真 ——

// TestReportTxnIDMatchesJournal 跨「报告」与「事务日志」两侧对撞 txn_id：
// 报告里的那一个必须是合法形态、必须恰好是盘上那个事务目录、且那个目录必须真的提交了。
func TestReportTxnIDMatchesJournal(t *testing.T) {
	dir := applyVault(t)
	base := txnIDsOn(t, dir)
	code, env, errOut := runApplyPlan(t, dir, notePlan())
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0：%s", code, errOut)
	}
	rep := applyReport(t, env)

	// ① 形态：t + 16 位小写十六进制（与 txn.FormatTxnID 同一口径）。
	if !regexp.MustCompile(`^t[0-9a-f]{16}$`).MatchString(rep.TxnID) {
		t.Fatalf("txn_id = %q，不符合 `t` + 16 位小写 hex", rep.TxnID)
	}
	if !txn.ValidTxnID(rep.TxnID) {
		t.Fatalf("txn_id = %q 未被 txn.ValidTxnID 接受", rep.TxnID)
	}

	// ② 与日志目录同真：本次恰新增一个事务目录，且名字就是它。
	if got := onlyNewTxn(t, dir, base, "一次成功写入"); got != rep.TxnID {
		t.Fatalf("新增事务目录 = %q，报告 txn_id = %q：两者必须恰好对上", got, rep.TxnID)
	}

	// ③ 那个事务确实提交了（commit 在、abort 不在），且 intent 覆盖了报告里的每一个 links[]。
	if !markerExists(dir, rep.TxnID, txn.CommitMarker) {
		t.Fatal("报告填了 txn_id，事务目录里却没有 commit 标记")
	}
	if markerExists(dir, rep.TxnID, txn.AbortMarker) {
		t.Fatal("已提交的事务不该有 abort 标记")
	}
	paths := intentPathsOf(t, dir, rep.TxnID)
	for _, link := range rep.Links {
		if !contains(paths, link) {
			t.Fatalf("报告说写了 %s，intent.files[] 里却没有它：%v", link, paths)
		}
	}
	// ④ intent 的 argv 里带得上本次命令（诊断可读性；至少含命令名）。
	if argv, _ := readIntentOf(t, dir, rep.TxnID)["argv"].([]any); len(argv) < 2 {
		t.Fatalf("intent.argv 至少要有 `eg <command>`，实得 %v", argv)
	}
}

// —— ③ S2：崩溃恢复屏障在报告里如实留痕 ——

// TestPlanTxnRecoverReportsW26BeforeReread：启动时发现未闭合事务 → 回滚 + 一条 W26 进报告，
// 且这一切都发生在 S3 重读之前。
func TestPlanTxnRecoverReportsW26BeforeReread(t *testing.T) {
	dir := applyVault(t)
	_, cardRel := applyNoteAndCard(t, dir)
	cardAbs := filepath.Join(dir, filepath.FromSlash(cardRel))
	pre := mustRead(t, cardAbs)

	// 手工造一个「intent 已发布、commit / abort 皆缺席」的未闭合事务（= 崩溃现场）。
	stale, err := txn.AllocateTxnID(dir)
	if err != nil {
		t.Fatalf("分配 txn_id 失败：%v", err)
	}
	now := func() time.Time { return captureAt(t) }
	if _, err := txn.WriteIntent(dir, stale, txn.IntentInput{
		Argv: []string{"eg", "apply"}, ExpectCommit: true, Now: now,
		Files: []txn.FileSpec{{
			Path: cardRel, PreBytes: pre,
			TargetBytes: append(append([]byte(nil), pre...), []byte("\n<!-- 崩溃前的目标态 -->\n")...),
			TargetOp:    "update",
		}},
	}); err != nil {
		t.Fatalf("发布 intent 失败：%v", err)
	}

	var sawRecovered bool
	rec := watchTxn(t, func(step, _ string) {
		if step == TxnStepRecovered {
			sawRecovered = true
			// 恢复完成时，未闭合事务已经被写上 abort。
			if !markerExists(dir, stale, txn.AbortMarker) {
				t.Fatal("恢复节点回调时，未闭合事务必须已写下 abort 标记（先回滚、后 abort）")
			}
		}
		if step == TxnStepReread && !sawRecovered {
			t.Fatal("S3 重读发生在 S2 恢复之前：屏障失效")
		}
	})

	code, env, errOut := runApplyPlan(t, dir, cardPlan("k-20260901-after-recover", ""))
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0（恢复成功后照常写入）：%s", code, errOut)
	}
	rep := applyReport(t, env)
	if !reportHasCode(rep, txn.CodeTxnRecovered) {
		t.Fatalf("未闭合事务被回滚，报告必须留一条 %s：%v",
			txn.CodeTxnRecovered, warnCodesOf(rep))
	}
	if rec.at(TxnStepRecovered) >= rec.at(TxnStepReread) {
		t.Fatalf("恢复必须早于重读，实际序列 %v", rec.names)
	}
	// 被回滚的那一笔没有把「崩溃前的目标态」留在盘上。
	if strings.Contains(string(mustRead(t, cardAbs)), "崩溃前的目标态") {
		t.Fatal("未闭合事务的目标态被当成已提交内容留在了盘上")
	}
	// 本次自己的事务另起一个 id，与被恢复的那个不是同一个。
	if rep.TxnID == "" || rep.TxnID == stale {
		t.Fatalf("本次事务 txn_id = %q，不得为空、也不得复用被回滚的 %q", rep.TxnID, stale)
	}
}

// —— ④ 三条「不开事务」的边界 ——

// TestPlanTxnValidationFailureOpensNoTxn：校验失败 ⇒ 退 2、权威零写、不开事务。
//
// 判据一律**逐用例、按增量**读：夹具 applyVault 用真实 `eg capture` 造原文，而 C1 起
// capture 自己就是一笔完整事务，因此 `.index/txn/<id>/` 里合法地躺着一笔历史事务、
// `.index/run.lock` 也早已在盘。所以本用例先抓五份基线（权威内容、Git、事务目录名、
// 运行时全树、锁 inode），再逐条断言**被测这一次**什么都没添、什么都没改：
// 权威零写、Git 零动、无新增事务、既有事务日志逐字节不变、锁还是同一个 inode
// （只有正文允许更新）。绝不整块忽略 `.index/`。
func TestPlanTxnValidationFailureOpensNoTxn(t *testing.T) {
	dir := applyVault(t)
	before := authoritySnapshot(t, dir)
	logBefore := gitLogCount(t, dir)
	headBefore := gitOut(t, dir, "rev-parse", "HEAD")
	txnsBefore := txnIDsOn(t, dir)
	indexBefore := indexTreeSnapshot(t, dir)
	lockBefore := lockIdentity(t, dir)

	bad := `{"plan_version":1,"verb":"process","domain":"ai-infra","ops":[
{"op":"add_relation","from":"k-20260901-nope","type":"supports","target":"k-20260901-nada","reason":"x"}]}`
	code, env, _ := runApplyPlan(t, dir, bad)
	if code != ExitValidation {
		t.Fatalf("退出码 = %d，期望 2", code)
	}
	assertAuthorityUnchanged(t, dir, before, "校验失败")
	assertNoNewTxn(t, dir, txnsBefore, "校验失败")
	assertTxnTreeUnchanged(t, dir, indexBefore, "校验失败")
	assertIndexTreeUnchanged(t, dir, indexBefore, "校验失败")
	assertLockInodeStable(t, dir, lockBefore, "校验失败")
	if got := gitLogCount(t, dir); got != logBefore {
		t.Fatalf("校验失败不得产生 commit：%d → %d", logBefore, got)
	}
	if got := gitOut(t, dir, "rev-parse", "HEAD"); got != headBefore {
		t.Fatalf("校验失败不得移动 HEAD：%q → %q", headBefore, got)
	}
	if rep := applyReport(t, env); rep.TxnID != "" {
		t.Fatalf("未开事务却填了 txn_id = %q", rep.TxnID)
	}
}

// TestPlanTxnAbortedPreviewIsZeroWrite：预演期出现普通写失败 ⇒ 整事务放弃。
//
// 关键在于「前一个 op 在 overlay 里已经写成了」也照样不落盘：原子性的意思是
// **要么全部生效、要么全部不生效**，不存在「先写成的那一半留下」。
func TestPlanTxnAbortedPreviewIsZeroWrite(t *testing.T) {
	dir := applyVault(t)
	_, cardRel := applyNoteAndCard(t, dir)
	cardAbs := filepath.Join(dir, filepath.FromSlash(cardRel))
	newCard := "k-20260901-never-lands"
	newAbs := filepath.Join(dir, filepath.FromSlash(store.CardRel("ai-infra", newCard)))
	logBefore := gitLogCount(t, dir)
	base := txnIDsOn(t, dir)
	before := authoritySnapshot(t, dir)

	// S3 校验通过之后、S4 预演之前，把既有卡换成一个同名目录：op2 的读会拿到 EISDIR。
	// 这既不是 B3 的「文件已变化」（那会记 skipped 并继续），也不是校验失败，而是一次
	// 货真价实的**普通写失败** —— 正是「失败即整事务放弃」要覆盖的那一类。
	watchTxn(t, func(step, _ string) {
		if step != TxnStepReread {
			return
		}
		if _, err := os.Stat(cardAbs); err != nil {
			return // 只在第一次触发时改造
		}
		if err := os.Rename(cardAbs, cardAbs+".moved"); err != nil {
			t.Fatalf("制造读失败失败：%v", err)
		}
		if err := os.MkdirAll(cardAbs, 0o755); err != nil {
			t.Fatalf("制造读失败失败：%v", err)
		}
	})

	plan := `{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"预演失败反证",
"base":{"` + applyCardID + `":"` + hashOf(t, dir, cardRel) + `"},
"ops":[{"op":"create_card","card_id":"` + newCard + `","title":"会被放弃的卡",
"sources":[{"source":"` + applySourceID + `","note":"` + applyNoteID + `","rel":"support","reason":"原文给出定义"}],
"sections":{"知识内容":"这张卡不该落盘。"}},
{"op":"append_card","card":"` + applyCardID + `","sections":{"条件与边界":"读不到的追加。"}}]}`
	code, env, errOut := runApplyPlan(t, dir, plan)

	// 先把夹具复原，避免「目录冒充卡」污染后面的快照比对。
	_ = os.RemoveAll(cardAbs)
	if err := os.Rename(cardAbs+".moved", cardAbs); err != nil {
		t.Fatalf("复原夹具失败：%v", err)
	}

	if code != ExitPartialWrite {
		t.Fatalf("退出码 = %d，期望 3：%s", code, errOut)
	}
	rep := applyReport(t, env)
	if len(rep.Warnings) == 0 {
		t.Fatal("预演失败必须留下可读的失败诊断")
	}
	// ① 预演里已经写成的那张新卡，一个字节都没落到实盘。
	if _, err := os.Stat(newAbs); !os.IsNotExist(err) {
		t.Fatalf("预演里写成的卡落到了实盘：整事务放弃必须零权威写（err=%v）", err)
	}
	// ② 权威内容整体回到命令执行前。
	assertAuthorityUnchanged(t, dir, before, "预演失败")
	// ③ 零 intent、零事务、零 commit。
	assertNoNewTxn(t, dir, base, "预演失败")
	if got := gitLogCount(t, dir); got != logBefore {
		t.Fatalf("预演失败不得产生 commit：%d → %d", logBefore, got)
	}
	if rep.TxnID != "" {
		t.Fatalf("未开事务却填了 txn_id = %q", rep.TxnID)
	}
	if len(rep.Links) != 0 {
		t.Fatalf("整事务放弃后 links[] 必须为空（预演里的写入不是事实）：%v", rep.Links)
	}
	if rep.Git.Commit != nil {
		t.Fatalf("整事务放弃不得产生 commit：%v", *rep.Git.Commit)
	}
}

// TestPlanTxnZeroAcceptedWriteSetSkipsTxnAndGit：accepted write-set 为空 ⇒
// 不分配 txn_id、不发 intent、不提交、也不制造空 Git commit。
func TestPlanTxnZeroAcceptedWriteSetSkipsTxnAndGit(t *testing.T) {
	dir := applyVault(t)
	_, cardRel := applyNoteAndCard(t, dir)
	logBefore := gitLogCount(t, dir)
	before := authoritySnapshot(t, dir)
	base := txnIDsOn(t, dir)

	// 唯一的 op 带着过期 content_hash ⇒ 整文件跳过 ⇒ 一个 accepted 写都没有。
	stale := "sha256:" + strings.Repeat("0", 64)
	plan := `{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"零写反证",
"base":{"` + applyCardID + `":"` + stale + `"},
"ops":[{"op":"append_card","card":"` + applyCardID + `","sections":{"条件与边界":"不会被写。"}}]}`
	code, env, errOut := runApplyPlan(t, dir, plan)
	if code != ExitPartialWrite {
		t.Fatalf("退出码 = %d，期望 3：%s", code, errOut)
	}
	_ = cardRel
	assertAuthorityUnchanged(t, dir, before, "全部跳过")
	assertNoNewTxn(t, dir, base, "accepted write-set 为空")
	if got := gitLogCount(t, dir); got != logBefore {
		t.Fatalf("没有写入就不该有 commit：%d → %d", logBefore, got)
	}
	rep := applyReport(t, env)
	if rep.TxnID != "" {
		t.Fatalf("未开事务却填了 txn_id = %q", rep.TxnID)
	}
	if rep.Git.Commit != nil {
		t.Fatalf("不得制造空 commit：%v", *rep.Git.Commit)
	}
}

// —— ⑤ S7：Git 失败的边界（退 4、不回滚、不二次写）——

// TestPlanTxnGitFailureKeepsTargetStateWithoutSecondWrite：
// Git 失败 ⇒ 退 4；Markdown 保持**目标态**；不回滚、不再写第二次、不开第二个事务。
func TestPlanTxnGitFailureKeepsTargetStateWithoutSecondWrite(t *testing.T) {
	dir := applyVault(t)
	logBefore := gitLogCount(t, dir)
	base := txnIDsOn(t, dir)

	var atGit map[string]string
	watchTxn(t, func(step, _ string) {
		if step == TxnStepGit {
			// Git 刚跑完（失败）的瞬间，把权威内容拍下来。
			atGit = authoritySnapshot(t, dir)
		}
	})

	r := newTestRoot(t, dir)
	r.NewRepo = failingCommitRepo()
	code, env, errOut := runApplyPlanWith(t, r, dir, notePlan())
	if code != ExitCommitFailed {
		t.Fatalf("退出码 = %d，期望 4：%s", code, errOut)
	}
	if atGit == nil {
		t.Fatal("没有触达 Git 节点：本用例什么都没证明")
	}
	rep := applyReport(t, env)

	// ① Markdown 已经生效并保持目标态：links[] 里的文件都在盘上。
	if len(rep.Links) == 0 {
		t.Fatal("Markdown 已提交，links[] 不该为空")
	}
	for _, rel := range rep.Links {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("Git 失败不得回滚 Markdown，%s 却不在盘上：%v", rel, err)
		}
	}
	// ② Git 之后**没有第二次权威写**：命令返回时的字节与 Git 节点当时逐字相同。
	assertAuthorityUnchanged(t, dir, atGit, "Git 失败后")

	// ③ 事务侧：本次恰一个事务、commit 标记在盘、没有第二个事务被开出来。
	if got := onlyNewTxn(t, dir, base, "Git 失败"); got != rep.TxnID {
		t.Fatalf("新增事务目录 %q 与报告 txn_id %q 对不上（Git 失败绝不开第二个事务）",
			got, rep.TxnID)
	}
	if !markerExists(dir, rep.TxnID, txn.CommitMarker) {
		t.Fatal("Markdown 事务已提交，commit 标记必须在盘")
	}
	if markerExists(dir, rep.TxnID, txn.AbortMarker) {
		t.Fatal("Git 失败不是 Markdown 事务失败，不该有 abort 标记")
	}
	// ④ Git 侧：没有新 commit，报告里 git.commit 为 null。
	if got := gitLogCount(t, dir); got != logBefore {
		t.Fatalf("commit 数 %d → %d，期望不变（Git 失败）", logBefore, got)
	}
	if rep.Git.Commit != nil {
		t.Fatalf("Git 失败时 git.commit 必须为 null，实得 %v", *rep.Git.Commit)
	}
}

// TestPlanTxnProposalWriteBackRidesSameTransaction：提案 `execution` 的回写与本次写入
// **同一个** overlay / write-set / intent；Git 失败也不会为它另起第二个事务。
func TestPlanTxnProposalWriteBackRidesSameTransaction(t *testing.T) {
	dir := applyVault(t)
	card1Rel, card2Rel := peSeedTwoCards(t, dir)
	prel := peApprovedProposal(t, dir)
	base := txnIDsOn(t, dir)

	// 一张卡照常写、另一张卡凭据过期被跳过 ⇒ 账本不完整 ⇒ execution=failed 需要回写。
	plan := `{"plan_version":1,"verb":"process","domain":"ai-infra",
"reason":"按已批准提案处理两张卡，其中一张的凭据已过期",
"base":{"` + applyCardID + `":"` + hashOf(t, dir, card1Rel) + `",
"` + applyCard2ID + `":"sha256:` + strings.Repeat("0", 64) + `"},
"ops":[{"op":"delete","target":"` + applyCardID + `","reason":"内容重复且无引用",
"initiator":"user","proposal":"` + string(peProposalID) + `"},
{"op":"append_card","card":"` + applyCardID + `","sections":{"条件与边界":"这一条会被写入。"}},
{"op":"append_card","card":"` + applyCard2ID + `","sections":{"条件与边界":"这一条不会被写入。"}}]}`

	r := newTestRoot(t, dir)
	r.NewRepo = failingCommitRepo() // Git 失败：正是「会不会补第二次写」最危险的那一支
	code, env, errOut := runApplyPlanWith(t, r, dir, plan, "--"+UserRequestFlag)
	if code != ExitCommitFailed {
		t.Fatalf("退出码 = %d，期望 4（Markdown 已提交、Git 失败）：%s", code, errOut)
	}
	rep := applyReport(t, env)
	if rep.TxnID == "" {
		t.Fatal("Markdown 事务已提交，报告必须带 txn_id")
	}

	// ① 本次恰一个事务，且提案文件在这**同一份** intent 里。
	if got := onlyNewTxn(t, dir, base, "提案回写"); got != rep.TxnID {
		t.Fatalf("新增事务目录 %q 与报告 txn_id %q 对不上（提案回写不得另起事务）",
			got, rep.TxnID)
	}
	paths := intentPathsOf(t, dir, rep.TxnID)
	if !contains(paths, prel) {
		t.Fatalf("提案 %s 不在 intent.files[] 里：%v（回写漏出了原子域）", prel, paths)
	}
	if !contains(paths, card1Rel) {
		t.Fatalf("本次写入的卡 %s 不在 intent.files[] 里：%v", card1Rel, paths)
	}
	if contains(paths, card2Rel) {
		t.Fatalf("被跳过的卡 %s 不该进 intent.files[]：%v", card2Rel, paths)
	}

	// ② 盘上的提案字节 == intent 声明的目标字节哈希：说明它只被写过一次（就是事务那次）。
	m := readIntentOf(t, dir, rep.TxnID)
	files, _ := m["files"].([]any)
	var wantHash string
	for _, one := range files {
		f, _ := one.(map[string]any)
		if p, _ := f["path"].(string); p == prel {
			wantHash, _ = f["target_hash"].(string)
		}
	}
	if wantHash == "" {
		t.Fatal("intent 里没有提案文件的 target_hash")
	}
	gotHash := store.ContentHash(mustRead(t, filepath.Join(dir, filepath.FromSlash(prel))))
	if gotHash != wantHash {
		t.Fatalf("提案文件的盘上字节与 intent 目标不一致：Git 失败后又被写了第二次？\n盘上 %s\nintent %s",
			gotHash, wantHash)
	}
	// ③ 磁盘上的提案确实记了 failed（回写发生过，不是被整条丢掉）。
	if got := peReadProposal(t, dir, prel).P.Execution.Status; got != proposal.ExecFailed {
		t.Fatalf("提案 execution.status = %q，期望 %q", got, proposal.ExecFailed)
	}
}

// —— ⑥ S6：提交期普通 I/O 失败 ⇒ 合同 §5.2 主动放弃（退 3，不是退 1）——

// TestPlanTxnCommitRollbackIsPartialWriteNotBlocked：
// 提交期出现普通 I/O 失败 ⇒ txn 层把**全部**权威文件回滚到前像并写 abort ⇒ CLI 必须把它
// 翻译成「一条都没写成」的退 3，而不是基础设施崩坏的退 1；Git 一步都不许跑。
//
// 制造失败的位置刻意选在**最后一格**：全部权威 rename 都已成功、只差 commit 标记没发布。
// 这一格最危险 —— 磁盘上此刻确实是目标态，若 CLI 把它当成「已写入」，用户就会拿到一份
// 「说写了、实际被回滚了」的报告；反过来若把它当基础设施崩坏退 1，又会掩盖「事务已干净
// 收敛」的事实。合同 §5.2 对这一格的裁决是：主动放弃、退 3、逐条交代目标未写。
func TestPlanTxnCommitRollbackIsPartialWriteNotBlocked(t *testing.T) {
	dir := applyVault(t)
	before := authoritySnapshot(t, dir)
	logBefore := gitLogCount(t, dir)
	base := txnIDsOn(t, dir)

	// 在 intent 发布之后、提交之前，把 commit 标记的**发布临时名**占成非空目录。
	// 标记发布走「写 commit.tmp → fsync → renameat(commit.tmp → commit)」，这一手因此
	// 精确地只让最后一格失败：备料与全部权威 rename 都照常成功，磁盘一度真的是目标态。
	// 若标记发布的落盘口径变了，提交会成功、下面每一条断言立刻红（不会静默通过）。
	var blocked string
	watchTxn(t, func(step, txnID string) {
		if step != TxnStepIntent {
			return
		}
		blocked = filepath.Join(txn.TxnDirPath(dir, txnID), txn.CommitMarker+".tmp")
		if err := os.MkdirAll(filepath.Join(blocked, "occupied"), 0o755); err != nil {
			t.Fatalf("制造标记发布失败失败：%v", err)
		}
	})

	code, env, errOut := runApplyPlan(t, dir, notePlan())
	if code != ExitPartialWrite {
		t.Fatalf("退出码 = %d，期望 3（提交失败已整体回滚，属主动放弃而非基础设施崩坏）：%s",
			code, errOut)
	}
	if blocked == "" {
		t.Fatal("没有触达 intent 节点：本用例什么都没证明")
	}
	_ = os.RemoveAll(blocked) // 清掉夹具，避免污染后续断言

	// ① 权威内容回到事务开始前（一条都没写成）。
	assertAuthorityUnchanged(t, dir, before, "提交失败并回滚后")
	// ② 事务侧：abort 在盘、commit 不在盘。
	id := onlyNewTxn(t, dir, base, "提交失败并回滚")
	if !markerExists(dir, id, txn.AbortMarker) {
		t.Fatal("回滚完成后必须写下 abort 标记")
	}
	if markerExists(dir, id, txn.CommitMarker) {
		t.Fatal("没提交成功却有 commit 标记")
	}
	// ③ Git 一步没跑。
	if got := gitLogCount(t, dir); got != logBefore {
		t.Fatalf("提交失败不得产生 commit：%d → %d", logBefore, got)
	}
	// ④ 报告：txn_id **必填**且恰是 abort 日志目录名（A-59 的审计边界是「号码已分配」，
	// 不是「事务已提交」）；links[] 为空、无 commit、逐条交代目标未写。
	//
	// 这一格是本用例最不能省的一条：磁盘上凭空多了一个 `.index/txn/<id>` 目录、里面躺着
	// abort 标记，报告若只字不提这个号码，用户就没有任何入口去核对「那笔事务到底是干净
	// 放弃了，还是留下了半应用态」。报告里的 txn_id 与 abort 目录名对上，才是这条闭环。
	rep := applyReport(t, env)
	if rep.TxnID != id {
		t.Fatalf("报告 txn_id = %q，abort 日志目录 = %q：主动回滚的事务号必须进报告且两者相等"+
			"（A-59：Allocate 成功即入报告）", rep.TxnID, id)
	}
	if !markerExists(dir, rep.TxnID, txn.AbortMarker) {
		t.Fatalf("报告 txn_id = %q 指向的目录里没有 abort 标记", rep.TxnID)
	}
	if len(rep.Links) != 0 {
		t.Fatalf("一条都没写成，links[] 必须为空：%v", rep.Links)
	}
	if rep.Git.Commit != nil {
		t.Fatalf("提交失败不得产生 commit：%v", *rep.Git.Commit)
	}
	var unwritten int
	for _, w := range rep.Warnings {
		if strings.Contains(w.Message, "目标未写入") {
			unwritten++
		}
	}
	if unwritten == 0 {
		t.Fatalf("必须逐条交代「目标未写入」，实际 warnings = %+v", rep.Warnings)
	}
}

// —— ⑦ A-59 审计边界：Allocate 之后的阻断也必须交付报告 ——

// TestPlanTxnIntentFailureStillReportsTxnIDAndW26：
// txn_id 已分配、intent 发布失败 ⇒ 权威零写，但**报告照产**，且必须同时带上
//   - `txn_id`：号码已在 `.index/txn/` 占位，用户得知道去哪个目录看这笔未闭合事务；
//   - S2 的 `W26`：本次启动确实回滚过一笔崩溃残留，这条留痕不能因为后续阻断就消失。
//
// 这一条是对「`return nil, error` 把报告整个丢掉」的直接反证。早先那版在 intent 失败时
// 直接把错误抛给调用方，runPlan 的 `if err != nil { return nil, err }` 随即让整份报告
// 蒸发 —— 用户拿到的只有一句错误文案，而库里凭空多了一个事务目录、还刚发生过一次恢复。
// A-59 的审计边界是**号码分配成功**，不是提交成功：分配之后的任何结局都要留下可追溯记录。
func TestPlanTxnIntentFailureStillReportsTxnIDAndW26(t *testing.T) {
	dir := applyVault(t)
	_, cardRel := applyNoteAndCard(t, dir)
	cardAbs := filepath.Join(dir, filepath.FromSlash(cardRel))
	pre := mustRead(t, cardAbs)

	// ① 造一个未闭合事务（崩溃现场）：本次 S2 恢复会回滚它并产出 W26。
	stale, err := txn.AllocateTxnID(dir)
	if err != nil {
		t.Fatalf("分配 txn_id 失败：%v", err)
	}
	now := func() time.Time { return captureAt(t) }
	if _, err := txn.WriteIntent(dir, stale, txn.IntentInput{
		Argv: []string{"eg", "apply"}, ExpectCommit: true, Now: now,
		Files: []txn.FileSpec{{
			Path: cardRel, PreBytes: pre,
			TargetBytes: append(append([]byte(nil), pre...), []byte("\n<!-- 崩溃前的目标态 -->\n")...),
			TargetOp:    "update",
		}},
	}); err != nil {
		t.Fatalf("发布 intent 失败：%v", err)
	}

	before := authoritySnapshot(t, dir) // 崩溃现场只写 .index/，权威内容此刻已是前像
	logBefore := gitLogCount(t, dir)
	base := txnIDsOn(t, dir)

	// ② 在号码分配之后、发布屏障之前，把 intent.json 提前占住：
	// WriteIntent 的防覆盖闸门（fail closed，携 E15）会拒绝二次写，于是 S5 精确失败。
	var mine string
	watchTxn(t, func(step, txnID string) {
		if step != TxnStepAllocated {
			return
		}
		mine = txnID
		p := filepath.Join(txn.TxnDirPath(dir, txnID), txn.IntentFileName)
		if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
			t.Fatalf("制造 intent 发布失败失败：%v", err)
		}
	})

	code, env, errOut := runApplyPlan(t, dir, cardPlan("k-20260901-intent-blocked", ""))
	if mine == "" {
		t.Fatal("没有触达 allocated 节点：本用例什么都没证明")
	}
	if code == ExitOK {
		t.Fatalf("intent 发布失败必须阻断，实得退出码 0：%s", errOut)
	}

	// ③ 权威零写、零 Git：发布屏障没过，一个字节都不许落到知识内容上。
	assertAuthorityUnchanged(t, dir, before, "intent 发布失败")
	if got := gitLogCount(t, dir); got != logBefore {
		t.Fatalf("intent 发布失败不得产生 commit：%d → %d", logBefore, got)
	}
	if markerExists(dir, mine, txn.CommitMarker) {
		t.Fatal("intent 都没发布成功，不该有 commit 标记")
	}

	// ④ **报告存在**，且 txn_id 恰是本次那个已在盘的事务目录。
	if got := onlyNewTxn(t, dir, base, "intent 发布失败"); got != mine {
		t.Fatalf("新增事务目录 = %q，本次分配的号码 = %q", got, mine)
	}
	rep := applyReport(t, env)
	if rep.TxnID != mine {
		t.Fatalf("报告 txn_id = %q，本次已分配的号码 = %q：Allocate 之后的阻断也必须交出号码"+
			"（A-59；`return nil, error` 会把它连同整份报告一起吞掉）", rep.TxnID, mine)
	}

	// ⑤ S2 的 W26 一条不少地留在报告里：它先于本次阻断发生，不该被阻断带走。
	if !reportHasCode(rep, txn.CodeTxnRecovered) {
		t.Fatalf("启动时回滚过未闭合事务 %s，报告必须保留 %s：%v",
			stale, txn.CodeTxnRecovered, warnCodesOf(rep))
	}

	// ⑥ 报告如实：没写就是没写。
	if len(rep.Links) != 0 {
		t.Fatalf("零权威写，links[] 必须为空：%v", rep.Links)
	}
	if rep.Git.Commit != nil {
		t.Fatalf("未提交不得填 commit：%v", *rep.Git.Commit)
	}
}

// —— ⑧ 诊断提取：包装过的 E15 也必须能取到 ——

// TestBlockedErrorExtractsDiagnosticsThroughWrapping 反证 blockedError 用的是
// errors.As 而不是裸类型断言。
//
// 三种形态逐一过：裸错误、`fmt.Errorf(%w)` 单层包装、`errors.Join` 多值包装。
// 若实现退回 `err.(txnDiagnoser)`，后两种当场落到兜底分支 —— 诊断码从 E15 变成空串。
// 这不是风格问题：诊断码是 T-…-074 做退出码映射的唯一依据，丢码就等于把一次
// 「写前安全复核 fail closed」降级成「不明原因失败」，退 5 也就无从谈起。
func TestBlockedErrorExtractsDiagnosticsThroughWrapping(t *testing.T) {
	// **C2 · I-…-025 夹具适配（零弱化）**：ScanBlockedError 的损坏事务清单由「纯 ID 列表」
	// 换成「ID + 不可解析原因」的 CorruptTxnRef —— 合同 §5（I-…-025 脚注第 2 条）要求每个
	// 损坏事务都必须交出不可解析原因，而 ID 与原因分两份清单存必然漂移。本处只换夹具构造
	// 形状：原有判据（条目数守恒 / 码恒为 E15 / errors.As 一路穿透 / 兜底恰 1 条 E21）
	// **一条未减、一条未放宽**，并按 I-…-025 **加严**一条 —— 包装穿透后 txn_id 必须仍在
	// 诊断的 target 位（机器读法唯一取 target，不得改回解析 message 文案，也不得落 path）。
	const corruptID = "t0000000000000001"
	base := &txn.ScanBlockedError{Corrupt: []txn.CorruptTxnRef{
		{TxnID: corruptID, Reason: "intent.json 不是合法 JSON"},
	}}
	// 裸错误自带两条诊断（每个损坏事务一条 + 总述一条），全部携 E15。
	want := len(base.Diagnostics())
	if want < 2 {
		t.Fatalf("夹具错误至少要带 2 条诊断，实得 %d", want)
	}

	cases := []struct {
		name string
		err  error
	}{
		{"裸错误", base},
		{"fmt.Errorf 单层包装", fmt.Errorf("上层加了点上下文：%w", base)},
		{"fmt.Errorf 双层包装", fmt.Errorf("再包一层：%w", fmt.Errorf("外层：%w", base))},
		{"errors.Join 多值", errors.Join(errors.New("同时还有别的毛病"), base)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := blockedError("原子提交前的复核失败", c.err)
			if len(got.Diags) != want {
				t.Fatalf("诊断条目 = %d，期望 %d（条目数守恒，不折叠不去重）：%+v",
					len(got.Diags), want, got.Diags)
			}
			for _, d := range got.Diags {
				if d.Code != txn.CodePrecheckFailed {
					t.Fatalf("诊断码 = %q，期望 %q —— 包装之后取不到码，说明用的是类型断言而非 errors.As",
						d.Code, txn.CodePrecheckFailed)
				}
			}
			// **加严（C2 · I-…-025）**：txn_id 必须活着穿过包装落到 target 位，
			// 且不得被塞进 path（path 位留给 .index/txn/<txn_id> 这一真实目录）。
			hasTarget := false
			for _, d := range got.Diags {
				if d.Path == corruptID {
					t.Fatalf("txn_id 落在了 path 位（%+v）：path 恒是路径，txn_id 归 target", d)
				}
				if d.Target == corruptID {
					hasTarget = true
				}
			}
			if !hasTarget {
				t.Fatalf("包装穿透后没有任何诊断把 txn_id %q 放进 target 位：%+v", corruptID, got.Diags)
			}
			// Unwrap 链仍然通到底层 sentinel 类型（供上层 errors.As 继续分类）。
			var probe *txn.ScanBlockedError
			if !errors.As(error(got), &probe) {
				t.Fatal("TxnBlockedError 必须让 errors.As 一路穿透到底层 txn 错误")
			}
		})
	}

	// 反面：不带诊断的普通错误走兜底，恰一条 error（不许静默产出零条）。
	//
	// **C2 · I-…-015 口径修正**：合同 §5 只授权「未编号 **warning** 填空码」，兜底这条是
	// **error 级**，因此 code 位必须落在编号域内（现为 E21「执行未能完成」）。原判据「码恒为
	// 空」正是被 I-…-015 判红的那个事实，这里按合同改成**更严**的双侧断言：条目数仍恰 1，
	// 且码既非空、又必须等于命令层为该错误族发放的那一个编号（不许随便换成别的码）。
	plain := blockedError("锁路径异常", errors.New("某个说不清的 I/O 错误"))
	if len(plain.Diags) != 1 {
		t.Fatalf("无诊断错误应兜底成恰 1 条 error，实得 %+v", plain.Diags)
	}
	if got := plain.Diags[0]; got.Code == "" || got.Code != E21 || got.Level != LevelError {
		t.Fatalf("兜底条目应是 level=error 且 code=%s（error 级不许留空码，见合同 §5 与 I-…-015），实得 %+v",
			E21, plain.Diags)
	}
}

// —— ⑨ P0：锁等待必须走真实墙钟，不能用被钉死的业务时钟 ——

// errorDiagsOf 从 --json 信封的 data.errors 里取回诊断清单。
//
// **C2 · I-…-016 口径修正**：合同 §3 把信封 `warnings[]` 定义为 warning / info 级诊断的
// 唯一去处，`data.errors[]` 只承载 **error 级**。因此错误路径上 TxnBlockedError 携带的诊断
// 会按 `level` **分桶**投递：终态 `E16`（error）落 `data.errors[]`，等待期的 `W28`
// （warning）落信封 `warnings[]`。取 W28 一律改用 warningDiagsOf / assertLockWaitBuckets。
func errorDiagsOf(t *testing.T, env Envelope) []Diagnostic {
	t.Helper()
	raw, err := json.Marshal(env.Data["errors"])
	if err != nil {
		t.Fatalf("data.errors 不可序列化：%v", err)
	}
	var out []Diagnostic
	if strings.TrimSpace(string(raw)) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("data.errors 不可解析：%v\n%s", err, raw)
	}
	return out
}

func diagsHaveCode(list []Diagnostic, code string) bool {
	for _, d := range list {
		if d.Code == code {
			return true
		}
	}
	return false
}

// warningDiagsOf 从 --json 信封的顶层 warnings[] 里取回诊断清单（warning / info 级的唯一去处）。
func warningDiagsOf(t *testing.T, env Envelope) []Diagnostic {
	t.Helper()
	out := make([]Diagnostic, 0, len(env.Warnings))
	out = append(out, env.Warnings...)
	return out
}

// assertLockWaitBuckets 是锁忙路径的**分桶纪律**判据（C2 · I-…-016），比原先「W28 随错误
// 一起交付」更严：既要 W28 在册，还要它**只**在 warnings[] 里，且 data.errors[] 恰一条
// 终态 E16 —— 也就是说，原来那条「W28 必须可取到」的保护一格没松，只是取材面从
// data.errors[] 搬到了合同 §3 指定的 warnings[]，另外加了两道反向封闭。
func assertLockWaitBuckets(t *testing.T, env Envelope, who string) {
	t.Helper()
	errs := errorDiagsOf(t, env)
	warns := warningDiagsOf(t, env)
	// ① 终态 E16 仍在 data.errors[]（error 级不许搬家）。
	if !diagsHaveCode(errs, txn.CodeLockTimeout) {
		t.Fatalf("%s：锁等待超时必须在 data.errors[] 里携 %s，实得 %+v", who, txn.CodeLockTimeout, errs)
	}
	// ② 等待期的 W28 留痕一条不少，但落点是信封 warnings[]。
	if !diagsHaveCode(warns, txn.CodeLockWaitRetry) {
		t.Fatalf("%s：等待期的 %s 退避留痕必须随失败一起交付在信封 warnings[]（合同 §3：warning 级只进这里），"+
			"实得 warnings=%+v / errors=%+v", who, txn.CodeLockWaitRetry, warns, errs)
	}
	// ③ 反向封闭：warning 级不得回流 data.errors[]。
	if diagsHaveCode(errs, txn.CodeLockWaitRetry) {
		t.Fatalf("%s：%s 是 level=warning，不得出现在 data.errors[]，实得 %+v", who, txn.CodeLockWaitRetry, errs)
	}
	for _, d := range errs {
		if d.Level != LevelError {
			t.Fatalf("%s：data.errors[] 只承载 error 级，实得 %+v", who, d)
		}
	}
	// ④ 双侧等号：失败态的 data.errors[] 恰一条（终态 E16），不许把进度留痕混进来。
	if len(errs) != 1 {
		t.Fatalf("%s：data.errors[] 应恰 1 条（终态 %s），实得 %d 条 %+v", who, txn.CodeLockTimeout, len(errs), errs)
	}
	for _, d := range warns {
		if d.Level == LevelError {
			t.Fatalf("%s：warnings[] 不得承载 error 级诊断，实得 %+v", who, d)
		}
	}
}

// TestPlanTxnLockWaitUsesRealClockNotFixedBusinessClock：
// **业务时钟被钉成常量 + 锁被别人占住 ⇒ 必须在有限时间内退 E16，绝不挂死。**
//
// 这条盯的是一个会让命令**永不返回**的 P0。Acquire 的超时判据是
// `now().Sub(start) >= timeout`：一旦把 `Root.Now` 这种**业务时钟**喂给它，
// 而 Root.Now 又常被钉成常量（stamp / intent.started_at 需要可复算的确定值 ——
// 本仓每一个 CLI 用例都这么做），`waited` 就恒等于 0，`waited >= timeout` 永不成立。
// 此时 backoff 的每一次 `remain = timeout - 0` 仍是正数，于是循环**无限**转下去：
// 表现不是「退 E16」，而是整条命令连同调用它的 CI 一起挂住。
//
// 因此本用例刻意把三件事同时摆齐：
//  1. Root.Now 固定为常量（与其余 CLI 用例逐字同一口径）；
//  2. 锁被**另一个 fd** 真实占住（flock 按 OFD 生效，同进程另开一次 open 照样冲突）；
//  3. EG_LOCK_TIMEOUT_MS 调到很小 —— 修好之后应当**很快**返回。
//
// 断言分两层：外层用 select + 计时器兜住「有没有挂死」（挂死时用例超时失败而不是卡住整包），
// 内层核对退出码非零、E16 与 W28 留痕俱在、且权威内容与 Git 一个字节都没动。
func TestPlanTxnLockWaitUsesRealClockNotFixedBusinessClock(t *testing.T) {
	dir := applyVault(t)
	before := authoritySnapshot(t, dir)
	logBefore := gitLogCount(t, dir)
	base := txnIDsOn(t, dir)

	// 很小的等待上限：修复后应在百毫秒量级返回；未修复则无论多小都不会返回。
	t.Setenv(txn.LockTimeoutEnv, "80")

	// 外部持锁者：另开一个 fd 真实握住 flock，命令必然撞上 EWOULDBLOCK 走等待分支。
	held, err := txn.Acquire(dir, txn.LockOptions{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("外部持锁失败，用例前提不成立：%v", err)
	}
	defer func() { _ = held.Release() }()

	setGitIdentity(t)
	r := newTestRoot(t, dir)
	r.In = strings.NewReader(notePlan())
	// **核心前提**：业务时钟钉死。把它传给 Acquire 就会造成无限等待。
	fixed := captureAt(t)
	r.Now = func() time.Time { return fixed }

	type runOut struct {
		code   int
		stdout string
		stderr string
	}
	done := make(chan runOut, 1)
	started := time.Now()
	go func() {
		var out, errOut bytes.Buffer
		code := r.Run([]string{"apply", "--vault", dir, "--json", "--plan", "-"}, &out, &errOut)
		done <- runOut{code: code, stdout: out.String(), stderr: errOut.String()}
	}()

	var got runOut
	select {
	case got = <-done:
	case <-time.After(20 * time.Second):
		t.Fatalf("锁被占用时命令在 20s 内没有返回：等待上限只有 %s=80ms，说明 Acquire 拿到的是"+
			"被钉死的业务时钟（elapsed 恒为 0 ⇒ 超时永不成立 ⇒ 无限退避）", txn.LockTimeoutEnv)
	}
	elapsed := time.Since(started)

	// ① 有限时间内返回，且量级与配置的等待上限相称（留足 CI 抖动余量）。
	if elapsed > 10*time.Second {
		t.Fatalf("等待上限 80ms，实际耗时 %s：锁等待没有走真实墙钟", elapsed)
	}
	// ② 退出码非零（TxnBlockedError 未分类，当前落 1；退 5 的启用属 T-…-074）。
	if got.code == ExitOK {
		t.Fatalf("锁被占用必须阻断，实得退出码 0：%s", got.stdout)
	}

	var env Envelope
	if strings.TrimSpace(got.stdout) != "" {
		if err := json.Unmarshal([]byte(got.stdout), &env); err != nil {
			t.Fatalf("--json 输出不可解析：%v\n%s", err, got.stdout)
		}
	}
	// ③ E16 如实上报在 data.errors[]，等待期的 W28 一条不少地落在信封 warnings[]（I-…-016）。
	assertLockWaitBuckets(t, env, "plan 事务锁等待")

	// ④ 零权威写、零事务、零 Git：连锁都没拿到，后面每一格都不该发生。
	assertAuthorityUnchanged(t, dir, before, "锁被占用")
	assertNoNewTxn(t, dir, base, "锁被占用")
	if got := gitLogCount(t, dir); got != logBefore {
		t.Fatalf("没拿到锁不得产生 commit：%d → %d", logBefore, got)
	}
}
