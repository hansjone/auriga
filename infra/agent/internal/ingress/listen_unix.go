//go:build unix

package ingress

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
)

// ListenLocal opens a Unix domain socket at path (removes stale file first).
// With gid < 0 only the agent's user can connect (dir 0700, socket 0600).
// With gid >= 0 the socket directory and socket are shared with that group
// (dir 0750, socket 0660) so a non-root edge in that group can connect; keep
// such a socket in its own directory, not inside the agent's private dir.
func ListenLocal(path string, gid int) (net.Listener, error) {
	dir := filepath.Dir(path)
	dirMode, sockMode := os.FileMode(0o700), os.FileMode(0o600)
	if gid >= 0 {
		dirMode, sockMode = 0o750, 0o660
	}
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return nil, err
	}
	if gid >= 0 {
		if err := os.Chown(dir, -1, gid); err != nil {
			return nil, fmt.Errorf("chown %s: %w", dir, err)
		}
		if err := os.Chmod(dir, dirMode); err != nil {
			return nil, err
		}
	}
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen unix %s: %w", path, err)
	}
	if gid >= 0 {
		if err := os.Chown(path, -1, gid); err != nil {
			_ = ln.Close()
			return nil, fmt.Errorf("chown %s: %w", path, err)
		}
	}
	if err := os.Chmod(path, sockMode); err != nil {
		_ = ln.Close()
		return nil, err
	}
	return ln, nil
}
