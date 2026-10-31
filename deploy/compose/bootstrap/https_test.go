package main

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	networkdeploy "github.com/hansjone/auriga/deploy/bootstrap/network"
	"github.com/hansjone/auriga/deploy/bootstrap/relayui"
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
			sourceCertInfo, err := os.Stat(certFile)
			if err != nil {
				t.Fatal(err)
			}
			sourceKeyInfo, err := os.Stat(keyFile)
			if err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(root, mode)
			// The host-UID copy probe traverses this test-only parent. Docker binds
			// edge-https itself, so its source ancestors are not part of that mount.
			if err := os.Mkdir(out, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(out, 0755); err != nil {
				t.Fatal(err)
			}
			args := []string{"-out", out, "-access-file", input, "-edge-container", "public-test-edge", "-image", image, "-fixture", "real", "-runtime-proofs", proofs, "-https-cert-file", certFile, "-https-key-file", keyFile}
			if mode == "domain" {
				missingSAN := append(append([]string{}, args...), "-validate-access", "-litellm-ui-origin", "https://litellm-separate.example.com:8446")
				if b, err := exec.Command(binary, missingSAN...).CombinedOutput(); err == nil || !strings.Contains(string(b), "invalid external HTTPS material") {
					t.Fatalf("UI origin without certificate SAN accepted: %v %s", err, b)
				}
			}
			for i := 0; i < 2; i++ {
				if b, err := exec.Command(binary, args...).CombinedOutput(); err != nil {
					t.Fatalf("HTTPS generation/reentry: %v %s", err, b)
				}
				if err := checkNativeClient(out); err != nil {
					t.Fatalf("HTTPS native client: %v", err)
				}
			}
			for _, source := range []struct {
				path   string
				raw    []byte
				before os.FileInfo
			}{{certFile, pem, sourceCertInfo}, {keyFile, key, sourceKeyInfo}} {
				after, err := os.Stat(source.path)
				if err != nil {
					t.Fatal(err)
				}
				raw, err := os.ReadFile(source.path)
				if err != nil || string(raw) != string(source.raw) || !os.SameFile(source.before, after) || source.before.Mode() != after.Mode() || !source.before.ModTime().Equal(after.ModTime()) {
					t.Fatal("external TLS source changed during generation/reentry")
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
			if runtime.GOOS == "linux" {
				for _, broken := range []struct {
					name string
					mode os.FileMode
				}{{"browser.crt", 0600}, {"browser.key", 0644}} {
					t.Run("reject-existing-mode/"+broken.name, func(t *testing.T) {
						path := filepath.Join(out, "browser-tls", broken.name)
						original, err := os.Stat(path)
						if err != nil {
							t.Fatal(err)
						}
						if err := os.Chmod(path, broken.mode); err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() {
							if err := os.Chmod(path, original.Mode().Perm()); err != nil {
								t.Error(err)
							}
						})
						badSnapshot := installationSnapshot(t, out)
						if b, err := exec.Command(binary, args...).CombinedOutput(); err == nil || !strings.Contains(string(b), "invalid managed HTTPS metadata") {
							t.Fatalf("bad existing mode accepted/wrong error: %v %s", err, b)
						}
						if badSnapshot != installationSnapshot(t, out) {
							t.Fatal("reentry repaired existing bad material")
						}
					})
				}
			}
			uiURL := fmt.Sprintf("https://%s:18446", a.AdminOrigin.Host)
			changedUI := append(append([]string{}, args...), "-litellm-ui-origin", uiURL)
			if b, err := exec.Command(binary, changedUI...).CombinedOutput(); err == nil || !strings.Contains(string(b), "installation inputs or identity materials changed") {
				t.Fatalf("UI origin drift accepted: %v %s", err, b)
			}
			if before != installationSnapshot(t, out) {
				t.Fatal("rejected UI origin drift changed installation")
			}
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
			accessJSON, access, err := loadComposeAccess(input, "public-test-edge")
			if err != nil {
				t.Fatal(err)
			}
			browser, err := networkdeploy.ReadBrowserTLS(certFile, keyFile, true, dns)
			if err != nil {
				t.Fatal(err)
			}
			browserDigest := browser.Digest()
			legacy := sha256.Sum256(append(accessJSON, []byte(fmt.Sprintf("\nnetwork-wss-v2-https\n%s\nreal\nfalse\ntrue\n%s", image, browserDigest))...))
			browser.Clear()
			if err := os.WriteFile(receipt, append([]byte(fmt.Sprintf("%x", legacy)), sealed[64:]...), 0600); err != nil {
				t.Fatal(err)
			}
			before = installationSnapshot(t, out)
			if b, err := exec.Command(binary, args...).CombinedOutput(); err == nil {
				t.Fatalf("old TLS receipt accepted: %s", b)
			}
			if before != installationSnapshot(t, out) {
				t.Fatal("old receipt repaired")
			}
			ui, err := relayui.Resolve(access, "")
			if err != nil {
				t.Fatal(err)
			}
			for _, oldRecipe := range []string{"network-wss-v3-https-native-client-litellm-ui-v2", "network-wss-v4-managed-device"} {
				oldInput := sha256.Sum256(append(append([]byte{}, accessJSON...), []byte(fmt.Sprintf("\n%s\n%s\nreal\nfalse\ntrue\n%s\n%s", oldRecipe, image, browserDigest, ui.Digest()))...))
				if err := os.WriteFile(receipt, append([]byte(fmt.Sprintf("%x", oldInput)), sealed[64:]...), 0600); err != nil {
					t.Fatal(err)
				}
				before = installationSnapshot(t, out)
				if b, err := exec.Command(binary, args...).CombinedOutput(); err == nil {
					t.Fatalf("old recipe %s accepted: %s", oldRecipe, b)
				}
				if before != installationSnapshot(t, out) {
					t.Fatalf("old recipe %s repaired managed materials", oldRecipe)
				}
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
