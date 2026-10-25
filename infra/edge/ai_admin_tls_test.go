package main

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAIAdminTLSFailureClosed(t *testing.T) {
	for _, domain := range []bool{true, false} {
		for _, failure := range []string{"wrong-ca", "wrong-server-name", "wrong-client-ca", "missing-client-cert"} {
			t.Run(fmt.Sprintf("domain=%v/%s", domain, failure), func(t *testing.T) {
				pki, other := createAccessPKI(t), createAccessPKI(t)
				var calls atomic.Int32
				server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					w.WriteHeader(204)
				}))
				server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pki.server}, ClientCAs: pki.roots, ClientAuth: tls.RequireAndVerifyClientCert}
				server.StartTLS()
				t.Cleanup(server.Close)
				g, addresses, store := accessFixtureConfigured(t, domain, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("wrong upstream"); w.WriteHeader(500) }), func(c *Config, _ map[string]any) {
					c.Upstreams["ai-admin"] = server.URL
					c.AIAdminHop = pki.hop
					switch failure {
					case "wrong-ca":
						c.AIAdminHop.CAFile = other.hop.CAFile
					case "wrong-server-name":
						c.AIAdminHop.ServerName = "wrong.example.com"
					case "wrong-client-ca":
						c.AIAdminHop.CertFile, c.AIAdminHop.KeyFile = other.hop.CertFile, other.hop.KeyFile
					}
				})
				closeAIAdminTransports(t, g)
				if failure == "missing-client-cert" {
					// Simulate lost transport credentials after startup: TLS still
					// must not send an HTTP request without its edge identity.
					g.APITransports[g.aiAdminURL].(*http.Transport).TLSClientConfig.Certificates = nil
				}
				backend, _ := browserRPCFixture(t, g, store)
				putBrowserSession(g, backend, "admin", "admin")
				status, _, body := aiAdminRequest(t, g, addresses, "admin", "GET", "/api/ai/v1/admin/providers", http.Header{"Cookie": {"auriga_admin_session=admin"}})
				if status != 503 || !strings.Contains(body, `"code":"common.unavailable"`) || calls.Load() != 0 {
					t.Fatalf("TLS failure did not close before HTTP: %d %s calls=%d", status, body, calls.Load())
				}
			})
		}
	}
}

func TestAIAdminUnconfiguredDoesNotServeSPA(t *testing.T) {
	for _, domain := range []bool{true, false} {
		t.Run(fmt.Sprintf("domain=%v", domain), func(t *testing.T) {
			var calls atomic.Int32
			g, addresses, _ := accessFixture(t, domain, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(204) }))
			closeAIAdminTransports(t, g)
			for _, path := range []string{"/api/ai", "/api/ai/", "/api/ai/v1/admin", "/api/ai/v1/admin/providers", "/api/ai/v1/me/usage"} {
				status, _, body := aiAdminRequest(t, g, addresses, "admin", "GET", path, nil)
				if status != 404 || !strings.Contains(body, `"code":"common.not_found"`) || calls.Load() != 0 {
					t.Fatalf("unconfigured API fell through: %d %s calls=%d", status, body, calls.Load())
				}
			}
		})
	}
}
