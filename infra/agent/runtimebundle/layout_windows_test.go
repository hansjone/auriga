//go:build windows

package runtimebundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestStageLayoutRejectsWindowsUnsafeCAS(t *testing.T) {
	for _, kind := range []string{"hardlink", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			o, layer, _ := stageFixture(t, false)
			leaf := filepath.Join(o.LayoutDir, "blobs", "sha256", strings.TrimPrefix(hash(layer), "sha256:"))
			if err := os.Remove(leaf); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(t.TempDir(), "layer")
			put(t, target, layer)
			if kind == "hardlink" {
				if err := os.Link(target, leaf); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Symlink(target, leaf); err != nil {
				t.Skipf("Windows symlink privilege unavailable: %v", err)
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

func TestStageLayoutRejectsWindowsShortNameOverlap(t *testing.T) {
	o, _, _ := stageFixture(t, false)
	for _, input := range []string{o.LayoutDir, o.Verify.Dir} {
		p, err := windows.UTF16PtrFromString(input)
		if err != nil {
			t.Fatal(err)
		}
		buf := make([]uint16, 32768)
		n, err := windows.GetShortPathName(p, &buf[0], uint32(len(buf)))
		if err != nil || n == 0 || n >= uint32(len(buf)) {
			t.Skipf("8.3 alias unavailable: %v", err)
		}
		short := windows.UTF16ToString(buf[:n])
		if strings.EqualFold(short, input) {
			t.Skip("8.3 alias unavailable on this volume")
		}
		o.OutputDir = filepath.Join(short, "nested-output")
		if _, err := StageLayout(o); err == nil {
			t.Fatal("physical input overlap via 8.3 alias accepted")
		}
		if _, err := os.Stat(o.OutputDir); !os.IsNotExist(err) {
			t.Fatal("created output through 8.3 alias")
		}
	}
}
