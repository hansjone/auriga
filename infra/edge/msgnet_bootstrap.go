package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httputil"
	"strings"
)

// Fixed public bootstrap routes use installation signatures in C, never the
// message channel, a browser cookie or an expired service bearer token.
func (g *Gateway) proxyMsgnetBootstrap(w http.ResponseWriter, r *http.Request, rid string) bool {
	method := ""
	switch r.URL.Path {
	case "/api/collab/v2/network/bootstrap/controls", "/api/collab/v2/network/bootstrap/transport", "/api/collab/v2/network/bootstrap/keys":
		method = http.MethodGet
	case "/api/collab/v2/network/bootstrap/materials":
		method = http.MethodPost
	default:
		return false
	}
	fail := func(status int, code string) {
		writeProblem(w, problem(status, code, http.StatusText(status), "installation bootstrap rejected", rid))
	}
	entry, ok := r.Context().Value(accessEntryKey{}).(accessEntry)
	if !ok || entry.scheme != "https" || len(r.Header.Values("Origin")) > 0 && !strictOrigin(r, entry) {
		fail(403, "common.forbidden")
		return true
	}
	if r.URL.EscapedPath() != r.URL.Path || r.URL.RawQuery != "" || r.URL.ForceQuery {
		fail(400, "common.validation_failed")
		return true
	}
	if r.Method != method {
		fail(405, "common.validation_failed")
		return true
	}
	if g.collabURL == nil || g.APITransports[g.collabURL] == nil {
		fail(503, "common.unavailable")
		return true
	}
	if r.Body != nil {
		raw, e := io.ReadAll(io.LimitReader(r.Body, 65537))
		_ = r.Body.Close()
		if e != nil || len(raw) > 65536 || method == http.MethodGet && len(raw) != 0 {
			fail(400, "common.validation_failed")
			return true
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		r.ContentLength = int64(len(raw))
	}
	proxy := newReverseProxy(g.collabURL, g.APITransports[g.collabURL], true)
	base := proxy.Rewrite
	proxy.Rewrite = func(pr *httputil.ProxyRequest) {
		base(pr)
		stripUntrustedEntryHeaders(pr.Out.Header)
		for key := range pr.Out.Header {
			if strings.HasPrefix(strings.ToLower(key), "auriga-") {
				pr.Out.Header.Del(key)
			}
		}
		pr.Out.Header.Del("Authorization")
		pr.Out.Header.Del("Cookie")
		pr.Out.Header.Del("Auriga-Identity")
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			return errString("bootstrap redirect rejected")
		}
		resp.Header.Set("Cache-Control", "no-store")
		resp.Header.Del("Set-Cookie")
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) { fail(503, "common.unavailable") }
	proxy.ServeHTTP(w, r)
	return true
}
