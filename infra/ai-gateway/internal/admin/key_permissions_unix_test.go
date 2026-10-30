//go:build !windows

package admin

import (
	"os"
	"path/filepath"
	"testing"
)

func protectTestKey(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
}
func TestUnprotectedKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(path, make([]byte, 32), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCipher(path); err == nil {
		t.Fatal("world-readable master key accepted")
	}
}
