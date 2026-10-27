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
