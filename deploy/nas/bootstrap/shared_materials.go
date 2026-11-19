package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"

	"github.com/nats-io/nkeys"
)

// These private source leaves are fixed installation inputs, not caller-selected
// paths. The Linux snapshot/controller owns permissions and projection roles.
var sharedMaterialLeaves = []string{
	"platform/agent-secret/installation.secret", "platform/agent-secret.sha256",
	"platform/ticket.pk8.pem", "platform/enterprise.pk8.pem", "platform/model.pk8.pem", "platform/message.pk8.pem",
	"edge/identity-signing-keys.json", "edge/replay.seed",
	"public/ticket.jwks.json", "public/enterprise.jwks.json", "public/model.jwks.json", "public/message.jwks.json", "public/identity.jwks.json",
	"public/ticket.pem", "public/enterprise.pem", "public/model.pem", "public/message.pem", "public/identity.pem", "public/replay.nkey",
}

var sharedKeyPurposes = []string{"ticket", "enterprise", "model", "message", "identity"}

//go:embed shared_replay_user.conf.tmpl
var sharedReplayUserTemplate string

type sharedJWK struct {
	Kty   string `json:"kty"`
	Crv   string `json:"crv"`
	X     string `json:"x"`
	D     string `json:"d,omitempty"`
	Kid   string `json:"kid"`
	Alg   string `json:"alg"`
	Use   string `json:"use"`
	State string `json:"state,omitempty"`
}

func sharedKeyUse(purpose string) string {
	if purpose == "ticket" {
		return "placement-ticket"
	}
	if purpose == "identity" {
		return "shared-ingress"
	}
	return "auriga.shared." + purpose
}

func sharedKeyID(purpose string, public []byte) string {
	h := sha256.Sum256(public)
	return "shared-" + purpose + "-" + hex.EncodeToString(h[:8])
}

func sharedEncodeJWK(key sharedJWK) ([]byte, error) {
	return json.Marshal(struct {
		Keys []sharedJWK `json:"keys"`
	}{[]sharedJWK{key}})
}

func generateSharedMaterials() (map[string][]byte, error) {
	files := make(map[string][]byte, len(sharedMaterialLeaves))
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("shared installation entropy unavailable")
	}
	encoded := []byte(base64.RawURLEncoding.EncodeToString(secret))
	clear(secret)
	h := sha256.Sum256(encoded)
	files["platform/agent-secret/installation.secret"] = encoded
	files["platform/agent-secret.sha256"] = []byte(hex.EncodeToString(h[:]))
	for _, purpose := range sharedKeyPurposes {
		public, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("shared signing entropy unavailable")
		}
		key := sharedJWK{Kty: "OKP", Crv: "Ed25519", X: base64.RawURLEncoding.EncodeToString(public), Kid: sharedKeyID(purpose, public), Alg: "EdDSA", Use: sharedKeyUse(purpose)}
		files["public/"+purpose+".jwks.json"], err = sharedEncodeJWK(key)
		if err != nil {
			clear(private)
			return nil, err
		}
		spki, err := x509.MarshalPKIXPublicKey(public)
		if err != nil {
			clear(private)
			return nil, err
		}
		files["public/"+purpose+".pem"] = pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: spki})
		if purpose == "identity" {
			key.D, key.State = base64.RawURLEncoding.EncodeToString(private.Seed()), "CURRENT"
			files["edge/identity-signing-keys.json"], err = sharedEncodeJWK(key)
		} else {
			var pkcs8 []byte
			pkcs8, err = x509.MarshalPKCS8PrivateKey(private)
			if err == nil {
				files["platform/"+purpose+".pk8.pem"] = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})
			}
			clear(pkcs8)
		}
		clear(private)
		if err != nil {
			return nil, err
		}
	}
	user, err := nkeys.CreateUser()
	if err != nil {
		return nil, fmt.Errorf("shared NKey entropy unavailable")
	}
	defer user.Wipe()
	seed, err := user.Seed()
	if err != nil {
		return nil, err
	}
	files["edge/replay.seed"] = bytes.Clone(seed)
	public, err := user.PublicKey()
	if err != nil {
		return nil, err
	}
	files["public/replay.nkey"] = []byte(public)
	if err = verifySharedMaterials(files, nil); err != nil {
		return nil, err
	}
	return files, nil
}

// Re-derive every public projection and purpose from private source bytes.
// A self-consistent hash index alone is never a key-provenance check.
func verifySharedMaterials(files map[string][]byte, legacy ed25519.PublicKey) error {
	if len(files) != len(sharedMaterialLeaves) {
		return fmt.Errorf("shared material set is incomplete")
	}
	for _, leaf := range sharedMaterialLeaves {
		if len(files[leaf]) == 0 || len(files[leaf]) > 8192 {
			return fmt.Errorf("invalid fixed shared material leaf")
		}
	}
	secret := files["platform/agent-secret/installation.secret"]
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(string(secret))
	if err != nil || len(decoded) != 32 || len(secret) != 43 {
		return fmt.Errorf("invalid agent installation secret")
	}
	clear(decoded)
	h := sha256.Sum256(secret)
	if string(files["platform/agent-secret.sha256"]) != hex.EncodeToString(h[:]) {
		return fmt.Errorf("agent installation secret digest mismatch")
	}
	seen := map[string]bool{}
	if len(legacy) != 0 {
		if len(legacy) != ed25519.PublicKeySize {
			return fmt.Errorf("invalid legacy installation public key")
		}
		seen[string(legacy)] = true
	}
	for _, purpose := range sharedKeyPurposes {
		var public ed25519.PublicKey
		var private ed25519.PrivateKey
		if purpose == "identity" {
			var body struct {
				Keys []sharedJWK `json:"keys"`
			}
			if err := sharedDecodeJSON(files["edge/identity-signing-keys.json"], &body); err != nil || len(body.Keys) != 1 {
				return fmt.Errorf("invalid shared ingress signing key")
			}
			key := body.Keys[0]
			seed, err := base64.RawURLEncoding.Strict().DecodeString(key.D)
			if err != nil || len(seed) != ed25519.SeedSize || key.State != "CURRENT" {
				return fmt.Errorf("invalid shared ingress private seed")
			}
			private = ed25519.NewKeyFromSeed(seed)
			clear(seed)
			public = private.Public().(ed25519.PublicKey)
			want := sharedJWK{Kty: "OKP", Crv: "Ed25519", X: base64.RawURLEncoding.EncodeToString(public), D: key.D, Kid: sharedKeyID(purpose, public), Alg: "EdDSA", Use: sharedKeyUse(purpose), State: "CURRENT"}
			if key != want {
				clear(private)
				return fmt.Errorf("shared ingress key projection mismatch")
			}
		} else {
			block, rest := pem.Decode(files["platform/"+purpose+".pk8.pem"])
			if block == nil || block.Type != "PRIVATE KEY" || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 {
				return fmt.Errorf("invalid shared private key encoding")
			}
			parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
			clear(block.Bytes)
			var ok bool
			if private, ok = parsed.(ed25519.PrivateKey); err != nil || !ok {
				return fmt.Errorf("shared Ed25519 private key required")
			}
			public = private.Public().(ed25519.PublicKey)
		}
		clear(private)
		if seen[string(public)] {
			return fmt.Errorf("shared key reused across installation purposes")
		}
		seen[string(public)] = true
		want, err := sharedEncodeJWK(sharedJWK{Kty: "OKP", Crv: "Ed25519", X: base64.RawURLEncoding.EncodeToString(public), Kid: sharedKeyID(purpose, public), Alg: "EdDSA", Use: sharedKeyUse(purpose)})
		if err != nil || !bytes.Equal(want, files["public/"+purpose+".jwks.json"]) {
			return fmt.Errorf("shared public JWK projection mismatch")
		}
		der, err := x509.MarshalPKIXPublicKey(public)
		if err != nil || !bytes.Equal(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), files["public/"+purpose+".pem"]) {
			return fmt.Errorf("shared public SPKI projection mismatch")
		}
	}
	seed := bytes.Clone(files["edge/replay.seed"])
	defer clear(seed)
	user, err := nkeys.FromSeed(seed)
	if err != nil {
		return fmt.Errorf("invalid shared replay NKey seed")
	}
	defer user.Wipe()
	public, err := user.PublicKey()
	if err != nil || !nkeys.IsValidPublicUserKey(public) || public != string(files["public/replay.nkey"]) {
		return fmt.Errorf("shared replay public NKey mismatch")
	}
	return nil
}

func sharedDecodeJSON(raw []byte, value any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return err
	}
	if d.More() {
		return fmt.Errorf("trailing shared JSON")
	}
	// Source JSON is emitted canonically by this producer, not accepted from a
	// caller. Re-marshalling also refuses duplicate fields and trailing values.
	canonical, err := json.Marshal(value)
	if err != nil || !bytes.Equal(canonical, raw) {
		return fmt.Errorf("noncanonical shared material JSON")
	}
	return nil
}

// Fixed account-local permissions; no other bucket, global API or foreign inbox.
func sharedReplayNATSUser(public string) (string, error) {
	if !nkeys.IsValidPublicUserKey(public) {
		return "", fmt.Errorf("shared replay user NKey required")
	}
	quoted, _ := json.Marshal(public)
	return fmt.Sprintf(sharedReplayUserTemplate, quoted), nil
}

func sharedReplayPlatformPermissions() []string {
	return []string{
		"$JS.API.STREAM.INFO.KV_auriga-shared-entry",
		"$JS.API.STREAM.MSG.GET.KV_auriga-shared-entry",
		"$JS.API.DIRECT.GET.KV_auriga-shared-entry",
		"$JS.API.DIRECT.GET.KV_auriga-shared-entry.>",
	}
}

func sharedMaterialPublicKey(files map[string][]byte, purpose string) (sharedJWK, error) {
	switch purpose {
	case "ticket", "enterprise", "model", "message", "identity":
	default:
		return sharedJWK{}, fmt.Errorf("unknown shared purpose")
	}
	var body struct {
		Keys []sharedJWK `json:"keys"`
	}
	if err := sharedDecodeJSON(files["public/"+purpose+".jwks.json"], &body); err != nil || len(body.Keys) != 1 {
		return sharedJWK{}, fmt.Errorf("invalid shared public key")
	}
	return body.Keys[0], nil
}
