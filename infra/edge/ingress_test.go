package main

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const testNodeID = "44444444-4444-4444-4444-444444444444"
const testInstanceID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"

func testNode(t *testing.T, cfg *Config, h http.Handler, nodeID string) *httptest.Server {
	t.Helper()
	cfg.AllowLocalIngress = true // Explicit opt-in for local test listeners.
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err = x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	issue := func(uri string, serial int64, usage ...x509.ExtKeyUsage) (tls.Certificate, []byte, []byte) {
		key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if e != nil {
			t.Fatal(e)
		}
		u, e := url.Parse(uri)
		if e != nil {
			t.Fatal(e)
		}
		leaf := &x509.Certificate{SerialNumber: big.NewInt(serial), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), URIs: []*url.URL{u}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: usage}
		der, e := x509.CreateCertificate(rand.Reader, leaf, ca, &key.PublicKey, caKey)
		if e != nil {
			t.Fatal(e)
		}
		keyDER, e := x509.MarshalPKCS8PrivateKey(key)
		if e != nil {
			t.Fatal(e)
		}
		cp := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
		kp := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
		pair, e := tls.X509KeyPair(cp, kp)
		if e != nil {
			t.Fatal(e)
		}
		return pair, cp, kp
	}
	_, certPEM, keyPEM := issue("auriga://edge/test", 2, x509.ExtKeyUsageClientAuth)
	server, _, _ := issue("auriga://node/"+nodeID, 3, x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth)
	dir := t.TempDir()
	cfg.NodeTLS = NodeTLSConfig{CertFile: filepath.Join(dir, "edge.pem"), KeyFile: filepath.Join(dir, "edge.key"), CAFile: filepath.Join(dir, "ca.pem")}
	for path, data := range map[string][]byte{cfg.NodeTLS.CertFile: certPEM, cfg.NodeTLS.KeyFile: keyPEM, cfg.NodeTLS.CAFile: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	srv := httptest.NewUnstartedServer(h)
	endpoint := ""
	// Windows HTTPS inspection can substitute certificates on loopback TCP.
	// Exercise the same real TLS handshake over a randomly named local pipe there.
	if runtime.GOOS == "windows" {
		_ = srv.Listener.Close()
		srv.Listener, endpoint = localTestListener(t)
	}
	srv.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{server}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool}
	srv.StartTLS()
	if endpoint != "" {
		srv.URL = endpoint
	}
	t.Cleanup(srv.Close)
	return srv
}

func instanceFixture(t *testing.T, h http.Handler) (*Gateway, *MemorySessionStore, *MemoryRouteStore, *InstanceRoute, string) {
	t.Helper()
	cfg, sessions, routes, tickets, signer, _ := testEnv(t)
	cfg.SecureCookies = false
	node := testNode(t, &cfg, h, testNodeID)
	sess := putPortalSession(sessions, "session", "csrf", "token")
	binding, err := tickets.BindInstance(sess.SessionID, "", sess.AccountID, sess.TenantID, testInstanceID, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	route := &InstanceRoute{InstanceID: testInstanceID, NodeID: testNodeID, TenantID: sess.TenantID, OwnerID: sess.AccountID, Ingress: node.URL, State: "running", UpdatedAt: time.Now()}
	routes.Put(route)
	g := newTestGateway(t, cfg, sessions, routes, tickets, signer)
	t.Cleanup(g.Ingress.Close)
	return g, sessions, routes, route, binding.CookieRaw
}

func instanceRequest(cookie, path string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "http://"+testInstanceID+".i.example.com"+path, nil)
	r.AddCookie(&http.Cookie{Name: "auriga_inst", Value: cookie})
	return r
}

func TestIngressMTLSIdentityAndAuthorization(t *testing.T) {
	seen := make(chan *http.Request, 1)
	g, _, routes, route, cookie := instanceFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen <- r; w.WriteHeader(204) }))
	r := instanceRequest(cookie, "/app?a=1")
	r.Header.Set("Authorization", "forged")
	r.Header.Set("Auriga-Identity", "forged")
	w := httptest.NewRecorder()
	g.Handler().ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatalf("forward: %d %s", w.Code, w.Body.String())
	}
	got := <-seen
	claims, _, err := VerifyIdentityToken(got.Header.Get("Auriga-Identity"), g.Signer.PublicKey())
	if err != nil || claims.Aud != testInstanceID || claims.Sub != route.OwnerID || claims.Tid != route.TenantID || claims.Htu != "/app" || got.URL.RequestURI() != "/i/"+testInstanceID+"/app?a=1" || got.Header.Get("Authorization") != "" {
		t.Fatalf("identity/path: %+v %v %s", claims, err, got.URL)
	}
	for _, mutate := range []func(){func() { route.OwnerID = "55555555-5555-5555-5555-555555555555" }, func() { route.OwnerID = claims.Sub; route.TenantID = "55555555-5555-5555-5555-555555555555" }} {
		mutate()
		routes.Put(route)
		w = httptest.NewRecorder()
		g.Handler().ServeHTTP(w, instanceRequest(cookie, "/"))
		if w.Code != 403 || !strings.Contains(w.Body.String(), "edge.not_owner") {
			t.Fatalf("authorization: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestIngressRejectsUntrustedTargets(t *testing.T) {
	var forwarded atomic.Int64
	g, _, routes, route, cookie := instanceFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded.Add(1) }))
	valid := route.Ingress
	for _, raw := range []string{"http://node.example.com:7443", "https://node.example.com/admin", "https://user:pass@example.com", "file:///etc/passwd"} {
		route.Ingress = raw
		routes.Put(route)
		w := httptest.NewRecorder()
		g.Handler().ServeHTTP(w, instanceRequest(cookie, "/"))
		if w.Code != 502 || !strings.Contains(w.Body.String(), "edge.bad_ingress") {
			t.Fatalf("target %q: %d", raw, w.Code)
		}
	}
	route.Ingress = valid
	route.NodeID = "55555555-5555-5555-5555-555555555555"
	routes.Put(route)
	w := httptest.NewRecorder()
	g.Handler().ServeHTTP(w, instanceRequest(cookie, "/"))
	if w.Code != 502 || !strings.Contains(w.Body.String(), "edge.upstream_error") {
		t.Fatalf("wrong node: %d %s", w.Code, w.Body.String())
	}
	// Correct node identity issued by another CA must also fail before an HTTP request.
	cfg := g.Cfg
	other := testNode(t, &cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded.Add(1) }), testNodeID)
	route.NodeID = testNodeID
	route.Ingress = other.URL
	routes.Put(route)
	w = httptest.NewRecorder()
	g.Handler().ServeHTTP(w, instanceRequest(cookie, "/"))
	if w.Code != 502 || forwarded.Load() != 0 {
		t.Fatalf("untrusted CA: %d forwarded=%d", w.Code, forwarded.Load())
	}
}

func TestIngressWebSocketRevoke(t *testing.T) {
	verified := make(chan string, 1)
	g, sessions, _, _, cookie := instanceFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		verified <- r.Header.Get("Auriga-Identity")
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		_ = rw.Flush()
		_, _ = io.Copy(conn, conn)
	}))
	srv := httptest.NewServer(g.Handler())
	defer srv.Close()
	conn, err := net.DialTimeout("tcp", strings.TrimPrefix(srv.URL, "http://"), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	r := instanceRequest(cookie, "/ws")
	r.RequestURI = ""
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Origin", "http://"+r.Host)
	if err := r.Write(conn); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, r)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 101 {
		t.Fatalf("upgrade %d", response.StatusCode)
	}
	token := <-verified
	claims, _, err := VerifyIdentityToken(token, g.Signer.PublicKey())
	if err != nil || claims.Aud != testInstanceID {
		t.Fatalf("WS identity %v", err)
	}
	if _, err := conn.Write([]byte{0x81, 1, 'x'}); err != nil {
		t.Fatal(err)
	}
	echo := make([]byte, 3)
	if _, err := io.ReadFull(reader, echo); err != nil {
		t.Fatal(err)
	}
	sessions.Revoke(claims.Sid)
	if _, err := reader.ReadByte(); err == nil {
		t.Fatal("revoked websocket stayed open")
	} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("revoke did not close websocket")
	}
}

func TestNATSRoutesMigrationDeleteAndUnavailable(t *testing.T) {
	raw, stop := startTestNATS(t, "routes", "test-pass")
	defer stop()
	nc, err := nats.Connect(raw)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	kv, err := js.CreateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: "auriga-routes"})
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewNATSRouteStore(raw, "")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	route := InstanceRoute{InstanceID: testInstanceID, NodeID: testNodeID, TenantID: testNodeID, OwnerID: testNodeID, Ingress: "https://node.example.com:7443", State: "running"}
	put := func() {
		b, e := json.Marshal(route)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = kv.Put(ctx, testInstanceID, b); e != nil {
			t.Fatal(e)
		}
	}
	put()
	first, err := store.Get(ctx, testInstanceID)
	if err != nil {
		t.Fatal(err)
	}
	first.OwnerID = "mutated"
	route.Ingress = "https://new-node.example.com:7443"
	put()
	next, err := store.Get(ctx, testInstanceID)
	if err != nil || next.Ingress != route.Ingress || next.OwnerID != testNodeID {
		t.Fatalf("migration %v %+v", err, next)
	}
	if err := kv.Delete(ctx, testInstanceID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, testInstanceID); err == nil {
		t.Fatal("deleted route served from cache")
	}
	put()
	if _, err := store.Get(ctx, testInstanceID); err != nil {
		t.Fatal(err)
	}
	store.Close()
	if _, err := store.Get(ctx, testInstanceID); err == nil {
		t.Fatal("unavailable store served cache")
	}
}

// Real DSH multiplexes its browser transport over /api/remote.mux; the edge
// must forward that upgrade with an identity bound to the exact path, and must
// still refuse a cross-site Origin before reaching the node.
func TestIngressRealDSHRemoteMuxUpgrade(t *testing.T) {
	seen := make(chan *http.Request, 1)
	g, _, _, _, cookie := instanceFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		_ = rw.Flush()
	}))
	srv := httptest.NewServer(g.Handler())
	defer srv.Close()

	bad := instanceRequest(cookie, "/api/remote.mux")
	bad.Header.Set("Connection", "Upgrade")
	bad.Header.Set("Upgrade", "websocket")
	bad.Header.Set("Origin", "http://evil.example.net")
	w := httptest.NewRecorder()
	g.Handler().ServeHTTP(w, bad)
	if w.Code != http.StatusForbidden || len(seen) != 0 {
		t.Fatalf("cross-site mux upgrade: %d forwarded=%d", w.Code, len(seen))
	}

	conn, err := net.DialTimeout("tcp", strings.TrimPrefix(srv.URL, "http://"), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	r := instanceRequest(cookie, "/api/remote.mux")
	r.RequestURI = ""
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Origin", "http://"+r.Host)
	if err := r.Write(conn); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), r)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("mux upgrade %d", response.StatusCode)
	}
	got := <-seen
	claims, _, err := VerifyIdentityToken(got.Header.Get("Auriga-Identity"), g.Signer.PublicKey())
	if err != nil || claims.Htu != "/api/remote.mux" || claims.Htm != http.MethodGet || got.URL.Path != "/i/"+testInstanceID+"/api/remote.mux" {
		t.Fatalf("mux identity/path: %+v %v %s", claims, err, got.URL)
	}
}
