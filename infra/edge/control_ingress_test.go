package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/gen/deploymentconfig"
)

func TestControlIngressActualLocalDestination(t *testing.T) {
	for _, mode := range []string{"ip", "domain", "proxy"} {
		t.Run(mode, func(t *testing.T) { testControlIngressActualLocalDestination(t, mode) })
	}
}

func testControlIngressActualLocalDestination(t *testing.T, mode string) {
	cfg, sessions, routes, tickets, signer, _ := testEnv(t)
	var upstreamCalls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ait_control_ingress_test" {
			t.Error("test upstream received an invalid test AIT")
		}
		upstreamCalls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)
	cfg.Upstreams = map[string]string{"iam": upstream.URL, "org": upstream.URL, "workspace": upstream.URL}
	cfg.IAMHop.AllowLoopbackHTTP = true
	// JSON keeps this regression runnable against the old Config: it must fail
	// on actual LAN reachability, rather than a missing Go field at compile time.
	if err := json.Unmarshal([]byte(`{"instanceControlIngress":{"denyLocalIPv4":["127.0.0.1"]}}`), &cfg); err != nil {
		t.Fatal(err)
	}
	fixture := "ip-http.json"
	if mode != "ip" {
		fixture = "domain-proxy.json"
	}
	raw, err := os.ReadFile("../../contracts/deployment/fixtures/" + fixture)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	sockets := map[string]net.Listener{}
	ports := map[string]int{}
	for _, role := range []string{"portal", "admin", "instanceControl"} {
		ln, err := net.Listen("tcp4", "0.0.0.0:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ln.Close() })
		sockets[role] = ln
		ports[role] = ln.Addr().(*net.TCPAddr).Port
		doc["listeners"].(map[string]any)[role] = map[string]any{"address": "0.0.0.0", "port": ports[role], "tls": mode == "proxy" && role != "instanceControl"}
	}
	scheme, portalHost, adminHost := "http", "127.0.0.1", "127.0.0.1"
	if mode != "ip" {
		portalHost, adminHost = "portal.example.com", "admin.example.com"
	}
	var proxyPKI accessPKI
	if mode == "proxy" {
		scheme = "https"
		proxyPKI = createAccessPKI(t)
		cfg.TLS = writeAccessServerTLS(t, proxyPKI)
		cfg.ProxyTLS = ProxyTLSConfig{CAFile: proxyPKI.hop.CAFile, IdentityURI: "auriga://proxy/test"}
		doc["ingress"] = map[string]any{"kind": "trusted_proxy", "peerCidrs": []string{"127.0.0.1/32"}, "requireMtls": true, "preserveAuthority": true}
	} else {
		doc["ingress"] = map[string]any{"kind": "direct"}
	}
	doc["insecureHttpAcknowledged"] = mode != "proxy"
	if mode != "ip" {
		doc["instances"] = map[string]any{"mode": "subdomain", "parentDomain": "i.example.com", "scheme": scheme, "port": ports["portal"]}
	}
	doc["portalOrigin"] = map[string]any{"scheme": scheme, "host": portalHost, "port": ports["portal"]}
	doc["issuerOrigin"] = doc["portalOrigin"]
	doc["adminOrigin"] = map[string]any{"scheme": scheme, "host": adminHost, "port": ports["admin"]}
	control := doc["instanceControl"].(map[string]any)
	control["origin"].(map[string]any)["port"] = ports["instanceControl"]
	raw, _ = json.Marshal(doc)
	cfg.Access, err = deploymentconfig.Load(raw)
	if err != nil {
		t.Fatal(err)
	}
	cfg.PortalHost, cfg.AdminHost = originAuthority(cfg.Access.PortalOrigin()), originAuthority(cfg.Access.AdminOrigin())
	cfg.SecureCookies = mode == "proxy"
	gateway := newTestGateway(t, cfg, sessions, routes, tickets, signer)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	server := &Server{Cfg: cfg, Handler: gateway.Handler(), Listen: func(_, address string) (net.Listener, error) {
		for role, port := range ports {
			if address == net.JoinHostPort("0.0.0.0", strconv.Itoa(port)) {
				return sockets[role], nil
			}
		}
		return nil, fmt.Errorf("unexpected test listener")
	}}
	go func() { done <- server.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("control listener failed to stop")
		}
	})
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	t.Cleanup(client.CloseIdleConnections)
	request := func(target string) (*http.Response, error) {
		req, _ := http.NewRequest("GET", "http://"+net.JoinHostPort(target, strconv.Itoa(ports["instanceControl"]))+"/api/workspace/v1/instance/self", nil)
		req.Host = originAuthority(cfg.Access.InstanceControl().Origin)
		req.Header.Set("Authorization", "Bearer ait_control_ingress_test")
		req.Header.Set("X-Forwarded-Host", "127.0.0.2")
		req.Header.Set("X-Forwarded-For", "127.0.0.2")
		return client.Do(req)
	}
	// Loopback aliases are no longer a private-prefix positive control. Real
	// private and dynamic-interface positives run in the explicit Linux netns test.
	for _, target := range []string{"127.0.0.1", "127.0.0.2"} {
		response, err := request(target)
		if err == nil {
			_ = response.Body.Close()
			t.Errorf("non-private target reached HTTP with status %d", response.StatusCode)
		}
	}
	if upstreamCalls.Load() != 0 {
		t.Fatal("non-private connection reached HTTP/upstream")
	}
	// The same LAN target remains available to both original browser roles.
	for _, role := range []string{"portal", "admin"} {
		transport := &http.Transport{Proxy: nil}
		if mode == "proxy" {
			transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: proxyPKI.roots, ServerName: "iam.internal.test", Certificates: []tls.Certificate{proxyPKI.proxy}}
		}
		browser := &http.Client{Timeout: 2 * time.Second, Transport: transport}
		t.Cleanup(browser.CloseIdleConnections)
		req, _ := http.NewRequest("GET", scheme+"://"+net.JoinHostPort("127.0.0.1", strconv.Itoa(ports[role]))+"/", nil)
		if role == "portal" {
			req.Host = cfg.PortalHost
		} else {
			req.Host = cfg.AdminHost
		}
		resp, err := browser.Do(req)
		if err != nil {
			t.Fatalf("%s browser listener regressed: %v", role, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("%s browser listener status %d", role, resp.StatusCode)
		}
	}
}

func controlIngressValidationConfig(t *testing.T) Config {
	t.Helper()
	cfg, _, _, _, _, _ := testEnv(t)
	raw, err := os.ReadFile("../../contracts/deployment/fixtures/ip-http.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, value := range doc["listeners"].(map[string]any) {
		value.(map[string]any)["address"] = "0.0.0.0"
	}
	raw, _ = json.Marshal(doc)
	cfg.Access, err = deploymentconfig.Load(raw)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestControlIngressConfigurationAndStartup(t *testing.T) {
	for _, values := range [][]string{nil, {""}, {"::ffff:127.0.0.1"}, {"127.000.0.1"}, {"0.0.0.0"}, {"224.0.0.1"}, {"127.0.0.1/32"}, {"127.0.0.1", "127.0.0.1"}} {
		cfg := controlIngressValidationConfig(t)
		cfg.InstanceControlIngress = &InstanceControlIngressConfig{DenyLocalIPv4: values}
		if _, err := cfg.parseControlIngress(); err == nil {
			t.Errorf("invalid control ingress accepted: %v", values)
		}
	}
	cfg := controlIngressValidationConfig(t)
	cfg.InstanceControlIngress = &InstanceControlIngressConfig{DenyLocalIPv4: []string{"203.0.113.254"}}
	listened := false
	server := &Server{Cfg: cfg, Listen: func(string, string) (net.Listener, error) {
		listened = true
		return nil, fmt.Errorf("unexpected listen")
	}}
	if err := server.Run(context.Background()); err == nil || listened {
		t.Fatal("foreign interface address must fail before opening any browser/control socket")
	}
	cfg.InstanceControlIngress = &InstanceControlIngressConfig{DenyLocalIPv4: []string{"127.0.0.1"}}
	t.Setenv("AURIGA_EDGE_INSTANCE_CONTROL_DENY_LOCAL_IPV4", "203.0.113.254")
	applyEnv(&cfg)
	if cfg.InstanceControlIngress.DenyLocalIPv4[0] != "127.0.0.1" {
		t.Fatal("environment overrode trusted control ingress configuration")
	}
	if _, err := cfg.prepareControlIngress(); err != nil {
		t.Fatal(err)
	}
	cfg.Access = deploymentconfig.Config{}
	if _, err := cfg.parseControlIngress(); err == nil {
		t.Fatal("control ingress accepted missing deployment context")
	}
	cfg.InstanceControlIngress = nil
	if policy, err := cfg.parseControlIngress(); err != nil || policy != nil {
		t.Fatal("non-NAS recipe support changed")
	}
}

func controlTestInterfaces() []controlInterface {
	return []controlInterface{
		{index: 1, flags: net.FlagUp, addresses: []controlInterfaceAddress{{local: netip.MustParseAddr("192.0.2.10"), prefix: netip.MustParsePrefix("192.0.2.0/26")}}},
		{index: 2, flags: net.FlagUp, addresses: []controlInterfaceAddress{{local: netip.MustParseAddr("192.0.2.138"), prefix: netip.MustParsePrefix("192.0.2.128/26")}}},
	}
}
func TestControlIngressKernelAddressNormalization(t *testing.T) {
	policy := &controlIngressPolicy{port: 8083, denied: map[netip.Addr]struct{}{netip.MustParseAddr("192.0.2.10"): {}}}
	tcp := func(ip string, port int) net.Addr { return &net.TCPAddr{IP: net.ParseIP(ip), Port: port} }
	for _, test := range []struct {
		name          string
		local, remote net.Addr
		allow         bool
	}{
		{"private", tcp("192.0.2.138", 8083), tcp("192.0.2.148", 40000), true},
		{"LAN-target", tcp("192.0.2.10", 8083), tcp("192.0.2.20", 40000), false},
		{"routed-LAN", tcp("192.0.2.138", 8083), tcp("192.0.2.20", 40000), false},
		{"routed-external", tcp("192.0.2.138", 8083), tcp("198.51.100.20", 40000), false},
		{"mapped-private", tcp("::ffff:192.0.2.138", 8083), tcp("::ffff:192.0.2.148", 40000), true},
		{"mapped-LAN-source", tcp("::ffff:192.0.2.138", 8083), tcp("::ffff:192.0.2.20", 40000), false},
		{"unknown-local", tcp("192.0.2.139", 8083), tcp("192.0.2.148", 40000), false},
		{"wrong-port", tcp("192.0.2.138", 8084), tcp("192.0.2.148", 40000), false},
		{"missing-local", nil, tcp("192.0.2.148", 40000), false},
		{"missing-peer", tcp("192.0.2.138", 8083), nil, false},
		{"wrong-type", &net.UnixAddr{Name: "private", Net: "tcp"}, tcp("192.0.2.148", 40000), false},
		{"missing-IP", &net.TCPAddr{Port: 8083}, tcp("192.0.2.148", 40000), false},
		{"IPv6-local", tcp("2001:db8::1", 8083), tcp("192.0.2.148", 40000), false},
		{"IPv6-peer", tcp("192.0.2.138", 8083), tcp("2001:db8::2", 40000), false},
		{"zoned", &net.TCPAddr{IP: net.ParseIP("192.0.2.138"), Zone: "untrusted", Port: 8083}, tcp("192.0.2.148", 40000), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if policy.allowsConnection(test.local, test.remote, controlTestInterfaces()) != test.allow {
				t.Fatal("incorrect direct-private decision")
			}
		})
	}
}
func TestControlIngressInterfaceTopology(t *testing.T) {
	policy := &controlIngressPolicy{port: 8083, denied: map[netip.Addr]struct{}{netip.MustParseAddr("192.0.2.10"): {}}}
	local := &net.TCPAddr{IP: net.ParseIP("192.0.2.138"), Port: 8083}
	peer := &net.TCPAddr{IP: net.ParseIP("192.0.2.148"), Port: 40000}
	for _, test := range []struct {
		name   string
		change func([]controlInterface) []controlInterface
	}{
		{"missing-LAN", func(s []controlInterface) []controlInterface { return s[1:] }},
		{"missing-private", func(s []controlInterface) []controlInterface { return s[:1] }},
		{"down-LAN", func(s []controlInterface) []controlInterface { s[0].flags = 0; return s }},
		{"down-private", func(s []controlInterface) []controlInterface { s[1].flags = 0; return s }},
		{"loopback-private", func(s []controlInterface) []controlInterface { s[1].flags |= net.FlagLoopback; return s }},
		{"ambiguous-LAN", func(s []controlInterface) []controlInterface { return append(s, s[0]) }},
		{"ambiguous-private", func(s []controlInterface) []controlInterface { return append(s, s[1]) }},
		{"overlap", func(s []controlInterface) []controlInterface {
			s[0].addresses[0].prefix = netip.MustParsePrefix("192.0.2.0/24")
			return s
		}},
		{"nonmatching-prefix", func(s []controlInterface) []controlInterface {
			s[1].addresses[0].prefix = netip.MustParsePrefix("198.51.100.0/24")
			return s
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if policy.allowsConnection(local, peer, test.change(controlTestInterfaces())) {
				t.Fatal("unsafe topology accepted")
			}
		})
	}
}

func TestControlIngressCloseUnblocksAccept(t *testing.T) {
	listener, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	wrapped := &controlIngressListener{Listener: listener, policy: &controlIngressPolicy{port: port, denied: map[netip.Addr]struct{}{netip.MustParseAddr("127.0.0.1"): {}}}}
	done := make(chan error, 1)
	go func() {
		connection, err := wrapped.Accept()
		if connection != nil {
			_ = connection.Close()
		}
		done <- err
	}()
	connection, err := net.DialTimeout("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = connection.Close()
	_ = wrapped.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("closed control listener did not return its Accept error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("closed control listener left Accept blocked")
	}
}
