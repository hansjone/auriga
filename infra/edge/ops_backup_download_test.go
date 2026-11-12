package main

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestOpsBackupNativeDownloadThroughEdge(t *testing.T) {
	for _, domain := range []bool{true, false} {
		t.Run(map[bool]string{true: "domain", false: "ip"}[domain], func(t *testing.T) {
			var calls atomic.Int32
			g, addresses, store := accessFixture(t, domain, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Header.Get("Authorization") == "" || r.Header.Get("X-Auriga-Download") != "" || r.URL.RawQuery != "" {
					t.Error("invalid forwarding")
				}
				if err := r.ParseForm(); err != nil || r.PostForm.Get("ticket") != "single-use-test-ticket" {
					t.Error("ticket lost in proxy")
				}
				w.Header().Set("Content-Disposition", `attachment; filename="backup.tar"`)
				_, _ = io.WriteString(w, "streamed archive")
			}))
			backend, _ := browserRPCFixture(t, g, store)
			session := putBrowserSession(g, backend, "admin", "admin")
			session.AccessTokenExpiresAt = g.Now().Add(10 * time.Minute)
			store.Put("admin", session)
			// Use the same verifier fixture as normal IP admin writes.
			csrf := "csrf"
			if !domain {
				g.AdminCSRF = aiAdminCSRF{}
			}
			for _, valid := range []bool{false, true} {
				form := url.Values{"ticket": {"single-use-test-ticket"}, "csrf": {csrf}}
				if !valid {
					form.Set("csrf", "wrong")
				}
				req, err := http.NewRequest("POST", "http://"+addresses["admin"]+"/api/workspace/v1/admin/ops/backups/01900000-0000-7000-8000-000000000001/download", strings.NewReader(form.Encode()))
				if err != nil {
					t.Fatal(err)
				}
				req.Host = g.Cfg.AdminHost
				req.Header.Set("Origin", g.Cfg.Access.AdminURL())
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req.Header.Set("X-Auriga-Download", "must-be-stripped")
				req.AddCookie(&http.Cookie{Name: sessionCookieBase("admin"), Value: "admin"})
				req.AddCookie(&http.Cookie{Name: csrfCookieBase(), Value: csrf})
				response, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				body, _ := io.ReadAll(response.Body)
				_ = response.Body.Close()
				expected := 403
				if valid {
					expected = 200
				}
				if response.StatusCode != expected {
					t.Fatalf("status %d, expected %d: %s", response.StatusCode, expected, body)
				}
			}
			if calls.Load() != 1 {
				t.Fatalf("upstream calls %d", calls.Load())
			}
		})
	}
}
