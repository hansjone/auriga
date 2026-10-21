package reconciler_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/reconciler"
	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/state"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
)

type countedLifecycleDriver struct {
	*runtime.Fake
	ensures, starts, stops atomic.Int64
}

func (d *countedLifecycleDriver) Ensure(ctx context.Context, spec *runtime.Spec, rendered *runtime.Rendered) error {
	d.ensures.Add(1)
	return d.Fake.Ensure(ctx, spec, rendered)
}
func (d *countedLifecycleDriver) Start(ctx context.Context, id string) error {
	d.starts.Add(1)
	return d.Fake.Start(ctx, id)
}
func (d *countedLifecycleDriver) Stop(ctx context.Context, id string, grace time.Duration) error {
	d.stops.Add(1)
	return d.Fake.Stop(ctx, id, grace)
}

func TestRecoveryChangesDoNotRestartPendingOrRepeatedStoppedInstances(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	driver := &countedLifecycleDriver{Fake: runtime.NewFake()}
	rec := reconciler.New(store, driver, nil, 1)
	t.Cleanup(rec.Close)
	statuses := make(chan *nodev1.InstanceStatus, 16)
	rec.Sink = func(status *nodev1.InstanceStatus) { statuses <- status }
	apply := func(generation int64, desired nodev1.DesiredState, phase nodev1.InstancePhase) *nodev1.InstanceStatus {
		t.Helper()
		if err := rec.Apply(&nodev1.InstanceSpec{InstanceId: "stable-test", Generation: generation, Desired: desired}); err != nil {
			t.Fatal(err)
		}
		deadline := time.NewTimer(time.Second)
		defer deadline.Stop()
		for {
			select {
			case status := <-statuses:
				if status.Phase == phase {
					return status
				}
				if status.Phase == nodev1.InstancePhase_INSTANCE_PHASE_FAILED {
					t.Fatalf("unexpected lifecycle failure: %v", status)
				}
			case <-deadline.C:
				t.Fatal("lifecycle phase missing")
				return nil
			}
		}
	}
	apply(1, nodev1.DesiredState_DESIRED_STATE_RUNNING, nodev1.InstancePhase_INSTANCE_PHASE_RUNNING)
	for range 2 {
		status := apply(2, nodev1.DesiredState_DESIRED_STATE_RUNNING, nodev1.InstancePhase_INSTANCE_PHASE_PENDING_RESTART)
		if !status.Ready || status.ObservedGeneration != 1 {
			t.Fatal("pending restart changed the running process")
		}
	}
	if driver.ensures.Load() != 1 || driver.starts.Load() != 1 {
		t.Fatal("pending restart was applied without workspace decision")
	}
	for range 2 {
		status := apply(3, nodev1.DesiredState_DESIRED_STATE_STOPPED, nodev1.InstancePhase_INSTANCE_PHASE_STOPPED)
		if status.Ready {
			t.Fatal("stopped instance reported ready")
		}
	}
	if driver.ensures.Load() != 2 || driver.stops.Load() != 1 {
		t.Fatal("repeated stopped spec changed runtime")
	}
	apply(4, nodev1.DesiredState_DESIRED_STATE_RUNNING, nodev1.InstancePhase_INSTANCE_PHASE_RUNNING)
	if driver.ensures.Load() != 3 || driver.starts.Load() != 2 {
		t.Fatal("normal restart no longer starts the stopped runtime")
	}
}
