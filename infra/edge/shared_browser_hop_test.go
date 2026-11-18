package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestSharedPlacementBrowserContextIsLimitedToItsExactWorkspaceEndpoints(t *testing.T) {
	seen := make(chan *http.Request, 4)
	g, addresses, sessions := accessFixture(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Clone(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	session := putPortalSession(sessions, "portal", "csrf", "portal-jwt")
	session.TenantID = g.Cfg.Access.TenantID()
	sessions.Put("portal", session)
	for _, sample := range []struct {
		method, path string
		browser      bool
	}{
		{http.MethodGet, "/api/workspace/v1/me/placement", true},
		{http.MethodPost, "/api/workspace/v1/me/placement/open", true},
		{http.MethodGet, "/api/workspace/v1/me", false},
		{http.MethodGet, "/api/workspace/v1/me/placement?other=parameter", false},
	} {
		headers := http.Header{
			"Cookie": {"auriga_session=portal; auriga_csrf=csrf; auriga_admin_session=admin; company=synthetic"},
			"Origin": {g.Cfg.Access.PortalURL()}, "X-Auriga-CSRF": {"csrf"},
			"X-Auriga-Entry-Role": {"admin"}, "X-Auriga-Entry-Origin": {"https://untrusted.invalid"},
		}
		status, _ := entryRequest(t, addresses["portal"], sample.method, sample.path, g.Cfg.PortalHost, headers)
		if status != http.StatusNoContent {
			t.Fatalf("%s %s returned %d", sample.method, sample.path, status)
		}
		request := <-seen
		if request.Header.Get("Authorization") != "Bearer portal-jwt" {
			t.Fatal("normal browser JWT boundary changed")
		}
		cookie := request.Header.Get("Cookie")
		if sample.browser {
			if !strings.Contains(cookie, "auriga_session=portal") ||
				strings.Contains(cookie, "auriga_admin_session") || strings.Contains(cookie, "company") ||
				request.Header.Get("X-Auriga-Entry-Origin") != g.Cfg.Access.PortalURL() || request.Header.Get("X-Auriga-Entry-Role") != "portal" {
				t.Fatal("placement request lost its real, sanitized browser context")
			}
			if sample.method == http.MethodPost && !strings.Contains(cookie, "auriga_csrf=csrf") {
				t.Fatal("placement mutation did not carry the normal CSRF binding")
			}
		} else if cookie != "" || request.Header.Get("X-Auriga-Entry-Origin") != "" || request.Header.Get("X-Auriga-Entry-Role") != "" {
			t.Fatal("browser context escaped the exact placement endpoint allowlist")
		}
	}
}
