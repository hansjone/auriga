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

func nasHTTPSFixture(t *testing.T, root, mode string) []string {
	t.Helper()
	raw, err := os.ReadFile("../access-" + mode + ".example.json")
	if err != nil {
		t.Fatal(err)
	}
	var a map[string]any
	if err := json.Unmarshal(raw, &a); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"portalOrigin", "adminOrigin", "issuerOrigin"} {
		a[key].(map[string]any)["scheme"] = "https"
	}
	for _, key := range []string{"portal", "admin"} {
		a["listeners"].(map[string]any)[key].(map[string]any)["tls"] = true
	}
	a["insecureHttpAcknowledged"] = false
	if mode == "domain" {
		a["instances"].(map[string]any)["scheme"] = "https"
	}
	raw, err = json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(root, mode+".json")
	if err := os.WriteFile(input, raw, 0600); err != nil {
		t.Fatal(err)
	}
	_, cfg, err := loadNASAccess(input, "192.0.2.138", "192.0.2.128/26", 15000)
	if err != nil {
		t.Fatal(err)
	}
	dns := []string{cfg.PortalOrigin().Host, cfg.AdminOrigin().Host}
	var ips []net.IP
	if mode == "ip" {
		dns = nil
		ips = []net.IP{net.ParseIP(cfg.PortalOrigin().Host)}
	} else {
		dns = append(dns, "*."+cfg.InstanceParentDomain())
	}
	ca, cert, _ := mustCA()
	pem, key := mustIssue(ca, cert, "browser", "auriga://test/browser", dns, ips, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
	private := filepath.Join(root, mode+"-private")
	if err := os.Mkdir(private, 0700); err != nil {
		t.Fatal(err)
	}
	certFile, keyFile := filepath.Join(private, "cert.pem"), filepath.Join(private, "key.pem")
	if err := os.WriteFile(certFile, pem, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, key, 0600); err != nil {
		t.Fatal(err)
	}
	return []string{"-access-file", input, "-edge-peer-ip", "192.0.2.138", "-internal-subnet", "192.0.2.128/26", "-lan-ip", "192.0.2.10", "-lan-subnet", "192.0.2.0/26", "-https-cert-file", certFile, "-https-key-file", keyFile}
}

func TestNASDirectHTTPSValidationCLI(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "bootstrap")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	if b, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, b)
	}
	for _, mode := range []string{"domain", "ip"} {
		t.Run(mode, func(t *testing.T) {
			args := append(nasHTTPSFixture(t, root, mode), "-validate-access")
			if b, err := exec.Command(binary, args...).CombinedOutput(); err != nil {
				t.Fatalf("HTTPS validation: %v %s", err, b)
			}
			if mode == "domain" {
				missingSAN := append(append([]string{}, args...), "-litellm-ui-origin", "https://litellm-separate.example.com:8446")
				if b, err := exec.Command(binary, missingSAN...).CombinedOutput(); err == nil || !strings.Contains(string(b), "invalid external HTTPS material") {
					t.Fatalf("UI origin without certificate SAN accepted: %v %s", err, b)
				}
			}
			args[11] = ""
			if b, err := exec.Command(binary, args...).CombinedOutput(); err == nil || !strings.Contains(string(b), "invalid external HTTPS material") {
				t.Fatalf("missing certificate accepted: %v %s", err, b)
			}
		})
	}
}
