//go:build linux

package main

import (
	"fmt"
	"os"
	"syscall"
)

// canonicalFileIdentity deliberately excludes access time: ordinary reads may
// advance atime and must not invalidate the rolling append digest. Device,
// inode, and ctime detect replacement and same-size restored-mtime rewrites.
func canonicalFileIdentity(info os.FileInfo) string {
	if info == nil {
		return ""
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return ""
	}
	return fmt.Sprintf("%d:%d:%d:%d", stat.Dev, stat.Ino, stat.Ctim.Sec, stat.Ctim.Nsec)
}
