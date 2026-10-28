package network

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testOptions() Options {
	rpc := map[string]string{"ServiceTokenFile": "/private/service.jwt"}
	return Options{Root: "/etc/auriga/network", SubmitListen: "127.0.0.1:18443", BrokerHost: "127.0.0.1", BrokerURL: "tls://127.0.0.1:14224", BrokerPort: 14224, WebsocketPort: 18444, Audience: "auriga-network", IAM: rpc, Collab: rpc, ExportServiceToken: true}
}

func TestGenerateFailureRetry(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "empty-pinned"}[existing], func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "network")
			if existing {
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			// Credential creation succeeds, then entropy fails while issuing the CA.
			err := generate(dir, testOptions(), io.LimitReader(rand.Reader, 32))
			if ErrorCategory(err) != "generation_failed" {
				t.Fatalf("unexpected category: %v", err)
			}
			entries, readErr := os.ReadDir(dir)
			if existing && readErr != nil || len(entries) != 0 {
				t.Fatalf("partial material remains: %v", readErr)
			}
			if !existing && !os.IsNotExist(readErr) {
				t.Fatalf("new failed directory remains: %v", readErr)
			}
			if err := Generate(dir, testOptions()); err != nil {
				t.Fatalf("retry: %v", err)
			}
		})
	}
}

func TestGeneratePreservesForeignDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "foreign")
	if err := os.WriteFile(path, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Generate(dir, testOptions()); ErrorCategory(err) != "directory_refused" {
		t.Fatalf("unexpected category: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "preserve" {
		t.Fatal("foreign material changed", err)
	}
	if ErrorCategory(Generate(filepath.Join(dir, "bad"), Options{})) != "invalid_options" {
		t.Fatal("missing options category")
	}
}

func TestCertificateWarnings(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "network")
	if err := Generate(dir, testOptions()); err != nil {
		t.Fatal(err)
	}
	cert, err := readCertificate(filepath.Join(dir, "server-ca.pem"))
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "submit.key"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name          string
		now           time.Time
		warn, expired bool
	}{
		{"early", cert.NotAfter.Add(-CertificateWarningWindow - time.Second), false, false},
		{"boundary", cert.NotAfter.Add(-CertificateWarningWindow), true, false},
		{"expired", cert.NotAfter, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			err := CheckCertificates(dir, tc.now, &out)
			if errors.Is(err, ErrCertificateExpired) != tc.expired || err != nil && !tc.expired {
				t.Fatalf("status: %v", err)
			}
			if strings.Contains(out.String(), "network_certificate_expiring") != (tc.warn && !tc.expired) || strings.Contains(out.String(), "network_certificate_expired") != tc.expired {
				t.Fatalf("warning: %s", out.String())
			}
			if strings.Contains(out.String(), dir) || strings.Contains(out.String(), "PRIVATE KEY") {
				t.Fatal("private data in warning")
			}
		})
	}
	after, err := os.ReadFile(filepath.Join(dir, "submit.key"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("status changed private key", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "edge.pem"), []byte("secret-invalid-certificate"), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := CheckCertificates(dir, time.Now(), &out); !errors.Is(err, ErrCertificateInvalid) || !strings.Contains(out.String(), "network_certificate_invalid file=edge.pem") || strings.Contains(out.String(), "secret-invalid") {
		t.Fatal("invalid certificate not safely reported", err)
	}
}
