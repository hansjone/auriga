package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixtureSingleKit(t *testing.T, version, marker string) string {
	t.Helper()
	root := t.TempDir()
	var r release
	if err := readJSON("release.example.json", &r); err != nil {
		t.Fatal(err)
	}
	r.Version = version
	r.NAS.URL = "nas.tar"
	r.Build.Inputs.URL = "inputs.tar"
	r.Build.Recipes.URL = "recipes.tar"
	kit := devkitManifest{Schema: 1, Release: r, Source: &provenance{Commit: strings.Repeat("a", 40), Version: version, UpstreamVersion: version}, Locks: &lockInventory{Digest: strings.Repeat(marker, 64), Files: map[string]string{"go.mod": strings.Repeat(marker, 64)}}}
	refs, err := buildImageRefs(r)
	if err != nil {
		t.Fatal(err)
	}
	for role, ref := range refs {
		kit.Images = append(kit.Images, kitImage{Role: role, Ref: ref, Tag: kitImageTag(role, ref), File: "images/" + role + ".tar"})
	}
	for _, name := range []string{"nas.tar", "inputs.tar", "recipes.tar"} {
		path := filepath.Join(root, "cache", name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(name+marker), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, image := range kit.Images {
		path := filepath.Join(root, "cache", image.File)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(image.Role), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := jsonFile(filepath.Join(root, "devkit.json"), kit, 0644); err != nil {
		t.Fatal(err)
	}
	if err := writeSums(root); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestCumulativeDevkitDeltaMergeCoverageAndReset(t *testing.T) {
	key, _ := testSigningKeys(t)
	single := fixtureSingleKit(t, "1.0", "a")
	first := filepath.Join(t.TempDir(), "full")
	if _, err := composeDevkit("", single, "1.0", strings.Repeat("a", 40), first, key, 1<<30, false); err != nil {
		t.Fatal(err)
	}
	next := filepath.Join(t.TempDir(), "full")
	single2 := fixtureSingleKit(t, "1.1", "b")
	cat, err := composeDevkit(first, single2, "1.1", strings.Repeat("b", 40), next, key, 1<<30, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Versions) != 2 || cat.Baseline != "1.0" {
		t.Fatal(cat)
	}
	if len(cat.Blobs) >= 2*len(cat.Versions["1.0"].Files) {
		t.Fatal("shared image archives not deduplicated")
	}
	delta := filepath.Join(t.TempDir(), "delta.tar")
	if err := writeDevkitDelta(first, next, delta, key); err != nil {
		t.Fatal(err)
	}
	if err := mergeDevkit(first, delta, ""); err != nil {
		t.Fatal(err)
	}
	if err := mergeDevkit(first, delta, ""); err != nil {
		t.Fatal("retry should be idempotent", err)
	}
	merged, err := readCatalog(first, "")
	if err != nil || len(merged.Versions) != 2 {
		t.Fatal(merged, err)
	}
	if _, err := catalogVersion(merged, "1.0"); err != nil {
		t.Fatal(err)
	}
	if _, err := catalogVersion(merged, "1.9"); err == nil {
		t.Fatal("accepted uncovered version")
	}
	for version, expected := range map[string]string{"1.0": "inputs.tara", "1.1": "inputs.tarb"} {
		selected, err := materializeVersion(first, merged.Versions[version])
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(filepath.Join(selected, "cache/inputs.tar"))
		_ = os.RemoveAll(selected)
		if err != nil || string(raw) != expected {
			t.Fatal("old/new dependencies must coexist", version, string(raw), err)
		}
	}
	wrong := filepath.Join(t.TempDir(), "wrong")
	if _, err := composeDevkit("", single, "1.0", strings.Repeat("c", 40), wrong, key, 1<<30, false); err != nil {
		t.Fatal(err)
	}
	if err := mergeDevkit(wrong, delta, ""); err == nil {
		t.Fatal("accepted wrong base")
	}
	reset := filepath.Join(t.TempDir(), "reset")
	cat, err = composeDevkit(first, fixtureSingleKit(t, "2.0", "c"), "2.0", strings.Repeat("c", 40), reset, key, 1<<30, false)
	if err != nil || cat.Baseline != "2.0" || len(cat.Versions) != 1 {
		t.Fatal(cat, err)
	}
	tiny := filepath.Join(t.TempDir(), "threshold")
	cat, err = composeDevkit(first, single2, "1.2", strings.Repeat("b", 40), tiny, key, 1, false)
	if err == nil {
		t.Fatal("single version over threshold must fail", cat)
	}
	var singleBytes int64
	for _, name := range []string{"nas.tar", "inputs.tar", "recipes.tar"} {
		st, err := os.Stat(filepath.Join(single2, "cache", name))
		if err != nil {
			t.Fatal(err)
		}
		singleBytes += st.Size()
	}
	for _, image := range cat.Versions["1.2"].Kit.Images {
		st, err := os.Stat(filepath.Join(single2, "cache", image.File))
		if err != nil {
			t.Fatal(err)
		}
		singleBytes += st.Size()
	}
	threshold := filepath.Join(t.TempDir(), "rebase")
	cat, err = composeDevkit(first, single2, "1.2", strings.Repeat("b", 40), threshold, key, singleBytes, false)
	if err != nil || cat.Baseline != "1.2" || len(cat.Versions) != 1 || cat.ResetReason == "" {
		t.Fatal("threshold must open fresh baseline", cat, err)
	}
	resetDelta := filepath.Join(t.TempDir(), "reset.tar")
	if err := writeDevkitDelta(first, threshold, resetDelta, key); err != nil {
		t.Fatal(err)
	}
	if err := mergeDevkit(first, resetDelta, ""); err == nil {
		t.Fatal("merged fresh baseline over old history")
	}
	before, err := digest(filepath.Join(first, "devkit-catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	badDelta := filepath.Join(t.TempDir(), "bad.tar")
	raw, err := os.ReadFile(delta)
	if err != nil {
		t.Fatal(err)
	}
	raw[100] ^= 1
	if err := os.WriteFile(badDelta, raw, 0644); err != nil {
		t.Fatal(err)
	}
	if err := mergeDevkit(first, badDelta, ""); err == nil {
		t.Fatal("accepted damaged delta")
	}
	after, err := digest(filepath.Join(first, "devkit-catalog.json"))
	if err != nil || before != after {
		t.Fatal("damaged delta changed original kit", err)
	}
}
