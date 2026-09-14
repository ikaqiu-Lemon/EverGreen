//go:build linux || darwin

package txn

// [S5] 平台适配层的**编译反证**（single point of truth for signatures）。
//
// 目的：把 sysOpenat / sysMkdirat / sysRenameat 的**唯一权威签名**钉在这里。任何平台文件
// （syscall_linux.go / syscall_darwin.go）若签名漂移（参数或返回类型不一致），本文件会
// 在该平台上编译失败——从而在 CI 的四平台编译门禁里立刻暴露，而不是留到运行期。
//
// 这也是「无论走哪个平台实现，调用点看到的都是同一份契约」的静态保证：Linux 版透传标准库、
// Darwin 版走 Syscall6，但对 safedir.go / id.go / journal.go / lock.go 完全等价。
var (
	_ func(dirfd int, path string, flags int, mode uint32) (int, error) = sysOpenat
	_ func(dirfd int, path string, mode uint32) error                   = sysMkdirat
	_ func(oldfd int, oldpath string, newfd int, newpath string) error  = sysRenameat
	_ func(dirfd int, path string, flags int) error                     = sysUnlinkat
)
