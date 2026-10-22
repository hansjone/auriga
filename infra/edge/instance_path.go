package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

func canonicalInstanceID(id string) bool {
	if len(id) != 36 || id[14] != '7' || !strings.ContainsRune("89ab", rune(id[19])) {
		return false
	}
	for i, c := range id {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func instanceMount(id string) string { return "/i/" + id + "/" }

// strictInstancePath retains ordinary escaping, but forbids a second decoding
// pass or a different path interpretation by browsers, edge and node ingress.
func strictInstancePath(p, raw string) bool {
	if strings.ContainsAny(p, "\\\x00\r\n") || strings.Contains(strings.ToLower(raw), "%25") {
		return false
	}
	for _, c := range p {
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	u := &url.URL{Path: p, RawPath: raw}
	if strings.Contains(strings.ToLower(u.EscapedPath()), "%25") {
		return false
	}
	if raw != "" {
		decoded, err := url.PathUnescape(raw)
		if err != nil || decoded != p {
			return false
		}
	}
	r := &http.Request{URL: u}
	_, ok := sanitizeRequestPath(r)
	return ok
}

func (g *Gateway) instanceTarget(r *http.Request, id string) InstanceTarget {
	mount := "/"
	if g.Cfg.InstanceParentDomain == "" {
		mount = instanceMount(id)
	}
	if g.Cfg.Access.Valid() {
		o := g.Cfg.Access.PortalOrigin()
		if parent := g.Cfg.Access.InstanceParentDomain(); parent != "" {
			o.Host = id + "." + parent
		}
		return InstanceTarget{Origin: o.Scheme + "://" + originAuthority(o), Mount: mount}
	}
	scheme := "http"
	if g.requestSecureCookies(r) || r.TLS != nil {
		scheme = "https"
	}
	authority := r.Host
	if parent := g.Cfg.InstanceParentDomain; parent != "" {
		authority = id + "." + parent
	}
	host, port, err := parseAuthority(authority, scheme)
	if err != nil {
		return InstanceTarget{}
	}
	// Reuse the installation origin formatter to compare effective ports.
	o := g.Cfg.Access.PortalOrigin()
	o.Scheme, o.Host, o.Port = scheme, host, int32(port)
	return InstanceTarget{Origin: scheme + "://" + originAuthority(o), Mount: mount}
}

func uniqueInstanceCookie(r *http.Request, name string) (string, error) {
	var value string
	count := 0
	for _, c := range r.Cookies() {
		if c.Name == name {
			value = c.Value
			count++
		}
	}
	if count != 1 || value == "" {
		return "", fmt.Errorf("missing or ambiguous instance cookie")
	}
	return value, nil
}

func instanceCookieName(secure bool, mount string) string {
	if secure && mount != "/" {
		return "__Secure-" + instCookieBase()
	}
	return cookieName(secure, instCookieBase())
}
