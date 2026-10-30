package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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
						if strings.Contains(r.Header.Get("Cookie"), "auriga_") || r.Header.Get("Auriga-Identity") != "" || r.URL.Path != tc.path {
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
