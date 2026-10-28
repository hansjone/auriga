package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNASDirectHTTPSPrivateInstaller(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires Linux root and fixed installer/edge UIDs")
	}
	root, err := os.MkdirTemp("", "auriga-nas-https-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(root) }()
	if err := os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "bootstrap")
	if b, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, b)
	}
	for _, mode := range []string{"domain", "ip"} {
		t.Run(mode, func(t *testing.T) {
			args := nasHTTPSFixture(t, root, mode)
			out := filepath.Join(root, mode)
			args = append(args, "-out", out)
			private := filepath.Join(root, mode+"-private")
			sourceBefore := nasSnapshot(t, private)
			for i := 0; i < 2; i++ {
				if b, err := exec.Command(binary, args...).CombinedOutput(); err != nil {
					t.Fatalf("HTTPS install/reentry: %v %s", err, b)
				}
			}
			if sourceBefore != nasSnapshot(t, private) {
				t.Fatal("external private source changed")
			}
			if _, err := os.Lstat(filepath.Join(out, "pki/browser.key")); !os.IsNotExist(err) {
				t.Fatal("external private key entered the platform PKI mount")
			}
			edge, err := os.ReadFile(filepath.Join(out, "edge.config.json"))
			if err != nil || !strings.Contains(string(edge), `/etc/auriga/pki/browser.key`) {
				t.Fatal("direct TLS files not wired")
			}
			runtimeRoot := filepath.Join(root, mode+"-runtime")
			if err := os.MkdirAll(filepath.Join(runtimeRoot, "config/pki-edge"), 0755); err != nil {
				t.Fatal(err)
			}
			lib, err := filepath.Abs("../scripts/lib.sh")
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("sh", "-c", `. "$1"; install_edge_material "$2/browser-tls/browser.crt" "$AURIGA_DATA_ROOT/config/pki-edge/browser.crt" 644; install_edge_material "$2/browser-tls/browser.key" "$AURIGA_DATA_ROOT/config/pki-edge/browser.key" 600`, "sh", lib, out)
			cmd.Env = append(os.Environ(), "NAS_ROOT="+filepath.Dir(filepath.Dir(lib)), "AURIGA_DATA_ROOT="+runtimeRoot)
			if b, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("runtime copy: %v %s", err, b)
			}
			cmd = exec.Command("setpriv", "--reuid=65532", "--regid=65532", "--clear-groups", "sh", "-c", `test -r "$1/config/pki-edge/browser.key" && ! test -r "$2/browser-tls/browser.key"`, "sh", runtimeRoot, out)
			if b, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("edge minimum key access: %v %s", err, b)
			}
			before := nasSnapshot(t, out)
			key := filepath.Join(private, "key.pem")
			if err := os.WriteFile(key, []byte("invalid key"), 0600); err != nil {
				t.Fatal(err)
			}
			if b, err := exec.Command(binary, args...).CombinedOutput(); err == nil || !strings.Contains(string(b), "invalid external HTTPS material") {
				t.Fatalf("changed TLS input accepted: %v %s", err, b)
			}
			if before != nasSnapshot(t, out) {
				t.Fatal("failed source validation repaired managed installation")
			}
		})
	}
}
