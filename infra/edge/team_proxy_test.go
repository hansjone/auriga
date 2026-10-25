package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestTeamRoutesPreserveDeviceAndStripBrowserIdentity(t *testing.T) {
	for _, domain := range []bool{false, true} {
		t.Run(map[bool]string{false: "ip", true: "domain"}[domain], func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Header.Get("Authorization") != "Bearer device-credential" || r.Header.Get("Cookie") != "" || r.Header.Get("Auriga-Identity") != "" || r.Header.Get("X-Auriga-Entry-Role") != "" || r.Header.Get("Auriga-Tenant-Id") != "" {
					t.Error("credential or identity rewrite")
				}
				if r.Header.Get("Auriga-Endpoint-Id") != "endpoint-ref" || r.Header.Get("Auriga-Binding-Id") != "binding-ref" || r.Header.Get("Mcp-Protocol-Version") != "2025-06-18" {
					t.Error("lost protocol or binding references")
				}
				w.WriteHeader(http.StatusAccepted)
			}))
			defer upstream.Close()
			g, addresses, _ := accessFixtureConfigured(t, domain, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("Team request reached IAM"); w.WriteHeader(500) }), func(cfg *Config, _ map[string]any) { cfg.Upstreams["ai-gateway"] = upstream.URL })
			headers := http.Header{"Authorization": {"Bearer device-credential"}, "Origin": {g.Cfg.Access.PortalURL()}, "Cookie": {"auriga_session=browser; other=private"}, "Auriga-Identity": {"forged"}, "X-Auriga-Entry-Role": {"admin"}, "Auriga-Tenant-Id": {"foreign"}, "Auriga-Endpoint-Id": {"endpoint-ref"}, "Auriga-Binding-Id": {"binding-ref"}, "Mcp-Protocol-Version": {"2025-06-18"}}
			for _, path := range []string{"/ai/mcp", "/ai/team/node/register", "/ai/team/node/bind", "/ai/team/node/pull", "/ai/team/node/ack", "/ai/team/node/revoke"} {
				status, body := entryRequest(t, addresses["portal"], "POST", path, g.Cfg.PortalHost, headers)
				if status != 202 {
					t.Fatalf("%s: %d %s", path, status, body)
				}
			}
			if calls.Load() != 6 {
				t.Fatalf("upstream calls=%d", calls.Load())
			}
			for _, tc := range []struct {
				method, path, role string
				headers            http.Header
				status             int
				code               string
			}{
				{"POST", "/ai/mcp", "portal", nil, 401, "common.unauthenticated"},
				{"POST", "/ai/mcp", "portal", http.Header{"Authorization": {"Bearer one", "Bearer two"}}, 401, "common.unauthenticated"},
				{"POST", "/ai/mcp", "portal", http.Header{"Authorization": {"Basic value"}}, 401, "common.unauthenticated"},
				{"POST", "/ai/mcp", "portal", http.Header{"Authorization": {"Bearer a b"}}, 401, "common.unauthenticated"},
				{"POST", "/ai/mcp", "portal", http.Header{"Authorization": {"Bearer device-credential"}, "Origin": {"https://attacker.example"}}, 403, "common.forbidden"},
				{"POST", "/ai/mcp", "admin", http.Header{"Authorization": {"Bearer device-credential"}}, 403, "common.forbidden"},
				{"GET", "/ai/team/node/pull", "portal", headers, 405, "common.validation_failed"},
				{"POST", "/ai/team/node/unknown", "portal", headers, 404, "common.not_found"},
				{"POST", "/ai/mcp/extra", "portal", headers, 404, "common.not_found"},
			} {
				authority := g.Cfg.PortalHost
				if tc.role == "admin" {
					authority = g.Cfg.AdminHost
				}
				requireEntryProblem(t, addresses[tc.role], tc.method, tc.path, authority, tc.headers, tc.status, tc.code)
			}
			if calls.Load() != 6 {
				t.Fatal("rejected request reached upstream")
			}
		})
	}
}

func TestTeamUpstreamFailureAndExactRevoke(t *testing.T) {
	for _, domain := range []bool{false, true} {
		t.Run(map[bool]string{false: "ip", true: "domain"}[domain], func(t *testing.T) {
			var revoke atomic.Int32
			g, addresses, _ := accessFixture(t, domain, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/oauth2/revoke" || r.Method != "POST" {
					t.Error("unexpected IAM operation")
				}
				if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "Bearer own-device" || r.Header.Get("X-Auriga-Entry-Role") != "portal" {
					t.Error("bad revoke authentication")
				}
				revoke.Add(1)
				w.WriteHeader(200)
			}))
			h := http.Header{"Authorization": {"Bearer own-device"}}
			status, body := entryRequest(t, addresses["portal"], "POST", "/oauth2/revoke", g.Cfg.PortalHost, h)
			if status != 200 {
				t.Fatalf("revoke=%d %s", status, body)
			}
			requireEntryProblem(t, addresses["admin"], "POST", "/oauth2/revoke", g.Cfg.AdminHost, h, 403, "common.forbidden")
			if protocolOperation(httptest.NewRequest("GET", "http://example.com/oauth2/revoke", nil)) || protocolOperation(httptest.NewRequest("POST", "http://example.com/oauth2/revoke/extra", nil)) {
				t.Fatal("revoke allowlist broadened")
			}
			if revoke.Load() != 1 {
				t.Fatal("unexpected revoke delivery")
			}
			requireEntryProblem(t, addresses["portal"], "POST", "/ai/mcp", g.Cfg.PortalHost, h, 503, "common.unavailable")
			dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403); _, _ = w.Write([]byte("rejected")) }))
			g.teamURL, _ = parseTeamUpstream(dead.URL)
			status, body = entryRequest(t, addresses["portal"], "POST", "/ai/mcp", g.Cfg.PortalHost, h)
			if status != 403 || strings.TrimSpace(body) != "rejected" {
				t.Fatalf("upstream rejection=%d %s", status, body)
			}
			dead.Close()
			requireEntryProblem(t, addresses["portal"], "POST", "/ai/mcp", g.Cfg.PortalHost, h, 503, "common.unavailable")
		})
	}
}

func TestTeamUpstreamIsFixedTrustedOrigin(t *testing.T) {
	for _, raw := range []string{"http://example.com", "http://localhost:8080", "https://user:pass@example.com", "https://example.com/path", "https://example.com?query", "https://example.com#fragment", "file:///tmp/x", "//example.com", "https://example.com/../"} {
		if _, err := parseTeamUpstream(raw); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	for _, raw := range []string{"http://127.0.0.1:1234", "http://[::1]:1234", "https://example.com"} {
		if _, err := parseTeamUpstream(raw); err != nil {
			t.Errorf("rejected %s: %v", raw, err)
		}
	}
}
