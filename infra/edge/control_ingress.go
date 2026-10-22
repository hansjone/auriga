package main

import (
	"fmt"
	"net"
	"net/netip"
)

// InstanceControlIngressConfig restricts the control socket's local destination,
// independently of HTTP authority, caller credentials, and proxy headers.
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

func (c Config) prepareControlIngress() (*controlIngressPolicy, error) {
	policy, err := c.parseControlIngress()
	if err != nil || policy == nil {
		return policy, err
	}
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return nil, fmt.Errorf("instanceControlIngress cannot inspect network interfaces: %w", err)
	}
	local := map[netip.Addr]struct{}{}
	for _, address := range addresses {
		if network, ok := address.(*net.IPNet); ok {
			if ip, valid := netip.AddrFromSlice(network.IP); valid {
				local[ip.Unmap()] = struct{}{}
			}
		}
	}
	for address := range policy.denied {
		if _, present := local[address]; !present {
			return nil, fmt.Errorf("instanceControlIngress address is not owned by this network namespace")
		}
	}
	return policy, nil
}

func (p *controlIngressPolicy) allowsLocal(address net.Addr) bool {
	tcp, ok := address.(*net.TCPAddr)
	if !ok || tcp == nil || tcp.Zone != "" || tcp.Port != p.port {
		return false
	}
	ip, ok := netip.AddrFromSlice(tcp.IP)
	if !ok {
		return false
	}
	ip = ip.Unmap()
	// NAS access and instance bridges are IPv4. Do not leave a wildcard IPv6
	// route around the configured IPv4 LAN exclusion.
	if !ip.Is4() || ip.IsUnspecified() {
		return false
	}
	_, denied := p.denied[ip]
	return !denied
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
		if l.policy.allowsLocal(connection.LocalAddr()) {
			return connection, nil
		}
		_ = connection.Close()
	}
}
