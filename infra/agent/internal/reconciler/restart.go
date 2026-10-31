package reconciler

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/state"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Command serializes an authorized restart with this instance's apply/delete work.
func (r *Reconciler) Command(ctx context.Context, command *nodev1.Command) *nodev1.CommandResult {
	if command == nil {
		return commandFailure(nil, "agent.policy_denied", false)
	}
	command = proto.Clone(command).(*nodev1.Command)
	if command.CommandId == "" || len(command.CommandId) > 128 || command.InstanceId == "" || command.GetRestart() == nil {
		return commandFailure(command, "agent.policy_denied", false)
	}
	if ctx.Err() != nil {
		return commandFailure(command, "common.unavailable", true)
	}
	reply := make(chan *nodev1.CommandResult, 1)
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return commandFailure(command, "common.unavailable", true)
	}
	ch, ok := r.workers[command.InstanceId]
	if !ok {
		ch = make(chan workItem, 8)
		r.workers[command.InstanceId] = ch
		r.wg.Add(1)
		go r.loop(command.InstanceId, ch)
	}
	select {
	case ch <- workItem{kind: "restart", command: command, result: reply}:
		r.mu.Unlock()
	default:
		r.mu.Unlock()
		return commandFailure(command, "common.unavailable", true)
	}
	select {
	case result := <-reply:
		return result
	case <-ctx.Done():
		return commandFailure(command, "common.unavailable", true)
	}
}

func commandFailure(command *nodev1.Command, code string, retryable bool) *nodev1.CommandResult {
	return &nodev1.CommandResult{CommandId: command.GetCommandId(), InstanceId: command.GetInstanceId(), Error: &nodev1.InstanceError{Code: code, Message: "Restart command could not be completed", Retryable: retryable}}
}

func (r *Reconciler) handleRestart(ctx context.Context, command *nodev1.Command) *nodev1.CommandResult {
	denied := func() *nodev1.CommandResult { return commandFailure(command, "agent.policy_denied", false) }
	failed := func() *nodev1.CommandResult { return commandFailure(command, "agent.start_failed", true) }
	grace := 30 * time.Second
	if value := command.GetRestart().GetGrace(); value != nil {
		if value.CheckValid() != nil || value.AsDuration() < 0 || value.AsDuration() > r.StartTimeout {
			return denied()
		}
		grace = value.AsDuration()
	}
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(command)
	if err != nil {
		return denied()
	}
	record, err := r.Store.GetRestartCommand(command.CommandId)
	if err != nil {
		return failed()
	}
	if record != nil && !bytes.Equal(record.Request, raw) {
		return denied()
	}
	success := &nodev1.CommandResult{CommandId: command.CommandId, InstanceId: command.InstanceId, Ok: true}
	if record != nil && record.Complete {
		return success
	}
	spec, err := r.Store.GetDesired(command.InstanceId)
	if err != nil {
		return failed()
	}
	if spec == nil || spec.Generation < 1 || spec.Desired != nodev1.DesiredState_DESIRED_STATE_RUNNING {
		return denied()
	}
	if record == nil {
		record = &state.RestartRecord{Request: raw, Generation: spec.Generation}
	}
	// The wire command authorizes applying pending configuration, not a node-made
	// generation. A newer desired spec may arrive while the stop is in progress.
	if spec.Generation < record.Generation {
		return denied()
	}
	record.Generation = spec.Generation
	if err := r.Store.PutRestartCommand(command.CommandId, *record); err != nil {
		if errors.Is(err, state.ErrRestartConflict) {
			return denied()
		}
		return failed()
	}
	if !record.Stopped {
		stopCtx, cancel := context.WithTimeout(ctx, r.StartTimeout)
		err := r.Driver.Stop(stopCtx, command.InstanceId, grace)
		cancel()
		if err != nil {
			return failed()
		}
		record.Stopped = true
		if err := r.Store.PutRestartCommand(command.CommandId, *record); err != nil {
			return failed()
		}
	}
	actual, err := r.Store.GetActual(command.InstanceId)
	if err != nil {
		return failed()
	}
	// A durable stopped checkpoint resumes start without stopping again. If a
	// previous start already reported this generation, normal recovery inspects it.
	if actual.Status == nil || actual.Status.ObservedGeneration != spec.Generation || actual.Status.Phase == nodev1.InstancePhase_INSTANCE_PHASE_PENDING_RESTART {
		if err := r.persist(&nodev1.InstanceStatus{InstanceId: command.InstanceId, ObservedGeneration: spec.Generation, Phase: nodev1.InstancePhase_INSTANCE_PHASE_STOPPED, UpdatedAt: timestamppb.Now()}, 0); err != nil {
			return failed()
		}
	}
	r.handleApply(ctx, command.InstanceId, spec)
	actual, err = r.Store.GetActual(command.InstanceId)
	if err != nil || actual.Status == nil || actual.Status.Phase != nodev1.InstancePhase_INSTANCE_PHASE_RUNNING || !actual.Status.Ready || actual.Status.ObservedGeneration != spec.Generation {
		return failed()
	}
	record.Complete = true
	if err := r.Store.PutRestartCommand(command.CommandId, *record); err != nil {
		return failed()
	}
	return success
}
