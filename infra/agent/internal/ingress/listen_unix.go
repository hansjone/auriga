//go:build unix

package ingress

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
)

const localDirMarker = ".auriga-ingress-v1"

// ListenLocal opens a Unix domain socket at path (removes stale file first).
// With gid < 0 only the agent's user can connect (dir 0700, socket 0600).
// With gid >= 0 the socket directory and socket are shared with that group
// (dir 0750, socket 0660), but only in an agent-created ingress/ subdirectory.
func ListenLocal(path string, gid int) (net.Listener, error) {
	dir := filepath.Dir(path)
	dirMode, sockMode := os.FileMode(0o700), os.FileMode(0o600)
	if gid >= 0 {
		dirMode, sockMode = 0o750, 0o660
		if err := prepareSharedLocalDir(path); err != nil {
			return nil, fmt.Errorf("unsafe shared ingress directory %s: %w", dir, err)
		}
		if err := os.Chown(dir, -1, gid); err != nil {
			return nil, fmt.Errorf("chown %s: %w", dir, err)
		}
		if err := os.Chmod(dir, dirMode); err != nil {
			return nil, err
		}
	} else if err := os.MkdirAll(dir, dirMode); err != nil {
		return nil, err
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

// The parent must already exist. Never adopt an arbitrary existing directory,
// even an empty one: only a private marker created here permits a restart.
func prepareSharedLocalDir(path string) error {
	dir := filepath.Dir(path)
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Base(dir) != "ingress" || filepath.Base(path) != "ingress.sock" {
		return fmt.Errorf("group sharing requires an absolute <parent>/ingress/ingress.sock path")
	}
	parent := filepath.Dir(dir)
	// No symlink component or untrusted writable ancestor may redirect chmod/chown.
	for current := parent; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.IsDir() || (int(stat.Uid) != os.Geteuid() && stat.Uid != 0) {
			return fmt.Errorf("parent %s must be a real directory owned by the agent or root", current)
		}
		// Root-owned sticky /tmp is safe as an ancestor of a private agent-owned parent,
		// but must never be the direct parent of the shared ingress directory.
		if info.Mode().Perm()&0o022 != 0 && (current == parent || stat.Uid != 0 || info.Mode()&os.ModeSticky == 0) {
			return fmt.Errorf("parent %s must not be group/other writable", current)
		}
		if current == filepath.Dir(current) {
			break
		}
	}
	if err := os.Mkdir(dir, 0o700); err == nil {
		marker, err := os.OpenFile(filepath.Join(dir, localDirMarker), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		if err := marker.Close(); err != nil {
			return err
		}
	} else if !os.IsExist(err) {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || int(stat.Uid) != os.Geteuid() || info.Mode().Perm()&0o027 != 0 {
		return fmt.Errorf("ingress must be agent-owned, without group write or other permissions")
	}
	marker, err := os.Lstat(filepath.Join(dir, localDirMarker))
	if err != nil {
		return fmt.Errorf("directory was not created by the agent (missing private marker): %w", err)
	}
	markerStat, ok := marker.Sys().(*syscall.Stat_t)
	if !ok || !marker.Mode().IsRegular() || marker.Mode().Perm() != 0o600 || int(markerStat.Uid) != os.Geteuid() || markerStat.Nlink != 1 || marker.Size() != 0 {
		return fmt.Errorf("invalid agent directory marker")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == localDirMarker {
			continue
		}
		if entry.Name() == "ingress.sock" {
			socket, err := os.Lstat(path)
			if err != nil {
				return err
			}
			socketStat, ok := socket.Sys().(*syscall.Stat_t)
			if ok && socket.Mode()&os.ModeSocket != 0 && int(socketStat.Uid) == os.Geteuid() {
				continue
			}
		}
		return fmt.Errorf("ingress contains unexpected entry %q; only the agent socket and marker are allowed", entry.Name())
	}
	return nil
}
