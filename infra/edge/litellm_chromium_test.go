package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Opt in after npm ci in browser-tests and playwright install chromium.
// The browser clicks a model link on the admin origin and submits the production
// handoff form. Neither its Origin nor the handoff POST is synthesized by tests.
func TestLiteLLMChromiumHandoff(t *testing.T) {
	if os.Getenv("AURIGA_LITELLM_CHROMIUM_TEST") != "1" {
		t.Skip("set AURIGA_LITELLM_CHROMIUM_TEST=1 with the pinned browser fixture installed")
	}
	for _, domain := range []bool{false, true} {
		t.Run(map[bool]string{false: "ip", true: "subdomain"}[domain], func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				_, _ = io.WriteString(w, "<!doctype html><title>LiteLLM</title><h1>LiteLLM fixture</h1>")
			}))
			defer upstream.Close()
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "index.html"), []byte(`<!doctype html><html><head><title>管理台</title></head><body><a href="/ai/ui/">模型</a></body></html>`), 0600); err != nil {
				t.Fatal(err)
			}
			g, _, store := accessFixtureConfigured(t, domain, nil, func(c *Config, _ map[string]any) {
				c.LiteLLMOrigin = upstream.URL
				if live := os.Getenv("AURIGA_LITELLM_UI_TEST_ORIGIN"); live != "" {
					c.LiteLLMOrigin = live
				}
				c.Static.AdminRoot = root
			})
			backend, _ := browserRPCFixture(t, g, store)
			// Use the production default for this browser integration; the shared
			// unit fixture's 100ms limit is intended for timeout tests.
			g.SessionRPC.(*iamRPC).timeout = 2 * time.Second
			admin := putBrowserSession(g, backend, "admin", "admin")
			claims, err := json.Marshal(map[string]any{"ctx": "admin", "tid": admin.TenantID, "sub": admin.AccountID, "perms": []string{"ai.providers.write"}})
			if err != nil {
				t.Fatal(err)
			}
			admin.AccessToken = "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".fixture"
			store.Put("admin", admin)
			native := os.Getenv("AURIGA_LITELLM_UI_TEST_PASSWORD_FILE") != ""
			if native {
				ui := *g.Cfg.LiteLLMUI
				ui.Login = &LiteLLMUILogin{PasswordFile: os.Getenv("AURIGA_LITELLM_UI_TEST_PASSWORD_FILE")}
				g.Cfg.LiteLLMUI = &ui
			}
			revoke := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					w.WriteHeader(405)
					return
				}
				store.Revoke(admin.SessionID)
				w.WriteHeader(204)
			}))
			defer revoke.Close()
			input, err := json.Marshal(map[string]any{"admin": g.Cfg.Access.AdminURL(), "portal": g.Cfg.Access.PortalURL(), "ui": g.Cfg.liteLLMUIURL(), "live": os.Getenv("AURIGA_LITELLM_UI_TEST_ORIGIN") != "", "native": native, "revoke": revoke.URL, "mode": map[bool]string{false: "ip", true: "domain"}[domain], "evidence": os.Getenv("AURIGA_LITELLM_UI_TEST_EVIDENCE")})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, "node", filepath.Join("browser-tests", "litellm-handoff.mjs"))
			cmd.Stdin = bytes.NewReader(input)
			if os.Getenv("PLAYWRIGHT_BROWSERS_PATH") == "" {
				browsers, err := filepath.Abs(filepath.Join("browser-tests", ".browsers"))
				if err != nil {
					t.Fatal(err)
				}
				cmd.Env = append(os.Environ(), "PLAYWRIGHT_BROWSERS_PATH="+browsers)
			}
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("Chromium handoff: %v\n%s", err, output)
			}
			t.Log(string(output))
		})
	}
}
