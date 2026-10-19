package main

import (
	"net/http"
	"path"
	"strings"
)

// sanitizeRequestPath rejects traversal / encoded-separator tricks and returns a cleaned path.
// Trailing slash (except "/") is allowed and preserved for SPA routes.
func sanitizeRequestPath(r *http.Request) (string, bool) {
	if r == nil || r.URL == nil {
		return "", false
	}
	// Prefer raw wire forms: EscapedPath() drops RawPath when it disagrees with Path.
	for _, raw := range []string{r.URL.RawPath, r.URL.EscapedPath(), r.RequestURI} {
		if raw == "" {
			continue
		}
		// Strip query from RequestURI
		if i := strings.IndexByte(raw, '?'); i >= 0 {
			raw = raw[:i]
		}
		lower := strings.ToLower(raw)
		if strings.Contains(lower, "%2f") || strings.Contains(lower, "%5c") {
			return "", false
		}
	}
	p := r.URL.Path
	if p == "" {
		p = "/"
	}
	if strings.Contains(p, ";") || strings.Contains(p, `\`) {
		return "", false
	}
	if strings.Contains(p, "//") || strings.Contains(p, "/./") || strings.Contains(p, "/../") {
		return "", false
	}
	if strings.HasSuffix(p, "/.") || strings.HasSuffix(p, "/..") {
		return "", false
	}
	cleaned := path.Clean(p)
	if cleaned != p && cleaned+"/" != p {
		return "", false
	}
	if cleaned+"/" == p {
		return p, true
	}
	return cleaned, true
}
