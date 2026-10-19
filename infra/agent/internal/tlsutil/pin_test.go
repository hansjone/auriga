package tlsutil_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/identity"
	"github.com/hansjone/auriga/infra/agent/internal/tlsutil"
)

func TestVerifyPinnedPeerChainAcceptsMatchingCA(t *testing.T) {
	caCert, caKey := mustSelfSigned(t, true)
	leafCert, leafKey := mustIssued(t, caCert, caKey)
	_ = leafKey
	want := identity.SPKISHA256Hex(caCert)
	raw := [][]byte{leafCert.Raw, caCert.Raw}
	if err := tlsutil.VerifyPinnedPeerChain(raw, want, ""); err != nil {
		t.Fatal(err)
	}
	if err := tlsutil.VerifyPinnedPeerChain(raw, "00"+want[2:], ""); err == nil {
		t.Fatal("expected mismatch")
	}
}

func TestVerifyCAPEMFingerprint(t *testing.T) {
	caCert, _ := mustSelfSigned(t, true)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCert.Raw})
	want := identity.SPKISHA256Hex(caCert)
	if err := tlsutil.VerifyCAPEMFingerprint(pemBytes, want); err != nil {
		t.Fatal(err)
	}
	if err := tlsutil.VerifyCAPEMFingerprint(pemBytes, "deadbeef"); err == nil {
		t.Fatal("expected mismatch")
	}
}

func TestPipeTLSPin(t *testing.T) {
	// net.Pipe avoids localhost TLS MITM from antivirus.
	caCert, caKey := mustSelfSigned(t, true)
	want := identity.SPKISHA256Hex(caCert)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCert.Raw})
	keyDER, err := x509.MarshalECPrivateKey(caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}

	c1, c2 := net.Pipe()
	defer func() { _ = c1.Close() }()
	defer func() { _ = c2.Close() }()

	errCh := make(chan error, 1)
	go func() {
		srv := tls.Server(c2, &tls.Config{
			Certificates: []tls.Certificate{tlsCert},
			MinVersion:   tls.VersionTLS12,
		})
		errCh <- srv.Handshake()
		_ = srv.Close()
	}()

	cli := tls.Client(c1, &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			return tlsutil.VerifyPinnedPeerChain(rawCerts, want, "")
		},
	})
	if err := cli.Handshake(); err != nil {
		t.Fatal("client handshake:", err)
	}
	_ = cli.Close()
	if err := <-errCh; err != nil {
		t.Fatal("server handshake:", err)
	}
}

func mustSelfSigned(t *testing.T, isCA bool) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "pipe-ca"},
		DNSNames:              []string{"platform.example.com"},
		IPAddresses:           []net.IP{net.ParseIP("192.0.2.10")},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  isCA,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key
}

func mustIssued(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "leaf"},
		DNSNames:     []string{"platform.example.com"},
		IPAddresses:  []net.IP{net.ParseIP("192.0.2.10")},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key
}

func TestPinnedPeerRejectsHostnameMismatch(t *testing.T) {
	ca, key := mustSelfSigned(t, true)
	leaf, _ := mustIssued(t, ca, key)
	for name, raw := range map[string][][]byte{"chain": {leaf.Raw, ca.Raw}, "single": {ca.Raw}} {
		t.Run(name, func(t *testing.T) {
			fp := identity.SPKISHA256Hex(ca)
			if err := tlsutil.VerifyPinnedPeerChain(raw, fp, "platform.example.com"); err != nil {
				t.Fatal(err)
			}
			for _, host := range []string{"other.example.com", "192.0.2.11"} {
				err := tlsutil.VerifyPinnedPeerChain(raw, fp, host)
				var hostErr x509.HostnameError
				if !errors.As(err, &hostErr) {
					t.Fatalf("expected hostname rejection for %s: %v", host, err)
				}
			}
			if err := tlsutil.VerifyPinnedPeerChain(raw, fp, "192.0.2.10"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPinnedSingleCertificateRejectsInvalidValidity(t *testing.T) {
	ca, key := mustSelfSigned(t, true)
	for _, future := range []bool{false, true} {
		cert := *ca
		cert.NotBefore = time.Now().Add(-2 * time.Hour)
		cert.NotAfter = time.Now().Add(-time.Hour)
		if future {
			cert.NotBefore = time.Now().Add(time.Hour)
			cert.NotAfter = time.Now().Add(2 * time.Hour)
		}
		der, err := x509.CreateCertificate(rand.Reader, &cert, &cert, &key.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		if err := tlsutil.VerifyPinnedPeerChain([][]byte{der}, identity.SPKISHA256Hex(ca), "platform.example.com"); err == nil || !strings.Contains(err.Error(), "not valid at current time") {
			t.Fatalf("invalid validity accepted: %v", err)
		}
	}
}

func TestDialRejectsPlaintext(t *testing.T) {
	for _, endpoint := range []string{"http://platform.example.com:7443", "grpc://platform.example.com:7443"} {
		c, err := tlsutil.Dial(context.Background(), tlsutil.DialOptions{Endpoint: endpoint})
		if c != nil {
			_ = c.Close()
			t.Fatal("plaintext connection created")
		}
		if err == nil || !strings.Contains(err.Error(), "insecure endpoint") {
			t.Fatalf("plaintext accepted: %v", err)
		}
	}
}
