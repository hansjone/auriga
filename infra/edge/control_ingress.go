package main

import (
	"fmt"
	"net"
	"net/netip"
	"reflect"
	"sort"
)

// InstanceControlIngressConfig restricts control TCP source/destination pairs
// using current directly connected interface prefixes, independently of HTTP
// authority, caller credentials, and proxy headers.
// It is installation JSON only; there is deliberately no environment override.
type InstanceControlIngressConfig struct {
	DenyLocalIPv4 []string `json:"denyLocalIPv4"`
}

type controlIngressPolicy struct {
	denied map[netip.Addr]struct{}
	port   int
}

func (c Config) parseControlIngress() (*controlIngressPolicy, error) {
	if c.InstanceControlIngress == nil {
		return nil, nil // Existing non-NAS recipes retain their original support.
	}
	if !c.Access.Valid() || c.Access.Listeners().InstanceControl.Address != "0.0.0.0" || len(c.InstanceControlIngress.DenyLocalIPv4) == 0 {
		return nil, fmt.Errorf("instanceControlIngress requires validated wildcard control and nonempty denyLocalIPv4")
	}
	policy := &controlIngressPolicy{denied: map[netip.Addr]struct{}{}, port: int(c.Access.Listeners().InstanceControl.Port)}
	for _, value := range c.InstanceControlIngress.DenyLocalIPv4 {
		address, err := netip.ParseAddr(value)
		if err != nil || !address.Is4() || address.String() != value || address.IsUnspecified() || address.IsMulticast() {
			return nil, fmt.Errorf("instanceControlIngress requires canonical local IPv4 addresses")
		}
		if _, duplicate := policy.denied[address]; duplicate {
			return nil, fmt.Errorf("instanceControlIngress has duplicate local addresses")
		}
		policy.denied[address] = struct{}{}
	}
	return policy, nil
}

// The snapshot identifies address ownership and directly connected IPv4 prefixes,
// not the packet's incoming interface. Docker owns the private bridge topology.
type controlInterfaceAddress struct {
	local  netip.Addr
	prefix netip.Prefix
}
type controlInterface struct {
	index     int
	flags     net.Flags
	addresses []controlInterfaceAddress
}

func controlInterfaces() ([]controlInterface, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	snapshot := make([]controlInterface, 0, len(interfaces))
	for _, iface := range interfaces {
		addresses, err := iface.Addrs()
		if err != nil {
			return nil, err
		}
		entry := controlInterface{index: iface.Index, flags: iface.Flags}
		for _, address := range addresses {
			network, ok := address.(*net.IPNet)
			if !ok {
				return nil, fmt.Errorf("unknown interface address context")
			}
			local, ok := netip.AddrFromSlice(network.IP)
			if !ok {
				return nil, fmt.Errorf("invalid interface address")
			}
			local = local.Unmap()
			if !local.Is4() {
				continue
			}
			ones, bits := network.Mask.Size()
			if bits != 32 || ones < 1 || ones > 32 {
				return nil, fmt.Errorf("invalid IPv4 interface prefix")
			}
			entry.addresses = append(entry.addresses, controlInterfaceAddress{local: local, prefix: netip.PrefixFrom(local, ones).Masked()})
		}
		sort.Slice(entry.addresses, func(i, j int) bool {
			a, b := entry.addresses[i], entry.addresses[j]
			if a.local != b.local {
				return a.local.Less(b.local)
			}
			return a.prefix.Bits() < b.prefix.Bits()
		})
		snapshot = append(snapshot, entry)
	}
	sort.Slice(snapshot, func(i, j int) bool { return snapshot[i].index < snapshot[j].index })
	return snapshot, nil
}

// All configured LAN addresses must still have a unique, live owner. Prefixes
// on different live interfaces cannot overlap, including LAN/private overlap.
func (p *controlIngressPolicy) privateAddresses(snapshot []controlInterface) ([]controlInterfaceAddress, error) {
	lan := map[int]bool{}
	for configured := range p.denied {
		owners := 0
		for _, iface := range snapshot {
			for _, address := range iface.addresses {
				if address.local == configured {
					if iface.index <= 0 || iface.flags&net.FlagUp == 0 {
						return nil, fmt.Errorf("LAN interface unavailable")
					}
					owners++
					lan[iface.index] = true
				}
			}
		}
		if owners != 1 {
			return nil, fmt.Errorf("LAN address ownership is missing or ambiguous")
		}
	}
	type ownedPrefix struct {
		index  int
		prefix netip.Prefix
	}
	var prefixes []ownedPrefix
	var private []controlInterfaceAddress
	for _, iface := range snapshot {
		if iface.flags&net.FlagUp == 0 {
			continue
		}
		if iface.index <= 0 {
			return nil, fmt.Errorf("invalid interface identity")
		}
		if iface.flags&net.FlagLoopback != 0 && !lan[iface.index] {
			continue
		}
		for _, address := range iface.addresses {
			if !address.local.Is4() || !address.prefix.IsValid() || !address.prefix.Addr().Is4() || address.prefix != address.prefix.Masked() || !address.prefix.Contains(address.local) {
				return nil, fmt.Errorf("invalid direct interface prefix")
			}
			for _, previous := range prefixes {
				if previous.index != iface.index && previous.prefix.Overlaps(address.prefix) {
					return nil, fmt.Errorf("overlapping interface prefixes")
				}
			}
			prefixes = append(prefixes, ownedPrefix{index: iface.index, prefix: address.prefix})
			if !lan[iface.index] && iface.flags&(net.FlagLoopback|net.FlagPointToPoint) == 0 && address.local.IsGlobalUnicast() && !address.local.IsLoopback() && !address.local.IsLinkLocalUnicast() && address.prefix.Bits() <= 30 {
				private = append(private, address)
			}
		}
	}
	return private, nil
}

func (c Config) prepareControlIngress() (*controlIngressPolicy, error) {
	policy, err := c.parseControlIngress()
	if err != nil || policy == nil {
		return policy, err
	}
	snapshot, err := controlInterfaces()
	if err != nil {
		return nil, fmt.Errorf("instanceControlIngress cannot inspect interfaces: %w", err)
	}
	if _, err = policy.privateAddresses(snapshot); err != nil {
		return nil, fmt.Errorf("instanceControlIngress: %w", err)
	}
	return policy, nil
}

func controlTCPAddress(address net.Addr) (netip.Addr, int, bool) {
	tcp, ok := address.(*net.TCPAddr)
	if !ok || tcp == nil || tcp.Zone != "" || tcp.Port < 1 || tcp.Port > 65535 {
		return netip.Addr{}, 0, false
	}
	ip, ok := netip.AddrFromSlice(tcp.IP)
	if !ok {
		return netip.Addr{}, 0, false
	}
	ip = ip.Unmap()
	if !ip.Is4() || !ip.IsGlobalUnicast() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return netip.Addr{}, 0, false
	}
	return ip, tcp.Port, true
}

func (p *controlIngressPolicy) allowsConnection(local, remote net.Addr, snapshot []controlInterface) bool {
	destination, port, ok := controlTCPAddress(local)
	if !ok || port != p.port {
		return false
	}
	peer, _, ok := controlTCPAddress(remote)
	if !ok {
		return false
	}
	private, err := p.privateAddresses(snapshot)
	if err != nil {
		return false
	}
	matches := 0
	allowed := false
	for _, address := range private {
		if address.local == destination {
			matches++
			// Only peers on this very direct private prefix are admitted. A routed
			// external source is rejected even when it targets a private local IP.
			allowed = address.prefix.Contains(peer)
		}
	}
	return matches == 1 && allowed
}

func (p *controlIngressPolicy) allows(connection net.Conn) bool {
	first, err := controlInterfaces()
	if err != nil || !p.allowsConnection(connection.LocalAddr(), connection.RemoteAddr(), first) {
		return false
	}
	second, err := controlInterfaces()
	// Avoid retaining a stale startup allowlist as Docker adds/removes bridges.
	// Two equal observations conservatively reject an observed topology change;
	// they do not make network administration atomic with Accept.
	return err == nil && reflect.DeepEqual(first, second) && p.allowsConnection(connection.LocalAddr(), connection.RemoteAddr(), second)
}

type controlIngressListener struct {
	net.Listener
	policy *controlIngressPolicy
}

func (l *controlIngressListener) Accept() (net.Conn, error) {
	for {
		connection, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if connection == nil {
			return nil, fmt.Errorf("control listener returned no connection")
		}
		if l.policy.allows(connection) {
			return connection, nil
		}
		_ = connection.Close()
	}
}
