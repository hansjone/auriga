package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
)

func lockedDSHVersion(t *testing.T) string {
	t.Helper()
	read := func(name string, target any) {
		path := filepath.Join("..", "real-dsh", name)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, target); err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
	}
	var lock struct {
		Packages map[string]struct{ Version string }
	}
	var versions struct{ DSHVersion string }
	read("package-lock.json", &lock)
	read("versions.json", &versions)
	version := lock.Packages["node_modules/@deepseek-ai/dsh"].Version
	if version == "" || versions.DSHVersion != version {
		t.Fatalf("real DSH version pin %q differs from npm lock %q", versions.DSHVersion, version)
	}
	return version
}

// Exercise the actual CLI and protobuf for each fixture in a fresh installation.
func TestFixtureTemplates(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "bootstrap")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	if output, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build bootstrap: %v: %s", err, output)
	}
	for _, tc := range []struct {
		name    string
		fixture string
	}{
		{"fake", "fake"},
		{"real", "real"},
		{"real-with-metadata", "real"},
	} {
		fixture := tc.fixture
		proofRoot, image := runtimeProofFixture(t, fixture)
		digest := digestOf(image)
		out := filepath.Join(root, tc.name)
		args := []string{"-fixture", fixture, "-image", image, "-out", out, "-runtime-proofs", proofRoot}
		name, version := fixture+"-dsh", "0.0.0-fake"
		if fixture == "real" {
			args = append(args, "-heartbeat-observer")
			version = lockedDSHVersion(t)
		}
		if tc.name == "real-with-metadata" {
			metadata := filepath.Join(root, "real-dsh.json")
			args = append(args, "-real-dsh-metadata", metadata)
			for _, invalid := range []struct {
				name    string
				image   string
				version string
			}{
				{"stale-version", image, "0.0.0-stale"},
				{"different-image", "127.0.0.1:5000/auriga/real-dsh@sha256:" + strings.Repeat("f", 64), version},
			} {
				t.Run(invalid.name, func(t *testing.T) {
					writeJSON(metadata, map[string]string{"image": invalid.image, "dshVersion": invalid.version}, 0o644)
					if output, err := exec.Command(binary, args...).CombinedOutput(); err == nil ||
						!strings.Contains(string(output), "real DSH build metadata must match") {
						t.Fatalf("expected bootstrap metadata mismatch: %v: %s", err, output)
					}
					if _, err := os.Stat(out); !os.IsNotExist(err) {
						t.Fatalf("invalid metadata created installation output: %v", err)
					}
				})
			}
			writeJSON(metadata, map[string]string{"image": image, "dshVersion": version}, 0o644)
		}
		if err := os.MkdirAll(filepath.Join(out, "templates"), 0o755); err != nil {
			t.Fatal(err)
		}
		if output, err := exec.Command(binary, args...).CombinedOutput(); err != nil {
			t.Fatalf("bootstrap %s: %v: %s", tc.name, err, output)
		}
		assertAccessMaterials(t, out, fixture == "real")
		properties, err := os.ReadFile(filepath.Join(out, "network-platform", "platform.properties"))
		if err != nil || strings.Contains(string(properties), "auriga.iam.clients") {
			t.Fatal("default HTTP recipe gained a native registration")
		}
		entries, err := os.ReadDir(filepath.Join(out, "templates"))
		if err != nil || len(entries) != 1 || entries[0].Name() != name+".pb" {
			t.Fatalf("stale or missing template after %s: %v %v", fixture, entries, err)
		}
		raw, err := os.ReadFile(filepath.Join(out, "templates", name+".pb"))
		if err != nil {
			t.Fatal(err)
		}
		var spec nodev1.InstanceSpec
		if err := proto.Unmarshal(raw, &spec); err != nil {
			t.Fatal(err)
		}
		if spec.GetDsh().GetImage() != image || spec.GetDsh().GetDigest() != "" ||
			spec.GetDsh().GetArtifactId() != name ||
			spec.GetLabels()["template"] != name+"-e2e" || spec.GetProfile().GetListenPort() != 7860 {
			t.Fatalf("wrong runtime binding in %s template", fixture)
		}
		if got := spec.GetDsh().GetVersion(); got != version {
			t.Fatalf("%s template DSH version = %q, want %q", fixture, got, version)
		}
		patches := spec.GetProfile().GetPatches()
		if spec.GetNetwork().GetEgressProxy() != "http://auriga-egress.internal:3128" {
			t.Fatal("fixture has no trusted instance egress")
		}
		if fixture == "real" {
			if len(patches) != 2 || patches[0].GetPath() != "plugins.auriga-dsh.imBots" || patches[0].GetValueJson() != `"none"` ||
				patches[1].GetPath() != "plugins.auriga-dsh.terminalSources" || patches[1].GetValueJson() != `"browser-only"` {
				t.Fatalf("real template must declare no IM and browser-only terminal sources: %v", patches)
			}
		} else if len(patches) != 0 {
			t.Fatalf("fake template must not declare plugin config: %v", patches)
		}
		release := spec.GetDsh().GetReleaseEnvelope()
		if spec.GetDsh().GetSigned() != nil || spec.GetDsh().GetUrl() != "" || release.GetName() != "dsh" || release.GetArtifactId() != name || release.GetDshVersion() != version || release.GetImageDigest() != digest || release.GetArchiveDigest() != "" {
			t.Fatalf("wrong formal Docker-only release in %s", fixture)
		}
		if err := validateRuntimeTemplate(out); err != nil {
			t.Fatal(err)
		}
	}
}
