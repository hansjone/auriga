package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSharedPortalMetadataProjectsOnlyTrustedRuntimeConfiguration(t *testing.T) {
	root := t.TempDir()
	page := `<html><head><meta name="auriga-shared-placement-enabled" content="false"><meta name="auriga-instance-parent-domain" content="untrusted.invalid"></head><body>portal</body></html>`
	for _, filename := range []string{"index.html", "instance-login.html"} {
		if err := os.WriteFile(filepath.Join(root, filename), []byte(page), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "asset.js"), []byte("const shared = false;"), 0600); err != nil {
		t.Fatal(err)
	}
	handler := newAccessSPAFileServer(root, "domain").(*spaFileServer)
	handler.sharedPlacement = &sharedPortalPlacement{parentDomain: "instances.example.test"}
	for _, path := range []string{"/", "/instance-login.html", "/index.html", "/?auriga-instance-parent-domain=attacker.invalid"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", "https://portal.example.test"+path, nil))
		body := response.Body.String()
		if response.Code != 200 || strings.Count(body, `name="auriga-shared-placement-enabled"`) != 1 ||
			!strings.Contains(body, `name="auriga-shared-placement-enabled" content="true"`) ||
			!strings.Contains(body, `name="auriga-instance-parent-domain" content="instances.example.test"`) ||
			strings.Contains(body, "attacker.invalid") || strings.Contains(body, "untrusted.invalid") || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("runtime metadata missing or caller-controlled: %d %s", response.Code, body)
		}
	}
	asset := httptest.NewRecorder()
	handler.ServeHTTP(asset, httptest.NewRequest("GET", "https://portal.example.test/asset.js", nil))
	if asset.Body.String() != "const shared = false;" {
		t.Fatal("runtime metadata rewrote a static asset")
	}
	for _, source := range []string{
		`<meta name="auriga-shared-placement-enabled"><meta name="auriga-shared-placement-enabled">`,
		`<meta name="auriga-instance-parent-domain"><meta name="auriga-instance-parent-domain">`,
	} {
		if _, err := injectSharedPlacementMetadata([]byte(source), "instances.example.test"); err == nil {
			t.Fatal("ambiguous metadata was silently enabled")
		}
	}
}

func TestSharedSSOAndNativeShellsUseTheSamePlacementProjection(t *testing.T) {
	root := t.TempDir()
	for _, filename := range []string{"index.html", "instance-login.html"} {
		if err := os.WriteFile(filepath.Join(root, filename), []byte("<html><head></head><body>sign in</body></html>"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	g, _, _ := accessFixture(t, false, nil)
	g.Cfg.Static.PortalRoot = root
	for _, enabled := range []bool{false, true} {
		g.Cfg.SharedDsh = nil
		if enabled {
			g.Cfg.SharedDsh = &SharedDshConfig{}
		}
		for _, path := range []string{"/login", "/instance-login.html"} {
			page, err := g.uacPageHTML(httptest.NewRequest("GET", "http://portal.example.test"+path, nil))
			if err != nil || strings.Contains(string(page), `name="auriga-shared-placement-enabled"`) != enabled {
				t.Fatalf("shared=%v path=%s err=%v", enabled, path, err)
			}
		}
	}
}
