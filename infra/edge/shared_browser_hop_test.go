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
		w.Header().Set("ETag", `"opaque-placement-tag"`)
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
			"If-Match": {`"opaque-placement-tag"`}, "Idempotency-Key": {"original-placement-intent"},
		}
		status, response, _ := aiAdminRequest(t, g, addresses, "portal", sample.method, sample.path, headers)
		if status != http.StatusNoContent {
			t.Fatalf("%s %s returned %d", sample.method, sample.path, status)
		}
		request := <-seen
		if response.Get("ETag") != `"opaque-placement-tag"` || request.Header.Get("If-Match") != `"opaque-placement-tag"` || request.Header.Get("Idempotency-Key") != "original-placement-intent" {
			t.Fatal("opaque version or original idempotency precondition changed in transit")
		}
		if sample.browser != strings.Contains(response.Get("Cache-Control"), "no-transform") {
			t.Fatal("exact placement responses must preserve their opaque write-precondition ETags through outer encoders")
		}
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

func TestPlacementMigrationWizardAdminBrowserHop(t *testing.T) {
	const memberID = "11111111-2222-3333-4444-555555555555"
	seen := make(chan *http.Request, 8)
	g, addresses, sessions := accessFixture(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Clone(r.Context())
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", `"opaque-wizard-tag"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	g.AdminCSRF = aiAdminCSRF{}
	adminSession := putPortalSession(sessions, "admin", "csrf", "admin-jwt")
	adminSession.Audience, adminSession.TenantID = "admin", g.Cfg.Access.TenantID()
	sessions.Put("admin", adminSession)

	for _, sample := range []struct {
		method, path, wantQuery string
		browser                 bool
	}{
		{
			http.MethodGet,
			"/api/workspace/v1/admin/placement-migration-candidates?q=%E5%BC%A0%E4%B8%89&limit=50&offset=0&memberId=" + memberID,
			"q=%E5%BC%A0%E4%B8%89&limit=50&offset=0&memberId=" + memberID,
			true,
		},
		{
			http.MethodGet,
			"/api/workspace/v1/admin/placement-rebuild-plans",
			"",
			true,
		},
		{
			http.MethodPost,
			"/api/workspace/v1/admin/placement-rebuild-plans/preflight",
			"",
			true,
		},
		{
			http.MethodGet,
			"/api/workspace/v1/admin/placement-migration-candidates?unknown=1",
			"unknown=1",
			false,
		},
		{
			http.MethodGet,
			"/api/workspace/v1/admin/placement-migration-candidates?limit=10&limit=20",
			"limit=10&limit=20",
			false,
		},
		{
			http.MethodGet,
			"/api/workspace/v1/admin/placement-rebuild-plans?limit=10",
			"limit=10",
			false,
		},
		{
			http.MethodPost,
			"/api/workspace/v1/admin/placement-rebuild-plans/preflight?x=1",
			"x=1",
			false,
		},
	} {
		t.Run(sample.method+" "+sample.path, func(t *testing.T) {
			headers := http.Header{
				"Cookie":                {"auriga_admin_session=admin; auriga_csrf=csrf; auriga_session=portal; company=synthetic"},
				"Origin":                {g.Cfg.Access.AdminURL()},
				"X-Auriga-CSRF":         {"csrf"},
				"X-Auriga-Entry-Role":   {"portal"},
				"X-Auriga-Entry-Origin": {"https://untrusted.invalid"},
			}
			status, response, _ := aiAdminRequest(t, g, addresses, "admin", sample.method, sample.path, headers)
			if status != http.StatusOK {
				t.Fatalf("%s %s returned %d", sample.method, sample.path, status)
			}
			request := <-seen
			if request.URL.RawQuery != sample.wantQuery {
				t.Fatalf("upstream query=%q, want %q", request.URL.RawQuery, sample.wantQuery)
			}
			if request.Header.Get("Authorization") != "Bearer admin-jwt" {
				t.Fatal("normal admin JWT boundary changed")
			}
			if sample.browser != strings.Contains(response.Get("Cache-Control"), "no-transform") {
				t.Fatal("placement wizard response Cache-Control no-transform mismatch")
			}
			cookie := request.Header.Get("Cookie")
			if sample.browser {
				if !strings.Contains(cookie, "auriga_admin_session=admin") ||
					strings.Contains(cookie, "auriga_session=portal") ||
					strings.Contains(cookie, "company=synthetic") ||
					request.Header.Get("X-Auriga-Entry-Origin") != g.Cfg.Access.AdminURL() ||
					request.Header.Get("X-Auriga-Entry-Role") != "admin" {
					t.Fatalf("admin placement wizard lost sanitized browser context: cookie=%q origin=%q role=%q",
						cookie, request.Header.Get("X-Auriga-Entry-Origin"), request.Header.Get("X-Auriga-Entry-Role"))
				}
				if sample.method == http.MethodPost && request.Header.Get("X-Auriga-CSRF") != "csrf" {
					t.Fatalf("preflight POST missing X-Auriga-CSRF header: %q", request.Header.Get("X-Auriga-CSRF"))
				}
			} else if cookie != "" || request.Header.Get("X-Auriga-Entry-Origin") != "" || request.Header.Get("X-Auriga-Entry-Role") != "" {
				t.Fatalf("disallowed route/query leaked browser context: cookie=%q origin=%q role=%q",
					cookie, request.Header.Get("X-Auriga-Entry-Origin"), request.Header.Get("X-Auriga-Entry-Role"))
			}
		})
	}
}
