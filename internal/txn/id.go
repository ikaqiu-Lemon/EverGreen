package txn

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// 事务日志根目录与持久计数器（合同 §4）。
const (
	// TxnDirName 是 .index/ 下的事务日志目录名（M6 runtime reserved entry，必须是目录）。
	TxnDirName = "txn"
	// SeqFileName 是全局持久计数器文件名；保留策略清理**不重置**它。
	SeqFileName = "seq"
)

// txnIDPattern 是 A-59 定稿的 txn_id 正则：t + 16 位小写十六进制，无秒段拼接。
const txnIDPattern = `^t[0-9a-f]{16}$`

var txnIDRe = regexp.MustCompile(txnIDPattern)

// SeqOverflowError 是 seq 超 uint64 上限时的 fail closed 错误（合同 §4 第 3 条）：
// 不回绕、不截断、不改格式、**不创建事务目录**、权威零写入。
type SeqOverflowError struct {
	Base uint64
}

func (e *SeqOverflowError) Error() string {
	return fmt.Sprintf("txn_id 分配失败：seq 已达 uint64 上限（%d），拒绝回绕 / 截断 / 改格式", e.Base)
}

// Code 返回 E15（写前安全复核语义大类的原因之一，合同 §12 原因 ⑦）。
func (e *SeqOverflowError) Code() string { return CodePrecheckFailed }

// Diagnostics 返回一条 E15。
func (e *SeqOverflowError) Diagnostics() []Diag {
	return []Diag{{Code: CodePrecheckFailed, Level: LevelError, Message: e.Error()}}
}

// ErrSeqUnreadable 是「seq 持久计数器在盘但不可信」的 sentinel。
//
// **为什么必须 fail closed 而不能当作「无持久值」继续分配**：三源公式里，磁盘 seq 是
// 唯一能跨进程记住「已消耗到哪个序号」的来源。保留策略（Prune）会删掉已闭合事务目录，
// 于是「现存目录最大序号」也随之变小；进程重启后 process-local 游标归零。此时若把一个
// **已存在但损坏**的 seq 当成缺失、按 0 起算，新事务就会**重用**早已发过的低位编号，
// 直接违反合同 §4「当前 .index/txn 生命周期内同一 vault 唯一且字典序单调递增」，
// 并让报告 ↔ 日志对账指向错误的历史事务。**只有 os.ErrNotExist（真的没有这个文件）
// 才等价于「无持久值」**；空文件 / 非十进制 / 溢出 / 非普通文件 / 读取失败一律 fail closed。
var ErrSeqUnreadable = errors.New(".index/txn/seq 在盘但不可信，拒绝据此分配 txn_id")

// SeqStateError 是 seq 文件不可信时的类型化 fail closed 错误，携 E15。
// 返回它之前**不创建任何新事务目录**、不改写 seq、权威零写入。
type SeqStateError struct {
	Path   string
	Reason string
}

func (e *SeqStateError) Error() string {
	return fmt.Sprintf("txn_id 分配失败：%s %s（拒绝按缺失值重用编号，需人工处理）", e.Path, e.Reason)
}

// Unwrap 让 errors.Is(err, ErrSeqUnreadable) 成立。
func (e *SeqStateError) Unwrap() error { return ErrSeqUnreadable }

// Code 返回 E15（写前安全复核语义大类；R8 §18.4 已撤销「精确形态计数」，原因清单可增）。
func (e *SeqStateError) Code() string { return CodePrecheckFailed }

// Diagnostics 返回一条 E15。
func (e *SeqStateError) Diagnostics() []Diag {
	return []Diag{{Code: CodePrecheckFailed, Level: LevelError, Path: e.Path, Message: e.Error()}}
}

// seqCursor 是 **process-local lastSeq**（合同 §4 三源之一）。
// 它必须进入分配公式：磁盘 seq 与现存目录都可能被保留策略清理、被 .index/ 合法删除
// 或被外部工具改小，只有进程游标能在这些情况下继续为本进程提供唯一性与单调性。
type seqCursor struct {
	mu   sync.Mutex
	last uint64
}

// processSeq 是本进程唯一的游标实例，启动值为 0，分配成功后原子更新。
var processSeq seqCursor

// TxnRootPath 返回 vault/.index/txn。
func TxnRootPath(vaultRoot string) string {
	return filepath.Join(vaultRoot, IndexDirName, TxnDirName)
}

// TxnDirPath 返回 vault/.index/txn/<txn_id>。
func TxnDirPath(vaultRoot, txnID string) string {
	return filepath.Join(TxnRootPath(vaultRoot), txnID)
}

// FormatTxnID 把 seq 编成 txn_id：定宽 16 位零填充小写十六进制，
// 使字典序与数值序一致（报告 ↔ 日志对账与目录寻址都依赖这条性质）。
func FormatTxnID(seq uint64) string {
	return fmt.Sprintf("t%016x", seq)
}

// ValidTxnID 判断是否匹配 A-59 定稿正则。
func ValidTxnID(id string) bool { return txnIDRe.MatchString(id) }

// ParseTxnID 从 txn_id 还原 seq。
func ParseTxnID(id string) (uint64, bool) {
	if !ValidTxnID(id) {
		return 0, false
	}
	seq, err := strconv.ParseUint(id[1:], 16, 64)
	if err != nil {
		return 0, false
	}
	return seq, true
}

// AllocateTxnID 分配一个 txn_id 并以 O_EXCL 语义建出事务目录。
//
// **只允许在持锁之后调用**（合同 §4）。分配公式三源取最大，缺一即违约：
//
//	seq = max(读 .index/txn/seq 得到的持久值, 现存事务目录名解出的最大序号, process-local lastSeq) + 1
//
// 撞名（EEXIST）则 seq+1 重试；成功后原子更新 seq 文件与进程游标。
// seq 达 uint64 上限时 fail closed：不创建目录、零权威写入、返回 *SeqOverflowError。
//
// 唯一性 / 单调性的保证域（如实限定）：**同进程内无条件**唯一且单调；
// 同一 vault 在**当前 .index/txn 生命周期内**跨进程唯一且字典序单调递增；
// `.index/` 被合法删除后 seq 重置 ⇒ 跨生命周期不保证唯一（在册限制，合同 §4）。
func AllocateTxnID(vaultRoot string) (string, error) {
	return allocateTxnID(vaultRoot, &processSeq)
}

// allocateTxnID 是可注入游标的实现体：传入独立游标即等价于「另一个进程」。
func allocateTxnID(vaultRoot string, cur *seqCursor) (string, error) {
	cur.mu.Lock()
	defer cur.mu.Unlock()

	base := cur.last
	// 溢出判定先于任何 mkdir：fail closed 时连事务目录都不许创建。
	if base == math.MaxUint64 {
		return "", &SeqOverflowError{Base: base}
	}

	// 安全解析 .index/txn/（parent-symlink fail closed，见 safedir.go）：.index 或 txn 是
	// symlink / 非目录 ⇒ 直接 fail closed，绝不在其下创建任何事务目录 / seq。之后 seq 读写与
	// 事务目录创建全部相对**已解析的目录 fd**（*at 族），不再按路径重新解析。
	txnDir, err := openRuntimeDir(vaultRoot, []string{IndexDirName, TxnDirName}, true)
	if err != nil {
		return "", err
	}
	defer txnDir.Close()
	dirFd := int(txnDir.Fd())
	seqPath := filepath.Join(TxnRootPath(vaultRoot), SeqFileName) // 仅用于诊断展示

	// 三源取最大。**seq 不可信必须在创建任何新事务目录之前 fail closed**（见 readSeqFileAt）。
	persisted, ok, serr := readSeqFileAt(dirFd, seqPath)
	if serr != nil {
		return "", serr
	}
	if ok && persisted > base {
		base = persisted
	}
	// 现存目录最大序号：ReadDir 的错误必须传播，不能静默当空集。
	scanned, ok, merr := maxExistingSeqAt(txnDir)
	if merr != nil {
		return "", merr
	}
	if ok && scanned > base {
		base = scanned
	}
	if base == math.MaxUint64 {
		return "", &SeqOverflowError{Base: base}
	}
	for {
		seq := base + 1
		id := FormatTxnID(seq)
		err := sysMkdirat(dirFd, id, uint32(runtimeDirMode))
		if err == nil {
			if werr := writeSeqFileAt(dirFd, seq); werr != nil {
				return "", werr
			}
			cur.last = seq
			return id, nil
		}
		if errors.Is(err, syscall.EEXIST) || errors.Is(err, os.ErrExist) {
			if seq == math.MaxUint64 {
				return "", &SeqOverflowError{Base: seq}
			}
			base = seq
			continue
		}
		return "", fmt.Errorf("创建事务目录 %s 失败：%w", id, err)
	}
}

// readSeqFileAt 相对**已安全解析**的 .index/txn 目录 fd 读持久计数器（openat + O_NOFOLLOW）。
//
// **只有 ENOENT 等价于「无持久值」**（返回 ok=false, err=nil）。
// 文件为空 / 非十进制 / 超 uint64 / 非普通文件 / symlink / 读取失败 ⇒ 返回 *SeqStateError
// fail closed（携 E15）——绝不降级成「缺失」，否则会重用编号（见 ErrSeqUnreadable 的说明）。
// 本函数**只读**：不创建、不改写、不修复 seq 文件。diagPath 仅用于诊断展示。
func readSeqFileAt(dirFd int, diagPath string) (uint64, bool, error) {
	fd, err := sysOpenat(dirFd, SeqFileName, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, syscall.ENOENT) {
			return 0, false, nil // 真的没有这个文件 —— 唯一可视为「无持久值」的情形
		}
		if errors.Is(err, syscall.ELOOP) {
			return 0, false, &SeqStateError{Path: diagPath,
				Reason: "是 symlink（拒绝跟随，值必须来自 .index/txn 内普通文件）"}
		}
		return 0, false, &SeqStateError{Path: diagPath, Reason: "无法打开：" + err.Error()}
	}
	f := os.NewFile(uintptr(fd), diagPath)
	defer f.Close()
	info, serr := f.Stat()
	if serr != nil {
		return 0, false, &SeqStateError{Path: diagPath, Reason: "无法 stat：" + serr.Error()}
	}
	if !info.Mode().IsRegular() {
		return 0, false, &SeqStateError{Path: diagPath,
			Reason: fmt.Sprintf("不是普通文件（mode=%s）", info.Mode())}
	}
	raw, rerr := io.ReadAll(f)
	if rerr != nil {
		return 0, false, &SeqStateError{Path: diagPath, Reason: "读取失败：" + rerr.Error()}
	}
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return 0, false, &SeqStateError{Path: diagPath, Reason: "在盘但内容为空"}
	}
	seq, perr := strconv.ParseUint(text, 10, 64)
	if perr != nil {
		if errors.Is(perr, strconv.ErrRange) {
			return 0, false, &SeqStateError{Path: diagPath,
				Reason: fmt.Sprintf("值 %q 超出 uint64 上限", text)}
		}
		return 0, false, &SeqStateError{Path: diagPath,
			Reason: fmt.Sprintf("值 %q 不是合法十进制无符号整数", text)}
	}
	return seq, true, nil
}

// writeSeqFileAt 相对 .index/txn 目录 fd 以 tmp + Fsync + renameat + 目录 Fsync 原子更新持久计数器。
func writeSeqFileAt(dirFd int, seq uint64) error {
	if err := writeFileSyncedAt(dirFd, SeqFileName+".tmp", []byte(strconv.FormatUint(seq, 10)+"\n")); err != nil {
		return fmt.Errorf("写 seq 临时文件失败：%w", err)
	}
	if err := sysRenameat(dirFd, SeqFileName+".tmp", dirFd, SeqFileName); err != nil {
		return fmt.Errorf("发布 seq 失败：%w", err)
	}
	if err := syscall.Fsync(dirFd); err != nil {
		return fmt.Errorf("Fsync 事务根目录失败：%w", err)
	}
	return nil
}

// maxExistingSeqAt 通过**已解析的目录 fd** 扫描现存事务目录名解出的最大序号（只读）。
//
// ReadDir 的错误必须传播为 fail closed，绝不静默当空集——否则会低估已消耗序号而重用编号。
func maxExistingSeqAt(dir *os.File) (uint64, bool, error) {
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return 0, false, &SeqStateError{Path: dir.Name(), Reason: "扫描现存事务目录失败：" + err.Error()}
	}
	var maxSeq uint64
	found := false
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		seq, ok := ParseTxnID(e.Name())
		if !ok {
			continue
		}
		if !found || seq > maxSeq {
			maxSeq = seq
			found = true
		}
	}
	return maxSeq, found, nil
}
