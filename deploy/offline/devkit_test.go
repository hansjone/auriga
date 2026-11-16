package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOfflineFetchRejectsNetworkAndMissingDependency(t *testing.T) {
	a := artifact{URL: "https://example.com/dependency.tar", SHA256: strings.Repeat("a", 64)}
	if err := fetchInput(a, filepath.Join(t.TempDir(), "file"), buildOptions{Offline: true}); err == nil {
		t.Fatal("accepted network input")
	}
	if err := fetchInput(artifact{URL: "missing.tar", SHA256: a.SHA256}, filepath.Join(t.TempDir(), "file"), buildOptions{Offline: true, Cache: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "missing.tar") {
		t.Fatalf("missing dependency not named %v", err)
	}
}

func TestDevkitCacheAllowsSourceEditsRejectsCacheTampering(t *testing.T) {
	root := t.TempDir()
	for name, content := range map[string]string{"cache/input.tar": "dependency", "source/main.go": "source", "devkit.json": "manifest"} {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeSums(root); err != nil {
		t.Fatal(err)
	}
	key, _ := testSigningKeys(t)
	signTestRoot(t, root, key)
	if err := os.WriteFile(filepath.Join(root, "source/main.go"), []byte("site edit"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := verifyDevkitCache(root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cache/input.tar"), []byte("tampered"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := verifyDevkitCache(root); err == nil {
		t.Fatal("accepted modified cache")
	}
}

func TestDevkitRequiresCompleteImageInventory(t *testing.T) {
	var r release
	if err := readJSON("release.example.json", &r); err != nil {
		t.Fatal(err)
	}
	refs, err := buildImageRefs(r)
	if err != nil {
		t.Fatal(err)
	}
	kit := devkitManifest{Schema: 1, Release: r, Source: &provenance{}}
	for role, ref := range refs {
		kit.Images = append(kit.Images, kitImage{Role: role, Ref: ref, Tag: kitImageTag(role, ref), File: "images/" + role + ".tar"})
	}
	if err := validateDevkit(kit); err != nil {
		t.Fatal(err)
	}
	kit.Images = kit.Images[1:]
	if err := validateDevkit(kit); err == nil {
		t.Fatal("accepted incomplete image inventory")
	}
	delete(r.Build.Tools, "go")
	if _, err := buildImageRefs(r); err == nil || !strings.Contains(err.Error(), "go") {
		t.Fatalf("missing tool not named %v", err)
	}
}
