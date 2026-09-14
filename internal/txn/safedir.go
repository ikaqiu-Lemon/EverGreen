package txn

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// [S5] safedir.go —— 运行时目录的**单点**安全解析器（parent-symlink fail closed）。
//
// 为什么只给最终文件加 O_NOFOLLOW 不够：`os.MkdirAll` / 按路径 open 会**跟随已存在的父目录
// symlink**。若攻击者预置 `.index`、`.index/txn` 或 `<txn_id>/pre` 指向 vault 之外，随后即便
// 叶子文件用 O_NOFOLLOW 打开，写入仍落在外部目录里——因为逃逸发生在**父组件**而非叶子。
//
// 防御：从**受信锚点** vaultRoot 出发，用 openat/mkdirat + O_DIRECTORY|O_NOFOLLOW **基于已打开
// 父目录 fd 逐段行走**，任一运行时组件是 symlink / 非目录立即 fail closed，之后所有创建 / 写入
// 都相对**已解析的目录 fd**（*at 族）完成，绝不再按路径重新解析。这把「检查」与「使用」尽量
// 收敛到同一 fd 上以压制 TOCTOU；但**不宣称**单纯 Lstat 或本 helper 能彻底消除非协作竞态——
// 对手仍可能在 mkdir 与 open 的窗口内换目录，此时 O_NOFOLLOW 让我们**失败关闭**而非误写。
//
// 覆盖的运行时组件（至少）：.index、txn、<txn_id>、pre、quarantine。

// ErrUnsafeRuntimeDir 是「运行时目录组件不是真实目录」的 sentinel（symlink / 非目录）。
var ErrUnsafeRuntimeDir = errors.New("运行时目录组件是 symlink 或非目录，拒绝在其下创建 / 写入")

// RuntimeDirError 是运行时目录组件类型违规的类型化错误，携 E15（写前安全复核语义大类）。
// 返回它之前**不创建任何目录 / 文件、不写任何字节**。
type RuntimeDirError struct {
	Path      string // 违规组件的（尽力拼出的）完整路径
	Component string // 违规的单个组件名
	Reason    string
}

func (e *RuntimeDirError) Error() string {
	return fmt.Sprintf("拒绝解析运行时目录 %s：组件 %q %s（parent-symlink fail closed）",
		e.Path, e.Component, e.Reason)
}

// Unwrap 让 errors.Is(err, ErrUnsafeRuntimeDir) 成立。
func (e *RuntimeDirError) Unwrap() error { return ErrUnsafeRuntimeDir }

// Code 返回 E15。**不返回退出码**——翻译权在 T-…-074。
func (e *RuntimeDirError) Code() string { return CodePrecheckFailed }

// Diagnostics 返回一条 E15。
func (e *RuntimeDirError) Diagnostics() []Diag {
	return []Diag{{Code: CodePrecheckFailed, Level: LevelError, Path: e.Path, Message: e.Error()}}
}

// dirOpenFlags 是「打开一个目录且拒绝跟随 symlink」的固定 flag 组合。
const dirOpenFlags = syscall.O_DIRECTORY | syscall.O_NOFOLLOW | syscall.O_RDONLY | syscall.O_CLOEXEC

// openRuntimeDir 从受信锚点 vaultRoot 出发，逐段安全解析 components 并返回**最终目录**的句柄。
//
//   - vaultRoot 本身是调用方给定的受信锚点：允许其路径含 symlink（一次性用 O_DIRECTORY 打开）；
//   - 其后**每一段**都以 openat + O_DIRECTORY|O_NOFOLLOW 打开：symlink ⇒ ELOOP、非目录 ⇒ ENOTDIR，
//     一律映射 *RuntimeDirError 失败关闭；
//   - create=true 时，缺失段用 mkdirat 原子创建后**再次** O_NOFOLLOW 打开（撞了 symlink 仍失败关闭）；
//   - create=false 时，缺失段返回可 errors.Is(ENOENT) 的错误，交由调用方决定语义。
//
// 返回的 *os.File 拥有该目录 fd，调用方负责 Close；期间的中间 fd 已被逐一关闭，无泄漏。
func openRuntimeDir(vaultRoot string, components []string, create bool) (*os.File, error) {
	if vaultRoot == "" {
		return nil, errors.New("安全目录解析失败：vaultRoot 为空")
	}
	baseFd, err := syscall.Open(vaultRoot, syscall.O_DIRECTORY|syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("打开 vault 根目录 %s 失败：%w", vaultRoot, err)
	}
	cur := os.NewFile(uintptr(baseFd), vaultRoot)
	pathSoFar := vaultRoot
	for _, comp := range components {
		pathSoFar = filepath.Join(pathSoFar, comp)
		child, cerr := openChildDir(int(cur.Fd()), comp, create)
		_ = cur.Close() // 父 fd 用完即关，只保留刚解析出的子 fd
		if cerr != nil {
			var rde *RuntimeDirError
			if errors.As(cerr, &rde) {
				rde.Path = pathSoFar
				return nil, rde
			}
			return nil, fmt.Errorf("解析运行时目录 %s 失败：%w", pathSoFar, cerr)
		}
		cur = child
	}
	return cur, nil
}

// openChildDir 相对**借用的**父目录 fd 打开（可选创建）单个子目录组件，O_NOFOLLOW 拒绝 symlink。
// 不关闭 parentFd（调用方所有）。
func openChildDir(parentFd int, name string, create bool) (*os.File, error) {
	fd, err := sysOpenat(parentFd, name, dirOpenFlags, 0)
	if err != nil && errors.Is(err, syscall.ENOENT) && create {
		if merr := sysMkdirat(parentFd, name, uint32(runtimeDirMode)); merr != nil && !errors.Is(merr, syscall.EEXIST) {
			return nil, fmt.Errorf("mkdirat %q 失败：%w", name, merr)
		}
		// 重新 O_NOFOLLOW 打开：若刚建好的目录在窗口内被换成 symlink，这里 ELOOP 失败关闭。
		fd, err = sysOpenat(parentFd, name, dirOpenFlags, 0)
	}
	if err != nil {
		return nil, mapDirError(name, err)
	}
	return os.NewFile(uintptr(fd), name), nil
}

// mapDirError 把 openat 的 errno 归一为 *RuntimeDirError（symlink / 非目录），其余原样透传。
func mapDirError(comp string, err error) error {
	switch {
	case errors.Is(err, syscall.ELOOP):
		return &RuntimeDirError{Component: comp, Reason: "是 symlink（O_NOFOLLOW 拒绝跟随）"}
	case errors.Is(err, syscall.ENOTDIR):
		return &RuntimeDirError{Component: comp, Reason: "存在但不是目录"}
	default:
		return err
	}
}

// writeFileSyncedAt 相对已安全解析的目录 fd 写全量字节并 fsync（供 seq.tmp / pre/<n> /
// intent.json.tmp / marker.tmp 复用）。
//
// **O_NOFOLLOW（叶子防线）**：目标预置 symlink 时 O_TRUNC 会顺着它清零运行时目录外的文件，
// 加 O_NOFOLLOW 后此情形以 ELOOP 失败关闭，symlink 目标字节永不被截断 / 写入；遗留的普通 .tmp
// （崩溃残留、我们自有的垃圾）仍允许被覆盖。父目录的 symlink 由 openRuntimeDir 更早拦下。
func writeFileSyncedAt(dirFd int, name string, data []byte) error {
	fd, err := sysOpenat(dirFd, name,
		os.O_CREATE|os.O_WRONLY|os.O_TRUNC|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, uint32(markerPerm))
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return &PathViolationError{Field: "write_path", Value: name,
				Reason: "目标是 symlink（O_NOFOLLOW 拒绝，防截断运行时目录外文件）"}
		}
		return err
	}
	f := os.NewFile(uintptr(fd), name)
	if _, werr := f.Write(data); werr != nil {
		_ = f.Close()
		return werr
	}
	if serr := f.Sync(); serr != nil { // Fsync 落盘
		_ = f.Close()
		return serr
	}
	return f.Close()
}
