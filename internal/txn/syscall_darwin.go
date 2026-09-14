//go:build darwin

package txn

import (
	"syscall"
	"unsafe"
)

// [S5] Darwin 平台适配层（单点）——用 syscall.Syscall/Syscall6 + 公开稳定的 BSD 系统调用号
// 补齐标准库未导出的 fd-relative 系统调用（Openat/Mkdirat/Renameat）。
//
// 背景：现代 darwin 标准库 syscall 经由 libSystem trampoline 实现，且**未导出** Openat/
// Mkdirat/Renameat/Pwrite。为在「go.mod 直接依赖数不变（不引入 golang.org/x/sys）」的约束下
// 让 darwin/amd64 与 darwin/arm64 也能静态编译，这里在**单点**用 syscall.Syscall6 直接发起
// 系统调用，语义与 Linux 版逐字一致；no-follow / dirfd 相对寻址全部由调用方传入的 flags 与
// dirfd 决定，本层不改变任何安全语义。
//
// 系统调用号取自 XNU 公开头文件 <sys/syscall.h>（Apple 长期稳定的 UNIX class 号）：
//
//	openat = 463、renameat = 465、mkdirat = 475。
//
// 架构差异由标准库汇编吸收，两个架构都只需传入**裸 BSD 号**（见 GOROOT/src/syscall/
// asm_darwin_{amd64,arm64}.s）：
//   - darwin/amd64：Syscall6 汇编对 trap 号执行 ADDQ $0x2000000（SYSCALL_CLASS_UNIX）后再 SYSCALL；
//   - darwin/arm64：正的 BSD 号直接放入 x16 后 SVC——同一常量通用。
//
// syscall.Syscall/Syscall6 带 //go:uintptrescapes 语义，uintptr(unsafe.Pointer(p)) 形参
// 会令 p 在调用期间保持存活，无需额外 runtime.KeepAlive。
const (
	sysnumOpenat   = 463
	sysnumRenameat = 465
	sysnumMkdirat  = 475
	sysnumUnlinkat = 472
)

// atRemoveDir 是 unlinkat 的「删目录」标志（XNU <sys/fcntl.h> AT_REMOVEDIR = 0x0080）。
const atRemoveDir = 0x80

func sysOpenat(dirfd int, path string, flags int, mode uint32) (int, error) {
	p, err := syscall.BytePtrFromString(path)
	if err != nil {
		return -1, err
	}
	r0, _, e := syscall.Syscall6(sysnumOpenat, uintptr(dirfd),
		uintptr(unsafe.Pointer(p)), uintptr(flags), uintptr(mode), 0, 0)
	if e != 0 {
		return -1, e
	}
	return int(r0), nil
}

func sysMkdirat(dirfd int, path string, mode uint32) error {
	p, err := syscall.BytePtrFromString(path)
	if err != nil {
		return err
	}
	if _, _, e := syscall.Syscall(sysnumMkdirat, uintptr(dirfd),
		uintptr(unsafe.Pointer(p)), uintptr(mode)); e != 0 {
		return e
	}
	return nil
}

func sysRenameat(oldfd int, oldpath string, newfd int, newpath string) error {
	from, err := syscall.BytePtrFromString(oldpath)
	if err != nil {
		return err
	}
	to, err := syscall.BytePtrFromString(newpath)
	if err != nil {
		return err
	}
	if _, _, e := syscall.Syscall6(sysnumRenameat, uintptr(oldfd),
		uintptr(unsafe.Pointer(from)), uintptr(newfd), uintptr(unsafe.Pointer(to)), 0, 0); e != 0 {
		return e
	}
	return nil
}

func sysUnlinkat(dirfd int, path string, flags int) error {
	p, err := syscall.BytePtrFromString(path)
	if err != nil {
		return err
	}
	if _, _, e := syscall.Syscall(sysnumUnlinkat, uintptr(dirfd),
		uintptr(unsafe.Pointer(p)), uintptr(flags)); e != 0 {
		return e
	}
	return nil
}
