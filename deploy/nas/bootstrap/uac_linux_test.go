package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hansjone/auriga/deploy/bootstrap/materials"
)

func TestNASOptionalUACMaterials(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires Linux root for owner metadata")
	}
	for _, kind := range []string{"missing", "empty", "valid", "unknown", "directory", "symlink", "hardlink", "file-mode", "file-owner", "dir-mode", "dir-owner", "replaced", "appeared"} {
		t.Run(kind, func(t *testing.T) {
			root, err := os.MkdirTemp("", "auriga-uac-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(root) })
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			must(os.Chmod(root, 0o755))
			must(os.Chown(root, 10001, 10001))
			must(os.Mkdir(filepath.Join(root, "iam-installation"), 0o700))
			uac := filepath.Join(root, "uac")
			if kind != "missing" && kind != "appeared" {
				must(os.Mkdir(uac, 0o750))
				must(os.Chmod(uac, 0o750))
				must(os.Chown(uac, 10001, 10001))
				if kind != "empty" {
					for _, name := range []string{"uac.properties", "adapter.json", "members.json"} {
						path := filepath.Join(uac, name)
						must(os.WriteFile(path, []byte("private fixture"), 0o640))
						must(os.Chmod(path, 0o640))
						must(os.Chown(path, 10001, 10001))
					}
				}
			}
			file := filepath.Join(uac, "adapter.json")
			switch kind {
			case "unknown":
				must(os.WriteFile(filepath.Join(uac, "extra"), nil, 0o640))
			case "directory":
				must(os.Remove(file))
				must(os.Mkdir(file, 0o750))
			case "symlink":
				must(os.Remove(file))
				must(os.Symlink("members.json", file))
			case "hardlink":
				must(os.Remove(file))
				must(os.Link(filepath.Join(uac, "members.json"), file))
			case "file-mode":
				must(os.Chmod(file, 0o644))
			case "file-owner":
				must(os.Chown(file, 0, 0))
			case "dir-mode":
				must(os.Chmod(uac, 0o755))
			case "dir-owner":
				must(os.Chown(uac, 0, 0))
			}
			g, err := openNASMaterialGuard(root, materials.MaterialSet{})
			good := kind == "missing" || kind == "empty" || kind == "valid" || kind == "replaced" || kind == "appeared"
			if !good {
				if err == nil {
					g.Close()
					t.Fatal("unsafe UAC accepted")
				}
				if kind == "unknown" && !strings.Contains(err.Error(), "unknown NAS source leaf") {
					t.Fatal(err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer g.Close()
			if kind == "replaced" {
				must(os.Rename(uac, uac+"-old"))
			}
			if kind == "replaced" || kind == "appeared" {
				must(os.Mkdir(uac, 0o750))
				must(os.Chown(uac, 10001, 10001))
			}
			err = g.Validate(false)
			if (err != nil) != (kind == "replaced" || kind == "appeared") {
				t.Fatalf("unexpected validation: %v", err)
			}
		})
	}
}
