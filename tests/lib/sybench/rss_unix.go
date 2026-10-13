//go:build darwin || linux

package main

import (
	"runtime"
	"syscall"
)

func maxRSSBytes() uint64 {
	var usage syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &usage) != nil || usage.Maxrss < 0 {
		return 0
	}
	value := uint64(usage.Maxrss)
	if runtime.GOOS == "linux" {
		value *= 1024
	}
	return value
}
