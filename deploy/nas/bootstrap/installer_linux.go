package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// Only an empty newly created source root is handed to the fixed NAS installer.
// Existing owners/permissions are checked, never repaired.
func runAsInstaller(out string, raw []byte, peer, subnet string, registry int) error {
	if !filepath.IsAbs(out) || filepath.Clean(out) != out || strings.Contains(out, "\\") {
		return fmt.Errorf("NAS material root must be a canonical absolute path")
	}
	if os.Geteuid() != 0 {
		if os.Geteuid() != 10001 || os.Getegid() != 10001 {
			return fmt.Errorf("NAS material activation requires root or the fixed installer uid10001")
		}
		return nil
	}
	// O_NOFOLLOW rejects every existing ancestor link.
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	for _, part := range strings.Split(strings.TrimPrefix(filepath.Dir(out), "/"), "/") {
		if part == "" {
			continue
		}
		next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return fmt.Errorf("unsafe NAS material ancestor")
		}
		var st unix.Stat_t
		statErr := unix.Fstat(next, &st)
		if statErr != nil || (st.Uid != 0 && st.Uid != 10001) || (st.Mode&0o022 != 0 && (st.Uid != 0 || st.Mode&unix.S_ISVTX == 0)) {
			_ = unix.Close(next)
			return fmt.Errorf("untrusted NAS material ancestor")
		}
		_ = unix.Close(fd)
		fd = next
	}
	leaf := filepath.Base(out)
	created := false
	if err := unix.Mkdirat(fd, leaf, 0o755); err == nil {
		created = true
	} else if err != unix.EEXIST {
		return fmt.Errorf("create NAS source root")
	}
	child, err := unix.Openat(fd, leaf, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("invalid NAS source root")
	}
	defer unix.Close(child)
	var before unix.Stat_t
	if unix.Fstat(child, &before) != nil || before.Mode&0o7777 != 0o755 {
		return fmt.Errorf("invalid NAS source root mode")
	}
	if created {
		duplicate, dupErr := unix.Dup(child)
		if dupErr != nil {
			return dupErr
		}
		f := os.NewFile(uintptr(duplicate), "NAS source root")
		names, err := f.Readdirnames(1)
		_ = f.Close()
		if err != nil && err != io.EOF {
			return fmt.Errorf("inspect new NAS source root")
		}
		if len(names) != 0 || before.Uid != 0 {
			return fmt.Errorf("new NAS source root is not empty/root-owned")
		}
		if unix.Fchown(child, 10001, 10001) != nil {
			return fmt.Errorf("set new NAS source installer")
		}
	} else if before.Uid != 10001 || before.Gid != 10001 {
		return fmt.Errorf("existing NAS source root has wrong owner; refusing repair")
	}
	var after, path unix.Stat_t
	if unix.Fstat(child, &after) != nil || unix.Fstatat(fd, leaf, &path, unix.AT_SYMLINK_NOFOLLOW) != nil || after.Dev != path.Dev || after.Ino != path.Ino || after.Uid != 10001 || after.Gid != 10001 || after.Mode != before.Mode {
		return fmt.Errorf("changed NAS source root")
	}
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(binary, "-out", out, "-access-file", "-", "-edge-peer-ip", peer, "-internal-subnet", subnet, "-registry-port", fmt.Sprint(registry))
	cmd.Stdin = bytes.NewReader(raw)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 10001, Gid: 10001, Groups: []uint32{}}}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("fixed NAS installer failed")
	}
	os.Exit(0)
	return nil
}
