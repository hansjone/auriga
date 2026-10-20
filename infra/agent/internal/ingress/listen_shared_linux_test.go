//go:build linux

package ingress

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestSharedLocalRejectsUnsafeDirectories(t *testing.T) {
	for _, name := range []string{"data root", "run", "tmp", "existing populated", "directory symlink", "ancestor symlink", "writable parent", "writable directory", "marker symlink", "marker shared", "foreign file"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "ingress", "ingress.sock")
			mkdir := func(dir string) {
				t.Helper()
				if err := os.Mkdir(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			write := func(file string) {
				t.Helper()
				if err := os.WriteFile(file, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			switch name {
			case "data root":
				path = filepath.Join(root, "ingress.sock")
			case "run":
				path = "/run/ingress.sock"
			case "tmp":
				path = "/tmp/ingress.sock"
			case "existing populated":
				mkdir(filepath.Dir(path))
				write(filepath.Join(filepath.Dir(path), "private-data"))
			case "directory symlink":
				if err := os.Symlink(root, filepath.Dir(path)); err != nil {
					t.Fatal(err)
				}
			case "ancestor symlink":
				link := filepath.Join(root, "link")
				if err := os.Symlink(root, link); err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(link, "ingress", "ingress.sock")
			case "writable parent":
				if err := os.Chmod(root, 0o777); err != nil {
					t.Fatal(err)
				}
			default:
				if err := prepareSharedLocalDir(path); err != nil {
					t.Fatal(err)
				}
				marker := filepath.Join(filepath.Dir(path), localDirMarker)
				switch name {
				case "writable directory":
					if err := os.Chmod(filepath.Dir(path), 0o770); err != nil {
						t.Fatal(err)
					}
				case "marker symlink":
					if err := os.Remove(marker); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(root, marker); err != nil {
						t.Fatal(err)
					}
				case "marker shared":
					if err := os.Chmod(marker, 0o640); err != nil {
						t.Fatal(err)
					}
				case "foreign file":
					write(path)
				}
			}
			before, err := os.Lstat(filepath.Dir(path))
			if err != nil {
				t.Fatal(err)
			}
			listener, err := ListenLocal(path, os.Getgid())
			if listener != nil {
				_ = listener.Close()
				t.Fatal("unsafe path accepted")
			}
			if err == nil || !strings.Contains(err.Error(), "unsafe shared ingress directory") {
				t.Fatalf("expected explicit rejection, got %v", err)
			}
			after, err := os.Lstat(filepath.Dir(path))
			if err != nil {
				t.Fatal(err)
			}
			if after.Mode() != before.Mode() {
				t.Fatal("rejected directory permissions changed")
			}
		})
	}
}

func TestSharedLocalAgentDirectorySurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ingress", "ingress.sock")
	for range 2 {
		listener, err := ListenLocal(path, os.Getgid())
		if err != nil {
			t.Fatal(err)
		}
		if err := listener.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSharedLocalMovesLegacyDirectoryAside(t *testing.T) {
	for _, withSocket := range []bool{false, true} {
		root := t.TempDir()
		dir := filepath.Join(root, "ingress")
		path := filepath.Join(dir, "ingress.sock")
		if err := os.Mkdir(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		if withSocket {
			old, err := net.Listen("unix", path)
			if err != nil {
				t.Fatal(err)
			}
			old.(*net.UnixListener).SetUnlinkOnClose(false)
			_ = old.Close()
		}
		listener, err := ListenLocal(path, os.Getgid())
		if err != nil {
			t.Fatalf("legacy directory (socket=%v): %v", withSocket, err)
		}
		_ = listener.Close()
		if _, err := os.Lstat(filepath.Join(dir, localDirMarker)); err != nil {
			t.Fatal("fresh directory has no marker")
		}
		backups, err := filepath.Glob(filepath.Join(root, localDirLegacyPrefix+"*"))
		if err != nil || len(backups) != 1 {
			t.Fatalf("expected one moved-aside legacy directory, got %v %v", backups, err)
		}
		info, err := os.Lstat(backups[0])
		if err != nil || info.Mode().Perm() != 0o750 {
			t.Fatalf("legacy directory permissions must stay untouched: %v %v", info, err)
		}
	}
}

func TestSharedLocalCreationIgnoresUmaskAndStaleTemporaryDirs(t *testing.T) {
	root := t.TempDir()
	stale := filepath.Join(root, localDirTempPrefix+"crashed")
	if err := os.Mkdir(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "ingress", "ingress.sock")
	previous := syscall.Umask(0o277)
	listener, err := ListenLocal(path, os.Getgid())
	syscall.Umask(previous)
	if err != nil {
		t.Fatal(err)
	}
	_ = listener.Close()
	marker, err := os.Lstat(filepath.Join(root, "ingress", localDirMarker))
	if err != nil || marker.Mode().Perm() != 0o600 {
		t.Fatalf("marker must be 0600 regardless of umask: %v %v", marker, err)
	}
	if _, err := os.Lstat(stale); !os.IsNotExist(err) {
		t.Fatal("stale temporary directory was not removed")
	}
	// Restart still accepts the directory created under the restrictive umask.
	listener, err = ListenLocal(path, os.Getgid())
	if err != nil {
		t.Fatal(err)
	}
	_ = listener.Close()
}
