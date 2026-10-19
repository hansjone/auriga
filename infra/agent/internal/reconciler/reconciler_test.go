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
