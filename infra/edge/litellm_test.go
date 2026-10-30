package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestLiteLLMEntryBoundary(t *testing.T) {
	for _, domain := range []bool{false, true} {
		t.Run(map[bool]string{false: "ip", true: "domain"}[domain], func(t *testing.T) {
			var calls atomic.Int32
			seen := make(chan *http.Request, 8)
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				seen <- r.Clone(context.Background())
				w.WriteHeader(204)
			}))
			defer proxy.Close()
			g, addresses, store := accessFixtureConfigured(t, domain, nil, func(c *Config, _ map[string]any) { c.LiteLLMOrigin = proxy.URL })
			backend, _ := browserRPCFixture(t, g, store)
			admin := putBrowserSession(g, backend, "admin", "admin")
			ordinary := putBrowserSession(g, backend, "ordinary", "admin")
			for _, row := range []*Session{admin, ordinary} {
				if row == ordinary {
					row.SessionID += "-ordinary"
				}
				permissions := []string{}
				if row == admin {
					permissions = []string{"ai.providers.write"}
				}
				claims, err := json.Marshal(map[string]any{"ctx": "admin", "tid": row.TenantID, "sub": row.AccountID, "perms": permissions})
				if err != nil {
					t.Fatal(err)
				}
				row.AccessToken = "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".fixture"
			}
			store.Put("admin", admin)
			store.Put("ordinary", ordinary)
			putBrowserSession(g, backend, "portal", "portal")
			for _, tc := range []struct {
				name, role, method, path string
				headers                  http.Header
				status                   int
			}{
				{"external", "portal", "GET", "/ai/v1/models", http.Header{"Authorization": {"Bearer sk-virtual"}}, 204},
				{"no-key", "portal", "GET", "/ai/v1/models", http.Header{}, 401},
				{"ui-anonymous", "admin", "GET", "/ai/ui/", http.Header{}, 401},
				{"ui-bearer", "admin", "GET", "/ai/ui/", http.Header{"Authorization": {"Bearer sk-virtual"}}, 401},
				{"ui-portal", "portal", "GET", "/ai/ui/", http.Header{"Cookie": {"auriga_admin_session=admin"}}, 403},
				{"ui-member", "admin", "GET", "/ai/ui/", http.Header{"Cookie": {"auriga_admin_session=portal"}}, 401},
				{"ui-nonadministrator", "admin", "GET", "/ai/ui/", http.Header{"Cookie": {"auriga_admin_session=ordinary"}, "Authorization": {"Bearer sk-forged"}}, 403},
				{"ui-admin", "admin", "GET", "/ai/ui/", http.Header{"Cookie": {"auriga_admin_session=admin; unrelated=litellm"}}, 204},
				{"management-origin", "admin", "POST", "/ai/key/generate", http.Header{"Cookie": {"auriga_admin_session=admin"}, "Origin": {g.Cfg.Access.PortalURL()}}, 403},
				{"management", "admin", "POST", "/ai/key/generate", http.Header{"Cookie": {"auriga_admin_session=admin"}, "Origin": {g.Cfg.Access.AdminURL()}}, 204},
				{"ui-playground", "admin", "POST", "/ai/v1/chat/completions", http.Header{"Cookie": {"auriga_admin_session=admin"}, "Origin": {g.Cfg.Access.AdminURL()}, "Authorization": {"Bearer ui-session-token"}}, 204},
				{"ui-api-alias", "admin", "GET", "/ai/v1/model/info", http.Header{"Cookie": {"auriga_admin_session=admin"}}, 204},
				{"api-prefix", "portal", "POST", "/ai/v1/key/generate", http.Header{"Authorization": {"Bearer sk-virtual"}}, 404},
				{"root-management", "portal", "POST", "/key/generate", http.Header{"Authorization": {"Bearer sk-virtual"}}, 405},
				{"root-api-management", "admin", "POST", "/v1/key/generate", http.Header{"Authorization": {"Bearer sk-virtual"}}, 405},
			} {
				t.Run(tc.name, func(t *testing.T) {
					before := calls.Load()
					status, _, body := aiAdminRequest(t, g, addresses, tc.role, tc.method, tc.path, tc.headers)
					if status != tc.status {
						t.Fatalf("%d %s", status, body)
					}
					if tc.status == 204 {
						r := <-seen
						if strings.Contains(r.Header.Get("Cookie"), "auriga_") || r.Header.Get("Auriga-Identity") != "" || r.URL.Path != tc.path {
							t.Fatalf("bad boundary %s %v", r.URL.Path, r.Header)
						}
					} else if calls.Load() != before {
						t.Fatal("rejected request reached LiteLLM")
					}
				})
			}
			store.Revoke(backend.rows["admin"].SessionID)
			status, _, _ := aiAdminRequest(t, g, addresses, "admin", "GET", "/ai/ui/", http.Header{"Cookie": {"auriga_admin_session=admin"}})
			if status != 401 {
				t.Fatalf("revoked admin = %d", status)
			}
		})
	}
}
