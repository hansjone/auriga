package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hansjone/auriga/deploy/bootstrap/collab"
	"github.com/hansjone/auriga/deploy/bootstrap/collab/testfixture"
	networkdeploy "github.com/hansjone/auriga/deploy/bootstrap/network"
	"github.com/hansjone/auriga/deploy/bootstrap/relayui"
)

func TestNASCollabProjectionModes(t *testing.T) {
	for _, mode := range []string{"domain", "ip", "proxy"} {
		t.Run(mode, func(t *testing.T) {
			raw, cfg, err := loadNASAccess("../access-"+mode+".example.json", "192.0.2.138", "192.0.2.128/26", 15000)
			if err != nil {
				t.Fatal(err)
			}
			input, err := collab.Read(testfixture.Write(t, cfg.TenantID()), cfg.TenantID())
			if err != nil {
				t.Fatal(err)
			}
			defer input.Clear()
			base, out := t.TempDir(), t.TempDir()
			mustMkdir(filepath.Join(base, "pki"), 0755)
			mustPKI(filepath.Join(base, "pki"))
			rpc := map[string]string{"Target": "platform:9091", "ServiceTokenFile": "/run/auriga-network-token/gateway/service.jwt", "CertFile": "/etc/auriga/pki/gateway.crt", "KeyFile": "/etc/auriga/pki/gateway.key", "CAFile": "/etc/auriga/pki/ca.crt", "ServerName": "platform"}
			if err := networkdeploy.Generate(filepath.Join(base, "network"), networkdeploy.Options{PlatformDirectory: filepath.Join(base, "network-platform"), Root: "/etc/auriga/network", SubmitListen: "0.0.0.0:18443", BrokerHost: "0.0.0.0", BrokerURL: "tls://network-broker:14224", BrokerPort: 14224, WebsocketPort: 18444, Audience: "auriga-network", IAM: rpc, Collab: rpc, ExportServiceToken: true}); err != nil {
				t.Fatal(err)
			}
			ui, err := relayui.Resolve(cfg, "")
			if err != nil {
				t.Fatal(err)
			}
			writeNASConfig(base, raw, cfg, "192.0.2.138", 15000, "192.0.2.10", 512, ui)
			if err := writeNASCollab(out, base, input, cfg, strings.Replace(cfg.PortalURL(), "http://", "https://", 1)); err != nil {
				t.Fatal(err)
			}
			read := func(name string) string {
				b, e := os.ReadFile(filepath.Join(out, name))
				if e != nil {
					t.Fatal(e)
				}
				return string(b)
			}
			p := read("collab-installation/collab.properties")
			for _, want := range []string{"jdbc:postgresql://postgres:5432/auriga", "auriga_collab_app", "auriga_collab_owner", "server.address=0.0.0.0", "auriga.grpc.server.host=0.0.0.0", "https://platform:8081/oauth2/jwks", "auriga.security.jwt.issuer=" + cfg.Issuer()} {
				if !strings.Contains(p, want) {
					t.Fatalf("missing %s", want)
				}
			}
			if strings.Contains(p, "e2e_collab") || strings.Contains(p, "15432") {
				t.Fatal("compose database escaped into NAS")
			}
			var edge map[string]any
			if err := json.Unmarshal([]byte(read("edge.config.json")), &edge); err != nil {
				t.Fatal(err)
			}
			if edge["upstreams"].(map[string]any)["collab"] != "https://collab:8085" {
				t.Fatal("collab route missing")
			}
			if edge["networkWss"].(map[string]any)["allowedOrigins"].([]any)[0] != cfg.PortalURL() {
				t.Fatal("portal origin changed")
			}
			var gateway map[string]any
			_ = json.Unmarshal([]byte(read("network/runtime.json")), &gateway)
			if gateway["collab"].(map[string]any)["Target"] != "collab:9092" {
				t.Fatal("collab RPC missing")
			}
			if !strings.Contains(read("network-platform/platform.properties"), "managed-network-installation-file") {
				t.Fatal("managed network absent")
			}
			var managed struct {
				Network struct {
					WssURL string `json:"wssUrl"`
				}
			}
			_ = json.Unmarshal([]byte(read("network-platform/managed-network.json")), &managed)
			want := strings.Replace(strings.Replace(cfg.PortalURL(), "http://", "https://", 1), "https://", "wss://", 1) + "/net/v1/ws"
			if managed.Network.WssURL != want {
				t.Fatalf("WSS mismatch: %s", managed.Network.WssURL)
			}
		})
	}
}

func TestNASCollabPresentationOrigin(t *testing.T) {
	_, cfg, err := loadNASAccess("../access-ip.example.json", "192.0.2.138", "192.0.2.128/26", 15000)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		origin, allowed string
		valid           bool
	}{
		{"", "", false},
		{"http://192.0.2.10:8080", "http://192.0.2.10:8080", false},
		{"https://portal.example.com", "", false},
		{"https://portal.example.com", "https://other.example.com", false},
		{"https://portal.example.com", "https://portal.example.com", true},
		{"https://portal.example.com/path", "https://portal.example.com/path", false},
		{"https://alice@portal.example.com", "https://alice@portal.example.com", false},
		{"https://portal.example.com:70000", "https://portal.example.com:70000", false},
	} {
		t.Run(tc.origin+tc.allowed, func(t *testing.T) {
			_, err := nasCollabOrigin(cfg, tc.origin, tc.allowed)
			if (err == nil) != tc.valid {
				t.Fatalf("unexpected presentation result: %v", err)
			}
		})
	}
}
