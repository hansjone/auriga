package reconciler

import (
	"context"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/state"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	resourcev1 "github.com/hansjone/auriga/infra/gen/auriga/resource/v1"
)

type unifiedRecoveryDriver struct {
	*runtime.Fake
	isolate                     bool
	starts, stops, observations int
}

func (d *unifiedRecoveryDriver) GuardResources(ctx context.Context, spec *nodev1.InstanceSpec) error {
	if d.isolate {
		d.isolate = false
		_ = d.Stop(ctx, spec.InstanceId, time.Second)
		return runtime.ErrUnifiedPrivateOnly
	}
	return nil
}
func (d *unifiedRecoveryDriver) Start(ctx context.Context, id string) error {
	d.starts++
	return d.Fake.Start(ctx, id)
}
func (d *unifiedRecoveryDriver) Stop(ctx context.Context, id string, grace time.Duration) error {
	d.stops++
	return d.Fake.Stop(ctx, id, grace)
}
func (d *unifiedRecoveryDriver) ResourceObservations(context.Context, string) ([]*resourcev1.UnifiedInstallationReport, *resourcev1.UnifiedGuardianState, error) {
	d.observations++
	return nil, nil, nil
}
func (d *unifiedRecoveryDriver) ResourceState(string) ([]*resourcev1.UnifiedInstallationReport, *resourcev1.UnifiedGuardianState, error) {
	return nil, &resourcev1.UnifiedGuardianState{State: "private_only", NewSessionRequired: true, ViewCleared: true}, nil
}

func TestUnifiedIsolationRecoversDesiredRunningWithoutNewGeneration(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	driver := &unifiedRecoveryDriver{Fake: runtime.NewFake(), isolate: true}
	spec := &nodev1.InstanceSpec{InstanceId: "shared-recovery", Generation: 1, Desired: nodev1.DesiredState_DESIRED_STATE_RUNNING}
	if err := store.PutDesired(spec); err != nil {
		t.Fatal(err)
	}
	if err := driver.Ensure(t.Context(), &runtime.Spec{InstanceID: spec.InstanceId, Generation: 1}, nil); err != nil {
		t.Fatal(err)
	}
	if err := driver.Fake.Start(t.Context(), spec.InstanceId); err != nil {
		t.Fatal(err)
	}
	if err := store.PutActual(state.ActualRecord{Status: &nodev1.InstanceStatus{InstanceId: spec.InstanceId, ObservedGeneration: 1, Phase: nodev1.InstancePhase_INSTANCE_PHASE_RUNNING, Ready: true}}); err != nil {
		t.Fatal(err)
	}
	reconciler := New(store, driver, nil, 1)
	if err := reconciler.ReconcileAll(t.Context()); err != nil {
		t.Fatal(err)
	}
	reconciler.Close()
	actual, err := driver.Inspect(t.Context(), spec.InstanceId)
	if err != nil || !actual.Running || driver.starts != 1 || driver.stops != 1 {
		t.Fatalf("private-only recovery stranded runtime: %+v starts=%d stops=%d %v", actual, driver.starts, driver.stops, err)
	}
	record, err := store.GetActual(spec.InstanceId)
	if err != nil || !record.Status.Ready || record.Status.Phase != nodev1.InstancePhase_INSTANCE_PHASE_RUNNING || record.Status.ObservedGeneration != 1 {
		t.Fatalf("recovery status: %+v %v", record, err)
	}
	if driver.observations != 0 {
		t.Fatalf("ordinary status persistence queried instance HTTP %d times", driver.observations)
	}
}
