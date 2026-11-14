package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	trust "github.com/hansjone/auriga/infra/edge/pkg/msgnet"
	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

func TestMsgnetRuntimeLoadsRootVerifiedTLSAndDisconnectsRevokedIdentity(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	stamp := func(v time.Time) string { return v.Format(time.RFC3339) }
	key := func() ed25519.PrivateKey {
		t.Helper()
		_, k, e := ed25519.GenerateKey(rand.Reader)
		if e != nil {
			t.Fatal(e)
		}
		return k
	}
	spki := func(v any) string {
		t.Helper()
		raw, e := x509.MarshalPKIXPublicKey(v)
		if e != nil {
			t.Fatal(e)
		}
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	sign := func(v any, k ed25519.PrivateKey, kid, domain string) api.Signature {
		t.Helper()
		raw, e := trust.Bytes(v)
		if e != nil {
			t.Fatal(e)
		}
		return api.Signature{Alg: "Ed25519", Kid: kid, Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(k, append([]byte(domain+"\x00"), raw...)))}
	}
	root, runtime := key(), key()
	tenant := "00000000-0000-4000-8000-000000000001"
	scope := []api.TransportScope{{Component: "edge", IdentityId: "edge-install", Sans: []string{"edge.network.internal", "edge-presence.network.internal"}, Ekus: []string{"clientAuth"}}}
	a := api.SignedRuntimeAuthority{Payload: api.RuntimeAuthority{SchemaVersion: 2, TenantId: tenant, Realm: "1", AuthorityId: "runtime-authority", AuthorityRevision: "1", RuntimeKid: "runtime", PublicKeySpki: spki(runtime.Public()), NotBefore: stamp(now.Add(-time.Hour)), ExpiryPolicy: "no_hard_expiry", ReviewPeriodDays: 365, Capabilities: []string{"keyset", "business_rotation", "network_transport"}, Limits: api.Limits{KeysetSeconds: 86400, BusinessKeySeconds: 691200, RestartKeySeconds: 604800, NextMaturitySeconds: 86400, OverlapSeconds: 360, ArchiveGraceSeconds: 360}, TransportScope: scope}}
	a.RootAuth = sign(a.Payload, root, "root", "Auriga.NetworkRuntimeAuthority.v2")
	ad, _ := trust.ObjectDigest(a)
	c := api.SignedRootControl{Payload: api.RootControl{SchemaVersion: 2, TenantId: tenant, Realm: "1", ControlRevision: "1", IssuedAt: stamp(now), AllowedAuthorities: []api.AuthorityReference{{AuthorityId: a.Payload.AuthorityId, AuthorityDigest: ad}}, RevokedAuthorityIds: []string{}, RevokedInstallationIds: []string{}, MinimumGeneration: "1", MinimumKeysetVersion: "1"}}
	c.RootAuth = sign(c.Payload, root, "root", "Auriga.NetworkRootControl.v2")
	p := api.Publication{Authority: a, Control: c, Keyset: api.SignedKeysetV2{Payload: api.KeysetPayload{SchemaVersion: 2, TenantId: tenant, Realm: "1", AuthorityId: a.Payload.AuthorityId, AuthorityDigest: ad, ControlRevision: "1", Generation: "1", KeysetVersion: "1", IssuedAt: stamp(now), ValidUntil: stamp(now.Add(24 * time.Hour)), Mode: "bootstrap", Archive: api.Archive{Before: stamp(now.Add(-time.Hour)), HistoryDigest: trust.Digest(nil)}}}}
	for _, purpose := range []string{"business_delivery", "protocol_response", "management_state"} {
		k := key()
		p.Keyset.Payload.Keys = append(p.Keyset.Payload.Keys, api.PublicKey{Kid: purpose, Purpose: purpose, State: "active", Alg: "Ed25519", PublicKeySpki: spki(k.Public()), NotBefore: stamp(now), NotAfter: stamp(now.Add(7 * 24 * time.Hour))})
		p.Keyset.Payload.FirstPublications = append(p.Keyset.Payload.FirstPublications, api.FirstPublication{Kid: purpose, CommittedVersion: "1", CommittedAt: stamp(now)})
	}
	p.Keyset.RuntimeAuth = sign(p.Keyset.Payload, runtime, "runtime", "Auriga.NetworkKeyset.v2")
	pki := createAccessPKI(t)
	caKey, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(41), NotBefore: now.Add(-time.Minute), NotAfter: now.AddDate(1, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, e := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if e != nil {
		t.Fatal(e)
	}
	ca, e = x509.ParseCertificate(caDER)
	if e != nil {
		t.Fatal(e)
	}
	serverPEM, e := os.ReadFile(pki.hop.CAFile)
	if e != nil {
		t.Fatal(e)
	}
	serverCA, _ := pem.Decode(serverPEM)
	serverCert, e := x509.ParseCertificate(serverCA.Bytes)
	if e != nil {
		t.Fatal(e)
	}
	caWire := func(id, role string, cert *x509.Certificate) api.TransportCa {
		return api.TransportCa{CaId: id, Role: role, CertificateDer: base64.RawURLEncoding.EncodeToString(cert.Raw), NotBefore: stamp(cert.NotBefore), NotAfter: stamp(cert.NotAfter)}
	}
	b := api.SignedTransportTrustBundle{Payload: api.TransportTrustBundle{SchemaVersion: 2, TenantId: tenant, Realm: "1", AuthorityId: a.Payload.AuthorityId, AuthorityDigest: ad, ControlRevision: "1", Generation: "1", BundleVersion: "1", IssuedAt: stamp(now), Phase: "dual_trust", Cas: []api.TransportCa{caWire("server-ca", "server", serverCert), caWire("client-ca", "client", ca)}, Scope: scope, RequiredReaderIds: []string{"edge-reader"}, PreviousBundleDigest: trust.Digest(nil)}}
	b.RuntimeAuth = sign(b.Payload, runtime, "runtime", "Auriga.NetworkTransportTrustBundle.v2")
	pd, _ := trust.ObjectDigest(p.Keyset)
	nonce := make([]byte, 32)
	if _, e = rand.Read(nonce); e != nil {
		t.Fatal(e)
	}
	material := api.ProjectionMaterial{TenantId: tenant, Realm: "1", InstallationId: "edge-install", Component: "edge", Nonce: base64.RawURLEncoding.EncodeToString(nonce), Generation: "1", KeysetVersion: "1", PublicationDigest: pd, Bundle: b}
	for j, role := range []string{"edge", "edge-presence"} {
		k, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if e != nil {
			t.Fatal(e)
		}
		leaf := &x509.Certificate{SerialNumber: big.NewInt(int64(j + 80)), DNSNames: []string{role + ".network.internal"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
		der, e := x509.CreateCertificate(rand.Reader, leaf, ca, &k.PublicKey, caKey)
		if e != nil {
			t.Fatal(e)
		}
		raw, e := x509.MarshalPKCS8PrivateKey(k)
		if e != nil {
			t.Fatal(e)
		}
		material.Leaves = append(material.Leaves, api.TlsLeaf{Component: "edge", Role: role, CertificateDer: base64.RawURLEncoding.EncodeToString(der), PrivatePkcs8: base64.RawURLEncoding.EncodeToString(raw), CaId: "client-ca", NotBefore: stamp(leaf.NotBefore), NotAfter: stamp(leaf.NotAfter)})
	}
	g, e := trust.BuildGeneration(material, p, "1")
	if e != nil {
		t.Fatal(e)
	}
	defer g.Clear()
	var mu sync.Mutex
	current := c
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/collab/v2/network/bootstrap/controls" {
			mu.Lock()
			raw, e := trust.Bytes(current)
			mu.Unlock()
			if e != nil {
				t.Error(e)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write(raw)
			return
		}
		w.WriteHeader(204)
	}))
	roots := pki.roots.Clone()
	roots.AddCert(ca)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{pki.server}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	defer server.Close()
	cfg := MsgnetProjectionConfig{Root: t.TempDir(), Control: t.TempDir(), State: t.TempDir(), SchemaFile: "../../contracts/network/auto-renewal-v2.schema.json", RootSPKI: spki(root.Public()), RootKid: "root", Tenant: tenant, Realm: "1", LedgerOrigin: server.URL, TokenFile: filepath.Join(t.TempDir(), "token"), Installation: api.InstallationPin{IdentityId: "edge-install", Component: "edge"}, SubmitLeafSAN: "edge.network.internal", PresenceLeafSAN: "edge-presence.network.internal"}
	if e = os.WriteFile(cfg.TokenFile, []byte("synthetic-test-credential"), 0600); e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(cfg.Root, "generations", g.Selector.ManifestDigest[7:])
	if e = os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	write := func(path string, v any) {
		t.Helper()
		raw, e := trust.Bytes(v)
		if e == nil {
			e = os.WriteFile(path, raw, 0600)
		}
		if e != nil {
			t.Fatal(e)
		}
	}
	write(filepath.Join(cfg.Root, "selector.json"), g.Selector)
	write(filepath.Join(dir, "manifest.json"), g.Manifest)
	for name, raw := range g.Files {
		if e = os.WriteFile(filepath.Join(dir, name), raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	x, e := newEdgeMsgnet(cfg, pki.hop)
	if e != nil {
		t.Fatal(e)
	}
	if e = x.reader.Reload(); e != nil {
		t.Fatal(e)
	}
	tr := &http.Transport{}
	if e = x.configure(tr, "edge", pki.hop.ServerName); e != nil {
		t.Fatal(e)
	}
	u, e := url.Parse(server.URL)
	if e != nil {
		t.Fatal(e)
	}
	conn, e := tr.DialTLSContext(context.Background(), "tcp", u.Host)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = conn.Close() }()
	request := api.LocalProjectionRequest{Operation: "confirm", Selector: g.Selector}
	if _, phase, _ := x.reader.Handle(request); phase != "loaded" {
		t.Fatal("one TLS role falsely confirmed all leaves")
	}
	presenceTransport := &http.Transport{}
	if e = x.configure(presenceTransport, "edge-presence", pki.hop.ServerName); e != nil {
		t.Fatal(e)
	}
	presenceConn, e := presenceTransport.DialTLSContext(context.Background(), "tcp", u.Host)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = presenceConn.Close() }()
	if _, phase, _ := x.reader.Handle(request); phase != "handshake_verified" {
		t.Fatalf("actual handshake not confirmed: %s", phase)
	}
	// An exact discovery retry must preserve the live connection and handshake.
	if e = x.reader.Reload(); e != nil {
		t.Fatal(e)
	}
	x.mu.Lock()
	if len(x.connections) != 2 {
		t.Error("exact retry disconnected active TLS")
	}
	x.mu.Unlock()
	mu.Lock()
	current.Payload.ControlRevision = "2"
	current.Payload.RevokedInstallationIds = []string{"edge-install"}
	current.RootAuth = sign(current.Payload, root, "root", "Auriga.NetworkRootControl.v2")
	revoked := current
	mu.Unlock()
	x.mu.Lock()
	e = x.consumer.UpdateControl(revoked)
	isolated := x.withdrawLocked()
	x.mu.Unlock()
	if e != nil || !isolated {
		t.Fatal("root revocation did not enforce isolation")
	}
	if _, e = tr.DialTLSContext(context.Background(), "tcp", u.Host); e == nil {
		t.Fatal("revoked installation connected")
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, e = conn.Read(make([]byte, 1)); e == nil {
		t.Fatal("existing TLS connection survived isolation")
	}
	if _, phase, _ := x.reader.Handle(request); phase != "failed" {
		t.Fatal("revoked selector confirmed")
	}
	// A new process restores the root fence but cannot claim live TLS readiness.
	restarted, e := newEdgeMsgnet(cfg, pki.hop)
	if e != nil {
		t.Fatal(e)
	}
	if e = restarted.reader.Reload(); e == nil {
		t.Fatal("restart restored revoked TLS")
	}
}
