package network

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
)

// A diagnostic guard, independent of production RPC/business deadlines.
const registrationPhaseGuard = 5 * time.Second

func awaitRegistrationRPC(ctx context.Context, conn *grpc.ClientConn) error {
	conn.Connect()
	for {
		state := conn.GetState()
		if state == connectivity.Ready {
			return nil
		}
		if !conn.WaitForStateChange(ctx, state) {
			return ctx.Err()
		}
	}
}

func TestLocalRegisterPhaseRPCReadinessHonorsCancellation(t *testing.T) {
	conn, err := grpc.NewClient("127.0.0.1:0", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := awaitRegistrationRPC(ctx, conn); err != context.Canceled {
		t.Fatalf("connection wait lost cancellation: %v", err)
	}
}

func TestLocalRegisterPhaseWaitsForRPCConnection(t *testing.T) {
	cfg := startMailboxRPC(t, &fakeMailboxRPC{})
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	conn, err := grpc.NewClient(cfg.Target, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, address string) (net.Conn, error) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return (&net.Dialer{}).DialContext(ctx, "tcp", address)
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), registrationPhaseGuard)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- awaitRegistrationRPC(ctx, conn) }()
	select {
	case <-entered:
	case err := <-result:
		t.Fatalf("readiness returned before connecting: %v", err)
	case <-ctx.Done():
		t.Fatal("connection attempt never entered")
	}
	select {
	case err := <-result:
		t.Fatalf("readiness returned while connection held: %v", err)
	default:
	}
	unblock()
	select {
	case err := <-result:
		if err != nil || conn.GetState() != connectivity.Ready {
			t.Fatalf("connection not ready: state=%v error=%v", conn.GetState(), err)
		}
	case <-ctx.Done():
		t.Fatal("connection readiness guard expired")
	}
}

type registrationPublication struct {
	subject string
	raw     []byte
	err     error
}

type registrationPhasePublisher struct {
	nc                 *nats.Conn
	client             *nats.Conn
	before             func([]byte)
	completed          chan registrationPublication
	mu                 sync.Mutex
	entered, published int
}

func (p *registrationPhasePublisher) Publish(subject string, raw []byte) error {
	p.mu.Lock()
	p.entered++
	p.mu.Unlock()
	if p.before != nil {
		p.before(raw)
	}
	err := p.nc.Publish(subject, raw)
	if err == nil {
		// Publish only queues bytes; PONG confirms the actual broker write.
		err = p.nc.FlushTimeout(registrationPhaseGuard)
	}
	p.mu.Lock()
	if err == nil {
		p.published++
	}
	p.mu.Unlock()
	p.completed <- registrationPublication{subject, append([]byte(nil), raw...), err}
	return err
}

func registrationPhaseObserver(run *Runtime) *registrationPhasePublisher {
	p := run.mailbox.publisher
	if counter, ok := p.(*countingPublisher); ok {
		p = counter.delegate
	}
	return p.(*registrationPhasePublisher)
}

func awaitRegistrationPublication(p *registrationPhasePublisher, guard time.Duration) (registrationPublication, error) {
	timer := time.NewTimer(guard)
	defer timer.Stop()
	select {
	case event := <-p.completed:
		if event.err != nil {
			return event, fmt.Errorf("publication failed: %w", event.err)
		}
		return event, nil
	case <-timer.C:
		return registrationPublication{}, fmt.Errorf("publication completion guard expired")
	}
}

func registrationPhaseDiagnostic(run *Runtime, client *nats.Conn, b liveBinding) string {
	r := run.mailbox
	p := registrationPhaseObserver(run)
	if p.client != nil {
		client = p.client
	}
	node := nodeIdentity{b.Tenant, b.Realm, b.Node}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.redeemer.mu.Lock()
	current, known := r.redeemer.bindings[b.Ingress]
	inFlight := r.redeemer.inFlight[b.Ingress]
	r.redeemer.mu.Unlock()
	var delivered int64
	var pending, dropped int
	if sub := r.upSubscriptions[b.Ingress]; sub != nil {
		delivered, _ = sub.Delivered()
		pending, _, _ = sub.Pending()
		dropped, _ = sub.Dropped()
	}
	var budget nodeBudget
	if value := r.nodeBudgets[node]; value != nil {
		budget = *value
	}
	var tenant requestBucket
	if value := r.tenantBudgets[b.Tenant]; value != nil {
		tenant = *value
	}
	p.mu.Lock()
	entered, published := p.entered, p.published
	p.mu.Unlock()
	return fmt.Sprintf("client=%v client_error=%v data=%v data_error=%v\ninbound_delivered=%d pending=%d dropped=%d publisher_entered=%d broker_published=%d\nbusy=%v in_flight=%v slots=%d/%d error_slots=%d/%d tenant_active=%d tenant_errors=%d\nnode_budget=%+v tenant_budget=%+v\nknown=%v binding=%+v\nexpected=%+v", client.Status(), client.LastError(), r.nc.Status(), r.nc.LastError(), delivered, pending, dropped, entered, published, r.nodeBusy[node], inFlight, len(r.slots), cap(r.slots), len(r.errorSlots), cap(r.errorSlots), r.tenantActive[b.Tenant], r.tenantErrors[b.Tenant], budget, tenant, known, current, b)
}

func validateRegistrationPhaseAuth(cfg ProtocolSigningConfig, b liveBinding, raw []byte, requestID any) error {
	var frame map[string]any
	if err := json.Unmarshal(raw, &frame); err != nil {
		return fmt.Errorf("invalid signed frame: %w", err)
	}
	auth, ok := frame["server_auth"].(map[string]any)
	if !ok {
		return fmt.Errorf("missing server_auth")
	}
	audience, _ := auth["audience"].(map[string]any)
	if auth["request_id"] != requestID || auth["channel_nonce"] != b.ChannelNonce || audience["realm"] != b.Realm || audience["node"] != b.Node || audience["incarnation"] != b.Incarnation || audience["epoch"] != u64str(b.Epoch) {
		return fmt.Errorf("signed audience/nonce/request mismatch")
	}
	if auth["alg"] != "Ed25519" || auth["purpose"] != "protocol_response" || auth["kid"] != cfg.KeyID {
		return fmt.Errorf("signed authority mismatch")
	}
	encoded, _ := auth["signature"].(string)
	signature, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Errorf("signature encoding: %w", err)
	}
	delete(auth, "signature")
	canonical, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	signer, err := newErrorSigner(cfg)
	if err != nil {
		return err
	}
	defer clear(signer.key)
	if !ed25519.Verify(signer.key.Public().(ed25519.PublicKey), append([]byte("Auriga.ServerEnvelope.v1\x00"), canonical...), signature) {
		return fmt.Errorf("signature verification failed")
	}
	return nil
}

func validateRegistrationReady(cfg ProtocolSigningConfig, b liveBinding, raw []byte) error {
	var frame struct {
		Type string `json:"type"`
		Code string `json:"code"`
	}
	if json.Unmarshal(raw, &frame) != nil || frame.Type != "ready_result" {
		return fmt.Errorf("READY success required, received %q code=%q", frame.Type, frame.Code)
	}
	updated, err := validateMailboxResponse(b, "ready", "req-ready", []byte(`{"channel_nonce":"nonce-1"}`), raw)
	if err != nil || updated != b {
		return fmt.Errorf("READY binding mismatch: %v", err)
	}
	return validateRegistrationPhaseAuth(cfg, b, raw, "req-ready")
}

func validateRegistrationRejection(cfg ProtocolSigningConfig, b liveBinding, msg *nats.Msg, code string, requestID any) error {
	var frame map[string]any
	if json.Unmarshal(msg.Data, &frame) != nil || msg.Subject != DownSubject(b.Ingress) || frame["app_version"] != AppVersion || frame["type"] != "error" || frame["code"] != code || frame["request_id"] != requestID || strings.Contains(string(msg.Data), "private backend") {
		return fmt.Errorf("wrong rejection subject=%s body=%s", msg.Subject, msg.Data)
	}
	return validateRegistrationPhaseAuth(cfg, b, msg.Data, requestID)
}

func registrationBusinessRPCs(f *fakeMailboxRPC) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) != 1 || f.calls[0] != "ready" {
		return fmt.Errorf("unexpected business RPC: %v", f.calls)
	}
	return nil
}

func expectLocalRegistrationRejection(t *testing.T, run *Runtime, f *fakeMailboxRPC, counter *countingPublisher, down *nats.Subscription, cfg ProtocolSigningConfig, b liveBinding, requestID any) {
	t.Helper()
	p := registrationPhaseObserver(run)
	publication, err := awaitRegistrationPublication(p, registrationPhaseGuard)
	if err != nil {
		t.Fatalf("%v; %s", err, registrationPhaseDiagnostic(run, p.nc, b))
	}
	// The local mailboxOperation rejection must never enter a business RPC.
	// Check these anchors before inspecting the signed downlink.
	assertRegistrationCounts(t, run, f, counter, b, 1, 1)
	if err := registrationBusinessRPCs(f); err != nil {
		t.Fatal(err)
	}
	expectRegistrationDownlink(t, run, down, cfg, b, CodeProtocolError, requestID, publication)
}

func TestLocalRegisterPhaseReadiness(t *testing.T) {
	f := &fakeMailboxRPC{block: true}
	run, _, _, b, _, err := localRegisterPhaseFixture(t, f, nil)
	if err == nil || !strings.Contains(err.Error(), "READY success required") {
		t.Fatalf("failed READY was not rejected precisely: %v", err)
	}
	if got := run.redeemer.snapshot()[0]; got.Epoch != 0 || got == b {
		t.Fatalf("failed READY unexpectedly installed binding: %+v", got)
	}
}

// Hold real publication after entry, beyond the production RPC limit.
// The timer injects known latency; it never infers that a worker entered.
func registrationPublicationGate(t *testing.T, kind string) func(*registrationPhasePublisher) {
	t.Helper()
	return func(p *registrationPhasePublisher) {
		entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
		var once sync.Once
		unblock := func() { once.Do(func() { close(release) }) }
		t.Cleanup(func() {
			unblock()
			<-done
			select {
			case <-entered:
			default:
				t.Error("controlled publication gate never entered")
			}
		})
		go func() {
			defer close(done)
			select {
			case <-entered:
			case <-release:
				return
			}
			timer := time.NewTimer(300 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-release:
			}
			p.mu.Lock()
			published := p.published
			p.mu.Unlock()
			want := 0
			if kind == "error" {
				want = 1 // Only the successful READY has reached the broker.
			}
			if published != want {
				t.Errorf("publication completed while held: got=%d want=%d", published, want)
			}
			unblock()
		}()
		p.before = func(raw []byte) {
			var frame struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(raw, &frame) == nil && frame.Type == kind {
				close(entered)
				<-release
			}
		}
	}
}

func TestLocalRegisterPhaseDelayedPublication(t *testing.T) {
	for _, kind := range []string{"normal", "ready_result", "error"} {
		t.Run(kind, func(t *testing.T) {
			f := &fakeMailboxRPC{}
			var configure func(*registrationPhasePublisher)
			if kind != "normal" {
				configure = registrationPublicationGate(t, kind)
			}
			run, nc, down, b, cfg, err := localRegisterPhaseFixture(t, f, configure)
			if err != nil {
				t.Fatalf("%v; %s", err, registrationPhaseDiagnostic(run, nc, b))
			}
			p := registrationPublicationCounter(run)
			raw := []byte(strings.Replace(validLocalRegister, `"epoch":"42"`, `"epoch":null`, 1))
			publishRegistration(t, nc, b, raw)
			expectLocalRegistrationRejection(t, run, f, p, down, cfg.ProtocolSigning, b, "req-1")
		})
	}
}

func TestLocalRegisterPhaseIncompletePublication(t *testing.T) {
	f := &fakeMailboxRPC{}
	run, nc, down, b, cfg := localRegisterFixture(t, f)
	p := registrationPhaseObserver(run)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	p.before = func([]byte) { close(entered); <-release }
	counter := registrationPublicationCounter(run)
	publishRegistration(t, nc, b, []byte(strings.Replace(validLocalRegister, `"local_register"`, `"contact_assign"`, 1)))
	select {
	case <-entered:
	case <-time.After(registrationPhaseGuard):
		t.Fatalf("publisher never entered; %s", registrationPhaseDiagnostic(run, nc, b))
	}
	if _, err := awaitRegistrationPublication(p, time.Millisecond); err == nil || err.Error() != "publication completion guard expired" {
		t.Fatalf("incomplete real publication accepted: %v", err)
	}
	diagnostic := registrationPhaseDiagnostic(run, nc, b)
	for _, anchor := range []string{"known=true", "inbound_delivered=2", "publisher_entered=2", "broker_published=1", "error_slots=1/16"} {
		if !strings.Contains(diagnostic, anchor) {
			t.Fatalf("missing phase anchor %s: %s", anchor, diagnostic)
		}
	}
	if err := registrationBusinessRPCs(f); err != nil {
		t.Fatal(err)
	}
	unblock()
	expectLocalRegistrationRejection(t, run, f, counter, down, cfg.ProtocolSigning, b, "req-1")
}

func TestLocalRegisterPhaseRejectsBusinessRPC(t *testing.T) {
	f := &fakeMailboxRPC{}
	run, nc, down, b, cfg := localRegisterFixture(t, f)
	response := signedRegisterResult(t, cfg.ProtocolSigning, b, []byte(validLocalRegister))
	f.mu.Lock()
	f.response = response
	f.mu.Unlock()
	publishRegistration(t, nc, b, []byte(validLocalRegister))
	p := registrationPhaseObserver(run)
	if _, err := awaitRegistrationPublication(p, registrationPhaseGuard); err != nil {
		t.Fatalf("%v; %s", err, registrationPhaseDiagnostic(run, nc, b))
	}
	run.mailbox.workers.Wait()
	if err := registrationBusinessRPCs(f); err == nil || !strings.Contains(err.Error(), "local_register") {
		t.Fatalf("real business RPC escaped rejection anchor: %v", err)
	}
	msg, err := down.NextMsg(time.Second)
	if err != nil || string(msg.Data) != string(response) {
		t.Fatalf("business RPC control did not complete normally: %v %v", msg, err)
	}
}

func TestLocalRegisterPhaseRejectsBadEvidence(t *testing.T) {
	f := &fakeMailboxRPC{}
	run, _, _, b, cfg := localRegisterFixture(t, f)
	ready := signedMailboxFrame(t, cfg.ProtocolSigning, "ready", b, "req-ready")
	if err := validateRegistrationReady(cfg.ProtocolSigning, b, ready); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"type", "epoch", "audience", "signature"} {
		t.Run("READY/"+field, func(t *testing.T) {
			m := decodeObject(t, ready)
			a := m["server_auth"].(map[string]any)
			switch field {
			case "type":
				m["type"] = "error"
			case "epoch":
				m["epoch"] = "43"
			case "audience":
				a["audience"].(map[string]any)["node"] = "999"
			case "signature":
				a["signature"] = strings.Repeat("A", 86)
			}
			raw := marshalRegister(t, m)
			if field != "signature" {
				raw = resignRegistration(t, cfg.ProtocolSigning, m)
			}
			if err := validateRegistrationReady(cfg.ProtocolSigning, b, raw); err == nil {
				t.Fatal("bad READY evidence accepted")
			}
		})
	}
	raw, err := run.mailbox.signer.encode(b, "req-1", &AppError{Code: CodeProtocolError, Scope: "request"})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"normal", "type", "code", "request_id", "signature", "audience"} {
		t.Run("error/"+field, func(t *testing.T) {
			m := decodeObject(t, raw)
			switch field {
			case "type":
				delete(m, "type")
			case "code":
				delete(m, "code")
			case "request_id":
				m["request_id"] = "other"
			case "signature":
				m["server_auth"].(map[string]any)["signature"] = strings.Repeat("A", 86)
			case "audience":
				m["server_auth"].(map[string]any)["audience"].(map[string]any)["epoch"] = "43"
			}
			body := marshalRegister(t, m)
			if field != "signature" {
				body = resignRegistration(t, cfg.ProtocolSigning, m)
			}
			err := validateRegistrationRejection(cfg.ProtocolSigning, b, &nats.Msg{Subject: DownSubject(b.Ingress), Data: body}, CodeProtocolError, "req-1")
			if (err == nil) != (field == "normal") {
				t.Fatalf("rejection evidence %s: %v", field, err)
			}
		})
	}
	// Explicit guard self-test; this timeout is not counted as a product red.
	t.Run("missing publication", func(t *testing.T) {
		p := &registrationPhasePublisher{completed: make(chan registrationPublication, 1)}
		if _, err := awaitRegistrationPublication(p, time.Millisecond); err == nil || err.Error() != "publication completion guard expired" {
			t.Fatalf("missing publication accepted: %v", err)
		}
		p.completed <- registrationPublication{err: fmt.Errorf("injected write failure")}
		if _, err := awaitRegistrationPublication(p, time.Second); err == nil || !strings.Contains(err.Error(), "publication failed") {
			t.Fatalf("failed publication accepted: %v", err)
		}
	})
}
