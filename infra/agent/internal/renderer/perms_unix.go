//go:build unix

package renderer

import "os"

func restrictSecretFile(path string) error {
	return os.Chmod(path, 0o600)
}
