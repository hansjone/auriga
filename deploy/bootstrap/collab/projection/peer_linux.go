package projection

import (
	"errors"
	"golang.org/x/sys/unix"
	"net"
	"os"
	"path/filepath"
)

func checkPeer(c net.Conn, want uint32) error {
	u, ok := c.(*net.UnixConn)
	if !ok {
		return errors.New("projection: Unix peer required")
	}
	raw, e := u.SyscallConn()
	if e != nil {
		return e
	}
	var cred *unix.Ucred
	var inner error
	e = raw.Control(func(fd uintptr) { cred, inner = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) })
	if e != nil {
		return e
	}
	if inner != nil {
		return inner
	}
	if cred == nil || cred.Uid != want {
		return errors.New("projection: peer UID rejected")
	}
	return nil
}
func checkListenerUID(want uint32) error {
	if uint32(os.Geteuid()) != want || uint32(os.Getegid()) != want {
		return errors.New("projection: listener UID rejected")
	}
	return nil
}
func privateMetadata(path string, dir bool) error {
	var st unix.Stat_t
	if e := unix.Lstat(path, &st); e != nil {
		return e
	}
	want := uint32(unix.S_IFREG)
	if dir {
		want = unix.S_IFDIR
	}
	if st.Mode&unix.S_IFMT != want || st.Uid != uint32(os.Geteuid()) || st.Mode&0077 != 0 || (!dir && st.Nlink != 1) {
		return &os.PathError{Op: "verify private metadata", Path: path, Err: errors.New("projection: private file metadata rejected")}
	}
	return nil
}

func lockListener(control string) (func(), error) {
	return lockFile(filepath.Join(control, "reader.lock"))
}
func lockFile(path string) (func(), error) {
	fd, e := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if e != nil {
		return nil, e
	}
	var st unix.Stat_t
	if e = unix.Fstat(fd, &st); e != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 || st.Uid != uint32(os.Geteuid()) {
		_ = unix.Close(fd)
		return nil, errors.New("projection: invalid listener lock")
	}
	if e = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); e != nil {
		_ = unix.Close(fd)
		return nil, e
	}
	return func() { _ = unix.Close(fd) }, nil
}
func clearStaleSocket(path string, uid uint32) error {
	var st unix.Stat_t
	e := unix.Lstat(path, &st)
	if e == unix.ENOENT {
		return nil
	}
	if e != nil {
		return e
	}
	if st.Mode&unix.S_IFMT != unix.S_IFSOCK || st.Uid != uid {
		return errors.New("projection: invalid stale socket")
	}
	return os.Remove(path)
}
