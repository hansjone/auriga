package network

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
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
	h, cfg, _ := runtimeFixture(t)
	cfg.Collab = startMailboxRPC(t, f)
	run, err := StartRuntime(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(run.Close)
	nc, err := nats.Connect(h.wsURL(), nats.Token(connectTicket), nats.NoReconnect())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	b := run.redeemer.snapshot()[0]
	b.Epoch, b.Incarnation, b.ChannelNonce = 42, "inc-1", "nonce-1"
	down, err := nc.SubscribeSync(DownSubject(b.Ingress))
	if err != nil {
		t.Fatal(err)
	}
	if err = nc.Flush(); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.response = signedMailboxFrame(t, cfg.ProtocolSigning, "ready", b, "req-ready")
	f.mu.Unlock()
	if err = nc.Publish(UpSubject(b.Ingress), []byte(`{"app_version":"1.0","type":"ready","request_id":"req-ready","channel_nonce":"nonce-1","boot_id":"boot-1"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err = down.NextMsg(time.Second); err != nil {
		t.Fatal(err)
	}
	run.mailbox.workers.Wait()
	return run, nc, down, b, cfg
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

func expectRegistrationCode(t *testing.T, sub *nats.Subscription, cfg ProtocolSigningConfig, b liveBinding, code string) {
	t.Helper()
	expectRegistrationError(t, sub, cfg, b, code, "req-1")
}

func expectRegistrationError(t *testing.T, sub *nats.Subscription, cfg ProtocolSigningConfig, b liveBinding, code string, requestID any) {
	t.Helper()
	msg, err := sub.NextMsg(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	m := decodeObject(t, msg.Data)
	if msg.Subject != DownSubject(b.Ingress) || m["type"] != "error" || m["code"] != code || m["request_id"] != requestID || strings.Contains(string(msg.Data), "private backend") {
		t.Fatalf("wrong rejection subject=%s body=%s", msg.Subject, msg.Data)
	}
	verifyErrorSignature(t, cfg, msg.Data)
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
		"extra":                []byte(strings.Replace(validLocalRegister, `"boot_id"`, `"tenant":"forged","boot_id"`, 1)),
		"empty key":            []byte(strings.Replace(validLocalRegister, `"boot_id"`, `"":"forged","boot_id"`, 1)),
		"combined key":         []byte(strings.Replace(validLocalRegister, `"boot_id"`, `"app_version type":"forged","boot_id"`, 1)),
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
			expectRegistrationError(t, down, cfg.ProtocolSigning, b, CodeProtocolError, requestID)
			assertRegistrationCounts(t, run, f, p, b, 1, 1)
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
			}
			raw := []byte(validLocalRegister)
			if epoch != 0 {
				raw = []byte(strings.Replace(validLocalRegister, `"epoch":"42"`, `"epoch":"`+u64str(epoch)+`"`, 1))
			}
			publishRegistration(t, nc, b, raw)
			expectRegistrationCode(t, down, cfg.ProtocolSigning, b, CodeStaleEpoch)
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
		"top epoch":           func(m map[string]any) { m["epoch"] = "42" },
		"top boot":            func(m map[string]any) { m["boot_id"] = "boot-1" },
		"top fingerprint":     func(m map[string]any) { m["allocation_fingerprint"] = "sha256:invalid" },
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
		"extra auth":        func(a map[string]any) { a["trusted"] = true },
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
	changes["extra audience"] = func(m map[string]any) {
		m["server_auth"].(map[string]any)["audience"].(map[string]any)["tenant"] = "forged"
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
			expectRegistrationCode(t, down, cfg.ProtocolSigning, b, CodeProtocolError)
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
			expectRegistrationCode(t, down, cfg.ProtocolSigning, b, code)
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
			expectRegistrationCode(t, down, cfg.ProtocolSigning, b, tc.code)
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
					expectRegistrationCode(t, down, cfg.ProtocolSigning, b, "network.revoked")
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
	expectRegistrationCode(t, down, cfg.ProtocolSigning, b, "network.overloaded")
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
