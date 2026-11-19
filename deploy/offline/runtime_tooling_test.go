package main

import (
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeToolingOriginalIntegrityAndLock(t *testing.T) {
	source := t.TempDir()
	data := []byte("fixture original distribution")
	sum := sha512.Sum512(data)
	integrity := "sha512-" + base64.StdEncoding.EncodeToString(sum[:])
	writeReleaseFixture(t, filepath.Join(source, "clients/installer/upstream.json"), []byte(`{"pnpm":"11.28.5","pnpmIntegrity":"`+integrity+`"}`))
	writeReleaseFixture(t, filepath.Join(source, "deploy/compose/real-dsh/tooling/package.json"), []byte(`{"name":"fixture","dependencies":{"pnpm":"11.28.5"}}`))
	lock := `{"packages":{"":{"dependencies":{"pnpm":"11.28.5"}},"node_modules/pnpm":{"version":"11.28.5","integrity":"` + integrity + `","resolved":"https://registry.npmjs.org/pnpm/-/pnpm-11.28.5.tgz"}}}`
	file := filepath.Join(source, "original.tgz")
	writeReleaseFixture(t, file, data)
	writeReleaseFixture(t, filepath.Join(source, "deploy/compose/real-dsh/tooling/package-lock.json"), []byte(lock))
	if err := verifyRuntimeToolingInput(t.Context(), source, file); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("tampered"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := verifyRuntimeToolingInput(t.Context(), source, file); err == nil {
		t.Fatal("accepted modified distribution")
	}
	writeReleaseFixture(t, file, data)
	writeReleaseFixture(t, filepath.Join(source, "deploy/compose/real-dsh/tooling/package-lock.json"), []byte(strings.ReplaceAll(lock, "11.28.5", "11.28.6")))
	if err := verifyRuntimeToolingInput(t.Context(), source, file); err == nil {
		t.Fatal("accepted build version in runtime lock")
	}
}

func TestRuntimeToolingChangesDependencyIdentityAndRoundTrip(t *testing.T) {
	r := release{Build: &buildSpec{}}
	source := t.TempDir()
	first, err := locksFromNamesContext(t.Context(), source, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Build.RuntimeTooling = &artifact{URL: "approved-original.tgz", SHA256: strings.Repeat("a", 64)}
	second, err := locksFromNamesContext(t.Context(), source, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest == second.Digest {
		t.Fatal("runtime tool omitted from source dependency identity")
	}
	r.Build.RuntimeTooling.URL = "mirror/same-bytes.tgz"
	third, err := locksFromNamesContext(t.Context(), source, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.Digest != third.Digest {
		t.Fatal("mirror location changed byte identity")
	}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip release
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if roundTrip.Build.RuntimeTooling.SHA256 != r.Build.RuntimeTooling.SHA256 {
		t.Fatal("runtime tooling pin was lost")
	}
}

func TestRuntimeToolingMustSurviveCumulativeInventory(t *testing.T) {
	root := fixtureSingleKit(t, "1.0", "a")
	var kit devkitManifest
	if err := readJSON(filepath.Join(root, "devkit.json"), &kit); err != nil {
		t.Fatal(err)
	}
	data := []byte("original pinned distribution")
	file := filepath.Join(root, "cache/runtime-tooling.tgz")
	writeReleaseFixture(t, file, data)
	sha, err := digest(file)
	if err != nil {
		t.Fatal(err)
	}
	kit.Release.Build.RuntimeTooling = &artifact{URL: "runtime-tooling.tgz", SHA256: sha}
	if err := jsonFile(filepath.Join(root, "devkit.json"), kit, 0644); err != nil {
		t.Fatal(err)
	}
	key, _ := testSigningKeys(t)
	signTestRoot(t, root, key)
	target := filepath.Join(t.TempDir(), "catalog")
	cat, err := composeDevkit("", root, "1.0", strings.Repeat("a", 40), target, key, 1<<30, false)
	if err != nil {
		t.Fatal(err)
	}
	entry := cat.Versions["1.0"]
	if entry.Files["runtime-tooling.tgz"] != sha {
		t.Fatal("runtime tool dropped from cumulative blobs")
	}
	delete(entry.Files, "runtime-tooling.tgz")
	cat.Versions["1.0"] = entry
	if err := validateCatalog(cat); err == nil {
		t.Fatal("accepted missing runtime tool blob")
	}
}
