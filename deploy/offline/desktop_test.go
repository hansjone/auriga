package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSiteDesktopProvision(t *testing.T) {
	data := t.TempDir()
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(data, "input.pem")
	want := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	if err := os.WriteFile(key, want, 0600); err != nil {
		t.Fatal(err)
	}
	envFile := filepath.Join(data, "stack.env")
	if err := os.WriteFile(envFile, []byte("OTHER=value\nAURIGA_DESKTOP_PUBLISHER_TENANT_ID=\n"), 0600); err != nil {
		t.Fatal(err)
	}
	tenant := "11111111-1111-4111-8111-111111111111"
	s := state{Data: data}
	if err := configureDesktop(&s, key, tenant); err != nil {
		t.Fatal(err)
	}
	if s.DesktopPublicKeyFingerprint == "" || s.DesktopPublisherTenant != tenant {
		t.Fatal("desktop trust not recorded in state")
	}
	if err := configureDesktop(&s, "", ""); err != nil {
		t.Fatal(err)
	}
	bad := s
	bad.DesktopPublicKeyFingerprint = strings.Repeat("0", 64)
	if err := configureDesktop(&bad, key, tenant); err == nil {
		t.Fatal("state trust replacement accepted")
	}
	for range 2 {
		if err := provisionDesktop(data, key, tenant); err != nil {
			t.Fatal(err)
		}
	}
	actual, err := os.ReadFile(filepath.Join(data, "config/desktop/release-public.pem"))
	if err != nil || !bytes.Equal(actual, want) {
		t.Fatalf("public key mismatch %v", err)
	}
	env, err := os.ReadFile(envFile)
	if err != nil || !bytes.Contains(env, []byte("AURIGA_DESKTOP_PUBLISHER_TENANT_ID="+tenant)) || !bytes.Contains(env, []byte("OTHER=value")) {
		t.Fatalf("env mismatch %v", err)
	}
	if err := provisionDesktop(data, key, "22222222-2222-4222-8222-222222222222"); err == nil {
		t.Fatal("tenant rotation accepted")
	}
	if err := provisionDesktop(data, key, "invalid"); err == nil {
		t.Fatal("invalid tenant accepted")
	}
	if err := os.WriteFile(key, []byte("-----BEGIN PRIVATE KEY-----\ninvalid\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := provisionDesktop(data, key, tenant); err == nil {
		t.Fatal("private key accepted")
	}
	public2, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der2, err := x509.MarshalPKIXPublicKey(public2)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der2}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := provisionDesktop(data, key, tenant); err == nil {
		t.Fatal("public key rotation accepted")
	}
}
