package collab

import (
	"os"
	"syscall"
)

func sourceMetadata(info os.FileInfo, secret bool) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Uid == uint32(os.Geteuid()) && st.Nlink == 1 && info.Mode().Perm()&0022 == 0 && (!secret || info.Mode().Perm()&0077 == 0)
}
