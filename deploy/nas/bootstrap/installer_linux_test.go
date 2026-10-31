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
)

func TestNASFixedInstallerAndReceipt(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires Linux root for the real fixed uid boundary")
	}
	t.Run("executable-boundary", testNASFixedInstallerExecutableBoundary)
	t.Run("runtime-upgrade", testNASRuntimeUpgradeLifecycle)
	root, err := os.MkdirTemp("", "auriga-nas-installer-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "bootstrap")
	if b, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, b)
	}
	raw, err := os.ReadFile("../../../contracts/deployment/fixtures/ip-http.json")
	if err != nil {
		t.Fatal(err)
	}
	var a map[string]any
	if err := json.Unmarshal(raw, &a); err != nil {
		t.Fatal(err)
	}
	a["instanceControl"].(map[string]any)["edgeContainer"] = "auriga-nas-test-edge"
	raw, _ = json.Marshal(a)
	input := filepath.Join(root, "input.json")
	if err := os.WriteFile(input, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Run("memory-budget-and-receipt", func(t *testing.T) {
		testNASMemoryBudgetAndReceipt(t, root, binary, input, raw)
	})
	t.Run("signed-runtime", func(t *testing.T) {
		testNASSignedRuntime(t, root, binary, input)
	})
	invokeWithLAN := func(out, lanIP string) ([]byte, error) {
		return exec.Command(binary, "-out", out, "-access-file", input, "-edge-peer-ip", "192.0.2.138", "-internal-subnet", "192.0.2.128/26", "-lan-ip", lanIP, "-lan-subnet", "192.0.2.0/26").CombinedOutput()
	}
	invoke := func(out string) ([]byte, error) { return invokeWithLAN(out, "192.0.2.10") }
	for _, kind := range []string{"valid", "wrong-owner", "source-link", "template-only", "pki-link", "private-hardlink", "private-mode", "missing-receipt", "source-ca-mode", "source-signer-owner", "source-signer-readable-owner", "source-signer-hardlink", "source-pki-mode", "lan-input-drift"} {
		t.Run(kind, func(t *testing.T) {
			out := filepath.Join(root, kind)
			if kind == "wrong-owner" {
				if err := os.Mkdir(out, 0o755); err != nil {
					t.Fatal(err)
				}
				if b, err := invoke(out); err == nil {
					t.Fatalf("wrong owner accepted %s", b)
				}
				st, _ := os.Stat(out)
				if st.Sys().(*syscall.Stat_t).Uid != 0 {
					t.Fatal("existing owner repaired")
				}
				return
			}
			if kind == "source-link" {
				outside := filepath.Join(root, "outside")
				if err := os.Mkdir(outside, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, out); err != nil {
					t.Fatal(err)
				}
				if b, err := invoke(out); err == nil {
					t.Fatalf("source link accepted %s", b)
				}
				entries, _ := os.ReadDir(outside)
				if len(entries) != 0 {
					t.Fatal("wrote outside source")
				}
				return
			}
			if b, err := invoke(out); err != nil {
				t.Fatalf("first init: %v %s", err, b)
			}
			for _, name := range []string{"", "iam-installation", "iam-installation/edge.secret", "iam-installation/internal.properties", "iam-installation/bootstrap.receipt"} {
				st, err := os.Lstat(filepath.Join(out, name))
				if err != nil {
					t.Fatal(err)
				}
				s := st.Sys().(*syscall.Stat_t)
				if s.Uid != 10001 || s.Gid != 10001 {
					t.Fatalf("wrong installed owner %s", name)
				}
				if strings.HasPrefix(name, "iam-installation/") && (st.Mode().Perm() != 0o600 || s.Nlink != 1) {
					t.Fatalf("wrong private file metadata %s", name)
				}
			}
			before := nasSnapshot(t, out)
			if kind == "lan-input-drift" {
				if b, err := invokeWithLAN(out, "192.0.2.11"); err == nil {
					t.Fatalf("changed LAN isolation input accepted: %s", b)
				}
				if before != nasSnapshot(t, out) {
					t.Fatal("LAN input rejection changed source materials or receipt")
				}
				return
			}
			if kind == "valid" {
				broker, err := os.ReadFile(filepath.Join(out, "network/broker.conf"))
				if err != nil || !strings.Contains(string(broker), "websocket {host: \"0.0.0.0\", port: 18444, tls {") {
					t.Fatalf("NAS internal WSS listener not generated: %v", err)
				}
				edge, err := os.ReadFile(filepath.Join(out, "edge.config.json"))
				if err != nil || !strings.Contains(string(edge), `"upstreamUrl": "wss://network-broker:18444"`) || !strings.Contains(string(edge), `"serverName": "broker.network.internal"`) {
					t.Fatalf("real NAS bootstrap lost broker WSS TLS hop: %v", err)
				}
				t.Run("held-directory-path-replacement", func(t *testing.T) {
					guard, err := openNASMaterialGuard(out)
					if err != nil {
						t.Fatal(err)
					}
					defer guard.Close()
					pki := filepath.Join(out, "pki")
					saved := filepath.Join(out, "pki-saved")
					if err := os.Rename(pki, saved); err != nil {
						t.Fatal(err)
					}
					defer func() {
						os.Remove(pki)
						if err := os.Rename(saved, pki); err != nil {
							t.Error(err)
						}
					}()
					if err := os.Mkdir(pki, 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.Chown(pki, 10001, 10001); err != nil {
						t.Fatal(err)
					}
					if err := guard.Validate(false); err == nil {
						t.Fatal("held source directory was replaced through its path")
					}
				})
				if b, err := invoke(out); err != nil {
					t.Fatalf("reentry: %v %s", err, b)
				}
				if before != nasSnapshot(t, out) {
					t.Fatal("reentry changed source")
				}
				return
			}
			switch kind {
			case "template-only":
				os.Remove(filepath.Join(out, "iam-installation/bootstrap.receipt"))
				if err := os.WriteFile(filepath.Join(out, "templates/stale.pb"), []byte("stale"), 0o644); err != nil {
					t.Fatal(err)
				}
			case "pki-link":
				os.Remove(filepath.Join(out, "iam-installation/bootstrap.receipt"))
				os.Rename(filepath.Join(out, "pki"), filepath.Join(out, "saved-pki"))
				if err := os.Symlink(filepath.Join(out, "saved-pki"), filepath.Join(out, "pki")); err != nil {
					t.Fatal(err)
				}
			case "private-hardlink":
				if err := os.Link(filepath.Join(out, "iam-installation/edge.secret"), filepath.Join(out, "duplicate")); err != nil {
					t.Fatal(err)
				}
			case "private-mode":
				if err := os.Chmod(filepath.Join(out, "iam-installation/edge.secret"), 0o644); err != nil {
					t.Fatal(err)
				}
			case "missing-receipt":
				os.Remove(filepath.Join(out, "iam-installation/bootstrap.receipt"))
			case "source-ca-mode":
				if err := os.Chmod(filepath.Join(out, "pki/ca.key"), 0o644); err != nil {
					t.Fatal(err)
				}
			case "source-signer-owner":
				if err := os.Chown(filepath.Join(out, "identity-signing-keys.json"), 0, 0); err != nil {
					t.Fatal(err)
				}
			case "source-signer-readable-owner":
				if err := os.Chown(filepath.Join(out, "identity-signing-keys.json"), 0, 10001); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(filepath.Join(out, "identity-signing-keys.json"), 0o640); err != nil {
					t.Fatal(err)
				}
			case "source-signer-hardlink":
				if err := os.Link(filepath.Join(out, "identity-signing-keys.json"), filepath.Join(out, "signer-duplicate")); err != nil {
					t.Fatal(err)
				}
			case "source-pki-mode":
				if err := os.Chmod(filepath.Join(out, "pki"), 0o777); err != nil {
					t.Fatal(err)
				}
			}
			before = nasSnapshot(t, out)
			if b, err := invoke(out); err == nil {
				t.Fatalf("damaged source accepted %s", b)
			}
			if before != nasSnapshot(t, out) {
				t.Fatal("rejection modified source materials")
			}
		})
	}
}

func testNASMemoryBudgetAndReceipt(t *testing.T, root, binary, input string, raw []byte) {
	t.Helper()
	invoke := func(out string, memory ...string) ([]byte, error) {
		args := []string{"-out", out, "-access-file", input, "-edge-peer-ip", "192.0.2.138", "-internal-subnet", "192.0.2.128/26", "-lan-ip", "192.0.2.10", "-lan-subnet", "192.0.2.0/26"}
		if len(memory) != 0 {
			args = append(args, "-instance-max-memory-mb", memory[0])
		}
		return exec.Command(binary, args...).CombinedOutput()
	}
	for _, tc := range []struct{ name, memory, want, changed string }{
		{"default", "", "512", "1024"}, {"explicit-512", "512", "512", "1024"}, {"explicit-1024", "1024", "1024", "512"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := filepath.Join(root, "budget-"+tc.name)
			memory := []string{}
			if tc.memory != "" {
				memory = append(memory, tc.memory)
			}
			if b, err := invoke(out, memory...); err != nil {
				t.Fatalf("first install: %v %s", err, b)
			}
			policyPath := filepath.Join(out, "agent-policy.yaml")
			p, err := os.ReadFile(policyPath)
			if err != nil || !strings.Contains(string(p), "\nmaxMemoryMB: "+tc.want+"\n") {
				t.Fatalf("fixed uid lost memory input: %v %s", err, p)
			}
			st, err := os.Stat(policyPath)
			if err != nil || st.Sys().(*syscall.Stat_t).Uid != 10001 || st.Sys().(*syscall.Stat_t).Gid != 10001 {
				t.Fatalf("policy not generated by fixed uid10001: %v", err)
			}
			before := nasSnapshot(t, out)
			for _, same := range [][]string{memory, {tc.want}} {
				if b, err := invoke(out, same...); err != nil {
					t.Fatalf("same budget reentry: %v %s", err, b)
				}
				if before != nasSnapshot(t, out) {
					t.Fatal("same budget changed material bytes or metadata")
				}
			}
			for _, changed := range []string{tc.changed, ""} {
				if b, err := invoke(out, changed); err == nil || !strings.Contains(string(b), "instance-max-memory-mb") && !strings.Contains(string(b), "input") {
					t.Fatalf("changed/empty memory accepted or wrong rejection: %v %s", err, b)
				}
				if before != nasSnapshot(t, out) {
					t.Fatal("rejected budget modified material bytes or metadata")
				}
			}
			if tc.name != "default" {
				return
			}
			// Recreate the previous producer's exact input digest, retaining sealed
			// materials, receipt format and ownership. No implicit default migration.
			receiptPath := filepath.Join(out, "iam-installation", "bootstrap.receipt")
			receipt, err := os.ReadFile(receiptPath)
			if err != nil {
				t.Fatal(err)
			}
			oldInput := sha256.Sum256(append(append([]byte{}, raw...), []byte("\n192.0.2.138\n192.0.2.128/26\n15000\n192.0.2.10\n192.0.2.0/26\n512\nnetwork-wss-v1")...))
			oldReceipt := fmt.Sprintf("%x%s", oldInput, receipt[strings.IndexByte(string(receipt), '\n'):])
			if err := os.WriteFile(receiptPath, []byte(oldReceipt), 0o600); err != nil {
				t.Fatal(err)
			}
			before = nasSnapshot(t, out)
			for _, same := range [][]string{nil, {"512"}} {
				if b, err := invoke(out, same...); err == nil || !strings.Contains(string(b), "input") {
					t.Fatalf("old receipt accepted or wrong rejection: %v %s", err, b)
				}
				if before != nasSnapshot(t, out) {
					t.Fatal("legacy receipt rejection repaired source")
				}
			}
		})
	}
}

func nasSnapshot(t *testing.T, out string) string {
	t.Helper()
	h := sha256.New()
	err := filepath.WalkDir(out, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		st, err := os.Lstat(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(out, path)
		s := st.Sys().(*syscall.Stat_t)
		fmt.Fprintf(h, "%s:%d:%d:%d:%d\n", rel, s.Uid, s.Gid, st.Mode(), s.Nlink)
		if st.Mode().IsRegular() {
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			h.Write(raw)
			clear(raw)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func testNASFixedInstallerExecutableBoundary(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires Linux root for the real executable and fixed uid boundary")
	}
	root, err := os.MkdirTemp("", "auriga-nas-executable-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(root, "private-bundle")
	if err := os.Mkdir(private, 0o700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(private, "bootstrap")
	if b, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, b)
	}
	beforeBinary := nasSnapshot(t, private)
	input := filepath.Join(root, "input.json")
	raw, err := os.ReadFile("../access-ip.example.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	invoke := func(out string) ([]byte, error) {
		return exec.Command(binary, "-out", out, "-access-file", input, "-edge-peer-ip", "192.0.2.138", "-internal-subnet", "192.0.2.128/26", "-lan-ip", "192.0.2.10", "-lan-subnet", "192.0.2.0/26").CombinedOutput()
	}
	t.Run("private-bundle-public-data", func(t *testing.T) {
		out := filepath.Join(root, "public-data")
		if b, err := invoke(out); err != nil {
			t.Fatalf("private bundle with legal data root: %v %s", err, b)
		}
		before := nasSnapshot(t, out)
		if b, err := invoke(out); err != nil {
			t.Fatalf("private bundle reentry: %v %s", err, b)
		}
		if before != nasSnapshot(t, out) || beforeBinary != nasSnapshot(t, private) {
			t.Fatal("private bundle reentry changed source or executable metadata/bytes")
		}
	})
	for _, mode := range []os.FileMode{0o700, 0o775} {
		t.Run(fmt.Sprintf("unsafe-data-ancestor-%o", mode), func(t *testing.T) {
			parent := filepath.Join(root, fmt.Sprintf("data-parent-%o", mode))
			if err := os.Mkdir(parent, 0o755); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(parent, "materials")
			if err := os.Mkdir(out, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Chown(out, 10001, 10001); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(parent, mode); err != nil {
				t.Fatal(err)
			}
			before := nasSnapshot(t, parent)
			if b, err := invoke(out); err == nil {
				t.Fatalf("unsafe data ancestor accepted: %s", b)
			}
			if before != nasSnapshot(t, parent) {
				t.Fatal("unsafe data ancestor rejection repaired or wrote materials")
			}
		})
	}
	t.Run("non-executable-for-installer", func(t *testing.T) {
		if err := os.Chmod(binary, 0o700); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(binary, 0o755)
		if b, err := invoke(filepath.Join(root, "bad-binary-mode")); err == nil {
			t.Fatalf("non-executable binary for fixed uid accepted: %s", b)
		}
	})
}
