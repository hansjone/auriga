package network_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Run from the worktree: go test -count=1 contracts/test/network-signatures_test.go.
// Consume the authoritative generated vectors without adding a module or dependencies.
func TestNetworkSignatureVectors(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate contract fixtures")
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "network", "fixtures", "signature-vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Keys []struct {
			Kid             string
			PublicKeySpki   string
			TestOnlySeedHex string
		}
		Vectors []struct {
			Name, Canonical, Signature, Kid string
		}
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	keys := make(map[string]ed25519.PrivateKey)
	for _, key := range fixture.Keys {
		seed, err := hex.DecodeString(key.TestOnlySeedHex)
		if err != nil || len(seed) != ed25519.SeedSize {
			t.Fatal("invalid test seed", err)
		}
		private := ed25519.NewKeyFromSeed(seed)
		spki, err := base64.RawURLEncoding.DecodeString(key.PublicKeySpki)
		if err != nil {
			t.Fatal(err)
		}
		public, err := x509.ParsePKIXPublicKey(spki)
		if err != nil || !private.Public().(ed25519.PublicKey).Equal(public) {
			t.Fatal("test key mismatch", err)
		}
		keys[key.Kid] = private
	}
	typed := 0
	for _, vector := range fixture.Vectors {
		t.Run(vector.Name, func(t *testing.T) {
			signature, err := base64.RawURLEncoding.DecodeString(vector.Signature)
			if err != nil {
				t.Fatal(err)
			}
			key := keys[vector.Kid]
			input := append([]byte("Auriga.ServerEnvelope.v1\x00"), vector.Canonical...)
			if len(key) != ed25519.PrivateKeySize || !bytes.Equal(signature, ed25519.Sign(key, input)) {
				t.Fatal("Go signature differs from authoritative Java/Node vector")
			}
			var body map[string]any
			if err := json.Unmarshal([]byte(vector.Canonical), &body); err != nil {
				t.Fatal(err)
			}
			if body["type"] != "register_result" {
				return
			}
			typed++
			original := body["address_type"]
			if original != "session" && original != "subagent" {
				t.Fatal("missing typed registration vector")
			}
			for _, changed := range []any{nil, "session", "subagent"} {
				if changed == original {
					continue
				}
				if changed == nil {
					delete(body, "address_type")
				} else {
					body["address_type"] = changed
				}
				var encoded bytes.Buffer
				encoder := json.NewEncoder(&encoded)
				encoder.SetEscapeHTML(false)
				if err := encoder.Encode(body); err != nil {
					t.Fatal(err)
				}
				mutated := append([]byte("Auriga.ServerEnvelope.v1\x00"), bytes.TrimSuffix(encoded.Bytes(), []byte("\n"))...)
				if ed25519.Verify(key.Public().(ed25519.PublicKey), mutated, signature) {
					t.Fatal("changed or missing address_type retained a valid signature")
				}
			}
		})
	}
	if typed != 2 {
		t.Fatalf("expected session and subagent signed vectors, got %d", typed)
	}
}
