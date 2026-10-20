package reconciler_test

import (
	"context"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/reconciler"
	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/state"
	"github.com/hansjone/auriga/infra/agent/internal/testutil"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
)

func TestReconcilerRunningAndStopped(t *testing.T) {
	dir := t.TempDir()
	store, err := state.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	drv := runtime.NewFake()
	rec := reconciler.New(store, drv, nil, 2)
	defer rec.Close()

	if err := rec.Apply(&nodev1.InstanceSpec{
		InstanceId: "i1",
		Generation: 1,
		Desired:    nodev1.DesiredState_DESIRED_STATE_RUNNING,
	}); err != nil {
		t.Fatal(err)
	}
	if err := testutil.WaitFor(2*time.Second, func() bool {
		a, _ := store.GetActual("i1")
		return a.Status != nil && a.Status.Phase == nodev1.InstancePhase_INSTANCE_PHASE_RUNNING
	}); err != nil {
		t.Fatal(err)
	}

	if err := rec.Apply(&nodev1.InstanceSpec{
		InstanceId: "i1",
		Generation: 2,
		Desired:    nodev1.DesiredState_DESIRED_STATE_STOPPED,
	}); err != nil {
		t.Fatal(err)
	}
	if err := testutil.WaitFor(2*time.Second, func() bool {
		a, _ := store.GetActual("i1")
		return a.Status != nil && a.Status.Phase == nodev1.InstancePhase_INSTANCE_PHASE_STOPPED
	}); err != nil {
		t.Fatal(err)
	}
	_ = context.Background()
}

func TestStatusSinkKeepsEachTransitionSnapshot(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	rec := reconciler.New(store, runtime.NewFake(), nil, 1)
	defer rec.Close()
	snapshots := make(chan *nodev1.InstanceStatus, 4)
	rec.Sink = func(st *nodev1.InstanceStatus) { snapshots <- st }
	if err := rec.Apply(&nodev1.InstanceSpec{
		InstanceId: "i_snapshot", Generation: 7,
		Desired: nodev1.DesiredState_DESIRED_STATE_RUNNING,
		Storage: &nodev1.Storage{SchemaVersion: 1},
	}); err != nil {
		t.Fatal(err)
	}
	// Close drains the queued work and waits until the producer stops mutating status.
	rec.Close()
	want := []nodev1.InstancePhase{
		nodev1.InstancePhase_INSTANCE_PHASE_PREPARING,
		nodev1.InstancePhase_INSTANCE_PHASE_MIGRATING,
		nodev1.InstancePhase_INSTANCE_PHASE_STARTING,
		nodev1.InstancePhase_INSTANCE_PHASE_RUNNING,
	}
	for i, phase := range want {
		select {
		case st := <-snapshots:
			if st.Phase != phase {
				t.Errorf("transition %d changed after publication: got %s, want %s", i, st.Phase, phase)
			}
			if st.Ready != (i == len(want)-1) {
				t.Errorf("transition %d readiness changed after publication", i)
			}
			// Every report for this attempt names the generation being applied,
			// otherwise workspace drops it as stale.
			if st.ObservedGeneration != 7 {
				t.Errorf("transition %d observed generation = %d, want 7", i, st.ObservedGeneration)
			}
		default:
			t.Fatalf("missing transition %d", i)
		}
	}
}
