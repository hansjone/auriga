package main

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"unicode"
)

func (c Config) parseDeviceUpstream() (*url.URL, error) {
	raw := c.Upstreams["ai-device"]
	if raw == "" {
		return nil, nil
	}
	if !c.Access.Valid() {
		return nil, fmt.Errorf("ai-device requires validated Access configuration")
	}
	u, err := parseResourceDeviceOrigin(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid ai-device upstream origin")
	}
	return u, nil
}

func isDevicePath(path string) bool {
	return path == "/ai/v1" || strings.HasPrefix(path, "/ai/v1/") || path == "/api/ai/v1/me/usage" || path == "/api/ai/v1/network/submit"
}

func deviceMethod(path string) string {
	switch path {
	case "/ai/v1/models", "/api/ai/v1/me/usage":
		return http.MethodGet
	case "/ai/v1/chat/completions", "/api/ai/v1/network/submit":
		return http.MethodPost
	default:
		return ""
	}
}

func (g *Gateway) proxyDevice(w http.ResponseWriter, r *http.Request, audience, clientIP, rid string) {
	fail := func(status int, code string) {
		writeProblem(w, problem(status, code, http.StatusText(status), "device AI request rejected", rid))
	}
	entry, ok := r.Context().Value(accessEntryKey{}).(accessEntry)
	if audience != "portal" || !ok || entry.role != "portal" {
		fail(403, "common.forbidden")
		return
	}
	// Reject encoded aliases before route selection. No path normalization or
	// prefix expansion can turn a new endpoint into device access.
	if r.URL.RawPath != "" || r.URL.EscapedPath() != r.URL.Path {
		fail(400, "common.validation_failed")
		return
	}
	method := deviceMethod(r.URL.Path)
	if method == "" {
		fail(404, "common.not_found")
		return
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		fail(405, "common.validation_failed")
		return
	}
	if r.URL.Path != "/api/ai/v1/me/usage" && (r.URL.RawQuery != "" || r.URL.ForceQuery) {
		fail(400, "common.validation_failed")
		return
	}
	// Native Node requests only. Never strip browser credentials into an allowed
	// request: reject their presence just as the device runtime does.
	for name := range r.Header {
		n := strings.ToLower(name)
		// Deployment proxies may supply their configured Origin to native
		// submits. This does not admit browser cookies or other browser headers.
		if n == "origin" && r.URL.Path == "/api/ai/v1/network/submit" && g.Cfg.bearerOriginAllowed(r) {
			continue
		}
		// Node's native fetch automatically sends this single transport value,
		// even with credentials omitted. It conveys no browser session identity.
		if n == "sec-fetch-mode" {
			values := r.Header.Values(name)
			if len(values) == 1 && values[0] == "cors" {
				continue
			}
		}
		if n == "cookie" || n == "cookie2" || n == "origin" || n == "referer" || strings.HasPrefix(n, "sec-fetch-") || strings.HasPrefix(n, "access-control-") || strings.HasPrefix(n, "auriga-forwarded-") {
			fail(403, "common.forbidden")
			return
		}
	}
	auth := r.Header.Values("Authorization")
	if len(auth) != 1 || !strings.HasPrefix(auth[0], "Bearer ") {
		fail(401, "common.unauthenticated")
		return
	}
	token := strings.TrimPrefix(auth[0], "Bearer ")
	if len(token) == 0 || len(token) > 16384 || strings.ContainsAny(token, ",") || strings.ContainsFunc(token, unicode.IsSpace) {
		fail(401, "common.unauthenticated")
		return
	}
	target, transport := g.deviceURL, apiProxyTransport
	if configured := g.APITransports[target]; configured != nil {
		transport = configured
	}
	if r.URL.Path == "/api/ai/v1/network/submit" {
		target = g.networkSubmitURL
		transport = g.APITransports[target]
	}
	if target == nil || transport == nil {
		fail(503, "common.unavailable")
		return
	}
	if r.Body != nil && r.Body != http.NoBody {
		r.Body = http.MaxBytesReader(w, r.Body, maxAPIBodyBytes)
	}
	proxy := newReverseProxy(target, transport, false)
	base := proxy.Rewrite
	proxy.Rewrite = func(pr *httputil.ProxyRequest) {
		base(pr)
		stripUntrustedEntryHeaders(pr.Out.Header)
		pr.Out.Header.Del("Origin")
		pr.Out.Header.Del("X-Auriga-CSRF")
		pr.Out.Header.Del("X-Auriga-CSRF-Bootstrap")
		rewriteOutboundAPI(pr.Out, clientIP, token, rid, ensureTraceparent(r))
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		writeProblem(w, problem(503, "common.unavailable", "Service Unavailable", "device AI service unavailable", requestID(r)))
	}
	proxy.ModifyResponse = func(response *http.Response) error {
		response.Header.Del("Set-Cookie")
		response.Header.Set("Cache-Control", "no-store")
		if response.StatusCode >= 300 && response.StatusCode < 400 {
			return fmt.Errorf("device AI redirect rejected")
		}
		return nil
	}
	proxy.ServeHTTP(w, r)
}
