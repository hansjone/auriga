//go:build windows && amd64

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

type pathLocks []windows.Handle

func (locks pathLocks) close() {
	for i := len(locks) - 1; i >= 0; i-- {
		_ = windows.CloseHandle(locks[i])
	}
}

// Each ancestor is held without FILE_SHARE_DELETE; no component may be renamed into
// a junction between validation and the privileged operation. Mounted quota volumes
// are accessed by their validated volume GUID, not through a host reparse point.
func lockPlainPath(path string) (pathLocks, error) {
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("absolute path required")
	}
	volume := filepath.VolumeName(path)
	tail := strings.TrimPrefix(path, volume)
	parts := strings.Split(strings.Trim(tail, `\`), `\`)
	current := volume + `\`
	var locks pathLocks
	paths := []string{current}
	for _, part := range parts {
		if part != "" {
			current = filepath.Join(current, part)
			paths = append(paths, current)
		}
	}
	for _, p := range paths {
		h, e := windows.CreateFile(u16(p), windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
		if e != nil {
			locks.close()
			return nil, e
		}
		var info windows.ByHandleFileInformation
		e = windows.GetFileInformationByHandle(h, &info)
		if e != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			_ = windows.CloseHandle(h)
			locks.close()
			if e != nil {
				return nil, e
			}
			return nil, fmt.Errorf("reparse point rejected: %s", p)
		}
		locks = append(locks, h)
	}
	return locks, nil
}

// Delete by the verified object handle. A reparse point fails closed rather than
// being traversed. Concurrent creation causes a nonempty-directory error and retry.
func safeRemoveTree(path string) error {
	if _, e := os.Lstat(path); os.IsNotExist(e) {
		return nil
	} else if e != nil {
		return e
	}
	ancestors, e := lockPlainPath(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer ancestors.close()
	return removePlainObject(path)
}
func removePlainObject(path string) error {
	h, e := windows.CreateFile(u16(path), windows.DELETE|windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if errors.Is(e, windows.ERROR_FILE_NOT_FOUND) || errors.Is(e, windows.ERROR_PATH_NOT_FOUND) {
		return nil
	}
	if e != nil {
		return e
	}
	defer windows.CloseHandle(h)
	var info windows.ByHandleFileInformation
	if e = windows.GetFileInformationByHandle(h, &info); e != nil {
		return e
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("refuse recursive deletion through reparse point: %s", path)
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		entries, e := os.ReadDir(path)
		if e != nil {
			return e
		}
		for _, entry := range entries {
			if e = removePlainObject(filepath.Join(path, entry.Name())); e != nil {
				return e
			}
		}
	}
	disposition := byte(1)
	return windows.SetFileInformationByHandle(h, 4, (*byte)(unsafe.Pointer(&disposition)), 1)
}
func internalWorkerGate() error {
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	root := filepath.Dir(filepath.Dir(exe))
	id := strings.TrimPrefix(filepath.Base(root), "auriga-s3-")
	o := options{Root: root, ID: id}
	if e = validateOptions(o); e != nil {
		return e
	}
	if !strings.EqualFold(exe, probePath(o)) {
		return fmt.Errorf("worker must use staged probe")
	}
	locks, e := lockPlainPath(root)
	if e != nil {
		return e
	}
	defer locks.close()
	b, e := os.ReadFile(filepath.Join(root, "ci.guard"))
	if e != nil {
		return e
	}
	if string(b) != id {
		return fmt.Errorf("invalid disposable run guard")
	}
	return nil
}
