//go:build !linux

package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// Windows retains material generation for development only. It does not provide the
// Linux metadata trust guarantee, and the existing Java publisher rejects non-POSIX activation.
type privateInstallation struct {
	path string
	stat os.FileInfo
	seen map[string]os.FileInfo
}

func openPrivateInstallation(out string) (*privateInstallation, error) {
	if runtime.GOOS != "windows" {
		return nil, fmt.Errorf("private IAM installation requires Linux")
	}
	path := filepath.Join(out, "iam-installation")
	if err := os.Mkdir(path, 0o700); err != nil && !os.IsExist(err) {
		return nil, fmt.Errorf("create private IAM installation directory")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("invalid private IAM installation directory")
	}
	return &privateInstallation{path: path, stat: info, seen: make(map[string]os.FileInfo)}, nil
}

func (p *privateInstallation) owner() int { return os.Getuid() }
func (p *privateInstallation) close()     {}

func (p *privateInstallation) read(name string) ([]byte, error) {
	path := filepath.Join(p.path, name)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > 256 {
		return nil, fmt.Errorf("invalid private IAM installation file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open private IAM installation file")
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !samePrivateWindowsFile(info, opened) {
		return nil, fmt.Errorf("changed private IAM installation file")
	}
	raw, readErr := io.ReadAll(io.LimitReader(f, 257))
	after, statErr := f.Stat()
	if readErr != nil || statErr != nil || int64(len(raw)) != opened.Size() || !samePrivateWindowsFile(opened, after) {
		clear(raw)
		return nil, fmt.Errorf("read private IAM installation file")
	}
	p.seen[name] = after
	return raw, nil
}

func samePrivateWindowsFile(a, b os.FileInfo) bool {
	return os.SameFile(a, b) && a.Mode() == b.Mode() && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

func (p *privateInstallation) create(name string, data []byte) error {
	f, err := os.OpenFile(filepath.Join(p.path, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create private IAM installation file")
	}
	n, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil || n != len(data) {
		return fmt.Errorf("write private IAM installation file")
	}
	raw, err := p.read(name)
	defer clear(raw)
	if err != nil || !bytes.Equal(raw, data) {
		return fmt.Errorf("changed new private IAM installation file")
	}
	return nil
}

func (p *privateInstallation) validate() error {
	info, err := os.Lstat(p.path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !os.SameFile(p.stat, info) {
		return fmt.Errorf("changed private IAM installation directory")
	}
	for name, before := range p.seen {
		after, err := os.Lstat(filepath.Join(p.path, name))
		if err != nil || !samePrivateWindowsFile(before, after) {
			return fmt.Errorf("changed private IAM installation file")
		}
	}
	return nil
}
