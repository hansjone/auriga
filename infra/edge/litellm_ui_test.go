package main

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestLiteLLMUIHandoff(t *testing.T) {
	for _, domain := range []bool{false, true} {
		t.Run(map[bool]string{false: "ip", true: "subdomain"}[domain], func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.Header.Get("Cookie"), "auriga_") {
					t.Error("platform cookie reached LiteLLM")
				}
				w.Header().Set("Content-Type", "text/html")
				_, _ = io.WriteString(w, "<html>LiteLLM fixture</html>")
			}))
			defer upstream.Close()
			upstreamURL := upstream.URL
			if live := os.Getenv("AURIGA_LITELLM_UI_TEST_ORIGIN"); live != "" {
				upstreamURL = live
			}
			g, addresses, store := accessFixtureConfigured(t, domain, nil, func(c *Config, _ map[string]any) { c.LiteLLMOrigin = upstreamURL })
			backend, _ := browserRPCFixture(t, g, store)
			admin := putBrowserSession(g, backend, "admin", "admin")
			claims, err := json.Marshal(map[string]any{"ctx": "admin", "tid": admin.TenantID, "sub": admin.AccountID, "perms": []string{"ai.providers.write"}})
			if err != nil {
				t.Fatal(err)
			}
			admin.AccessToken = "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".fixture"
			store.Put("admin", admin)
			client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			defer client.CloseIdleConnections()
			do := func(role, method, path, body, origin, cookie string) (int, http.Header, string) {
				t.Helper()
				r, err := http.NewRequest(method, "http://"+addresses[role]+path, strings.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				r.Host = originAuthority(g.Cfg.Access.AdminOrigin())
				if role == "relayAdmin" {
					r.Host = originAuthority(g.Cfg.LiteLLMUI.Origin)
				}
				if origin != "" {
					r.Header.Set("Origin", origin)
				}
				if cookie != "" {
					r.Header.Set("Cookie", cookie)
				}
				if method == http.MethodPost {
					r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				}
				response, err := client.Do(r)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = response.Body.Close() }()
				data, err := io.ReadAll(response.Body)
				if err != nil {
					t.Fatal(err)
				}
				return response.StatusCode, response.Header, string(data)
			}
			issue := func() string {
				t.Helper()
				status, headers, body := do("admin", "GET", "/ai/ui/", "", "", "auriga_admin_session=admin")
				if status != 200 || !strings.Contains(body, `action="`+g.Cfg.liteLLMUIURL()+`/ai/session"`) || headers.Get("Referrer-Policy") != "no-referrer" || headers.Get("Cache-Control") != "no-store" || !strings.Contains(headers.Get("Content-Security-Policy"), "form-action "+g.Cfg.liteLLMUIURL()) {
					t.Fatalf("invalid first-party handoff: status=%d headers=%v", status, headers)
				}
				match := regexp.MustCompile(`name="code" value="([a-zA-Z0-9_-]{43})"`).FindStringSubmatch(body)
				if len(match) != 2 || strings.Contains(body, `value="admin"`) {
					t.Fatal("handoff must contain a one-time code, never the browser session")
				}
				return match[1]
			}
			code := issue()
			form := url.Values{"code": {code}}.Encode()
			if status, _, _ := do("relayAdmin", "POST", "/ai/session", form, g.Cfg.Access.PortalURL(), "auriga_admin_session=admin"); status != 403 {
				t.Fatalf("wrong-origin handoff=%d", status)
			}
			status, headers, _ := do("relayAdmin", "POST", "/ai/session", form, g.Cfg.Access.AdminURL(), "auriga_admin_session=admin")
			response := &http.Response{Header: headers}
			cookies := response.Cookies()
			if status != 303 || headers.Get("Location") != "/ai/ui/" || len(cookies) != 1 || cookies[0].Name != "auriga_litellm_session" || cookies[0].Domain != "" || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
				t.Fatalf("handoff did not establish an isolated session: %d", status)
			}
			if status, _, page := do("relayAdmin", "GET", "/ai/ui/", "", "", "auriga_litellm_session=admin"); status != 200 {
				t.Fatalf("authenticated UI=%d", status)
			} else if os.Getenv("AURIGA_LITELLM_UI_TEST_ORIGIN") != "" {
				asset := regexp.MustCompile(`src="(/ai/[^"<>?]+\.js)"`).FindStringSubmatch(page)
				if len(asset) != 2 {
					t.Fatal("real LiteLLM UI did not reference its prefixed JavaScript asset")
				}
				status, headers, body := do("relayAdmin", "GET", asset[1], "", "", "auriga_litellm_session=admin")
				if status != 200 || len(body) == 0 || !strings.Contains(headers.Get("Content-Type"), "javascript") {
					t.Fatalf("real UI asset=%d", status)
				}
				for name := range headers {
					if strings.HasPrefix(strings.ToLower(name), "access-control-") {
						t.Fatal("real UI CORS header escaped edge")
					}
				}
			}
			if status, _, _ := do("relayAdmin", "POST", "/ai/session", form, g.Cfg.Access.AdminURL(), ""); status != 401 {
				t.Fatalf("replayed code=%d", status)
			}
			expired := issue()
			now := g.Now
			g.Now = func() time.Time { return now().Add(2 * time.Minute) }
			if status, _, _ := do("relayAdmin", "POST", "/ai/session", url.Values{"code": {expired}}.Encode(), g.Cfg.Access.AdminURL(), ""); status != 401 {
				t.Fatalf("expired code=%d", status)
			}
			g.Now = now
			revoked := issue()
			store.Revoke(backend.rows["admin"].SessionID)
			if status, _, _ := do("relayAdmin", "POST", "/ai/session", url.Values{"code": {revoked}}.Encode(), g.Cfg.Access.AdminURL(), ""); status != 401 {
				t.Fatalf("revoked administrator handoff=%d", status)
			}
		})
	}
}
