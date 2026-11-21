//go:build !windows

package main

import (
	"os"
	"syscall"
)

func sharedSeedMetadata(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.Mode().IsRegular() && info.Mode().Perm()&0077 == 0 && st.Nlink == 1 && (st.Uid == 0 || st.Uid == uint32(os.Geteuid()))
}
