package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrUnsafePath is returned (wrapped) when a path built from spec, manifest,
// or archive-entry input fails normalization or escapes its root.
var ErrUnsafePath = errors.New("storage: unsafe path")

// SafeJoin joins relative elements under root and fails closed when the result
// could leave root. Each element must be relative (no volume, no leading
// separator) and must not contain "." or ".." components or NUL; the joined
// path is filepath.Clean'ed, checked with AssertUnderRoot, and every existing
// component below root must be a real entry, not a symlink.
func SafeJoin(root string, elems ...string) (string, error) {
	if root == "" {
		return "", fmt.Errorf("%w: empty root", ErrUnsafePath)
	}
	if len(elems) == 0 {
		return "", fmt.Errorf("%w: nothing to join", ErrUnsafePath)
	}
	root = filepath.Clean(root)
	parts := make([]string, 0, len(elems)+1)
	parts = append(parts, root)
	for _, e := range elems {
		if err := checkRelElem(e); err != nil {
			return "", err
		}
		parts = append(parts, e)
	}
	joined := filepath.Clean(filepath.Join(parts...))
	if joined == root {
		return "", fmt.Errorf("%w: resolves to root", ErrUnsafePath)
	}
	if err := AssertUnderRoot(root, joined); err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnsafePath, err)
	}
	if err := CheckNoSymlink(root, joined); err != nil {
		return "", err
	}
	return joined, nil
}

func checkRelElem(e string) error {
	if e == "" {
		return fmt.Errorf("%w: empty element", ErrUnsafePath)
	}
	if strings.ContainsRune(e, 0) || strings.ContainsRune(e, ':') {
		return fmt.Errorf("%w: NUL or colon in element", ErrUnsafePath)
	}
	if filepath.IsAbs(e) || filepath.VolumeName(e) != "" ||
		strings.HasPrefix(e, "/") || strings.HasPrefix(e, `\`) {
		return fmt.Errorf("%w: absolute element %q", ErrUnsafePath, e)
	}
	for _, c := range strings.FieldsFunc(e, func(r rune) bool { return r == '/' || r == '\\' }) {
		if c == "." || c == ".." {
			return fmt.Errorf("%w: dot component in %q", ErrUnsafePath, e)
		}
	}
	return nil
}

// CheckNoSymlink verifies that no existing path component strictly below root
// (up to and including path) is a symlink. Missing components end the walk:
// they cannot redirect anything yet. root itself may be a symlink (operator
// choice). path must already be under root.
func CheckNoSymlink(root, path string) error {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	if err := AssertUnderRoot(root, path); err != nil {
		return fmt.Errorf("%w: %v", ErrUnsafePath, err)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnsafePath, err)
	}
	if rel == "." {
		return nil
	}
	cur := root
	for _, c := range strings.Split(rel, string(filepath.Separator)) {
		cur = filepath.Join(cur, c)
		fi, err := os.Lstat(cur)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return fmt.Errorf("storage: lstat %s: %w", cur, err)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: symlink at %s", ErrUnsafePath, cur)
		}
	}
	return nil
}
