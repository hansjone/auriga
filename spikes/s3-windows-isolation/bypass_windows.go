//go:build windows && amd64

package main

import (
	"golang.org/x/sys/windows"
	"net"
	"unsafe"
)

func udpEcho() (net.PacketConn, error) {
	p, e := net.ListenPacket("udp4", "127.0.0.1:0")
	if e != nil {
		return nil, e
	}
	go func() {
		b := make([]byte, 128)
		for {
			n, addr, e := p.ReadFrom(b)
			if e != nil {
				return
			}
			_, _ = p.WriteTo(b[:n], addr)
		}
	}()
	return p, nil
}
func nativeDNS(name string) uint32 {
	dll := windows.NewLazySystemDLL("dnsapi.dll")
	var record uintptr
	v, _, _ := dll.NewProc("DnsQuery_W").Call(uintptr(unsafe.Pointer(u16(name))), 1, 8, 0, uintptr(unsafe.Pointer(&record)), 0)
	if record != 0 {
		dll.NewProc("DnsRecordListFree").Call(record, 1)
	}
	return status32(v)
}
func networkBypassProbes(f *fixture, r *Result, udpAddress string, dnsBaseline map[string]any) error {
	data, e := runWork(f, "udp", udpAddress)
	if e != nil {
		return e
	}
	r.check("UDP_nonallowlisted_requires_WSAEACCES", win32Code(data[udpAddress]) == 10013, data[udpAddress])
	dns, e := runWork(f, "dns", "example.com")
	if e != nil {
		return e
	}
	baseline, ok := dnsBaseline["example.com"].(map[string]any)
	if ok && baseline["win32Error"] == float64(0) {
		code := win32Code(dns["example.com"])
		r.check("DNS_bypass_cache_must_be_denied", code == 5 || code == 10013, dns["example.com"])
	} else {
		r.unsupported("DNS_positive_control", dnsBaseline)
	}
	r.unsupported("external_UNC_WebDAV_BITS_WPAD_gate", "No second disposable authenticated SMB/WebDAV/proxy endpoint is configured. SID-only ALE filtering is not a proof against system-service forwarding; production needs machine-wide egress policy and WebClient/BITS/DNS policy. Do not test an uncontrolled deployment.")
	return nil
}
