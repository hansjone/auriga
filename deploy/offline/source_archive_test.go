package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveSourceWithoutGitTracksChangesAndLocks(t *testing.T) {
	root := t.TempDir()
	for name, body := range map[string]string{"main.go": "original", "go.mod": "module fixture\n", "delete.txt": "old"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	r := release{Version: "1.0", Build: &buildSpec{}}
	info := provenance{Commit: strings.Repeat("a", 40), Version: "1.0", UpstreamVersion: "1.0"}
	if err := signedTestSourceManifest(t, root, info, strings.Repeat("b", 40), r); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git/HEAD"), []byte("ref: refs/heads/site/local\n"), 0644); err != nil {
		t.Fatal(err)
	}
	before, err := snapshotSource(root, filepath.Join(t.TempDir(), "snapshot"))
	if err != nil {
		t.Fatal(err)
	}
	if before.SiteChanges || before.Dirty || before.Commit != info.Commit {
		t.Fatal(before)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("site modification"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "delete.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "new.txt"), []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}
	after, err := snapshotSource(root, filepath.Join(t.TempDir(), "snapshot"))
	if err != nil {
		t.Fatal(err)
	}
	if !after.SiteChanges || !after.Dirty || after.SnapshotSHA256 == before.SnapshotSHA256 {
		t.Fatal(after)
	}
	if err := sourceVersion(after, r, "", true); err != nil || !strings.HasPrefix(after.Version, "1.0-site.") {
		t.Fatal(after, err)
	}
	locks, err := dependencyLocks(root, r)
	if err != nil || locks.Files["go.mod"] == "" {
		t.Fatal(locks, err)
	}
}

func TestSignedSourceBaselineCannotBeRewritten(t *testing.T) {
	root := t.TempDir()
	key, _ := testSigningKeys(t)
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("upstream"), 0644); err != nil {
		t.Fatal(err)
	}
	r := release{Version: "1.0", Build: &buildSpec{}}
	if err := signedTestSourceManifest(t, root, provenance{Commit: strings.Repeat("a", 40), UpstreamVersion: "1.0", Version: "1.0"}, strings.Repeat("b", 40), r); err != nil {
		t.Fatal(err)
	}
	if err := signingPublic(key, filepath.Join(root, "release-public.pem")); err != nil {
		t.Fatal(err)
	}
	if err := writeSums(root); err != nil {
		t.Fatal(err)
	}
	if err := signSums(root, key); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := releaseIdentity(root, "1.0"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("local"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readSourceManifest(root); err != nil {
		t.Fatal("source edits must remain possible", err)
	}
	if _, err := releaseIdentity(root, "1.0"); err == nil {
		t.Fatal("dirty archive accepted as upstream release")
	}
	m, err := readSourceManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	m.Source.Commit = strings.Repeat("c", 40)
	if err := jsonFile(filepath.Join(root, "auriga-source.json"), m, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readSourceManifest(root); err == nil {
		t.Fatal("accepted altered signed baseline")
	}
}

func TestChangesRebaseConflictAndTraversal(t *testing.T) {
	original := t.TempDir()
	r := release{Version: "1.0", Build: &buildSpec{}}
	for name, body := range map[string]string{"change.txt": "before", "delete.txt": "delete"} {
		if err := os.WriteFile(filepath.Join(original, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := signedTestSourceManifest(t, original, provenance{Commit: strings.Repeat("a", 40), Version: "1.0", UpstreamVersion: "1.0"}, strings.Repeat("b", 40), r); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "next")
	if err := copyTree(original, target); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(original, "change.txt"), []byte("after"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(original, "delete.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(original, "add.txt"), []byte("added"), 0644); err != nil {
		t.Fatal(err)
	}
	patch := filepath.Join(t.TempDir(), "site.tar")
	if err := exportChanges(original, patch); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	if err := applyChanges(target, patch); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(filepath.Join(target, "change.txt")); err != nil || string(raw) != "after" {
		t.Fatal(string(raw), err)
	}
	if _, err := os.Stat(filepath.Join(target, "delete.txt")); !os.IsNotExist(err) {
		t.Fatal("deletion lost", err)
	}
	if err := applyChanges(target, patch); err != nil {
		t.Fatal("retry must be idempotent", err)
	}
	if err := os.WriteFile(filepath.Join(target, "change.txt"), []byte("new upstream conflict"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := applyChanges(target, patch); err == nil || !strings.Contains(err.Error(), "change.txt") {
		t.Fatal("conflict not named", err)
	}
	if sourcePathAllowed("../escape") || sourcePathAllowed(".git/config") || sourcePathAllowed("auriga-source.json") || sourcePathAllowed("SHA256SUMS") {
		t.Fatal("unsafe patch target")
	}
	if !sourcePathAllowed("docs/company-uac/SHA256SUMS") || !sourcePathAllowed("docs/auriga-source.json") {
		t.Fatal("nested source file matching root metadata name should be allowed")
	}
}
