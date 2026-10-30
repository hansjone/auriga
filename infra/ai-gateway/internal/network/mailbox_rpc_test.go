package network

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/team"
	collab "github.com/hansjone/auriga/infra/gen/auriga/collab/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestMailboxPreservesDurableUnknownOutcomeWithoutTreatingItAsOverload(t *testing.T) {
	result := mailboxError(status.Error(codes.Unavailable, "network.outcome_unknown"))
	if result.Code != "network.outcome_unknown" || result.Scope != "request" || result.Retryable {
		t.Fatalf("source unknown outcome was reclassified: %+v", result)
	}
	hostile := mailboxError(status.Error(codes.Unavailable, "network.outcome_unknown private-detail"))
	if hostile.Code != "network.overloaded" {
		t.Fatal("arbitrary status details crossed boundary")
	}
}

type fakeMailboxRPC struct {
	collab.UnimplementedNetworkMailboxServiceServer
	mu         sync.Mutex
	calls      []string
	identities []*collab.NetworkMailboxConnection
	frames     [][]byte
	response   []byte
	failure    error
	block      bool
	entered    chan struct{}
	release    chan struct{}
}

func (f *fakeMailboxRPC) call(ctx context.Context, op string, id *collab.NetworkMailboxConnection, raw []byte) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, op)
	f.identities = append(f.identities, id)
	f.frames = append(f.frames, raw)
	response, failure, block := f.response, f.failure, f.block
	f.mu.Unlock()
	if f.entered != nil {
		select {
		case f.entered <- struct{}{}:
		default:
		}
	}
	if f.release != nil {
		select {
		case <-f.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return response, failure
}
func (f *fakeMailboxRPC) Ready(ctx context.Context, r *collab.NetworkMailboxServiceReadyRequest) (*collab.NetworkMailboxServiceReadyResponse, error) {
	b, e := f.call(ctx, "ready", r.Connection, r.FrameJson)
	return &collab.NetworkMailboxServiceReadyResponse{FrameJson: b}, e
}
func (f *fakeMailboxRPC) Head(ctx context.Context, r *collab.NetworkMailboxServiceHeadRequest) (*collab.NetworkMailboxServiceHeadResponse, error) {
	b, e := f.call(ctx, "head", r.Connection, nil)
	return &collab.NetworkMailboxServiceHeadResponse{FrameJson: b}, e
}
func (f *fakeMailboxRPC) Fetch(ctx context.Context, r *collab.NetworkMailboxServiceFetchRequest) (*collab.NetworkMailboxServiceFetchResponse, error) {
	b, e := f.call(ctx, "fetch", r.Connection, r.FrameJson)
	return &collab.NetworkMailboxServiceFetchResponse{FrameJson: b}, e
}
func (f *fakeMailboxRPC) Receipt(ctx context.Context, r *collab.NetworkMailboxServiceReceiptRequest) (*collab.NetworkMailboxServiceReceiptResponse, error) {
	b, e := f.call(ctx, "receipt", r.Connection, r.FrameJson)
	return &collab.NetworkMailboxServiceReceiptResponse{FrameJson: b}, e
}

func (f *fakeMailboxRPC) LocalRegister(ctx context.Context, r *collab.NetworkMailboxServiceLocalRegisterRequest) (*collab.NetworkMailboxServiceLocalRegisterResponse, error) {
	b, e := f.call(ctx, "local_register", r.Connection, r.FrameJson)
	return &collab.NetworkMailboxServiceLocalRegisterResponse{FrameJson: b}, e
}

func startMailboxRPC(t *testing.T, f collab.NetworkMailboxServiceServer) team.RPCConfig {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	token := randHex(t, 32)
	file := filepath.Join(t.TempDir(), "service.jwt")
	if err = os.WriteFile(file, []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		connection := req.(interface {
			GetConnection() *collab.NetworkMailboxConnection
		}).GetConnection()
		if got := md.Get("authorization"); len(got) != 1 || got[0] != "Bearer "+token {
			t.Error("missing service JWT")
		}
		if got := md.Get("auriga-tenant-id"); len(got) != 1 || got[0] != connection.GetTenantId() {
			t.Error("wrong delegated tenant")
		}
		return handler(ctx, req)
	}))
	collab.RegisterNetworkMailboxServiceServer(srv, f)
	done := make(chan struct{})
	go func() { defer close(done); _ = srv.Serve(listener) }()
	t.Cleanup(func() { srv.Stop(); _ = listener.Close(); <-done })
	return team.RPCConfig{Target: listener.Addr().String(), ServiceTokenFile: file}
}

func TestMailboxRPCPreservesCompleteFramesAndTrustedIdentity(t *testing.T) {
	f := &fakeMailboxRPC{response: []byte(`{"opaque":"signed response bytes"}`)}
	conn, err := team.DialRPC(startMailboxRPC(t, f))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	client, err := NewMailboxClient(conn, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	b := liveBinding{Binding: Binding{ServerID: "server", ClientID: 7, Account: clientAccount, Ingress: ingressForTicket("x")}, Tenant: testTenant, Realm: "2", Node: "64"}
	raw := []byte(`{"type":"fetch","continuation":"opaque","tenant":"forged","results":[{"message_digest":"proof"}]}`)
	for _, op := range []string{"ready", "head", "fetch", "receipt"} {
		got, e := client.Call(context.Background(), b, op, raw)
		if e != nil || string(got) != string(f.response) {
			t.Fatalf("%s: %s %v", op, got, e)
		}
	}
	for i, id := range f.identities {
		if id.TenantId != b.Tenant || id.Realm != b.Realm || id.Node != b.Node || id.Ingress != b.Ingress || id.ServerId != b.ServerID || id.ClientId != "7" || id.ClientAccount != b.Account {
			t.Fatalf("untrusted binding: %+v", id)
		}
		if i != 1 && string(f.frames[i]) != string(raw) {
			t.Fatal("request evidence was lost")
		}
	}
}

func TestMailboxRPCFailuresNeverProduceSuccess(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failure error
		block   bool
		want    string
	}{
		{"timeout", nil, true, "network.overloaded"},
		{"unavailable", status.Error(codes.Unavailable, "private backend detail"), false, "network.overloaded"},
		{"denied", status.Error(codes.PermissionDenied, "private backend detail"), false, "network.unauthorized"},
		{"conflict", status.Error(codes.Aborted, "network.conflict"), false, CodeConflict},
		{"unknown", status.Error(codes.Internal, "network.made_up"), false, "network.overloaded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeMailboxRPC{failure: tc.failure, block: tc.block}
			conn, err := team.DialRPC(startMailboxRPC(t, f))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conn.Close() }()
			client, err := NewMailboxClient(conn, 30*time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			raw, e := client.Call(context.Background(), liveBinding{Tenant: testTenant}, "fetch", []byte(`{}`))
			if len(raw) != 0 || e == nil || e.Code != tc.want {
				t.Fatalf("raw=%s err=%v", raw, e)
			}
		})
	}
}

func decodeObject(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestMailboxRPCUnreachableFailsClosed(t *testing.T) {
	cfg := startMailboxRPC(t, &fakeMailboxRPC{})
	cfg.Target = "127.0.0.1:0"
	conn, err := team.DialRPC(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	client, err := NewMailboxClient(conn, 30*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	raw, ae := client.Call(context.Background(), liveBinding{Tenant: testTenant}, "fetch", []byte(`{}`))
	if len(raw) != 0 || ae == nil || ae.Code != "network.overloaded" || !ae.Retryable {
		t.Fatalf("unreachable collab returned %s %v", raw, ae)
	}
}

func TestMailboxRPCResponseSizeBound(t *testing.T) {
	for _, size := range []int{0, EncodingMaxPayloadBytes + 1, EncodingMaxPayloadBytes * 2} {
		t.Run(u64str(uint64(size)), func(t *testing.T) {
			f := &fakeMailboxRPC{response: []byte(strings.Repeat("x", size))}
			conn, err := team.DialRPC(startMailboxRPC(t, f))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conn.Close() }()
			client, err := NewMailboxClient(conn, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if data, ae := client.Call(context.Background(), liveBinding{Tenant: testTenant}, "fetch", []byte(`{}`)); len(data) != 0 || ae == nil {
				t.Fatal("invalid response size became success")
			}
		})
	}
}
