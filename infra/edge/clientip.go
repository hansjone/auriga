package main

import (
	"net"
	"net/http"
	"strings"
)

const headerAurigaClientIP = "X-Auriga-Client-IP"
const headerXForwardedFor = "X-Forwarded-For"
const headerXRealIP = "X-Real-IP"

// TrustedProxies decides when to honor forwarded client address headers.
type TrustedProxies struct {
	nets []*net.IPNet
	ips  map[string]struct{}
}

func NewTrustedProxies(entries []string) (*TrustedProxies, error) {
	tp := &TrustedProxies{ips: map[string]struct{}{}}
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if ip := net.ParseIP(e); ip != nil {
			tp.ips[ip.String()] = struct{}{}
			continue
		}
		_, n, err := net.ParseCIDR(e)
		if err != nil {
			return nil, err
		}
		tp.nets = append(tp.nets, n)
	}
	return tp, nil
}

func (tp *TrustedProxies) contains(ip net.IP) bool {
	if tp == nil || ip == nil {
		return false
	}
	if _, ok := tp.ips[ip.String()]; ok {
		return true
	}
	for _, n := range tp.nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func remoteIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return net.ParseIP(host)
}

// ResolveClientIP returns the client IP edge should use and propagate.
// Only peers in trustedProxies may supply X-Forwarded-For / X-Real-IP / X-Auriga-Client-IP.
// Untrusted peers' forged X-Auriga-Client-IP is ignored; RemoteAddr is used.
func ResolveClientIP(r *http.Request, trusted *TrustedProxies) string {
	rip := remoteIP(r)
	if rip == nil {
		return ""
	}
	if trusted == nil || !trusted.contains(rip) {
		return rip.String()
	}
	if v := strings.TrimSpace(r.Header.Get(headerAurigaClientIP)); v != "" && isIPLiteral(v) {
		return normalizeIP(v)
	}
	if v := strings.TrimSpace(r.Header.Get(headerXRealIP)); v != "" && isIPLiteral(v) {
		return normalizeIP(v)
	}
	if v := strings.TrimSpace(r.Header.Get(headerXForwardedFor)); v != "" {
		first := strings.TrimSpace(strings.Split(v, ",")[0])
		if isIPLiteral(first) {
			return normalizeIP(first)
		}
	}
	return rip.String()
}

func isIPLiteral(s string) bool {
	return net.ParseIP(s) != nil
}

func normalizeIP(s string) string {
	ip := net.ParseIP(s)
	if ip == nil {
		return s
	}
	return ip.String()
}

// SetClientIPHeader overwrites X-Auriga-Client-IP on the outbound request so backends
// never see a browser-forged value.
func SetClientIPHeader(h http.Header, clientIP string) {
	h.Del(headerAurigaClientIP)
	if clientIP != "" {
		h.Set(headerAurigaClientIP, clientIP)
	}
}
