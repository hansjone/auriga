package runtimebundle

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func absolutePath(path string) (string, error) {
	if path == "" || strings.ContainsRune(path, 0) {
		return "", fmt.Errorf("explicit ordinary path required")
	}
	for _, part := range strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return "", fmt.Errorf("parent traversal is not allowed")
		}
	}
	return filepath.Abs(path)
}

func openDirectory(path string) (*os.Root, error) {
	absolute, err := absolutePath(path)
	if err != nil {
		return nil, err
	}
	f, err := openOrdinary(absolute, true)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	before, err := f.Stat()
	if err != nil {
		return nil, err
	}
	r, err := os.OpenRoot(absolute)
	if err != nil {
		return nil, err
	}
	after, err := r.Stat(".")
	if err != nil || !os.SameFile(before, after) {
		_ = r.Close()
		return nil, fmt.Errorf("directory changed while opening")
	}
	return r, nil
}

func readInput(path string, limit int, private bool) ([]byte, error) {
	absolute, err := absolutePath(path)
	if err != nil {
		return nil, err
	}
	f, err := openOrdinary(absolute, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	if private {
		if err := privatePermissions(f); err != nil {
			return nil, err
		}
	}
	return readBounded(f, limit)
}

func readBounded(f *os.File, limit int) ([]byte, error) {
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() <= 0 || st.Size() > int64(limit) {
		return nil, fmt.Errorf("input must be a nonempty bounded ordinary file")
	}
	if err := singleLink(f); err != nil {
		return nil, err
	}
	b, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil || len(b) == 0 || len(b) > limit || int64(len(b)) != st.Size() {
		return nil, fmt.Errorf("input size changed or exceeds limit")
	}
	return b, nil
}

func readMember(r *os.Root, name string, limit int) ([]byte, error) {
	f, err := openMember(r, name, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return readBounded(f, limit)
}

func exactNames(r *os.Root, name string, expected ...string) error {
	f, err := openMember(r, name, true)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	// Bound enumeration independently of the potentially hostile directory size.
	entries, err := f.ReadDir(len(expected) + 1)
	if err != nil && err != io.EOF {
		return err
	}
	if len(entries) != len(expected) {
		return fmt.Errorf("missing or extra bundle materials in %s", name)
	}
	for _, e := range entries {
		if !slices.Contains(expected, e.Name()) || e.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("unexpected or linked bundle material in %s", name)
		}
	}
	return nil
}

func writeNew(path string, m *Material) error {
	absolute, err := absolutePath(path)
	if err != nil {
		return err
	}
	parent, err := openDirectory(filepath.Dir(absolute))
	if err != nil {
		return err
	}
	defer func() { _ = parent.Close() }()
	name := filepath.Base(absolute)
	// Atomic mkdir refuses every existing object, including an empty directory.
	// On later I/O failure the new partial directory is retained for diagnosis.
	if err := parent.Mkdir(name, 0o700); err != nil {
		return fmt.Errorf("output must be a new directory: %w", err)
	}
	created, err := parent.Lstat(name)
	if err != nil || !created.IsDir() || created.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("new output directory changed")
	}
	r, err := parent.OpenRoot(name)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	opened, err := r.Stat(".")
	if err != nil || !os.SameFile(created, opened) {
		return fmt.Errorf("new output directory changed while opening")
	}
	proof := "runtime-proofs/" + strings.TrimPrefix(m.dsh.ReleaseEnvelope.ImageDigest, "sha256:")
	for _, dir := range []string{"runtime-keys", "runtime-proofs", proof} {
		if err := r.Mkdir(dir, 0o755); err != nil {
			return err
		}
	}
	for _, file := range []struct {
		name string
		raw  []byte
	}{{"runtime.pb", m.runtime}, {"runtime-keys/root.pb", m.root}, {proof + "/manifest.json", m.manifest}, {proof + "/config.json", m.config}} {
		f, err := r.OpenFile(file.name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return err
		}
		_, writeErr := f.Write(file.raw)
		syncErr := f.Sync()
		closeErr := f.Close()
		if err := firstError(writeErr, syncErr, closeErr); err != nil {
			return err
		}
	}
	return nil
}

func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
