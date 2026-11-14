package runtime

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	resourcev1 "github.com/hansjone/auriga/infra/gen/auriga/resource/v1"
)

func TestUnifiedMigrationEvidenceIsDurableAndImmutable(t *testing.T) {
	f := newUnifiedFixture(t)
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "contracts", "resource", "unified-vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	document, err := DecodeUnifiedJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	var input map[string]any
	for _, value := range ua(document["cases"]) {
		v := um(value)
		if v["id"] == "migration-proof-valid" {
			input = um(v["input"])
			break
		}
	}
	if input == nil {
		t.Fatal("frozen migration case missing")
	}
	f.now = ut(input["now"])
	f.manager.Protocol.Keys = nil
	for purpose, value := range um(document["trust"]) {
		record := um(value)
		der, err := base64.StdEncoding.DecodeString(us(record["publicKeySpkiBase64"]))
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := x509.ParsePKIXPublicKey(der)
		if err != nil {
			t.Fatal(err)
		}
		f.manager.Protocol.Keys = append(f.manager.Protocol.Keys, &resourcev1.ResourceTrustKey{Kid: us(record["kid"]), Purpose: purpose, PublicKey: parsed.(ed25519.PublicKey), NotBefore: "2020-01-01T00:00:00Z", NotAfter: "2100-01-01T00:00:00Z"})
	}
	if err := f.manager.AdmitMigration(f.source.spec.InstanceId, input); err != nil {
		t.Fatal(err)
	}
	state, root, _, err := f.manager.load(f.source.spec.InstanceId)
	if err != nil || len(state.Migrations) != 1 || len(state.Watermarks) != 1 {
		t.Fatalf("proof was not persisted: %+v %v", state, err)
	}
	for _, watermark := range state.Watermarks {
		if len(ua(watermark["verifiedDigestAliases"])) != 2 {
			t.Fatal("A/M aliases missing")
		}
	}
	if err := f.manager.AdmitMigration(f.source.spec.InstanceId, input); err != nil {
		t.Fatal("exact proof replay", err)
	}
	if _, err := os.Stat(filepath.Join(root, "migration-evidence", us(um(um(input["proof"])["unifiedEnvelope"])["versionId"]), "legacy.archive")); err != nil {
		t.Fatal(err)
	}
	input["previousManifestDigest"] = "sha256:" + string(make([]byte, 64))
	if err := f.manager.AdmitMigration(f.source.spec.InstanceId, input); err == nil {
		t.Fatal("replacement M accepted")
	}
	again, _, _, err := f.manager.load(f.source.spec.InstanceId)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Migrations) != len(again.Migrations) {
		t.Fatal("rejected mapping changed facts")
	}
	for key, proof := range state.Migrations {
		if !uequal(proof, again.Migrations[key]) {
			t.Fatal("rejected mapping changed persistent facts")
		}
	}
}
