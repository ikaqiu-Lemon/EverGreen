//go:build !windows

package credentialperm

import (
	"os"
)

// Protect 收紧配置和凭据权限，仅允许当前本机用户读写。
func Protect(path string, directory bool) error {
	mode := os.FileMode(0600)
	if directory {
		mode = 0700
	}
	return os.Chmod(path, mode)
}

func Valid(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && info.Mode().Perm()&0077 == 0
}
