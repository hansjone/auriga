//go:build windows

package identity

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsGuardFileFlushAndError(t *testing.T) {
	for _, fail := range []bool{false, true} {
		calls := 0
		injected := errors.New("FlushFileBuffers failure")
		err := syncGuardFile(func() error {
			calls++
			if fail {
				return injected
			}
			return nil
		})
		if calls != 1 || (err == nil) == fail {
			t.Fatalf("file flush result ignored: calls=%d err=%v", calls, err)
		}
		if fail && !errors.Is(err, injected) {
			t.Fatal("original flush error lost")
		}
	}
	dir := filepath.Join(t.TempDir(), "new[1]", "agent")
	if err := CreateTrustGuard(dir, guardTestValue()); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTrustGuard(dir); err != nil {
		t.Fatal(err)
	}
	// File.Sync really reports invalid closed handles; no directory/volume
	// flush or special privileges are used to fabricate a stronger guarantee.
	f, err := os.CreateTemp(t.TempDir(), "closed")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := syncTrustGuard(f, "unused"); err == nil {
		t.Fatal("closed Windows file flush error ignored")
	}
}
