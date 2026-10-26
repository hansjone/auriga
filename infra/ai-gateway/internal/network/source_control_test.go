package network

import (
	"context"
	"strings"
	"testing"
	"time"

	collab "github.com/hansjone/auriga/infra/gen/auriga/collab/v1"
)

const sourceRegistrationJSON = `{"registration_id":"reg-1","allocation_op_id":"alloc-1","allocation_fingerprint":"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","registration_revision":"1","local":"2","address_type":"session"}`
const sourceWitness = `{"witness_id":"witness-1","binding_id":"binding-1","binding_revision":"1","observed_at":"2026-10-27T00:00:00Z","valid_until":"2026-10-27T00:00:30Z"}`

func sourceRequest(op string) []byte {
	extra := `,"source_binding":"source-1","operation_id":"op-1"`
	switch op {
	case "source_get":
		extra = `,"source_binding":"source-1","registration":` + sourceRegistrationJSON + `,"native_witness":` + sourceWitness
	case "source_register":
		extra = `,"operation_id":"op-1","mode":"issue","registration":` + sourceRegistrationJSON + `,"scope":"durable_unicast_send","expected_revision":"0","predecessor_binding":"","native_witness":` + sourceWitness
	}
	return []byte(` { "app_version":"1.0","type":"` + op + `","request_id":"req-1","epoch":"42","boot_id":"boot-1"` + extra + `,"future":{"TYPE":"ignored"}} `)
}

func (f *fakeMailboxRPC) SourceRegister(ctx context.Context, q *collab.NetworkMailboxServiceSourceRegisterRequest) (*collab.NetworkMailboxServiceSourceRegisterResponse, error) {
	b, e := f.call(ctx, "source_register", q.Connection, q.FrameJson)
	return &collab.NetworkMailboxServiceSourceRegisterResponse{FrameJson: b}, e
}
func (f *fakeMailboxRPC) SourceGet(ctx context.Context, q *collab.NetworkMailboxServiceSourceGetRequest) (*collab.NetworkMailboxServiceSourceGetResponse, error) {
	b, e := f.call(ctx, "source_get", q.Connection, q.FrameJson)
	return &collab.NetworkMailboxServiceSourceGetResponse{FrameJson: b}, e
}
func (f *fakeMailboxRPC) SourceRevoke(ctx context.Context, q *collab.NetworkMailboxServiceSourceRevokeRequest) (*collab.NetworkMailboxServiceSourceRevokeResponse, error) {
	b, e := f.call(ctx, "source_revoke", q.Connection, q.FrameJson)
	return &collab.NetworkMailboxServiceSourceRevokeResponse{FrameJson: b}, e
}

func TestSourceOperationAndLegacyRejection(t *testing.T) {
	for _, op := range []string{"source_register", "source_get", "source_revoke"} {
		raw := sourceRequest(op)
		got, id, ae := mailboxOperation(raw, false)
		if ae != nil || got != op || id != "req-1" {
			t.Fatalf("%s: operation=%s id=%s error=%v", op, got, id, ae)
		}
		if _, ae = DecodeUpstream(raw); ae == nil || ae.Code != CodeProtocolError {
			t.Fatal("legacy decoder unexpectedly supports source control")
		}
	}
}

func sourceResult(t *testing.T, cfg ProtocolSigningConfig, b liveBinding, op, state string) []byte {
	t.Helper()
	s, err := newErrorSigner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(s.key)
	errRaw, err := s.encode(b, "req-1", &AppError{Code: CodeConflict, Scope: "request"})
	if err != nil {
		t.Fatal(err)
	}
	fact := decodeObject(t, []byte(`{"source_binding":"source-1","registration":`+sourceRegistrationJSON+`,"scope":"durable_unicast_send","revision":"1","state":"`+state+`","not_before":"2026-10-26T00:00:00Z","expires_at":"2026-10-27T00:00:00Z","predecessor_binding":"","native_binding":{"binding_id":"binding-1","binding_revision":"1"}}`))
	m := map[string]any{"app_version": "1.0", "type": op + "_result", "request_id": "req-1", "server_auth": decodeObject(t, errRaw)["server_auth"], "future": map[string]any{"保留": "原字节"}}
	switch op {
	case "source_register":
		m["operation_id"], m["source"], m["result"], m["failure_evidence"] = "op-1", fact, "registered", nil
	case "source_get":
		m["source"], m["policy_revision"], m["observed_at"], m["witness_id"] = fact, "1", "2026-10-27T00:00:00Z", "witness-1"
		if state == "active" {
			m["valid_until"] = "2026-10-27T00:00:30Z"
		}
	case "source_revoke":
		m["operation_id"], m["source_binding"], m["revision"], m["state"], m["lineage_stopped"] = "op-1", "source-1", "2", "revoked", true
	}
	return append(append([]byte(" \n"), resignRegistration(t, cfg, m)...), '\n')
}

func TestProductionSourceRoundTrip(t *testing.T) {
	for _, op := range []string{"source_register", "source_get", "source_revoke"} {
		t.Run(op, func(t *testing.T) {
			f := &fakeMailboxRPC{}
			run, nc, down, b, cfg := sourceFixture(t, f, f)
			response := sourceResult(t, cfg.ProtocolSigning, b, op, "active")
			f.mu.Lock()
			f.response = response
			f.mu.Unlock()
			publishRegistration(t, nc, b, sourceRequest(op))
			msg, err := down.NextMsg(time.Second)
			if err != nil || string(msg.Data) != string(response) {
				t.Fatalf("response=%v err=%v", msg, err)
			}
			run.mailbox.workers.Wait()
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.calls) != 2 || f.calls[1] != op || string(f.frames[1]) != string(sourceRequest(op)) {
				t.Fatalf("calls=%v frames=%q", f.calls, f.frames)
			}
			id := f.identities[1]
			if id.TenantId != b.Tenant || id.Realm != b.Realm || id.Node != b.Node || id.Ingress != b.Ingress || id.ServerId != b.ServerID || id.ClientId != u64str(b.ClientID) || id.ClientAccount != b.Account {
				t.Fatal("untrusted identity")
			}
			verifyErrorSignature(t, cfg.ProtocolSigning, msg.Data)
			if got := run.redeemer.snapshot()[0]; got != b {
				t.Fatal("source transport changed connection authority")
			}
		})
	}
}

func TestProductionSourceReservedLane(t *testing.T) {
	f := &fakeMailboxRPC{}
	run, nc, down, b, cfg := sourceFixture(t, f, f)
	f.mu.Lock()
	f.block = true
	f.entered = make(chan struct{}, 4)
	f.mu.Unlock()
	publishRegistration(t, nc, b, sourceRequest("source_register"))
	select {
	case <-f.entered:
	case <-time.After(time.Second):
		t.Fatal("register did not enter")
	}
	// The regular RPC remains blocked. A second regular request must be busy,
	// but revocation must enter the real generated RPC independently.
	publishRegistration(t, nc, b, sourceRequest("source_get"))
	expectRegistrationCode(t, down, cfg.ProtocolSigning, b, "network.overloaded")
	run.mailbox.mu.Lock()
	run.mailbox.budgetLocked(nodeIdentity{b.Tenant, b.Realm, b.Node}).requests = requestBucket{updated: time.Now(), tokens: -1000}
	run.mailbox.mu.Unlock()
	f.mu.Lock()
	f.block = false
	f.response = sourceResult(t, cfg.ProtocolSigning, b, "source_revoke", "revoked")
	f.mu.Unlock()
	publishRegistration(t, nc, b, sourceRequest("source_revoke"))
	msg, err := down.NextMsg(time.Second)
	if err != nil || !strings.Contains(string(msg.Data), `"source_revoke_result"`) {
		t.Fatalf("revocation queued behind regular call: %v %v", msg, err)
	}
	run.Close()
}

func TestProductionSourceRevokeBusyAndCancelRelease(t *testing.T) {
	f := &fakeMailboxRPC{}
	run, nc, down, b, cfg := sourceFixture(t, f, f)
	f.mu.Lock()
	f.block = true
	f.entered = make(chan struct{}, 2)
	f.mu.Unlock()
	publishRegistration(t, nc, b, sourceRequest("source_revoke"))
	select {
	case <-f.entered:
	case <-time.After(time.Second):
		t.Fatal("revoke did not enter")
	}
	publishRegistration(t, nc, b, sourceRequest("source_revoke"))
	expectRegistrationCode(t, down, cfg.ProtocolSigning, b, "network.overloaded")
	run.mailbox.mu.Lock()
	run.mailbox.sources[sourceLane{nodeIdentity{b.Tenant, b.Realm, b.Node}, true}].cancel()
	run.mailbox.mu.Unlock()
	expectRegistrationCode(t, down, cfg.ProtocolSigning, b, "network.overloaded")
	run.mailbox.workers.Wait()
	if len(run.mailbox.sourceRevokes) != 0 {
		t.Fatal("canceled revoke leaked reserved slot")
	}
	f.mu.Lock()
	f.block = false
	f.response = sourceResult(t, cfg.ProtocolSigning, b, "source_revoke", "revoked")
	f.mu.Unlock()
	publishRegistration(t, nc, b, sourceRequest("source_revoke"))
	msg, err := down.NextMsg(time.Second)
	if err != nil || !strings.Contains(string(msg.Data), `"source_revoke_result"`) {
		t.Fatalf("new revoke cannot reuse slot: %v %v", msg, err)
	}
	run.mailbox.workers.Wait()
}
