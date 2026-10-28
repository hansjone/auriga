package main

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNetworkSubmitConfiguration(t *testing.T) {
	pki, other := createAccessPKI(t), createAccessPKI(t)
	access, _, _ := accessFixture(t, false, nil)
	badCA := filepath.Join(t.TempDir(), "bad-ca.crt")
	if err := os.WriteFile(badCA, []byte("invalid CA"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, origin string
		change       func(*Config)
	}{
		{"no-access", "https://network.example.com", func(c *Config) { c.Access = Config{}.Access }},
		{"missing-hop", "https://network.example.com", func(c *Config) { c.NetworkSubmitHop = IAMHopConfig{}; c.IAMHop = pki.hop }},
		{"orphan-hop", "", nil},
		{"userinfo", "https://user@network.example.com", nil},
		{"path", "https://network.example.com/submit", nil},
		{"query", "https://network.example.com?x=1", nil},
		{"empty-query", "https://network.example.com?", nil},
		{"fragment", "https://network.example.com#fragment", nil},
		{"empty-fragment", "https://network.example.com#", nil},
		{"escaped-root", "https://network.example.com/%2f", nil},
		{"invalid-port", "https://network.example.com:0", nil},
		{"plaintext-loopback", "http://127.0.0.1:12345", nil},
		{"plaintext-switch", "http://127.0.0.1:12345", func(c *Config) { c.NetworkSubmitHop.AllowLoopbackHTTP = true }},
		{"https-with-fallback", "https://network.example.com", func(c *Config) { c.NetworkSubmitHop.AllowLoopbackHTTP = true }},
		{"missing-cert", "https://network.example.com", func(c *Config) { c.NetworkSubmitHop.CertFile = "" }},
		{"missing-key", "https://network.example.com", func(c *Config) { c.NetworkSubmitHop.KeyFile = "" }},
		{"missing-ca", "https://network.example.com", func(c *Config) { c.NetworkSubmitHop.CAFile = "" }},
		{"missing-name", "https://network.example.com", func(c *Config) { c.NetworkSubmitHop.ServerName = "" }},
		{"ip-name", "https://network.example.com", func(c *Config) { c.NetworkSubmitHop.ServerName = "127.0.0.1" }},
		{"name-with-port", "https://network.example.com", func(c *Config) { c.NetworkSubmitHop.ServerName = "network.example.com:443" }},
		{"unreadable-cert", "https://network.example.com", func(c *Config) { c.NetworkSubmitHop.CertFile = "missing" }},
		{"invalid-ca", "https://network.example.com", func(c *Config) { c.NetworkSubmitHop.CAFile = badCA }},
		{"mismatched-key", "https://network.example.com", func(c *Config) { c.NetworkSubmitHop.KeyFile = other.hop.KeyFile }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := access.Cfg
			cfg.Upstreams = map[string]string{"iam": cfg.Upstreams["iam"], "org": cfg.Upstreams["org"], "workspace": cfg.Upstreams["workspace"], "network-submit": tc.origin}
			cfg.NetworkSubmitHop = pki.hop
			if tc.change != nil {
				tc.change(&cfg)
			}
			if _, err := NewGateway(cfg, access.Sessions, access.Routes, access.Tickets, access.Signer, nil); err == nil {
				t.Fatal("invalid network submit configuration accepted")
			}
		})
	}
	for _, origin := range []string{"https://network.example.com", "https://127.0.0.1:12345", "https://[::1]:12345"} {
		cfg := access.Cfg
		cfg.Upstreams = map[string]string{"iam": cfg.Upstreams["iam"], "org": cfg.Upstreams["org"], "workspace": cfg.Upstreams["workspace"], "network-submit": origin}
		cfg.NetworkSubmitHop = pki.hop
		cfg.IdentityKeysFile = pki.hop.KeyFile // Validation requires a nonempty installation-owned path.
		if err := cfg.validate(); err != nil {
			t.Fatal(err)
		}
		g, err := NewGateway(cfg, access.Sessions, access.Routes, access.Tickets, access.Signer, nil)
		if err != nil {
			t.Fatal(err)
		}
		closeAIAdminTransports(t, g)
		tr := g.APITransports[g.networkSubmitURL].(*http.Transport)
		if tr.Proxy != nil || tr == apiProxyTransport || tr.TLSClientConfig == nil || tr.TLSClientConfig.RootCAs == nil || tr.TLSClientConfig.ServerName != pki.hop.ServerName || len(tr.TLSClientConfig.Certificates) != 1 || tr.ResponseHeaderTimeout != 30*time.Second || tr.TLSHandshakeTimeout != 10*time.Second {
			t.Fatal("network submit lost independent TLS trust or device timeout bounds")
		}
	}
}

func TestNetworkSubmitEnvironment(t *testing.T) {
	values := map[string]string{
		"AURIGA_EDGE_UPSTREAM_NETWORK_SUBMIT":    "https://network.example.com:8443",
		"AURIGA_EDGE_NETWORK_SUBMIT_CERT_FILE":   "edge-network.crt",
		"AURIGA_EDGE_NETWORK_SUBMIT_KEY_FILE":    "edge-network.key",
		"AURIGA_EDGE_NETWORK_SUBMIT_CA_FILE":     "network-ca.crt",
		"AURIGA_EDGE_NETWORK_SUBMIT_SERVER_NAME": "network.example.com",
	}
	for key, value := range values {
		t.Setenv(key, value)
	}
	var cfg Config
	applyEnv(&cfg)
	if cfg.Upstreams["network-submit"] != values["AURIGA_EDGE_UPSTREAM_NETWORK_SUBMIT"] || cfg.NetworkSubmitHop != (IAMHopConfig{CertFile: "edge-network.crt", KeyFile: "edge-network.key", CAFile: "network-ca.crt", ServerName: "network.example.com"}) {
		t.Fatal("dedicated network submit environment was not loaded")
	}
	var decoded Config
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded.NetworkSubmitHop != cfg.NetworkSubmitHop || decoded.Upstreams["network-submit"] != cfg.Upstreams["network-submit"] {
		t.Fatal("dedicated hop JSON configuration did not round-trip")
	}
}

func TestNetworkSubmitTLSFailureClosed(t *testing.T) {
	for _, domain := range []bool{false, true} {
		for _, failure := range []string{"wrong-ca", "wrong-name", "wrong-client-ca", "missing-client-cert"} {
			t.Run(map[bool]string{false: "ip", true: "domain"}[domain]+"/"+failure, func(t *testing.T) {
				pki, other := createAccessPKI(t), createAccessPKI(t)
				var calls atomic.Int32
				server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
				server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pki.server}, ClientCAs: pki.roots, ClientAuth: tls.RequireAndVerifyClientCert}
				server.StartTLS()
				t.Cleanup(server.Close)
				g, addresses, _ := accessFixtureConfigured(t, domain, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("TLS failure fell back to platform/models") }), func(c *Config, _ map[string]any) {
					c.Upstreams["network-submit"], c.NetworkSubmitHop = server.URL, pki.hop
					c.Upstreams["ai-device"] = c.Upstreams["iam"]
					switch failure {
					case "wrong-ca":
						c.NetworkSubmitHop.CAFile = other.hop.CAFile
					case "wrong-name":
						c.NetworkSubmitHop.ServerName = "wrong.example.com"
					case "wrong-client-ca":
						c.NetworkSubmitHop.CertFile, c.NetworkSubmitHop.KeyFile = other.hop.CertFile, other.hop.KeyFile
					}
				})
				closeAIAdminTransports(t, g)
				if failure == "missing-client-cert" {
					g.APITransports[g.networkSubmitURL].(*http.Transport).TLSClientConfig.Certificates = nil
				}
				status, _, body := aiAdminRequest(t, g, addresses, "portal", "POST", "/api/ai/v1/network/submit", http.Header{"Authorization": {"Bearer submit-device"}})
				if status != 503 || !strings.Contains(body, `"code":"common.unavailable"`) || calls.Load() != 0 {
					t.Fatalf("TLS failure reached HTTP: status=%d body=%s calls=%d", status, body, calls.Load())
				}
			})
		}
	}
}
