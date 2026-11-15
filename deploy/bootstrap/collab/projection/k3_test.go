package projection

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"net"
	"path/filepath"
	"testing"

	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

func TestNodePinsKeepEnrollmentSeparateFromProjection(t *testing.T) {
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	clear(key)
	der, e := x509.MarshalPKIXPublicKey(pub)
	if e != nil {
		t.Fatal(e)
	}
	pin := api.NodeControlPin{NodeId: "node-1", TargetId: "node-target", IdentityId: "node-receipt", IdentityRevision: "1", SigningSpki: base64.RawURLEncoding.EncodeToString(der)}
	pins := api.NodeControlPins{SchemaVersion: 2, TenantId: "synthetic-tenant", Realm: "1", Pins: []api.NodeControlPin{pin}}
	schemas, e := LoadSchemas(filepath.Join("../../../..", "contracts/network/auto-renewal-v2.schema.json"))
	if e != nil {
		t.Fatal(e)
	}
	raw, e := Bytes(pins)
	if e != nil {
		t.Fatal(e)
	}
	var decoded api.NodeControlPins
	if e = schemas.Decode("NodeControlPins", raw, &decoded); e != nil {
		t.Fatal(e)
	}
	if e = ValidateNodeControlPins(decoded); e != nil {
		t.Fatal(e)
	}
	var projectionPins api.InstallationPins
	if e = schemas.Decode("InstallationPins", raw, &projectionPins); e == nil {
		t.Fatal("node pin granted projection identity")
	}
	for _, field := range []string{"node", "target", "identity"} {
		other := pin
		other.NodeId = "other-node"
		other.TargetId = "other-target"
		other.IdentityId = "other-identity"
		switch field {
		case "node":
			other.NodeId = pin.NodeId
		case "target":
			other.TargetId = pin.TargetId
		case "identity":
			other.IdentityId = pin.IdentityId
		}
		pins.Pins = []api.NodeControlPin{pin, other}
		if e = ValidateNodeControlPins(pins); e == nil {
			t.Fatal("accepted duplicate", field)
		}
	}
}

func TestLocalHigherGenerationPreservesMaterialHighWater(t *testing.T) {
	var vectors []struct {
		Input struct{ Request api.LocalProjectionRequest }
	}
	fixture(t, "msgnet-local-projection-vectors.json", &vectors)
	old := vectors[0].Input.Request.Selector
	old.BundleVersion = "900"
	old.KeysetVersion = "800"
	old.MaterialRevision = "100"
	current := old
	current.Generation = "2"
	current.KeysetVersion = "1"
	current.BundleVersion = "1"
	current.MaterialRevision = "101"
	// Root-authorized generation reset is verified later by Trust.Load. The
	// local progress gate must not compare independent generation version spaces.
	if e := CheckProgress(current, &old); e != nil {
		t.Fatal(e)
	}
	current.MaterialRevision = "99"
	if e := CheckProgress(current, &old); e == nil {
		t.Fatal("reset durable material revision")
	}
}

func TestSocketRejectsNonCanonicalJCS(t *testing.T) {
	for _, raw := range []string{`{} `, `{"b":1,"a":1}`, `{"a":1,"a":1}`} {
		reader, writer := net.Pipe()
		done := make(chan struct{})
		go func() {
			defer close(done)
			defer func() { _ = writer.Close() }()
			var header [4]byte
			binary.BigEndian.PutUint32(header[:], uint32(len(raw)))
			_, _ = writer.Write(header[:])
			_, _ = writer.Write([]byte(raw))
		}()
		_, e := readFrame(reader)
		_ = reader.Close()
		<-done
		if e == nil {
			t.Fatal("accepted non-canonical socket JSON", raw)
		}
	}
}
