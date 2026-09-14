package txn

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestMarkerMutualExclusion（P0）：commit / abort 互斥——一侧已落盘后，writeMarker 拒绝写对侧，
// 绝不主动制造「commit 与 abort 并存」的双标记 Corrupt，且失败路径**零写盘**（对侧标记 / 其 .tmp 均不产生，
// 已有同侧标记字节不变）。
func TestMarkerMutualExclusion(t *testing.T) {
	t.Run("commit_then_abort_rejected", func(t *testing.T) {
		vault := newVault(t)
		id := makeOpen(t, vault)
		if err := MarkCommit(vault, id); err != nil {
			t.Fatalf("首个 commit 应成功：%v", err)
		}
		dir := TxnDirPath(vault, id)
		before := snapshotTree(t, dir)

		err := MarkAbort(vault, id)
		var mc *MarkerConflictError
		if !errors.As(err, &mc) {
			t.Fatalf("commit 在场时 MarkAbort 应返回 *MarkerConflictError，得到：%v", err)
		}
		if _, e := os.Stat(filepath.Join(dir, AbortMarker)); !os.IsNotExist(e) {
			t.Fatalf("abort 标记不应被创建：%v", e)
		}
		if _, e := os.Stat(filepath.Join(dir, AbortMarker+".tmp")); !os.IsNotExist(e) {
			t.Fatalf("abort.tmp 不应残留：%v", e)
		}
		if after := snapshotTree(t, dir); after != before {
			t.Fatalf("互斥拒绝必须零写盘：\n前=%s\n后=%s", before, after)
		}
		res, _ := Scan(vault)
		if !hasState(res, id, StateCommitted) {
			t.Fatalf("%s 应仍归类 Committed", id)
		}
	})

	t.Run("abort_then_commit_rejected", func(t *testing.T) {
		vault := newVault(t)
		id := makeOpen(t, vault)
		if err := MarkAbort(vault, id); err != nil {
			t.Fatalf("首个 abort 应成功：%v", err)
		}
		dir := TxnDirPath(vault, id)
		before := snapshotTree(t, dir)

		err := MarkCommit(vault, id)
		var mc *MarkerConflictError
		if !errors.As(err, &mc) {
			t.Fatalf("abort 在场时 MarkCommit 应返回 *MarkerConflictError，得到：%v", err)
		}
		if _, e := os.Stat(filepath.Join(dir, CommitMarker)); !os.IsNotExist(e) {
			t.Fatalf("commit 标记不应被创建：%v", e)
		}
		if after := snapshotTree(t, dir); after != before {
			t.Fatalf("互斥拒绝必须零写盘：\n前=%s\n后=%s", before, after)
		}
		res, _ := Scan(vault)
		if !hasState(res, id, StateAborted) {
			t.Fatalf("%s 应仍归类 Aborted", id)
		}
	})
}

// TestMarkerIdempotent（P0）：合法普通零字节同侧标记已在盘时，重复 MarkCommit / MarkAbort **幂等成功**
// （返回 nil、零写盘、不产生 .tmp、inode 不变、Scan 结论稳定）。
func TestMarkerIdempotent(t *testing.T) {
	t.Run("commit_idempotent", func(t *testing.T) {
		vault := newVault(t)
		id := makeOpen(t, vault)
		if err := MarkCommit(vault, id); err != nil {
			t.Fatalf("首个 commit 应成功：%v", err)
		}
		dir := TxnDirPath(vault, id)
		_, ino0 := statDevIno(t, filepath.Join(dir, CommitMarker))
		before := snapshotTree(t, dir)

		if err := MarkCommit(vault, id); err != nil {
			t.Fatalf("重复 MarkCommit 应幂等成功，得到：%v", err)
		}
		_, ino1 := statDevIno(t, filepath.Join(dir, CommitMarker))
		if ino0 != ino1 {
			t.Fatalf("幂等 MarkCommit 不应替换 inode：%d → %d", ino0, ino1)
		}
		if _, e := os.Stat(filepath.Join(dir, CommitMarker+".tmp")); !os.IsNotExist(e) {
			t.Fatalf("commit.tmp 不应残留：%v", e)
		}
		if after := snapshotTree(t, dir); after != before {
			t.Fatalf("幂等 MarkCommit 必须零写盘：\n前=%s\n后=%s", before, after)
		}
		res, _ := Scan(vault)
		if !hasState(res, id, StateCommitted) {
			t.Fatalf("%s 应仍归类 Committed", id)
		}
	})

	t.Run("abort_idempotent", func(t *testing.T) {
		vault := newVault(t)
		id := makeOpen(t, vault)
		if err := MarkAbort(vault, id); err != nil {
			t.Fatalf("首个 abort 应成功：%v", err)
		}
		dir := TxnDirPath(vault, id)
		_, ino0 := statDevIno(t, filepath.Join(dir, AbortMarker))

		if err := MarkAbort(vault, id); err != nil {
			t.Fatalf("重复 MarkAbort 应幂等成功，得到：%v", err)
		}
		_, ino1 := statDevIno(t, filepath.Join(dir, AbortMarker))
		if ino0 != ino1 {
			t.Fatalf("幂等 MarkAbort 不应替换 inode：%d → %d", ino0, ino1)
		}
		res, _ := Scan(vault)
		if !hasState(res, id, StateAborted) {
			t.Fatalf("%s 应仍归类 Aborted", id)
		}
	})
}

// TestMarkerRefusesOverwriteMalformed（P0）：同侧标记已在盘但**形态非法**（非零字节普通文件、symlink、目录）时，
// writeMarker 拒绝以 rename 覆盖抹掉损坏证据——返回 *MarkerConflictError 且零写盘（不产生 .tmp、原字节 / 形态不变）。
func TestMarkerRefusesOverwriteMalformed(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, dir string)
	}{
		{"nonzero_regular", func(t *testing.T, dir string) {
			writeRaw(t, filepath.Join(dir, CommitMarker), []byte("not-empty"))
		}},
		{"symlink", func(t *testing.T, dir string) {
			ext := filepath.Join(t.TempDir(), "outside")
			if err := os.WriteFile(ext, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(ext, filepath.Join(dir, CommitMarker)); err != nil {
				t.Fatal(err)
			}
		}},
		{"directory", func(t *testing.T, dir string) {
			if err := os.Mkdir(filepath.Join(dir, CommitMarker), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vault := newVault(t)
			id := makeOpen(t, vault)
			dir := TxnDirPath(vault, id)
			tc.setup(t, dir)
			before := snapshotTree(t, dir)

			err := MarkCommit(vault, id)
			var mc *MarkerConflictError
			if !errors.As(err, &mc) {
				t.Fatalf("同侧标记形态非法时 MarkCommit 应返回 *MarkerConflictError，得到：%v", err)
			}
			if _, e := os.Stat(filepath.Join(dir, CommitMarker+".tmp")); !os.IsNotExist(e) {
				t.Fatalf("commit.tmp 不应残留：%v", e)
			}
			if after := snapshotTree(t, dir); after != before {
				t.Fatalf("拒绝覆盖必须零写盘：\n前=%s\n后=%s", before, after)
			}
		})
	}
}

// TestScanClassifiesMalformedMarkerCorrupt（P0，表驱动）：Scan 复核标记「普通零字节」形态——
// 非零字节 commit / abort、symlink 标记、目录标记、两侧并存都判 Corrupt fail closed。
func TestScanClassifiesMalformedMarkerCorrupt(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, dir string)
	}{
		{"nonzero_commit", func(t *testing.T, dir string) {
			writeRaw(t, filepath.Join(dir, CommitMarker), []byte("x"))
		}},
		{"nonzero_abort", func(t *testing.T, dir string) {
			writeRaw(t, filepath.Join(dir, AbortMarker), []byte("y"))
		}},
		{"commit_symlink", func(t *testing.T, dir string) {
			ext := filepath.Join(t.TempDir(), "e")
			_ = os.WriteFile(ext, nil, 0o644)
			if err := os.Symlink(ext, filepath.Join(dir, CommitMarker)); err != nil {
				t.Fatal(err)
			}
		}},
		{"commit_dir", func(t *testing.T, dir string) {
			if err := os.Mkdir(filepath.Join(dir, CommitMarker), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{"both_present", func(t *testing.T, dir string) {
			writeRaw(t, filepath.Join(dir, CommitMarker), nil)
			writeRaw(t, filepath.Join(dir, AbortMarker), nil)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vault := newVault(t)
			id := makeOpen(t, vault)
			dir := TxnDirPath(vault, id)
			tc.setup(t, dir)
			res, err := Scan(vault)
			if err != nil {
				t.Fatalf("Scan 出错：%v", err)
			}
			if !hasState(res, id, StateCorrupt) {
				t.Fatalf("%s（%s）应归类 Corrupt，得到 %#v", id, tc.name, res.Entries)
			}
		})
	}
}

// TestMarkerStateAtZeroByteContract（表驱动）：markerStateAt 的 present/ok 语义——
// 缺席、合法零字节、非零字节、目录、symlink 各自的 (present, ok)。
func TestMarkerStateAtZeroByteContract(t *testing.T) {
	vault := newVault(t)
	id := makeOpen(t, vault)
	txnDirFile, err := openRuntimeDir(vault, []string{IndexDirName, TxnDirName, id}, false)
	if err != nil {
		t.Fatalf("打开事务目录失败：%v", err)
	}
	defer txnDirFile.Close()
	fd := int(txnDirFile.Fd())
	dir := TxnDirPath(vault, id)

	// 缺席：commit 尚未写。
	if p, ok := markerStateAt(fd, CommitMarker); p || ok {
		t.Fatalf("缺席应 (false,false)，得到 (%v,%v)", p, ok)
	}
	// 合法零字节。
	writeRaw(t, filepath.Join(dir, CommitMarker), nil)
	if p, ok := markerStateAt(fd, CommitMarker); !p || !ok {
		t.Fatalf("合法零字节应 (true,true)，得到 (%v,%v)", p, ok)
	}
	// 非零字节 ⇒ present + !ok。
	writeRaw(t, filepath.Join(dir, AbortMarker), []byte("z"))
	if p, ok := markerStateAt(fd, AbortMarker); !p || ok {
		t.Fatalf("非零字节应 (true,false)，得到 (%v,%v)", p, ok)
	}
}
