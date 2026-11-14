//go:build linux

package projection

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

func projectorPeer(c *net.UnixConn) error {
	raw, e := c.SyscallConn()
	if e != nil {
		return e
	}
	var credentials *unix.Ucred
	var socketErr error
	e = raw.Control(func(fd uintptr) {
		credentials, socketErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	})
	if e != nil {
		return e
	}
	if socketErr != nil {
		return socketErr
	}
	if credentials == nil || credentials.Uid != 10001 {
		return fmt.Errorf("projector peer denied")
	}
	return nil
}

// Listen serves the private edge control volume. Startup refuses a wrong UID,
// writable-to-others volume or symlink. A per-reader lock serializes crash
// recovery of a stale socket. It grants no filesystem operations to peers.
func (r *Reader) Listen(ctx context.Context) error {
	if r.Scope.Component != "edge" || os.Geteuid() != 65532 {
		return fmt.Errorf("edge reader requires UID 65532")
	}
	info, e := os.Lstat(r.Control)
	if e != nil || !info.IsDir() || info.Mode().Perm() != 0710 {
		return fmt.Errorf("invalid private control directory")
	}
	var st unix.Stat_t
	if e = unix.Lstat(r.Control, &st); e != nil || st.Uid != 65532 || st.Gid != 10001 {
		return fmt.Errorf("invalid control volume owner")
	}
	fd, e := unix.Open(filepath.Join(r.Control, "reader.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if e != nil {
		return e
	}
	defer func() { _ = unix.Close(fd) }()
	if e = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); e != nil {
		return fmt.Errorf("reader already running")
	}
	defer func() { _ = unix.Flock(fd, unix.LOCK_UN) }()
	path := filepath.Join(r.Control, "reload.sock")
	if info, e = os.Lstat(path); e == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("invalid control endpoint")
		}
		if e = unix.Lstat(path, &st); e != nil || st.Uid != 65532 || st.Gid != 10001 {
			return fmt.Errorf("invalid socket owner")
		}
		connection, dialErr := net.DialTimeout("unix", path, time.Second)
		if dialErr == nil {
			_ = connection.Close()
			return fmt.Errorf("reader already listening")
		}
		if !errors.Is(dialErr, unix.ECONNREFUSED) {
			return fmt.Errorf("cannot verify stale socket")
		}
		if e = os.Remove(path); e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	listener, e := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if e != nil {
		return e
	}
	defer func() { _ = listener.Close() }()
	if e = os.Chown(path, 65532, 10001); e != nil {
		return e
	}
	if e = os.Chmod(path, 0660); e != nil {
		return e
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = listener.Close()
		case <-done:
		}
	}()
	slots := make(chan struct{}, 32)
	for {
		conn, e := listener.AcceptUnix()
		if e != nil {
			if ctx.Err() != nil {
				return nil
			}
			return e
		}
		if e = projectorPeer(conn); e != nil {
			_ = conn.Close()
			continue
		}
		select {
		case slots <- struct{}{}:
			go func() { defer func() { <-slots }(); r.serveConnection(conn) }()
		default:
			_ = conn.Close()
		}
	}
}
