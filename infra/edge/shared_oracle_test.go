package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestSharedKOracleInterop(t *testing.T) {
	if _, err := os.Stat("../../services/contracts/node_modules/ajv"); err != nil {
		t.Fatal("K oracle dependencies missing: run npm ci --ignore-scripts --no-audit --no-fund in services/contracts")
	}
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now().Unix()
	claims := sharedTestClaims(true, now)
	claims["canonicalPath"] = "/path&<>/中文"
	token, err := sharedSign(claims, "test", priv)
	if err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]any{"identity": token, "publicX": base64.RawURLEncoding.EncodeToString(pub), "now": now, "ticketJti": sharedJTI()})
	cmd := exec.Command("node", "testdata/shared-oracle.mjs")
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("K oracle: %v %s", err, out)
	}
	var reply struct {
		Ticket  string
		PublicX string
	}
	if json.Unmarshal(out, &reply) != nil {
		t.Fatal(string(out))
	}
	key, err := base64.RawURLEncoding.DecodeString(reply.PublicX)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sharedVerify(reply.Ticket, map[string]ed25519.PublicKey{"w-test": key}, false, now); err != nil {
		t.Fatal("W oracle ticket rejected", err)
	}
}
