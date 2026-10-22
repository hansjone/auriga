package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// NAS source keys have stricter metadata than the shared receipt's byte manifest.
// Hold the source and child directories across generation and receipt validation.
type nasMaterialGuard struct {
	path string
	dirs map[string]*os.File
}

func sameNASMetadata(a, b unix.Stat_t) bool {
	return a.Dev == b.Dev && a.Ino == b.Ino && a.Mode == b.Mode && a.Uid == b.Uid && a.Gid == b.Gid && a.Nlink == b.Nlink
}

func openNASDirectory(parent int, name string, source bool) (*os.File, error) {
	valid := func(s unix.Stat_t) bool {
		if s.Mode&unix.S_IFMT != unix.S_IFDIR {
			return false
		}
		if source {
			return s.Uid == 10001 && s.Gid == 10001 && s.Mode&0o7777 == 0o755
		}
		return (s.Uid == 0 || s.Uid == 10001) && (s.Mode&0o022 == 0 || s.Uid == 0 && s.Mode&unix.S_ISVTX != 0)
	}
	var before, opened, after unix.Stat_t
	if unix.Fstatat(parent, name, &before, unix.AT_SYMLINK_NOFOLLOW) != nil || !valid(before) {
		return nil, fmt.Errorf("invalid NAS source directory metadata")
	}
	fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open NAS source directory")
	}
	f := os.NewFile(uintptr(fd), name)
	if unix.Fstat(fd, &opened) != nil || unix.Fstatat(parent, name, &after, unix.AT_SYMLINK_NOFOLLOW) != nil || !valid(opened) || !sameNASMetadata(before, opened) || !sameNASMetadata(opened, after) {
		f.Close()
		return nil, fmt.Errorf("changed NAS source directory")
	}
	return f, nil
}

func openNASSourceRoot(path string) (*os.File, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.Contains(path, "\\") {
		return nil, fmt.Errorf("invalid NAS source path")
	}
	current, err := openNASDirectory(unix.AT_FDCWD, "/", false)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i, part := range parts {
		next, err := openNASDirectory(int(current.Fd()), part, i == len(parts)-1)
		current.Close()
		if err != nil {
			return nil, err
		}
		current = next
	}
	return current, nil
}

func openNASMaterialGuard(path string) (*nasMaterialGuard, error) {
	root, err := openNASSourceRoot(path)
	if err != nil {
		return nil, err
	}
	g := &nasMaterialGuard{path: path, dirs: map[string]*os.File{"": root}}
	entries, err := root.ReadDir(-1)
	if err != nil {
		g.Close()
		return nil, err
	}
	fresh := len(entries) == 0
	for _, name := range []string{"pki", "templates"} {
		if fresh {
			if err := unix.Mkdirat(int(root.Fd()), name, 0o755); err != nil {
				g.Close()
				return nil, fmt.Errorf("create fresh NAS source directory")
			}
		}
		dir, err := openNASDirectory(int(root.Fd()), name, true)
		if err != nil {
			g.Close()
			return nil, err
		}
		g.dirs[name] = dir
	}
	if err := g.Validate(fresh); err != nil {
		g.Close()
		return nil, err
	}
	return g, nil
}

func (g *nasMaterialGuard) Close() {
	for _, dir := range g.dirs {
		dir.Close()
	}
}

func (g *nasMaterialGuard) Validate(allowMissing bool) error {
	root, err := openNASSourceRoot(g.path)
	if err != nil {
		return err
	}
	defer root.Close()
	var held, current unix.Stat_t
	if unix.Fstat(int(g.dirs[""].Fd()), &held) != nil || unix.Fstat(int(root.Fd()), &current) != nil || !sameNASMetadata(held, current) {
		return fmt.Errorf("changed NAS source root")
	}
	for _, name := range []string{"pki", "templates"} {
		dir, err := openNASDirectory(int(root.Fd()), name, true)
		if err != nil {
			return err
		}
		statErr := unix.Fstat(int(dir.Fd()), &current)
		dir.Close()
		if statErr != nil || unix.Fstat(int(g.dirs[name].Fd()), &held) != nil || !sameNASMetadata(held, current) {
			return fmt.Errorf("changed NAS source child directory")
		}
	}
	for _, name := range nasMaterialSet().PublicFiles {
		parent, leaf := filepath.Split(name)
		dir := g.dirs[strings.TrimSuffix(parent, "/")]
		if dir == nil {
			return fmt.Errorf("invalid fixed NAS source manifest")
		}
		var before, opened, after unix.Stat_t
		err := unix.Fstatat(int(dir.Fd()), leaf, &before, unix.AT_SYMLINK_NOFOLLOW)
		if err == unix.ENOENT && allowMissing {
			continue
		}
		mode := uint32(0o644)
		if strings.HasSuffix(name, ".key") || name == "identity-signing-keys.json" {
			mode = 0o600
		}
		valid := func(s unix.Stat_t) bool {
			return s.Mode&unix.S_IFMT == unix.S_IFREG && s.Mode&0o7777 == mode && s.Uid == 10001 && s.Gid == 10001 && s.Nlink == 1
		}
		if err != nil || !valid(before) {
			return fmt.Errorf("invalid NAS source leaf metadata: %s", name)
		}
		fd, err := unix.Openat(int(dir.Fd()), leaf, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
		if err != nil {
			return fmt.Errorf("open NAS source leaf: %s", name)
		}
		statErr := unix.Fstat(fd, &opened)
		unix.Close(fd)
		if statErr != nil || unix.Fstatat(int(dir.Fd()), leaf, &after, unix.AT_SYMLINK_NOFOLLOW) != nil || !valid(opened) || !valid(after) || !sameNASMetadata(before, opened) || !sameNASMetadata(opened, after) {
			return fmt.Errorf("changed NAS source leaf: %s", name)
		}
	}
	return nil
}
