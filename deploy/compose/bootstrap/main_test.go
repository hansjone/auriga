package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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

func realFixtureMetadata(t *testing.T, image string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "real-dsh.json")
	writeJSON(path, map[string]string{"image": image, "dshVersion": lockedDSHVersion(t)}, 0o600)
	return path
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
	for _, fixture := range []string{"fake", "real"} {
		proofRoot, image := runtimeProofFixture(t, fixture)
		digest := digestOf(image)
		out := filepath.Join(root, fixture)
		args := []string{"-fixture", fixture, "-image", image, "-out", out, "-runtime-proofs", proofRoot}
		name, version := fixture+"-dsh", "0.0.0-fake"
		var reuseArgs []string
		if fixture == "real" {
			args = append(args, "-heartbeat-observer")
			reuseArgs = args
			version = lockedDSHVersion(t)
			metadata := filepath.Join(root, "real-dsh.json")
			t.Run("old-archive", func(t *testing.T) {
				// An old image can still supply correct OCI hashes and platform.
				proofs, oldImage := runtimeProofFixtureWithConfig(t, "real",
					[]byte(`{"architecture":"amd64","os":"linux","created":"2025-01-01T00:00:00Z"}`))
				if _, _, err := loadRuntimeProofs(proofs, oldImage); err != nil {
					t.Fatalf("old archive must have valid raw OCI proofs: %v", err)
				}
				for _, tc := range []struct {
					name, want string
					evidence   []string
				}{
					{"without-metadata", "requires -real-dsh-metadata", nil},
					{"with-rc2-metadata", "real DSH build metadata must match", []string{"-real-dsh-metadata", metadata}},
				} {
					t.Run(tc.name, func(t *testing.T) {
						oldOut := filepath.Join(root, tc.name)
						oldArgs := []string{"-fixture", "real", "-image", oldImage, "-out", oldOut, "-runtime-proofs", proofs}
						writeJSON(metadata, map[string]string{"image": oldImage, "dshVersion": "0.2.0-rc.2"}, 0o600)
						if output, err := exec.Command(binary, append(oldArgs, tc.evidence...)...).CombinedOutput(); err == nil ||
							!strings.Contains(string(output), tc.want) {
							t.Fatalf("expected rejection of old archive: %v: %s", err, output)
						}
						if _, err := os.Stat(oldOut); !os.IsNotExist(err) {
							t.Fatalf("old archive wrote installation materials: %v", err)
						}
					})
				}
			})
			args = append(args, "-real-dsh-metadata", metadata)
			t.Run("different-image", func(t *testing.T) {
				otherImage := "127.0.0.1:5000/auriga/real-dsh@sha256:" + strings.Repeat("f", 64)
				writeJSON(metadata, map[string]string{"image": otherImage, "dshVersion": version}, 0o600)
				if output, err := exec.Command(binary, args...).CombinedOutput(); err == nil ||
					!strings.Contains(string(output), "real DSH build metadata must match") {
					t.Fatalf("expected bootstrap metadata mismatch: %v: %s", err, output)
				}
				if _, err := os.Stat(out); !os.IsNotExist(err) {
					t.Fatalf("invalid metadata created installation output: %v", err)
				}
			})
			writeJSON(metadata, map[string]string{"image": image, "dshVersion": version}, 0o644)
		}
		if err := os.MkdirAll(filepath.Join(out, "templates"), 0o755); err != nil {
			t.Fatal(err)
		}
		if output, err := exec.Command(binary, args...).CombinedOutput(); err != nil {
			t.Fatalf("bootstrap %s: %v: %s", fixture, err, output)
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
		if fixture == "real" {
			t.Run("sealed-rc2", func(t *testing.T) {
				checkLegacyRealFixture(t, binary, out, reuseArgs, &spec)
			})
		}
	}
}

func checkLegacyRealFixture(t *testing.T, binary, out string, reuseArgs []string, spec *nodev1.InstanceSpec) {
	t.Helper()
	// In this disposable installation, seal the template with the historical
	// version under a new test-only signer. Normal reentry must keep that seal.
	receipt := filepath.Join(out, "iam-installation", "bootstrap.receipt")
	raw, err := os.ReadFile(receipt)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{receipt, filepath.Join(out, "runtime-keys", "root.pb")} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	signer, err := generateRuntimeRootSigner(out)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(signer)
	spec.Dsh.Version = "0.2.0-rc.2"
	if err := signRuntimeRelease(spec.Dsh, signer, time.Now()); err != nil {
		t.Fatal(err)
	}
	template, err := proto.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "templates", "real-dsh.pb"), template, 0o644); err != nil {
		t.Fatal(err)
	}
	inputDigest, _, _ := strings.Cut(string(raw), "\n")
	if err := writeIdentityManifest(out, inputDigest); err != nil {
		t.Fatal(err)
	}
	before := installationSnapshot(t, out)
	if output, err := exec.Command(binary, reuseArgs...).CombinedOutput(); err != nil ||
		!strings.Contains(string(output), "(existing installation)") {
		t.Fatalf("legacy reuse without build metadata: %v: %s", err, output)
	}
	if output, err := exec.Command(binary, "-out", out, "-print-compose-project").CombinedOutput(); err != nil ||
		strings.TrimSpace(string(output)) != "auriga-m2" {
		t.Fatalf("legacy public start preflight: %v: %s", err, output)
	}
	newProofs, newImage := runtimeProofFixtureWithConfig(t, "real",
		[]byte(`{"architecture":"amd64","os":"linux","created":"2026-01-01T00:00:00Z"}`))
	upgradeArgs := []string{"-fixture", "real", "-image", newImage, "-out", out, "-runtime-proofs", newProofs,
		"-heartbeat-observer", "-real-dsh-metadata", realFixtureMetadata(t, newImage)}
	if output, err := exec.Command(binary, upgradeArgs...).CombinedOutput(); err == nil ||
		!strings.Contains(string(output), "existing installation refused") {
		t.Fatalf("expected refusal to replace a sealed image: %v: %s", err, output)
	}
	if installationSnapshot(t, out) != before {
		t.Fatal("legacy reentry or attempted upgrade changed the sealed installation")
	}
}
