package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLinkOriginUsesAllowedDirectOrTrustedProxySource(t *testing.T) {
	cfg := Config{LinkOrigins: []string{"http://192.0.2.10:8080", "https://portal.example.com"}, LinkTrustedProxies: []string{"192.0.2.20"}}
	for _, tc := range []struct{ peer, host, proto, want string }{
		{"192.0.2.30:2000", "portal.example.com", "https", "http://192.0.2.10:8080"},
		{"192.0.2.20:2000", "portal.example.com", "https", "https://portal.example.com"},
		{"192.0.2.20:2000", "attacker.example.com", "https", ""},
		{"192.0.2.20:2000", "portal.example.com, attacker.example.com", "https", ""},
		{"192.0.2.20:2000", "portal.example.com", "https,http", ""},
	} {
		req := httptest.NewRequest("GET", "http://192.0.2.10:8080/api/iam/v1/login/providers", nil)
		req.RemoteAddr = tc.peer
		req.Header.Set("X-Forwarded-Host", tc.host)
		req.Header.Set("X-Forwarded-Proto", tc.proto)
		if got := cfg.linkOrigin(req, "http"); got != tc.want {
			t.Errorf("%+v: got %q", tc, got)
		}
	}
	direct := httptest.NewRequest("GET", "https://portal.example.com/", nil)
	direct.RemoteAddr = "192.0.2.30:2000"
	if got := cfg.linkOrigin(direct, "https"); got != "https://portal.example.com" {
		t.Fatalf("direct domain origin=%q", got)
	}
	if got := (Config{}).linkOrigin(direct, "https"); got != "" {
		t.Fatalf("unconfigured presentation=%q", got)
	}
}

func TestLinkOriginDeclarationsRejectUnsafeOriginsAndProxyTrust(t *testing.T) {
	for _, origin := range []string{"https://portal.example.com/", "https://portal.example.com:443", "https://alice@portal.example.com", "https://portal.example.com?x=1", "https://portal.example.com#x", "https://portal.example.com:0", "https://portal.example.com:65536", "https://PORTAL.example.com", "https://*.example.com", "https://portal.example.com\n"} {
		if err := (Config{LinkOrigins: []string{origin}}).validateLinkOrigins(); err == nil {
			t.Errorf("accepted %q", origin)
		}
	}
	for _, peer := range []string{"proxy.example.com", "*", "0.0.0.0/0", "192.0.2.20/24", "192.0.2.20, 192.0.2.21"} {
		if err := (Config{LinkTrustedProxies: []string{peer}}).validateLinkOrigins(); err == nil {
			t.Errorf("accepted %q", peer)
		}
	}
}

func TestLinkOriginIsSanitizedBeforeAccessApiForwarding(t *testing.T) {
	upstream := make(chan http.Header, 1)
	g, addresses, _ := accessFixtureConfigured(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream <- r.Header.Clone()
		w.WriteHeader(200)
	}), func(cfg *Config, _ map[string]any) {
		cfg.LinkOrigins = []string{"https://portal.example.com"}
		cfg.LinkTrustedProxies = []string{"127.0.0.1"}
	})
	headers := http.Header{"X-Forwarded-Host": {"portal.example.com"}, "X-Forwarded-Proto": {"https"}, "X-Auriga-Link-Origin": {"https://attacker.example.com"}}
	status, _ := entryRequest(t, addresses["portal"], "GET", "/api/iam/v1/login/providers", g.Cfg.PortalHost, headers)
	if status != 200 {
		t.Fatalf("status=%d", status)
	}
	got := <-upstream
	if got.Get("X-Forwarded-Host") != "portal.example.com" || got.Get("X-Forwarded-Proto") != "https" || got.Get("X-Auriga-Link-Origin") != "" {
		t.Fatalf("unsafe presentation headers: %v", got)
	}
	// The presentation selection must never change client identity or the authenticated entry.
	if got.Get("X-Auriga-Entry-Origin") != g.Cfg.Access.PortalURL() || got.Get("X-Auriga-Client-IP") != "127.0.0.1" {
		t.Fatal("presentation affected identity")
	}
}
