package txn

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
)

// newVault 返回一个干净的临时 vault 根目录（.index/ 尚不存在，交由被测代码按需创建）。
func newVault(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// statDevIno 返回 path 的 (Dev, Ino)，供「inode 不可替换」断言使用。
func statDevIno(t *testing.T, path string) (uint64, uint64) {
	t.Helper()
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		t.Fatalf("stat %s 失败：%v", path, err)
	}
	return uint64(st.Dev), st.Ino
}

// mustReadFile 读文件字节，失败即 Fatal。
func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败：%v", path, err)
	}
	return b
}

// mkTxnDir 以合法 txn_id 名手工建出一个事务目录（绕过 AllocateTxnID，用于构造扫描场景）。
func mkTxnDir(t *testing.T, vault string, seq uint64) (string, string) {
	t.Helper()
	id := FormatTxnID(seq)
	dir := TxnDirPath(vault, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建事务目录失败：%v", err)
	}
	return id, dir
}

// writeRaw 在 path 写入原始字节（测试构造用，不走被测的安全落盘原语）。
func writeRaw(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("写 %s 失败：%v", path, err)
	}
}

// makeOpen 建出一个「已写 intent、无 commit/abort」的 OpenTxn，返回其 txn_id。
func makeOpen(t *testing.T, vault string) string {
	t.Helper()
	id, err := AllocateTxnID(vault)
	if err != nil {
		t.Fatalf("分配 txn_id 失败：%v", err)
	}
	if _, err := WriteIntent(vault, id, IntentInput{
		Argv:  []string{"eg", "edit", "o.md"},
		Files: []FileSpec{{Path: "o.md", Create: true, TargetBytes: []byte("x"), TargetOp: "create"}},
	}); err != nil {
		t.Fatalf("写 intent 失败：%v", err)
	}
	return id
}

// makeCommitted 建出一个已提交（commit 标记在场）的闭合事务，返回其 txn_id。
func makeCommitted(t *testing.T, vault string) string {
	t.Helper()
	id := makeOpen(t, vault)
	if err := MarkCommit(vault, id); err != nil {
		t.Fatalf("写 commit 标记失败：%v", err)
	}
	return id
}

// snapshotTree 把 root 子树（相对路径 + 文件大小 + 内容哈希）编成稳定字符串，
// 供「Scan 全程零写盘」的前后对比。
func snapshotTree(t *testing.T, root string) string {
	t.Helper()
	var lines []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		if info.IsDir() {
			lines = append(lines, "d "+rel)
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		lines = append(lines, fmt.Sprintf("f %s %d %s", rel, info.Size(), HashBytes(b)))
		return nil
	})
	if err != nil {
		t.Fatalf("快照 %s 失败：%v", root, err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}
