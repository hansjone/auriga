package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runtimeDependencyFixture(t *testing.T) (string, string, *runtimeDependencyInputs, string) {
	t.Helper()
	source := t.TempDir()
	directory := t.TempDir()
	for _, name := range runtimeDependencySources {
		raw, err := os.ReadFile(filepath.Join("../..", filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		writeReleaseFixture(t, filepath.Join(source, filepath.FromSlash(name)), raw)
	}
	for _, name := range []string{"corepack.tgz", "web-cache.tar.gz", "npm-cache.tar.gz"} {
		writeReleaseFixture(t, filepath.Join(directory, name), []byte("fixture raw archive "+name))
	}
	node := os.Getenv("AURIGA_RUNTIME_DEPENDENCY_TEST_NODE")
	if node == "" {
		var err error
		node, err = exec.LookPath("node")
		if err != nil {
			t.Skip("Node is required for the public envelope producer interoperability check")
		}
	}
	module, err := filepath.Abs("../compose/real-dsh/dependencies.mjs")
	if err != nil {
		t.Fatal(err)
	}
	config := "sha256:daa74c183f7d8c1ba55ed79c76e912f50be8782ca9d2da640645f906dce474b8"
	if out, err := exec.Command(node, "--max-old-space-size=512", module, "seal", source, directory, config, strings.Repeat("a", 64)).CombinedOutput(); err != nil {
		t.Fatalf("pure Node envelope producer failed: %v %s", err, out)
	}
	inputs := &runtimeDependencyInputs{}
	for name, a := range inputs.artifacts() {
		sha, err := digest(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		a.URL = "runtime-dependencies/" + name
		a.SHA256 = sha
	}
	return source, directory, inputs, config
}

func TestRuntimeDependenciesPublicEnvelopeInterop(t *testing.T) {
	source, directory, inputs, config := runtimeDependencyFixture(t)
	if err := verifyRuntimeDependenciesInput(t.Context(), source, directory, inputs, config); err != nil {
		t.Fatal(err)
	}
	var m struct {
		NodeImage struct {
			Ref string `json:"ref"`
		} `json:"nodeImage"`
	}
	raw, err := os.ReadFile(filepath.Join(directory, "runtime-dependencies.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if err := verifyRuntimeDependenciesInput(t.Context(), source, directory, inputs, m.NodeImage.Ref); err != nil {
		t.Fatal(err)
	}
	if err := verifyRuntimeDependenciesInput(t.Context(), source, directory, inputs, "node:latest"); err == nil {
		t.Fatal("accepted unreviewed runtime image")
	}
	writeReleaseFixture(t, filepath.Join(directory, "npm-cache.tar.gz"), []byte("tampered"))
	if err := verifyRuntimeDependenciesInput(t.Context(), source, directory, inputs, config); err == nil {
		t.Fatal("accepted modified cache")
	}
}

func TestRuntimeDependenciesChangeLockIdentityAndRejectStaleSource(t *testing.T) {
	source, directory, inputs, config := runtimeDependencyFixture(t)
	r := release{Build: &buildSpec{}}
	first, err := locksFromNamesContext(t.Context(), source, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Build.RuntimeDependencies = inputs
	second, err := locksFromNamesContext(t.Context(), source, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest == second.Digest {
		t.Fatal("closure omitted from dependency identity")
	}
	writeReleaseFixture(t, filepath.Join(source, "clients/dsh-plugin/package.json"), []byte("{\"exports\":{\"./new\":\"./new.js\"}}"))
	if err := verifyRuntimeDependenciesInput(t.Context(), source, directory, inputs, config); err == nil {
		t.Fatal("accepted stale manifest after source foundation change")
	}
}

func TestRuntimeDependenciesCumulativeCarriesEveryApprovedBlob(t *testing.T) {
	_, directory, inputs, config := runtimeDependencyFixture(t)
	root := fixtureSingleKit(t, "1.0", "a")
	var kit devkitManifest
	if err := readJSON(filepath.Join(root, "devkit.json"), &kit); err != nil {
		t.Fatal(err)
	}
	kit.Release.Build.RuntimeDependencies = inputs
	kit.Release.Build.Tools["runtime"] = config
	for name := range inputs.artifacts() {
		raw, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		writeReleaseFixture(t, filepath.Join(root, "cache/runtime-dependencies", name), raw)
	}
	entry := kitImage{Role: "tool-runtime", Ref: config, Tag: kitImageTag("tool-runtime", config), File: "images/tool-runtime.tar"}
	kit.Images = append(kit.Images, entry)
	writeReleaseFixture(t, filepath.Join(root, "cache", entry.File), []byte("fixture reviewed image archive"))
	if err := jsonFile(filepath.Join(root, "devkit.json"), kit, 0644); err != nil {
		t.Fatal(err)
	}
	key, _ := testSigningKeys(t)
	signTestRoot(t, root, key)
	cat, err := composeDevkit("", root, "1.0", strings.Repeat("a", 40), filepath.Join(t.TempDir(), "catalog"), key, 1<<30, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range inputs.artifacts() {
		if cat.Versions["1.0"].Files[a.URL] != a.SHA256 {
			t.Fatal("closure blob dropped")
		}
	}
	version := cat.Versions["1.0"]
	delete(version.Files, inputs.NpmCache.URL)
	cat.Versions["1.0"] = version
	if err := validateCatalog(cat); err == nil {
		t.Fatal("accepted omitted npm closure blob")
	}
}
