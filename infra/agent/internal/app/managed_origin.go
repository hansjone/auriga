package app

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var managedDNSLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// This input is projected from the same installation-owned origin approved by
// workspace and edge. It never comes from a request, profile or instance spec.
func managedOriginHost(raw, allowed string) (string, error) {
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || !strings.HasPrefix(raw, "https://") || u.Scheme != "https" || u.User != nil || u.Hostname() == "" || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || u.String() != raw || u.Host != strings.ToLower(u.Host) || strings.ContainsAny(raw, "\\\r\n\t ") || !slices.Contains(strings.Split(allowed, ","), raw) {
		return "", fmt.Errorf("managed platform requires an approved canonical HTTPS origin")
	}
	host := u.Hostname()
	isIP := net.ParseIP(host) != nil
	if !isIP {
		if len(host) > 253 {
			return "", fmt.Errorf("managed platform requires a canonical DNS hostname")
		}
		for _, label := range strings.Split(host, ".") {
			if !managedDNSLabel.MatchString(label) {
				return "", fmt.Errorf("managed platform requires a canonical DNS hostname")
			}
		}
	}
	authority := host
	if isIP && strings.Contains(host, ":") {
		authority = "[" + host + "]"
	}
	if port := u.Port(); port != "" {
		n, portErr := strconv.Atoi(port)
		if portErr != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
			return "", fmt.Errorf("managed platform has an invalid TLS port")
		}
		authority = net.JoinHostPort(host, port)
	}
	if u.Host != authority {
		return "", fmt.Errorf("managed platform requires a canonical authority")
	}
	// Approved literal addresses need no Docker DNS alias. Validate the entire
	// origin first so this does not bypass its HTTPS, authority or trust checks.
	if isIP {
		return "", nil
	}
	return host, nil
}
