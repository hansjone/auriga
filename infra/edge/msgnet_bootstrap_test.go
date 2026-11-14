package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMsgnetBootstrapIndependentAuthenticationAndFixedRoutes(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		for _, name := range []string{"Authorization", "Cookie", "Auriga-Identity", "Auriga-Forwarded-Tenant", "X-Tenant-ID"} {
			if r.Header.Get(name) != "" {
				t.Errorf("credential forwarded: %s", name)
			}
		}
		if r.Method == "POST" {
			raw, _ := io.ReadAll(r.Body)
			if string(raw) != `{"signed":"request"}` {
				t.Errorf("signed bytes changed: %q", raw)
			}
		}
		if r.Header.Get("Test-Redirect") == "yes" {
			w.Header().Set("Location", "https://other.example.com")
			w.WriteHeader(302)
			return
		}
		w.Header().Set("Set-Cookie", "forged=1")
		_, _ = w.Write([]byte(`{"public":"signed-or-sealed"}`))
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	g := &Gateway{collabURL: target, APITransports: map[*url.URL]http.RoundTripper{target: upstream.Client().Transport}}
	for _, tc := range []struct {
		path, method, scheme string
		status               int
	}{
		{"/api/collab/v2/network/bootstrap/controls", "GET", "https", 200},
		{"/api/collab/v2/network/bootstrap/transport", "GET", "https", 200},
		{"/api/collab/v2/network/bootstrap/keys", "GET", "https", 200},
		{"/api/collab/v2/network/bootstrap/materials", "POST", "https", 200},
		{"/api/collab/v2/network/bootstrap/materials", "POST", "http", 403},
		{"/api/collab/v2/network/bootstrap/keys?realm=2", "GET", "https", 400},
		{"/api/collab/v2/network/bootstrap/materials", "GET", "https", 405},
	} {
		t.Run(tc.method+tc.path+tc.scheme, func(t *testing.T) {
			body := ""
			if tc.method == "POST" {
				body = `{"signed":"request"}`
			}
			r := httptest.NewRequest(tc.method, "https://portal.example.com"+tc.path, strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer expired")
			r.Header.Set("Cookie", "old=expired")
			r.Header.Set("Auriga-Forwarded-Tenant", "forged")
			r.Header.Set("Auriga-Identity", "forged")
			r = r.WithContext(context.WithValue(r.Context(), accessEntryKey{}, accessEntry{role: "portal", scheme: tc.scheme, host: "portal.example.com", port: 443}))
			w := httptest.NewRecorder()
			if !g.proxyMsgnetBootstrap(w, r, "test") {
				t.Fatal("route missed")
			}
			if w.Code != tc.status {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			if tc.status == 200 && (w.Header().Get("Set-Cookie") != "" || w.Header().Get("Cache-Control") != "no-store") {
				t.Fatal("credential/cache leakage")
			}
		})
	}
	if calls.Load() != 4 {
		t.Fatal("rejected bootstrap reached upstream", calls.Load())
	}
	r := httptest.NewRequest("GET", "https://portal.example.com/api/collab/v2/network/bootstrap/keys", nil)
	r.Header.Set("Test-Redirect", "yes")
	r = r.WithContext(context.WithValue(r.Context(), accessEntryKey{}, accessEntry{scheme: "https"}))
	w := httptest.NewRecorder()
	g.proxyMsgnetBootstrap(w, r, "test")
	if w.Code != 503 {
		t.Fatal("redirect accepted", w.Code)
	}
	r = httptest.NewRequest("POST", "https://portal.example.com/api/collab/v2/network/projections/transport", nil)
	if g.proxyMsgnetBootstrap(httptest.NewRecorder(), r, "test") {
		t.Fatal("plaintext material endpoint exposed")
	}
}
