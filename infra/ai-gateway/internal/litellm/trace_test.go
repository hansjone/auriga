package litellm

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/observability"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/team"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
)

func TestMemberTraceReachesRPCAndLiteLLM(t *testing.T) {
	const parent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	rpcSpans := make(chan string, 2)
	server := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context, request any, _ *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		span := ""
		if values := md.Get("traceparent"); len(values) == 1 {
			span = values[0]
		}
		rpcSpans <- span
		return next(ctx, request)
	}))
	healthpb.RegisterHealthServer(server, health.NewServer())
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	keyFile := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(keyFile, []byte("sk-fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	conn, err := team.DialRPC(team.RPCConfig{Target: listener.Addr().String(), ServiceTokenFile: keyFile})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	upstreamSpans := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamSpans <- r.Header.Get("traceparent")
		w.WriteHeader(204)
	}))
	defer upstream.Close()
	handler, err := New(upstream.URL, keyFile, func(ctx context.Context, _ string) (Principal, error) {
		// Exercise the production authenticated RPC interceptor for both checks.
		for range 2 {
			if _, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{}); err != nil {
				return Principal{}, err
			}
		}
		return Principal{Tenant: "tenant", Account: "member", Device: "device", Expires: time.Now().Add(time.Minute)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	request := httptest.NewRequest("GET", "/ai/v1/models", nil)
	request.Header.Set("Authorization", "Bearer device-fixture")
	request.Header.Set("traceparent", parent)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	local := response.Header().Get("traceparent")
	if response.Code != 204 || !observability.Valid(local) || local == parent || local[3:35] != parent[3:35] {
		t.Fatalf("member request did not create a child span: status=%d", response.Code)
	}
	for range 2 {
		if got := <-rpcSpans; got != local {
			t.Fatal("IAM/org RPC did not receive the member request span")
		}
	}
	if upstreamSpan := <-upstreamSpans; upstreamSpan != local {
		t.Fatal("LiteLLM did not receive the local child span")
	}
}
