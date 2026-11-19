package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nats-io/nkeys"
)

func sharedFixture(t *testing.T) map[string][]byte {
	t.Helper()
	files, err := generateSharedMaterials()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, raw := range files {
			clear(raw)
		}
	})
	return files
}

func TestSharedMaterialsExistingKeyEncodingsAndPurposeSeparation(t *testing.T) {
	files := sharedFixture(t)
	if err := verifySharedMaterials(files, nil); err != nil {
		t.Fatal(err)
	}
	nonce := []byte("synthetic-installation-protocol-proof")
	seen := map[string]bool{}
	for _, purpose := range sharedKeyPurposes {
		jwk, err := sharedMaterialPublicKey(files, purpose)
		if err != nil || jwk.D != "" || jwk.Use != sharedKeyUse(purpose) {
			t.Fatal("invalid public purpose projection")
		}
		public, err := base64.RawURLEncoding.DecodeString(jwk.X)
		if err != nil || seen[string(public)] {
			t.Fatal("purpose key reused")
		}
		seen[string(public)] = true
		block, _ := pem.Decode(files["public/"+purpose+".pem"])
		parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil || !parsed.(ed25519.PublicKey).Equal(ed25519.PublicKey(public)) {
			t.Fatal("public SPKI differs")
		}
		if purpose != "identity" {
			block, _ = pem.Decode(files["platform/"+purpose+".pk8.pem"])
			private, err := x509.ParsePKCS8PrivateKey(block.Bytes)
			if err != nil || !ed25519.Verify(public, nonce, ed25519.Sign(private.(ed25519.PrivateKey), nonce)) {
				t.Fatal("PKCS8 producer does not sign for projected key")
			}
		}
	}
	user, err := nkeys.FromSeed(files["edge/replay.seed"])
	if err != nil {
		t.Fatal("generated replay seed invalid")
	}
	defer user.Wipe()
	signature, err := user.Sign(nonce)
	if err != nil || user.Verify(nonce, signature) != nil {
		t.Fatal("NKey nonce signature differs")
	}
	config, err := sharedReplayNATSUser(string(files["public/replay.nkey"]))
	if err != nil {
		t.Fatal(err)
	}
	if output := os.Getenv("AURIGA_SHARED_MATERIAL_FIXTURE"); output != "" {
		if !filepath.IsAbs(output) {
			t.Fatal("absolute new fixture output required")
		}
		if err := os.Mkdir(output, 0700); err != nil {
			t.Fatal(err)
		}
		for name, raw := range files {
			path := filepath.Join(output, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(output, "replay-user.conf"), []byte(config), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSharedMaterialsRejectMissingExtraDriftAndCrossPurposeReuse(t *testing.T) {
	original := sharedFixture(t)
	mutations := map[string]func(map[string][]byte){
		"missing": func(f map[string][]byte) { delete(f, "platform/ticket.pk8.pem") },
		"extra":   func(f map[string][]byte) { f["unapproved"] = []byte("extra") },
		"agent-secret-drift": func(f map[string][]byte) {
			f["platform/agent-secret/installation.secret"] = []byte(strings.Repeat("a", 43))
		},
		"agent-digest-drift":   func(f map[string][]byte) { f["platform/agent-secret.sha256"] = []byte(strings.Repeat("a", 64)) },
		"wrong-private-type":   func(f map[string][]byte) { f["platform/ticket.pk8.pem"] = bytes.Clone(f["public/ticket.pem"]) },
		"wrong-public-purpose": func(f map[string][]byte) { f["public/ticket.jwks.json"] = bytes.Clone(f["public/model.jwks.json"]) },
		"wrong-spki":           func(f map[string][]byte) { f["public/model.pem"] = bytes.Clone(f["public/enterprise.pem"]) },
		"duplicate-private-jwk": func(f map[string][]byte) {
			f["edge/identity-signing-keys.json"] = bytes.Replace(f["edge/identity-signing-keys.json"], []byte(`"keys":`), []byte(`"keys":[],"keys":`), 1)
		},
		"ingress-public-drift": func(f map[string][]byte) { f["public/identity.jwks.json"] = bytes.Clone(f["public/ticket.jwks.json"]) },
		"replay-public-drift":  func(f map[string][]byte) { f["public/replay.nkey"] = []byte("not-a-user-key") },
		"replay-account-seed": func(f map[string][]byte) {
			key, _ := nkeys.CreateAccount()
			defer key.Wipe()
			seed, _ := key.Seed()
			f["edge/replay.seed"] = bytes.Clone(seed)
			pub, _ := key.PublicKey()
			f["public/replay.nkey"] = []byte(pub)
		},
		"reuse-enterprise-as-model": func(f map[string][]byte) {
			f["platform/model.pk8.pem"] = bytes.Clone(f["platform/enterprise.pk8.pem"])
			key, _ := sharedMaterialPublicKey(f, "enterprise")
			public, _ := base64.RawURLEncoding.DecodeString(key.X)
			key.Kid, key.Use = sharedKeyID("model", public), sharedKeyUse("model")
			f["public/model.jwks.json"], _ = sharedEncodeJWK(key)
			f["public/model.pem"] = bytes.Clone(f["public/enterprise.pem"])
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			files := map[string][]byte{}
			for name, raw := range original {
				files[name] = bytes.Clone(raw)
			}
			defer func() {
				for _, raw := range files {
					clear(raw)
				}
			}()
			mutate(files)
			if verifySharedMaterials(files, nil) == nil {
				t.Fatal("invalid shared installation material accepted")
			}
		})
	}
	model, _ := sharedMaterialPublicKey(original, "model")
	legacy, _ := base64.RawURLEncoding.DecodeString(model.X)
	if verifySharedMaterials(original, legacy) == nil {
		t.Fatal("legacy signing key accepted for shared purpose")
	}
	for _, purpose := range []string{"", "ticket|enterprise", "../ticket", "unknown"} {
		if _, err := sharedMaterialPublicKey(original, purpose); err == nil {
			t.Fatal("unapproved purpose accepted")
		}
	}
}

func TestSharedReplayNATSUserHasNoGlobalOrForeignPermissions(t *testing.T) {
	files := sharedFixture(t)
	config, err := sharedReplayNATSUser(string(files["public/replay.nkey"]))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`"$JS.API.>"`, `"$KV.>"`, `"_INBOX.>"`, `KV_auriga-sessions`, `KV_auriga-routes`, `STREAM.DELETE`, `CONSUMER.`} {
		if strings.Contains(config, forbidden) {
			t.Fatal("replay identity has unrelated access")
		}
	}
	for _, subject := range sharedReplayPlatformPermissions() {
		if !strings.Contains(subject, "KV_auriga-shared-entry") || strings.Contains(subject, "CREATE") || strings.Contains(subject, "UPDATE") || strings.HasPrefix(subject, "$KV.") {
			t.Fatal("platform replay permissions allow write or foreign bucket")
		}
	}
	if _, err := sharedReplayNATSUser("unknown"); err == nil {
		t.Fatal("invalid NKey user accepted")
	}
	for _, purpose := range sharedKeyPurposes {
		var doc map[string]any
		if err := json.Unmarshal(files["public/"+purpose+".jwks.json"], &doc); err != nil {
			t.Fatal(err)
		}
		key := doc["keys"].([]any)[0].(map[string]any)
		if key["d"] != nil || key["kty"] != "OKP" {
			t.Fatal("public JWK leaks a private field or changed wire encoding")
		}
	}
}
