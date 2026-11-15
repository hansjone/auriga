package main

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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

func TestMsgnetBootstrapThroughActualHTTPSGatewayWithoutServiceJWT(t *testing.T) {
	publicPKI, collabPKI := createAccessPKI(t), createAccessPKI(t)
	var calls atomic.Int32
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 {
			t.Error("collab hop skipped mTLS")
		}
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("bootstrap depended on stale bearer/cookie")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sealed_or_public":true}`))
	}))
	upstream.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{collabPKI.server}, ClientCAs: collabPKI.roots, ClientAuth: tls.RequireAndVerifyClientCert}
	upstream.StartTLS()
	defer upstream.Close()
	g, addresses, _ := accessFixtureConfigured(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("bootstrap touched unrelated API")
		w.WriteHeader(500)
	}), func(c *Config, doc map[string]any) {
		c.TLS = writeAccessServerTLS(t, publicPKI)
		c.Upstreams["collab"], c.CollabHop = upstream.URL, collabPKI.hop
		for _, role := range []string{"portal", "admin"} {
			doc[role+"Origin"].(map[string]any)["scheme"] = "https"
			doc["listeners"].(map[string]any)[role].(map[string]any)["tls"] = true
		}
	})
	closeAIAdminTransports(t, g)
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: publicPKI.hop.ServerName, RootCAs: publicPKI.roots}}}
	defer client.CloseIdleConnections()
	for _, endpoint := range []string{"controls", "transport", "keys", "materials"} {
		method := http.MethodGet
		var body io.Reader
		if endpoint == "materials" {
			method, body = http.MethodPost, strings.NewReader(`{"signed":"request"}`)
		}
		r, e := http.NewRequest(method, "https://"+addresses["portal"]+"/api/collab/v2/network/bootstrap/"+endpoint, body)
		if e != nil {
			t.Fatal(e)
		}
		r.Header.Set("Authorization", "Bearer expired")
		r.Header.Set("Cookie", "old=expired")
		response, e := client.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		data, e := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if e != nil || response.StatusCode != 200 {
			t.Fatalf("%s: %d %s, %v", endpoint, response.StatusCode, data, e)
		}
	}
	if calls.Load() != 4 {
		t.Fatalf("public bootstrap calls: %d", calls.Load())
	}
}
