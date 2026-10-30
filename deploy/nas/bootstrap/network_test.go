package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestNASNetworkSubmitConfiguration(t *testing.T) {
	for _, mode := range []string{"domain", "ip", "proxy", "proxy-port"} {
		t.Run(mode, func(t *testing.T) {
			input := "../access-" + mode + ".example.json"
			if mode == "proxy-port" {
				content, err := os.ReadFile("../access-proxy.example.json")
				if err != nil {
					t.Fatal(err)
				}
				var a map[string]any
				if err := json.Unmarshal(content, &a); err != nil {
					t.Fatal(err)
				}
				for _, key := range []string{"portalOrigin", "issuerOrigin"} {
					a[key].(map[string]any)["port"] = 9443
				}
				a["adminOrigin"].(map[string]any)["port"] = 9444
				a["instances"].(map[string]any)["port"] = 9443
				content, err = json.Marshal(a)
				if err != nil {
					t.Fatal(err)
				}
				input = filepath.Join(t.TempDir(), "access.json")
				if err := os.WriteFile(input, content, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			raw, cfg, err := loadNASAccess(input, "192.0.2.138", "192.0.2.128/26", 15000)
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			writeNASConfig(dir, raw, cfg, "192.0.2.138", 15000, "192.0.2.10", 512)
			content, err := os.ReadFile(filepath.Join(dir, "edge.config.json"))
			if err != nil {
				t.Fatal(err)
			}
			var edge struct {
				Upstreams                map[string]string
				IAMHop, NetworkSubmitHop map[string]string
				NetworkWSS               struct {
					Enabled                               bool
					Path, UpstreamURL, CAFile, ServerName string
					AllowedOrigins                        []string
				} `json:"networkWss"`
			}
			if err = json.Unmarshal(content, &edge); err != nil {
				t.Fatal(err)
			}
			hop := edge.NetworkSubmitHop
			wss := edge.NetworkWSS
			if mode == "proxy-port" && cfg.PortalURL() != "https://portal.example.com:9443" {
				t.Fatal("fixture lost explicit portal port")
			}
			if !wss.Enabled || wss.Path != "/net/v1/ws" || wss.UpstreamURL != "wss://network-broker:18444" || wss.ServerName != "broker.network.internal" || wss.CAFile != "/etc/auriga/pki/network-server-ca.pem" || len(wss.AllowedOrigins) != 1 || wss.AllowedOrigins[0] != cfg.PortalURL() || wss.AllowedOrigins[0] == cfg.AdminURL() {
				t.Fatal("WSS must use internal broker TLS and exact portal Origin, excluding admin")
			}
			if edge.Upstreams["network-submit"] != "https://network:18443" || hop["serverName"] != "submit.network.internal" || hop["caFile"] != "/etc/auriga/pki/network-server-ca.pem" || hop["keyFile"] == edge.IAMHop["keyFile"] {
				t.Fatal("network submit TLS configuration missing or shares IAM material")
			}
		})
	}
	set := nasMaterialSet()
	leaves := map[string]bool{}
	for _, name := range set.PublicFiles {
		leaves[name] = true
	}
	for _, name := range []string{"network/edge.key", "network/client-ca.pem", "network/runtime.json", "network-platform/gateway.secret", "network-platform/platform.properties", "network/broker.conf", "network/broker.pem", "network/broker.key", "network/server-ca.pem", "pki/gateway.key"} {
		if !leaves[name] {
			t.Fatalf("material absent from installation receipt: %s", name)
		}
	}
}
