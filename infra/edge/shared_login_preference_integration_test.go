package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSharedPlacementAndTeamPreferenceCoexistInEntryDocuments(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"index.html", "instance-login.html"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("<html><head></head><body>entry</body></html>"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	g, _, _ := accessFixtureConfigured(t, false, nil, func(cfg *Config, _ map[string]any) {
		cfg.LoginPreference = "team"
		cfg.Static.PortalRoot, cfg.Static.AdminRoot = root, root
	})
	// Compose the two independently configured entry features without starting
	// NATS. Their authorities have separate transport/authorization regressions.
	g.Cfg.SharedDsh = &SharedDshConfig{}
	portal := g.portalStatic.(*spaFileServer)
	portal.sharedPlacement = &sharedPortalPlacement{parentDomain: g.Cfg.Access.InstanceParentDomain()}
	assertMetadata := func(raw []byte) {
		t.Helper()
		for _, expected := range []string{
			`name="auriga-shared-placement-enabled" content="true"`,
			`name="auriga-login-preference" content="team"`,
		} {
			if strings.Count(string(raw), expected) != 1 {
				t.Fatalf("entry lost or duplicated trusted metadata %q: %s", expected, raw)
			}
		}
	}
	for _, path := range []string{"/login", "/instance-login.html", "/device/BCDFGHJK"} {
		response := httptest.NewRecorder()
		portal.ServeHTTP(response, httptest.NewRequest("GET", "http://portal.example.test"+path, nil))
		if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("entry response: %d %v", response.Code, response.Header())
		}
		assertMetadata(response.Body.Bytes())
		page, err := g.uacPageHTML(httptest.NewRequest("GET", path, nil))
		if err != nil {
			t.Fatal(err)
		}
		assertMetadata(page)
	}
}
