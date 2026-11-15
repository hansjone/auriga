package app

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/identity"
	"github.com/hansjone/auriga/infra/agent/internal/msgnet"
	"github.com/hansjone/auriga/infra/agent/internal/testutil"
	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

func TestMsgnetRunAssemblyRequiresActualExecutionAndIndependentReceipt(t *testing.T) {
	rootDir := t.TempDir()
	agentDir := filepath.Join(rootDir, "agent")
	ca, e := testutil.NewTestCA()
	if e != nil {
		t.Fatal(e)
	}
	nativeKey, e := identity.GenerateKeyPair()
	if e != nil {
		t.Fatal(e)
	}
	cert, e := ca.IssueNode("native-node", &nativeKey.PublicKey, time.Now().Add(time.Hour))
	if e != nil {
		t.Fatal(e)
	}
	material := &identity.Material{NodeID: "native-node", PrivateKey: nativeKey, CertificatePEM: cert, CAPEM: ca.PEM}
	if _, e = rand.Read(material.SealingPrivate[:]); e != nil {
		t.Fatal(e)
	}
	if e = identity.Save(agentDir, material); e != nil {
		t.Fatal(e)
	}
	var output bytes.Buffer
	if e = RunCLI([]string{"msgnet-pin", "--data-root", rootDir, "--target-id", "node-target", "--identity-id", "independent-id", "--identity-revision", "1"}, &output, &bytes.Buffer{}); e != nil {
		t.Fatal(e)
	}
	var pin api.NodeControlPin
	if e = json.Unmarshal(output.Bytes(), &pin); e != nil {
		t.Fatal(e)
	}
	rootPublic, rootPrivate, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	spki, e := x509.MarshalPKIXPublicKey(rootPublic)
	if e != nil {
		t.Fatal(e)
	}
	serverCert, serverKey, e := ca.IssueWorkspace([]string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")})
	if e != nil {
		t.Fatal(e)
	}
	serverIdentity, e := tls.X509KeyPair(serverCert, serverKey)
	if e != nil {
		t.Fatal(e)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca.Cert)
	var allow, wrongScope atomic.Bool
	var serial atomic.Value
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 {
			t.Error("native mTLS missing")
			w.WriteHeader(403)
			return
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("node sent service credential")
		}
		id, err := identity.NodeIDFromCert(r.TLS.PeerCertificates[0])
		if err != nil || id != "native-node" {
			w.WriteHeader(403)
			return
		}
		serial.Store(r.TLS.PeerCertificates[0].SerialNumber.String())
		if !allow.Load() {
			w.WriteHeader(503)
			return
		}
		var record api.IsolationRecord
		if err = json.NewDecoder(r.Body).Decode(&record); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if wrongScope.Load() {
			record.TargetId = "self-reported-target"
		}
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(record)
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{serverIdentity}, ClientCAs: roots, ClientAuth: tls.RequireAndVerifyClientCert}
	server.StartTLS()
	defer server.Close()
	write := func(name string, value any) string {
		t.Helper()
		raw, err := msgnet.Bytes(value)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(agentDir, name)
		if err = identity.WritePrivateSnapshot(path, raw); err != nil {
			t.Fatal(err)
		}
		return path
	}
	pins := write("node-pins.json", api.NodeControlPins{SchemaVersion: 2, TenantId: "tenant", Realm: "1", Pins: []api.NodeControlPin{pin}})
	caPath := filepath.Join(agentDir, "proof-ca.crt")
	if e = identity.WritePrivateSnapshot(caPath, ca.PEM); e != nil {
		t.Fatal(e)
	}
	schema, e := filepath.Abs("../../../../contracts/network/auto-renewal-v2.schema.json")
	if e != nil {
		t.Fatal(e)
	}
	config := write("msgnet.json", msgnetRunConfig{RootSPKI: base64.RawURLEncoding.EncodeToString(spki), RootKid: "offline-root", NodePinsFile: pins, SchemaFile: schema, ProofOrigin: server.URL, ProofCAFile: caPath})
	installer, closeInstaller, e := newMsgnetInstaller(agentDir, config)
	if e != nil {
		t.Fatal(e)
	}
	defer closeInstaller()
	payload := api.RootControl{SchemaVersion: 2, TenantId: "tenant", Realm: "1", ControlRevision: "1", IssuedAt: time.Now().UTC().Truncate(time.Second).Format(time.RFC3339), AllowedAuthorities: []api.AuthorityReference{}, RevokedAuthorityIds: []string{}, RevokedInstallationIds: []string{}, MinimumGeneration: "1", MinimumKeysetVersion: "1"}
	raw, e := msgnet.Bytes(payload)
	if e != nil {
		t.Fatal(e)
	}
	control := api.SignedRootControl{Payload: payload, RootAuth: api.Signature{Alg: "Ed25519", Kid: "offline-root", Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(rootPrivate, append([]byte("Auriga.NetworkRootControl.v2\x00"), raw...)))}}
	raw, e = msgnet.Bytes(control)
	if e != nil {
		t.Fatal(e)
	}
	challenge := make([]byte, 32)
	if _, e = rand.Read(challenge); e != nil {
		t.Fatal(e)
	}
	pkg := api.ControlPackage{PackageId: "package-1", TenantId: "tenant", Realm: "1", TargetId: pin.TargetId, Control: control, ControlDigest: msgnet.Digest(raw), Challenge: base64.RawURLEncoding.EncodeToString(challenge)}
	raw, e = msgnet.Bytes(pkg)
	if e != nil {
		t.Fatal(e)
	}
	if receipt, err := installer.InstallControl(context.Background(), raw); err == nil || len(receipt) != 0 {
		t.Fatal("unconfirmed execution emitted a receipt")
	}
	allow.Store(true)
	receipt, e := installer.InstallControl(context.Background(), raw)
	if e != nil {
		t.Fatal(e)
	}
	var parsed api.InstallationReceipt
	if e = json.Unmarshal(receipt, &parsed); e != nil {
		t.Fatal(e)
	}
	var body map[string]any
	if e = json.Unmarshal(receipt, &body); e != nil {
		t.Fatal(e)
	}
	delete(body, "identity_auth")
	canonical, e := msgnet.Bytes(body)
	if e != nil {
		t.Fatal(e)
	}
	publicDER, e := base64.RawURLEncoding.DecodeString(pin.SigningSpki)
	if e != nil {
		t.Fatal(e)
	}
	public, e := x509.ParsePKIXPublicKey(publicDER)
	if e != nil {
		t.Fatal(e)
	}
	signature, e := base64.RawURLEncoding.DecodeString(parsed.IdentityAuth.Signature)
	if e != nil {
		t.Fatal(e)
	}
	if !ed25519.Verify(public.(ed25519.PublicKey), append([]byte("Auriga.NetworkControlReceipt.v2\x00"), canonical...), signature) {
		t.Fatal("receipt did not use independent confirmation identity")
	}
	before := serial.Load()
	material.CertificatePEM, e = ca.IssueNode("native-node", &nativeKey.PublicKey, time.Now().Add(2*time.Hour))
	if e != nil {
		t.Fatal(e)
	}
	if e = identity.Save(agentDir, material); e != nil {
		t.Fatal(e)
	}
	if _, e = installer.InstallControl(context.Background(), raw); e != nil {
		t.Fatal(e)
	}
	if serial.Load() == before {
		t.Fatal("continuous polling retained the old native TLS certificate")
	}
	wrongScope.Store(true)
	if receipt, err := installer.InstallControl(context.Background(), raw); err == nil || len(receipt) != 0 {
		t.Fatal("cached receipt bypassed mismatched execution scope")
	}
}
