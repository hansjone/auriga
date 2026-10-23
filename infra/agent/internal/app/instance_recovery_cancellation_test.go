package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/state"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
)

func TestRuntimeObservationPropagatesCallerCancellation(t *testing.T) {
	for _, operation := range []string{"inspect", "list"} {
		t.Run(operation, func(t *testing.T) {
			f := newRecoveryFixture(t)
			d := f.driver.Driver.(*runtime.Docker)
			entered, requestCanceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			var requests atomic.Int64
			engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/containers/json" {
					_ = json.NewEncoder(w).Encode([]any{map[string]any{"State": "running", "Labels": map[string]string{"auriga.instance": "recovery-test", "auriga.generation": "1"}}})
					return
				}
				if r.URL.Path != "/containers/"+fixtureContainerID+"/json" {
					t.Errorf("unexpected Engine request %s", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				n := requests.Add(1)
				if (operation == "inspect" && n == 2) || operation == "list" {
					close(entered)
					select {
					case <-r.Context().Done():
						close(requestCanceled)
					case <-release:
					}
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"Id": fixtureContainerID, "Image": fixtureConfigID,
					"State": map[string]any{"Running": true}, "Config": map[string]any{"Labels": map[string]string{
						"auriga.generation": "1", "auriga.instance": "recovery-test", "auriga.managed": "true"}}})
			}))
			t.Cleanup(engine.Close)
			client, err := runtime.DialEngine(engine.URL, 5*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			d.Client = client
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				if operation == "inspect" {
					_, err := d.Inspect(ctx, "recovery-test")
					done <- err
				} else {
					_, err := d.List(ctx)
					done <- err
				}
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("internal Endpoint request missing")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("observation swallowed caller cancellation: %v", err)
				}
			case <-time.After(time.Second):
				t.Error("observation did not return after cancellation while server barrier remained closed")
				unblock()
				if err := <-done; !errors.Is(err, context.Canceled) {
					t.Errorf("observation returned a different error after releasing the server: %v", err)
				}
			}
			select {
			case <-requestCanceled:
			case <-time.After(time.Second):
				t.Error("Engine request did not inherit caller cancellation")
			}
		})
	}
}

func TestCanceledInspectionWithdrawsPublishedBridge(t *testing.T) {
	f := newRecoveryFixture(t)
	f.healthy.Store(true)
	if err := f.rec.ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.waitTerminal(t)
	if s := f.next(t); s.Phase != nodev1.InstancePhase_INSTANCE_PHASE_STARTING || s.Ready {
		t.Fatal(s)
	}
	if s := f.next(t); !s.Ready {
		t.Fatal(s)
	}
	if _, ok := f.registry.Lookup("recovery-test"); !ok {
		t.Fatal("initial healthy bridge missing")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.driver.Inspect(ctx, "recovery-test"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled inspection returned %v", err)
	}
	if _, ok := f.registry.Lookup("recovery-test"); ok || len(f.targets.Targets()) != 0 {
		t.Fatal("canceled observation retained its previously published bridge")
	}
	if f.driver.ensures.Load() != 0 || f.starts.Load() != 1 {
		t.Fatal("canceled observation rebuilt or restarted the process")
	}
}

type pausedExecutionSave struct {
	runtime.ReleaseState
	entered, release chan struct{}
	once             sync.Once
}

func (s *pausedExecutionSave) PutExecution(r state.ExecutionRecord) error {
	s.once.Do(func() { close(s.entered); <-s.release })
	return s.ReleaseState.PutExecution(r)
}

type capturedStartContext struct {
	runtime.Driver
	contexts chan context.Context
}

func (d *capturedStartContext) Start(ctx context.Context, id string) error {
	d.contexts <- ctx
	return d.Driver.Start(ctx, id)
}

func TestRecoveryDeadlineDuringExecutionSaveWithholdsReadyAndPreservesRetry(t *testing.T) {
	f := newRecoveryFixtureWithPort(t, "8888/tcp")
	f.healthy.Store(true)
	// The deadline is a production Start deadline, not a wait-for-status budget.
	f.rec.StartTimeout = time.Second
	if err := f.rec.Store.PutDesired(&nodev1.InstanceSpec{InstanceId: "recovery-test", Generation: 2,
		Desired: nodev1.DesiredState_DESIRED_STATE_RUNNING, Profile: &nodev1.Profile{ListenPort: 9999}}); err != nil {
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
	before, err := f.rec.Store.GetExecution("recovery-test")
	if err != nil {
		t.Fatal(err)
	}
	high, found, err := f.rec.Store.GetReleaseHighWater(before.Candidate.Partition)
	if err != nil || !found {
		t.Fatalf("missing initial high-water: %v", err)
	}
	gate := &pausedExecutionSave{ReleaseState: f.rec.Activation.Store, entered: make(chan struct{}), release: make(chan struct{})}
	f.rec.Activation.Store = gate
	capture := &capturedStartContext{Driver: f.driver.Driver, contexts: make(chan context.Context, 2)}
	f.driver.Driver = capture
	var once sync.Once
	unblock := func() { once.Do(func() { close(gate.release) }) }
	defer unblock()
	if err := f.rec.ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s := f.next(t); s.Phase != nodev1.InstancePhase_INSTANCE_PHASE_STARTING || s.Ready {
		t.Fatal(s)
	}
	select {
	case <-gate.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("final execution save not reached")
	}
	startCtx := <-capture.contexts
	<-startCtx.Done()
	if !errors.Is(startCtx.Err(), context.DeadlineExceeded) {
		t.Fatal(startCtx.Err())
	}
	unblock()
	failed := f.next(t)
	if failed.Phase != nodev1.InstancePhase_INSTANCE_PHASE_FAILED || failed.Ready || failed.ObservedGeneration != 1 ||
		!slices.Equal(failed.RestartReasons, reasons) || !failed.StartedAt.AsTime().Equal(f.startedAt.AsTime()) {
		t.Fatalf("expired recovery published success or lost original decision: %v", failed)
	}
	if _, ok := f.registry.Lookup("recovery-test"); ok || len(f.targets.Targets()) != 0 {
		t.Fatal("expired recovery published ingress/observer")
	}
	if _, err := capture.Endpoint("recovery-test"); err == nil {
		t.Fatal("expired Start retained driver endpoint")
	}
	after, err := f.rec.Store.GetExecution("recovery-test")
	if err != nil || after == nil || after.Phase != "started" || after.ContainerID != before.ContainerID || after.Generation != before.Generation || after.Candidate != before.Candidate {
		t.Fatalf("lost durable running execution: %v %v", after, err)
	}
	currentHigh, found, err := f.rec.Store.GetReleaseHighWater(before.Candidate.Partition)
	if err != nil || !found || currentHigh != high {
		t.Fatalf("changed high-water: %v %v", currentHigh, err)
	}
	if err := f.rec.Store.CheckReleaseExecution(before.Candidate); err != nil {
		t.Fatalf("definite Engine replies left a false unresolved intent: %v", err)
	}
	// Same reconciler and worker retry the original running process. The saved
	// started record prevents a second Engine start, and Ensure must never run.
	if err := f.rec.ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s := f.next(t); s.Phase != nodev1.InstancePhase_INSTANCE_PHASE_STARTING || s.Ready {
		t.Fatal(s)
	}
	s := f.next(t)
	if s.Phase != nodev1.InstancePhase_INSTANCE_PHASE_PENDING_RESTART || !s.Ready || s.ObservedGeneration != 1 ||
		!slices.Equal(s.RestartReasons, reasons) || !s.StartedAt.AsTime().Equal(f.startedAt.AsTime()) || f.driver.ensures.Load() != 0 || f.starts.Load() != 1 {
		t.Fatalf("retry changed the original process/decision: %v starts=%d", s, f.starts.Load())
	}
	if _, ok := f.registry.Lookup("recovery-test"); !ok || len(f.targets.Targets()) != 1 {
		t.Fatal("qualified retry did not restore bridge")
	}
}
