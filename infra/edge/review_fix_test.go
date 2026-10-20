package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

func TestIngressRestrictedResolvedAddresses(t *testing.T) {
	blocked := []string{"127.0.0.1", "::1", "0.0.0.0", "::", "::ffff:127.0.0.1", "fe80::1", "224.0.0.1"} // gitleaks:allow
	blocked = append(blocked, "169.254.169.254")                                                         // gitleaks:allow
	for _, raw := range blocked {
		if err := validateIngressAddresses([]netip.Addr{netip.MustParseAddr(raw)}, false); err == nil {
			t.Errorf("allowed %s", raw)
		}
	}
	if err := validateIngressAddresses([]netip.Addr{netip.MustParseAddr("192.0.2.10")}, false); err != nil {
		t.Fatal(err)
	}
	if err := validateIngressAddresses([]netip.Addr{netip.MustParseAddr("192.0.2.10"), netip.MustParseAddr("127.0.0.1")}, false); err == nil {
		t.Fatal("mixed DNS answer allowed")
	}
	if err := validateIngressAddresses(nil, false); err == nil {
		t.Fatal("empty DNS answer allowed")
	}
	if err := validateIngressAddresses([]netip.Addr{netip.MustParseAddr("127.0.0.1")}, true); err != nil {
		t.Fatal(err)
	}
}

func TestIngressDialsValidatedNumericAddress(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	addresses := []netip.Addr{netip.MustParseAddr("127.0.0.1")}
	if conn, err := dialResolvedIngress(ctx, &net.Dialer{}, "tcp", port, addresses, false); err == nil {
		if conn != nil {
			_ = conn.Close()
		}
		t.Fatal("default dial reached loopback")
	}
	conn, err := dialResolvedIngress(ctx, &net.Dialer{}, "tcp", port, addresses, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if conn.RemoteAddr().String() != ln.Addr().String() {
		t.Fatalf("not pinned to checked address: %s", conn.RemoteAddr())
	}
	peer, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	_ = peer.Close()
}

func TestIngressLocalRequiresOptIn(t *testing.T) {
	g, _, _, route, _ := instanceFixture(t, http.NotFoundHandler())
	g.Ingress.allowLocal = false
	for _, raw := range []string{"unix:///var/run/example.sock", "npipe:////./pipe/example"} {
		route.Ingress = raw
		if _, _, err := g.Ingress.target(route); err == nil {
			t.Fatalf("default allowed %s", raw)
		}
	}
	if defaultConfig().AllowLocalIngress {
		t.Fatal("local enabled by default")
	}
	t.Setenv("AURIGA_EDGE_ALLOW_LOCAL_INGRESS", "true")
	t.Setenv("AURIGA_EDGE_ROUTES_NATS_CREDS_FILE", "routes.creds")
	cfg := defaultConfig()
	applyEnv(&cfg)
	if !cfg.AllowLocalIngress || cfg.RoutesNATSCredsFile != "routes.creds" {
		t.Fatal("missing config overrides")
	}
}

func TestIngressDualEKUCertificateAcceptedAndClientOnlyRejected(t *testing.T) {
	cfg := defaultConfig()
	node := testNode(t, &cfg, http.NotFoundHandler(), testNodeID)
	ingress, err := loadNodeIngress(cfg.NodeTLS)
	if err != nil {
		t.Fatal(err)
	}
	defer ingress.Close()
	_, transport, err := ingress.target(&InstanceRoute{InstanceID: testInstanceID, NodeID: testNodeID, Ingress: "https://node.example.com:7443"})
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(node.TLS.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(leaf.ExtKeyUsage) != 2 {
		t.Fatalf("test cert differs from NodeCa EKU: %v", leaf.ExtKeyUsage)
	}
	verify := transport.(*http.Transport).TLSClientConfig.VerifyConnection
	if err := verify(tls.ConnectionState{PeerCertificates: []*x509.Certificate{leaf}}); err != nil {
		t.Fatal(err)
	}
	clientPEM, err := tls.LoadX509KeyPair(cfg.NodeTLS.CertFile, cfg.NodeTLS.KeyFile)
	if err != nil {
		t.Fatal(err)
	}
	clientLeaf, err := x509.ParseCertificate(clientPEM.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	var invalid x509.CertificateInvalidError
	if err := verify(tls.ConnectionState{PeerCertificates: []*x509.Certificate{clientLeaf}}); !errors.As(err, &invalid) || invalid.Reason != x509.IncompatibleUsage {
		t.Fatalf("client-only certificate was not rejected for EKU: %v", err)
	}
}

type failingRoutes struct {
	err   error
	calls int
}

func (s *failingRoutes) Get(context.Context, string) (*InstanceRoute, error) {
	s.calls++
	return nil, s.err
}

func TestInstanceAuthenticatesBeforeRouteLookupAndHidesExistence(t *testing.T) {
	g, _, _, _, cookie := instanceFixture(t, http.NotFoundHandler())
	store := &failingRoutes{err: ErrRouteNotFound}
	g.Routes = store
	w := httptest.NewRecorder()
	g.Handler().ServeHTTP(w, instanceRequest("", "/"))
	if w.Code != 401 || store.calls != 0 {
		t.Fatalf("anonymous probed routes: %d calls=%d", w.Code, store.calls)
	}
	w = httptest.NewRecorder()
	g.Handler().ServeHTTP(w, instanceRequest(cookie, "/"))
	if w.Code != 403 || !strings.Contains(w.Body.String(), "edge.not_owner") {
		t.Fatalf("missing route leaked: %d %s", w.Code, w.Body.String())
	}
	store.err = ErrRouteUnavailable
	w = httptest.NewRecorder()
	g.Handler().ServeHTTP(w, instanceRequest(cookie, "/"))
	if w.Code != 503 || !strings.Contains(w.Body.String(), "common.unavailable") {
		t.Fatalf("outage: %d %s", w.Code, w.Body.String())
	}
	// /open must also distinguish dependency outages from authorization failures.
	g.Owners = RouteOwnerChecker{Routes: store}
	r := httptest.NewRequest(http.MethodGet, "http://portal.example.com/open/"+testInstanceID, nil)
	r.AddCookie(&http.Cookie{Name: "auriga_session", Value: "session"})
	w = httptest.NewRecorder()
	g.Handler().ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatalf("open outage: %d %s", w.Code, w.Body.String())
	}
}

func TestNATSRoutesDistinguishMissingFromUnavailable(t *testing.T) {
	raw, stop := startTestNATS(t, "routes", "test-pass")
	defer stop()
	store, err := NewNATSRouteStore(raw, "")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if _, err := store.Get(ctx, testInstanceID); !errors.Is(err, ErrRouteUnavailable) {
		t.Fatalf("missing bucket: %v", err)
	}
	nc, err := nats.Connect(raw)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	kv, err := js.CreateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: "auriga-routes"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, testInstanceID); !errors.Is(err, ErrRouteNotFound) {
		t.Fatalf("missing key: %v", err)
	}
	route := InstanceRoute{InstanceID: testInstanceID, NodeID: testNodeID, TenantID: testNodeID, OwnerID: testNodeID, Ingress: "https://node.example.com:7443"}
	value, err := json.Marshal(route)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kv.Put(ctx, testInstanceID, value); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, testInstanceID); err != nil {
		t.Fatal(err)
	}
	if err := kv.Delete(ctx, testInstanceID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, testInstanceID); !errors.Is(err, ErrRouteNotFound) {
		t.Fatalf("deleted key: %v", err)
	}
	store.Close()
	if _, err := store.Get(ctx, testInstanceID); !errors.Is(err, ErrRouteUnavailable) {
		t.Fatalf("closed connection: %v", err)
	}
}
