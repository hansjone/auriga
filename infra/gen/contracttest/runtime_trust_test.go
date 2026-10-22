package contracttest

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type runtimeFixture struct {
	Producer string
	Resigned struct{ Canonical, PublicKeySpki, Signature string }
	Cases    []struct {
		Name, Schema, Canonical, SHA256, PublicKeySpki, Signature string
		Document                                                  map[string]any
		Timestamp                                                 struct {
			Seconds int64
			Nanos   int32
		}
	}
	Keys []struct {
		Kid, PublicKey, PublicKeySpki, EndorsedByKid, Endorsement, ParentPublicKeySpki, InputHex string
		InputLength                                                                              int
	}
}

func readRuntimeFixture(t *testing.T, file string) runtimeFixture {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var fixture runtimeFixture
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Producer != "Java JCA Ed25519" || len(fixture.Cases) != 14 {
		t.Fatal("expected fourteen Java-signed runtime/standalone vectors")
	}
	return fixture
}

// Always verifies committed Java-generated signatures; the optional file additionally verifies a fresh Java run.
func TestRuntimeJavaGoldenVectors(t *testing.T) {
	files := []string{filepath.Join("..", "..", "..", "contracts", "runtime", "signature-vectors.json")}
	if file := os.Getenv("AURIGA_JAVA_RUNTIME_VECTORS"); file != "" {
		files = append(files, file)
	}
	for _, file := range files {
		fixture := readRuntimeFixture(t, file)
		if fixture.Resigned.Canonical != fixture.Cases[0].Canonical || fixture.Resigned.PublicKeySpki == fixture.Cases[0].PublicKeySpki {
			t.Fatal("re-signing must preserve the complete body with a different key")
		}
		assertSignature(t, fixture.Resigned.PublicKeySpki, fixture.Resigned.Signature, fixture.Resigned.Canonical)
		for _, v := range fixture.Cases {
			t.Run(filepath.Base(file)+"/"+v.Name, func(t *testing.T) {
				signature := v.Signature
				if v.Schema == "runtime" {
					signature = v.Document["signature"].(map[string]any)["value"].(string)
					delete(v.Document, "signature")
				}
				canonical, err := canonicalJSON(v.Document)
				if err != nil {
					t.Fatal(err)
				}
				if canonical != v.Canonical {
					t.Fatal("canonical mapping changed")
				}
				digest := sha256.Sum256([]byte(canonical))
				if hex.EncodeToString(digest[:]) != v.SHA256 {
					t.Fatal("body digest changed")
				}
				assertSignature(t, v.PublicKeySpki, signature, canonical)
				timestamp := &timestamppb.Timestamp{Seconds: v.Timestamp.Seconds, Nanos: v.Timestamp.Nanos}
				instant, err := runtimeTimestamp(timestamp)
				if err != nil {
					t.Fatal(err)
				}
				if instant != v.Document["issuedAt"] {
					t.Fatal("Timestamp mapping differs from Java")
				}
				get := func(key string) string { return v.Document[key].(string) }
				var mapped map[string]any
				if v.Schema == "runtime" {
					n, err := strconv.ParseUint(string(v.Document["releaseSequence"].(json.Number)), 10, 64)
					if err != nil {
						t.Fatal(err)
					}
					model := &nodev1.RuntimeReleaseEnvelope{SchemaVersion: 1, Purpose: get("purpose"), Name: get("name"), ArtifactId: get("artifactId"), DshVersion: get("dshVersion"), Os: get("os"), Arch: get("arch"), ArchiveDigest: get("archiveDigest"), ImageDigest: get("imageDigest"), ReleaseSequence: n, IssuedAt: timestamp}
					mapped, err = runtimeMapping(model)
					if err != nil {
						t.Fatal(err)
					}
				} else {
					model := &nodev1.ArtifactStatement{Purpose: get("purpose"), Name: get("name"), Version: get("version"), Os: get("os"), Arch: get("arch"), Digest: get("digest"), IssuedAt: timestamp}
					mapped = map[string]any{"purpose": model.Purpose, "name": model.Name, "version": model.Version, "os": model.Os, "arch": model.Arch, "digest": model.Digest, "issuedAt": instant}
				}
				roundtrip, err := canonicalJSON(mapped)
				if err != nil || roundtrip != canonical {
					t.Fatalf("generated protobuf mapping differs: %s %v", roundtrip, err)
				}
			})
		}
		for _, key := range fixture.Keys {
			if key.EndorsedByKid == "" {
				continue
			}
			raw, err := base64.RawURLEncoding.Strict().DecodeString(key.PublicKey)
			if err != nil || len(raw) != 32 {
				t.Fatal("invalid raw key")
			}
			input := append([]byte(key.Kid), raw...)
			if len(input) != key.InputLength || hex.EncodeToString(input) != key.InputHex {
				t.Fatal("endorsement input differs from Java")
			}
			assertSignature(t, key.ParentPublicKeySpki, key.Endorsement, string(input))
			sig, err := base64.RawURLEncoding.Strict().DecodeString(key.Endorsement)
			if err != nil {
				t.Fatal(err)
			}
			var parent []byte
			for _, candidate := range fixture.Keys {
				if candidate.Kid == key.EndorsedByKid {
					parent, err = base64.RawURLEncoding.Strict().DecodeString(candidate.PublicKey)
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			if len(parent) != 32 || !ed25519.Verify(parent, input, sig) {
				t.Fatal("raw v1 endorsement rejected")
			}
			for _, wrong := range [][]byte{append([]byte("auriga/v1/"), input...), append(append([]byte(key.Kid), 0), raw...), []byte(hex.EncodeToString(input))} {
				if ed25519.Verify(parent, wrong, sig) {
					t.Fatal("v1 domain/input changed unnoticed")
				}
			}
			if ed25519.Verify(raw, input, sig) {
				t.Fatal("wrong parent accepted")
			}
		}
	}
}

func runtimeTimestamp(ts *timestamppb.Timestamp) (string, error) {
	if err := ts.CheckValid(); err != nil {
		return "", err
	}
	return ts.AsTime().UTC().Format("2006-01-02T15:04:05.999999999Z"), nil
}

func runtimeMapping(m *nodev1.RuntimeReleaseEnvelope) (map[string]any, error) {
	if m.ReleaseSequence < 1 || m.ReleaseSequence > 9007199254740991 || len(m.ProtoReflect().GetUnknown()) > 0 {
		return nil, fmt.Errorf("runtime mapping")
	}
	instant, err := runtimeTimestamp(m.IssuedAt)
	if err != nil {
		return nil, err
	}
	return map[string]any{"schemaVersion": json.Number(strconv.FormatUint(uint64(m.SchemaVersion), 10)), "purpose": m.Purpose, "name": m.Name, "artifactId": m.ArtifactId, "dshVersion": m.DshVersion, "os": m.Os, "arch": m.Arch, "archiveDigest": m.ArchiveDigest, "imageDigest": m.ImageDigest, "releaseSequence": json.Number(strconv.FormatUint(m.ReleaseSequence, 10)), "issuedAt": instant}, nil
}

func TestRuntimeTimestampAndGeneratedFieldBoundaries(t *testing.T) {
	for _, ts := range []*timestamppb.Timestamp{nil, {Seconds: -62135596801}, {Seconds: 253402300800}, {Nanos: -1}, {Nanos: 1000000000}} {
		if _, err := runtimeTimestamp(ts); err == nil {
			t.Fatal("invalid Timestamp accepted")
		}
	}
	for _, item := range []struct {
		Seconds int64
		Nanos   int32
		Want    string
	}{{-62135596800, 0, "0001-01-01T00:00:00Z"}, {253402300799, 999999999, "9999-12-31T23:59:59.999999999Z"}, {0, 100000000, "1970-01-01T00:00:00.1Z"}, {0, 123000000, "1970-01-01T00:00:00.123Z"}, {0, 123456000, "1970-01-01T00:00:00.123456Z"}, {0, 1, "1970-01-01T00:00:00.000000001Z"}} {
		got, err := runtimeTimestamp(&timestamppb.Timestamp{Seconds: item.Seconds, Nanos: item.Nanos})
		if err != nil || got != item.Want {
			t.Fatalf("%s %v", got, err)
		}
	}
	model := &nodev1.RuntimeReleaseEnvelope{ReleaseSequence: 1, IssuedAt: timestamppb.New(time.Unix(0, 0))}
	for _, n := range []uint64{0, 9007199254740992, ^uint64(0)} {
		model.ReleaseSequence = n
		if _, err := runtimeMapping(model); err == nil {
			t.Fatal("bad sequence accepted")
		}
	}
	for _, n := range []uint64{1, 9007199254740991} {
		model.ReleaseSequence = n
		if _, err := runtimeMapping(model); err != nil {
			t.Fatal(err)
		}
	}
	model.ProtoReflect().SetUnknown([]byte{0x98, 0x06, 0x01})
	if _, err := runtimeMapping(model); err == nil {
		t.Fatal("unknown protobuf field accepted")
	}
	fields := (&nodev1.DshRuntime{}).ProtoReflect().Descriptor().Fields()
	for name, number := range map[protoreflect.Name]protoreflect.FieldNumber{"artifact_id": 1, "version": 2, "digest": 3, "url": 4, "signature": 5, "signing_kid": 6, "image": 7, "signed": 8, "release_envelope": 9} {
		if fields.ByName(name).Number() != number {
			t.Fatal("DshRuntime wire compatibility")
		}
	}
	body := map[string]any{"😀": "é", "\ue000": "e\u0301"}
	got, err := canonicalJSON(body)
	if err != nil || !strings.HasPrefix(got, "{\"😀\"") {
		t.Fatal("UTF-16 ordering")
	}
}
