package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
)

const parent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

func TestNATSTraceVectorAndRequestBoundaries(t *testing.T) {
	raw := []byte(`{"request_id":"f20d474e-07d3-48db-9f32-930a43329c02","type":"fetch"}`)
	before := append([]byte(nil), raw...)
	first, second := NATSContext(t.Context(), raw), NATSContext(t.Context(), raw)
	if TraceID(first) != "fd0a7edb479e9a9982f9912dfa755842" || TraceID(first) != TraceID(second) || Parent(first) == Parent(second) || !bytes.Equal(before, raw) {
		t.Fatal("NATS logical trace vector or immutable payload differs")
	}
	if TraceID(NATSContext(t.Context(), nil)) == TraceID(NATSContext(t.Context(), nil)) {
		t.Fatal("background events incorrectly correlated")
	}
}

func TestTraceHTTPToRPCWire(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	seen := make(chan string, 2)
	server := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		seen <- md.Get("traceparent")[0]
		return next(ctx, req)
	}))
	healthpb.RegisterHealthServer(server, health.NewServer())
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, done := Begin(w, r)
		defer done()
		_, err := healthpb.NewHealthClient(conn).Check(Outgoing(r.Context()), &healthpb.HealthCheckRequest{})
		if err != nil {
			http.Error(w, "RPC failed", 502)
			return
		}
		w.WriteHeader(204)
	}))
	defer httpServer.Close()
	for _, incoming := range []string{parent, "forged-trace"} {
		req, _ := http.NewRequestWithContext(t.Context(), "GET", httpServer.URL, nil)
		req.Header.Set("traceparent", incoming)
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		got := <-seen
		if !Valid(got) || got[3:35] != response.Header.Get("X-Trace-Id") {
			t.Fatal("HTTP/RPC/response trace differs")
		}
		if incoming == parent && got[3:35] != parent[3:35] {
			t.Fatal("incoming trace lost")
		}
	}
}

func TestTraceHTTPResponseAndRPC(t *testing.T) {
	q := httptest.NewRequest("POST", "https://example.com/api/ai/v1/network/submit", nil)
	q.Header.Set("traceparent", parent)
	q.Header.Set("X-Request-Id", "01JTESTBUSINESSULID00000000")
	w := httptest.NewRecorder()
	q, finish := Begin(w, q)
	defer finish()
	if w.Header().Get("X-Trace-Id") != parent[3:35] || q.Header.Get("X-Request-Id") != "01JTESTBUSINESSULID00000000" {
		t.Fatal("correlation changed business id")
	}
	md := metadata.Pairs("authorization", "Bearer test-secret", "auriga-tenant-id", "alice-tenant")
	ctx := Outgoing(metadata.NewOutgoingContext(q.Context(), md))
	got, _ := metadata.FromOutgoingContext(ctx)
	if len(got.Get("traceparent")) != 1 || got.Get("traceparent")[0][3:35] != parent[3:35] || got.Get("authorization")[0] != "Bearer test-secret" {
		t.Fatal("RPC context not propagated")
	}
	if len(md.Get("traceparent")) != 0 {
		t.Fatal("mutated caller metadata")
	}
	fresh := Continue(context.Background(), "")
	if TraceID(fresh) == TraceID(ctx) {
		t.Fatal("background trace reused")
	}
}
func TestTraceInvalidExternalValues(t *testing.T) {
	for _, v := range []string{"", strings.Repeat("x", 10000), "ff" + parent[2:], strings.Replace(parent, parent[3:35], strings.Repeat("0", 32), 1), strings.Replace(parent, parent[36:52], strings.Repeat("0", 16), 1), parent + "\nforged"} {
		if Valid(v) {
			t.Fatal("invalid trace accepted")
		}
		q := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
		q.Header.Set("traceparent", v)
		w := httptest.NewRecorder()
		q, done := Begin(w, q)
		done()
		if !Valid(w.Header().Get("traceparent")) || TraceID(q.Context()) == "" {
			t.Fatal("trace not regenerated")
		}
	}
	q := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	q.Header["Traceparent"] = []string{parent, parent}
	w := httptest.NewRecorder()
	_, done := Begin(w, q)
	done()
	if w.Header().Get("X-Trace-Id") == parent[3:35] {
		t.Fatal("duplicate accepted")
	}
}
func TestTraceJSONDebugMaskAndBusinessContext(t *testing.T) {
	t.Setenv("AURIGA_LOG_LEVEL", "DEBUG")
	var out bytes.Buffer
	log := Logger(&out)
	ctx := Continue(context.Background(), parent)
	log.DebugContext(ctx, "failure account_id=alice Authorization: Bearer do-not-log Cookie: session=secret-cookie\n-----BEGIN PRIVATE KEY-----\nprivate-material\n-----END PRIVATE KEY-----", "tenant_id", "tenant-alice", "password", "secret-password", "error", slog.AnyValue("AURIGA_SIGNING_TOKEN=secret-env\nstack caller.go:42"))
	var event map[string]any
	if err := json.Unmarshal(out.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"do-not-log", "secret-cookie", "private-material", "secret-password", "secret-env"} {
		if strings.Contains(out.String(), v) {
			t.Fatalf("secret leaked %s", v)
		}
	}
	if event["trace_id"] != parent[3:35] || event["service"] != "ai-gateway" || event["level"] != "DEBUG" || event["tenant_id"] != "tenant-alice" || !strings.Contains(out.String(), "caller.go:42") {
		t.Fatal(out.String())
	}
}
