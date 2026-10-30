package main

import (
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
)

type linkOriginKey struct{}

func (c Config) validateLinkOrigins() error {
	for _, origin := range c.LinkOrigins {
		u, err := url.Parse(origin)
		if err != nil || u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("invalid link origin")
		}
		host, port, err := parseAuthority(u.Host, u.Scheme)
		authority := host
		if u.Scheme == "http" && port != 80 || u.Scheme == "https" && port != 443 {
			authority += ":" + strconv.Itoa(port)
		}
		if err != nil || origin != u.Scheme+"://"+authority {
			return fmt.Errorf("link origin must be canonical HTTP(S) origin")
		}
	}
	for _, peer := range c.LinkTrustedProxies {
		if _, err := netip.ParseAddr(peer); err == nil {
			continue
		}
		prefix, err := netip.ParsePrefix(peer)
		if err != nil || prefix.Bits() == 0 || prefix != prefix.Masked() {
			return fmt.Errorf("link proxy must be a canonical numeric IP or nonzero CIDR")
		}
	}
	return nil
}

// Presentation trust is deliberately separate from client-address and entry authentication.
func (c Config) linkOrigin(r *http.Request, scheme string) string {
	if len(c.LinkOrigins) == 0 {
		return ""
	}
	proxies, err := NewTrustedProxies(c.LinkTrustedProxies)
	if err != nil {
		return ""
	}
	host := r.Host
	if proxies.contains(remoteIP(r)) {
		if len(r.Header.Values("X-Forwarded-Host")) != 1 || len(r.Header.Values("X-Forwarded-Proto")) != 1 {
			return ""
		}
		host, scheme = r.Header.Get("X-Forwarded-Host"), r.Header.Get("X-Forwarded-Proto")
	}
	candidate := scheme + "://" + host
	for _, allowed := range c.LinkOrigins {
		if allowed == candidate {
			return candidate
		}
	}
	return ""
}
