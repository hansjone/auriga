package network

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func browserFixture(t *testing.T, expired bool, dns ...string) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "not-a-san.example"}, DNSNames: []string{"portal.example.com", "admin.example.com", "*.i.example.com"}, IPAddresses: []net.IP{net.ParseIP("192.0.2.10"), net.ParseIP("127.0.0.1")}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	if len(dns) > 0 {
		cert.DNSNames, cert.IPAddresses = dns, nil
	}
	if expired {
		cert.NotAfter = now.Add(-time.Minute)
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pk, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk}), 0600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}

func TestBrowserTLSValidationAndHandshake(t *testing.T) {
	cert, key := browserFixture(t, false)
	expired, expiredKey := browserFixture(t, true)
	_, otherKey := browserFixture(t, false)
	fixed, fixedKey := browserFixture(t, false, "instance.i.example.com")
	numericDNS, numericKey := browserFixture(t, false, "192.0.2.10")
	certBytes, err := os.ReadFile(cert)
	if err != nil {
		t.Fatal(err)
	}
	keyBytes, err := os.ReadFile(key)
	if err != nil {
		t.Fatal(err)
	}
	contaminated := filepath.Join(t.TempDir(), "private-block-in-certificate.pem")
	if err := os.WriteFile(contaminated, append(append([]byte{}, certBytes...), keyBytes...), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, cert, key string
		enabled         bool
		hosts           []string
		valid           bool
	}{
		{"domain", cert, key, true, []string{"portal.example.com", "admin.example.com", "*.i.example.com"}, true},
		{"IP-SAN", cert, key, true, []string{"192.0.2.10"}, true},
		{"wrong-IP", cert, key, true, []string{"192.0.2.11"}, false},
		{"CN-is-not-SAN", cert, key, true, []string{"not-a-san.example"}, false},
		{"wrong-wildcard", cert, key, true, []string{"*.other.example.com"}, false},
		{"fixed-instance-is-not-wildcard", fixed, fixedKey, true, []string{"*.i.example.com"}, false},
		{"numeric-DNS-is-not-IP-SAN", numericDNS, numericKey, true, []string{"192.0.2.10"}, false},
		{"expired", expired, expiredKey, true, []string{"portal.example.com"}, false},
		{"mismatch", cert, otherKey, true, nil, false},
		{"certificate-hides-private-key", contaminated, key, true, nil, false},
		{"missing", cert, "", true, nil, false},
		{"HTTP-conflict", cert, key, false, nil, false},
		{"HTTP-default", "", "", false, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, err := ReadBrowserTLS(tc.cert, tc.key, tc.enabled, tc.hosts)
			if (err == nil) != tc.valid {
				t.Fatalf("accepted=%t err=%v", tc.valid, err)
			}
			if err != nil && (strings.Contains(err.Error(), cert) || strings.Contains(err.Error(), "PRIVATE KEY")) {
				t.Fatal("secret/path in error")
			}
			if b != nil {
				defer b.Clear()
			}
		})
	}
	b, err := ReadBrowserTLS(cert, key, true, []string{"127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Clear()
	out := t.TempDir()
	if err := os.Mkdir(filepath.Join(out, "pki"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := b.Write(out); err != nil {
		t.Fatal(err)
	}
	if err := b.Write(out); err == nil {
		t.Fatal("managed material overwritten")
	}
	before, _ := os.ReadFile(key)
	if string(before) != string(b.PrivateKey) {
		t.Fatal("source key changed")
	}
	pair, err := tls.X509KeyPair(b.Certificate, b.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp4", "127.0.0.1:0", &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 2; i++ {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.SetDeadline(time.Now().Add(time.Second))
			_ = c.(*tls.Conn).Handshake()
			_ = c.Close()
		}
	}()
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(b.Certificate)
	for _, trusted := range []bool{true, false} {
		cfg := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: "127.0.0.1"}
		if !trusted {
			cfg.RootCAs = x509.NewCertPool()
		}
		c, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp4", ln.Addr().String(), cfg)
		if (err == nil) != trusted {
			t.Fatalf("TLS trusted=%t: %v", trusted, err)
		}
		if c != nil {
			_ = c.Close()
		}
	}
	<-done
}
