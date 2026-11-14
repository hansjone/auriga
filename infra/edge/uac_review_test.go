package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestUACReviewCrossSiteDocumentsRemainUsable(t *testing.T) {
	for _, path := range []string{"/login", "/instance-login.html", "/device/BCDFGHJK"} {
		t.Run(path, func(t *testing.T) {
			id, _ := navigationRequestID()
			var payload map[string]any
			g := uacNavFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				w.Header().Set(uacCorrelationHeader, id)
				w.WriteHeader(201)
				purpose := payload["continuation"].(string)
				_ = json.NewEncoder(w).Encode(map[string]any{"context": uacNavigationContext{NavigationProof: testUACSecret(t), ExpiresAt: time.Now().Add(120 * time.Second), TransactionID: id, Purpose: purpose, AutoEligible: purpose == "portal"}, "transactionCookie": testUACSecret(t), "csrfCookie": testUACSecret(t), "ttlSeconds": 120})
			}))
			req := httptest.NewRequest("GET", g.Cfg.Access.PortalURL()+path, nil)
			req.Header.Set("Sec-Fetch-Site", "cross-site")
			req.Header.Set("Sec-Fetch-Dest", "document")
			req.Header.Set("Sec-Fetch-Mode", "navigate")
			rec := httptest.NewRecorder()
			g.serveUacNavigation(rec, req)
			if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"autoEligible":false`) || !strings.Contains(rec.Body.String(), "auriga-uac-navigation") {
				t.Fatalf("cross-site document failed: %d", rec.Code)
			}
			if payload["fetchMetadata"].(map[string]any)["site"] != "cross-site" {
				t.Fatal("IAM did not receive cross-site evidence")
			}
		})
	}
}

func TestUACReviewExplicitIAMRejectionsServeFallbackHTML(t *testing.T) {
	for _, path := range []string{"/login", "/instance-login.html", "/device/BCDFGHJK"} {
		for _, status := range []int{400, 401, 403, 404, 409, 429} {
			t.Run(fmt.Sprintf("%s/%d", path, status), func(t *testing.T) {
				secret := testUACSecret(t)
				g := uacNavFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					// A general rejection need not have a UAC transaction yet: retain edge ID.
					w.Header().Set("Content-Type", "application/problem+json")
					w.WriteHeader(status)
					_ = json.NewEncoder(w).Encode(map[string]any{"status": status, "title": "Rejected", "code": "common.forbidden", "detail": secret})
				}))
				req := httptest.NewRequest("GET", g.Cfg.Access.PortalURL()+path, nil)
				rec := httptest.NewRecorder()
				g.serveUacNavigation(rec, req)
				if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Type"), "text/html") || strings.Contains(rec.Body.String(), "auriga-uac-navigation") || strings.Contains(rec.Body.String(), secret) || len(rec.Result().Cookies()) != 0 {
					t.Fatalf("fallback document failed: %d", rec.Code)
				}
				if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("X-Frame-Options") != "DENY" || !uacUUID.MatchString(rec.Header().Get(uacCorrelationHeader)) {
					t.Fatal("fallback document lost safety headers")
				}
			})
		}
	}
}

func TestUACReviewMalformedIAMResponseRemainsUnavailable(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
	}{
		{"invalid-json", "broken response", 403},
		{"missing-problem", "null", 403},
		{"incomplete-problem", `{}`, 403},
		{"mismatched-status", `{"status":404,"code":"common.not_found","title":"Rejected"}`, 403},
		{"oversized-problem", strings.Repeat(" ", 32769), 403},
		{"upstream-unavailable", `{"status":503,"code":"common.unavailable","title":"Unavailable"}`, 503},
	} {
		t.Run(test.name, func(t *testing.T) {
			g := uacNavFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			rec := httptest.NewRecorder()
			g.serveUacNavigation(rec, httptest.NewRequest("GET", g.Cfg.Access.PortalURL()+"/login", nil))
			if rec.Code != 503 || len(rec.Result().Cookies()) != 0 {
				t.Fatal("malformed response was not rejected")
			}
		})
	}
}

func TestUACReviewBadBrowserCSRFIsReinitialized(t *testing.T) {
	id, _ := navigationRequestID()
	csrf := testUACSecret(t)
	var payload map[string]any
	g := uacNavFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		w.Header().Set(uacCorrelationHeader, id)
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{"context": uacNavigationContext{NavigationProof: testUACSecret(t), ExpiresAt: time.Now().Add(120 * time.Second), TransactionID: id, Purpose: "portal"}, "transactionCookie": testUACSecret(t), "csrfCookie": csrf, "ttlSeconds": 120})
	}))
	req := httptest.NewRequest("GET", g.Cfg.Access.PortalURL()+"/login", nil)
	req.Header.Set("Cookie", "auriga_csrf=malformed")
	rec := httptest.NewRecorder()
	g.serveUacNavigation(rec, req)
	if rec.Code != 200 || payload["csrfCookie"] != nil {
		t.Fatal("invalid browser CSRF reached IAM")
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 2 || cookies[1].Value != csrf || cookies[1].MaxAge < 120 {
		t.Fatal("CSRF was not renewed for the full proof lifetime")
	}
}

func TestUACReviewSSOCookieNamesFailStartup(t *testing.T) {
	for _, names := range [][]string{{""}, {"auriga_session"}, {"__Host-auriga_csrf"}, {"company token"}, {"token;other"}, {"token=other"}, {"company_token", "company_token"}} {
		t.Run(fmt.Sprint(names), func(t *testing.T) {
			cfg, sessions, routes, tickets, signer, _ := testEnv(t)
			cfg.UacSSOCookieNames = names
			if err := cfg.validate(); err == nil || !strings.Contains(err.Error(), "uacSsoCookieNames") {
				t.Fatal("configuration did not fail validation")
			}
			if _, err := NewGateway(cfg, sessions, routes, tickets, signer, nil); err == nil || !strings.Contains(err.Error(), "uacSsoCookieNames") {
				t.Fatal("gateway accepted invalid configuration")
			}
		})
	}
	if err := (Config{UacSSOCookieNames: []string{"company_token", "__Host-company_subject"}}).validateUacCookieNames(); err != nil {
		t.Fatal(err)
	}
}

func TestUACReviewEnterpriseCookieRawHop(t *testing.T) {
	expected := `company_token="part with spaces\comma,=end"; company_subject=staff 01`
	seen := make(chan string, 1)
	g := uacNavFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen <- r.Header.Get("Cookie"); w.WriteHeader(204) }))
	g.Cfg.UacSSOCookieNames = []string{"company_token", "company_subject"}
	req := httptest.NewRequest("POST", g.Cfg.Access.PortalURL()+"/api/iam/v1/login/uac/sso", strings.NewReader("{}"))
	req.Header.Add("Cookie", `company_token="part with spaces\comma,=end"; unrelated=discard`)
	req.Header.Add("Cookie", "company_subject=staff 01")
	req = req.WithContext(context.WithValue(req.Context(), accessEntryKey{}, accessEntry{role: "portal", scheme: "http"}))
	rec := httptest.NewRecorder()
	g.proxyAccessAPI(rec, req, g.iamURL, "portal", "192.0.2.10", "test-request")
	if rec.Code != 204 {
		t.Fatalf("raw cookie hop failed: %d", rec.Code)
	}
	if got := <-seen; got != expected {
		t.Fatal("enterprise cookie bytes were changed")
	}
	for _, value := range []string{`company_token="unterminated`, "company_token=bad\x01value", "company_token=one; company_token=two"} {
		req := httptest.NewRequest("POST", "/", nil)
		req.Header.Set("Cookie", value)
		if _, err := rawSSOCookies(req, g.Cfg.UacSSOCookieNames); err == nil {
			t.Fatal("invalid cookie was silently discarded")
		}
	}
}
