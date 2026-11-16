package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDevkitVerifyBindsNewTagAndExplainsMismatch(t *testing.T) {
	source := t.TempDir()
	for _, args := range [][]string{{"init"}, {"config", "user.name", "fixture"}, {"config", "user.email", "fixture@example.com"}} {
		if _, err := gitAt(source, args...); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range map[string]string{"go.mod": "module fixture\n", "application.txt": "original", ".gitignore": ".auriga-devkit.json\n"} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := gitAt(source, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := gitAt(source, "commit", "-m", "upstream"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitAt(source, "tag", "1.0"); err != nil {
		t.Fatal(err)
	}
	original, err := gitAt(source, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	var r release
	if err := readJSON("release.example.json", &r); err != nil {
		t.Fatal(err)
	}
	r.Version = "1.0"
	refs, err := buildImageRefs(r)
	if err != nil {
		t.Fatal(err)
	}
	locks, err := dependencyLocks(source, r)
	if err != nil {
		t.Fatal(err)
	}
	kit := devkitManifest{Schema: 1, Release: r, Locks: &locks, Source: &provenance{Commit: original, UpstreamVersion: "1.0"}}
	for role, ref := range refs {
		kit.Images = append(kit.Images, kitImage{Role: role, Ref: ref, Tag: kitImageTag(role, ref), File: "images/" + role + ".tar"})
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "cache"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cache", "input"), []byte("fixture"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := jsonFile(filepath.Join(root, "devkit.json"), kit, 0644); err != nil {
		t.Fatal(err)
	}
	if err := writeSums(root); err != nil {
		t.Fatal(err)
	}
	key, public := testSigningKeys(t)
	if err := signSums(root, key); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "application.txt"), []byte("new upstream"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := gitAt(source, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := gitAt(source, "commit", "-m", "upstream code"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitAt(source, "tag", "1.1"); err != nil {
		t.Fatal(err)
	}
	if err := devkitVerify([]string{root, "--source", source, "--verify-key", public}); err != nil {
		t.Fatal(err)
	}
	_, bound, err := locateDevkit(source)
	if err != nil {
		t.Fatal(err)
	}
	head, err := gitAt(source, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if bound.Source.Commit != head || bound.Source.UpstreamVersion != "1.1" || bound.Release.Version != "1.1" {
		t.Fatal("reused old upstream baseline", bound.Source)
	}
	if err := os.WriteFile(filepath.Join(source, "go.mod"), []byte("module changed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := locateDevkit(source); err == nil || !strings.Contains(err.Error(), "go.mod") || !strings.Contains(err.Error(), "auriga-devkit-1.1.tar") {
		t.Fatal("no actionable mismatch", err)
	}
	kit.Locks = nil
	if err := checkKitLocks(source, kit); err == nil {
		t.Fatal("legacy devkit falsely matched locks")
	}
}

func TestPartManifestRejectsTraversalAndOverallDigest(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "kit.tar")
	if err := os.WriteFile(file, []byte(strings.Repeat("a", 100)), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := splitAsset(file, 60, ""); err != nil {
		t.Fatal(err)
	}
	var manifest partsManifest
	if err := readJSON(file+".parts.json", &manifest); err != nil {
		t.Fatal(err)
	}
	original := manifest.Parts[0].Name
	manifest.Parts[0].Name = "../outside"
	if err := jsonFile(file+".parts.json", manifest, 0644); err != nil {
		t.Fatal(err)
	}
	if f, err := openArchive(file + ".parts.json"); err == nil {
		_ = f.Close()
		t.Fatal("accepted traversal")
	}
	manifest.Parts[0].Name = original
	manifest.Original.SHA256 = strings.Repeat("b", 64)
	if err := jsonFile(file+".parts.json", manifest, 0644); err != nil {
		t.Fatal(err)
	}
	if f, err := openArchive(file + ".part001"); err == nil {
		_ = f.Close()
		t.Fatal("accepted overall hash mismatch")
	}
}
