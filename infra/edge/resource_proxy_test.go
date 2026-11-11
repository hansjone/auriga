package main

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestResourceMetadataRoutes(t *testing.T) {
	for _, domain := range []bool{false, true} {
		t.Run(map[bool]string{false: "ip", true: "domain"}[domain], func(t *testing.T) {
			var calls atomic.Int32
			g, addresses, store := accessFixtureConfigured(t, domain, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if !strings.HasPrefix(r.URL.Path, "/api/resource/v1/") || r.Header.Get("Authorization") != "Bearer admin-token" || r.Header.Get("Cookie") != "" || r.Header.Get("Auriga-Identity") != "" {
					t.Error("resource authentication lost")
				}
				if r.Method == "PATCH" && r.Header.Get("If-Match") != `"v1"` || r.Method == "POST" && r.Header.Get("Idempotency-Key") != "resource-create" {
					t.Error("conditional header lost")
				}
				w.Header().Set("ETag", `"v2"`)
				w.WriteHeader(200)
			}), func(c *Config, _ map[string]any) { c.Upstreams["resource"] = c.Upstreams["iam"] })
			closeAIAdminTransports(t, g)
			backend, _ := browserRPCFixture(t, g, store)
			putBrowserSession(g, backend, "admin", "admin").AccessToken = "admin-token"
			store.Put("admin", backend.rows["admin"])
			g.AdminCSRF = aiAdminCSRF{}
			good := http.Header{"Cookie": {"auriga_admin_session=admin; auriga_csrf=csrf"}, "Origin": {g.Cfg.Access.AdminURL()}, "X-Auriga-CSRF": {"csrf"}, "If-Match": {`"v1"`}, "Idempotency-Key": {"resource-create"}, "Authorization": {"Bearer forged"}, "Auriga-Identity": {"forged"}}
			for _, op := range []struct{ method, path string }{{"GET", "/api/resource/v1/packages?limit=50"}, {"POST", "/api/resource/v1/packages"}, {"PATCH", "/api/resource/v1/packages/example"}} {
				status, headers, body := aiAdminRequest(t, g, addresses, "admin", op.method, op.path, good)
				if status != 200 || headers.Get("ETag") != `"v2"` {
					t.Fatalf("%d %s", status, body)
				}
			}
			bad := good.Clone()
			bad.Set("X-Auriga-CSRF", "wrong")
			requireEntryProblem(t, addresses["admin"], "POST", "/api/resource/v1/packages", g.Cfg.AdminHost, bad, 403, "common.forbidden")
			requireEntryProblem(t, addresses["portal"], "PATCH", "/api/resource/v1/admin/packages/example", g.Cfg.PortalHost, nil, 403, "common.forbidden")
			requireEntryProblem(t, addresses["admin"], "GET", "/api/resource/v1/packages", g.Cfg.AdminHost, nil, 401, "common.unauthenticated")
			store.Revoke(backend.rows["admin"].SessionID)
			requireEntryProblem(t, addresses["admin"], "GET", "/api/resource/v1/packages", g.Cfg.AdminHost, good, 401, "common.unauthenticated")
			if calls.Load() != 3 {
				t.Fatal("rejected request reached resource")
			}
		})
	}
}

func TestResourceUnconfigured(t *testing.T) {
	g, addresses, _ := accessFixture(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected platform request") }))
	requireEntryProblem(t, addresses["admin"], "GET", "/api/resource/v1/packages", g.Cfg.AdminHost, nil, 503, "common.unavailable")
}

func TestResourceMemberDeviceBearerThroughPortal(t *testing.T) {
	for _, domain := range []bool{false, true} {
		g, addresses, _ := accessFixtureConfigured(t, domain, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer device-member-token" || r.Header.Get("Cookie") != "" || r.URL.RawQuery != "" {
				t.Error("member identity or ticket URL boundary regressed")
			}
			w.WriteHeader(http.StatusCreated)
		}), func(c *Config, _ map[string]any) { c.Upstreams["resource"] = c.Upstreams["iam"] })
		closeAIAdminTransports(t, g)
		for _, path := range []string{"/api/resource/v1/member/download-tickets", "/api/resource/v1/member/downloads", "/api/resource/v1/member/installations", "/api/resource/v1/member/installation-reports"} {
			status, _, body := aiAdminRequest(t, g, addresses, "portal", "POST", path, http.Header{"Authorization": {"Bearer device-member-token"}})
			if status != http.StatusCreated {
				t.Fatalf("member route rejected: %d %s", status, body)
			}
		}
	}
}

func TestResourceDeviceConfiguration(t *testing.T) {
	g, _, _ := accessFixture(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	for _, key := range []string{"resource", "ai-device"} {
		for _, raw := range []string{"http://example.com", "http://localhost:8080", "https://user@example.com", "https://example.com/path", "https://example.com?", "https://example.com#", "https://example.com:0", "https://example.com:65536"} {
			cfg := g.Cfg
			cfg.Upstreams = map[string]string{"iam": g.Cfg.Upstreams["iam"], "org": g.Cfg.Upstreams["org"], "workspace": g.Cfg.Upstreams["workspace"], key: raw}
			if _, err := NewGateway(cfg, g.Sessions, g.Routes, g.Tickets, g.Signer, nil); err == nil {
				t.Errorf("accepted %s origin %s", key, raw)
			}
		}
	}
	t.Setenv("AURIGA_EDGE_UPSTREAM_RESOURCE", "http://127.0.0.1:12345")
	t.Setenv("AURIGA_EDGE_UPSTREAM_AI_DEVICE", "http://127.0.0.1:12346")
	cfg := defaultConfig()
	applyEnv(&cfg)
	if cfg.Upstreams["resource"] != "http://127.0.0.1:12345" || cfg.Upstreams["ai-device"] != "http://127.0.0.1:12346" {
		t.Fatal("missing environment wiring")
	}
	if _, err := cfg.parseResourceUpstream(); err == nil {
		t.Fatal("resource accepted missing Access")
	}
	if _, err := cfg.parseDeviceUpstream(); err == nil {
		t.Fatal("device accepted missing Access")
	}
}

func TestResourceUploadBodyLimitIsSpecificToArchiveRoute(t *testing.T) {
	var size int64
	g, addresses, store := accessFixtureConfigured(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, err := io.Copy(io.Discard, r.Body)
		size = n
		if err != nil {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}), func(c *Config, _ map[string]any) { c.Upstreams["resource"] = c.Upstreams["iam"] })
	closeAIAdminTransports(t, g)
	backend, _ := browserRPCFixture(t, g, store)
	putBrowserSession(g, backend, "admin", "admin").AccessToken = "admin-token"
	store.Put("admin", backend.rows["admin"])
	g.AdminCSRF = aiAdminCSRF{}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	for _, tc := range []struct {
		path   string
		length int
		ok     bool
	}{
		{"/api/resource/v1/packages/01900000-0000-7000-8000-000000000001/versions", 2 << 20, true},
		{"/api/resource/v1/packages/01900000-0000-7000-8000-000000000001/versions", (64 << 20) + 131072 + 1, false},
		{"/api/resource/v1/packages", 2 << 20, false},
	} {
		req, err := http.NewRequest("POST", "http://"+addresses["admin"]+tc.path, bytes.NewReader(make([]byte, tc.length)))
		if err != nil {
			t.Fatal(err)
		}
		req.Host = g.Cfg.AdminHost
		req.Header.Set("Cookie", "auriga_admin_session=admin; auriga_csrf=csrf")
		req.Header.Set("Origin", g.Cfg.Access.AdminURL())
		req.Header.Set("X-Auriga-CSRF", "csrf")
		req.Header.Set("Content-Type", "multipart/form-data; boundary=test")
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		if tc.ok && (response.StatusCode != 201 || size != int64(tc.length)) {
			t.Fatalf("upload rejected: status=%d bytes=%d", response.StatusCode, size)
		}
		if !tc.ok && response.StatusCode == 201 {
			t.Fatal("body limit bypassed")
		}
	}
}
