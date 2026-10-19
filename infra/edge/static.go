package main

import (
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// spaFileServer serves a directory with index.html fallback for non-API GETs.
// It refuses to serve paths that look like secrets (service-roles, keys).
type spaFileServer struct {
	root http.Dir
	fs   http.Handler
}

func newSPAFileServer(root string) http.Handler {
	if root == "" {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeProblem(w, problem(http.StatusNotFound, "edge.static_unconfigured", "Not Found", "static root not configured", requestID(r)))
		})
	}
	d := http.Dir(root)
	return &spaFileServer{root: d, fs: http.FileServer(d)}
}

func (s *spaFileServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeProblem(w, problem(http.StatusMethodNotAllowed, "edge.method_not_allowed", "Method Not Allowed", "", requestID(r)))
		return
	}
	upath := path.Clean("/" + r.URL.Path)
	if blockedStaticPath(upath) {
		writeProblem(w, problem(http.StatusNotFound, "edge.not_found", "Not Found", "", requestID(r)))
		return
	}
	full := filepath.Join(string(s.root), filepath.FromSlash(upath))
	fi, err := os.Stat(full)
	if err == nil && !fi.IsDir() {
		s.fs.ServeHTTP(w, r)
		return
	}
	// directory or missing → SPA fallback
	index := filepath.Join(string(s.root), "index.html")
	if _, err := os.Stat(index); err != nil {
		writeProblem(w, problem(http.StatusNotFound, "edge.not_found", "Not Found", "", requestID(r)))
		return
	}
	http.ServeFile(w, r, index)
}

func blockedStaticPath(p string) bool {
	lp := strings.ToLower(p)
	blocked := []string{
		"service-roles",
		"credentials.env",
		"identity-keys",
		"identity-signing",
		".env",
		"/keys/",
		"/secrets/",
	}
	for _, b := range blocked {
		if strings.Contains(lp, b) {
			return true
		}
	}
	return false
}
