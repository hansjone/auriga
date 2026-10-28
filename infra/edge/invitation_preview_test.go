package main

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAccessInvitationPreviewAnonymous(t *testing.T) {
	for _, domain := range []bool{false, true} {
		t.Run(map[bool]string{false: "ip", true: "domain"}[domain], func(t *testing.T) {
			seen := make(chan *http.Request, 8)
			g, addresses, sessions := accessFixture(t, domain, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen <- r.Clone(context.Background())
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"valid":true,"tenantName":"Example"}`))
			}))
			for _, role := range []string{"portal", "admin"} {
				row := putPortalSession(sessions, role, "csrf", "session-jwt")
				row.Audience, row.TenantID = role, g.Cfg.Access.TenantID()
				sessions.Put(role, row)
			}
			var reads atomic.Int32
			g.Sessions = afterGetSessionStore{SessionStore: sessions, afterGet: func() { reads.Add(1) }}
			for role, authority := range map[string]string{"portal": g.Cfg.PortalHost, "admin": g.Cfg.AdminHost} {
				for _, headers := range []http.Header{
					nil,
					{"Cookie": {"auriga_session=portal; auriga_admin_session=admin; auriga_csrf=csrf; auriga_portal_login_tx=tx"}, "Authorization": {"Bearer client-jwt"}, "X-Auriga-Tenant": {"forged"}, "Auriga-Identity": {"forged"}},
					{"Cookie": {"auriga_session=expired; auriga_admin_session=expired"}},
				} {
					status, body := entryRequest(t, addresses[role], "GET", "/api/org/v1/invitations/preview/invite-code?source=email", authority, headers)
					if status != 200 || !strings.Contains(body, `"valid":true`) {
						t.Fatalf("%s preview: %d %s", role, status, body)
					}
					r := <-seen
					for _, name := range []string{"Cookie", "Authorization", "X-Auriga-Tenant", "Auriga-Identity"} {
						if r.Header.Get(name) != "" {
							t.Fatalf("preview forwarded %s", name)
						}
					}
					if r.URL.Path != "/api/org/v1/invitations/preview/invite-code" || r.URL.RawQuery != "source=email" {
						t.Fatalf("preview target changed: %s", r.URL)
					}
				}
			}
			if reads.Load() != 0 {
				t.Fatalf("public preview loaded browser sessions %d times", reads.Load())
			}
		})
	}
}

func TestAccessInvitationPreviewRejectsOtherOperations(t *testing.T) {
	for _, domain := range []bool{false, true} {
		t.Run(map[bool]string{false: "ip", true: "domain"}[domain], func(t *testing.T) {
			var calls atomic.Int32
			g, addresses, _ := accessFixture(t, domain, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(204)
			}))
			for role, authority := range map[string]string{"portal": g.Cfg.PortalHost, "admin": g.Cfg.AdminHost} {
				for _, tc := range []struct{ method, path string }{
					{"POST", "/api/org/v1/invitations/preview/invite-code"},
					{"PUT", "/api/org/v1/invitations/preview/invite-code"},
					{"PATCH", "/api/org/v1/invitations/preview/invite-code"},
					{"OPTIONS", "/api/org/v1/invitations/preview/invite-code"},
					{"DELETE", "/api/org/v1/invitations/preview/invite-code"},
					{"GET", "/api/org/v1/invitations/preview/"},
					{"GET", "/api/org/v1/invitations/preview/invite-code/extra"},
					{"GET", "/api/org/v1/invitations/preview/invite-code/"},
					{"GET", "/api/org/v1/admin/invitations"},
					{"GET", "/api/org/v1/admin/members"},
					{"GET", "/api/org/v1/me"},
					{"GET", "/api/iam/v1/me"},
					{"GET", "/api/iam/v1/invitations/preview/invite-code"},
				} {
					status, code := 401, "common.unauthenticated"
					if role == "portal" && strings.Contains(tc.path, "/admin/") {
						status, code = 403, "common.forbidden"
					}
					requireEntryProblem(t, addresses[role], tc.method, tc.path, authority, nil, status, code)
					if calls.Load() != 0 {
						t.Fatalf("%s %s %s reached upstream", role, tc.method, tc.path)
					}
				}
				if status, _ := entryRequest(t, addresses[role], "HEAD", "/api/org/v1/invitations/preview/invite-code", authority, nil); status != 401 {
					t.Fatalf("HEAD preview: %d", status)
				}
			}
			if calls.Load() != 0 {
				t.Fatal("protected operation reached upstream")
			}
		})
	}
}

func TestAccessInvitationPreviewRejectsAmbiguousPaths(t *testing.T) {
	for _, domain := range []bool{false, true} {
		t.Run(map[bool]string{false: "ip", true: "domain"}[domain], func(t *testing.T) {
			var calls atomic.Int32
			g, addresses, _ := accessFixture(t, domain, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusNoContent)
			}))
			for role, authority := range map[string]string{"portal": g.Cfg.PortalHost, "admin": g.Cfg.AdminHost} {
				for _, tc := range []struct {
					path   string
					status int
					code   string
				}{
					{"/api/org/v1/invitations/preview/invite%2Fextra", 400, "edge.bad_path"},
					{"/api/org/v1/invitations/preview/invite%2fextra", 400, "edge.bad_path"},
					{"/api/org/v1/invitations/preview/invite%5Cextra", 400, "edge.bad_path"},
					{"/api/org/v1/invitations/preview/invite%5cextra", 400, "edge.bad_path"},
					{"/api/org/v1/invitations/preview/%2e", 400, "edge.bad_path"},
					{"/api/org/v1/invitations/preview/%2E%2e", 400, "edge.bad_path"},
					{"/api/org/v1/invitations/preview/.%2E/invite-code", 400, "edge.bad_path"},
					{"/api/org/v1/invitations/preview/%252F", 400, "edge.bad_path"},
					{"/api/org/v1/invitations/preview/%255c", 400, "edge.bad_path"},
					{"/api/org/v1/invitations/preview/%252e%252E", 400, "edge.bad_path"},
					{"/api/org/v1/Invitations/preview/invite-code", 401, "common.unauthenticated"},
					{"/api/org/v1/invitations/Preview/invite-code", 401, "common.unauthenticated"},
				} {
					t.Run(role+tc.path, func(t *testing.T) {
						requireEntryProblem(t, addresses[role], http.MethodGet, tc.path, authority, nil, tc.status, tc.code)
						if calls.Load() != 0 {
							t.Fatal("ambiguous preview path reached upstream")
						}
					})
				}
			}
		})
	}
}
