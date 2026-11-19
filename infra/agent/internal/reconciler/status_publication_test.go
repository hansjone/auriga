package reconciler

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/state"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	resourcev1 "github.com/hansjone/auriga/infra/gen/auriga/resource/v1"
)

type leaseStatusDriver struct {
	*runtime.Fake
	now, expires time.Time
	afterInspect func()
}

func (d *leaseStatusDriver) Inspect(ctx context.Context, id string) (runtime.Status, error) {
	st, err := d.Fake.Inspect(ctx, id)
	st.Ready = st.Ready && d.now.Before(d.expires)
	if d.afterInspect != nil {
		d.afterInspect()
		d.afterInspect = nil
	}
	return st, err
}

func (*leaseStatusDriver) ResourceState(string) ([]*resourcev1.UnifiedInstallationReport, *resourcev1.UnifiedGuardianState, error) {
	return []*resourcev1.UnifiedInstallationReport{{State: "applied", Generation: 1, ObservedGeneration: 1, SelectionRevision: 2}},
		&resourcev1.UnifiedGuardianState{State: "active", Generation: 1, SelectionRevision: 2}, nil
}

func (d *leaseStatusDriver) ResourceObservations(_ context.Context, id string) ([]*resourcev1.UnifiedInstallationReport, *resourcev1.UnifiedGuardianState, error) {
	return d.ResourceState(id)
}

func publicationFixture(t *testing.T) (*Reconciler, *leaseStatusDriver, *nodev1.InstanceSpec) {
	t.Helper()
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	driver := &leaseStatusDriver{Fake: runtime.NewFake(), now: now, expires: now.Add(time.Minute)}
	spec := &nodev1.InstanceSpec{InstanceId: "publication-test", Generation: 1, Desired: nodev1.DesiredState_DESIRED_STATE_RUNNING}
	if err := store.PutDesired(spec); err != nil {
		t.Fatal(err)
	}
	if err := driver.Ensure(t.Context(), &runtime.Spec{InstanceID: spec.InstanceId, Generation: spec.Generation}, nil); err != nil {
		t.Fatal(err)
	}
	if err := driver.Start(t.Context(), spec.InstanceId); err != nil {
		t.Fatal(err)
	}
	r := New(store, driver, nil, 1)
	t.Cleanup(r.Close)
	// The fake models the signed driver's live readiness gate; the
	// production persistence and bbolt operations are unchanged.
	r.Activation = &runtime.ReleaseActivation{}
	return r, driver, spec
}

func TestResourceStatusLeaseAfterDurableWrite(t *testing.T) {
	for _, health := range []bool{false, true} {
		for _, expire := range []bool{false, true} {
			t.Run(fmt.Sprintf("health=%t/expire=%t", health, expire), func(t *testing.T) {
				r, d, spec := publicationFixture(t)
				wroteReady := false
				r.writeActual = func(rec state.ActualRecord) error {
					if err := r.Store.PutActual(rec); err != nil {
						return err
					}
					if rec.Status.GetReady() {
						wroteReady = true
						if expire {
							d.now = d.expires
						}
					}
					return nil
				}
				st := &nodev1.InstanceStatus{InstanceId: spec.InstanceId, ObservedGeneration: 1, Phase: nodev1.InstancePhase_INSTANCE_PHASE_RUNNING, Ready: true}
				var sent *nodev1.InstanceStatus
				r.Sink = func(st *nodev1.InstanceStatus) { sent = st }
				if health {
					if !r.persistHealth(t.Context(), 0, spec, st, 0) {
						t.Fatal("health status failed to persist")
					}
				} else if err := r.persist(st, 0); err != nil {
					t.Fatal(err)
				}
				saved, err := r.Store.GetActual(spec.InstanceId)
				if err != nil || !wroteReady || sent == nil || sent.GetReady() == expire || saved.Status.GetReady() == expire ||
					(len(sent.GetUnifiedResourceReports()) != 0) == expire || (len(saved.Status.GetUnifiedResourceReports()) != 0) == expire {
					t.Fatal("status disagreed with proof after durable write", saved.Status, sent, err)
				}
			})
		}
	}
}

func TestResourceRestartLeaseAfterCompletionWrite(t *testing.T) {
	for _, window := range []string{"valid", "checkpoint", "reply"} {
		t.Run(window, func(t *testing.T) {
			expire := window != "valid"
			r, d, spec := publicationFixture(t)
			r.Activation = nil // The ordinary fake lifecycle supplies a running instance for the test.
			r.SyncEndpoint = func(st *nodev1.InstanceStatus) bool {
				if st.GetReady() {
					r.Activation = &runtime.ReleaseActivation{}
				}
				return true
			}
			wroteComplete := false
			r.writeRestart = func(id string, record state.RestartRecord) error {
				if err := r.Store.PutRestartCommand(id, record); err != nil {
					return err
				}
				if record.Complete {
					wroteComplete = r.Activation != nil
					switch window {
					case "checkpoint":
						d.now = d.expires
					case "reply":
						d.afterInspect = func() { d.now = d.expires }
					}
				}
				return nil
			}
			cmd := &nodev1.Command{CommandId: "publication-restart", InstanceId: spec.InstanceId, Kind: &nodev1.Command_Restart{Restart: &nodev1.RestartCommand{}}}
			if result := r.Command(t.Context(), cmd); !wroteComplete || result.GetOk() == expire {
				t.Fatal("restart completion disagreed with live proof", wroteComplete, result)
			}
			if replay := r.Command(t.Context(), cmd); replay.GetOk() == expire {
				t.Fatal("replay completion disagreed with live proof", replay)
			}
		})
	}
}

func TestResourceStatusLeaseAfterBridgeSync(t *testing.T) {
	for _, expire := range []bool{false, true} {
		t.Run(fmt.Sprintf("expire=%t", expire), func(t *testing.T) {
			r, d, spec := publicationFixture(t)
			syncedReady := false
			r.SyncEndpoint = func(st *nodev1.InstanceStatus) bool {
				saved, err := r.Store.GetActual(st.InstanceId)
				syncedReady = err == nil && st.GetReady() && saved.Status.GetReady()
				if expire {
					d.now = d.expires
				}
				return true
			}
			var sent *nodev1.InstanceStatus
			r.Sink = func(st *nodev1.InstanceStatus) { sent = st }
			st := &nodev1.InstanceStatus{InstanceId: spec.InstanceId, ObservedGeneration: 1, Phase: nodev1.InstancePhase_INSTANCE_PHASE_RUNNING, Ready: true}
			if err := r.persist(st, 0); err != nil {
				t.Fatal(err)
			}
			saved, err := r.Store.GetActual(spec.InstanceId)
			if err != nil || !syncedReady || sent == nil || sent.GetReady() == expire || saved.Status.GetReady() == expire ||
				(len(sent.GetUnifiedResourceReports()) != 0) == expire || (len(saved.Status.GetUnifiedResourceReports()) != 0) == expire {
				t.Fatal("bridge outlived the proof used to sync it", saved.Status, sent, err)
			}
		})
	}
}
