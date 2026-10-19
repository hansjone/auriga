package main

import (
	"net/http"
	"path"
	"strings"
)

// sanitizeRequestPath rejects traversal / encoded-separator tricks and returns a cleaned path.
// Used for portal/admin routing only. Trailing slash (except "/") is preserved for SPA routes.
func sanitizeRequestPath(r *http.Request) (string, bool) {
	if r == nil || r.URL == nil {
		return "", false
	}
	if hasEncodedSep(r) {
		return "", false
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

// instancePathOK allows normal instance app paths (including //, ;, %2f) but still rejects traversal.
func instancePathOK(r *http.Request) bool {
	if r == nil || r.URL == nil {
		return false
	}
	p := r.URL.Path
	if strings.Contains(p, "/../") || strings.HasSuffix(p, "/..") || strings.Contains(p, `\..`) {
		return false
	}
	if path.Clean(p) != p && !strings.Contains(p, "/../") {
		// Clean changed path — only reject when .. was involved
		if strings.Contains(p, "..") {
			return false
		}
	}
	if strings.Contains(p, "..") {
		return false
	}
	// Encoded dot-dot
	for _, raw := range []string{r.URL.RawPath, r.RequestURI} {
		if raw == "" {
			continue
		}
		if i := strings.IndexByte(raw, '?'); i >= 0 {
			raw = raw[:i]
		}
		lower := strings.ToLower(raw)
		if strings.Contains(lower, "%2e%2e") || strings.Contains(lower, "..") {
			return false
		}
	}
	return true
}

func hasEncodedSep(r *http.Request) bool {
	for _, raw := range []string{r.URL.RawPath, r.URL.EscapedPath(), r.RequestURI} {
		if raw == "" {
			continue
		}
		if i := strings.IndexByte(raw, '?'); i >= 0 {
			raw = raw[:i]
		}
		lower := strings.ToLower(raw)
		if strings.Contains(lower, "%2f") || strings.Contains(lower, "%5c") {
			return true
		}
	}
	return false
}
