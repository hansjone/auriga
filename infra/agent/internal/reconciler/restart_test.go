package reconciler_test

import (
	"context"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/reconciler"
	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/state"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestRestartCommandAppliesPendingGenerationAndReplaysWithoutStopping(t *testing.T) {
	dir := t.TempDir()
	store, err := state.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	driver := &countedLifecycleDriver{Fake: runtime.NewFake()}
	rec := reconciler.New(store, driver, nil, 1)
	statuses := make(chan *nodev1.InstanceStatus, 32)
	rec.Sink = func(s *nodev1.InstanceStatus) { statuses <- s }
	apply := func(gen int64, phase nodev1.InstancePhase) {
		t.Helper()
		if err := rec.Apply(&nodev1.InstanceSpec{InstanceId: "restart-test", Generation: gen, Desired: nodev1.DesiredState_DESIRED_STATE_RUNNING}); err != nil {
			t.Fatal(err)
		}
		timer := time.NewTimer(3 * time.Second)
		defer timer.Stop()
		for {
			select {
			case s := <-statuses:
				if s.Phase == phase {
					return
				}
			case <-timer.C:
				t.Fatal("missing phase", phase)
			}
		}
	}
	apply(1, nodev1.InstancePhase_INSTANCE_PHASE_RUNNING)
	apply(2, nodev1.InstancePhase_INSTANCE_PHASE_PENDING_RESTART)
	command := &nodev1.Command{CommandId: "restart-command", InstanceId: "restart-test", Kind: &nodev1.Command_Restart{Restart: &nodev1.RestartCommand{Grace: durationpb.New(time.Second), Reason: "user"}}}
	result := rec.Command(context.Background(), command)
	if !result.Ok || result.CommandId != command.CommandId || result.InstanceId != command.InstanceId {
		t.Fatalf("restart failed: %v", result)
	}
	actual, err := store.GetActual(command.InstanceId)
	if err != nil || actual.Status == nil || !actual.Status.Ready || actual.Status.Phase != nodev1.InstancePhase_INSTANCE_PHASE_RUNNING || actual.Status.ObservedGeneration != 2 || len(actual.Status.RestartReasons) != 0 {
		t.Fatalf("pending did not converge: %+v %v", actual, err)
	}
	if driver.starts.Load() != 2 || driver.stops.Load() != 1 {
		t.Fatal("restart did not stop/start once")
	}
	rec.Close()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = state.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	rec = reconciler.New(store, driver, nil, 1)
	defer rec.Close()
	if !rec.Command(context.Background(), command).Ok || driver.stops.Load() != 1 || driver.starts.Load() != 2 {
		t.Fatal("durable replay restarted the process")
	}
	changed := &nodev1.Command{CommandId: command.CommandId, InstanceId: "another-instance", Kind: command.Kind}
	if got := rec.Command(context.Background(), changed); got.Ok || got.Error.GetCode() != "agent.policy_denied" {
		t.Fatalf("command collision accepted: %v", got)
	}
}

func TestRestartCommandRejectsUnknownStoppedUnsupportedAndInvalidGrace(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	driver := &countedLifecycleDriver{Fake: runtime.NewFake()}
	rec := reconciler.New(store, driver, nil, 1)
	defer rec.Close()
	if err := store.PutDesired(&nodev1.InstanceSpec{InstanceId: "stopped", Generation: 2, Desired: nodev1.DesiredState_DESIRED_STATE_STOPPED}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutDesired(&nodev1.InstanceSpec{InstanceId: "running", Generation: 1, Desired: nodev1.DesiredState_DESIRED_STATE_RUNNING}); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []*nodev1.Command{
		{CommandId: "unknown", InstanceId: "missing", Kind: &nodev1.Command_Restart{Restart: &nodev1.RestartCommand{}}},
		{CommandId: "stopped", InstanceId: "stopped", Kind: &nodev1.Command_Restart{Restart: &nodev1.RestartCommand{}}},
		{CommandId: "unsupported", InstanceId: "stopped", Kind: &nodev1.Command_Snapshot{Snapshot: &nodev1.SnapshotCommand{}}},
		{CommandId: "negative", InstanceId: "running", Kind: &nodev1.Command_Restart{Restart: &nodev1.RestartCommand{Grace: durationpb.New(-time.Second)}}},
		{CommandId: "excessive", InstanceId: "running", Kind: &nodev1.Command_Restart{Restart: &nodev1.RestartCommand{Grace: durationpb.New(time.Hour)}}},
		{InstanceId: "stopped", Kind: &nodev1.Command_Restart{Restart: &nodev1.RestartCommand{}}},
	} {
		if got := rec.Command(context.Background(), cmd); got.Ok || got.Error.GetCode() != "agent.policy_denied" {
			t.Fatalf("unsafe command accepted: %v", got)
		}
	}
	if driver.stops.Load() != 0 || driver.starts.Load() != 0 {
		t.Fatal("rejected command touched runtime")
	}
}

func TestRestartCommandContinuesAfterStopAndStartFailures(t *testing.T) {
	dir := t.TempDir()
	store, err := state.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	driver := &countedLifecycleDriver{Fake: runtime.NewFake()}
	spec := &nodev1.InstanceSpec{InstanceId: "retry-instance", Generation: 2, Desired: nodev1.DesiredState_DESIRED_STATE_RUNNING}
	if err := store.PutDesired(spec); err != nil {
		t.Fatal(err)
	}
	if err := driver.Ensure(context.Background(), &runtime.Spec{InstanceID: spec.InstanceId, Generation: 1}, nil); err != nil {
		t.Fatal(err)
	}
	if err := driver.Start(context.Background(), spec.InstanceId); err != nil {
		t.Fatal(err)
	}
	if err := store.PutActual(state.ActualRecord{Status: &nodev1.InstanceStatus{InstanceId: spec.InstanceId, ObservedGeneration: 1, Phase: nodev1.InstancePhase_INSTANCE_PHASE_PENDING_RESTART, Ready: true}}); err != nil {
		t.Fatal(err)
	}
	rec := reconciler.New(store, driver, nil, 1)
	cmd := &nodev1.Command{CommandId: "retry-command", InstanceId: spec.InstanceId, Kind: &nodev1.Command_Restart{Restart: &nodev1.RestartCommand{}}}
	driver.FailNextStop = true
	if got := rec.Command(context.Background(), cmd); got.Ok || got.Error.GetCode() != "agent.start_failed" || !got.Error.Retryable {
		t.Fatalf("stop failure hidden: %v", got)
	}
	if driver.starts.Load() != 1 {
		t.Fatal("started after failed stop")
	}
	driver.FailNextStart = true
	if got := rec.Command(context.Background(), cmd); got.Ok || got.Error.GetCode() != "agent.start_failed" {
		t.Fatalf("start failure hidden: %v", got)
	}
	checkpoint, err := store.GetRestartCommand(cmd.CommandId)
	if err != nil || checkpoint == nil || !checkpoint.Stopped || checkpoint.Complete {
		t.Fatal("missing durable stop checkpoint", err)
	}
	rec.Close()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = state.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	rec = reconciler.New(store, driver, nil, 1)
	defer rec.Close()
	if got := rec.Command(context.Background(), cmd); !got.Ok {
		t.Fatalf("retry did not finish: %v", got)
	}
	if driver.stops.Load() != 2 || driver.starts.Load() != 3 {
		t.Fatal("resumed command stopped twice or failed to start")
	}
}
