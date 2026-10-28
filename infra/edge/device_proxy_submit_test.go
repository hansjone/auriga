package main

import (
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func networkSubmitTestServer(t *testing.T, handler http.Handler) (*httptest.Server, IAMHopConfig) {
	t.Helper()
	pki := createAccessPKI(t)
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pki.server}, ClientCAs: pki.roots, ClientAuth: tls.RequireAndVerifyClientCert}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server, pki.hop
}

// These are transport probes, not authenticated SEND/PG acceptance tests.
func TestDeviceNetworkSubmitRoutes(t *testing.T) {
	for _, domain := range []bool{false, true} {
		t.Run(map[bool]string{false: "ip", true: "domain"}[domain], func(t *testing.T) {
			var calls atomic.Int32
			expectedBodies := make(chan string, 1)
			device, hop := networkSubmitTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodPost || r.URL.RequestURI() != "/api/ai/v1/network/submit" {
					t.Errorf("submit route changed: %s %s", r.Method, r.URL.RequestURI())
				}
				if r.Header.Get("Authorization") != "Bearer submit-device" || r.Header.Get("Content-Type") != "application/json" {
					t.Error("submit credential or content type changed")
				}
				for _, name := range []string{"Cookie", "Cookie2", "Auriga-Identity", "X-Auriga-Entry-Role", "X-Auriga-CSRF", "X-Auriga-CSRF-Bootstrap"} {
					if r.Header.Get(name) != "" {
						t.Errorf("untrusted header reached device: %s", name)
					}
				}
				body, err := io.ReadAll(r.Body)
				if expectedBody := <-expectedBodies; err != nil || string(body) != expectedBody {
					t.Errorf("submit body changed: bytes=%d error=%v", len(body), err)
				}
				w.Header().Set("Set-Cookie", "unexpected=value")
				w.WriteHeader(http.StatusAccepted)
				_, _ = io.WriteString(w, "device-runtime-response")
			}))
			g, addresses, _ := accessFixtureConfigured(t, domain, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("submit reached platform/Team/admin instead of device runtime")
				w.WriteHeader(http.StatusInternalServerError)
			}), func(c *Config, _ map[string]any) {
				c.Upstreams["network-submit"] = device.URL
				c.NetworkSubmitHop = hop
				c.Upstreams["ai-device"] = c.Upstreams["iam"]
				c.Upstreams["ai-gateway"] = c.Upstreams["iam"]
				c.Upstreams["ai-admin"] = c.Upstreams["iam"]
				c.AIAdminHop.AllowLoopbackHTTP = true
			})
			closeAIAdminTransports(t, g)
			client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 3 * time.Second}
			t.Cleanup(client.CloseIdleConnections)
			// SEND-1 preflights complete frames at 65536 bytes. Keep the existing
			// device proxy's larger 1 MiB request limit, including native fetch.
			for _, size := range []int{2, 65536, 65537, maxAPIBodyBytes} {
				expectedBody := `"` + strings.Repeat("x", size-2) + `"`
				expectedBodies <- expectedBody
				req, err := http.NewRequest(http.MethodPost, "http://"+addresses["portal"]+"/api/ai/v1/network/submit", strings.NewReader(expectedBody))
				if err != nil {
					t.Fatal(err)
				}
				req.Host = g.Cfg.PortalHost
				req.Header = http.Header{
					"Authorization": {"Bearer submit-device"}, "Content-Type": {"application/json"},
					"Auriga-Identity": {"forged"}, "X-Auriga-Entry-Role": {"admin"},
					"X-Auriga-CSRF": {"forged"}, "X-Auriga-CSRF-Bootstrap": {"1"},
				}
				if size != 2 {
					req.Header.Set("Sec-Fetch-Mode", "cors")
				}
				resp, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if err != nil || resp.StatusCode != http.StatusAccepted || string(body) != "device-runtime-response" || resp.Header.Get("Set-Cookie") != "" || resp.Header.Get("Cache-Control") != "no-store" {
					t.Fatalf("size=%d status=%d body=%s error=%v", size, resp.StatusCode, body, err)
				}
			}
			if calls.Load() != 4 {
				t.Fatalf("expected four device requests, got %d", calls.Load())
			}
		})
	}
}

func TestDeviceNetworkSubmitRejected(t *testing.T) {
	const path = "/api/ai/v1/network/submit"
	for _, domain := range []bool{false, true} {
		t.Run(map[bool]string{false: "ip", true: "domain"}[domain], func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusAccepted)
			}))
			t.Cleanup(upstream.Close)
			network, hop := networkSubmitTestServer(t, upstream.Config.Handler)
			g, addresses, _ := accessFixtureConfigured(t, domain, upstream.Config.Handler, func(c *Config, _ map[string]any) {
				c.Upstreams["ai-device"] = upstream.URL
				c.Upstreams["ai-gateway"] = upstream.URL
				c.Upstreams["ai-admin"] = upstream.URL
				c.Upstreams["network-submit"] = network.URL
				c.NetworkSubmitHop = hop
				c.AIAdminHop.AllowLoopbackHTTP = true
			})
			closeAIAdminTransports(t, g)
			good := http.Header{"Authorization": {"Bearer submit-device"}}
			for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions} {
				t.Run(method, func(t *testing.T) {
					status, headers, body := aiAdminRequest(t, g, addresses, "portal", method, path, good)
					if status != http.StatusMethodNotAllowed || headers.Get("Allow") != http.MethodPost || method != http.MethodHead && !strings.Contains(body, `"code":"common.validation_failed"`) {
						t.Fatalf("status=%d allow=%q body=%s", status, headers.Get("Allow"), body)
					}
					if calls.Load() != 0 {
						t.Fatal("wrong method reached upstream")
					}
				})
			}
			for _, tc := range []struct {
				name, path, role, code string
				status                 int
				headers                http.Header
			}{
				{"admin-entry", path, "admin", "common.forbidden", 403, good},
				{"missing-bearer", path, "portal", "common.unauthenticated", 401, nil},
				{"basic", path, "portal", "common.unauthenticated", 401, http.Header{"Authorization": {"Basic submit-device"}}},
				{"empty-bearer", path, "portal", "common.unauthenticated", 401, http.Header{"Authorization": {"Bearer "}}},
				{"two-bearers", path, "portal", "common.unauthenticated", 401, http.Header{"Authorization": {"Bearer one", "Bearer two"}}},
				{"merged-bearers", path, "portal", "common.unauthenticated", 401, http.Header{"Authorization": {"Bearer one,two"}}},
				{"whitespace-bearer", path, "portal", "common.unauthenticated", 401, http.Header{"Authorization": {"Bearer one two"}}},
				{"query", path + "?other=1", "portal", "common.validation_failed", 400, good},
				{"empty-query", path + "?", "portal", "common.validation_failed", 400, good},
				{"trailing-slash", path + "/", "portal", "common.not_found", 404, good},
				{"extra-segment", path + "/extra", "portal", "common.not_found", 404, good},
				{"prefix-match", path + "extra", "portal", "common.not_found", 404, good},
				{"sibling", "/api/ai/v1/network/other", "portal", "common.not_found", 404, good},
				{"team-alias", "/ai/v1/network/submit", "portal", "common.not_found", 404, good},
				{"case-alias", "/api/ai/v1/network/Submit", "portal", "common.not_found", 404, good},
				{"encoded-letter", "/api/ai/v1/network/%73ubmit", "portal", "common.validation_failed", 400, good},
				{"encoded-separator", "/api/ai/v1/network%2fsubmit", "portal", "edge.bad_path", 400, good},
				{"double-slash", "/api/ai/v1/network//submit", "portal", "edge.bad_path", 400, good},
				{"dot-segment", "/api/ai/v1/network/./submit", "portal", "edge.bad_path", 400, good},
				{"parent-segment", "/api/ai/v1/network/other/../submit", "portal", "edge.bad_path", 400, good},
				{"semicolon", path + ";extra", "portal", "edge.bad_path", 400, good},
			} {
				t.Run(tc.name, func(t *testing.T) {
					requireEntryProblem(t, addresses[tc.role], http.MethodPost, tc.path, map[string]string{"portal": g.Cfg.PortalHost, "admin": g.Cfg.AdminHost}[tc.role], tc.headers, tc.status, tc.code)
					if calls.Load() != 0 {
						t.Fatal("rejected request reached upstream")
					}
				})
			}
			for _, tc := range []struct {
				name   string
				values []string
			}{
				{"Cookie", []string{"auriga_session=portal"}},
				{"Cookie", []string{"auriga_admin_session=admin"}},
				{"Cookie", []string{"unrelated=value"}},
				{"Cookie2", []string{"browser=value"}},
				{"Origin", []string{g.Cfg.Access.PortalURL()}},
				{"Referer", []string{g.Cfg.Access.PortalURL() + "/"}},
				{"Sec-Fetch-Mode", []string{"navigate"}},
				{"Sec-Fetch-Mode", []string{"no-cors"}},
				{"Sec-Fetch-Mode", []string{"cors", "cors"}},
				{"Sec-Fetch-Site", []string{"same-origin"}},
				{"Sec-Fetch-Dest", []string{"empty"}},
				{"Sec-Fetch-User", []string{"?1"}},
				{"Sec-Fetch-Storage-Access", []string{"active"}},
				{"Access-Control-Request-Method", []string{"POST"}},
				{"Access-Control-Request-Headers", []string{"authorization"}},
				{"Auriga-Forwarded-Tenant", []string{"forged"}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					headers := good.Clone()
					headers[tc.name] = tc.values
					requireEntryProblem(t, addresses["portal"], http.MethodPost, path, g.Cfg.PortalHost, headers, 403, "common.forbidden")
					if calls.Load() != 0 {
						t.Fatal("browser/forwarded credential reached upstream")
					}
				})
			}
		})
	}
}

func TestDeviceNetworkSubmitUnconfigured(t *testing.T) {
	for _, domain := range []bool{false, true} {
		t.Run(map[bool]string{false: "ip", true: "domain"}[domain], func(t *testing.T) {
			g, addresses, _ := accessFixtureConfigured(t, domain, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("unconfigured device request reached platform")
			}), func(c *Config, _ map[string]any) { c.Upstreams["ai-device"] = c.Upstreams["iam"] })
			requireEntryProblem(t, addresses["portal"], http.MethodPost, "/api/ai/v1/network/submit", g.Cfg.PortalHost, http.Header{"Authorization": {"Bearer submit-device"}}, 503, "common.unavailable")
		})
	}
}

func TestDeviceNetworkSubmitBodyLimit(t *testing.T) {
	for _, chunked := range []bool{false, true} {
		t.Run(map[bool]string{false: "content-length", true: "chunked"}[chunked], func(t *testing.T) {
			readBytes := make(chan int, 1)
			device, hop := networkSubmitTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n, err := io.Copy(io.Discard, r.Body)
				readBytes <- int(n)
				if err == nil {
					t.Error("oversized request completed at device runtime")
				}
				w.WriteHeader(http.StatusBadRequest)
			}))
			g, addresses, _ := accessFixtureConfigured(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("oversized device request reached platform")
			}), func(c *Config, _ map[string]any) {
				c.Upstreams["network-submit"], c.NetworkSubmitHop = device.URL, hop
			})
			closeAIAdminTransports(t, g)
			req, err := http.NewRequest(http.MethodPost, "http://"+addresses["portal"]+"/api/ai/v1/network/submit", strings.NewReader(strings.Repeat("x", maxAPIBodyBytes+1)))
			if err != nil {
				t.Fatal(err)
			}
			if chunked {
				req.ContentLength = -1
			}
			req.Header.Set("Authorization", "Bearer submit-device")
			req.Host = g.Cfg.PortalHost
			client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 3 * time.Second}
			t.Cleanup(client.CloseIdleConnections)
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			// Preserve the existing streaming proxy failure behavior. The limit
			// bounds forwarded bytes; it does not buffer before contacting upstream.
			if resp.StatusCode < 400 {
				t.Fatalf("oversized body accepted: status=%d", resp.StatusCode)
			}
			select {
			case n := <-readBytes:
				if n > maxAPIBodyBytes {
					t.Fatalf("forwarded %d bytes beyond device limit", n)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("device body reader did not finish")
			}
		})
	}
}

func TestDeviceNetworkSubmitRedirectRejected(t *testing.T) {
	var calls atomic.Int32
	device, hop := networkSubmitTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Location", "https://device.example.com/other")
		w.Header().Set("Set-Cookie", "unexpected=value")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	g, addresses, _ := accessFixtureConfigured(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("device redirect reached platform")
	}), func(c *Config, _ map[string]any) {
		c.Upstreams["network-submit"], c.NetworkSubmitHop = device.URL, hop
	})
	closeAIAdminTransports(t, g)
	status, headers, body := aiAdminRequest(t, g, addresses, "portal", http.MethodPost, "/api/ai/v1/network/submit", http.Header{"Authorization": {"Bearer submit-device"}})
	if status != http.StatusServiceUnavailable || headers.Get("Set-Cookie") != "" || !strings.Contains(body, `"code":"common.unavailable"`) || calls.Load() != 1 {
		t.Fatalf("redirect escaped device boundary: status=%d body=%s calls=%d", status, body, calls.Load())
	}
}
