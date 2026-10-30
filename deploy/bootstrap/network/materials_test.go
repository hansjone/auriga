package network

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDedicatedNetworkMaterial(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "network")
	rpc := map[string]string{"Target": "127.0.0.1:19090", "ServiceTokenFile": "private/service.jwt"}
	options := Options{Root: filepath.ToSlash(dir), SubmitListen: "127.0.0.1:18443", BrokerHost: "127.0.0.1", BrokerURL: "tls://127.0.0.1:14224", BrokerPort: 14224, WebsocketPort: 18444, Audience: "auriga-network", IAM: rpc, Collab: rpc}
	if err := Generate(dir, options); err != nil {
		t.Fatal(err)
	}
	load := func(name string) *x509.Certificate {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(dir, name+".pem"))
		if err != nil {
			t.Fatal(err)
		}
		block, _ := pem.Decode(raw)
		if block == nil {
			t.Fatal("missing cert")
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		return cert
	}
	roots := x509.NewCertPool()
	roots.AddCert(load("client-ca"))
	for _, name := range []string{"edge", "submit", "broker", "broker-client"} {
		_, err := load(name).Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
		if (err == nil) != (name == "edge") {
			t.Fatalf("unexpected submit client trust: %s %v", name, err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, "runtime.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err = json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["submitHTTP"].(map[string]any)["clientCAFile"] != options.Root+"/client-ca.pem" {
		t.Fatal("missing client CA")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != len(Files) {
		t.Fatal("incomplete material set", err)
	}
	if err = Generate(dir, options); err == nil {
		t.Fatal("existing installation overwritten")
	}
}

func TestNetworkCredentialExport(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "network")
	rpc := map[string]string{"Target": "127.0.0.1:19090", "ServiceTokenFile": "/run/auriga-network-token/gateway/service.jwt"}
	if err := Generate(dir, Options{Root: "/etc/auriga/network", SubmitListen: "0.0.0.0:18443", BrokerHost: "127.0.0.1", BrokerURL: "tls://127.0.0.1:14224", BrokerPort: 14224, WebsocketPort: 18444, Audience: "auriga-network", IAM: rpc, Collab: rpc, ExportServiceToken: true}); err != nil {
		t.Fatal(err)
	}
	secret, err := os.ReadFile(filepath.Join(dir, "gateway.secret"))
	if err != nil {
		t.Fatal(err)
	}
	defer clear(secret)
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(string(secret))
	defer clear(decoded)
	if err != nil || len(decoded) != 32 || len(secret) != 43 {
		t.Fatal("credential violates existing service token publisher format")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "platform.properties"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{fmt.Sprintf("auriga.iam.internal-secret-hashes.ai-gateway=%x", sha256.Sum256(secret)), "installation-secret-file=/etc/auriga/network/gateway.secret", "token-file=/run/auriga-network-token/gateway/service.jwt", "reader-group=65533", "auriga.iam.network.source-max-seconds=${AURIGA_IAM_NETWORK_SOURCE_MAX_SECONDS:3600}\n", "auriga.iam.network.source-decision-seconds=${AURIGA_IAM_NETWORK_SOURCE_DECISION_SECONDS:60}\n"} {
		if !strings.Contains(string(raw), expected) {
			t.Fatal("incomplete isolated token export configuration")
		}
	}
}
