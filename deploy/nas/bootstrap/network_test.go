package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestNASNetworkSubmitConfiguration(t *testing.T) {
	for _, mode := range []string{"domain", "ip", "proxy"} {
		t.Run(mode, func(t *testing.T) {
			raw, cfg, err := loadNASAccess("../access-"+mode+".example.json", "192.0.2.138", "192.0.2.128/26", 15000)
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
			}
			if err = json.Unmarshal(content, &edge); err != nil {
				t.Fatal(err)
			}
			hop := edge.NetworkSubmitHop
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
