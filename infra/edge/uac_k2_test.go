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

func uacNavFixture(t *testing.T, upstream http.Handler) *Gateway {
	t.Helper()
	cfg, sessions, routes, tickets, signer, _ := testEnv(t)
	server := httptest.NewServer(upstream)
	t.Cleanup(server.Close)
	raw, err := os.ReadFile("../../contracts/deployment/fixtures/ip-http.json")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Access, err = deploymentconfig.Load(raw)
	if err != nil {
		t.Fatal(err)
	}
	cfg.SecureCookies = false
	cfg.Upstreams["iam"] = server.URL
	cfg.IAMHop.AllowLoopbackHTTP = true
	cfg.UacProviderID, _ = navigationRequestID()
	cfg.IAMRPC.ServiceTokenFile = filepath.Join(t.TempDir(), "service-token")
	if err := os.WriteFile(cfg.IAMRPC.ServiceTokenFile, []byte(testUACSecret(t)), 0600); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"index.html", "instance-login.html"} {
		if err := os.WriteFile(filepath.Join(cfg.Static.PortalRoot, file), []byte("<html><head></head><body>portal</body></html>"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return newTestGateway(t, cfg, sessions, routes, tickets, signer)
}

func TestUACK2DeviceNavigationAndCSRF(t *testing.T) {
	correlation, _ := navigationRequestID()
	tx, csrf, proof := testUACSecret(t), testUACSecret(t), testUACSecret(t)
	suppression := testUACSecret(t)
	var received map[string]any
	g := uacNavFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/iam/v1/internal/uac/device-navigation-proofs" {
			t.Error("device request used old navigation endpoint")
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		w.Header().Set(uacCorrelationHeader, correlation)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{"context": uacNavigationContext{NavigationProof: proof, ExpiresAt: time.Now().Add(120 * time.Second), TransactionID: correlation, Purpose: "device", AutoEligible: false, AutoSuppressed: true}, "transactionCookie": tx, "csrfCookie": csrf, "ttlSeconds": 120})
	}))
	request := httptest.NewRequest("GET", g.Cfg.Access.PortalURL()+"/device/BCDF-GHJK?deviceGrantId=forged", nil)
	request.Header.Set("Cookie", "auriga_portal_sso_suppression="+suppression+"; auriga_csrf="+csrf)
	request.Header.Set("Sec-Fetch-Dest", "document")
	request.Header.Set("Sec-Fetch-Mode", "navigate")
	request.Header.Set("X-Auriga-Correlation-Id", "forged")
	rec := httptest.NewRecorder()
	if !g.serveUacNavigation(rec, request) || rec.Code != 200 {
		t.Fatalf("device navigation: %d", rec.Code)
	}
	if received["userCode"] != "BCDF-GHJK" || received["continuation"] != "device" || received["csrfCookie"] != csrf || received["suppressionCookie"] != suppression || received["deviceGrantId"] != nil {
		t.Fatal("incorrect device binding")
	}
	if rec.Header().Get(uacCorrelationHeader) != correlation || !strings.Contains(rec.Body.String(), correlation) || strings.Contains(rec.Body.String(), csrf) || strings.Contains(rec.Body.String(), tx) || strings.Contains(rec.Body.String(), suppression) {
		t.Fatal("unsafe navigation projection")
	}
	// Reuse preserves the value and renews its lifetime for the new proof.
	cookies := rec.Result().Cookies()
	if len(cookies) != 2 || cookies[1].Value != csrf || cookies[1].MaxAge < 120 || !cookies[0].HttpOnly || cookies[0].Secure || cookies[0].Domain != "" {
		t.Fatal("cookie reuse regressed")
	}
	request.Header.Del("Cookie")
	rec = httptest.NewRecorder()
	g.serveUacNavigation(rec, request)
	cookies = rec.Result().Cookies()
	if len(cookies) != 2 || cookies[1].HttpOnly || cookies[1].Secure || cookies[1].Path != "/" || cookies[1].Domain != "" || cookies[1].MaxAge != 120 || cookies[1].SameSite != http.SameSiteLaxMode {
		t.Fatal("unsafe anonymous CSRF cookie")
	}
	if rec.Header().Get("X-Frame-Options") != "DENY" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("authentication document is embeddable/cacheable")
	}
}

func TestUACK2NavigationRejectsUntrustedAndInvalidResponses(t *testing.T) {
	for _, test := range []string{"iframe", "prefetch", "duplicate-metadata", "wrong-origin", "duplicate-cookie", "missing-csrf", "device-auto", "expired", "bad-correlation"} {
		t.Run(test, func(t *testing.T) {
			id, _ := navigationRequestID()
			hits := 0
			g := uacNavFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits++
				if test == "bad-correlation" {
					w.Header().Set(uacCorrelationHeader, "invalid")
				} else {
					w.Header().Set(uacCorrelationHeader, id)
				}
				expires := time.Now().Add(time.Minute)
				if test == "expired" {
					expires = time.Now().Add(-time.Minute)
				}
				response := map[string]any{"context": uacNavigationContext{NavigationProof: testUACSecret(t), ExpiresAt: expires, TransactionID: id, Purpose: "device", AutoEligible: test == "device-auto"}, "transactionCookie": testUACSecret(t), "ttlSeconds": 120}
				if test != "missing-csrf" {
					response["csrfCookie"] = testUACSecret(t)
				}
				w.WriteHeader(201)
				_ = json.NewEncoder(w).Encode(response)
			}))
			req := httptest.NewRequest("GET", g.Cfg.Access.PortalURL()+"/device/BCDFGHJK", nil)
			switch test {
			case "iframe":
				req.Header.Set("Sec-Fetch-Dest", "iframe")
			case "prefetch":
				req.Header.Set("Sec-Purpose", "prefetch")
			case "duplicate-metadata":
				req.Header.Add("Sec-Fetch-Mode", "navigate")
				req.Header.Add("Sec-Fetch-Mode", "cors")
			case "wrong-origin":
				req.Header.Set("Origin", "https://untrusted.example.com")
			case "duplicate-cookie":
				req.Header.Set("Cookie", "auriga_csrf="+testUACSecret(t)+"; auriga_csrf="+testUACSecret(t))
			}
			rec := httptest.NewRecorder()
			g.serveUacNavigation(rec, req)
			expected := 503
			switch test {
			case "iframe", "prefetch", "duplicate-metadata", "wrong-origin":
				expected = 403
			case "duplicate-cookie":
				expected = 400
			}
			if rec.Code != expected || len(rec.Result().Cookies()) != 0 {
				t.Fatalf("unsafe rejection %d, expected %d", rec.Code, expected)
			}
			if (expected == 400 || expected == 403) && hits != 0 {
				t.Fatal("untrusted navigation reached IAM")
			}
		})
	}
}

func TestUACK2PrivateSSOCookieAmbiguity(t *testing.T) {
	g := &Gateway{Cfg: Config{UacSSOCookieNames: []string{"company_token", "company_subject"}}}
	req := httptest.NewRequest("POST", "/api/iam/v1/login/uac/sso", nil)
	req.Header.Set("Cookie", "company_token="+testUACSecret(t)+"; company_token="+testUACSecret(t))
	if _, err := g.iamCookies(req, accessEntry{role: "portal", scheme: "http"}, false); err == nil {
		t.Fatal("ambiguous enterprise cookies accepted")
	}
}
