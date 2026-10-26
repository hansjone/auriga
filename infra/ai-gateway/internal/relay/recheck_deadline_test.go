package relay

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/modelgate"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/team"
	iam "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
	org "github.com/hansjone/auriga/infra/gen/auriga/org/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type deadlineDevice struct {
	iam.UnimplementedDeviceAccessServiceServer
	entered, exited chan struct{}
	cause           chan error
}

type deadlineRPC struct {
	grpc.ClientConnInterface
	code codes.Code
}

func (c *deadlineRPC) Invoke(ctx context.Context, method string, args, reply any, opts ...grpc.CallOption) error {
	err := c.ClientConnInterface.Invoke(ctx, method, args, reply, opts...)
	c.code = status.Code(err)
	return err
}

func (s *deadlineDevice) VerifyDeviceAiAccess(ctx context.Context, _ *iam.VerifyDeviceAiAccessRequest) (*iam.VerifyDeviceAiAccessResponse, error) {
	defer close(s.exited)
	close(s.entered)
	<-ctx.Done()
	s.cause <- ctx.Err()
	return nil, status.FromContextError(ctx.Err()).Err()
}

// The real gRPC deadline is independent of parent Done propagation. Authority
// deliberately sanitizes it, so testing errors.Is(RecheckError, DeadlineExceeded)
// alone cannot fix the relay's classification race.
func TestOnlineCheckDeadlineBeforeParentCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	rpcDevice := &deadlineDevice{entered: make(chan struct{}), exited: make(chan struct{}), cause: make(chan error, 1)}
	server := grpc.NewServer()
	iam.RegisterDeviceAccessServiceServer(server, rpcDevice)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	tokenFile := filepath.Join(t.TempDir(), "service.jwt")
	if err = os.WriteFile(tokenFile, []byte("fixture-service"), 0600); err != nil {
		t.Fatal(err)
	}
	conn, err := team.DialRPC(team.RPCConfig{Target: listener.Addr().String(), ServiceTokenFile: tokenFile})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	rpc := &deadlineRPC{ClientConnInterface: conn}
	authority, err := modelgate.NewAuthority(iam.NewDeviceAccessServiceClient(rpc), org.NewMemberServiceClient(conn), org.NewAuthorizationServiceClient(conn), org.NewQuotaServiceClient(conn), "USD", time.Now)
	if err != nil {
		t.Fatal(err)
	}
	claims, _ := json.Marshal(map[string]any{"tid": provider, "sub": account, "did": device, "exp": time.Now().Add(time.Minute).Unix()})
	raw := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"ES256","typ":"at+jwt"}`)) + "." + base64.RawURLEncoding.EncodeToString(claims) + ".ZmFrZQ"
	ctx := delayedDeadline{Context: t.Context(), deadline: time.Now().Add(time.Second)}
	_, err = authority.Check(ctx, raw)
	if err == nil || err.Error() != "ai.budget_unavailable" || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil || rpc.code != codes.DeadlineExceeded {
		t.Fatalf("deadline/cancellation ordering not reproduced: check=%v parent=%v", err, ctx.Err())
	}
	select {
	case <-rpcDevice.exited:
		// The client may cancel the server at its own RPC deadline first.
		if cause := <-rpcDevice.cause; cause != context.DeadlineExceeded && cause != context.Canceled {
			t.Fatal("RPC worker did not cancel", cause)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RPC worker did not exit")
	}
	select {
	case <-rpcDevice.entered:
	default:
		t.Fatal("RPC worker never entered")
	}
	if time.Now().Before(ctx.deadline) {
		t.Fatal("execution deadline has not elapsed")
	}
}
