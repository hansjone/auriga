package proxy

import (
	"context"
	"encoding/binary"
	"net"
	"net/netip"
	"reflect"
	"testing"
	"time"
)

type fakeDNS struct{ addresses []netip.Addr }

func (d fakeDNS) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return d.addresses, nil
}

func TestTargetAuthorityAndPublicAddresses(t *testing.T) {
	for _, authority := range []string{
		"api.example.com:80", "api.example.com:0443", "localhost:443", "127.1:443",
		"user@api.example.com:443", "api.example.com:443/path", "api.example.com..:443",
		"127.0.0.1:443", "10.0.0.1:443", "169.254.169.254:443", "100.64.0.1:443",
		"198.18.0.1:443", "192.0.2.1:443", "[::1]:443", "[::ffff:127.0.0.1]:443",
		"[fe80::1%eth0]:443", "[64:ff9b::a00:1]:443", "[2002:a00:1::1]:443", "[2001:db8::1]:443", "[3fff::191]:443",
	} {
		t.Run(authority, func(t *testing.T) {
			host, err := targetHost(authority)
			// Unusual IPv4 spellings still pass through DNS and are checked by
			// publicTargets before any dial, just like ordinary DNS names.
			if authority == "127.1:443" {
				if _, err = publicTargets(t.Context(), fakeDNS{[]netip.Addr{netip.MustParseAddr("127.0.0.1")}}, host); err == nil {
					t.Fatal("obfuscated loopback accepted")
				}
				return
			}
			if err == nil {
				t.Fatal("unsafe authority accepted")
			}
		})
	}
	for _, authority := range []string{"API.EXAMPLE.COM.:443", "8.8.8.8:443", "[2001:4860:4860::8888]:443"} {
		if _, err := targetHost(authority); err != nil {
			t.Fatal(authority, err)
		}
	}
}

func TestExplicitResolverIsTrustedNumericEndpoint(t *testing.T) {
	for _, endpoint := range []string{"resolver.example.com:53", "0.0.0.0:53", "[::]:53", "224.0.0.1:53", "8.8.8.8:0", "8.8.8.8:domain", "[fe80::1%eth0]:53"} {
		if _, err := WithDNSServer(endpoint); err == nil {
			t.Fatal("unsafe resolver endpoint accepted", endpoint)
		}
	}
	for _, endpoint := range []string{"", "8.8.8.8:53", "192.0.2.53:53", "[fd00::53]:53", "[2001:4860:4860::8888]:53"} {
		if _, err := WithDNSServer(endpoint); err != nil {
			t.Fatal("valid application resolver denied", endpoint, err)
		}
	}
}

func TestConfiguredResolverUsesOnlyThePinnedApplicationEndpoint(t *testing.T) {
	packet, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = packet.Close() }()
	done := make(chan struct{})
	go func() {
		defer close(done)
		buffer := make([]byte, 4096)
		for {
			n, peer, readErr := packet.ReadFrom(buffer)
			if readErr != nil {
				return
			}
			if n < 17 {
				continue
			}
			end := 12
			for end < n && buffer[end] != 0 {
				end += 1 + int(buffer[end])
			}
			end += 5 // zero label, question type, class
			if end > n {
				continue
			}
			kind := binary.BigEndian.Uint16(buffer[end-4 : end-2])
			response := append([]byte(nil), buffer[:end]...)
			binary.BigEndian.PutUint16(response[2:4], 0x8180)
			binary.BigEndian.PutUint16(response[6:8], 0)
			binary.BigEndian.PutUint16(response[8:10], 0)
			binary.BigEndian.PutUint16(response[10:12], 0)
			if kind == 1 {
				binary.BigEndian.PutUint16(response[6:8], 1)
				response = append(response, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 0, 0, 4, 8, 8, 8, 8)
			}
			_, _ = packet.WriteTo(response, peer)
		}
	}()
	s, err := WithDNSServer(packet.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	got, err := publicTargets(ctx, s.dns, "api.example.com")
	if err != nil || len(got) != 1 || got[0] != "8.8.8.8:443" {
		t.Fatal("configured resolver was bypassed", got, err)
	}
	_ = packet.Close()
	<-done
	ctx, cancel = context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if _, err = publicTargets(ctx, s.dns, "api.example.com"); err == nil {
		t.Fatal("closed application DNS fell back to ambient DNS")
	}
}

func TestDNSValidatesEveryAnswerAndPinsNumericAddresses(t *testing.T) {
	for _, blocked := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "192.0.2.1", "::ffff:10.0.0.1", "fd00::1", "2001:db8::1"} {
		dns := fakeDNS{[]netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr(blocked)}}
		if _, err := publicTargets(t.Context(), dns, "api.example.com"); err == nil {
			t.Fatal("mixed public/private DNS accepted", blocked)
		}
	}
	addresses := []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("2001:4860:4860::8888")}
	got, err := publicTargets(t.Context(), fakeDNS{addresses}, "api.example.com")
	if err != nil || !reflect.DeepEqual(got, []string{"8.8.8.8:443", "[2001:4860:4860::8888]:443"}) {
		t.Fatal(got, err)
	}
	if _, err = publicTargets(t.Context(), fakeDNS{}, "api.example.com"); err == nil {
		t.Fatal("empty DNS accepted")
	}
}
