package txn

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// countTxnDirs 数 .index/txn 下合法命名的事务目录个数（用于「零事务落盘」断言）。
func countTxnDirs(t *testing.T, vault string) int {
	t.Helper()
	entries, err := os.ReadDir(TxnRootPath(vault))
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("读取 txn 根失败：%v", err)
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() && ValidTxnID(e.Name()) {
			n++
		}
	}
	return n
}

// TestTxnIDUniqueMonotonic：单游标批量分配与「双进程」交错分配都必须唯一、字典序单调递增。
func TestTxnIDUniqueMonotonic(t *testing.T) {
	t.Run("single_cursor_bulk", func(t *testing.T) {
		vault := newVault(t)
		cur := &seqCursor{}
		const n = 500
		seen := make(map[string]bool, n)
		var prev string
		for i := 0; i < n; i++ {
			id, err := allocateTxnID(vault, cur)
			if err != nil {
				t.Fatalf("第 %d 次分配失败：%v", i, err)
			}
			if !ValidTxnID(id) {
				t.Fatalf("id %q 不匹配正则", id)
			}
			if seen[id] {
				t.Fatalf("id %q 重复", id)
			}
			seen[id] = true
			if prev != "" && !(id > prev) {
				t.Fatalf("字典序未单调递增：%q 之后是 %q", prev, id)
			}
			prev = id
		}
	})

	t.Run("two_cursors_interleaved", func(t *testing.T) {
		vault := newVault(t)
		a, b := &seqCursor{}, &seqCursor{}
		seen := make(map[string]bool)
		for i := 0; i < 50; i++ {
			for _, cur := range []*seqCursor{a, b} {
				id, err := allocateTxnID(vault, cur)
				if err != nil {
					t.Fatal(err)
				}
				if seen[id] {
					t.Fatalf("跨游标 id %q 重复（磁盘 seq + 现存目录 guard 失效）", id)
				}
				seen[id] = true
			}
		}
	})
}

// TestTxnIDFormatIsSixteenHexNoTimestamp：格式恒为 t + 16 位零填充小写十六进制，
// 纯 seq 函数，无秒段拼接；相邻分配仅差 1。
func TestTxnIDFormatIsSixteenHexNoTimestamp(t *testing.T) {
	for _, seq := range []uint64{0, 1, 0x100000000, math.MaxUint64} {
		id := FormatTxnID(seq)
		if want := fmt.Sprintf("t%016x", seq); id != want {
			t.Fatalf("FormatTxnID(%d)=%q，应为 %q", seq, id, want)
		}
		if len(id) != 17 {
			t.Fatalf("id %q 长度应为 17", id)
		}
		if !ValidTxnID(id) {
			t.Fatalf("id %q 应匹配正则", id)
		}
		got, ok := ParseTxnID(id)
		if !ok || got != seq {
			t.Fatalf("ParseTxnID(%q)=(%d,%v)，应为 (%d,true)", id, got, ok, seq)
		}
	}

	// 相邻两次分配是纯 seq 递增（+1），不含时间戳成分。
	vault := newVault(t)
	cur := &seqCursor{}
	id1, _ := allocateTxnID(vault, cur)
	id2, _ := allocateTxnID(vault, cur)
	s1, _ := ParseTxnID(id1)
	s2, _ := ParseTxnID(id2)
	if s2 != s1+1 {
		t.Fatalf("相邻分配应恰差 1（纯 seq），得 %d → %d", s1, s2)
	}
}

// TestTxnIDSeqOverflowFailsClosed：seq 达 uint64 上限必须 fail closed，且不创建任何事务目录。
func TestTxnIDSeqOverflowFailsClosed(t *testing.T) {
	vault := newVault(t)
	cur := &seqCursor{last: math.MaxUint64}
	_, err := allocateTxnID(vault, cur)
	if err == nil {
		t.Fatal("seq 溢出应 fail closed")
	}
	var oe *SeqOverflowError
	if !errors.As(err, &oe) {
		t.Fatalf("应为 *SeqOverflowError，得 %T", err)
	}
	if oe.Code() != CodePrecheckFailed {
		t.Fatalf("码应为 %s，得 %s", CodePrecheckFailed, oe.Code())
	}
	if _, statErr := os.Stat(TxnRootPath(vault)); !os.IsNotExist(statErr) {
		t.Fatalf("溢出时连事务根目录都不应创建，stat 得 %v", statErr)
	}
}

// TestTxnIDUsesProcessLocalCursor：磁盘 seq 与现存目录被清空后，进程游标仍保证不重用编号。
func TestTxnIDUsesProcessLocalCursor(t *testing.T) {
	vault := newVault(t)
	cur := &seqCursor{}
	id1, err := allocateTxnID(vault, cur)
	if err != nil {
		t.Fatal(err)
	}
	s1, _ := ParseTxnID(id1)

	// 模拟保留策略清目录 + seq 消失（但进程未重启，游标仍在）。
	if err := os.RemoveAll(TxnRootPath(vault)); err != nil {
		t.Fatal(err)
	}

	id2, err := allocateTxnID(vault, cur)
	if err != nil {
		t.Fatal(err)
	}
	s2, _ := ParseTxnID(id2)
	if !(s2 > s1) {
		t.Fatalf("进程游标应阻止编号重用：清盘后 %d 之后应 > 它，得 %d", s1, s2)
	}
}

// TestCorruptSeqFailsClosed（P0）：seq 在盘但空 / 非十进制 / 溢出 / 非普通文件时必须 E15
// fail closed，且**不创建任何新事务目录、不改写 seq 字节**。
func TestCorruptSeqFailsClosed(t *testing.T) {
	type kind int
	const (
		kindBytes kind = iota
		kindDir
	)
	cases := []struct {
		name string
		kind kind
		body []byte
	}{
		{"empty", kindBytes, []byte("")},
		{"whitespace", kindBytes, []byte("   \n")},
		{"non_decimal", kindBytes, []byte("12x3")},
		{"overflow", kindBytes, []byte("99999999999999999999999999")},
		{"not_regular_dir", kindDir, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vault := newVault(t)
			root := TxnRootPath(vault)
			if err := os.MkdirAll(root, 0o755); err != nil {
				t.Fatal(err)
			}
			seqPath := filepath.Join(root, SeqFileName)
			var before []byte
			if tc.kind == kindDir {
				if err := os.Mkdir(seqPath, 0o755); err != nil {
					t.Fatal(err)
				}
			} else {
				writeRaw(t, seqPath, tc.body)
				before = append([]byte(nil), tc.body...)
			}
			dirsBefore := countTxnDirs(t, vault)

			_, err := allocateTxnID(vault, &seqCursor{})
			if err == nil {
				t.Fatal("损坏 seq 应 fail closed")
			}
			if !errors.Is(err, ErrSeqUnreadable) {
				t.Fatalf("应可 errors.Is 到 ErrSeqUnreadable，得 %v", err)
			}
			var c coded
			if !errors.As(err, &c) || c.Code() != CodePrecheckFailed {
				t.Fatalf("应携 E15，得 %T/%v", err, err)
			}
			if got := countTxnDirs(t, vault); got != dirsBefore {
				t.Fatalf("fail closed 不应创建新事务目录：%d → %d", dirsBefore, got)
			}
			if tc.kind == kindBytes {
				after := mustReadFile(t, seqPath)
				if string(after) != string(before) {
					t.Fatalf("seq 字节不应被改写：%q → %q", before, after)
				}
			}
		})
	}
}
