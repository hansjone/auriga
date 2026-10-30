package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func panelOwnerEnvironment(out string) ([]byte, error) {
	info, err := os.Lstat(filepath.Join(out, "panel-signing.key"))
	if err != nil {
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("invalid panel signing key owner")
	}
	return fmt.Appendf(nil, "AURIGA_PANEL_UID=%d\nAURIGA_PANEL_GID=%d\n", stat.Uid, stat.Gid), nil
}
