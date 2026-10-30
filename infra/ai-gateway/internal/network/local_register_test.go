package network

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/team"
	collab "github.com/hansjone/auriga/infra/gen/auriga/collab/v1"
	"github.com/nats-io/nats.go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const validLocalRegister = ` { "app_version":"1.0", "type":"local_register", "request_id":"req-1", "epoch":"42", "boot_id":"boot-1", "candidate_local":"18446744073709551615", "allocation_op_id":"Op-1:A", "allocation_fingerprint":"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", "expected_registration_revision":"18446744073709551615", "address_type":"session" } `

func marshalRegister(t *testing.T, m map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func signedRegisterResult(t *testing.T, cfg ProtocolSigningConfig, b liveBinding, request []byte) []byte {
	t.Helper()
	s, err := newErrorSigner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(s.key)
	raw, err := s.encode(b, "req-1", &AppError{Code: CodeConflict, Scope: "request"})
	if err != nil {
		t.Fatal(err)
	}
	q := decodeObject(t, request)
	auth := decodeObject(t, raw)["server_auth"].(map[string]any)
	delete(auth, "signature")
	m := map[string]any{"app_version": AppVersion, "type": "register_result", "request_id": q["request_id"], "local": q["candidate_local"], "allocation_op_id": q["allocation_op_id"], "address_type": q["address_type"], "registration_id": "reg-1", "result": "registered", "registration_revision": "18446744073709551615", "local_high_water": "18446744073709551615", "server_auth": auth}
	auth["signature"] = base64.RawURLEncoding.EncodeToString(ed25519.Sign(s.key, append([]byte("Auriga.ServerEnvelope.v1\x00"), marshalRegister(t, m)...)))
	return append(append([]byte(" \n"), marshalRegister(t, m)...), '\n')
}

func TestLocalRegisterOperationAndLegacyRejection(t *testing.T) {
	op, id, ae := mailboxOperation([]byte(validLocalRegister), false)
	if ae != nil || op != "local_register" || id != "req-1" {
		t.Fatalf("op=%q id=%q error=%v", op, id, ae)
	}
	if _, ae = DecodeUpstream([]byte(validLocalRegister)); ae == nil || ae.Code != CodeProtocolError {
		t.Fatalf("legacy mailbox accepted registration: %v", ae)
	}
}

// This uses the generated gRPC server and the existing authenticated NATS WSS
// route. IAM/collab are explicit test fakes, not real Java services.
func localRegisterFixture(t *testing.T, f *fakeMailboxRPC) (*Runtime, *nats.Conn, *nats.Subscription, liveBinding, RuntimeConfig) {
	t.Helper()
	run, nc, down, b, cfg, err := localRegisterPhaseFixture(t, f, nil)
	if err != nil {
		t.Fatalf("%v; %s", err, registrationPhaseDiagnostic(run, nc, b))
	}
	return run, nc, down, b, cfg
}

func localRegisterPhaseFixture(t *testing.T, f *fakeMailboxRPC, configure func(*registrationPhasePublisher)) (*Runtime, *nats.Conn, *nats.Subscription, liveBinding, RuntimeConfig, error) {
	t.Helper()
	h, cfg, _ := runtimeFixture(t)
	cfg.Collab = startMailboxRPC(t, f)
	run, err := StartRuntime(context.Background(), cfg)
	if err != nil {
		t.Fatalf("runtime start before READY: %v", err)
	}
	t.Cleanup(run.Close)
	// First-use transport setup is a fixture phase, not part of the 100ms
	// mailbox business deadline. No business RPC or retry is used to warm it.
	connectCtx, cancelConnect := context.WithTimeout(context.Background(), registrationPhaseGuard)
	err = awaitRegistrationRPC(connectCtx, run.collab)
	cancelConnect()
	if err != nil {
		t.Fatalf("collab connection before READY: %v", err)
	}
	// These tests exercise frame validation/publication, not the deliberately
	// short 100ms reconciliation deadline in runtimeFixture. A ready loopback
	// HTTP/2 connection can still take >100ms under CI scheduling pressure.
	// Keep the real RPC bounded by MailboxClient's supported one-second limit.
	run.mailbox.client.timeout = time.Second
	p := &registrationPhasePublisher{nc: run.mailbox.nc, completed: make(chan registrationPublication, 4)}
	if configure != nil {
		configure(p)
	}
	run.mailbox.publisher = p
	nc, err := nats.Connect(h.wsURL(), nats.Token(connectTicket), nats.NoReconnect())
	if err != nil {
		t.Fatalf("WSS connect before READY: %v", err)
	}
	t.Cleanup(nc.Close)
	p.client = nc
	b := run.redeemer.snapshot()[0]
	b.Epoch, b.Incarnation, b.ChannelNonce = 42, "inc-1", "nonce-1"
	down, err := nc.SubscribeSync(DownSubject(b.Ingress))
	if err != nil {
		t.Fatal(err)
	}
	if err = nc.Flush(); err != nil {
		t.Fatal(err)
	}
	// CONNECT confirms auth on a different socket. Confirm the dynamically
	// registered ingress SUB reached the broker before publishing READY.
	if err = run.mailbox.nc.FlushTimeout(registrationPhaseGuard); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.response = signedMailboxFrame(t, cfg.ProtocolSigning, "ready", b, "req-ready")
	f.mu.Unlock()
	if err = nc.Publish(UpSubject(b.Ingress), []byte(`{"app_version":"1.0","type":"ready","request_id":"req-ready","channel_nonce":"nonce-1","boot_id":"boot-1"}`)); err != nil {
		t.Fatal(err)
	}
	if err = nc.FlushTimeout(registrationPhaseGuard); err != nil {
		return run, nc, down, b, cfg, fmt.Errorf("READY ingress flush: %w", err)
	}
	publication, err := awaitRegistrationPublication(p, registrationPhaseGuard)
	if err != nil {
		return run, nc, down, b, cfg, err
	}
	msg, err := down.NextMsg(time.Second)
	if err != nil {
		return run, nc, down, b, cfg, err
	}
	if msg.Subject != publication.subject || !bytes.Equal(msg.Data, publication.raw) {
		return run, nc, down, b, cfg, fmt.Errorf("READY downlink differs from completed publication")
	}
	if err = validateRegistrationReady(cfg.ProtocolSigning, b, msg.Data); err != nil {
		return run, nc, down, b, cfg, err
	}
	// The completed publication proves a worker entered; Wait alone cannot.
	run.mailbox.workers.Wait()
	run.redeemer.mu.Lock()
	current, ok := run.redeemer.bindings[b.Ingress]
	run.redeemer.mu.Unlock()
	if !ok || !sameMailboxBinding(current, b) || current.Revoked || !time.Now().Before(current.NotAfter) || current.NotAfter != b.NotAfter {
		return run, nc, down, b, cfg, fmt.Errorf("READY binding not effective: got=%+v want=%+v", current, b)
	}
	// IAM's active check may refresh scheduling metadata during slow preparation.
	return run, nc, down, current, cfg, nil
}

func TestProductionLocalRegisterRoundTrip(t *testing.T) {
	f := &fakeMailboxRPC{}
	run, nc, down, b, cfg := localRegisterFixture(t, f)
	response := signedRegisterResult(t, cfg.ProtocolSigning, b, []byte(validLocalRegister))
	f.mu.Lock()
	f.response = response
	f.mu.Unlock()
	if err := nc.PublishRequest(UpSubject(b.Ingress), "auriga.net.down.foreign", []byte(validLocalRegister)); err != nil {
		t.Fatal(err)
	}
	msg, err := down.NextMsg(time.Second)
	if err != nil || msg.Subject != DownSubject(b.Ingress) || string(msg.Data) != string(response) {
		t.Fatalf("response=%v err=%v", msg, err)
	}
	run.mailbox.workers.Wait()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) != 2 || f.calls[1] != "local_register" || string(f.frames[1]) != validLocalRegister {
		t.Fatalf("calls=%v frames=%q", f.calls, f.frames)
	}
	id := f.identities[1]
	if id.TenantId != b.Tenant || id.Realm != b.Realm || id.Node != b.Node || id.Ingress != b.Ingress || id.ServerId != b.ServerID || id.ClientId != u64str(b.ClientID) || id.ClientAccount != b.Account {
		t.Fatalf("wrong trusted identity: %+v", id)
	}
	if got := run.redeemer.snapshot()[0]; got != b {
		t.Fatalf("registration mutated binding: %+v", got)
	}
}

func publishRegistration(t *testing.T, nc *nats.Conn, b liveBinding, raw []byte) {
	t.Helper()
	msg := &nats.Msg{Subject: UpSubject(b.Ingress), Reply: "auriga.net.down.foreign", Data: raw, Header: nats.Header{}}
	msg.Header.Set("Auriga-Tenant-Id", "forged")
	msg.Header.Set("Node", "999")
	if err := nc.PublishMsg(msg); err != nil {
		t.Fatal(err)
	}
	if err := nc.FlushTimeout(registrationPhaseGuard); err != nil {
		t.Fatalf("registration ingress flush: %v; connection=%v last_error=%v", err, nc.Status(), nc.LastError())
	}
}

func registrationPublicationCounter(run *Runtime) *countingPublisher {
	p := &countingPublisher{delegate: run.mailbox.publisher, counts: make(map[string]int)}
	run.mailbox.publisher = p
	return p
}

func assertRegistrationCounts(t *testing.T, run *Runtime, f *fakeMailboxRPC, p *countingPublisher, b liveBinding, calls, publications int) {
	t.Helper()
	run.mailbox.workers.Wait()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) != calls {
		t.Fatalf("RPC count=%d want=%d operations=%v", len(f.calls), calls, f.calls)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.counts) != min(publications, 1) || p.counts[DownSubject(b.Ingress)] != publications {
		t.Fatalf("publication subjects/counts=%v want=%s:%d", p.counts, DownSubject(b.Ingress), publications)
	}
}

func expectRegistrationCode(t *testing.T, run *Runtime, sub *nats.Subscription, cfg ProtocolSigningConfig, b liveBinding, code string) {
	t.Helper()
	expectRegistrationError(t, run, sub, cfg, b, code, "req-1")
}

func expectRegistrationError(t *testing.T, run *Runtime, sub *nats.Subscription, cfg ProtocolSigningConfig, b liveBinding, code string, requestID any) {
	t.Helper()
	p := registrationPhaseObserver(run)
	publication, err := awaitRegistrationPublication(p, registrationPhaseGuard)
	if err != nil {
		t.Fatalf("%v; %s", err, registrationPhaseDiagnostic(run, p.nc, b))
	}
	expectRegistrationDownlink(t, run, sub, cfg, b, code, requestID, publication)
}

func expectRegistrationDownlink(t *testing.T, run *Runtime, sub *nats.Subscription, cfg ProtocolSigningConfig, b liveBinding, code string, requestID any, publication registrationPublication) {
	t.Helper()
	p := registrationPhaseObserver(run)
	// Completion proves entry into the real publisher; unrelated RPCs may be held.
	msg, err := sub.NextMsg(time.Second)
	if err != nil {
		t.Fatalf("completed publication missing downlink: %v; %s", err, registrationPhaseDiagnostic(run, p.nc, b))
	}
	if msg.Subject != publication.subject || !bytes.Equal(msg.Data, publication.raw) {
		t.Fatal("downlink differs from completed publication")
	}
	if err := validateRegistrationRejection(cfg, b, msg, code, requestID); err != nil {
		t.Fatalf("%v; %s", err, registrationPhaseDiagnostic(run, p.nc, b))
	}
}

func TestProductionLocalRegisterTypesAndErrorBytes(t *testing.T) {
	for _, kind := range []string{"session", "subagent", "error"} {
		t.Run(kind, func(t *testing.T) {
			f := &fakeMailboxRPC{}
			run, nc, down, b, cfg := localRegisterFixture(t, f)
			p := registrationPublicationCounter(run)
			raw := []byte(strings.Replace(validLocalRegister, `"session"`, `"subagent"`, 1))
			if kind == "session" {
				raw = []byte(validLocalRegister)
			}
			response := signedRegisterResult(t, cfg.ProtocolSigning, b, raw)
			if kind == "subagent" {
				// An existing registration is the same immutable typed receipt.
				m := decodeObject(t, response)
				m["result"] = "existing"
				response = resignRegistration(t, cfg.ProtocolSigning, m)
			}
			if kind == "error" {
				var err error
				response, err = run.mailbox.signer.encode(b, "req-1", &AppError{Code: CodeConflict, Scope: "request"})
				if err != nil {
					t.Fatal(err)
				}
			}
			verifyErrorSignature(t, cfg.ProtocolSigning, response)
			f.mu.Lock()
			f.response = response
			f.mu.Unlock()
			publishRegistration(t, nc, b, raw)
			msg, err := down.NextMsg(time.Second)
			if err != nil || string(msg.Data) != string(response) {
				t.Fatalf("bytes changed: %v %v", msg, err)
			}
			assertRegistrationCounts(t, run, f, p, b, 2, 1)
			f.mu.Lock()
			if string(f.frames[1]) != string(raw) || f.identities[1].TenantId != b.Tenant || f.identities[1].Node != b.Node {
				t.Error("request bytes/identity changed")
			}
			f.mu.Unlock()
		})
	}
}

func resignRegistration(t *testing.T, cfg ProtocolSigningConfig, m map[string]any) []byte {
	t.Helper()
	s, err := newErrorSigner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(s.key)
	a := m["server_auth"].(map[string]any)
	delete(a, "signature")
	a["signature"] = base64.RawURLEncoding.EncodeToString(ed25519.Sign(s.key, append([]byte("Auriga.ServerEnvelope.v1\x00"), marshalRegister(t, m)...)))
	return marshalRegister(t, m)
}

func TestProductionLocalRegisterRejectsRequest(t *testing.T) {
	cases := map[string][]byte{
		"invalid UTF8":         []byte(strings.Replace(validLocalRegister, "boot-1", "boot-\xff", 1)),
		"duplicate":            []byte(strings.Replace(validLocalRegister, `"epoch":"42"`, `"epoch":"41","epoch":"42"`, 1)),
		"escaped duplicate":    []byte(strings.Replace(validLocalRegister, `"epoch":"42"`, `"ep\u006fch":"41","epoch":"42"`, 1)),
		"unknown action":       []byte(strings.Replace(validLocalRegister, `"local_register"`, `"contact_assign"`, 1)),
		"optional decimal":     registrationExtension(`{"number":1.0}`),
		"optional exponent":    registrationExtension(`{"number":1e0}`),
		"case variant":         []byte(strings.Replace(validLocalRegister, `"boot_id"`, `"Boot_id"`, 1)),
		"trailing object":      []byte(validLocalRegister + `{}`),
		"trailing token":       []byte(validLocalRegister + `true`),
		"address type unknown": []byte(strings.Replace(validLocalRegister, `"session"`, `"system"`, 1)),
		"missing address type": []byte(strings.Replace(validLocalRegister, `, "address_type":"session"`, ``, 1)),
		"fingerprint":          []byte(strings.Replace(validLocalRegister, "sha256:", "sha512:", 1)),
		"surrogate":            []byte(strings.Replace(validLocalRegister, "boot-1", `boot-\ud800`, 1)),
	}
	for _, field := range strings.Fields(localRegisterFields) {
		if field == "request_id" { // Missing request ID intentionally has null association.
			continue
		}
		m := decodeObject(t, []byte(validLocalRegister))
		delete(m, field)
		cases["missing "+field] = marshalRegister(t, m)
	}
	for _, field := range []string{"epoch", "candidate_local", "expected_registration_revision"} {
		for name, value := range map[string]any{"number": 42, "null": nil, "overflow": "18446744073709551616", "leading zero": "042", "negative": "-1", "exponent": "1e2", "space": " 42"} {
			m := decodeObject(t, []byte(validLocalRegister))
			m[field] = value
			cases[field+" "+name] = marshalRegister(t, m)
		}
		if field != "expected_registration_revision" {
			m := decodeObject(t, []byte(validLocalRegister))
			m[field] = "0"
			cases[field+" zero"] = marshalRegister(t, m)
		}
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			f := &fakeMailboxRPC{}
			run, nc, down, b, cfg := localRegisterFixture(t, f)
			p := registrationPublicationCounter(run)
			publishRegistration(t, nc, b, raw)
			var requestID any = "req-1"
			if name == "trailing object" || name == "trailing token" {
				requestID = nil // A malformed envelope has no trustworthy request ID.
			}
			expectLocalRegistrationRejection(t, run, f, p, down, cfg.ProtocolSigning, b, requestID)
		})
	}
}

func TestProductionLocalRegisterRejectsOldOrUnreadyEpoch(t *testing.T) {
	for _, epoch := range []uint64{0, 41, 43} {
		t.Run(u64str(epoch), func(t *testing.T) {
			f := &fakeMailboxRPC{}
			run, nc, down, b, cfg := localRegisterFixture(t, f)
			p := registrationPublicationCounter(run)
			if epoch == 0 {
				run.redeemer.mu.Lock()
				current := run.redeemer.bindings[b.Ingress]
				current.Epoch = 0
				run.redeemer.bindings[b.Ingress] = current
				run.redeemer.mu.Unlock()
				b.Epoch = 0
			}
			raw := []byte(validLocalRegister)
			if epoch != 0 {
				raw = []byte(strings.Replace(validLocalRegister, `"epoch":"42"`, `"epoch":"`+u64str(epoch)+`"`, 1))
			}
			publishRegistration(t, nc, b, raw)
			expectRegistrationCode(t, run, down, cfg.ProtocolSigning, b, CodeStaleEpoch)
			assertRegistrationCounts(t, run, f, p, b, 1, 1)
		})
	}
}

func TestProductionLocalRegisterRejectsResponse(t *testing.T) {
	changes := map[string]func(map[string]any){
		"wrong frame":         func(m map[string]any) { m["type"] = "assign_result" },
		"wrong op":            func(m map[string]any) { m["allocation_op_id"] = "other-op" },
		"wrong local":         func(m map[string]any) { m["local"] = "1" },
		"wrong address type":  func(m map[string]any) { m["address_type"] = "subagent" },
		"wrong request":       func(m map[string]any) { m["request_id"] = "other-request" },
		"numeric local":       func(m map[string]any) { m["local"] = 42 },
		"zero revision":       func(m map[string]any) { m["registration_revision"] = "0" },
		"overflow high water": func(m map[string]any) { m["local_high_water"] = "18446744073709551616" },
		"unknown result":      func(m map[string]any) { m["result"] = "created" },
		"wrong major":         func(m map[string]any) { m["app_version"] = "2.0" },
		"bad minor format":    func(m map[string]any) { m["app_version"] = "1.01" },
		"unknown category":    func(m map[string]any) { m["address_type"] = "system" },
	}
	for _, field := range strings.Fields(registerResultFields) {
		key := field
		changes["missing "+key] = func(m map[string]any) { delete(m, key) }
	}
	for name, mutate := range map[string]func(map[string]any){
		"missing signature": func(a map[string]any) { delete(a, "signature") },
		"invalid signature": func(a map[string]any) { a["signature"] = "invalid" },
		"padded signature":  func(a map[string]any) { a["signature"] = a["signature"].(string) + "==" },
		"newline signature": func(a map[string]any) { a["signature"] = a["signature"].(string) + "\n" },
		"unsigned skeleton": func(a map[string]any) { a["kid"] = "stub-unsigned" },
		"algorithm":         func(a map[string]any) { a["alg"] = "RS256" },
		"purpose":           func(a map[string]any) { a["purpose"] = "business_delivery" },
		"nonce":             func(a map[string]any) { a["channel_nonce"] = "other-nonce" },
		"auth request":      func(a map[string]any) { a["request_id"] = "other-request" },
		"null auth request": func(a map[string]any) { a["request_id"] = nil },
		"null purpose":      func(a map[string]any) { a["purpose"] = nil },
		"auth version":      func(a map[string]any) { a["version"] = 2 },
		"missing auth time": func(a map[string]any) { delete(a, "issued_at") },
		"bad auth time":     func(a map[string]any) { a["not_after"] = "tomorrow" },
	} {
		change := mutate
		changes[name] = func(m map[string]any) { change(m["server_auth"].(map[string]any)) }
	}
	for _, field := range []string{"realm", "node", "incarnation", "epoch"} {
		key := field
		changes["wrong audience "+key] = func(m map[string]any) { m["server_auth"].(map[string]any)["audience"].(map[string]any)[key] = "123" }
		changes["missing audience "+key] = func(m map[string]any) { delete(m["server_auth"].(map[string]any)["audience"].(map[string]any), key) }
	}
	changes["numeric audience epoch"] = func(m map[string]any) {
		m["server_auth"].(map[string]any)["audience"].(map[string]any)["epoch"] = 42
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			f := &fakeMailboxRPC{}
			run, nc, down, b, cfg := localRegisterFixture(t, f)
			p := registrationPublicationCounter(run)
			m := decodeObject(t, signedRegisterResult(t, cfg.ProtocolSigning, b, []byte(validLocalRegister)))
			change(m)
			f.mu.Lock()
			f.response = marshalRegister(t, m)
			f.mu.Unlock()
			publishRegistration(t, nc, b, []byte(validLocalRegister))
			expectRegistrationCode(t, run, down, cfg.ProtocolSigning, b, CodeProtocolError)
			assertRegistrationCounts(t, run, f, p, b, 2, 1)
		})
	}
}

func TestProductionLocalRegisterRejectsRawResponse(t *testing.T) {
	for _, name := range []string{"UTF8", "duplicate top", "duplicate auth", "duplicate audience", "trailing", "oversize", "empty"} {
		t.Run(name, func(t *testing.T) {
			f := &fakeMailboxRPC{}
			run, nc, down, b, cfg := localRegisterFixture(t, f)
			p := registrationPublicationCounter(run)
			raw := string(signedRegisterResult(t, cfg.ProtocolSigning, b, []byte(validLocalRegister)))
			switch name {
			case "UTF8":
				raw = strings.Replace(raw, "reg-1", "reg-\xff", 1)
			case "duplicate top":
				raw = strings.Replace(raw, `"local":`, `"local":"1","local":`, 1)
			case "duplicate auth":
				raw = strings.Replace(raw, `"channel_nonce":`, `"channel_nonce":"wrong","channel_nonce":`, 1)
			case "duplicate audience":
				raw = strings.Replace(raw, `"node":`, `"n\u006fde":"999","node":`, 1)
			case "trailing":
				raw += `{}`
			case "oversize":
				raw += strings.Repeat(" ", EncodingMaxPayloadBytes)
			case "empty":
				raw = ""
			}
			f.mu.Lock()
			f.response = []byte(raw)
			f.mu.Unlock()
			publishRegistration(t, nc, b, []byte(validLocalRegister))
			code := CodeProtocolError
			if name == "oversize" { // gRPC rejects before a body reaches the validator.
				code = "network.overloaded"
			}
			expectRegistrationCode(t, run, down, cfg.ProtocolSigning, b, code)
			assertRegistrationCounts(t, run, f, p, b, 2, 1)
		})
	}
}

func TestProductionLocalRegisterRPCFailure(t *testing.T) {
	for _, tc := range []struct {
		name    string
		block   bool
		failure error
		code    string
	}{
		{"timeout", true, nil, "network.overloaded"},
		{"UNIMPLEMENTED", false, status.Error(codes.Unimplemented, "private backend credential"), "network.overloaded"},
		{"denied", false, status.Error(codes.PermissionDenied, "private backend credential"), "network.unauthorized"},
		{"conflict", false, status.Error(codes.Aborted, CodeConflict), CodeConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeMailboxRPC{}
			run, nc, down, b, cfg := localRegisterFixture(t, f)
			p := registrationPublicationCounter(run)
			f.mu.Lock()
			f.block, f.failure = tc.block, tc.failure
			f.mu.Unlock()
			publishRegistration(t, nc, b, []byte(validLocalRegister))
			expectRegistrationCode(t, run, down, cfg.ProtocolSigning, b, tc.code)
			assertRegistrationCounts(t, run, f, p, b, 2, 1)
		})
	}
}

func TestLocalRegisterGeneratedUnimplementedServer(t *testing.T) {
	conn, err := team.DialRPC(startMailboxRPC(t, &collab.UnimplementedNetworkMailboxServiceServer{}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	client, err := NewMailboxClient(conn, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	raw, ae := client.Call(context.Background(), liveBinding{Tenant: testTenant}, "local_register", []byte(validLocalRegister))
	if len(raw) != 0 || ae == nil || ae.Code != "network.overloaded" || !ae.Retryable {
		t.Fatalf("UNIMPLEMENTED became success: %s %v", raw, ae)
	}
}

func TestProductionLocalRegisterBindingChange(t *testing.T) {
	for _, outcome := range []string{"success", "signed error", "RPC error", "invalid response"} {
		for _, change := range []string{"revoked", "expired", "new READY epoch", "new nonce", "new incarnation", "forgotten"} {
			t.Run(outcome+"/"+change, func(t *testing.T) {
				f := &fakeMailboxRPC{}
				run, nc, down, b, cfg := localRegisterFixture(t, f)
				p := registrationPublicationCounter(run)
				response := signedRegisterResult(t, cfg.ProtocolSigning, b, []byte(validLocalRegister))
				if outcome == "signed error" {
					var err error
					response, err = run.mailbox.signer.encode(b, "req-1", &AppError{Code: CodeConflict, Scope: "request"})
					if err != nil {
						t.Fatal(err)
					}
				}
				if outcome == "invalid response" {
					response = []byte(`{}`)
				}
				f.mu.Lock()
				f.response, f.entered, f.release = response, make(chan struct{}, 1), make(chan struct{})
				if outcome == "RPC error" {
					f.failure = status.Error(codes.Internal, "private backend credential")
				}
				f.mu.Unlock()
				publishRegistration(t, nc, b, []byte(validLocalRegister))
				select {
				case <-f.entered:
				case <-time.After(time.Second):
					t.Fatal("LocalRegister did not enter RPC")
				}
				guard := run.redeemer.connectionGuard(b.Ingress)
				guard.Lock()
				run.redeemer.mu.Lock()
				current := run.redeemer.bindings[b.Ingress]
				switch change {
				case "revoked":
					current.Revoked = true
				case "expired":
					current.NotAfter = time.Now().Add(10 * time.Millisecond)
				case "new READY epoch":
					current.Epoch++
				case "new nonce":
					current.ChannelNonce = "nonce-new"
				case "new incarnation":
					current.Incarnation = "inc-new"
				}
				run.redeemer.bindings[b.Ingress] = current
				run.redeemer.mu.Unlock()
				guard.Unlock()
				if change == "expired" {
					<-time.After(time.Until(current.NotAfter))
				}
				if change == "forgotten" {
					run.redeemer.forget(b.Ingress)
				}
				close(f.release)
				publications := 0
				if change == "revoked" {
					// The original result/error is suppressed. Only the existing,
					// freshly signed revocation diagnostic may reach this binding.
					expectRegistrationCode(t, run, down, cfg.ProtocolSigning, b, "network.revoked")
					publications = 1
				}
				assertRegistrationCounts(t, run, f, p, b, 2, publications)
				if msg, err := down.NextMsg(20 * time.Millisecond); err != nats.ErrTimeout {
					t.Fatalf("late response=%v err=%v", msg, err)
				}
			})
		}
	}
}

func TestLocalRegisterSizeBoundaryAndClientRejection(t *testing.T) {
	f := &fakeMailboxRPC{response: []byte(`{"opaque":"unchanged"}`)}
	conn, err := team.DialRPC(startMailboxRPC(t, f))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	client, err := NewMailboxClient(conn, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(validLocalRegister + strings.Repeat(" ", EncodingMaxPayloadBytes-len(validLocalRegister)))
	if got, ae := client.Call(context.Background(), liveBinding{Tenant: testTenant}, "local_register", raw); ae != nil || string(got) != string(f.response) {
		t.Fatalf("64KiB rejected: %s %v", got, ae)
	}
	for _, invalid := range [][]byte{append(append([]byte{}, raw...), ' '), []byte(`{}`), []byte(strings.Replace(validLocalRegister, `"request_id":"req-1"`, `"request_id":null`, 1))} {
		if got, ae := client.Call(context.Background(), liveBinding{Tenant: testTenant}, "local_register", invalid); len(got) != 0 || ae == nil || ae.Code != CodeProtocolError {
			t.Fatalf("invalid input succeeded: %s %v", got, ae)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) != 1 || string(f.frames[0]) != string(raw) {
		t.Fatalf("rejected input reached RPC: %v", f.calls)
	}
}

func TestProductionLocalRegisterOversizeIsSilent(t *testing.T) {
	f := &fakeMailboxRPC{}
	run, nc, down, b, _ := localRegisterFixture(t, f)
	p := registrationPublicationCounter(run)
	raw := []byte(validLocalRegister + strings.Repeat(" ", EncodingMaxPayloadBytes))
	// The authenticated connection's broker limit rejects this before routing.
	if err := nc.Publish(UpSubject(b.Ingress), raw); err != nats.ErrMaxPayload {
		t.Fatalf("broker size rejection=%v", err)
	}
	// Direct callback exercises the router's independent bound too.
	run.mailbox.receive(&nats.Msg{Subject: UpSubject(b.Ingress), Data: raw})
	assertRegistrationCounts(t, run, f, p, b, 1, 0)
	if msg, err := down.NextMsg(20 * time.Millisecond); err != nats.ErrTimeout {
		t.Fatalf("oversize produced response=%v err=%v", msg, err)
	}
}

func TestProductionLocalRegisterSerialAdmission(t *testing.T) {
	f := &fakeMailboxRPC{}
	run, nc, down, b, cfg := localRegisterFixture(t, f)
	p := registrationPublicationCounter(run)
	response := signedRegisterResult(t, cfg.ProtocolSigning, b, []byte(validLocalRegister))
	f.mu.Lock()
	f.response, f.entered, f.release = response, make(chan struct{}, 1), make(chan struct{})
	f.mu.Unlock()
	publishRegistration(t, nc, b, []byte(validLocalRegister))
	select {
	case <-f.entered:
	case <-time.After(time.Second):
		t.Fatal("LocalRegister did not enter RPC")
	}
	publishRegistration(t, nc, b, []byte(validLocalRegister))
	expectRegistrationCode(t, run, down, cfg.ProtocolSigning, b, "network.overloaded")
	close(f.release)
	msg, err := down.NextMsg(time.Second)
	if err != nil || string(msg.Data) != string(response) {
		t.Fatalf("admitted response=%v err=%v", msg, err)
	}
	assertRegistrationCounts(t, run, f, p, b, 2, 2)
}

func TestProductionLocalRegisterMaximumEpoch(t *testing.T) {
	f := &fakeMailboxRPC{}
	run, nc, down, b, cfg := localRegisterFixture(t, f)
	b.Epoch = ^uint64(0)
	f.mu.Lock()
	f.response = signedMailboxFrame(t, cfg.ProtocolSigning, "ready", b, "req-ready")
	f.mu.Unlock()
	if err := nc.Publish(UpSubject(b.Ingress), []byte(`{"app_version":"1.0","type":"ready","request_id":"req-ready","channel_nonce":"nonce-1","boot_id":"boot-1"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := down.NextMsg(time.Second); err != nil {
		t.Fatal(err)
	}
	run.mailbox.workers.Wait()
	p := registrationPublicationCounter(run)
	raw := []byte(strings.Replace(validLocalRegister, `"epoch":"42"`, `"epoch":"18446744073709551615"`, 1))
	response := signedRegisterResult(t, cfg.ProtocolSigning, b, raw)
	f.mu.Lock()
	f.response = response
	f.mu.Unlock()
	publishRegistration(t, nc, b, raw)
	msg, err := down.NextMsg(time.Second)
	if err != nil || string(msg.Data) != string(response) {
		t.Fatalf("max uint64 changed: %v %v", msg, err)
	}
	assertRegistrationCounts(t, run, f, p, b, 3, 1)
}

func TestProductionLocalRegisterPublicationGuard(t *testing.T) {
	for _, outcome := range []string{"success", "RPC error"} {
		t.Run(outcome, func(t *testing.T) {
			f := &fakeMailboxRPC{}
			run, nc, _, b, cfg := localRegisterFixture(t, f)
			f.mu.Lock()
			f.response = signedRegisterResult(t, cfg.ProtocolSigning, b, []byte(validLocalRegister))
			if outcome == "RPC error" {
				f.failure = status.Error(codes.Unavailable, "private backend detail")
			}
			f.mu.Unlock()
			p := &blockedPublisher{entered: make(chan struct{}), release: make(chan struct{})}
			run.mailbox.publisher = p
			defer close(p.release)
			publishRegistration(t, nc, b, []byte(validLocalRegister))
			select {
			case <-p.entered:
			case <-time.After(time.Second):
				t.Fatal("publication did not start")
			}
			guard := run.redeemer.connectionGuard(b.Ingress)
			if guard.TryLock() {
				guard.Unlock()
				t.Fatal("publication does not hold the connection guard")
			}
			done := make(chan struct{})
			go func() { run.redeemer.snapshot(); close(done) }()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("publication holds the global binding lock")
			}
		})
	}
}

func TestProductionLocalRegisterConsumerOwnsCryptographicVerification(t *testing.T) {
	f := &fakeMailboxRPC{}
	run, nc, down, b, cfg := localRegisterFixture(t, f)
	p := registrationPublicationCounter(run)
	m := decodeObject(t, signedRegisterResult(t, cfg.ProtocolSigning, b, []byte(validLocalRegister)))
	a := m["server_auth"].(map[string]any)
	// A well-formed but incorrect signature cannot be checked by this gateway:
	// it has no collab trust roots. The consumer must reject it cryptographically.
	a["signature"] = base64.RawURLEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
	response := marshalRegister(t, m)
	f.mu.Lock()
	f.response = response
	f.mu.Unlock()
	publishRegistration(t, nc, b, []byte(validLocalRegister))
	msg, err := down.NextMsg(time.Second)
	if err != nil || string(msg.Data) != string(response) {
		t.Fatalf("gateway altered the signed bytes: %v %v", msg, err)
	}
	s, err := newErrorSigner(cfg.ProtocolSigning)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(s.key)
	delete(a, "signature")
	if ed25519.Verify(s.key.Public().(ed25519.PublicKey), append([]byte("Auriga.ServerEnvelope.v1\x00"), marshalRegister(t, m)...), make([]byte, ed25519.SignatureSize)) {
		t.Fatal("incorrect signature passed consumer verification")
	}
	assertRegistrationCounts(t, run, f, p, b, 2, 1)
}

func TestProductionLocalRegisterConsumerExtensions(t *testing.T) {
	for _, variant := range []string{"minor version", "maximum minor", "upstream extensions", "result extensions", "auth extensions", "audience extensions", "error extensions", "case shadows", "empty required features"} {
		t.Run(variant, func(t *testing.T) {
			f := &fakeMailboxRPC{}
			run, nc, down, b, cfg := localRegisterFixture(t, f)
			p := registrationPublicationCounter(run)
			q := decodeObject(t, []byte(validLocalRegister))
			ext := map[string]any{"objects": []any{nil, true, map[string]any{"text": "汉😀", "number": -9007199254740991}}, "list": []any{1, 2}}
			switch variant {
			case "minor version":
				q["app_version"] = "1.1"
			case "maximum minor":
				q["app_version"] = "1.9999"
			case "upstream extensions":
				q["future"], q["tenant"], q["node"] = ext, "forged", "999"
				q[""], q["app_version type"] = ext, ext
			case "case shadows":
				q["TYPE"], q["App_version"], q["EPOCH"] = "send", "2.0", "0"
			case "empty required features":
				q["required_features"] = []any{}
			}
			raw := marshalRegister(t, q)
			m := decodeObject(t, signedRegisterResult(t, cfg.ProtocolSigning, b, raw))
			switch variant {
			case "minor version":
				m["app_version"], m["future"] = "1.1", ext
			case "maximum minor":
				m["app_version"] = "1.9999"
			case "result extensions":
				m["future"], m["epoch"], m["boot_id"], m["allocation_fingerprint"] = ext, "optional", "optional", "optional"
			case "auth extensions":
				m["server_auth"].(map[string]any)["future"] = ext
			case "audience extensions":
				m["server_auth"].(map[string]any)["audience"].(map[string]any)["tenant"] = ext
			case "error extensions":
				errorRaw, err := run.mailbox.signer.encode(b, "req-1", &AppError{Code: CodeConflict, Scope: "request"})
				if err != nil {
					t.Fatal(err)
				}
				m = decodeObject(t, errorRaw)
				m["app_version"], m["future"] = "1.1", ext
				m["related"] = map[string]any{"recipient_id": "r-1", "future": ext}
				m["server_auth"].(map[string]any)["future"] = ext
				m["server_auth"].(map[string]any)["audience"].(map[string]any)["future"] = ext
			case "case shadows":
				m["TYPE"], m["App_version"], m["Request_id"] = "assign_result", "2.0", "other"
				a := m["server_auth"].(map[string]any)
				a["Purpose"], a["Channel_nonce"], a["Request_id"] = "management_state", "other", "other"
				a["audience"].(map[string]any)["Epoch"] = "0"
			case "empty required features":
				m["required_features"] = []any{}
			}
			response := resignRegistration(t, cfg.ProtocolSigning, m)
			if variant == "case shadows" {
				// Force aliases AFTER known names at every object level. A struct
				// decoder would overwrite known fields; the JCS signature is intact.
				raw = reverseRegistrationKeys(t, q)
				response = reverseRegistrationKeys(t, decodeObject(t, response))
			}
			verifyErrorSignature(t, cfg.ProtocolSigning, response)
			f.mu.Lock()
			f.response = response
			f.mu.Unlock()
			publishRegistration(t, nc, b, raw)
			msg, err := down.NextMsg(time.Second)
			if err != nil || string(msg.Data) != string(response) {
				t.Fatalf("compatible extension changed/rejected: %v %v", msg, err)
			}
			verifyErrorSignature(t, cfg.ProtocolSigning, msg.Data)
			assertRegistrationCounts(t, run, f, p, b, 2, 1)
			f.mu.Lock()
			if string(f.frames[1]) != string(raw) || f.identities[1].TenantId != b.Tenant || f.identities[1].Node != b.Node {
				t.Error("extension altered bytes/trusted identity")
			}
			f.mu.Unlock()
		})
	}
}

func reverseRegistrationKeys(t *testing.T, value any) []byte {
	t.Helper()
	switch v := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Sort(sort.Reverse(sort.StringSlice(keys)))
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			quoted, err := json.Marshal(key)
			if err != nil {
				t.Fatal(err)
			}
			parts = append(parts, string(quoted)+":"+string(reverseRegistrationKeys(t, v[key])))
		}
		return []byte("{" + strings.Join(parts, ",") + "}")
	case []any:
		parts := make([]string, len(v))
		for i, item := range v {
			parts[i] = string(reverseRegistrationKeys(t, item))
		}
		return []byte("[" + strings.Join(parts, ",") + "]")
	default:
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
}

func registrationExtension(extension string) []byte {
	base := strings.TrimSpace(validLocalRegister)
	return []byte(base[:len(base)-1] + `,"future":` + extension + `}`)
}

func readRegistrationFixture(t *testing.T, name string, out any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "contracts", "network", "fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatal(err)
	}
}

func registrationMembers(n int) string {
	members := make([]string, n)
	for i := range members {
		members[i] = `"k` + u64str(uint64(i)) + `":0`
	}
	return "{" + strings.Join(members, ",") + "}"
}

func registrationValueTree(branches, members, lastMembers int) string {
	parts := make([]string, branches)
	for i := range parts {
		n := members
		if i == branches-1 {
			n = lastMembers
		}
		parts[i] = registrationMembers(n)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func TestLocalRegisterSharedRawFixtures(t *testing.T) {
	type rawCase struct {
		Name         string
		Base         string
		Replace      []string
		Append       string
		RepeatAppend int
		Nest         int
		Members      int
	}
	var rawCases struct {
		Cases  []rawCase
		Accept []rawCase
	}
	var frames struct {
		Frames map[string]struct{ Value json.RawMessage }
	}
	readRegistrationFixture(t, "raw-cases.json", &rawCases)
	readRegistrationFixture(t, "frames.json", &frames)
	check := func(c rawCase, accept bool) {
		t.Run(c.Name, func(t *testing.T) {
			var raw string
			switch {
			case c.Nest != 0:
				raw = strings.Repeat("[", c.Nest) + strings.Repeat("]", c.Nest)
			case c.Members != 0:
				raw = registrationMembers(c.Members)
			default:
				var compact bytes.Buffer
				if err := json.Compact(&compact, frames.Frames[c.Base].Value); err != nil {
					t.Fatal(err)
				}
				raw = compact.String()
				if len(c.Replace) != 0 {
					if !strings.Contains(raw, c.Replace[0]) {
						t.Fatalf("shared fixture replacement not found: %q", c.Replace[0])
					}
					raw = strings.Replace(raw, c.Replace[0], c.Replace[1], 1)
				}
				raw += strings.Repeat(c.Append, max(c.RepeatAppend, 1))
			}
			err := validateRegistrationJSON([]byte(raw))
			if accept && err != nil || !accept && err != errUnauthorized {
				t.Fatalf("accepted=%v want=%v error=%v", err == nil, accept, err)
			}
		})
	}
	for _, c := range rawCases.Cases {
		check(c, false)
	}
	for _, c := range rawCases.Accept {
		check(c, true)
	}
	var boundaries struct {
		Cases []struct {
			Name        string
			Kind        string
			Unit        string
			Repeat      int
			Suffix      string
			Containers  int
			Branches    int
			Members     int
			LastMembers int
			Accept      bool
		}
	}
	readRegistrationFixture(t, "raw-boundaries.json", &boundaries)
	for _, c := range boundaries.Cases {
		t.Run(c.Name, func(t *testing.T) {
			var raw string
			switch c.Kind {
			case "string", "key":
				s, err := json.Marshal(strings.Repeat(c.Unit, c.Repeat) + c.Suffix)
				if err != nil {
					t.Fatal(err)
				}
				raw = string(s)
				if c.Kind == "key" {
					raw = "{" + raw + ":0}"
				}
			case "nestedScalar":
				raw = strings.Repeat("[", c.Containers) + "0" + strings.Repeat("]", c.Containers)
			case "nestedEmpty":
				raw = strings.Repeat("[", c.Containers) + strings.Repeat("]", c.Containers)
			case "valueTree":
				raw = registrationValueTree(c.Branches, c.Members, c.LastMembers)
			default:
				t.Fatal("unhandled shared boundary kind", c.Kind)
			}
			err := validateRegistrationJSON([]byte(raw))
			if c.Accept && err != nil || !c.Accept && err != errUnauthorized {
				t.Fatalf("accepted=%v want=%v error=%v", err == nil, c.Accept, err)
			}
		})
	}
}

func TestProductionLocalRegisterRejectsUnknownRaw(t *testing.T) {
	for name, extension := range map[string]string{
		"nested duplicate":   `{"nested":[{"k":1,"k":2}]}`,
		"escaped duplicate":  `{"nested":{"key":1,"k\u0065y":2}}`,
		"high surrogate":     `{"text":"\ud800"}`,
		"low surrogate":      `{"text":"\udc00"}`,
		"bad surrogate pair": `{"text":"\ud800\u0041"}`,
		"raw control":        "{\"text\":\"a\x01b\"}",
		"invalid UTF8":       "{\"text\":\"a\xffb\"}",
		"unsafe positive":    `{"number":9007199254740992}`,
		"unsafe negative":    `{"number":-9007199254740992}`,
		"decimal":            `{"number":1.0}`,
		"fraction":           `{"number":0.5}`,
		"exponent":           `{"number":1e0}`,
		"NaN":                `{"number":NaN}`,
		"depth":              strings.Repeat("[", 15) + "0" + strings.Repeat("]", 15),
		"object members":     registrationMembers(129),
		"array items":        "[" + strings.Repeat("0,", 256) + "0]",
	} {
		t.Run(name, func(t *testing.T) {
			f := &fakeMailboxRPC{}
			run, nc, down, b, cfg := localRegisterFixture(t, f)
			p := registrationPublicationCounter(run)
			raw := registrationExtension(extension)
			publishRegistration(t, nc, b, raw)
			var requestID any = "req-1"
			if name == "raw control" || name == "NaN" {
				requestID = nil
			}
			expectRegistrationError(t, run, down, cfg.ProtocolSigning, b, CodeProtocolError, requestID)
			assertRegistrationCounts(t, run, f, p, b, 1, 1)
		})
	}
}

func TestProductionLocalRegisterRejectsVersionAndRequirements(t *testing.T) {
	for _, direction := range []string{"request", "response"} {
		cases := map[string]any{
			"wrong major": "2.0", "zero major": "0.1", "leading major": "01.1", "leading minor": "1.01", "too long minor": "1.10000",
			"missing minor": "1.", "no minor": "1", "patch version": "1.1.0", "negative minor": "1.-1", "space": "1.1 ", "numeric": 1, "null": nil,
		}
		for name, version := range cases {
			t.Run(direction+"/"+name, func(t *testing.T) {
				f := &fakeMailboxRPC{}
				run, nc, down, b, cfg := localRegisterFixture(t, f)
				p := registrationPublicationCounter(run)
				q := decodeObject(t, []byte(validLocalRegister))
				m := decodeObject(t, signedRegisterResult(t, cfg.ProtocolSigning, b, []byte(validLocalRegister)))
				calls := 2
				if direction == "request" {
					q["app_version"] = version
					calls = 1
				} else {
					m["app_version"] = version
				}
				f.mu.Lock()
				f.response = resignRegistration(t, cfg.ProtocolSigning, m)
				f.mu.Unlock()
				publishRegistration(t, nc, b, marshalRegister(t, q))
				expectRegistrationCode(t, run, down, cfg.ProtocolSigning, b, CodeProtocolError)
				assertRegistrationCounts(t, run, f, p, b, calls, 1)
			})
		}
		for _, required := range []any{[]any{"unknown_feature_v1"}, "unknown_feature_v1", nil} {
			t.Run(direction+"/required-feature-"+string(reverseRegistrationKeys(t, required)), func(t *testing.T) {
				f := &fakeMailboxRPC{}
				run, nc, down, b, cfg := localRegisterFixture(t, f)
				p := registrationPublicationCounter(run)
				q := decodeObject(t, []byte(validLocalRegister))
				m := decodeObject(t, signedRegisterResult(t, cfg.ProtocolSigning, b, []byte(validLocalRegister)))
				calls := 2
				if direction == "request" {
					q["required_features"] = required
					calls = 1
				} else {
					m["required_features"] = required
				}
				f.mu.Lock()
				f.response = resignRegistration(t, cfg.ProtocolSigning, m)
				f.mu.Unlock()
				publishRegistration(t, nc, b, marshalRegister(t, q))
				expectRegistrationCode(t, run, down, cfg.ProtocolSigning, b, CodeProtocolError)
				assertRegistrationCounts(t, run, f, p, b, calls, 1)
			})
		}
	}
}

func TestProductionLocalRegisterRejectsUnknownResponseRaw(t *testing.T) {
	for _, location := range []string{"result", "auth", "audience", "error related"} {
		for _, extension := range []string{`{"a":0,"\u0061":1}`, `{"a":"\ud800"}`, `{"a":1e0}`, `{"a":9007199254740992}`} {
			t.Run(location+"/"+extension, func(t *testing.T) {
				f := &fakeMailboxRPC{}
				run, nc, down, b, cfg := localRegisterFixture(t, f)
				p := registrationPublicationCounter(run)
				response := string(signedRegisterResult(t, cfg.ProtocolSigning, b, []byte(validLocalRegister)))
				switch location {
				case "result":
					response = strings.Replace(response, `"server_auth":`, `"future":`+extension+`,"server_auth":`, 1)
				case "auth":
					response = strings.Replace(response, `"signature":`, `"future":`+extension+`,"signature":`, 1)
				case "audience":
					response = strings.Replace(response, `"epoch":"42"`, `"future":`+extension+`,"epoch":"42"`, 1)
				case "error related":
					raw, err := run.mailbox.signer.encode(b, "req-1", &AppError{Code: CodeConflict, Scope: "request"})
					if err != nil {
						t.Fatal(err)
					}
					response = strings.Replace(string(raw), `"code":`, `"related":{"future":`+extension+`},"code":`, 1)
				}
				f.mu.Lock()
				f.response = []byte(response)
				f.mu.Unlock()
				publishRegistration(t, nc, b, []byte(validLocalRegister))
				expectRegistrationCode(t, run, down, cfg.ProtocolSigning, b, CodeProtocolError)
				assertRegistrationCounts(t, run, f, p, b, 2, 1)
			})
		}
	}
}

func TestProductionLocalRegisterRawBudget(t *testing.T) {
	zeros := func(n int) string { return "[" + strings.Repeat("0,", n-1) + "0]" }
	values := func(last int) string {
		parts := make([]string, 32)
		for i := range parts {
			parts[i] = zeros(256)
		}
		parts[31] = zeros(last)
		return "[" + strings.Join(parts, ",") + "]"
	}
	for _, tc := range []struct {
		name, extension string
		accept          bool
	}{
		{"depth16", strings.Repeat("[", 14) + "0" + strings.Repeat("]", 14), true},
		{"depth17", strings.Repeat("[", 15) + "0" + strings.Repeat("]", 15), false},
		{"members128", registrationMembers(128), true},
		{"members129", registrationMembers(129), false},
		{"array256", zeros(256), true},
		{"array257", zeros(257), false},
		{"string16384", `"` + strings.Repeat(`\u6c49`, 5461) + `a"`, true},
		{"string16385", `"` + strings.Repeat(`\u6c49`, 5461) + `ab"`, false},
		{"astral16384", `"` + strings.Repeat(`\ud83d\ude00`, 4096) + `"`, true},
		{"values8192", values(212), true},
		{"values8193", values(213), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeMailboxRPC{}
			run, _, down, b, cfg := localRegisterFixture(t, f)
			p := registrationPublicationCounter(run)
			raw := registrationExtension(tc.extension)
			response := signedRegisterResult(t, cfg.ProtocolSigning, b, []byte(validLocalRegister))
			f.mu.Lock()
			f.response = response
			f.mu.Unlock()
			// Large vectors exceed this fixture client's existing 4KiB broker
			// cap. Exercise the production callback and real gRPC/down NATS path.
			run.mailbox.receive(&nats.Msg{Subject: UpSubject(b.Ingress), Data: raw})
			calls := 1
			if tc.accept {
				calls = 2
				msg, err := down.NextMsg(time.Second)
				if err != nil || string(msg.Data) != string(response) {
					t.Fatalf("boundary response=%v err=%v", msg, err)
				}
			} else {
				expectRegistrationCode(t, run, down, cfg.ProtocolSigning, b, CodeProtocolError)
			}
			assertRegistrationCounts(t, run, f, p, b, calls, 1)
		})
	}
}

func TestProductionLocalRegisterKnownFieldsCannotBeReplaced(t *testing.T) {
	for _, direction := range []string{"request", "response"} {
		t.Run(direction, func(t *testing.T) {
			f := &fakeMailboxRPC{}
			run, nc, down, b, cfg := localRegisterFixture(t, f)
			p := registrationPublicationCounter(run)
			q := decodeObject(t, []byte(validLocalRegister))
			m := decodeObject(t, signedRegisterResult(t, cfg.ProtocolSigning, b, []byte(validLocalRegister)))
			calls := 2
			if direction == "request" {
				q["candidate_local"], q["Candidate_local"], q["Request_id"] = "0", "18446744073709551615", "other-request"
				calls = 1
			} else {
				m["allocation_op_id"], m["Allocation_op_id"] = "wrong-op", "Op-1:A"
			}
			f.mu.Lock()
			f.response = reverseRegistrationKeys(t, decodeObject(t, resignRegistration(t, cfg.ProtocolSigning, m)))
			f.mu.Unlock()
			publishRegistration(t, nc, b, reverseRegistrationKeys(t, q))
			expectRegistrationCode(t, run, down, cfg.ProtocolSigning, b, CodeProtocolError)
			assertRegistrationCounts(t, run, f, p, b, calls, 1)
		})
	}
}

func TestLocalRegisterCompatibilityDoesNotChangeOriginalOperations(t *testing.T) {
	cfg := signingFixture(t)
	b := liveBinding{Realm: "2", Node: "64", Incarnation: "inc-1", Epoch: 42, ChannelNonce: "nonce-1", NotAfter: time.Now().Add(time.Hour)}
	for _, op := range []string{"ready", "head", "fetch", "receipt"} {
		t.Run(op, func(t *testing.T) {
			requestID := "req-1"
			if op == "head" {
				requestID = ""
			}
			response := decodeObject(t, signedMailboxFrame(t, cfg, op, b, requestID))
			response["app_version"] = "1.1"
			if _, err := validateMailboxResponse(b, op, requestID, []byte(validMailboxFetch), marshalRegister(t, response)); err != errUnauthorized {
				t.Fatalf("original %s version policy changed: %v", op, err)
			}
		})
	}
	if _, _, ae := mailboxOperation([]byte(strings.Replace(validMailboxFetch, `"app_version":"1.0"`, `"app_version":"1.1"`, 1)), false); ae == nil || ae.Code != CodeProtocolError {
		t.Fatalf("original FETCH upstream version policy changed: %v", ae)
	}
}
