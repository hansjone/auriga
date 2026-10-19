//go:build unix

package identity

import "os"

func restrictFile(path string) error {
	return os.Chmod(path, 0o600)
}

func restrictDir(path string) error {
	return os.Chmod(path, 0o700)
}
