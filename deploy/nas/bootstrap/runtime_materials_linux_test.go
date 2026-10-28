package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/runtimebundle"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func runtimeTestPut(t *testing.T, path string, raw []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, raw, mode); err != nil {
		t.Fatal(err)
	}
}

func runtimeTestMarshal(t *testing.T, m proto.Message) []byte {
	t.Helper()
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func runtimeTestFixture(t *testing.T, parent string) (runtimeInputs, *runtimebundle.Material) {
	t.Helper()
	dir, err := os.MkdirTemp(parent, "runtime-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	root := &nodev1.ArtifactSigningKey{Kid: "test-publisher", PublicKey: pub, NotAfter: timestamppb.New(time.Now().Add(time.Hour))}
	r := runtimeInputs{bundle: filepath.Join(dir, "bundle"), root: filepath.Join(dir, "operator-root.pb")}
	runtimeTestPut(t, r.root, runtimeTestMarshal(t, root), 0o644)
	private := filepath.Join(dir, "publisher.pem")
	runtimeTestPut(t, private, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600)
	config := []byte(`{ "architecture":"amd64", "os":"linux", "config":{"Cmd":["/dsh"]}, "rootfs":{"type":"layers","diff_ids":["sha256:` + strings.Repeat("a", 64) + `"]} }` + "\n")
	manifest := []byte(fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:%x","size":%d},"layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar+gzip","digest":"sha256:%s","size":12}]}`+"\n", sha256.Sum256(config), len(config), strings.Repeat("b", 64)))
	configPath, manifestPath := filepath.Join(dir, "config.json"), filepath.Join(dir, "manifest.json")
	runtimeTestPut(t, configPath, config, 0o644)
	runtimeTestPut(t, manifestPath, manifest, 0o644)
	r.image = fmt.Sprintf("registry.example.com/auriga/dsh@sha256:%x", sha256.Sum256(manifest))
	m, err := runtimebundle.Sign(runtimebundle.SignOptions{Image: r.image, ArtifactID: "dsh-test-linux-amd64", Version: "1.2.3", OS: "linux", Arch: "amd64", Sequence: 7, IssuedAt: timestamppb.New(time.Now().Add(-time.Minute)), RootPath: r.root, PrivateKeyPath: private, ManifestPath: manifestPath, ConfigPath: configPath, OutputDir: r.bundle})
	if err != nil {
		t.Fatal(err)
	}
	// Sign creates a private output root. The test operator explicitly publishes
	// that new fixture before the installer sees it; production never repairs it.
	if err := os.Chmod(r.bundle, 0o755); err != nil {
		t.Fatal(err)
	}
	return r, m
}

// Called under the existing CI root test parent: this must never become an
// unrelated root-only top-level test silently skipped by the normal CI job.
func testNASSignedRuntime(t *testing.T, parent, binary, input string) {
	invoke := func(out string, r runtimeInputs, extra ...string) ([]byte, error) {
		args := []string{"-out", out, "-access-file", input, "-edge-peer-ip", "192.0.2.138", "-internal-subnet", "192.0.2.128/26", "-lan-ip", "192.0.2.10", "-lan-subnet", "192.0.2.0/26", "-runtime-bundle", r.bundle, "-runtime-root", r.root, "-runtime-image", r.image}
		return exec.Command(binary, append(args, extra...)...).CombinedOutput()
	}
	t.Run("public-snapshot-and-receipt", func(t *testing.T) {
		r, m := runtimeTestFixture(t, parent)
		// uid10001 must consume already public, read-only publisher materials.
		if err := filepath.WalkDir(r.bundle, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return os.Chmod(path, 0o555)
			}
			return os.Chmod(path, 0o444)
		}); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(r.root, 0o444); err != nil {
			t.Fatal(err)
		}
		otherRoot := filepath.Join(filepath.Dir(r.root), "other-operator-root.pb")
		runtimeTestPut(t, otherRoot, m.RootBytes(), 0o444)
		inputBefore := nasSnapshot(t, filepath.Dir(r.bundle))
		for _, memory := range []int64{512, 1024} {
			out := filepath.Join(parent, fmt.Sprintf("signed-runtime-%d", memory))
			if b, err := invoke(out, r, "-instance-max-memory-mb", fmt.Sprint(memory)); err != nil {
				t.Fatalf("install: %v %s", err, b)
			}
			assertRuntimeInstallation(t, out, r, m, memory)
			before := nasSnapshot(t, out)
			if b, err := invoke(out, r, "-instance-max-memory-mb", fmt.Sprint(memory)); err != nil {
				t.Fatalf("reentry: %v %s", err, b)
			}
			if before != nasSnapshot(t, out) || inputBefore != nasSnapshot(t, filepath.Dir(r.bundle)) {
				t.Fatal("reentry repaired/rotated source, receipt or input")
			}
			for _, extra := range [][]string{{"-instance-max-memory-mb", "768"}, {"-runtime-bundle", "", "-runtime-root", "", "-runtime-image", ""}} {
				if b, err := invoke(out, r, extra...); err == nil {
					t.Fatalf("changed inputs accepted: %s", b)
				}
				if before != nasSnapshot(t, out) {
					t.Fatal("changed input rejection changed materials")
				}
			}
			// Even an independently fixed root with identical bytes at a different
			// operator locator requires a new installation, not automatic repair.
			r2 := r
			r2.root = otherRoot
			if b, err := invoke(out, r2, "-instance-max-memory-mb", fmt.Sprint(memory)); err == nil {
				t.Fatalf("changed root input accepted: %s", b)
			}
			if before != nasSnapshot(t, out) {
				t.Fatal("changed root input rewrote materials")
			}
		}
	})
	for _, kind := range []string{"partial-input", "low-memory", "inside-root", "wrong-root", "bad-signature", "bad-manifest", "bad-config", "wrong-image", "missing-proof", "extra-proof", "extra-bundle", "proof-link", "proof-hardlink", "ancestor-link", "unreadable-root"} {
		t.Run("reject-"+kind, func(t *testing.T) {
			r, m := runtimeTestFixture(t, parent)
			proof := filepath.Join(r.bundle, filepath.FromSlash(runtimeProofDirectory(m)))
			extra := []string{}
			switch kind {
			case "partial-input":
				r.root = ""
			case "low-memory":
				extra = []string{"-instance-max-memory-mb", "255"}
			case "inside-root":
				r.root = filepath.Join(r.bundle, "runtime-keys", "root.pb")
			case "wrong-root":
				other, _ := runtimeTestFixture(t, parent)
				r.root = other.root
			case "bad-signature":
				d := m.Runtime()
				d.ReleaseEnvelope.Signature.Value = strings.Repeat("A", 86)
				runtimeTestPut(t, filepath.Join(r.bundle, "runtime.pb"), runtimeTestMarshal(t, d), 0o644)
			case "bad-manifest":
				runtimeTestPut(t, filepath.Join(proof, "manifest.json"), append(m.Manifest(), ' '), 0o644)
			case "bad-config":
				runtimeTestPut(t, filepath.Join(proof, "config.json"), append(m.Config(), ' '), 0o644)
			case "wrong-image":
				r.image = "registry.example.com/auriga/other@" + m.Runtime().GetReleaseEnvelope().GetImageDigest()
			case "missing-proof":
				if err := os.Remove(filepath.Join(proof, "config.json")); err != nil {
					t.Fatal(err)
				}
			case "extra-proof":
				runtimeTestPut(t, filepath.Join(proof, "extra.json"), []byte("{}"), 0o644)
			case "extra-bundle":
				runtimeTestPut(t, filepath.Join(r.bundle, "unexpected"), []byte("x"), 0o644)
			case "proof-link", "proof-hardlink":
				path := filepath.Join(proof, "config.json")
				outside := filepath.Join(filepath.Dir(r.bundle), "outside-config")
				if err := os.Rename(path, outside); err != nil {
					t.Fatal(err)
				}
				if kind == "proof-link" {
					if err := os.Symlink(outside, path); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Link(outside, path); err != nil {
					t.Fatal(err)
				}
			case "ancestor-link":
				alias := filepath.Join(parent, "alias-"+filepath.Base(filepath.Dir(r.bundle)))
				if err := os.Symlink(filepath.Dir(r.bundle), alias); err != nil {
					t.Fatal(err)
				}
				r.bundle = filepath.Join(alias, "bundle")
			case "unreadable-root":
				if err := os.Chmod(r.root, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			before := nasSnapshot(t, filepath.Dir(mustRealBundle(t, r.bundle)))
			out := filepath.Join(parent, "rejected-"+kind)
			if b, err := invoke(out, r, extra...); err == nil || !strings.Contains(string(b), "runtime") && !strings.Contains(string(b), "instance-max-memory-mb") {
				t.Fatalf("invalid runtime accepted/wrong rejection: %v %s", err, b)
			}
			for _, leaf := range []string{"pki", "identity-keys.json", "iam-installation"} {
				if _, err := os.Lstat(filepath.Join(out, leaf)); !os.IsNotExist(err) {
					t.Fatal("invalid runtime wrote installation leaf")
				}
			}
			if before != nasSnapshot(t, filepath.Dir(mustRealBundle(t, r.bundle))) {
				t.Fatal("input rejection repaired publisher material")
			}
			// The public shell preflight also executes verification as uid10001.
			if b, err := invoke(out+"-validate", r, append(extra, "-validate-access")...); err == nil {
				t.Fatalf("invalid preflight accepted: %s", b)
			}
			if _, err := os.Lstat(out + "-validate"); !os.IsNotExist(err) {
				t.Fatal("preflight created output")
			}
		})
	}
	for _, kind := range []string{"template-byte", "root-byte", "proof-byte", "extra-template", "extra-proof", "extra-root", "nested-dir-mode", "nested-dir-owner", "leaf-mode", "leaf-owner", "leaf-hardlink", "nested-link", "bad-receipt", "missing-receipt", "base-receipt"} {
		t.Run("reentry-reject-"+kind, func(t *testing.T) {
			r, m := runtimeTestFixture(t, parent)
			out := filepath.Join(parent, "reentry-"+kind)
			installInputs := r
			if kind == "base-receipt" {
				installInputs = runtimeInputs{}
			}
			if b, err := invoke(out, installInputs); err != nil {
				t.Fatalf("setup: %v %s", err, b)
			}
			proof := filepath.Join(out, filepath.FromSlash(runtimeProofDirectory(m)))
			leaf := filepath.Join(proof, "config.json")
			switch kind {
			case "template-byte":
				runtimeTestPut(t, filepath.Join(out, "templates/real-dsh.pb"), []byte("bad"), 0o644)
			case "root-byte":
				runtimeTestPut(t, filepath.Join(out, "runtime-keys/root.pb"), []byte("bad"), 0o644)
			case "proof-byte":
				runtimeTestPut(t, leaf, append(m.Config(), ' '), 0o644)
			case "extra-template":
				runtimeTestPut(t, filepath.Join(out, "templates/extra.pb"), []byte("bad"), 0o644)
			case "extra-proof":
				runtimeTestPut(t, filepath.Join(proof, "extra.json"), []byte("bad"), 0o644)
			case "extra-root":
				runtimeTestPut(t, filepath.Join(out, "runtime-keys/extra.pb"), []byte("bad"), 0o644)
			case "nested-dir-mode":
				if err := os.Chmod(proof, 0o775); err != nil {
					t.Fatal(err)
				}
			case "nested-dir-owner":
				if err := os.Chown(proof, 0, 0); err != nil {
					t.Fatal(err)
				}
			case "leaf-mode":
				if err := os.Chmod(leaf, 0o666); err != nil {
					t.Fatal(err)
				}
			case "leaf-owner":
				if err := os.Chown(leaf, 0, 0); err != nil {
					t.Fatal(err)
				}
			case "leaf-hardlink":
				if err := os.Link(leaf, filepath.Join(parent, "hardlink-"+filepath.Base(filepath.Dir(r.bundle)))); err != nil {
					t.Fatal(err)
				}
			case "nested-link":
				saved := filepath.Join(parent, "saved-"+filepath.Base(filepath.Dir(r.bundle)))
				if err := os.Rename(proof, saved); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(saved, proof); err != nil {
					t.Fatal(err)
				}
			case "bad-receipt":
				runtimeTestPut(t, filepath.Join(out, "iam-installation/bootstrap.receipt"), []byte("bad\n"), 0o600)
			case "missing-receipt":
				if err := os.Remove(filepath.Join(out, "iam-installation/bootstrap.receipt")); err != nil {
					t.Fatal(err)
				}
			}
			before := nasSnapshot(t, out)
			if b, err := invoke(out, r); err == nil {
				t.Fatalf("corrupt/old material accepted: %s", b)
			}
			if before != nasSnapshot(t, out) {
				t.Fatal("reentry rejection repaired source or receipt")
			}
		})
	}
}

func TestRuntimeAssemblyUsesVerifiedSnapshot(t *testing.T) {
	r, original := runtimeTestFixture(t, t.TempDir())
	verified, err := r.verify()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, name := range []string{"templates", "runtime-keys", "runtime-proofs"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustIdentity(dir)
	// A verified result is a snapshot: assembly must not reopen publisher files
	// or borrow mutable accessor buffers after successful Verify.
	if err := os.Remove(filepath.Join(r.bundle, "runtime.pb")); err != nil {
		t.Fatal(err)
	}
	runtimeTestPut(t, r.root, []byte("changed after verification"), 0o644)
	copy := verified.Runtime()
	copy.Image = "changed"
	for _, raw := range [][]byte{verified.RootBytes(), verified.Manifest(), verified.Config()} {
		clear(raw)
	}
	if err := writeRuntimeMaterials(dir, verified, 512); err != nil {
		t.Fatal(err)
	}
	writeRuntimePolicy(dir, r.image, 512)
	raw, err := os.ReadFile(filepath.Join(dir, "templates/real-dsh.pb"))
	if err != nil {
		t.Fatal(err)
	}
	spec := new(nodev1.InstanceSpec)
	if err := proto.Unmarshal(raw, spec); err != nil || !proto.Equal(spec.Dsh, original.Runtime()) {
		t.Fatal("assembly reused unverified publisher inputs")
	}
	for name, want := range map[string][]byte{"runtime-keys/root.pb": original.RootBytes(), runtimeProofDirectory(original) + "/manifest.json": original.Manifest(), runtimeProofDirectory(original) + "/config.json": original.Config()} {
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("snapshot changed: %s %v", name, err)
		}
	}
}

func mustRealBundle(t *testing.T, path string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func assertRuntimeInstallation(t *testing.T, out string, r runtimeInputs, m *runtimebundle.Material, memory int64) {
	t.Helper()
	read := func(name string) []byte {
		raw, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	spec := new(nodev1.InstanceSpec)
	if err := proto.Unmarshal(read("templates/real-dsh.pb"), spec); err != nil {
		t.Fatal(err)
	}
	if spec.Runtime != nodev1.RuntimeKind_RUNTIME_KIND_DOCKER || !proto.Equal(spec.Dsh, m.Runtime()) || !proto.Equal(spec.Resources, &nodev1.Resources{CpuMillis: 500, MemoryMb: memory, Pids: 128, DiskGb: 1}) {
		t.Fatal("formal runtime/budget changed")
	}
	if spec.GetProfile().GetListenPort() != 7860 || len(spec.GetProfile().GetPatches()) != 2 || spec.Profile.Patches[0].ValueJson != `"none"` || spec.Profile.Patches[1].ValueJson != `"browser-only"` {
		t.Fatal("real profile missing")
	}
	var public struct {
		Keys []struct{ Kid, Alg, X string }
	}
	if err := json.Unmarshal(read("identity-keys.json"), &public); err != nil {
		t.Fatal(err)
	}
	if len(public.Keys) != 1 || len(spec.GetSecrets().GetIdentityKeys()) != 1 || spec.Secrets.InstanceToken != nil || spec.Secrets.AiGatewayToken != nil {
		t.Fatal("template contains installation secrets or missing public identity")
	}
	identity := spec.Secrets.IdentityKeys[0]
	if identity.Kid != public.Keys[0].Kid || identity.Alg != "EdDSA" || base64.RawURLEncoding.EncodeToString(identity.PublicKey) != public.Keys[0].X {
		t.Fatal("template not bound to installation public identity")
	}
	proof := runtimeProofDirectory(m)
	for name, want := range map[string][]byte{"runtime-keys/root.pb": m.RootBytes(), proof + "/manifest.json": m.Manifest(), proof + "/config.json": m.Config()} {
		if !bytes.Equal(read(name), want) {
			t.Fatalf("original snapshot changed: %s", name)
		}
	}
	policy := string(read("agent-policy.yaml"))
	repo, _, _ := strings.Cut(r.image, "@")
	if !strings.Contains(policy, fmt.Sprintf("allowedImageRepos:\n  - %q\nallowedImageRefs:\n  - %q\n", repo, r.image)) || strings.Contains(policy, "127.0.0.1:") {
		t.Fatal("approved exact image policy missing")
	}
	set := runtimeMaterialSet(m)
	guard, err := openNASMaterialGuard(out, set)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	if err := guard.Validate(false); err != nil {
		t.Fatal(err)
	}
	for _, name := range append(append([]string{}, set.ManagedDirectories...), "templates/real-dsh.pb", "runtime-keys/root.pb", proof, proof+"/manifest.json", proof+"/config.json") {
		st, err := os.Lstat(filepath.Join(out, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		s := st.Sys().(*syscall.Stat_t)
		mode := os.FileMode(0o644)
		if st.IsDir() {
			mode = 0o755
			// Managed directories also include private submit keys and credentials.
			if name == "network" || name == "network-platform" {
				mode = 0o700
			}
		}
		if s.Uid != 10001 || s.Gid != 10001 || st.Mode().Perm() != mode || !st.IsDir() && s.Nlink != 1 {
			t.Fatalf("wrong installed metadata: %s", name)
		}
	}
}
