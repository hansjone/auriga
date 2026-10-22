package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSPAInstallationModeIsUniqueAndOnlyInIndex(t *testing.T) {
	for _, mode := range []string{"ip", "domain"} {
		for _, source := range []string{`<!doctype html><html><head><title>test</title></head><body>app</body></html>`, `<html><head><meta name="auriga-access-mode" content="domain"></head><body>app</body></html>`} {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(source), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "asset.js"), []byte(`console.log("asset")`), 0o600); err != nil {
				t.Fatal(err)
			}
			handler := newAccessSPAFileServer(dir, mode)
			for _, path := range []string{"/", "/route", "/index.html"} {
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, httptest.NewRequest("GET", "http://example.com"+path, nil))
				body := response.Body.String()
				if response.Code != 200 || strings.Count(body, `name="auriga-access-mode"`) != 1 || !strings.Contains(body, `content="`+mode+`"`) || response.Header().Get("Cache-Control") != "no-store" {
					t.Fatalf("mode %s index %s: %d %s", mode, path, response.Code, body)
				}
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest("GET", "http://example.com/asset.js", nil))
			if response.Body.String() != `console.log("asset")` || strings.Contains(response.Body.String(), "auriga-access-mode") {
				t.Fatal("asset rewritten")
			}
		}
	}
	for _, source := range []string{`<meta name="auriga-access-mode" content="ip"><meta name="auriga-access-mode" content="domain">`, `<meta name="auriga-access-mode" content="guessed">`} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
		response := httptest.NewRecorder()
		newAccessSPAFileServer(dir, "ip").ServeHTTP(response, httptest.NewRequest("GET", "http://example.com/", nil))
		if response.Code != 503 || !strings.Contains(response.Body.String(), `"code":"common.unavailable"`) {
			t.Fatal(response.Code, response.Body.String())
		}
	}
}

func TestAccessConfigEntryDominatesLegacyEnvironment(t *testing.T) {
	cfg, _, _, _, _, _ := testEnv(t)
	raw, err := os.ReadFile("../../contracts/deployment/fixtures/ip-http.json")
	if err != nil {
		t.Fatal(err)
	}
	accessFile := filepath.Join(t.TempDir(), "access.json")
	if err := os.WriteFile(accessFile, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(t.TempDir(), "edge.json")
	raw, err = json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configFile, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AURIGA_EDGE_PORTAL_HOST", "attacker.example.com")
	t.Setenv("AURIGA_EDGE_ADMIN_HOST", "attacker.example.com")
	t.Setenv("AURIGA_EDGE_LISTEN", "127.0.0.1:1")
	t.Setenv("AURIGA_EDGE_SECURE_COOKIES", "true")
	loaded, err := loadConfigWithAccess(configFile, accessFile)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Access.Valid() || loaded.PortalHost != "192.0.2.10:8080" || loaded.AdminHost != "192.0.2.10:8082" || loaded.SecureCookies || loaded.Access.Listeners().Portal.Port != 8080 {
		t.Fatal("legacy environment overwrote validated installation")
	}
}
