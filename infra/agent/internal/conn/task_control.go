package conn

import (
	"context"
	"fmt"
	"time"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	workspacev1 "github.com/hansjone/auriga/infra/gen/auriga/workspace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

type controlTask struct {
	binding *nodev1.SharedDshSessionBinding
	epoch   uint64
}

func (s *Session) taskConnection() (*grpc.ClientConn, context.Context, uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.connected || s.cfg.AllowInsecure || s.controlConn == nil || s.controlContext == nil || s.controlContext.Err() != nil {
		return nil, nil, 0, fmt.Errorf("authenticated NodeControl unavailable")
	}
	return s.controlConn, s.controlContext, s.connectionEpoch, nil
}

func (s *Session) sameTaskConnection(conn *grpc.ClientConn, epoch uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connected && s.controlConn == conn && s.connectionEpoch == epoch && s.controlContext != nil && s.controlContext.Err() == nil
}

// AdmitSharedTask multiplexes on the ClientConn that owns the live Session.
// Sending field12 is bounded and acknowledged by the actual serialized Send
// loop; an unavailable W observation remains a real failure, never a fake ACK.
func (s *Session) AdmitSharedTask(ctx context.Context, b *nodev1.SharedDshSessionBinding, id string, sample *nodev1.SharedDshPoolMemorySample) error {
	conn, live, epoch, err := s.taskConnection()
	if err != nil {
		return err
	}
	if b == nil || !validPoolMemory(sample) || sample.TenantId != b.TenantId || sample.PhysicalInstanceId != b.PhysicalInstanceId {
		return fmt.Errorf("invalid admission memory binding")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	stop := context.AfterFunc(live, cancel)
	defer stop()
	queued := queuedPoolMemory{sample: proto.Clone(sample).(*nodev1.SharedDshPoolMemorySample), epoch: epoch, sent: make(chan error, 1)}
	select {
	case s.poolMemoryCh <- queued:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err = <-queued.sent:
		if err != nil {
			return err
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	if !s.sameTaskConnection(conn, epoch) || ctx.Err() != nil {
		return fmt.Errorf("NodeControl changed before Admit")
	}
	// No internal service JWT or caller metadata is forwarded onto Node mTLS.
	out, err := workspacev1.NewSharedDshServiceClient(conn).AdmitSharedDshTask(metadata.NewOutgoingContext(ctx, nil), &workspacev1.AdmitSharedDshTaskRequest{Binding: b, TaskId: id})
	if err != nil {
		return err
	}
	if out == nil || !out.Admitted || out.TaskId != id || !proto.Equal(out.Binding, b) || ctx.Err() != nil || !s.sameTaskConnection(conn, epoch) {
		return fmt.Errorf("unconfirmed W Admit")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.connected || s.controlConn != conn || s.connectionEpoch != epoch || live.Err() != nil {
		return fmt.Errorf("late W Admit")
	}
	if s.controlTasks == nil {
		s.controlTasks = map[string]controlTask{}
	}
	if old, exists := s.controlTasks[id]; exists && !proto.Equal(old.binding, b) {
		return fmt.Errorf("workspace task binding changed")
	}
	s.controlTasks[id] = controlTask{binding: proto.Clone(b).(*nodev1.SharedDshSessionBinding), epoch: epoch}
	return nil
}

func (s *Session) CheckSharedTask(b *nodev1.SharedDshSessionBinding, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.controlTasks[id]
	if !ok || !s.connected || s.controlConn == nil || s.controlContext == nil || s.controlContext.Err() != nil || task.epoch != s.connectionEpoch || !proto.Equal(task.binding, b) {
		return fmt.Errorf("workspace task lease no longer current")
	}
	return nil
}

// Release may drain an old reservation over a new current session. W checks
// the owning Node and exact binding/task; no old holder's success is adopted.
func (s *Session) ReleaseSharedTask(ctx context.Context, b *nodev1.SharedDshSessionBinding, id string) error {
	conn, live, epoch, err := s.taskConnection()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	stop := context.AfterFunc(live, cancel)
	defer stop()
	out, err := workspacev1.NewSharedDshServiceClient(conn).ReleaseSharedDshTask(metadata.NewOutgoingContext(ctx, nil), &workspacev1.ReleaseSharedDshTaskRequest{Binding: b, TaskId: id})
	if err != nil {
		return err
	}
	if out == nil || !out.Fenced || out.TaskId != id || !proto.Equal(out.Binding, b) || ctx.Err() != nil || !s.sameTaskConnection(conn, epoch) {
		return fmt.Errorf("unconfirmed W Release")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.connected || s.controlConn != conn || s.connectionEpoch != epoch || live.Err() != nil {
		return fmt.Errorf("late W Release")
	}
	if old, exists := s.controlTasks[id]; exists && !proto.Equal(old.binding, b) {
		return fmt.Errorf("workspace release binding changed")
	}
	delete(s.controlTasks, id)
	return nil
}
