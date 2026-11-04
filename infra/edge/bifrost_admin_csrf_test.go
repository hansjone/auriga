package main

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBifrostDomainAdminCSRF(t *testing.T) {
	for _, secure := range []bool{false, true} {
		scheme := "http"
		if secure {
			scheme = "https"
		}
		t.Run(scheme, func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				_, _ = w.Write([]byte(`{"items":[]}`))
			}))
			defer upstream.Close()
			secret := filepath.Join(t.TempDir(), "password")
			if err := os.WriteFile(secret, []byte(strings.Repeat("s", 64)), 0600); err != nil {
				t.Fatal(err)
			}
			pki := createAccessPKI(t)
			g, addresses, store := accessFixtureConfigured(t, true, http.NotFoundHandler(), func(c *Config, doc map[string]any) {
				c.Relay = &RelayConfig{Origin: upstream.URL, AdminPasswordFile: secret}
				if secure {
					c.TLS = writeAccessServerTLS(t, pki)
					for _, name := range []string{"portalOrigin", "adminOrigin", "issuerOrigin"} {
						doc[name].(map[string]any)["scheme"] = "https"
					}
					doc["instances"].(map[string]any)["scheme"] = "https"
					for _, role := range []string{"portal", "admin"} {
						doc["listeners"].(map[string]any)[role].(map[string]any)["tls"] = true
					}
				}
			})
			backend, _ := browserRPCFixture(t, g, store)
			s := putBrowserSession(g, backend, "admin", "admin")
			claims, _ := json.Marshal(map[string]any{"ctx": "admin", "tid": s.TenantID, "sub": s.AccountID, "perms": []string{"ai.providers.read", "ai.providers.write"}})
			s.AccessToken = "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".fixture"
			s.AccessTokenExpiresAt = g.Now().Add(10 * time.Minute)
			s.StepUpAt = g.Now().Add(-time.Minute)
			store.Put("admin", s)
			transport := &http.Transport{}
			if secure {
				transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, ServerName: "iam.internal.test", RootCAs: pki.roots}
			}
			client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
			defer client.CloseIdleConnections()
			for _, test := range []struct {
				name, drift, token string
				cookie             bool
				status             int
				code               string
			}{
				{"valid", "", "csrf", true, 200, ""},
				{"wrong-header", "", "wrong", true, 403, "common.forbidden"},
				{"missing-cookie", "", "csrf", false, 403, "common.forbidden"},
				{"session-drift", "sid", "csrf", true, 503, "common.unavailable"},
				{"account-drift", "account", "csrf", true, 503, "common.unavailable"},
				{"tenant-drift", "tenant", "csrf", true, 503, "common.unavailable"},
				{"audience-drift", "audience", "csrf", true, 503, "common.unavailable"},
				{"expiry-drift", "absolute", "csrf", true, 503, "common.unavailable"},
			} {
				t.Run(test.name, func(t *testing.T) {
					backend.mu.Lock()
					backend.mode = test.drift
					backend.mu.Unlock()
					request, err := http.NewRequest("POST", scheme+"://"+addresses["admin"]+relayAdminPrefix+"/providers", strings.NewReader("{}"))
					if err != nil {
						t.Fatal(err)
					}
					request.Host = g.Cfg.AdminHost
					request.Header.Set("Origin", scheme+"://"+g.Cfg.AdminHost)
					request.Header.Set("Sec-Fetch-Site", "same-origin")
					request.Header.Set("Idempotency-Key", "domain-csrf-positive")
					request.Header.Set("X-Auriga-CSRF", test.token)
					request.AddCookie(&http.Cookie{Name: cookieName(secure, sessionCookieBase("admin")), Value: "admin"})
					if test.cookie {
						request.AddCookie(&http.Cookie{Name: cookieName(secure, csrfCookieBase()), Value: "csrf"})
					}
					before := calls.Load()
					response, err := client.Do(request)
					if err != nil {
						t.Fatal(err)
					}
					defer func() { _ = response.Body.Close() }()
					body, err := io.ReadAll(response.Body)
					if err != nil {
						t.Fatal(err)
					}
					wantCalls := before
					if test.status == 200 {
						wantCalls++
					}
					if response.StatusCode != test.status || calls.Load() != wantCalls || test.code != "" && !strings.Contains(string(body), `"code":"`+test.code+`"`) {
						t.Fatalf("status=%d upstreamCalls=%d expected=%d body=%s", response.StatusCode, calls.Load()-before, test.status, body)
					}
				})
			}
		})
	}
}
