package projection_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hansjone/auriga/deploy/bootstrap/collab/projection"
	"github.com/hansjone/auriga/deploy/bootstrap/collab/projection/testfixture"
	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

func TestTwoYearsFiveDayRotationKeepsDurableHistory(t *testing.T) {
	f := testfixture.New(t)
	schemas, e := projection.LoadSchemas(filepath.Join("../../../..", "contracts/network/auto-renewal-v2.schema.json"))
	if e != nil {
		t.Fatal(e)
	}
	state := testfixture.Dir(t)
	base := time.Now().UTC().Truncate(time.Second)
	now := base
	clock := func() time.Time { return now }
	trust, e := projection.NewTrustWithClock(schemas, f.RootSPKI, f.Initial.Keyset.Payload.TenantId, "1", state, f.Pin, clock)
	if e != nil {
		t.Fatal(e)
	}
	version := uint64(0)
	keys := []api.PublicKey{}
	proofs := map[string]api.FirstPublication{}
	var previous *api.Publication
	var first api.PublicKey
	stamp := func(t time.Time) string { return t.Format(time.RFC3339) }
	appendKey := func(purpose, state string) {
		t.Helper()
		pub, private, e := ed25519.GenerateKey(rand.Reader)
		if e != nil {
			t.Fatal(e)
		}
		clear(private)
		der, e := x509.MarshalPKIXPublicKey(pub)
		if e != nil {
			t.Fatal(e)
		}
		kid := fmt.Sprintf("%s-%d", purpose, version)
		k := api.PublicKey{Kid: kid, Alg: "Ed25519", Purpose: purpose, State: state, NotBefore: stamp(now), NotAfter: stamp(now.Add(8 * 24 * time.Hour)), PublicKeySpki: base64.RawURLEncoding.EncodeToString(der)}
		keys = append(keys, k)
		proofs[kid] = api.FirstPublication{Kid: kid, CommittedVersion: fmt.Sprint(version), CommittedAt: stamp(now)}
		if first.Kid == "" {
			first = k
		}
	}
	publish := func(mode string) {
		t.Helper()
		payload := f.Initial.Keyset.Payload
		payload.Keys = append([]api.PublicKey(nil), keys...)
		payload.FirstPublications = []api.FirstPublication{}
		for _, k := range keys {
			payload.FirstPublications = append(payload.FirstPublications, proofs[k.Kid])
		}
		payload.KeysetVersion = fmt.Sprint(version)
		payload.Mode = mode
		payload.IssuedAt = stamp(now)
		payload.ValidUntil = stamp(now.Add(24 * time.Hour))
		payload.Archive.Before = stamp(now.Add(-360 * time.Second))
		raw, e := projection.Bytes(payload)
		if e != nil {
			t.Fatal(e)
		}
		p := api.Publication{Authority: f.Initial.Authority, Control: f.Initial.Control, Keyset: api.SignedKeysetV2{Payload: payload, RuntimeAuth: api.Signature{Alg: "Ed25519", Kid: f.Initial.Authority.Payload.RuntimeKid, Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(f.Runtime, append([]byte("Auriga.NetworkKeyset.v2\x00"), raw...)))}}}
		if previous != nil {
			digest, _ := projection.ObjectDigest(previous.Keyset)
			p.Predecessor = &api.PredecessorProof{Keyset: previous.Keyset, KeysetDigest: digest, Authority: previous.Authority, Control: previous.Control}
		}
		if e = trust.Accept(p, f.Bundle); e != nil {
			t.Fatalf("virtual day %.0f mode %s: %v", now.Sub(base).Hours()/24, mode, e)
		}
		previous = &p
	}
	purposes := []string{"business_delivery", "protocol_response", "management_state"}
	version++
	for _, purpose := range purposes {
		appendKey(purpose, "active")
	}
	publish("bootstrap")
	for cycle := 1; cycle <= 147; cycle++ {
		now = base.Add(time.Duration(cycle*5-2) * 24 * time.Hour)
		remaining := []api.PublicKey{}
		for _, k := range keys {
			end, e := time.Parse(time.RFC3339, k.NotAfter)
			if e != nil {
				t.Fatal(e)
			}
			if k.State != "retired" || end.After(now.Add(-360*time.Second)) {
				remaining = append(remaining, k)
			}
		}
		keys = remaining
		version++
		for _, purpose := range purposes {
			appendKey(purpose, "next")
		}
		publish("refresh")
		now = base.Add(time.Duration(cycle*5) * 24 * time.Hour)
		version++
		for j := range keys {
			switch keys[j].State {
			case "active":
				keys[j].State = "retired"
			case "next":
				keys[j].State = "active"
			}
		}
		publish("activate")
	}
	restarted, e := projection.NewTrustWithClock(schemas, f.RootSPKI, f.Initial.Keyset.Payload.TenantId, "1", state, f.Pin, clock)
	if e != nil {
		t.Fatal(e)
	}
	if e = restarted.CheckCurrent(*previous); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(filepath.Join(state, "trust-ledger.json"))
	if e != nil {
		t.Fatal(e)
	}
	var stored struct {
		Keys  map[string]api.PublicKey
		First map[string]api.FirstPublication
	}
	if e = json.Unmarshal(raw, &stored); e != nil {
		t.Fatal(e)
	}
	if len(stored.Keys) <= 128 || len(stored.First) <= 128 || len(previous.Keyset.Payload.Keys) > 128 {
		t.Fatal("history was truncated or transmission unbounded")
	}
	// Reusing archived public material under a new kid must be rejected even when
	// the current predecessor no longer contains that old material.
	version++
	now = now.Add(time.Hour)
	reused := first
	reused.Kid = "reused-archived-material"
	reused.State = "next"
	reused.NotBefore = stamp(now)
	reused.NotAfter = stamp(now.Add(7 * 24 * time.Hour))
	payload := previous.Keyset.Payload
	payload.Keys = append(append([]api.PublicKey(nil), payload.Keys...), reused)
	payload.KeysetVersion = fmt.Sprint(version)
	payload.Mode = "refresh"
	payload.IssuedAt = stamp(now)
	payload.ValidUntil = stamp(now.Add(24 * time.Hour))
	payload.FirstPublications = append(append([]api.FirstPublication(nil), payload.FirstPublications...), api.FirstPublication{Kid: reused.Kid, CommittedVersion: payload.KeysetVersion, CommittedAt: payload.IssuedAt})
	body, _ := projection.Bytes(payload)
	d, _ := projection.ObjectDigest(previous.Keyset)
	bad := api.Publication{Authority: previous.Authority, Control: previous.Control, Keyset: api.SignedKeysetV2{Payload: payload, RuntimeAuth: api.Signature{Alg: "Ed25519", Kid: previous.Authority.Payload.RuntimeKid, Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(f.Runtime, append([]byte("Auriga.NetworkKeyset.v2\x00"), body...)))}}, Predecessor: &api.PredecessorProof{Keyset: previous.Keyset, KeysetDigest: d, Authority: previous.Authority, Control: previous.Control}}
	if e = restarted.Accept(bad, f.Bundle); e != projection.Fault("invalid_transition") {
		t.Fatal("archived SPKI reuse accepted", e)
	}
}
