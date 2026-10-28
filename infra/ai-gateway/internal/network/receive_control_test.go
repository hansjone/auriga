package network

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	collab "github.com/hansjone/auriga/infra/gen/auriga/collab/v1"
)

func (f *fakeMailboxRPC) ReceiveSet(ctx context.Context, q *collab.NetworkMailboxServiceReceiveSetRequest) (*collab.NetworkMailboxServiceReceiveSetResponse, error) {
	b, e := f.call(ctx, "receive_set", q.Connection, q.FrameJson)
	return &collab.NetworkMailboxServiceReceiveSetResponse{FrameJson: b}, e
}
func (f *fakeMailboxRPC) AttemptBegin(ctx context.Context, q *collab.NetworkMailboxServiceAttemptBeginRequest) (*collab.NetworkMailboxServiceAttemptBeginResponse, error) {
	b, e := f.call(ctx, "attempt_begin", q.Connection, q.FrameJson)
	return &collab.NetworkMailboxServiceAttemptBeginResponse{FrameJson: b}, e
}
func (f *fakeMailboxRPC) ExecutionAdmit(ctx context.Context, q *collab.NetworkMailboxServiceExecutionAdmitRequest) (*collab.NetworkMailboxServiceExecutionAdmitResponse, error) {
	b, e := f.call(ctx, "execution_admit", q.Connection, q.FrameJson)
	return &collab.NetworkMailboxServiceExecutionAdmitResponse{FrameJson: b}, e
}

func TestReceiveControlForwarding(t *testing.T) {
	f := &fakeMailboxRPC{}
	run, nc, down, b, cfg := sourceFixture(t, f, f)
	for _, tc := range []struct{ op, fields, result string }{
		{"receive_set", `"epoch":"42","registration":{"local":"1","registration_id":"reg-1","allocation_op_id":"alloc-1","allocation_fingerprint":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","registration_revision":"1","address_type":"session"},"enabled":true,"expected_version":"1"`, "receive_set_result"},
		{"attempt_begin", `"epoch":"42","attempt_id":"attempt-1","delivery_id":"delivery-1","recipient_id":"recipient-1","purpose":"process"`, "attempt_result"},
		{"execution_admit", `"connection_epoch":"42","recipient_id":"recipient-1","host_request_id":"recipient-1","attempt_id":"attempt-1","target":{"version":1,"realm":"2","node":"64","local":"1","type":"session"}`, "admit_result"},
	} {
		t.Run(tc.op, func(t *testing.T) {
			request := []byte(`{"app_version":"1.0","type":"` + tc.op + `","request_id":"receive-1","boot_id":"boot-1",` + tc.fields + `}`)
			body := decodeObject(t, signedMailboxFrame(t, cfg.ProtocolSigning, "receipt", b, "receive-1"))
			body["type"], body["state_version"] = tc.result, "9007199254740993"
			response := resignRegistration(t, cfg.ProtocolSigning, body)
			f.mu.Lock()
			f.response = response
			before := len(f.calls)
			f.mu.Unlock()
			if err := nc.Publish(UpSubject(b.Ingress), request); err != nil {
				t.Fatal(err)
			}
			msg, err := down.NextMsg(3 * time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(msg.Data, response) {
				t.Fatalf("response rewritten: %s", msg.Data)
			}
			if err := validateRegistrationPhaseAuth(cfg.ProtocolSigning, b, msg.Data, "receive-1"); err != nil {
				t.Fatal(err)
			}
			run.mailbox.workers.Wait()
			f.mu.Lock()
			if len(f.calls) != before+1 || f.calls[before] != tc.op || !bytes.Equal(f.frames[before], request) || f.identities[before].Ingress != b.Ingress || f.identities[before].TenantId != b.Tenant {
				t.Fatal("control frame/identity changed")
			}
			f.mu.Unlock()
			bad := bytes.Replace(request, []byte(`"boot-1"`), []byte(`"wrong-boot"`), 1)
			if err := nc.Publish(UpSubject(b.Ingress), bad); err != nil {
				t.Fatal(err)
			}
			msg, err = down.NextMsg(3 * time.Second)
			if err != nil {
				t.Fatal(err)
			}
			var result map[string]json.RawMessage
			_ = json.Unmarshal(msg.Data, &result)
			if registrationString(result["code"]) != CodeStaleEpoch {
				t.Fatalf("bad boot accepted: %s", msg.Data)
			}
			run.mailbox.workers.Wait()
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.calls) != before+1 {
				t.Fatal("bad boot reached collab")
			}
		})
	}
}

func TestMailboxContinuationAndReceiptVersion(t *testing.T) {
	f := &fakeMailboxRPC{}
	run, nc, down, b, cfg := sourceFixture(t, f, f)
	for _, tc := range []struct{ op, raw string }{
		{"fetch", `{"app_version":"1.0","type":"fetch","request_id":"page-1","epoch":"42","resume_cursor":"0","continuation":"opaque-saved-position","max_items":1,"max_bytes":4096,"held_ids":[],"paused":[],"state_barrier":[]}`},
		{"receipt", `{"app_version":"1.0","type":"receipt","request_id":"page-1","operation_id":"operation-1","epoch":"42","delivery_id":"delivery-1","mail_seq":"1","results":[{"recipient_id":"recipient-1","status":"host_accepted","expected_state_version":"9007199254740993","message_digest":"sha256:proof","host_request_id":"recipient-1"}]}`},
	} {
		response := signedMailboxFrame(t, cfg.ProtocolSigning, tc.op, b, "page-1")
		f.mu.Lock()
		f.response = response
		before := len(f.calls)
		f.mu.Unlock()
		if err := nc.Publish(UpSubject(b.Ingress), []byte(tc.raw)); err != nil {
			t.Fatal(err)
		}
		msg, err := down.NextMsg(3 * time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(msg.Data, response) {
			t.Fatal("signed downstream was rewritten")
		}
		run.mailbox.workers.Wait()
		f.mu.Lock()
		exact := len(f.calls) == before+1 && string(f.frames[before]) == tc.raw
		f.mu.Unlock()
		if !exact {
			t.Fatal("continuation or recipient expected_state_version rewritten")
		}
	}
}
