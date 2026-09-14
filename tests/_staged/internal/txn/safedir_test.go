package txn

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestRuntimeDirParentSymlinkFailsClosed（P0）：运行时目录**父组件**（.index / txn / pre）预置为
// 指向 vault 外的 symlink 时，写路径必须 fail closed——外部目标字节零变化、无任何事务落盘。
//
// 这正是「仅给最终文件加 O_NOFOLLOW 不够」的反证：叶子 O_NOFOLLOW 挡不住父组件逃逸，
// 必须由 openRuntimeDir 逐段 O_DIRECTORY|O_NOFOLLOW 行走拦下。
func TestRuntimeDirParentSymlinkFailsClosed(t *testing.T) {
	// external 是「攻击者希望被误写」的 vault 外目录，放一个哨兵文件盯字节。
	newExternal := func(t *testing.T) (dir, sentinel string) {
		t.Helper()
		dir = t.TempDir()
		sentinel = filepath.Join(dir, "sentinel")
		writeRaw(t, sentinel, []byte("EXTERNAL-INTACT"))
		return dir, sentinel
	}
	assertUntouched := func(t *testing.T, sentinel string) {
		t.Helper()
		if got := string(mustReadFile(t, sentinel)); got != "EXTERNAL-INTACT" {
			t.Fatalf("vault 外目标被写入：%q", got)
		}
	}

	t.Run("dot_index_symlink_blocks_acquire", func(t *testing.T) {
		vault := newVault(t)
		external, sentinel := newExternal(t)
		if err := os.Symlink(external, filepath.Join(vault, IndexDirName)); err != nil {
			t.Fatal(err)
		}
		_, err := Acquire(vault, LockOptions{})
		if !errors.Is(err, ErrUnsafeRuntimeDir) {
			t.Fatalf(".index 为 symlink 时 Acquire 应 fail closed（ErrUnsafeRuntimeDir），得 %v", err)
		}
		var c coded
		if !errors.As(err, &c) || c.Code() != CodePrecheckFailed {
			t.Fatalf("应携 E15，得 %T/%v", err, err)
		}
		// run.lock 绝不能被建到外部目录里。
		if _, statErr := os.Stat(filepath.Join(external, LockFileName)); !os.IsNotExist(statErr) {
			t.Fatalf("run.lock 不应落到 .index symlink 目标下：%v", statErr)
		}
		assertUntouched(t, sentinel)
	})

	t.Run("txn_symlink_blocks_allocate", func(t *testing.T) {
		vault := newVault(t)
		external, sentinel := newExternal(t)
		// .index 是真实目录，但 txn 被预置为指向外部的 symlink。
		if err := os.MkdirAll(filepath.Join(vault, IndexDirName), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(external, filepath.Join(vault, IndexDirName, TxnDirName)); err != nil {
			t.Fatal(err)
		}
		_, err := AllocateTxnID(vault)
		if !errors.Is(err, ErrUnsafeRuntimeDir) {
			t.Fatalf("txn 为 symlink 时分配应 fail closed，得 %v", err)
		}
		// 外部目录里不应冒出 seq 文件或事务目录。
		if _, statErr := os.Stat(filepath.Join(external, SeqFileName)); !os.IsNotExist(statErr) {
			t.Fatalf("seq 不应落到 txn symlink 目标下：%v", statErr)
		}
		entries, _ := os.ReadDir(external)
		for _, e := range entries {
			if ValidTxnID(e.Name()) {
				t.Fatalf("事务目录 %s 落到了外部目录", e.Name())
			}
		}
		assertUntouched(t, sentinel)
	})

	t.Run("pre_symlink_blocks_write_intent", func(t *testing.T) {
		vault := newVault(t)
		external, sentinel := newExternal(t)
		// 先合法分配一个事务目录，再把它的 pre/ 预置成指向外部的 symlink。
		id, err := AllocateTxnID(vault)
		if err != nil {
			t.Fatal(err)
		}
		dir := TxnDirPath(vault, id)
		if err := os.Symlink(external, filepath.Join(dir, PreDirName)); err != nil {
			t.Fatal(err)
		}
		// 用 create:true 的文件（无 pre_bytes_ref，避开 ValidateIntentPaths 的前像路径校验），
		// 以隔离 openChildDir(pre) 的 parent-symlink 防线：WriteIntent 仍会**无条件**安全打开 pre/。
		_, err = WriteIntent(vault, id, IntentInput{
			Files: []FileSpec{{Path: "a.md", Create: true, TargetBytes: []byte("t"), TargetOp: "create"}},
		})
		if !errors.Is(err, ErrUnsafeRuntimeDir) {
			t.Fatalf("pre/ 为 symlink 时 WriteIntent 应 fail closed，得 %v", err)
		}
		// 前像副本绝不能被写进外部目录，intent.json 也不应发布。
		if _, statErr := os.Stat(filepath.Join(external, "0")); !os.IsNotExist(statErr) {
			t.Fatalf("pre/0 不应落到 pre symlink 目标下：%v", statErr)
		}
		if _, statErr := os.Stat(filepath.Join(dir, IntentFileName)); !os.IsNotExist(statErr) {
			t.Fatalf("pre/ 逃逸时 intent.json 不应发布：%v", statErr)
		}
		assertUntouched(t, sentinel)
	})
}

// TestScanAndPruneParentSymlinkFailsClosed（P0，读 / 删路径）：当 .index/txn 被预置为指向 vault 外的
// symlink 时，Scan（读）与 Prune（删）都必须 fail closed——既不顺着 symlink 列举 / 分类外部目录，
// 也绝不删除 vault 外的任何内容。这是把安全 dirfd 语义从写路径延伸到读 / 删路径的反证。
func TestScanAndPruneParentSymlinkFailsClosed(t *testing.T) {
	setup := func(t *testing.T) (vault, external, sentinel string) {
		t.Helper()
		vault = newVault(t)
		external = t.TempDir()
		sentinel = filepath.Join(external, "sentinel")
		writeRaw(t, sentinel, []byte("EXTERNAL-INTACT"))
		// 在外部目录里放一个"看似可被 Prune 清理"的 residue 事务目录（intent.json 缺席）。
		writeRaw(t, filepath.Join(external, FormatTxnID(1), PreDirName, "0"), []byte("orphan"))
		// .index 是真实目录，但 txn 被预置为指向 external 的 symlink。
		if err := os.MkdirAll(filepath.Join(vault, IndexDirName), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(external, filepath.Join(vault, IndexDirName, TxnDirName)); err != nil {
			t.Fatal(err)
		}
		return vault, external, sentinel
	}
	assertIntact := func(t *testing.T, external, sentinel string) {
		t.Helper()
		if got := string(mustReadFile(t, sentinel)); got != "EXTERNAL-INTACT" {
			t.Fatalf("vault 外哨兵被改动：%q", got)
		}
		if _, err := os.Stat(filepath.Join(external, FormatTxnID(1))); err != nil {
			t.Fatalf("vault 外事务目录被删除 / 触碰：%v", err)
		}
	}

	t.Run("scan_fails_closed", func(t *testing.T) {
		vault, external, sentinel := setup(t)
		if _, err := Scan(vault); !errors.Is(err, ErrUnsafeRuntimeDir) {
			t.Fatalf("txn 为 symlink 时 Scan 应 fail closed（ErrUnsafeRuntimeDir），得 %T：%v", err, err)
		}
		assertIntact(t, external, sentinel)
	})

	t.Run("prune_fails_closed", func(t *testing.T) {
		vault, external, sentinel := setup(t)
		if _, err := Prune(vault, PruneOptions{}); !errors.Is(err, ErrUnsafeRuntimeDir) {
			t.Fatalf("txn 为 symlink 时 Prune 应 fail closed（ErrUnsafeRuntimeDir），得 %T：%v", err, err)
		}
		assertIntact(t, external, sentinel)
	})
}
