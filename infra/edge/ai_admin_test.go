package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func closeAIAdminTransports(t *testing.T, g *Gateway) {
	t.Helper()
	for _, tr := range g.APITransports {
		if closer, ok := tr.(interface{ CloseIdleConnections() }); ok {
			t.Cleanup(closer.CloseIdleConnections)
		}
	}
	if closer, ok := g.IAMTransport.(interface{ CloseIdleConnections() }); ok {
		t.Cleanup(closer.CloseIdleConnections)
	}
}

func TestAIAdminConfiguration(t *testing.T) {
	pki := createAccessPKI(t)
	other := createAccessPKI(t)
	badCA := filepath.Join(t.TempDir(), "bad-ca.pem")
	if err := os.WriteFile(badCA, []byte("invalid CA bundle"), 0o600); err != nil {
		t.Fatal(err)
	}
	access, _, _ := accessFixture(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for _, test := range []struct {
		name, raw string
		change    func(*Config)
	}{
		{"legacy", "https://ai.example.com", func(c *Config) { c.Access = Config{}.Access }},
		{"missing-hop", "https://ai.example.com", func(c *Config) { c.AIAdminHop = IAMHopConfig{} }},
		{"orphan-hop", "", nil},
		{"userinfo", "https://user@ai.example.com", nil},
		{"query", "https://ai.example.com?x=1", nil},
		{"empty-query", "https://ai.example.com?", nil},
		{"fragment", "https://ai.example.com#x", nil},
		{"empty-fragment", "https://ai.example.com#", nil},
		{"path", "https://ai.example.com/api", nil},
		{"escaped-root", "https://ai.example.com/%2f", nil},
		{"bad-port", "https://ai.example.com:0", nil},
		{"http-default", "http://127.0.0.1:12345", nil},
		{"http-localhost", "http://localhost:12345", func(c *Config) { c.AIAdminHop.AllowLoopbackHTTP = true }},
		{"http-private", "http://192.168.1.1:12345", func(c *Config) { c.AIAdminHop.AllowLoopbackHTTP = true }}, // gitleaks:allow
		{"missing-cert", "https://ai.example.com", func(c *Config) { c.AIAdminHop.CertFile = "" }},
		{"missing-key", "https://ai.example.com", func(c *Config) { c.AIAdminHop.KeyFile = "" }},
		{"missing-ca", "https://ai.example.com", func(c *Config) { c.AIAdminHop.CAFile = "" }},
		{"missing-name", "https://ai.example.com", func(c *Config) { c.AIAdminHop.ServerName = "" }},
		{"ip-name", "https://ai.example.com", func(c *Config) { c.AIAdminHop.ServerName = "127.0.0.1" }},
		{"bad-name", "https://ai.example.com", func(c *Config) { c.AIAdminHop.ServerName = "ai.example.com:443" }},
		{"missing-file", "https://ai.example.com", func(c *Config) { c.AIAdminHop.CAFile = "missing-ca.pem" }},
		{"invalid-ca", "https://ai.example.com", func(c *Config) { c.AIAdminHop.CAFile = badCA }},
		{"mismatched-key", "https://ai.example.com", func(c *Config) { c.AIAdminHop.KeyFile = other.hop.KeyFile }},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := access.Cfg
			cfg.Upstreams = map[string]string{"iam": cfg.Upstreams["iam"], "org": cfg.Upstreams["org"], "workspace": cfg.Upstreams["workspace"], "ai-admin": test.raw}
			cfg.AIAdminHop = pki.hop
			if test.change != nil {
				test.change(&cfg)
			}
			if _, err := NewGateway(cfg, access.Sessions, access.Routes, access.Tickets, access.Signer, nil); err == nil {
				t.Fatal("invalid AI admin configuration accepted")
			}
		})
	}
	for _, raw := range []string{"https://127.0.0.1:12345", "http://127.0.0.1:12345", "http://[::1]:12345"} {
		cfg := access.Cfg
		cfg.Upstreams = map[string]string{"iam": cfg.Upstreams["iam"], "org": cfg.Upstreams["org"], "workspace": cfg.Upstreams["workspace"], "ai-admin": raw}
		cfg.AIAdminHop = pki.hop
		cfg.AIAdminHop.AllowLoopbackHTTP = true
		g, err := NewGateway(cfg, access.Sessions, access.Routes, access.Tickets, access.Signer, nil)
		if err != nil {
			t.Fatal(err)
		}
		closeAIAdminTransports(t, g)
	}
}

// IP requests use their actual authority without a Host override or hosts edits.
func aiAdminRequest(t *testing.T, g *Gateway, addresses map[string]string, role, method, path string, headers http.Header) (int, http.Header, string) {
	t.Helper()
	request, err := http.NewRequest(method, "http://"+addresses[role]+path, strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	if g.Cfg.Access.Mode() == "domain" {
		request.Host = g.Cfg.PortalHost
		if role == "admin" {
			request.Host = g.Cfg.AdminHost
		}
	}
	if role == "instanceControl" {
		request.Host = originAuthority(g.Cfg.Access.InstanceControl().Origin)
	}
	request.Header = headers.Clone()
	client := &http.Client{Transport: &http.Transport{}, Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, response.Header, string(body)
}

func TestAIAdminAccessMatrix(t *testing.T) {
	for _, domain := range []bool{true, false} {
		t.Run(fmt.Sprintf("domain=%v", domain), func(t *testing.T) {
			pki := createAccessPKI(t)
			seen := make(chan *http.Request, 32)
			var calls atomic.Int32
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.TLS == nil || r.TLS.ServerName != pki.hop.ServerName || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) != 1 || r.TLS.PeerCertificates[0].DNSNames[0] != "edge.internal.test" {
					t.Error("unverified TLS server name or edge client")
				}
				seen <- r.Clone(context.Background())
				w.Header().Set("ETag", `"v2"`)
				if r.URL.Query().Get("stepup") == "required" {
					writeProblem(w, problem(403, "common.forbidden", "Forbidden", "stepup required", "test"))
					return
				}
				w.WriteHeader(204)
			}))
			server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pki.server}, ClientCAs: pki.roots, ClientAuth: tls.RequireAndVerifyClientCert}
			server.StartTLS()
			t.Cleanup(server.Close)
			g, addresses, store := accessFixtureConfigured(t, domain, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("AI request reached IAM/org/workspace")
				w.WriteHeader(500)
			}), func(c *Config, _ map[string]any) {
				c.Upstreams["ai-admin"] = server.URL
				c.AIAdminHop = pki.hop
				// Same origin must not reuse IAM's separately configured name.
				c.Upstreams["iam"] = server.URL
				c.IAMHop = pki.hop
				c.IAMHop.AllowLoopbackHTTP = true
				c.IAMHop.ServerName = "wrong-iam.example.com"
			})
			closeAIAdminTransports(t, g)
			if g.APITransports[g.aiAdminURL] == nil || g.APITransports[g.aiAdminURL] == g.IAMTransport {
				t.Fatal("AI admin transport was not independently constructed")
			}
			backend, _ := browserRPCFixture(t, g, store)
			putBrowserSession(g, backend, "admin", "admin").AccessToken = "admin-token"
			store.Put("admin", backend.rows["admin"])
			putBrowserSession(g, backend, "portal", "portal")
			g.AdminCSRF = aiAdminCSRF{}
			good := http.Header{}
			good.Set("Cookie", "auriga_admin_session=admin; auriga_session=portal; auriga_csrf=csrf; unrelated=private")
			good.Set("Origin", g.Cfg.Access.AdminURL())
			good.Set("X-Auriga-CSRF", "csrf")
			good.Set("Authorization", "Bearer forged")
			good.Set("If-Match", `"v1"`)
			good.Set("Idempotency-Key", "create-provider")
			good.Set("If-None-Match", `"v0"`)
			good.Set("X-Auriga-Entry-Role", "portal")
			good.Add("X-Auriga-Entry-Role", "admin")
			good.Set("Auriga-Identity", "forged")
			good.Set("Forwarded", "host=forged")
			for _, path := range []string{"/api/ai/v1/admin", "/api/ai/v1/admin/", "/api/ai/v1/admin/providers"} {
				status, headers, body := aiAdminRequest(t, g, addresses, "admin", "POST", path, good)
				if status != 204 || headers.Get("ETag") != `"v2"` {
					t.Fatalf("success %d %s", status, body)
				}
				r := <-seen
				if r.URL.Path != path || r.Header.Get("Authorization") != "Bearer admin-token" || r.Header.Get("If-Match") != `"v1"` || r.Header.Get("Idempotency-Key") != "create-provider" || r.Header.Get("If-None-Match") != `"v0"` || r.Header.Get("Cookie") != "" || r.Header.Get("Auriga-Identity") != "" || r.Header.Get("X-Auriga-Entry-Role") != "" || r.Header.Get("Forwarded") != "" {
					t.Fatalf("bad forwarded request: %s %v", r.URL.Path, r.Header)
				}
			}
			status, headers, body := aiAdminRequest(t, g, addresses, "admin", "PATCH", "/api/ai/v1/admin/providers?stepup=required", good)
			if status != 403 || headers.Get("Content-Type") != "application/problem+json" || !strings.Contains(body, "stepup required") {
				t.Fatalf("problem lost: %d %v %s", status, headers, body)
			}
			<-seen
			for _, test := range []struct {
				name, role, path string
				status           int
				code             string
				change           func(http.Header)
			}{
				{"portal", "portal", "/api/ai/v1/admin/providers", 403, "common.forbidden", nil},
				{"instance-control", "instanceControl", "/api/ai/v1/admin/providers", 404, "common.not_found", nil},
				{"origin", "admin", "/api/ai/v1/admin/providers", 403, "common.forbidden", func(h http.Header) { h.Set("Origin", g.Cfg.Access.PortalURL()) }},
				{"csrf", "admin", "/api/ai/v1/admin/providers", 403, "common.forbidden", func(h http.Header) { h.Set("X-Auriga-CSRF", "bad") }},
				{"duplicate-csrf", "admin", "/api/ai/v1/admin/providers", 403, "common.forbidden", func(h http.Header) { h.Add("X-Auriga-CSRF", "csrf") }},
				{"duplicate-cookie", "admin", "/api/ai/v1/admin/providers", 400, "common.validation_failed", func(h http.Header) { h.Add("Cookie", "auriga_admin_session=admin") }},
				{"portal-cookie", "admin", "/api/ai/v1/admin/providers", 401, "common.unauthenticated", func(h http.Header) { h.Set("Cookie", "auriga_session=portal; auriga_csrf=csrf") }},
				{"bearer-only", "admin", "/api/ai/v1/admin/providers", 401, "common.unauthenticated", func(h http.Header) { h.Del("Cookie") }},
				{"no-credentials", "admin", "/api/ai/v1/admin/providers", 401, "common.unauthenticated", func(h http.Header) { h.Del("Cookie"); h.Del("Authorization") }},
				{"portal-session-as-admin", "admin", "/api/ai/v1/admin/providers", 401, "common.unauthenticated", func(h http.Header) { h.Set("Cookie", "auriga_admin_session=portal; auriga_csrf=csrf") }},
				{"forwarded-identity", "admin", "/api/ai/v1/admin/providers", 403, "common.forbidden", func(h http.Header) {
					h.Add("Auriga-Forwarded-Tenant", "forged")
					h.Add("Auriga-Forwarded-Tenant", "duplicate")
				}},
				{"traversal", "admin", "/api/ai/v1/admin/../providers", 400, "edge.bad_path", nil},
				{"separator", "admin", "/api/ai/v1/admin%2fproviders", 400, "edge.bad_path", nil},
				{"double-slash", "admin", "/api/ai/v1/admin//providers", 400, "edge.bad_path", nil},
				{"managed", "admin", "/api/ai/v1/internal/chat", 404, "common.not_found", nil},
				{"usage", "admin", "/api/ai/v1/me/usage", 404, "common.not_found", nil},
				{"near-prefix", "admin", "/api/ai/v1/administrator/providers", 404, "common.not_found", nil},
				{"backslash", "admin", "/api/ai/v1/admin%5cproviders", 400, "edge.bad_path", nil},
				{"semicolon", "admin", "/api/ai/v1/admin;foo/providers", 400, "edge.bad_path", nil},
				{"device", "admin", "/api/ai/v1/device/models", 404, "common.not_found", nil},
				{"chat", "admin", "/ai/v1/chat/completions", 403, "common.forbidden", nil},
			} {
				t.Run(test.name, func(t *testing.T) {
					h := good.Clone()
					if test.change != nil {
						test.change(h)
					}
					before := calls.Load()
					status, _, body := aiAdminRequest(t, g, addresses, test.role, "POST", test.path, h)
					if status != test.status || !strings.Contains(body, `"code":"`+test.code+`"`) {
						t.Fatalf("got %d %s", status, body)
					}
					if calls.Load() != before {
						t.Fatal("rejected request reached AI upstream")
					}
				})
			}
			if backend.touches == 0 {
				t.Fatal("browser session was not checked online")
			}
			instancePath := "/i/019a0000-0000-7000-8000-000000000001/api/ai/v1/admin/providers"
			instanceHost := g.Cfg.PortalHost
			if domain {
				_, port, err := net.SplitHostPort(addresses["portal"])
				if err != nil {
					t.Fatal(err)
				}
				instanceHost = net.JoinHostPort("019a0000-0000-7000-8000-000000000001.i.example.com", port)
				instancePath = "/api/ai/v1/admin/providers"
			}
			beforeInstance := calls.Load()
			status, body = entryRequest(t, addresses["portal"], "GET", instancePath, instanceHost, good)
			if status != 401 || !strings.Contains(body, `"code":"edge.unauthorized"`) || calls.Load() != beforeInstance {
				t.Fatalf("instance entry reached AI upstream: %d %s", status, body)
			}
			backend.clock.Add(int64((6 * time.Minute) / time.Second))
			status, _, body = aiAdminRequest(t, g, addresses, "admin", "GET", "/api/ai/v1/admin/providers", good)
			if status != 204 {
				t.Fatalf("current browser JWT renewal failed: %d %s", status, body)
			}
			if r := <-seen; r.Header.Get("Authorization") != "Bearer renewed-1" {
				t.Fatal("stale browser JWT forwarded after online renewal")
			}
			backend.clock.Add(int64((6 * time.Minute) / time.Second))
			backend.mu.Lock()
			backend.mode = "unavailable"
			backend.mu.Unlock()
			before := calls.Load()
			status, _, body = aiAdminRequest(t, g, addresses, "admin", "GET", "/api/ai/v1/admin/providers", good)
			if status != 503 || !strings.Contains(body, `"code":"common.unavailable"`) || calls.Load() != before {
				t.Fatalf("unavailable session RPC reached upstream: %d %s", status, body)
			}
			backend.mu.Lock()
			backend.mode = ""
			backend.mu.Unlock()
			backend.clock.Add(-int64((6 * time.Minute) / time.Second))
			rpc := g.SessionRPC
			g.SessionRPC = nil
			status, _, body = aiAdminRequest(t, g, addresses, "admin", "GET", "/api/ai/v1/admin/providers", good)
			g.SessionRPC = rpc
			if status != 503 || !strings.Contains(body, `"code":"common.unavailable"`) || calls.Load() != before {
				t.Fatalf("missing session RPC used a fallback: %d %s", status, body)
			}
			// A missing explicit transport cannot use the default transport.
			transport := g.APITransports[g.aiAdminURL]
			delete(g.APITransports, g.aiAdminURL)
			status, _, body = aiAdminRequest(t, g, addresses, "admin", "GET", "/api/ai/v1/admin/providers", good)
			g.APITransports[g.aiAdminURL] = transport
			if status != 503 || !strings.Contains(body, `"code":"common.unavailable"`) || calls.Load() != before {
				t.Fatalf("missing transport used a fallback: %d %s", status, body)
			}
			store.Revoke(backend.rows["admin"].SessionID)
			status, _, body = aiAdminRequest(t, g, addresses, "admin", "GET", "/api/ai/v1/admin/providers", good)
			if status != 401 || !strings.Contains(body, `"code":"common.unauthenticated"`) || calls.Load() != before {
				t.Fatalf("revoked browser session reached upstream: %d %s", status, body)
			}
		})
	}
}

type aiAdminCSRF struct{}

func (aiAdminCSRF) Verify(_ context.Context, raw, token string, sess *Session, _ time.Time) error {
	if raw != "admin" || token != "csrf" || sess.Audience != "admin" {
		return errCSRF
	}
	return nil
}
