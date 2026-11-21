package main

import (
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"os"
)

func sharedRoleReadLeaf(path string, uid uint32, mode os.FileMode, limit int64) ([]byte, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer func() { _ = f.Close() }()
	var before, after, pathStat unix.Stat_t
	valid := func(s unix.Stat_t) bool {
		return s.Uid == uid && s.Gid == uid && s.Nlink == 1 && s.Mode&unix.S_IFMT == unix.S_IFREG && s.Mode&07777 == uint32(mode) && s.Size > 0 && s.Size <= limit
	}
	if unix.Fstat(fd, &before) != nil || !valid(before) {
		return nil, fmt.Errorf("shared role leaf metadata refused")
	}
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if unix.Fstat(fd, &after) != nil || unix.Lstat(path, &pathStat) != nil || !valid(after) || !sameNASMetadata(before, after) || !sameNASMetadata(after, pathStat) || before.Size != after.Size || before.Mtim != after.Mtim || before.Ctim != after.Ctim || int64(len(raw)) > limit {
		clear(raw)
		return nil, fmt.Errorf("shared role leaf changed")
	}
	return raw, nil
}

func sharedRoleDirectory(path string, uid uint32, mode uint32) error {
	var s unix.Stat_t
	if unix.Lstat(path, &s) != nil || s.Mode&unix.S_IFMT != unix.S_IFDIR || s.Mode&07777 != mode || s.Uid != uid || s.Gid != uid {
		return fmt.Errorf("shared role directory metadata refused")
	}
	return nil
}

func sharedRoleCreate(path string, file sharedProjectedFile) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	writeErr := f.Chown(int(file.UID), int(file.UID))
	if writeErr == nil {
		writeErr = f.Chmod(file.Mode)
	}
	if writeErr == nil {
		_, writeErr = f.Write(file.Body)
	}
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}
