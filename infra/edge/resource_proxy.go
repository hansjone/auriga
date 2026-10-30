package main

import (
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

// Resource is a platform service and uses the same protected hop as IAM.
func (c Config) parseResourceUpstream() (*url.URL, error) {
	raw := c.Upstreams["resource"]
	if raw == "" {
		return nil, nil
	}
	if !c.Access.Valid() {
		return nil, fmt.Errorf("resource requires validated Access configuration")
	}
	u, err := parseResourceDeviceOrigin(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid resource upstream origin")
	}
	return u, nil
}

func parseResourceDeviceOrigin(raw string) (*url.URL, error) {
	u, err := parseTeamUpstream(raw)
	if err != nil || strings.Contains(raw, "#") {
		return nil, fmt.Errorf("invalid upstream origin")
	}
	authority := u.Host
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && ip.Is6() && ip.Zone() == "" {
		authority = strings.Replace(authority, "["+u.Hostname()+"]", "api.example.com", 1)
	}
	if _, _, err := parseAuthority(authority, u.Scheme); err != nil {
		return nil, fmt.Errorf("invalid upstream authority")
	}
	return u, nil
}

func (g *Gateway) proxyResource(w http.ResponseWriter, r *http.Request, audience, clientIP, rid string) {
	if !strings.HasPrefix(r.URL.Path, "/api/resource/v1/") {
		writeProblem(w, problem(404, "common.not_found", "Not Found", "resource route not allowed", rid))
		return
	}
	if g.resourceURL == nil {
		writeProblem(w, problem(503, "common.unavailable", "Service Unavailable", "resource service not configured", rid))
		return
	}
	for name := range r.Header {
		if strings.HasPrefix(strings.ToLower(name), "auriga-forwarded-") {
			writeProblem(w, problem(403, "common.forbidden", "Forbidden", "instance identity not allowed", rid))
			return
		}
	}
	// Resource READ/UPLOAD, tenant visibility, ETags and recent step-up remain
	// authoritative in the resource service. Edge supplies the current audience
	// session token and the existing domain/IP CSRF policy; it grants no rights.
	g.proxyAccessAPI(w, r, g.resourceURL, audience, clientIP, rid)
}
