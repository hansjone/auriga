package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestIAMRPCIgnoresEnvironmentProxy(t *testing.T) {
	var connects atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		connects.Add(1)
		w.WriteHeader(502)
	}))
	defer proxy.Close()
	for _, key := range []string{"HTTPS_PROXY", "https_proxy"} {
		t.Setenv(key, proxy.URL)
	}
	for _, key := range []string{"NO_PROXY", "no_proxy"} {
		t.Setenv(key, "")
	}
	pki := createAccessPKI(t)
	token, _ := serviceJWT(t)
	file := filepath.Join(t.TempDir(), "service.jwt")
	if err := os.WriteFile(file, []byte(token), 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := newIAMRPC(IAMRPCConfig{Address: "192.0.2.91:43123", ServerName: "iam.internal.test", ServiceTokenFile: file, TimeoutMillis: 200}, pki.hop)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	if err := client.Verify(context.Background(), "cookie", "csrf", &Session{TenantID: "tenant"}, time.Now()); !errors.Is(err, errIAMUnavailable) {
		t.Fatalf("unreachable fixed RPC did not fail closed: %v", err)
	}
	if connects.Load() != 0 {
		t.Fatalf("fixed RPC used environment proxy: %d CONNECT requests", connects.Load())
	}
}

func TestAccessConsumedCSRFCookieIsUnambiguous(t *testing.T) {
	for _, entry := range []struct {
		domain bool
		role   string
	}{{false, "portal"}, {true, "portal"}, {true, "admin"}} {
		t.Run(fmt.Sprintf("domain=%t/%s", entry.domain, entry.role), func(t *testing.T) {
			var calls atomic.Int32
			g, addresses, sessions := accessFixture(t, entry.domain, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(204)
			}))
			session := putPortalSession(sessions, "session", "correct", "jwt")
			session.TenantID, session.Audience = g.Cfg.Access.TenantID(), entry.role
			sessions.Put("session", session)
			origin, authority := g.Cfg.Access.PortalURL(), g.Cfg.PortalHost
			if entry.role == "admin" {
				origin, authority = g.Cfg.Access.AdminURL(), g.Cfg.AdminHost
			}
			for _, path := range []string{"/api/org/v1/members", "/api/workspace/v1/instances"} {
				headers := http.Header{"Origin": {origin}, "Cookie": {sessionCookieBase(entry.role) + "=session; auriga_csrf=correct; auriga_csrf=conflicting"}, "X-Auriga-Csrf": {"correct"}}
				requireEntryProblem(t, addresses[entry.role], "POST", path, authority, headers, 400, "common.validation_failed")
				if calls.Load() != 0 {
					t.Fatal("ambiguous cookie reached upstream")
				}
				headers.Set("Cookie", sessionCookieBase(entry.role)+"=session; auriga_csrf=correct")
				headers["X-Auriga-Csrf"] = []string{"correct", "conflicting"}
				requireEntryProblem(t, addresses[entry.role], "POST", path, authority, headers, 403, "common.forbidden")
				if calls.Load() != 0 {
					t.Fatal("ambiguous header reached upstream")
				}
			}
			// Duplicate unconsumed cookies from the other audience are discarded.
			headers := http.Header{"Origin": {origin}, "Cookie": {sessionCookieBase(entry.role) + "=session; auriga_csrf=correct; auriga_portal_login_tx=a; auriga_portal_login_tx=b"}, "X-Auriga-Csrf": {"correct"}}
			status, _ := entryRequest(t, addresses[entry.role], "POST", "/api/org/v1/members", authority, headers)
			if status != 204 || calls.Load() != 1 {
				t.Fatalf("unused cookie affected ordinary write: status=%d calls=%d", status, calls.Load())
			}
		})
	}
}

func TestAccessDomainOpenPreservesExternalPortThroughRedemption(t *testing.T) {
	g, addresses, sessions := accessFixture(t, true, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	session := putPortalSession(sessions, "portal", "csrf", "jwt")
	session.TenantID = g.Cfg.Access.TenantID()
	sessions.Put("portal", session)
	instanceID := "019a0000-0000-7000-8000-000000000099"
	g.Routes.(*MemoryRouteStore).Put(&InstanceRoute{InstanceID: instanceID, TenantID: session.TenantID, OwnerID: session.AccountID, State: "running", Generation: 1, UpdatedAt: time.Now()})
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", addresses["portal"])
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequest("GET", g.Cfg.Access.PortalURL()+"/open/"+instanceID, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: "auriga_session", Value: "portal"})
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 302 {
		t.Fatalf("open status=%d", resp.StatusCode)
	}
	location, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if location.Scheme != "http" || location.Hostname() != instanceID+".i.example.com" || location.Port() != fmt.Sprint(g.Cfg.Access.PortalOrigin().Port) {
		t.Fatal("ticket Location differs from complete installation origin")
	}
	// Keep the generated authority unchanged; only route TCP to the test socket.
	redeem, err := http.NewRequest("GET", location.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, cookie := range resp.Cookies() {
		redeem.AddCookie(cookie)
	}
	rr, err := client.Do(redeem)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rr.Body.Close() }()
	if rr.StatusCode != 302 || rr.Header.Get("Location") != "/" {
		t.Fatalf("generated URL redemption failed: status=%d", rr.StatusCode)
	}
	var instanceCookie string
	for _, cookie := range rr.Cookies() {
		if cookie.Name == instCookieBase() {
			instanceCookie = cookie.Value
		}
	}
	binding, err := g.Tickets.LookupInstance(instanceCookie)
	if err != nil || binding.InstanceID != instanceID || binding.SessionID != session.SessionID {
		t.Fatal("successful redemption did not bind the expected session and instance")
	}
}
