package runtimebundle

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// Open each component relative to a pinned descriptor; no symlink traversal,
// including ancestors, and O_NONBLOCK avoids blocking on hostile FIFO inputs.
func openAt(dirfd int, path string, directory bool) (*os.File, error) {
	parts := strings.Split(filepath.ToSlash(path), "/")
	owned := -1
	defer func() {
		if owned >= 0 {
			_ = unix.Close(owned)
		}
	}()
	for i, part := range parts {
		flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
		if i != len(parts)-1 || directory {
			flags |= unix.O_DIRECTORY
		}
		fd, err := unix.Openat(dirfd, part, flags, 0)
		if err != nil {
			return nil, err
		}
		if owned >= 0 {
			_ = unix.Close(owned)
		}
		owned, dirfd = fd, fd
	}
	f := os.NewFile(uintptr(owned), path)
	owned = -1
	return f, nil
}

func openOrdinary(path string, directory bool) (*os.File, error) {
	f, err := os.Open("/")
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	rel := strings.TrimPrefix(filepath.ToSlash(path), "/")
	if rel == "" {
		rel = "."
	}
	return openAt(int(f.Fd()), rel, directory)
}

func openMember(r *os.Root, name string, directory bool) (*os.File, error) {
	f, err := r.Open(".")
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return openAt(int(f.Fd()), name, directory)
}

func singleLink(f *os.File) error {
	var st unix.Stat_t
	if err := unix.Fstat(int(f.Fd()), &st); err != nil {
		return err
	}
	if st.Nlink != 1 {
		return fmt.Errorf("input hard links are forbidden")
	}
	return nil
}

func privatePermissions(f *os.File) error {
	st, err := f.Stat()
	if err != nil {
		return err
	}
	s, ok := st.Sys().(*syscall.Stat_t)
	if !ok || s.Uid != uint32(os.Geteuid()) || st.Mode().Perm()&0o077 != 0 || st.Mode().Perm()&0o400 == 0 || st.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return fmt.Errorf("private key must be owned by the current user with owner-only read permissions")
	}
	return singleLink(f)
}
