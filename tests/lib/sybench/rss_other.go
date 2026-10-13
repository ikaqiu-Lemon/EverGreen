//go:build !darwin && !linux

package main

func maxRSSBytes() uint64 {
	return 0
}
