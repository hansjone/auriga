package releasetrust

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestStrictProtoAndCachedDeadline(t *testing.T) {
	now := time.Now()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := &nodev1.ArtifactSigningKey{Kid: "root", PublicKey: pub, NotAfter: timestamppb.New(now.Add(time.Hour))}
	for name, mutate := range map[string]func(*nodev1.ArtifactSigningKey){
		"unknown key":       func(k *nodev1.ArtifactSigningKey) { k.ProtoReflect().SetUnknown([]byte{0x98, 0x06, 0x01}) },
		"unknown timestamp": func(k *nodev1.ArtifactSigningKey) { k.NotAfter.ProtoReflect().SetUnknown([]byte{0x98, 0x06, 0x01}) },
		"negative nanos":    func(k *nodev1.ArtifactSigningKey) { k.NotAfter.Nanos = -1 },
		"overflow nanos":    func(k *nodev1.ArtifactSigningKey) { k.NotAfter.Nanos = 1000000000 },
		"overflow seconds":  func(k *nodev1.ArtifactSigningKey) { k.NotAfter.Seconds = 253402300800 },
		"nil timestamp":     func(k *nodev1.ArtifactSigningKey) { k.NotAfter = nil },
	} {
		t.Run(name, func(t *testing.T) {
			bad := proto.Clone(root).(*nodev1.ArtifactSigningKey)
			mutate(bad)
			if _, err := Merge(Ring{}, []*nodev1.ArtifactSigningKey{bad}, true, now); err == nil {
				t.Fatal("invalid protobuf accepted")
			}
		})
	}
	ring, err := Merge(Ring{}, []*nodev1.ArtifactSigningKey{root}, true, now)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ring.Lookup("root", now)
	if err != nil {
		t.Fatal(err)
	}
	key[0] ^= 0xff
	if ring.Records[0].PublicKey[0] != pub[0] {
		t.Fatal("lookup exposed mutable cache")
	}
	copyRing := ring.Clone()
	copyRing.Records[0].PublicKey[0] ^= 0xff
	if ring.Records[0].PublicKey[0] != pub[0] {
		t.Fatal("clone exposed mutable cache")
	}
	if _, err := ring.Lookup("root", root.NotAfter.AsTime()); err == nil {
		t.Fatal("cached key accepted at deadline")
	}
}
