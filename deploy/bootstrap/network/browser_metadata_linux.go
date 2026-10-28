package network

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// ValidateBrowserSource checks the installer's source metadata without repairs.
func ValidateBrowserSource(out string, enabled bool) error {
	dir := filepath.Join(out, "browser-tls")
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
		return fmt.Errorf("invalid managed HTTPS metadata")
	}
	entries, err := os.ReadDir(dir)
	want := 0
	if enabled {
		want = 2
	}
	if err != nil || len(entries) != want {
		return fmt.Errorf("invalid managed HTTPS metadata")
	}
	for _, entry := range entries {
		mode := os.FileMode(0644)
		if entry.Name() == "browser.key" {
			mode = 0600
		} else if entry.Name() != "browser.crt" {
			return fmt.Errorf("invalid managed HTTPS metadata")
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != mode {
			return fmt.Errorf("invalid managed HTTPS metadata")
		}
		st := info.Sys().(*syscall.Stat_t)
		if st.Uid != uint32(os.Getuid()) || st.Nlink != 1 {
			return fmt.Errorf("invalid managed HTTPS metadata")
		}
	}
	return nil
}
