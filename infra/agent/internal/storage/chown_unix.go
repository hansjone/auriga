//go:build unix

package storage

import "os"

// Lchown is os.Lchown (does not follow symlinks).
func Lchown(path string, uid, gid int) error {
	return os.Lchown(path, uid, gid)
}
