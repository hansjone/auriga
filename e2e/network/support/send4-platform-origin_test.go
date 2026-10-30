package main

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// Added to the unmodified edge test package with go test -overlay. This is a
// routing precondition, not IAM authentication, PG acceptance or a SEND result.
// The upstream deliberately returns a transport sentinel, never business success.
func TestSEND4PlatformOrigin(t *testing.T) {
	for _, domain := range []bool{false, true} {
		t.Run(map[bool]string{false: "ip", true: "domain"}[domain], func(t *testing.T) {
			var calls, submitCalls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Header.Get("Authorization") != "Bearer send4-route-probe" || r.Header.Get("Cookie") != "" {
					t.Error("device credential was changed or browser credentials were introduced")
				}
				w.WriteHeader(http.StatusTeapot)
			}))
			t.Cleanup(upstream.Close)
			submit, hop := networkSubmitTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				submitCalls.Add(1)
				if r.URL.Path != "/api/ai/v1/network/submit" || r.Header.Get("Authorization") != "Bearer send4-route-probe" || r.Header.Get("Cookie") != "" {
					t.Error("submit route or device credential changed")
				}
				w.WriteHeader(http.StatusTeapot)
			}))
			g, addresses, _ := accessFixtureConfigured(t, domain, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				t.Error("device request reached the IAM/Org/workspace upstream")
				w.WriteHeader(http.StatusInternalServerError)
			}), func(c *Config, _ map[string]any) {
				// Configure every existing AI upstream, so an absent setting cannot
				// explain why the exact production submit path is rejected.
				c.Upstreams["ai-device"] = upstream.URL
				c.Upstreams["ai-gateway"] = upstream.URL
				c.Upstreams["ai-admin"] = upstream.URL
				c.AIAdminHop.AllowLoopbackHTTP = true
				c.Upstreams["network-submit"] = submit.URL
				c.NetworkSubmitHop = hop
			})
			closeAIAdminTransports(t, g)
			headers := http.Header{
				"Authorization":  {"Bearer send4-route-probe"},
				"Content-Type":   {"application/json"},
				"Sec-Fetch-Mode": {"cors"}, // Native Node fetch's transport header.
			}
			status, body := entryRequest(t, addresses["portal"], http.MethodGet, "/ai/v1/models", g.Cfg.PortalHost, headers)
			if status != http.StatusTeapot || calls.Load() != 1 {
				t.Fatalf("SEND-4 routing positive control failed: status=%d upstreamCalls=%d body=%s", status, calls.Load(), body)
			}
			t.Log("SEND-4 positive control: native device request reached configured AI upstream")
			status, body = entryRequest(t, addresses["portal"], http.MethodPost, "/api/ai/v1/network/submit", g.Cfg.PortalHost, headers)
			if status != http.StatusTeapot || submitCalls.Load() != 1 || calls.Load() != 1 {
				t.Fatalf("SEND-4 platform Origin must route separate HTTPS network submit: status=%d networkCalls=%d modelCalls=%d body=%s", status, submitCalls.Load(), calls.Load(), body)
			}
		})
	}
}
