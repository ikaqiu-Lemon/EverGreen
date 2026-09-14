package index_test

// T-…-072 批次 B2b2 的机器判据：`.index/` 上**两条方向相反**的删除策略。
//
// 判据来源：M6 合同 §16.5（R6 P0-3：Rebuild 不得连同运行时保留条目一起清空）+
// R8 §18.2（build 失败只回收本次产物，不替用户打扫）+ R9（`os.RemoveAll(.index)` 禁令）。
//
// 两条策略的**判据方向相反**，因此必须分别对撞，不能只测「删干净了」或只测「没删」：
//
//	Rebuild ⇒ purgeNonReserved       凭**名册**删：除 run.lock / txn/ 外一切归零，
//	                                 包含 eg.db 三件套、遗留 tmp、非空的意外子目录；
//	                                 run.lock 连 **inode** 都不许换，txn/ 全树逐字不动。
//	Build 失败 ⇒ cleanupBuildAttempt 凭**因果**删：只回收本次调用亲手造的路径；
//	                                 调用前已有的污染与保留条目一律原样留下。
//	                                 目录本身只在「本次建的 + 现在空的」双条件下才收走。
//
// 本文件不碰 CLI，也不改历史用例（TestRebuildRemovesPollution /
// TestBuildRejectsUnknownSkippedKind 的文件与断言原样保留，且必须继续绿）。

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/index"
)

// —— 策略一：Rebuild 凭名册删 ——

// TestIndexRebuildPreservesTxnAndLockInode 反证重建的删除面**恰好**是「非保留条目」。
//
// 现场刻意做满：健康库（eg.db 三件套）+ 遗留 tmp + 非空的意外子目录 +
// 合法 run.lock + 含 seq / 未闭合事务（intent.json + pre/）/ quarantine 的 txn 全树。
// 重建之后：污染与旧库必须一个不剩，保留条目必须一格未动 —— 锁的 dev+ino 与
// txn/ 全树（名字 / 类型 / 字节）逐字相同。
func TestIndexRebuildPreservesTxnAndLockInode(t *testing.T) {
	dir := buildFixture(t, sampleSnapshot())
	writeLegalRuntimeEntries(t, dir)
	mkTxnQuarantine(t, dir)
	pollution := writePollution(t, dir)

	// 前置自检：这些东西真的都在盘上（否则后面的「删干净了」是空断言）。
	assertLegalRuntimeEntriesOnDisk(t, dir)
	assertExists(t, filepath.Join(dir, index.DBFileName), "重建前 eg.db 应在盘")
	for _, p := range pollution {
		assertExists(t, p, "重建前污染应在盘")
	}

	lockPath := filepath.Join(dir, "run.lock")
	lockBefore := lstatOrFatal(t, lockPath)
	txnPath := filepath.Join(dir, "txn")
	txnBefore := reservedTreeSnapshot(t, txnPath)
	lockBytesBefore := readOrFatal(t, lockPath)

	if _, err := index.Rebuild(dir, sampleSnapshot(), fixedOptions()); err != nil {
		t.Fatalf("Rebuild 失败：%v", err)
	}

	// ① 非保留条目全清：遗留 tmp、非空意外子目录都不得残留。
	for _, p := range pollution {
		if _, err := os.Lstat(p); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("重建后 %s 仍在（purge 未覆盖非保留条目），lstat = %v", p, err)
		}
	}
	// ② 索引确实被重建出来且自洽（反向自检：不是「什么都没干」）。
	if diag := index.Inspect(dir); !diag.Usable() {
		t.Fatalf("重建后应 healthy，实际 = %s / %s / %s", diag.Health, diag.Reason, diag.Message)
	}
	// ③ 锁**不是**被删了重建：dev+ino 必须是同一个 inode（os.SameFile 恰是这个口径）。
	//    只比内容是不够的 —— 「删掉再写一个同样内容的新文件」会让互斥当场失效，
	//    而两个进程各自锁住一个不同 inode 时，双方都以为自己拿到了锁。
	lockAfter := lstatOrFatal(t, lockPath)
	if !os.SameFile(lockBefore, lockAfter) {
		t.Fatalf("run.lock 的 inode 被换掉了：重建必须保住同一把锁（同一个 dev+ino）")
	}
	if got := readOrFatal(t, lockPath); got != lockBytesBefore {
		t.Fatalf("run.lock 内容被改写：%q → %q", lockBytesBefore, got)
	}
	// ④ txn/ 全树逐字不变：seq、未闭合事务的 intent.json 与 pre/、quarantine 一格未动。
	assertTreeUnchanged(t, txnBefore, reservedTreeSnapshot(t, txnPath))
}

// TestIndexRebuildKeepsReservedEvenWhenTypeIsIllegal 反证 purge 的判定**只看名字**：
// 一把变成了目录的 `run.lock` 是需要人来看的现场，重建不得顺手把它删掉 ——
// 删了之后，Inspect 那条 W24 / unexpected_file 诊断会在下一次体检时凭空消失。
func TestIndexRebuildKeepsReservedEvenWhenTypeIsIllegal(t *testing.T) {
	dir := filepath.Join(t.TempDir(), index.DirName)
	if err := os.MkdirAll(filepath.Join(dir, "run.lock", "inner"), 0o755); err != nil {
		t.Fatalf("造反类型 run.lock 失败：%v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "txn"), []byte("不是目录\n"), 0o644); err != nil {
		t.Fatalf("造反类型 txn 失败：%v", err)
	}
	before := reservedTreeSnapshot(t, dir)

	if _, err := index.Rebuild(dir, sampleSnapshot(), fixedOptions()); err != nil {
		t.Fatalf("Rebuild 失败：%v", err)
	}

	// 两个违规条目原样还在（含 run.lock/ 里那层子目录）。
	for rel, want := range before {
		got := reservedTreeSnapshot(t, dir)[rel]
		if got != want {
			t.Fatalf("类型违规的保留条目 %s 被动过了：%q → %q", rel, want, got)
		}
	}
	// 体检仍能看见这个现场（诊断没有被删除动作抹掉）。
	if diag := index.Inspect(dir); diag.Reason != index.ReasonUnexpectedFile ||
		diag.Code != index.CodeIndexCorrupt {
		t.Fatalf("重建后仍应能诊断出保留条目类型违规，实际 = %s / %s", diag.Code, diag.Reason)
	}
}

// TestIndexRebuildHealsNonDirectoryIndexPath 钉住「`.index` 位置被换成普通文件」这一支：
// 那是目录位置上的污染（它连保留条目都装不下），purge 就地删掉这一个条目再重建。
// 这条语义是 e2e `m5_index_corrupt_rebuild.sh` 第 4a 步（action=repaired）的包内对应物。
func TestIndexRebuildHealsNonDirectoryIndexPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), index.DirName)
	if err := os.WriteFile(dir, []byte("not a dir\n"), 0o644); err != nil {
		t.Fatalf("把 .index 造成普通文件失败：%v", err)
	}
	if _, err := index.Rebuild(dir, sampleSnapshot(), fixedOptions()); err != nil {
		t.Fatalf("Rebuild 失败：%v", err)
	}
	if diag := index.Inspect(dir); !diag.Usable() {
		t.Fatalf("自愈后应 healthy，实际 = %s / %s", diag.Health, diag.Message)
	}
}

// —— 策略二：Build 失败凭因果删 ——

// TestIndexBuildFailurePreservesTxnAndLock 反证失败清理的删除面**恰好**是「本次产物」。
//
//	A 预存现场：`.index/` 与污染、保留条目都在 —— 失败后它们一格未动，
//	           本次造出来的 eg.db 三件套被清掉，目录**仍在**（它不归本次调用处置）。
//	B 空手起步：`.index/` 调用前不存在 —— 失败后目录里空无一物，于是被非递归收走。
func TestIndexBuildFailurePreservesTxnAndLock(t *testing.T) {
	t.Run("A_预存目录与污染保留条目全部原样", testBuildFailureKeepsPreexisting)
	t.Run("B_本次新建的空目录被收走", testBuildFailureRemovesOwnEmptyDir)
}

// badSnapshot 返回一份**必定构建失败**的快照：skipped.kind 不在封闭取值域内。
//
// 失败点刻意选在「库已经建出来之后」—— 这样 eg.db（及可能的 -wal / -shm）确实已落盘，
// 清理面才有东西可测；若失败发生在开库之前，本用例会退化成空断言。
func badSnapshot() index.Snapshot {
	snap := sampleSnapshot()
	snap.Skipped = []index.SkippedFile{{Path: "a.md", Kind: "brand_new_kind"}}
	return snap
}

func testBuildFailureKeepsPreexisting(t *testing.T) {
	dir := filepath.Join(t.TempDir(), index.DirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("预建 .index 失败：%v", err)
	}
	writeLegalRuntimeEntries(t, dir)
	mkTxnQuarantine(t, dir)
	pollution := writePollution(t, dir)

	lockPath := filepath.Join(dir, "run.lock")
	lockBefore := lstatOrFatal(t, lockPath)
	before := reservedTreeSnapshot(t, dir)

	if _, err := index.Build(dir, badSnapshot(), fixedOptions()); err == nil {
		t.Fatalf("非法 skipped.kind 应导致构建失败")
	}

	// ① 目录仍在：它是调用前就有的，不归这次失败处置。
	st, err := os.Lstat(dir)
	if err != nil || !st.IsDir() {
		t.Fatalf("预存的 .index 目录被失败清理删掉了：err = %v", err)
	}
	// ② 本次产物清零：eg.db 三件套一个都不许留（否则下一次 build 会撞 ErrIndexExists）。
	for _, name := range index.AllowedFiles() {
		p := filepath.Join(dir, name)
		if _, err := os.Lstat(p); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("失败后本次产物 %s 仍在：lstat = %v", name, err)
		}
	}
	// ③ 预存污染原样保留：一次失败的构建没有资格替用户打扫他自己的目录。
	for _, p := range pollution {
		assertExists(t, p, "预存污染应被失败清理原样保留")
	}
	// ④ 保留条目一格未动，且锁仍是同一个 inode。
	assertTreeUnchanged(t, before, reservedTreeSnapshot(t, dir))
	if !os.SameFile(lockBefore, lstatOrFatal(t, lockPath)) {
		t.Fatalf("run.lock 的 inode 被换掉了：失败清理不得触碰运行时保留条目")
	}

	// ⑤ 清理之后必须能从同一起点重来（这才是「不留半成品」的意义）。
	if _, err := index.Build(dir, sampleSnapshot(), fixedOptions()); err != nil {
		t.Fatalf("清理后重建失败：%v", err)
	}
	if diag := index.Inspect(dir); diag.Reason != index.ReasonUnexpectedFile {
		// 污染仍在，因此体检必然报 unexpected_file —— 这正是「没替用户打扫」的正面证据。
		t.Fatalf("污染应仍被判 unexpected_file，实际 = %s / %s", diag.Code, diag.Reason)
	}

	// ⑥ ErrIndexExists 早退 ⇒ **零删除**：整棵树前后逐字相同。
	snapshotBefore := reservedTreeSnapshot(t, dir)
	_, err = index.Build(dir, sampleSnapshot(), fixedOptions())
	if !errors.Is(err, index.ErrIndexExists) {
		t.Fatalf("eg.db 已存在时应返回 ErrIndexExists，实际 = %v", err)
	}
	assertTreeUnchanged(t, snapshotBefore, reservedTreeSnapshot(t, dir))
}

func testBuildFailureRemovesOwnEmptyDir(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, index.DirName)
	if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("前置不成立：.index 调用前不应存在，lstat = %v", err)
	}

	if _, err := index.Build(dir, badSnapshot(), fixedOptions()); err == nil {
		t.Fatalf("非法 skipped.kind 应导致构建失败")
	}

	// 本次建的目录 + 本次建的产物 = 全部由本次收走，父目录一格未动。
	if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
		leftovers, _ := os.ReadDir(dir)
		t.Fatalf("本次新建的空 .index 应已被非递归收走，实际仍在（残留 %d 项）", len(leftovers))
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("读父目录失败：%v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("父目录应保持空，实际有 %d 项", len(entries))
	}
}

// —— 本文件专用脚手架（reserved_test.go 已有的 run.lock / txn 造场与快照工具直接复用）——

// mkTxnQuarantine 在 txn/ 下补一个非空的 quarantine 目录（隔离区同样属于「全树保留」）。
func mkTxnQuarantine(t *testing.T, dir string) {
	t.Helper()
	q := filepath.Join(dir, "txn", "quarantine")
	if err := os.MkdirAll(q, 0o755); err != nil {
		t.Fatalf("造 quarantine 失败：%v", err)
	}
	if err := os.WriteFile(filepath.Join(q, "t0000000000000000.json"),
		[]byte(`{"reason":"conflict"}`), 0o644); err != nil {
		t.Fatalf("写 quarantine 记录失败：%v", err)
	}
}

// writePollution 在 dir 下铺两类非保留污染：遗留 tmp 文件 + **非空**的意外子目录。
// 非空这点很关键：它逼着 purge 走「先试非递归、确认是目录再整棵删」那条路。
func writePollution(t *testing.T, dir string) []string {
	t.Helper()
	tmp := filepath.Join(dir, "leftover.tmp")
	if err := os.WriteFile(tmp, []byte("垃圾"), 0o644); err != nil {
		t.Fatalf("写遗留 tmp 失败：%v", err)
	}
	strayLeaf := filepath.Join(dir, "stray.d", "nested")
	if err := os.MkdirAll(strayLeaf, 0o755); err != nil {
		t.Fatalf("造意外子目录失败：%v", err)
	}
	if err := os.WriteFile(filepath.Join(strayLeaf, "x.bin"), []byte("\x00\x01"), 0o644); err != nil {
		t.Fatalf("写意外子目录内文件失败：%v", err)
	}
	return []string{tmp, filepath.Join(dir, "stray.d")}
}

func assertExists(t *testing.T, path, why string) {
	t.Helper()
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("%s：%s 不在盘（%v）", why, path, err)
	}
}

func lstatOrFatal(t *testing.T, path string) os.FileInfo {
	t.Helper()
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat %s 失败：%v", path, err)
	}
	return fi
}

func readOrFatal(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 %s 失败：%v", path, err)
	}
	return string(data)
}
