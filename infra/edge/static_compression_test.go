package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestLiteLLMStaticCompression(t *testing.T) {
	asset := strings.Repeat("const dashboard = 'LiteLLM';\n", 24000)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		w.Header().Set("Content-Length", strconv.Itoa(len(asset)))
		_, _ = io.WriteString(w, asset)
	}))
	defer upstream.Close()
	g, _, store := accessFixtureConfigured(t, false, nil, func(c *Config, _ map[string]any) { c.LiteLLMOrigin = upstream.URL })
	backend, _ := browserRPCFixture(t, g, store)
	admin := putBrowserSession(g, backend, "admin", "admin")
	claims, err := json.Marshal(map[string]any{"ctx": "admin", "tid": admin.TenantID, "sub": admin.AccountID, "perms": []string{"ai.providers.write"}})
	if err != nil {
		t.Fatal(err)
	}
	admin.AccessToken = "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".fixture"
	store.Put("admin", admin)
	for _, tc := range []struct {
		name, path, encoding, cookie string
		status                       int
		compressed                   bool
	}{
		{"asset", "/ai/_next/static/chunks/dashboard.js", "gzip, deflate, br", "admin", 200, true},
		{"q-value", "/ai/_next/static/chunks/dashboard.js", "br, gzip;q=0.5", "admin", 200, true},
		{"identity", "/ai/_next/static/chunks/dashboard.js", "identity", "admin", 200, false},
		{"disabled", "/ai/_next/static/chunks/dashboard.js", "gzip;q=0, *;q=1", "admin", 200, false},
		{"api-not-compressed", "/ai/model/info", "gzip", "admin", 200, false},
		{"anonymous", "/ai/_next/static/chunks/dashboard.js", "gzip", "", 401, false},
		{"wrong-session", "/ai/_next/static/chunks/dashboard.js", "gzip", "invalid", 401, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, tc.path, nil)
			r.Header.Set("Accept-Encoding", tc.encoding)
			if tc.cookie != "" {
				r.Header.Set("Cookie", "auriga_litellm_session="+tc.cookie)
			}
			r = r.WithContext(context.WithValue(r.Context(), accessEntryKey{}, accessEntry{role: "relayAdmin", scheme: "http"}))
			w := httptest.NewRecorder()
			g.proxyLiteLLM(w, r, "relayAdmin", "compression")
			if w.Code != tc.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			assertStaticEncoding(t, w, asset, tc.compressed, tc.status == 200)
		})
	}
	r := httptest.NewRequest(http.MethodHead, "/ai/_next/static/chunks/dashboard.js", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	r.Header.Set("Cookie", "auriga_litellm_session=admin")
	r = r.WithContext(context.WithValue(r.Context(), accessEntryKey{}, accessEntry{role: "relayAdmin", scheme: "http"}))
	w := httptest.NewRecorder()
	g.proxyLiteLLM(w, r, "relayAdmin", "head-compression")
	get := r.Clone(r.Context())
	get.Method = http.MethodGet
	want := httptest.NewRecorder()
	g.proxyLiteLLM(want, get, "relayAdmin", "get-compression")
	if w.Code != 200 || w.Body.Len() != 0 || w.Header().Get("Content-Encoding") != "gzip" || w.Header().Get("Content-Length") != want.Header().Get("Content-Length") {
		t.Fatal("upstream HEAD differs from compressed GET")
	}
}

func TestSPAStaticCompression(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "assets"), 0700); err != nil {
		t.Fatal(err)
	}
	asset := strings.Repeat("const login = 'Auriga';\n", 32000)
	if err := os.WriteFile(filepath.Join(root, "assets", "login.js"), []byte(asset), 0600); err != nil {
		t.Fatal(err)
	}
	handler := newSPAFileServer(root)
	for _, encoding := range []string{"gzip", "identity", "gzip;q=0"} {
		t.Run(encoding, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/assets/login.js", nil)
			r.Header.Set("Accept-Encoding", encoding)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			assertStaticEncoding(t, w, asset, encoding == "gzip", true)
		})
	}
	r := httptest.NewRequest(http.MethodGet, "/assets/login.js", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	r.Header.Set("Range", "bytes=0-31")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 206 || w.Header().Get("Content-Encoding") != "" || w.Body.String() != asset[:32] {
		t.Fatal("byte ranges must retain the original representation")
	}
}

func TestStaticCompressionResponseBoundaries(t *testing.T) {
	asset := strings.Repeat("const asset = true;", 128)
	for _, tc := range []struct {
		name, method, contentType, encoding, byteRange string
		status                                         int
	}{
		{"head", "HEAD", "text/javascript", "", "", 200},
		{"range", "GET", "text/javascript", "", "bytes=0-31", 206},
		{"encoded", "GET", "text/javascript", "br", "", 200},
		{"json", "GET", "application/json", "", "", 200},
		{"html", "GET", "text/html", "", "", 200},
		{"error", "GET", "text/javascript", "", "", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "/ai/_next/static/chunks/app.js", nil)
			r.Header.Set("Accept-Encoding", "gzip")
			r.Header.Set("Range", tc.byteRange)
			resp := &http.Response{StatusCode: tc.status, Request: r, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(asset)), ContentLength: int64(len(asset))}
			resp.Header.Set("Content-Type", tc.contentType)
			resp.Header.Set("Content-Encoding", tc.encoding)
			if err := gzipStaticResponse(resp); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			raw, err := io.ReadAll(resp.Body)
			if err != nil || string(raw) != asset || resp.Header.Get("Content-Encoding") != tc.encoding {
				t.Fatal("non-static or pre-encoded response changed")
			}
		})
	}
}

func assertStaticEncoding(t *testing.T, w *httptest.ResponseRecorder, original string, compressed, success bool) {
	t.Helper()
	if (w.Header().Get("Content-Encoding") == "gzip") != compressed {
		t.Fatalf("Content-Encoding=%q, compressed=%t", w.Header().Get("Content-Encoding"), compressed)
	}
	if !success {
		return
	}
	raw := w.Body.Bytes()
	if compressed {
		if len(raw) >= len(original)/4 || !strings.Contains(w.Header().Get("Vary"), "Accept-Encoding") {
			t.Fatal("large static payload was not reduced or did not vary by encoding")
		}
		reader, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = reader.Close() }()
		raw, err = io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
	}
	if string(raw) != original {
		t.Fatal("static asset changed after decompression")
	}
}
