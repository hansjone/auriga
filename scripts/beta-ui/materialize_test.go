package main

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestInstallationCertificatesAndIdentity(t *testing.T) {
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		_, source, _, _ := runtime.Caller(0)
		cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-File", filepath.Join(filepath.Dir(source), "private.ps1"), "-Path", dir)
		if err := cmd.Run(); err != nil {
			t.Fatal("cannot protect test installation directory:", err)
		}
	}
	if err := generate(dir); err != nil {
		t.Fatal(err)
	}
	load := func(name string) *x509.Certificate {
		raw, err := os.ReadFile(filepath.Join(dir, name+".pem"))
		if err != nil {
			t.Fatal(err)
		}
		block, _ := pem.Decode(raw)
		if block == nil {
			t.Fatal("missing certificate")
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		return cert
	}
	ca := load("ca")
	if !ca.IsCA {
		t.Fatal("installation CA is not a CA")
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	for _, name := range []string{"platform", "edge", "ai-gateway"} {
		cert := load(name)
		for _, usage := range []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth} {
			if _, err := cert.Verify(x509.VerifyOptions{Roots: roots, DNSName: name + ".beta.internal", KeyUsages: []x509.ExtKeyUsage{usage}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := generate(dir); err == nil {
		t.Fatal("installation material must not be overwritten")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "identity.json"))
	if err != nil {
		t.Fatal(err)
	}
	var identity struct {
		Keys []map[string]string `json:"keys"`
	}
	if err = json.Unmarshal(raw, &identity); err != nil {
		t.Fatal(err)
	}
	if len(identity.Keys) != 1 || identity.Keys[0]["crv"] != "Ed25519" || identity.Keys[0]["d"] == "" {
		t.Fatal("missing installation identity key")
	}
}
