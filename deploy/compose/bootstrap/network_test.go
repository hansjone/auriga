package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nkeys"
)

func TestNetworkMaterials(t *testing.T) {
	out := t.TempDir()
	mustMkdir(filepath.Join(out, "pki"), 0o755)
	generateIdentityMaterials(out)
	for _, name := range networkMaterialFiles {
		if raw, err := os.ReadFile(filepath.Join(out, name)); err != nil || len(raw) == 0 {
			t.Fatalf("missing network material %s: %v", name, err)
		}
	}
	var cfg map[string]any
	raw, _ := os.ReadFile(filepath.Join(out, "network/gateway.json"))
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["brokerConfigFile"] != "/etc/auriga/network/nats.conf" || cfg["clientAccount"] != "CLIENTS" {
		t.Fatal("gateway must read the Broker's exact config and isolated account")
	}
	broker, _ := os.ReadFile(filepath.Join(out, "network/nats.conf"))
	for _, required := range []string{"auth_callout", "timeout: 2", "max_payload: 65536", "max_subscriptions: 8", "allowed_connection_types: [STANDARD]", "tls {", "network-auth", "network-ops"} {
		if !strings.Contains(string(broker), required) {
			t.Errorf("Broker missing %s", required)
		}
	}
	for _, forbidden := range []string{"no_auth_user", "jetstream", "e2e_nats", "no_tls"} {
		if strings.Contains(string(broker), forbidden) {
			t.Errorf("unsafe network Broker: %s", forbidden)
		}
	}
}

// Parse with the exact NATS version used by the deployed Broker, without
// opening sockets. Only container paths are relocated into this test's temp dir.
func TestNetworkBrokerConfig(t *testing.T) {
	out := t.TempDir()
	mustMkdir(filepath.Join(out, "pki"), 0o755)
	generateIdentityMaterials(out)
	raw, err := os.ReadFile(filepath.Join(out, "network/nats.conf"))
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.ReplaceAll(string(raw), "/etc/auriga/network/", filepath.ToSlash(filepath.Join(out, "network"))+"/"))
	path := filepath.Join(out, "network/test.conf")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	opts, err := server.ProcessConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if opts.AuthTimeout != 2 || opts.Websocket.AuthTimeout != 0 || opts.JetStream || opts.NoAuthUser != "" || opts.SystemAccount != "SYS" || opts.AuthCallout.Account != "AUTH" {
		t.Fatal("unsafe or inconsistent broker auth boundaries")
	}
	if opts.TLSConfig == nil || opts.TLSConfig.ClientAuth != tls.RequireAndVerifyClientCert || opts.Websocket.TLSConfig == nil {
		t.Fatal("control mTLS and WSS are required")
	}
	for _, tc := range []struct{ seed, pub string }{
		{"callout-account.seed", opts.AuthCallout.Issuer}, {"callout-curve.seed", opts.AuthCallout.XKey},
	} {
		raw, err := os.ReadFile(filepath.Join(out, "network", tc.seed))
		if err != nil {
			t.Fatal(err)
		}
		kp, err := nkeys.FromSeed(raw)
		clear(raw)
		if err != nil {
			t.Fatal(err)
		}
		pub, err := kp.PublicKey()
		kp.Wipe()
		if err != nil || pub != tc.pub {
			t.Fatal("callout key mismatch")
		}
	}
	if len(opts.Users) != 2 {
		t.Fatal("unexpected static network user")
	}
	for _, user := range opts.Users {
		if len(user.AllowedConnectionTypes) != 1 {
			t.Fatal("control user accepts external transport")
		}
		if _, ok := user.AllowedConnectionTypes["STANDARD"]; !ok {
			t.Fatal("control user must be standard-only")
		}
		if user.Username == "network-ops" && (len(user.Permissions.Publish.Allow) != 2 || len(user.Permissions.Subscribe.Allow) != 1) {
			t.Fatal("system control permissions widened")
		}
	}
	ca, _ := os.ReadFile(filepath.Join(out, "network/ca.crt"))
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("invalid CA")
	}
	pair, err := tls.LoadX509KeyPair(filepath.Join(out, "network/nats.crt"), filepath.Join(out, "network/nats.key"))
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: "network-nats"}); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		for _, name := range []string{"network", "network/nats.conf", "network/callout-account.seed", "network/callout-curve.seed", "network/auth.password", "network/ops.password", "network/nats.key", "network/iam-client.key"} {
			info, err := os.Stat(filepath.Join(out, name))
			if err != nil || info.Mode().Perm()&0o077 != 0 {
				t.Fatalf("network secret exposed: %s %v", name, err)
			}
		}
	}
}
