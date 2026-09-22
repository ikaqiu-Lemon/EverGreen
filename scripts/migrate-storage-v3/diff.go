package main

import (
	"bytes"
	"fmt"
	"strings"
)

// unifiedDiff deliberately emits one deterministic whole-file hunk. Migration
// reports favor complete auditability over compact edit scripts.
func unifiedDiff(path string, before, after []byte) string {
	if bytes.Equal(before, after) {
		return ""
	}
	oldLines := splitDiffLines(before)
	newLines := splitDiffLines(after)
	var out strings.Builder
	fmt.Fprintf(&out, "--- a/%s\n+++ b/%s\n", path, path)
	fmt.Fprintf(&out, "@@ -1,%d +1,%d @@\n", len(oldLines), len(newLines))
	for _, line := range oldLines {
		out.WriteByte('-')
		out.WriteString(line)
		out.WriteByte('\n')
	}
	for _, line := range newLines {
		out.WriteByte('+')
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.String()
}

func splitDiffLines(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	lines := strings.Split(string(raw), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
