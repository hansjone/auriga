package app

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/config"
	"github.com/hansjone/auriga/infra/agent/internal/ingress"
	"github.com/hansjone/auriga/infra/agent/internal/observer"
	"github.com/hansjone/auriga/infra/agent/internal/reconciler"
	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/state"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type countedRecoveryDriver struct {
	runtime.Driver
	ensures atomic.Int64
}

func (d *countedRecoveryDriver) Ensure(ctx context.Context, spec *runtime.Spec, rendered *runtime.Rendered) error {
	d.ensures.Add(1)
	return d.Driver.Ensure(ctx, spec, rendered)
}

type recoveryFixture struct {
	rec                   *reconciler.Reconciler
	bridge                *instanceBridge
	registry              *ingress.MapRegistry
	targets               *observer.SliceTargets
	driver                *countedRecoveryDriver
	statuses              chan *nodev1.InstanceStatus
	healthy, inspectError atomic.Bool
	starts, probes        atomic.Int64
	startedAt             *timestamppb.Timestamp
}

func newRecoveryFixture(t *testing.T) *recoveryFixture {
	t.Helper()
	return newRecoveryFixtureWithPort(t, "7860/tcp")
}

func newRecoveryFixtureWithPort(t *testing.T, actualPort string) *recoveryFixture {
	t.Helper()
	f := &recoveryFixture{statuses: make(chan *nodev1.InstanceStatus, 32), startedAt: timestamppb.New(time.Now().Add(-time.Hour))}
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.probes.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "ready": f.healthy.Load(), "protocol": 1, "pluginVersion": "test"})
	}))
	t.Cleanup(health.Close)
	_, port, err := net.SplitHostPort(strings.TrimPrefix(health.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/containers/auriga-inst-recovery-test/json":
			if f.inspectError.Load() {
				http.Error(w, "inspection unavailable", http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"State": map[string]any{"Running": true},
				"Config":          map[string]any{"Labels": map[string]string{"auriga.generation": "1"}},
				"HostConfig":      map[string]any{"PortBindings": map[string]any{actualPort: []any{map[string]string{"HostIp": "127.0.0.1", "HostPort": "0"}}}},
				"NetworkSettings": map[string]any{"Ports": map[string]any{actualPort: []any{map[string]string{"HostPort": port}}}}})
		case r.Method == http.MethodPost && r.URL.Path == "/containers/auriga-inst-recovery-test/start":
			f.starts.Add(1)
			w.WriteHeader(http.StatusNotModified) // Existing user process is still running.
		default:
			t.Errorf("recovery unnecessarily changed runtime: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(engine.Close)
	client, err := runtime.DialEngine(engine.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	f.driver = &countedRecoveryDriver{Driver: runtime.NewDockerWithClient(client, "")}
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.PutDesired(&nodev1.InstanceSpec{InstanceId: "recovery-test", Generation: 1, Desired: nodev1.DesiredState_DESIRED_STATE_RUNNING}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutActual(state.ActualRecord{Status: &nodev1.InstanceStatus{InstanceId: "recovery-test", ObservedGeneration: 1,
		Phase: nodev1.InstancePhase_INSTANCE_PHASE_RUNNING, Ready: true, StartedAt: f.startedAt}}); err != nil {
		t.Fatal(err)
	}
	f.registry, f.targets = ingress.NewMapRegistry(), &observer.SliceTargets{}
	f.bridge = newInstanceBridge(config.Config{DataRoot: t.TempDir()}, f.driver, f.registry, f.targets)
	f.rec = reconciler.New(store, f.driver, nil, 1)
	t.Cleanup(f.rec.Close)
	f.rec.StartTimeout = 120 * time.Millisecond
	f.rec.Sink = func(status *nodev1.InstanceStatus) { f.bridge.Sync(status); f.statuses <- status }
	return f
}

func (f *recoveryFixture) next(t *testing.T) *nodev1.InstanceStatus {
	t.Helper()
	select {
	case status := <-f.statuses:
		return status
	case <-time.After(2 * time.Second):
		t.Fatal("recovery status missing")
		return nil
	}
}

func TestAgentRestartRestoresHealthAndBridgeWithoutRebuildingContainer(t *testing.T) {
	f := newRecoveryFixture(t)
	f.rec.StartTimeout = 2 * time.Second
	initial, err := f.rec.Inventory()
	if err != nil || len(initial) != 1 || initial[0].Ready {
		t.Fatalf("first inventory reused persisted ready: %v %v", initial, err)
	}
	if err := f.rec.ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := f.next(t)
	if first.Ready || first.Phase != nodev1.InstancePhase_INSTANCE_PHASE_STARTING {
		t.Fatalf("re-emitted old ready without live health: %v", first)
	}
	f.healthy.Store(true)
	final := f.next(t)
	if !final.Ready || final.Phase != nodev1.InstancePhase_INSTANCE_PHASE_RUNNING || !final.StartedAt.AsTime().Equal(f.startedAt.AsTime()) {
		t.Fatalf("bad recovered status: %v", final)
	}
	if _, ok := f.registry.Lookup("recovery-test"); !ok || len(f.targets.Targets()) != 1 {
		t.Fatal("healthy recovery has no ingress/observer")
	}
	if f.driver.ensures.Load() != 0 || f.starts.Load() != 1 || f.probes.Load() == 0 {
		t.Fatal("did not recover the existing process with real bounded health")
	}
	verified, err := f.rec.Inventory()
	if err != nil || len(verified) != 1 || !verified[0].Ready {
		t.Fatal("healthy inventory did not become ready")
	}
	if err := f.rec.ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !f.next(t).Ready || f.starts.Load() != 1 || f.driver.ensures.Load() != 0 {
		t.Fatal("healthy idempotent reconciliation restarted process")
	}
}

func TestRecoveryInspectionOrHealthFailureDoesNotPublishReadyOrRebuildOnRetry(t *testing.T) {
	for _, failure := range []string{"inspect", "health"} {
		t.Run(failure, func(t *testing.T) {
			f := newRecoveryFixture(t)
			f.inspectError.Store(failure == "inspect")
			if err := f.rec.ReconcileAll(context.Background()); err != nil {
				t.Fatal(err)
			}
			for {
				status := f.next(t)
				if status.Ready {
					t.Fatal("failure reused persisted ready")
				}
				if status.Phase == nodev1.InstancePhase_INSTANCE_PHASE_FAILED {
					break
				}
			}
			if _, ok := f.registry.Lookup("recovery-test"); ok || len(f.targets.Targets()) != 0 || f.driver.ensures.Load() != 0 {
				t.Fatal("failed recovery left ingress or rebuilt process")
			}
			f.inspectError.Store(false)
			f.healthy.Store(true)
			f.rec.StartTimeout = time.Second
			if err := f.rec.ReconcileAll(context.Background()); err != nil {
				t.Fatal(err)
			}
			for {
				status := f.next(t)
				if status.Phase == nodev1.InstancePhase_INSTANCE_PHASE_RUNNING && status.Ready {
					if !status.StartedAt.AsTime().Equal(f.startedAt.AsTime()) {
						t.Fatal("retry invented a new process start time")
					}
					break
				}
				if status.Phase == nodev1.InstancePhase_INSTANCE_PHASE_FAILED {
					t.Fatal("healthy retry failed")
				}
			}
			if f.driver.ensures.Load() != 0 {
				t.Fatal("retry rebuilt still-running user process")
			}
		})
	}
}

func TestOldGenerationPendingRecoveryRetainsDecisionAcrossFailure(t *testing.T) {
	f := newRecoveryFixtureWithPort(t, "8888/tcp")
	if err := f.rec.Store.PutDesired(&nodev1.InstanceSpec{InstanceId: "recovery-test", Generation: 2, Desired: nodev1.DesiredState_DESIRED_STATE_RUNNING,
		Profile: &nodev1.Profile{ListenPort: 9999}}); err != nil {
		t.Fatal(err)
	}
	previous, err := f.rec.Store.GetActual("recovery-test")
	if err != nil {
		t.Fatal(err)
	}
	previous.Status.Phase = nodev1.InstancePhase_INSTANCE_PHASE_PENDING_RESTART
	reasons := []nodev1.RestartReason{nodev1.RestartReason_RESTART_REASON_DSH_VERSION, nodev1.RestartReason_RESTART_REASON_RESOURCES}
	previous.Status.RestartReasons = reasons
	if err := f.rec.Store.PutActual(previous); err != nil {
		t.Fatal(err)
	}
	if err := f.rec.ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	for {
		status := f.next(t)
		if status.Ready {
			t.Fatal("unhealthy pending recovery reused ready")
		}
		if status.Phase == nodev1.InstancePhase_INSTANCE_PHASE_FAILED {
			break
		}
	}
	f.healthy.Store(true)
	f.rec.StartTimeout = time.Second
	if err := f.rec.ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if status := f.next(t); status.Phase != nodev1.InstancePhase_INSTANCE_PHASE_STARTING || status.Ready {
		t.Fatal("missing pending recovery start")
	}
	status := f.next(t)
	if status.Phase != nodev1.InstancePhase_INSTANCE_PHASE_PENDING_RESTART || !status.Ready || status.ObservedGeneration != 1 ||
		!slices.Equal(status.RestartReasons, reasons) || !status.StartedAt.AsTime().Equal(f.startedAt.AsTime()) || f.driver.ensures.Load() != 0 {
		t.Fatalf("pending decision lost or process rebuilt: %v", status)
	}
	starts := f.starts.Load()
	if err := f.rec.ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if status := f.next(t); status.Phase != nodev1.InstancePhase_INSTANCE_PHASE_PENDING_RESTART || !status.Ready || status.ObservedGeneration != 1 || f.starts.Load() != starts {
		t.Fatalf("healthy old-generation pending recovery was not idempotent: %v", status)
	}
}
