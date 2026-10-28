package main

import (
	"crypto/x509"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestComposeDirectHTTPSCLI(t *testing.T) {
	root, err := os.MkdirTemp("", "auriga-compose-https-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(root) }()
	if err := os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "bootstrap")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	if b, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, b)
	}
	proofs, image := runtimeProofFixture(t, "real")
	for _, mode := range []string{"domain", "ip"} {
		t.Run(mode, func(t *testing.T) {
			a := customAccess(mode)
			a.PortalOrigin.Scheme = "https"
			a.AdminOrigin.Scheme = "https"
			a.IssuerOrigin.Scheme = "https"
			a.Listeners.Portal.Tls = true
			a.Listeners.Admin.Tls = true
			a.InsecureHttpAcknowledged = false
			dns := []string{a.PortalOrigin.Host, a.AdminOrigin.Host}
			var ips []net.IP
			if mode == "domain" {
				a.Instances.AccessInstancesSubdomain.Scheme = "https"
				dns = append(dns, "*."+a.Instances.AccessInstancesSubdomain.ParentDomain)
			} else {
				dns = nil
				ips = []net.IP{net.ParseIP(a.PortalOrigin.Host)}
			}
			raw, err := json.Marshal(a)
			if err != nil {
				t.Fatal(err)
			}
			input := filepath.Join(root, mode+".json")
			if err := os.WriteFile(input, raw, 0600); err != nil {
				t.Fatal(err)
			}
			ca, cert, _ := mustCA()
			pem, key := mustIssue(ca, cert, "browser", "auriga://test/browser", dns, ips, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
			certFile, keyFile := filepath.Join(root, mode+".crt"), filepath.Join(root, mode+".key")
			if err := os.WriteFile(certFile, pem, 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(keyFile, key, 0600); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(root, mode)
			args := []string{"-out", out, "-access-file", input, "-edge-container", "public-test-edge", "-image", image, "-fixture", "real", "-runtime-proofs", proofs, "-https-cert-file", certFile, "-https-key-file", keyFile}
			for i := 0; i < 2; i++ {
				if b, err := exec.Command(binary, args...).CombinedOutput(); err != nil {
					t.Fatalf("HTTPS generation/reentry: %v %s", err, b)
				}
			}
			edge, err := os.ReadFile(filepath.Join(out, "edge.config.json"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(edge), `"mode": "files"`) || !strings.Contains(string(edge), `/etc/auriga/https/browser.key`) {
				t.Fatal("HTTPS files not wired")
			}
			before := installationSnapshot(t, out)
			if source, err := os.ReadFile(keyFile); err != nil || string(source) != string(key) {
				t.Fatal("external key changed during bootstrap/reentry")
			}
			t.Run("edge-runtime-copy", func(t *testing.T) {
				if runtime.GOOS != "linux" || os.Geteuid() != 0 {
					t.Skip("requires Linux root for actual edge UID")
				}
				for i := 0; i < 2; i++ {
					if b, err := exec.Command("bash", "../scripts/https-materials.sh", out).CombinedOutput(); err != nil {
						t.Fatalf("runtime HTTPS copy: %v %s", err, b)
					}
				}
				if b, err := exec.Command("setpriv", "--reuid=65532", "--regid=65532", "--clear-groups", "sh", "-c", `test -r "$1/edge-https/browser.key" && ! test -r "$1/browser-tls/browser.key"`, "sh", out).CombinedOutput(); err != nil {
					t.Fatalf("edge UID access: %v %s", err, b)
				}
				if before != installationSnapshotWithoutRuntime(t, out) {
					t.Fatal("runtime copy changed managed source")
				}
			})
			before = installationSnapshot(t, out)
			if err := os.WriteFile(keyFile, []byte("invalid private source"), 0600); err != nil {
				t.Fatal(err)
			}
			if b, err := exec.Command(binary, args...).CombinedOutput(); err == nil || !strings.Contains(string(b), "invalid external HTTPS material") {
				t.Fatalf("changed key accepted/wrong error: %v %s", err, b)
			}
			if before != installationSnapshot(t, out) {
				t.Fatal("rejected TLS source changed managed material")
			}
			managed, err := os.ReadFile(filepath.Join(out, "browser-tls/browser.key"))
			if err != nil || string(managed) != string(key) {
				t.Fatal("managed private snapshot differs")
			}
			if err := os.WriteFile(keyFile, key, 0600); err != nil {
				t.Fatal(err)
			}
			rotatedCert, rotatedKey := mustIssue(ca, cert, "browser", "auriga://test/browser", dns, ips, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
			if err := os.WriteFile(certFile, rotatedCert, 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(keyFile, rotatedKey, 0600); err != nil {
				t.Fatal(err)
			}
			if b, err := exec.Command(binary, args...).CombinedOutput(); err == nil || !strings.Contains(string(b), "installation inputs or identity materials changed") {
				t.Fatalf("valid TLS rotation accepted: %v %s", err, b)
			}
			if before != installationSnapshot(t, out) {
				t.Fatal("rejected valid rotation changed managed source")
			}
			if err := os.WriteFile(certFile, pem, 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(keyFile, key, 0600); err != nil {
				t.Fatal(err)
			}
			receipt := filepath.Join(out, "iam-installation/bootstrap.receipt")
			sealed, err := os.ReadFile(receipt)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(receipt, append([]byte(strings.Repeat("0", 64)), sealed[64:]...), 0600); err != nil {
				t.Fatal(err)
			}
			before = installationSnapshot(t, out)
			if b, err := exec.Command(binary, args...).CombinedOutput(); err == nil {
				t.Fatalf("old TLS receipt accepted: %s", b)
			}
			if before != installationSnapshot(t, out) {
				t.Fatal("old receipt repaired")
			}
		})
	}
}

func installationSnapshotWithoutRuntime(t *testing.T, out string) string {
	t.Helper()
	// The existing snapshot records files only. Filter the separate runtime copies.
	var lines []string
	for _, line := range strings.Split(installationSnapshot(t, out), "\n") {
		if !strings.HasPrefix(line, "edge-https/") && !strings.HasPrefix(line, `edge-https\`) {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}
