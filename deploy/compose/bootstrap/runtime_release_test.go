package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func runtimeProofFixture(t *testing.T, fixture string) (string, string) {
	t.Helper()
	config := []byte("{\n \"architecture\":\"amd64\", \"os\":\"linux\"\n}")
	manifest := []byte(fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":%q,"size":%d},"layers":[]}`, proofDigest(config), len(config)))
	image := "127.0.0.1:5000/auriga/" + fixture + "-dsh@" + proofDigest(manifest)
	root := t.TempDir()
	leaf := filepath.Join(root, strings.TrimPrefix(proofDigest(manifest), "sha256:"))
	if err := os.Mkdir(leaf, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string][]byte{"manifest.json": manifest, "config.json": config} {
		if err := os.WriteFile(filepath.Join(leaf, name), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root, image
}

func TestRuntimeReleaseJavaVectors(t *testing.T) {
	raw, err := os.ReadFile("../../../contracts/runtime/signature-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors struct {
		Cases []struct {
			Schema, Canonical, PublicKeySpki string
			Document                         json.RawMessage
		}
	}
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, v := range vectors.Cases {
		if v.Schema != "runtime" {
			continue
		}
		var e nodev1.RuntimeReleaseEnvelope
		if err := protojson.Unmarshal(v.Document, &e); err != nil {
			t.Fatal(err)
		}
		body, err := runtimeReleaseBody(&e)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != v.Canonical {
			t.Fatalf("JCS mismatch: %s", body)
		}
		der, err := base64.RawURLEncoding.DecodeString(v.PublicKeySpki)
		if err != nil {
			t.Fatal(err)
		}
		pub, err := x509.ParsePKIXPublicKey(der)
		if err != nil {
			t.Fatal(err)
		}
		sig, err := base64.RawURLEncoding.DecodeString(e.Signature.Value)
		if err != nil || !ed25519.Verify(pub.(ed25519.PublicKey), body, sig) {
			t.Fatal("Java signature rejected")
		}
		count++
	}
	if count < 4 {
		t.Fatalf("missing runtime vectors: %d", count)
	}
}

func TestRuntimeSignerIndependentAndCleared(t *testing.T) {
	out := t.TempDir()
	if err := os.Mkdir(filepath.Join(out, "runtime-keys"), 0o755); err != nil {
		t.Fatal(err)
	}
	signer, err := generateRuntimeRootSigner(out)
	if err != nil {
		t.Fatal(err)
	}
	d := &nodev1.DshRuntime{ArtifactId: "fake-dsh", Version: "0.0.0-fake", Image: "127.0.0.1:5000/auriga/fake-dsh@sha256:" + strings.Repeat("a", 64)}
	if err := signRuntimeRelease(d, signer, time.Date(2026, 10, 23, 0, 0, 0, 100000000, time.UTC)); err != nil {
		t.Fatal(err)
	}
	clear(signer)
	if !bytes.Equal(signer, make([]byte, 64)) {
		t.Fatal("signer not cleared")
	}
	var root nodev1.ArtifactSigningKey
	raw, err := os.ReadFile(filepath.Join(out, "runtime-keys", "root.pb"))
	if err != nil || proto.Unmarshal(raw, &root) != nil {
		t.Fatal("invalid root")
	}
	body, err := runtimeReleaseBody(d.ReleaseEnvelope)
	if err != nil {
		t.Fatal(err)
	}
	sig, _ := base64.RawURLEncoding.DecodeString(d.ReleaseEnvelope.Signature.Value)
	if !ed25519.Verify(root.PublicKey, body, sig) || !bytes.Contains(body, []byte(`"issuedAt":"2026-10-23T00:00:00.1Z"`)) {
		t.Fatal("formal signer mismatch")
	}
	entries, _ := os.ReadDir(filepath.Join(out, "runtime-keys"))
	if len(entries) != 1 {
		t.Fatal("signer material leaked")
	}
	for _, mutate := range []func(*nodev1.RuntimeReleaseEnvelope){func(e *nodev1.RuntimeReleaseEnvelope) { e.ReleaseSequence = 9007199254740992 }, func(e *nodev1.RuntimeReleaseEnvelope) { e.IssuedAt = &timestamppb.Timestamp{Nanos: -1} }, func(e *nodev1.RuntimeReleaseEnvelope) { e.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01}) }} {
		bad := proto.Clone(d.ReleaseEnvelope).(*nodev1.RuntimeReleaseEnvelope)
		mutate(bad)
		if _, err := runtimeReleaseBody(bad); err == nil {
			t.Fatal("invalid release accepted")
		}
	}
}

func TestRuntimeProofRejectsAmbiguity(t *testing.T) {
	root, image := runtimeProofFixture(t, "fake")
	m, c, err := loadRuntimeProofs(root, image)
	if err != nil {
		t.Fatal(err)
	}
	for _, depth := range []int{32, 33} {
		raw := []byte(`{"x":` + strings.Repeat("[", depth-1) + "0" + strings.Repeat("]", depth-1) + "}")
		var v any
		if err := strictProofJSON(raw, &v); (err == nil) != (depth == 32) {
			t.Fatalf("JSON depth %d: %v", depth, err)
		}
	}
	for _, tc := range []struct {
		name string
		m, c []byte
	}{
		{"changed manifest", append(append([]byte{}, m...), ' '), c},
		{"changed config", m, append(append([]byte{}, c...), ' ')},
		{"oversize", bytes.Repeat([]byte(" "), maxProofBytes+1), c},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if verifyRuntimeProof(image, tc.m, tc.c) == nil {
				t.Fatal("bad proof accepted")
			}
		})
	}
	for _, raw := range []string{`{"os":"linux","os":"linux"}`, `{"a":{"x":1,"x":2}}`, `{"a":1} {}`, `{"x":[1,]}`, `{"x":"\ud800"}`, `{"x":"\udc00"}`} {
		var v any
		if strictProofJSON([]byte(raw), &v) == nil {
			t.Fatal("ambiguous JSON accepted")
		}
	}
	for _, media := range []string{"application/vnd.docker.distribution.manifest.v2+json", "application/vnd.oci.image.manifest.v1+json"} {
		ct := "application/vnd.oci.image.config.v1+json"
		if strings.Contains(media, "docker") {
			ct = "application/vnd.docker.container.image.v1+json"
		}
		mm := []byte(fmt.Sprintf(`{"schemaVersion":2,"mediaType":%q,"config":{"mediaType":%q,"digest":%q,"size":%d},"layers":[]}`, media, ct, proofDigest(c), len(c)))
		ii := "127.0.0.1:5000/auriga/fake-dsh@" + proofDigest(mm)
		if err := verifyRuntimeProof(ii, mm, c); err != nil {
			t.Fatal(err)
		}
		for _, replacement := range []string{`"schemaVersion":2,"schemaVersion":2`, `"schemaVersion":2,"manifests":[]`, `"schemaVersion":2,"urls":[]`} {
			bad := []byte(strings.Replace(string(mm), `"schemaVersion":2`, replacement, 1))
			ii := "127.0.0.1:5000/auriga/fake-dsh@" + proofDigest(bad)
			if verifyRuntimeProof(ii, bad, c) == nil {
				t.Fatal("ambiguous/index/URL manifest accepted")
			}
		}
	}
}
