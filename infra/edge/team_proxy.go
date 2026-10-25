package main

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"strings"
	"unicode"
)

// The optional Team upstream is one installation-owned origin, never a request URL.
func parseTeamUpstream(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u == nil || u.Host == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.RawPath != "" || u.Path != "" && u.Path != "/" {
		return nil, fmt.Errorf("invalid ai-gateway upstream origin")
	}
	if u.Scheme != "https" {
		address, parseErr := netip.ParseAddr(u.Hostname())
		if u.Scheme != "http" || parseErr != nil || !address.IsLoopback() {
			return nil, fmt.Errorf("ai-gateway upstream requires HTTPS or numeric loopback HTTP")
		}
	}
	return u, nil
}

func teamMethod(path, method string) (bool, bool) {
	switch path {
	case "/ai/mcp":
		return true, method == http.MethodGet || method == http.MethodPost
	case "/ai/team/node/register", "/ai/team/node/bind", "/ai/team/node/pull", "/ai/team/node/ack", "/ai/team/node/revoke":
		return true, method == http.MethodPost
	default:
		return false, false
	}
}

func (g *Gateway) proxyTeam(w http.ResponseWriter, r *http.Request, audience, clientIP, rid string) {
	fail := func(status int, code string) {
		writeProblem(w, problem(status, code, http.StatusText(status), "Team request rejected", rid))
	}
	entry, entryOK := r.Context().Value(accessEntryKey{}).(accessEntry)
	if audience != "portal" || !entryOK || entry.role != "portal" {
		fail(403, "common.forbidden")
		return
	}
	known, allowed := teamMethod(r.URL.Path, r.Method)
	if !known {
		fail(404, "common.not_found")
		return
	}
	if !allowed {
		w.Header().Set("Allow", "POST")
		if r.URL.Path == "/ai/mcp" {
			w.Header().Set("Allow", "GET, POST")
		}
		fail(405, "common.validation_failed")
		return
	}
	if r.URL.RawQuery != "" || len(r.Header.Values("Origin")) != 0 && !strictOrigin(r, entry) {
		fail(403, "common.forbidden")
		return
	}
	if g.teamURL == nil {
		fail(503, "common.unavailable")
		return
	}
	authorization := r.Header.Values("Authorization")
	if len(authorization) != 1 || !strings.HasPrefix(authorization[0], "Bearer ") || len(authorization[0]) <= len("Bearer ") || strings.ContainsFunc(strings.TrimPrefix(authorization[0], "Bearer "), unicode.IsSpace) || strings.Contains(authorization[0], ",") {
		fail(401, "common.unauthenticated")
		return
	}
	// The remote gateway verifies this credential and current membership for each
	// operation. A portal cookie is never exchanged for a Team credential here.
	if r.Body != nil && r.Body != http.NoBody {
		r.Body = http.MaxBytesReader(w, r.Body, maxAPIBodyBytes)
	}
	proxy := newReverseProxy(g.teamURL, apiProxyTransport, false)
	base := proxy.Rewrite
	proxy.Rewrite = func(pr *httputil.ProxyRequest) {
		base(pr)
		stripUntrustedEntryHeaders(pr.Out.Header)
		pr.Out.Header.Del("Cookie")
		pr.Out.Header.Del("X-Auriga-CSRF")
		pr.Out.Header.Del("X-Auriga-CSRF-Bootstrap")
		rewriteOutboundAPI(pr.Out, clientIP, strings.TrimPrefix(authorization[0], "Bearer "), rid, ensureTraceparent(r))
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		writeProblem(w, problem(503, "common.unavailable", "Service Unavailable", "Team service unavailable", requestID(r)))
	}
	proxy.ModifyResponse = func(response *http.Response) error {
		response.Header.Del("Set-Cookie")
		return nil
	}
	proxy.ServeHTTP(w, r)
}
