//go:build linux

package txn

import (
	"syscall"
	"unsafe"
)

// [S5] Linux 平台适配层（单点）——直接委托标准库 syscall 的 *at 族封装。
//
// 之所以要这层薄封装：darwin 标准库 syscall **没有导出** Openat/Mkdirat/Renameat，
// 只提供 libc 版内部符号。为在「不新增 go.mod 直接依赖」的前提下让四平台都能静态编译，
// 把这三个 fd-relative 系统调用收敛到 sysOpenat/sysMkdirat/sysRenameat 单点，
// 各平台各自实现（见 syscall_darwin.go）。本文件在 Linux（amd64 / arm64）编译。
//
// **安全语义不变**：no-follow / dirfd 相对寻址完全由调用方传入的 flags 与 dirfd 决定，
// 本层只做透传，不放宽任何 O_NOFOLLOW / O_DIRECTORY 语义。

func sysOpenat(dirfd int, path string, flags int, mode uint32) (int, error) {
	return syscall.Openat(dirfd, path, flags, mode)
}

func sysMkdirat(dirfd int, path string, mode uint32) error {
	return syscall.Mkdirat(dirfd, path, mode)
}

func sysRenameat(oldfd int, oldpath string, newfd int, newpath string) error {
	return syscall.Renameat(oldfd, oldpath, newfd, newpath)
}

func sysUnlinkat(dirfd int, path string, flags int) error {
	// 标准库 syscall.Unlinkat（linux）是**不带 flags 的已废弃 2 参形态**，无法传 AT_REMOVEDIR；
	// 因此这里直接经 SYS_UNLINKAT 发起 3 参系统调用（与 darwin 版语义一致），不新增依赖。
	p, err := syscall.BytePtrFromString(path)
	if err != nil {
		return err
	}
	if _, _, e := syscall.Syscall(syscall.SYS_UNLINKAT, uintptr(dirfd),
		uintptr(unsafe.Pointer(p)), uintptr(flags)); e != 0 {
		return e
	}
	return nil
}

// atRemoveDir 是 unlinkat 的「删目录」标志（Linux 恒 0x200）。
const atRemoveDir = 0x200
