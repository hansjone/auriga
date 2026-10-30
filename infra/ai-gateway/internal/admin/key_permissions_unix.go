//go:build !windows

package admin

import (
	"errors"
	"os"
	"syscall"
)

func checkKeyFile(f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0077 != 0 {
		return errors.New("AI master key is accessible by group or others")
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Nlink != 1 {
		return errors.New("AI master key must have exactly one hard link")
	}
	return nil
}
