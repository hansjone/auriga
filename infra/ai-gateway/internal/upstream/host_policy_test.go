package upstream

import (
	"context"
	"crypto/rand"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"testing"
)

func TestAllowedHostAddressPolicy(t *testing.T) {
	for _, address := range []string{"198.18.1.2", "10.1.2.3", "fc00::1", "100.64.0.1", "169.254.1.1", "fe80::1", "192.0.2.10", "2001:db8::1", "0.0.0.0", "::", "224.0.0.1", "ff02::1"} { // gitleaks:allow
		t.Run(address, func(t *testing.T) {
			s := privateServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, completion()) }))
			defer s.Close()
			c, err := New(Config{AllowedHosts: []HostRule{{Host: "models.example.com", AllowPlaintext: true}}, MaxResponseBytes: 4096, MaxEventBytes: 2048})
			if err != nil {
				t.Fatal(err)
			}
			ip := netip.MustParseAddr(address)
			lookups, dials := 0, 0
			c.lookup = func(context.Context, string) ([]netip.Addr, error) { lookups++; return []netip.Addr{ip}, nil }
			c.dial = func(ctx context.Context, network, target string) (net.Conn, error) {
				dials++
				h, _, err := net.SplitHostPort(target)
				if err != nil || h != address {
					t.Error("dial was not pinned to selected IP")
				}
				conn, err := (&net.Dialer{}).DialContext(ctx, network, s.Listener.Addr().String())
				if err != nil {
					return nil, err
				}
				return &reportedRemote{Conn: conn, remote: &net.TCPAddr{IP: net.IP(ip.AsSlice()), Port: 443}}, nil
			}
			x, err := c.Transport(t.Context(), submission("http://models.example.com", false), []byte(rand.Text()))
			_ = readExchange(t, x, err)
			if lookups != 2 || dials != 1 {
				t.Fatalf("lookup/dial counts: %d/%d", lookups, dials)
			}
		})
	}
	for _, address := range []string{"127.0.0.1", "127.255.255.254", "::1", "::ffff:127.0.0.1", "169.254.169.254", "fd00:ec2::254", "168.63.129.16", "100.100.100.200", "::ffff:169.254.169.254"} { // gitleaks:allow
		for _, phase := range []string{"initial", "dial_resolution", "peer"} {
			t.Run(address+"/"+phase, func(t *testing.T) {
				s := privateServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, completion()) }))
				defer s.Close()
				c := clientFor(t)
				c.allowed["models.example.com"] = HostRule{Host: "models.example.com", AllowPlaintext: true}
				lookups, dials := 0, 0
				c.lookup = func(context.Context, string) ([]netip.Addr, error) {
					lookups++
					if phase == "initial" || (phase == "dial_resolution" && lookups > 1) {
						return []netip.Addr{netip.MustParseAddr(address)}, nil
					}
					return []netip.Addr{fixturePublicIP()}, nil
				}
				c.dial = func(ctx context.Context, network, _ string) (net.Conn, error) {
					dials++
					conn, err := (&net.Dialer{}).DialContext(ctx, network, s.Listener.Addr().String())
					if err != nil {
						return nil, err
					}
					return &reportedRemote{Conn: conn, remote: &net.TCPAddr{IP: net.ParseIP(address), Port: 443}}, nil
				}
				x, err := c.Transport(t.Context(), submission("http://models.example.com", false), []byte(rand.Text()))
				if x.Body != nil {
					_ = x.Body.Close()
				}
				if err == nil || (phase != "peer" && dials != 0) {
					t.Fatal("forbidden address reached request")
				}
			})
		}
	}
	c, _ := New(Config{AllowedHosts: []HostRule{{Host: "models.example.com"}}, MaxResponseBytes: 4096, MaxEventBytes: 2048})
	if _, err := c.endpoint("http://models.example.com"); err == nil {
		t.Fatal("allowlist implicitly enabled plaintext")
	}
	if c.allows("other.example.com", netip.MustParseAddr("198.18.1.2")) || c.allows("sub.models.example.com", netip.MustParseAddr("192.0.2.10")) {
		t.Fatal("host trust inherited outside exact match")
	}
	if c.allows("models.example.com", netip.Addr{}) || c.allows("models.example.com", netip.MustParseAddr("fe80::1%zone")) {
		t.Fatal("invalid or scoped address accepted")
	}
	if _, err := c.endpoint("https://" + strings.ToUpper("models.example.com")); err != nil {
		t.Fatal(err)
	}
}
