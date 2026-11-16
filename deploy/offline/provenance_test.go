package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSourceSnapshotIncludesSiteChangesAndVersion(t *testing.T) {
	root := t.TempDir()
	for _, args := range [][]string{{"init", "-b", "site/fix"}, {"config", "user.name", "alice"}, {"config", "user.email", "alice@example.com"}} {
		if _, err := gitAt(root, args...); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range map[string]string{"main.go": "original", "remove.go": "remove", ".gitignore": "secret\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := gitAt(root, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := gitAt(root, "commit", "-m", "fixture"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitAt(root, "tag", "0.2.0"); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"main.go": "site change", "new.go": "new source", " new.go": "leading space source", "secret": "do not package", "signing-secret": "private signer"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(filepath.Join(root, "remove.go")); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "snapshot")
	info, err := snapshotSource(root, out, filepath.Join(root, "signing-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if !info.Dirty || info.Branch != "site/fix" || info.UpstreamVersion != "0.2.0" || !hex64.MatchString(info.SnapshotSHA256) {
		t.Fatalf("%+v", info)
	}
	for _, name := range []string{"main.go", "new.go", " new.go"} {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"secret", "remove.go", "signing-secret"} {
		if _, err := os.Stat(filepath.Join(out, name)); !os.IsNotExist(err) {
			t.Fatal("included", name)
		}
	}
	if _, err := snapshotSource(root, filepath.Join(t.TempDir(), "rejected"), filepath.Join(root, "main.go")); err == nil {
		t.Fatal("accepted tracked signing key history")
	}
	if err := sourceVersion(info, release{Version: "0.2.0"}, "0.2.0-site.3", true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(printProvenance(info), "0.2.0-site.3") || !strings.Contains(printProvenance(info), info.Commit) {
		t.Fatal("source provenance missing")
	}
	info.Dirty = false
	info.SiteChanges = true
	if err := sourceVersion(info, release{Version: "0.2.0"}, "", true); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(info.Version, "0.2.0-site.") {
		t.Fatal("committed site changes lost their suffix")
	}
}
