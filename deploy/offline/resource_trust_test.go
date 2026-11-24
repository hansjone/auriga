package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureAgentResourceTrustProjectsRoots(t *testing.T) {
	data := t.TempDir()
	keyRoot := filepath.Join(data, "resource-keys")
	if err := os.MkdirAll(keyRoot, 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeResourceKeys(keyRoot); err != nil {
		t.Fatal(err)
	}
	var roots map[string]map[string]string
	if err := readJSON(filepath.Join(keyRoot, "resource-roots.json"), &roots); err != nil {
		t.Fatal(err)
	}
	if err := ensureAgentResourceTrust(data, roots); err != nil {
		t.Fatalf("ensureAgentResourceTrust failed: %v", err)
	}
	trustPath := filepath.Join(data, "data", "agent", "agent", "resource-trust.json")
	raw, err := os.ReadFile(trustPath)
	if err != nil {
		t.Fatalf("expected resource-trust.json: %v", err)
	}
	var cfg agentResourceTrustConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("unmarshal resource-trust.json: %v", err)
	}
	if cfg.SchemaVersion != 1 || cfg.Origin != defaultAgentResourceTrustOrigin || len(cfg.Keys) != 3 {
		t.Fatalf("unexpected trust config: %+v", cfg)
	}
	seen := map[string]bool{}
	for _, k := range cfg.Keys {
		pub, err := base64.StdEncoding.DecodeString(k.PublicKey)
		if err != nil || len(pub) != ed25519.PublicKeySize {
			t.Fatalf("expected 32-byte raw ed25519 key for %s, got len=%d err=%v", k.Purpose, len(pub), err)
		}
		if k.Kid != roots[k.Purpose]["kid"] {
			t.Fatalf("kid mismatch for %s: got %q want %q", k.Purpose, k.Kid, roots[k.Purpose]["kid"])
		}
		if k.NotBefore == "" || k.NotAfter == "" {
			t.Fatalf("expected validity window for %s", k.Purpose)
		}
		seen[k.Purpose] = true
	}
	for _, p := range []string{"release", "revocation", "downgrade"} {
		if !seen[p] {
			t.Fatalf("missing purpose %s", p)
		}
	}
	// Existing valid custom origin must be preserved on re-run.
	custom := `{"schemaVersion":1,"origin":"https://custom-platform:18081","keys":[{"kid":"k","publicKey":"` + cfg.Keys[0].PublicKey + `","purpose":"release","notBefore":"2020-01-01T00:00:00Z","notAfter":"2100-01-01T00:00:00Z"}]}`
	if err := os.WriteFile(trustPath, []byte(custom), 0644); err != nil {
		t.Fatal(err)
	}
	if err := ensureAgentResourceTrust(data, roots); err != nil {
		t.Fatalf("idempotent ensureAgentResourceTrust failed: %v", err)
	}
	after, err := os.ReadFile(trustPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != custom {
		t.Fatalf("existing valid resource-trust.json was overwritten")
	}
}
