package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/hansjone/auriga/deploy/bootstrap/collab/testfixture"
	"github.com/hansjone/auriga/deploy/bootstrap/materials"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"github.com/hansjone/auriga/infra/gen/deploymentconfig"
	"google.golang.org/protobuf/proto"
)

// Build an exact historical receipt in a disposable installation as its real
// fixed installer uid. This helper is test-only; production has no reseal flag.
func TestMaterialsUpgradeLegacyFixture(t *testing.T) {
	base := os.Getenv("AURIGA_TEST_LEGACY_BASE")
	if base == "" {
		return
	}
	if os.Geteuid() != 10001 {
		t.Fatal("fixed fixture installer required")
	}
	for _, p := range []string{"bifrost", "bifrost-runtime.json", "iam-installation/bootstrap.receipt"} {
		if err := os.RemoveAll(filepath.Join(base, p)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(base, "litellm"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"master", "salt", "team", "pg_password"} {
		if err := os.WriteFile(filepath.Join(base, "litellm", n), []byte(strings.Repeat("c", 64)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for n, b := range map[string][]byte{"litellm-runtime.json": []byte(`{"origin":"http://litellm:4000"}`), "litellm-public.env": []byte("PROXY_BASE_URL=https://relay.example.com/ai\n")} {
		if err := os.WriteFile(filepath.Join(base, n), b, 0644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(base, "access.json"))
	if err != nil {
		t.Fatal(err)
	}
	var access map[string]any
	if err = json.Unmarshal(raw, &access); err != nil {
		t.Fatal(err)
	}
	portal := access["portalOrigin"].(map[string]any)
	direct := access["ingress"].(map[string]any)["kind"] == "direct" && portal["scheme"] == "https"
	set := legacyNASMaterialSet(direct)
	if b, err := os.ReadFile(filepath.Join(base, "templates/real-dsh.pb")); err == nil {
		var spec nodev1.InstanceSpec
		if err := proto.Unmarshal(b, &spec); err != nil {
			t.Fatal(err)
		}
		proof := "runtime-proofs/" + strings.TrimPrefix(spec.GetDsh().GetReleaseEnvelope().GetImageDigest(), "sha256:")
		set.PublicFiles = append(set.PublicFiles, "templates/real-dsh.pb", "runtime-keys/root.pb", proof+"/manifest.json", proof+"/config.json")
		set.PublicFileLimits = map[string]int64{proof + "/manifest.json": 4 << 20, proof + "/config.json": 4 << 20}
	}
	if err := materials.SealReceipt(base, strings.Repeat("a", 64), set); err != nil {
		t.Fatal(err)
	}
}

func testNASMaterialsUpgradeLifecycle(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires Linux root and fixed installer uid")
	}
	parent, err := os.MkdirTemp("", "auriga-materials-upgrade-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(parent) }()
	if err := os.Chmod(parent, 0755); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(parent, "bootstrap")
	if b, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, b)
	}
	for _, mode := range []string{"domain", "ip", "proxy"} {
		t.Run(mode, func(t *testing.T) {
			root := filepath.Join(parent, mode)
			if err := os.Mkdir(root, 0755); err != nil {
				t.Fatal(err)
			}
			base := filepath.Join(root, "materials")
			access := filepath.Join("..", "access-"+mode+".example.json")
			args := []string{"-out", base, "-access-file", access, "-edge-peer-ip", "192.0.2.138", "-internal-subnet", "192.0.2.128/26", "-lan-ip", "192.0.2.10", "-lan-subnet", "192.0.2.0/26"}
			if mode != "proxy" {
				args = append([]string{"-out", base}, nasHTTPSFixture(t, root, mode)...)
				access = args[3]
			}
			r, _ := runtimeTestFixture(t, root)
			args = append(args, "-runtime-bundle", r.bundle, "-runtime-root", r.root, "-runtime-image", r.image)
			if b, err := exec.Command(binary, args...).CombinedOutput(); err != nil {
				t.Fatalf("initial install: %v %s", err, b)
			}
			helper, err := os.Open("/proc/self/exe")
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("/proc/self/fd/3", "-test.run=^TestMaterialsUpgradeLegacyFixture$")
			cmd.ExtraFiles = []*os.File{helper}
			cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 10001, Gid: 10001, Groups: []uint32{10001}}}
			cmd.Env = append(os.Environ(), "AURIGA_TEST_LEGACY_BASE="+base)
			b, e := cmd.CombinedOutput()
			_ = helper.Close()
			if e != nil {
				t.Fatalf("legacy fixture: %v %s", e, b)
			}
			before := nasSnapshot(t, base)
			raw, err := os.ReadFile(filepath.Join(base, "iam-installation/bootstrap.receipt"))
			if err != nil {
				t.Fatal(err)
			}
			hash := fmt.Sprintf("%x", sha256.Sum256(raw))
			out := filepath.Join(root, "materials-upgrades")
			run := func(action string, ok bool, extra ...string) string {
				t.Helper()
				a := []string{"materials-upgrade", "-action", action, "-base", base, "-out", out, "-access-file", access}
				if action == "stage" || action == "commit" || action == "rollback" {
					a = append(a, "-expected-base-sha256", hash)
				}
				b, err := exec.Command(binary, append(a, extra...)...).CombinedOutput()
				if (err == nil) != ok {
					t.Fatalf("%s: %v %s", action, err, b)
				}
				if nasSnapshot(t, base) != before {
					t.Fatal("original identity/TLS/receipt changed")
				}
				return string(b)
			}
			run("stage", false, "-version", "0")
			run("stage", false, "-expected-base-sha256", strings.Repeat("0", 64))
			if _, err := os.Lstat(out); !os.IsNotExist(err) {
				t.Fatal("failed approval left partial materials")
			}
			collab := filepath.Join(root, "collab-source")
			// Upgrade first, then activate collab through the real public CLI.
			// Neither staged nor committed Bifrost receipts may pin its absence.
			run("stage", true)
			run("commit", true)
			receiptBefore, err := os.ReadFile(filepath.Join(out, bifrostGeneration, "upgrade.receipt"))
			if err != nil {
				t.Fatal(err)
			}
			accessRaw, err := os.ReadFile(filepath.Join(base, "access.json"))
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := deploymentconfig.Load(accessRaw)
			if err != nil {
				t.Fatal(err)
			}
			input := testfixture.Write(t, cfg.TenantID())
			activate := exec.Command(binary, "collab", "-base", base, "-out", collab, "-input", input, "-public-origin", "https://network.example.com", "-stopped-confirmed")
			activate.Env = append(os.Environ(), "AURIGA_LINKS_ALLOWED_ORIGINS=https://network.example.com")
			if b, err := activate.CombinedOutput(); err != nil {
				t.Fatalf("late collab activation: %v %s", err, b)
			}
			run("check", true)
			// A sealed optional message source may outlive its disposable edge
			// projection. Core Bifrost projection must not read that missing leaf.
			if err := os.Mkdir(filepath.Join(root, "bifrost"), 0700); err != nil {
				t.Fatal(err)
			}
			run("project-core", true)
			run("project", false) // strict message projection still refuses absence
			if b, err := exec.Command("sh", "../scripts/collab-materials.sh", collab, filepath.Join(root, "collab-runtime")).CombinedOutput(); err != nil {
				t.Fatalf("rebuild message projections: %v %s", err, b)
			}
			run("project", true)
			// Optional receipt damage must be rejected by its own authority check,
			// while core check/path/project keep validating only base/generation.
			optionalReceipt := filepath.Join(collab, "activation.receipt")
			originalOptional, err := os.ReadFile(optionalReceipt)
			if err != nil {
				t.Fatal(err)
			}
			for _, damage := range []string{"metadata", "content"} {
				if damage == "metadata" {
					if err := os.Chmod(optionalReceipt, 0644); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(optionalReceipt, []byte("damaged optional receipt"), 0600); err != nil {
					t.Fatal(err)
				}
				run("check", true)
				run("path", true)
				run("project-core", true)
				check := exec.Command(binary, "collab", "-base", base, "-out", collab, "-public-origin", "https://network.example.com", "-check")
				check.Env = activate.Env
				if b, err := check.CombinedOutput(); err == nil {
					t.Fatalf("damaged message %s accepted: %s", damage, b)
				}
				if err := os.WriteFile(optionalReceipt, originalOptional, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(optionalReceipt, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.RemoveAll(filepath.Join(root, "bifrost")); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(filepath.Join(root, "collab-runtime")); err != nil {
				t.Fatal(err)
			}
			// Re-serialization also changes the receipt hash without changing its
			// independently sealed collab source or original Bifrost generation.
			collabReceipt := filepath.Join(collab, "activation.receipt")
			collabRaw, err := os.ReadFile(collabReceipt)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(collabReceipt, append(collabRaw, '\n'), 0600); err != nil {
				t.Fatal(err)
			}
			checkCollab := exec.Command(binary, "collab", "-base", base, "-out", collab, "-public-origin", "https://network.example.com", "-check")
			checkCollab.Env = activate.Env
			if b, err := checkCollab.CombinedOutput(); err != nil {
				t.Fatalf("rewritten collab receipt: %v %s", err, b)
			}
			run("check", true)
			receiptAfter, err := os.ReadFile(filepath.Join(out, bifrostGeneration, "upgrade.receipt"))
			if err != nil || string(receiptBefore) != string(receiptAfter) {
				t.Fatal("collab activation rewrote Bifrost receipt")
			}
			// Keep the lifecycle/rollback tests below independent of committed state.
			// This is disposable fixture cleanup, never an installation upgrade path.
			if err := os.RemoveAll(out); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(collab); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(collab, 0700); err != nil {
				t.Fatal(err)
			}
			activation := filepath.Join(collab, "activation.receipt")
			if err := os.WriteFile(activation, []byte("retained original collab receipt"), 0644); err != nil {
				t.Fatal(err)
			}
			run("stage", false) // failure after secret generation must remove its pending tree
			entries, err := os.ReadDir(out)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 || entries[0].Name() != ".lock" {
				t.Fatal("failed stage left half material")
			}
			if err := os.Chmod(activation, 0600); err != nil {
				t.Fatal(err)
			}
			projectDir := filepath.Join(root, "collab-runtime", "edge")
			if err := os.MkdirAll(projectDir, 0755); err != nil {
				t.Fatal(err)
			}
			originalEdge, err := os.ReadFile(filepath.Join(base, "edge.config.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(projectDir, "edge.config.json"), originalEdge, 0640); err != nil {
				t.Fatal(err)
			}
			run("stage", true)
			active := run("path", true)
			if active != filepath.Join(out, bifrostGeneration) {
				t.Fatal(active)
			}
			staged := nasSnapshot(t, out)
			run("stage", true)
			if nasSnapshot(t, out) != staged {
				t.Fatal("stage regenerated secrets")
			}
			if err := os.Mkdir(filepath.Join(root, "bifrost"), 0700); err != nil {
				t.Fatal(err)
			}
			run("project", true)
			edge, err := os.ReadFile(filepath.Join(root, "bifrost", "edge.config.json"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(edge), `"relay"`) || strings.Contains(string(edge), "litellm") {
				t.Fatal("wrong edge projection")
			}
			run("rollback", true)
			run("path", false)
			run("stage", true)
			if run("path", true) != active {
				t.Fatal("resume generated different material")
			}
			run("commit", true)
			run("commit", true)
			run("rollback", false)
			for _, name := range []string{"bifrost/member_password", "bifrost-runtime.json"} {
				leaf := filepath.Join(active, name)
				raw, err := os.ReadFile(leaf)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(leaf, append(raw, 'x'), 0600); err != nil {
					t.Fatal(err)
				}
				run("check", false)
				run("project-core", false)
				if err := os.WriteFile(leaf, raw, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(leaf, 0644); err != nil {
					t.Fatal(err)
				}
				run("check", false)
				run("project-core", false)
				if err := os.Chmod(leaf, 0600); err != nil {
					t.Fatal(err)
				}
			}
			run("check", true)
			if b, err := os.ReadFile(activation); err != nil || string(b) != "retained original collab receipt" {
				t.Fatal("collab seal changed")
			}
			last := filepath.Join(out, "e-00000000000000000004.json")
			b, err = os.ReadFile(last)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(last, []byte(`{}`), 0600); err != nil {
				t.Fatal(err)
			}
			run("check", false)
			if err := os.WriteFile(last, b, 0600); err != nil {
				t.Fatal(err)
			}
			run("check", true)
		})
	}
}
