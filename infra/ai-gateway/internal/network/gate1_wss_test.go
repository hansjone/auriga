package network

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/url"
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
	wsURL := h.wsURL()
	if !strings.HasPrefix(wsURL, "wss://") {
		t.Fatalf("ws URL = %q, want wss://", wsURL)
	}
	trustedTLS := &tls.Config{RootCAs: mat.pool, MinVersion: tls.VersionTLS12}

	t.Run("trusted-ca-connects-and-callout-issues", func(t *testing.T) {
		tk, ing := h.tickets.issue(t, time.Now().Add(time.Hour))
		hdr := http.Header{}
		hdr.Set("Origin", allowedOrigin)
		c, err := h.connect(wsURL, tk, ing,
			nats.Secure(trustedTLS),
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
		_, err := h.connect(wsURL, tk, ing,
			nats.Secure(&tls.Config{MinVersion: tls.VersionTLS12}), // system roots only
			nats.WebSocketConnectionHeaders(hdr),
		)
		if err == nil {
			t.Fatal("connected without trusting test CA")
		}
		// Prefer UnknownAuthority; also accept it wrapped by CertificateVerificationError.
		var unknownAuth x509.UnknownAuthorityError
		ok := errors.As(err, &unknownAuth)
		if !ok {
			var certVerify *tls.CertificateVerificationError
			ok = errors.As(err, &certVerify) && errors.As(certVerify.Err, &unknownAuth)
		}
		if !ok {
			t.Fatalf("want x509.UnknownAuthorityError, got %T: %v", err, err)
		}
		t.Logf("untrusted: %v", err)
	})

	// Raw Upgrade status codes (nats-server websocket.go: Origin fail -> 403).
	// nats.Connect only surfaces "invalid websocket connection", which cannot
	// distinguish Origin rejection from other handshake failures.
	t.Run("origin-upgrade-status", func(t *testing.T) {
		if code := wsUpgradeStatus(t, wsURL, allowedOrigin, trustedTLS); code != http.StatusSwitchingProtocols {
			t.Fatalf("allowed Origin status = %d, want 101", code)
		}
		if code := wsUpgradeStatus(t, wsURL, "https://evil.example.com", trustedTLS); code != http.StatusForbidden {
			t.Fatalf("disallowed Origin status = %d, want 403", code)
		}
	})
}

// wsUpgradeStatus performs a TLS WebSocket Upgrade with the given Origin and
// returns the HTTP status from the handshake response (101 or 4xx).
func wsUpgradeStatus(t *testing.T, wssURL, origin string, tlsCfg *tls.Config) int {
	t.Helper()
	u, err := url.Parse(wssURL)
	if err != nil {
		t.Fatal(err)
	}
	host := u.Host
	if !strings.Contains(host, ":") {
		host += ":443"
	}
	d := &net.Dialer{Timeout: 5 * time.Second}
	raw, err := d.Dial("tcp", host)
	if err != nil {
		t.Fatal(err)
	}
	cfg := tlsCfg.Clone()
	if cfg.ServerName == "" {
		cfg.ServerName = "127.0.0.1"
	}
	conn := tls.Client(raw, cfg)
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if err := conn.Handshake(); err != nil {
		t.Fatalf("tls handshake: %v", err)
	}

	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	req := &http.Request{
		Method:     http.MethodGet,
		URL:        &url.URL{Scheme: "https", Host: u.Host, Path: "/"},
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     make(http.Header),
		Host:       u.Host,
	}
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Sec-WebSocket-Key", base64.StdEncoding.EncodeToString(key))
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Origin", origin)
	if err := req.Write(conn); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		t.Fatalf("read upgrade response: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	t.Logf("origin=%q status=%d", origin, resp.StatusCode)
	return resp.StatusCode
}
