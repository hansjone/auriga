package main

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

// This optional origin has its own credentials even when IAM shares its address.
func (c Config) parseAIAdminUpstream() (*url.URL, error) {
	raw := c.Upstreams["ai-admin"]
	if raw == "" {
		if c.AIAdminHop != (IAMHopConfig{}) {
			return nil, fmt.Errorf("aiAdminHop requires upstreams.ai-admin")
		}
		return nil, nil
	}
	if !c.Access.Valid() {
		return nil, fmt.Errorf("ai-admin requires validated Access configuration")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") || u.RawPath != "" || u.Path != "" && u.Path != "/" {
		return nil, fmt.Errorf("invalid ai-admin upstream origin")
	}
	authority := u.Host
	// Access frontend authorities are IPv4/DNS only. The private hop also
	// supports bracketed numeric IPv6; reuse the same strict port validation.
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && ip.Is6() && ip.Zone() == "" {
		authority = strings.Replace(authority, "["+u.Hostname()+"]", "ai.example.com", 1)
	}
	if _, _, err := parseAuthority(authority, u.Scheme); err != nil {
		return nil, fmt.Errorf("invalid ai-admin upstream authority: %w", err)
	}
	if u.Scheme == "http" && c.AIAdminHop.AllowLoopbackHTTP {
		ip, err := netip.ParseAddr(u.Hostname())
		if err == nil && ip.IsLoopback() {
			return u, nil
		}
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("ai-admin requires HTTPS mTLS or explicit numeric loopback development HTTP")
	}
	hop := c.AIAdminHop
	name, _, nameErr := parseAuthority(hop.ServerName, "https")
	if nameErr != nil || !strings.EqualFold(name, hop.ServerName) || net.ParseIP(name) != nil || hop.CertFile == "" || hop.KeyFile == "" || hop.CAFile == "" {
		return nil, fmt.Errorf("aiAdminHop requires fixed DNS serverName, certFile, keyFile and caFile")
	}
	return u, nil
}

func (g *Gateway) proxyAIAdmin(w http.ResponseWriter, r *http.Request, target *url.URL, audience, clientIP, rid string) {
	// Managed-instance markers belong to a separate internal authenticated channel.
	for name := range r.Header {
		if strings.HasPrefix(strings.ToLower(name), "auriga-forwarded-") {
			writeProblem(w, problem(http.StatusForbidden, "common.forbidden", "Forbidden", "instance identity not allowed", rid))
			return
		}
	}
	// Bearer credentials alone cannot turn this browser entry into an admin API.
	name := cookieName(g.requestSecureCookies(r), sessionCookieBase("admin"))
	found := false
	for _, cookie := range r.Cookies() {
		if cookie.Name == name && cookie.Value != "" {
			found = true
		}
	}
	if !found {
		writeProblem(w, problem(http.StatusUnauthorized, "common.unauthenticated", "Unauthorized", "admin browser session required", rid))
		return
	}
	if g.SessionRPC == nil {
		writeProblem(w, problem(http.StatusServiceUnavailable, "common.unavailable", "Service Unavailable", "session service unavailable", rid))
		return
	}
	g.proxyAccessAPI(w, r, target, audience, clientIP, rid)
}
