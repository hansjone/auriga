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
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

const allowedOrigin = "https://portal.example.com"

type testTLSMaterial struct {
	caPEM    []byte
	certFile string
	keyFile  string
	pool     *x509.CertPool
}

// genTestTLSCA writes a self-signed CA and a server cert for 127.0.0.1 into
// dir (temp). Nothing is committed to the repository.
func genTestTLSCA(t *testing.T, dir string) testTLSMaterial {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "auriga-n1-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})

	srvKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	srvTpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	srvDER, err := x509.CreateCertificate(rand.Reader, srvTpl, caCert, &srvKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	certFile := filepath.Join(dir, "server.pem")
	keyFile := filepath.Join(dir, "server-key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srvDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(srvKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		t.Fatal("append CA")
	}
	return testTLSMaterial{caPEM: caPEM, certFile: certFile, keyFile: keyFile, pool: pool}
}

func wssHarness(t *testing.T) (*harness, testTLSMaterial) {
	t.Helper()
	mat := genTestTLSCA(t, t.TempDir())
	o := defaultBrokerOpts()
	o.tls = true
	o.tlsCertFile = mat.certFile
	o.tlsKeyFile = mat.keyFile
	o.allowedOrigins = []string{allowedOrigin}
	return newHarness(t, o), mat
}

func TestGate1_WSS_TLSAndOrigin(t *testing.T) {
	h, mat := wssHarness(t)
	url := h.wsURL()
	if !strings.HasPrefix(url, "wss://") {
		t.Fatalf("ws URL = %q, want wss://", url)
	}

	t.Run("trusted-ca-connects-and-callout-issues", func(t *testing.T) {
		tk, ing := h.tickets.issue(t, time.Now().Add(time.Hour))
		hdr := http.Header{}
		hdr.Set("Origin", allowedOrigin)
		c, err := h.connect(url, tk, ing,
			nats.Secure(&tls.Config{RootCAs: mat.pool, MinVersion: tls.VersionTLS12}),
			nats.WebSocketConnectionHeaders(hdr),
		)
		if err != nil {
			t.Fatalf("trusted connect: %v", err)
		}
		d := h.lastDecision()
		if d.err != nil || d.uc == nil {
			t.Fatalf("callout decision err=%v uc=%v", d.err, d.uc != nil)
		}
		if d.a.ClientType != "websocket" || d.uc.Name != ing {
			t.Fatalf("type=%q name=%q", d.a.ClientType, d.uc.Name)
		}
		if !c.nc.IsConnected() {
			t.Fatal("not connected")
		}
	})

	t.Run("untrusted-ca-rejected", func(t *testing.T) {
		tk, ing := h.tickets.issue(t, time.Now().Add(time.Hour))
		hdr := http.Header{}
		hdr.Set("Origin", allowedOrigin)
		_, err := h.connect(url, tk, ing,
			nats.Secure(&tls.Config{MinVersion: tls.VersionTLS12}), // system roots only
			nats.WebSocketConnectionHeaders(hdr),
		)
		if err == nil {
			t.Fatal("connected without trusting test CA")
		}
		if !tlsFailed(err) {
			t.Fatalf("want TLS failure, got %v", err)
		}
		t.Logf("untrusted: %v", err)
	})

	t.Run("wrong-origin-rejected", func(t *testing.T) {
		tk, ing := h.tickets.issue(t, time.Now().Add(time.Hour))
		hdr := http.Header{}
		hdr.Set("Origin", "https://evil.example.com")
		_, err := h.connect(url, tk, ing,
			nats.Secure(&tls.Config{RootCAs: mat.pool, MinVersion: tls.VersionTLS12}),
			nats.WebSocketConnectionHeaders(hdr),
		)
		if err == nil {
			t.Fatal("connected with disallowed Origin")
		}
		t.Logf("wrong origin: %v", err)
	})
}

func tlsFailed(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "certificate") ||
		strings.Contains(msg, "x509") ||
		strings.Contains(msg, "tls") ||
		strings.Contains(msg, "unknown authority")
}
