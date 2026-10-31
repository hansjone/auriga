package main

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/net/html"
)

// spaFileServer serves a directory with index.html fallback for non-API GETs.
// It refuses to serve paths that look like secrets (service-roles, keys).
type spaFileServer struct {
	root       http.Dir
	fs         http.Handler
	accessMode string
}

func newAccessSPAFileServer(root, mode string) http.Handler {
	h := newSPAFileServer(root)
	if s, ok := h.(*spaFileServer); ok {
		s.accessMode = mode
	}
	return h
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
		if s.accessMode != "" && upath == "/index.html" {
			s.serveAccessIndex(w, r, full)
			return
		}
		if serveGzipStaticFile(w, r, full, fi) {
			return
		}
		s.fs.ServeHTTP(w, r)
		return
	}
	// directory or missing → SPA fallback
	index := filepath.Join(string(s.root), "index.html")
	if _, err := os.Stat(index); err != nil {
		writeProblem(w, problem(http.StatusNotFound, "edge.not_found", "Not Found", "", requestID(r)))
		return
	}
	if s.accessMode != "" {
		s.serveAccessIndex(w, r, index)
		return
	}
	http.ServeFile(w, r, index)
}

func (s *spaFileServer) serveAccessIndex(w http.ResponseWriter, r *http.Request, filename string) {
	raw, err := os.ReadFile(filename)
	if err == nil {
		raw, err = injectAccessMode(raw, s.accessMode)
	}
	if err != nil {
		writeProblem(w, problem(503, "common.unavailable", "Service Unavailable", "SPA access configuration unavailable", requestID(r)))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", fmt.Sprint(len(raw)))
	if r.Method != http.MethodHead {
		_, _ = w.Write(raw)
	}
}

func injectAccessMode(raw []byte, mode string) ([]byte, error) {
	if mode != "domain" && mode != "ip" {
		return nil, fmt.Errorf("invalid access mode")
	}
	document, err := html.Parse(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	var head, meta *html.Node
	count := 0
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "head" {
			head = node
		}
		if node.Type == html.ElementNode && node.Data == "meta" {
			name, content := "", ""
			for _, attr := range node.Attr {
				if attr.Key == "name" {
					name = attr.Val
				}
				if attr.Key == "content" {
					content = attr.Val
				}
			}
			if name == "auriga-access-mode" {
				count++
				meta = node
				if content != "domain" && content != "ip" {
					count += 2
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(document)
	if head == nil || count > 1 {
		return nil, fmt.Errorf("ambiguous SPA access mode")
	}
	if meta == nil {
		meta = &html.Node{Type: html.ElementNode, Data: "meta"}
		head.AppendChild(meta)
	}
	meta.Attr = []html.Attribute{{Key: "name", Val: "auriga-access-mode"}, {Key: "content", Val: mode}}
	var result bytes.Buffer
	if err := html.Render(&result, document); err != nil {
		return nil, err
	}
	return result.Bytes(), nil
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
