package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBifrostAdminAuthority(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		u, p, ok := r.BasicAuth()
		if !ok || u != "auriga" || p != strings.Repeat("s", 64) || (r.URL.Path != "/api/providers" && r.URL.Path != "/api/virtual-keys" && r.URL.Path != "/api/providers/019a0000-0000-7000-8000-000000000001/models") || r.Header.Get("X-Auriga-Tenant") == "forged" || r.Header.Get("Cookie") != "" {
			t.Error("unsafe management proxy")
		}
		w.Header().Set("Set-Cookie", "bad=1")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer upstream.Close()
	secret := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(secret, []byte(strings.Repeat("s", 64)), 0600); err != nil {
		t.Fatal(err)
	}
	g, addresses, store := accessFixtureConfigured(t, false, http.NotFoundHandler(), func(c *Config, _ map[string]any) {
		c.Relay = &RelayConfig{Origin: upstream.URL, AdminPasswordFile: secret}
	})
	backend, _ := browserRPCFixture(t, g, store)
	g.AdminCSRF = aiAdminCSRF{}
	s := putBrowserSession(g, backend, "admin", "admin")
	claims, _ := json.Marshal(map[string]any{"ctx": "admin", "tid": s.TenantID, "sub": s.AccountID, "perms": []string{"ai.providers.read", "ai.providers.write"}})
	s.AccessToken = "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".fixture"
	s.AccessTokenExpiresAt = g.Now().Add(10 * time.Minute)
	store.Put("admin", s)
	headers := http.Header{"Cookie": {"auriga_admin_session=admin"}, "X-Auriga-Tenant": {"forged"}}
	// Use the project's audience-specific cookie name.
	headers.Set("Cookie", sessionCookieBase("admin")+"=admin")
	code, h, _ := aiAdminRequest(t, g, addresses, "admin", "GET", relayAdminPrefix+"/providers", headers)
	if code != 200 || h.Get("Set-Cookie") != "" || h.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("read %d", code)
	}
	code, _, _ = aiAdminRequest(t, g, addresses, "admin", "GET", relayAdminPrefix+"/providers/019a0000-0000-7000-8000-000000000001/models", headers)
	if code != 200 {
		t.Fatalf("provider catalog %d", code)
	}
	catalogCalls := calls.Load()
	catalogHeaders := headers.Clone()
	catalogHeaders.Set("Origin", "http://"+addresses["admin"])
	code, _, _ = aiAdminRequest(t, g, addresses, "admin", "POST", relayAdminPrefix+"/providers/019a0000-0000-7000-8000-000000000001/models", catalogHeaders)
	if code != 405 || calls.Load() != catalogCalls {
		t.Fatalf("catalog write accepted %d", code)
	}
	writeToken := s.AccessToken
	readClaims, _ := json.Marshal(map[string]any{"ctx": "admin", "tid": s.TenantID, "sub": s.AccountID, "perms": []string{"ai.providers.read"}})
	s.AccessToken = "e30." + base64.RawURLEncoding.EncodeToString(readClaims) + ".fixture"
	store.Put("admin", s)
	code, _, _ = aiAdminRequest(t, g, addresses, "admin", "GET", relayAdminPrefix+"/virtual-keys", headers)
	if code != 200 {
		t.Fatalf("read-only key listing %d", code)
	}
	s.AccessToken = writeToken
	store.Put("admin", s)
	before := calls.Load()
	headers.Set("Origin", "http://"+addresses["admin"])
	headers.Set("X-Auriga-CSRF", "csrf")
	code, _, body := aiAdminRequest(t, g, addresses, "admin", "POST", relayAdminPrefix+"/providers", headers)
	if code != 403 || !strings.Contains(body, "iam.step_up_required") || calls.Load() != before {
		t.Fatalf("missing stepup: %d %s", code, body)
	}
	s.StepUpAt = g.Now().Add(-time.Minute)
	store.Put("admin", s)
	headers.Set("Idempotency-Key", "fixture-admin-write")
	code, _, _ = aiAdminRequest(t, g, addresses, "admin", "POST", relayAdminPrefix+"/providers", headers)
	if code != 200 {
		t.Fatalf("write %d", code)
	}
	for _, tc := range []struct {
		name, role, path, header, value string
		status                          int
	}{
		{"origin", "admin", "/providers", "Origin", "http://evil.example", 403},
		{"csrf", "admin", "/providers", "X-Auriga-CSRF", "wrong", 403},
		{"bearer", "admin", "/providers", "Authorization", "Bearer forged", 403},
		{"fetch", "admin", "/providers", "Sec-Fetch-Site", "cross-site", 403},
		{"path", "admin", "/config", "", "", 404},
		{"portal", "portal", "/providers", "", "", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := headers.Clone()
			if tc.header != "" {
				h.Set(tc.header, tc.value)
			}
			n := calls.Load()
			status, _, _ := aiAdminRequest(t, g, addresses, tc.role, "POST", relayAdminPrefix+tc.path, h)
			if status != tc.status || calls.Load() != n {
				t.Fatalf("got %d, calls %d", status, calls.Load()-n)
			}
		})
	}
	backend.mu.Lock()
	delete(backend.rows, "admin")
	backend.mu.Unlock()
	code, _, _ = aiAdminRequest(t, g, addresses, "admin", "GET", relayAdminPrefix+"/providers", headers)
	if code != 401 {
		t.Fatalf("revoked session %d", code)
	}
}

func TestBifrostExternalKeySourceAndHeaders(t *testing.T) {
	var source = "external"
	var active = true
	var calls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/keys/check" {
			_ = json.NewEncoder(w).Encode(map[string]any{"name": "test", "active": active, "source": source})
			return
		}
		calls++
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Cookie") != "" || r.Header.Get("X-Bf-Vk") != "" || r.Header.Get("X-Auriga-Tenant") != "" {
			t.Error("forwarded untrusted headers")
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["user"] == "forged" {
			t.Error("forged attribution")
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	_, addresses, _ := accessFixtureConfigured(t, false, http.NotFoundHandler(), func(c *Config, _ map[string]any) {
		c.Relay = &RelayConfig{Origin: upstream.URL, AdminPasswordFile: "not-read"}
	})
	request := func(body string) int {
		r, _ := http.NewRequest("POST", "http://"+addresses["portal"]+"/ai/v1/chat/completions", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer sk-bf-fixture")
		r.Header.Set("Cookie", "test=1")
		r.Header.Set("X-Auriga-Tenant", "forged")
		r.Header.Set("X-Bf-Vk", "forged")
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		return resp.StatusCode
	}
	if code := request(`{"model":"pool","messages":[],"user":"forged"}`); code != 200 || calls != 1 {
		t.Fatalf("external %d calls %d", code, calls)
	}
	source = "member"
	if code := request(`{"model":"pool","messages":[]}`); code != 403 || calls != 1 {
		t.Fatalf("member bypass %d", code)
	}
	source = "external"
	active = false
	if code := request(`{"model":"pool","messages":[]}`); code != 403 || calls != 1 {
		t.Fatalf("revoked %d", code)
	}
	if code := request(`{"api_base":"http://evil","model":"pool","messages":[]}`); code != 400 {
		t.Fatalf("override %d", code)
	}
}
