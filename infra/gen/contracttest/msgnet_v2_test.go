package contracttest

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"
)

// 公有合成向量与 Java/Node 共用；无测试私钥字面量。
func TestMsgnetV2Signatures(t *testing.T) {
	data, err := os.ReadFile("../../../contracts/network/fixtures/auto-renewal-v2-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Synthetic    bool `json:"synthetic"`
		Certificates []struct {
			CA   string `json:"ca_der"`
			Leaf string `json:"leaf_der"`
		} `json:"certificates"`
		Signatures []struct {
			ID        string `json:"id"`
			Domain    string `json:"domain"`
			Canonical string `json:"canonical"`
			SPKI      string `json:"public_key_spki"`
			Signature string `json:"signature"`
			Valid     bool   `json:"valid"`
		} `json:"signatures"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if !fixture.Synthetic || len(fixture.Signatures) < 90 {
		t.Fatal("missing synthetic vectors")
	}
	for _, c := range fixture.Certificates {
		caDER, err := base64.RawURLEncoding.DecodeString(c.CA)
		if err != nil {
			t.Fatal(err)
		}
		leafDER, err := base64.RawURLEncoding.DecodeString(c.Leaf)
		if err != nil {
			t.Fatal(err)
		}
		ca, err := x509.ParseCertificate(caDER)
		if err != nil {
			t.Fatal(err)
		}
		leaf, err := x509.ParseCertificate(leafDER)
		if err != nil {
			t.Fatal(err)
		}
		if err := leaf.CheckSignatureFrom(ca); err != nil {
			t.Fatal(err)
		}
		if err := leaf.VerifyHostname("submit.example.com"); err != nil {
			t.Fatal(err)
		}
	}
	for _, v := range fixture.Signatures {
		t.Run(v.ID, func(t *testing.T) {
			der, err := base64.RawURLEncoding.DecodeString(v.SPKI)
			if err != nil {
				t.Fatal(err)
			}
			key, err := x509.ParsePKIXPublicKey(der)
			if err != nil {
				t.Fatal(err)
			}
			pub, ok := key.(ed25519.PublicKey)
			if !ok {
				t.Fatal("not Ed25519")
			}
			sig, err := base64.RawURLEncoding.DecodeString(v.Signature)
			if err != nil {
				t.Fatal(err)
			}
			if ed25519.Verify(pub, []byte(v.Domain+"\x00"+v.Canonical), sig) != v.Valid {
				t.Fatal("signature verdict")
			}
			if ed25519.Verify(pub, []byte("Auriga.Business.v1\x00"+v.Canonical), sig) {
				t.Fatal("accepted wrong purpose domain")
			}
		})
	}
}
