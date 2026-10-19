//go:build windows

package storage

// Lchown is a no-op on Windows (ownership uses ACLs elsewhere).
func Lchown(path string, uid, gid int) error {
	_, _, _ = path, uid, gid
	return nil
}
