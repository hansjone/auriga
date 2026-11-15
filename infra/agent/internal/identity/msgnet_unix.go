//go:build unix

package identity

import (
	"fmt"
	"os"
	"syscall"
)

func checkPrivateKeyFile(path string) error {
	i, err := os.Lstat(path)
	if err != nil {
		return err
	}
	s, ok := i.Sys().(*syscall.Stat_t)
	if !ok || s.Uid != uint32(os.Geteuid()) || i.Mode().Perm() != 0600 {
		return fmt.Errorf("private key permission denied")
	}
	return nil
}
