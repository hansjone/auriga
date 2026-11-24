package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureSharedAgentResourceTrust(t *testing.T) {
	data := t.TempDir()
	rootsDir := filepath.Join(data, "resource-keys")
	if err := os.MkdirAll(rootsDir, 0700); err != nil {
		t.Fatal(err)
	}
	roots := map[string]map[string]string{}
	for _, purpose := range []string{"release", "revocation", "downgrade"} {
		pub, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		spki, err := x509.MarshalPKIXPublicKey(pub)
		if err != nil {
			t.Fatal(err)
		}
		roots[purpose] = map[string]string{
			"kid":                 "site-test-" + purpose,
			"publicKeySpkiBase64": base64.StdEncoding.EncodeToString(spki),
		}
	}
	rawRoots, err := json.Marshal(roots)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootsDir, "resource-roots.json"), rawRoots, 0644); err != nil {
		t.Fatal(err)
	}
	if err := ensureSharedAgentResourceTrust(data); err != nil {
		t.Fatalf("ensureSharedAgentResourceTrust failed: %v", err)
	}
	trustPath := filepath.Join(data, "data", "agent", "agent", "resource-trust.json")
	raw, err := os.ReadFile(trustPath)
	if err != nil {
		t.Fatalf("read resource-trust.json: %v", err)
	}
	var cfg sharedAgentResourceTrustConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("unmarshal resource-trust.json: %v", err)
	}
	if cfg.SchemaVersion != 1 || cfg.Origin != sharedAgentResourceTrustOrigin || len(cfg.Keys) != 3 {
		t.Fatalf("unexpected trust config: %+v", cfg)
	}
	for _, k := range cfg.Keys {
		pub, err := base64.StdEncoding.DecodeString(k.PublicKey)
		if err != nil || len(pub) != ed25519.PublicKeySize {
			t.Fatalf("expected 32-byte raw ed25519 key for %s", k.Purpose)
		}
	}
}
