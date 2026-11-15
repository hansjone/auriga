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
	if credentials == nil || credentials.Uid != 65532 || credentials.Gid != 65532 {
		return fmt.Errorf("projector peer denied")
	}
	return nil
}

// Listen serves the private edge control volume. Startup refuses a wrong UID,
// writable-to-others volume or symlink. A per-reader lock serializes crash
// recovery of a stale socket. It grants no filesystem operations to peers.
func (r *Reader) Listen(ctx context.Context) error {
	if r.Scope.Component != "edge" || os.Geteuid() != 65532 || os.Getegid() != 65532 {
		return fmt.Errorf("edge reader requires UID 65532")
	}
	noNewPrivileges, e := unix.PrctlRetInt(unix.PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0)
	if e != nil || noNewPrivileges != 1 {
		return fmt.Errorf("edge reader requires no-new-privileges")
	}
	header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	var capabilities [2]unix.CapUserData
	if e = unix.Capget(&header, &capabilities[0]); e != nil {
		return e
	}
	for _, set := range capabilities {
		if set.Effective != 0 || set.Permitted != 0 || set.Inheritable != 0 {
			return fmt.Errorf("edge reader requires cap_drop ALL")
		}
	}
	var info os.FileInfo
	var st unix.Stat_t
	for _, path := range []string{r.Root, r.Control, r.State} {
		info, e = os.Lstat(path)
		if e != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
			return fmt.Errorf("invalid private reader volume")
		}
		if e = unix.Lstat(path, &st); e != nil || st.Uid != 65532 || st.Gid != 65532 {
			return fmt.Errorf("invalid reader volume owner")
		}
	}
	if !independentRoots(r.State, r.Control, r.Root) {
		return fmt.Errorf("reader state volume must be private")
	}
	fd, e := unix.Open(filepath.Join(r.State, "reader.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
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
		if e = unix.Lstat(path, &st); e != nil || st.Uid != 65532 || st.Gid != 65532 {
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
	if e = os.Chmod(path, 0600); e != nil {
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
