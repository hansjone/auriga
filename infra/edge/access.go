package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"

	"github.com/hansjone/auriga/infra/gen/deployment"
	"golang.org/x/net/idna"
)

type accessRoleKey struct{}
type accessEntryKey struct{}
type accessEntry struct {
	role, scheme, host string
	port               int
}

var entryDNSProfile = idna.New(idna.ValidateForRegistration(), idna.VerifyDNSLength(true), idna.Transitional(false))

// ProxyTLSConfig binds an installation proxy identity, independently of its IP.
// The certificate and CA files are existing platform PKI material.
type ProxyTLSConfig struct {
	CAFile      string `json:"caFile"`
	IdentityURI string `json:"identityUri"`
}

func originAuthority(o deployment.Origin) string {
	if o.Scheme == "http" && o.Port == 80 || o.Scheme == "https" && o.Port == 443 {
		return o.Host
	}
	return net.JoinHostPort(o.Host, strconv.Itoa(int(o.Port)))
}

func parseAuthority(raw, scheme string) (string, int, error) {
	if raw == "" || strings.ContainsAny(raw, "@/%\\?#,[] \t\r\n") {
		return "", 0, fmt.Errorf("invalid authority")
	}
	host, p := raw, ""
	if strings.Contains(raw, ":") {
		if strings.Count(raw, ":") != 1 {
			return "", 0, fmt.Errorf("invalid authority")
		}
		host, p, _ = strings.Cut(raw, ":")
		if p == "" {
			return "", 0, fmt.Errorf("empty port")
		}
	}
	if host == "" || len(host) > 253 || strings.HasSuffix(host, ".") {
		return "", 0, fmt.Errorf("invalid host")
	}
	host = strings.ToLower(host)
	numeric := true
	for _, ch := range host {
		if ch != '.' && (ch < '0' || ch > '9') {
			numeric = false
		}
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if !ip.Is4() || ip.String() != host {
			return "", 0, fmt.Errorf("noncanonical IPv4")
		}
	} else {
		if numeric || numericAddressAlias(host) {
			return "", 0, fmt.Errorf("noncanonical IPv4")
		}
		for _, label := range strings.Split(host, ".") {
			if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return "", 0, fmt.Errorf("invalid DNS")
			}
			for _, ch := range label {
				if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && ch != '-' {
					return "", 0, fmt.Errorf("invalid DNS")
				}
			}
		}
		ascii, err := entryDNSProfile.ToASCII(host)
		if err != nil || ascii != host {
			return "", 0, fmt.Errorf("invalid DNS A-label")
		}
	}
	port := 80
	if scheme == "https" {
		port = 443
	}
	if p != "" {
		for _, ch := range p {
			if ch < '0' || ch > '9' {
				return "", 0, fmt.Errorf("invalid port")
			}
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return "", 0, fmt.Errorf("invalid port")
		}
		port = n
	}
	return host, port, nil
}

func numericAddressAlias(host string) bool {
	for _, label := range strings.Split(host, ".") {
		if label == "" {
			return false
		}
		hex := strings.HasPrefix(label, "0x")
		if hex {
			label = strings.TrimPrefix(label, "0x")
			if label == "" {
				return false
			}
		}
		for _, ch := range label {
			if (ch < '0' || ch > '9') && (!hex || ch < 'a' || ch > 'f') {
				return false
			}
		}
	}
	return true
}

func strictOrigin(r *http.Request, e accessEntry) bool {
	values := r.Header.Values("Origin")
	if len(values) != 1 {
		return false
	}
	scheme, authority, found := strings.Cut(values[0], "://")
	if !found || scheme != e.scheme {
		return false
	}
	host, port, err := parseAuthority(authority, scheme)
	return err == nil && host == e.host && port == e.port
}

func stripUntrustedEntryHeaders(h http.Header) {
	for name := range h {
		n := strings.ToLower(name)
		if n == "forwarded" || strings.HasPrefix(n, "x-forwarded-") || strings.HasPrefix(n, "x-auriga-") && n != "x-auriga-csrf" && n != "x-auriga-csrf-bootstrap" || n == "auriga-identity" || n == "x-real-ip" || n == "auriga-tenant-id" || n == "x-tenant-id" || n == "x-audience" {
			h.Del(name)
		}
	}
}

func (g *Gateway) accessHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fail := func(status int, code string) {
			writeProblem(w, problem(status, code, http.StatusText(status), "entry rejected", requestID(r)))
		}
		role, _ := r.Context().Value(accessRoleKey{}).(string)
		var origin deployment.Origin
		switch role {
		case "portal":
			origin = g.Cfg.Access.PortalOrigin()
		case "admin":
			origin = g.Cfg.Access.AdminOrigin()
		case "instanceControl":
			origin = g.Cfg.Access.InstanceControl().Origin
		default:
			fail(403, "common.forbidden")
			return
		}
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		if g.Cfg.Access.IngressKind() == "trusted_proxy" && role != "instanceControl" {
			if !g.authenticatedProxy(r) {
				fail(403, "common.forbidden")
				return
			}
			scheme = origin.Scheme
		}
		host, port, err := parseAuthority(r.Host, scheme)
		if err != nil || len(r.Header.Values("Host")) > 1 {
			fail(400, "common.validation_failed")
			return
		}
		if values := r.Header.Values("Host"); len(values) == 1 {
			h, p, err := parseAuthority(values[0], scheme)
			if err != nil || h != host || p != port {
				fail(400, "common.validation_failed")
				return
			}
		}
		instance := false
		if role == "portal" && host != origin.Host {
			_, instance = g.instanceIDFromHost(host)
		}
		if scheme != origin.Scheme || port != int(origin.Port) || host != origin.Host && !instance {
			fail(403, "common.forbidden")
			return
		}
		stripUntrustedEntryHeaders(r.Header)
		e := accessEntry{role: role, scheme: scheme, host: host, port: port}
		r = r.WithContext(context.WithValue(r.Context(), accessEntryKey{}, e))
		if role == "instanceControl" {
			g.serveInstanceControl(w, r)
			return
		}
		if role == "admin" && isUpgradeRequest(r) {
			fail(403, "common.forbidden")
			return
		}
		// A browser write carrying any Auriga session cookie must never escape
		// Origin validation through an additional Bearer or Fetch Metadata header.
		cookieWrite := isWriteMethod(r.Method) && hasAurigaCookie(r)
		loginWrite := isWriteMethod(r.Method) && strings.HasPrefix(r.URL.Path, "/api/iam/v1/login/")
		// A registered provider's form callback is authenticated by IAM's
		// protocol state/signature and only creates a short-lived resume.
		if r.Method == "POST" && loginCallbackPath.MatchString(r.URL.Path) {
			cookieWrite, loginWrite = false, false
		}
		if (cookieWrite || loginWrite || isUpgradeRequest(r)) && !strictOrigin(r, e) {
			fail(403, "common.forbidden")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func hasAurigaCookie(r *http.Request) bool {
	for _, c := range r.Cookies() {
		if strings.Contains(strings.ToLower(c.Name), "auriga_") {
			return true
		}
	}
	return false
}

func (g *Gateway) authenticatedProxy(r *http.Request) bool {
	if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) == 0 {
		return false
	}
	leaf := r.TLS.PeerCertificates[0]
	if len(leaf.URIs) != 1 || leaf.URIs[0].String() != g.Cfg.ProxyTLS.IdentityURI || g.Cfg.ProxyTLS.IdentityURI == "" {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	for _, prefix := range g.Cfg.Access.ProxyPeerCIDRs() {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}

func (g *Gateway) serveInstanceControl(w http.ResponseWriter, r *http.Request) {
	path, ok := sanitizeRequestPath(r)
	allowed := r.Method == "GET" && path == "/api/workspace/v1/instance/self" || r.Method == "POST" && path == "/api/workspace/v1/instance/heartbeat"
	if !ok || !allowed {
		writeProblem(w, problem(404, "common.not_found", "Not Found", "", requestID(r)))
		return
	}
	auth := r.Header.Values("Authorization")
	if len(auth) != 1 || !strings.HasPrefix(auth[0], "Bearer ait_") || len(auth[0]) <= len("Bearer ait_") {
		writeProblem(w, problem(401, "common.unauthenticated", "Unauthorized", "", requestID(r)))
		return
	}
	r.Header.Del("Cookie")
	r.Header.Del("X-Auriga-CSRF")
	r.Header.Del("X-Auriga-CSRF-Bootstrap")
	transport := g.APITransports[g.workspaceURL]
	if transport == nil {
		writeProblem(w, problem(503, "common.unavailable", "Service Unavailable", "workspace transport unavailable", requestID(r)))
		return
	}
	proxy := newReverseProxy(g.workspaceURL, transport, false)
	proxy.ServeHTTP(w, r)
}
