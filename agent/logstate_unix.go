//go:build !windows

package main

import (
	"fmt"
	"os"
	"syscall"
)

// fileKey identifies a file across renames: a rotated log keeps its inode while
// its path changes, and the next file takes the old path with a new inode.
func fileKey(info os.FileInfo) (string, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", false
	}
	return fmt.Sprintf("%d:%d", uint64(stat.Dev), uint64(stat.Ino)), true
}
