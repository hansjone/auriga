package conn

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/reconciler"
	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/state"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type unhealthySessionDriver struct {
	*runtime.Fake
	checks atomic.Int64
}

func (d *unhealthySessionDriver) CheckHealth(_ context.Context, id string) (runtime.Status, error) {
	d.checks.Add(1)
	return runtime.Status{InstanceID: id, Running: true, Generation: 7}, nil
}

func (*unhealthySessionDriver) Start(context.Context, string) error {
	return errors.New("public fake edge temporarily unavailable")
}

func TestSessionHealthRechecksCurrentExecutionAndWithdrawsStaleReady(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	const id = "health-test"
	if err := store.PutDesired(&nodev1.InstanceSpec{InstanceId: id, Generation: 7, Desired: nodev1.DesiredState_DESIRED_STATE_RUNNING}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutActual(state.ActualRecord{Status: &nodev1.InstanceStatus{InstanceId: id, ObservedGeneration: 7, Phase: nodev1.InstancePhase_INSTANCE_PHASE_RUNNING, Ready: true, StartedAt: timestamppb.Now()}}); err != nil {
		t.Fatal(err)
	}
	driver := &unhealthySessionDriver{Fake: runtime.NewFake()}
	rec := reconciler.New(store, driver, nil, 1)
	t.Cleanup(rec.Close)
	sess := New(Config{Store: store, Reconciler: rec})
	rec.Sink = sess.StatusSink()
	// The old bool is deliberately true. It is only a request to inspect the
	// current execution; it must not authorize Ready when the real check fails.
	sess.ReportReady(id, true)
	select {
	case st := <-sess.statusCh:
		if st.Ready || st.ObservedGeneration != 7 || driver.checks.Load() == 0 {
			t.Fatalf("unhealthy current execution retained Ready: %v checks=%d", st, driver.checks.Load())
		}
	case <-time.After(time.Second):
		t.Fatal("health observer never reached the canonical InstanceStatus channel")
	}
}
