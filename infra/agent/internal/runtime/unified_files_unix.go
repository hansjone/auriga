//go:build !windows

package runtime

import (
	"os"
	"path/filepath"
)

func unifiedReplace(source, target string) error {
	if err := os.Rename(source, target); err != nil {
		return err
	}
	return unifiedSyncDirectory(filepath.Dir(target))
}
func unifiedSyncDirectory(directory string) error {
	file, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	return file.Sync()
}
