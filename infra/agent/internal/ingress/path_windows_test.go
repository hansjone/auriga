//go:build windows

package ingress

import (
	"archive/zip"
	"bytes"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindows_JunctionRejected(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	junction := filepath.Join(root, "junction")
	if output, err := exec.Command("cmd", "/c", "mklink", "/J", junction, outside).CombinedOutput(); err != nil {
		t.Fatalf("mklink: %v %s", err, output)
	}
	for _, rel := range []string{"junction", "junction/secret"} {
		if _, err := ResolveUnderRoot(root, rel); err == nil || !strings.Contains(err.Error(), CodePathEscape) {
			t.Fatalf("resolve: %v", err)
		}
		f, err := OpenBeneath(root, rel, os.O_RDONLY, 0)
		if f != nil {
			_ = f.Close()
		}
		if err == nil || !strings.Contains(err.Error(), CodePathEscape) {
			t.Fatalf("open: %v", err)
		}
	}
	s := New(Config{})
	defer func() { _ = s.Close() }()
	err := s.writeFile(httptest.NewRecorder(), httptest.NewRequest("PUT", "/", strings.NewReader("bad")), root, "junction/new/file")
	if err == nil || !strings.Contains(err.Error(), CodePathEscape) {
		t.Fatalf("write: %v", err)
	}
	w := httptest.NewRecorder()
	if err := s.zipTree(w, root, "."); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil || len(zr.File) != 0 {
		t.Fatalf("zip junction: %v %+v", err, zr)
	}
	if err := s.zipTree(httptest.NewRecorder(), root, "junction"); err == nil {
		t.Fatal("junction zip accepted")
	}
	if _, err := os.Stat(filepath.Join(outside, "new")); !os.IsNotExist(err) {
		t.Fatalf("write escaped: %v", err)
	}
}
