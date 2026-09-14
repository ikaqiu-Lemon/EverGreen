package txn

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"
)

// TestLockMutualExclusion：同一 vault 上持锁期间，第二次 Acquire 必须超时失败；释放后可再取。
func TestLockMutualExclusion(t *testing.T) {
	vault := newVault(t)

	l1, err := Acquire(vault, LockOptions{Timeout: time.Second})
	if err != nil {
		t.Fatalf("首个 Acquire 应成功，得 %v", err)
	}

	l2, err := Acquire(vault, LockOptions{Timeout: 120 * time.Millisecond})
	if err == nil {
		_ = l2.Release()
		t.Fatal("持锁期间第二次 Acquire 应超时失败，却成功了（互斥被破坏）")
	}
	var te *LockTimeoutError
	if !errors.As(err, &te) {
		t.Fatalf("应得 *LockTimeoutError，得 %T：%v", err, err)
	}

	if err := l1.Release(); err != nil {
		t.Fatalf("释放失败：%v", err)
	}
	l3, err := Acquire(vault, LockOptions{Timeout: time.Second})
	if err != nil {
		t.Fatalf("释放后应能再取锁，得 %v", err)
	}
	_ = l3.Release()
}

// TestLockTimeoutReturnsE16：超时错误必须携 E16，且诊断里含等待期的 W28 留痕。
func TestLockTimeoutReturnsE16(t *testing.T) {
	vault := newVault(t)
	l1, err := Acquire(vault, LockOptions{})
	if err != nil {
		t.Fatalf("首个 Acquire 应成功：%v", err)
	}
	defer l1.Release()

	_, err = Acquire(vault, LockOptions{Timeout: 150 * time.Millisecond})
	if err == nil {
		t.Fatal("应超时失败")
	}
	if !errors.Is(err, ErrLockUnavailable) {
		t.Fatalf("应可 errors.Is 到 ErrLockUnavailable，得 %v", err)
	}
	var c coded
	if !errors.As(err, &c) {
		t.Fatalf("超时错误应实现 coded，得 %T", err)
	}
	if c.Code() != CodeLockTimeout {
		t.Fatalf("码应为 %s，得 %s", CodeLockTimeout, c.Code())
	}
	var e16, w28 int
	for _, d := range c.Diagnostics() {
		switch d.Code {
		case CodeLockTimeout:
			e16++
		case CodeLockWaitRetry:
			w28++
		}
	}
	if e16 != 1 {
		t.Fatalf("应恰有 1 条 E16，得 %d", e16)
	}
	if w28 < 1 {
		t.Fatalf("等待期应至少留 1 条 W28，得 %d", w28)
	}
}

// TestStaleLockTakeoverSafe：run.lock 已存在且带陈旧持有者正文但无活跃 flock 时，
// Acquire 必须成功且**原地复用同一 inode**（不新建、不换 inode）。
func TestStaleLockTakeoverSafe(t *testing.T) {
	vault := newVault(t)
	indexDir := filepath.Join(vault, IndexDirName)
	if err := os.MkdirAll(indexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(indexDir, LockFileName)
	writeRaw(t, lockPath, []byte(`{"pid":999999,"acquired_at":"2020-01-01T00:00:00Z","argv":["old"]}`+"\n"))
	dev0, ino0 := statDevIno(t, lockPath)

	l, err := Acquire(vault, LockOptions{})
	if err != nil {
		t.Fatalf("陈旧锁应可安全接管，得 %v", err)
	}
	defer l.Release()

	dev1, ino1 := statDevIno(t, lockPath)
	if dev0 != dev1 || ino0 != ino1 {
		t.Fatalf("接管必须复用同一 inode：(%d,%d) → (%d,%d)", dev0, ino0, dev1, ino1)
	}
	h, ok := ReadHolder(vault)
	if !ok || h.PID != os.Getpid() {
		t.Fatalf("正文应被本进程原地覆盖，得 ok=%v holder=%+v", ok, h)
	}
}

// TestCriticalSectionCoversWriteOnly：锁在 WithLock 临界区内被持有，
// 临界区之前与之后均不持锁（S0 提示与释放后不占锁）。
func TestCriticalSectionCoversWriteOnly(t *testing.T) {
	vault := newVault(t)

	// 之前：锁空闲。
	pre, err := Acquire(vault, LockOptions{Timeout: 300 * time.Millisecond})
	if err != nil {
		t.Fatalf("临界区之前锁应空闲：%v", err)
	}
	_ = pre.Release()

	// 期间：并发 Acquire 必须失败（证明临界区确实持锁）。
	err = WithLock(vault, LockOptions{}, func(_ *Lock) error {
		l2, aerr := Acquire(vault, LockOptions{Timeout: 120 * time.Millisecond})
		if aerr == nil {
			_ = l2.Release()
			return errors.New("临界区内锁未被持有")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithLock 临界区断言失败：%v", err)
	}

	// 之后：锁已释放。
	post, err := Acquire(vault, LockOptions{Timeout: 300 * time.Millisecond})
	if err != nil {
		t.Fatalf("临界区之后锁应已释放：%v", err)
	}
	_ = post.Release()
}

// TestLockHolderInfoRecorded：正文恰记 pid/acquired_at/argv，txn_id 初始缺席、补写后可读。
func TestLockHolderInfoRecorded(t *testing.T) {
	vault := newVault(t)
	argv := []string{"eg", "add", "note"}
	l, err := Acquire(vault, LockOptions{Argv: argv})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()

	h := l.Holder()
	if h.PID != os.Getpid() {
		t.Fatalf("pid 应为 %d，得 %d", os.Getpid(), h.PID)
	}
	if !reflect.DeepEqual(h.Argv, argv) {
		t.Fatalf("argv 应为 %v，得 %v", argv, h.Argv)
	}
	if _, perr := time.Parse(time.RFC3339, h.AcquiredAt); perr != nil {
		t.Fatalf("acquired_at 应为 RFC3339，得 %q（%v）", h.AcquiredAt, perr)
	}
	if h.TxnID != "" {
		t.Fatalf("初始 txn_id 应缺席，得 %q", h.TxnID)
	}

	want := FormatTxnID(1)
	if err := l.RecordTxnID(want); err != nil {
		t.Fatalf("补写 txn_id 失败：%v", err)
	}
	hr, ok := ReadHolder(vault)
	if !ok || hr.TxnID != want {
		t.Fatalf("补写后应能读到 txn_id=%q，得 ok=%v holder=%+v", want, ok, hr)
	}
}

// TestLockReleasedOnPanic：临界区 panic 也必须释放锁（defer 释放，panic 继续上抛）。
func TestLockReleasedOnPanic(t *testing.T) {
	vault := newVault(t)

	func() {
		defer func() { _ = recover() }()
		_ = WithLock(vault, LockOptions{}, func(_ *Lock) error {
			panic("boom in critical section")
		})
	}()

	l, err := Acquire(vault, LockOptions{Timeout: 500 * time.Millisecond})
	if err != nil {
		t.Fatalf("panic 后锁应已释放，却仍不可取：%v", err)
	}
	_ = l.Release()
}

// TestLockInodeNeverReplacedWhileHeld：持锁期间无论写正文 / 补写 txn_id，
// 路径 inode 与 fd inode 四次取值完全一致（永不 tmp+rename / unlink / 重建）。
func TestLockInodeNeverReplacedWhileHeld(t *testing.T) {
	vault := newVault(t)
	l, err := Acquire(vault, LockOptions{Argv: []string{"eg"}})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	lockPath := LockPath(vault)

	d0, i0 := statDevIno(t, lockPath)
	if err := l.RecordTxnID(FormatTxnID(1)); err != nil {
		t.Fatal(err)
	}
	d1, i1 := statDevIno(t, lockPath)
	if err := l.RecordTxnID(FormatTxnID(2)); err != nil {
		t.Fatal(err)
	}
	d2, i2 := statDevIno(t, lockPath)

	var fst syscall.Stat_t
	if err := syscall.Fstat(int(l.file.Fd()), &fst); err != nil {
		t.Fatalf("fstat 已加锁 fd 失败：%v", err)
	}

	if !(i0 == i1 && i1 == i2 && i2 == fst.Ino && d0 == d1 && d1 == d2) {
		t.Fatalf("持锁期 inode 被替换：path=(%d,%d)/(%d,%d)/(%d,%d) fd_ino=%d",
			d0, i0, d1, i1, d2, i2, fst.Ino)
	}
}

// TestInodeReplacementCannotYieldSecondLock：先证「rename 换 inode ⇒ 能拿到第二把锁」
// 这一反面事实，再证正确实现（inode 恒定）下第二次 flock 必然失败。
func TestInodeReplacementCannotYieldSecondLock(t *testing.T) {
	// 反证：手工 rename 替换 inode，旧 fd 的 flock 拦不住新 inode 上的 flock。
	t.Run("rename_replacement_yields_double_lock", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "run.lock")
		fd1, err := syscall.Open(p, syscall.O_RDWR|syscall.O_CREAT, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		defer syscall.Close(fd1)
		if err := syscall.Flock(fd1, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			t.Fatalf("首把 flock 应成功：%v", err)
		}
		// tmp + rename 换掉 inode。
		tmp := p + ".tmp"
		fdt, err := syscall.Open(tmp, syscall.O_RDWR|syscall.O_CREAT, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		_ = syscall.Close(fdt)
		if err := os.Rename(tmp, p); err != nil {
			t.Fatal(err)
		}
		fd2, err := syscall.Open(p, syscall.O_RDWR, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		defer syscall.Close(fd2)
		if err := syscall.Flock(fd2, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			t.Fatalf("换 inode 后第二把 flock 本应成功（这正是禁止 rename 的原因）：%v", err)
		}
	})

	// 正证：Acquire 恒定 inode，持锁期间再开同一路径 flock 必失败。
	t.Run("stable_inode_blocks_second_lock", func(t *testing.T) {
		vault := newVault(t)
		l, err := Acquire(vault, LockOptions{})
		if err != nil {
			t.Fatal(err)
		}
		defer l.Release()
		fd, err := syscall.Open(LockPath(vault), syscall.O_RDWR, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		defer syscall.Close(fd)
		ferr := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if ferr == nil {
			t.Fatal("持锁期间第二把 flock 应失败（EWOULDBLOCK）")
		}
		if !errors.Is(ferr, syscall.EWOULDBLOCK) && !errors.Is(ferr, syscall.EAGAIN) {
			t.Fatalf("应为 EWOULDBLOCK/EAGAIN，得 %v", ferr)
		}
	})
}

// TestLockBodyToleratesCorruptContent：正文为空 / 半写 / 非 JSON 时互斥不受影响，
// Acquire 仍成功且不产诊断码；损坏正文经 ReadHolder 返回 ok=false。
func TestLockBodyToleratesCorruptContent(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{"empty", []byte("")},
		{"half_json", []byte("{not valid json")},
		{"binary", []byte("\x00\x01\x02\x03")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vault := newVault(t)
			indexDir := filepath.Join(vault, IndexDirName)
			if err := os.MkdirAll(indexDir, 0o755); err != nil {
				t.Fatal(err)
			}
			lockPath := filepath.Join(indexDir, LockFileName)
			writeRaw(t, lockPath, tc.body)

			if _, ok := ReadHolder(vault); ok {
				t.Fatal("损坏正文的 ReadHolder 应返回 ok=false")
			}
			l, err := Acquire(vault, LockOptions{})
			if err != nil {
				t.Fatalf("正文损坏不应妨碍取锁，得 %v", err)
			}
			for _, d := range l.Diagnostics() {
				if d.Code == CodeLockTimeout {
					t.Fatalf("正文损坏不应产 E16：%+v", d)
				}
			}
			_ = l.Release()
		})
	}
}

// TestLockUsableWithoutTxnAllocation：取锁与 txn 分配解耦——只取锁不开事务时，
// .index/txn 不应被创建（B 类 index 维护命令的形态）。
func TestLockUsableWithoutTxnAllocation(t *testing.T) {
	vault := newVault(t)
	l, err := Acquire(vault, LockOptions{Argv: []string{"eg", "index", "maintain"}})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()

	if _, err := os.Stat(TxnRootPath(vault)); !os.IsNotExist(err) {
		t.Fatalf("只取锁不应创建 .index/txn，stat 得 err=%v", err)
	}
}

// TestAcquireRejectsSymlinkWithoutTouchingTarget（P0）：run.lock 是指向 vault 外的 symlink 时，
// Acquire 必须以 E15 fail closed，且**绝不触碰** symlink 目标字节、也不把 run.lock 变成普通文件。
func TestAcquireRejectsSymlinkWithoutTouchingTarget(t *testing.T) {
	vault := newVault(t)
	externalDir := t.TempDir()
	external := filepath.Join(externalDir, "victim")
	const secret = "DO-NOT-TOUCH"
	writeRaw(t, external, []byte(secret))

	indexDir := filepath.Join(vault, IndexDirName)
	if err := os.MkdirAll(indexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(indexDir, LockFileName)
	if err := os.Symlink(external, lockPath); err != nil {
		t.Fatal(err)
	}

	_, err := Acquire(vault, LockOptions{})
	if err == nil {
		t.Fatal("run.lock 为 symlink 时 Acquire 必须失败")
	}
	if !errors.Is(err, ErrLockPathUnsafe) {
		t.Fatalf("应可 errors.Is 到 ErrLockPathUnsafe，得 %v", err)
	}
	var pe *LockPathError
	if !errors.As(err, &pe) {
		t.Fatalf("应为 *LockPathError，得 %T", err)
	}
	if pe.Code() != CodePrecheckFailed {
		t.Fatalf("码应为 %s，得 %s", CodePrecheckFailed, pe.Code())
	}
	// 目标字节零触碰。
	if got := string(mustReadFile(t, external)); got != secret {
		t.Fatalf("symlink 目标被改写：%q → %q", secret, got)
	}
	// run.lock 仍是 symlink（未被替换成普通文件）。
	fi, lerr := os.Lstat(lockPath)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("run.lock 不应被改写成普通文件")
	}
}
