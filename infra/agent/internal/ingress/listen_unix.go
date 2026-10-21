//go:build unix

package ingress

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	localDirMarker       = ".auriga-ingress-v1"
	localDirTempPrefix   = ".auriga-ingress-tmp-"
	localDirLegacyPrefix = ".auriga-ingress-legacy-"
)

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
		return fmt.Errorf("group sharing (AURIGA_AGENT_INGRESS_LOCAL_GID) requires an absolute <parent>/ingress/ingress.sock path such as /var/lib/auriga/ingress/ingress.sock, got %q", path)
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
	removeStaleTemporaryDirs(parent)
	if _, err := os.Lstat(dir); os.IsNotExist(err) {
		if err := createSharedLocalDir(parent, dir); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if _, err := os.Lstat(filepath.Join(dir, localDirMarker)); os.IsNotExist(err) && legacySharedLocalDir(dir) {
		// Upgrade from agents that predate the marker: never adopt the old directory or
		// touch its permissions; move it aside and create a fresh private directory.
		backup := filepath.Join(parent, fmt.Sprintf("%s%d", localDirLegacyPrefix, time.Now().UnixNano()))
		if err := os.Rename(dir, backup); err != nil {
			return fmt.Errorf("move legacy ingress directory aside: %w", err)
		}
		if err := createSharedLocalDir(parent, dir); err != nil {
			return err
		}
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

// createSharedLocalDir publishes the directory and its marker in one rename, so a
// crash or a restrictive umask can never leave an unmarked directory behind.
func createSharedLocalDir(parent, dir string) (err error) {
	tmp, err := os.MkdirTemp(parent, localDirTempPrefix)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(tmp)
		}
	}()
	// Explicit modes: MkdirTemp/OpenFile results depend on the process umask.
	if err = os.Chmod(tmp, 0o700); err != nil {
		return err
	}
	marker, err := os.OpenFile(filepath.Join(tmp, localDirMarker), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if err = marker.Chmod(0o600); err != nil {
		_ = marker.Close()
		return err
	}
	if err = marker.Close(); err != nil {
		return err
	}
	// rename(2) silently replaces an empty directory; the parent is writable only by
	// the agent or root (checked above), so this existence check cannot be raced.
	if _, statErr := os.Lstat(dir); !os.IsNotExist(statErr) {
		err = fmt.Errorf("ingress appeared during creation")
		return err
	}
	err = os.Rename(tmp, dir)
	return err
}

// legacySharedLocalDir recognises the layout written by earlier agents: a real,
// agent-owned directory holding nothing but (optionally) the agent's own socket.
func legacySharedLocalDir(dir string) bool {
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		return false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.Name() != "ingress.sock" {
			return false
		}
		socket, err := os.Lstat(filepath.Join(dir, entry.Name()))
		if err != nil {
			return false
		}
		socketStat, ok := socket.Sys().(*syscall.Stat_t)
		if !ok || socket.Mode()&os.ModeSocket == 0 || int(socketStat.Uid) != os.Geteuid() {
			return false
		}
	}
	return true
}

// removeStaleTemporaryDirs drops agent-owned leftovers of an interrupted creation.
func removeStaleTemporaryDirs(parent string) {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), localDirTempPrefix) {
			continue
		}
		path := filepath.Join(parent, entry.Name())
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() {
			continue
		}
		if stat, ok := info.Sys().(*syscall.Stat_t); ok && int(stat.Uid) == os.Geteuid() {
			_ = os.RemoveAll(path)
		}
	}
}
