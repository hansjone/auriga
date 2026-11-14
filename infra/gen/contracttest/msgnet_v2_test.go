package contracttest

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
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
			if ed25519.Verify(pub, []byte("Auriga.ServerEnvelope.v1\x00"+v.Canonical), sig) {
				t.Fatal("accepted wrong purpose domain")
			}
		})
	}
}

// 契约参考adapter全量跑数据；N/P的生产验证器必须独立跑同一三组输入。
func TestMsgnetV2EverySemanticCase(t *testing.T) {
	data, err := os.ReadFile("../../../contracts/network/fixtures/auto-renewal-v2-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var groups map[string]json.RawMessage
	if err := json.Unmarshal(data, &groups); err != nil {
		t.Fatal(err)
	}
	expected := make(map[string]string)
	for _, group := range []string{"cases", "issuer_cases", "boundary_cases"} {
		var vectors []struct {
			ID       string                     `json:"id"`
			Expected string                     `json:"expected"`
			Input    map[string]json.RawMessage `json:"input"`
		}
		if err := json.Unmarshal(groups[group], &vectors); err != nil {
			t.Fatal(err)
		}
		for _, v := range vectors {
			key := group + "/" + v.ID
			if _, ok := expected[key]; ok || len(v.Input) == 0 {
				t.Fatal("duplicate or missing input", key)
			}
			expected[key] = v.Expected
		}
	}
	cmd := exec.Command("node", "../../../contracts/network/fixtures/run-msgnet-v2-cases.mjs", "--stdin")
	cmd.Stdin = bytes.NewReader(data)
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var results []struct {
		ID     string `json:"id"`
		Group  string `json:"group"`
		Result string `json:"result"`
	}
	if err := json.Unmarshal(output, &results); err != nil {
		t.Fatal(err)
	}
	if len(results) != len(expected) {
		t.Fatal("missing case results")
	}
	for _, r := range results {
		key := r.Group + "/" + r.ID
		if expected[key] != r.Result {
			t.Fatalf("%s: got %s want %s", key, r.Result, expected[key])
		}
		delete(expected, key)
	}
	if len(expected) != 0 {
		t.Fatal("unconsumed cases")
	}
}
