// Package testfixture generates disposable message-network trust/materials.
// It is imported only by tests; no private key bytes are embedded in source.
package testfixture

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"net"
	"os"
	"testing"
	"time"

	"github.com/hansjone/auriga/deploy/bootstrap/collab/projection"
	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

func Dir(t *testing.T) string {
	t.Helper()
	path := t.TempDir()
	if e := os.Chmod(path, 0700); e != nil {
		t.Fatal(e)
	}
	return path
}

type Fixture struct {
	Root, Runtime, Signing ed25519.PrivateKey
	Sealing                *ecdh.PrivateKey
	RootSPKI               string
	Pin                    api.InstallationPin
	Initial, Activated     api.Publication
	Bundle                 api.SignedTransportTrustBundle
	Leaves                 []api.TlsLeaf
	Private                map[string]string
	BrokerLeaf             api.TlsLeaf
	caKeys                 map[string]ed25519.PrivateKey
}

func encoded(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
func spki(t *testing.T, p any) string {
	t.Helper()
	b, e := x509.MarshalPKIXPublicKey(p)
	if e != nil {
		t.Fatal(e)
	}
	return encoded(b)
}
func private(t *testing.T, key any) string {
	t.Helper()
	b, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		t.Fatal(e)
	}
	defer clear(b)
	return encoded(b)
}
func key(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, k, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	return k
}
func sign(t *testing.T, payload any, k ed25519.PrivateKey, kid, domain string) api.Signature {
	t.Helper()
	b, e := projection.Bytes(payload)
	if e != nil {
		t.Fatal(e)
	}
	return api.Signature{Alg: "Ed25519", Kid: kid, Signature: encoded(ed25519.Sign(k, append([]byte(domain+"\x00"), b...)))}
}
func digest(t *testing.T, v any) string {
	t.Helper()
	d, e := projection.ObjectDigest(v)
	if e != nil {
		t.Fatal(e)
	}
	return d
}
func New(t *testing.T) *Fixture {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	stamp := func(t time.Time) string { return t.Format(time.RFC3339) }
	f := &Fixture{Root: key(t), Runtime: key(t), Signing: key(t), Private: map[string]string{}, caKeys: map[string]ed25519.PrivateKey{}}
	t.Cleanup(func() {
		clear(f.Root)
		clear(f.Runtime)
		clear(f.Signing)
		for _, k := range f.caKeys {
			clear(k)
		}
	})
	var e error
	f.Sealing, e = ecdh.X25519().GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	f.RootSPKI = spki(t, f.Root.Public())
	f.Pin = api.InstallationPin{TargetId: "network-reader", IdentityId: "network-projector", SigningSpki: spki(t, f.Signing.Public()), Component: "network", GatewayId: "gateway-1", SealingSpki: spki(t, f.Sealing.PublicKey()), Subject: "CN=network-projector"}
	scopes := []api.TransportScope{{Component: "network", IdentityId: f.Pin.IdentityId, Sans: []string{"localhost", "127.0.0.1"}, Ekus: []string{"serverAuth", "clientAuth"}}, {Component: "broker", IdentityId: "broker-projector", Sans: []string{"localhost", "127.0.0.1"}, Ekus: []string{"serverAuth"}}}
	authority := api.RuntimeAuthority{SchemaVersion: 2, TenantId: "synthetic-tenant", Realm: "1", AuthorityId: "runtime-authority", AuthorityRevision: "1", RuntimeKid: "runtime-key", PublicKeySpki: spki(t, f.Runtime.Public()), NotBefore: stamp(now.Add(-7 * 24 * time.Hour)), ExpiryPolicy: "no_hard_expiry", ReviewPeriodDays: 365, Capabilities: []string{"keyset", "announcement", "business_rotation", "restart_recovery", "history_archive", "network_transport"}, Limits: api.Limits{KeysetSeconds: 86400, BusinessKeySeconds: 691200, RestartKeySeconds: 604800, NextMaturitySeconds: 86400, OverlapSeconds: 360, ArchiveGraceSeconds: 360}, TransportScope: scopes}
	signedAuthority := api.SignedRuntimeAuthority{Payload: authority, RootAuth: sign(t, authority, f.Root, "root-key", "Auriga.NetworkRuntimeAuthority.v2")}
	ad := digest(t, signedAuthority)
	control := api.RootControl{SchemaVersion: 2, TenantId: authority.TenantId, Realm: "1", ControlRevision: "1", IssuedAt: stamp(now.Add(-4 * 24 * time.Hour)), AllowedAuthorities: []api.AuthorityReference{{AuthorityId: authority.AuthorityId, AuthorityDigest: ad}}, RevokedAuthorityIds: []string{}, RevokedInstallationIds: []string{}, MinimumGeneration: "1", MinimumKeysetVersion: "1"}
	signedControl := api.SignedRootControl{Payload: control, RootAuth: sign(t, control, f.Root, "root-key", "Auriga.NetworkRootControl.v2")}
	initialTime := now.Add(-3 * 24 * time.Hour)
	payload := api.KeysetPayload{SchemaVersion: 2, TenantId: authority.TenantId, Realm: "1", AuthorityId: authority.AuthorityId, AuthorityDigest: ad, ControlRevision: "1", Generation: "1", KeysetVersion: "1", IssuedAt: stamp(initialTime), ValidUntil: stamp(initialTime.Add(24 * time.Hour)), Mode: "bootstrap", Archive: api.Archive{Before: stamp(initialTime.Add(-360 * time.Second)), HistoryDigest: projection.Digest(nil)}}
	for _, purpose := range []string{"business_delivery", "protocol_response", "management_state"} {
		for _, state := range []string{"active", "next"} {
			k := key(t)
			kid := purpose + "-" + state
			before, after := now.Add(-6*24*time.Hour), now.Add(24*time.Hour)
			if state == "next" {
				before, after = now.Add(-2*24*time.Hour), now.Add(5*24*time.Hour)
			}
			payload.Keys = append(payload.Keys, api.PublicKey{Kid: kid, Alg: "Ed25519", Purpose: purpose, State: state, NotBefore: stamp(before), NotAfter: stamp(after), PublicKeySpki: spki(t, k.Public())})
			payload.FirstPublications = append(payload.FirstPublications, api.FirstPublication{Kid: kid, CommittedVersion: "1", CommittedAt: stamp(initialTime)})
			f.Private[kid] = private(t, k)
			clear(k)
		}
	}
	predecessor := api.SignedKeysetV2{Payload: payload, RuntimeAuth: sign(t, payload, f.Runtime, authority.RuntimeKid, "Auriga.NetworkKeyset.v2")}
	payload.KeysetVersion = "2"
	payload.Mode = "refresh"
	payload.IssuedAt = stamp(now.Add(-2 * time.Second))
	payload.ValidUntil = stamp(now.Add(23 * time.Hour))
	f.Initial = api.Publication{Authority: signedAuthority, Control: signedControl, Keyset: api.SignedKeysetV2{Payload: payload, RuntimeAuth: sign(t, payload, f.Runtime, authority.RuntimeKid, "Auriga.NetworkKeyset.v2")}, Predecessor: &api.PredecessorProof{Keyset: predecessor, KeysetDigest: digest(t, predecessor), Authority: signedAuthority, Control: signedControl}}
	activated := payload
	activated.Keys = append([]api.PublicKey(nil), payload.Keys...)
	for j := range activated.Keys {
		if activated.Keys[j].State == "active" {
			activated.Keys[j].State = "retired"
		} else {
			activated.Keys[j].State = "active"
		}
	}
	activated.KeysetVersion = "3"
	activated.Mode = "activate"
	activated.IssuedAt = stamp(now.Add(-time.Second))
	f.Activated = api.Publication{Authority: signedAuthority, Control: signedControl, Keyset: api.SignedKeysetV2{Payload: activated, RuntimeAuth: sign(t, activated, f.Runtime, authority.RuntimeKid, "Auriga.NetworkKeyset.v2")}, Predecessor: &api.PredecessorProof{Keyset: f.Initial.Keyset, KeysetDigest: digest(t, f.Initial.Keyset), Authority: signedAuthority, Control: signedControl}}
	bundle := api.TransportTrustBundle{SchemaVersion: 2, TenantId: authority.TenantId, Realm: "1", AuthorityId: authority.AuthorityId, AuthorityDigest: ad, ControlRevision: "1", Generation: "1", BundleVersion: "1", IssuedAt: stamp(now.Add(-2 * time.Second)), Phase: "dual_trust", Scope: scopes, RequiredReaderIds: []string{f.Pin.IdentityId, "broker-projector"}, PreviousBundleDigest: projection.Digest(nil)}
	for j, role := range []string{"server", "client"} {
		caKey := key(t)
		ca := &x509.Certificate{SerialNumber: big.NewInt(int64(100 + j)), Subject: pkix.Name{CommonName: role + " CA"}, NotBefore: now.Add(-5 * time.Minute), NotAfter: now.AddDate(9, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
		der, e := x509.CreateCertificate(rand.Reader, ca, ca, caKey.Public(), caKey)
		if e != nil {
			t.Fatal(e)
		}
		ca, e = x509.ParseCertificate(der)
		if e != nil {
			t.Fatal(e)
		}
		caID := role + "-ca"
		bundle.Cas = append(bundle.Cas, api.TransportCa{CaId: caID, Role: role, CertificateDer: encoded(der), NotBefore: stamp(ca.NotBefore), NotAfter: stamp(ca.NotAfter)})
		makeLeaf := func(component, leafRole string) api.TlsLeaf {
			k := key(t)
			defer clear(k)
			eku := x509.ExtKeyUsageServerAuth
			if role == "client" {
				eku = x509.ExtKeyUsageClientAuth
			}
			leaf := &x509.Certificate{SerialNumber: big.NewInt(int64(200 + len(f.Leaves))), Subject: pkix.Name{CommonName: leafRole}, NotBefore: now.Add(-5 * time.Minute), NotAfter: now.Add(60 * 24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{eku}, DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
			raw, e := x509.CreateCertificate(rand.Reader, leaf, ca, k.Public(), caKey)
			if e != nil {
				t.Fatal(e)
			}
			return api.TlsLeaf{Component: component, Role: leafRole, CertificateDer: encoded(raw), PrivatePkcs8: private(t, k), CaId: caID, NotBefore: stamp(leaf.NotBefore), NotAfter: stamp(leaf.NotAfter)}
		}
		if role == "server" {
			f.Leaves = append(f.Leaves, makeLeaf("network", "submit"))
			f.BrokerLeaf = makeLeaf("broker", "broker")
		} else {
			f.Leaves = append(f.Leaves, makeLeaf("network", "broker-client"))
		}
		f.caKeys[caID] = caKey
	}
	f.Bundle = api.SignedTransportTrustBundle{Payload: bundle, RuntimeAuth: sign(t, bundle, f.Runtime, authority.RuntimeKid, "Auriga.NetworkTransportTrustBundle.v2")}
	return f
}
func (f *Fixture) RenewLeaves(t *testing.T) {
	t.Helper()
	for j, old := range f.Leaves {
		der, e := base64.RawURLEncoding.DecodeString(old.CertificateDer)
		if e != nil {
			t.Fatal(e)
		}
		leaf, e := x509.ParseCertificate(der)
		if e != nil {
			t.Fatal(e)
		}
		leaf.SerialNumber, e = rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
		if e != nil {
			t.Fatal(e)
		}
		leaf.NotBefore = time.Now().UTC().Truncate(time.Second).Add(-time.Minute)
		leaf.NotAfter = leaf.NotBefore.Add(70 * 24 * time.Hour)
		var ca *x509.Certificate
		for _, c := range f.Bundle.Payload.Cas {
			if c.CaId == old.CaId {
				b, e := base64.RawURLEncoding.DecodeString(c.CertificateDer)
				if e != nil {
					t.Fatal(e)
				}
				ca, e = x509.ParseCertificate(b)
				if e != nil {
					t.Fatal(e)
				}
			}
		}
		k := key(t)
		raw, e := x509.CreateCertificate(rand.Reader, leaf, ca, k.Public(), f.caKeys[old.CaId])
		if e != nil {
			t.Fatal(e)
		}
		old.CertificateDer = encoded(raw)
		old.PrivatePkcs8 = private(t, k)
		clear(k)
		old.NotBefore = leaf.NotBefore.Format(time.RFC3339)
		old.NotAfter = leaf.NotAfter.Format(time.RFC3339)
		f.Leaves[j] = old
	}
}
func (f *Fixture) Material(t *testing.T, p api.Publication, nonce string) api.ProjectionMaterial {
	t.Helper()
	active, next := "", ""
	for _, k := range p.Keyset.Payload.Keys {
		if k.Purpose == "protocol_response" {
			switch k.State {
			case "active":
				active = k.Kid
			case "next":
				next = k.Kid
			}
		}
	}
	protocol := &api.ProtocolMaterial{Publication: p, ActiveKid: active, ActivePkcs8: f.Private[active]}
	if next != "" {
		v := f.Private[next]
		protocol.NextKid = &next
		protocol.NextPkcs8 = &v
	}
	return api.ProjectionMaterial{TenantId: p.Keyset.Payload.TenantId, Realm: "1", InstallationId: f.Pin.IdentityId, Component: "network", Nonce: nonce, Generation: "1", KeysetVersion: p.Keyset.Payload.KeysetVersion, PublicationDigest: digest(t, p.Keyset), Bundle: f.Bundle, Leaves: f.Leaves, Protocol: protocol}
}
