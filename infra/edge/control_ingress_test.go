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
	response, err := request("127.0.0.2")
	if err != nil {
		t.Fatalf("private target rejected: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent || upstreamCalls.Load() != 1 {
		t.Fatal("private target did not reach authenticated test upstream")
	}
	response, err = request("127.0.0.1")
	if err == nil {
		response.Body.Close()
		t.Errorf("LAN target reached HTTP with status %d", response.StatusCode)
	}
	if upstreamCalls.Load() != 1 {
		t.Error("LAN target reached upstream before isolation")
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
		resp.Body.Close()
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

func TestControlIngressKernelAddressNormalization(t *testing.T) {
	policy := &controlIngressPolicy{port: 8083, denied: map[netip.Addr]struct{}{netip.MustParseAddr("127.0.0.1"): {}}}
	for _, test := range []struct {
		name    string
		address net.Addr
		allow   bool
	}{
		{"private", &net.TCPAddr{IP: net.ParseIP("127.0.0.2"), Port: 8083}, true},
		{"LAN", &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8083}, false},
		{"mapped-LAN", &net.TCPAddr{IP: net.ParseIP("::ffff:127.0.0.1"), Port: 8083}, false},
		{"mapped-private", &net.TCPAddr{IP: net.ParseIP("::ffff:127.0.0.2"), Port: 8083}, true},
		{"wrong-port", &net.TCPAddr{IP: net.ParseIP("127.0.0.2"), Port: 8084}, false},
		{"missing", nil, false},
		{"wrong-type", &net.UnixAddr{Name: "127.0.0.2:8083", Net: "tcp"}, false},
		{"missing-IP", &net.TCPAddr{Port: 8083}, false},
		{"IPv6", &net.TCPAddr{IP: net.ParseIP("::1"), Port: 8083}, false},
		{"zoned", &net.TCPAddr{IP: net.ParseIP("127.0.0.2"), Zone: "untrusted", Port: 8083}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if policy.allowsLocal(test.address) != test.allow {
				t.Fatal("incorrect actual local target decision")
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
			connection.Close()
		}
		done <- err
	}()
	connection, err := net.DialTimeout("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	connection.Close()
	wrapped.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("closed control listener did not return its Accept error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("closed control listener left Accept blocked")
	}
}
