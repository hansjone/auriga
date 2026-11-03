// Package proxy implements the managed instance's public HTTPS CONNECT exit.
package proxy

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
)

var errTarget = errors.New("egress target denied")

// In addition to ordinary private/local ranges, reject special-purpose address
// space that can name local translators, metadata services or non-public routes.
var denied = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3fff::/20"),
}

func publicAddress(address netip.Addr) bool {
	address = address.Unmap()
	if !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.Zone() != "" {
		return false
	}
	// Native global IPv6 only; do not permit IPv4-translation or local-use ranges.
	if address.Is6() && !netip.MustParsePrefix("2000::/3").Contains(address) {
		return false
	}
	for _, prefix := range denied {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

func targetHost(authority string) (string, error) {
	host, port, err := net.SplitHostPort(authority)
	if err != nil || port != "443" || host == "" || strings.ContainsAny(host, "%/\\@?#\x00\r\n\t ") {
		return "", errTarget
	}
	if address, parseErr := netip.ParseAddr(host); parseErr == nil {
		if !publicAddress(address) {
			return "", errTarget
		}
		return address.Unmap().String(), nil
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if len(host) > 253 || !strings.Contains(host, ".") {
		return "", errTarget
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", errTarget
		}
		for _, c := range label {
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return "", errTarget
			}
		}
	}
	return host, nil
}

type resolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

func publicTargets(ctx context.Context, dns resolver, host string) ([]string, error) {
	addresses, err := dns.LookupNetIP(ctx, "ip", host)
	if err != nil || len(addresses) == 0 || len(addresses) > 64 {
		return nil, errTarget
	}
	result := make([]string, 0, len(addresses))
	for _, address := range addresses {
		if !publicAddress(address) {
			return nil, errTarget
		}
		result = append(result, net.JoinHostPort(address.Unmap().String(), "443"))
	}
	return result, nil
}
