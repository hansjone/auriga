package upstream

import (
	"net"
	"net/netip"
	"strings"
	"testing"
)

// Numeric examples are only parsed or resolved as literals, never dialed.
func TestNewLiteralHostPolicy(t *testing.T) {
	publicIPv6 := netip.AddrFrom16([16]byte{0x20, 0x03, 0x12, 0x34, 15: 1}).String()
	accepted := []string{
		"198.18.1.2", "169.254.1.1", "fe80::1", "192.0.2.10", "2001:db8::1", "::ffff:198.18.1.2",
		"10.1.2.3", "fc00::1", "100.64.0.1", // gitleaks:allow
		fixturePublicIP().String(), publicIPv6,
		"198.51.100.10", "203.0.113.10", "fd12::1", "::ffff:10.1.2.3", // gitleaks:allow
		"0.0.0.0", "::", "224.0.0.1", "ff02::1", "64:ff9b::c000:20a", "2002:c000:20a::1",
	}
	for _, host := range accepted {
		t.Run("accept/"+host, func(t *testing.T) {
			c, err := New(Config{AllowedHosts: []HostRule{{Host: host}}, MaxResponseBytes: 4096, MaxEventBytes: 2048})
			if err != nil {
				t.Fatalf("New rejected deployment-trusted numeric host: %v", err)
			}
			ips, err := c.resolve(t.Context(), canonicalHost(host))
			if err != nil || len(ips) != 1 || ips[0] != netip.MustParseAddr(host) {
				t.Fatalf("constructor and runtime address policies disagree: %v %v", ips, err)
			}
		})
	}
	for _, host := range []string{
		"127.0.0.1", "127.255.255.254", "::1", "::ffff:127.0.0.1", "::ffff:127.255.255.254",
		"169.254.169.254", "fd00:ec2::254", "168.63.129.16", "100.100.100.200", // gitleaks:allow
		"::ffff:169.254.169.254", "::ffff:168.63.129.16", "::ffff:100.100.100.200", // gitleaks:allow
		"", "*", "*.example.com", "https://models.example.com", "models.example.com:443",
		"[2001:db8::1]", "fe80::1%zone", "::ffff:198.18.1.2%zone", "invalid host", strings.Repeat("a", 254),
	} {
		t.Run("reject/"+host, func(t *testing.T) {
			for _, plaintext := range []bool{false, true} {
				if _, err := New(Config{AllowedHosts: []HostRule{{Host: host, AllowPlaintext: plaintext}}, MaxResponseBytes: 4096, MaxEventBytes: 2048}); err == nil {
					t.Fatalf("New accepted forbidden host with AllowPlaintext=%t", plaintext)
				}
			}
		})
	}
}

func TestNewHostPolicyIsolation(t *testing.T) {
	for _, plaintext := range []bool{false, true} {
		c, err := New(Config{AllowedHosts: []HostRule{{Host: "MoDeLs.ExAmPlE.CoM.", AllowPlaintext: plaintext}, {Host: "198.18.1.2", AllowPlaintext: plaintext}}, MaxResponseBytes: 4096, MaxEventBytes: 2048})
		if err != nil {
			t.Fatal(err)
		}
		for _, address := range []string{"198.18.1.2", "169.254.1.1", "fe80::1", "192.0.2.10", "2001:db8::1", "::ffff:198.18.1.2", "10.1.2.3", "fc00::1", "100.64.0.1"} { // gitleaks:allow
			ip := netip.MustParseAddr(address)
			if !c.allows("models.example.com", ip) || c.allows("other.example.com", ip) || c.allows("sub.models.example.com", ip) {
				t.Fatalf("trusted address policy escaped exact host match for %s", address)
			}
		}
		for _, host := range []string{"models.example.com", "198.18.1.2"} {
			authority := net.JoinHostPort(host, "443")
			if _, err := c.endpoint("https://" + authority); err != nil {
				t.Fatal("HTTPS rejected for deployment host")
			}
			if _, err := c.endpoint("http://" + authority); (err == nil) != plaintext {
				t.Fatalf("HTTP authorization changed for %s", host)
			}
		}
		for _, host := range []string{"other.example.com", "sub.models.example.com", "198.18.1.3"} {
			if _, err := c.endpoint("http://" + host); err == nil {
				t.Fatalf("HTTP trust inherited by unlisted host %s", host)
			}
		}
	}
	defaults, err := New(Config{MaxResponseBytes: 4096, MaxEventBytes: 2048})
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range []netip.Addr{fixturePublicIP(), netip.AddrFrom16([16]byte{0x20, 0x03, 0x12, 0x34, 15: 1})} {
		if !defaults.allows("other.example.com", address) {
			t.Fatal("unlisted public HTTPS address policy changed")
		}
	}
}
