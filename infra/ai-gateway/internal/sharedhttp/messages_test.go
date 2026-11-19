package sharedhttp

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/sharedauth"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/team"
	collab "github.com/hansjone/auriga/infra/gen/auriga/collab/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type messageCallFunc func(context.Context, *collab.CallSharedMessageRequest) ([]byte, error)

func (f messageCallFunc) Call(ctx context.Context, r *collab.CallSharedMessageRequest) ([]byte, error) {
	return f(ctx, r)
}
func messageFixtureRequest(t *testing.T, f *fixture) (*http.Request, map[string]any) {
	t.Helper()
	r := f.request(t, false)
	c, err := f.gate.Verify(t.Context(), r.Header.Get("Auriga-Shared-Capability"), f.tenant, r.Header.Get("Auriga-Shared-Session-Id"), r.Header.Get("Auriga-Shared-Task-Id"), sharedauth.Model, "model.invoke")
	if err != nil {
		t.Fatal(err)
	}
	token, err := f.gate.Issue(t.Context(), sharedauth.Lookup{TenantID: f.tenant, SessionID: c.Binding.SessionID, TaskID: c.TaskID, Audience: sharedauth.Message}, []string{"message.send"}, time.Minute, "model", f.private)
	if err != nil {
		t.Fatal(err)
	}
	in := map[string]any{"tenantId": f.tenant, "sessionId": c.Binding.SessionID, "taskId": c.TaskID, "capability": token, "operationId": uuid.NewString(), "bootId": uuid.NewString(), "nativeWitness": map[string]string{"witness_id": uuid.NewString(), "binding_id": uuid.NewString(), "binding_revision": "1", "observed_at": time.Now().UTC().Truncate(time.Second).Format(time.RFC3339), "valid_until": time.Now().UTC().Add(30 * time.Second).Truncate(time.Second).Format(time.RFC3339)}}
	r.URL.Path = MessagePrefix + "context"
	r.Header.Set("Auriga-Shared-Capability", token)
	setMessageBody(t, r, in)
	return r, in
}
func setMessageBody(t *testing.T, r *http.Request, in map[string]any) {
	t.Helper()
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	r.Body = io.NopCloser(strings.NewReader(string(b)))
	r.ContentLength = int64(len(b))
}
func TestSharedMessageHTTPUsesRealKAndNodeIdentityAndPreservesOriginalResult(t *testing.T) {
	f := newFixture(t, nil)
	r, in := messageFixtureRequest(t, f)
	h, err := NewMessageHandler(f.tenant, []string{f.node}, f.gate, messageCallFunc(func(ctx context.Context, req *collab.CallSharedMessageRequest) ([]byte, error) {
		if req.AuthenticatedNodeId != f.node || req.Capability.Binding.TenantId != f.tenant || req.Capability.Audience != sharedauth.Message || req.Operation != "context" {
			t.Fatal("wrong trusted identity")
		}
		md, _ := metadata.FromOutgoingContext(ctx)
		if md.Get("auriga-tenant-id")[0] != f.tenant {
			t.Fatal("missing tenant delegation")
		}
		var original map[string]any
		if json.Unmarshal(req.RequestJson, &original) != nil || original["operationId"] != in["operationId"] {
			t.Fatal("request was rewritten")
		}
		return []byte(`{"contextId":"original-result"}`), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Auriga-Node-Id", uuid.NewString())
	out := httptest.NewRecorder()
	h.ServeHTTP(out, r)
	if out.Code != 200 || out.Body.String() != `{"contextId":"original-result"}` {
		t.Fatalf("%d %s", out.Code, out.Body.String())
	}
}
func TestSharedMessageHTTPRejectsForgedBoundariesBeforeDispatch(t *testing.T) {
	for _, name := range []string{"node", "token", "tenant", "task", "jwt", "unknown", "query", "duplicate-header", "old-version"} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, nil)
			r, in := messageFixtureRequest(t, f)
			var calls atomic.Int32
			h, err := NewMessageHandler(f.tenant, []string{f.node}, f.gate, messageCallFunc(func(context.Context, *collab.CallSharedMessageRequest) ([]byte, error) {
				calls.Add(1)
				return []byte(`{}`), nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			switch name {
			case "node":
				r.TLS = nil
			case "token":
				r.Header.Set("Auriga-Shared-Capability", in["capability"].(string)+"x")
				in["capability"] = r.Header.Get("Auriga-Shared-Capability")
			case "tenant":
				in["tenantId"] = uuid.NewString()
			case "task":
				in["taskId"] = uuid.NewString()
			case "jwt":
				r.Header.Set("Authorization", "Bearer ordinary-user")
			case "unknown":
				in["sender"] = map[string]string{"user": "pool-owner"}
			case "query":
				r.URL.RawQuery = "owner=pool"
			case "duplicate-header":
				r.Header.Add("Auriga-Shared-Task-Id", in["taskId"].(string))
			case "old-version":
				f.a.mu.Lock()
				f.a.version++
				f.a.mu.Unlock()
			}
			setMessageBody(t, r, in)
			out := httptest.NewRecorder()
			h.ServeHTTP(out, r)
			if out.Code == 200 || calls.Load() != 0 {
				t.Fatalf("accepted %s: %d %s", name, out.Code, out.Body.String())
			}
		})
	}
}
func TestSharedMessageHTTPRechecksRevocationAndCancellationAfterTheRealPort(t *testing.T) {
	for _, name := range []string{"revocation", "cancellation", "temporary-backend", "empty-result", "bad-json"} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, nil)
			r, _ := messageFixtureRequest(t, f)
			ctx, cancel := context.WithCancel(r.Context())
			defer cancel()
			r = r.WithContext(ctx)
			h, err := NewMessageHandler(f.tenant, []string{f.node}, f.gate, messageCallFunc(func(context.Context, *collab.CallSharedMessageRequest) ([]byte, error) {
				switch name {
				case "revocation":
					f.a.mu.Lock()
					f.a.revoked = true
					f.a.mu.Unlock()
				case "cancellation":
					cancel()
				case "temporary-backend":
					return nil, sharedauth.ErrUnavailable
				case "empty-result":
					return nil, nil
				case "bad-json":
					return []byte("not-json"), nil
				}
				return []byte(`{"status":"platform_stored"}`), nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			out := httptest.NewRecorder()
			h.ServeHTTP(out, r)
			if out.Code == 200 || strings.Contains(out.Body.String(), "platform_stored") {
				t.Fatalf("late success: %d %s", out.Code, out.Body.String())
			}
		})
	}
}

type sharedCollabPeer struct {
	collab.UnimplementedSharedMessageServiceServer
	tenant, node string
	calls        atomic.Int32
}

func (p *sharedCollabPeer) CallSharedMessage(ctx context.Context, r *collab.CallSharedMessageRequest) (*collab.CallSharedMessageResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	if len(md.Get("authorization")) != 1 || md.Get("authorization")[0] != "Bearer synthetic-collab-service" || len(md.Get("auriga-tenant-id")) != 1 || md.Get("auriga-tenant-id")[0] != p.tenant || r.AuthenticatedNodeId != p.node {
		return nil, status.Error(codes.PermissionDenied, "sensitive-detail")
	}
	p.calls.Add(1)
	return &collab.CallSharedMessageResponse{ResultJson: []byte(`{"type":"real-collab-response"}`)}, nil
}
func TestSharedMessageHTTPRealCollabRPCComposition(t *testing.T) {
	f := newFixture(t, nil)
	r, _ := messageFixtureRequest(t, f)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	peer := &sharedCollabPeer{tenant: f.tenant, node: f.node}
	collab.RegisterSharedMessageServiceServer(server, peer)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	token := filepath.Join(t.TempDir(), "service-token")
	if err = os.WriteFile(token, []byte("synthetic-collab-service"), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := team.DialRPC(team.RPCConfig{Target: listener.Addr().String(), ServiceTokenFile: token})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	h, err := NewMessageHandler(f.tenant, []string{f.node}, f.gate, RPCMessageCaller{Client: collab.NewSharedMessageServiceClient(client)})
	if err != nil {
		t.Fatal(err)
	}
	out := httptest.NewRecorder()
	h.ServeHTTP(out, r)
	if out.Code != 200 || peer.calls.Load() != 1 || out.Body.String() != `{"type":"real-collab-response"}` {
		t.Fatalf("%d %s", out.Code, out.Body.String())
	}
}
func TestSharedMessageReceiveOperationClosedSetAndRawBudget(t *testing.T) {
	valid := messageRequest{ContextID: uuid.NewString(), Operation: "receiveSet", Frame: json.RawMessage(`{}`)}
	for _, op := range []string{"receiveSet", "fetch", "attemptBegin", "executionAdmit", "receipt"} {
		valid.Operation = op
		if !validMessageOperation("receive", valid) {
			t.Fatal(op)
		}
	}
	for _, op := range []string{"ready", "source_register", "shell", "arbitrary-url"} {
		valid.Operation = op
		if validMessageOperation("receive", valid) {
			t.Fatal(op)
		}
	}
	valid.Operation = "fetch"
	valid.MessageID = uuid.NewString()
	if validMessageOperation("receive", valid) {
		t.Fatal("receive accepted sender fields")
	}
}
