package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func (g *Gateway) servePanelStatic(w http.ResponseWriter, r *http.Request, path string) bool {
	if path != "/panel" && !strings.HasPrefix(path, "/panel/") {
		return false
	}
	if g.Cfg.Static.PanelRoot == "" {
		writeProblem(w, problem(404, "edge.panel_unconfigured", "Not Found", "panel static root not configured", requestID(r)))
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
