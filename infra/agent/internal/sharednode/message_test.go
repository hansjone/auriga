package sharednode

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func messageInput() map[string]any {
	now := time.Now().UTC().Truncate(time.Second)
	return map[string]any{"tenantId": testTenant, "sessionId": testSession, "taskId": testTask, "capability": "synthetic-capability-1", "bootId": testPool, "operationId": testTask, "nativeWitness": map[string]any{"witness_id": testTask, "binding_id": testPool, "binding_revision": "1", "observed_at": now.Format(time.RFC3339), "valid_until": now.Add(30 * time.Second).Format(time.RFC3339)}}
}

func TestMessagesFixedMTLSContextSendReceiveAndDegradedHealth(t *testing.T) {
	r, a := fixture(t)
	begin(t, r, a, "auriga.shared.message", []string{"message.send", "message.receive"})
	var calls atomic.Int32
	n := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls.Add(1)
		if req.TLS == nil || len(req.TLS.PeerCertificates) != 1 || req.Method != "POST" || req.URL.RawQuery != "" || req.Header.Get("Authorization") != "" || req.Header.Get("Content-Type") != "application/json" {
			t.Error("not a fixed mTLS request")
		}
		for k, v := range map[string]string{"Auriga-Shared-Capability": "synthetic-capability-1", "Auriga-Shared-Session-Id": testSession, "Auriga-Shared-Task-Id": testTask} {
			if values := req.Header.Values(k); len(values) != 1 || values[0] != v {
				t.Error("identity carrier lost", k)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.URL.Path {
		case "/internal/shared/messages/context":
			binding, _ := wire(a.binding)
			_ = json.NewEncoder(w).Encode(map[string]any{"binding": binding, "taskId": testTask, "contextId": testSession, "connection": map[string]any{"bootId": testPool, "requestId": testTask}, "sourceGet": map[string]any{"opaque": "original-signed-fixture"}})
		case "/internal/shared/messages/send":
			raw, _ := io.ReadAll(req.Body)
			if !strings.Contains(string(raw), `"body":{"n":1}`) {
				t.Error("body rewritten")
			}
			w.WriteHeader(409)
			_, _ = io.WriteString(w, `{"code":"network.outcome_unknown","retryable":false,"signature":"original"}`)
		case "/internal/shared/messages/receive":
			_ = json.NewEncoder(w).Encode(map[string]any{"page": map[string]any{"items": []any{}}, "signature": "original-page", "fixtureBatch": strings.Repeat("x", 70000)})
		default:
			t.Error("nonfixed endpoint", req.URL.Path)
		}
	}))
	n.TLS = &tls.Config{MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAnyClientCert}
	n.StartTLS()
	defer n.Close()
	r.Client.Messages = n.Client()
	r.Client.Messages.Transport.(*http.Transport).TLSClientConfig.Certificates = n.TLS.Certificates
	r.Client.Settings.SharedMessages = &Endpoint{Address: n.URL}
	r.Message = r.Client.message
	in := messageInput()
	raw, _ := json.Marshal(in)
	value, err := r.invoke(context.Background(), "messageContext", raw)
	if err != nil || !strings.Contains(string(value.(json.RawMessage)), "original-signed-fixture") {
		t.Fatal("context not connected", err)
	}
	delete(in, "operationId")
	in["contextId"], in["messageId"], in["target"], in["body"] = testSession, testTask, map[string]string{"realm": "1", "node": "2", "local": "3"}, map[string]any{"n": 1}
	raw, _ = json.Marshal(in)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/_auriga/shared-rpc/send", strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != 409 || w.Body.String() != `{"code":"network.outcome_unknown","retryable":false,"signature":"original"}` {
		t.Fatal("original error lost", w.Code, w.Body.String())
	}
	in["bootId"] = testSession
	raw, _ = json.Marshal(in)
	if _, err = r.invoke(context.Background(), "send", raw); err == nil || calls.Load() != 2 {
		t.Fatal("boot substitution reached N", err, calls.Load())
	}
	in["bootId"] = testPool
	delete(in, "messageId")
	delete(in, "target")
	delete(in, "body")
	in["operation"], in["frame"] = "fetch", map[string]any{"opaque": "original-frame"}
	raw, _ = json.Marshal(in)
	value, err = r.invoke(context.Background(), "receive", raw)
	if err != nil || !strings.Contains(string(value.(json.RawMessage)), "original-page") {
		t.Fatal("receive not connected", err)
	}
	if len(value.(json.RawMessage)) < 70000 {
		t.Fatal("signed batch was truncated")
	}
	in["operation"] = "receiveSet"
	raw, _ = json.Marshal(in)
	if _, err = r.invoke(context.Background(), "receive", raw); err != nil {
		t.Fatal("explicit receive toggle not connected", err)
	}
	health, _ := r.invoke(context.Background(), "health", []byte("{}"))
	ops := health.(map[string]any)["operations"].([]string)
	if !has(ops, "messageContext") || !has(ops, "send") || !has(ops, "receive") {
		t.Fatal("installed operations disappeared", ops)
	}
	in["operation"] = "pretendExecuted"
	raw, _ = json.Marshal(in)
	if _, err = r.invoke(context.Background(), "receive", raw); err == nil {
		t.Fatal("arbitrary receive phase accepted")
	}
}

func TestMessageCancellationAndAuthorityRotationDoNotReturnStaleProof(t *testing.T) {
	for _, mode := range []string{"cancel", "rotate"} {
		t.Run(mode, func(t *testing.T) {
			r, a := fixture(t)
			begin(t, r, a, "auriga.shared.message", []string{"message.send"})
			entered, closed := make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			n := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls.Add(1)
				_, _ = io.Copy(io.Discard, req.Body)
				w.Header().Set("Content-Type", "application/json")
				if mode == "cancel" {
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
				}
				close(entered)
				if mode == "cancel" {
					<-req.Context().Done()
					close(closed)
					return
				}
				a.version.Store(2)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"contextId":"unused"}`)
			}))
			defer n.Close()
			r.Client.Messages, r.Client.Settings.SharedMessages = n.Client(), &Endpoint{Address: n.URL}
			r.Message = r.Client.message
			raw, _ := json.Marshal(messageInput())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := r.invoke(ctx, "messageContext", raw); done <- err }()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("not connected")
			}
			if mode == "cancel" {
				cancel()
				select {
				case <-closed:
				case <-time.After(time.Second):
					t.Fatal("HTTP not canceled")
				}
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("stale/canceled proof returned")
				}
			case <-time.After(time.Second):
				t.Fatal("call did not exit")
			}
			if calls.Load() != 1 {
				t.Fatal("call replayed")
			}
		})
	}
}
