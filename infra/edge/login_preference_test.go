package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoginPreferenceConfigurationAndHTMLProjection(t *testing.T) {
	for _, value := range []string{"", "company", "team"} {
		t.Setenv("AURIGA_EDGE_LOGIN_PREFERENCE", value)
		cfg := Config{}
		applyEnv(&cfg)
		if cfg.LoginPreference != value {
			t.Fatal("deployment environment was not consumed")
		}
		raw, err := injectLoginPreference([]byte("<html><head></head><body>login</body></html>"), cfg.LoginPreference)
		expected := value
		if expected == "" {
			expected = "company"
		}
		if err != nil || !strings.Contains(string(raw), `name="auriga-login-preference" content="`+expected+`"`) {
			t.Fatalf("wrong deployment preference: %s %v", raw, err)
		}
	}
	if err := (Config{LoginPreference: "typo"}).validate(); err == nil || !strings.Contains(err.Error(), "loginPreference") {
		t.Fatal("invalid deployment preference was not rejected")
	}
	if _, err := injectLoginPreference([]byte(`<meta name="auriga-login-preference"><meta name="auriga-login-preference">`), "team"); err == nil {
		t.Fatal("duplicate trusted metadata was accepted")
	}
}

func TestLoginPreferenceReachesPortalAdminNativeAndUACDocuments(t *testing.T) {
	root := t.TempDir()
	for _, filename := range []string{"index.html", "instance-login.html"} {
		if err := os.WriteFile(filepath.Join(root, filename), []byte("<html><head></head><body>login</body></html>"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	g, _, _ := accessFixtureConfigured(t, false, nil, func(cfg *Config, _ map[string]any) {
		cfg.LoginPreference = "team"
		cfg.Static.PortalRoot, cfg.Static.AdminRoot = root, root
	})
	g.Cfg.UacProviderID = sharedLoginFixtureProvider
	if g.serveUacNavigation(httptest.NewRecorder(), httptest.NewRequest("GET", "/login", nil)) {
		t.Fatal("team preference unexpectedly required a company navigation transaction")
	}
	for _, handler := range []*spaFileServer{g.portalStatic.(*spaFileServer), g.adminStatic.(*spaFileServer)} {
		for _, path := range []string{"/login?preference=company", "/device/BCDFGHJK", "/instance-login.html"} {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest("GET", "http://portal.example.test"+path, nil))
			if response.Code != 200 || !strings.Contains(response.Body.String(), `name="auriga-login-preference" content="team"`) || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("site preference missing: %s", response.Body.String())
			}
		}
	}
	for _, path := range []string{"/login", "/instance-login.html", "/device/BCDFGHJK"} {
		page, err := g.uacPageHTML(httptest.NewRequest("GET", path, nil))
		if err != nil || !strings.Contains(string(page), `name="auriga-login-preference" content="team"`) {
			t.Fatalf("UAC navigation shell preference missing: %v", err)
		}
	}
}

const sharedLoginFixtureProvider = "01900000-0000-7000-8000-000000000002"
