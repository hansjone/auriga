package releaseverify

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/releasetrust"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func trust(t *testing.T, pub ed25519.PublicKey, kid string) releasetrust.Ring {
	t.Helper()
	r, err := releasetrust.Merge(releasetrust.Ring{}, []*nodev1.ArtifactSigningKey{{Kid: kid, PublicKey: pub, NotAfter: timestamppb.New(time.Now().Add(time.Hour))}}, true, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func fixture(t *testing.T) (*nodev1.DshRuntime, releasetrust.Ring, ed25519.PrivateKey, Target) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	e := &nodev1.RuntimeReleaseEnvelope{SchemaVersion: 1, Purpose: "dsh_runtime", Name: "dsh", ArtifactId: "dsh-test", DshVersion: "1.0.0", Os: "linux", Arch: "amd64", ArchiveDigest: "sha256:" + strings.Repeat("a", 64), ImageDigest: "sha256:" + strings.Repeat("b", 64), ReleaseSequence: 7, IssuedAt: timestamppb.New(time.Now()), Signature: &nodev1.RuntimeReleaseSignature{Alg: "Ed25519", Kid: "root"}}
	d := &nodev1.DshRuntime{ArtifactId: e.ArtifactId, Version: e.DshVersion, Digest: e.ArchiveDigest, Url: "https://download.example.com/dsh.tar.gz", Image: "registry.example.com/auriga/dsh@" + e.ImageDigest, ReleaseEnvelope: e}
	sign(t, d, priv)
	return d, trust(t, pub, "root"), priv, Target{OS: "linux", Arch: "amd64", Driver: "docker", AllowImage: func(string) error { return nil }}
}
func sign(t *testing.T, d *nodev1.DshRuntime, key ed25519.PrivateKey) {
	t.Helper()
	body, err := releaseBody(d.ReleaseEnvelope)
	if err != nil {
		t.Fatal(err)
	}
	d.ReleaseEnvelope.Signature.Value = base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, body))
}
func legacy(t *testing.T, d *nodev1.DshRuntime, key ed25519.PrivateKey) {
	t.Helper()
	e := d.ReleaseEnvelope
	s := &nodev1.ArtifactStatement{Purpose: e.Purpose, Name: e.Name, Version: e.DshVersion, Os: e.Os, Arch: e.Arch, Digest: e.ArchiveDigest, IssuedAt: proto.Clone(e.IssuedAt).(*timestamppb.Timestamp)}
	body, err := standaloneBody(s)
	if err != nil {
		t.Fatal(err)
	}
	d.Signed = &nodev1.SignedArtifact{Statement: s, SigningKid: e.Signature.Kid, Signature: ed25519.Sign(key, body)}
}
func rejected(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("wanted ErrInvalid, got %v", err)
	}
}

func TestReleaseRejectsEverySignedFieldMutation(t *testing.T) {
	d, ring, _, target := fixture(t)
	mutations := map[string]func(*nodev1.RuntimeReleaseEnvelope){
		"schemaVersion":   func(e *nodev1.RuntimeReleaseEnvelope) { e.SchemaVersion++ },
		"purpose":         func(e *nodev1.RuntimeReleaseEnvelope) { e.Purpose += "x" },
		"name":            func(e *nodev1.RuntimeReleaseEnvelope) { e.Name += "x" },
		"artifactId":      func(e *nodev1.RuntimeReleaseEnvelope) { e.ArtifactId += "x" },
		"dshVersion":      func(e *nodev1.RuntimeReleaseEnvelope) { e.DshVersion += "x" },
		"os":              func(e *nodev1.RuntimeReleaseEnvelope) { e.Os = "windows" },
		"arch":            func(e *nodev1.RuntimeReleaseEnvelope) { e.Arch = "arm64" },
		"archiveDigest":   func(e *nodev1.RuntimeReleaseEnvelope) { e.ArchiveDigest = "sha256:" + strings.Repeat("c", 64) },
		"imageDigest":     func(e *nodev1.RuntimeReleaseEnvelope) { e.ImageDigest = "sha256:" + strings.Repeat("c", 64) },
		"releaseSequence": func(e *nodev1.RuntimeReleaseEnvelope) { e.ReleaseSequence++ },
		"issuedAt":        func(e *nodev1.RuntimeReleaseEnvelope) { e.IssuedAt.Nanos = (e.IssuedAt.Nanos + 1) % 1000000000 },
		"alg":             func(e *nodev1.RuntimeReleaseEnvelope) { e.Signature.Alg = "other" },
		"kid":             func(e *nodev1.RuntimeReleaseEnvelope) { e.Signature.Kid = "unknown" },
		"value":           func(e *nodev1.RuntimeReleaseEnvelope) { e.Signature.Value = strings.Repeat("A", 86) },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			bad := proto.Clone(d).(*nodev1.DshRuntime)
			mutate(bad.ReleaseEnvelope)
			// Keep unsigned mappings consistent so changed valid signed values
			// reach cryptographic verification, not an incidental mapping guard.
			e := bad.ReleaseEnvelope
			bad.ArtifactId, bad.Version, bad.Digest = e.ArtifactId, e.DshVersion, e.ArchiveDigest
			bad.Image = "registry.example.com/auriga/dsh@" + e.ImageDigest
			changedTarget := target
			changedTarget.OS, changedTarget.Arch = e.Os, e.Arch
			_, err := Verify(bad, ring, changedTarget)
			rejected(t, err)
		})
	}
}

func TestReleaseMalformedAndMapping(t *testing.T) {
	d, ring, key, target := fixture(t)
	legacy(t, d, key)
	unknown := []byte{0x98, 0x06, 0x01}
	cases := map[string]func(*nodev1.DshRuntime){
		"no envelope":              func(d *nodev1.DshRuntime) { d.ReleaseEnvelope = nil },
		"runtime unknown":          func(d *nodev1.DshRuntime) { d.ProtoReflect().SetUnknown(unknown) },
		"envelope unknown":         func(d *nodev1.DshRuntime) { d.ReleaseEnvelope.ProtoReflect().SetUnknown(unknown) },
		"signature unknown":        func(d *nodev1.DshRuntime) { d.ReleaseEnvelope.Signature.ProtoReflect().SetUnknown(unknown) },
		"timestamp unknown":        func(d *nodev1.DshRuntime) { d.ReleaseEnvelope.IssuedAt.ProtoReflect().SetUnknown(unknown) },
		"legacy unknown":           func(d *nodev1.DshRuntime) { d.Signed.ProtoReflect().SetUnknown(unknown) },
		"statement unknown":        func(d *nodev1.DshRuntime) { d.Signed.Statement.ProtoReflect().SetUnknown(unknown) },
		"legacy timestamp unknown": func(d *nodev1.DshRuntime) { d.Signed.Statement.IssuedAt.ProtoReflect().SetUnknown(unknown) },
		"negative nanos":           func(d *nodev1.DshRuntime) { d.ReleaseEnvelope.IssuedAt.Nanos = -1 },
		"overflow nanos":           func(d *nodev1.DshRuntime) { d.ReleaseEnvelope.IssuedAt.Nanos = 1000000000 },
		"before year 1":            func(d *nodev1.DshRuntime) { d.ReleaseEnvelope.IssuedAt.Seconds = -62135596801 },
		"after year 9999":          func(d *nodev1.DshRuntime) { d.ReleaseEnvelope.IssuedAt.Seconds = 253402300800 },
		"missing timestamp":        func(d *nodev1.DshRuntime) { d.ReleaseEnvelope.IssuedAt = nil },
		"zero sequence":            func(d *nodev1.DshRuntime) { d.ReleaseEnvelope.ReleaseSequence = 0 },
		"unsafe sequence":          func(d *nodev1.DshRuntime) { d.ReleaseEnvelope.ReleaseSequence = maxSafeInteger + 1 },
		"bad UTF8":                 func(d *nodev1.DshRuntime) { d.ReleaseEnvelope.ArtifactId = string([]byte{0xff}) },
		"padded signature":         func(d *nodev1.DshRuntime) { d.ReleaseEnvelope.Signature.Value += "==" },
		"signature newline":        func(d *nodev1.DshRuntime) { d.ReleaseEnvelope.Signature.Value += "\n" },
		"nonzero pad bits": func(d *nodev1.DshRuntime) {
			s := d.ReleaseEnvelope.Signature.Value
			d.ReleaseEnvelope.Signature.Value = s[:85] + "B"
		},
		"short signature":  func(d *nodev1.DshRuntime) { d.ReleaseEnvelope.Signature.Value = "AA" },
		"bad kid":          func(d *nodev1.DshRuntime) { d.ReleaseEnvelope.Signature.Kid = "with space" },
		"runtime artifact": func(d *nodev1.DshRuntime) { d.ArtifactId += "x" },
		"runtime version":  func(d *nodev1.DshRuntime) { d.Version += "x" },
		"runtime digest":   func(d *nodev1.DshRuntime) { d.Digest = "" },
		"missing url":      func(d *nodev1.DshRuntime) { d.Url = "" },
		"image swapped archive unchanged": func(d *nodev1.DshRuntime) {
			d.Image = "registry.example.com/auriga/dsh@sha256:" + strings.Repeat("c", 64)
		},
		"legacy signature":       func(d *nodev1.DshRuntime) { d.Signed.Signature[0] ^= 1 },
		"legacy unknown key":     func(d *nodev1.DshRuntime) { d.Signed.SigningKid = "unknown" },
		"legacy short signature": func(d *nodev1.DshRuntime) { d.Signed.Signature = []byte{1} },
		"legacy no statement":    func(d *nodev1.DshRuntime) { d.Signed.Statement = nil },
		"legacy purpose":         func(d *nodev1.DshRuntime) { d.Signed.Statement.Purpose += "x" },
		"legacy name":            func(d *nodev1.DshRuntime) { d.Signed.Statement.Name += "x" },
		"legacy version":         func(d *nodev1.DshRuntime) { d.Signed.Statement.Version += "x" },
		"legacy os":              func(d *nodev1.DshRuntime) { d.Signed.Statement.Os = "any" },
		"legacy arch":            func(d *nodev1.DshRuntime) { d.Signed.Statement.Arch = "any" },
		"legacy digest":          func(d *nodev1.DshRuntime) { d.Signed.Statement.Digest += "x" },
		"legacy timestamp":       func(d *nodev1.DshRuntime) { d.Signed.Statement.IssuedAt.Seconds++ },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			bad := proto.Clone(d).(*nodev1.DshRuntime)
			mutate(bad)
			_, err := Verify(bad, ring, target)
			rejected(t, err)
		})
	}
	for _, field := range []string{"OS", "Arch", "Driver", "Policy", "MissingPolicy"} {
		t.Run(field, func(t *testing.T) {
			target := target
			switch field {
			case "OS":
				target.OS = "windows"
			case "Arch":
				target.Arch = "arm64"
			case "Driver":
				target.Driver = "other"
			case "MissingPolicy":
				target.AllowImage = nil
			case "Policy":
				target.AllowImage = func(string) error { return errors.New("denied") }
			}
			_, err := Verify(d, ring, target)
			rejected(t, err)
		})
	}
	if _, err := Verify(nil, ring, target); err == nil {
		t.Fatal("nil runtime accepted")
	}
}

func TestReleaseModesBoundariesAndIsolation(t *testing.T) {
	d, ring, key, target := fixture(t)
	for _, ts := range []*timestamppb.Timestamp{{Seconds: -62135596800}, {Seconds: 253402300799, Nanos: 999999999}, {Seconds: 0, Nanos: 1}} {
		d.ReleaseEnvelope.IssuedAt = ts
		d.ReleaseEnvelope.ReleaseSequence = maxSafeInteger
		sign(t, d, key)
		if _, err := Verify(d, ring, target); err != nil {
			t.Fatal(err)
		}
	}
	legacy(t, d, key)
	v, err := Verify(d, ring, target)
	if err != nil {
		t.Fatal(err)
	}
	original := v.BodyDigest()
	b := v.CanonicalBody()
	b[0] = 'x'
	d.ReleaseEnvelope.ArtifactId = "changed"
	d.Signed.Signature[0] ^= 1
	if v.BodyDigest() != original || v.CanonicalBody()[0] != '{' || v.ArtifactID() != "dsh-test" || v.Sequence() != maxSafeInteger || v.DSHVersion() != "1.0.0" || v.Partition() != (Partition{"dsh_runtime", "dsh", "linux", "amd64"}) || !v.Valid() {
		t.Fatal("mutable verified result")
	}
	if err := v.Recheck(ring); err != nil {
		t.Fatal(err)
	}
	rejected(t, (VerifiedRelease{}).Recheck(ring))
	rejected(t, v.Recheck(releasetrust.Ring{}))
	expired := ring.Clone()
	expired.Records[0].Deadline = releasetrust.FromTime(time.Now().Add(-time.Nanosecond))
	rejected(t, v.Recheck(expired))
	wrongPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	wrong := trust(t, wrongPub, "root")
	rejected(t, v.Recheck(wrong))
	// Both legacy and new key deadlines must be checked again, not cached.
	pub2, key2, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	combined, err := releasetrust.Merge(releasetrust.Ring{}, []*nodev1.ArtifactSigningKey{{Kid: "root", PublicKey: key.Public().(ed25519.PublicKey), NotAfter: timestamppb.New(time.Now().Add(time.Hour))}, {Kid: "legacy", PublicKey: pub2, NotAfter: timestamppb.New(time.Now().Add(time.Hour))}}, true, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	d, _, _, target = fixture(t)
	sign(t, d, key)
	legacy(t, d, key2)
	d.Signed.SigningKid = "legacy"
	v, err = Verify(d, combined, target)
	if err != nil {
		t.Fatal(err)
	}
	for i := range combined.Records {
		if combined.Records[i].Kid == "legacy" {
			combined.Records[i].Deadline = releasetrust.FromTime(time.Now().Add(-time.Second))
		}
	}
	rejected(t, v.Recheck(combined))
	d.Signed = nil
	d.Url = ""
	d.Digest = ""
	d.ReleaseEnvelope.ArchiveDigest = ""
	sign(t, d, key)
	if _, err := Verify(d, ring, target); err != nil {
		t.Fatal(err)
	}
	legacy(t, d, key)
	_, err = Verify(d, ring, target)
	rejected(t, err)
	d.Signed = nil
	for _, osName := range []string{"linux", "windows"} {
		d, ring, key, target = fixture(t)
		d.Image = ""
		d.ReleaseEnvelope.ImageDigest = ""
		d.ReleaseEnvelope.Os = osName
		target.OS = osName
		target.Driver = osName + "-process"
		sign(t, d, key)
		if _, err := Verify(d, ring, target); err != nil {
			t.Fatal(err)
		}
		d.Image = "unexpected"
		_, err = Verify(d, ring, target)
		rejected(t, err)
	}
}

func TestPinnedRepositorySyntax(t *testing.T) {
	sum := "sha256:" + strings.Repeat("a", 64)
	for _, repo := range []string{"registry.example.com/dsh", "registry.example.com:5000/a/dsh", "registry:5000/auriga/dsh", "localhost/dsh", "192.0.2.10/dsh", "registry.example.com/a__b/c--d"} {
		if !pinnedImage(repo+"@"+sum, sum) {
			t.Fatalf("valid repo: %s", repo)
		}
	}
	for _, repo := range []string{"dsh", "user/dsh", "Registry.example.com/dsh", "https://registry.example.com/dsh", "registry.example.com/dsh:latest", "registry.example.com/../dsh", "registry.example.com//dsh", "registry.example.com/dsh/", "registry.example.com:05000/dsh", "registry.example.com:0/dsh", "registry.example.com:65536/dsh", "registry.example.com./dsh", "registry.example.com/-dsh", "registry.example.com/a@b", "registry.example.com/a?x", "registry.example.com/a#x", "registry.example.com/a%2Fb"} {
		if pinnedImage(repo+"@"+sum, sum) {
			t.Fatalf("bad repo accepted: %s", repo)
		}
	}
}

func TestSingleLabelRegistryPortStillRequiresPolicy(t *testing.T) {
	d, ring, _, target := fixture(t)
	d.Image = "registry:5000/auriga/dsh@" + d.ReleaseEnvelope.ImageDigest
	seen := ""
	target.AllowImage = func(image string) error { seen = image; return nil }
	if _, err := Verify(d, ring, target); err != nil || seen != d.Image {
		t.Fatalf("canonical local registry: %q %v", seen, err)
	}
	target.AllowImage = func(string) error { return errors.New("denied") }
	_, err := Verify(d, ring, target)
	rejected(t, err)
	d.Image = "registry/auriga/dsh@" + d.ReleaseEnvelope.ImageDigest
	target.AllowImage = func(string) error { t.Fatal("ambiguous short repo reached policy"); return nil }
	_, err = Verify(d, ring, target)
	rejected(t, err)
}

func TestJCSUnicodeAndEscaping(t *testing.T) {
	body, err := canonical(map[string]string{"\ue000": "<>&/\u2028\u2029", "😀": "é é\x00\b\t\n\f\r\x1f\"\\"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\"😀\":\"é é\\u0000\\b\\t\\n\\f\\r\\u001f\\\"\\\\\",\"\ue000\":\"<>&/\u2028\u2029\"}"
	if string(body) != want {
		t.Fatalf("JCS %q != %q", body, want)
	}
	if _, err := canonical(map[string]string{"bad": string([]byte{0xff})}, nil); err == nil {
		t.Fatal("bad Unicode accepted")
	}
}

type vector struct {
	Name, Schema, Canonical, SHA256, PublicKeySpki, Signature string
	Timestamp                                                 *timestamppb.Timestamp
	Document                                                  json.RawMessage
}

func TestJavaSharedVectors(t *testing.T) {
	paths := []string{filepath.Join("..", "..", "..", "..", "contracts", "runtime", "signature-vectors.json")}
	if p := os.Getenv("AURIGA_JAVA_RUNTIME_VECTORS"); p != "" {
		paths = append(paths, p)
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var vectors struct {
				Cases    []vector
				Resigned struct{ Canonical, PublicKeySpki, Signature string }
			}
			if err := json.Unmarshal(raw, &vectors); err != nil {
				t.Fatal(err)
			}
			if len(vectors.Cases) < 14 {
				t.Fatalf("incomplete shared vectors: %d", len(vectors.Cases))
			}
			for _, v := range vectors.Cases {
				t.Run(v.Name, func(t *testing.T) {
					spki, err := base64.RawURLEncoding.DecodeString(v.PublicKeySpki)
					if err != nil {
						t.Fatal(err)
					}
					pub, err := x509.ParsePKIXPublicKey(spki)
					if err != nil {
						t.Fatal(err)
					}
					ring := trust(t, pub.(ed25519.PublicKey), "release-root")
					var body []byte
					switch v.Schema {
					case "runtime":
						var doc struct {
							SchemaVersion                                                               uint32
							Purpose, Name, ArtifactID, DSHVersion, OS, Arch, ArchiveDigest, ImageDigest string
							ReleaseSequence                                                             uint64
							Signature                                                                   struct{ Alg, Kid, Value string }
						}
						if err := json.Unmarshal(v.Document, &doc); err != nil {
							t.Fatal(err)
						}
						e := &nodev1.RuntimeReleaseEnvelope{SchemaVersion: doc.SchemaVersion, Purpose: doc.Purpose, Name: doc.Name, ArtifactId: doc.ArtifactID, DshVersion: doc.DSHVersion, Os: doc.OS, Arch: doc.Arch, ArchiveDigest: doc.ArchiveDigest, ImageDigest: doc.ImageDigest, ReleaseSequence: doc.ReleaseSequence, IssuedAt: v.Timestamp, Signature: &nodev1.RuntimeReleaseSignature{Alg: doc.Signature.Alg, Kid: doc.Signature.Kid, Value: doc.Signature.Value}}
						d := &nodev1.DshRuntime{ArtifactId: e.ArtifactId, Version: e.DshVersion, Digest: e.ArchiveDigest, Image: "registry.example.com/auriga/dsh@" + e.ImageDigest, ReleaseEnvelope: e}
						target := Target{OS: e.Os, Arch: e.Arch, Driver: "docker", AllowImage: func(string) error { return nil }}
						if e.ImageDigest == "" {
							d.Image = ""
							target.Driver = e.Os + "-process"
						}
						if e.ArchiveDigest != "" {
							d.Url = "https://download.example.com/dsh"
							for _, old := range vectors.Cases {
								if old.Schema != "standalone" || old.Timestamp.Seconds != e.IssuedAt.Seconds || old.Timestamp.Nanos != e.IssuedAt.Nanos {
									continue
								}
								var statement struct{ Purpose, Name, Version, OS, Arch, Digest string }
								if err := json.Unmarshal(old.Document, &statement); err != nil {
									t.Fatal(err)
								}
								sig, err := base64.RawURLEncoding.DecodeString(old.Signature)
								if err != nil {
									t.Fatal(err)
								}
								d.Signed = &nodev1.SignedArtifact{Statement: &nodev1.ArtifactStatement{Purpose: statement.Purpose, Name: statement.Name, Version: statement.Version, Os: statement.OS, Arch: statement.Arch, Digest: statement.Digest, IssuedAt: old.Timestamp}, SigningKid: "release-root", Signature: sig}
								break
							}
							if d.Signed == nil {
								t.Fatal("missing matching shared standalone vector")
							}
						}
						verified, err := Verify(d, ring, target)
						if err != nil {
							t.Fatal(err)
						}
						body = verified.CanonicalBody()
					case "standalone":
						var doc struct{ Purpose, Name, Version, OS, Arch, Digest string }
						if err := json.Unmarshal(v.Document, &doc); err != nil {
							t.Fatal(err)
						}
						s := &nodev1.ArtifactStatement{Purpose: doc.Purpose, Name: doc.Name, Version: doc.Version, Os: doc.OS, Arch: doc.Arch, Digest: doc.Digest, IssuedAt: v.Timestamp}
						body, err = standaloneBody(s)
						if err != nil {
							t.Fatal(err)
						}
						sig, err := base64.RawURLEncoding.DecodeString(v.Signature)
						if err != nil {
							t.Fatal(err)
						}
						if err := verifySignature(ring, "release-root", body, sig); err != nil {
							t.Fatal(err)
						}
					default:
						t.Fatalf("unknown vector schema %s", v.Schema)
					}
					if !bytes.Equal(body, []byte(v.Canonical)) || digest(body) != "sha256:"+v.SHA256 {
						t.Fatal("Java JCS/digest mismatch")
					}
				})
			}
			spki, err := base64.RawURLEncoding.DecodeString(vectors.Resigned.PublicKeySpki)
			if err != nil {
				t.Fatal(err)
			}
			pub, err := x509.ParsePKIXPublicKey(spki)
			if err != nil {
				t.Fatal(err)
			}
			sig, err := base64.RawURLEncoding.DecodeString(vectors.Resigned.Signature)
			if err != nil {
				t.Fatal(err)
			}
			if err := verifySignature(trust(t, pub.(ed25519.PublicKey), "resigned"), "resigned", []byte(vectors.Resigned.Canonical), sig); err != nil {
				t.Fatal(err)
			}
		})
	}
}
