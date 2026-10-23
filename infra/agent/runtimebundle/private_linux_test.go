package runtimebundle

import (
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func protectPrivate(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
}

func weakenPrivate(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLinuxPrivatePermissionsAndFIFO(t *testing.T) {
	for _, mode := range []os.FileMode{0o644, 0o640, 0o660, 0o200} {
		t.Run(mode.String(), func(t *testing.T) {
			f := fresh(t)
			if err := os.Chmod(f.o.PrivateKeyPath, mode); err != nil {
				t.Fatal(err)
			}
			_, err := Sign(f.o)
			invalid(t, err)
		})
	}
	t.Run("fifo", func(t *testing.T) {
		f := fresh(t)
		if err := os.Remove(f.o.ManifestPath); err != nil {
			t.Fatal(err)
		}
		if err := unix.Mkfifo(f.o.ManifestPath, 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := Sign(f.o)
		invalid(t, err)
	})
	t.Run("wrong owner", func(t *testing.T) {
		if os.Geteuid() != 0 {
			t.Skip("ownership negative test requires root")
		}
		f := fresh(t)
		if err := os.Chown(f.o.PrivateKeyPath, 65534, 65534); err != nil {
			t.Fatal(err)
		}
		_, err := Sign(f.o)
		invalid(t, err)
	})
}
