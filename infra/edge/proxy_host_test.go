package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestReverseProxyHostDirection(t *testing.T) {
	for _, tc := range []struct {
		name     string
		keepHost bool
		host     string
	}{
		{"portal API", true, "portal.example.com"},
		{"admin API", true, "admin.example.com"},
		{"instance", false, "portal.example.com"},
		{"API without Host", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Received-Host", r.Host)
			}))
			defer upstream.Close()
			target, err := url.Parse(upstream.URL)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodGet, "http://portal.example.com/test", nil)
			request.Host = tc.host
			response := httptest.NewRecorder()
			newReverseProxy(target, upstream.Client().Transport, tc.keepHost).ServeHTTP(response, request)
			want := target.Host
			if tc.keepHost && tc.host != "" {
				want = tc.host
			}
			if response.Code != http.StatusOK || response.Header().Get("Received-Host") != want {
				t.Fatalf("status=%d received Host=%q, want %q", response.Code, response.Header().Get("Received-Host"), want)
			}
		})
	}
}
