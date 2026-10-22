//go:build windows

package identity

import (
	"fmt"
	"os"
)

// Go File.Sync calls FlushFileBuffers on this writable file handle. Propagate
// its real errors. This retains Windows' per-file flush behavior; it does not
// claim Linux directory-chain fsync semantics or power-loss proof for arbitrary
// Windows filesystems. Do not open directory/volume handles or request privileges.
func syncTrustGuard(f *os.File, _ string) error {
	return syncGuardFile(f.Sync)
}

func syncGuardFile(syncFile func() error) error {
	if err := syncFile(); err != nil {
		return fmt.Errorf("flush initialization guard file: %w", err)
	}
	return nil
}
