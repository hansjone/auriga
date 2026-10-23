package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

func TestRuntimeStructuralAliases(t *testing.T) {
	base := `{"os":"linux","architecture":"amd64","rootfs":{"type":"layers","diff_ids":[]},"config":{}}`
	cases := []struct{ name, raw string }{
		{"ASCII", `{"OS":"windows","os":"linux","architecture":"amd64"}`},
		{"long-s", `{"o\u017f":"windows","os":"linux","architecture":"amd64"}`},
		{"rootfs", `{"os":"linux","architecture":"amd64","rootfs":{"Type":"layers","type":"layers","diff_ids":[]}}`},
		{"history", `{"os":"linux","architecture":"amd64","history":[{"Empty_Layer":true,"empty_layer":true}]}`},
		{"execution", `{"os":"linux","architecture":"amd64","config":{"workingdir":"/","WorkingDir":"/"}}`},
		{"Kelvin", `{"os":"linux","architecture":"amd64","config":{"healthchec\u212a":{},"Healthcheck":{}}}`},
		{"healthcheck", `{"os":"linux","architecture":"amd64","config":{"Healthcheck":{"startinterval":1,"StartInterval":1}}}`},
		{"historical config", `{"os":"linux","architecture":"amd64","container_config":{"attach\u017ftdin":false,"AttachStdin":false,"macaddress":"","MacAddress":""}}`},
		{"historical healthcheck", `{"os":"linux","architecture":"amd64","container_config":{"Healthcheck":{"RETRIES":1,"Retries":1}}}`},
	}
	buildProof := func(c []byte) ([]byte, string) {
		m := []byte(fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":%q,"size":%d},"layers":[]}`, proofDigest(c), len(c)))
		return m, "127.0.0.1:5000/auriga/fake-dsh@" + proofDigest(m)
	}
	// Reverse raw object member order at every structural nesting level while
	// retaining exact key spelling. This is a second independent JSON input.
	var reverse func(json.RawMessage) []byte
	reverse = func(raw json.RawMessage) []byte {
		if len(raw) == 0 {
			return raw
		}
		if raw[0] == '{' {
			d := json.NewDecoder(bytes.NewReader(raw))
			_, _ = d.Token()
			var fields [][]byte
			for d.More() {
				key, err := d.Token()
				if err != nil {
					t.Fatal(err)
				}
				var value json.RawMessage
				if err := d.Decode(&value); err != nil {
					t.Fatal(err)
				}
				encoded, _ := json.Marshal(key)
				fields = append(fields, append(append(encoded, ':'), reverse(value)...))
			}
			var b bytes.Buffer
			b.WriteByte('{')
			for i := len(fields) - 1; i >= 0; i-- {
				if i != len(fields)-1 {
					b.WriteByte(',')
				}
				b.Write(fields[i])
			}
			b.WriteByte('}')
			return b.Bytes()
		}
		if raw[0] == '[' {
			var values []json.RawMessage
			if err := json.Unmarshal(raw, &values); err != nil {
				t.Fatal(err)
			}
			var b bytes.Buffer
			b.WriteByte('[')
			for i, v := range values {
				if i > 0 {
					b.WriteByte(',')
				}
				b.Write(reverse(v))
			}
			b.WriteByte(']')
			return b.Bytes()
		}
		return raw
	}
	root := t.TempDir()
	binary := filepath.Join(root, "bootstrap")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	if output, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, output)
	}
	for _, tc := range cases {
		for _, order := range []string{"original", "reversed"} {
			t.Run(tc.name+"/"+order, func(t *testing.T) {
				c := []byte(tc.raw)
				if order == "reversed" {
					c = reverse(c)
				}
				m, image := buildProof(c)
				if err := verifyRuntimeProof(image, m, c); err == nil || !strings.Contains(err.Error(), "noncanonical structural JSON member") {
					t.Fatalf("alias not specifically rejected: %v", err)
				}
				proof := t.TempDir()
				leaf := filepath.Join(proof, strings.TrimPrefix(digestOf(image), "sha256:"))
				if err := os.Mkdir(leaf, 0o755); err != nil {
					t.Fatal(err)
				}
				for name, raw := range map[string][]byte{"manifest.json": m, "config.json": c} {
					if err := os.WriteFile(filepath.Join(leaf, name), raw, 0o644); err != nil {
						t.Fatal(err)
					}
				}
				out := filepath.Join(t.TempDir(), "not-created")
				output, err := exec.Command(binary, "-out", out, "-image", image, "-runtime-proofs", proof).CombinedOutput()
				if err == nil || !strings.Contains(string(output), "noncanonical structural JSON member") {
					t.Fatalf("CLI accepted wrong structure: %v %s", err, output)
				}
				if _, err := os.Lstat(out); !os.IsNotExist(err) {
					t.Fatal("rejected proof generated installation/signing material")
				}
			})
		}
	}
	c := []byte(base)
	m, _ := buildProof(c)
	for _, tc := range []struct{ name, from, to string }{
		{"manifest", `"schemaVersion":2`, `"SchemaVersion":2,"schemaVersion":2`},
		{"descriptor", `"config":{"mediaType":`, `"config":{"MediaType":"ignored","mediaType":`},
		{"layer", `"layers":[]`, `"layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar","digest":"sha256:` + strings.Repeat("a", 64) + `","size":1,"ſize":1}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, raw := range [][]byte{[]byte(strings.Replace(string(m), tc.from, tc.to, 1)), reverse([]byte(strings.Replace(string(m), tc.from, tc.to, 1)))} {
				image := "127.0.0.1:5000/auriga/fake-dsh@" + proofDigest(raw)
				if err := verifyRuntimeProof(image, raw, c); err == nil || !strings.Contains(err.Error(), "noncanonical structural JSON member") {
					t.Fatalf("alias accepted: %v", err)
				}
			}
		})
	}
	positive := []byte(`{"os":"linux","architecture":"amd64","rootfs":{"type":"layers","diff_ids":[]},"config":{"Labels":{"OS":"user","oſ":"user","Key":"user"},"ExposedPorts":{"OS":{}},"Volumes":{"/OS":{}}},"container_config":{"Labels":{"OS":"user"},"Volumes":{"OS":{}},"ExposedPorts":{"OS":{}},"future":{"OS":"extension"}},"future":{"OS":"extension"},"İd":"extension"}`)
	m, _ = buildProof(positive)
	m = bytes.Replace(m, []byte(`"layers":[]`), []byte(`"layers":[],"annotations":{"OS":"user"},"future":{"OS":"extension"}`), 1)
	image := "127.0.0.1:5000/auriga/fake-dsh@" + proofDigest(m)
	if err := verifyRuntimeProof(image, m, positive); err != nil {
		t.Fatalf("valid user keys/metadata extension rejected: %v", err)
	}
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
