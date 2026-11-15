package identity

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ReadPrivateConfiguration uses the confirmation key's owner/ACL checks. D
// selects the file and its private installation directory, never a wire input.
func ReadPrivateConfiguration(path string, limit int64) ([]byte, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, fmt.Errorf("absolute private configuration required")
	}
	for current := path; ; current = filepath.Dir(current) {
		info, e := os.Lstat(current)
		if e != nil || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("linked private configuration")
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	before, e := os.Lstat(path)
	if e != nil || !before.Mode().IsRegular() || before.Size() > limit || checkPrivateKeyFile(path) != nil {
		return nil, fmt.Errorf("private configuration denied")
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer func() { _ = f.Close() }()
	opened, e := f.Stat()
	if e != nil || !os.SameFile(before, opened) {
		return nil, fmt.Errorf("private configuration changed")
	}
	raw, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil {
		return nil, e
	}
	after, e := os.Lstat(path)
	if e != nil || !os.SameFile(before, after) || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) || int64(len(raw)) > limit {
		return nil, fmt.Errorf("private configuration changed")
	}
	return raw, nil
}
