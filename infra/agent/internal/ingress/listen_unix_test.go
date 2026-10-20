//go:build unix

package ingress

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestListenLocalModes(t *testing.T) {
	for _, tc := range []struct {
		name          string
		gid           int
		dirMode, mode os.FileMode
	}{
		{"private", -1, 0o700, 0o600},
		{"shared with group", os.Getgid(), 0o750, 0o660},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ingress", "ingress.sock")
			ln, err := ListenLocal(path, tc.gid)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = ln.Close() }()
			for p, want := range map[string]os.FileMode{filepath.Dir(path): tc.dirMode, path: tc.mode} {
				fi, err := os.Stat(p)
				if err != nil {
					t.Fatal(err)
				}
				if fi.Mode().Perm() != want {
					t.Fatalf("%s mode %v, want %v", p, fi.Mode().Perm(), want)
				}
				if tc.gid >= 0 {
					if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Gid) != tc.gid {
						t.Fatalf("%s gid %d, want %d", p, st.Gid, tc.gid)
					}
				}
			}
		})
	}
}
