package app

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

func TestSharedHostConfigCommandUsesOnlyPurposePublicMaterial(t *testing.T) {
	dir := t.TempDir()
	var args = []string{"render-shared-host-config"}
	original := map[string]ed25519.PublicKey{}
	for _, name := range []string{"identity", "enterprise", "model", "message"} {
		public, _, e := ed25519.GenerateKey(rand.Reader)
		if e != nil {
			t.Fatal(e)
		}
		original[name] = public
		raw, _ := json.Marshal(map[string]any{"keys": []any{map[string]string{"kty": "OKP", "crv": "Ed25519", "kid": name, "x": base64.RawURLEncoding.EncodeToString(public)}}})
		path := filepath.Join(dir, name+".json")
		if e = os.WriteFile(path, raw, 0444); e != nil {
			t.Fatal(e)
		}
		args = append(args, "--"+name+"-keys", path)
	}
	policy := filepath.Join(dir, "enterprise-policy.json")
	if e := os.WriteFile(policy, []byte(`{"allowRawAgentToken":true,"outboundAllowedHosts":["synthetic.example.invalid"],"empNoHeader":"Synthetic-Emp-No","authValueHeader":"Synthetic-Auth"}`), 0444); e != nil {
		t.Fatal(e)
	}
	output := filepath.Join(dir, "host.json")
	trustFile := filepath.Join(dir, "message-trust.json")
	der, _ := x509.MarshalPKIXPublicKey(original["message"])
	trust, _ := json.Marshal(map[string]any{"realm": "1", "roots": []any{map[string]string{"kid": "fixture-root", "public_key_spki": base64.RawURLEncoding.EncodeToString(der)}}})
	if e := os.WriteFile(trustFile, trust, 0444); e != nil {
		t.Fatal(e)
	}
	args = append(args, "--model", "synthetic-model", "--enterprise-config", policy, "--message-trust", trustFile, "--output", output)
	var log bytes.Buffer
	if e := RunCLI(args, &log, &log); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(output)
	if e != nil {
		t.Fatal(e)
	}
	var doc struct {
		IdentityKeys   map[string]string
		CapabilityKeys map[string]map[string]string
		Model          string
		MessageTrust   struct {
			Realm string
			Roots []any
		}
	}
	if e = json.Unmarshal(raw, &doc); e != nil {
		t.Fatal(e)
	}
	block, _ := pem.Decode([]byte(doc.IdentityKeys["identity"]))
	if block == nil {
		t.Fatal("SPKI missing")
	}
	key, e := x509.ParsePKIXPublicKey(block.Bytes)
	if e != nil || !bytes.Equal(key.(ed25519.PublicKey), original["identity"]) {
		t.Fatal("public key changed", e)
	}
	if len(doc.CapabilityKeys) != 3 || doc.Model != "synthetic-model" || bytes.Contains(raw, []byte("PRIVATE KEY")) {
		t.Fatal("invalid public config")
	}
	if doc.MessageTrust.Realm != "1" || len(doc.MessageTrust.Roots) != 1 {
		t.Fatal("approved message roots not rendered")
	}
	if e = RunCLI(args, &log, &log); e == nil {
		t.Fatal("existing Host config overwritten")
	}
}
