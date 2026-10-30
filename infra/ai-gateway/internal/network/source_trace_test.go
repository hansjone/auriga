package network

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/observability"
	collab "github.com/hansjone/auriga/infra/gen/auriga/collab/v1"
	"github.com/nats-io/nats.go"
	"google.golang.org/grpc/metadata"
)

type sourceTraceRPC struct {
	*fakeMailboxRPC
	parents chan []string
}

func (f *sourceTraceRPC) record(ctx context.Context) {
	md, _ := metadata.FromIncomingContext(ctx)
	f.parents <- md.Get("traceparent")
}

func (f *sourceTraceRPC) SourceRegister(ctx context.Context, q *collab.NetworkMailboxServiceSourceRegisterRequest) (*collab.NetworkMailboxServiceSourceRegisterResponse, error) {
	f.record(ctx)
	return f.fakeMailboxRPC.SourceRegister(ctx, q)
}

func (f *sourceTraceRPC) SourceGet(ctx context.Context, q *collab.NetworkMailboxServiceSourceGetRequest) (*collab.NetworkMailboxServiceSourceGetResponse, error) {
	f.record(ctx)
	return f.fakeMailboxRPC.SourceGet(ctx, q)
}

func (f *sourceTraceRPC) SourceRevoke(ctx context.Context, q *collab.NetworkMailboxServiceSourceRevokeRequest) (*collab.NetworkMailboxServiceSourceRevokeResponse, error) {
	f.record(ctx)
	return f.fakeMailboxRPC.SourceRevoke(ctx, q)
}

// Runtime reconciliation can log concurrently with request workers.
type sourceTraceLog struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (w *sourceTraceLog) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.Write(p)
}

func (w *sourceTraceLog) snapshot() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return bytes.Clone(w.buffer.Bytes())
}

func TestProductionSourceTraceContinuity(t *testing.T) {
	for _, op := range []string{"source_register", "source_get", "source_revoke"} {
		for _, withHeader := range []bool{false, true} {
			name := op + "/without_header"
			if withHeader {
				name = op + "/with_header"
			}
			t.Run(name, func(t *testing.T) {
				var logs sourceTraceLog
				previous := slog.Default()
				slog.SetDefault(observability.Logger(&logs))
				t.Cleanup(func() { slog.SetDefault(previous) })
				f := &fakeMailboxRPC{}
				rpc := &sourceTraceRPC{fakeMailboxRPC: f, parents: make(chan []string, 1)}
				run, nc, down, b, cfg := sourceFixture(t, f, rpc)
				response := sourceResult(t, cfg.ProtocolSigning, b, op, "active")
				f.mu.Lock()
				f.response = response
				f.mu.Unlock()
				raw := sourceRequest(op)
				msg := &nats.Msg{Subject: UpSubject(b.Ingress), Data: raw}
				if withHeader {
					// Existing NATS correlation derives from request_id, as in the
					// plugin. A transport header must not override that trace.
					msg.Header = nats.Header{"traceparent": {"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}}
				}
				if err := nc.PublishMsg(msg); err != nil {
					t.Fatal(err)
				}
				got, err := down.NextMsg(time.Second)
				if err != nil {
					t.Fatal(err)
				}
				run.mailbox.workers.Wait()
				if !bytes.Equal(got.Data, response) {
					t.Fatal("trace observation changed signed response bytes")
				}
				f.mu.Lock()
				forwarded := bytes.Clone(f.frames[len(f.frames)-1])
				f.mu.Unlock()
				if !bytes.Equal(forwarded, raw) || run.redeemer.snapshot()[0] != b {
					t.Fatal("trace observation changed request bytes or connection authority")
				}
				var parents []string
				select {
				case parents = <-rpc.parents:
				case <-time.After(time.Second):
					t.Fatal("source RPC was not observed")
				}
				// Fixed SHA-256 vector from the plugin's natsTraceparent(req-1).
				const wantTrace = "257c7be6dfcc23c9d67f72366726f063"
				if len(parents) != 1 || !observability.Valid(parents[0]) || parents[0][3:35] != wantTrace {
					t.Fatalf("source RPC traceparent=%q, want trace_id=%s", parents, wantTrace)
				}
				found := false
				for _, line := range bytes.Split(logs.snapshot(), []byte{'\n'}) {
					if len(line) == 0 {
						continue
					}
					var event map[string]any
					if err := json.Unmarshal(line, &event); err != nil {
						t.Fatal(err)
					}
					if event["msg"] == "NATS business request" && event["operation"] == op && event["request_id"] == "req-1" {
						found = true
						if event["trace_id"] != wantTrace || event["service"] != "ai-gateway" {
							t.Fatalf("gateway source log lost plugin trace: %s", line)
						}
					}
				}
				if !found {
					t.Fatal("gateway source request log missing")
				}
			})
		}
	}
}
