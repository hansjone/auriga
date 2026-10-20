//go:build linux

package ingress

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// OpenBeneath never follows symlinks and never blocks on FIFOs.
func OpenBeneath(root, rel string, flags int, mode os.FileMode) (*os.File, error) {
	return openBeneath(root, rel, flags, mode, false)
}

func createBeneath(root, rel string) (*os.File, error) {
	return openBeneath(root, rel, os.O_CREATE|os.O_WRONLY, 0o600, true)
}

func linuxPathError(err error) error {
	if errors.Is(err, unix.EXDEV) || errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) {
		return fmt.Errorf("%s: %w", CodePathEscape, err)
	}
	return err
}

func openBeneath(root, rel string, flags int, mode os.FileMode, parents bool) (*os.File, error) {
	clean, err := ValidateRelPath(rel)
	if err != nil {
		return nil, err
	}
	dirfd, err := unix.Open(root, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = unix.Close(dirfd) }()
	if !parents {
		how := &unix.OpenHow{Flags: uint64(flags | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC), Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS}
		if flags&os.O_CREATE != 0 {
			how.Mode = uint64(mode.Perm())
		}
		fd, err := unix.Openat2(dirfd, clean, how)
		if err == nil {
			return os.NewFile(uintptr(fd), filepath.Join(root, clean)), nil
		}
		if !errors.Is(err, unix.ENOSYS) && !errors.Is(err, unix.EINVAL) {
			return nil, linuxPathError(err)
		}
	}
	// mkdirat and every open are relative to a pinned directory, never a pathname.
	parts := strings.Split(clean, "/")
	for i, part := range parts {
		last := i == len(parts)-1
		fl := unix.O_PATH | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC
		if last {
			fl = flags | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC
		}
		fd, err := openAtBeneath(dirfd, part, fl, mode)
		if errors.Is(err, unix.ENOENT) && parents && !last {
			if e := unix.Mkdirat(dirfd, part, 0o700); e != nil && !errors.Is(e, unix.EEXIST) {
				return nil, e
			}
			fd, err = openAtBeneath(dirfd, part, fl, 0)
		}
		if err != nil {
			return nil, linuxPathError(err)
		}
		if last {
			return os.NewFile(uintptr(fd), filepath.Join(root, clean)), nil
		}
		_ = unix.Close(dirfd)
		dirfd = fd
	}
	return nil, fmt.Errorf("%s: empty path", CodePathInvalid)
}

func openAtBeneath(dirfd int, name string, flags int, mode os.FileMode) (int, error) {
	how := &unix.OpenHow{Flags: uint64(flags), Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS}
	if flags&unix.O_CREAT != 0 {
		how.Mode = uint64(mode.Perm())
	}
	fd, err := unix.Openat2(dirfd, name, how)
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) {
		return unix.Openat(dirfd, name, flags|unix.O_NOFOLLOW, uint32(mode.Perm()))
	}
	return fd, err
}
