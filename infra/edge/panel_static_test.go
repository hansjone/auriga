package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPanelStaticOnPortalInstanceAndControl(t *testing.T) {
	cfg, sessions, routes, tickets, signer, _ := testEnv(t)
	cfg.Static.PanelRoot = t.TempDir()
	for name, content := range map[string]string{"manifest.json": `{"entry":"auriga-team-panel-abc.js","issuedAt":"2020-01-01T00:00:00Z","expiresAt":"2099-01-01T00:00:00Z","signature":{"alg":"EdDSA","kid":"fixture","value":"fixture"}}`, "auriga-team-panel-abc.js": "module"} {
		if err := os.WriteFile(filepath.Join(cfg.Static.PanelRoot, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	gw := newTestGateway(t, cfg, sessions, routes, tickets, signer)
	for _, host := range []string{"portal.example.com", "11111111-1111-1111-1111-111111111111.i.example.com", "control.example.com"} {
		for _, path := range []string{"/panel/manifest.json", "/panel/auriga-team-panel-abc.js"} {
			req := httptest.NewRequest("GET", "http://"+host+path, nil)
			res := httptest.NewRecorder()
			if host == "control.example.com" {
				gw.serveInstanceControl(res, req)
			} else {
				gw.Handler().ServeHTTP(res, req)
			}
			if res.Code != 200 || res.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("%s%s: %d %s", host, path, res.Code, res.Body.String())
			}
		}
	}
	for _, path := range []string{"/panel/", "/panel/missing.js", "/panel/../identity-keys.json"} {
		res := httptest.NewRecorder()
		gw.Handler().ServeHTTP(res, httptest.NewRequest("GET", "http://portal.example.com"+path, nil))
		if res.Code == 200 {
			t.Fatalf("unsafe/missing panel file served: %s", path)
		}
	}
	res := httptest.NewRecorder()
	gw.serveInstanceControl(res, httptest.NewRequest("POST", "http://control.example.com/panel/manifest.json", nil))
	if res.Code == 200 {
		t.Fatal("panel write accepted")
	}
	res = httptest.NewRecorder()
	gw.serveInstanceControl(res, httptest.NewRequest("GET", "http://control.example.com/api/workspace/v1/instance/self", nil).WithContext(context.Background()))
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("self authentication changed: %d", res.Code)
	}
}

func TestPanelFailureDoesNotBlockOtherRoutes(t *testing.T) {
	cfg, sessions, routes, tickets, signer, _ := testEnv(t)
	cfg.Static.PanelRoot = t.TempDir()
	gw := newTestGateway(t, cfg, sessions, routes, tickets, signer)
	for _, raw := range []string{"", `{}`, `{"entry":"missing.js","expiresAt":"2020-01-01T00:00:00Z"}`} {
		if raw != "" {
			if err := os.WriteFile(filepath.Join(cfg.Static.PanelRoot, "manifest.json"), []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
		}
		res := httptest.NewRecorder()
		gw.Handler().ServeHTTP(res, httptest.NewRequest("GET", "http://portal.example.com/panel/manifest.json", nil))
		if res.Code != 503 {
			t.Fatalf("panel status %d", res.Code)
		}
		res = httptest.NewRecorder()
		gw.Handler().ServeHTTP(res, httptest.NewRequest("GET", "http://portal.example.com/", nil))
		if res.Code == 503 {
			t.Fatal("panel failure affected portal")
		}
	}
	if panelManifestReady(cfg.Static.PanelRoot, time.Now()) {
		t.Fatal("invalid manifest ready")
	}
}
