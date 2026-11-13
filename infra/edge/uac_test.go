package main

import (
	"encoding/json"
	"github.com/hansjone/auriga/infra/gen/deploymentconfig"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInternalUACNeverProxied(t *testing.T) {
	hits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits++; w.WriteHeader(201) }))
	defer upstream.Close()
	cfg, sessions, routes, tickets, signer, _ := testEnv(t)
	cfg.Upstreams["iam"] = upstream.URL
	g := newTestGateway(t, cfg, sessions, routes, tickets, signer)
	for _, host := range []string{cfg.PortalHost, cfg.AdminHost, "192.0.2.10"} {
		for _, method := range []string{"GET", "POST", "PUT", "OPTIONS", "DELETE"} {
			for _, path := range []string{"/api/iam/v1/internal/uac/navigation-proofs", "/api/iam/v1/internal/uac/navigation-proofs/", "/api/iam/v1/%69nternal/uac/navigation-proofs"} {
				req := httptest.NewRequest(method, "https://"+host+path, nil)
				req.Header.Set("Authorization", "Bearer forged-service-token")
				req.Header.Set("auriga-tenant-id", "forged-tenant")
				rec := httptest.NewRecorder()
				g.Handler().ServeHTTP(rec, req)
				if rec.Code != 404 {
					t.Fatalf("%s %s %s: %d", host, method, path, rec.Code)
				}
				var p map[string]any
				if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || p["code"] != "common.not_found" {
					t.Fatal(rec.Body.String())
				}
			}
		}
	}
	if hits != 0 {
		t.Fatalf("internal endpoint reached upstream %d times", hits)
	}
}

func TestUACLoginOperationMethods(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	cases := []struct {
		method, path string
		allowed      bool
	}{
		{"POST", "/api/iam/v1/login/uac/sso", true},
		{"GET", "/api/iam/v1/login/uac/sso", false},
		{"POST", "/api/iam/v1/login/uac/challenges", true},
		{"GET", "/api/iam/v1/login/uac/challenges", false},
		{"GET", "/api/iam/v1/login/uac/challenges/" + id, true},
		{"DELETE", "/api/iam/v1/login/uac/challenges/" + id, true},
		{"POST", "/api/iam/v1/login/uac/challenges/" + id, false},
		{"POST", "/api/iam/v1/login/uac/challenges/" + id + "/complete", true},
		{"GET", "/api/iam/v1/login/uac/challenges/" + id + "/complete", false},
		{"POST", "/api/iam/v1/internal/uac/navigation-proofs", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, c.path, nil)
		if got := loginOperation(r); got != c.allowed {
			t.Fatalf("%s %s: %v", c.method, c.path, got)
		}
	}
}

func TestUACCookiesKeepAudienceIsolation(t *testing.T) {
	g := &Gateway{Cfg: Config{UacSSOCookieNames: []string{"company_pair_a", "company_pair_b"}}}
	r := httptest.NewRequest("POST", "/api/iam/v1/login/uac/sso", nil)
	r.Header.Set("Cookie", "auriga_portal_login_tx=p; auriga_admin_login_tx=a; auriga_admin_session=secret; company_pair_a=one; company_pair_b=two; unrelated=private")
	got, err := g.iamCookies(r, accessEntry{role: "portal", scheme: "http"}, false)
	if err != nil || !strings.Contains(got, "company_pair_a=one") || !strings.Contains(got, "company_pair_b=two") || !strings.Contains(got, "auriga_portal_login_tx=p") || strings.Contains(got, "secret") || strings.Contains(got, "unrelated") || strings.Contains(got, "admin") {
		t.Fatalf("unexpected cookies: %q %v", got, err)
	}
}

func TestUACNavigationHTML(t *testing.T) {
	for _, fixture := range []string{"ip-http.json", "domain-proxy.json"} {
		t.Run(fixture, func(t *testing.T) {
			var received map[string]any
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/iam/v1/internal/uac/navigation-proofs" || r.Header.Get("Authorization") != "Bearer synthetic-service" || r.Header.Get("auriga-tenant-id") == "" {
					t.Error("missing trusted authentication")
				}
				if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
					t.Error(err)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(201)
				_ = json.NewEncoder(w).Encode(map[string]any{"ttlSeconds": 120, "transactionCookie": strings.Repeat("c", 43), "context": map[string]any{"navigationProof": strings.Repeat("p", 43), "expiresAt": time.Now().Add(120 * time.Second), "transactionId": "11111111-1111-4111-8111-111111111111", "purpose": "portal", "autoEligible": true, "autoSuppressed": false, "unexpectedSecret": "MUST-NOT-ENTER-HTML"}})
			}))
			defer upstream.Close()
			cfg, sessions, routes, tickets, signer, _ := testEnv(t)
			raw, err := os.ReadFile(filepath.Join("../../contracts/deployment/fixtures", fixture))
			if err != nil {
				t.Fatal(err)
			}
			cfg.Access, err = deploymentconfig.Load(raw)
			if err != nil {
				t.Fatal(err)
			}
			cfg.Upstreams["iam"] = upstream.URL
			cfg.IAMHop.AllowLoopbackHTTP = true
			cfg.UacProviderID = "11111111-1111-4111-8111-111111111111"
			cfg.IAMRPC.ServiceTokenFile = filepath.Join(t.TempDir(), "service-token")
			if err := os.WriteFile(cfg.IAMRPC.ServiceTokenFile, []byte("synthetic-service"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(cfg.Static.PortalRoot, "index.html"), []byte("<html><head></head><body>portal</body></html>"), 0600); err != nil {
				t.Fatal(err)
			}
			g := newTestGateway(t, cfg, sessions, routes, tickets, signer)
			r := httptest.NewRequest("GET", cfg.Access.PortalURL()+"/login", nil)
			r.Header.Set("Sec-Fetch-Dest", "document")
			r.Header.Set("Sec-Fetch-Mode", "navigate")
			r.Header.Set("Sec-Fetch-Site", "same-origin")
			r.Header.Set("Sec-Fetch-User", "?1")
			r.Header.Set("auriga-tenant-id", "browser-forgery")
			rec := httptest.NewRecorder()
			if !g.serveUacNavigation(rec, r) || rec.Code != 200 {
				t.Fatalf("navigation failed: %s", rec.Body.String())
			}
			if rec.Header().Get("Cache-Control") != "no-store" || !strings.Contains(rec.Body.String(), "auriga-uac-navigation") || strings.Contains(rec.Body.String(), strings.Repeat("c", 43)) || strings.Contains(rec.Body.String(), "MUST-NOT-ENTER-HTML") {
				t.Fatal("unsafe HTML")
			}
			cookies := rec.Result().Cookies()
			if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].Path != "/" || cookies[0].SameSite != http.SameSiteLaxMode {
				t.Fatal("unsafe transaction cookie")
			}
			if received["origin"] != cfg.Access.PortalURL() || received["continuation"] != "portal" {
				t.Fatal("incorrect trusted binding")
			}
		})
	}
}
