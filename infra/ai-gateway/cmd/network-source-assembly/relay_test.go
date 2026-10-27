package main

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	collab "github.com/hansjone/auriga/infra/gen/auriga/collab/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Transport guards only, not IAM/collab business or authentication acceptance.
type relayReadyGuard struct {
	collab.UnimplementedNetworkMailboxServiceServer
	entered chan struct{}
	failure bool
}

func (s *relayReadyGuard) Ready(ctx context.Context, _ *collab.NetworkMailboxServiceReadyRequest) (*collab.NetworkMailboxServiceReadyResponse, error) {
	close(s.entered)
	if s.failure {
		return nil, status.Error(codes.Unavailable, "private-backend-marker")
	}
	<-ctx.Done()
	return nil, status.FromContextError(ctx.Err()).Err()
}

func TestRelayReadyDiagnosticsPreserveDeadlineCancellationAndHidePrivateDetails(t *testing.T) {
	for _, mode := range []string{"deadline", "cancel", "downstream"} {
		t.Run(mode, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			server := grpc.NewServer()
			guard := &relayReadyGuard{entered: make(chan struct{}), failure: mode == "downstream"}
			collab.RegisterNetworkMailboxServiceServer(server, guard)
			done := make(chan struct{})
			go func() { defer close(done); _ = server.Serve(listener) }()
			defer func() { server.Stop(); _ = listener.Close(); <-done }()
			target := listener.Addr().String()
			r, err := startRelay(context.Background(), target, target)
			if err != nil {
				t.Fatal(err)
			}
			defer r.close()
			budget := time.Second
			if mode == "deadline" {
				budget = 80 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), budget)
			defer cancel()
			if mode == "cancel" {
				go func(callContext context.Context) {
					select {
					case <-guard.entered:
						cancel()
					case <-callContext.Done():
					}
				}(ctx)
			}
			ctx = metadata.NewIncomingContext(ctx, metadata.Pairs("authorization", "Bearer private-credential-marker"))
			_, err = r.Ready(ctx, &collab.NetworkMailboxServiceReadyRequest{Connection: &collab.NetworkMailboxConnection{TenantId: "tenant"}, FrameJson: []byte(`{"type":"ready"}`)})
			if err == nil {
				t.Fatal("transport failure became success")
			}
			trace := r.trace()
			if len(trace) != 1 {
				t.Fatal("missing real failed RPC trace")
			}
			event := trace[0]
			if event["peer"] != target || event["rpc"] != collab.NetworkMailboxService_Ready_FullMethodName {
				t.Fatal("missing actual generated peer/method")
			}
			wantContext := map[string]string{"deadline": "DeadlineExceeded", "cancel": "Canceled", "downstream": "OK"}[mode]
			if event["contextCode"] != wantContext || event["code"] != status.Code(err).String() {
				t.Fatalf("context/status classification mismatch: %v", event)
			}
			remaining, ok := event["remainingMillisAtStart"].(int64)
			if !ok || remaining < 0 || remaining > budget.Milliseconds() {
				t.Fatal("incoming deadline lost or extended")
			}
			elapsed, ok := event["elapsedMillis"].(int64)
			if !ok || elapsed < 0 {
				t.Fatal("missing elapsed time")
			}
			raw, err := json.Marshal(trace)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "private-backend-marker") || strings.Contains(string(raw), "private-credential-marker") {
				t.Fatal("private status or JWT leaked into diagnostics")
			}
		})
	}
}
