package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestSharedModelRuntimeLoadsOnlyEd25519PublicKeyFiles(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "model-public.pem")
	if err = os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}), 0600); err != nil {
		t.Fatal(err)
	}
	keys, err := loadModelPublicKeys(map[string]string{"model-1": path})
	if err != nil || !bytes.Equal(keys["model-1"], public) {
		t.Fatal("model key missing", err)
	}
	privatePath := filepath.Join(dir, "private.pem")
	if err = os.WriteFile(privatePath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}), 0600); err != nil {
		t.Fatal(err)
	}
	for _, keys := range []map[string]string{nil, {"": "invalid"}, {"model": "relative.pem"}, {"model": privatePath}} {
		if _, err := loadModelPublicKeys(keys); err == nil {
			t.Fatal("invalid model key accepted")
		}
	}
}

func TestSharedModelRuntimeIsRecognizedByMainAndFailsClosedBeforeServices(t *testing.T) {
	content := map[string]any{
		"listen": "127.0.0.1:0", "origin": "http://127.0.0.1:9", "memberPasswordFile": "unused-file",
		"iam":         map[string]string{"Target": "127.0.0.1:9", "ServiceTokenFile": "unused-token-file"},
		"org":         map[string]string{"Target": "127.0.0.1:9", "ServiceTokenFile": "unused-token-file"},
		"sharedModel": map[string]any{"listen": "127.0.0.1:0", "tenantId": uuid.NewString(), "allowedNodeIds": []string{uuid.NewString()}, "modelPublicKeys": map[string]string{}},
	}
	raw, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "bifrost.json")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	var out, logs bytes.Buffer
	err = run([]string{"--bifrost-config", path}, &out, &logs)
	if err == nil || !strings.Contains(err.Error(), "shared model") || strings.Contains(err.Error(), "unknown field") {
		t.Fatal("main did not install shared model runtime", err)
	}
}
