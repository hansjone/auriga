package materials

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateLeafNamesCannotEscape(t *testing.T) {
	out := t.TempDir()
	p, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for _, name := range []string{"", ".", "..", "../escape", "a/b", "a\\b", "C:escape", "a\x00b"} {
		if err := p.Create(name, []byte("secret")); err == nil {
			t.Fatalf("created invalid leaf %q", name)
		}
		if _, err := p.Read(name); err == nil {
			t.Fatalf("read invalid leaf %q", name)
		}
	}
	if _, err := os.Lstat(filepath.Join(out, "escape")); !os.IsNotExist(err) {
		t.Fatal("escaped private directory")
	}
}

func TestReceiptRejectsUnreceiptedDirectoriesBeforeMaterials(t *testing.T) {
	for _, kind := range []string{"template-only", "unknown-pki", "pki-link", "template-link"} {
		t.Run(kind, func(t *testing.T) {
			out := t.TempDir()
			for _, name := range []string{"pki", "templates"} {
				if err := os.Mkdir(filepath.Join(out, name), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			set := MaterialSet{PublicFiles: []string{"pki/ca.crt"}, PrivateFiles: []string{"edge.secret", "internal.properties"}, ManagedDirectories: []string{"pki", "templates"}}
			switch kind {
			case "template-only":
				if err := os.WriteFile(filepath.Join(out, "templates", "real-dsh.pb"), []byte("stale"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "unknown-pki":
				if err := os.WriteFile(filepath.Join(out, "pki", "old"), []byte("stale"), 0o600); err != nil {
					t.Fatal(err)
				}
			default:
				name := "pki"
				if kind == "template-link" {
					name = "templates"
				}
				outside := t.TempDir()
				if err := os.Remove(filepath.Join(out, name)); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(out, name)); err != nil {
					t.Skip("symlinks unavailable")
				}
			}
			if reuse, err := CheckReceipt(out, "digest", set); err == nil || reuse {
				t.Fatal("unreceipted residual material accepted")
			}
			if _, err := os.Lstat(filepath.Join(out, "iam-installation", "edge.secret")); !os.IsNotExist(err) {
				t.Fatal("credential created before rejection")
			}
		})
	}
}
