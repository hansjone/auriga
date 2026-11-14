package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
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
				req.Header.Set("Cookie", "auriga_csrf="+secret)
				var logs bytes.Buffer
				g.Log = slog.New(slog.NewJSONHandler(&logs, nil))
				rec := httptest.NewRecorder()
				g.serveUacNavigation(rec, req)
				if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Type"), "text/html") || strings.Contains(rec.Body.String(), "auriga-uac-navigation") || strings.Contains(rec.Body.String(), secret) || len(rec.Result().Cookies()) != 0 {
					t.Fatalf("fallback document failed: %d", rec.Code)
				}
				if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("X-Frame-Options") != "DENY" || !uacUUID.MatchString(rec.Header().Get(uacCorrelationHeader)) {
					t.Fatal("fallback document lost safety headers")
				}
				if status == 401 || status == 403 {
					event := singleBetaWarning(t, logs.Bytes())
					if event["status"] != float64(status) || event["correlationId"] != rec.Header().Get(uacCorrelationHeader) {
						t.Fatal("navigation rejection lost diagnostic status or correlation")
					}
				} else if logs.Len() != 0 {
					t.Fatal("ordinary rejection unexpectedly emitted a warning")
				}
				if strings.Contains(logs.String(), secret) {
					t.Fatal("navigation rejection leaked private data")
				}
			})
		}
	}
}

func TestUACReviewSessionCSRFRemainsUsableAfterNavigationExpiry(t *testing.T) {
	for _, secure := range []bool{false, true} {
		for _, path := range []string{"/login", "/instance-login.html", "/device/BCDFGHJK"} {
			t.Run(fmt.Sprintf("secure=%t%s", secure, path), func(t *testing.T) {
				now := time.Now()
				csrf, sessionCookie := testUACSecret(t), testUACSecret(t)
				id, _ := navigationRequestID()
				writes := 0
				g := uacNavFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasPrefix(r.URL.Path, "/api/iam/v1/internal/uac/") {
						var payload map[string]any
						if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
							t.Error(err)
						}
						if payload["csrfCookie"] != csrf {
							t.Error("existing CSRF was not supplied for IAM reuse")
						}
						w.Header().Set(uacCorrelationHeader, id)
						w.WriteHeader(201)
						_ = json.NewEncoder(w).Encode(map[string]any{"context": uacNavigationContext{NavigationProof: testUACSecret(t), ExpiresAt: now.Add(120 * time.Second), TransactionID: id, Purpose: payload["continuation"].(string)}, "transactionCookie": testUACSecret(t), "csrfCookie": csrf, "ttlSeconds": 120})
						return
					}
					writes++
					w.WriteHeader(204)
				}))
				g.Now = func() time.Time { return now }
				g.Cfg.SecureCookies = secure
				sess := putPortalSession(g.Sessions.(*MemorySessionStore), sessionCookie, csrf, testUACSecret(t))
				sess.TenantID = g.Cfg.Access.TenantID()
				g.Sessions.(*MemorySessionStore).Put(sessionCookie, sess)
				origin, err := url.Parse(g.Cfg.Access.PortalURL())
				if err != nil {
					t.Fatal(err)
				}
				if secure {
					origin.Scheme = "https"
				}
				jar, err := cookiejar.New(nil)
				if err != nil {
					t.Fatal(err)
				}
				csrfName := cookieName(secure, csrfCookieBase())
				jar.SetCookies(origin, []*http.Cookie{{Name: cookieName(secure, sessionCookieBase("portal")), Value: sessionCookie, Path: "/", Secure: secure}, {Name: csrfName, Value: csrf, Path: "/", Secure: secure}})
				nav := httptest.NewRequest("GET", origin.String()+path, nil)
				for _, cookie := range jar.Cookies(origin) {
					nav.AddCookie(cookie)
				}
				rec := httptest.NewRecorder()
				if !g.serveUacNavigation(rec, nav) || rec.Code != 200 {
					t.Fatalf("session navigation failed: %d", rec.Code)
				}
				for _, cookie := range rec.Result().Cookies() {
					if cookie.Name == csrfName {
						t.Fatal("navigation overwrote the session CSRF lifetime")
					}
				}
				jar.SetCookies(origin, rec.Result().Cookies())
				// Session cookies have no expiry. Advancing application time past
				// the proof lifetime must not invalidate the session's CSRF binding.
				now = now.Add(121 * time.Second)
				for _, operation := range []string{"/api/iam/v1/device/BCDFGHJK/approve", "/api/iam/v1/me/sessions/revoke-others"} {
					req := httptest.NewRequest("POST", origin.String()+operation, strings.NewReader("{}"))
					for _, cookie := range jar.Cookies(origin) {
						req.AddCookie(cookie)
					}
					req.Header.Set("Origin", origin.String())
					req.Header.Set("X-Auriga-CSRF", csrf)
					req = req.WithContext(context.WithValue(req.Context(), accessEntryKey{}, accessEntry{role: "portal", scheme: origin.Scheme}))
					rec := httptest.NewRecorder()
					g.proxyAccessAPI(rec, req, g.iamURL, "portal", "192.0.2.10", "csrf-regression")
					if rec.Code != 204 {
						t.Fatalf("session write after proof expiry failed: %d", rec.Code)
					}
				}
				if writes != 2 {
					t.Fatal("session writes did not reach IAM")
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
