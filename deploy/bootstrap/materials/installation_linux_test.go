package materials

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// Validate ownership rules using metadata values, without changing any account or filesystem owner.
func TestInstallationMetadataTrustPolicy(t *testing.T) {
	const installer = uint32(1001)
	for _, tc := range []struct {
		name    string
		stat    unix.Stat_t
		private bool
		want    bool
	}{
		{"installer-ancestor", unix.Stat_t{Mode: unix.S_IFDIR | 0o755, Uid: installer, Nlink: 4}, false, true},
		{"root-ancestor", unix.Stat_t{Mode: unix.S_IFDIR | 0o755, Uid: 0}, false, true},
		{"root-sticky-temp", unix.Stat_t{Mode: unix.S_IFDIR | 0o1777, Uid: 0}, false, true},
		{"untrusted-owner", unix.Stat_t{Mode: unix.S_IFDIR | 0o755, Uid: installer + 1}, false, false},
		{"group-writable", unix.Stat_t{Mode: unix.S_IFDIR | 0o775, Uid: installer}, false, false},
		{"root-writable", unix.Stat_t{Mode: unix.S_IFDIR | 0o777, Uid: 0}, false, false},
		{"installer-sticky-shared", unix.Stat_t{Mode: unix.S_IFDIR | 0o1777, Uid: installer}, false, false},
		{"ancestor-symlink", unix.Stat_t{Mode: unix.S_IFLNK | 0o777, Uid: installer}, false, false},
		{"private-natural-links", unix.Stat_t{Mode: unix.S_IFDIR | 0o700, Uid: installer, Nlink: 3}, true, true},
		{"private-root-owner", unix.Stat_t{Mode: unix.S_IFDIR | 0o700, Uid: 0}, true, false},
		{"private-readable", unix.Stat_t{Mode: unix.S_IFDIR | 0o750, Uid: installer}, true, false},
		{"private-special-mode", unix.Stat_t{Mode: unix.S_IFDIR | 0o2700, Uid: installer}, true, false},
	} {
		t.Run("directory/"+tc.name, func(t *testing.T) {
			if got := validInstallationDirectory(tc.stat, installer, tc.private); got != tc.want {
				t.Fatalf("directory metadata accepted=%t, want %t", got, tc.want)
			}
		})
	}
	base := unix.Stat_t{Mode: unix.S_IFREG | 0o600, Uid: installer, Nlink: 1, Size: 43}
	for _, tc := range []struct {
		name   string
		change func(*unix.Stat_t)
		want   bool
	}{
		{"private-file", func(*unix.Stat_t) {}, true},
		{"wrong-owner", func(s *unix.Stat_t) { s.Uid++ }, false},
		{"root-owner", func(s *unix.Stat_t) { s.Uid = 0 }, false},
		{"additional-link", func(s *unix.Stat_t) { s.Nlink = 2 }, false},
		{"unlinked", func(s *unix.Stat_t) { s.Nlink = 0 }, false},
		{"readable", func(s *unix.Stat_t) { s.Mode = unix.S_IFREG | 0o644 }, false},
		{"special-mode", func(s *unix.Stat_t) { s.Mode |= unix.S_ISUID }, false},
		{"directory", func(s *unix.Stat_t) { s.Mode = unix.S_IFDIR | 0o600 }, false},
		{"empty", func(s *unix.Stat_t) { s.Size = 0 }, false},
		{"too-large", func(s *unix.Stat_t) { s.Size = 257 }, false},
	} {
		t.Run("file/"+tc.name, func(t *testing.T) {
			stat := base
			tc.change(&stat)
			if got := validInstallationFile(stat, installer); got != tc.want {
				t.Fatalf("file metadata accepted=%t, want %t", got, tc.want)
			}
		})
	}
}

func TestInstallationRejectsExistingFileMetadata(t *testing.T) {
	for _, name := range []string{"edge.secret", "internal.properties"} {
		for _, kind := range []string{"mode", "directory", "symlink", "hardlink", "oversize", "empty"} {
			t.Run(name+"/"+kind, func(t *testing.T) {
				out := t.TempDir()
				if err := EnsureServiceCredentials(out); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(out, "iam-installation", name)
				original, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "mode":
					if err := os.Chmod(path, 0o644); err != nil {
						t.Fatal(err)
					}
				case "directory":
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(path, 0o700); err != nil {
						t.Fatal(err)
					}
				case "symlink":
					other := filepath.Join(out, "original")
					if err := os.Rename(path, other); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(other, path); err != nil {
						t.Fatal(err)
					}
				case "hardlink":
					if err := os.Link(path, filepath.Join(out, "copy")); err != nil {
						t.Fatal(err)
					}
				case "oversize":
					if err := os.WriteFile(path, bytes.Repeat([]byte("x"), 257), 0o600); err != nil {
						t.Fatal(err)
					}
				case "empty":
					if err := os.WriteFile(path, nil, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if err := EnsureServiceCredentials(out); err == nil {
					t.Fatal("invalid existing file metadata accepted")
				}
				if kind == "mode" {
					info, err := os.Stat(path)
					if err != nil || info.Mode().Perm() != 0o644 {
						t.Fatal("existing permissions were repaired")
					}
				}
				if kind == "hardlink" {
					after, err := os.ReadFile(path)
					if err != nil || !bytes.Equal(original, after) {
						t.Fatal("existing linked material was replaced")
					}
				}
			})
		}
	}
}

func TestInstallationRejectsDirectoryMetadata(t *testing.T) {
	for _, kind := range []string{"private-mode", "private-file", "private-symlink", "ancestor-mode", "ancestor-symlink", "parent-component"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			out := filepath.Join(root, "output")
			if err := os.Mkdir(out, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := EnsureServiceCredentials(out); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(out, "iam-installation")
			switch kind {
			case "private-mode":
				if err := os.Chmod(dir, 0o750); err != nil {
					t.Fatal(err)
				}
			case "private-file", "private-symlink":
				other := filepath.Join(out, "original")
				if err := os.Rename(dir, other); err != nil {
					t.Fatal(err)
				}
				if kind == "private-file" {
					if err := os.WriteFile(dir, []byte("invalid"), 0o600); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Symlink(other, dir); err != nil {
					t.Fatal(err)
				}
			case "ancestor-mode":
				if err := os.Chmod(out, 0o777); err != nil {
					t.Fatal(err)
				}
			case "ancestor-symlink":
				alias := filepath.Join(root, "alias")
				if err := os.Symlink(out, alias); err != nil {
					t.Fatal(err)
				}
				out = alias
			case "parent-component":
				out += "/../output"
			}
			if err := EnsureServiceCredentials(out); err == nil {
				t.Fatal("invalid directory metadata accepted")
			}
		})
	}
}

func TestInstallationFinalMetadataRecheck(t *testing.T) {
	for _, kind := range []string{"file-mode", "file-replaced", "directory-replaced"} {
		t.Run(kind, func(t *testing.T) {
			out := t.TempDir()
			if err := EnsureServiceCredentials(out); err != nil {
				t.Fatal(err)
			}
			p, err := Open(out)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			raw, err := p.Read("internal.properties")
			if err != nil {
				t.Fatal(err)
			}
			defer clear(raw)
			if err := p.Validate(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(out, "iam-installation", "internal.properties")
			switch kind {
			case "file-mode":
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
			case "file-replaced":
				if err := os.Rename(path, path+".old"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, raw, 0o600); err != nil {
					t.Fatal(err)
				}
			case "directory-replaced":
				dir := filepath.Dir(path)
				if err := os.Rename(dir, dir+".old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := p.Validate(); err == nil {
				t.Fatal("changed material passed final validation")
			}
		})
	}
}
