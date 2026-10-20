package storage_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/hansjone/auriga/infra/agent/internal/storage"
)

func TestSafeJoinAcceptsPlainRelative(t *testing.T) {
	root := t.TempDir()
	got, err := storage.SafeJoin(root, "sha256", "ab")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "sha256", "ab"); got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestSafeJoinRejectsEscapes(t *testing.T) {
	root := t.TempDir()
	bad := [][]string{
		{"../x"},
		{".."},
		{"."},
		{"a", ".."},
		{"a/../../x"},
		{`a\..\..\x`},
		{"a/./b"},
		{"/etc/passwd"},
		{`\windows`},
		{""},
		{"a\x00b"},
		{"C:x"},
		{"a:stream"},
	}
	if runtime.GOOS == "windows" {
		bad = append(bad, []string{`C:\Windows`})
	}
	for _, elems := range bad {
		if _, err := storage.SafeJoin(root, elems...); err == nil {
			t.Fatalf("SafeJoin(%q) should fail", elems)
		} else if !errors.Is(err, storage.ErrUnsafePath) {
			t.Fatalf("SafeJoin(%q): want ErrUnsafePath, got %v", elems, err)
		}
	}
	if _, err := storage.SafeJoin(root); err == nil {
		t.Fatal("no elements should fail")
	}
	if _, err := storage.SafeJoin("", "a"); err == nil {
		t.Fatal("empty root should fail")
	}
}

// symlinkOrSkip creates a symlink or skips (Windows without developer mode).
func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink not permitted here: %v", err)
	}
}

func TestSafeJoinRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	symlinkOrSkip(t, outside, filepath.Join(root, "link"))
	if _, err := storage.SafeJoin(root, "link", "file"); !errors.Is(err, storage.ErrUnsafePath) {
		t.Fatalf("symlinked component must be rejected, got %v", err)
	}
	// A symlink as the final component is rejected too.
	if _, err := storage.SafeJoin(root, "link"); !errors.Is(err, storage.ErrUnsafePath) {
		t.Fatalf("symlink leaf must be rejected, got %v", err)
	}
}

func TestLayoutEnsureRefusesPlantedSymlink(t *testing.T) {
	dir := t.TempDir()
	l := &storage.Layout{InstancesDir: dir}
	p, err := l.Ensure("inst_s", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	dsh := filepath.Join(p.Home, ".dsh")
	if err := os.Remove(dsh); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, outside, dsh)
	var chowned []string
	l.Chown = func(path string, uid, gid int) error {
		chowned = append(chowned, path)
		return nil
	}
	if _, err := l.Ensure("inst_s", 200001, 200001); !errors.Is(err, storage.ErrUnsafePath) {
		t.Fatalf("Ensure must refuse symlinked home subdir, got %v", err)
	}
	if len(chowned) != 0 {
		t.Fatalf("no chown expected after refusal, got %v", chowned)
	}
}

func TestLayoutRejectsSymlinkedInstanceRoot(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "keep"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, outside, filepath.Join(dir, "inst_r"))
	l := &storage.Layout{InstancesDir: dir}
	if err := l.Remove("inst_r"); !errors.Is(err, storage.ErrUnsafePath) {
		t.Fatalf("Remove through symlink must fail, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "keep")); err != nil {
		t.Fatal("outside file must survive:", err)
	}
}

func TestRejectWindowsDeviceNames(t *testing.T) {
	for _, id := range []string{"CON", "con", "Nul", "aux", "PRN", "COM1", "lpt9"} {
		if err := storage.ValidateInstanceID(id); err == nil {
			t.Fatalf("%q should be rejected", id)
		}
	}
	for _, id := range []string{"COM0", "COM10", "console", "nul1"} {
		if err := storage.ValidateInstanceID(id); err != nil {
			t.Fatalf("%q should be accepted: %v", id, err)
		}
	}
}
