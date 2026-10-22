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
	invoke := func(out string) ([]byte, error) {
		return exec.Command(binary, "-out", out, "-access-file", input, "-edge-peer-ip", "192.0.2.138", "-internal-subnet", "192.0.2.128/26").CombinedOutput()
	}
	for _, kind := range []string{"valid", "wrong-owner", "source-link", "template-only", "pki-link", "private-hardlink", "private-mode", "missing-receipt", "source-ca-mode", "source-signer-owner", "source-signer-readable-owner", "source-signer-hardlink", "source-pki-mode"} {
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
			if kind == "valid" {
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
