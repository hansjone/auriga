package projection_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/deploy/bootstrap/collab/projection"
	"github.com/hansjone/auriga/deploy/bootstrap/collab/projection/testfixture"
	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

func runtimeSign(t *testing.T, payload any, key ed25519.PrivateKey, kid, domain string) api.Signature {
	t.Helper()
	b, e := projection.Bytes(payload)
	if e != nil {
		t.Fatal(e)
	}
	return api.Signature{Alg: "Ed25519", Kid: kid, Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, append([]byte(domain+"\x00"), b...)))}
}
func objectDigest(t *testing.T, v any) string {
	t.Helper()
	d, e := projection.ObjectDigest(v)
	if e != nil {
		t.Fatal(e)
	}
	return d
}

func TestTransportSourceAuthenticatedGlobalGatesAndRestart(t *testing.T) {
	f := testfixture.New(t)
	schemas, e := projection.LoadSchemas(filepath.Join("../../../..", "contracts/network/auto-renewal-v2.schema.json"))
	if e != nil {
		t.Fatal(e)
	}
	state, files := testfixture.Dir(t), testfixture.Dir(t)
	write := func(name string, b []byte) string {
		t.Helper()
		p := filepath.Join(files, name)
		if e := os.WriteFile(p, b, 0600); e != nil {
			t.Fatal(e)
		}
		return p
	}
	var ca []byte
	for _, c := range f.Bundle.Payload.Cas {
		if c.Role == "client" {
			ca, _ = base64.RawURLEncoding.DecodeString(c.CertificateDer)
		}
	}
	clientRoots := x509.NewCertPool()
	clientRoots.AppendCertsFromPEM(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca}))
	clientLeaf := f.Leaves[1]
	cert, _ := base64.RawURLEncoding.DecodeString(clientLeaf.CertificateDer)
	key, _ := base64.RawURLEncoding.DecodeString(clientLeaf.PrivatePkcs8)
	defer clear(key)
	certPath := write("client.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert}))
	keyPath := write("client.key", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}))
	jwtPath := write("service.jwt", []byte("synthetic-service-token"))
	now := time.Now().UTC().Truncate(time.Second)
	bundles := map[string]api.SignedTransportTrustBundle{}
	dual := f.Bundle
	bundles[objectDigest(t, dual)] = dual
	makeBundle := func(previous api.SignedTransportTrustBundle, phase, version string) api.SignedTransportTrustBundle {
		p := previous.Payload
		p.BundleVersion = version
		p.Phase = phase
		p.PreviousBundleDigest = objectDigest(t, previous)
		return api.SignedTransportTrustBundle{Payload: p, RuntimeAuth: runtimeSign(t, p, f.Runtime, f.Initial.Authority.Payload.RuntimeKid, "Auriga.NetworkTransportTrustBundle.v2")}
	}
	leaves := makeBundle(dual, "new_leaves", "2")
	bundles[objectDigest(t, leaves)] = leaves
	retired := makeBundle(leaves, "retire_old", "3")
	_, nextRuntime, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	defer clear(nextRuntime)
	nextAuthority := f.Initial.Authority
	nextAuthority.Payload.AuthorityId = "next-runtime-authority"
	nextAuthority.Payload.RuntimeKid = "next-runtime"
	der, e := x509.MarshalPKIXPublicKey(nextRuntime.Public())
	if e != nil {
		t.Fatal(e)
	}
	nextAuthority.Payload.PublicKeySpki = base64.RawURLEncoding.EncodeToString(der)
	nextAuthority.RootAuth = runtimeSign(t, nextAuthority.Payload, f.Root, "root-key", "Auriga.NetworkRuntimeAuthority.v2")
	nextControl := f.Initial.Control
	nextControl.Payload.ControlRevision = "2"
	nextControl.Payload.AllowedAuthorities = append([]api.AuthorityReference(nil), nextControl.Payload.AllowedAuthorities...)
	nextControl.Payload.AllowedAuthorities = append(nextControl.Payload.AllowedAuthorities, api.AuthorityReference{AuthorityId: nextAuthority.Payload.AuthorityId, AuthorityDigest: objectDigest(t, nextAuthority)})
	start := retired
	start.Payload.AuthorityId = nextAuthority.Payload.AuthorityId
	start.Payload.AuthorityDigest = objectDigest(t, nextAuthority)
	start.Payload.ControlRevision = "2"
	start.Payload.BundleVersion = "4"
	start.Payload.Phase = "dual_trust"
	start.Payload.PreviousBundleDigest = objectDigest(t, retired)
	start.Payload.Cas = append([]api.TransportCa(nil), retired.Payload.Cas...)
	fresh := testfixture.New(t)
	for _, ca := range fresh.Bundle.Payload.Cas {
		ca.CaId = "next-" + ca.CaId
		start.Payload.Cas = append(start.Payload.Cas, ca)
	}
	start.RuntimeAuth = runtimeSign(t, start.Payload, nextRuntime, nextAuthority.Payload.RuntimeKid, "Auriga.NetworkTransportTrustBundle.v2")
	nextControl.Payload.TransportTransition = &api.TransportTransition{Mode: "authority_transition", PredecessorDigest: objectDigest(t, retired), FromGeneration: "1", ToGeneration: "1", AuthorityId: start.Payload.AuthorityId, AuthorityDigest: start.Payload.AuthorityDigest, FirstBundleVersion: "4"}
	nextControl.RootAuth = runtimeSign(t, nextControl.Payload, f.Root, "root-key", "Auriga.NetworkRootControl.v2")
	transition := api.TransportTransitionProof{Control: nextControl, Authority: nextAuthority, FirstBundle: start, Predecessor: api.TransportPredecessor{Bundle: retired, Authority: f.Initial.Authority, Control: f.Initial.Control}}
	nextPublication := f.Initial
	nextPublication.Authority = nextAuthority
	nextPublication.Control = nextControl
	nextPublication.Keyset.Payload.AuthorityId = nextAuthority.Payload.AuthorityId
	nextPublication.Keyset.Payload.AuthorityDigest = objectDigest(t, nextAuthority)
	nextPublication.Keyset.Payload.ControlRevision = "2"
	nextPublication.Keyset.Payload.Mode = "authority_transition"
	nextPublication.Keyset.Payload.KeysetVersion = "3"
	nextPublication.Predecessor = &api.PredecessorProof{Authority: f.Initial.Authority, Control: f.Initial.Control, Keyset: f.Initial.Keyset, KeysetDigest: objectDigest(t, f.Initial.Keyset)}
	nextPublication.Keyset.RuntimeAuth = runtimeSign(t, nextPublication.Keyset.Payload, nextRuntime, nextAuthority.Payload.RuntimeKid, "Auriga.NetworkKeyset.v2")
	bundles[objectDigest(t, start)] = start
	nextLeaves := makeBundle(start, "new_leaves", "5")
	nextLeaves.RuntimeAuth = runtimeSign(t, nextLeaves.Payload, nextRuntime, nextAuthority.Payload.RuntimeKid, "Auriga.NetworkTransportTrustBundle.v2")
	bundles[objectDigest(t, nextLeaves)] = nextLeaves
	nextRetired := makeBundle(nextLeaves, "retire_old", "6")
	nextRetired.Payload.Cas = append([]api.TransportCa(nil), start.Payload.Cas[len(retired.Payload.Cas):]...)
	nextRetired.RuntimeAuth = runtimeSign(t, nextRetired.Payload, nextRuntime, nextAuthority.Payload.RuntimeKid, "Auriga.NetworkTransportTrustBundle.v2")
	// These selectors are independent C material-ledger entries, built before
	// either a query or response. Never reconstruct this map from response rows.
	selectors := map[string]map[string]api.LocalProjectionSelector{}
	for d, b := range bundles {
		selectors[d] = map[string]api.LocalProjectionSelector{}
		for _, id := range b.Payload.RequiredReaderIds {
			nonce := make([]byte, 32)
			if _, e := rand.Read(nonce); e != nil {
				t.Fatal(e)
			}
			component := "network"
			if strings.HasPrefix(id, "broker") {
				component = "broker"
			}
			pub := f.Initial
			if b.Payload.AuthorityId == nextAuthority.Payload.AuthorityId {
				pub = nextPublication
			}
			selectors[d][id] = api.LocalProjectionSelector{TenantId: b.Payload.TenantId, Realm: b.Payload.Realm, InstallationId: id, Component: component, Nonce: base64.RawURLEncoding.EncodeToString(nonce), Generation: b.Payload.Generation, KeysetVersion: pub.Keyset.Payload.KeysetVersion, PublicationDigest: objectDigest(t, pub.Keyset), BundleVersion: b.Payload.BundleVersion, BundleDigest: d, MaterialRevision: b.Payload.BundleVersion, ManifestDigest: projection.Digest([]byte(id + d))}
		}
	}
	phase := "loaded"
	corrupt, stale, wrongChallenge := false, false, false
	requests := 0
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		if strings.HasPrefix(q.URL.Path, "/api/collab/v2/network/bootstrap/") {
			if q.Header.Get("Authorization") != "" || len(q.TLS.PeerCertificates) != 0 {
				http.Error(w, "bootstrap identity leaked", http.StatusForbidden)
				return
			}
			_ = json.NewEncoder(w).Encode(f.Initial.Control)
			return
		}
		if q.TLS == nil || len(q.TLS.VerifiedChains) == 0 || q.Header.Get("Authorization") != "Bearer synthetic-service-token" {
			http.Error(w, "auth", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		write := func(v any) { _ = json.NewEncoder(w).Encode(v) }
		switch {
		case strings.Contains(q.URL.Path, "/transitions/"):
			if strings.HasSuffix(q.URL.Path, objectDigest(t, nextAuthority)) {
				write(transition)
			} else {
				http.NotFound(w, q)
			}
		case strings.Contains(q.URL.Path, "/history/"):
			b, ok := bundles[strings.TrimPrefix(q.URL.Path, "/api/collab/v2/network/transport/history/")]
			if !ok {
				http.NotFound(w, q)
				return
			}
			a, c := f.Initial.Authority, f.Initial.Control
			if b.Payload.AuthorityId == nextAuthority.Payload.AuthorityId {
				a, c = nextAuthority, nextControl
			}
			write(api.TransportPredecessor{Bundle: b, Authority: a, Control: c})
		case strings.HasSuffix(q.URL.Path, "/confirmation"):
			var query api.TransportConfirmationQuery
			if json.NewDecoder(q.Body).Decode(&query) != nil {
				http.Error(w, "query", http.StatusBadRequest)
				return
			}
			b, ok := bundles[query.BundleDigest]
			if !ok || b.Payload.BundleVersion != query.BundleVersion {
				http.Error(w, "bundle", http.StatusConflict)
				return
			}
			requests++
			a, c, k := f.Initial.Authority, f.Initial.Control, f.Runtime
			if b.Payload.AuthorityId == nextAuthority.Payload.AuthorityId {
				a, c, k = nextAuthority, nextControl, nextRuntime
			}
			payload := api.TransportConfirmation{TenantId: b.Payload.TenantId, Realm: b.Payload.Realm, AuthorityId: b.Payload.AuthorityId, AuthorityDigest: b.Payload.AuthorityDigest, ControlRevision: c.Payload.ControlRevision, BundleVersion: b.Payload.BundleVersion, BundleDigest: query.BundleDigest, Phase: b.Payload.Phase, RequiredReaderIds: b.Payload.RequiredReaderIds, Challenge: query.Challenge, IssuedAt: now.Format(time.RFC3339)}
			for _, id := range b.Payload.RequiredReaderIds {
				s := selectors[query.BundleDigest][id]
				if corrupt {
					s.MaterialRevision = "99"
				}
				payload.Readers = append(payload.Readers, api.TransportReaderEvidence{ReaderId: id, IdentityId: id, Selector: s, Phase: phase})
			}
			if stale {
				payload.IssuedAt = now.Add(-61 * time.Second).Format(time.RFC3339)
			}
			if wrongChallenge {
				payload.Challenge = base64.RawURLEncoding.EncodeToString(make([]byte, 32))
			}
			write(api.SignedTransportConfirmation{Payload: payload, RuntimeAuth: runtimeSign(t, payload, k, a.Payload.RuntimeKid, "Auriga.NetworkTransportConfirmation.v2")})
		default:
			http.NotFound(w, q)
		}
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, ClientAuth: tls.VerifyClientCertIfGiven, ClientCAs: clientRoots}
	server.StartTLS()
	defer server.Close()
	caPath := write("https-ca.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}))
	request, e := projection.NewTransportHTTP(server.URL, caPath, certPath, keyPath, jwtPath)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = request(context.Background(), "GET", "/api/collab/v2/network/bootstrap/controls", nil); e != nil {
		t.Fatal("public bootstrap depended on service identity", e)
	}
	source := &projection.TransportSource{Schemas: schemas, Root: f.RootSPKI, Tenant: f.Initial.Keyset.Payload.TenantId, Realm: "1", Target: f.Pin.TargetId, Request: request, Now: func() time.Time { return now }}
	trust, e := projection.NewTrustWithClock(schemas, f.RootSPKI, source.Tenant, "1", state, f.Pin, func() time.Time { return now })
	if e != nil {
		t.Fatal(e)
	}
	source.Bind(context.Background(), trust)
	if e = trust.Accept(f.Initial, dual); e != nil {
		t.Fatal(e)
	}
	if e = trust.Accept(f.Initial, leaves); e == nil {
		t.Fatal("accepted missing independent material ledger")
	}
	source.ExpectedSelectors = func(_ context.Context, b api.SignedTransportTrustBundle) (map[string]string, error) {
		out := map[string]string{}
		for id, s := range selectors[objectDigest(t, b)] {
			out[id] = objectDigest(t, s)
		}
		return out, nil
	}
	for _, kind := range []string{"selector", "stale", "challenge"} {
		corrupt, stale, wrongChallenge = kind == "selector", kind == "stale", kind == "challenge"
		if e = trust.Accept(f.Initial, leaves); e == nil {
			t.Fatal("accepted", kind)
		}
	}
	corrupt, stale, wrongChallenge = false, false, false
	if e = trust.Accept(f.Initial, leaves); e != nil {
		t.Fatal(e)
	}
	if e = trust.Accept(f.Initial, retired); e == nil {
		t.Fatal("loaded falsely proved handshakes")
	}
	phase = "handshake_verified"
	if e = trust.Accept(f.Initial, retired); e != nil {
		t.Fatal(e)
	}
	restarted, e := projection.NewTrustWithClock(schemas, f.RootSPKI, source.Tenant, "1", state, f.Pin, func() time.Time { return now })
	if e != nil {
		t.Fatal(e)
	}
	source.Bind(context.Background(), restarted)
	stale = true
	if e = restarted.Accept(f.Initial, retired); e == nil {
		t.Fatal("restart reused stale global proof")
	}
	stale = false
	if e = restarted.Accept(f.Initial, retired); e != nil {
		t.Fatal(e)
	}
	if requests < 8 {
		t.Fatal("did not request fresh global evidence", requests)
	}
	// Changing the service token is observed without rebuilding the client.
	write("service.jwt", []byte("revoked-service-token"))
	if e = restarted.Accept(f.Initial, retired); e == nil {
		t.Fatal("reused cached JWT")
	}
	write("service.jwt", []byte("synthetic-service-token"))
	if e = restarted.Accept(nextPublication, start); e != nil {
		t.Fatal("root-anchored runtime transition", e)
	}
	if e = restarted.Accept(f.Initial, retired); e == nil {
		t.Fatal("rolled back the new runtime's bundle high water")
	}
	// Persisted current version survives a process restart without using a
	// historical root control as the latest allowlist.
	last, e := projection.NewTrustWithClock(schemas, f.RootSPKI, source.Tenant, "1", state, f.Pin, func() time.Time { return now })
	if e != nil {
		t.Fatal(e)
	}
	source.Bind(context.Background(), last)
	if e = last.Accept(nextPublication, start); e != nil {
		t.Fatal("repeat anchored bundle after restart", e)
	}
	if e = last.Accept(nextPublication, nextLeaves); e != nil {
		t.Fatal("new runtime leaves gate", e)
	}
	if e = last.Accept(nextPublication, nextRetired); e != nil {
		t.Fatal("new runtime retire gate", e)
	}
	last, e = projection.NewTrustWithClock(schemas, f.RootSPKI, source.Tenant, "1", state, f.Pin, func() time.Time { return now })
	if e != nil {
		t.Fatal(e)
	}
	source.Bind(context.Background(), last)
	for _, rename := range []bool{false, true} {
		resurrection := makeBundle(nextRetired, "dual_trust", "7")
		ca := dual.Payload.Cas[0]
		if rename {
			ca.CaId = "renamed-old-ca"
		}
		resurrection.Payload.Cas = append(append([]api.TransportCa(nil), resurrection.Payload.Cas...), ca)
		resurrection.RuntimeAuth = runtimeSign(t, resurrection.Payload, nextRuntime, nextAuthority.Payload.RuntimeKid, "Auriga.NetworkTransportTrustBundle.v2")
		if e = last.Accept(nextPublication, resurrection); e == nil {
			t.Fatal("resurrected retired CA after restart", rename)
		}
	}
}
