//go:build linux

package runtimebundle

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestStageLayoutRejectsLinuxUnsafeCAS(t *testing.T) {
	for _, kind := range []string{"leaf symlink", "ancestor symlink", "hardlink", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			o, layer, _ := stageFixture(t, false)
			leaf := filepath.Join(o.LayoutDir, "blobs", "sha256", strings.TrimPrefix(hash(layer), "sha256:"))
			if err := os.Remove(leaf); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "leaf symlink":
				target := filepath.Join(t.TempDir(), "layer")
				put(t, target, layer)
				if err := os.Symlink(target, leaf); err != nil {
					t.Fatal(err)
				}
			case "ancestor symlink":
				put(t, leaf, layer)
				cas := filepath.Dir(leaf)
				elsewhere := filepath.Join(t.TempDir(), "cas")
				if err := os.Rename(cas, elsewhere); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(elsewhere, cas); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				target := filepath.Join(t.TempDir(), "layer")
				put(t, target, layer)
				if err := os.Link(target, leaf); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := syscall.Mkfifo(leaf, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := StageLayout(o); err == nil {
				t.Fatal("unsafe CAS accepted")
			}
			if _, err := os.Stat(o.OutputDir); !os.IsNotExist(err) {
				t.Fatal("created output for unsafe CAS")
			}
		})
	}
}
