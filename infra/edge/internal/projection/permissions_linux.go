//go:build linux

package projection

import (
	"fmt"
	"os"
	"syscall"
)

func privateInfo(info os.FileInfo, directory bool) error {
	mode := os.FileMode(0600)
	if directory {
		mode = 0700
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) || stat.Gid != uint32(os.Getegid()) || info.Mode().Perm() != mode {
		return fmt.Errorf("projection permission denied")
	}
	return nil
}
