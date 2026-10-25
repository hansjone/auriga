package network

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

func signingFixture(t *testing.T) ProtocolSigningConfig {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "protocol.pem")
	if err = os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	return ProtocolSigningConfig{PrivateKeyFile: path, KeyID: "test-protocol", KeysetVersion: "1", NotBefore: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), NotAfter: time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}
}

func signedMailboxFrame(t *testing.T, cfg ProtocolSigningConfig, op string, b liveBinding, requestID string) []byte {
	t.Helper()
	s, err := newErrorSigner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(s.key)
	raw, err := s.encode(b, requestID, &AppError{Code: CodeConflict, Scope: "request"})
	if err != nil {
		t.Fatal(err)
	}
	frame := decodeObject(t, raw)
	delete(frame, "code")
	delete(frame, "scope")
	delete(frame, "retryable")
	delete(frame, "retry_after")
	frame["type"] = map[string]string{"ready": "ready_result", "head": "head", "fetch": "fetch_result", "receipt": "receipt_committed"}[op]
	frame["epoch"] = u64str(b.Epoch)
	frame["head_seq"] = "9"
	if op == "ready" {
		frame["status"] = "ready"
		frame["node_identity"] = map[string]any{"realm": b.Realm, "node": b.Node, "incarnation": b.Incarnation}
	}
	if op == "head" {
		delete(frame, "request_id")
	}
	if op == "fetch" {
		frame["type"] = "fetch_page"
	}
	auth := frame["server_auth"].(map[string]any)
	delete(auth, "signature")
	raw, err = json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	auth["signature"] = base64.RawURLEncoding.EncodeToString(ed25519.Sign(s.key, append([]byte("Auriga.ServerEnvelope.v1\x00"), raw...)))
	if op == "fetch" {
		frame = map[string]any{"app_version": "1.0", "type": "fetch_result", "page": frame, "items": []any{}}
	}
	raw, err = json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestProductionMailboxRoutesSignedFramesAndRejectsSpoofedIngress(t *testing.T) {
	h, cfg, _ := runtimeFixture(t)
	f := &fakeMailboxRPC{}
	cfg.Collab = startMailboxRPC(t, f)
	cfg.ProtocolSigning = signingFixture(t)
	cfg.Data = NATSConfig{URL: h.tcpURL(), Username: h.adptUser, PasswordFile: filepath.Join(t.TempDir(), "data.password")}
	if err := os.WriteFile(cfg.Data.PasswordFile, []byte(h.adptPass), 0600); err != nil {
		t.Fatal(err)
	}
	runtime, err := StartRuntime(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)
	nc, err := nats.Connect(h.wsURL(), nats.Token(connectTicket), nats.NoReconnect(), nats.ErrorHandler(func(*nats.Conn, *nats.Subscription, error) {}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	ingress := ingressForTicket("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	down, err := nc.SubscribeSync(DownSubject(ingress))
	if err != nil {
		t.Fatal(err)
	}
	if err = nc.Flush(); err != nil {
		t.Fatal(err)
	}
	b := runtime.redeemer.snapshot()[0]
	b.Epoch = 42
	b.Incarnation = "inc-1"
	b.ChannelNonce = "nonce-1"
	for _, op := range []string{"ready", "fetch", "receipt", "head"} {
		requestID := "req-1"
		if op == "head" {
			requestID = ""
		}
		response := signedMailboxFrame(t, cfg.ProtocolSigning, op, b, requestID)
		f.mu.Lock()
		f.response = response
		f.mu.Unlock()
		raw := map[string]string{
			"ready":   `{"app_version":"1.0","type":"ready","request_id":"req-1","channel_nonce":"nonce-1","boot_id":"boot-1"}`,
			"fetch":   `{"app_version":"1.0","type":"fetch","request_id":"req-1","epoch":"42","resume_cursor":"0","continuation":null,"max_items":1,"max_bytes":4096,"tenant":"forged","node":"999"}`,
			"receipt": `{"app_version":"1.0","type":"receipt","request_id":"req-1","operation_id":"op-1","epoch":"42","delivery_id":"d-1","mail_seq":"1","results":[{"recipient_id":"r-1","status":"host_accepted","expected_state_version":"1","message_digest":"sha256:proof","host_request_id":"host-1"}]}`,
		}[op]
		// An untrusted reply never redirects data away from down.<authenticated ingress>.
		if op == "head" {
			err = runtime.data.Publish(headHintRoot+ingress, nil)
		} else {
			err = nc.PublishRequest(UpSubject(ingress), "auriga.net.down.foreign", []byte(raw))
		}
		if err != nil {
			t.Fatal(err)
		}
		msg, e := down.NextMsg(time.Second)
		if e != nil || string(msg.Data) != string(response) {
			t.Fatalf("%s got=%v err=%v", op, msg, e)
		}
		runtime.mailbox.workers.Wait()
	}
	f.mu.Lock()
	calls := len(f.calls)
	for _, id := range f.identities {
		if id.Node != "1" || id.TenantId != testTenant || id.Ingress != ingress {
			t.Fatal("payload claims became identity")
		}
	}
	f.mu.Unlock()
	foreign := ingressForTicket("foreign")
	if err = nc.Publish(UpSubject(foreign), []byte(`{"type":"fetch"}`)); err != nil {
		t.Fatal(err)
	}
	if err = nc.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, err = down.NextMsg(50 * time.Millisecond); err == nil {
		t.Fatal("cross-ingress publish reached route")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) != calls {
		t.Fatal("forged subject reached collab")
	}
}

const validMailboxFetch = `{"app_version":"1.0","type":"fetch","request_id":"req-1","epoch":"42","resume_cursor":"0","continuation":null,"max_items":1,"max_bytes":4096}`

func TestProductionMailboxBoundsInflightAndSuppressesRevokedFetch(t *testing.T) {
	h, cfg, _ := runtimeFixture(t)
	f := &fakeMailboxRPC{entered: make(chan struct{}, 1), release: make(chan struct{})}
	cfg.Collab = startMailboxRPC(t, f)
	runtime, err := StartRuntime(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)
	nc, err := nats.Connect(h.wsURL(), nats.Token(connectTicket), nats.NoReconnect())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	b := runtime.redeemer.snapshot()[0]
	b.Epoch = 42
	b.Incarnation = "inc-1"
	b.ChannelNonce = "nonce-1"
	runtime.redeemer.mu.Lock()
	runtime.redeemer.bindings[b.Ingress] = b
	runtime.redeemer.mu.Unlock()
	f.mu.Lock()
	f.response = signedMailboxFrame(t, cfg.ProtocolSigning, "fetch", b, "req-1")
	f.mu.Unlock()
	sub, err := nc.SubscribeSync(DownSubject(b.Ingress))
	if err != nil {
		t.Fatal(err)
	}
	if err = nc.Flush(); err != nil {
		t.Fatal(err)
	}
	publish := func() {
		t.Helper()
		if err := nc.Publish(UpSubject(b.Ingress), []byte(validMailboxFetch)); err != nil {
			t.Fatal(err)
		}
	}
	expectCode := func(code string) {
		t.Helper()
		msg, err := sub.NextMsg(time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if got := decodeObject(t, msg.Data)["code"]; got != code {
			t.Fatalf("code=%v want=%s", got, code)
		}
		verifyErrorSignature(t, cfg.ProtocolSigning, msg.Data)
	}
	publish()
	select {
	case <-f.entered:
	case <-time.After(time.Second):
		t.Fatal("FETCH never reached collab")
	}
	publish()
	expectCode("network.overloaded")
	runtime.redeemer.mu.Lock()
	b.Revoked = true
	runtime.redeemer.bindings[b.Ingress] = b
	runtime.redeemer.mu.Unlock()
	close(f.release)
	expectCode("network.revoked")
	runtime.mailbox.workers.Wait()
	publish()
	expectCode("network.revoked")
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) != 1 {
		t.Fatalf("revoked/busy requests reached backend: %d", len(f.calls))
	}
}

func TestProductionMailboxGlobalInflightIsBounded(t *testing.T) {
	_, cfg, _ := runtimeFixture(t)
	f := &fakeMailboxRPC{block: true, entered: make(chan struct{}, mailboxWorkers+1)}
	cfg.Collab = startMailboxRPC(t, f)
	cfg.MaxConnections = mailboxWorkers + 1
	runtime, err := StartRuntime(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)
	for i := 0; i < mailboxWorkers+1; i++ {
		ingress := ingressForTicket(fmt.Sprint(i))
		b := liveBinding{Binding: Binding{ServerID: runtime.redeemer.cfg.ServerID, ClientID: uint64(i + 1), Account: clientAccount, Ingress: ingress}, Tenant: testTenant, Realm: "1", Node: "1", Incarnation: "inc-1", Epoch: 42, ChannelNonce: "nonce-1", NotAfter: time.Now().Add(time.Hour), CheckAfter: time.Now().Add(time.Hour)}
		runtime.redeemer.mu.Lock()
		runtime.redeemer.bindings[ingress] = b
		runtime.redeemer.mu.Unlock()
		runtime.mailbox.receive(&nats.Msg{Subject: UpSubject(ingress), Data: []byte(validMailboxFetch)})
	}
	if len(runtime.mailbox.slots) != mailboxWorkers {
		t.Fatalf("unbounded slots=%d", len(runtime.mailbox.slots))
	}
	for i := 0; i < mailboxWorkers; i++ {
		select {
		case <-f.entered:
		case <-time.After(time.Second):
			t.Fatal("admitted calls did not enter collab")
		}
	}
	f.mu.Lock()
	calls := len(f.calls)
	f.mu.Unlock()
	if calls != mailboxWorkers {
		t.Fatalf("backend calls=%d", calls)
	}
	runtime.Close()
	if len(runtime.mailbox.slots) != 0 || len(runtime.redeemer.inFlight) != 0 {
		t.Fatal("shutdown leaked in-flight requests")
	}
}

func verifyErrorSignature(t *testing.T, cfg ProtocolSigningConfig, raw []byte) {
	t.Helper()
	s, err := newErrorSigner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(s.key)
	frame := decodeObject(t, raw)
	auth := frame["server_auth"].(map[string]any)
	signature, err := base64.RawURLEncoding.DecodeString(auth["signature"].(string))
	if err != nil {
		t.Fatal(err)
	}
	delete(auth, "signature")
	canonical, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	if !ed25519.Verify(s.key.Public().(ed25519.PublicKey), append([]byte("Auriga.ServerEnvelope.v1\x00"), canonical...), signature) {
		t.Fatal("ERROR did not carry a real Ed25519 signature")
	}
}

func TestProductionMailboxTimeoutProducesSignedContractError(t *testing.T) {
	h, cfg, _ := runtimeFixture(t)
	cfg.Collab = startMailboxRPC(t, &fakeMailboxRPC{block: true})
	runtime, err := StartRuntime(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)
	nc, err := nats.Connect(h.wsURL(), nats.Token(connectTicket), nats.NoReconnect())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	b := runtime.redeemer.snapshot()[0]
	b.Epoch = 42
	b.Incarnation = "inc-1"
	b.ChannelNonce = "nonce-1"
	runtime.redeemer.mu.Lock()
	runtime.redeemer.bindings[b.Ingress] = b
	runtime.redeemer.mu.Unlock()
	sub, err := nc.SubscribeSync(DownSubject(b.Ingress))
	if err != nil {
		t.Fatal(err)
	}
	if err = nc.Flush(); err != nil {
		t.Fatal(err)
	}
	if err = nc.Publish(UpSubject(b.Ingress), []byte(validMailboxFetch)); err != nil {
		t.Fatal(err)
	}
	msg, err := sub.NextMsg(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	frame := decodeObject(t, msg.Data)
	if frame["code"] != "network.overloaded" || frame["retryable"] != true || frame["request_id"] != "req-1" {
		t.Fatalf("wrong timeout: %s", msg.Data)
	}
	verifyErrorSignature(t, cfg.ProtocolSigning, msg.Data)
}

func TestMailboxResponseRejectsWrongAudienceNonceRequestAndUnsignedSkeleton(t *testing.T) {
	cfg := signingFixture(t)
	b := liveBinding{Binding: Binding{Ingress: ingressForTicket("x")}, Realm: "2", Node: "64", Incarnation: "inc-1", Epoch: 42, ChannelNonce: "nonce-1", NotAfter: time.Now().Add(time.Hour)}
	original := signedMailboxFrame(t, cfg, "fetch", b, "req-1")
	for name, mutate := range map[string]func(map[string]any, map[string]any){
		"missing page version": func(p map[string]any, _ map[string]any) { delete(p, "app_version") },
		"foreign node":         func(_ map[string]any, a map[string]any) { a["audience"].(map[string]any)["node"] = "65" },
		"nonce":                func(_ map[string]any, a map[string]any) { a["channel_nonce"] = "nonce-2" },
		"epoch":                func(_ map[string]any, a map[string]any) { a["audience"].(map[string]any)["epoch"] = "43" },
		"request":              func(p map[string]any, _ map[string]any) { p["request_id"] = "another-request" },
		"unsigned":             func(_ map[string]any, a map[string]any) { delete(a, "signature") },
		"skeleton":             func(_ map[string]any, a map[string]any) { a["kid"] = "stub-unsigned" },
	} {
		t.Run(name, func(t *testing.T) {
			m := decodeObject(t, original)
			p := m["page"].(map[string]any)
			mutate(p, p["server_auth"].(map[string]any))
			raw, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = validateMailboxResponse(b, "fetch", "req-1", []byte(validMailboxFetch), raw); err == nil {
				t.Fatal("invalid signed response was forwarded")
			}
		})
	}
}

func TestShardedUpSubjectRejectsWrongShardAndExtraTokens(t *testing.T) {
	ingress := ingressForTicket("x")
	subject := UpSubject(ingress)
	if len(strings.Split(subject, ".")) != 5 {
		t.Fatal("up subject has no shard")
	}
	for _, bad := range []string{upRoot + ingress, upRoot + "wrong." + ingress, subject + ".extra", upRoot + "*." + ingress} {
		if _, err := IngressFromUp(bad); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}
