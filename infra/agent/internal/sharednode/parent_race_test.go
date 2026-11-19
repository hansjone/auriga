package sharednode

import (
	"context"
	"encoding/json"
	iamv1 "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"net"
	"sync/atomic"
	"testing"
)

type lateParentFinishIAM struct {
	*testAuthority
	relay       *Relay
	armed       atomic.Bool
	childBegins atomic.Int32
}

func (a *lateParentFinishIAM) AuthorizeSharedCapability(ctx context.Context, in *iamv1.AuthorizeSharedCapabilityRequest) (*iamv1.AuthorizeSharedCapabilityResponse, error) {
	out, err := a.testAuthority.AuthorizeSharedCapability(ctx, in)
	if err == nil && in.Capability.TaskId == testTask && a.armed.CompareAndSwap(true, false) {
		// Authorization was true when read, but its response arrives after the
		// parent Finish, just as a network-delayed valid authorization can do.
		if err = a.relay.finish(a.binding, testTask); err != nil {
			return nil, err
		}
	}
	return out, err
}
func (a *lateParentFinishIAM) BeginSharedTask(ctx context.Context, in *iamv1.BeginSharedTaskRequest) (*iamv1.BeginSharedTaskResponse, error) {
	if in.TaskId == testPool {
		a.check(ctx)
		a.childBegins.Add(1)
		return nil, status.Error(codes.Unavailable, "synthetic-unknown-child-begin")
	}
	return a.testAuthority.BeginSharedTask(ctx, in)
}
func TestChildAuthorizationAfterParentFinishCannotRegister(t *testing.T) {
	r, base := fixture(t)
	base.binding.Mode = nodev1.DshInstanceMode_DSH_INSTANCE_MODE_SHARED
	gate := &lifecycleAdmission{}
	r.Admission = gate
	authority := &lateParentFinishIAM{testAuthority: base, relay: r}
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	iamv1.RegisterSharedTaskServiceServer(server, authority)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///review-parent-race", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	r.Client.IAM = conn
	begin(t, r, base, "auriga.shared.enterprise", []string{"task.execute"})
	authority.armed.Store(true)
	binding, _ := wire(base.binding)
	raw, _ := json.Marshal(map[string]any{"binding": binding, "taskId": testPool, "audience": "auriga.shared.enterprise", "scopes": []string{"outbound"}, "parentCapability": "synthetic-capability-1"})
	if _, err := r.invoke(t.Context(), "beginTask", raw); err == nil {
		t.Fatal("unknown child unexpectedly succeeded")
	}
	r.mu.Lock()
	_, parentExists := r.tasks[testTask]
	child := r.tasks[testPool]
	r.mu.Unlock()
	gate.mu.Lock()
	active, completed := len(gate.active), gate.completed
	gate.mu.Unlock()
	t.Logf("childIAMBegins=%d parentExists=%t childClosing=%t childUnknown=%t reservedRoots=%d completedRoots=%d", authority.childBegins.Load(), parentExists, child.closing, child.uncertainBegin, active, completed)
	if authority.childBegins.Load() != 0 {
		t.Fatal("child was dispatched after parent Finish released its root; unknown child is outside all admission slots")
	}
}
