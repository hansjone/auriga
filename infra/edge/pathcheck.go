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

// instancePathOK allows normal instance app paths (including //, ;, %2f, and names like a..b)
// but still rejects path segments that are exactly ".." (and encoded forms of that segment).
func instancePathOK(r *http.Request) bool {
	if r == nil || r.URL == nil {
		return false
	}
	if hasDotDotSegment(r.URL.Path) {
		return false
	}
	for _, raw := range []string{r.URL.RawPath, r.RequestURI} {
		if raw == "" {
			continue
		}
		if i := strings.IndexByte(raw, '?'); i >= 0 {
			raw = raw[:i]
		}
		lower := strings.ToLower(raw)
		// Encoded segment ".." only — not substrings inside filenames.
		if strings.Contains(lower, "/%2e%2e/") || strings.HasSuffix(lower, "/%2e%2e") ||
			strings.HasPrefix(lower, "%2e%2e/") || lower == "%2e%2e" ||
			strings.Contains(lower, "/%2e./") || strings.Contains(lower, "/%.%2e/") {
			return false
		}
		if hasDotDotSegment(raw) {
			return false
		}
	}
	return true
}

func hasDotDotSegment(p string) bool {
	if p == "" {
		return false
	}
	p = strings.ReplaceAll(p, `\`, "/")
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
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
