package materials

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

type Installation struct {
	dir  *os.File
	path string
	uid  uint32
	stat unix.Stat_t
	seen map[string]unix.Stat_t
}

func Open(out string) (*Installation, error) {
	// Bind the recorded installer to the effective creator, never to an unverified file owner.
	uid := uint32(os.Geteuid())
	parent, path, err := openTrustedInstallationPath(out, uid)
	if err != nil {
		return nil, err
	}
	defer func() { _ = parent.Close() }()
	if err := unix.Mkdirat(int(parent.Fd()), "iam-installation", 0o700); err != nil && err != unix.EEXIST {
		return nil, fmt.Errorf("create private IAM installation directory")
	}
	dir, stat, err := openInstallationDirectory(int(parent.Fd()), "iam-installation", uid, true)
	if err != nil {
		return nil, err
	}
	return &Installation{dir: dir, path: filepath.Join(path, "iam-installation"), uid: uid, stat: stat, seen: make(map[string]unix.Stat_t)}, nil
}

// Anchor each component with openat: neither existing nor concurrently changed symlinks are followed.
func openTrustedInstallationPath(path string, uid uint32) (*os.File, string, error) {
	for _, part := range strings.Split(path, string(filepath.Separator)) {
		if part == ".." {
			return nil, "", fmt.Errorf("invalid private IAM installation path")
		}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, "", fmt.Errorf("invalid private IAM installation path")
	}
	current, _, err := openInstallationDirectory(unix.AT_FDCWD, "/", uid, false)
	if err != nil {
		return nil, "", err
	}
	for _, part := range strings.Split(strings.TrimPrefix(abs, "/"), "/") {
		if part == "" {
			continue
		}
		next, _, err := openInstallationDirectory(int(current.Fd()), part, uid, false)
		_ = current.Close()
		if err != nil {
			return nil, "", err
		}
		current = next
	}
	return current, abs, nil
}

func openInstallationDirectory(parent int, name string, uid uint32, private bool) (*os.File, unix.Stat_t, error) {
	var before, after unix.Stat_t
	if err := unix.Fstatat(parent, name, &before, unix.AT_SYMLINK_NOFOLLOW); err != nil || !validInstallationDirectory(before, uid, private) {
		return nil, after, fmt.Errorf("invalid private IAM installation directory")
	}
	fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, after, fmt.Errorf("open private IAM installation directory")
	}
	f := os.NewFile(uintptr(fd), name)
	if err := unix.Fstat(fd, &after); err != nil || !validInstallationDirectory(after, uid, private) || !sameInstallationDirectory(before, after) {
		_ = f.Close()
		return nil, after, fmt.Errorf("changed private IAM installation directory")
	}
	return f, after, nil
}

func validInstallationDirectory(s unix.Stat_t, uid uint32, private bool) bool {
	if s.Mode&unix.S_IFMT != unix.S_IFDIR {
		return false
	}
	if private {
		return s.Uid == uid && s.Mode&0o7777 == 0o700
	}
	// Only root and this installer are trusted. Writable shared ancestors require root + sticky.
	return (s.Uid == 0 || s.Uid == uid) && (s.Mode&0o022 == 0 || s.Uid == 0 && s.Mode&unix.S_ISVTX != 0)
}

func sameInstallationDirectory(a, b unix.Stat_t) bool {
	// Directory link counts and timestamps naturally change while creating children.
	return a.Dev == b.Dev && a.Ino == b.Ino && a.Uid == b.Uid && a.Gid == b.Gid && a.Mode == b.Mode
}

func validInstallationFile(s unix.Stat_t, uid uint32) bool {
	return s.Mode&unix.S_IFMT == unix.S_IFREG && s.Mode&0o7777 == 0o600 && s.Uid == uid && s.Nlink == 1 && s.Size > 0 && s.Size <= 256
}

func sameInstallationFile(a, b unix.Stat_t) bool {
	return sameInstallationDirectory(a, b) && a.Nlink == b.Nlink && a.Size == b.Size && a.Mtim == b.Mtim && a.Ctim == b.Ctim
}

func (p *Installation) Owner() int { return int(p.uid) }
func (p *Installation) Close()     { _ = p.dir.Close() }

func (p *Installation) Read(name string) ([]byte, error) {
	if err := checkLeaf(name); err != nil {
		return nil, err
	}
	var before, opened, after unix.Stat_t
	if err := unix.Fstatat(int(p.dir.Fd()), name, &before, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return nil, err
	}
	if !validInstallationFile(before, p.uid) {
		return nil, fmt.Errorf("invalid private IAM installation file")
	}
	fd, err := unix.Openat(int(p.dir.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open private IAM installation file")
	}
	f := os.NewFile(uintptr(fd), name)
	defer func() { _ = f.Close() }()
	if err := unix.Fstat(fd, &opened); err != nil || !validInstallationFile(opened, p.uid) || !sameInstallationFile(before, opened) {
		return nil, fmt.Errorf("changed private IAM installation file")
	}
	raw, readErr := io.ReadAll(io.LimitReader(f, 257))
	statErr := unix.Fstat(fd, &after)
	if readErr != nil || statErr != nil || int64(len(raw)) != opened.Size || !sameInstallationFile(opened, after) {
		clear(raw)
		return nil, fmt.Errorf("read private IAM installation file")
	}
	p.seen[name] = after
	return raw, nil
}

func (p *Installation) Create(name string, data []byte) error {
	if err := checkLeaf(name); err != nil {
		return err
	}
	fd, err := unix.Openat(int(p.dir.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return fmt.Errorf("create private IAM installation file")
	}
	f := os.NewFile(uintptr(fd), name)
	var created unix.Stat_t
	statErr := unix.Fstat(fd, &created)
	// Check the real creator before writing. A restrictive umask fails closed; existing files are never repaired.
	if statErr != nil || created.Uid != p.uid || created.Nlink != 1 || created.Mode&unix.S_IFMT != unix.S_IFREG || created.Mode&0o7777 != 0o600 {
		_ = f.Close()
		return fmt.Errorf("invalid new private IAM installation file")
	}
	n, writeErr := f.Write(data)
	var written unix.Stat_t
	writtenErr := unix.Fstat(fd, &written)
	closeErr := f.Close()
	if writeErr != nil || writtenErr != nil || closeErr != nil || n != len(data) || !validInstallationFile(written, p.uid) || !sameInstallationDirectory(created, written) {
		return fmt.Errorf("write private IAM installation file")
	}
	raw, err := p.Read(name)
	defer clear(raw)
	if err != nil || !bytes.Equal(raw, data) || !sameInstallationFile(written, p.seen[name]) {
		return fmt.Errorf("changed new private IAM installation file")
	}
	return nil
}

func (p *Installation) Validate() error {
	parent, _, err := openTrustedInstallationPath(filepath.Dir(p.path), p.uid)
	if err != nil {
		return err
	}
	defer func() { _ = parent.Close() }()
	dir, stat, err := openInstallationDirectory(int(parent.Fd()), "iam-installation", p.uid, true)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	if !sameInstallationDirectory(p.stat, stat) {
		return fmt.Errorf("changed private IAM installation directory")
	}
	for name, before := range p.seen {
		var after unix.Stat_t
		if err := unix.Fstatat(int(dir.Fd()), name, &after, unix.AT_SYMLINK_NOFOLLOW); err != nil || !validInstallationFile(after, p.uid) || !sameInstallationFile(before, after) {
			return fmt.Errorf("changed private IAM installation file")
		}
	}
	return nil
}
