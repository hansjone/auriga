package main

import (
	"os"
	"path/filepath"
	"syscall"
)

func collabSourceMetadata(info os.FileInfo, secret bool) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Uid == uint32(os.Getuid()) && st.Nlink == 1 &&
		info.Mode().Perm()&0022 == 0 && (!secret || info.Mode().Perm()&0077 == 0)
}

func validateCollabSource(out string) bool {
	dir := filepath.Join(out, "collab-installation")
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
		return false
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != len(collabLeaves) {
		return false
	}
	for _, name := range collabLeaves {
		info, err := os.Lstat(filepath.Join(dir, name))
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || !collabSourceMetadata(info, true) {
			return false
		}
	}
	return true
}
