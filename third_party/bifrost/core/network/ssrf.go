package network

import (
	"context"

	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

type IPLookuper interface {
	LookupIP(ctx context.Context, network, host string) ([]net.IP, error)
}
type Allowlist struct {
	hostnames map[string]struct{}
	prefixes  []netip.Prefix
}
type ProxyAwareTransport struct {
	Proxy    func(*http.Request) (*url.URL, error)
	Direct   *http.Transport
	ViaProxy *http.Transport
}

var teredo = netip.MustParsePrefix("2001:0000::/32")
var metadataEndpoints = []netip.Addr{
	netip.MustParseAddr("100.100.100.200"),
	netip.MustParseAddr("fd00:ec2::254"),
}

type ipLookuper = IPLookuper

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

type PrivateTargetNotAllowedError struct {
	Host string
	IP   net.IP
}

func IsMetadataEndpoint(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	addr = addr.Unmap()
	for _, endpoint := range metadataEndpoints {
		if addr == endpoint {
			return true
		}
	}
	return false
}

func IsPublicIP(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	addr = addr.Unmap()

	if embedded, ok := embeddedIPv4(addr); ok {
		addr = embedded
	}

	if addr.IsLoopback() || addr.IsPrivate() || cgnat.Contains(addr) ||
		addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() ||
		addr.IsMulticast() || addr.IsUnspecified() || addr.IsInterfaceLocalMulticast() ||
		addr == netip.AddrFrom4([4]byte{255, 255, 255, 255}) {
		return false
	}

	if addr.Is6() {
		b := addr.As16()
		if b[0] == 0xfe && (b[1]&0xc0) == 0xc0 {
			return false
		}

		if teredo.Contains(addr) {
			return false
		}
	}

	return true
}

func embeddedIPv4(addr netip.Addr) (netip.Addr, bool) {
	if !addr.Is6() {
		return netip.Addr{}, false
	}
	b := addr.As16()
	switch {
	case b[0] == 0x20 && b[1] == 0x02:
		return netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]}), true
	case b[0] == 0x00 && b[1] == 0x64 && b[2] == 0xff && b[3] == 0x9b:

		if b[4] == 0x00 && b[5] == 0x00 {
			return netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}), true
		}

		if b[4] == 0x00 && b[5] == 0x01 {
			return netip.AddrFrom4([4]byte{b[6], b[7], b[9], b[10]}), true
		}
	}
	return netip.Addr{}, false
}

func SSRFSafeDialContext(dialTimeout time.Duration) func(ctx context.Context, netw, addr string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: dialTimeout}
	return ssrfSafeDialContext(net.DefaultResolver, dialer.DialContext, nil)
}

func ssrfSafeDialContext(resolver ipLookuper, dial func(ctx context.Context, network, addr string) (net.Conn, error), allow *Allowlist) func(ctx context.Context, netw, addr string) (net.Conn, error) {
	return func(ctx context.Context, netw, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("invalid dial address %q: %w", addr, err)
		}
		ips, err := resolver.LookupIP(ctx, "ip", host)
		if err != nil {
			return nil, fmt.Errorf("DNS lookup failed for %s: %w", host, err)
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("DNS lookup for %s returned no addresses", host)
		}
		for _, ip := range ips {
			if !IsPublicIP(ip) && !allow.Permits(host, ip) {
				return nil, fmt.Errorf("blocked connection to non-public address %s (host %s)", ip, host)
			}
		}
		return dial(ctx, netw, net.JoinHostPort(ips[0].String(), port))
	}
}

func ResolvePublicTarget(ctx context.Context, host string) ([]net.IP, error) {
	return publicTargetCheck(net.DefaultResolver, nil)(ctx, host)
}

func publicTargetCheck(resolver ipLookuper, allow *Allowlist) func(ctx context.Context, host string) ([]net.IP, error) {
	return func(ctx context.Context, host string) ([]net.IP, error) {
		ips, err := resolver.LookupIP(ctx, "ip", host)
		if err != nil {
			return nil, fmt.Errorf("DNS lookup failed for %s: %w", host, err)
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("DNS lookup for %s returned no addresses", host)
		}
		for _, ip := range ips {
			if !IsPublicIP(ip) && !allow.Permits(host, ip) {
				return nil, fmt.Errorf("blocked connection to non-public address %s (host %s)", ip, host)
			}
		}
		return ips, nil
	}
}

func (a *Allowlist) Permits(host string, resolvedIP net.IP) bool {
	if a == nil {
		return false
	}
	if _, ok := a.hostnames[strings.ToLower(host)]; ok {
		return true
	}
	addr, ok := netip.AddrFromSlice(resolvedIP)
	if !ok {
		return false
	}
	addr = addr.Unmap()
	if embedded, ok := embeddedIPv4(addr); ok {
		addr = embedded
	}
	for _, prefix := range a.prefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func (e *PrivateTargetNotAllowedError) Error() string {
	return fmt.Sprintf("blocked connection to non-public address %s (host %s)", e.IP, e.Host)
}

func (t *ProxyAwareTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.Proxy != nil {
		proxyURL, err := t.Proxy(req)
		if err != nil {
			return nil, err
		}
		if proxyURL != nil {
			return t.ViaProxy.RoundTrip(req)
		}
	}
	return t.Direct.RoundTrip(req)
}
