package main

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

// This optional hop never borrows IAM credentials, even at the same origin.
func (c Config) parseCollabUpstream() (*url.URL, error) {
	raw := c.Upstreams["collab"]
	if raw == "" {
		if c.CollabHop != (IAMHopConfig{}) {
			return nil, fmt.Errorf("collabHop requires upstreams.collab")
		}
		return nil, nil
	}
	if !c.Access.Valid() {
		return nil, fmt.Errorf("collab requires validated Access configuration")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") || u.RawPath != "" || u.Path != "" && u.Path != "/" {
		return nil, fmt.Errorf("invalid collab upstream origin")
	}
	authority := u.Host
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && ip.Is6() && ip.Zone() == "" {
		authority = strings.Replace(authority, "["+u.Hostname()+"]", "collab.example.com", 1)
	}
	if _, _, err := parseAuthority(authority, u.Scheme); err != nil {
		return nil, fmt.Errorf("invalid collab upstream authority: %w", err)
	}
	if u.Scheme == "http" && c.CollabHop.AllowLoopbackHTTP {
		if ip, err := netip.ParseAddr(u.Hostname()); err == nil && ip.IsLoopback() {
			return u, nil
		}
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("collab requires HTTPS mTLS or explicit numeric loopback development HTTP")
	}
	hop := c.CollabHop
	name, _, nameErr := parseAuthority(hop.ServerName, "https")
	if nameErr != nil || !strings.EqualFold(name, hop.ServerName) || net.ParseIP(name) != nil || hop.CertFile == "" || hop.KeyFile == "" || hop.CAFile == "" {
		return nil, fmt.Errorf("collabHop requires fixed DNS serverName, certFile, keyFile and caFile")
	}
	return u, nil
}

func (g *Gateway) proxyNetworkKeys(w http.ResponseWriter, r *http.Request, audience, clientIP, rid string) {
	fail := func(status int, code string) {
		writeProblem(w, problem(status, code, http.StatusText(status), "network keys request rejected", rid))
	}
	path := r.URL.Path
	if g.collabURL == nil || (path != "/api/collab/v1/network/keys" && path != "/api/collab/v1/network/keys/announcement") || r.URL.EscapedPath() != path {
		fail(404, "common.not_found")
		return
	}
	if r.Method != http.MethodGet {
		fail(405, "common.validation_failed")
		return
	}
	// There are no caller-selected realms, roots or targets on these fixed URLs.
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		fail(400, "common.validation_failed")
		return
	}
	entry, ok := r.Context().Value(accessEntryKey{}).(accessEntry)
	if !ok || len(r.Header.Values("Origin")) != 0 && !strictOrigin(r, entry) {
		fail(403, "common.forbidden")
		return
	}
	// Authentication remains the normal API chain. Reject ambiguous credential
	// syntax before forwarding; validation of the principal/tenant stays upstream.
	if values := r.Header.Values("Authorization"); len(values) != 0 {
		if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") || strings.TrimSpace(strings.TrimPrefix(values[0], "Bearer ")) == "" || strings.ContainsAny(strings.TrimPrefix(values[0], "Bearer "), " \t\r\n,") {
			fail(401, "common.unauthenticated")
			return
		}
	}
	g.proxyAPI(w, r, g.collabURL, audience, clientIP, rid)
}
