//go:build unix

package ingress

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
)

// ListenLocal opens a Unix domain socket at path (removes stale file first).
func ListenLocal(path string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen unix %s: %w", path, err)
	}
	_ = os.Chmod(path, 0o600)
	return ln, nil
}
