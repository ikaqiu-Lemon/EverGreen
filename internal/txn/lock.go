package txn

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// 锁的固定位置与形态常量（合同 §3）。字面量在本包内自持（依赖禁令，见 doc.go）。
const (
	// IndexDirName 是 vault 下的运行时目录名；锁与事务日志都住在这里。
	IndexDirName = ".index"
	// LockFileName 是**唯一**的锁文件名，路径恒为 vault/.index/run.lock。
	LockFileName = "run.lock"

	// DefaultLockTimeout 是 A-54 定稿的默认等待上限。
	DefaultLockTimeout = 10 * time.Second
	// LockTimeoutEnv 覆盖等待上限（不新增命令、不新增全局 flag）。
	LockTimeoutEnv = "EG_LOCK_TIMEOUT_MS"

	lockBodyPerm = 0o644
	// runtimeDirMode 是 .index/ 与事务目录的权限位。
	runtimeDirMode = 0o755
)

// lockBackoff 是合同 §3 逐字定死的退避序列；用尽之后恒 1s。
var lockBackoff = []time.Duration{
	50 * time.Millisecond,
	100 * time.Millisecond,
	200 * time.Millisecond,
	400 * time.Millisecond,
	800 * time.Millisecond,
	time.Second,
}

// ErrLockUnavailable 是「锁忙且等待超时」的 sentinel。
// T-…-074 用 errors.Is / errors.As 穿透它来决定退出码，本包不感知任何数字。
var ErrLockUnavailable = errors.New("run.lock 等待超时：另一个写者正在持锁")

// LockTimeoutError 是超时的类型化错误，携 E16 与等待过程的 W28 留痕。
type LockTimeoutError struct {
	Path     string
	Timeout  time.Duration
	Waited   time.Duration
	Attempts int

	diags []Diag
}

func (e *LockTimeoutError) Error() string {
	return fmt.Sprintf("锁不可用：等待 %s（重试 %d 次）后仍无法获得 %s 上的 flock",
		e.Waited.Round(time.Millisecond), e.Attempts, e.Path)
}

// Unwrap 让 errors.Is(err, ErrLockUnavailable) 成立。
func (e *LockTimeoutError) Unwrap() error { return ErrLockUnavailable }

// Code 返回 E16（合同 §12）。**不返回退出码**——翻译权在 T-…-074。
func (e *LockTimeoutError) Code() string { return CodeLockTimeout }

// Diagnostics 返回「等待期的全部 W28 + 收尾的一条 E16」。
func (e *LockTimeoutError) Diagnostics() []Diag {
	out := make([]Diag, 0, len(e.diags)+1)
	out = append(out, e.diags...)
	out = append(out, Diag{
		Code:    CodeLockTimeout,
		Level:   LevelError,
		Path:    e.Path,
		Message: e.Error(),
	})
	return out
}

// ErrLockPathUnsafe 是「run.lock 路径本身不安全」的 sentinel（symlink / 非普通文件）。
// 它与「正文损坏可容错」严格区分：正文字节坏了不影响互斥，可容错继续；
// 但 run.lock 是 **symlink** 或 **非普通文件** 时，跟随它写入会改到 vault 外的目标，
// 因此必须在**任何写入之前** fail closed。合同 §16.3 / R8：reserved entry 类型违规
// 在 A / B 类写路径映射为 E15 + 退 5，本包只返回可被上层映射的类型化事实。
var ErrLockPathUnsafe = errors.New("run.lock 路径不安全：是 symlink 或非普通文件")

// LockPathError 是 run.lock 路径类型违规的类型化错误，携 E15。
// **绝不**在返回前截断 / 写入锁路径或其 symlink 目标（Acquire 用 O_NOFOLLOW 原子拒绝，
// 目标字节零触碰）。
type LockPathError struct {
	Path   string
	Reason string
}

func (e *LockPathError) Error() string {
	return fmt.Sprintf("拒绝在 %s 上取锁：%s（symlink 逃逸 / 非普通文件，写入前 fail closed）", e.Path, e.Reason)
}

// Unwrap 让 errors.Is(err, ErrLockPathUnsafe) 成立。
func (e *LockPathError) Unwrap() error { return ErrLockPathUnsafe }

// Code 返回 E15（写前安全复核语义大类，合同 §12 / §16.3）。**不返回退出码**——翻译权在 T-…-074。
func (e *LockPathError) Code() string { return CodePrecheckFailed }

// Diagnostics 返回一条 E15。
func (e *LockPathError) Diagnostics() []Diag {
	return []Diag{{Code: CodePrecheckFailed, Level: LevelError, Path: e.Path, Message: e.Error()}}
}

// Holder 是锁正文里的持有者信息，**恰三项** + 可选的 txn_id（合同 §3）。
// 纯诊断：不参与互斥判定，读侧对不可解析的正文必须容错。
// 不写 host —— 陈旧锁判据只认内核 flock，比 host 会在容器 / 迁移场景误判。
type Holder struct {
	PID        int      `json:"pid"`
	AcquiredAt string   `json:"acquired_at"`
	Argv       []string `json:"argv"`
	TxnID      string   `json:"txn_id,omitempty"`
}

// LockOptions 是取锁参数。**没有 txn_id 字段**：取锁与 txn_id 分配 / intent 写入
// 必须解耦，B 类 index 维护命令要能持同一把锁而不开事务（合同 §16.1）。
type LockOptions struct {
	// Timeout ≤ 0 时取 EG_LOCK_TIMEOUT_MS，再退回 DefaultLockTimeout。
	Timeout time.Duration
	// Argv 只进锁正文做诊断。
	Argv []string
	// Now 供测试注入时钟；nil 时用 time.Now。
	Now func() time.Time
}

// Lock 是一把已持有的 run.lock。零值不可用，只能由 Acquire 产出。
type Lock struct {
	path    string
	file    *os.File
	holder  Holder
	diags   []Diag
	bodyErr error

	mu       sync.Mutex
	released bool
}

// LockPath 返回锁文件的绝对（相对 vaultRoot）路径。
func LockPath(vaultRoot string) string {
	return filepath.Join(vaultRoot, IndexDirName, LockFileName)
}

// Acquire 取 vault/.index/run.lock 上的排他 flock（S1，临界区开始）。
//
// 形态被合同 §3 定死，本 task 无选择权：
//   - `OpenFile(O_CREATE|O_RDWR)` 拿**长驻 fd**，flock 加在该 fd 上直到 Release；
//   - 忙时**绝不**强抢、**绝不**删锁：退避重试并逐次留痕 W28，超时返回 *LockTimeoutError；
//   - flock 成功即等价于「没有活着的写者」（内核在进程退出 / fd 关闭时自动释放），
//     因此旧正文一律原地覆盖，**不比 pid 存活、不比 host、不要求 argv 可解析**；
//   - 正文只由**同一把已加锁 fd** 做 Ftruncate + Pwrite + Fsync 原地写，
//     **被加锁的 inode 在持锁期零替换**（无 tmp+rename、无 unlink、无先释放再重建）；
//   - 正文写失败只记人类可读信息（BodyWriteError），不影响互斥、不产诊断码、不改退出码。
//
// 返回值**不含 txn_id**：分配 txn_id 与写 intent 都发生在取锁之后且是可选步骤。
func Acquire(vaultRoot string, opts LockOptions) (*Lock, error) {
	if vaultRoot == "" {
		return nil, errors.New("取锁失败：vaultRoot 为空")
	}
	lockPath := filepath.Join(vaultRoot, IndexDirName, LockFileName)

	now := opts.Now
	if now == nil {
		now = time.Now
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = timeoutFromEnv()
	}

	// 安全解析 .index/（parent-symlink fail closed，见 safedir.go）：若 .index 是 symlink 或
	// 非目录，这里直接 fail closed，绝不在其下创建 / 写入锁文件。
	indexDir, err := openRuntimeDir(vaultRoot, []string{IndexDirName}, true)
	if err != nil {
		return nil, err
	}

	// 长驻 fd：全程不关（除 Release），也不换 inode。
	//
	// **P0 symlink fail closed（合同 §16.3 / R8）**：相对已解析的 .index/ fd 用 openat +
	// O_NOFOLLOW **原子**拒绝「run.lock 自身是 symlink」的情形——目标字节**永不被打开、
	// 永不被 Ftruncate / Pwrite 触碰**，且判定与打开是同一次 syscall（消除 Lstat→Open TOCTOU）。
	// 随后再用 f.Stat() 验证拿到的是**普通文件**（拒绝目录 / FIFO / 设备 / socket），全部在
	// 任何写入之前完成。父目录 .index/ 的 symlink 已由 openRuntimeDir 更早拦下。
	f, err := openLockFileNoFollowAt(int(indexDir.Fd()), LockFileName, lockPath)
	_ = indexDir.Close() // 目录 fd 用完即关；flock 与正文写入都在锁文件 fd 上进行
	if err != nil {
		var pe *LockPathError
		if errors.As(err, &pe) {
			return nil, err
		}
		return nil, fmt.Errorf("打开锁文件 %s 失败：%w", lockPath, err)
	}

	start := now()
	var diags []Diag
	attempts := 0
	for {
		attempts++
		ferr := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if ferr == nil {
			break
		}
		if !errors.Is(ferr, syscall.EWOULDBLOCK) && !errors.Is(ferr, syscall.EAGAIN) {
			_ = f.Close()
			return nil, fmt.Errorf("flock %s 失败：%w", lockPath, ferr)
		}
		waited := now().Sub(start)
		if waited >= timeout {
			// 超时：只关自己的 fd，**不删锁文件**、不改正文、零写入。
			_ = f.Close()
			return nil, &LockTimeoutError{
				Path:     lockPath,
				Timeout:  timeout,
				Waited:   waited,
				Attempts: attempts,
				diags:    diags,
			}
		}
		delay := backoffFor(attempts)
		if remain := timeout - waited; delay > remain {
			delay = remain
		}
		diags = append(diags, Diag{
			Code:  CodeLockWaitRetry,
			Level: LevelWarning,
			Path:  lockPath,
			Message: fmt.Sprintf("锁忙，第 %d 次退避重试（等待 %s，已等 %s / 上限 %s）",
				attempts, delay, waited.Round(time.Millisecond), timeout),
		})
		time.Sleep(delay)
	}

	l := &Lock{path: lockPath, file: f, diags: diags}
	l.holder = Holder{
		PID:        os.Getpid(),
		AcquiredAt: now().UTC().Format(time.RFC3339),
		Argv:       append([]string(nil), opts.Argv...),
	}
	// 初次写入不含 txn_id（此刻它还不存在，合同 §3）。
	if err := l.writeBody(); err != nil {
		l.bodyErr = err
	}
	return l, nil
}

// WithLock 把「S2 恢复屏障 + S3 锁内重新候选发现 / 重读 / 校验 + S5 落盘 + S6 提交 + S7 Git」
// 整段包进临界区，并保证**任何**退出路径（含 panic）都释放锁：
// 释放放在 defer 里，panic 会在 defer 执行后继续向上传播（无需 recover 也必然释放）。
// 参数解析、S0 的提示性候选发现与报告渲染必须留在本函数**之外**（合同 §2）。
func WithLock(vaultRoot string, opts LockOptions, critical func(*Lock) error) (err error) {
	l, aerr := Acquire(vaultRoot, opts)
	if aerr != nil {
		return aerr
	}
	defer func() {
		if rerr := l.Release(); rerr != nil && err == nil {
			err = rerr
		}
	}()
	return critical(l)
}

// Path 是锁文件路径。
func (l *Lock) Path() string { return l.path }

// Holder 返回本进程写入的持有者信息副本。
func (l *Lock) Holder() Holder { return l.holder }

// Diagnostics 返回取锁过程中的 W28 留痕（成功路径通常为空）。
func (l *Lock) Diagnostics() []Diag {
	return append([]Diag(nil), l.diags...)
}

// BodyWriteError 返回正文写入失败的原因（纯诊断，不影响互斥与退出码）。
func (l *Lock) BodyWriteError() error { return l.bodyErr }

// RecordTxnID 在 txn_id 分配之后**可选地**原地补写一次正文（同一 inode、同一把已加锁 fd）。
// 失败不影响互斥、不产诊断码、不改退出码，只把原因登记到 BodyWriteError。
func (l *Lock) RecordTxnID(txnID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return errors.New("锁已释放，拒绝补写正文")
	}
	l.holder.TxnID = txnID
	if err := l.writeBody(); err != nil {
		l.bodyErr = err
		return err
	}
	return nil
}

// Release 释放锁：LOCK_UN + Close，**不删锁文件**。
// 锁文件长期存在于磁盘是正常态（合同 §3），删它等于给下一个进程制造新 inode ⇒ 双写。
func (l *Lock) Release() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return nil
	}
	l.released = true
	unlockErr := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()
	if unlockErr != nil {
		return fmt.Errorf("释放 flock 失败：%w", unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("关闭锁文件失败：%w", closeErr)
	}
	return nil
}

// openLockFileNoFollowAt 相对**已安全解析**的 .index/ 目录 fd 原子打开 run.lock，
// 并保证拿到的是**普通文件**、绝非 symlink。
//
// 安全性由两步共同给出，且都在**任何写入之前**：
//   - openat + O_NOFOLLOW：若 run.lock 自身是 symlink，open 立即以 ELOOP 失败（同一次 syscall
//     内判定 + 拒绝，无 TOCTOU），symlink 目标**永不被打开、字节永不被触碰**；父目录 symlink
//     已由 openRuntimeDir 更早拦下；
//   - O_NONBLOCK：避免 run.lock 恰是 FIFO / 设备时 open 阻塞；随后 f.Stat() 若非普通文件即拒绝。
//
// 任一违规返回 *LockPathError（携 E15），**不**在返回前写入 / 截断任何路径。diagPath 仅用于诊断展示。
func openLockFileNoFollowAt(indexFd int, name, diagPath string) (*os.File, error) {
	var (
		fd  int
		err error
	)
	for attempt := 0; attempt < 4; attempt++ {
		fd, err = sysOpenat(indexFd, name,
			os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, uint32(lockBodyPerm))
		if !errors.Is(err, syscall.ENOENT) {
			break
		}
		// Darwin may transiently return ENOENT when several callers race to
		// create the same directory-relative lock file. Retry against the
		// already validated directory fd; every attempt retains O_NOFOLLOW.
		runtime.Gosched()
	}
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return nil, &LockPathError{Path: diagPath, Reason: "run.lock 是 symlink（O_NOFOLLOW 原子拒绝跟随）"}
		}
		if errors.Is(err, syscall.EISDIR) {
			return nil, &LockPathError{Path: diagPath, Reason: "run.lock 是目录，reserved entry 类型违规"}
		}
		return nil, err
	}
	f := os.NewFile(uintptr(fd), diagPath)
	info, serr := f.Stat()
	if serr != nil {
		_ = f.Close()
		return nil, fmt.Errorf("stat 锁文件失败：%w", serr)
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, &LockPathError{
			Path:   diagPath,
			Reason: fmt.Sprintf("run.lock 不是普通文件（mode=%s），reserved entry 类型违规", info.Mode()),
		}
	}
	return f, nil
}

// writeBody 是**唯一**的正文写入路径：同一把已加锁的 *os.File（同一 inode）上
// Truncate(0) → WriteAt → Sync（= fsync）。用 os.File 方法而非裸 syscall.Pwrite，
// 是因为 darwin 标准库未导出 Pwrite；WriteAt/Truncate/Sync 跨平台且语义等价（都作用于
// 同一 fd、不换 inode），既满足互斥不变式又免除新增依赖。
// 允许非原子（读者可能读到截断 / 半写的 JSON），因此读侧 ReadHolder 必须容错。
func (l *Lock) writeBody() error {
	body, err := json.Marshal(l.holder)
	if err != nil {
		return fmt.Errorf("序列化锁正文失败：%w", err)
	}
	body = append(body, '\n')
	if err := l.file.Truncate(0); err != nil {
		return fmt.Errorf("Truncate 锁正文失败：%w", err)
	}
	// WriteAt 在写满 len(body) 前会持续重试，短写时返回 err（内部保证「全写或报错」）。
	if _, werr := l.file.WriteAt(body, 0); werr != nil {
		return fmt.Errorf("WriteAt 锁正文失败：%w", werr)
	}
	if err := l.file.Sync(); err != nil {
		return fmt.Errorf("Fsync 锁正文失败：%w", err)
	}
	return nil
}

// ReadHolder 只读锁正文，且对**空 / 截断 / 非 JSON / 来自另一台机器**的正文全部容错：
// 解析失败返回 ok=false，绝不影响互斥判定、不产诊断码、不改退出码（合同 §3）。
func ReadHolder(vaultRoot string) (Holder, bool) {
	raw, err := os.ReadFile(LockPath(vaultRoot))
	if err != nil {
		return Holder{}, false
	}
	var h Holder
	if err := json.Unmarshal(raw, &h); err != nil {
		return Holder{}, false
	}
	if h.PID <= 0 {
		return h, false
	}
	return h, true
}

// backoffFor 给出第 attempt 次失败之后的等待时长（序列用尽后恒 1s）。
func backoffFor(attempt int) time.Duration {
	if attempt <= 0 {
		attempt = 1
	}
	if attempt > len(lockBackoff) {
		return lockBackoff[len(lockBackoff)-1]
	}
	return lockBackoff[attempt-1]
}

// timeoutFromEnv 解析 EG_LOCK_TIMEOUT_MS；缺失 / 非法 / 非正一律回落到默认 10s。
func timeoutFromEnv() time.Duration {
	raw := os.Getenv(LockTimeoutEnv)
	if raw == "" {
		return DefaultLockTimeout
	}
	ms, err := strconv.Atoi(raw)
	if err != nil || ms <= 0 {
		return DefaultLockTimeout
	}
	return time.Duration(ms) * time.Millisecond
}
