package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

func rotationSupported() bool { return os.Getuid() == 0 && os.Geteuid() == 0 }

func rotationDirectory(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && activationMetadata(info, true)
}

func rotationRuntimeRead(path string, uid int) ([]byte, error) {
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || (info.Mode().Perm()&0022 != 0 && info.Mode()&os.ModeSticky == 0) {
			return nil, fmt.Errorf("rotation runtime parent refused")
		}
		if stat, ok := info.Sys().(*syscall.Stat_t); !ok || (stat.Uid != 0 && stat.Uid != uint32(uid)) {
			return nil, fmt.Errorf("rotation runtime ancestor owner refused")
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm() != 0600 || before.Size() < 1 || before.Size() > 1<<20 {
		return nil, fmt.Errorf("rotation runtime file refused")
	}
	st, ok := before.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != uint32(uid) || st.Gid != uint32(uid) || st.Nlink != 1 {
		return nil, fmt.Errorf("rotation runtime ownership/link refused")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return nil, fmt.Errorf("rotation runtime changed")
	}
	raw, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	final, statErr := os.Lstat(path)
	if err != nil || statErr != nil || !os.SameFile(opened, final) || final.Size() != int64(len(raw)) || final.ModTime() != before.ModTime() {
		clear(raw)
		return nil, fmt.Errorf("rotation runtime changed during read")
	}
	return raw, nil
}

func rotationOwnRuntime(stage, state string) error {
	for _, name := range []string{"config", "network"} {
		uid := 10002
		if name == "network" {
			uid = 10001
		}
		if err := filepath.WalkDir(filepath.Join(stage, name), func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if err := os.Chown(path, uid, uid); err != nil {
				return err
			}
			mode := os.FileMode(0600)
			if entry.IsDir() {
				mode = 0700
			}
			return os.Chmod(path, mode)
		}); err != nil {
			return err
		}
	}
	// installState atomically creates a new state inode as the local installer, preserving the lock.
	return rotationOwnState(state)
}

func rotationOwnState(state string) error { return os.Chown(state, 10002, 10002) }

func rotationRuntimeDirectory(path string, uid int) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Uid == uint32(uid) && st.Gid == uint32(uid)
}

func rotationStateLock(state string) error {
	info, err := os.Lstat(state + ".lock")
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return fmt.Errorf("rotation original state lock metadata refused")
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 10002 || st.Gid != 10002 || st.Nlink != 1 {
		return fmt.Errorf("rotation original state lock ownership/link refused")
	}
	return nil
}

func rotationInputParents(path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for parent := filepath.Dir(absolute); ; parent = filepath.Dir(parent) {
		info, err := os.Lstat(parent)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("rotation input parent refused")
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		// System ancestors are trusted too; the leaf remains caller-owned and
		// public operations separately require both real and effective root.
		if !ok || (st.Uid != 0 && st.Uid != uint32(os.Getuid())) || (info.Mode().Perm()&0022 != 0 && info.Mode()&os.ModeSticky == 0) {
			return fmt.Errorf("rotation input ancestor DAC refused")
		}
		if parent == filepath.Dir(parent) {
			break
		}
	}
	return nil
}
