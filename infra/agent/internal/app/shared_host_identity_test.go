package app

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

func TestSharedIngressUsesSharedHostIdentityKeysOnly(t *testing.T) {
	public, _, _ := ed25519.GenerateKey(rand.Reader)
	der, err := x509.MarshalPKIXPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	text := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	write := func(v any) string {
		raw, _ := json.Marshal(v)
		path := filepath.Join(t.TempDir(), "host.json")
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	keys, err := loadSharedHostIdentityKeys(write(map[string]any{"identityKeys": map[string]string{"shared-id": text}}))
	if err != nil || len(keys) != 1 || !keys["shared-id"].Equal(public) {
		t.Fatalf("shared identity key not loaded: %v %v", keys, err)
	}
	for name, host := range map[string]any{
		"missing":   map[string]any{},
		"empty":     map[string]any{"identityKeys": map[string]string{}},
		"bad pem":   map[string]any{"identityKeys": map[string]string{"k": "not pem"}},
		"empty kid": map[string]any{"identityKeys": map[string]string{"": text}},
	} {
		if _, err := loadSharedHostIdentityKeys(write(host)); err == nil {
			t.Fatal(name)
		}
	}
}
