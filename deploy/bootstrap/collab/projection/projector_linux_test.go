package projection_test

import (
	"context"
	"crypto/hpke"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hansjone/auriga/deploy/bootstrap/collab/projection"
	"github.com/hansjone/auriga/deploy/bootstrap/collab/projection/testfixture"
	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

func TestProjectorSealedBootstrapLostAckAndRotation(t *testing.T) {
	if os.Geteuid() != 10001 {
		t.Skip("requires actual UID10001")
	}
	f := testfixture.New(t)
	root, control, projectorState, readerState := testfixture.Dir(t), testfixture.Dir(t), testfixture.Dir(t), testfixture.Dir(t)
	schemas, e := projection.LoadSchemas(filepath.Join("../../../..", "contracts/network/auto-renewal-v2.schema.json"))
	if e != nil {
		t.Fatal(e)
	}
	projectorTrust, e := projection.NewTrust(schemas, f.RootSPKI, f.Initial.Keyset.Payload.TenantId, "1", projectorState, f.Pin)
	if e != nil {
		t.Fatal(e)
	}
	readerTrust, e := projection.NewTrust(schemas, f.RootSPKI, f.Initial.Keyset.Payload.TenantId, "1", readerState, f.Pin)
	if e != nil {
		t.Fatal(e)
	}
	reader, e := projection.NewReader(root, control, readerState, func(g *projection.Generation) (func() error, error) {
		snap, e := readerTrust.Load(g)
		if e != nil {
			return nil, e
		}
		return func() error { defer snap.Clear(); return readerTrust.RecordLoaded(g.Selector, false) }, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- reader.Serve(ctx, "network") }()
	defer func() {
		cancel()
		if e := <-done; e != nil {
			t.Error(e)
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, e = os.Lstat(filepath.Join(control, "reload.sock")); e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reader socket not listening")
		}
		time.Sleep(time.Millisecond)
	}
	var mu sync.Mutex
	current := f.Initial
	nonces := map[string]api.ProjectionMaterial{}
	var receipts [][]byte
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		write := func(v any) { w.Header().Set("Content-Type", "application/json"); _ = json.NewEncoder(w).Encode(v) }
		switch q.URL.Path {
		case "/api/collab/v2/network/bootstrap/controls":
			write(current.Control)
		case "/api/collab/v2/network/bootstrap/keys":
			write(current)
		case "/api/collab/v2/network/bootstrap/transport":
			write(f.Bundle)
		case "/api/collab/v2/network/bootstrap/materials":
			var request api.ProjectionRequest
			if json.NewDecoder(q.Body).Decode(&request) != nil || projection.VerifyIdentity(request, request.IdentityAuth, "Auriga.NetworkProjectionRequest.v2", f.Pin.SigningSpki) != nil || request.InstallationId != f.Pin.IdentityId || request.IdentityAuth.Kid != f.Pin.IdentityId || request.Component != "network" || request.GatewayId != f.Pin.GatewayId {
				http.Error(w, "scope", http.StatusForbidden)
				return
			}
			if _, used := nonces[request.Nonce]; used {
				http.Error(w, "replay", http.StatusConflict)
				return
			}
			m := f.Material(t, current, request.Nonce)
			nonces[request.Nonce] = m
			bd, _ := projection.ObjectDigest(f.Bundle)
			s := api.SealedMaterial{TenantId: m.TenantId, Realm: m.Realm, InstallationId: m.InstallationId, Component: m.Component, Nonce: m.Nonce, Generation: m.Generation, KeysetVersion: m.KeysetVersion, BundleDigest: bd, Kem: "DHKEM(X25519,HKDF-SHA256)", Kdf: "HKDF-SHA256", Aead: "AES-256-GCM"}
			public, e := hpke.NewDHKEMPublicKey(f.Sealing.PublicKey())
			if e != nil {
				t.Error(e)
				http.Error(w, "suite", 500)
				return
			}
			enc, sender, e := hpke.NewSender(public, hpke.HKDFSHA256(), hpke.AES256GCM(), []byte("Auriga.NetworkSealedMaterial.v2"))
			if e != nil {
				t.Error(e)
				http.Error(w, "suite", 500)
				return
			}
			b, _ := projection.Bytes(s)
			var header map[string]json.RawMessage
			_ = json.Unmarshal(b, &header)
			delete(header, "enc")
			delete(header, "ciphertext")
			aad, _ := projection.Bytes(header)
			plain, _ := projection.Bytes(m)
			ciphertext, e := sender.Seal(aad, plain)
			clear(plain)
			if e != nil {
				t.Error(e)
				http.Error(w, "seal", 500)
				return
			}
			s.Enc = base64.RawURLEncoding.EncodeToString(enc)
			s.Ciphertext = base64.RawURLEncoding.EncodeToString(ciphertext)
			write(s)
		case "/api/collab/v2/network/projections/receipts":
			raw, e := io.ReadAll(q.Body)
			if e != nil {
				http.Error(w, "receipt", 400)
				return
			}
			var r api.ProjectionReceipt
			if json.Unmarshal(raw, &r) != nil || projection.VerifyIdentity(r, r.IdentityAuth, "Auriga.NetworkProjectionReceipt.v2", f.Pin.SigningSpki) != nil || r.LocalSelector == nil {
				http.Error(w, "receipt", http.StatusForbidden)
				return
			}
			m, ok := nonces[r.Nonce]
			if !ok || m.KeysetVersion != r.KeysetVersion || m.PublicationDigest != r.PublicationDigest || r.LocalSelector.Nonce != r.Nonce {
				http.Error(w, "binding", http.StatusConflict)
				return
			}
			receipts = append(receipts, raw)
			if len(receipts) == 1 {
				http.Error(w, "lost ack", http.StatusServiceUnavailable)
				return
			}
			write(r)
		default:
			http.NotFound(w, q)
		}
	}))
	defer server.Close()
	projector := &projection.Projector{Root: root, Control: control, Origin: server.URL, Schemas: schemas, Trust: projectorTrust, Pin: f.Pin, Signing: f.Signing, Sealing: f.Sealing, Client: server.Client()}
	if e = projector.Reconcile(ctx); e == nil {
		t.Fatal("lost acknowledgement treated as success")
	}
	if e = projector.Reconcile(ctx); e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	if len(receipts) != 2 || string(receipts[0]) != string(receipts[1]) {
		t.Fatal("ack retry changed signed bytes")
	}
	mu.Unlock()
	g, e := projection.ReadGeneration(root)
	if e != nil {
		t.Fatal(e)
	}
	if g.Selector.MaterialRevision != "1" {
		t.Fatal("retry created another material revision")
	}
	g.Clear()
	// A durable candidate existed when the issuer committed a newer snapshot.
	// Preserve its verified bytes and advance beyond its reserved material revision.
	nonce := make([]byte, 32)
	for j := range nonce {
		nonce[j] = 9
	}
	pending, e := projection.BuildGeneration(f.Material(t, f.Initial, base64.RawURLEncoding.EncodeToString(nonce)), f.Initial, "2")
	if e != nil {
		t.Fatal(e)
	}
	defer pending.Clear()
	candidate, e := projectorTrust.Load(pending)
	if e != nil {
		t.Fatal(e)
	}
	candidate.Clear()
	journal, e := projection.Bytes(pending)
	if e != nil {
		t.Fatal(e)
	}
	defer clear(journal)
	if e = os.WriteFile(filepath.Join(root, "transaction.json"), journal, 0600); e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	current = f.Activated
	f.RenewLeaves(t)
	mu.Unlock()
	if e = projector.Reconcile(ctx); e != nil {
		t.Fatal(e)
	}
	g, e = projection.ReadGeneration(root)
	if e != nil {
		t.Fatal(e)
	}
	defer g.Clear()
	if g.Selector.KeysetVersion != "3" || g.Selector.MaterialRevision != "3" {
		t.Fatal("rotation did not converge")
	}
	archived, e := os.ReadDir(filepath.Join(root, "transaction-history"))
	if e != nil || len(archived) != 1 {
		t.Fatal("superseded transaction evidence missing", e)
	}
}
