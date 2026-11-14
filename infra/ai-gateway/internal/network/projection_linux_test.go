package network

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/deploy/bootstrap/collab/projection"
	"github.com/hansjone/auriga/deploy/bootstrap/collab/projection/testfixture"
	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

func TestProjectionProtocolAndTLSWithoutRestart(t *testing.T) {
	f := testfixture.New(t)
	root, control, projectorControl := testfixture.Dir(t), testfixture.Dir(t), testfixture.Dir(t)
	schemaPath := filepath.Join("../../../..", "contracts/network/auto-renewal-v2.schema.json")
	schemas, e := projection.LoadSchemas(schemaPath)
	if e != nil {
		t.Fatal(e)
	}
	trust, e := projection.NewTrust(schemas, f.RootSPKI, f.Initial.Keyset.Payload.TenantId, "1", projectorControl, f.Pin)
	if e != nil {
		t.Fatal(e)
	}
	var current atomic.Pointer[api.Publication]
	current.Store(&f.Initial)
	fence := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		p := current.Load()
		switch q.URL.Path {
		case "/api/collab/v2/network/bootstrap/controls":
			_ = json.NewEncoder(w).Encode(p.Control)
		case "/api/collab/v2/network/bootstrap/keys":
			_ = json.NewEncoder(w).Encode(p)
		case "/api/collab/v2/network/keys/status":
			now := time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)
			s := api.RenewalStatus{TenantId: p.Keyset.Payload.TenantId, Realm: "1", State: "ready", AuthorityId: p.Authority.Payload.AuthorityId, ExpiryPolicy: "no_hard_expiry", Generation: p.Keyset.Payload.Generation, KeysetVersion: p.Keyset.Payload.KeysetVersion, ControlRevision: p.Control.Payload.ControlRevision, ReviewDueAt: now, NextRenewalAt: now, LastSuccessAt: now, BusinessValidUntil: now, FailureCode: "none"}
			_ = json.NewEncoder(w).Encode(s)
		default:
			http.NotFound(w, q)
		}
	}))
	defer fence.Close()
	write := func(name string, b []byte) string {
		t.Helper()
		path := filepath.Join(control, name)
		if e := os.WriteFile(path, b, 0600); e != nil {
			t.Fatal(e)
		}
		return path
	}
	caFile := write("fence-ca.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: fence.Certificate().Raw}))
	pins, _ := projection.Bytes(api.InstallationPins{SchemaVersion: 2, TenantId: f.Initial.Keyset.Payload.TenantId, Realm: "1", Pins: []api.InstallationPin{f.Pin}})
	pinsPath := write("pins.json", pins)
	controlBytes, _ := projection.Bytes(f.Initial.Control)
	controlSnapshotPath := write("root-control.json", controlBytes)
	publish := func(p api.Publication, revision, nonce string) *projection.Generation {
		t.Helper()
		m := f.Material(t, p, nonce)
		g, e := projection.BuildGeneration(m, p, revision)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(g.Clear)
		if e = projection.Publish(root, g, func(g *projection.Generation) error {
			s, e := trust.Load(g)
			if s != nil {
				s.Clear()
			}
			return e
		}); e != nil {
			t.Fatal(e)
		}
		return g
	}
	nonce := func(n byte) string {
		b := make([]byte, 32)
		for i := range b {
			b[i] = n
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	publish(f.Initial, "1", nonce(1))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signer := &errorSigner{}
	runtime, e := startProjection(ctx, ProjectionConfig{MaterialsDir: root, ControlDir: control, SchemaFile: schemaPath, RootSPKI: f.RootSPKI, TenantID: f.Initial.Keyset.Payload.TenantId, Realm: "1", PinsFile: pinsPath, InstallationID: f.Pin.IdentityId, FenceOrigin: fence.URL, FenceCAFile: caFile, ControlSnapshotFile: controlSnapshotPath}, signer)
	if e != nil {
		t.Fatal(e)
	}
	binding := liveBinding{Tenant: f.Initial.Keyset.Payload.TenantId, Realm: "1", Node: "node-1", Ingress: "ingress-1", Incarnation: "incarnation-1", ChannelNonce: "nonce-1", Epoch: 1, NotAfter: time.Now().Add(time.Hour)}
	verify := func(p api.Publication) {
		t.Helper()
		raw, e := signer.encode(binding, "request-1", &AppError{Code: CodeProtocolError, Scope: "request"})
		if e != nil {
			t.Fatal(e)
		}
		var frame map[string]any
		if e = json.Unmarshal(raw, &frame); e != nil {
			t.Fatal(e)
		}
		auth := frame["server_auth"].(map[string]any)
		sig, e := base64.RawURLEncoding.DecodeString(auth["signature"].(string))
		if e != nil {
			t.Fatal(e)
		}
		kid := auth["kid"].(string)
		delete(auth, "signature")
		body, e := json.Marshal(frame)
		if e != nil {
			t.Fatal(e)
		}
		for _, k := range p.Keyset.Payload.Keys {
			if k.Kid == kid && k.State == "active" && k.Purpose == "protocol_response" {
				der, e := base64.RawURLEncoding.DecodeString(k.PublicKeySpki)
				if e != nil {
					t.Fatal(e)
				}
				pub, e := x509.ParsePKIXPublicKey(der)
				if e != nil {
					t.Fatal(e)
				}
				if ed25519.Verify(pub.(ed25519.PublicKey), append([]byte("Auriga.ServerEnvelope.v1\x00"), body...), sig) {
					return
				}
			}
		}
		t.Fatal("response not verified by active published protocol key")
	}
	verify(f.Initial)
	importTrust, e := projection.NewTrust(schemas, f.RootSPKI, f.Initial.Keyset.Payload.TenantId, "1", testfixture.Dir(t), f.Pin)
	if e != nil {
		t.Fatal(e)
	}
	allowedControl := f.Initial.Control
	allowedControl.Payload.ControlRevision = "2"
	allowedBytes, _ := projection.Bytes(allowedControl.Payload)
	allowedControl.RootAuth.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(f.Root, append([]byte("Auriga.NetworkRootControl.v2\x00"), allowedBytes...)))
	if e = importTrust.PublishControlSnapshot(allowedControl, controlSnapshotPath); e != nil {
		t.Fatal(e)
	}
	verify(f.Initial)
	wrongRealm := binding
	wrongRealm.Realm = "2"
	if _, err := signer.encode(wrongRealm, "request-1", &AppError{Code: CodeProtocolError, Scope: "request"}); err == nil {
		t.Fatal("cross-realm protocol signing accepted")
	}
	wrongTenant := binding
	wrongTenant.Tenant = "another-tenant"
	if _, err := signer.encode(wrongTenant, "request-1", &AppError{Code: CodeProtocolError, Scope: "request"}); err == nil {
		t.Fatal("cross-tenant protocol signing accepted")
	}
	current.Store(&f.Activated)
	if _, e = signer.encode(binding, "request-1", &AppError{Code: CodeProtocolError, Scope: "request"}); e == nil {
		t.Fatal("old snapshot bypassed committed fence")
	}
	activated := publish(f.Activated, "2", nonce(2))
	reply := runtime.reader.Handle(api.LocalProjectionRequest{Operation: "reload", Selector: activated.Selector})
	_, phase, _, e := projection.ResponseFields(reply)
	if e != nil || phase != "loaded" {
		t.Fatal(phase, e)
	}
	verify(f.Activated)
	// A real mTLS server takes the current config for each handshake.
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) { return runtime.serverTLS() }}
	server.StartTLS()
	defer server.Close()
	handshake := func() string {
		t.Helper()
		snapshot := runtime.snapshot.Load()
		clientLeaf := snapshot.TLS.Leaves["broker-client"]
		client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: snapshot.TLS.ServerRoots, Certificates: []tls.Certificate{clientLeaf}}}}
		res, e := client.Get(server.URL)
		if e != nil {
			t.Fatal(e)
		}
		_, e = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
		if e != nil {
			t.Fatal(e)
		}
		if res.TLS == nil || len(res.TLS.VerifiedChains) == 0 {
			t.Fatal("unverified handshake")
		}
		return res.TLS.PeerCertificates[0].SerialNumber.String()
	}
	before := handshake()
	f.RenewLeaves(t)
	renewed := publish(f.Activated, "3", nonce(3))
	_, phase, _, e = projection.ResponseFields(runtime.reader.Handle(api.LocalProjectionRequest{Operation: "reload", Selector: renewed.Selector}))
	if e != nil || phase != "loaded" {
		t.Fatal(phase, e)
	}
	if after := handshake(); before == after {
		t.Fatal("TLS did not replace the certificate")
	}
	verify(f.Activated)
	// Same published keyset, wrong private material: rejected before selector swap.
	brokerCert, _ := base64.RawURLEncoding.DecodeString(f.BrokerLeaf.CertificateDer)
	brokerKey, _ := base64.RawURLEncoding.DecodeString(f.BrokerLeaf.PrivatePkcs8)
	brokerPair, e := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: brokerCert}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: brokerKey}))
	clear(brokerKey)
	if e != nil {
		t.Fatal(e)
	}
	broker, e := natsserver.NewServer(&natsserver.Options{Host: "127.0.0.1", Port: -1, NoLog: true, NoSigs: true, TLS: true, TLSVerify: true, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{brokerPair}, ClientCAs: runtime.snapshot.Load().TLS.ClientRoots, ClientAuth: tls.RequireAndVerifyClientCert}})
	if e != nil {
		t.Fatal(e)
	}
	go broker.Start()
	defer func() { broker.Shutdown(); broker.WaitForShutdown() }()
	if !broker.ReadyForConnections(5 * time.Second) {
		t.Fatal("native broker not ready")
	}
	conn, e := nats.Connect("tls://"+broker.Addr().String(), nats.SetCustomDialer(&projectedBrokerDialer{runtime: runtime, serverName: "localhost"}), nats.NoReconnect(), nats.Timeout(3*time.Second))
	if e != nil {
		t.Fatal(e)
	}
	if e = conn.FlushTimeout(3 * time.Second); e != nil {
		conn.Close()
		t.Fatal(e)
	}
	conn.Close()
	// Local load/handshake success must not stand in for C's global ledger.
	newLeaves := f.Bundle
	newLeaves.Payload.BundleVersion = "2"
	newLeaves.Payload.Phase = "new_leaves"
	newLeaves.Payload.PreviousBundleDigest, _ = projection.ObjectDigest(f.Bundle)
	bundleBytes, _ := projection.Bytes(newLeaves.Payload)
	newLeaves.RuntimeAuth.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(f.Runtime, append([]byte("Auriga.NetworkTransportTrustBundle.v2\x00"), bundleBytes...)))
	if err := runtime.trust.Accept(f.Activated, newLeaves); err != projection.Fault("projection_pending") {
		t.Fatal("global transport confirmation was fabricated", err)
	}
	m := f.Material(t, f.Activated, nonce(4))
	m.Protocol.ActivePkcs8 = f.Private["business_delivery-next"]
	wrong, e := projection.BuildGeneration(m, f.Activated, "4")
	if e != nil {
		t.Fatal(e)
	}
	defer wrong.Clear()
	if _, e = runtime.trust.Load(wrong); e == nil {
		t.Fatal("wrong protocol private key accepted")
	}
	revoked := f.Initial.Control
	revoked.Payload.ControlRevision = "3"
	revoked.Payload.RevokedAuthorityIds = []string{f.Initial.Authority.Payload.AuthorityId}
	raw, _ := projection.Bytes(revoked.Payload)
	revoked.RootAuth.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(f.Root, append([]byte("Auriga.NetworkRootControl.v2\x00"), raw...)))
	cd, _ := projection.ObjectDigest(revoked)
	controlPackage := api.ControlPackage{PackageId: "revoke-runtime", TenantId: f.Initial.Keyset.Payload.TenantId, Realm: "1", Control: revoked, ControlDigest: cd, TargetId: f.Pin.TargetId, Challenge: nonce(5)}
	receipts := testfixture.Dir(t)
	receipt, e := importTrust.InstallControlPackage(controlPackage, f.Signing, controlSnapshotPath, receipts)
	if e != nil {
		t.Fatal(e)
	}
	retry, e := importTrust.InstallControlPackage(controlPackage, f.Signing, controlSnapshotPath, receipts)
	if e != nil || !projection.Equal(receipt, retry) {
		t.Fatal("control acknowledgement retry changed bytes", e)
	}
	if e = projection.VerifyControlReceipt(controlPackage, receipt, f.Pin.SigningSpki, f.Pin.IdentityId, true, false, false, false, true); e != nil {
		t.Fatal(e)
	}
	if _, e = runtime.serverTLS(); e == nil {
		t.Fatal("independent revocation did not stop new TLS handshakes")
	}
	if _, e = signer.encode(binding, "request-1", &AppError{Code: CodeProtocolError, Scope: "request"}); e == nil {
		t.Fatal("revoked authority still signed")
	}
}
