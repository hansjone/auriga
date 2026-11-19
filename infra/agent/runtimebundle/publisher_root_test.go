package runtimebundle

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func publisherRootFixture(t *testing.T) (PublisherRootOptions, ed25519.PublicKey, []byte) {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	input := filepath.Join(dir, "approved-issuer-public.pem")
	put(t, input, pemBytes)
	return PublisherRootOptions{PublicKeyPath: input, Kid: "approved-existing-issuer", NotAfter: timestamppb.New(time.Now().Add(time.Hour)), OutputPath: filepath.Join(dir, "root.pb")}, pub, pemBytes
}

func TestWritePublisherRootOriginalRootReader(t *testing.T) {
	o, pub, input := publisherRootFixture(t)
	raw, err := WritePublisherRoot(o)
	if err != nil {
		t.Fatal(err)
	}
	expected := &nodev1.ArtifactSigningKey{Kid: o.Kid, PublicKey: pub, NotAfter: o.NotAfter}
	if !bytes.Equal(raw, marshal(t, expected)) {
		t.Fatal("output differs from the existing deterministic ArtifactSigningKey encoding")
	}
	checked, key, ring, _, err := readRoot(o.OutputPath)
	if err != nil || !proto.Equal(key, expected) || !bytes.Equal(checked, raw) || len(ring.Records) != 1 {
		t.Fatalf("original production root reader rejected metadata: %v", err)
	}
	if key.EndorsedByKid != "" || len(key.Endorsement) != 0 || len(key.ProtoReflect().GetUnknown()) != 0 {
		t.Fatal("publisher metadata introduced an endorsement or unknown field")
	}
	before := bytes.Clone(raw)
	raw[0] ^= 1
	o.NotAfter.Seconds++
	stored, err := os.ReadFile(o.OutputPath)
	if err != nil || !bytes.Equal(stored, before) {
		t.Fatal("caller mutation changed written public metadata")
	}
	publicAfter, err := os.ReadFile(o.PublicKeyPath)
	if err != nil || !bytes.Equal(publicAfter, input) {
		t.Fatal("issuer public input changed")
	}
	entries, err := os.ReadDir(filepath.Dir(o.OutputPath))
	if err != nil || len(entries) != 2 {
		t.Fatal("publisher-root wrote material beyond the one requested public file")
	}
}

func TestWritePublisherRootRejectsMetadataUsingProductionTrust(t *testing.T) {
	o, _, _ := publisherRootFixture(t)
	for _, kid := range []string{"", "not an identifier", "with/slash", "line\nbreak", string(bytes.Repeat([]byte{'a'}, 129)), "非ASCII"} {
		t.Run("kid-"+kid, func(t *testing.T) {
			bad := o
			bad.Kid = kid
			_, err := WritePublisherRoot(bad)
			invalid(t, err)
			if _, err := os.Lstat(o.OutputPath); !os.IsNotExist(err) {
				t.Fatal("invalid metadata created an output")
			}
		})
	}
	for _, expiry := range []*timestamppb.Timestamp{nil, timestamppb.New(time.Now().Add(-time.Hour)), {Seconds: 253402300800}, {Seconds: time.Now().Add(time.Hour).Unix(), Nanos: 1000000000}} {
		bad := o
		bad.NotAfter = expiry
		_, err := WritePublisherRoot(bad)
		invalid(t, err)
		if _, err := os.Lstat(o.OutputPath); !os.IsNotExist(err) {
			t.Fatal("invalid expiry created an output")
		}
	}
}

func TestWritePublisherRootRejectsNonPublicOrAmbiguousSPKI(t *testing.T) {
	o, _, good := publisherRootFixture(t)
	block, _ := pem.Decode(good)
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecDER, err := x509.MarshalPKIXPublicKey(ec.Public())
	if err != nil {
		t.Fatal(err)
	}
	// The private marker contains inert text, not a serialized private key.
	badInputs := map[string][]byte{
		"empty":                nil,
		"raw-der":              block.Bytes,
		"leading-text":         append([]byte("preamble\n"), good...),
		"leading-whitespace":   append([]byte("\n"), good...),
		"trailing-text":        append(bytes.Clone(good), []byte("unexpected")...),
		"second-public-object": append(bytes.Clone(good), good...),
		"private-marker":       pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("inert-non-key-body")}),
		"pem-headers":          pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Headers: map[string]string{"Comment": "not allowed"}, Bytes: block.Bytes}),
		"wrong-algorithm":      pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: ecDER}),
		"malformed-spki":       pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: []byte("not DER")}),
		"extra-der":            pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: append(bytes.Clone(block.Bytes), 0)}),
		"oversize":             append(bytes.Clone(good), bytes.Repeat([]byte{' '}, MaxRootBytes)...),
	}
	for name, input := range badInputs {
		t.Run(name, func(t *testing.T) {
			bad := o
			bad.PublicKeyPath = filepath.Join(t.TempDir(), "input.pem")
			put(t, bad.PublicKeyPath, input)
			_, err := WritePublisherRoot(bad)
			invalid(t, err)
			if _, err := os.Lstat(o.OutputPath); !os.IsNotExist(err) {
				t.Fatal("invalid public input created an output")
			}
		})
	}
}

func TestWritePublisherRootRejectsDiscardedPEMObjects(t *testing.T) {
	o, _, first := publisherRootFixture(t)
	_, laterPublic, later := publisherRootFixture(t)
	end := bytes.Index(first, []byte("-----END PUBLIC KEY-----"))
	if end < 0 {
		t.Fatal("fixture lacks its END marker")
	}
	inputs := map[string][]byte{
		"truncated-first-key-then-complete-later-key": append(bytes.Clone(first[:end]), later...),
		"repeated-begin-before-complete-later-key":    append([]byte("-----BEGIN PUBLIC KEY-----\n"), later...),
	}
	for name, input := range inputs {
		t.Run(name, func(t *testing.T) {
			bad := o
			bad.PublicKeyPath = filepath.Join(t.TempDir(), "ambiguous-public.pem")
			bad.OutputPath = filepath.Join(t.TempDir(), "root.pb")
			put(t, bad.PublicKeyPath, input)
			raw, err := WritePublisherRoot(bad)
			if err == nil {
				var key nodev1.ArtifactSigningKey
				if err := proto.Unmarshal(raw, &key); err != nil {
					t.Fatal(err)
				}
				t.Fatalf("ambiguous PEM accepted; prepared root contains the later public key: %t", bytes.Equal(key.PublicKey, laterPublic))
			}
			invalid(t, err)
			if _, err := os.Lstat(bad.OutputPath); !os.IsNotExist(err) {
				t.Fatal("ambiguous PEM created an output")
			}
		})
	}
}

func TestWritePublisherRootAcceptsSinglePEMLineEndingsAndTrailingWhitespace(t *testing.T) {
	o, expected, original := publisherRootFixture(t)
	for name, input := range map[string][]byte{
		"CRLF":                bytes.ReplaceAll(original, []byte("\n"), []byte("\r\n")),
		"trailing-whitespace": append(bytes.Clone(original), []byte(" \t\r\n")...),
	} {
		t.Run(name, func(t *testing.T) {
			good := o
			good.PublicKeyPath = filepath.Join(t.TempDir(), "single-public.pem")
			good.OutputPath = filepath.Join(t.TempDir(), "root.pb")
			put(t, good.PublicKeyPath, input)
			raw, err := WritePublisherRoot(good)
			if err != nil {
				t.Fatal(err)
			}
			var key nodev1.ArtifactSigningKey
			if err := proto.Unmarshal(raw, &key); err != nil || !bytes.Equal(key.PublicKey, expected) {
				t.Fatal("single PEM did not preserve the public key")
			}
		})
	}
}

func TestWritePublisherRootNeverReplacesExistingOutput(t *testing.T) {
	o, _, _ := publisherRootFixture(t)
	put(t, o.OutputPath, []byte("original-file-must-stay"))
	_, err := WritePublisherRoot(o)
	invalid(t, err)
	current, err := os.ReadFile(o.OutputPath)
	if err != nil || string(current) != "original-file-must-stay" {
		t.Fatal("existing output was changed")
	}
	for _, output := range []string{t.TempDir(), filepath.Join(t.TempDir(), "absent-parent", "root.pb"), filepath.Dir(o.OutputPath) + "/child/../root2.pb", filepath.Join(t.TempDir(), "root.pb:stream")} {
		bad := o
		bad.OutputPath = output
		_, err := WritePublisherRoot(bad)
		invalid(t, err)
	}
}

func TestWritePublisherRootRejectsHardlinkedPublicInput(t *testing.T) {
	o, _, _ := publisherRootFixture(t)
	alias := filepath.Join(filepath.Dir(o.PublicKeyPath), "public-alias.pem")
	if err := os.Link(o.PublicKeyPath, alias); err != nil {
		t.Fatal(err)
	}
	_, err := WritePublisherRoot(o)
	invalid(t, err)
}
