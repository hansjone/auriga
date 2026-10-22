//go:build unix

package identity

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func syncTrustGuard(f *os.File, dir string) error {
	return syncGuardDirectories(f.Sync, syncGuardDirectory, dir)
}

// Linux fsync(file) does not persist its directory entry. Flush the guard first,
// then every physical parent through the path root, including root. Callers such
// as CLI ensureLayout may have just created the entire directory chain before
// this function runs; seeing a directory already exist is not a durability proof.
// No database may be opened until all barriers succeed.
func syncGuardDirectories(syncFile func() error, syncDir func(string) error, dir string) error {
	if err := syncFile(); err != nil {
		return fmt.Errorf("sync initialization guard file: %w", err)
	}
	for current := dir; ; current = filepath.Dir(current) {
		if err := syncDir(current); err != nil {
			return fmt.Errorf("sync initialization directory %q: %w", current, err)
		}
		if filepath.Dir(current) == current {
			return nil
		}
	}
}

func syncGuardDirectory(path string) error {
	return syncGuardDirectoryWithOpen(path, func(path string) (guardDirectoryHandle, error) { return os.Open(path) })
}

type guardDirectoryHandle interface {
	Sync() error
	Close() error
}

func syncGuardDirectoryWithOpen(path string, open func(string) (guardDirectoryHandle, error)) error {
	f, err := open(path)
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}
