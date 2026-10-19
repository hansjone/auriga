package main

import (
	"net"
	"net/http"
	"strings"
)

const headerAurigaClientIP = "X-Auriga-Client-IP"
const headerXForwardedFor = "X-Forwarded-For"

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
// Only peers in trustedProxies may supply X-Forwarded-For. Client-supplied
// X-Auriga-Client-IP / X-Real-IP are never trusted (proxies must not forward them).
// From XFF, walk right-to-left and take the first address not in the trusted set.
func ResolveClientIP(r *http.Request, trusted *TrustedProxies) string {
	rip := remoteIP(r)
	if rip == nil {
		return ""
	}
	if trusted == nil || !trusted.contains(rip) {
		return rip.String()
	}
	if v := strings.TrimSpace(r.Header.Get(headerXForwardedFor)); v != "" {
		parts := strings.Split(v, ",")
		for i := len(parts) - 1; i >= 0; i-- {
			cand := strings.TrimSpace(parts[i])
			if !isIPLiteral(cand) {
				continue
			}
			ip := net.ParseIP(cand)
			if ip == nil {
				continue
			}
			if !trusted.contains(ip) {
				return normalizeIP(cand)
			}
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

// rateLimitKey folds IPv6 addresses to their /64 network for bucket keys.
func rateLimitKey(ip string) string {
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil {
		return ip
	}
	if v4 := parsed.To4(); v4 != nil {
		return v4.String()
	}
	masked := parsed.Mask(net.CIDRMask(64, 128))
	return masked.String() + "/64"
}
