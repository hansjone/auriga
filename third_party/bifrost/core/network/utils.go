// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package network

import (
	"net"
	"net/netip"
)

var linkLocalSubnet *net.IPNet
var privateSubnets []*net.IPNet

func init() {
	for _, cidr := range []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"169.254.0.0/16",
		"127.0.0.0/8",
	} {
		_, subnet, _ := net.ParseCIDR(cidr)
		privateSubnets = append(privateSubnets, subnet)
	}
	_, linkLocalSubnet, _ = net.ParseCIDR("169.254.0.0/16")
}

func IsLinkLocal(ip net.IP) bool {
	if ip.To4() != nil {
		return linkLocalSubnet.Contains(ip)
	}
	if ip.IsLinkLocalUnicast() {
		return true
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	addr = addr.Unmap()
	if teredo.Contains(addr) {
		return true
	}
	if embedded, ok := embeddedIPv4(addr); ok {
		return embedded.IsLinkLocalUnicast()
	}
	return false
}

func IsPrivateIP(ip net.IP) bool {
	if ip == nil || ip.IsUnspecified() {
		return true
	}
	for _, subnet := range privateSubnets {
		if subnet.Contains(ip) {
			return true
		}
	}

	if ip.To4() == nil {
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			return true
		}
		if len(ip) == 16 && (ip[0]&0xfe) == 0xfc {
			return true
		}
	}
	return false
}
