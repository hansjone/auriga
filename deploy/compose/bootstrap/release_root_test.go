package main

import (
	"bytes"
	"encoding/base64"
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

func TestRuntimeRootCLIReceiptAndReuse(t *testing.T) {
	base := t.TempDir()
	binary := filepath.Join(base, "bootstrap")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	for _, mode := range []string{"default", "domain", "ip"} {
		t.Run(mode, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "installation")
			proofRoot, image := runtimeProofFixture(t, "fake")
			args := []string{"-out", out, "-image", image, "-runtime-proofs", proofRoot}
			if mode != "default" {
				access, _ := json.Marshal(customAccess(mode))
				input := filepath.Join(t.TempDir(), "access.json")
				if err := os.WriteFile(input, access, 0o600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "-access-file", input, "-edge-container", "public-test-edge")
			}
			run := func() error {
				output, err := exec.Command(binary, args...).CombinedOutput()
				if err != nil {
					t.Logf("CLI rejected: %s", output)
				}
				return err
			}
			if err := run(); err != nil {
				t.Fatal(err)
			}
			if err := validateRuntimeRoot(out); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(out, "runtime-keys", "root.pb")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var key nodev1.ArtifactSigningKey
			if err := proto.Unmarshal(raw, &key); err != nil {
				t.Fatal(err)
			}
			var identity struct{ Keys []struct{ X string } }
			pubJSON, err := os.ReadFile(filepath.Join(out, "identity-keys.json"))
			if err != nil || json.Unmarshal(pubJSON, &identity) != nil {
				t.Fatal("identity public fixture invalid")
			}
			if key.Kid != "e2e-runtime-root" || len(key.PublicKey) != 32 || len(identity.Keys) != 1 || base64.RawURLEncoding.EncodeToString(key.PublicKey) == identity.Keys[0].X {
				t.Fatal("wrong runtime root")
			}
			before := installationSnapshot(t, out)
			if err := run(); err != nil {
				t.Fatal(err)
			}
			if installationSnapshot(t, out) != before {
				t.Fatal("reuse changed fixed root/materials")
			}
			for _, failure := range []string{"missing root", "damaged root", "old receipt missing leaf", "unknown root file", "damaged proof", "missing proof", "unknown proof file", "missing proof root"} {
				t.Run(failure, func(t *testing.T) {
					var restore func()
					switch failure {
					case "damaged proof", "missing proof":
						proof := filepath.Join(out, "runtime-proofs", strings.TrimPrefix(digestOf(image), "sha256:"), "manifest.json")
						original, err := os.ReadFile(proof)
						if err != nil {
							t.Fatal(err)
						}
						if failure == "missing proof" {
							err = os.Remove(proof)
						} else {
							err = os.WriteFile(proof, []byte("damaged"), 0o644)
						}
						if err != nil {
							t.Fatal(err)
						}
						restore = func() {
							if err := os.WriteFile(proof, original, 0o644); err != nil {
								t.Fatal(err)
							}
						}
					case "unknown proof file":
						proof := filepath.Join(out, "runtime-proofs", strings.TrimPrefix(digestOf(image), "sha256:"), "extra.json")
						if err := os.WriteFile(proof, []byte("extra"), 0o644); err != nil {
							t.Fatal(err)
						}
						restore = func() {
							if err := os.Remove(proof); err != nil {
								t.Fatal(err)
							}
						}
					case "missing proof root":
						proof := filepath.Join(out, "runtime-proofs")
						hidden := filepath.Join(t.TempDir(), "proofs")
						if err := os.Rename(proof, hidden); err != nil {
							t.Fatal(err)
						}
						restore = func() {
							if err := os.Rename(hidden, proof); err != nil {
								t.Fatal(err)
							}
						}
					case "missing root":
						if err := os.Remove(path); err != nil {
							t.Fatal(err)
						}
						restore = func() {
							if err := os.WriteFile(path, raw, 0o644); err != nil {
								t.Fatal(err)
							}
						}
					case "damaged root":
						bad := proto.Clone(&key).(*nodev1.ArtifactSigningKey)
						bad.NotAfter.Seconds++
						changed, _ := proto.Marshal(bad)
						if err := os.WriteFile(path, changed, 0o644); err != nil {
							t.Fatal(err)
						}
						restore = func() {
							if err := os.WriteFile(path, raw, 0o644); err != nil {
								t.Fatal(err)
							}
						}
					case "old receipt missing leaf":
						if err := os.Remove(path); err != nil {
							t.Fatal(err)
						}
						if err := os.Remove(filepath.Dir(path)); err != nil {
							t.Fatal(err)
						}
						restore = func() {
							if err := os.Mkdir(filepath.Dir(path), 0o755); err != nil {
								t.Fatal(err)
							}
							if err := os.WriteFile(path, raw, 0o644); err != nil {
								t.Fatal(err)
							}
						}
					case "unknown root file":
						p := filepath.Join(filepath.Dir(path), "unknown.pb")
						if err := os.WriteFile(p, []byte("unknown"), 0o644); err != nil {
							t.Fatal(err)
						}
						restore = func() {
							if err := os.Remove(p); err != nil {
								t.Fatal(err)
							}
						}
					}
					defer restore()
					snapshot := installationSnapshot(t, out)
					if err := run(); err == nil {
						t.Fatal("damaged/old root accepted")
					}
					if installationSnapshot(t, out) != snapshot {
						t.Fatal("failed reuse repaired or modified installation")
					}
				})
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(raw, after) {
				t.Fatal("root rotated")
			}
		})
	}
}

func TestRuntimeRootStrictPreflight(t *testing.T) {
	for _, name := range []string{"identity-keys.json", "iam-installation/bootstrap.receipt", "runtime-keys/root.pb"} {
		t.Run(name, func(t *testing.T) {
			out := t.TempDir()
			p := filepath.Join(out, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte("old material"), 0o600); err != nil {
				t.Fatal(err)
			}
			snapshot := installationSnapshot(t, out)
			if err := prepareRuntimeRootDirectory(out); err == nil && name != "runtime-keys/root.pb" {
				t.Fatal("old installation gained runtime root directory")
			}
			if installationSnapshot(t, out) != snapshot {
				t.Fatal("preflight changed old installation")
			}
		})
	}
}
func TestM2RuntimeRootReadOnlyMount(t *testing.T) {
	raw, err := os.ReadFile("../docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	start, end := strings.Index(text, "  platform:"), strings.Index(text, "  edge:")
	if start < 0 || end <= start {
		t.Fatal("platform service not found")
	}
	platform := text[start:end]
	if !strings.Contains(platform, "AURIGA_WORKSPACE_ARTIFACT_KEYS_DIRECTORY: /etc/auriga/runtime-keys") || !strings.Contains(platform, "./var/runtime-keys:/etc/auriga/runtime-keys:ro") {
		t.Fatal("runtime root must use existing configuration and a read-only public mount")
	}
	if strings.Contains(text[end:], "runtime-keys") {
		t.Fatal("test release root leaked into another service's materials")
	}
}
