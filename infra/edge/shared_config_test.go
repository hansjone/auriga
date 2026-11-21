package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSharedTicketKeysRequireTheConfiguredPublicPurpose(t *testing.T) {
	public, _, _ := ed25519.GenerateKey(rand.Reader)
	valid := jwkKey{Kty: "OKP", Crv: "Ed25519", X: base64.RawURLEncoding.EncodeToString(public), Kid: "fixture", Alg: "EdDSA", Use: "placement-ticket"}
	path := filepath.Join(t.TempDir(), "ticket-public.json")
	write := func(keys ...jwkKey) {
		t.Helper()
		raw, err := json.Marshal(jwkKeyFile{Keys: keys})
		if err != nil || os.WriteFile(path, raw, 0600) != nil {
			t.Fatal("could not write synthetic key fixture")
		}
	}
	write(valid)
	keys, err := loadSharedTicketKeys(path)
	if err != nil || !keys[valid.Kid].Equal(public) {
		t.Fatalf("public ticket key = %v", err)
	}
	write(valid, valid)
	if _, err := loadSharedTicketKeys(path); err == nil {
		t.Fatal("duplicate key ID accepted")
	}
	for _, mutation := range []func(*jwkKey){
		func(k *jwkKey) { k.D = "synthetic-private-material" },
		func(k *jwkKey) { k.Use = "shared-ingress" },
		func(k *jwkKey) { k.X = base64.RawURLEncoding.EncodeToString([]byte("short")) },
	} {
		changed := valid
		mutation(&changed)
		write(changed)
		if _, err := loadSharedTicketKeys(path); err == nil {
			t.Fatal("private, cross-purpose or malformed verification material accepted")
		}
	}
}

func TestSharedEntryUnconfiguredKeepsTheExistingInstallationPath(t *testing.T) {
	entry, close, err := openSharedEntry(Config{}, nil, nil)
	if err != nil || entry != nil || close == nil {
		t.Fatal("absent shared configuration tried to initialize production dependencies", err)
	}
	close()
}
