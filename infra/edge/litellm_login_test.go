package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLiteLLMNativeLoginExchange(t *testing.T) {
	secret := rand.Text()
	file := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(file, []byte(secret), 0600); err != nil {
		t.Fatal(err)
	}
	claims, _ := json.Marshal(map[string]any{"key": "sk-ui-only", "user_role": "proxy_admin", "exp": time.Now().Add(time.Hour).Unix()})
	token := "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".signature"
	mode := "good"
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/ai/v2/login" || r.Method != "POST" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("browser credentials or wrong route in native exchange")
		}
		var credentials map[string]string
		if json.NewDecoder(r.Body).Decode(&credentials) != nil || credentials["username"] != "admin" || credentials["password"] != secret {
			t.Error("invalid server credentials")
		}
		if mode == "redirect" {
			w.Header().Set("Location", "/unexpected")
			w.WriteHeader(307)
			return
		}
		if mode == "bad" {
			_, _ = w.Write([]byte(`{"token":"invalid"}`))
			return
		}
		if mode == "master" || mode == "expired" {
			key, expiry := "sk-ui-only", time.Now().Add(time.Hour).Unix()
			if mode == "master" {
				key = secret
			} else {
				expiry = time.Now().Add(-time.Hour).Unix()
			}
			claims, _ := json.Marshal(map[string]any{"key": key, "user_role": "proxy_admin", "exp": expiry})
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".signature"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"token": token})
	}))
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	g := &Gateway{Cfg: Config{LiteLLMUI: &LiteLLMUIConfig{Login: &LiteLLMUILogin{PasswordFile: file}}}, litellmURL: u, Now: time.Now}
	for _, scenario := range []string{"good", "redirect", "bad", "master", "expired"} {
		mode = scenario
		got, err := g.loginLiteLLMUI(httptest.NewRequest("POST", "/ai/session", nil))
		if scenario == "good" && (err != nil || got != token || strings.Contains(got, secret)) {
			t.Fatal("native token exchange failed")
		}
		if scenario != "good" && (err == nil || got != "") {
			t.Fatal("unsafe native response accepted")
		}
	}
	if calls != 5 {
		t.Fatal("native exchange followed a redirect")
	}

	// Finish the real handoff, check the original JS-readable token cookie,
	// then revoke/expire the platform session while retaining both UI cookies.
	gate, _, store := accessFixtureConfigured(t, false, nil, func(c *Config, _ map[string]any) { c.LiteLLMOrigin = upstream.URL })
	ui := *gate.Cfg.LiteLLMUI
	ui.Login = &LiteLLMUILogin{PasswordFile: file}
	gate.Cfg.LiteLLMUI = &ui
	backend, _ := browserRPCFixture(t, gate, store)
	sess := putBrowserSession(gate, backend, "admin", "admin")
	adminClaims, _ := json.Marshal(map[string]any{"ctx": "admin", "tid": sess.TenantID, "sub": sess.AccountID, "perms": []string{"ai.providers.write"}})
	sess.AccessToken = "e30." + base64.RawURLEncoding.EncodeToString(adminClaims) + ".fixture"
	store.Put("admin", sess)
	mode = "good"
	code, err := gate.issueLiteLLMHandoff("admin")
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/ai/session", strings.NewReader(url.Values{"code": {code}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", gate.Cfg.Access.AdminURL())
	w := httptest.NewRecorder()
	gate.finishLiteLLMHandoff(w, r, "native-handoff")
	if w.Code != 303 || len(w.Result().Cookies()) != 2 {
		t.Fatal("native handoff did not establish both cookies")
	}
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == "token" && (cookie.Value != token || cookie.HttpOnly || cookie.Path != "/ai") {
			t.Fatal("native cookie incompatible with dashboard")
		}
	}
	backend.clock.Add(int64((9 * time.Hour) / time.Second))
	r = httptest.NewRequest("GET", "/ai/ui/", nil)
	r.Header.Set("Cookie", "auriga_litellm_session=admin; token="+token)
	r = r.WithContext(context.WithValue(r.Context(), accessEntryKey{}, accessEntry{role: "relayAdmin", scheme: "http"}))
	w = httptest.NewRecorder()
	gate.proxyLiteLLM(w, r, "relayAdmin", "expired-native")
	if w.Code != 401 {
		t.Fatal("native cookie bypassed platform expiration")
	}
}
