package reconciler

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/state"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type healthDriver struct {
	*runtime.Fake
	healthy     atomic.Bool
	running     atomic.Bool
	failStart   atomic.Bool
	checks      atomic.Int64
	starts      atomic.Int64
	checking    chan struct{}
	release     chan struct{}
	block       atomic.Bool
	releaseOnce sync.Once
	startHook   func(context.Context) error
}

func (d *healthDriver) releaseCheck() { d.releaseOnce.Do(func() { close(d.release) }) }

func awaitChecking(t *testing.T, d *healthDriver) {
	t.Helper()
	select {
	case <-d.checking:
	case <-time.After(3 * time.Second):
		t.Fatal("health check did not enter public fake gate")
	}
}

func awaitHealthIdle(t *testing.T, r *Reconciler) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		r.mu.Lock()
		pending := r.healthPending["health-test"]
		r.mu.Unlock()
		if !pending {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("health worker did not finish")
		}
		time.Sleep(time.Millisecond)
	}
}

func (d *healthDriver) CheckHealth(ctx context.Context, id string) (runtime.Status, error) {
	d.checks.Add(1)
	if d.block.CompareAndSwap(true, false) {
		close(d.checking)
		// Deliberately uncooperative late result. Reconciler must fence it.
		select {
		case <-ctx.Done():
			<-d.release
		case <-d.release:
		}
	}
	return runtime.Status{InstanceID: id, Generation: 7, Running: d.running.Load(), Ready: d.healthy.Load()}, nil
}

func (d *healthDriver) Start(ctx context.Context, _ string) error {
	d.starts.Add(1)
	if d.startHook != nil {
		return d.startHook(ctx)
	}
	if d.failStart.Load() {
		return errors.New("public fake control network unavailable")
	}
	d.healthy.Store(true)
	return nil
}

func healthFixture(t *testing.T) (*Reconciler, *healthDriver, chan *nodev1.InstanceStatus) {
	t.Helper()
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	d := &healthDriver{Fake: runtime.NewFake(), checking: make(chan struct{}), release: make(chan struct{})}
	d.running.Store(true)
	d.healthy.Store(true)
	if err := d.Ensure(t.Context(), &runtime.Spec{InstanceID: "health-test", Generation: 7}, nil); err != nil {
		t.Fatal(err)
	}
	if err := d.Fake.Start(t.Context(), "health-test"); err != nil {
		t.Fatal(err)
	}
	if err := store.PutDesired(&nodev1.InstanceSpec{InstanceId: "health-test", Generation: 7, Desired: nodev1.DesiredState_DESIRED_STATE_RUNNING}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutActual(state.ActualRecord{Status: &nodev1.InstanceStatus{InstanceId: "health-test", ObservedGeneration: 7, Phase: nodev1.InstancePhase_INSTANCE_PHASE_RUNNING, Ready: true, StartedAt: timestamppb.Now()}}); err != nil {
		t.Fatal(err)
	}
	r := New(store, d, nil, 1)
	t.Cleanup(r.Close)
	t.Cleanup(d.releaseCheck)
	statuses := make(chan *nodev1.InstanceStatus, 128)
	r.Sink = func(st *nodev1.InstanceStatus) { statuses <- st }
	return r, d, statuses
}

func awaitHealthStatus(t *testing.T, statuses <-chan *nodev1.InstanceStatus, condition func(*nodev1.InstanceStatus) bool) *nodev1.InstanceStatus {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case st := <-statuses:
			if condition(st) {
				return st
			}
		case <-timer.C:
			t.Fatal("expected health status missing")
			return nil
		}
	}
}

func TestHealthMaintenanceRetriesWithoutObserverAndPreservesPendingGeneration(t *testing.T) {
	r, d, statuses := healthFixture(t)
	if err := r.Apply(&nodev1.InstanceSpec{InstanceId: "health-test", Generation: 8, Desired: nodev1.DesiredState_DESIRED_STATE_RUNNING}); err != nil {
		t.Fatal(err)
	}
	awaitHealthStatus(t, statuses, func(st *nodev1.InstanceStatus) bool {
		return st.Phase == nodev1.InstancePhase_INSTANCE_PHASE_PENDING_RESTART
	})
	d.healthy.Store(false)
	d.failStart.Store(true)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r.StartHealthChecks(ctx, 10*time.Millisecond)
	failed := awaitHealthStatus(t, statuses, func(st *nodev1.InstanceStatus) bool { return !st.Ready && st.Error != nil })
	if failed.ObservedGeneration != 7 || len(failed.RestartReasons) == 0 {
		t.Fatal("health failure lost the original pending execution", failed)
	}
	// No observer or ingress target exists in this fixture. The independent
	// bounded maintenance trigger must retry after the transient failure.
	d.failStart.Store(false)
	recovered := awaitHealthStatus(t, statuses, func(st *nodev1.InstanceStatus) bool { return st.Ready })
	if recovered.ObservedGeneration != 7 || recovered.Phase != nodev1.InstancePhase_INSTANCE_PHASE_PENDING_RESTART || d.starts.Load() < 2 {
		t.Fatalf("natural retry applied pending generation or did not recover: %v starts=%d", recovered, d.starts.Load())
	}
	cancel()
}

func TestHealthHintsCoalesceAndHealthyChecksNeverStart(t *testing.T) {
	r, d, _ := healthFixture(t)
	d.block.Store(true)
	r.RequestHealth("health-test")
	awaitChecking(t, d)
	for range 1000 {
		r.RequestHealth("health-test")
	}
	d.releaseCheck()
	awaitHealthIdle(t, r)
	if d.checks.Load() != 1 || d.starts.Load() != 0 {
		t.Fatalf("healthy hints flooded workers or started a process: checks=%d starts=%d", d.checks.Load(), d.starts.Load())
	}
}

func TestHealthLateCheckCannotOverrideStopDeleteOrNewGeneration(t *testing.T) {
	for _, operation := range []string{"stop", "delete", "new-generation"} {
		t.Run(operation, func(t *testing.T) {
			r, d, statuses := healthFixture(t)
			d.block.Store(true)
			r.RequestHealth("health-test")
			awaitChecking(t, d)
			var err error
			switch operation {
			case "delete":
				err = r.Delete(t.Context(), "health-test", 8, false)
			default:
				desired := nodev1.DesiredState_DESIRED_STATE_RUNNING
				if operation == "stop" {
					desired = nodev1.DesiredState_DESIRED_STATE_STOPPED
				}
				err = r.Apply(&nodev1.InstanceSpec{InstanceId: "health-test", Generation: 8, Desired: desired})
			}
			if err != nil {
				t.Fatal(err)
			}
			d.releaseCheck()
			terminal := awaitHealthStatus(t, statuses, func(st *nodev1.InstanceStatus) bool {
				return st.Phase == nodev1.InstancePhase_INSTANCE_PHASE_STOPPED || st.Phase == nodev1.InstancePhase_INSTANCE_PHASE_ABSENT || st.Phase == nodev1.InstancePhase_INSTANCE_PHASE_PENDING_RESTART
			})
			if operation != "new-generation" && terminal.Ready {
				t.Fatal("late health resurrected a stopped/deleted instance", terminal)
			}
			if d.starts.Load() != 0 {
				t.Fatal("stale health work started an obsolete process")
			}
		})
	}
}

func TestHealthCancellationJoinsWorkerAndDoesNotRestartStoppedProcess(t *testing.T) {
	r, d, statuses := healthFixture(t)
	d.running.Store(false)
	r.RequestHealth("health-test")
	awaitHealthStatus(t, statuses, func(st *nodev1.InstanceStatus) bool { return st.Error != nil })
	awaitHealthIdle(t, r)
	if d.starts.Load() != 0 {
		t.Fatal("maintenance restarted an unexpectedly stopped process")
	}
	r.mu.Lock()
	delete(r.healthRetries, "health-test")
	r.mu.Unlock()
	d.block.Store(true)
	r.RequestHealth("health-test")
	awaitChecking(t, d)
	done := make(chan struct{})
	go func() { r.Close(); close(done) }()
	d.releaseCheck()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel and join health worker")
	}
}

func TestHealthMaintenanceParentCancellationStopsActiveRepair(t *testing.T) {
	r, d, statuses := healthFixture(t)
	d.healthy.Store(false)
	started := make(chan struct{})
	canceled := make(chan struct{})
	d.startHook = func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		close(canceled)
		return ctx.Err()
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r.StartHealthChecks(ctx, 10*time.Millisecond)
	r.RequestHealth("health-test")
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("repair did not start")
	}
	cancel()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("parent cancellation did not reach active repair")
	}
	r.Close()
	if d.starts.Load() != 1 {
		t.Fatal("canceled maintenance started another repair")
	}
	for len(statuses) != 0 {
		if st := <-statuses; st.Ready {
			t.Fatal("canceled repair published Ready", st)
		}
	}
}
