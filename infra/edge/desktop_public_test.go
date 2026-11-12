package main

import (
	"net/http"
	"testing"
)

func TestDesktopAnonymousRoutes(t *testing.T) {
	for _, domain := range []bool{false, true} {
		t.Run(map[bool]string{true: "domain", false: "ip"}[domain], func(t *testing.T) {
			g, addresses, _ := accessFixtureConfigured(t, domain, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Error("anonymous download forwarded credentials")
				}
				w.WriteHeader(200)
			}), func(c *Config, _ map[string]any) { c.Upstreams["resource"] = c.Upstreams["iam"] })
			closeAIAdminTransports(t, g)
			for _, path := range []string{"/api/resource/v1/desktop/latest", "/api/resource/v1/desktop/packages/2"} {
				status, _, body := aiAdminRequest(t, g, addresses, "portal", "GET", path, http.Header{"Authorization": {"Bearer forged"}})
				if status != 200 {
					t.Fatalf("%s: %d %s", path, status, body)
				}
			}
			requireEntryProblem(t, addresses["admin"], "GET", "/api/resource/v1/admin/desktop/releases", g.Cfg.AdminHost, nil, 401, "common.unauthenticated")
			for _, path := range []string{"/api/resource/v1/desktop/latest/extra", "/api/resource/v1/desktop/packages/2/extra", "/api/resource/v1/packages", "/api/resource/v1/desktop/packages/0"} {
				requireEntryProblem(t, addresses["portal"], "GET", path, g.Cfg.PortalHost, nil, 401, "common.unauthenticated")
			}
			requireEntryProblem(t, addresses["portal"], "POST", "/api/resource/v1/desktop/latest", g.Cfg.PortalHost, nil, 401, "common.unauthenticated")
			g.Limiter.mu.Lock()
			g.Limiter.rate, g.Limiter.burst, g.Limiter.prefix = 0.0001, 1, "/oauth2"
			g.Limiter.buckets = map[string]*bucket{}
			g.Limiter.mu.Unlock()
			status, _, _ := aiAdminRequest(t, g, addresses, "portal", "GET", "/api/resource/v1/desktop/latest", nil)
			if status != 200 {
				t.Fatal(status)
			}
			status, _, _ = aiAdminRequest(t, g, addresses, "portal", "GET", "/api/resource/v1/desktop/packages/2", nil)
			if status != 429 {
				t.Fatalf("anonymous download rate limit: %d", status)
			}
		})
	}
}
