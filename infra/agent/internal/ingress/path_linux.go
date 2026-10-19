//go:build linux

package ingress

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

const resolveBeneath = 0x8 // RESOLVE_BENEATH

// OpenBeneath opens path under root, preferring openat2+RESOLVE_BENEATH.
func OpenBeneath(root, rel string, flags int, mode os.FileMode) (*os.File, error) {
	clean, err := ValidateRelPath(rel)
	if err != nil {
		return nil, err
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	dirfd, err := unix.Open(absRoot, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("%s: open root: %w", CodePathInvalid, err)
	}
	defer unix.Close(dirfd)

	how := &unix.OpenHow{
		Flags:   uint64(flags) | unix.O_CLOEXEC,
		Mode:    uint64(mode),
		Resolve: resolveBeneath,
	}
	fd, err := unix.Openat2(dirfd, clean, how)
	if err == nil {
		return os.NewFile(uintptr(fd), filepath.Join(absRoot, filepath.FromSlash(clean))), nil
	}
	// ENOSYS / EINVAL → walk openat fallback.
	if err != unix.ENOSYS && err != unix.EINVAL {
		return nil, fmt.Errorf("%s: openat2: %w", CodePathEscape, err)
	}
	return openBeneathWalk(dirfd, absRoot, clean, flags, mode)
}

func openBeneathWalk(rootFD int, absRoot, clean string, flags int, mode os.FileMode) (*os.File, error) {
	if clean == "." {
		fd, err := unix.Openat(rootFD, ".", flags|unix.O_CLOEXEC, uint32(mode))
		if err != nil {
			return nil, err
		}
		return os.NewFile(uintptr(fd), absRoot), nil
	}
	parts := strings.Split(clean, "/")
	cur := rootFD
	owned := false
	defer func() {
		if owned && cur != rootFD {
			unix.Close(cur)
		}
	}()
	for i, p := range parts {
		last := i == len(parts)-1
		fl := unix.O_PATH | unix.O_NOFOLLOW | unix.O_CLOEXEC
		if last {
			fl = flags | unix.O_NOFOLLOW | unix.O_CLOEXEC
		} else {
			fl |= unix.O_DIRECTORY
		}
		fd, err := unix.Openat(cur, p, fl, uint32(mode))
		if err != nil {
			return nil, fmt.Errorf("%s: openat walk: %w", CodePathEscape, err)
		}
		if owned && cur != rootFD {
			unix.Close(cur)
		}
		cur = fd
		owned = true
		if !last {
			continue
		}
		owned = false
		return os.NewFile(uintptr(fd), filepath.Join(absRoot, filepath.FromSlash(clean))), nil
	}
	return nil, fmt.Errorf("%s: empty path", CodePathInvalid)
}
