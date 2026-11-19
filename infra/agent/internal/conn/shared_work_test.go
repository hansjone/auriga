package conn

import (
	"context"
	"fmt"
	"github.com/hansjone/auriga/infra/agent/internal/reconciler"
	runtime "github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/state"
	"github.com/hansjone/auriga/infra/agent/internal/storage"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

type blockedSharedQuota struct {
	entered, canceled chan struct{}
	calls             atomic.Int32
}

func (q *blockedSharedQuota) Ensure(ctx context.Context, _ string, _, _ int64) error {
	q.calls.Add(1)
	select {
	case q.entered <- struct{}{}:
	default:
	}
	<-ctx.Done()
	select {
	case q.canceled <- struct{}{}:
	default:
	}
	return ctx.Err()
}
func TestSharedWorkDoesNotBlockOrdinaryApplyDeleteCommand(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	quota := &blockedSharedQuota{entered: make(chan struct{}, 1), canceled: make(chan struct{}, 1)}
	store, e := state.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	}()
	rec := reconciler.New(store, runtime.NewFake(), slog.New(slog.NewTextHandler(io.Discard, nil)), 1)
	defer rec.Close()
	absent := make(chan struct{}, 1)
	applied := make(chan struct{}, 1)
	rec.Sink = func(s *nodev1.InstanceStatus) {
		if s.Phase == nodev1.InstancePhase_INSTANCE_PHASE_RUNNING {
			select {
			case applied <- struct{}{}:
			default:
			}
		}
		if s.Phase == nodev1.InstancePhase_INSTANCE_PHASE_ABSENT {
			select {
			case absent <- struct{}{}:
			default:
			}
		}
	}
	session := New(Config{Store: store, Reconciler: rec, SharedPools: &runtime.SharedPools{Layout: &storage.PoolLayout{Root: t.TempDir(), Quota: quota}}})
	t.Cleanup(func() {
		cancel()
		ctx, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		if e := session.sharedWork.wait(ctx); e != nil {
			t.Error(e)
		}
	})
	stream := &startupReceiverStream{frames: make(chan *nodev1.SessionResponse, 8), polls: make(chan struct{}, 16)}
	done := make(chan error, 1)
	go func() { done <- session.recvLoop(ctx, stream) }()
	intent := preparationIntent(3)
	frame := &nodev1.SessionResponse{Body: &nodev1.SessionResponse_SharedDshRuntimeIntent{SharedDshRuntimeIntent: intent}}
	stream.frames <- frame
	select {
	case <-quota.entered:
	case <-time.After(time.Second):
		t.Fatal("quota not reached")
	}
	for i := 0; i < 100; i++ {
		if e = session.handleControl(ctx, frame); e != nil {
			t.Fatal(e)
		}
	}
	stream.frames <- &nodev1.SessionResponse{Body: &nodev1.SessionResponse_Apply{Apply: &nodev1.ApplySpec{Spec: &nodev1.InstanceSpec{InstanceId: "ordinary", Generation: 1, Desired: nodev1.DesiredState_DESIRED_STATE_RUNNING}}}}
	select {
	case <-applied:
	case <-time.After(time.Second):
		t.Fatal("ordinary Apply not processed")
	}
	stream.frames <- &nodev1.SessionResponse{Body: &nodev1.SessionResponse_Delete{Delete: &nodev1.DeleteInstance{InstanceId: "ordinary", Generation: 2}}}
	stream.frames <- &nodev1.SessionResponse{Body: &nodev1.SessionResponse_Command{Command: &nodev1.Command{CommandId: "00000000-0000-4000-8000-000000000099", InstanceId: "ordinary"}}}
	close(stream.frames)
	select {
	case err := <-done:
		if err == nil || err.Error() != "session closed by server" {
			t.Fatalf("unexpected closed stream: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ordinary control frames blocked by quota")
	}
	select {
	case <-absent:
	case <-time.After(time.Second):
		t.Fatal("ordinary Delete not processed")
	}
	select {
	case <-session.resultCh:
	case <-time.After(time.Second):
		t.Fatal("ordinary Command blocked")
	}
	if quota.calls.Load() != 1 {
		t.Fatal("duplicate work reached quota")
	}
	cancel()
	select {
	case <-quota.canceled:
	case <-time.After(time.Second):
		t.Fatal("connection did not cancel quota")
	}
}
func TestSharedWorkBoundedQueueTimeoutAndReconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	q := &blockedSharedQuota{entered: make(chan struct{}, 8), canceled: make(chan struct{}, 8)}
	session := New(Config{SharedPools: &runtime.SharedPools{Layout: &storage.PoolLayout{Root: t.TempDir(), Quota: q}}})
	t.Cleanup(func() {
		cancel()
		ctx, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		if e := session.sharedWork.wait(ctx); e != nil {
			t.Error(e)
		}
	})
	session.sharedWork.timeout = 200 * time.Millisecond
	frame := func(i int) *nodev1.SessionResponse {
		return &nodev1.SessionResponse{Body: &nodev1.SessionResponse_SharedDshRuntimeIntent{SharedDshRuntimeIntent: preparationIntent(i)}}
	}
	if e := session.handleControl(ctx, frame(3)); e != nil {
		t.Fatal(e)
	}
	<-q.entered
	accepted := 0
	for i := 4; i < sharedQueueSize+14; i++ {
		if session.handleControl(ctx, frame(i)) == nil {
			accepted++
		}
	}
	if accepted != sharedQueueSize {
		t.Fatalf("queue not bounded: %d", accepted)
	}
	cancel()
	next, stop := context.WithCancel(context.Background())
	defer stop()
	session.sharedWork.reset(next)
	if e := session.handleControl(next, frame(4)); e != nil {
		t.Fatal(e)
	}
	select {
	case <-q.entered:
	case <-time.After(time.Second):
		t.Fatal("reconnect did not reclaim queue")
	}
	select {
	case <-q.canceled:
	case <-time.After(time.Second):
		t.Fatal("job timeout/cancel not delivered")
	}
	if q.calls.Load() > 2 {
		t.Fatal(fmt.Sprint("canceled backlog executed: ", q.calls.Load()))
	}
}
