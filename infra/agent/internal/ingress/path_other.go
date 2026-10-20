//go:build !linux && !windows

package ingress

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func OpenBeneath(root, rel string, flags int, mode os.FileMode) (*os.File, error) {
	if _, err := ResolveUnderRoot(root, rel); err != nil {
		return nil, err
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	f, err := r.OpenFile(rel, flags, mode)
	return f, checkedPathError(err)
}

func createBeneath(root, rel string) (*os.File, error) {
	if _, err := ResolveUnderRoot(root, rel); err != nil {
		return nil, err
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	if err := r.MkdirAll(filepath.Dir(rel), 0o700); err != nil {
		return nil, checkedPathError(err)
	}
	f, err := r.OpenFile(rel, os.O_CREATE|os.O_WRONLY, 0o600)
	return f, checkedPathError(err)
}

func checkedPathError(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "path escapes from parent") {
		return fmt.Errorf("%s: %w", CodePathEscape, err)
	}
	return err
}
