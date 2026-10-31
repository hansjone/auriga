package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestLiteLLMExternalPayload(t *testing.T) {
	var calls atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil || !strings.HasPrefix(fmt.Sprint(body["user"]), "external:") || body["user"] == "tenant:member" {
			t.Error("external caller can impersonate a member")
		}
		metadata, _ := body["metadata"].(map[string]any)
		if metadata["source"] != "external" || len(metadata) != 1 || r.Header.Get("X-Litellm-End-User-Id") != "" || r.Header.Get("Cookie") != "" {
			t.Error("external caller controls trusted attribution")
		}
		w.WriteHeader(204)
	}))
	defer proxy.Close()
	g, _, _ := accessFixtureConfigured(t, false, nil, func(c *Config, _ map[string]any) { c.LiteLLMOrigin = proxy.URL })
	for _, field := range []string{"api_base", "api_key", "base_url", "user_config", "fallbacks", "mock_response", "litellm_metadata", "extra_body"} {
		t.Run(field, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/ai/v1/chat/completions", strings.NewReader(`{"model":"pool","messages":[],"`+field+`":"untrusted"}`))
			r.Header.Set("Authorization", "Bearer sk-external-test")
			r = r.WithContext(context.WithValue(r.Context(), accessEntryKey{}, accessEntry{role: "portal"}))
			w := httptest.NewRecorder()
			g.proxyLiteLLM(w, r, "portal", "test")
			if w.Code != 400 || calls.Load() != 0 {
				t.Fatalf("routing override reached upstream: %d", w.Code)
			}
		})
	}
	r := httptest.NewRequest("POST", "/ai/v1/chat/completions", strings.NewReader(`{"model":"pool","messages":[],"user":"tenant:member","metadata":{"tenant_id":"spoofed"}}`))
	r.Header.Set("Authorization", "Bearer sk-external-test")
	r.Header.Set("X-Litellm-End-User-Id", "tenant:member")
	r.Header.Set("Cookie", "auriga_admin_session=secret")
	r = r.WithContext(context.WithValue(r.Context(), accessEntryKey{}, accessEntry{role: "portal"}))
	w := httptest.NewRecorder()
	g.proxyLiteLLM(w, r, "portal", "test")
	_, _ = io.Copy(io.Discard, w.Result().Body)
	if w.Code != 204 || calls.Load() != 1 {
		t.Fatalf("valid external request=%d calls=%d", w.Code, calls.Load())
	}
}

func TestLiteLLMExternalAllowedOrigins(t *testing.T) {
	var calls atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer sk-virtual" || r.Header.Get("Origin") != "" || r.Header.Get("Cookie") != "" {
			t.Error("external key or origin/cookie isolation changed")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer proxy.Close()
	for _, configured := range []bool{false, true} {
		g, _, _ := accessFixtureConfigured(t, false, nil, func(c *Config, _ map[string]any) {
			c.LiteLLMOrigin = proxy.URL
			if configured {
				c.LinkOrigins = []string{"https://portal.example.com", "http://192.0.2.10:8080"}
			}
		})
		for _, tc := range []struct {
			name    string
			origins []string
			key     string
			status  int
		}{
			{"no-origin", nil, "Bearer sk-virtual", 204},
			{"domain", []string{"https://portal.example.com"}, "Bearer sk-virtual", 204},
			{"ip", []string{"http://192.0.2.10:8080"}, "Bearer sk-virtual", 204},
			{"foreign", []string{"https://foreign.example.com"}, "Bearer sk-virtual", 403},
			{"suffix", []string{"https://portal.example.com.attacker.example.com"}, "Bearer sk-virtual", 403},
			{"path", []string{"https://portal.example.com/"}, "Bearer sk-virtual", 403},
			{"null", []string{"null"}, "Bearer sk-virtual", 403},
			{"empty", []string{""}, "Bearer sk-virtual", 403},
			{"duplicate", []string{"https://portal.example.com", "https://portal.example.com"}, "Bearer sk-virtual", 403},
			{"combined", []string{"https://portal.example.com, https://portal.example.com"}, "Bearer sk-virtual", 403},
			{"without-key", []string{"https://portal.example.com"}, "", 401},
		} {
			t.Run(fmt.Sprintf("configured=%t/%s", configured, tc.name), func(t *testing.T) {
				before := calls.Load()
				r := httptest.NewRequest("GET", "/ai/v1/models", nil)
				if tc.origins != nil {
					r.Header["Origin"] = tc.origins
				}
				r.Header.Set("Authorization", tc.key)
				r.Header.Set("X-Forwarded-Host", "portal.example.com")
				r.Header.Set("X-Forwarded-Proto", "https")
				r.Header.Set("Cookie", "auriga_admin_session=fixture")
				r = r.WithContext(context.WithValue(r.Context(), accessEntryKey{}, accessEntry{role: "portal"}))
				w := httptest.NewRecorder()
				g.proxyLiteLLM(w, r, "portal", "test")
				want := tc.status
				if !configured && want == 204 && tc.origins != nil {
					want = 403
				}
				if w.Code != want {
					t.Fatalf("status=%d want=%d", w.Code, want)
				}
				delta := int32(0)
				if want == 204 {
					delta = 1
				}
				if calls.Load() != before+delta {
					t.Fatal("rejected request reached upstream")
				}
			})
		}
	}
}

func TestLiteLLMEntryBoundary(t *testing.T) {
	for _, domain := range []bool{false, true} {
		t.Run(map[bool]string{false: "ip", true: "domain"}[domain], func(t *testing.T) {
			var calls atomic.Int32
			seen := make(chan *http.Request, 8)
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				seen <- r.Clone(context.Background())
				w.Header().Set("Access-Control-Allow-Origin", "https://attacker.example.com")
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Expose-Headers", "Authorization")
				w.Header().Add("Set-Cookie", "token=litellm; Domain=example.com; Path=/")
				w.Header().Add("Set-Cookie", "auriga_admin_session=malicious; Path=/")
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
				{"ui-anonymous", "relayAdmin", "GET", "/ai/ui/", http.Header{}, 401},
				{"ui-bearer", "relayAdmin", "GET", "/ai/ui/", http.Header{"Authorization": {"Bearer sk-virtual"}}, 401},
				{"ui-portal", "portal", "GET", "/ai/ui/", http.Header{"Cookie": {"auriga_admin_session=admin"}}, 403},
				{"ui-member", "relayAdmin", "GET", "/ai/ui/", http.Header{"Cookie": {"auriga_litellm_session=portal"}}, 401},
				{"ui-nonadministrator", "relayAdmin", "GET", "/ai/ui/", http.Header{"Cookie": {"auriga_litellm_session=ordinary"}, "Authorization": {"Bearer sk-forged"}}, 403},
				{"ui-admin", "relayAdmin", "GET", "/ai/ui/", http.Header{"Cookie": {"auriga_litellm_session=admin; auriga_admin_session=bad; auriga_login_tx=tx; __Host-auriga_login_tx=secure-tx; unrelated=bad; token=litellm"}}, 204},
				{"admin-cookie-not-relay", "relayAdmin", "GET", "/ai/ui/", http.Header{"Cookie": {"auriga_admin_session=admin"}}, 401},
				{"admin-management-disabled", "admin", "POST", "/ai/key/generate", http.Header{"Cookie": {"auriga_admin_session=admin"}, "Origin": {g.Cfg.Access.AdminURL()}}, 403},
				{"management-origin", "relayAdmin", "POST", "/ai/key/generate", http.Header{"Cookie": {"auriga_litellm_session=admin"}, "Origin": {g.Cfg.Access.PortalURL()}}, 403},
				{"management", "relayAdmin", "POST", "/ai/key/generate", http.Header{"Cookie": {"auriga_litellm_session=admin"}, "Origin": {g.Cfg.liteLLMUIURL()}}, 204},
				{"ui-playground", "relayAdmin", "POST", "/ai/v1/chat/completions", http.Header{"Cookie": {"auriga_litellm_session=admin"}, "Origin": {g.Cfg.liteLLMUIURL()}, "Authorization": {"Bearer ui-session-token"}}, 204},
				{"ui-api-alias", "relayAdmin", "GET", "/ai/v1/model/info", http.Header{"Cookie": {"auriga_litellm_session=admin"}}, 204},
				{"ui-next-segment", "relayAdmin", "GET", "/ai/ui/models-and-endpoints/__next.%21KGRhc2hib2FyZCk.models-and-endpoints.__PAGE__.txt", http.Header{"Cookie": {"auriga_litellm_session=admin"}}, 204},
				{"ui-next-literal-layout", "relayAdmin", "GET", "/ai/ui/models-and-endpoints/__next.!KGRhc2hib2FyZCk.models-and-endpoints.__PAGE__.txt", http.Header{"Cookie": {"auriga_litellm_session=admin"}}, 204},
				{"ui-next-head", "relayAdmin", "HEAD", "/ai/ui/__next.%21KGRhc2hib2FyZCk.__PAGE__.txt", http.Header{"Cookie": {"auriga_litellm_session=admin"}}, 204},
				{"ui-next-anonymous", "relayAdmin", "GET", "/ai/ui/__next.%21KGRhc2hib2FyZCk.__PAGE__.txt", http.Header{}, 401},
				{"ui-next-cross-site", "relayAdmin", "GET", "/ai/ui/__next.%21KGRhc2hib2FyZCk.__PAGE__.txt", http.Header{"Cookie": {"auriga_litellm_session=admin"}, "Sec-Fetch-Site": {"same-site"}, "Sec-Fetch-Mode": {"cors"}}, 403},
				{"ui-next-write", "relayAdmin", "POST", "/ai/ui/__next.%21KGRhc2hib2FyZCk.__PAGE__.txt", http.Header{"Cookie": {"auriga_litellm_session=admin"}, "Origin": {g.Cfg.liteLLMUIURL()}}, 400},
				{"ui-next-separator", "relayAdmin", "GET", "/ai/ui/__next.%21KGRhc2hib2FyZCk%2f.__PAGE__.txt", http.Header{"Cookie": {"auriga_litellm_session=admin"}}, 400},
				{"ui-next-double-encoded", "relayAdmin", "GET", "/ai/ui/__next.%2521KGRhc2hib2FyZCk.__PAGE__.txt", http.Header{"Cookie": {"auriga_litellm_session=admin"}}, 400},
				{"ui-next-traversal", "relayAdmin", "GET", "/ai/ui/../__next.%21KGRhc2hib2FyZCk.__PAGE__.txt", http.Header{"Cookie": {"auriga_litellm_session=admin"}}, 400},
				{"ui-encoded-api", "relayAdmin", "GET", "/ai/model/%21info", http.Header{"Cookie": {"auriga_litellm_session=admin"}}, 400},
				{"ui-no-auriga-admin-api", "relayAdmin", "GET", "/api/org/v1/admin/roles", http.Header{"Cookie": {"auriga_litellm_session=admin"}}, 403},
				{"ui-options", "relayAdmin", "OPTIONS", "/ai/ui/", http.Header{"Cookie": {"auriga_litellm_session=admin"}}, 405},
				{"ui-cross-origin-fetch", "relayAdmin", "GET", "/ai/model/info", http.Header{"Cookie": {"auriga_litellm_session=admin"}, "Sec-Fetch-Site": {"same-site"}, "Sec-Fetch-Mode": {"cors"}}, 403},
				{"api-prefix", "portal", "POST", "/ai/v1/key/generate", http.Header{"Authorization": {"Bearer sk-virtual"}}, 404},
				{"root-management", "portal", "POST", "/key/generate", http.Header{"Authorization": {"Bearer sk-virtual"}}, 405},
				{"root-api-management", "admin", "POST", "/v1/key/generate", http.Header{"Authorization": {"Bearer sk-virtual"}}, 405},
			} {
				t.Run(tc.name, func(t *testing.T) {
					before := calls.Load()
					status, responseHeaders, body := aiAdminRequest(t, g, addresses, tc.role, tc.method, tc.path, tc.headers)
					if status != tc.status {
						t.Fatalf("%d %s", status, body)
					}
					if tc.status == 204 {
						for name := range responseHeaders {
							if strings.HasPrefix(strings.ToLower(name), "access-control-") {
								t.Errorf("upstream CORS header leaked: %s", name)
							}
						}
						cookies := (&http.Response{Header: responseHeaders}).Cookies()
						if tc.role == "portal" && len(cookies) != 0 || tc.role == "relayAdmin" && (len(cookies) != 1 || cookies[0].Name != "token" || cookies[0].Domain != "" || cookies[0].Path != "/ai" || cookies[0].SameSite != http.SameSiteStrictMode) {
							t.Fatal("untrusted upstream Set-Cookie escaped isolation")
						}
						r := <-seen
						wantPath, err := url.PathUnescape(tc.path)
						if err != nil {
							t.Fatal(err)
						}
						if strings.Contains(r.Header.Get("Cookie"), "auriga_") || r.Header.Get("Auriga-Identity") != "" || r.URL.Path != wantPath {
							t.Fatalf("bad boundary %s %v", r.URL.Path, r.Header)
						}
						if tc.name == "ui-admin" && r.Header.Get("Cookie") != "token=litellm" {
							t.Fatal("non-LiteLLM cookies crossed the management boundary")
						}
					} else if calls.Load() != before {
						t.Fatal("rejected request reached LiteLLM")
					}
				})
			}
			store.Revoke(backend.rows["admin"].SessionID)
			status, _, _ := aiAdminRequest(t, g, addresses, "relayAdmin", "GET", "/ai/ui/", http.Header{"Cookie": {"auriga_litellm_session=admin"}})
			if status != 401 {
				t.Fatalf("revoked admin = %d", status)
			}
		})
	}
}

func TestLiteLLMUIRefusesPlatformOrigin(t *testing.T) {
	g, _, store := accessFixtureConfigured(t, false, nil, func(c *Config, _ map[string]any) { c.LiteLLMOrigin = "http://127.0.0.1:4000" })
	for _, origin := range []any{g.Cfg.Access.AdminOrigin(), g.Cfg.Access.PortalOrigin()} {
		cfg := g.Cfg
		cfg.LiteLLMUI = nil
		raw, err := json.Marshal(map[string]any{"litellmUI": map[string]any{"origin": origin, "listener": g.Cfg.Access.Listeners().Admin}})
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			t.Fatal(err)
		}
		other, err := NewGateway(cfg, store, g.Routes, g.Tickets, g.Signer, g.Log)
		if err == nil {
			other.Ingress.Close()
			t.Fatal("LiteLLM UI accepted an Auriga browser origin")
		}
	}
}
