package ingress

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type testCA struct {
	key  *ecdsa.PrivateKey
	cert *x509.Certificate
	pem  []byte
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
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
	return &testCA{key: key, cert: cert, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

func (c *testCA) issue(t *testing.T, cn, uriSAN string, client, server bool) (tls.Certificate, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	uri, err := url.Parse(uriSAN)
	if err != nil {
		t.Fatal(err)
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	var eku []x509.ExtKeyUsage
	if client {
		eku = append(eku, x509.ExtKeyUsageClientAuth)
	}
	if server {
		eku = append(eku, x509.ExtKeyUsageServerAuth)
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  eku,
		URIs:         []*url.URL{uri},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.cert, &key.PublicKey, c.key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return tlsCert, certPEM
}

func startTestIngress(t *testing.T, reg *MapRegistry, tickets *TicketVerifier, idKeys IdentityKeys) (*Server, *http.Client, *testCA) {
	t.Helper()
	ca := newTestCA(t)
	nodeCert, _ := ca.issue(t, "node-1", "auriga://node/n1", false, true)
	edgeCert, _ := ca.issue(t, "edge", "auriga://edge", true, false)
	pool := x509.NewCertPool()
	pool.AddCert(ca.cert)

	srv := New(Config{
		ServerCert:   nodeCert,
		PlatformCA:   pool,
		Registry:     reg,
		Tickets:      tickets,
		IdentityKeys: idKeys,
		Logger:       nil,
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.ServeTLSForTest(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	edge := edgeCert
	clientTLS := &tls.Config{
		Certificates:       []tls.Certificate{edge},
		RootCAs:            pool,
		ServerName:         "localhost",
		InsecureSkipVerify: true, // gitleaks:allow - per-test self-signed CA
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			return &edge, nil
		},
	}
	// Wait until accept works.
	ready := false
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		c, err := tls.Dial("tcp", ln.Addr().String(), clientTLS.Clone())
		if err == nil {
			_ = c.Close()
			ready = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		t.Fatal("ingress TLS not ready")
	}
	tlsCfg := clientTLS
	base := "https://" + ln.Addr().String()
	client := &http.Client{
		Transport: roundTripBase{
			base: base,
			rt:   &http.Transport{TLSClientConfig: tlsCfg},
		},
		Timeout: 20 * time.Second,
	}
	t.Cleanup(func() { client.Transport.(roundTripBase).rt.(*http.Transport).CloseIdleConnections() })
	return srv, client, ca
}

type roundTripBase struct {
	base string
	rt   http.RoundTripper
}

func (r roundTripBase) RoundTrip(req *http.Request) (*http.Response, error) {
	u, err := url.Parse(r.base + req.URL.RequestURI())
	if err != nil {
		return nil, err
	}
	req2 := req.Clone(req.Context())
	req2.URL = u
	req2.Host = u.Host
	return r.rt.RoundTrip(req2)
}

func TestMTLS_RejectsNonEdge(t *testing.T) {
	ca := newTestCA(t)
	nodeCert, _ := ca.issue(t, "node", "auriga://node/n1", false, true)
	otherCert, _ := ca.issue(t, "other", "auriga://node/other", true, false)
	pool := x509.NewCertPool()
	pool.AddCert(ca.cert)
	srv := New(Config{ServerCert: nodeCert, PlatformCA: pool, Registry: NewMapRegistry()})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.ServeTLSForTest(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{otherCert},
				RootCAs:      pool,
				ServerName:   "localhost",
			},
		},
		Timeout: 3 * time.Second,
	}
	resp, err := client.Get("https://" + ln.Addr().String() + "/i/x/")
	if err == nil {
		_ = resp.Body.Close()
		t.Fatalf("expected TLS handshake failure for non-edge cert, got status %d", resp.StatusCode)
	}
}

func TestProxy_StripsForgedIdentity(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	kid := "id-1"
	fixed := time.Now()
	good, err := SignIdentityJWS(priv, kid, map[string]any{
		"iss": "auriga-edge", "aud": "inst-1", "sub": "alice",
		"iat": fixed.Unix(), "exp": fixed.Add(time.Minute).Unix(), "pv": 1,
		"htm": "GET", "htu": "/hello", "jti": "1",
	})
	if err != nil {
		t.Fatal(err)
	}

	seen := make(chan string, 2)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Get("Auriga-Identity")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(up.Close)
	u, _ := url.Parse(up.URL)

	root := t.TempDir()
	reg := NewMapRegistry(&Instance{ID: "inst-1", Upstream: u, RootDir: root, LogPath: filepath.Join(root, "out.log")})
	// Exercise identity forwarding over HTTP; the separate mTLS test checks the
	// certificate gate. This avoids local TLS interception affecting this test.
	srv := New(Config{Registry: reg, IdentityKeys: IdentityKeys{kid: pub}})
	t.Cleanup(func() { _ = srv.Close() })
	proxy := httptest.NewServer(srv.Handler())
	t.Cleanup(proxy.Close)
	client := proxy.Client()
	client.Timeout = 20 * time.Second
	t.Cleanup(client.CloseIdleConnections)

	req, _ := http.NewRequest(http.MethodGet, proxy.URL+"/i/inst-1/hello", nil)
	req.Header.Set("Auriga-Identity", "forged-not-a-jws")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if seen := <-seen; seen != "" {
		t.Fatalf("forged identity should be stripped, got %q", seen)
	}

	req2, _ := http.NewRequest(http.MethodGet, proxy.URL+"/i/inst-1/hello", nil)
	req2.Header.Set("Auriga-Identity", good)
	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp2.Body.Close()
	if seen := <-seen; seen != good {
		t.Fatalf("want edge identity forwarded, got %q", seen)
	}
}

func TestFiles_TicketAndPathEscape(t *testing.T) {
	priv, pub, kid := testTicketKeys(t)
	fixed := time.Now()
	v := &TicketVerifier{Keys: map[string]ed25519.PublicKey{kid: pub}, Now: func() time.Time { return fixed }}

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "workspace"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "workspace", "a.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	_ = os.WriteFile(filepath.Join(outside, "secret"), []byte("no"), 0o600)
	if err := os.Symlink(outside, filepath.Join(root, "leak")); err != nil {
		t.Log("symlink skipped:", err)
	}

	reg := NewMapRegistry(&Instance{ID: "inst-1", RootDir: root, LogPath: filepath.Join(root, "out.log")})
	_, client, _ := startTestIngress(t, reg, v, nil)

	ticketSeq := 0
	mk := func(mod func(*FileTicketClaims)) string {
		ticketSeq++
		c := FileTicketClaims{
			Iss: "auriga-workspace", Aud: "auriga-agent", IID: "inst-1",
			PathPrefix: "workspace", Ops: []string{"read", "write", "list", "delete", "zip"},
			Iat: fixed.Unix(), Exp: fixed.Add(time.Minute).Unix(), Jti: fmt.Sprint(ticketSeq),
		}
		if mod != nil {
			mod(&c)
		}
		tok, err := SignFileTicket(priv, kid, c)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}

	get := func(path, tok string) (int, string, Problem) {
		req, _ := http.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		var p Problem
		_ = json.Unmarshal(body, &p)
		return resp.StatusCode, string(body), p
	}

	code, body, _ := get("/files/inst-1/workspace/a.txt", mk(nil))
	if code != 200 || !strings.Contains(body, "hi") {
		t.Fatalf("read ok: %d %s", code, body)
	}

	_, _, p := get("/files/inst-1/workspace/a.txt", mk(func(c *FileTicketClaims) { c.Exp = fixed.Add(-time.Minute).Unix() }))
	if p.Code != CodeTicketExpired {
		t.Fatalf("expired: %+v", p)
	}

	_, _, p = get("/files/inst-1/workspace/a.txt", mk(func(c *FileTicketClaims) { c.IID = "other" }))
	if p.Code != CodeTicketInstanceMismatch {
		t.Fatalf("instance: %+v", p)
	}

	_, _, p = get("/files/inst-1/secrets/x", mk(nil))
	if p.Code != CodeTicketPathDenied {
		t.Fatalf("path denied: %+v", p)
	}

	tokWriteOnly := mk(func(c *FileTicketClaims) { c.Ops = []string{"write"} })
	_, _, p = get("/files/inst-1/workspace/a.txt", tokWriteOnly)
	if p.Code != CodeTicketOpDenied {
		t.Fatalf("op denied: %+v", p)
	}

	// `..` path escape is covered by TestValidateRelPath / TestResolveUnderRoot
	// (HTTP clients normalize `/files/inst/../x` away from the instance id).

	if _, err := os.Lstat(filepath.Join(root, "leak")); err == nil {
		req, _ := http.NewRequest(http.MethodGet, "/files/inst-1/leak/secret", nil)
		req.Header.Set("Authorization", "Bearer "+mk(func(c *FileTicketClaims) { c.PathPrefix = "" }))
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		bodyBytes, _ := io.ReadAll(resp.Body)
		var pe Problem
		_ = json.Unmarshal(bodyBytes, &pe)
		if pe.Code != CodePathEscape {
			t.Fatalf("symlink escape want %s got %+v body=%s", CodePathEscape, pe, bodyBytes)
		}
	}
}

func TestLogs_Tail(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "out.log")
	if err := os.WriteFile(logPath, []byte("line1\nline2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reg := NewMapRegistry(&Instance{ID: "inst-1", RootDir: root, LogPath: logPath})
	_, client, _ := startTestIngress(t, reg, &TicketVerifier{Keys: map[string]ed25519.PublicKey{}}, nil)
	resp, err := client.Get("/logs/inst-1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "line2") {
		t.Fatalf("got %q", b)
	}
}

func TestMetrics(t *testing.T) {
	reg := NewMapRegistry()
	_, client, _ := startTestIngress(t, reg, &TicketVerifier{Keys: map[string]ed25519.PublicKey{}}, nil)
	resp, err := client.Get("/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "auriga_agent_ingress_requests_total") {
		t.Fatalf("metrics: %s", b)
	}
}

func TestRollingWriter(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "a.log")
	w, err := NewRollingWriter(path, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	for i := 0; i < 10; i++ {
		if _, err := fmt.Fprintf(w, "xxxxxxxxxxxxxxxx"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("expected rotate: %v", err)
	}
}
