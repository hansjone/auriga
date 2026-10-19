package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Negative tests for post-merge B7 audit findings (auriga-B7-audit.md).

func TestAudit1_PathInstanceDisabledOnAdminAndWhenParentDomainSet(t *testing.T) {
	cfg, sessions, routes, tickets, signer, _ := testEnv(t)
	instID := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	routes.Put(&InstanceRoute{
		InstanceID: instID,
		TenantID:   "33333333-3333-3333-3333-333333333333",
		OwnerID:    "22222222-2222-2222-2222-222222222222",
		Ingress:    "http://127.0.0.1:9",
		State:      "running",
		UpdatedAt:  time.Now(),
	})
	gw := newTestGateway(t, cfg, sessions, routes, tickets, signer)

	// Parent domain configured → /i/ must 404 even on portal.
	req := httptest.NewRequest(http.MethodGet, "http://portal.example.com/i/"+instID+"/", nil)
	req.Host = "portal.example.com"
	req.RemoteAddr = "192.0.2.10:1"
	rr := httptest.NewRecorder()
	gw.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("portal /i/ with parent domain: %d", rr.Code)
	}

	// Admin host → /i/ must 404.
	req = httptest.NewRequest(http.MethodGet, "http://admin.example.com/i/"+instID+"/", nil)
	req.Host = "admin.example.com"
	req.RemoteAddr = "192.0.2.10:1"
	rr = httptest.NewRecorder()
	gw.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("admin /i/: %d", rr.Code)
	}
}

func TestAudit1_InstanceSetCookieStripped(t *testing.T) {
	cfg, sessions, routes, tickets, signer, _ := testEnv(t)
	cfg.SecureCookies = false
	instID := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	host := instID + ".i.example.com"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Set-Cookie", "__Host-auriga_admin_session=evil; Path=/; Secure; HttpOnly")
		w.Header().Add("Set-Cookie", "auriga_session=evil2; Path=/")
		w.Header().Add("Set-Cookie", "app_theme=dark; Path=/")
		w.WriteHeader(200)
	}))
	t.Cleanup(upstream.Close)
	routes.Put(&InstanceRoute{
		InstanceID: instID,
		TenantID:   "33333333-3333-3333-3333-333333333333",
		OwnerID:    "22222222-2222-2222-2222-222222222222",
		Host:       &host,
		Ingress:    upstream.URL,
		State:      "running",
		UpdatedAt:  time.Now(),
	})
	cookieRaw := "portal-sess"
	putPortalSession(sessions, cookieRaw, "csrf", "tok")
	binding, err := tickets.BindInstance(
		"11111111-1111-1111-1111-111111111111",
		"22222222-2222-2222-2222-222222222222",
		"33333333-3333-3333-3333-333333333333",
		instID,
	)
	if err != nil {
		t.Fatal(err)
	}
	gw := newTestGateway(t, cfg, sessions, routes, tickets, signer)
	req := httptest.NewRequest(http.MethodGet, "http://"+host+"/app", nil)
	req.Host = host
	req.RemoteAddr = "192.0.2.10:1"
	req.AddCookie(&http.Cookie{Name: "auriga_inst", Value: binding.CookieRaw})
	rr := httptest.NewRecorder()
	gw.Handler().ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status %d", rr.Code)
	}
	for _, c := range rr.Result().Cookies() {
		lname := strings.ToLower(c.Name)
		if strings.Contains(lname, "auriga_") || strings.HasPrefix(lname, "__host-") {
			t.Fatalf("dangerous Set-Cookie leaked: %s", c.Name)
		}
	}
	foundTheme := false
	for _, c := range rr.Result().Cookies() {
		if c.Name == "app_theme" {
			foundTheme = true
		}
	}
	if !foundTheme {
		t.Fatal("non-auriga Set-Cookie should pass through")
	}
}

func TestAudit2_PathTraversalRejected(t *testing.T) {
	cfg, sessions, routes, tickets, signer, _ := testEnv(t)
	gw := newTestGateway(t, cfg, sessions, routes, tickets, signer)

	cases := []string{
		"/api/iam/v1/x/../admin/members",
		"/oauth2/../api/iam/v1/admin/x",
		"/api/org/v1//admin/x",
		"/api/org/v1/./admin/x",
	}
	for _, p := range cases {
		req := httptest.NewRequest(http.MethodGet, "http://portal.example.com"+p, nil)
		req.Host = "portal.example.com"
		req.RemoteAddr = "192.0.2.10:1"
		rr := httptest.NewRecorder()
		gw.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("path %q got %d want 400", p, rr.Code)
		}
		var prob Problem
		_ = json.Unmarshal(rr.Body.Bytes(), &prob)
		if prob.Code != "edge.bad_path" {
			t.Fatalf("path %q code %q", p, prob.Code)
		}
	}

	// Encoded slash in RawPath / RequestURI (EscapedPath ignores mismatched RawPath)
	req := httptest.NewRequest(http.MethodGet, "http://portal.example.com/api/org/v1/groups", nil)
	req.Host = "portal.example.com"
	req.RemoteAddr = "192.0.2.10:1"
	req.URL.Path = "/api/org/v1/admin/x"
	req.URL.RawPath = "/api/org/v1%2fadmin/x"
	req.RequestURI = "/api/org/v1%2fadmin/x"
	rr := httptest.NewRecorder()
	gw.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("encoded slash got %d", rr.Code)
	}
}

func TestAudit3_PublicWriteNoSessionBearer(t *testing.T) {
	cfg, sessions, routes, tickets, signer, _ := testEnv(t)
	var gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(200)
	}))
	t.Cleanup(upstream.Close)
	cfg.Upstreams["iam"] = upstream.URL
	gw := newTestGateway(t, cfg, sessions, routes, tickets, signer)

	cookieRaw := "portal-session-cookie-value"
	csrfRaw := "csrf-token-value"
	putPortalSession(sessions, cookieRaw, csrfRaw, "access-token-abc")

	req := httptest.NewRequest(http.MethodPost, "http://portal.example.com/oauth2/revoke", nil)
	req.Host = "portal.example.com"
	req.RemoteAddr = "192.0.2.10:1"
	req.AddCookie(&http.Cookie{Name: "__Host-auriga_session", Value: cookieRaw})
	req.AddCookie(&http.Cookie{Name: "__Host-auriga_csrf", Value: csrfRaw})
	rr := httptest.NewRecorder()
	gw.Handler().ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status %d", rr.Code)
	}
	if gotAuth != "" {
		t.Fatalf("public write must not inject session bearer, got %q", gotAuth)
	}

	// /userinfoXYZ must not be treated as public (exact /userinfo only)
	gotAuth = "unset"
	cfg2, sessions2, routes2, tickets2, signer2, _ := testEnv(t)
	cfg2.Upstreams["org"] = upstream.URL
	gw2 := newTestGateway(t, cfg2, sessions2, routes2, tickets2, signer2)
	req = httptest.NewRequest(http.MethodGet, "http://portal.example.com/userinfoXYZ", nil)
	req.Host = "portal.example.com"
	req.RemoteAddr = "192.0.2.10:1"
	rr = httptest.NewRecorder()
	gw2.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized && rr.Code != http.StatusNotFound {
		// Not an API upstream match either → static/404; must not reach org with bearer.
		if rr.Code == 200 && gotAuth == "Bearer access-token-abc" {
			t.Fatal("userinfoXYZ must not be public with bearer")
		}
	}
}

func TestAudit4_IgnoreClientIPHeadersFromTrustedProxy(t *testing.T) {
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
	req.RemoteAddr = "127.0.0.1:9999"
	req.Header.Set("X-Auriga-Client-IP", "203.0.113.9")
	req.Header.Set("X-Real-IP", "203.0.113.8")
	req.Header.Set("X-Forwarded-For", "203.0.113.1, 198.51.100.7, 127.0.0.1")
	gw.Handler().ServeHTTP(httptest.NewRecorder(), req)
	if seen != "198.51.100.7" {
		t.Fatalf("got %q want rightmost untrusted XFF", seen)
	}
}

func TestAudit5_TicketRedeemWithoutPortalNavRejected(t *testing.T) {
	cfg, sessions, routes, tickets, signer, _ := testEnv(t)
	cfg.SecureCookies = false
	instID := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	host := instID + ".i.example.com"
	routes.Put(&InstanceRoute{
		InstanceID: instID,
		TenantID:   "33333333-3333-3333-3333-333333333333",
		OwnerID:    "22222222-2222-2222-2222-222222222222",
		Host:       &host,
		Ingress:    "http://127.0.0.1:9",
		State:      "running",
		UpdatedAt:  time.Now(),
	})
	putPortalSession(sessions, "portal-sess", "csrf", "tok")
	gw := newTestGateway(t, cfg, sessions, routes, tickets, signer)

	req := httptest.NewRequest(http.MethodGet, "http://portal.example.com/open/"+instID, nil)
	req.Host = "portal.example.com"
	req.RemoteAddr = "192.0.2.10:1"
	req.AddCookie(&http.Cookie{Name: "auriga_session", Value: "portal-sess"})
	rr := httptest.NewRecorder()
	gw.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusFound {
		t.Fatalf("open %d", rr.Code)
	}
	loc, _ := url.Parse(rr.Header().Get("Location"))
	ticket := loc.Query().Get("t")

	req = httptest.NewRequest(http.MethodGet, "http://"+host+"/_auriga/ticket?t="+url.QueryEscape(ticket), nil)
	req.Host = host
	req.RemoteAddr = "192.0.2.10:1"
	// No Referer / Sec-Fetch-Site — login CSRF vector
	rr = httptest.NewRecorder()
	gw.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("redeem without nav: %d", rr.Code)
	}
	var prob Problem
	_ = json.Unmarshal(rr.Body.Bytes(), &prob)
	if prob.Code != "edge.bad_ticket_nav" {
		t.Fatalf("%+v", prob)
	}
}

func TestAudit6_TicketAndBindingSweep(t *testing.T) {
	store := NewMemoryTicketStore()
	fixed := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return fixed }

	tok, err := store.Issue("s", "a", "t", "i", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return fixed.Add(2 * time.Second) }
	if _, err := store.Redeem(tok.Token); err == nil {
		t.Fatal("expired ticket should fail")
	}
	store.mu.Lock()
	if _, ok := store.tickets[tok.Token]; ok {
		t.Fatal("expired ticket should be removed")
	}
	store.mu.Unlock()

	store.now = func() time.Time { return fixed }
	b, err := store.BindInstance("s", "a", "t", "i")
	if err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return fixed.Add(defaultBindingTTL + time.Second) }
	if _, err := store.LookupInstance(b.CookieRaw); err == nil {
		t.Fatal("expired binding should fail")
	}
}

func TestAudit8_UpgradeBadOriginRejected(t *testing.T) {
	cfg, sessions, routes, tickets, signer, _ := testEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	t.Cleanup(upstream.Close)
	cfg.Upstreams["org"] = upstream.URL
	cookieRaw := "portal-session-cookie-value"
	csrfRaw := "csrf-token-value"
	putPortalSession(sessions, cookieRaw, csrfRaw, "access-token-abc")
	gw := newTestGateway(t, cfg, sessions, routes, tickets, signer)

	req := httptest.NewRequest(http.MethodGet, "http://portal.example.com/api/org/v1/groups", nil)
	req.Host = "portal.example.com"
	req.RemoteAddr = "192.0.2.10:1"
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Origin", "https://evil.example.net")
	req.AddCookie(&http.Cookie{Name: "__Host-auriga_session", Value: cookieRaw})
	rr := httptest.NewRecorder()
	gw.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("got %d", rr.Code)
	}
}

func TestAudit9_ConfigRejectsDangerousValues(t *testing.T) {
	cfg, _, _, _, _, _ := testEnv(t)
	cfg.RateLimit.RequestsPerSecond = 0
	if err := cfg.validate(); err == nil {
		t.Fatal("rps=0 should fail")
	}
	cfg, _, _, _, _, _ = testEnv(t)
	cfg.TrustedProxies = []string{"0.0.0.0/0"}
	if err := cfg.validate(); err == nil {
		t.Fatal("0.0.0.0/0 should fail")
	}
	cfg, _, _, _, _, _ = testEnv(t)
	cfg.InstanceParentDomain = "example.com"
	if err := cfg.validate(); err == nil {
		t.Fatal("parent capturing portal host should fail")
	}
}

func TestAudit9_RequestIDAndTraceparentSanitized(t *testing.T) {
	cfg, sessions, routes, tickets, signer, _ := testEnv(t)
	var seenID, seenTP string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenID = r.Header.Get("X-Request-Id")
		seenTP = r.Header.Get("traceparent")
		w.WriteHeader(200)
	}))
	t.Cleanup(upstream.Close)
	cfg.Upstreams["iam"] = upstream.URL
	cfg.PublicPathPrefixes = []string{"/api/iam/v1/login/"}
	gw := newTestGateway(t, cfg, sessions, routes, tickets, signer)

	req := httptest.NewRequest(http.MethodGet, "http://portal.example.com/api/iam/v1/login/providers", nil)
	req.Host = "portal.example.com"
	req.RemoteAddr = "192.0.2.10:1"
	req.Header.Set("X-Request-Id", "bad id with spaces\nand\nnewlines")
	req.Header.Set("traceparent", "not-a-valid-traceparent")
	gw.Handler().ServeHTTP(httptest.NewRecorder(), req)
	if seenID == "" || strings.Contains(seenID, " ") || strings.Contains(seenID, "\n") {
		t.Fatalf("request id not sanitized: %q", seenID)
	}
	if !validTraceparent(seenTP) {
		t.Fatalf("traceparent not regenerated: %q", seenTP)
	}
}
