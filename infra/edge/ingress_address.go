package main

import (
	"context"
	"fmt"
	"net"
	"net/netip"
)

// Check the entire DNS answer before sending even a TCP SYN. IPv4-mapped
// IPv6 answers are normalized so they cannot bypass IPv4 restrictions.
func validateIngressAddresses(addresses []netip.Addr, allowLocal bool) error {
	if len(addresses) == 0 {
		return fmt.Errorf("ingress resolved no addresses")
	}
	for _, address := range addresses {
		address = address.Unmap()
		if !address.IsValid() || address.IsMulticast() || address.IsUnspecified() || address.IsLinkLocalUnicast() || address.Zone() != "" || (!allowLocal && address.IsLoopback()) {
			return fmt.Errorf("ingress resolved a restricted address")
		}
	}
	return nil
}

func dialResolvedIngress(ctx context.Context, dialer *net.Dialer, network, port string, addresses []netip.Addr, allowLocal bool) (net.Conn, error) {
	if err := validateIngressAddresses(addresses, allowLocal); err != nil {
		return nil, err
	}
	var last error
	for _, address := range addresses {
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(address.Unmap().String(), port))
		if err == nil {
			return conn, nil
		}
		last = err
		if ctx.Err() != nil {
			break
		}
	}
	return nil, last
}
