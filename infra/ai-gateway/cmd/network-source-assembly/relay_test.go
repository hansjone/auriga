package main

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	collab "github.com/hansjone/auriga/infra/gen/auriga/collab/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// The deadline timer is controlled independently from the advertised deadline,
// so transport completion and timer delivery can be tested without a race.
type relayDeadlineContext struct {
	context.Context
	deadline time.Time
	done     chan struct{}
}

func (c *relayDeadlineContext) Deadline() (time.Time, bool) { return c.deadline, true }
func (c *relayDeadlineContext) Done() <-chan struct{}       { return c.done }
func (c *relayDeadlineContext) Err() error {
	select {
	case <-c.done:
		return context.DeadlineExceeded
	default:
		return nil
	}
}

func TestRelayReadyDiagnosticsClassifiesExpiredDeadlineBeforeContextTimer(t *testing.T) {
	r, err := startRelay(context.Background(), "127.0.0.1:0", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer r.close()
	ctx := &relayDeadlineContext{Context: context.Background(), deadline: time.Now().Add(-time.Second), done: make(chan struct{})}
	_, err = r.Ready(ctx, &collab.NetworkMailboxServiceReadyRequest{Connection: &collab.NetworkMailboxConnection{TenantId: "tenant"}})
	if err == nil || ctx.Err() != nil {
		t.Fatalf("did not exercise transport expiry before context timer: %v / %v", err, ctx.Err())
	}
	event := r.trace()[0]
	if event["contextCode"] != "DeadlineExceeded" || event["deadlineExpired"] != true || event["code"] != status.Code(err).String() {
		t.Fatalf("expired incoming deadline misclassified: %v", event)
	}
}

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
			budget := 5 * time.Second // A failure guard, never the deadline trigger.
			ctx, cancel := context.WithTimeout(context.Background(), budget)
			defer cancel()
			stop := cancel
			if mode == "deadline" {
				deadline, _ := ctx.Deadline()
				controlled := &relayDeadlineContext{Context: ctx, deadline: deadline, done: make(chan struct{})}
				var once sync.Once
				stop = func() { once.Do(func() { close(controlled.done) }) }
				defer stop()
				ctx = controlled
			}
			ctx = metadata.NewIncomingContext(ctx, metadata.Pairs("authorization", "Bearer private-credential-marker"))
			result := make(chan error, 1)
			go func() {
				_, callErr := r.Ready(ctx, &collab.NetworkMailboxServiceReadyRequest{Connection: &collab.NetworkMailboxConnection{TenantId: "tenant"}, FrameJson: []byte(`{"type":"ready"}`)})
				result <- callErr
			}()
			select {
			case <-guard.entered:
			case <-time.After(budget):
				t.Fatal("downstream RPC never entered")
			}
			if mode != "downstream" {
				stop()
			}
			select {
			case err = <-result:
			case <-time.After(budget):
				t.Fatal("relay RPC did not finish")
			}
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
