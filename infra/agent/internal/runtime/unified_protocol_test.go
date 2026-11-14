package runtime

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	resourcev1 "github.com/hansjone/auriga/infra/gen/auriga/resource/v1"
)

func TestUnifiedProductionProtocolAllFrozenVectors(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "contracts", "resource", "unified-vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	document, err := DecodeUnifiedJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	verifier := UnifiedProtocol{}
	for purpose, value := range um(document["trust"]) {
		key := um(value)
		der, err := base64.StdEncoding.DecodeString(us(key["publicKeySpkiBase64"]))
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := x509.ParsePKIXPublicKey(der)
		if err != nil {
			t.Fatal(err)
		}
		pub, ok := parsed.(ed25519.PublicKey)
		if !ok {
			t.Fatal("not Ed25519")
		}
		verifier.Keys = append(verifier.Keys, &resourcev1.ResourceTrustKey{Kid: us(key["kid"]), Purpose: purpose, PublicKey: pub, NotBefore: "2020-01-01T00:00:00Z", NotAfter: "2100-01-01T00:00:00Z"})
	}
	cases := ua(document["cases"])
	if len(cases) < 47 {
		t.Fatalf("incomplete shared vectors: %d", len(cases))
	}
	for _, value := range cases {
		test := um(value)
		t.Run(us(test["id"]), func(t *testing.T) {
			err := verifier.Evaluate(us(test["operation"]), um(test["input"]))
			want := um(test["expect"])
			decision, reason := "accept", "ok"
			if err != nil {
				decision, reason = "reject", err.Error()
			}
			if decision != want["decision"] || reason != want["reason"] {
				t.Fatalf("got %s/%s, want %v", decision, reason, want)
			}
		})
	}
}

func TestUnifiedJSONRejectsAmbiguity(t *testing.T) {
	for _, raw := range []string{`{"a":1,"a":2}`, `{"a":1} {}`, `[]`} {
		if _, err := DecodeUnifiedJSON([]byte(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	_, err := unifiedCanonical(map[string]any{"sequence": json.Number("9007199254740992")})
	if err == nil {
		t.Fatal("unsafe JSON integer accepted")
	}
}
