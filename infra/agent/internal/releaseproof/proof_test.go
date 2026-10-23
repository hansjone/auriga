package releaseproof

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadProofBoundsAndLinks(t *testing.T) {
	for _, mode := range []string{"ok", "missing", "directory", "oversize", "empty", "file-link", "directory-link", "bad-digest", "traversal"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			digest := "sha256:" + strings.Repeat("a", 64)
			entry := filepath.Join(root, digest[7:])
			if err := os.Mkdir(entry, 0700); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"manifest.json", "config.json"} {
				if err := os.WriteFile(filepath.Join(entry, name), []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			file := filepath.Join(entry, "manifest.json")
			switch mode {
			case "missing":
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(file, 0700); err != nil {
					t.Fatal(err)
				}
			case "oversize":
				if err := os.WriteFile(file, []byte(strings.Repeat(" ", (4<<20)+1)), 0600); err != nil {
					t.Fatal(err)
				}
			case "empty":
				if err := os.WriteFile(file, nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "file-link":
				target := filepath.Join(root, "target")
				if err := os.WriteFile(target, []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, file); err != nil {
					t.Skipf("OS cannot create symlink: %v", err)
				}
			case "directory-link":
				moved := filepath.Join(root, "moved")
				if err := os.Rename(entry, moved); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(moved, entry); err != nil {
					t.Skipf("OS cannot create symlink: %v", err)
				}
			case "bad-digest":
				digest = "sha256:" + strings.Repeat("A", 64)
			case "traversal":
				digest = "sha256:../../etc/passwd"
			}
			m, c, err := Read(root, digest)
			if mode == "ok" {
				if err != nil || string(m) != "{}" || string(c) != "{}" {
					t.Fatal(m, c, err)
				}
			} else if err == nil {
				t.Fatal("unsafe proof read accepted")
			}
		})
	}
}
