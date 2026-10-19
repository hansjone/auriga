package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVersionFlag(t *testing.T) {
	var out, logs bytes.Buffer
	if err := run([]string{"--version"}, &out, &logs); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "auriga-edge "+version) {
		t.Fatal(out.String())
	}
	if logs.Len() != 0 {
		t.Fatal("version must not initialize services")
	}
}

func TestMissingConfigRejected(t *testing.T) {
	var out, logs bytes.Buffer
	if err := run(nil, &out, &logs); err == nil {
		t.Fatal("expected config error")
	}
}

func testEnv(t *testing.T) (Config, *MemorySessionStore, *MemoryRouteStore, *MemoryTicketStore, *FileIdentitySigner, ed25519.PublicKey) {
	t.Helper()
	dir := t.TempDir()
	keys := filepath.Join(dir, "identity-signing-keys.json")
	pub, _, err := WriteTestIdentityKeys(keys, "test-kid-1")
	if err != nil {
		t.Fatal(err)
	}
	signer, err := LoadIdentitySigner(keys)
	if err != nil {
		t.Fatal(err)
	}
	portalRoot := filepath.Join(dir, "portal")
	adminRoot := filepath.Join(dir, "admin")
	_ = os.MkdirAll(portalRoot, 0o755)
	_ = os.MkdirAll(adminRoot, 0o755)
	_ = os.WriteFile(filepath.Join(portalRoot, "index.html"), []byte("portal"), 0o644)
	_ = os.WriteFile(filepath.Join(adminRoot, "index.html"), []byte("admin"), 0o644)

	cfg := defaultConfig()
	cfg.Listen = "127.0.0.1:0"
	cfg.PortalHost = "portal.example.com"
	cfg.AdminHost = "admin.example.com"
	cfg.InstanceParentDomain = "i.example.com"
	cfg.SecureCookies = true
	cfg.TrustedProxies = []string{"127.0.0.1", "::1"}
	cfg.IdentityKeysFile = keys
	cfg.Static.PortalRoot = portalRoot
	cfg.Static.AdminRoot = adminRoot
	cfg.RateLimit = RateLimitConfig{RequestsPerSecond: 1000, Burst: 1000}
	cfg.Upstreams = map[string]string{
		"iam":       "http://127.0.0.1:9",
		"org":       "http://127.0.0.1:9",
		"workspace": "http://127.0.0.1:9",
	}
	return cfg, NewMemorySessionStore(), NewMemoryRouteStore(), NewMemoryTicketStore(), signer, pub
}

func newTestGateway(t *testing.T, cfg Config, sessions *MemorySessionStore, routes *MemoryRouteStore, tickets *MemoryTicketStore, signer *FileIdentitySigner) *Gateway {
	t.Helper()
	gw, err := NewGateway(cfg, sessions, routes, tickets, signer, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return gw
}

func putPortalSession(store *MemorySessionStore, cookieRaw, csrfRaw, accessToken string) *Session {
	sum := sha256.Sum256([]byte(csrfRaw))
	s := &Session{
		SessionID:            "11111111-1111-1111-1111-111111111111",
		AccountID:            "22222222-2222-2222-2222-222222222222",
		TenantID:             "33333333-3333-3333-3333-333333333333",
		Audience:             "portal",
		AccessToken:          accessToken,
		AccessTokenExpiresAt: time.Now().Add(5 * time.Minute),
		ExpiresAt:            time.Now().Add(time.Hour),
		IdleExpiresAt:        time.Now().Add(time.Hour),
		MFA:                  true,
		AuthTime:             time.Now().Add(-time.Minute),
		CSRFHash:             hex.EncodeToString(sum[:]),
	}
	store.Put(cookieRaw, s)
	return s
}

func TestClientIPTrustedAndForged(t *testing.T) {
	cfg, sessions, routes, tickets, signer, _ := testEnv(t)
	var seen string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("X-Auriga-Client-IP")
		w.WriteHeader(200)
	}))
	t.Cleanup(upstream.Close)
	cfg.Upstreams["iam"] = upstream.URL
	cfg.PublicPathPrefixes = []string{"/api/iam/v1/login/"}
	gw := newTestGateway(t, cfg, sessions, routes, tickets, signer)

	req := httptest.NewRequest(http.MethodGet, "http://portal.example.com/api/iam/v1/login/providers", nil)
	req.Host = "portal.example.com"
	req.RemoteAddr = "192.0.2.50:1234"
	req.Header.Set("X-Auriga-Client-IP", "203.0.113.9")
	gw.Handler().ServeHTTP(httptest.NewRecorder(), req)
	if seen != "192.0.2.50" {
		t.Fatalf("untrusted forged IP: got %q", seen)
	}

	req = httptest.NewRequest(http.MethodGet, "http://portal.example.com/api/iam/v1/login/providers", nil)
	req.Host = "portal.example.com"
	req.RemoteAddr = "127.0.0.1:9999"
	req.Header.Set("X-Forwarded-For", "198.51.100.7, 127.0.0.1")
	gw.Handler().ServeHTTP(httptest.NewRecorder(), req)
	if seen != "198.51.100.7" {
		t.Fatalf("trusted xff: got %q", seen)
	}
}

func TestAdminHostIsolation(t *testing.T) {
	cfg, sessions, routes, tickets, signer, _ := testEnv(t)
	gw := newTestGateway(t, cfg, sessions, routes, tickets, signer)
	req := httptest.NewRequest(http.MethodGet, "http://portal.example.com/api/org/v1/admin/members", nil)
	req.Host = "portal.example.com"
	req.RemoteAddr = "192.0.2.10:1"
	rr := httptest.NewRecorder()
	gw.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("got %d", rr.Code)
	}
	var p Problem
	_ = json.Unmarshal(rr.Body.Bytes(), &p)
	if p.Code != "edge.admin_host_required" {
		t.Fatalf("%+v", p)
	}
}

func TestCSRFFailureAndBearerInjection(t *testing.T) {
	cfg, sessions, routes, tickets, signer, _ := testEnv(t)
	var gotAuth, gotCookie string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCookie = r.Header.Get("Cookie")
		w.WriteHeader(200)
	}))
	t.Cleanup(upstream.Close)
	cfg.Upstreams["org"] = upstream.URL
	gw := newTestGateway(t, cfg, sessions, routes, tickets, signer)

	cookieRaw := "portal-session-cookie-value"
	csrfRaw := "csrf-token-value"
	putPortalSession(sessions, cookieRaw, csrfRaw, "access-token-abc")

	req := httptest.NewRequest(http.MethodPost, "http://portal.example.com/api/org/v1/groups", nil)
	req.Host = "portal.example.com"
	req.RemoteAddr = "192.0.2.10:1"
	req.AddCookie(&http.Cookie{Name: "__Host-auriga_session", Value: cookieRaw})
	req.AddCookie(&http.Cookie{Name: "__Host-auriga_csrf", Value: csrfRaw})
	req.Header.Set("X-Auriga-CSRF", "wrong")
	rr := httptest.NewRecorder()
	gw.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("csrf status %d", rr.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "http://portal.example.com/api/org/v1/groups", nil)
	req.Host = "portal.example.com"
	req.RemoteAddr = "192.0.2.10:1"
	req.AddCookie(&http.Cookie{Name: "__Host-auriga_session", Value: cookieRaw})
	req.AddCookie(&http.Cookie{Name: "__Host-auriga_csrf", Value: csrfRaw})
	req.AddCookie(&http.Cookie{Name: "other", Value: "keep"})
	req.Header.Set("X-Auriga-CSRF", csrfRaw)
	rr = httptest.NewRecorder()
	gw.Handler().ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status %d %s", rr.Code, rr.Body.String())
	}
	if gotAuth != "Bearer access-token-abc" {
		t.Fatalf("auth %q", gotAuth)
	}
	if strings.Contains(gotCookie, "auriga_") || strings.Contains(gotCookie, "__Host-auriga") {
		t.Fatalf("cookie not stripped: %q", gotCookie)
	}
	if !strings.Contains(gotCookie, "other=keep") {
		t.Fatalf("non-auriga cookie lost: %q", gotCookie)
	}
}

func TestMissingSession(t *testing.T) {
	cfg, sessions, routes, tickets, signer, _ := testEnv(t)
	gw := newTestGateway(t, cfg, sessions, routes, tickets, signer)
	req := httptest.NewRequest(http.MethodGet, "http://portal.example.com/api/org/v1/groups", nil)
	req.Host = "portal.example.com"
	req.RemoteAddr = "192.0.2.10:1"
	rr := httptest.NewRecorder()
	gw.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("got %d", rr.Code)
	}
}

func TestRateLimit(t *testing.T) {
	cfg, sessions, routes, tickets, signer, _ := testEnv(t)
	// Tiny refill so burst=1 is exhausted immediately on the second call.
	cfg.RateLimit = RateLimitConfig{RequestsPerSecond: 0.0001, Burst: 1}
	cfg.PublicPathPrefixes = []string{"/api/iam/v1/login/"}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	t.Cleanup(upstream.Close)
	cfg.Upstreams["iam"] = upstream.URL
	gw := newTestGateway(t, cfg, sessions, routes, tickets, signer)

	mk := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "http://portal.example.com/api/iam/v1/login/providers", nil)
		r.Host = "portal.example.com"
		r.RemoteAddr = "192.0.2.88:1"
		return r
	}
	rr := httptest.NewRecorder()
	gw.Handler().ServeHTTP(rr, mk())
	if rr.Code != 200 {
		t.Fatalf("first %d", rr.Code)
	}
	rr = httptest.NewRecorder()
	gw.Handler().ServeHTTP(rr, mk())
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("second %d", rr.Code)
	}
}

func TestIdentitySignAndTicketOnce(t *testing.T) {
	cfg, sessions, routes, tickets, signer, _ := testEnv(t)
	cfg.SecureCookies = false

	var gotIdentity, gotAuth, gotCookie string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotIdentity = r.Header.Get("Auriga-Identity")
		gotAuth = r.Header.Get("Authorization")
		gotCookie = r.Header.Get("Cookie")
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(upstream.Close)

	instID := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	host := instID + ".i.example.com"
	routes.Put(&InstanceRoute{
		InstanceID: instID,
		TenantID:   "33333333-3333-3333-3333-333333333333",
		OwnerID:    "22222222-2222-2222-2222-222222222222",
		NodeID:     "44444444-4444-4444-4444-444444444444",
		Host:       &host,
		Ingress:    upstream.URL,
		State:      "running",
		Generation: 1,
		UpdatedAt:  time.Now(),
	})
	cookieRaw := "portal-sess"
	csrfRaw := "csrf"
	putPortalSession(sessions, cookieRaw, csrfRaw, "tok")

	gw := newTestGateway(t, cfg, sessions, routes, tickets, signer)

	req := httptest.NewRequest(http.MethodGet, "http://portal.example.com/open/"+instID, nil)
	req.Host = "portal.example.com"
	req.RemoteAddr = "192.0.2.10:1"
	req.AddCookie(&http.Cookie{Name: "auriga_session", Value: cookieRaw})
	rr := httptest.NewRecorder()
	gw.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusFound {
		t.Fatalf("open %d %s", rr.Code, rr.Body.String())
	}
	loc := rr.Header().Get("Location")
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatal(err)
	}
	ticket := u.Query().Get("t")
	if ticket == "" {
		t.Fatalf("location %s", loc)
	}

	nonce := ""
	for _, c := range rr.Result().Cookies() {
		if c.Name == "auriga_open_nonce" || c.Name == "__Host-auriga_open_nonce" {
			nonce = c.Value
		}
	}
	if nonce == "" {
		t.Fatal("missing open nonce cookie")
	}

	req = httptest.NewRequest(http.MethodGet, "http://"+host+"/_auriga/ticket?t="+url.QueryEscape(ticket), nil)
	req.Host = host
	req.RemoteAddr = "192.0.2.10:1"
	req.AddCookie(&http.Cookie{Name: "auriga_open_nonce", Value: nonce})
	rr = httptest.NewRecorder()
	gw.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusFound {
		t.Fatalf("redeem %d %s", rr.Code, rr.Body.String())
	}
	instCookie := ""
	for _, c := range rr.Result().Cookies() {
		if c.Name == "auriga_inst" {
			instCookie = c.Value
		}
	}
	if instCookie == "" {
		t.Fatal("missing inst cookie")
	}

	req = httptest.NewRequest(http.MethodGet, "http://"+host+"/_auriga/ticket?t="+url.QueryEscape(ticket), nil)
	req.Host = host
	req.RemoteAddr = "192.0.2.10:1"
	req.AddCookie(&http.Cookie{Name: "auriga_open_nonce", Value: nonce})
	rr = httptest.NewRecorder()
	gw.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("reuse %d", rr.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "http://"+host+"/app", nil)
	req.Host = host
	req.RemoteAddr = "192.0.2.10:1"
	req.AddCookie(&http.Cookie{Name: "auriga_inst", Value: instCookie})
	req.Header.Set("Auriga-Identity", "forged")
	req.Header.Set("Authorization", "Bearer forged")
	req.AddCookie(&http.Cookie{Name: "auriga_session", Value: "should-strip"})
	rr = httptest.NewRecorder()
	gw.Handler().ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("instance %d %s", rr.Code, rr.Body.String())
	}
	if gotAuth != "" {
		t.Fatalf("auth should be stripped, got %q", gotAuth)
	}
	if strings.Contains(gotCookie, "auriga_") {
		t.Fatalf("auriga cookies leaked: %q", gotCookie)
	}
	claims, hdr, err := VerifyIdentityToken(gotIdentity, signer.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	if hdr.Kid != "test-kid-1" || claims.Iss != "auriga-edge" || claims.Aud != instID || claims.Htm != "GET" || claims.Htu != "/app" || claims.Pv != 1 {
		t.Fatalf("claims=%+v hdr=%+v", claims, hdr)
	}
	if claims.Exp > claims.Iat+60 {
		t.Fatalf("exp too long")
	}
}

func TestResolveClientIPUnit(t *testing.T) {
	tp, err := NewTrustedProxies([]string{"127.0.0.1", "198.51.100.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "192.0.2.1:9"
	r.Header.Set("X-Auriga-Client-IP", "203.0.113.1")
	if got := ResolveClientIP(r, tp); got != "192.0.2.1" {
		t.Fatal(got)
	}
	r.RemoteAddr = "127.0.0.1:9"
	r.Header.Set("X-Auriga-Client-IP", "203.0.113.9")
	r.Header.Set("X-Real-IP", "203.0.113.8")
	r.Header.Set("X-Forwarded-For", "203.0.113.1, 192.0.2.50, 127.0.0.1")
	if got := ResolveClientIP(r, tp); got != "192.0.2.50" {
		t.Fatalf("expected rightmost untrusted XFF, got %q", got)
	}
}

func TestConfigExampleValidates(t *testing.T) {
	dir := t.TempDir()
	keys := filepath.Join(dir, "keys.json")
	if _, _, err := WriteTestIdentityKeys(keys, "k1"); err != nil {
		t.Fatal(err)
	}
	cfg := defaultConfig()
	cfg.PortalHost = "portal.example.com"
	cfg.AdminHost = "admin.example.com"
	cfg.IdentityKeysFile = keys
	cfg.Upstreams = map[string]string{
		"iam":       "http://192.0.2.10:8081",
		"org":       "http://192.0.2.10:8082",
		"workspace": "http://192.0.2.10:8083",
	}
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
}
