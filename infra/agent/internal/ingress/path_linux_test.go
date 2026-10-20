//go:build linux

package ingress

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/unix"
)

func TestLinux_ZipSkipsFIFOAndSymlink(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := unix.Mkfifo(filepath.Join(root, "fifo"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ok"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := New(Config{})
	defer s.Close()
	w := httptest.NewRecorder()
	if err := s.zipTree(w, root, "."); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil || len(zr.File) != 1 || zr.File[0].Name != "ok" {
		t.Fatalf("zip: %v %+v", err, zr)
	}
}

func TestLinux_ConcurrentSymlinkSwapsCannotEscape(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	secret := filepath.Join(outside, "secret")
	if err := os.WriteFile(secret, []byte("SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = os.Remove(filepath.Join(root, "file"))
			_ = os.Symlink(secret, filepath.Join(root, "file"))
			_ = os.Remove(filepath.Join(root, "file"))
			_ = os.WriteFile(filepath.Join(root, "file"), []byte("safe"), 0o600)
			_ = os.Symlink(outside, filepath.Join(root, "parent"))
			_ = os.Remove(filepath.Join(root, "parent"))
		}
	}()
	defer func() { close(stop); wg.Wait() }()
	s := New(Config{})
	defer s.Close()
	for i := 0; i < 100; i++ {
		f, err := OpenBeneath(root, "file", os.O_RDONLY, 0)
		if err == nil {
			data, err := io.ReadAll(f)
			_ = f.Close()
			if err == nil && strings.Contains(string(data), "SECRET") {
				t.Fatal("read escaped")
			}
		}
		_ = s.writeFile(httptest.NewRecorder(), httptest.NewRequest("PUT", "/", strings.NewReader("bad")), root, "parent/new/file")
		w := httptest.NewRecorder()
		if err := s.zipTree(w, root, "."); err == nil {
			zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range zr.File {
				f, err := entry.Open()
				if err != nil {
					t.Fatal(err)
				}
				data, err := io.ReadAll(f)
				_ = f.Close()
				if err == nil && strings.Contains(string(data), "SECRET") {
					t.Fatal("zip escaped")
				}
			}
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "new")); !os.IsNotExist(err) {
		t.Fatalf("created outside: %v", err)
	}
}

func TestLinux_DeleteDoesNotFollowLinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "tree"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "tree/link")); err != nil {
		t.Fatal(err)
	}
	if err := RemoveBeneath(root, "tree"); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(outside, "secret")); err != nil || string(data) != "keep" {
		t.Fatalf("deleted outside: %s %v", data, err)
	}
}
