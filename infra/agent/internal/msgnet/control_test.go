package msgnet

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type backend struct {
	fail                 bool
	controls, transports int
}

func (b *backend) ApplyControl(context.Context, api.ControlPackage) error {
	b.controls++
	if b.fail {
		return errors.New("consumer persistence unavailable")
	}
	return nil
}
func (b *backend) ApplyTransport(context.Context, api.SignedTransportTrustBundle) error {
	b.transports++
	if b.fail {
		return errors.New("transport consumer unavailable")
	}
	return nil
}
func signature(t *testing.T, key ed25519.PrivateKey, kid, domain string, payload any) api.Signature {
	t.Helper()
	raw, e := Bytes(payload)
	if e != nil {
		t.Fatal(e)
	}
	return api.Signature{Alg: "Ed25519", Kid: kid, Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, append([]byte(domain+"\x00"), raw...)))}
}
func encoded(t *testing.T, key any) string {
	t.Helper()
	raw, e := x509.MarshalPKIXPublicKey(key)
	if e != nil {
		t.Fatal(e)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}
func fixture(t *testing.T) (Options, ed25519.PrivateKey, *backend) {
	t.Helper()
	root, rootPrivate, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	b := &backend{}
	return Options{Directory: t.TempDir(), Tenant: "tenant", Realm: "1", RootKid: "root", NodeId: "node", RootKey: root, SigningKey: key, Pin: api.NodeControlPin{TargetId: "target", NodeId: "node", IdentityId: "node-installation", IdentityRevision: "1", SigningSpki: encoded(t, pub)}, Backend: b, Now: func() time.Time { return time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC) }}, rootPrivate, b
}
func control(t *testing.T, o Options, root ed25519.PrivateKey, revision string, revoked []string) api.ControlPackage {
	t.Helper()
	payload := api.RootControl{SchemaVersion: 2, TenantId: o.Tenant, Realm: o.Realm, ControlRevision: revision, IssuedAt: o.Now().Format(time.RFC3339), AllowedAuthorities: []api.AuthorityReference{}, RevokedAuthorityIds: []string{}, RevokedInstallationIds: revoked, MinimumGeneration: "1", MinimumKeysetVersion: "1"}
	signed := api.SignedRootControl{Payload: payload, RootAuth: signature(t, root, o.RootKid, "Auriga.NetworkRootControl.v2", payload)}
	raw, e := Bytes(signed)
	if e != nil {
		t.Fatal(e)
	}
	challenge := make([]byte, 32)
	if _, e = rand.Read(challenge); e != nil {
		t.Fatal(e)
	}
	return api.ControlPackage{PackageId: "package-" + revision, TenantId: o.Tenant, Realm: o.Realm, TargetId: o.Pin.TargetId, Control: signed, ControlDigest: Digest(raw), Challenge: base64.RawURLEncoding.EncodeToString(challenge)}
}
func install(t *testing.T, i *Installer, p api.ControlPackage) ([]byte, error) {
	t.Helper()
	raw, e := Bytes(p)
	if e != nil {
		t.Fatal(e)
	}
	return i.InstallControl(context.Background(), raw)
}

func TestControlReceiptDurableExactReplayAndRestart(t *testing.T) {
	o, root, b := fixture(t)
	i, e := New(o)
	if e != nil {
		t.Fatal(e)
	}
	p := control(t, o, root, "1", []string{})
	receipt, e := install(t, i, p)
	if e != nil {
		t.Fatal(e)
	}
	var parsed api.InstallationReceipt
	if e = json.Unmarshal(receipt, &parsed); e != nil {
		t.Fatal(e)
	}
	if e = i.receipt(parsed, p); e != nil {
		t.Fatal("receipt signature/binding", e)
	}
	restored, e := New(o)
	if e != nil {
		t.Fatal(e)
	}
	same, e := install(t, restored, p)
	if e != nil || !bytes.Equal(same, receipt) {
		t.Fatal("receipt not exact after restart", e)
	}
	if b.controls != 2 {
		t.Fatal("runtime fence not reapplied after restart")
	}
	conflict := p
	conflict.Challenge = base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	if _, e = install(t, restored, conflict); e == nil {
		t.Fatal("nonce conflict accepted")
	}
	newer := control(t, o, root, "2", []string{})
	if _, e = install(t, restored, newer); e != nil {
		t.Fatal(e)
	}
	if _, e = install(t, restored, p); e == nil {
		t.Fatal("old revision accepted")
	}
}

func TestFailureKeepsFenceAndDoesNotIssueReceipt(t *testing.T) {
	o, root, b := fixture(t)
	b.fail = true
	i, e := New(o)
	if e != nil {
		t.Fatal(e)
	}
	p := control(t, o, root, "2", []string{})
	if _, e = install(t, i, p); e == nil {
		t.Fatal("failed consumer acknowledged")
	}
	restored, e := New(o)
	if e != nil {
		t.Fatal(e)
	}
	if restored.state.Receipt != nil {
		t.Fatal("failed receipt persisted")
	}
	old := control(t, o, root, "1", []string{})
	if _, e = install(t, restored, old); e == nil {
		t.Fatal("failure lost rollback fence")
	}
	b.fail = false
	if _, e = install(t, restored, p); e != nil {
		t.Fatal("exact retry failed", e)
	}
}

func TestRevokedIdentityCannotSelfConfirmOrRemoveRevocation(t *testing.T) {
	o, root, b := fixture(t)
	i, e := New(o)
	if e != nil {
		t.Fatal(e)
	}
	p := control(t, o, root, "2", []string{o.Pin.IdentityId})
	if _, e = install(t, i, p); e == nil {
		t.Fatal("revoked signer confirmed")
	}
	if b.controls != 1 {
		t.Fatal("revocation not applied")
	}
	if i.state.Receipt != nil {
		t.Fatal("revoked receipt exists")
	}
	removal := control(t, o, root, "3", []string{})
	if _, e = install(t, i, removal); e == nil {
		t.Fatal("revocation removed")
	}
	restored, e := New(o)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = install(t, restored, p); e == nil {
		t.Fatal("revoked identity recovered on restart")
	}
}

func TestWrongScopeSignatureAndJournalTamperingReject(t *testing.T) {
	for _, mode := range []string{"target", "tenant", "signature", "duplicate", "state"} {
		t.Run(mode, func(t *testing.T) {
			o, root, _ := fixture(t)
			i, e := New(o)
			if e != nil {
				t.Fatal(e)
			}
			p := control(t, o, root, "1", []string{})
			switch mode {
			case "target":
				p.TargetId = "other"
			case "tenant":
				p.TenantId = "other"
			case "signature":
				p.Control.RootAuth.Kid = "runtime"
			case "duplicate":
				raw, _ := Bytes(p)
				raw = append([]byte(`{"package_id":"duplicate",`), raw[1:]...)
				if _, e = i.InstallControl(context.Background(), raw); e == nil {
					t.Fatal("duplicate accepted")
				}
				return
			case "state":
				if _, e = install(t, i, p); e != nil {
					t.Fatal(e)
				}
				raw, _ := os.ReadFile(filepath.Join(o.Directory, "control-state.json"))
				raw = bytes.Replace(raw, []byte(`"identity_id":"node-installation"`), []byte(`"identity_id":"forged"`), 1)
				_ = os.WriteFile(filepath.Join(o.Directory, "control-state.json"), raw, 0600)
				_, e = New(o)
				if e == nil {
					t.Fatal("journal tamper accepted")
				}
				return
			}
			if _, e = install(t, i, p); e == nil {
				t.Fatal("invalid control accepted")
			}
		})
	}
}

func TestTransportRevocationPrecedesHigherVersion(t *testing.T) {
	o, root, b := fixture(t)
	runtimePub, runtimePrivate, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	scope := api.TransportScope{Component: "edge", IdentityId: "independent-edge-installation", Sans: []string{"edge.example.com"}, Ekus: []string{"serverAuth"}}
	a := api.RuntimeAuthority{SchemaVersion: 2, TenantId: o.Tenant, Realm: o.Realm, AuthorityId: "authority", AuthorityRevision: "1", RuntimeKid: "runtime", PublicKeySpki: encoded(t, runtimePub), NotBefore: o.Now().Add(-time.Hour).Format(time.RFC3339), ExpiryPolicy: "no_hard_expiry", ReviewPeriodDays: 365, Capabilities: []string{"network_transport"}, Limits: api.Limits{KeysetSeconds: 86400, BusinessKeySeconds: 691200, RestartKeySeconds: 604800, NextMaturitySeconds: 86400, OverlapSeconds: 360, ArchiveGraceSeconds: 360}, TransportScope: []api.TransportScope{scope}}
	authority := api.SignedRuntimeAuthority{Payload: a, RootAuth: signature(t, root, o.RootKid, "Auriga.NetworkRuntimeAuthority.v2", a)}
	raw, _ := Bytes(authority)
	digest := Digest(raw)
	o.ResolveAuthority = func(context.Context, string, string) (api.SignedRuntimeAuthority, error) { return authority, nil }
	p := control(t, o, root, "1", []string{})
	p.Control.Payload.AllowedAuthorities = []api.AuthorityReference{{AuthorityId: a.AuthorityId, AuthorityDigest: digest}}
	p.Control.RootAuth = signature(t, root, o.RootKid, "Auriga.NetworkRootControl.v2", p.Control.Payload)
	raw, _ = Bytes(p.Control)
	p.ControlDigest = Digest(raw)
	i, e := New(o)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = install(t, i, p); e != nil {
		t.Fatal(e)
	}
	caPub, caKey, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "synthetic-ca"}, NotBefore: o.Now().Add(-time.Minute), NotAfter: o.Now().AddDate(10, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, e := x509.CreateCertificate(rand.Reader, ca, ca, caPub, caKey)
	if e != nil {
		t.Fatal(e)
	}
	payload := api.TransportTrustBundle{SchemaVersion: 2, TenantId: o.Tenant, Realm: o.Realm, AuthorityId: a.AuthorityId, AuthorityDigest: digest, ControlRevision: "1", Generation: "1", BundleVersion: "1", IssuedAt: o.Now().Format(time.RFC3339), Phase: "dual_trust", Cas: []api.TransportCa{{CaId: "ca", Role: "server", CertificateDer: base64.RawURLEncoding.EncodeToString(der), NotBefore: ca.NotBefore.Format(time.RFC3339), NotAfter: ca.NotAfter.Format(time.RFC3339)}}, Scope: []api.TransportScope{scope}, RequiredReaderIds: []string{o.Pin.IdentityId}, PreviousBundleDigest: "sha256:" + fmt.Sprintf("%064d", 0)}
	bundle := api.SignedTransportTrustBundle{Payload: payload, RuntimeAuth: signature(t, runtimePrivate, a.RuntimeKid, "Auriga.NetworkTransportTrustBundle.v2", payload)}
	raw, _ = Bytes(bundle)
	if e = i.InstallTransport(context.Background(), raw); e != nil {
		t.Fatal(e)
	}
	advance := func(next api.TransportTrustBundle, accept bool) api.SignedTransportTrustBundle {
		t.Helper()
		signed := api.SignedTransportTrustBundle{Payload: next, RuntimeAuth: signature(t, runtimePrivate, a.RuntimeKid, "Auriga.NetworkTransportTrustBundle.v2", next)}
		wire, _ := Bytes(signed)
		if err := i.InstallTransport(context.Background(), wire); (err == nil) != accept {
			t.Fatalf("phase %s version %s, accepted=%v, error=%v", next.Phase, next.BundleVersion, accept, err)
		}
		return signed
	}
	second := bundle.Payload
	second.PreviousBundleDigest = Digest(raw)
	second.BundleVersion = "2"
	advance(second, false) // A higher version cannot repeat the same CA phase.
	second.BundleVersion, second.Phase = "2", "retire_old"
	advance(second, false)
	second.Phase = "new_leaves"
	nextBundle := advance(second, true)
	previousBytes, _ := Bytes(nextBundle)
	third := second
	third.BundleVersion, third.Phase, third.PreviousBundleDigest = "3", "retire_old", Digest(previousBytes)
	nextBundle = advance(third, true)
	previousBytes, _ = Bytes(nextBundle)
	fourth := third
	fourth.BundleVersion, fourth.Phase, fourth.PreviousBundleDigest = "4", "dual_trust", Digest(previousBytes)
	advance(fourth, false) // A new cycle must add a fresh CA.
	newPub, newKey, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	nextCA := *ca
	nextCA.SerialNumber = big.NewInt(2)
	nextCA.Subject = pkix.Name{CommonName: "synthetic-next-ca"}
	nextDER, e := x509.CreateCertificate(rand.Reader, &nextCA, &nextCA, newPub, newKey)
	if e != nil {
		t.Fatal(e)
	}
	fourth.Cas = append(append([]api.TransportCa{}, third.Cas...), api.TransportCa{CaId: "next-ca", Role: "server", CertificateDer: base64.RawURLEncoding.EncodeToString(nextDER), NotBefore: nextCA.NotBefore.Format(time.RFC3339), NotAfter: nextCA.NotAfter.Format(time.RFC3339)})
	wrong := fourth
	wrong.PreviousBundleDigest = bundle.Payload.PreviousBundleDigest
	advance(wrong, false)
	b.fail = true
	advance(fourth, false)
	b.fail = false
	advance(third, false) // Failed installation still fences rollback.
	advance(fourth, true)
	i, e = New(o)
	if e != nil {
		t.Fatal(e)
	}
	advance(third, false) // The next-cycle fence survives restart.
	revoked := control(t, o, root, "2", []string{})
	revoked.Control.Payload.RevokedAuthorityIds = []string{a.AuthorityId}
	revoked.Control.RootAuth = signature(t, root, o.RootKid, "Auriga.NetworkRootControl.v2", revoked.Control.Payload)
	controlBytes, _ := Bytes(revoked.Control)
	revoked.ControlDigest = Digest(controlBytes)
	if _, e = install(t, i, revoked); e != nil {
		t.Fatal(e)
	}
	bundle.Payload.BundleVersion = "999"
	bundle.RuntimeAuth = signature(t, runtimePrivate, a.RuntimeKid, "Auriga.NetworkTransportTrustBundle.v2", bundle.Payload)
	raw, _ = Bytes(bundle)
	if e = i.InstallTransport(context.Background(), raw); e == nil {
		t.Fatal("revoked high bundle accepted")
	}
	if b.transports != 5 {
		t.Fatal("revoked transport reached consumer")
	}
}

func TestReplacementIdentityRequiresNewerRootControl(t *testing.T) {
	o, root, _ := fixture(t)
	first, e := New(o)
	if e != nil {
		t.Fatal(e)
	}
	p := control(t, o, root, "3", []string{})
	if _, e = install(t, first, p); e != nil {
		t.Fatal(e)
	}
	pub, private, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	o.Pin.IdentityId = "replacement-node"
	o.Pin.IdentityRevision = "2"
	o.Pin.SigningSpki, o.SigningKey = encoded(t, pub), private
	replacement, e := New(o)
	if e != nil {
		t.Fatal(e)
	}
	if receipt, e := install(t, replacement, p); e == nil || len(receipt) != 0 {
		t.Fatal("old root authorized a replacement identity")
	}
	if _, e = install(t, replacement, control(t, o, root, "4", []string{"node-installation"})); e != nil {
		t.Fatal(e)
	}
	if _, e = New(o); e != nil {
		t.Fatal("replacement journal not restartable:", e)
	}
}

func TestNestedUnknownFieldsAndDeepJSONAreRejected(t *testing.T) {
	o, root, b := fixture(t)
	i, e := New(o)
	if e != nil {
		t.Fatal(e)
	}
	p := control(t, o, root, "1", []string{})
	raw, _ := Bytes(p)
	var altered map[string]interface{}
	if e = json.Unmarshal(raw, &altered); e != nil {
		t.Fatal(e)
	}
	control := altered["control"].(map[string]interface{})
	payload := control["payload"].(map[string]interface{})
	payload["delegation"] = true
	control["root_auth"] = signature(t, root, o.RootKid, "Auriga.NetworkRootControl.v2", payload)
	raw, _ = Bytes(control)
	altered["control_digest"] = Digest(raw)
	raw, _ = Bytes(altered)
	if _, e = i.InstallControl(context.Background(), raw); e == nil {
		t.Fatal("unknown signed fields accepted")
	}
	if b.controls != 0 {
		t.Fatal("invalid control reached backend")
	}
	if _, e = Canonical([]byte(strings.Repeat("[", 65) + "0" + strings.Repeat("]", 65))); e == nil {
		t.Fatal("unbounded nesting")
	}
}
