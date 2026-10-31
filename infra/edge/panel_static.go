package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (g *Gateway) servePanelStatic(w http.ResponseWriter, r *http.Request, path string) bool {
	if path != "/panel" && !strings.HasPrefix(path, "/panel/") {
		return false
	}
	if g.Cfg.Static.PanelRoot == "" {
		http.Error(w, "panel unavailable: static root not configured", http.StatusServiceUnavailable)
		return true
	}
	r2 := r.Clone(r.Context())
	r2.URL.Path = strings.TrimPrefix(path, "/panel")
	r2.URL.RawPath = ""
	g.panelStatic.ServeHTTP(w, r2)
	return true
}

// A signed bundle has no SPA fallback or directory index. Only the manifest and
// content-addressed JS emitted by the installation signer are public.
func newPanelFileServer(root string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/")
		bundle := strings.HasPrefix(name, "auriga-team-panel-") && strings.HasSuffix(name, ".js")
		if name != "manifest.json" && !bundle || strings.ContainsAny(name, "/\\") {
			http.NotFound(w, r)
			return
		}
		if !panelManifestReady(root, time.Now()) {
			w.Header().Set("Cache-Control", "no-store")
			http.Error(w, "panel unavailable: missing, invalid or expired manifest/bundle", http.StatusServiceUnavailable)
			return
		}
		file := filepath.Join(root, name)
		info, err := os.Lstat(file)
		if err != nil || !info.Mode().IsRegular() {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeFile(w, r, file)
	})
}

// Availability only. Plugin verification remains responsible for Ed25519/SRI.
func panelManifestReady(root string, now time.Time) bool {
	path := filepath.Join(root, "manifest.json")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 256<<10 {
		return false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var m struct {
		Entry     string                           `json:"entry"`
		IssuedAt  string                           `json:"issuedAt"`
		ExpiresAt string                           `json:"expiresAt"`
		Signature struct{ Alg, Kid, Value string } `json:"signature"`
	}
	if json.Unmarshal(raw, &m) != nil || m.Signature.Alg != "EdDSA" || m.Signature.Kid == "" || m.Signature.Value == "" || !strings.HasPrefix(m.Entry, "auriga-team-panel-") || !strings.HasSuffix(m.Entry, ".js") || strings.ContainsAny(m.Entry, "/\\") {
		return false
	}
	expires, err := time.Parse(time.RFC3339, m.ExpiresAt)
	if err != nil || !now.Before(expires) {
		return false
	}
	issued, err := time.Parse(time.RFC3339, m.IssuedAt)
	if err != nil || now.Before(issued) || !issued.Before(expires) {
		return false
	}
	info, err = os.Lstat(filepath.Join(root, m.Entry))
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}
